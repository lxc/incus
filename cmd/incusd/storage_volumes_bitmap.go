package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"

	"github.com/gorilla/mux"

	"github.com/lxc/incus/v6/internal/server/auth"
	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/request"
	"github.com/lxc/incus/v6/internal/server/response"
	storagePools "github.com/lxc/incus/v6/internal/server/storage"
	"github.com/lxc/incus/v6/shared/api"
)

var storagePoolVolumeTypeBitmapsCmd = APIEndpoint{
	Path: "storage-pools/{poolName}/volumes/{type}/{volumeName}/bitmaps",

	Get:  APIEndpointAction{Handler: storagePoolVolumeTypeBitmapsGet, AccessHandler: allowPermission(auth.ObjectTypeStorageVolume, auth.EntitlementCanView, "poolName", "type", "volumeName", "location")},
	Post: APIEndpointAction{Handler: storagePoolVolumeTypeBitmapsPost, AccessHandler: allowPermission(auth.ObjectTypeStorageVolume, auth.EntitlementCanEdit, "poolName", "type", "volumeName", "location")},
}

var storagePoolVolumeTypeBitmapCmd = APIEndpoint{
	Path: "storage-pools/{poolName}/volumes/{type}/{volumeName}/bitmaps/{bitmapName}",

	Get:    APIEndpointAction{Handler: storagePoolVolumeTypeBitmapGet, AccessHandler: allowPermission(auth.ObjectTypeStorageVolume, auth.EntitlementCanView, "poolName", "type", "volumeName", "location")},
	Delete: APIEndpointAction{Handler: storagePoolVolumeTypeBitmapDelete, AccessHandler: allowPermission(auth.ObjectTypeStorageVolume, auth.EntitlementCanEdit, "poolName", "type", "volumeName", "location")},
}

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/bitmaps storage storage_pool_volume_type_bitmaps_get
//
//	Get the storage volume dirty bitmaps
//
//	Gets a specific storage volume bitmaps
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
//	    description: Storage volume bitmaps
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
//	          $ref: "#/definitions/StorageVolumeBitmap"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeTypeBitmapsGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)

	// Get the volume details.
	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	volumeName, err := url.PathUnescape(mux.Vars(r)["volumeName"])
	if err != nil {
		return response.SmartError(err)
	}

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeDBType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if !slices.Contains([]int{db.StoragePoolVolumeTypeVM, db.StoragePoolVolumeTypeCustom}, volumeDBType) {
		return response.BadRequest(fmt.Errorf("Unsupported storage volume type %q", volumeTypeName))
	}

	resp := forwardedResponseIfVolumeIsRemote(s, r, poolName, projectName, volumeName, volumeDBType)
	if resp != nil {
		return resp
	}

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(err)
	}

	volumeType, err := storagePools.VolumeDBTypeToType(volumeDBType)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the volume.
	dbVol, err := storagePools.VolumeDBGet(pool, projectName, volumeName, volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	contentType, err := storagePools.VolumeContentTypeNameToContentType(dbVol.ContentType)
	if err != nil {
		return response.SmartError(err)
	}

	if contentType != db.StoragePoolVolumeContentTypeBlock {
		return response.BadRequest(fmt.Errorf("Only block volumes are supported"))
	}

	inst, deviceName, err := storagePools.InstanceByVolumeName(s, poolName, projectName, volumeName, volumeDBType)
	if err != nil {
		return response.SmartError(err)
	}

	if !inst.IsRunning() {
		return response.BadRequest(fmt.Errorf("Listing bitmaps requires the instance to be running"))
	}

	bitmaps, err := inst.GetBitmaps(deviceName)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, bitmaps)
}

