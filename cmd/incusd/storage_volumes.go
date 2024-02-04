package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"

	"github.com/lxc/incus/internal/filter"
	internalInstance "github.com/lxc/incus/internal/instance"
	internalIO "github.com/lxc/incus/internal/io"
	"github.com/lxc/incus/internal/revert"
	"github.com/lxc/incus/internal/server/auth"
	"github.com/lxc/incus/internal/server/backup"
	"github.com/lxc/incus/internal/server/cluster"
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
	localUtil "github.com/lxc/incus/internal/server/util"
	internalUtil "github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/internal/version"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/archive"
	"github.com/lxc/incus/shared/logger"
	localtls "github.com/lxc/incus/shared/tls"
	"github.com/lxc/incus/shared/util"
)

var storagePoolVolumesCmd = APIEndpoint{
	Path: "storage-pools/{poolName}/volumes",

	Get:  APIEndpointAction{Handler: storagePoolVolumesGet, AccessHandler: allowAuthenticated},
	Post: APIEndpointAction{Handler: storagePoolVolumesPost, AccessHandler: allowPermission(auth.ObjectTypeProject, auth.EntitlementCanCreateStorageVolumes)},
}

var storagePoolVolumesTypeCmd = APIEndpoint{
	Path: "storage-pools/{poolName}/volumes/{type}",

	Get:  APIEndpointAction{Handler: storagePoolVolumesGet, AccessHandler: allowAuthenticated},
	Post: APIEndpointAction{Handler: storagePoolVolumesPost, AccessHandler: allowPermission(auth.ObjectTypeProject, auth.EntitlementCanCreateStorageVolumes)},
}

var storagePoolVolumeTypeCmd = APIEndpoint{
	Path: "storage-pools/{poolName}/volumes/{type}/{volumeName}",

	Delete: APIEndpointAction{Handler: storagePoolVolumeDelete, AccessHandler: allowPermission(auth.ObjectTypeStorageVolume, auth.EntitlementCanEdit, "poolName", "type", "volumeName", "location")},
	Get:    APIEndpointAction{Handler: storagePoolVolumeGet, AccessHandler: allowPermission(auth.ObjectTypeStorageVolume, auth.EntitlementCanView, "poolName", "type", "volumeName", "location")},
	Patch:  APIEndpointAction{Handler: storagePoolVolumePatch, AccessHandler: allowPermission(auth.ObjectTypeStorageVolume, auth.EntitlementCanEdit, "poolName", "type", "volumeName", "location")},
	Post:   APIEndpointAction{Handler: storagePoolVolumePost, AccessHandler: allowPermission(auth.ObjectTypeStorageVolume, auth.EntitlementCanEdit, "poolName", "type", "volumeName", "location")},
	Put:    APIEndpointAction{Handler: storagePoolVolumePut, AccessHandler: allowPermission(auth.ObjectTypeStorageVolume, auth.EntitlementCanEdit, "poolName", "type", "volumeName", "location")},
}

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes storage storage_pool_volumes_get
//
//  Get the storage volumes
//
//  Returns a list of storage volumes (URLs).
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
//                "/1.0/storage-pools/local/volumes/container/a1",
//                "/1.0/storage-pools/local/volumes/container/a2",
//                "/1.0/storage-pools/local/volumes/custom/backups",
//                "/1.0/storage-pools/local/volumes/custom/images"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes?recursion=1 storage storage_pool_volumes_get_recursion1
//
//  Get the storage volumes
//
//  Returns a list of storage volumes (structs).
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
//            description: List of storage volumes
//            items:
//              $ref: "#/definitions/StorageVolume"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes/{type} storage storage_pool_volumes_type_get
//
//  Get the storage volumes
//
//  Returns a list of storage volumes (URLs) (type specific endpoint).
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
//                "/1.0/storage-pools/local/volumes/custom/backups",
//                "/1.0/storage-pools/local/volumes/custom/images"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes/{type}?recursion=1 storage storage_pool_volumes_type_get_recursion1
//
//	Get the storage volumes
//
//	Returns a list of storage volumes (structs) (type specific endpoint).
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
//	          description: List of storage volumes
//	          items:
//	            $ref: "#/definitions/StorageVolume"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumesGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	targetMember := request.QueryParam(r, "target")
	memberSpecific := targetMember != ""

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the volume type.
	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	// Convert volume type name to internal integer representation if requested.
	var volumeType int
	if volumeTypeName != "" {
		volumeType, err = storagePools.VolumeTypeNameToDBType(volumeTypeName)
		if err != nil {
			return response.BadRequest(err)
		}
	}

	filterStr := r.FormValue("filter")
	clauses, err := filter.Parse(filterStr, filter.QueryOperatorSet())
	if err != nil {
		return response.SmartError(fmt.Errorf("Invalid filter: %w", err))
	}

	// Retrieve the storage pool (and check if the storage pool exists).
	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Detect project mode.
	requestProjectName := request.QueryParam(r, "project")
	allProjects := util.IsTrue(request.QueryParam(r, "all-projects"))

	if allProjects && requestProjectName != "" {
		return response.SmartError(api.StatusErrorf(http.StatusBadRequest, "Cannot specify a project when requesting all projects"))
	} else if !allProjects && requestProjectName == "" {
		requestProjectName = api.ProjectDefaultName
	}

	var dbVolumes []*db.StorageVolume
	var projectImages []string

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		var customVolProjectName string

		if !allProjects {
			dbProject, err := dbCluster.GetProject(ctx, tx.Tx(), requestProjectName)
			if err != nil {
				return err
			}

			p, err := dbProject.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			// The project name used for custom volumes varies based on whether the
			// project has the featues.storage.volumes feature enabled.
			customVolProjectName = project.StorageVolumeProjectFromRecord(p, db.StoragePoolVolumeTypeCustom)

			projectImages, err = tx.GetImagesFingerprints(ctx, requestProjectName, false)
			if err != nil {
				return err
			}
		}

		filters := make([]db.StorageVolumeFilter, 0)

		for i := range supportedVolumeTypes {
			supportedVolType := supportedVolumeTypes[i] // Local variable for use as pointer below.

			if volumeTypeName != "" && supportedVolType != volumeType {
				continue // Only include the requested type if specified.
			}

			switch supportedVolType {
			case db.StoragePoolVolumeTypeCustom:
				volTypeCustom := db.StoragePoolVolumeTypeCustom
				filter := db.StorageVolumeFilter{
					Type: &volTypeCustom,
				}

				if !allProjects {
					filter.Project = &customVolProjectName
				}

				filters = append(filters, filter)
			case db.StoragePoolVolumeTypeImage:
				// Image volumes are effectively a cache and are always linked to default project.
				// We filter the ones relevant to requested project below after the query has run.
				volTypeImage := db.StoragePoolVolumeTypeImage
				filters = append(filters, db.StorageVolumeFilter{
					Type: &volTypeImage,
				})
			default:
				// Include instance volume types using the specified project.
				filter := db.StorageVolumeFilter{
					Type: &supportedVolType,
				}

				if !allProjects {
					filter.Project = &requestProjectName
				}

				filters = append(filters, filter)
			}
		}

		dbVolumes, err = tx.GetStoragePoolVolumes(ctx, pool.ID(), memberSpecific, filters...)
		if err != nil {
			return fmt.Errorf("Failed loading storage volumes: %w", err)
		}

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Pre-fill UsedBy if using filtering.
	if clauses != nil && len(clauses.Clauses) > 0 {
		for i, vol := range dbVolumes {
			volumeUsedBy, err := storagePoolVolumeUsedByGet(s, requestProjectName, poolName, vol)
			if err != nil {
				return response.InternalError(err)
			}

			dbVolumes[i].UsedBy = project.FilterUsedBy(s.Authorizer, r, volumeUsedBy)
		}
	}

	// Filter the results.
	dbVolumes, err = filterVolumes(dbVolumes, clauses, allProjects, projectImages)
	if err != nil {
		return response.SmartError(err)
	}

	// Sort by type then volume name.
	sort.SliceStable(dbVolumes, func(i, j int) bool {
		volA := dbVolumes[i]
		volB := dbVolumes[j]

		if volA.Type != volB.Type {
			return dbVolumes[i].Type < dbVolumes[j].Type
		}

		return volA.Name < volB.Name
	})

	userHasPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), r, auth.EntitlementCanView, auth.ObjectTypeStorageVolume)
	if err != nil {
		return response.SmartError(err)
	}

	if localUtil.IsRecursionRequest(r) {
		volumes := make([]*api.StorageVolume, 0, len(dbVolumes))
		for _, dbVol := range dbVolumes {
			vol := &dbVol.StorageVolume

			var location string
			if s.ServerClustered && !pool.Driver().Info().Remote {
				location = vol.Location
			}

			volumeName, _, _ := api.GetParentAndSnapshotName(vol.Name)
			if !userHasPermission(auth.ObjectStorageVolume(vol.Project, poolName, dbVol.Type, volumeName, location)) {
				continue
			}

			// Fill in UsedBy if we haven't previously done so.
			if clauses == nil || len(clauses.Clauses) == 0 {
				volumeUsedBy, err := storagePoolVolumeUsedByGet(s, requestProjectName, poolName, dbVol)
				if err != nil {
					return response.InternalError(err)
				}

				vol.UsedBy = project.FilterUsedBy(s.Authorizer, r, volumeUsedBy)
			}

			volumes = append(volumes, vol)
		}

		return response.SyncResponse(true, volumes)
	}

	urls := make([]string, 0, len(dbVolumes))
	for _, dbVol := range dbVolumes {
		volumeName, _, _ := api.GetParentAndSnapshotName(dbVol.Name)

		var location string
		if s.ServerClustered && !pool.Driver().Info().Remote {
			location = dbVol.Location
		}

		if !userHasPermission(auth.ObjectStorageVolume(dbVol.Project, poolName, dbVol.Type, volumeName, location)) {
			continue
		}

		urls = append(urls, dbVol.StorageVolume.URL(version.APIVersion, poolName).String())
	}

	return response.SyncResponse(true, urls)
}

