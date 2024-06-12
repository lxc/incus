package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"github.com/lxc/incus/v6/internal/jmap"
	"github.com/lxc/incus/v6/internal/server/auth"
	"github.com/lxc/incus/v6/internal/server/cluster"
	"github.com/lxc/incus/v6/internal/server/db"
	dbCluster "github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/db/operationtype"
	"github.com/lxc/incus/v6/internal/server/lifecycle"
	"github.com/lxc/incus/v6/internal/server/operations"
	"github.com/lxc/incus/v6/internal/server/request"
	"github.com/lxc/incus/v6/internal/server/response"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/internal/server/task"
	localUtil "github.com/lxc/incus/v6/internal/server/util"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/util"
)

var operationCmd = APIEndpoint{
	Path: "operations/{id}",

	Delete: APIEndpointAction{Handler: operationDelete, AccessHandler: allowAuthenticated},
	Get:    APIEndpointAction{Handler: operationGet, AccessHandler: allowAuthenticated},
}

var operationsCmd = APIEndpoint{
	Path: "operations",

	Get: APIEndpointAction{Handler: operationsGet, AccessHandler: allowAuthenticated},
}

var operationWait = APIEndpoint{
	Path: "operations/{id}/wait",

	Get: APIEndpointAction{Handler: operationWaitGet, AllowUntrusted: true},
}

var operationWebsocket = APIEndpoint{
	Path: "operations/{id}/websocket",

	Get: APIEndpointAction{Handler: operationWebsocketGet, AllowUntrusted: true},
}

// waitForOperations waits for operations to finish.
// There's a timeout for console/exec operations that when reached will shut down the instances forcefully.
func waitForOperations(ctx context.Context, cluster *db.Cluster, consoleShutdownTimeout time.Duration) {
	timeout := time.After(consoleShutdownTimeout)

	defer func() {
		_ = cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			err := dbCluster.DeleteOperations(ctx, tx.Tx(), cluster.GetNodeID())
			if err != nil {
				logger.Error("Failed cleaning up operations")
			}

			return nil
		})
	}()

	// Check operation status every second.
	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	var i int
	for {
		// Get all the operations
		ops := operations.Clone()

		var runningOps, execConsoleOps int
		for _, op := range ops {
			if op.Status() != api.Running || op.Class() == operations.OperationClassToken {
				continue
			}

			runningOps++

			opType := op.Type()
			if opType == operationtype.CommandExec || opType == operationtype.ConsoleShow {
				execConsoleOps++
			}

			_, opAPI, err := op.Render()
			if err != nil {
				logger.Warn("Failed to render operation", logger.Ctx{"operation": op, "err": err})
			} else if opAPI.MayCancel {
				_, _ = op.Cancel()
			}
		}

		// No more running operations left. Exit function.
		if runningOps == 0 {
			logger.Info("All running operations finished, shutting down")
			return
		}

		// Print log message every minute.
		if i%60 == 0 {
			logger.Infof("Waiting for %d operation(s) to finish", runningOps)
		}

		i++

		select {
		case <-timeout:
			// We wait up to core.shutdown_timeout minutes for exec/console operations to finish.
			// If there are still running operations, we continue shutdown which will stop any running
			// instances and terminate the operations.
			if execConsoleOps > 0 {
				logger.Info("Shutdown timeout reached, continuing with shutdown")
			}

			return
		case <-ctx.Done():
			// Return here, and ignore any running operations.
			logger.Info("Forcing shutdown, ignoring running operations")
			return
		case <-tick.C:
		}
	}
}

// API functions

// swagger:operation GET /1.0/operations/{id} operations operation_get
//
//	Get the operation state
//
//	Gets the operation state.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Operation
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          $ref: "#/definitions/Operation"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func operationGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	id, err := url.PathUnescape(mux.Vars(r)["id"])
	if err != nil {
		return response.SmartError(err)
	}

	var body *api.Operation

	// First check if the query is for a local operation from this node
	op, err := operations.OperationGetInternal(id)
	if err == nil {
		_, body, err = op.Render()
		if err != nil {
			return response.SmartError(err)
		}

		return response.SyncResponse(true, body)
	}

	// Then check if the query is from an operation on another node, and, if so, forward it
	var address string
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		filter := dbCluster.OperationFilter{UUID: &id}
		ops, err := dbCluster.GetOperations(ctx, tx.Tx(), filter)
		if err != nil {
			return err
		}

		if len(ops) < 1 {
			return api.StatusErrorf(http.StatusNotFound, "Operation not found")
		}

		if len(ops) > 1 {
			return fmt.Errorf("More than one operation matches")
		}

		operation := ops[0]

		address = operation.NodeAddress
		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	client, err := cluster.Connect(address, s.Endpoints.NetworkCert(), s.ServerCert(), r, false)
	if err != nil {
		return response.SmartError(err)
	}

	return response.ForwardedResponse(client, r)
}

