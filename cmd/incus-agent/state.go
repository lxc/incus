package main

import (
	"errors"
	"net/http"

	"github.com/lxc/incus/v6/internal/server/response"
	"github.com/lxc/incus/v6/shared/api"
)

var stateCmd = APIEndpoint{
	Name: "state",
	Path: "state",

	Get: APIEndpointAction{Handler: stateGet},
}

func stateGet(d *Daemon, r *http.Request) response.Response {
	if d.Features != nil && !d.Features["state"] {
		return response.Forbidden(errors.New("Guest state reporting is disabled by configuration"))
	}

	return response.SyncResponse(true, renderState())
}

func renderState() *api.InstanceState {
	return &api.InstanceState{
		CPU:       osGetCPUState(),
		Memory:    osGetMemoryState(),
		Network:   osGetNetworkState(),
		Pid:       1,
		Processes: osGetProcessesState(),
		OSInfo:    osGetOSState(),
	}
}