// swagger:operation POST /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/bitmaps storage storage_pool_volumes_type_bitmaps_post
//
//	Create a storage volume bitmap
//
//	Creates a new storage volume bitmap.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	  - application/octet-stream
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
//	  - in: body
//	    name: volume
//	    description: Storage volume bitmap
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StorageVolumeBitmapsPost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeTypeBitmapsPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)

	// Get the volume details.
	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	volumeName, err := url.PathUnescape(mux.Vars(r)["volumeName"])
	if err != nil {
		return response.SmartError(err)
	}

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeDBType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if !slices.Contains([]int{db.StoragePoolVolumeTypeVM, db.StoragePoolVolumeTypeCustom}, volumeDBType) {
		return response.BadRequest(fmt.Errorf("Unsupported storage volume type %q", volumeTypeName))
	}

	resp := forwardedResponseIfVolumeIsRemote(s, r, poolName, projectName, volumeName, volumeDBType)
	if resp != nil {
		return resp
	}

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(err)
	}

	volumeType, err := storagePools.VolumeDBTypeToType(volumeDBType)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the volume.
	dbVol, err := storagePools.VolumeDBGet(pool, projectName, volumeName, volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	contentType, err := storagePools.VolumeContentTypeNameToContentType(dbVol.ContentType)
	if err != nil {
		return response.SmartError(err)
	}

	if contentType != db.StoragePoolVolumeContentTypeBlock {
		return response.BadRequest(fmt.Errorf("Only block volumes are supported"))
	}

	inst, deviceName, err := storagePools.InstanceByVolumeName(s, poolName, projectName, volumeName, volumeDBType)
	if err != nil {
		return response.SmartError(err)
	}

	if !inst.IsRunning() {
		return response.BadRequest(fmt.Errorf("Creating bitmaps requires the instance to be running"))
	}

	req := api.StorageVolumeBitmapsPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = inst.CreateBitmap([]string{deviceName}, req)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/bitmaps/{bitmapName} storage storage_pool_volume_type_bitmap_get
//
//	Get the storage volume dirty bitmap
//
//	Gets a specific storage volume bitmap
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
//	    description: Storage volume bitmap
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
//	          $ref: "#/definitions/StorageVolumeBitmap"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeTypeBitmapGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)

	// Get the volume details.
	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	volumeName, err := url.PathUnescape(mux.Vars(r)["volumeName"])
	if err != nil {
		return response.SmartError(err)
	}

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	bitmapName, err := url.PathUnescape(mux.Vars(r)["bitmapName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeDBType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if !slices.Contains([]int{db.StoragePoolVolumeTypeVM, db.StoragePoolVolumeTypeCustom}, volumeDBType) {
		return response.BadRequest(fmt.Errorf("Unsupported storage volume type %q", volumeTypeName))
	}

	resp := forwardedResponseIfVolumeIsRemote(s, r, poolName, projectName, volumeName, volumeDBType)
	if resp != nil {
		return resp
	}

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(err)
	}

	volumeType, err := storagePools.VolumeDBTypeToType(volumeDBType)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the volume.
	dbVol, err := storagePools.VolumeDBGet(pool, projectName, volumeName, volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	contentType, err := storagePools.VolumeContentTypeNameToContentType(dbVol.ContentType)
	if err != nil {
		return response.SmartError(err)
	}

	if contentType != db.StoragePoolVolumeContentTypeBlock {
		return response.BadRequest(fmt.Errorf("Only block volumes are supported"))
	}

	inst, deviceName, err := storagePools.InstanceByVolumeName(s, poolName, projectName, volumeName, volumeDBType)
	if err != nil {
		return response.SmartError(err)
	}

	if !inst.IsRunning() {
		return response.BadRequest(fmt.Errorf("Listing bitmap requires the instance to be running"))
	}

	bitmaps, err := inst.GetBitmaps(deviceName)
	if err != nil {
		return response.SmartError(err)
	}

	for _, b := range bitmaps {
		if b.Name == bitmapName {
			return response.SyncResponse(true, b)
		}
	}

	return response.BadRequest(fmt.Errorf("Bitmap %q not found", bitmapName))
}

// swagger:operation DELETE /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/bitmaps/{bitmapName} storage storage_pool_volumes_type_bitmap_delete
//
//	Delete a storage volume bitmap
//
//	Deletes a storage volume bitmap.
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
//	  - in: query
//	    name: target
//	    description: Cluster member name
//	    type: string
//	    example: server01
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeTypeBitmapDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)

	// Get the volume details.
	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	volumeName, err := url.PathUnescape(mux.Vars(r)["volumeName"])
	if err != nil {
		return response.SmartError(err)
	}

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	bitmapName, err := url.PathUnescape(mux.Vars(r)["bitmapName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeDBType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if !slices.Contains([]int{db.StoragePoolVolumeTypeVM, db.StoragePoolVolumeTypeCustom}, volumeDBType) {
		return response.BadRequest(fmt.Errorf("Unsupported storage volume type %q", volumeTypeName))
	}

	resp := forwardedResponseIfVolumeIsRemote(s, r, poolName, projectName, volumeName, volumeDBType)
	if resp != nil {
		return resp
	}

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(err)
	}

	volumeType, err := storagePools.VolumeDBTypeToType(volumeDBType)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the volume.
	dbVol, err := storagePools.VolumeDBGet(pool, projectName, volumeName, volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	contentType, err := storagePools.VolumeContentTypeNameToContentType(dbVol.ContentType)
	if err != nil {
		return response.SmartError(err)
	}

	if contentType != db.StoragePoolVolumeContentTypeBlock {
		return response.BadRequest(fmt.Errorf("Only block volumes are supported"))
	}

	inst, deviceName, err := storagePools.InstanceByVolumeName(s, poolName, projectName, volumeName, volumeDBType)
	if err != nil {
		return response.SmartError(err)
	}

	if !inst.IsRunning() {
		return response.BadRequest(fmt.Errorf("Deleting bitmaps requires the instance to be running"))
	}

	err = inst.DeleteBitmap(deviceName, bitmapName)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}
