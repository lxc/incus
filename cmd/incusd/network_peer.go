package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	"github.com/lxc/incus/internal/server/auth"
	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/lifecycle"
	"github.com/lxc/incus/internal/server/network"
	"github.com/lxc/incus/internal/server/project"
	"github.com/lxc/incus/internal/server/request"
	"github.com/lxc/incus/internal/server/response"
	localUtil "github.com/lxc/incus/internal/server/util"
	"github.com/lxc/incus/internal/version"
	"github.com/lxc/incus/shared/api"
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
//	Get the network peers
//
//	Returns a list of network peers (structs).
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
//	          description: List of network peers
//	          items:
//	            $ref: "#/definitions/NetworkPeer"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
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

	if localUtil.IsRecursionRequest(r) {
		var records map[int64]*api.NetworkPeer

		err := s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			records, err = tx.GetNetworkPeers(ctx, n.ID())

			return err
		})
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed loading network peers: %w", err))
		}

		peers := make([]*api.NetworkPeer, 0, len(records))
		for _, record := range records {
			record.UsedBy, _ = n.PeerUsedBy(record.Name)
			peers = append(peers, record)
		}

		return response.SyncResponse(true, peers)
	}

	var peerNames map[int64]string

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		peerNames, err = tx.GetNetworkPeerNames(ctx, n.ID())

		return err
	})
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed loading network peers: %w", err))
	}

	peerURLs := make([]string, 0, len(peerNames))
	for _, peerName := range peerNames {
		peerURLs = append(peerURLs, fmt.Sprintf("/%s/networks/%s/peers/%s", version.APIVersion, url.PathEscape(n.Name()), url.PathEscape(peerName)))
	}

	return response.SyncResponse(true, peerURLs)
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
		_, peer, err = tx.GetNetworkPeer(ctx, n.ID(), peerName)

		return err
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
