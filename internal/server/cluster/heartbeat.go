package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/db/cluster"
	"github.com/lxc/incus/internal/server/db/query"
	"github.com/lxc/incus/internal/server/db/warningtype"
	"github.com/lxc/incus/internal/server/response"
	"github.com/lxc/incus/internal/server/task"
	"github.com/lxc/incus/internal/server/warnings"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
	localtls "github.com/lxc/incus/shared/tls"
)

type heartbeatMode int

const (
	hearbeatNormal heartbeatMode = iota
	hearbeatImmediate
	hearbeatInitial
)

// APIHeartbeatMember contains specific cluster node info.
type APIHeartbeatMember struct {
	ID            int64            // ID field value in nodes table.
	Address       string           // Host and Port of node.
	Name          string           // Name of cluster member.
	RaftID        uint64           // ID field value in raft_nodes table, zero if non-raft node.
	RaftRole      int              // Node role in the raft cluster, from the raft_nodes table
	LastHeartbeat time.Time        // Last time we received a successful response from node.
	Online        bool             // Calculated from offline threshold and LastHeatbeat time.
	Roles         []db.ClusterRole // Supplementary non-database roles the member has.
	updated       bool             // Has node been updated during this heartbeat run. Not sent to nodes.
}

// APIHeartbeatVersion contains max versions for all nodes in cluster.
type APIHeartbeatVersion struct {
	Schema        int
	APIExtensions int
}

// NewAPIHearbeat returns initialized APIHeartbeat.
func NewAPIHearbeat(cluster *db.Cluster) *APIHeartbeat {
	return &APIHeartbeat{
		cluster: cluster,
	}
}

// APIHeartbeat contains data sent to nodes in heartbeat.
type APIHeartbeat struct {
	sync.Mutex // Used to control access to Members maps.
	cluster    *db.Cluster
	Members    map[int64]APIHeartbeatMember
	Version    APIHeartbeatVersion
	Time       time.Time

	// Indicates if heartbeat contains a fresh set of node states.
	// This can be used to indicate to the receiving node that the state is fresh enough to
	// trigger node refresh activies.
	FullStateList bool
}

// Update updates an existing APIHeartbeat struct with the raft and all node states supplied.
// If allNodes provided is an empty set then this is considered a non-full state list.
func (hbState *APIHeartbeat) Update(fullStateList bool, raftNodes []db.RaftNode, allNodes []db.NodeInfo, offlineThreshold time.Duration) {
	var maxSchemaVersion, maxAPIExtensionsVersion int

	if hbState.Members == nil {
		hbState.Members = make(map[int64]APIHeartbeatMember)
	}

	// If we've been supplied a fresh set of node states, this is a full state list.
	hbState.FullStateList = fullStateList

	// Convert raftNodes to a map keyed on address for lookups later.
	raftNodeMap := make(map[string]db.RaftNode, len(raftNodes))
	for _, raftNode := range raftNodes {
		raftNodeMap[raftNode.Address] = raftNode
	}

	// Add nodes (overwrites any nodes with same ID in map with fresh data).
	for _, node := range allNodes {
		member := APIHeartbeatMember{
			ID:            node.ID,
			Address:       node.Address,
			Name:          node.Name,
			LastHeartbeat: node.Heartbeat,
			Online:        !node.IsOffline(offlineThreshold),
			Roles:         node.Roles,
		}

		raftNode, exists := raftNodeMap[member.Address]
		if exists {
			member.RaftID = raftNode.ID
			member.RaftRole = int(raftNode.Role)
			delete(raftNodeMap, member.Address) // Used to check any remaining later.
		}

		// Add to the members map using the node ID (not the Raft Node ID).
		hbState.Members[node.ID] = member

		// Keep a record of highest APIExtensions and Schema version seen in all nodes.
		if node.APIExtensions > maxAPIExtensionsVersion {
			maxAPIExtensionsVersion = node.APIExtensions
		}

		if node.Schema > maxSchemaVersion {
			maxSchemaVersion = node.Schema
		}
	}

	hbState.Version = APIHeartbeatVersion{
		Schema:        maxSchemaVersion,
		APIExtensions: maxAPIExtensionsVersion,
	}

	if len(raftNodeMap) > 0 && hbState.cluster != nil {
		_ = hbState.cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			for addr, raftNode := range raftNodeMap {
				_, err := tx.GetPendingNodeByAddress(ctx, addr)
				if err != nil {
					logger.Errorf("Unaccounted raft node(s) not found in 'nodes' table for heartbeat: %+v", raftNode)
				}
			}

			return nil
		})
	}
}

