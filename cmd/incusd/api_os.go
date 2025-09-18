package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"

	"github.com/lxc/incus/v6/internal/server/auth"
	"github.com/lxc/incus/v6/internal/server/response"
)

var apiOS = APIEndpoint{
	Path:   "{name:.*}",
	Patch:  APIEndpointAction{Handler: apiOSProxy, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
	Put:    APIEndpointAction{Handler: apiOSProxy, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
	Get:    APIEndpointAction{Handler: apiOSProxy, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
	Post:   APIEndpointAction{Handler: apiOSProxy, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
	Delete: APIEndpointAction{Handler: apiOSProxy, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
	Head:   APIEndpointAction{Handler: apiOSProxy, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

func apiOSProxy(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// If a target was specified, forward the request to the relevant node.
	resp := forwardedResponseIfTargetIsRemote(s, r)
	if resp != nil {
		return resp
	}

	// Check if this is an IncusOS system.
	if s.OS.IncusOS == nil {
		return response.BadRequest(errors.New("System isn't running IncusOS"))
	}

	// Prepare the proxy.
	proxy := &httputil.ReverseProxy{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", "/run/incus-os/unix.socket")
			},
		},
		Director: func(r *http.Request) {
			r.URL.Scheme = "http"
			r.URL.Host = "incus-os"
		},
	}

	// Allow IncusOS to adjust the returned paths to the prefix used by the proxy.
	r.Header.Add("X-IncusOS-Proxy", "/os")

	// Handle the request.
	return response.ManualResponse(func(w http.ResponseWriter) error {
		http.StripPrefix("/os", proxy).ServeHTTP(w, r)

		return nil
	})
}
