package main

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	"github.com/lxc/incus/internal/server/auth"
	clusterRequest "github.com/lxc/incus/internal/server/cluster/request"
	"github.com/lxc/incus/internal/server/lifecycle"
	"github.com/lxc/incus/internal/server/network/zone"
	"github.com/lxc/incus/internal/server/project"
	"github.com/lxc/incus/internal/server/request"
	"github.com/lxc/incus/internal/server/response"
	localUtil "github.com/lxc/incus/internal/server/util"
	"github.com/lxc/incus/internal/version"
	"github.com/lxc/incus/shared/api"
)

var networkZoneRecordsCmd = APIEndpoint{
	Path: "network-zones/{zone}/records",

	Get:  APIEndpointAction{Handler: networkZoneRecordsGet, AccessHandler: allowPermission(auth.ObjectTypeNetworkZone, auth.EntitlementCanView, "zone")},
	Post: APIEndpointAction{Handler: networkZoneRecordsPost, AccessHandler: allowPermission(auth.ObjectTypeNetworkZone, auth.EntitlementCanEdit, "zone")},
}

var networkZoneRecordCmd = APIEndpoint{
	Path: "network-zones/{zone}/records/{name}",

	Delete: APIEndpointAction{Handler: networkZoneRecordDelete, AccessHandler: allowPermission(auth.ObjectTypeNetworkZone, auth.EntitlementCanEdit, "zone")},
	Get:    APIEndpointAction{Handler: networkZoneRecordGet, AccessHandler: allowPermission(auth.ObjectTypeNetworkZone, auth.EntitlementCanView, "zone")},
	Put:    APIEndpointAction{Handler: networkZoneRecordPut, AccessHandler: allowPermission(auth.ObjectTypeNetworkZone, auth.EntitlementCanEdit, "zone")},
	Patch:  APIEndpointAction{Handler: networkZoneRecordPut, AccessHandler: allowPermission(auth.ObjectTypeNetworkZone, auth.EntitlementCanEdit, "zone")},
}

// API endpoints.

// swagger:operation GET /1.0/network-zones/{zone}/records network-zones network_zone_records_get
//
//  Get the network zone records
//
//  Returns a list of network zone records (URLs).
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
//                "/1.0/network-zones/example.net/records/foo",
//                "/1.0/network-zones/example.net/records/bar"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/network-zones/{zone}/records?recursion=1 network-zones network_zone_records_get_recursion1
//
//	Get the network zone records
//
//	Returns a list of network zone records (structs).
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
//	          description: List of network zone records
//	          items:
//	            $ref: "#/definitions/NetworkZoneRecord"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkZoneRecordsGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := project.NetworkZoneProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	recursion := localUtil.IsRecursionRequest(r)

	zoneName, err := url.PathUnescape(mux.Vars(r)["zone"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the network zone.
	netzone, err := zone.LoadByNameAndProject(s, projectName, zoneName)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the records.
	records, err := netzone.GetRecords()
	if err != nil {
		return response.SmartError(err)
	}

	resultString := []string{}
	resultMap := []api.NetworkZoneRecord{}
	for _, record := range records {
		if !recursion {
			resultString = append(resultString, api.NewURL().Path(version.APIVersion, "network-zones", zoneName, "records", record.Name).String())
		} else {
			resultMap = append(resultMap, record)
		}
	}

	if !recursion {
		return response.SyncResponse(true, resultString)
	}

	return response.SyncResponse(true, resultMap)
}

// swagger:operation POST /1.0/network-zones/{zone}/records network-zones network_zone_records_post
//
//	Add a network zone record
//
//	Creates a new network zone record.
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
//	      $ref: "#/definitions/NetworkZoneRecordsPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkZoneRecordsPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := project.NetworkZoneProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	zoneName, err := url.PathUnescape(mux.Vars(r)["zone"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the network zone.
	netzone, err := zone.LoadByNameAndProject(s, projectName, zoneName)
	if err != nil {
		return response.SmartError(err)
	}

	// Parse the request into a record.
	req := api.NetworkZoneRecordsPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Create the record.
	err = netzone.AddRecord(req)
	if err != nil {
		return response.SmartError(err)
	}

	lc := lifecycle.NetworkZoneRecordCreated.Event(netzone, req.Name, request.CreateRequestor(r), nil)
	s.Events.SendLifecycle(projectName, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// swagger:operation DELETE /1.0/network-zones/{zone}/records/{name} network-zones network_zone_record_delete
//
//	Delete the network zone record
//
//	Removes the network zone record.
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
func networkZoneRecordDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := project.NetworkZoneProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	zoneName, err := url.PathUnescape(mux.Vars(r)["zone"])
	if err != nil {
		return response.SmartError(err)
	}

	recordName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the network zone.
	netzone, err := zone.LoadByNameAndProject(s, projectName, zoneName)
	if err != nil {
		return response.SmartError(err)
	}

	// Delete the record.
	err = netzone.DeleteRecord(recordName)
	if err != nil {
		return response.SmartError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.NetworkZoneRecordDeleted.Event(netzone, recordName, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/network-zones/{zone}/records/{name} network-zones network_zone_record_get
//
//	Get the network zone record
//
//	Gets a specific network zone record.
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
//	          $ref: "#/definitions/NetworkZoneRecord"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func networkZoneRecordGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := project.NetworkZoneProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	zoneName, err := url.PathUnescape(mux.Vars(r)["zone"])
	if err != nil {
		return response.SmartError(err)
	}

	recordName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the network zone.
	netzone, err := zone.LoadByNameAndProject(s, projectName, zoneName)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the record.
	record, err := netzone.GetRecord(recordName)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponseETag(true, record, record.Writable())
}

// swagger:operation PATCH /1.0/network-zones/{zone}/records/{name} network-zones network_zone_record_patch
//
//  Partially update the network zone record
//
//  Updates a subset of the network zone record configuration.
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
//      description: zone record configuration
//      required: true
//      schema:
//        $ref: "#/definitions/NetworkZoneRecordPut"
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

// swagger:operation PUT /1.0/network-zones/{zone}/records/{name} network-zones network_zone_record_put
//
//	Update the network zone record
//
//	Updates the entire network zone record configuration.
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
//	    description: zone record configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/NetworkZoneRecordPut"
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
func networkZoneRecordPut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName, _, err := project.NetworkZoneProject(s.DB.Cluster, request.ProjectParam(r))
	if err != nil {
		return response.SmartError(err)
	}

	zoneName, err := url.PathUnescape(mux.Vars(r)["zone"])
	if err != nil {
		return response.SmartError(err)
	}

	recordName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the network zone.
	netzone, err := zone.LoadByNameAndProject(s, projectName, zoneName)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the record.
	record, err := netzone.GetRecord(recordName)
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag.
	err = localUtil.EtagCheck(r, record.Writable())
	if err != nil {
		return response.PreconditionFailed(err)
	}

	// Decode the request.
	req := api.NetworkZoneRecordPut{}
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
	err = netzone.UpdateRecord(recordName, req, clientType)
	if err != nil {
		return response.SmartError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.NetworkZoneRecordUpdated.Event(netzone, recordName, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}