// swagger:operation DELETE /1.0/operations/{id} operations operation_delete
//
//	Cancel the operation
//
//	Cancels the operation if supported.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func operationDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	id, err := url.PathUnescape(mux.Vars(r)["id"])
	if err != nil {
		return response.SmartError(err)
	}

	// First check if the query is for a local operation from this node
	op, err := operations.OperationGetInternal(id)
	if err == nil {
		projectName := op.Project()
		if projectName == "" {
			projectName = api.ProjectDefaultName
		}

		objectType, entitlement := op.Permission()
		if objectType != "" {
			for _, v := range op.Resources() {
				for _, u := range v {
					_, _, _, pathArgs, err := dbCluster.URLToEntityType(u.String())
					if err != nil {
						return response.InternalError(fmt.Errorf("Unable to parse operation resource URL: %w", err))
					}

					object, err := auth.NewObject(objectType, projectName, pathArgs...)
					if err != nil {
						return response.InternalError(fmt.Errorf("Unable to create authorization object for operation: %w", err))
					}

					err = s.Authorizer.CheckPermission(r.Context(), r, object, entitlement)
					if err != nil {
						return response.SmartError(err)
					}
				}
			}
		}

		_, err = op.Cancel()
		if err != nil {
			return response.BadRequest(err)
		}

		s.Events.SendLifecycle(projectName, lifecycle.OperationCancelled.Event(op, request.CreateRequestor(r), nil))

		return response.EmptySyncResponse
	}

	// Then check if the query is from an operation on another node, and, if so, forward it
	var address string
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		filter := dbCluster.OperationFilter{UUID: &id}
		ops, err := dbCluster.GetOperations(ctx, tx.Tx(), filter)
		if err != nil {
			return err
		}

		if len(ops) < 1 {
			return api.StatusErrorf(http.StatusNotFound, "Operation not found")
		}

		if len(ops) > 1 {
			return fmt.Errorf("More than one operation matches")
		}

		operation := ops[0]

		address = operation.NodeAddress
		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	client, err := cluster.Connect(address, s.Endpoints.NetworkCert(), s.ServerCert(), r, false)
	if err != nil {
		return response.SmartError(err)
	}

	return response.ForwardedResponse(client, r)
}

// operationCancel cancels an operation that exists on any member.
func operationCancel(s *state.State, r *http.Request, projectName string, op *api.Operation) error {
	// Check if operation is local and if so, cancel it.
	localOp, _ := operations.OperationGetInternal(op.ID)
	if localOp != nil {
		if localOp.Status() == api.Running {
			_, err := localOp.Cancel()
			if err != nil {
				return fmt.Errorf("Failed to cancel local operation %q: %w", op.ID, err)
			}
		}

		s.Events.SendLifecycle(projectName, lifecycle.OperationCancelled.Event(localOp, request.CreateRequestor(r), nil))

		return nil
	}

	// If not found locally, try connecting to remote member to delete it.
	var memberAddress string
	var err error
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		filter := dbCluster.OperationFilter{UUID: &op.ID}
		ops, err := dbCluster.GetOperations(ctx, tx.Tx(), filter)
		if err != nil {
			return fmt.Errorf("Failed loading operation %q: %w", op.ID, err)
		}

		if len(ops) < 1 {
			return api.StatusErrorf(http.StatusNotFound, "Operation not found")
		}

		if len(ops) > 1 {
			return fmt.Errorf("More than one operation matches")
		}

		operation := ops[0]

		memberAddress = operation.NodeAddress
		return nil
	})
	if err != nil {
		return err
	}

	client, err := cluster.Connect(memberAddress, s.Endpoints.NetworkCert(), s.ServerCert(), r, true)
	if err != nil {
		return fmt.Errorf("Failed to connect to %q: %w", memberAddress, err)
	}

	err = client.UseProject(projectName).DeleteOperation(op.ID)
	if err != nil {
		return fmt.Errorf("Failed to delete remote operation %q on %q: %w", op.ID, memberAddress, err)
	}

	return nil
}

