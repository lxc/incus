package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"

	"github.com/lxc/incus/v7/internal/server/auth"
	"github.com/lxc/incus/v7/internal/server/cluster"
	"github.com/lxc/incus/v7/internal/server/db"
	dbCluster "github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/events"
	"github.com/lxc/incus/v7/internal/server/request"
	"github.com/lxc/incus/v7/internal/server/response"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
	"github.com/lxc/incus/v7/shared/util"
	"github.com/lxc/incus/v7/shared/ws"
)

var (
	eventTypes           = []string{api.EventTypeLogging, api.EventTypeOperation, api.EventTypeLifecycle, api.EventTypeNetworkACL}
	privilegedEventTypes = []string{api.EventTypeLogging, api.EventTypeNetworkACL}
)

var eventsCmd = APIEndpoint{
	Path: "events",

	Get: APIEndpointAction{Handler: eventsGet, AccessHandler: allowAuthenticated},
}

type eventsServe struct {
	req *http.Request
	s   *state.State
}

func (r *eventsServe) Render(w http.ResponseWriter) error {
	return eventsSocket(r.s, r.req, w)
}

func (r *eventsServe) String() string {
	return "event handler"
}

// Code returns the HTTP code.
func (r *eventsServe) Code() int {
	return http.StatusOK
}

func eventsSocket(s *state.State, r *http.Request, w http.ResponseWriter) error {
	// Relay the event stream of another cluster member when targeted (groups are served locally).
	targetMember := request.QueryParam(r, "target")
	if targetMember != "" && !strings.HasPrefix(targetMember, targetGroupPrefix) {
		address, err := cluster.ResolveTarget(r.Context(), s, targetMember)
		if err != nil {
			return err
		}

		if address != "" {
			return eventsForward(s, r, w, address)
		}
	}

	// Detect project mode.
	projectName := request.QueryParam(r, "project")
	allProjects := util.IsTrue(request.QueryParam(r, "all-projects"))

	if allProjects && projectName != "" {
		return api.StatusErrorf(http.StatusBadRequest, "Cannot specify a project when requesting all projects")
	} else if !allProjects && projectName == "" {
		projectName = api.ProjectDefaultName
	}

	if !allProjects && projectName != api.ProjectDefaultName {
		_, err := s.DB.GetProject(context.Background(), projectName)
		if err != nil {
			return err
		}
	}

	var projectPermissionFunc auth.PermissionChecker
	if projectName != "" {
		err := s.Authorizer.CheckPermission(r.Context(), r, auth.ObjectProject(projectName), auth.EntitlementCanViewEvents)
		if err != nil {
			return err
		}
	} else if allProjects {
		var err error
		projectPermissionFunc, err = s.Authorizer.GetPermissionChecker(r.Context(), r, auth.EntitlementCanViewEvents, auth.ObjectTypeProject)
		if err != nil {
			return err
		}
	}

	canViewPrivilegedEvents := s.Authorizer.CheckPermission(r.Context(), r, auth.ObjectServer(), auth.EntitlementCanViewPrivilegedEvents) == nil

	types := strings.Split(r.FormValue("type"), ",")
	if len(types) == 1 && types[0] == "" {
		types = []string{}
		for _, entry := range eventTypes {
			if !canViewPrivilegedEvents && slices.Contains(privilegedEventTypes, entry) {
				continue
			}

			types = append(types, entry)
		}
	}

	// Validate event types.
	for _, entry := range types {
		if !slices.Contains(eventTypes, entry) {
			return api.StatusErrorf(http.StatusBadRequest, "%q isn't a supported event type", entry)
		}
	}

	if !canViewPrivilegedEvents {
		for _, entry := range types {
			if slices.Contains(privilegedEventTypes, entry) {
				return api.StatusErrorf(http.StatusForbidden, "Forbidden")
			}
		}
	}

	l := logger.AddContext(logger.Ctx{"remote": r.RemoteAddr})

	var excludeLocations []string
	if isClusterNotification(r) {
		ctx := r.Context()

		// Try and match cluster member certificate fingerprint to member name.
		fingerprint, found := ctx.Value(request.CtxUsername).(string)
		if found {
			err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				cert, err := dbCluster.GetCertificateByFingerprintPrefix(context.Background(), tx.Tx(), fingerprint)
				if err != nil {
					return fmt.Errorf("Failed matching client certificate to cluster member: %w", err)
				}

				// Add the cluster member client's name to the excluded locations so that we can avoid
				// looping the event back to them when they send us an event via recvFunc.
				excludeLocations = append(excludeLocations, cert.Name)

				return nil
			})
			if err != nil {
				l.Warn("Failed setting up event connection", logger.Ctx{"err": err})
				return nil
			}
		}
	}

	var recvFunc events.EventHandler
	var excludeSources []events.EventSource
	if isClusterNotification(r) {
		// If client is another cluster member, it will already be pulling events from other cluster
		// members so no need to also deliver forwarded events that this member receives.
		excludeSources = append(excludeSources, events.EventSourcePull)

		recvFunc = func(event api.Event) {
			// Inject event received via push from event listener client so its forwarded to
			// other event hub members (if operating in event hub mode).
			s.Events.Inject(event, events.EventSourcePush)
		}
	}

	// Upgrade the connection to websocket as late as possible.
	// This is because the client will assume it's getting events as soon as the upgrade is performed.
	conn, err := ws.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		l.Warn("Failed upgrading event connection", logger.Ctx{"err": err})
		return nil
	}

	defer logger.WarnOnErrorExcept(conn.Close, []error{net.ErrClosed}, "Failed to close connection") // Ensure listener below ends when this function ends.

	listenerConnection := events.NewWebsocketListenerConnection(conn)
	listener, err := s.Events.AddListener(projectName, allProjects, canViewPrivilegedEvents, projectPermissionFunc, listenerConnection, types, excludeSources, recvFunc, excludeLocations)
	if err != nil {
		l.Warn("Failed to add event listener", logger.Ctx{"err": err})
		return nil
	}

	listener.SetRequestor(requestIdentity(r))

	listener.Wait(r.Context())

	return nil
}

