package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"

	internalInstance "github.com/lxc/incus/v7/internal/instance"
	"github.com/lxc/incus/v7/internal/server/cluster"
	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/internal/server/request"
	"github.com/lxc/incus/v7/internal/server/response"
	"github.com/lxc/incus/v7/shared/api"
)

// swagger:operation POST /1.0/instances/{name}/port-forward instances instance_port_forward_post
//
//	Connect to a TCP port inside the instance
//
//	Upgrades the request to a raw TCP connection to the given address and port inside of the instance.
//
//	For containers, the connection is established directly by the server from within the
//	container's network namespace. For virtual machines, the request is forwarded to the
//	agent which then handles the connection.
//
//	This is only available on running instances.
//
//	---
//	consumes:
//	  - application/json
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
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: port-forward
//	    description: Port forwarding request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/InstancePortForwardPost"
//	responses:
//	  "101":
//	    description: Switching protocols to TCP
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instancePortForwardHandler(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)

	instName, err := pathVar(r, "name")
	if err != nil {
		return response.SmartError(err)
	}

	if internalInstance.IsSnapshot(instName) {
		return response.BadRequest(errors.New("Invalid instance name"))
	}

	if r.Header.Get("Upgrade") != "tcp" {
		return response.SmartError(api.StatusErrorf(http.StatusBadRequest, "Missing or invalid upgrade header"))
	}

	// Parse the request.
	req := api.InstancePortForwardPost{}

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Address == "" {
		req.Address = "127.0.0.1"
	}

	if net.ParseIP(req.Address) == nil {
		return response.BadRequest(fmt.Errorf("Invalid address %q", req.Address))
	}

	if req.Port <= 0 || req.Port > 65535 {
		return response.BadRequest(fmt.Errorf("Invalid port %d", req.Port))
	}

	// Forward the request if the instance is remote.
	client, err := cluster.ConnectIfInstanceIsRemote(s, projectName, instName, r)
	if err != nil {
		return response.SmartError(err)
	}

	if client != nil {
		conn, err := client.GetInstancePortForwardConn(instName, req)
		if err != nil {
			return response.SmartError(err)
		}

		return response.UpgradeResponse(r, conn, "tcp", nil)
	}

	// Local requests.
	inst, err := instance.LoadByProjectAndName(s, projectName, instName)
	if err != nil {
		return response.SmartError(err)
	}

	conn, err := inst.PortForwardConn(req.Address, req.Port)
	if err != nil {
		return response.SmartError(api.StatusErrorf(http.StatusInternalServerError, "Failed getting instance port forward connection: %v", err))
	}

	return response.UpgradeResponse(r, conn, "tcp", nil)
}