// swagger:operation GET /1.0/operations operations operations_get
//
//  Get the operations
//
//  Returns a JSON object of operation type to operation list (URLs).
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: query
//      name: all-projects
//      description: Retrieve operations from all projects
//      type: boolean
//  responses:
//    "200":
//      description: API endpoints
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            type: object
//            additionalProperties:
//              type: array
//              items:
//                type: string
//            description: JSON object of operation types to operation URLs
//            example: |-
//              {
//                "running": [
//                  "/1.0/operations/6916c8a6-9b7d-4abd-90b3-aedfec7ec7da"
//                ]
//              }
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/operations?recursion=1 operations operations_get_recursion1
//
//	Get the operations
//
//	Returns a list of operations (structs).
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: query
//	    name: all-projects
//	    description: Retrieve operations from all projects
//	    type: boolean
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          type: array
//	          description: List of operations
//	          items:
//	            $ref: "#/definitions/Operation"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func operationsGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.QueryParam(r, "project")
	allProjects := util.IsTrue(request.QueryParam(r, "all-projects"))
	recursion := localUtil.IsRecursionRequest(r)

	if allProjects && projectName != "" {
		return response.SmartError(
			api.StatusErrorf(http.StatusBadRequest, "Cannot specify a project when requesting all projects"),
		)
	} else if !allProjects && projectName == "" {
		projectName = api.ProjectDefaultName
	}

	userHasPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), r, auth.EntitlementCanViewOperations, auth.ObjectTypeProject)
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed to get operation permission checker: %w", err))
	}

	localOperationURLs := func() (jmap.Map, error) {
		// Get all the operations.
		localOps := operations.Clone()

		// Build a list of URLs.
		body := jmap.Map{}

		for _, v := range localOps {
			if !allProjects && v.Project() != "" && v.Project() != projectName {
				continue
			}

			if !userHasPermission(auth.ObjectProject(v.Project())) {
				continue
			}

			status := strings.ToLower(v.Status().String())
			_, ok := body[status]
			if !ok {
				body[status] = make([]string, 0)
			}

			body[status] = append(body[status].([]string), v.URL())
		}

		return body, nil
	}

	localOperations := func() (jmap.Map, error) {
		// Get all the operations.
		localOps := operations.Clone()

		// Build a list of operations.
		body := jmap.Map{}

		for _, v := range localOps {
			if !allProjects && v.Project() != "" && v.Project() != projectName {
				continue
			}

			if !userHasPermission(auth.ObjectProject(v.Project())) {
				continue
			}

			status := strings.ToLower(v.Status().String())
			_, ok := body[status]
			if !ok {
				body[status] = make([]*api.Operation, 0)
			}

			_, op, err := v.Render()
			if err != nil {
				return nil, err
			}

			body[status] = append(body[status].([]*api.Operation), op)
		}

		return body, nil
	}

	// Check if called from a cluster node.
	if isClusterNotification(r) {
		// Only return the local data.
		if recursion {
			// Recursive queries.
			body, err := localOperations()
			if err != nil {
				return response.InternalError(err)
			}

			return response.SyncResponse(true, body)
		}

		// Normal queries
		body, err := localOperationURLs()
		if err != nil {
			return response.InternalError(err)
		}

		return response.SyncResponse(true, body)
	}

	// Start with local operations.
	var md jmap.Map

	if recursion {
		md, err = localOperations()
		if err != nil {
			return response.InternalError(err)
		}
	} else {
		md, err = localOperationURLs()
		if err != nil {
			return response.InternalError(err)
		}
	}

	// If not clustered, then just return local operations.
	if !s.ServerClustered {
		return response.SyncResponse(true, md)
	}

	// Get all nodes with running operations in this project.
	var membersWithOps []string
	var members []db.NodeInfo
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		if allProjects {
			membersWithOps, err = tx.GetAllNodesWithOperations(ctx)
		} else {
			membersWithOps, err = tx.GetNodesWithOperations(ctx, projectName)
		}

		if err != nil {
			return fmt.Errorf("Failed getting members with operations: %w", err)
		}

		members, err = tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting cluster members: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Get local address.
	localClusterAddress := s.LocalConfig.ClusterAddress()
	offlineThreshold := s.GlobalConfig.OfflineThreshold()

	memberOnline := func(memberAddress string) bool {
		for _, member := range members {
			if member.Address == memberAddress {
				if member.IsOffline(offlineThreshold) {
					logger.Warn("Excluding offline member from operations list", logger.Ctx{"member": member.Name, "address": member.Address, "ID": member.ID, "lastHeartbeat": member.Heartbeat})
					return false
				}

				return true
			}
		}

		return false
	}

	networkCert := s.Endpoints.NetworkCert()
	for _, memberAddress := range membersWithOps {
		if memberAddress == localClusterAddress {
			continue
		}

		if !memberOnline(memberAddress) {
			continue
		}

		// Connect to the remote server. Use notify=true to only get local operations on remote member.
		client, err := cluster.Connect(memberAddress, networkCert, s.ServerCert(), r, true)
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed connecting to member %q: %w", memberAddress, err))
		}

		// Get operation data.
		var ops []api.Operation
		if allProjects {
			ops, err = client.GetOperationsAllProjects()
		} else {
			ops, err = client.UseProject(projectName).GetOperations()
		}

		if err != nil {
			logger.Warn("Failed getting operations from member", logger.Ctx{"address": memberAddress, "err": err})
			continue
		}

		// Merge with existing data.
		for _, o := range ops {
			op := o // Local var for pointer.
			status := strings.ToLower(op.Status)

			_, ok := md[status]
			if !ok {
				if recursion {
					md[status] = make([]*api.Operation, 0)
				} else {
					md[status] = make([]string, 0)
				}
			}

			if recursion {
				md[status] = append(md[status].([]*api.Operation), &op)
			} else {
				md[status] = append(md[status].([]string), fmt.Sprintf("/1.0/operations/%s", op.ID))
			}
		}
	}

	return response.SyncResponse(true, md)
}

