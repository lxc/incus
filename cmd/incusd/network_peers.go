package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	"github.com/lxc/incus/v6/internal/filter"
	"github.com/lxc/incus/v6/internal/server/auth"
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
	"github.com/lxc/incus/v6/shared/validate"
)

var networkPeersCmd = APIEndpoint{
	Path: "networks/{networkName}/peers",

	Get:  APIEndpointAction{Handler: networkPeersGet, AccessHandler: allowPermission(auth.ObjectTypeNetwork, auth.EntitlementCanView, "networkName")},
	Post: APIEndpointAction{Handler: networkPeersPost, AccessHandler: allowPermission(auth.ObjectTypeNetwork, auth.EntitlementCanEdit, "networkName")},
}

var networkPeerCmd = APIEndpoint{
	Path: "networks/{networkName}/peers/{peerName}",

	Delete: APIEndpointAction{Handler: networkPeerDelete, AccessHandler: allowPermission(auth.ObjectTypeNetwork, auth.EntitlementCanEdit, "networkName")},
	Get:    APIEndpointAction{Handler: networkPeerGet, AccessHandler: allowPermission(auth.ObjectTypeNetwork, auth.EntitlementCanView, "networkName")},
	Put:    APIEndpointAction{Handler: networkPeerPut, AccessHandler: allowPermission(auth.ObjectTypeNetwork, auth.EntitlementCanEdit, "networkName")},
	Patch:  APIEndpointAction{Handler: networkPeerPut, AccessHandler: allowPermission(auth.ObjectTypeNetwork, auth.EntitlementCanEdit, "networkName")},
}

// API endpoints

// swagger:operation GET /1.0/networks/{networkName}/peers network-peers network_peers_get
//
//  Get the network peers
//
//  Returns a list of network peers (URLs).
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
//                "/1.0/networks/mybr0/peers/my-peer-1",
//                "/1.0/networks/mybr0/peers/my-peer-2"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/networks/{networkName}/peers?recursion=1 network-peers network_peer_get_recursion1
//
//  Get the network peers
//
//  Returns a list of network peers (structs).
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
//            description: List of network peers
//            items:
//              $ref: "#/definitions/NetworkPeer"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

func networkPeersGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, reqProject, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	networkName, err := url.PathUnescape(mux.Vars(r)["networkName"])
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

	if !n.Info().Peering {
		return response.BadRequest(fmt.Errorf("Network driver %q does not support peering", n.Type()))
	}

	recursion := localUtil.IsRecursionRequest(r)

	// Parse filter value.
	filterStr := r.FormValue("filter")
	clauses, err := filter.Parse(filterStr, filter.QueryOperatorSet())
	if err != nil {
		return response.BadRequest(fmt.Errorf("Invalid filter: %w", err))
	}

	mustLoadObjects := recursion || (clauses != nil && len(clauses.Clauses) > 0)

	fullResults := make([]api.NetworkPeer, 0)
	linkResults := make([]string, 0)

	if mustLoadObjects {
		var peers map[int64]*api.NetworkPeer

		err := s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Use generated function to get peers.
			netID := n.ID()
			filter := dbCluster.NetworkPeerFilter{NetworkID: &netID}
			dbPeers, err := dbCluster.GetNetworkPeers(ctx, tx.Tx(), filter)
			if err != nil {
				return fmt.Errorf("Failed loading network peer DB objects: %w", err)
			}

			// Convert DB objects to API objects and build the map.
			peers = make(map[int64]*api.NetworkPeer, len(dbPeers))
			for _, dbPeer := range dbPeers {
				peer, err := dbPeer.ToAPI(ctx, tx.Tx())
				if err != nil {
					// Use fmt.Errorf as requested, though logging might be preferable in some contexts.
					return fmt.Errorf("Failed converting network peer DB object to API object for peer ID %d: %w", dbPeer.ID, err)
				}

				peers[dbPeer.ID] = peer
			}

			return nil
		})
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed loading network peers: %w", err))
		}

		for _, peer := range peers {
			peer.UsedBy, _ = n.PeerUsedBy(peer.Name)

			if clauses != nil && len(clauses.Clauses) > 0 {
				match, err := filter.Match(*peer, *clauses)
				if err != nil {
					return response.SmartError(err)
				}

				if !match {
					continue
				}
			}

			fullResults = append(fullResults, *peer)
			linkResults = append(linkResults, fmt.Sprintf("/%s/networks/%s/peers/%s", version.APIVersion, url.PathEscape(n.Name()), url.PathEscape(peer.Name)))
		}
	} else {
		var peerNames map[int64]string

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Use the generated GetNetworkPeers function with a filter.
			netID := n.ID()
			filter := dbCluster.NetworkPeerFilter{NetworkID: &netID}
			peers, err := dbCluster.GetNetworkPeers(ctx, tx.Tx(), filter)
			if err != nil {
				return err
			}

			peerNames = make(map[int64]string, len(peers))
			for _, peer := range peers {
				peerNames[peer.ID] = peer.Name
			}

			return nil
		})
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed loading network peers: %w", err))
		}

		for _, peerName := range peerNames {
			linkResults = append(linkResults, fmt.Sprintf("/%s/networks/%s/peers/%s", version.APIVersion, url.PathEscape(n.Name()), url.PathEscape(peerName)))
		}
	}

	if recursion {
		return response.SyncResponse(true, fullResults)
	}

	return response.SyncResponse(true, linkResults)
}