// Send sends heartbeat requests to the nodes supplied and updates heartbeat state.
func (hbState *APIHeartbeat) Send(ctx context.Context, networkCert *localtls.CertInfo, serverCert *localtls.CertInfo, localAddress string, nodes []db.NodeInfo, spreadDuration time.Duration) {
	heartbeatsWg := sync.WaitGroup{}
	sendHeartbeat := func(nodeID int64, address string, spreadDuration time.Duration, heartbeatData *APIHeartbeat) {
		defer heartbeatsWg.Done()

		if spreadDuration > 0 {
			// Spread in time by waiting up to 3s less than the interval.
			spreadDurationMs := int(spreadDuration.Milliseconds())
			spreadRange := spreadDurationMs - 3000

			if spreadRange > 0 {
				select {
				case <-time.After(time.Duration(rand.Intn(spreadRange)) * time.Millisecond):
				case <-ctx.Done(): // Proceed immediately to heartbeat of member if asked to.
				}
			}
		}

		// Update timestamp to current, used for time skew detection
		heartbeatData.Time = time.Now().UTC()

		// Don't use ctx here, as we still want to finish off the request if the ctx has been cancelled.
		err := HeartbeatNode(context.Background(), address, networkCert, serverCert, heartbeatData)
		if err == nil {
			heartbeatData.Lock()
			// Ensure only update nodes that exist in Members already.
			hbNode, existing := hbState.Members[nodeID]
			if !existing {
				return
			}

			hbNode.LastHeartbeat = time.Now()
			hbNode.Online = true
			hbNode.updated = true
			heartbeatData.Members[nodeID] = hbNode
			heartbeatData.Unlock()
			logger.Debug("Successful heartbeat", logger.Ctx{"remote": address})

			err = warnings.ResolveWarningsByLocalNodeAndProjectAndTypeAndEntity(hbState.cluster, "", warningtype.OfflineClusterMember, cluster.TypeNode, int(nodeID))
			if err != nil {
				logger.Warn("Failed to resolve warning", logger.Ctx{"err": err})
			}
		} else {
			logger.Warn("Failed heartbeat", logger.Ctx{"remote": address, "err": err})

			if ctx.Err() == nil {
				err = hbState.cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
					return tx.UpsertWarningLocalNode(ctx, "", cluster.TypeNode, int(nodeID), warningtype.OfflineClusterMember, err.Error())
				})
				if err != nil {
					logger.Warn("Failed to create warning", logger.Ctx{"err": err})
				}
			}
		}
	}

	for _, node := range nodes {
		// Special case for the local member - just record the time now.
		if node.Address == localAddress {
			hbState.Lock()
			hbNode := hbState.Members[node.ID]
			hbNode.LastHeartbeat = time.Now()
			hbNode.Online = true
			hbNode.updated = true
			hbState.Members[node.ID] = hbNode
			hbState.Unlock()
			continue
		}

		// Parallelize the rest.
		heartbeatsWg.Add(1)
		go sendHeartbeat(node.ID, node.Address, spreadDuration, hbState)
	}

	heartbeatsWg.Wait()
}

// HeartbeatTask returns a task function that performs leader-initiated heartbeat
// checks against all cluster members in the cluster.
//
// It will update the heartbeat timestamp column of the nodes table
// accordingly, and also notify them of the current list of database nodes.
func HeartbeatTask(gateway *Gateway) (task.Func, task.Schedule) {
	// Since the database APIs are blocking we need to wrap the core logic
	// and run it in a goroutine, so we can abort as soon as the context expires.
	heartbeatWrapper := func(ctx context.Context) {
		if gateway.HearbeatCancelFunc() == nil {
			ch := make(chan struct{})
			go func() {
				gateway.heartbeat(ctx, hearbeatNormal)
				close(ch)
			}()
			select {
			case <-ch:
			case <-ctx.Done():
			}
		}
	}

	schedule := func() (time.Duration, error) {
		return task.Every(gateway.heartbeatInterval())()
	}

	return heartbeatWrapper, schedule
}

// heartbeatInterval returns heartbeat interval to use.
func (g *Gateway) heartbeatInterval() time.Duration {
	threshold := g.HeartbeatOfflineThreshold
	if threshold <= 0 {
		threshold = time.Duration(db.DefaultOfflineThreshold) * time.Second
	}

	return threshold / 2
}

// HearbeatCancelFunc returns the function that can be used to cancel an ongoing heartbeat.
// Returns nil if no ongoing heartbeat.
func (g *Gateway) HearbeatCancelFunc() func() {
	g.heartbeatCancelLock.Lock()
	defer g.heartbeatCancelLock.Unlock()
	return g.heartbeatCancel
}

