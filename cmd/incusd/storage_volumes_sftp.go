package main

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"

	"github.com/lxc/incus/v6/internal/server/cluster"
	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/internal/server/request"
	"github.com/lxc/incus/v6/internal/server/response"
	storagePools "github.com/lxc/incus/v6/internal/server/storage"
	storageDrivers "github.com/lxc/incus/v6/internal/server/storage/drivers"
	"github.com/lxc/incus/v6/shared/api"
)

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/sftp storage storage_pool_volume_type_sftp_get
//
//	Get the storage volume SFTP connection
//
//	Upgrades the request to an SFTP connection of the storage volume's filesystem.
//
//	---
//	produces:
//	  - application/json
//	  - application/octet-stream
//	responses:
//	  "101":
//	    description: Switching protocols to SFTP
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeTypeSFTPHandler(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)

	if r.Header.Get("Upgrade") != "sftp" {
		return response.SmartError(api.StatusErrorf(http.StatusBadRequest, "Missing or invalid upgrade header"))
	}

	// Get the volume details.
	volumeTypeName, err := url.PathUnescape(r.PathValue("type"))
	if err != nil {
		return response.SmartError(err)
	}

	volumeName, err := url.PathUnescape(r.PathValue("volumeName"))
	if err != nil {
		return response.SmartError(err)
	}

	poolName, err := url.PathUnescape(r.PathValue("poolName"))
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

	volumeProjectName, err := project.StorageVolumeProject(s.DB.Cluster, projectName, volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Redirect to correct server if needed.
	var conn net.Conn

	// Forward the request if the instance is remote.
	client, err := cluster.ConnectIfVolumeIsRemote(s, poolName, volumeProjectName, volumeName, volumeType, s.Endpoints.NetworkCert(), s.ServerCert(), r)
	if err != nil {
		return response.SmartError(err)
	}

	if client != nil {
		conn, err = client.GetStoragePoolVolumeFileSFTPConn(poolName, volumeTypeName, volumeName)
		if err != nil {
			return response.SmartError(err)
		}
	} else {
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

		conn, err = vol.FileSFTPConn(d.State())
		if err != nil {
			return response.SmartError(api.StatusErrorf(http.StatusInternalServerError, "Failed getting storage volume SFTP connection: %v", err))
		}
	}

	return response.SFTPResponse(r, conn)
}
