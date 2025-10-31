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
	"github.com/lxc/incus/v6/internal/server/network/zone"
	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/internal/server/request"
	"github.com/lxc/incus/v6/internal/server/response"
	localUtil "github.com/lxc/incus/v6/internal/server/util"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/util"
)

var networkZonesCmd = APIEndpoint{
	Path: "network-zones",

	Get:  APIEndpointAction{Handler: networkZonesGet, AccessHandler: allowAuthenticated},
	Post: APIEndpointAction{Handler: networkZonesPost, AccessHandler: allowPermission(auth.ObjectTypeProject, auth.EntitlementCanCreateNetworkZones)},
}

var networkZoneCmd = APIEndpoint{
	Path: "network-zones/{zone}",

	Delete: APIEndpointAction{Handler: networkZoneDelete, AccessHandler: allowPermission(auth.ObjectTypeNetworkZone, auth.EntitlementCanEdit, "zone")},
	Get:    APIEndpointAction{Handler: networkZoneGet, AccessHandler: allowPermission(auth.ObjectTypeNetworkZone, auth.EntitlementCanView, "zone")},
	Put:    APIEndpointAction{Handler: networkZonePut, AccessHandler: allowPermission(auth.ObjectTypeNetworkZone, auth.EntitlementCanEdit, "zone")},
	Patch:  APIEndpointAction{Handler: networkZonePut, AccessHandler: allowPermission(auth.ObjectTypeNetworkZone, auth.EntitlementCanEdit, "zone")},
}

// API endpoints.

// swagger:operation GET /1.0/network-zones network-zones network_zones_get
//
//  Get the network zones
//
//  Returns a list of network zones (URLs).
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
//      description: Retrieve network zones from all projects
//      type: boolean
//      example: true
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
//                "/1.0/network-zones/example.net",
//                "/1.0/network-zones/example.com"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/network-zones?recursion=1 network-zones network_zones_get_recursion1
//
//  Get the network zones
//
//  Returns a list of network zones (structs).
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
//      description: Retrieve network zones from all projects
//      type: boolean
//      example: true
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
//            description: List of network zones
//            items:
//              $ref: "#/definitions/NetworkZone"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

func networkZonesGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := project.NetworkZoneProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	recursion := localUtil.IsRecursionRequest(r)

	// Parse filter value.
	filterStr := r.FormValue("filter")
	clauses, err := filter.Parse(filterStr, filter.QueryOperatorSet())
	if err != nil {
		return response.BadRequest(fmt.Errorf("Invalid filter: %w", err))
	}

	mustLoadObjects := recursion || (clauses != nil && len(clauses.Clauses) > 0)

	var zones []dbCluster.NetworkZone
	var zoneNamesMap map[string]string
	allProjects := util.IsTrue(request.QueryParam(r, "all-projects"))

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		if allProjects {
			zones, err = dbCluster.GetNetworkZones(ctx, tx.Tx())
			if err != nil {
				return err
			}

			zoneNamesMap = map[string]string{}
			for _, zone := range zones {
				zoneNamesMap[zone.Name] = zone.Project
			}
		} else {
			filter := dbCluster.NetworkZoneFilter{Project: &projectName}
			zones, err = dbCluster.GetNetworkZones(ctx, tx.Tx(), filter)
			if err != nil {
				return err
			}

			zoneNamesMap = map[string]string{}
			for _, zone := range zones {
				zoneNamesMap[zone.Name] = projectName
			}
		}

		return err
	})
	if err != nil {
		return response.InternalError(err)
	}

	userHasPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), r, auth.EntitlementCanView, auth.ObjectTypeNetworkZone)
	if err != nil {
		return response.InternalError(err)
	}

	linkResults := make([]string, 0)
	fullResults := make([]api.NetworkZone, 0)
	for zoneName, projectName := range zoneNamesMap {
		if !userHasPermission(auth.ObjectNetworkZone(projectName, zoneName)) {
			continue
		}

		if mustLoadObjects {
			netzone, err := zone.LoadByNameAndProject(s, projectName, zoneName)
			if err != nil {
				continue
			}

			netzoneInfo := netzone.Info()
			netzoneInfo.UsedBy, _ = netzone.UsedBy() // Ignore errors in UsedBy, will return nil.
			netzoneInfo.Project = projectName

			if clauses != nil && len(clauses.Clauses) > 0 {
				match, err := filter.Match(*netzoneInfo, *clauses)
				if err != nil {
					return response.SmartError(err)
				}

				if !match {
					continue
				}
			}

			fullResults = append(fullResults, *netzoneInfo)
		}

		linkResults = append(linkResults, api.NewURL().Path(version.APIVersion, "network-zones", zoneName).String())
	}

	if !recursion {
		return response.SyncResponse(true, linkResults)
	}

	return response.SyncResponse(true, fullResults)
}