// filterVolumes returns a filtered list of volumes that match the given clauses.
func filterVolumes(volumes []*db.StorageVolume, clauses *filter.ClauseSet, allProjects bool, filterProjectImages []string) ([]*db.StorageVolume, error) {
	// FilterStorageVolume is for filtering purpose only.
	// It allows to filter snapshots by using default filter mechanism.
	type FilterStorageVolume struct {
		api.StorageVolume `yaml:",inline"`
		Snapshot          string `yaml:"snapshot"`
	}

	filtered := []*db.StorageVolume{}
	for _, volume := range volumes {
		// Filter out image volumes that are not used by this project.
		if volume.Type == db.StoragePoolVolumeTypeNameImage && !allProjects && !slices.Contains(filterProjectImages, volume.Name) {
			continue
		}

		tmpVolume := FilterStorageVolume{
			StorageVolume: volume.StorageVolume,
			Snapshot:      strconv.FormatBool(strings.Contains(volume.Name, internalInstance.SnapshotDelimiter)),
		}

		match, err := filter.Match(tmpVolume, *clauses)
		if err != nil {
			return nil, err
		}

		if !match {
			continue
		}

		filtered = append(filtered, volume)
	}

	return filtered, nil
}

// swagger:operation POST /1.0/storage-pools/{poolName}/volumes storage storage_pool_volumes_post
//
//	Add a storage volume
//
//	Creates a new storage volume.
//	Will return an empty sync response on simple volume creation but an operation on copy or migration.
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
//	    description: Storage volume
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StorageVolumesPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"

// swagger:operation POST /1.0/storage-pools/{poolName}/volumes/{type} storage storage_pool_volumes_type_post
//
//	Add a storage volume
//
//	Creates a new storage volume (type specific endpoint).
//	Will return an empty sync response on simple volume creation but an operation on copy or migration.
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
//	    description: Storage volume
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StorageVolumesPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumesPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	projectName, err := project.StorageVolumeProject(s.DB.Cluster, request.ProjectParam(r), db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	// If we're getting binary content, process separately.
	if r.Header.Get("Content-Type") == "application/octet-stream" {
		if r.Header.Get("X-Incus-type") == "iso" {
			return createStoragePoolVolumeFromISO(s, r, request.ProjectParam(r), projectName, r.Body, poolName, r.Header.Get("X-Incus-name"))
		}

		return createStoragePoolVolumeFromBackup(s, r, request.ProjectParam(r), projectName, r.Body, poolName, r.Header.Get("X-Incus-name"))
	}

	req := api.StorageVolumesPost{}

	// Parse the request.
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

	// Backward compatibility.
	if req.ContentType == "" {
		req.ContentType = db.StoragePoolVolumeContentTypeNameFS
	}

	_, err = storagePools.VolumeContentTypeNameToContentType(req.ContentType)
	if err != nil {
		return response.BadRequest(err)
	}

	// Handle being called through the typed URL.
	_, ok := mux.Vars(r)["type"]
	if ok {
		req.Type, err = url.PathUnescape(mux.Vars(r)["type"])
		if err != nil {
			return response.SmartError(err)
		}
	}

	// We currently only allow to create storage volumes of type storagePoolVolumeTypeCustom.
	// So check, that nothing else was requested.
	if req.Type != db.StoragePoolVolumeTypeNameCustom {
		return response.BadRequest(fmt.Errorf("Currently not allowed to create storage volumes of type %q", req.Type))
	}

	var poolID int64
	var dbVolume *db.StorageVolume

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		poolID, err = tx.GetStoragePoolID(ctx, poolName)
		if err != nil {
			return err
		}

		// Check if destination volume exists.
		dbVolume, err = tx.GetStoragePoolVolume(ctx, poolID, projectName, db.StoragePoolVolumeTypeCustom, req.Name, true)
		if err != nil && !response.IsNotFoundError(err) {
			return err
		}

		err = project.AllowVolumeCreation(tx, projectName, req)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	} else if dbVolume != nil && !req.Source.Refresh {
		return response.Conflict(fmt.Errorf("Volume by that name already exists"))
	}

	target := request.QueryParam(r, "target")

	// Check if we need to switch to migration
	serverName := s.ServerName
	var nodeAddress string

	if s.ServerClustered && target != "" && (req.Source.Location != "" && serverName != req.Source.Location) {
		err := s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			nodeInfo, err := tx.GetNodeByName(ctx, req.Source.Location)
			if err != nil {
				return err
			}

			nodeAddress = nodeInfo.Address

			return nil
		})
		if err != nil {
			return response.SmartError(err)
		}

		if nodeAddress == "" {
			return response.BadRequest(fmt.Errorf("The source is currently offline"))
		}

		return clusterCopyCustomVolumeInternal(s, r, nodeAddress, projectName, poolName, &req)
	}

	switch req.Source.Type {
	case "":
		return doVolumeCreateOrCopy(s, r, request.ProjectParam(r), projectName, poolName, &req)
	case "copy":
		if dbVolume != nil {
			return doCustomVolumeRefresh(s, r, request.ProjectParam(r), projectName, poolName, &req)
		}

		return doVolumeCreateOrCopy(s, r, request.ProjectParam(r), projectName, poolName, &req)
	case "migration":
		return doVolumeMigration(s, r, request.ProjectParam(r), projectName, poolName, &req)
	default:
		return response.BadRequest(fmt.Errorf("Unknown source type %q", req.Source.Type))
	}
}

