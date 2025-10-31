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

var networkLoadBalancersCmd = APIEndpoint{
	Path: "networks/{networkName}/load-balancers",

	Get:  APIEndpointAction{Handler: networkLoadBalancersGet, AccessHandler: allowPermission(auth.ObjectTypeNetwork, auth.EntitlementCanView, "networkName")},
	Post: APIEndpointAction{Handler: networkLoadBalancersPost, AccessHandler: allowPermission(auth.ObjectTypeNetwork, auth.EntitlementCanEdit, "networkName")},
}

var networkLoadBalancerCmd = APIEndpoint{
	Path: "networks/{networkName}/load-balancers/{listenAddress}",

	Delete: APIEndpointAction{Handler: networkLoadBalancerDelete, AccessHandler: allowPermission(auth.ObjectTypeNetwork, auth.EntitlementCanEdit, "networkName")},
	Get:    APIEndpointAction{Handler: networkLoadBalancerGet, AccessHandler: allowPermission(auth.ObjectTypeNetwork, auth.EntitlementCanView, "networkName")},
	Put:    APIEndpointAction{Handler: networkLoadBalancerPut, AccessHandler: allowPermission(auth.ObjectTypeNetwork, auth.EntitlementCanEdit, "networkName")},
	Patch:  APIEndpointAction{Handler: networkLoadBalancerPut, AccessHandler: allowPermission(auth.ObjectTypeNetwork, auth.EntitlementCanEdit, "networkName")},
}

var networkLoadBalancerStateCmd = APIEndpoint{
	Path: "networks/{networkName}/load-balancers/{listenAddress}/state",

	Get: APIEndpointAction{Handler: networkLoadBalancerStateGet, AccessHandler: allowPermission(auth.ObjectTypeNetwork, auth.EntitlementCanView, "networkName")},
}

// API endpoints

// swagger:operation GET /1.0/networks/{networkName}/load-balancers network-load-balancers network_load_balancers_get
//
//  Get the network address of load balancers
//
//  Returns a list of network address load balancers (URLs).
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
//                "/1.0/networks/mybr0/load-balancers/192.0.2.1",
//                "/1.0/networks/mybr0/load-balancers/192.0.2.2"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/networks/{networkName}/load-balancers?recursion=1 network-load-balancers network_load_balancer_get_recursion1
//
//  Get the network address load balancers
//
//  Returns a list of network address load balancers (structs).
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
//            description: List of network address load balancers
//            items:
//              $ref: "#/definitions/NetworkLoadBalancer"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

func networkLoadBalancersGet(d *Daemon, r *http.Request) response.Response {
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

	if !n.Info().LoadBalancers {
		return response.BadRequest(fmt.Errorf("Network driver %q does not support load balancers", n.Type()))
	}

	recursion := localUtil.IsRecursionRequest(r)

	// Parse filter value.
	filterStr := r.FormValue("filter")
	clauses, err := filter.Parse(filterStr, filter.QueryOperatorSet())
	if err != nil {
		return response.BadRequest(fmt.Errorf("Invalid filter: %w", err))
	}

	mustLoadObjects := recursion || (clauses != nil && len(clauses.Clauses) > 0)

	fullResults := make([]api.NetworkLoadBalancer, 0)
	linkResults := make([]string, 0)

	if mustLoadObjects {
		var records map[int64]*api.NetworkLoadBalancer

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			networkID := n.ID()

			// Get the load balancers.
			dbLoadBalancers, err := dbCluster.GetNetworkLoadBalancers(ctx, tx.Tx(), dbCluster.NetworkLoadBalancerFilter{NetworkID: &networkID})
			if err != nil {
				return err
			}

			records = make(map[int64]*api.NetworkLoadBalancer)

			for _, lb := range dbLoadBalancers {
				// Get the full API record.
				apiLoadBalancer, err := lb.ToAPI(ctx, tx.Tx())
				if err != nil {
					return err
				}

				records[lb.ID] = apiLoadBalancer
			}

			return nil
		})
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed loading network load balancers: %w", err))
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
			u := api.NewURL().Path(version.APIVersion, "networks", n.Name(), "load-balancers", record.ListenAddress)
			linkResults = append(linkResults, u.String())
		}
	} else {
		listenAddresses := []string{}

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			networkID := n.ID()

			// Get the load balancers.
			dbLoadBalancers, err := dbCluster.GetNetworkLoadBalancers(ctx, tx.Tx(), dbCluster.NetworkLoadBalancerFilter{
				NetworkID: &networkID,
			})
			if err != nil {
				return fmt.Errorf("Failed loading network load balancers: %w", err)
			}

			for _, lb := range dbLoadBalancers {
				listenAddresses = append(listenAddresses, lb.ListenAddress)
			}

			return nil
		})
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed loading network load balancers: %w", err))
		}

		for _, listenAddress := range listenAddresses {
			u := api.NewURL().Path(version.APIVersion, "networks", n.Name(), "load-balancers", listenAddress)
			linkResults = append(linkResults, u.String())
		}
	}

	if recursion {
		return response.SyncResponse(true, fullResults)
	}

	return response.SyncResponse(true, linkResults)
}

