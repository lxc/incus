package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/internal/server/cluster"
	"github.com/lxc/incus/v6/internal/server/db"
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
	volumeType, err := storagePools.VolumeTypeNameToDBType(volumeTypeName)
	if err != nil {
		return response.BadRequest(err)
	}

	// Only custom volumes are served, so the type must match the authorized object.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return response.BadRequest(fmt.Errorf("Invalid storage volume type %q", volumeTypeName))
	}

	volumeProjectName, err := project.StorageVolumeProject(s.DB.Cluster, projectName, volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// The permission was checked on the targeted member's volume, so only serve that one.
	target := request.QueryParam(r, "target")
	if target != "" && target != s.ServerName {
		return response.BadRequest(errors.New("Targeting another cluster member isn't supported on this endpoint"))
	}

	// Redirect to correct server if needed.
	var conn net.Conn
	var client incus.InstanceServer

	// Forward the request if the volume is remote.
	if target == "" {
		client, err = cluster.ConnectIfVolumeIsRemote(s, poolName, volumeProjectName, volumeName, volumeType, s.Endpoints.NetworkCert(), s.ServerCert(), r)
		if err != nil {
			return response.SmartError(err)
		}
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

		// Only filesystem volumes have a file tree; the mount path of other content types holds the disk image.
		if volumeDB.ContentType != db.StoragePoolVolumeContentTypeNameFS {
			return response.SmartError(api.StatusErrorf(http.StatusBadRequest, "Only filesystem volumes support file access"))
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
