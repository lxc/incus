package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"

	"github.com/lxc/incus/v7/internal/server/response"
	"github.com/lxc/incus/v7/shared/api"
)

var portForwardCmd = APIEndpoint{
	Name: "port-forward",
	Path: "port-forward",

	Post: APIEndpointAction{Handler: portForwardHandler},
}

func portForwardHandler(d *Daemon, r *http.Request) response.Response {
	if d.Features != nil && !d.Features["port-forward"] {
		return response.Forbidden(errors.New("Port forwarding is disabled by configuration"))
	}

	if r.Header.Get("Upgrade") != "tcp" {
		return response.BadRequest(errors.New("Missing or invalid upgrade header"))
	}

	// Parse the request.
	req := api.InstancePortForwardPost{}

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Address == "" {
		req.Address = "127.0.0.1"
	}

	if req.Port <= 0 || req.Port > 65535 {
		return response.BadRequest(fmt.Errorf("Invalid port %d", req.Port))
	}

	// Connect to the target.
	conn, err := net.Dial("tcp", net.JoinHostPort(req.Address, strconv.Itoa(req.Port)))
	if err != nil {
		return response.InternalError(fmt.Errorf("Failed connecting to %q port %d: %w", req.Address, req.Port, err))
	}

	return response.UpgradeResponse(r, conn, "tcp", nil)
}
