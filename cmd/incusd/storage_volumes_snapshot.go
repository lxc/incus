package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/flosch/pongo2"
	"github.com/gorilla/mux"

	internalInstance "github.com/lxc/incus/internal/instance"
	"github.com/lxc/incus/internal/server/auth"
	"github.com/lxc/incus/internal/server/db"
	dbCluster "github.com/lxc/incus/internal/server/db/cluster"
	"github.com/lxc/incus/internal/server/db/operationtype"
	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/operations"
	"github.com/lxc/incus/internal/server/project"
	"github.com/lxc/incus/internal/server/request"
	"github.com/lxc/incus/internal/server/response"
	"github.com/lxc/incus/internal/server/state"
	storagePools "github.com/lxc/incus/internal/server/storage"
	"github.com/lxc/incus/internal/server/task"
	localUtil "github.com/lxc/incus/internal/server/util"
	internalUtil "github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/internal/version"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/util"
)

var storagePoolVolumeSnapshotsTypeCmd = APIEndpoint{
	Path: "storage-pools/{poolName}/volumes/{type}/{volumeName}/snapshots",

	Get:  APIEndpointAction{Handler: storagePoolVolumeSnapshotsTypeGet, AccessHandler: allowPermission(auth.ObjectTypeStorageVolume, auth.EntitlementCanView, "poolName", "type", "volumeName")},
	Post: APIEndpointAction{Handler: storagePoolVolumeSnapshotsTypePost, AccessHandler: allowPermission(auth.ObjectTypeStorageVolume, auth.EntitlementCanManageSnapshots, "poolName", "type", "volumeName")},
}

var storagePoolVolumeSnapshotTypeCmd = APIEndpoint{
	Path: "storage-pools/{poolName}/volumes/{type}/{volumeName}/snapshots/{snapshotName}",

	Delete: APIEndpointAction{Handler: storagePoolVolumeSnapshotTypeDelete, AccessHandler: allowPermission(auth.ObjectTypeStorageVolume, auth.EntitlementCanManageSnapshots, "poolName", "type", "volumeName")},
	Get:    APIEndpointAction{Handler: storagePoolVolumeSnapshotTypeGet, AccessHandler: allowPermission(auth.ObjectTypeStorageVolume, auth.EntitlementCanView, "poolName", "type", "volumeName")},
	Post:   APIEndpointAction{Handler: storagePoolVolumeSnapshotTypePost, AccessHandler: allowPermission(auth.ObjectTypeStorageVolume, auth.EntitlementCanManageSnapshots, "poolName", "type", "volumeName")},
	Patch:  APIEndpointAction{Handler: storagePoolVolumeSnapshotTypePatch, AccessHandler: allowPermission(auth.ObjectTypeStorageVolume, auth.EntitlementCanManageSnapshots, "poolName", "type", "volumeName")},
	Put:    APIEndpointAction{Handler: storagePoolVolumeSnapshotTypePut, AccessHandler: allowPermission(auth.ObjectTypeStorageVolume, auth.EntitlementCanManageSnapshots, "poolName", "type", "volumeName")},
}