// operationsGetByType gets all operations for a project and type.
func operationsGetByType(s *state.State, r *http.Request, projectName string, opType operationtype.Type) ([]*api.Operation, error) {
	ops := make([]*api.Operation, 0)

	// Get local operations for project.
	for _, op := range operations.Clone() {
		if op.Project() != projectName || op.Type() != opType {
			continue
		}

		_, apiOp, err := op.Render()
		if err != nil {
			return nil, fmt.Errorf("Failed converting local operation %q to API representation: %w", op.ID(), err)
		}

		ops = append(ops, apiOp)
	}

	// Return just local operations if not clustered.
	if !s.ServerClustered {
		return ops, nil
	}

	// Get all operations of the specified type in project.
	var members []db.NodeInfo
	memberOps := make(map[string]map[string]dbCluster.Operation)
	err := s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		members, err = tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting cluster members: %w", err)
		}

		ops, err := tx.GetOperationsOfType(ctx, projectName, opType)
		if err != nil {
			return fmt.Errorf("Failed getting operations for project %q and type %d: %w", projectName, opType, err)
		}

		// Group operations by member address and UUID.
		for _, op := range ops {
			if memberOps[op.NodeAddress] == nil {
				memberOps[op.NodeAddress] = make(map[string]dbCluster.Operation)
			}

			memberOps[op.NodeAddress][op.UUID] = op
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Get local address.
	localClusterAddress := s.LocalConfig.ClusterAddress()
	offlineThreshold := s.GlobalConfig.OfflineThreshold()

	memberOnline := func(memberAddress string) bool {
		for _, member := range members {
			if member.Address == memberAddress {
				if member.IsOffline(offlineThreshold) {
					logger.Warn("Excluding offline member from operations by type list", logger.Ctx{"member": member.Name, "address": member.Address, "ID": member.ID, "lastHeartbeat": member.Heartbeat, "opType": opType})
					return false
				}

				return true
			}
		}

		return false
	}

	networkCert := s.Endpoints.NetworkCert()
	serverCert := s.ServerCert()
	for memberAddress := range memberOps {
		if memberAddress == localClusterAddress {
			continue
		}

		if !memberOnline(memberAddress) {
			continue
		}

		// Connect to the remote server. Use notify=true to only get local operations on remote member.
		client, err := cluster.Connect(memberAddress, networkCert, serverCert, r, true)
		if err != nil {
			return nil, fmt.Errorf("Failed connecting to member %q: %w", memberAddress, err)
		}

		// Get all remote operations in project.
		remoteOps, err := client.UseProject(projectName).GetOperations()
		if err != nil {
			logger.Warn("Failed getting operations from member", logger.Ctx{"address": memberAddress, "err": err})
			continue
		}

		for _, o := range remoteOps {
			op := o // Local var for pointer.

			// Exclude remote operations that don't have the desired type.
			if memberOps[memberAddress][op.ID].Type != opType {
				continue
			}

			ops = append(ops, &op)
		}
	}

	return ops, nil
}