// eventsForward relays the event stream of the target cluster member to the client.
func eventsForward(s *state.State, r *http.Request, w http.ResponseWriter, address string) error {
	client, err := cluster.Connect(address, s.Endpoints.NetworkCert(), s.ServerCert(), r, false)
	if err != nil {
		return err
	}

	// The target resolves to itself and serves the stream locally.
	source, err := client.RawWebsocket("/events?" + r.URL.RawQuery)
	if err != nil {
		return err
	}

	defer logger.WarnOnErrorExcept(source.Close, []error{net.ErrClosed}, "Failed to close source connection")

	conn, err := ws.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.Warn("Failed upgrading event connection", logger.Ctx{"remote": r.RemoteAddr, "err": err})
		return nil
	}

	defer logger.WarnOnErrorExcept(conn.Close, []error{net.ErrClosed}, "Failed to close connection")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Unblock the relay loop below once the client is gone.
	stop := context.AfterFunc(ctx, func() { _ = source.Close() })
	defer stop()

	// The listener connection pings the client and notices it going away.
	listenerConnection := events.NewWebsocketListenerConnection(conn)
	go func() {
		listenerConnection.Reader(ctx, nil)
		cancel()
	}()

	for {
		event := api.Event{}
		err = source.ReadJSON(&event)
		if err != nil {
			return nil
		}

		err = listenerConnection.WriteJSON(event)
		if err != nil {
			return nil
		}
	}
}

// swagger:operation GET /1.0/events server events_get
//
//	Get the event stream
//
//	Connects to the event API using websocket.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    x-example: default
//	  - in: query
//	    name: type
//	    description: Event type(s), comma separated (valid types are logging, operation or lifecycle)
//	    type: string
//	    x-example: logging,lifecycle
//	  - in: query
//	    name: all-projects
//	    description: Retrieve instances from all projects
//	    type: boolean
//	  - in: query
//	    name: target
//	    description: Cluster member
//	    type: string
//	    x-example: default
//	responses:
//	  "101":
//	    description: Websocket message (JSON)
//	    schema:
//	      $ref: "#/definitions/Event"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func eventsGet(d *Daemon, r *http.Request) response.Response {
	return &eventsServe{req: r, s: d.State()}
}
