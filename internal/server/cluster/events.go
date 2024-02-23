package cluster

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/lxc/incus/client"
	"github.com/lxc/incus/internal/revert"
	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/endpoints"
	"github.com/lxc/incus/internal/server/events"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
	localtls "github.com/lxc/incus/shared/tls"
)

// eventHubMinHosts is the minimum number of members that must have the event-hub role to trigger switching into
// event-hub mode (where cluster members will only connect to event-hub members rather than all members when
// operating in the normal full-mesh mode).
const eventHubMinHosts = 2

// EventMode indicates the event distribution mode.
type EventMode string

// EventModeFullMesh is when every cluster member connects to every other cluster member to pull events.
const EventModeFullMesh EventMode = "full-mesh"

// EventModeHubServer is when the cluster is operating in event-hub mode and this server is designated as a hub
// server, meaning that it will only connect to the other event-hub members and not other members.
const EventModeHubServer EventMode = "hub-server"

// EventModeHubClient is when the cluster is operating in event-hub mode and this member is designated as a hub
// client, meaning that it is expected to connect to the event-hub members.
const EventModeHubClient EventMode = "hub-client"

// eventListenerClient stores both the event listener and its associated client.
type eventListenerClient struct {
	*incus.EventListener

	client        incus.InstanceServer
	hubPushCancel context.CancelFunc
}

// Disconnect disconnects both the listener and the client.
func (lc *eventListenerClient) Disconnect() {
	if lc.hubPushCancel != nil {
		lc.hubPushCancel()
	}

	lc.EventListener.Disconnect()
	lc.client.Disconnect()
}

// SetEventMode applies the specified eventMode of the local server to the listener.
// If the eventMode is EventModeHubClient then a go routine is started that consumes events from eventHubPushCh and
// pushes them to the remote server. If the eventMode is anything else then the go routine is stopped if running.
func (lc *eventListenerClient) SetEventMode(eventMode EventMode, eventHubPushCh chan api.Event) {
	if eventMode == EventModeHubClient {
		if lc.hubPushCancel != nil || !lc.IsActive() {
			return
		}

		ctx, cancel := context.WithCancel(context.Background())

		go func() {
			lc.hubPushCancel = cancel
			info, _ := lc.client.GetConnectionInfo()
			logger.Info("Event hub client started", logger.Ctx{"remote": info.URL})
			defer logger.Info("Event hub client stopped", logger.Ctx{"remote": info.URL})
			defer func() {
				cancel()
				lc.hubPushCancel = nil
			}()

			for {
				select {
				case event, more := <-eventHubPushCh:
					if !more {
						return
					}

					err := lc.client.SendEvent(event)
					if err != nil {
						//  Send failed, something is wrong with this hub server.
						lc.Disconnect() // Disconnect listener and client.

						// Try and put event back onto event hub push queue for consumption
						// by another consumer.
						ctx, cancel := context.WithTimeout(context.Background(), eventHubPushChTimeout)
						defer cancel()

						select {
						case eventHubPushCh <- event:
						case <-ctx.Done(): // Don't block if all consumers are slow/down.
						}

						return
					}

				case <-ctx.Done():
					return
				}
			}
		}()
	} else if lc.hubPushCancel != nil {
		lc.hubPushCancel()
		lc.hubPushCancel = nil
	}
}

var eventMode EventMode = EventModeFullMesh
var eventHubAddresses []string
var eventHubPushCh = make(chan api.Event, 10) // Buffer size to accommodate slow consumers before dropping events.
var eventHubPushChTimeout = time.Duration(time.Second)
var listeners = map[string]*eventListenerClient{}
var listenersNotify = map[chan struct{}][]string{}
var listenersLock sync.Mutex
var listenersUpdateLock sync.Mutex

// ServerEventMode returns the event distribution mode that this local server is operating in.
func ServerEventMode() EventMode {
	listenersLock.Lock()
	defer listenersLock.Unlock()

	return eventMode
}

// RoleInSlice returns whether or not the rule is within the roles list.
func RoleInSlice(role db.ClusterRole, roles []db.ClusterRole) bool {
	for _, r := range roles {
		if r == role {
			return true
		}
	}

	return false
}