// swagger:operation POST /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/snapshots storage storage_pool_volumes_type_snapshots_post
//
//	Create a storage volume snapshot
//
//	Creates a new storage volume snapshot.
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
//	  - in: body
//	    name: volume
//	    description: Storage volume snapshot
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StorageVolumeSnapshotsPost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeSnapshotsTypePost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the name of the pool.
	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the volume type.
	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the volume.
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
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	// Get the project name.
	projectName, err := project.StorageVolumeProject(s.DB.Cluster, request.ProjectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbProject, err := dbCluster.GetProject(context.Background(), tx.Tx(), projectName)
		if err != nil {
			return err
		}

		p, err := dbProject.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		err = project.AllowSnapshotCreation(p)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Forward if needed.
	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(s, r, poolName, projectName, volumeName, volumeType)
	if resp != nil {
		return resp
	}

	// Parse the request.
	req := api.StorageVolumeSnapshotsPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get a snapshot name.
	if req.Name == "" {
		i := s.DB.Cluster.GetNextStorageVolumeSnapshotIndex(poolName, volumeName, volumeType, "snap%d")
		req.Name = fmt.Sprintf("snap%d", i)
	}

	// Check that this isn't a restricted volume
	used, err := storagePools.VolumeUsedByDaemon(s, poolName, volumeName)
	if err != nil {
		return response.InternalError(err)
	}

	if used {
		return response.BadRequest(fmt.Errorf("Volumes used by Incus itself cannot have snapshots"))
	}

	// Retrieve the storage pool (and check if the storage pool exists).
	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the snapshot name using same rule as pool name.
	err = pool.ValidateName(req.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	var parentDBVolume *db.StorageVolume
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Ensure that the snapshot doesn't already exist.
		snapDBVolume, err := tx.GetStoragePoolVolume(ctx, pool.ID(), projectName, volumeType, fmt.Sprintf("%s/%s", volumeName, req.Name), true)
		if err != nil && !response.IsNotFoundError(err) {
			return err
		} else if snapDBVolume != nil {
			return api.StatusErrorf(http.StatusConflict, "Snapshot %q already in use", req.Name)
		}

		// Get the parent volume so we can get the config.
		parentDBVolume, err = tx.GetStoragePoolVolume(ctx, pool.ID(), projectName, volumeType, volumeName, true)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Fill in the expiry.
	var expiry time.Time
	if req.ExpiresAt != nil {
		expiry = *req.ExpiresAt
	} else {
		expiry, err = internalInstance.GetExpiry(time.Now(), parentDBVolume.Config["snapshots.expiry"])
		if err != nil {
			return response.BadRequest(err)
		}
	}

	// Create the snapshot.
	snapshot := func(op *operations.Operation) error {
		return pool.CreateCustomVolumeSnapshot(projectName, volumeName, req.Name, expiry, op)
	}

	resources := map[string][]api.URL{}
	resources["storage_volumes"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", poolName, "volumes", volumeTypeName, volumeName)}
	resources["storage_volume_snapshots"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", poolName, "volumes", volumeTypeName, volumeName, "snapshots", req.Name)}

	op, err := operations.OperationCreate(s, request.ProjectParam(r), operations.OperationClassTask, operationtype.VolumeSnapshotCreate, resources, nil, snapshot, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/snapshots storage storage_pool_volumes_type_snapshots_get
//
//  Get the storage volume snapshots
//
//  Returns a list of storage volume snapshots (URLs).
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
//      name: target
//      description: Cluster member name
//      type: string
//      example: server01
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
//                "/1.0/storage-pools/local/volumes/custom/foo/snapshots/snap0",
//                "/1.0/storage-pools/local/volumes/custom/foo/snapshots/snap1"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/snapshots?recursion=1 storage storage_pool_volumes_type_snapshots_get_recursion1
//
//	Get the storage volume snapshots
//
//	Returns a list of storage volume snapshots (structs).
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
//	          description: List of storage volume snapshots
//	          items:
//	            $ref: "#/definitions/StorageVolumeSnapshot"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeSnapshotsTypeGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the name of the pool the storage volume is supposed to be attached to.
	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	recursion := localUtil.IsRecursionRequest(r)

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
	if !util.ValueInSlice(volumeType, supportedVolumeTypes) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(s.DB.Cluster, request.ProjectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Retrieve ID of the storage pool (and check if the storage pool exists).
	poolID, err := s.DB.Cluster.GetStoragePoolID(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the names of all storage volume snapshots of a given volume.
	volumes, err := s.DB.Cluster.GetLocalStoragePoolVolumeSnapshotsWithType(projectName, volumeName, volumeType, poolID)
	if err != nil {
		return response.SmartError(err)
	}

	// Prepare the response.
	resultString := []string{}
	resultMap := []*api.StorageVolumeSnapshot{}
	for _, volume := range volumes {
		_, snapshotName, _ := api.GetParentAndSnapshotName(volume.Name)

		if !recursion {
			resultString = append(resultString, fmt.Sprintf("/%s/storage-pools/%s/volumes/%s/%s/snapshots/%s", version.APIVersion, poolName, volumeTypeName, volumeName, snapshotName))
		} else {
			var vol *db.StorageVolume
			err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
				vol, err = tx.GetStoragePoolVolume(ctx, poolID, projectName, volumeType, volume.Name, true)
				return err
			})
			if err != nil {
				return response.SmartError(err)
			}

			volumeUsedBy, err := storagePoolVolumeUsedByGet(s, projectName, poolName, vol)
			if err != nil {
				return response.SmartError(err)
			}

			vol.UsedBy = project.FilterUsedBy(s.Authorizer, r, volumeUsedBy)

			tmp := &api.StorageVolumeSnapshot{}
			tmp.Config = vol.Config
			tmp.Description = vol.Description
			tmp.Name = vol.Name
			tmp.CreatedAt = vol.CreatedAt

			expiryDate := volume.ExpiryDate
			if expiryDate.Unix() > 0 {
				tmp.ExpiresAt = &expiryDate
			}

			resultMap = append(resultMap, tmp)
		}
	}

	if !recursion {
		return response.SyncResponse(true, resultString)
	}

	return response.SyncResponse(true, resultMap)
}

// swagger:operation POST /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/snapshots/{snapshotName} storage storage_pool_volumes_type_snapshot_post
//
//	Rename a storage volume snapshot
//
//	Renames a storage volume snapshot.
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
//	  - in: body
//	    name: volume rename
//	    description: Storage volume snapshot
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StorageVolumeSnapshotPost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeSnapshotTypePost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the volume type.
	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage volume.
	volumeName, err := url.PathUnescape(mux.Vars(r)["volumeName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage volume.
	snapshotName, err := url.PathUnescape(mux.Vars(r)["snapshotName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	// Get the project name.
	projectName, err := project.StorageVolumeProject(s.DB.Cluster, request.ProjectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Forward if needed.
	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", volumeName, snapshotName)
	resp = forwardedResponseIfVolumeIsRemote(s, r, poolName, projectName, volumeName, volumeType)
	if resp != nil {
		return resp
	}

	// Parse the request.
	req := api.StorageVolumeSnapshotPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	if req.Name == "" {
		return response.BadRequest(fmt.Errorf("No name provided"))
	}

	if strings.Contains(req.Name, "/") {
		return response.BadRequest(fmt.Errorf("Storage volume names may not contain slashes"))
	}

	// This is a migration request so send back requested secrets.
	if req.Migration {
		req := api.StorageVolumePost{
			Name:   req.Name,
			Target: req.Target,
		}

		return storagePoolVolumeTypePostMigration(s, r, request.ProjectParam(r), projectName, poolName, fullSnapshotName, req)
	}

	// Rename the snapshot.
	snapshotRename := func(op *operations.Operation) error {
		pool, err := storagePools.LoadByName(s, poolName)
		if err != nil {
			return err
		}

		return pool.RenameCustomVolumeSnapshot(projectName, fullSnapshotName, req.Name, op)
	}

	resources := map[string][]api.URL{}
	resources["storage_volume_snapshots"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", poolName, "volumes", volumeTypeName, volumeName, "snapshots", snapshotName)}

	op, err := operations.OperationCreate(s, request.ProjectParam(r), operations.OperationClassTask, operationtype.VolumeSnapshotRename, resources, nil, snapshotRename, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/snapshots/{snapshotName} storage storage_pool_volumes_type_snapshot_get
//
//	Get the storage volume snapshot
//
//	Gets a specific storage volume snapshot.
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
//	    description: Storage volume snapshot
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
//	          $ref: "#/definitions/StorageVolumeSnapshot"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeSnapshotTypeGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the name of the storage pool the volume is supposed to be
	// attached to.
	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the volume type.
	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage volume.
	volumeName, err := url.PathUnescape(mux.Vars(r)["volumeName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage volume.
	snapshotName, err := url.PathUnescape(mux.Vars(r)["snapshotName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get the project name.
	projectName, err := project.StorageVolumeProject(s.DB.Cluster, request.ProjectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Forward if needed.
	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", volumeName, snapshotName)
	resp = forwardedResponseIfVolumeIsRemote(s, r, poolName, projectName, fullSnapshotName, volumeType)
	if resp != nil {
		return resp
	}

	// Get the snapshot.
	poolID, _, _, err := s.DB.Cluster.GetStoragePool(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	var dbVolume *db.StorageVolume
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbVolume, err = tx.GetStoragePoolVolume(ctx, poolID, projectName, volumeType, fullSnapshotName, true)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	expiry, err := s.DB.Cluster.GetStorageVolumeSnapshotExpiry(dbVolume.ID)
	if err != nil {
		return response.SmartError(err)
	}

	snapshot := api.StorageVolumeSnapshot{}
	snapshot.Config = dbVolume.Config
	snapshot.Description = dbVolume.Description
	snapshot.Name = snapshotName
	snapshot.ExpiresAt = &expiry
	snapshot.ContentType = dbVolume.ContentType
	snapshot.CreatedAt = dbVolume.CreatedAt

	etag := []any{snapshot.Description, expiry}
	return response.SyncResponseETag(true, &snapshot, etag)
}

// swagger:operation PUT /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/snapshots/{snapshotName} storage storage_pool_volumes_type_snapshot_put
//
//	Update the storage volume snapshot
//
//	Updates the entire storage volume snapshot configuration.
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
//	  - in: body
//	    name: storage volume snapshot
//	    description: Storage volume snapshot configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StorageVolumeSnapshotPut"
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
func storagePoolVolumeSnapshotTypePut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the name of the storage pool the volume is supposed to be
	// attached to.
	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the volume type.
	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage volume.
	volumeName, err := url.PathUnescape(mux.Vars(r)["volumeName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage volume.
	snapshotName, err := url.PathUnescape(mux.Vars(r)["snapshotName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get the project name.
	projectName, err := project.StorageVolumeProject(s.DB.Cluster, request.ProjectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Forward if needed.
	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", volumeName, snapshotName)
	resp = forwardedResponseIfVolumeIsRemote(s, r, poolName, projectName, fullSnapshotName, volumeType)
	if resp != nil {
		return resp
	}

	// Get the snapshot.
	poolID, _, _, err := s.DB.Cluster.GetStoragePool(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	var dbVolume *db.StorageVolume
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbVolume, err = tx.GetStoragePoolVolume(ctx, poolID, projectName, volumeType, fullSnapshotName, true)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	expiry, err := s.DB.Cluster.GetStorageVolumeSnapshotExpiry(dbVolume.ID)
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag
	etag := []any{dbVolume.Description, expiry}
	err = localUtil.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.StorageVolumeSnapshotPut{}

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	return doStoragePoolVolumeSnapshotUpdate(s, r, poolName, projectName, dbVolume.Name, volumeType, req)
}

// swagger:operation PATCH /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/snapshots/{snapshotName} storage storage_pool_volumes_type_snapshot_patch
//
//	Partially update the storage volume snapshot
//
//	Updates a subset of the storage volume snapshot configuration.
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
//	  - in: body
//	    name: storage volume snapshot
//	    description: Storage volume snapshot configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StorageVolumeSnapshotPut"
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
func storagePoolVolumeSnapshotTypePatch(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the name of the storage pool the volume is supposed to be
	// attached to.
	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the volume type.
	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage volume.
	volumeName, err := url.PathUnescape(mux.Vars(r)["volumeName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage volume.
	snapshotName, err := url.PathUnescape(mux.Vars(r)["snapshotName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Get the project name.
	projectName, err := project.StorageVolumeProject(s.DB.Cluster, request.ProjectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Forward if needed.
	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", volumeName, snapshotName)
	resp = forwardedResponseIfVolumeIsRemote(s, r, poolName, projectName, fullSnapshotName, volumeType)
	if resp != nil {
		return resp
	}

	// Get the snapshot.
	poolID, _, _, err := s.DB.Cluster.GetStoragePool(poolName)
	if err != nil {
		return response.SmartError(err)
	}

	var dbVolume *db.StorageVolume
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbVolume, err = tx.GetStoragePoolVolume(ctx, poolID, projectName, volumeType, fullSnapshotName, true)
		return err
	})

	expiry, err := s.DB.Cluster.GetStorageVolumeSnapshotExpiry(dbVolume.ID)
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag
	etag := []any{dbVolume.Description, expiry}
	err = localUtil.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.StorageVolumeSnapshotPut{
		Description: dbVolume.Description,
		ExpiresAt:   &expiry,
	}

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	return doStoragePoolVolumeSnapshotUpdate(s, r, poolName, projectName, dbVolume.Name, volumeType, req)
}

func doStoragePoolVolumeSnapshotUpdate(s *state.State, r *http.Request, poolName string, projectName string, volName string, volumeType int, req api.StorageVolumeSnapshotPut) response.Response {
	expiry := time.Time{}
	if req.ExpiresAt != nil {
		expiry = *req.ExpiresAt
	}

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Use an empty operation for this sync response to pass the requestor
	op := &operations.Operation{}
	op.SetRequestor(r)

	// Update the database.
	if volumeType == db.StoragePoolVolumeTypeCustom {
		err = pool.UpdateCustomVolumeSnapshot(projectName, volName, req.Description, nil, expiry, op)
		if err != nil {
			return response.SmartError(err)
		}
	} else {
		inst, err := instance.LoadByProjectAndName(s, projectName, volName)
		if err != nil {
			return response.SmartError(err)
		}

		err = pool.UpdateInstanceSnapshot(inst, req.Description, nil, op)
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.EmptySyncResponse
}

// swagger:operation DELETE /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/snapshots/{snapshotName} storage storage_pool_volumes_type_snapshot_delete
//
//	Delete a storage volume snapshot
//
//	Deletes a new storage volume snapshot.
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
func storagePoolVolumeSnapshotTypeDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the volume type.
	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage volume.
	volumeName, err := url.PathUnescape(mux.Vars(r)["volumeName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage volume.
	snapshotName, err := url.PathUnescape(mux.Vars(r)["snapshotName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	// Get the project name.
	projectName, err := project.StorageVolumeProject(s.DB.Cluster, request.ProjectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Forward if needed.
	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	fullSnapshotName := fmt.Sprintf("%s/%s", volumeName, snapshotName)
	resp = forwardedResponseIfVolumeIsRemote(s, r, poolName, projectName, fullSnapshotName, volumeType)
	if resp != nil {
		return resp
	}

	snapshotDelete := func(op *operations.Operation) error {
		pool, err := storagePools.LoadByName(s, poolName)
		if err != nil {
			return err
		}

		return pool.DeleteCustomVolumeSnapshot(projectName, fullSnapshotName, op)
	}

	resources := map[string][]api.URL{}
	resources["storage_volume_snapshots"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", poolName, "volumes", volumeTypeName, volumeName, "snapshots", snapshotName)}

	op, err := operations.OperationCreate(s, request.ProjectParam(r), operations.OperationClassTask, operationtype.VolumeSnapshotDelete, resources, nil, snapshotDelete, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func pruneExpiredAndAutoCreateCustomVolumeSnapshotsTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		s := d.State()
		var volumes, remoteVolumes, expiredSnapshots, expiredRemoteSnapshots []db.StorageVolumeArgs
		var memberCount int
		var onlineMemberIDs []int64

		err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			// Get the list of expired custom volume snapshots for this member (or remote).
			allExpiredSnapshots, err := tx.GetExpiredStorageVolumeSnapshots(ctx, true)
			if err != nil {
				return fmt.Errorf("Failed getting expired custom volume snapshots: %w", err)
			}

			for _, v := range allExpiredSnapshots {
				if v.NodeID < 0 {
					// Keep a separate list of remote volumes in order to select a member to
					// perform the snapshot expiry on later.
					expiredRemoteSnapshots = append(expiredRemoteSnapshots, v)
				} else {
					logger.Debug("Scheduling local custom volume snapshot expiry", logger.Ctx{"volName": v.Name, "project": v.ProjectName, "pool": v.PoolName})
					expiredSnapshots = append(expiredSnapshots, v) // Always include local volumes.
				}
			}

			projs, err := dbCluster.GetProjects(ctx, tx.Tx())
			if err != nil {
				return fmt.Errorf("Failed loading projects: %w", err)
			}

			// Key by project name for lookup later.
			projects := make(map[string]*api.Project, len(projs))
			for _, p := range projs {
				projects[p.Name], err = p.ToAPI(ctx, tx.Tx())
				if err != nil {
					return fmt.Errorf("Failed loading project %q: %w", p.Name, err)
				}
			}

			allVolumes, err := tx.GetStoragePoolVolumesWithType(ctx, db.StoragePoolVolumeTypeCustom, true)
			if err != nil {
				return fmt.Errorf("Failed getting volumes for auto custom volume snapshot task: %w", err)
			}

			for _, v := range allVolumes {
				err = project.AllowSnapshotCreation(projects[v.ProjectName])
				if err != nil {
					continue
				}

				schedule, ok := v.Config["snapshots.schedule"]
				if !ok || schedule == "" {
					continue
				}

				// Check if snapshot is scheduled.
				if !snapshotIsScheduledNow(schedule, v.ID) {
					continue
				}

				if v.NodeID < 0 {
					// Keep a separate list of remote volumes in order to select a member to
					// perform the snapshot later.
					remoteVolumes = append(remoteVolumes, v)
				} else {
					logger.Debug("Scheduling local auto custom volume snapshot", logger.Ctx{"volName": v.Name, "project": v.ProjectName, "pool": v.PoolName})
					volumes = append(volumes, v) // Always include local volumes.
				}
			}

			if len(remoteVolumes) > 0 || len(expiredRemoteSnapshots) > 0 {
				// Get list of cluster members.
				members, err := tx.GetNodes(ctx)
				if err != nil {
					return fmt.Errorf("Failed getting cluster members: %w", err)
				}

				memberCount = len(members)

				// Filter to online members.
				for _, member := range members {
					if member.IsOffline(s.GlobalConfig.OfflineThreshold()) {
						continue
					}

					onlineMemberIDs = append(onlineMemberIDs, member.ID)
				}

				return nil
			}

			return nil
		})
		if err != nil {
			logger.Error("Failed getting custom volume info", logger.Ctx{"err": err})
			return
		}

		localMemberID := s.DB.Cluster.GetNodeID()

		if len(expiredRemoteSnapshots) > 0 {
			// Skip expiring remote custom volume snapshots if there are no online members, as we can't
			// be sure that the cluster isn't partitioned and we may end up attempting to expire
			// snapshot on multiple members.
			if memberCount > 1 && len(onlineMemberIDs) <= 0 {
				logger.Error("Skipping remote volumes for expire custom volume snapshot task due to no online members")
			} else {
				for _, v := range expiredRemoteSnapshots {
					// If there are multiple cluster members, a stable random member is chosen
					// to perform the snapshot expiry. This avoids expiring the snapshot on
					// every member and spreads the load across the online cluster members.
					if memberCount > 1 {
						selectedMemberID, err := localUtil.GetStableRandomInt64FromList(int64(v.ID), onlineMemberIDs)
						if err != nil {
							logger.Error("Failed scheduling remote expire custom volume snapshot task", logger.Ctx{"volName": v.Name, "project": v.ProjectName, "pool": v.PoolName, "err": err})
							continue
						}

						// Don't snapshot, if we're not the chosen one.
						if localMemberID != selectedMemberID {
							continue
						}
					}

					logger.Debug("Scheduling remote custom volume snapshot expiry", logger.Ctx{"volName": v.Name, "project": v.ProjectName, "pool": v.PoolName})
					expiredSnapshots = append(expiredSnapshots, v)
				}
			}
		}

		if len(remoteVolumes) > 0 {
			// Skip snapshotting remote custom volumes if there are no online members, as we can't be
			// sure that the cluster isn't partitioned and we may end up attempting the snapshot on
			// multiple members.
			if memberCount > 1 && len(onlineMemberIDs) <= 0 {
				logger.Error("Skipping remote volumes for auto custom volume snapshot task due to no online members")
			} else {
				for _, v := range remoteVolumes {
					// If there are multiple cluster members, a stable random member is chosen
					// to perform the snapshot from. This avoids taking the snapshot on every
					// member and spreads the load taking the snapshots across the online
					// cluster members.
					if memberCount > 1 {
						selectedNodeID, err := localUtil.GetStableRandomInt64FromList(int64(v.ID), onlineMemberIDs)
						if err != nil {
							logger.Error("Failed scheduling remote auto custom volume snapshot task", logger.Ctx{"volName": v.Name, "project": v.ProjectName, "pool": v.PoolName, "err": err})
							continue
						}

						// Don't snapshot, if we're not the chosen one.
						if localMemberID != selectedNodeID {
							continue
						}
					}

					logger.Debug("Scheduling remote auto custom volume snapshot", logger.Ctx{"volName": v.Name, "project": v.ProjectName, "pool": v.PoolName})
					volumes = append(volumes, v)
				}
			}
		}

		// Handle snapshot expiry first before creating new ones to reduce the chances of running out of
		// disk space.
		if len(expiredSnapshots) > 0 {
			opRun := func(op *operations.Operation) error {
				return pruneExpiredCustomVolumeSnapshots(ctx, s, expiredSnapshots)
			}

			op, err := operations.OperationCreate(s, "", operations.OperationClassTask, operationtype.CustomVolumeSnapshotsExpire, nil, nil, opRun, nil, nil, nil)
			if err != nil {
				logger.Error("Failed creating expired custom volume snapshots prune operation", logger.Ctx{"err": err})
			} else {
				logger.Info("Pruning expired custom volume snapshots")
				err = op.Start()
				if err != nil {
					logger.Error("Failed starting expired custom volume snapshots prune operation", logger.Ctx{"err": err})
				} else {
					err = op.Wait(ctx)
					if err != nil {
						logger.Error("Failed pruning expired custom volume snapshots", logger.Ctx{"err": err})
					} else {
						logger.Info("Done pruning expired custom volume snapshots")
					}
				}
			}
		}

		// Handle snapshot auto creation.
		if len(volumes) > 0 {
			opRun := func(op *operations.Operation) error {
				return autoCreateCustomVolumeSnapshots(ctx, s, volumes)
			}

			op, err := operations.OperationCreate(s, "", operations.OperationClassTask, operationtype.VolumeSnapshotCreate, nil, nil, opRun, nil, nil, nil)
			if err != nil {
				logger.Error("Failed creating scheduled volume snapshot operation", logger.Ctx{"err": err})
			} else {
				logger.Info("Creating scheduled volume snapshots")
				err = op.Start()
				if err != nil {
					logger.Error("Failed starting scheduled volume snapshot operation", logger.Ctx{"err": err})
				} else {
					err = op.Wait(ctx)
					if err != nil {
						logger.Error("Failed scheduled custom volume snapshots", logger.Ctx{"err": err})
					} else {
						logger.Info("Done creating scheduled volume snapshots")
					}
				}
			}
		}
	}

	first := true
	schedule := func() (time.Duration, error) {
		interval := time.Minute

		if first {
			first = false
			return interval, task.ErrSkip
		}

		return interval, nil
	}

	return f, schedule
}

var customVolSnapshotsPruneRunning = sync.Map{}

func pruneExpiredCustomVolumeSnapshots(ctx context.Context, s *state.State, expiredSnapshots []db.StorageVolumeArgs) error {
	for _, v := range expiredSnapshots {
		err := ctx.Err()
		if err != nil {
			return err // Stop if context is cancelled.
		}

		_, loaded := customVolSnapshotsPruneRunning.LoadOrStore(v.ID, struct{}{})
		if loaded {
			continue // Deletion of this snapshot is already running, skip.
		}

		pool, err := storagePools.LoadByName(s, v.PoolName)
		if err != nil {
			customVolSnapshotsPruneRunning.Delete(v.ID)
			return fmt.Errorf("Error loading pool for volume snapshot %q (project %q, pool %q): %w", v.Name, v.ProjectName, v.PoolName, err)
		}

		err = pool.DeleteCustomVolumeSnapshot(v.ProjectName, v.Name, nil)
		customVolSnapshotsPruneRunning.Delete(v.ID)
		if err != nil {
			return fmt.Errorf("Error deleting custom volume snapshot %q (project %q, pool %q): %w", v.Name, v.ProjectName, v.PoolName, err)
		}
	}

	return nil
}

func autoCreateCustomVolumeSnapshots(ctx context.Context, s *state.State, volumes []db.StorageVolumeArgs) error {
	// Make the snapshots sequentially.
	for _, v := range volumes {
		err := ctx.Err()
		if err != nil {
			return err // Stop if context is cancelled.
		}

		snapshotName, err := volumeDetermineNextSnapshotName(s, v, "snap%d")
		if err != nil {
			return fmt.Errorf("Error retrieving next snapshot name for volume %q (project %q, pool %q): %w", v.Name, v.ProjectName, v.PoolName, err)
		}

		expiry, err := internalInstance.GetExpiry(time.Now(), v.Config["snapshots.expiry"])
		if err != nil {
			return fmt.Errorf("Error getting snapshot expiry for volume %q (project %q, pool %q): %w", v.Name, v.ProjectName, v.PoolName, err)
		}

		pool, err := storagePools.LoadByName(s, v.PoolName)
		if err != nil {
			return fmt.Errorf("Error loading pool for volume %q (project %q, pool %q): %w", v.Name, v.ProjectName, v.PoolName, err)
		}

		err = pool.CreateCustomVolumeSnapshot(v.ProjectName, v.Name, snapshotName, expiry, nil)
		if err != nil {
			return fmt.Errorf("Error creating snapshot for volume %q (project %q, pool %q): %w", v.Name, v.ProjectName, v.PoolName, err)
		}
	}

	return nil
}

func volumeDetermineNextSnapshotName(s *state.State, volume db.StorageVolumeArgs, defaultPattern string) (string, error) {
	var err error

	pattern, ok := volume.Config["snapshots.pattern"]
	if !ok {
		pattern = defaultPattern
	}

	pattern, err = internalUtil.RenderTemplate(pattern, pongo2.Context{
		"creation_date": time.Now(),
	})
	if err != nil {
		return "", err
	}

	count := strings.Count(pattern, "%d")
	if count > 1 {
		return "", fmt.Errorf("Snapshot pattern may contain '%%d' only once")
	} else if count == 1 {
		i := s.DB.Cluster.GetNextStorageVolumeSnapshotIndex(volume.PoolName, volume.Name, db.StoragePoolVolumeTypeCustom, pattern)
		return strings.Replace(pattern, "%d", strconv.Itoa(i), 1), nil
	}

	snapshotExists := false

	var snapshots []db.StorageVolumeArgs
	var projects []string

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		projects, err = dbCluster.GetProjectNames(ctx, tx.Tx())
		return err
	})
	if err != nil {
		return "", err
	}

	pools, err := s.DB.Cluster.GetStoragePoolNames()
	if err != nil {
		return "", err
	}

	for _, pool := range pools {
		poolID, err := s.DB.Cluster.GetStoragePoolID(pool)
		if err != nil {
			return "", err
		}

		for _, project := range projects {
			snaps, err := s.DB.Cluster.GetLocalStoragePoolVolumeSnapshotsWithType(project, volume.Name, db.StoragePoolVolumeTypeCustom, poolID)
			if err != nil {
				return "", err
			}

			snapshots = append(snapshots, snaps...)
		}
	}

	for _, snap := range snapshots {
		_, snapOnlyName, _ := api.GetParentAndSnapshotName(snap.Name)

		if snapOnlyName == pattern {
			snapshotExists = true
			break
		}
	}

	if snapshotExists {
		i := s.DB.Cluster.GetNextStorageVolumeSnapshotIndex(volume.PoolName, volume.Name, db.StoragePoolVolumeTypeCustom, pattern)
		return strings.Replace(pattern, "%d", strconv.Itoa(i), 1), nil
	}

	return pattern, nil
}