// swagger:operation POST /1.0/networks/{networkName}/peers network-peers network_peers_post
//
//	Add a network peer
//
//	Initiates/creates a new network peering.
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
//	    name: peer
//	    description: Peer
//	    required: true
//	    schema:
//	      $ref: "#/definitions/NetworkPeersPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "202":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkPeersPost(d *Daemon, r *http.Request) response.Response {
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
	req := api.NetworkPeersPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	err = validate.IsAPIName(req.Name, false)
	if err != nil {
		return response.BadRequest(fmt.Errorf("Invalid network peer name: %w", err))
	}

	networkName, err := url.PathUnescape(mux.Vars(r)["networkName"])
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

	if !n.Info().Peering {
		return response.BadRequest(fmt.Errorf("Network driver %q does not support peering", n.Type()))
	}

	err = n.PeerCreate(req)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed creating peer: %w", err))
	}

	lc := lifecycle.NetworkPeerCreated.Event(n, req.Name, request.CreateRequestor(r), nil)
	s.Events.SendLifecycle(projectName, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation DELETE /1.0/networks/{networkName}/peers/{peerName} network-peers network_peer_delete
//
//	Delete the network peer
//
//	Removes the network peering.
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
func networkPeerDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	projectName, reqProject, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	networkName, err := url.PathUnescape(mux.Vars(r)["networkName"])
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

	if !n.Info().Peering {
		return response.BadRequest(fmt.Errorf("Network driver %q does not support peering", n.Type()))
	}

	peerName, err := url.PathUnescape(mux.Vars(r)["peerName"])
	if err != nil {
		return response.SmartError(err)
	}

	err = n.PeerDelete(peerName)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed deleting peer: %w", err))
	}

	s.Events.SendLifecycle(projectName, lifecycle.NetworkPeerDeleted.Event(n, peerName, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/networks/{networkName}/peers/{peerName} network-peers network_peer_get
//
//	Get the network peer
//
//	Gets a specific network peering.
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
//	    description: Peer
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
//	          $ref: "#/definitions/NetworkPeer"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkPeerGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	projectName, reqProject, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	networkName, err := url.PathUnescape(mux.Vars(r)["networkName"])
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

	if !n.Info().Peering {
		return response.BadRequest(fmt.Errorf("Network driver %q does not support peering", n.Type()))
	}

	peerName, err := url.PathUnescape(mux.Vars(r)["peerName"])
	if err != nil {
		return response.SmartError(err)
	}

	var peer *api.NetworkPeer

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		netID := n.ID()
		dbPeer, err := dbCluster.GetNetworkPeer(ctx, tx.Tx(), netID, peerName)
		if err != nil {
			return fmt.Errorf("Failed getting network peer DB object: %w", err)
		}

		peer, err = dbPeer.ToAPI(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed converting network peer DB object to API object: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	peer.UsedBy, _ = n.PeerUsedBy(peer.Name)

	return response.SyncResponseETag(true, peer, peer.Etag())
}

// swagger:operation PATCH /1.0/networks/{networkName}/peers/{peerName} network-peers network_peer_patch
//
//  Partially update the network peer
//
//  Updates a subset of the network peering configuration.
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
//      name: Peer
//      description: Peer configuration
//      required: true
//      schema:
//        $ref: "#/definitions/NetworkPeerPut"
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

// swagger:operation PUT /1.0/networks/{networkName}/peers/{peerName} network-peers network_peer_put
//
//	Update the network peer
//
//	Updates the entire network peering configuration.
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
//	    name: peer
//	    description: Peer configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/NetworkPeerPut"
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
func networkPeerPut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	projectName, reqProject, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	networkName, err := url.PathUnescape(mux.Vars(r)["networkName"])
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

	if !n.Info().Peering {
		return response.BadRequest(fmt.Errorf("Network driver %q does not support peering", n.Type()))
	}

	peerName, err := url.PathUnescape(mux.Vars(r)["peerName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Decode the request.
	req := api.NetworkPeerPut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = n.PeerUpdate(peerName, req)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed updating peer: %w", err))
	}

	s.Events.SendLifecycle(projectName, lifecycle.NetworkPeerUpdated.Event(n, peerName, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}
