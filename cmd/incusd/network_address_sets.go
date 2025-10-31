package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/lxc/incus/v6/internal/filter"
	"github.com/lxc/incus/v6/internal/server/auth"
	clusterRequest "github.com/lxc/incus/v6/internal/server/cluster/request"
	"github.com/lxc/incus/v6/internal/server/db"
	dbCluster "github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/lifecycle"
	addressset "github.com/lxc/incus/v6/internal/server/network/address-set"
	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/internal/server/request"
	"github.com/lxc/incus/v6/internal/server/response"
	localUtil "github.com/lxc/incus/v6/internal/server/util"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/util"
)

var networkAddressSetsCmd = APIEndpoint{
	Path: "network-address-sets",

	Get:  APIEndpointAction{Handler: networkAddressSetsGet, AccessHandler: allowAuthenticated},
	Post: APIEndpointAction{Handler: networkAddressSetsPost, AccessHandler: allowPermission(auth.ObjectTypeProject, auth.EntitlementCanCreateNetworkAddressSets)},
}

var networkAddressSetCmd = APIEndpoint{
	Path: "network-address-sets/{name}",

	Delete: APIEndpointAction{Handler: networkAddressSetDelete, AccessHandler: allowPermission(auth.ObjectTypeNetworkAddressSet, auth.EntitlementCanEdit, "name")},
	Get:    APIEndpointAction{Handler: networkAddressSetGet, AccessHandler: allowPermission(auth.ObjectTypeNetworkAddressSet, auth.EntitlementCanView, "name")},
	Put:    APIEndpointAction{Handler: networkAddressSetPut, AccessHandler: allowPermission(auth.ObjectTypeNetworkAddressSet, auth.EntitlementCanEdit, "name")},
	Patch:  APIEndpointAction{Handler: networkAddressSetPut, AccessHandler: allowPermission(auth.ObjectTypeNetworkAddressSet, auth.EntitlementCanEdit, "name")},
	Post:   APIEndpointAction{Handler: networkAddressSetPost, AccessHandler: allowPermission(auth.ObjectTypeNetworkAddressSet, auth.EntitlementCanEdit, "name")},
}

// API endpoints.

// swagger:operation GET /1.0/network-address-sets network-address-sets network_address_sets_get
//
// Get the network address sets
//
// Returns a list of network address sets (URLs).
//
// ---
// produces:
//   - application/json
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: query
//     name: all-projects
//     description: Retrieve network address sets from all projects
//     type: boolean
//     example: true
//   - in: query
//     name: filter
//     description: Collection filter
//     type: string
//     example: default
// responses:
//   "200":
//     description: API endpoints
//     schema:
//       type: object
//       description: Sync response
//       properties:
//         type:
//           type: string
//           description: Response type
//           example: sync
//         status:
//           type: string
//           description: Status description
//           example: Success
//         status_code:
//           type: integer
//           description: Status code
//           example: 200
//         metadata:
//           type: array
//           description: List of endpoints
//           items:
//             type: string
//           example: |-
//             [
//               "/1.0/network-address-sets/foo",
//               "/1.0/network-address-sets/bar"
//             ]
//   "403":
//     $ref: "#/responses/Forbidden"
//   "500":
//     $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/network-address-sets?recursion=1 network-address-sets network_address_sets_get_recursion1
//
// # Get the network address sets
//
// Returns a list of network address sets (structs).
//
// ---
// produces:
//   - application/json
//
// parameters:
//   - in: query
//     name: project
//     description: Project name
//     type: string
//     example: default
//   - in: query
//     name: all-projects
//     description: Retrieve network address sets from all projects
//     type: boolean
//     example: true
//   - in: query
//     name: filter
//     description: Collection filter
//     type: string
//     example: default
//
// responses:
//
//  "200":
//    description: API endpoints
//    schema:
//      type: object
//      description: Sync response
//      properties:
//        type:
//          type: string
//          description: Response type
//          example: sync
//        status:
//          type: string
//          description: Status description
//          example: Success
//        status_code:
//          type: integer
//          description: Status code
//          example: 200
//        metadata:
//          type: array
//          description: List of network address sets
//          items:
//            $ref: "#/definitions/NetworkAddressSet"
//  "403":
//    $ref: "#/responses/Forbidden"
//  "500":
//    $ref: "#/responses/InternalServerError"

func networkAddressSetsGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	recursion := localUtil.IsRecursionRequest(r)
	allProjects := util.IsTrue(r.FormValue("all-projects"))

	filterStr := r.FormValue("filter")
	clauses, err := filter.Parse(filterStr, filter.QueryOperatorSet())
	if err != nil {
		return response.BadRequest(fmt.Errorf("Invalid filter: %w", err))
	}

	mustLoadObjects := recursion || (clauses != nil && len(clauses.Clauses) > 0)

	var addrSets []dbCluster.NetworkAddressSet
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		if allProjects {
			addrSets, err = dbCluster.GetNetworkAddressSets(ctx, tx.Tx())
			if err != nil {
				return err
			}
		} else {
			addrSets, err = dbCluster.GetNetworkAddressSets(ctx, tx.Tx(), dbCluster.NetworkAddressSetFilter{Project: &projectName})
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return response.InternalError(err)
	}

	userHasPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), r, auth.EntitlementCanView, auth.ObjectTypeNetworkAddressSet)
	if err != nil {
		return response.SmartError(err)
	}

	linkResults := make([]string, 0)
	fullResults := make([]api.NetworkAddressSet, 0)

	for _, addrSet := range addrSets {
		if !userHasPermission(auth.ObjectNetworkAddressSet(addrSet.Project, addrSet.Name)) {
			continue
		}

		if mustLoadObjects {
			netAddressSet, err := addressset.LoadByName(s, addrSet.Project, addrSet.Name)
			if err != nil {
				continue
			}

			netAddressSetInfo := netAddressSet.Info()
			netAddressSetInfo.UsedBy, _ = netAddressSet.UsedBy() // Ignore errors in UsedBy, will return nil.
			if clauses != nil && len(clauses.Clauses) > 0 {
				match, err := filter.Match(*netAddressSetInfo, *clauses)
				if err != nil {
					return response.SmartError(err)
				}

				if !match {
					continue
				}
			}

			fullResults = append(fullResults, *netAddressSetInfo)
		}

		linkResults = append(linkResults, fmt.Sprintf("/%s/network-address-sets/%s", version.APIVersion, addrSet.Name))
	}

	if !recursion {
		return response.SyncResponse(true, linkResults)
	}

	return response.SyncResponse(true, fullResults)
}

// swagger:operation POST /1.0/network-address-sets network-address-sets network_address_sets_post
//
//  Add a network address set
//
//  Creates a new network address set.
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
//      name: address set
//      description: address set
//      required: true
//      schema:
//        $ref: "#/definitions/NetworkAddressSetsPost"
//  responses:
//    "200":
//      $ref: "#/responses/EmptySyncResponse"
//    "400":
//      $ref: "#/responses/BadRequest"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

func networkAddressSetsPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	req := api.NetworkAddressSetsPost{}

	// Parse the request into a record.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	_, err = addressset.LoadByName(s, projectName, req.Name)
	if err == nil {
		return response.BadRequest(errors.New("The network address set already exists"))
	}

	err = addressset.Create(s, projectName, &req)
	if err != nil {
		return response.SmartError(err)
	}

	netAddrSet, err := addressset.LoadByName(s, projectName, req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	err = s.Authorizer.AddNetworkAddressSet(r.Context(), projectName, req.Name)
	if err != nil {
		logger.Error("Failed to add network address set to authorizer", logger.Ctx{"name": req.Name, "project": projectName, "error": err})
	}

	lc := lifecycle.NetworkAddressSetCreated.Event(netAddrSet, request.CreateRequestor(r), nil)
	s.Events.SendLifecycle(projectName, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation DELETE /1.0/network-address-sets/{name} network-address-sets network_address_set_delete
//
//  Delete the network address set
//
//  Removes the network address set.
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
//      $ref: "#/responses/EmptySyncResponse"
//    "400":
//      $ref: "#/responses/BadRequest"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

func networkAddressSetDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	addrSetName, err := url.PathUnescape(r.PathValue("name"))
	if err != nil {
		return response.SmartError(err)
	}

	netAddrSet, err := addressset.LoadByName(s, projectName, addrSetName)
	if err != nil {
		return response.SmartError(err)
	}

	err = netAddrSet.Delete()
	if err != nil {
		return response.SmartError(err)
	}

	err = s.Authorizer.DeleteNetworkAddressSet(r.Context(), projectName, addrSetName)
	if err != nil {
		logger.Error("Failed to remove network address set from authorizer", logger.Ctx{"name": addrSetName, "project": projectName, "error": err})
	}

	s.Events.SendLifecycle(projectName, lifecycle.NetworkAddressSetDeleted.Event(netAddrSet, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/network-address-sets/{name} network-address-sets network_address_set_get
//
//  Get the network address set
//
//  Gets a specific network address set.
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
//      description: address set
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
//            $ref: "#/definitions/NetworkAddressSet"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

func networkAddressSetGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	addrSetName, err := url.PathUnescape(r.PathValue("name"))
	if err != nil {
		return response.SmartError(err)
	}

	netAddrSet, err := addressset.LoadByName(s, projectName, addrSetName)
	if err != nil {
		return response.SmartError(err)
	}

	info := netAddrSet.Info()
	info.UsedBy, err = netAddrSet.UsedBy()
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseETag(true, info, netAddrSet.Etag())
}

// swagger:operation PATCH /1.0/network-address-sets/{name} network-address-sets network_address_set_patch
//
//  Partially update the network address set
//
//  Updates a subset of the network address set configuration.
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
//      name: address set
//      description: Address set configuration
//      required: true
//      schema:
//        $ref: "#/definitions/NetworkAddressSetPut"
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

// swagger:operation PUT /1.0/network-address-sets/{name} network-address-sets network_address_set_put
//
//  Update the network address set
//
//  Updates the entire network address set configuration.
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
//      name: address set
//      description: Address set configuration
//      required: true
//      schema:
//        $ref: "#/definitions/NetworkAddressSetPut"
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

func networkAddressSetPut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	addrSetName, err := url.PathUnescape(r.PathValue("name"))
	if err != nil {
		return response.SmartError(err)
	}

	// Get the existing network address set.
	netAddrSet, err := addressset.LoadByName(s, projectName, addrSetName)
	if err != nil {
		return response.SmartError(err)
	}

	// Validate ETag.
	err = localUtil.EtagCheck(r, netAddrSet.Etag())
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.NetworkAddressSetPut{}

	// Decode the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if r.Method == http.MethodPatch {
		for k, v := range netAddrSet.Info().Config {
			_, ok := req.Config[k]
			if !ok {
				req.Config[k] = v
			}
		}
	}

	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))

	err = netAddrSet.Update(&req, clientType)
	if err != nil {
		return response.SmartError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.NetworkAddressSetUpdated.Event(netAddrSet, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

// swagger:operation POST /1.0/network-address-sets/{name} network-address-sets network_address_set_post
//
//  Rename the network address set
//
//  Renames an existing network address set.
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
//      name: address set
//      description: Address set rename request
//      required: true
//      schema:
//        $ref: "#/definitions/NetworkAddressSetPost"
//  responses:
//    "200":
//      $ref: "#/responses/EmptySyncResponse"
//    "400":
//      $ref: "#/responses/BadRequest"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

func networkAddressSetPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := project.NetworkProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	addrSetName, err := url.PathUnescape(r.PathValue("name"))
	if err != nil {
		return response.SmartError(err)
	}

	// Parse the request.
	req := api.NetworkAddressSetPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get the existing network address set.
	netAddrSet, err := addressset.LoadByName(s, projectName, addrSetName)
	if err != nil {
		return response.SmartError(err)
	}

	oldName := addrSetName
	err = netAddrSet.Rename(req.Name)
	if err != nil {
		return response.SmartError(err)
	}

	err = s.Authorizer.RenameNetworkAddressSet(r.Context(), projectName, oldName, req.Name)
	if err != nil {
		logger.Error("Failed to rename network address set in authorizer", logger.Ctx{"old_name": oldName, "new_name": req.Name, "project": projectName, "error": err})
	}

	lc := lifecycle.NetworkAddressSetRenamed.Event(netAddrSet, request.CreateRequestor(r), logger.Ctx{"old_name": oldName})
	s.Events.SendLifecycle(projectName, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}