// EventListenerWait waits for there to be listener connected to the specified address, or one of the event hubs
// if operating in event hub mode.
func EventListenerWait(ctx context.Context, address string) error {
	// Check if there is already a listener.
	listenersLock.Lock()
	listener, found := listeners[address]
	if found && listener.IsActive() {
		listenersLock.Unlock()
		return nil
	}

	listenAddresses := []string{address}

	// Check if operating in event hub mode and if one of the event hub connections is available.
	// If so then we are ready to receive events from all members.
	if eventMode != EventModeFullMesh {
		for _, eventHubAddress := range eventHubAddresses {
			listener, found := listeners[eventHubAddress]
			if found && listener.IsActive() {
				listenersLock.Unlock()
				return nil
			}

			listenAddresses = append(listenAddresses, eventHubAddress)
		}
	}

	// If not setup a notification for when the desired address or any of the event hubs connect.
	connected := make(chan struct{})
	listenersNotify[connected] = listenAddresses
	listenersLock.Unlock()

	defer func() {
		listenersLock.Lock()
		delete(listenersNotify, connected)
		listenersLock.Unlock()
	}()

	// Wait for the connected channel to be closed (indicating a new listener has been connected), and return.
	select {
	case <-connected:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// hubAddresses returns the addresses of members with event-hub role, and the event mode of the server.
// The event mode will only be hub-server or hub-client if at least eventHubMinHosts have an event-hub role.
// Otherwise the mode will be full-mesh.
func hubAddresses(localAddress string, members map[int64]APIHeartbeatMember) ([]string, EventMode) {
	var hubAddresses []string
	var localHasHubRole bool

	// Do a first pass of members to count the members with event-hub role, and whether we are a hub server.
	for _, member := range members {
		if RoleInSlice(db.ClusterRoleEventHub, member.Roles) {
			hubAddresses = append(hubAddresses, member.Address)

			if member.Address == localAddress {
				localHasHubRole = true
			}
		}
	}

	eventMode := EventModeFullMesh
	if len(hubAddresses) >= eventHubMinHosts {
		if localHasHubRole {
			eventMode = EventModeHubServer
		} else {
			eventMode = EventModeHubClient
		}
	}

	return hubAddresses, eventMode
}

// EventsUpdateListeners refreshes the cluster event listener connections.
func EventsUpdateListeners(endpoints *endpoints.Endpoints, cluster *db.Cluster, serverCert func() *localtls.CertInfo, hbMembers map[int64]APIHeartbeatMember, inject events.InjectFunc) {
	listenersUpdateLock.Lock()
	defer listenersUpdateLock.Unlock()

	// If no heartbeat members provided, populate from global database.
	if hbMembers == nil {
		var err error
		var members []db.NodeInfo
		var offlineThreshold time.Duration

		err = cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			members, err = tx.GetNodes(ctx)
			if err != nil {
				return err
			}

			offlineThreshold, err = tx.GetNodeOfflineThreshold(ctx)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			logger.Warn("Failed to get current cluster members", logger.Ctx{"err": err})
			return
		}

		hbMembers = make(map[int64]APIHeartbeatMember, len(members))
		for _, member := range members {
			hbMembers[member.ID] = APIHeartbeatMember{
				ID:            member.ID,
				Name:          member.Name,
				Address:       member.Address,
				LastHeartbeat: member.Heartbeat,
				Online:        !member.IsOffline(offlineThreshold),
				Roles:         member.Roles,
			}
		}
	}

	localAddress := endpoints.NetworkAddress()
	hubAddresses, localEventMode := hubAddresses(localAddress, hbMembers)

	keepListeners := make(map[string]struct{})
	wg := sync.WaitGroup{}
	for _, hbMember := range hbMembers {
		// Don't bother trying to connect to ourselves or offline members.
		if hbMember.Address == localAddress || !hbMember.Online {
			continue
		}

		if localEventMode != EventModeFullMesh && !RoleInSlice(db.ClusterRoleEventHub, hbMember.Roles) {
			continue // Skip non-event-hub members if we are operating in event-hub mode.
		}

		listenersLock.Lock()
		listener, ok := listeners[hbMember.Address]

		// If the member already has a listener associated to it, check that the listener is still active.
		// If it is, just move on to next member, but if not then we'll try to connect again.
		if ok {
			if listener.IsActive() {
				keepListeners[hbMember.Address] = struct{}{} // Add to current listeners list.
				listener.SetEventMode(localEventMode, eventHubPushCh)
				listenersLock.Unlock()
				continue
			}

			// Disconnect and delete listener, but don't delete any listenersNotify entry as there
			// might be something waiting for a future connection.
			listener.Disconnect()
			delete(listeners, hbMember.Address)
			listenersLock.Unlock()

			// Log after releasing listenersLock to avoid deadlock on listenersLock with EventHubPush.
			logger.Info("Removed inactive member event listener client", logger.Ctx{"local": localAddress, "remote": hbMember.Address})
		} else {
			listenersLock.Unlock()
		}

		keepListeners[hbMember.Address] = struct{}{} // Add to current listeners list.

		// Connect to remote concurrently and add to active listeners if successful.
		wg.Add(1)
		go func(m APIHeartbeatMember) {
			l := logger.AddContext(logger.Ctx{"local": localAddress, "remote": m.Address})

			defer wg.Done()
			listener, err := eventsConnect(m.Address, endpoints.NetworkCert(), serverCert())
			if err != nil {
				l.Warn("Failed adding member event listener client", logger.Ctx{"err": err})
				return
			}

			_, _ = listener.AddHandler(nil, func(event api.Event) {
				// Inject event received via pull as forwarded so that its not forwarded again
				// onto other members.
				inject(event, events.EventSourcePull)
			})

			listener.SetEventMode(localEventMode, eventHubPushCh)

			listenersLock.Lock()
			listeners[m.Address] = listener

			// Indicate to any notifiers waiting for this member's address that it is connected.
			for connected, notifyAddresses := range listenersNotify {
				if slices.Contains(notifyAddresses, m.Address) {
					close(connected)
					delete(listenersNotify, connected)
				}
			}

			listenersLock.Unlock()

			// Log after releasing listenersLock to avoid deadlock on listenersLock with EventHubPush.
			l.Info("Added member event listener client")
		}(hbMember)
	}

	wg.Wait()

	// Disconnect and delete any out of date listeners and their notifiers.
	var removedAddresses []string

	listenersLock.Lock()
	for address, listener := range listeners {
		_, found := keepListeners[address]
		if !found {
			listener.Disconnect()
			delete(listeners, address)

			// Record address removed, but don't log it here as this could cause a deadlock on
			// listenersLock with EventHubPush
			removedAddresses = append(removedAddresses, address)
		}
	}

	// Store event hub addresses in global slice late in the function after all event connections have been
	// opened above. This way the reported state by this server won't be updated until its ready.
	eventHubAddresses = hubAddresses
	eventMode = localEventMode

	listenersLock.Unlock()

	// Log the listeners removed after releasing listenersLock.
	for _, removedAddress := range removedAddresses {
		logger.Info("Removed old member event listener client", logger.Ctx{"local": localAddress, "remote": removedAddress})
	}

	if len(hbMembers) > 1 && len(keepListeners) <= 0 {
		logger.Error("No active cluster event listener clients", logger.Ctx{"local": localAddress})
	}
}

// Establish a client connection to get events from the given node.
func eventsConnect(address string, networkCert *localtls.CertInfo, serverCert *localtls.CertInfo) (*eventListenerClient, error) {
	client, err := Connect(address, networkCert, serverCert, nil, true)
	if err != nil {
		return nil, err
	}

	revert := revert.New()
	revert.Add(func() {
		client.Disconnect()
	})

	listener, err := client.GetEventsAllProjects()
	if err != nil {
		return nil, err
	}

	revert.Success()

	lc := &eventListenerClient{
		EventListener: listener,
		client:        client,
	}

	return lc, nil
}

// EventHubPush pushes the event to the event hub members if local server is an event-hub client.
func EventHubPush(event api.Event) {
	listenersLock.Lock()
	// If the local server isn't an event-hub client, then we don't need to push messages as the other
	// members should be connected to us via a pull event listener and so will receive the event that way.
	// Also if there are no listeners available then there's no point in pushing to the eventHubPushCh as it
	// will have no consumers reading from it (this allows somewhat graceful handling of the situation where
	// all event-hub members are down by dropping events rather than slowing down the local system).
	if eventMode != EventModeHubClient || len(listeners) <= 0 {
		listenersLock.Unlock()
		return
	}

	listenersLock.Unlock()

	// Run in a go routine so as not to delay caller of this function as we try and deliver it.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), eventHubPushChTimeout)
		defer cancel()

		select {
		case eventHubPushCh <- event:
		case <-ctx.Done(): // Don't block if all consumers are slow/down.
		}
	}()
}