// HeartbeatRestart restarts cancels any ongoing heartbeat and restarts it.
// If there is no ongoing heartbeat then this is a no-op.
// Returns true if new heartbeat round was started.
func (g *Gateway) HeartbeatRestart() bool {
	heartbeatCancel := g.HearbeatCancelFunc()

	// There is a cancellable heartbeat round ongoing.
	if heartbeatCancel != nil {
		g.heartbeatCancel() // Request ongoing hearbeat round cancel itself.

		// Start a new heartbeat round async that will run as soon as ongoing heartbeat round exits.
		go g.heartbeat(g.ctx, hearbeatImmediate)

		return true
	}

	return false
}

func (g *Gateway) heartbeat(ctx context.Context, mode heartbeatMode) {
	if g.Cluster == nil || g.server == nil || g.memoryDial != nil {
		// We're not a raft node or we're not clustered
		return
	}

	// Avoid concurent heartbeat loops.
	// This is possible when both the regular task and the out of band heartbeat round from a dqlite
	// connection or notification restart both kick in at the same time.
	g.HeartbeatLock.Lock()
	defer g.HeartbeatLock.Unlock()

	// Acquire the cancellation lock and populate it so that this heartbeat round can be cancelled if a
	// notification cancellation request arrives during the round. Also setup a defer so that the cancellation
	// function is set to nil when this function ends to indicate there is no ongoing heartbeat round.
	g.heartbeatCancelLock.Lock()
	ctx, g.heartbeatCancel = context.WithCancel(ctx)
	g.heartbeatCancelLock.Unlock()

	defer func() {
		heartbeatCancel := g.HearbeatCancelFunc()
		if heartbeatCancel != nil {
			g.heartbeatCancel()
			g.heartbeatCancel = nil
		}
	}()

	raftNodes, err := g.currentRaftNodes()
	if err != nil {
		if errors.Is(err, ErrNotLeader) {
			return
		}

		logger.Error("Failed to get current raft members", logger.Ctx{"err": err})
		return
	}

	// Address of this node.
	var localClusterAddress string
	s := g.state()

	if s.LocalConfig != nil {
		localClusterAddress = s.LocalConfig.ClusterAddress()
	}

	var members []db.NodeInfo
	err = g.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		members, err = tx.GetNodes(ctx)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		logger.Warn("Failed to get current cluster members", logger.Ctx{"err": err})
		return
	}

	modeStr := "normal"
	switch mode {
	case hearbeatImmediate:
		modeStr = "immediate"
	case hearbeatInitial:
		modeStr = "initial"
	}

	if mode != hearbeatNormal {
		// Log unscheduled heartbeats with a higher level than normal heartbeats.
		logger.Info("Starting heartbeat round", logger.Ctx{"mode": modeStr, "local": localClusterAddress})
	} else {
		// Don't spam the normal log with regular heartbeat messages.
		logger.Debug("Starting heartbeat round", logger.Ctx{"mode": modeStr, "local": localClusterAddress})
	}

	// Replace the local raft_nodes table immediately because it
	// might miss a row containing ourselves, since we might have
	// been elected leader before the former leader had chance to
	// send us a fresh update through the heartbeat pool.
	logger.Debug("Heartbeat updating local raft members", logger.Ctx{"members": raftNodes})
	err = g.db.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
		return tx.ReplaceRaftNodes(raftNodes)
	})
	if err != nil {
		logger.Warn("Failed to replace local raft members", logger.Ctx{"err": err, "mode": modeStr, "local": localClusterAddress})
		return
	}

	if localClusterAddress == "" {
		logger.Error("No local address set, aborting heartbeat round", logger.Ctx{"mode": modeStr})
		return
	}

	startTime := time.Now()

	heartbeatInterval := g.heartbeatInterval()

	// Cumulative set of node states (will be written back to database once done).
	hbState := NewAPIHearbeat(g.Cluster)

	// If we are doing a normal heartbeat round then spread the requests over the heartbeatInterval in order
	// to reduce load on the cluster.
	spreadDuration := time.Duration(0)
	if mode == hearbeatNormal {
		spreadDuration = heartbeatInterval
	}

	serverCert := g.state().ServerCert()

	// If this leader node hasn't sent a heartbeat recently, then its node state records
	// are likely out of date, this can happen when a node becomes a leader.
	// Send stale set to all nodes in database to get a fresh set of active nodes.
	if mode == hearbeatInitial {
		hbState.Update(false, raftNodes, members, g.HeartbeatOfflineThreshold)
		hbState.Send(ctx, g.networkCert, serverCert, localClusterAddress, members, spreadDuration)

		// We have the latest set of node states now, lets send that state set to all nodes.
		hbState.FullStateList = true
		hbState.Send(ctx, g.networkCert, serverCert, localClusterAddress, members, spreadDuration)
	} else {
		hbState.Update(true, raftNodes, members, g.HeartbeatOfflineThreshold)
		hbState.Send(ctx, g.networkCert, serverCert, localClusterAddress, members, spreadDuration)
	}

	// Check if context has been cancelled.
	ctxErr := ctx.Err()

	// Look for any new node which appeared since sending last heartbeat.
	if ctxErr == nil {
		var currentMembers []db.NodeInfo
		err = g.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			var err error
			currentMembers, err = tx.GetNodes(ctx)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			logger.Warn("Failed to get current cluster members", logger.Ctx{"err": err, "mode": modeStr, "local": localClusterAddress})
			return
		}

		newMembers := []db.NodeInfo{}
		for _, currentMember := range currentMembers {
			existing := false
			for _, member := range members {
				if member.Address == currentMember.Address && member.ID == currentMember.ID {
					existing = true
					break
				}
			}

			if !existing {
				// We found a new node
				members = append(members, currentMember)
				newMembers = append(newMembers, currentMember)
			}
		}

		// If any new nodes found, send heartbeat to just them (with full node state).
		if len(newMembers) > 0 {
			hbState.Update(true, raftNodes, members, g.HeartbeatOfflineThreshold)
			hbState.Send(ctx, g.networkCert, serverCert, localClusterAddress, newMembers, 0)
		}
	}

	// Initialize slice to indicate to HeartbeatNodeHook that its being called from leader.
	unavailableMembers := make([]string, 0)

	err = query.Retry(ctx, func(ctx context.Context) error {
		// Durating cluster member fluctuations/upgrades the cluster can become unavailable so check here.
		if g.Cluster == nil {
			return fmt.Errorf("Cluster unavailable")
		}

		return g.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			for _, node := range hbState.Members {
				if !node.updated {
					// If member has not been updated during this heartbeat round it means
					// they are currently unreachable or rejecting heartbeats due to being
					// in the process of shutting down. Either way we do not want to use this
					// member as a candidate for role promotion.
					unavailableMembers = append(unavailableMembers, node.Address)
					continue
				}

				err := tx.SetNodeHeartbeat(node.Address, node.LastHeartbeat)
				if err != nil && !response.IsNotFoundError(err) {
					return fmt.Errorf("Failed updating heartbeat time for member %q: %w", node.Address, err)
				}
			}

			return nil
		})
	})
	if err != nil {
		logger.Error("Failed updating cluster heartbeats", logger.Ctx{"err": err})
		return
	}

	// If the context has been cancelled, return prematurely after saving the members we did manage to ping.
	if ctxErr != nil {
		logger.Warn("Aborting heartbeat round", logger.Ctx{"err": ctxErr, "mode": modeStr, "local": localClusterAddress})
		return
	}

	// If full node state was sent and node refresh task is specified.
	if g.HeartbeatNodeHook != nil {
		g.HeartbeatNodeHook(hbState, true, unavailableMembers)
	}

	duration := time.Since(startTime)
	if duration > heartbeatInterval {
		logger.Warn("Heartbeat round duration greater than heartbeat interval", logger.Ctx{"duration": duration, "interval": heartbeatInterval})
	}

	if mode != hearbeatNormal {
		// Log unscheduled heartbeats with a higher level than normal heartbeats.
		logger.Info("Completed heartbeat round", logger.Ctx{"duration": duration, "local": localClusterAddress})
	} else {
		// Don't spam the normal log with regular heartbeat messages.
		logger.Debug("Completed heartbeat round", logger.Ctx{"duration": duration, "local": localClusterAddress})
	}
}

