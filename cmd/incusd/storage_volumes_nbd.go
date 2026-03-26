package main

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"

	"github.com/gorilla/mux"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/internal/server/cluster"
	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/internal/server/request"
	"github.com/lxc/incus/v6/internal/server/response"
	storagePools "github.com/lxc/incus/v6/internal/server/storage"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/util"
)

// swagger:operation GET /1.0/storage-pools/{poolName}/volumes/{type}/{volumeName}/nbd storage storage_pool_volume_type_nbd_get
//
//	Get the storage volume NBD connection
//
//	Upgrades the request to an NBD connection of the storage volume's block device.
//
//	---
//	produces:
//	  - application/json
//	  - application/octet-stream
//	responses:
//	  "101":
//	    description: Switching protocols to NBD
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func storagePoolVolumeTypeNBDHandler(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)
	writable := util.IsTrue(request.QueryParam(r, "writable"))

	if r.Header.Get("Upgrade") != "nbd" {
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

	// Check that the storage volume type is valid.
	if !slices.Contains([]int{db.StoragePoolVolumeTypeVM, db.StoragePoolVolumeTypeCustom}, volumeType) {
		return response.BadRequest(fmt.Errorf("Unsupported storage volume type %q", volumeTypeName))
	}

	// Determine the relevant project.
	volumeProjectName, err := project.StorageVolumeProject(s.DB.Cluster, projectName, volumeType)
	if err != nil {
		return response.SmartError(err)
	}

	// Forward the request if the instance is remote.
	c, err := cluster.ConnectIfVolumeIsRemote(s, poolName, volumeProjectName, volumeName, volumeType, s.Endpoints.NetworkCert(), s.ServerCert(), r)
	if err != nil {
		return response.SmartError(err)
	}

	if c != nil {
		conn, err := c.GetStoragePoolVolumeBlockNBDConn(poolName, volumeTypeName, volumeName, incus.StorageVolumeNBDPost{Writable: writable})
		if err != nil {
			return response.SmartError(err)
		}

		return response.UpgradeResponse(r, conn, "nbd", nil)
	}

	// Local requests.
	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(err)
	}

	// Special handling for instances.
	if volumeType == db.StoragePoolVolumeTypeVM {
		inst, err := instance.LoadByProjectAndName(s, volumeProjectName, volumeName)
		if err != nil {
			return response.SmartError(err)
		}

		conn, disconnect, err := pool.GetInstanceNBD(inst, writable)
		if err != nil {
			return response.SmartError(err)
		}

		return response.UpgradeResponse(r, conn, "nbd", disconnect)
	}

	// Handle custom volumes.
	conn, disconnect, err := pool.GetCustomVolumeNBD(volumeProjectName, volumeName, writable)
	if err != nil {
		return response.SmartError(err)
	}

	return response.UpgradeResponse(r, conn, "nbd", disconnect)
}