// swagger:operation POST /1.0/network-zones network-zones network_zones_post
//
//	Add a network zone
//
//	Creates a new network zone.
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
//	    name: zone
//	    description: zone
//	    required: true
//	    schema:
//	      $ref: "#/definitions/NetworkZonesPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkZonesPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := project.NetworkZoneProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	req := api.NetworkZonesPost{}

	// Parse the request into a record.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Create the zone.
	err = zone.Exists(s, req.Name)
	if err == nil {
		return response.BadRequest(errors.New("The network zone already exists"))
	}

	err = zone.Create(s, projectName, &req)
	if err != nil {
		return response.SmartError(err)
	}

	netzone, err := zone.LoadByNameAndProject(s, projectName, req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	err = s.Authorizer.AddNetworkZone(r.Context(), projectName, req.Name)
	if err != nil {
		logger.Error("Failed to add network zone to authorizer", logger.Ctx{"name": req.Name, "project": projectName, "error": err})
	}

	lc := lifecycle.NetworkZoneCreated.Event(netzone, request.CreateRequestor(r), nil)
	s.Events.SendLifecycle(projectName, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation DELETE /1.0/network-zones/{zone} network-zones network_zone_delete
//
//	Delete the network zone
//
//	Removes the network zone.
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
func networkZoneDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := project.NetworkZoneProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	zoneName, err := url.PathUnescape(r.PathValue("zone"))
	if err != nil {
		return response.SmartError(err)
	}

	netzone, err := zone.LoadByNameAndProject(s, projectName, zoneName)
	if err != nil {
		return response.SmartError(err)
	}

	err = netzone.Delete()
	if err != nil {
		return response.SmartError(err)
	}

	err = s.Authorizer.DeleteNetworkZone(r.Context(), projectName, zoneName)
	if err != nil {
		logger.Error("Failed to remove network zone from authorizer", logger.Ctx{"name": zoneName, "project": projectName, "error": err})
	}

	s.Events.SendLifecycle(projectName, lifecycle.NetworkZoneDeleted.Event(netzone, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/network-zones/{zone} network-zones network_zone_get
//
//	Get the network zone
//
//	Gets a specific network zone.
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
//	    description: zone
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
//	          $ref: "#/definitions/NetworkZone"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkZoneGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := project.NetworkZoneProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	zoneName, err := url.PathUnescape(r.PathValue("zone"))
	if err != nil {
		return response.SmartError(err)
	}

	netzone, err := zone.LoadByNameAndProject(s, projectName, zoneName)
	if err != nil {
		return response.SmartError(err)
	}

	info := netzone.Info()
	info.UsedBy, err = netzone.UsedBy()
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseETag(true, info, netzone.Etag())
}

// swagger:operation PATCH /1.0/network-zones/{zone} network-zones network_zone_patch
//
//  Partially update the network zone
//
//  Updates a subset of the network zone configuration.
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
//      name: zone
//      description: zone configuration
//      required: true
//      schema:
//        $ref: "#/definitions/NetworkZonePut"
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

// swagger:operation PUT /1.0/network-zones/{zone} network-zones network_zone_put
//
//	Update the network zone
//
//	Updates the entire network zone configuration.
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
//	    name: zone
//	    description: zone configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/NetworkZonePut"
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
func networkZonePut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := project.NetworkZoneProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	zoneName, err := url.PathUnescape(r.PathValue("zone"))
	if err != nil {
		return response.SmartError(err)
	}

	// Get the existing Network zone.
	netzone, err := zone.LoadByNameAndProject(s, projectName, zoneName)
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag.
	err = localUtil.EtagCheck(r, netzone.Etag())
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.NetworkZonePut{}

	// Decode the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if r.Method == http.MethodPatch {
		// If config being updated via "patch" method, then merge all existing config with the keys that
		// are present in the request config.
		for k, v := range netzone.Info().Config {
			_, ok := req.Config[k]
			if !ok {
				req.Config[k] = v
			}
		}
	}

	clientType := clusterRequest.UserAgentClientType(r.Header.Get("User-Agent"))

	err = netzone.Update(&req, clientType)
	if err != nil {
		return response.SmartError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.NetworkZoneUpdated.Event(netzone, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}