// swagger:operation POST /1.0/networks/{networkName}/load-balancers network-load-balancers network_load_balancers_post
//
//	Add a network load balancer
//
//	Creates a new network load balancer.
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
//	    name: load-balancer
//	    description: Load Balancer
//	    required: true
//	    schema:
//	      $ref: "#/definitions/NetworkLoadBalancersPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkLoadBalancersPost(d *Daemon, r *http.Request) response.Response {
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
	req := api.NetworkLoadBalancersPost{}
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

	if !n.Info().LoadBalancers {
		return response.BadRequest(fmt.Errorf("Network driver %q does not support load balancers", n.Type()))
	}

	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))

	err = n.LoadBalancerCreate(req, clientType)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed creating load balancer: %w", err))
	}

	lc := lifecycle.NetworkLoadBalancerCreated.Event(n, req.ListenAddress, request.CreateRequestor(r), nil)
	s.Events.SendLifecycle(projectName, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation DELETE /1.0/networks/{networkName}/load-balancers/{listenAddress} network-load-balancers network_load_balancer_delete
//
//	Delete the network address load balancer
//
//	Removes the network address load balancer.
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
func networkLoadBalancerDelete(d *Daemon, r *http.Request) response.Response {
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

	if !n.Info().LoadBalancers {
		return response.BadRequest(fmt.Errorf("Network driver %q does not support load balancers", n.Type()))
	}

	listenAddress, err := url.PathUnescape(r.PathValue("listenAddress"))
	if err != nil {
		return response.SmartError(err)
	}

	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))

	err = n.LoadBalancerDelete(listenAddress, clientType)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed deleting load balancer: %w", err))
	}

	s.Events.SendLifecycle(projectName, lifecycle.NetworkLoadBalancerDeleted.Event(n, listenAddress, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/networks/{networkName}/load-balancers/{listenAddress} network-load-balancers network_load_balancer_get
//
//	Get the network address load balancer
//
//	Gets a specific network address load balancer.
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
//	    description: Load Balancer
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
//	          $ref: "#/definitions/NetworkLoadBalancer"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkLoadBalancerGet(d *Daemon, r *http.Request) response.Response {
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

	if !n.Info().LoadBalancers {
		return response.BadRequest(fmt.Errorf("Network driver %q does not support load balancers", n.Type()))
	}

	listenAddress, err := url.PathUnescape(r.PathValue("listenAddress"))
	if err != nil {
		return response.SmartError(err)
	}

	var loadBalancer *api.NetworkLoadBalancer
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		networkID := n.ID()

		// Get the load balancer.
		dbLoadBalancers, err := dbCluster.GetNetworkLoadBalancers(ctx, tx.Tx(), dbCluster.NetworkLoadBalancerFilter{
			NetworkID:     &networkID,
			ListenAddress: &listenAddress,
		})
		if err != nil {
			return err
		}

		if len(dbLoadBalancers) != 1 {
			return api.StatusErrorf(http.StatusNotFound, "Network load balancer not found")
		}

		// Get API struct.
		loadBalancer, err = dbLoadBalancers[0].ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseETag(true, loadBalancer, loadBalancer.Etag())
}

// swagger:operation PATCH /1.0/networks/{networkName}/load-balancers/{listenAddress} network-load-balancers network_load_balancer_patch
//
//  Partially update the network address load balancer
//
//  Updates a subset of the network address load balancer configuration.
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
//      name: load-balancer
//      description: Address load balancer configuration
//      required: true
//      schema:
//        $ref: "#/definitions/NetworkLoadBalancerPut"
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

// swagger:operation PUT /1.0/networks/{networkName}/load-balancers/{listenAddress} network-load-balancers network_load_balancer_put
//
//	Update the network address load balancer
//
//	Updates the entire network address load balancer configuration.
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
//	    name: load-balancer
//	    description: Address load balancer configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/NetworkLoadBalancerPut"
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
func networkLoadBalancerPut(d *Daemon, r *http.Request) response.Response {
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

	if !n.Info().LoadBalancers {
		return response.BadRequest(fmt.Errorf("Network driver %q does not support load balancers", n.Type()))
	}

	listenAddress, err := url.PathUnescape(r.PathValue("listenAddress"))
	if err != nil {
		return response.SmartError(err)
	}

	// Decode the request.
	req := api.NetworkLoadBalancerPut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if r.Method == http.MethodPatch {
		var loadBalancer *api.NetworkLoadBalancer

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			networkID := n.ID()

			// Get the load balancer.
			dbLoadBalancers, err := dbCluster.GetNetworkLoadBalancers(ctx, tx.Tx(), dbCluster.NetworkLoadBalancerFilter{
				NetworkID:     &networkID,
				ListenAddress: &listenAddress,
			})
			if err != nil {
				return err
			}

			if len(dbLoadBalancers) != 1 {
				return api.StatusErrorf(http.StatusNotFound, "Network load balancer not found")
			}

			// Get the API struct.
			loadBalancer, err = dbLoadBalancers[0].ToAPI(ctx, tx.Tx())
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
		for k, v := range loadBalancer.Config {
			_, ok := req.Config[k]
			if !ok {
				req.Config[k] = v
			}
		}

		// If load balancer being updated via "patch" method and backends not specified, then merge
		// existing backends into load balancer.
		if req.Backends == nil {
			req.Backends = loadBalancer.Backends
		}

		// If load balancer being updated via "patch" method and ports not specified, then merge existing
		// ports into load balancer.
		if req.Ports == nil {
			req.Ports = loadBalancer.Ports
		}
	}

	req.Normalise() // So we handle the request in normalised/canonical form.

	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))

	err = n.LoadBalancerUpdate(listenAddress, req, clientType)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed updating load balancer: %w", err))
	}

	s.Events.SendLifecycle(projectName, lifecycle.NetworkLoadBalancerUpdated.Event(n, listenAddress, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/networks/{networkName}/load-balancers/{listenAddress}/state network-load-balancers network_load_balancer_state_get
//
//	Get the network address load balancer state
//
//	Get the current state of a specific network address load balancer.
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
//	    description: Load Balancer state
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
//	          $ref: "#/definitions/NetworkLoadBalancerState"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkLoadBalancerStateGet(d *Daemon, r *http.Request) response.Response {
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

	if !n.Info().LoadBalancers {
		return response.BadRequest(fmt.Errorf("Network driver %q does not support load balancers", n.Type()))
	}

	listenAddress, err := url.PathUnescape(r.PathValue("listenAddress"))
	if err != nil {
		return response.SmartError(err)
	}

	var loadBalancer *api.NetworkLoadBalancer
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		networkID := n.ID()

		// Get the load balancers.
		dbLoadBalancers, err := dbCluster.GetNetworkLoadBalancers(ctx, tx.Tx(), dbCluster.NetworkLoadBalancerFilter{
			NetworkID:     &networkID,
			ListenAddress: &listenAddress,
		})
		if err != nil {
			return err
		}

		if len(dbLoadBalancers) != 1 {
			return api.StatusErrorf(http.StatusNotFound, "Network load balancer not found")
		}

		// Get the API struct.
		loadBalancer, err = dbLoadBalancers[0].ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	lbState, err := n.LoadBalancerState(*loadBalancer)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed fetching load balancer state: %w", err))
	}

	return response.SyncResponse(true, lbState)
}
