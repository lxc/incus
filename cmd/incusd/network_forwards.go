package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/lxc/incus/v6/internal/filter"
	"github.com/lxc/incus/v6/internal/server/auth"
	clusterRequest "github.com/lxc/incus/v6/internal/server/cluster/request"
	"github.com/lxc/incus/v6/internal/server/db"
	dbCluster "github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/lifecycle"
	"github.com/lxc/incus/v6/internal/server/network"
	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/internal/server/request"
	"github.com/lxc/incus/v6/internal/server/response"
	localUtil "github.com/lxc/incus/v6/internal/server/util"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

var networkForwardsCmd = APIEndpoint{
	Path: "networks/{networkName}/forwards",

	Get:  APIEndpointAction{Handler: networkForwardsGet, AccessHandler: allowPermission(auth.ObjectTypeNetwork, auth.EntitlementCanView, "networkName")},
	Post: APIEndpointAction{Handler: networkForwardsPost, AccessHandler: allowPermission(auth.ObjectTypeNetwork, auth.EntitlementCanEdit, "networkName")},
}

var networkForwardCmd = APIEndpoint{
	Path: "networks/{networkName}/forwards/{listenAddress}",

	Delete: APIEndpointAction{Handler: networkForwardDelete, AccessHandler: allowPermission(auth.ObjectTypeNetwork, auth.EntitlementCanEdit, "networkName")},
	Get:    APIEndpointAction{Handler: networkForwardGet, AccessHandler: allowPermission(auth.ObjectTypeNetwork, auth.EntitlementCanView, "networkName")},
	Put:    APIEndpointAction{Handler: networkForwardPut, AccessHandler: allowPermission(auth.ObjectTypeNetwork, auth.EntitlementCanEdit, "networkName")},
	Patch:  APIEndpointAction{Handler: networkForwardPut, AccessHandler: allowPermission(auth.ObjectTypeNetwork, auth.EntitlementCanEdit, "networkName")},
}

// API endpoints

// swagger:operation GET /1.0/networks/{networkName}/forwards network-forwards network_forwards_get
//
//  Get the network address forwards
//
//  Returns a list of network address forwards (URLs).
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
//      name: filter
//      description: Collection filter
//      type: string
//      example: default
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
//            type: array
//            description: List of endpoints
//            items:
//              type: string
//            example: |-
//              [
//                "/1.0/networks/mybr0/forwards/192.0.2.1",
//                "/1.0/networks/mybr0/forwards/192.0.2.2"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/networks/{networkName}/forwards?recursion=1 network-forwards network_forward_get_recursion1
//
//  Get the network address forwards
//
//  Returns a list of network address forwards (structs).
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
//      name: filter
//      description: Collection filter
//      type: string
//      example: default
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
//            type: array
//            description: List of network address forwards
//            items:
//              $ref: "#/definitions/NetworkForward"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

func networkForwardsGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, reqProject, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	networkName, err := url.PathUnescape(r.PathValue("networkName"))
	if err != nil {
		return response.SmartError(err)
	}

	n, err := network.LoadByName(s, projectName, networkName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading network: %w", err))
	}

	// Check if project allows access to network.
	if !project.NetworkAllowed(reqProject.Config, networkName, n.IsManaged()) {
		return response.SmartError(api.StatusErrorf(http.StatusNotFound, "Network not found"))
	}

	if !n.Info().AddressForwards {
		return response.BadRequest(fmt.Errorf("Network driver %q does not support forwards", n.Type()))
	}

	recursion := localUtil.IsRecursionRequest(r)

	// Parse filter value.
	filterStr := r.FormValue("filter")
	clauses, err := filter.Parse(filterStr, filter.QueryOperatorSet())
	if err != nil {
		return response.BadRequest(fmt.Errorf("Invalid filter: %w", err))
	}

	mustLoadObjects := recursion || (clauses != nil && len(clauses.Clauses) > 0)

	linkResults := make([]string, 0)
	fullResults := make([]api.NetworkForward, 0)

	if mustLoadObjects {
		var records map[int64]*api.NetworkForward

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			networkID := n.ID()
			dbRecords, err := dbCluster.GetNetworkForwards(ctx, tx.Tx(), dbCluster.NetworkForwardFilter{
				NetworkID: &networkID,
			})
			if err != nil {
				return err
			}

			records = make(map[int64]*api.NetworkForward)
			for _, dbRecord := range dbRecords {
				forward, err := dbRecord.ToAPI(ctx, tx.Tx())
				if err != nil {
					return err
				}

				records[dbRecord.ID] = forward
			}

			return err
		})
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed loading network forwards: %w", err))
		}

		for _, record := range records {

			if clauses != nil && len(clauses.Clauses) > 0 {
				match, err := filter.Match(*record, *clauses)
				if err != nil {
					return response.SmartError(err)
				}

				if !match {
					continue
				}
			}

			fullResults = append(fullResults, *record)
			linkResults = append(linkResults, fmt.Sprintf("/%s/networks/%s/forwards/%s", version.APIVersion, url.PathEscape(n.Name()), url.PathEscape(record.ListenAddress)))
		}
	} else {
		var listenAddresses map[int64]string

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			networkID := n.ID()
			dbRecords, err := dbCluster.GetNetworkForwards(ctx, tx.Tx(), dbCluster.NetworkForwardFilter{
				NetworkID: &networkID,
			})
			if err != nil {
				return err
			}

			listenAddresses = make(map[int64]string)
			for _, dbRecord := range dbRecords {
				listenAddresses[dbRecord.ID] = dbRecord.ListenAddress
			}

			return err
		})
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed loading network forwards: %w", err))
		}

		for _, listenAddress := range listenAddresses {
			linkResults = append(linkResults, fmt.Sprintf("/%s/networks/%s/forwards/%s", version.APIVersion, url.PathEscape(n.Name()), url.PathEscape(listenAddress)))
		}
	}

	if recursion {
		return response.SyncResponse(true, fullResults)
	}

	return response.SyncResponse(true, linkResults)
}

