package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"

	internalInstance "github.com/lxc/incus/v7/internal/instance"
	"github.com/lxc/incus/v7/internal/server/auth"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/operationtype"
	"github.com/lxc/incus/v7/internal/server/operations"
	"github.com/lxc/incus/v7/internal/server/project"
	"github.com/lxc/incus/v7/internal/server/request"
	"github.com/lxc/incus/v7/internal/server/response"
	storagePools "github.com/lxc/incus/v7/internal/server/storage"
	"github.com/lxc/incus/v7/internal/version"
	"github.com/lxc/incus/v7/shared/api"
)

var storagePoolVolumeTypeRebuildCmd = APIEndpoint{
	Path: "storage-pools/{poolName}/volumes/{type}/{volumeName}/rebuild",

	Post: APIEndpointAction{Handler: storagePoolVolumeTypeRebuildPost, AccessHandler: allowPermission(auth.ObjectTypeStorageVolume, auth.EntitlementCanEdit, "poolName", "type", "volumeName", "location")},
}

// swagger:operation POST /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/rebuild storage storage_pool_volume_type_rebuild_post
//
//	Rebuild the storage volume
//
//	Wipes the underlying storage volume and re-creates an empty one with the
//	same configuration. Only allowed for custom volumes without snapshots.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: path
//	    name: poolName
//	    description: Storage pool name
//	    type: string
//	    required: true
//	  - in: path
//	    name: type
//	    description: Storage volume type
//	    type: string
//	    required: true
//	  - in: path
//	    name: volumeName
//	    description: Storage volume name
//	    type: string
//	    required: true
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
//	  - in: body
//	    name: volume
//	    description: Storage volume rebuild request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StorageVolumeRebuildPost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeTypeRebuildPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the name of the storage volume.
	volumeName, err := pathVar(r, "volumeName")
	if err != nil {
		return response.SmartError(err)
	}

	volumeTypeName, err := pathVar(r, "type")
	if err != nil {
		return response.SmartError(err)
	}

	if internalInstance.IsSnapshot(volumeName) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume %q", volumeName))
	}

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName, err := pathVar(r, "poolName")
	if err != nil {
		return response.SmartError(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	requestProjectName := request.ProjectParam(r)
	volumeProjectName, err := project.StorageVolumeProject(s.DB.Cluster, requestProjectName, volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Check that the storage volume type is valid.
	if !slices.Contains(supportedVolumeTypes, volumeType) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	// Only custom volumes can be rebuilt via this endpoint.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Storage volumes of type %q cannot be rebuilt", volumeTypeName))
	}

	// Parse the request body (currently empty but reserved for future use).
	req := api.StorageVolumeRebuildPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil && !errors.Is(err, io.EOF) {
		return response.BadRequest(err)
	}

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(s, r, poolName, volumeProjectName, volumeName, volumeType)
	if resp != nil {
		return resp
	}

	// Get the storage pool.
	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(err)
	}

	run := func(op *operations.Operation) error {
		return pool.RebuildCustomVolume(volumeProjectName, volumeName, op)
	}

	resources := map[string][]api.URL{}
	resources["storage_volumes"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", poolName, "volumes", volumeTypeName, volumeName)}

	op, err := operations.OperationCreate(s, requestProjectName, operations.OperationClassTask, operationtype.VolumeRebuild, resources, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}
