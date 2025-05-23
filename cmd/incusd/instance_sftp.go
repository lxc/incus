package main

import (
	"errors"
	"net"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	internalInstance "github.com/lxc/incus/v6/internal/instance"
	"github.com/lxc/incus/v6/internal/server/cluster"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/request"
	"github.com/lxc/incus/v6/internal/server/response"
	"github.com/lxc/incus/v6/shared/api"
)

// swagger:operation GET /1.0/instances/{name}/sftp instances instance_sftp
//
//	Get the instance SFTP connection
//
//	Upgrades the request to an SFTP connection of the instance's filesystem.
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
func instanceSFTPHandler(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)
	instName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if internalInstance.IsSnapshot(instName) {
		return response.BadRequest(errors.New("Invalid instance name"))
	}

	if r.Header.Get("Upgrade") != "sftp" {
		return response.SmartError(api.StatusErrorf(http.StatusBadRequest, "Missing or invalid upgrade header"))
	}

	// Forward the request if the instance is remote.
	client, err := cluster.ConnectIfInstanceIsRemote(s, projectName, instName, r)
	if err != nil {
		return response.SmartError(err)
	}

	// Redirect to correct server if needed.
	var conn net.Conn
	if client != nil {
		conn, err = client.GetInstanceFileSFTPConn(instName)
		if err != nil {
			return response.SmartError(err)
		}
	} else {
		inst, err := instance.LoadByProjectAndName(s, projectName, instName)
		if err != nil {
			return response.SmartError(err)
		}

		conn, err = inst.FileSFTPConn()
		if err != nil {
			return response.SmartError(api.StatusErrorf(http.StatusInternalServerError, "Failed getting instance SFTP connection: %v", err))
		}
	}

	return response.SFTPResponse(r, conn)
}