func clusterCopyCustomVolumeInternal(s *state.State, r *http.Request, sourceAddress string, projectName string, poolName string, req *api.StorageVolumesPost) response.Response {
	websockets := map[string]string{}

	client, err := cluster.Connect(sourceAddress, s.Endpoints.NetworkCert(), s.ServerCert(), r, false)
	if err != nil {
		return response.SmartError(err)
	}

	client = client.UseProject(req.Source.Project)

	pullReq := api.StorageVolumePost{
		Name:       req.Source.Name,
		Pool:       req.Source.Pool,
		Migration:  true,
		VolumeOnly: req.Source.VolumeOnly,
		Project:    req.Source.Project,
		Source: api.StorageVolumeSource{
			Location: req.Source.Location,
		},
	}

	op, err := client.MigrateStoragePoolVolume(req.Source.Pool, pullReq)
	if err != nil {
		return response.SmartError(err)
	}

	opAPI := op.Get()

	for k, v := range opAPI.Metadata {
		websockets[k] = v.(string)
	}

	// Reset the source for a migration
	req.Source.Type = "migration"
	req.Source.Certificate = string(s.Endpoints.NetworkCert().PublicKey())
	req.Source.Mode = "pull"
	req.Source.Operation = fmt.Sprintf("https://%s/%s/operations/%s", sourceAddress, version.APIVersion, opAPI.ID)
	req.Source.Websockets = websockets
	req.Source.Project = ""

	return doVolumeMigration(s, r, req.Source.Project, projectName, poolName, req)
}