// swagger:operation GET /1.0/operations/{id}/wait?public operations operation_wait_get_untrusted
//
//  Wait for the operation
//
//  Waits for the operation to reach a final state (or timeout) and retrieve its final state.
//
//  When accessed by an untrusted user, the secret token must be provided.
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: secret
//      description: Authentication token
//      type: string
//      example: random-string
//    - in: query
//      name: timeout
//      description: Timeout in seconds (-1 means never)
//      type: integer
//      example: -1
//  responses:
//    "200":
//      description: Operation
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            $ref: "#/definitions/Operation"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/operations/{id}/wait operations operation_wait_get
//
//	Wait for the operation
//
//	Waits for the operation to reach a final state (or timeout) and retrieve its final state.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: timeout
//	    description: Timeout in seconds (-1 means never)
//	    type: integer
//	    example: -1
//	responses:
//	  "200":
//	    description: Operation
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          $ref: "#/definitions/Operation"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func operationWaitGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	id, err := url.PathUnescape(mux.Vars(r)["id"])
	if err != nil {
		return response.SmartError(err)
	}

	secret := r.FormValue("secret")

	trusted, _, _, _ := d.Authenticate(nil, r)
	if !trusted && secret == "" {
		return response.Forbidden(nil)
	}

	timeoutSecs := -1
	if r.FormValue("timeout") != "" {
		timeoutSecs, err = strconv.Atoi(r.FormValue("timeout"))
		if err != nil {
			return response.InternalError(err)
		}
	}

	// First check if the query is for a local operation from this node
	op, err := operations.OperationGetInternal(id)
	if err == nil {
		if secret != "" && op.Metadata()["secret"] != secret {
			return response.Forbidden(nil)
		}

		var ctx context.Context
		var cancel context.CancelFunc

		// If timeout is -1, it will wait indefinitely otherwise it will timeout after timeoutSecs.
		if timeoutSecs > -1 {
			ctx, cancel = context.WithDeadline(r.Context(), time.Now().Add(time.Second*time.Duration(timeoutSecs)))
		} else {
			ctx, cancel = context.WithCancel(r.Context())
		}

		waitResponse := func(w http.ResponseWriter) error {
			defer cancel()

			// Write header to avoid client side timeouts.
			w.Header().Set("Connection", "keep-alive")
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.WriteHeader(http.StatusOK)
			f, ok := w.(http.Flusher)
			if ok {
				f.Flush()
			}

			// Wait for the operation.
			err = op.Wait(ctx)
			if err != nil {
				_ = response.SmartError(err).Render(w)
				return nil
			}

			_, body, err := op.Render()
			if err != nil {
				_ = response.SmartError(err).Render(w)
				return nil
			}

			_ = response.SyncResponse(true, body).Render(w)
			return nil
		}

		return response.ManualResponse(waitResponse)
	}

	// Then check if the query is from an operation on another node, and, if so, forward it
	var address string
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		filter := dbCluster.OperationFilter{UUID: &id}
		ops, err := dbCluster.GetOperations(ctx, tx.Tx(), filter)
		if err != nil {
			return err
		}

		if len(ops) < 1 {
			return api.StatusErrorf(http.StatusNotFound, "Operation not found")
		}

		if len(ops) > 1 {
			return fmt.Errorf("More than one operation matches")
		}

		operation := ops[0]

		address = operation.NodeAddress
		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	client, err := cluster.Connect(address, s.Endpoints.NetworkCert(), s.ServerCert(), r, false)
	if err != nil {
		return response.SmartError(err)
	}

	return response.ForwardedResponse(client, r)
}

type operationWebSocket struct {
	req *http.Request
	op  *operations.Operation
}

func (r *operationWebSocket) Render(w http.ResponseWriter) error {
	chanErr, err := r.op.Connect(r.req, w)
	if err != nil {
		return err
	}

	err = <-chanErr
	return err
}

func (r *operationWebSocket) String() string {
	_, md, err := r.op.Render()
	if err != nil {
		return fmt.Sprintf("error: %s", err)
	}

	return md.ID
}

// Code returns the HTTP code.
func (r *operationWebSocket) Code() int {
	return http.StatusOK
}

