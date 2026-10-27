package main

import (
	"net/http"
	"net/http/pprof"

	"github.com/lxc/incus/v7/internal/server/auth"
	"github.com/lxc/incus/v7/internal/server/response"
)

var internalDebugPprofCmd = APIEndpoint{
	Path: "debug/pprof/{name...}",

	Get: APIEndpointAction{Handler: internalDebugPprof, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

// internalDebugPprof serves the pprof profiles of this daemon over the authenticated API.
func internalDebugPprof(d *Daemon, r *http.Request) response.Response {
	var handler http.Handler

	name := r.PathValue("name")
	switch name {
	case "":
		handler = http.HandlerFunc(pprof.Index)
	case "cmdline":
		handler = http.HandlerFunc(pprof.Cmdline)
	case "profile":
		handler = http.HandlerFunc(pprof.Profile)
	case "symbol":
		handler = http.HandlerFunc(pprof.Symbol)
	case "trace":
		handler = http.HandlerFunc(pprof.Trace)
	default:
		handler = pprof.Handler(name)
	}

	return response.ManualResponse(func(w http.ResponseWriter) error {
		handler.ServeHTTP(w, r)

		return nil
	})
}
