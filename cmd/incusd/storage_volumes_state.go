package main

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"

	"github.com/gorilla/mux"

	"github.com/lxc/incus/v6/internal/server/auth"
	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/internal/server/request"
	"github.com/lxc/incus/v6/internal/server/response"
	storagePools "github.com/lxc/incus/v6/internal/server/storage"
	"github.com/lxc/incus/v6/shared/api"
)

var storagePoolVolumeTypeStateCmd = APIEndpoint{
	Path: "storage-pools/{poolName}/volumes/{type}/{volumeName}/state",

	Get: APIEndpointAction{Handler: storagePoolVolumeTypeStateGet, AccessHandler: allowPermission(auth.ObjectTypeStorageVolume, auth.EntitlementCanView, "poolName", "type", "volumeName")},
}

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/state storage storage_pool_volume_type_state_get
//
//	Get the storage volume state
//
//	Gets a specific storage volume state (usage data).
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
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: server01
//	responses:
//	  "200":
//	    description: Storage pool
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
//	          $ref: "#/definitions/StorageVolumeState"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeTypeStateGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the name of the pool the storage volume is supposed to be attached to.
	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the volume type.
	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the volume type.
	volumeName, err := url.PathUnescape(mux.Vars(r)["volumeName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if !slices.Contains([]int{db.StoragePoolVolumeTypeCustom, db.StoragePoolVolumeTypeContainer, db.StoragePoolVolumeTypeVM}, volumeType) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	// Get the storage project name.
	projectName, err := project.StorageVolumeProject(s.DB.Cluster, request.ProjectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Load the storage pool.
	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Fetch the current usage.
	var usage *storagePools.VolumeUsage
	if volumeType == db.StoragePoolVolumeTypeCustom {
		// Custom volumes.
		usage, err = pool.GetCustomVolumeUsage(projectName, volumeName)
		if err != nil {
			return response.SmartError(err)
		}
	} else {
		resp, err := forwardedResponseIfInstanceIsRemote(s, r, projectName, volumeName, instancetype.Any)
		if err != nil {
			return response.SmartError(err)
		}

		if resp != nil {
			return resp
		}

		// Instance volumes.
		inst, err := instance.LoadByProjectAndName(s, projectName, volumeName)
		if err != nil {
			return response.SmartError(err)
		}

		usage, err = pool.GetInstanceUsage(inst)
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Prepare the state struct.
	state := api.StorageVolumeState{}
	state.Usage = &api.StorageVolumeStateUsage{}

	// Only fill 'used' field if receiving a valid value.
	if usage.Used >= 0 {
		state.Usage.Used = uint64(usage.Used)
	}

	// Only fill 'total' field if receiving a valid value.
	if usage.Total >= 0 {
		state.Usage.Total = usage.Total
	}

	return response.SyncResponse(true, state)
}