// swagger:operation POST /1.0/networks/{networkName}/forwards network-forwards network_forwards_post
//
//	Add a network address forward
//
//	Creates a new network address forward.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: forward
//	    description: Forward
//	    required: true
//	    schema:
//	      $ref: "#/definitions/NetworkForwardsPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkForwardsPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	projectName, reqProject, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	// Parse the request into a record.
	req := api.NetworkForwardsPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	req.Normalise() // So we handle the request in normalised/canonical form.

	networkName, err := url.PathUnescape(r.PathValue("networkName"))
	if err != nil {
		return response.SmartError(err)
	}

	n, err := network.LoadByName(s, projectName, networkName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading network: %w", err))
	}

	// Check if project allows access to network.
	if !project.NetworkAllowed(reqProject.Config, networkName, n.IsManaged()) {
		return response.SmartError(api.StatusErrorf(http.StatusNotFound, "Network not found"))
	}

	if !n.Info().AddressForwards {
		return response.BadRequest(fmt.Errorf("Network driver %q does not support forwards", n.Type()))
	}

	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))

	err = n.ForwardCreate(req, clientType)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed creating forward: %w", err))
	}

	lc := lifecycle.NetworkForwardCreated.Event(n, req.ListenAddress, request.CreateRequestor(r), nil)
	s.Events.SendLifecycle(projectName, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation DELETE /1.0/networks/{networkName}/forwards/{listenAddress} network-forwards network_forward_delete
//
//	Delete the network address forward
//
//	Removes the network address forward.
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
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkForwardDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	projectName, reqProject, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	networkName, err := url.PathUnescape(r.PathValue("networkName"))
	if err != nil {
		return response.SmartError(err)
	}

	n, err := network.LoadByName(s, projectName, networkName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading network: %w", err))
	}

	// Check if project allows access to network.
	if !project.NetworkAllowed(reqProject.Config, networkName, n.IsManaged()) {
		return response.SmartError(api.StatusErrorf(http.StatusNotFound, "Network not found"))
	}

	if !n.Info().AddressForwards {
		return response.BadRequest(fmt.Errorf("Network driver %q does not support forwards", n.Type()))
	}

	listenAddress, err := url.PathUnescape(r.PathValue("listenAddress"))
	if err != nil {
		return response.SmartError(err)
	}

	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))

	err = n.ForwardDelete(listenAddress, clientType)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed deleting forward: %w", err))
	}

	s.Events.SendLifecycle(projectName, lifecycle.NetworkForwardDeleted.Event(n, listenAddress, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/networks/{networkName}/forwards/{listenAddress} network-forwards network_forward_get
//
//	Get the network address forward
//
//	Gets a specific network address forward.
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
//	responses:
//	  "200":
//	    description: Address forward
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
//	          $ref: "#/definitions/NetworkForward"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkForwardGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	projectName, reqProject, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	networkName, err := url.PathUnescape(r.PathValue("networkName"))
	if err != nil {
		return response.SmartError(err)
	}

	n, err := network.LoadByName(s, projectName, networkName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading network: %w", err))
	}

	// Check if project allows access to network.
	if !project.NetworkAllowed(reqProject.Config, networkName, n.IsManaged()) {
		return response.SmartError(api.StatusErrorf(http.StatusNotFound, "Network not found"))
	}

	if !n.Info().AddressForwards {
		return response.BadRequest(fmt.Errorf("Network driver %q does not support forwards", n.Type()))
	}

	listenAddress, err := url.PathUnescape(r.PathValue("listenAddress"))
	if err != nil {
		return response.SmartError(err)
	}

	targetMember := request.QueryParam(r, "target")
	memberSpecific := targetMember != ""

	var forward *api.NetworkForward

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		networkID := n.ID()
		dbRecords, err := dbCluster.GetNetworkForwards(ctx, tx.Tx(), dbCluster.NetworkForwardFilter{
			NetworkID:     &networkID,
			ListenAddress: &listenAddress,
		})
		if err != nil {
			return err
		}

		filteredRecords := make([]dbCluster.NetworkForward, 0, len(dbRecords))
		for _, dbRecord := range dbRecords {
			// Include all records if memberSpecific is turned off
			// Otherwise, filter based offed of dbRecords with same node id
			if !memberSpecific || (!dbRecord.NodeID.Valid || (dbRecord.NodeID.Int64 == tx.GetNodeID())) {
				filteredRecords = append(filteredRecords, dbRecord)
			}
		}

		if len(filteredRecords) == 0 {
			return api.StatusErrorf(http.StatusNotFound, "Network forward not found")
		}

		if len(filteredRecords) > 1 {
			return api.StatusErrorf(http.StatusConflict, "Network forward found on more than one cluster member. Please target a specific member")
		}

		dbNetworkForward := filteredRecords[0]
		forward, err = dbNetworkForward.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseETag(true, forward, forward.Etag())
}

// swagger:operation PATCH /1.0/networks/{networkName}/forwards/{listenAddress} network-forwards network_forward_patch
//
//  Partially update the network address forward
//
//  Updates a subset of the network address forward configuration.
//
//  ---
//  consumes:
//    - application/json
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
//    - in: body
//      name: forward
//      description: Address forward configuration
//      required: true
//      schema:
//        $ref: "#/definitions/NetworkForwardPut"
//  responses:
//    "200":
//      $ref: "#/responses/EmptySyncResponse"
//    "400":
//      $ref: "#/responses/BadRequest"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "412":
//      $ref: "#/responses/PreconditionFailed"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation PUT /1.0/networks/{networkName}/forwards/{listenAddress} network-forwards network_forward_put
//
//	Update the network address forward
//
//	Updates the entire network address forward configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: forward
//	    description: Address forward configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/NetworkForwardPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkForwardPut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	projectName, reqProject, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	networkName, err := url.PathUnescape(r.PathValue("networkName"))
	if err != nil {
		return response.SmartError(err)
	}

	n, err := network.LoadByName(s, projectName, networkName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading network: %w", err))
	}

	// Check if project allows access to network.
	if !project.NetworkAllowed(reqProject.Config, networkName, n.IsManaged()) {
		return response.SmartError(api.StatusErrorf(http.StatusNotFound, "Network not found"))
	}

	if !n.Info().AddressForwards {
		return response.BadRequest(fmt.Errorf("Network driver %q does not support forwards", n.Type()))
	}

	listenAddress, err := url.PathUnescape(r.PathValue("listenAddress"))
	if err != nil {
		return response.SmartError(err)
	}

	// Decode the request.
	req := api.NetworkForwardPut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	targetMember := request.QueryParam(r, "target")
	memberSpecific := targetMember != ""

	if r.Method == http.MethodPatch {
		var forward *api.NetworkForward

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			networkID := n.ID()
			dbRecords, err := dbCluster.GetNetworkForwards(ctx, tx.Tx(), dbCluster.NetworkForwardFilter{
				NetworkID:     &networkID,
				ListenAddress: &listenAddress,
			})
			if err != nil {
				return err
			}

			filteredRecords := make([]dbCluster.NetworkForward, 0, len(dbRecords))
			for _, dbRecord := range dbRecords {
				// Include all records if memberSpecific is turned off
				// Otherwise, filter based offed of dbRecords with same node id
				if !memberSpecific || (!dbRecord.NodeID.Valid || (dbRecord.NodeID.Int64 == tx.GetNodeID())) {
					filteredRecords = append(filteredRecords, dbRecord)
				}
			}

			if len(filteredRecords) == 0 {
				return api.StatusErrorf(http.StatusNotFound, "Network forward not found")
			}

			if len(filteredRecords) > 1 {
				return api.StatusErrorf(http.StatusConflict, "Network forward found on more than one cluster member. Please target a specific member")
			}

			dbNetworkForward := filteredRecords[0]
			forward, err = dbNetworkForward.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return response.SmartError(err)
		}

		// If config being updated via "patch" method, then merge all existing config with the keys that
		// are present in the request config.
		for k, v := range forward.Config {
			_, ok := req.Config[k]
			if !ok {
				req.Config[k] = v
			}
		}

		// If forward being updated via "patch" method and ports not specified, then merge existing ports
		// into forward.
		if req.Ports == nil {
			req.Ports = forward.Ports
		}
	}

	req.Normalise() // So we handle the request in normalised/canonical form.

	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))

	err = n.ForwardUpdate(listenAddress, req, clientType)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed updating forward: %w", err))
	}

	s.Events.SendLifecycle(projectName, lifecycle.NetworkForwardUpdated.Event(n, listenAddress, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}
