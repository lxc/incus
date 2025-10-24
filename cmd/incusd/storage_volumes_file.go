package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"

	"github.com/gorilla/mux"

	"github.com/lxc/incus/v6/internal/server/lifecycle"
	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/internal/server/request"
	"github.com/lxc/incus/v6/internal/server/response"
	"github.com/lxc/incus/v6/internal/server/state"
	storagePools "github.com/lxc/incus/v6/internal/server/storage"
	storageDrivers "github.com/lxc/incus/v6/internal/server/storage/drivers"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/revert"
)

func storagePoolVolumeTypeFileHandler(d *Daemon, r *http.Request) response.Response {
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

	// Redirect to correct server if needed.
	resp := forwardedResponseIfVolumeIsRemote(s, r, poolName, volumeProjectName, volumeName, volumeType)
	if resp != nil {
		return resp
	}

	if resp != nil {
		return resp
	}

	// Load the storage volume.
	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(err)
	}

	volumeDB, err := storagePools.VolumeDBGet(pool, volumeProjectName, volumeName, storageDrivers.VolumeTypeCustom)
	if err != nil {
		return response.SmartError(err)
	}

	diskVolName := project.StorageVolume(volumeProjectName, volumeName)
	vol := pool.GetVolume(storageDrivers.VolumeTypeCustom, storageDrivers.ContentTypeFS, diskVolName, volumeDB.Config)

	// Parse the path.
	path := r.FormValue("path")
	if path == "" {
		return response.BadRequest(errors.New("Missing path argument"))
	}

	switch r.Method {
	case "GET":
		return storageVolumeFileGet(s, vol, volumeProjectName, path, r)
	case "HEAD":
		return storageVolumeFileHead(s, vol, volumeProjectName, path, r)
	case "DELETE":
		return storageVolumeFileDelete(s, vol, volumeProjectName, path, r)
	case "POST":
		return storageVolumeFilePost(s, vol, volumeProjectName, path, r)
	default:
		return response.NotFound(fmt.Errorf("Method %q not found", r.Method))
	}
}

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/files storage storage_pool_volume_type_files_get
//
//	Get a file
//
//	Gets the file content. If it's a directory, a json list of files will be returned instead.
//
//	---
//	produces:
//	  - application/json
//	  - application/octet-stream
//	parameters:
//	  - in: query
//	    name: path
//	    description: Path to the file
//	    type: string
//	    example: default
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	     description: Raw file or directory listing
//	     headers:
//	       X-Incus-uid:
//	         description: File owner UID
//	         schema:
//	           type: integer
//	       X-Incus-gid:
//	         description: File owner GID
//	         schema:
//	           type: integer
//	       X-Incus-mode:
//	         description: Mode mask
//	         schema:
//	           type: integer
//	       X-Incus-modified:
//	         description: Last modified date
//	         schema:
//	           type: string
//	       X-Incus-type:
//	         description: Type of file (file, symlink or directory)
//	         schema:
//	           type: string
//	     content:
//	       application/octet-stream:
//	         schema:
//	           type: string
//	           example: some-text
//	       application/json:
//	         schema:
//	           type: array
//	           items:
//	             type: string
//	           example: |-
//	             [
//	               "/etc",
//	               "/home"
//	             ]
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storageVolumeFileGet(s *state.State, vol storageDrivers.Volume, volumeProjectName string, path string, r *http.Request) response.Response {
	reverter := revert.New()
	defer reverter.Fail()

	client, err := vol.FileSFTP(s)
	if err != nil {
		return response.SmartError(err)
	}

	reverter.Add(func() { _ = client.Close() })

	return fileSFTPGet(client, path, r, reverter, func() {
		s.Events.SendLifecycle(volumeProjectName, lifecycle.StorageVolumeFileRetrieved.Event(vol, string(vol.Type()), volumeProjectName, nil, logger.Ctx{"path": path}))
	})
}

// swagger:operation HEAD /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/files storage storage_pool_volume_type_files_head
//
//	Get metadata for a file
//
//	Gets the file or directory metadata.
//
//	---
//	parameters:
//	  - in: query
//	    name: path
//	    description: Path to the file
//	    type: string
//	    example: default
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	     description: Raw file or directory listing
//	     headers:
//	       X-Incus-uid:
//	         description: File owner UID
//	         schema:
//	           type: integer
//	       X-Incus-gid:
//	         description: File owner GID
//	         schema:
//	           type: integer
//	       X-Incus-mode:
//	         description: Mode mask
//	         schema:
//	           type: integer
//	       X-Incus-modified:
//	         description: Last modified date
//	         schema:
//	           type: string
//	       X-Incus-type:
//	         description: Type of file (file, symlink or directory)
//	         schema:
//	           type: string
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storageVolumeFileHead(s *state.State, vol storageDrivers.Volume, volumeProjectName string, path string, _ *http.Request) response.Response {
	reverter := revert.New()
	defer reverter.Fail()

	client, err := vol.FileSFTP(s)
	if err != nil {
		return response.SmartError(err)
	}

	reverter.Add(func() { _ = client.Close() })

	return fileSFTPHead(client, path)
}

// swagger:operation POST /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/files storage storage_pool_volume_type_files_post
//
//	Create or replace a file
//
//	Creates a new file in the storage volume.
//
//	---
//	consumes:
//	  - application/octet-stream
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: path
//	    description: Path to the file
//	    type: string
//	    example: default
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: raw_file
//	    description: Raw file content
//	  - in: header
//	    name: X-Incus-uid
//	    description: File owner UID
//	    schema:
//	      type: integer
//	    example: 1000
//	  - in: header
//	    name: X-Incus-gid
//	    description: File owner GID
//	    schema:
//	      type: integer
//	    example: 1000
//	  - in: header
//	    name: X-Incus-mode
//	    description: File mode
//	    schema:
//	      type: integer
//	    example: 0644
//	  - in: header
//	    name: X-Incus-type
//	    description: Type of file (file, symlink or directory)
//	    schema:
//	      type: string
//	    example: file
//	  - in: header
//	    name: X-Incus-write
//	    description: Write mode (overwrite or append)
//	    schema:
//	      type: string
//	    example: overwrite
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storageVolumeFilePost(s *state.State, vol storageDrivers.Volume, volumeProjectName string, path string, r *http.Request) response.Response {
	client, err := vol.FileSFTP(s)
	if err != nil {
		return response.SmartError(err)
	}

	defer func() { _ = client.Close() }()

	return fileSFTPPost(client, path, r, func() {
		s.Events.SendLifecycle(volumeProjectName, lifecycle.StorageVolumeFilePushed.Event(vol, string(vol.Type()), volumeProjectName, nil, logger.Ctx{"path": path}))
	})
}

// swagger:operation DELETE /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/files storage storage_pool_volume_type_files_delete
//
//	Delete a file
//
//	Removes the file.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: path
//	    description: Path to the file
//	    type: string
//	    example: default
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
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storageVolumeFileDelete(s *state.State, vol storageDrivers.Volume, volumeProjectName string, path string, _ *http.Request) response.Response {
	client, err := vol.FileSFTP(s)
	if err != nil {
		return response.SmartError(err)
	}

	defer func() { _ = client.Close() }()

	return fileSFTPDelete(client, path, func() {
		s.Events.SendLifecycle(volumeProjectName, lifecycle.StorageVolumeFileDeleted.Event(vol, string(vol.Type()), volumeProjectName, nil, logger.Ctx{"path": path}))
	})
}