func doCustomVolumeRefresh(s *state.State, r *http.Request, requestProjectName string, projectName string, poolName string, req *api.StorageVolumesPost) response.Response {
	var run func(op *operations.Operation) error

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(err)
	}

	run = func(op *operations.Operation) error {
		revert := revert.New()
		defer revert.Fail()

		if req.Source.Name == "" {
			return fmt.Errorf("No source volume name supplied")
		}

		err = pool.RefreshCustomVolume(projectName, req.Source.Project, req.Name, req.Description, req.Config, req.Source.Pool, req.Source.Name, !req.Source.VolumeOnly, op)
		if err != nil {
			return err
		}

		revert.Success()
		return nil
	}

	op, err := operations.OperationCreate(s, requestProjectName, operations.OperationClassTask, operationtype.VolumeCopy, nil, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func doVolumeCreateOrCopy(s *state.State, r *http.Request, requestProjectName string, projectName string, poolName string, req *api.StorageVolumesPost) response.Response {
	var run func(op *operations.Operation) error

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(err)
	}

	volumeDBContentType, err := storagePools.VolumeContentTypeNameToContentType(req.ContentType)
	if err != nil {
		return response.SmartError(err)
	}

	contentType, err := storagePools.VolumeDBContentTypeToContentType(volumeDBContentType)
	if err != nil {
		return response.SmartError(err)
	}

	run = func(op *operations.Operation) error {
		if req.Source.Name == "" {
			// Use an empty operation for this sync response to pass the requestor
			op := &operations.Operation{}
			op.SetRequestor(r)
			return pool.CreateCustomVolume(projectName, req.Name, req.Description, req.Config, contentType, op)
		}

		return pool.CreateCustomVolumeFromCopy(projectName, req.Source.Project, req.Name, req.Description, req.Config, req.Source.Pool, req.Source.Name, !req.Source.VolumeOnly, op)
	}

	// If no source name supplied then this a volume create operation.
	if req.Source.Name == "" {
		err := run(nil)
		if err != nil {
			return response.SmartError(err)
		}

		return response.EmptySyncResponse
	}

	// Volume copy operations potentially take a long time, so run as an async operation.
	op, err := operations.OperationCreate(s, requestProjectName, operations.OperationClassTask, operationtype.VolumeCopy, nil, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func doVolumeMigration(s *state.State, r *http.Request, requestProjectName string, projectName string, poolName string, req *api.StorageVolumesPost) response.Response {
	// Validate migration mode
	if req.Source.Mode != "pull" && req.Source.Mode != "push" {
		return response.NotImplemented(fmt.Errorf("Mode '%s' not implemented", req.Source.Mode))
	}

	// create new certificate
	var err error
	var cert *x509.Certificate
	if req.Source.Certificate != "" {
		certBlock, _ := pem.Decode([]byte(req.Source.Certificate))
		if certBlock == nil {
			return response.InternalError(fmt.Errorf("Invalid certificate"))
		}

		cert, err = x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			return response.InternalError(err)
		}
	}

	config, err := localtls.GetTLSConfig(cert)
	if err != nil {
		return response.InternalError(err)
	}

	push := false
	if req.Source.Mode == "push" {
		push = true
	}

	// Initialise migrationArgs, don't set the Storage property yet, this is done in DoStorage,
	// to avoid this function relying on the legacy storage layer.
	migrationArgs := migrationSinkArgs{
		URL: req.Source.Operation,
		Dialer: &websocket.Dialer{
			TLSClientConfig:  config,
			NetDialContext:   localtls.RFC3493Dialer,
			HandshakeTimeout: time.Second * 5,
		},
		Secrets:    req.Source.Websockets,
		Push:       push,
		VolumeOnly: req.Source.VolumeOnly,
		Refresh:    req.Source.Refresh,
	}

	sink, err := newStorageMigrationSink(&migrationArgs)
	if err != nil {
		return response.InternalError(err)
	}

	resources := map[string][]api.URL{}
	resources["storage_volumes"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", poolName, "volumes", "custom", req.Name)}

	run := func(op *operations.Operation) error {
		// And finally run the migration.
		err = sink.DoStorage(s, projectName, poolName, req, op)
		if err != nil {
			logger.Error("Error during migration sink", logger.Ctx{"err": err})
			return fmt.Errorf("Error transferring storage volume: %s", err)
		}

		return nil
	}

	var op *operations.Operation
	if push {
		op, err = operations.OperationCreate(s, requestProjectName, operations.OperationClassWebsocket, operationtype.VolumeCreate, resources, sink.Metadata(), run, nil, sink.Connect, r)
		if err != nil {
			return response.InternalError(err)
		}
	} else {
		op, err = operations.OperationCreate(s, requestProjectName, operations.OperationClassTask, operationtype.VolumeCopy, resources, nil, run, nil, nil, r)
		if err != nil {
			return response.InternalError(err)
		}
	}

	return operations.OperationResponse(op)
}

// swagger:operation POST /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName} storage storage_pool_volume_type_post
//
//	Rename or move/migrate a storage volume
//
//	Renames, moves a storage volume between pools or migrates an instance to another server.
//
//	The returned operation metadata will vary based on what's requested.
//	For rename or move within the same server, this is a simple background operation with progress data.
//	For migration, in the push case, this will similarly be a background
//	operation with progress data, for the pull case, it will be a websocket
//	operation with a number of secrets to be passed to the target server.
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
//	    name: migration
//	    description: Migration request
//	    schema:
//	      $ref: "#/definitions/StorageVolumePost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumePost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the name of the storage volume.
	volumeName, err := url.PathUnescape(mux.Vars(r)["volumeName"])
	if err != nil {
		return response.SmartError(err)
	}

	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	if internalInstance.IsSnapshot(volumeName) {
		return response.BadRequest(fmt.Errorf("Invalid volume name"))
	}

	// Get the name of the storage pool the volume is supposed to be attached to.
	srcPoolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	req := api.StorageVolumePost{}

	// Parse the request.
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	if req.Name == "" {
		return response.BadRequest(fmt.Errorf("No name provided"))
	}

	// Check requested new volume name is not a snapshot volume.
	if internalInstance.IsSnapshot(req.Name) {
		return response.BadRequest(fmt.Errorf("Storage volume names may not contain slashes"))
	}

	// We currently only allow to create storage volumes of type storagePoolVolumeTypeCustom.
	// So check, that nothing else was requested.
	if volumeTypeName != db.StoragePoolVolumeTypeNameCustom {
		return response.BadRequest(fmt.Errorf("Renaming storage volumes of type %q is not allowed", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(s.DB.Cluster, request.ProjectParam(r), db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	targetProjectName := projectName
	if req.Project != "" {
		targetProjectName, err = project.StorageVolumeProject(s.DB.Cluster, req.Project, db.StoragePoolVolumeTypeCustom)
		if err != nil {
			return response.SmartError(err)
		}

		// Check whether the effective storage project differs from the requested target project.
		// If they do it means that the requested target project doesn't have features.storage.volumes
		// and this means that the volume would effectively be moved into the default project, and so we
		// require the user explicitly indicates this by targeting it directly.
		if targetProjectName != req.Project {
			return response.BadRequest(fmt.Errorf("Target project does not have features.storage.volumes enabled"))
		}

		if projectName == targetProjectName {
			return response.BadRequest(fmt.Errorf("Project and target project are the same"))
		}

		// Check if user has access to effective storage target project
		err := s.Authorizer.CheckPermission(r.Context(), r, auth.ObjectProject(targetProjectName), auth.EntitlementCanCreateStorageVolumes)
		if err != nil {
			return response.SmartError(err)
		}
	}

	// We need to restore the body of the request since it has already been read, and if we
	// forwarded it now no body would be written out.
	buf := bytes.Buffer{}
	err = json.NewEncoder(&buf).Encode(req)
	if err != nil {
		return response.SmartError(err)
	}

	r.Body = internalIO.BytesReadCloser{Buf: &buf}

	target := request.QueryParam(r, "target")

	// Check if clustered.
	if s.ServerClustered && target != "" && req.Source.Location != "" && req.Migration {
		var sourceNodeOffline bool

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Load source node.
			nodeInfo, err := tx.GetNodeByName(ctx, req.Source.Location)
			if err != nil {
				return err
			}

			sourceAddress := nodeInfo.Address

			if sourceAddress == "" {
				// Local node.
				sourceNodeOffline = false
				return nil
			}

			sourceMemberInfo, err := tx.GetNodeByAddress(ctx, sourceAddress)
			if err != nil {
				return fmt.Errorf("Failed to get source member for %q: %w", sourceAddress, err)
			}

			sourceNodeOffline = sourceMemberInfo.IsOffline(s.GlobalConfig.OfflineThreshold())

			return nil
		})
		if err != nil {
			return response.SmartError(err)
		}

		var targetProject *api.Project
		var targetMemberInfo *db.NodeInfo

		if sourceNodeOffline {
			resp := forwardedResponseIfTargetIsRemote(s, r)
			if resp != nil {
				return resp
			}

			srcPool, err := storagePools.LoadByName(s, srcPoolName)
			if err != nil {
				return response.SmartError(err)
			}

			if srcPool.Driver().Info().Remote {
				var dbVolume *db.StorageVolume
				var volumeNotFound bool
				var targetIsSet bool

				err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
					// Load source volume.
					srcPoolID, err := tx.GetStoragePoolID(ctx, srcPoolName)
					if err != nil {
						return err
					}

					dbVolume, err = tx.GetStoragePoolVolume(ctx, srcPoolID, projectName, db.StoragePoolVolumeTypeCustom, volumeName, true)
					if err != nil {
						// Check if the user provided an incorrect target query parameter and return a helpful error message.
						_, volumeNotFound = api.StatusErrorMatch(err, http.StatusNotFound)
						targetIsSet = r.URL.Query().Get("target") != ""

						return err
					}

					return nil
				})
				if err != nil {
					if s.ServerClustered && targetIsSet && volumeNotFound {
						return response.NotFound(fmt.Errorf("Storage volume not found on this cluster member"))
					}

					return response.SmartError(err)
				}

				req := api.StorageVolumePost{
					Name: req.Name,
				}

				return storagePoolVolumeTypePostRename(s, r, srcPool.Name(), projectName, &dbVolume.StorageVolume, req)
			}
		} else {
			resp := forwardedResponseToNode(s, r, req.Source.Location)
			if resp != nil {
				return resp
			}
		}

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			p, err := dbCluster.GetProject(ctx, tx.Tx(), projectName)
			if err != nil {
				return err
			}

			targetProject, err = p.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			allMembers, err := tx.GetNodes(ctx)
			if err != nil {
				return fmt.Errorf("Failed getting cluster members: %w", err)
			}

			targetMemberInfo, _, err = project.CheckTarget(ctx, s.Authorizer, r, tx, targetProject, target, allMembers)
			if err != nil {
				return err
			}

			if targetMemberInfo == nil {
				return fmt.Errorf("Failed checking cluster member %q", target)
			}

			return nil
		})
		if err != nil {
			return response.SmartError(err)
		}

		if targetMemberInfo.IsOffline(s.GlobalConfig.OfflineThreshold()) {
			return response.BadRequest(fmt.Errorf("Target cluster member is offline"))
		}

		run := func(op *operations.Operation) error {
			return migrateStorageVolume(s, r, volumeName, srcPoolName, targetMemberInfo.Name, targetProjectName, req, op)
		}

		resources := map[string][]api.URL{}
		resources["storage_volumes"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", srcPoolName, "volumes", "custom", volumeName)}

		op, err := operations.OperationCreate(s, projectName, operations.OperationClassTask, operationtype.VolumeMigrate, resources, nil, run, nil, nil, r)
		if err != nil {
			return response.InternalError(err)
		}

		return operations.OperationResponse(op)
	}

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// If source is set, we know the source and the target, and therefore don't need this function to figure out where to forward the request to.
	if req.Source.Location == "" {
		resp = forwardedResponseIfVolumeIsRemote(s, r, srcPoolName, projectName, volumeName, volumeType)
		if resp != nil {
			return resp
		}
	}

	// This is a migration request so send back requested secrets.
	if req.Migration {
		return storagePoolVolumeTypePostMigration(s, r, request.ProjectParam(r), projectName, srcPoolName, volumeName, req)
	}

	// Retrieve ID of the storage pool (and check if the storage pool exists).
	var targetPoolID int64
	var targetPoolName string

	if req.Pool != "" {
		targetPoolName = req.Pool
	} else {
		targetPoolName = srcPoolName
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		targetPoolID, err = tx.GetStoragePoolID(ctx, targetPoolName)

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Check that the name isn't already in use.
		_, err = tx.GetStoragePoolNodeVolumeID(ctx, targetProjectName, req.Name, volumeType, targetPoolID)

		return err
	})
	if !response.IsNotFoundError(err) {
		if err != nil {
			return response.InternalError(err)
		}

		return response.Conflict(fmt.Errorf("Volume by that name already exists"))
	}

	// Check if the daemon itself is using it.
	used, err := storagePools.VolumeUsedByDaemon(s, srcPoolName, volumeName)
	if err != nil {
		return response.SmartError(err)
	}

	if used {
		return response.SmartError(fmt.Errorf("Volume is used by Incus itself and cannot be renamed"))
	}

	var dbVolume *db.StorageVolume
	var volumeNotFound bool
	var targetIsSet bool

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Load source volume.
		srcPoolID, err := tx.GetStoragePoolID(ctx, srcPoolName)
		if err != nil {
			return err
		}

		dbVolume, err = tx.GetStoragePoolVolume(ctx, srcPoolID, projectName, volumeType, volumeName, true)
		if err != nil {
			// Check if the user provided an incorrect target query parameter and return a helpful error message.
			_, volumeNotFound = api.StatusErrorMatch(err, http.StatusNotFound)
			targetIsSet = r.URL.Query().Get("target") != ""

			return err
		}

		return nil
	})
	if err != nil {
		if s.ServerClustered && targetIsSet && volumeNotFound {
			return response.NotFound(fmt.Errorf("Storage volume not found on this cluster member"))
		}

		return response.SmartError(err)
	}

	// Check if a running instance is using it.
	err = storagePools.VolumeUsedByInstanceDevices(s, srcPoolName, projectName, &dbVolume.StorageVolume, true, func(dbInst db.InstanceArgs, project api.Project, usedByDevices []string) error {
		inst, err := instance.Load(s, dbInst, project)
		if err != nil {
			return err
		}

		if inst.IsRunning() {
			return fmt.Errorf("Volume is still in use by running instances")
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Detect a rename request.
	if (req.Pool == "" || req.Pool == srcPoolName) && (projectName == targetProjectName) {
		return storagePoolVolumeTypePostRename(s, r, srcPoolName, projectName, &dbVolume.StorageVolume, req)
	}

	// Otherwise this is a move request.
	return storagePoolVolumeTypePostMove(s, r, srcPoolName, projectName, targetProjectName, &dbVolume.StorageVolume, req)
}

func migrateStorageVolume(s *state.State, r *http.Request, sourceVolumeName string, sourcePoolName string, targetNode string, projectName string, req api.StorageVolumePost, op *operations.Operation) error {
	if targetNode == req.Source.Location {
		return fmt.Errorf("Target must be different than storage volumes' current location")
	}

	var err error
	var srcMember, newMember db.NodeInfo

	// If the source member is online then get its address so we can connect to it and see if the
	// instance is running later.
	err = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		srcMember, err = tx.GetNodeByName(ctx, req.Source.Location)
		if err != nil {
			return fmt.Errorf("Failed getting current cluster member of storage volume %q", req.Source.Name)
		}

		newMember, err = tx.GetNodeByName(ctx, targetNode)
		if err != nil {
			return fmt.Errorf("Failed loading new cluster member for storage volume: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	srcPool, err := storagePools.LoadByName(s, sourcePoolName)
	if err != nil {
		return fmt.Errorf("Failed loading storage volume storage pool: %w", err)
	}

	f, err := storageVolumePostClusteringMigrate(s, r, srcPool, projectName, sourceVolumeName, req.Pool, req.Project, req.Name, srcMember, newMember, req.VolumeOnly)
	if err != nil {
		return err
	}

	return f(op)
}

func storageVolumePostClusteringMigrate(s *state.State, r *http.Request, srcPool storagePools.Pool, srcProjectName string, srcVolumeName string, newPoolName string, newProjectName string, newVolumeName string, srcMember db.NodeInfo, newMember db.NodeInfo, volumeOnly bool) (func(op *operations.Operation) error, error) {
	srcMemberOffline := srcMember.IsOffline(s.GlobalConfig.OfflineThreshold())

	// Make sure that the source member is online if we end up being called from another member after a
	// redirection due to the source member being offline.
	if srcMemberOffline {
		return nil, fmt.Errorf("The cluster member hosting the storage volume is offline")
	}

	run := func(op *operations.Operation) error {
		if newVolumeName == "" {
			newVolumeName = srcVolumeName
		}

		networkCert := s.Endpoints.NetworkCert()

		// Connect to the destination member, i.e. the member to migrate the custom volume to.
		// Use the notify argument to indicate to the destination that we are moving a custom volume between
		// cluster members.
		dest, err := cluster.Connect(newMember.Address, networkCert, s.ServerCert(), r, true)
		if err != nil {
			return fmt.Errorf("Failed to connect to destination server %q: %w", newMember.Address, err)
		}

		dest = dest.UseTarget(newMember.Name).UseProject(srcProjectName)

		resources := map[string][]api.URL{}
		resources["storage_volumes"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", srcPool.Name(), "volumes", "custom", srcVolumeName)}

		srcMigration, err := newStorageMigrationSource(volumeOnly, nil)
		if err != nil {
			return fmt.Errorf("Failed setting up storage volume migration on source: %w", err)
		}

		run := func(op *operations.Operation) error {
			err := srcMigration.DoStorage(s, srcProjectName, srcPool.Name(), srcVolumeName, op)
			if err != nil {
				return err
			}

			err = srcPool.DeleteCustomVolume(srcProjectName, srcVolumeName, op)
			if err != nil {
				return err
			}

			return nil
		}

		cancel := func(op *operations.Operation) error {
			srcMigration.disconnect()
			return nil
		}

		srcOp, err := operations.OperationCreate(s, srcProjectName, operations.OperationClassWebsocket, operationtype.VolumeMigrate, resources, srcMigration.Metadata(), run, cancel, srcMigration.Connect, r)
		if err != nil {
			return err
		}

		err = srcOp.Start()
		if err != nil {
			return fmt.Errorf("Failed starting migration source operation: %w", err)
		}

		sourceSecrets := make(map[string]string, len(srcMigration.conns))
		for connName, conn := range srcMigration.conns {
			sourceSecrets[connName] = conn.Secret()
		}

		// Request pull mode migration on destination.
		err = dest.CreateStoragePoolVolume(newPoolName, api.StorageVolumesPost{
			Name: newVolumeName,
			Type: "custom",
			Source: api.StorageVolumeSource{
				Type:        "migration",
				Mode:        "pull",
				Operation:   fmt.Sprintf("https://%s%s", srcMember.Address, srcOp.URL()),
				Websockets:  sourceSecrets,
				Certificate: string(networkCert.PublicKey()),
				Name:        newVolumeName,
				Pool:        newPoolName,
				Project:     newProjectName,
			},
		})
		if err != nil {
			return fmt.Errorf("Failed requesting instance create on destination: %w", err)
		}

		return nil
	}

	return run, nil
}

// storagePoolVolumeTypePostMigration handles volume migration type POST requests.
func storagePoolVolumeTypePostMigration(state *state.State, r *http.Request, requestProjectName string, projectName string, poolName string, volumeName string, req api.StorageVolumePost) response.Response {
	ws, err := newStorageMigrationSource(req.VolumeOnly, req.Target)
	if err != nil {
		return response.InternalError(err)
	}

	resources := map[string][]api.URL{}
	srcVolParentName, srcVolSnapName, srcIsSnapshot := api.GetParentAndSnapshotName(volumeName)
	if srcIsSnapshot {
		resources["storage_volume_snapshots"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", poolName, "volumes", "custom", srcVolParentName, "snapshots", srcVolSnapName)}
	} else {
		resources["storage_volumes"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", poolName, "volumes", "custom", volumeName)}
	}

	run := func(op *operations.Operation) error {
		return ws.DoStorage(state, projectName, poolName, volumeName, op)
	}

	if req.Target != nil {
		// Push mode.
		op, err := operations.OperationCreate(state, requestProjectName, operations.OperationClassTask, operationtype.VolumeMigrate, resources, nil, run, nil, nil, r)
		if err != nil {
			return response.InternalError(err)
		}

		return operations.OperationResponse(op)
	}

	// Pull mode.
	op, err := operations.OperationCreate(state, requestProjectName, operations.OperationClassWebsocket, operationtype.VolumeMigrate, resources, ws.Metadata(), run, nil, ws.Connect, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// storagePoolVolumeTypePostRename handles volume rename type POST requests.
func storagePoolVolumeTypePostRename(s *state.State, r *http.Request, poolName string, projectName string, vol *api.StorageVolume, req api.StorageVolumePost) response.Response {
	newVol := *vol
	newVol.Name = req.Name

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(err)
	}

	revert := revert.New()
	defer revert.Fail()

	// Update devices using the volume in instances and profiles.
	err = storagePoolVolumeUpdateUsers(r.Context(), s, projectName, pool.Name(), vol, pool.Name(), &newVol)
	if err != nil {
		return response.SmartError(err)
	}

	// Use an empty operation for this sync response to pass the requestor
	op := &operations.Operation{}
	op.SetRequestor(r)

	err = pool.RenameCustomVolume(projectName, vol.Name, req.Name, op)
	if err != nil {
		return response.SmartError(err)
	}

	revert.Success()

	u := api.NewURL().Path(version.APIVersion, "storage-pools", pool.Name(), "volumes", db.StoragePoolVolumeTypeNameCustom, req.Name).Project(projectName)

	return response.SyncResponseLocation(true, nil, u.String())
}

// storagePoolVolumeTypePostMove handles volume move type POST requests.
func storagePoolVolumeTypePostMove(s *state.State, r *http.Request, poolName string, requestProjectName string, projectName string, vol *api.StorageVolume, req api.StorageVolumePost) response.Response {
	newVol := *vol
	newVol.Name = req.Name

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(err)
	}

	newPool, err := storagePools.LoadByName(s, req.Pool)
	if err != nil {
		return response.SmartError(err)
	}

	run := func(op *operations.Operation) error {
		revert := revert.New()
		defer revert.Fail()

		// Update devices using the volume in instances and profiles.
		err = storagePoolVolumeUpdateUsers(context.TODO(), s, requestProjectName, pool.Name(), vol, newPool.Name(), &newVol)
		if err != nil {
			return err
		}

		revert.Add(func() {
			_ = storagePoolVolumeUpdateUsers(context.TODO(), s, projectName, newPool.Name(), &newVol, pool.Name(), vol)
		})

		// Provide empty description and nil config to instruct CreateCustomVolumeFromCopy to copy it
		// from source volume.
		err = newPool.CreateCustomVolumeFromCopy(projectName, requestProjectName, newVol.Name, "", nil, pool.Name(), vol.Name, true, op)
		if err != nil {
			return err
		}

		err = pool.DeleteCustomVolume(requestProjectName, vol.Name, op)
		if err != nil {
			return err
		}

		revert.Success()
		return nil
	}

	op, err := operations.OperationCreate(s, requestProjectName, operations.OperationClassTask, operationtype.VolumeMove, nil, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName} storage storage_pool_volume_type_get
//
//	Get the storage volume
//
//	Gets a specific storage volume.
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
//	    description: Storage volume
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
//	          $ref: "#/definitions/StorageVolume"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage volume.
	volumeName, err := url.PathUnescape(mux.Vars(r)["volumeName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is valid.
	if !slices.Contains(supportedVolumeTypes, volumeType) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	requestProjectName := request.ProjectParam(r)
	volumeProjectName, err := project.StorageVolumeProject(s.DB.Cluster, requestProjectName, volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(s, r, poolName, volumeProjectName, volumeName, volumeType)
	if resp != nil {
		return resp
	}

	var dbVolume *db.StorageVolume

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the ID of the storage pool the storage volume is supposed to be attached to.
		poolID, err := tx.GetStoragePoolID(ctx, poolName)
		if err != nil {
			return err
		}

		// Get the storage volume.
		dbVolume, err = tx.GetStoragePoolVolume(ctx, poolID, volumeProjectName, volumeType, volumeName, true)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	volumeUsedBy, err := storagePoolVolumeUsedByGet(s, requestProjectName, poolName, dbVolume)
	if err != nil {
		return response.SmartError(err)
	}

	dbVolume.UsedBy = project.FilterUsedBy(s.Authorizer, r, volumeUsedBy)

	etag := []any{volumeName, dbVolume.Type, dbVolume.Config}

	return response.SyncResponseETag(true, dbVolume.StorageVolume, etag)
}

// swagger:operation PUT /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName} storage storage_pool_volume_type_put
//
//	Update the storage volume
//
//	Updates the entire storage volume configuration.
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
//	    name: storage volume
//	    description: Storage volume configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StorageVolumePut"
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
func storagePoolVolumePut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)
	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage volume.
	volumeName, err := url.PathUnescape(mux.Vars(r)["volumeName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	projectName, err = project.StorageVolumeProject(s.DB.Cluster, projectName, volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Check that the storage volume type is valid.
	if !slices.Contains(supportedVolumeTypes, volumeType) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(s, r, pool.Name(), projectName, volumeName, volumeType)
	if resp != nil {
		return resp
	}

	// Get the existing storage volume.
	var dbVolume *db.StorageVolume
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbVolume, err = tx.GetStoragePoolVolume(ctx, pool.ID(), projectName, volumeType, volumeName, true)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag
	etag := []any{volumeName, dbVolume.Type, dbVolume.Config}

	err = localUtil.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.StorageVolumePut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Use an empty operation for this sync response to pass the requestor
	op := &operations.Operation{}
	op.SetRequestor(r)

	if volumeType == db.StoragePoolVolumeTypeCustom {
		// Restore custom volume from snapshot if requested. This should occur first
		// before applying config changes so that changes are applied to the
		// restored volume.
		if req.Restore != "" {
			err = pool.RestoreCustomVolume(projectName, dbVolume.Name, req.Restore, op)
			if err != nil {
				return response.SmartError(err)
			}
		}

		// Handle custom volume update requests.
		// Only apply changes during a snapshot restore if a non-nil config is supplied to avoid clearing
		// the volume's config if only restoring snapshot.
		if req.Config != nil || req.Restore == "" {
			// Possibly check if project limits are honored.
			err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
				return project.AllowVolumeUpdate(tx, projectName, volumeName, req, dbVolume.Config)
			})
			if err != nil {
				return response.SmartError(err)
			}

			err = pool.UpdateCustomVolume(projectName, dbVolume.Name, req.Description, req.Config, op)
			if err != nil {
				return response.SmartError(err)
			}
		}
	} else if volumeType == db.StoragePoolVolumeTypeContainer || volumeType == db.StoragePoolVolumeTypeVM {
		inst, err := instance.LoadByProjectAndName(s, projectName, dbVolume.Name)
		if err != nil {
			return response.SmartError(err)
		}

		// Handle instance volume update requests.
		err = pool.UpdateInstance(inst, req.Description, req.Config, op)
		if err != nil {
			return response.SmartError(err)
		}
	} else if volumeType == db.StoragePoolVolumeTypeImage {
		// Handle image update requests.
		err = pool.UpdateImage(dbVolume.Name, req.Description, req.Config, op)
		if err != nil {
			return response.SmartError(err)
		}
	} else {
		return response.SmartError(fmt.Errorf("Invalid volume type"))
	}

	return response.EmptySyncResponse
}

// swagger:operation PATCH /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName} storage storage_pool_volume_type_patch
//
//	Partially update the storage volume
//
//	Updates a subset of the storage volume configuration.
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
//	    name: storage volume
//	    description: Storage volume configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/StorageVolumePut"
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
func storagePoolVolumePatch(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the name of the storage volume.
	volumeName, err := url.PathUnescape(mux.Vars(r)["volumeName"])
	if err != nil {
		return response.SmartError(err)
	}

	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	if internalInstance.IsSnapshot(volumeName) {
		return response.BadRequest(fmt.Errorf("Invalid volume name"))
	}

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
	if err != nil {
		return response.SmartError(err)
	}

	// Convert the volume type name to our internal integer representation.
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check that the storage volume type is custom.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	projectName, err := project.StorageVolumeProject(s.DB.Cluster, request.ProjectParam(r), volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(err)
	}

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(s, r, pool.Name(), projectName, volumeName, volumeType)
	if resp != nil {
		return resp
	}

	// Get the existing storage volume.
	var dbVolume *db.StorageVolume
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbVolume, err = tx.GetStoragePoolVolume(ctx, pool.ID(), projectName, volumeType, volumeName, true)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag.
	etag := []any{volumeName, dbVolume.Type, dbVolume.Config}

	err = localUtil.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	req := api.StorageVolumePut{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Config == nil {
		req.Config = map[string]string{}
	}

	// Merge current config with requested changes.
	for k, v := range dbVolume.Config {
		_, ok := req.Config[k]
		if !ok {
			req.Config[k] = v
		}
	}

	// Use an empty operation for this sync response to pass the requestor
	op := &operations.Operation{}
	op.SetRequestor(r)

	err = pool.UpdateCustomVolume(projectName, dbVolume.Name, req.Description, req.Config, op)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// swagger:operation DELETE /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName} storage storage_pool_volume_type_delete
//
//	Delete the storage volume
//
//	Removes the storage volume.
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
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Get the name of the storage volume.
	volumeName, err := url.PathUnescape(mux.Vars(r)["volumeName"])
	if err != nil {
		return response.SmartError(err)
	}

	volumeTypeName, err := url.PathUnescape(mux.Vars(r)["type"])
	if err != nil {
		return response.SmartError(err)
	}

	if internalInstance.IsSnapshot(volumeName) {
		return response.BadRequest(fmt.Errorf("Invalid storage volume %q", volumeName))
	}

	// Get the name of the storage pool the volume is supposed to be attached to.
	poolName, err := url.PathUnescape(mux.Vars(r)["poolName"])
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

	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	resp = forwardedResponseIfVolumeIsRemote(s, r, poolName, volumeProjectName, volumeName, volumeType)
	if resp != nil {
		return resp
	}

	if volumeType != db.StoragePoolVolumeTypeCustom && volumeType != db.StoragePoolVolumeTypeImage {
		return response.BadRequest(fmt.Errorf("Storage volumes of type %q cannot be deleted with the storage API", volumeTypeName))
	}

	// Get the storage pool the storage volume is supposed to be attached to.
	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Get the storage volume.
	var dbVolume *db.StorageVolume
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbVolume, err = tx.GetStoragePoolVolume(ctx, pool.ID(), volumeProjectName, volumeType, volumeName, true)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	volumeUsedBy, err := storagePoolVolumeUsedByGet(s, requestProjectName, poolName, dbVolume)
	if err != nil {
		return response.SmartError(err)
	}

	// isImageURL checks whether the provided usedByURL represents an image resource for the fingerprint.
	isImageURL := func(usedByURL string, fingerprint string) bool {
		usedBy, _ := url.Parse(usedByURL)
		if usedBy == nil {
			return false
		}

		img := api.NewURL().Path(version.APIVersion, "images", fingerprint)
		return usedBy.Path == img.URL.Path
	}

	if len(volumeUsedBy) > 0 {
		if len(volumeUsedBy) != 1 || volumeType != db.StoragePoolVolumeTypeImage || !isImageURL(volumeUsedBy[0], dbVolume.Name) {
			return response.BadRequest(fmt.Errorf("The storage volume is still in use"))
		}
	}

	// Use an empty operation for this sync response to pass the requestor
	op := &operations.Operation{}
	op.SetRequestor(r)

	switch volumeType {
	case db.StoragePoolVolumeTypeCustom:
		err = pool.DeleteCustomVolume(volumeProjectName, volumeName, op)
	case db.StoragePoolVolumeTypeImage:
		err = pool.DeleteImage(volumeName, op)
	default:
		return response.BadRequest(fmt.Errorf(`Storage volumes of type %q cannot be deleted with the storage API`, volumeTypeName))
	}

	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func createStoragePoolVolumeFromISO(s *state.State, r *http.Request, requestProjectName string, projectName string, data io.Reader, pool string, volName string) response.Response {
	revert := revert.New()
	defer revert.Fail()

	if volName == "" {
		return response.BadRequest(fmt.Errorf("Missing volume name"))
	}

	// Create isos directory if needed.
	if !util.PathExists(internalUtil.VarPath("isos")) {
		err := os.MkdirAll(internalUtil.VarPath("isos"), 0644)
		if err != nil {
			return response.InternalError(err)
		}
	}

	// Create temporary file to store uploaded ISO data.
	isoFile, err := os.CreateTemp(internalUtil.VarPath("isos"), fmt.Sprintf("%s_", "incus_iso"))
	if err != nil {
		return response.InternalError(err)
	}

	defer func() { _ = os.Remove(isoFile.Name()) }()
	revert.Add(func() { _ = isoFile.Close() })

	// Stream uploaded ISO data into temporary file.
	size, err := io.Copy(isoFile, data)
	if err != nil {
		return response.InternalError(err)
	}

	// Copy reverter so far so we can use it inside run after this function has finished.
	runRevert := revert.Clone()

	run := func(op *operations.Operation) error {
		defer func() { _ = isoFile.Close() }()
		defer runRevert.Fail()

		pool, err := storagePools.LoadByName(s, pool)
		if err != nil {
			return err
		}

		// Dump ISO to storage.
		err = pool.CreateCustomVolumeFromISO(projectName, volName, isoFile, size, op)
		if err != nil {
			return fmt.Errorf("Failed creating custom volume from ISO: %w", err)
		}

		runRevert.Success()
		return nil
	}

	resources := map[string][]api.URL{}
	resources["storage_volumes"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", pool, "volumes", "custom", volName)}

	op, err := operations.OperationCreate(s, requestProjectName, operations.OperationClassTask, operationtype.VolumeCreate, resources, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	revert.Success()
	return operations.OperationResponse(op)
}

func createStoragePoolVolumeFromBackup(s *state.State, r *http.Request, requestProjectName string, projectName string, data io.Reader, pool string, volName string) response.Response {
	revert := revert.New()
	defer revert.Fail()

	// Create temporary file to store uploaded backup data.
	backupFile, err := os.CreateTemp(internalUtil.VarPath("backups"), fmt.Sprintf("%s_", backup.WorkingDirPrefix))
	if err != nil {
		return response.InternalError(err)
	}

	defer func() { _ = os.Remove(backupFile.Name()) }()
	revert.Add(func() { _ = backupFile.Close() })

	// Stream uploaded backup data into temporary file.
	_, err = io.Copy(backupFile, data)
	if err != nil {
		return response.InternalError(err)
	}

	// Detect squashfs compression and convert to tarball.
	_, err = backupFile.Seek(0, io.SeekStart)
	if err != nil {
		return response.InternalError(err)
	}

	_, algo, decomArgs, err := archive.DetectCompressionFile(backupFile)
	if err != nil {
		return response.InternalError(err)
	}

	if algo == ".squashfs" {
		// Pass the temporary file as program argument to the decompression command.
		decomArgs := append(decomArgs, backupFile.Name())

		// Create temporary file to store the decompressed tarball in.
		tarFile, err := os.CreateTemp(internalUtil.VarPath("backups"), fmt.Sprintf("%s_decompress_", backup.WorkingDirPrefix))
		if err != nil {
			return response.InternalError(err)
		}

		defer func() { _ = os.Remove(tarFile.Name()) }()

		// Decompress to tarFile temporary file.
		err = archive.ExtractWithFds(decomArgs[0], decomArgs[1:], nil, nil, tarFile)
		if err != nil {
			return response.InternalError(err)
		}

		// We don't need the original squashfs file anymore.
		_ = backupFile.Close()
		_ = os.Remove(backupFile.Name())

		// Replace the backup file handle with the handle to the tar file.
		backupFile = tarFile
	}

	// Parse the backup information.
	_, err = backupFile.Seek(0, io.SeekStart)
	if err != nil {
		return response.InternalError(err)
	}

	logger.Debug("Reading backup file info")
	bInfo, err := backup.GetInfo(backupFile, s.OS, backupFile.Name())
	if err != nil {
		return response.BadRequest(err)
	}

	bInfo.Project = projectName

	// Override pool.
	if pool != "" {
		bInfo.Pool = pool
	}

	// Override volume name.
	if volName != "" {
		bInfo.Name = volName
	}

	logger.Debug("Backup file info loaded", logger.Ctx{
		"type":      bInfo.Type,
		"name":      bInfo.Name,
		"project":   bInfo.Project,
		"backend":   bInfo.Backend,
		"pool":      bInfo.Pool,
		"optimized": *bInfo.OptimizedStorage,
		"snapshots": bInfo.Snapshots,
	})

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Check storage pool exists.
		_, _, _, err = tx.GetStoragePoolInAnyState(ctx, bInfo.Pool)

		return err
	})
	if response.IsNotFoundError(err) {
		// The storage pool doesn't exist. If backup is in binary format (so we cannot alter
		// the backup.yaml) or the pool has been specified directly from the user restoring
		// the backup then we cannot proceed so return an error.
		if *bInfo.OptimizedStorage || pool != "" {
			return response.InternalError(fmt.Errorf("Storage pool not found: %w", err))
		}

		var profile *api.Profile

		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Otherwise try and restore to the project's default profile pool.
			_, profile, err = tx.GetProfile(ctx, bInfo.Project, "default")

			return err
		})
		if err != nil {
			return response.InternalError(fmt.Errorf("Failed to get default profile: %w", err))
		}

		_, v, err := internalInstance.GetRootDiskDevice(profile.Devices)
		if err != nil {
			return response.InternalError(fmt.Errorf("Failed to get root disk device: %w", err))
		}

		// Use the default-profile's root pool.
		bInfo.Pool = v["pool"]
	} else if err != nil {
		return response.InternalError(err)
	}

	// Copy reverter so far so we can use it inside run after this function has finished.
	runRevert := revert.Clone()

	run := func(op *operations.Operation) error {
		defer func() { _ = backupFile.Close() }()
		defer runRevert.Fail()

		pool, err := storagePools.LoadByName(s, bInfo.Pool)
		if err != nil {
			return err
		}

		// Check if the backup is optimized that the source pool driver matches the target pool driver.
		if *bInfo.OptimizedStorage && pool.Driver().Info().Name != bInfo.Backend {
			return fmt.Errorf("Optimized backup storage driver %q differs from the target storage pool driver %q", bInfo.Backend, pool.Driver().Info().Name)
		}

		// Dump tarball to storage.
		err = pool.CreateCustomVolumeFromBackup(*bInfo, backupFile, nil)
		if err != nil {
			return fmt.Errorf("Create custom volume from backup: %w", err)
		}

		runRevert.Success()
		return nil
	}

	resources := map[string][]api.URL{}
	resources["storage_volumes"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", bInfo.Pool, "volumes", string(bInfo.Type), bInfo.Name)}

	op, err := operations.OperationCreate(s, requestProjectName, operations.OperationClassTask, operationtype.CustomVolumeBackupRestore, resources, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	revert.Success()
	return operations.OperationResponse(op)
}