// HeartbeatNode performs a single heartbeat request against the node with the given address.
func HeartbeatNode(taskCtx context.Context, address string, networkCert *localtls.CertInfo, serverCert *localtls.CertInfo, heartbeatData *APIHeartbeat) error {
	logger.Debug("Sending heartbeat request", logger.Ctx{"address": address})

	config, err := tlsClientConfig(networkCert, serverCert)
	if err != nil {
		return err
	}

	timeout := 2 * time.Second
	url := fmt.Sprintf("https://%s%s", address, databaseEndpoint)
	transport, cleanup := tlsTransport(config)
	defer cleanup()
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}

	buffer := bytes.Buffer{}
	heartbeatData.Lock()
	err = json.NewEncoder(&buffer).Encode(heartbeatData)
	heartbeatData.Unlock()
	if err != nil {
		return err
	}

	request, err := http.NewRequest("PUT", url, bytes.NewReader(buffer.Bytes()))
	if err != nil {
		return err
	}

	setDqliteVersionHeader(request)

	// Use 1s later timeout to give HTTP client chance timeout with more useful info.
	ctx, cancel := context.WithTimeout(taskCtx, timeout+time.Second)
	defer cancel()
	request = request.WithContext(ctx)
	request.Close = true // Immediately close the connection after the request is done

	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("Failed to send heartbeat request: %w", err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Heartbeat request failed with status: %w", api.StatusErrorf(response.StatusCode, response.Status))
	}

	return nil
}
