package main

import (
	"errors"
	"net/http"

	incus "github.com/lxc/incus/v7/client"
	internalInstance "github.com/lxc/incus/v7/internal/instance"
	"github.com/lxc/incus/v7/internal/server/cluster"
	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/request"
	"github.com/lxc/incus/v7/internal/server/response"
	storagePools "github.com/lxc/incus/v7/internal/server/storage"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/util"
)

// swagger:operation GET /1.0/instances/{name}/nbd instances instance_nbd_get
//
//	Get an NBD connection for all of the instance's disks
//
//	Upgrades the request to an NBD connection exposing all of the instance's block devices.
//	Each disk is exported under an NBD export named after its Incus device name.
//
//	This is only available on running virtual machines. For stopped instances, access the disks
//	individually through the storage volume NBD endpoint.
//
//	Passing reuse=1 returns an additional connection to an already running NBD session instead of
//	starting a new one. The session is terminated when all of its connections are closed.
//
//	---
//	produces:
//	  - application/json
//	  - application/octet-stream
//	parameters:
//	  - in: path
//	    name: name
//	    description: Instance name
//	    type: string
//	    required: true
//	  - in: query
//	    name: reuse
//	    description: Whether to connect to an already running NBD session
//	    type: integer
//	    example: 1
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
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
func instanceNBDHandler(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)
	reuse := util.IsTrue(request.QueryParam(r, "reuse"))

	instName, err := pathVar(r, "name")
	if err != nil {
		return response.SmartError(err)
	}

	if internalInstance.IsSnapshot(instName) {
		return response.BadRequest(errors.New("Invalid instance name"))
	}

	if r.Header.Get("Upgrade") != "nbd" {
		return response.SmartError(api.StatusErrorf(http.StatusBadRequest, "Missing or invalid upgrade header"))
	}

	// Forward the request if the instance is remote.
	client, err := cluster.ConnectIfInstanceIsRemote(s, projectName, instName, r)
	if err != nil {
		return response.SmartError(err)
	}

	if client != nil {
		conn, err := client.GetInstanceNBDConn(instName, incus.InstanceNBDArgs{Reuse: reuse})
		if err != nil {
			return response.SmartError(err)
		}

		return response.UpgradeResponse(r, conn, "nbd", nil)
	}

	// Local requests.
	inst, err := instance.LoadByProjectAndName(s, projectName, instName)
	if err != nil {
		return response.SmartError(err)
	}

	if inst.Type() != instancetype.VM {
		return response.BadRequest(errors.New("NBD export of all disks is only supported on virtual machines"))
	}

	pool, err := storagePools.LoadByInstance(s, inst)
	if err != nil {
		return response.SmartError(err)
	}

	conn, disconnect, err := pool.GetInstanceAllDisksNBD(inst, reuse)
	if err != nil {
		return response.SmartError(err)
	}

	return response.UpgradeResponse(r, conn, "nbd", disconnect)
}
