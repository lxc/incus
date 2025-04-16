package main

import (
	"net/http"

	"github.com/lxc/incus/v6/internal/server/response"
	"github.com/lxc/incus/v6/shared/api"
)

var stateCmd = APIEndpoint{
	Name: "state",
	Path: "state",

	Get: APIEndpointAction{Handler: stateGet},
	Put: APIEndpointAction{Handler: statePut},
}

func stateGet(d *Daemon, r *http.Request) response.Response {
	return response.SyncResponse(true, renderState())
}

func statePut(d *Daemon, r *http.Request) response.Response {
	return response.NotImplemented(nil)
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