// swagger:operation GET /1.0/operations/{id}/websocket?public operations operation_websocket_get_untrusted
//
//  Get the websocket stream
//
//  Connects to an associated websocket stream for the operation.
//  This should almost never be done directly by a client, instead it's
//  meant for server to server communication with the client only relaying the
//  connection information to the servers.
//
//  The untrusted endpoint is used by the target server to connect to the source server.
//  Authentication is performed through the secret token.
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: secret
//      description: Authentication token
//      type: string
//      example: random-string
//  responses:
//    "200":
//      description: Websocket operation messages (dependent on operation)
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/operations/{id}/websocket operations operation_websocket_get
//
//	Get the websocket stream
//
//	Connects to an associated websocket stream for the operation.
//	This should almost never be done directly by a client, instead it's
//	meant for server to server communication with the client only relaying the
//	connection information to the servers.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: secret
//	    description: Authentication token
//	    type: string
//	    example: random-string
//	responses:
//	  "200":
//	    description: Websocket operation messages (dependent on operation)
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func operationWebsocketGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	id, err := url.PathUnescape(mux.Vars(r)["id"])
	if err != nil {
		return response.SmartError(err)
	}

	// First check if the query is for a local operation from this node
	op, err := operations.OperationGetInternal(id)
	if err == nil {
		return &operationWebSocket{r, op}
	}

	// Then check if the query is from an operation on another node, and, if so, forward it
	secret := r.FormValue("secret")
	if secret == "" {
		return response.BadRequest(fmt.Errorf("Missing websocket secret"))
	}

	var address string
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		filter := dbCluster.OperationFilter{UUID: &id}
		ops, err := dbCluster.GetOperations(ctx, tx.Tx(), filter)
		if err != nil {
			return err
		}

		if len(ops) < 1 {
			return api.StatusErrorf(http.StatusNotFound, "Operation not found")
		}

		if len(ops) > 1 {
			return fmt.Errorf("More than one operation matches")
		}

		operation := ops[0]

		address = operation.NodeAddress
		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	client, err := cluster.Connect(address, s.Endpoints.NetworkCert(), s.ServerCert(), r, false)
	if err != nil {
		return response.SmartError(err)
	}

	source, err := client.GetOperationWebsocket(id, secret)
	if err != nil {
		return response.SmartError(err)
	}

	return operations.ForwardedOperationWebSocket(r, id, source)
}

func autoRemoveOrphanedOperationsTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		s := d.State()

		localClusterAddress := s.LocalConfig.ClusterAddress()

		leader, err := d.gateway.LeaderAddress()
		if err != nil {
			if errors.Is(err, cluster.ErrNodeIsNotClustered) {
				return // No error if not clustered.
			}

			logger.Error("Failed to get leader cluster member address", logger.Ctx{"err": err})
			return
		}

		if localClusterAddress != leader {
			logger.Debug("Skipping remove orphaned operations task since we're not leader")
			return
		}

		opRun := func(op *operations.Operation) error {
			return autoRemoveOrphanedOperations(ctx, s)
		}

		op, err := operations.OperationCreate(s, "", operations.OperationClassTask, operationtype.RemoveOrphanedOperations, nil, nil, opRun, nil, nil, nil)
		if err != nil {
			logger.Error("Failed creating remove orphaned operations operation", logger.Ctx{"err": err})
			return
		}

		err = op.Start()
		if err != nil {
			logger.Error("Failed starting remove orphaned operations operation", logger.Ctx{"err": err})
			return
		}

		err = op.Wait(ctx)
		if err != nil {
			logger.Error("Failed removing orphaned operations", logger.Ctx{"err": err})
			return
		}
	}

	return f, task.Hourly()
}

// autoRemoveOrphanedOperations removes old operations from offline members. Operations can be left
// behind if a cluster member abruptly becomes unreachable. If the affected cluster members comes
// back online, these operations won't be cleaned up. We therefore need to periodically clean up
// such operations.
func autoRemoveOrphanedOperations(ctx context.Context, s *state.State) error {
	logger.Debug("Removing orphaned operations across the cluster")

	offlineThreshold := s.GlobalConfig.OfflineThreshold()

	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		members, err := tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting cluster members: %w", err)
		}

		for _, member := range members {
			// Skip online nodes
			if !member.IsOffline(offlineThreshold) {
				continue
			}

			err = dbCluster.DeleteOperations(ctx, tx.Tx(), member.ID)
			if err != nil {
				return fmt.Errorf("Failed to delete operations: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to remove orphaned operations: %w", err)
	}

	logger.Debug("Done removing orphaned operations across the cluster")

	return nil
}
