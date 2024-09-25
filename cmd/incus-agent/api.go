package main

import (
	"net/http"

	"github.com/lxc/incus/v6/internal/server/response"
)

// APIEndpoint represents a URL in our API.
type APIEndpoint struct {
	Name   string // Name for this endpoint.
	Path   string // Path pattern for this endpoint.
	Get    APIEndpointAction
	Put    APIEndpointAction
	Post   APIEndpointAction
	Delete APIEndpointAction
	Patch  APIEndpointAction
}

// APIEndpointAction represents an action on an API endpoint.
type APIEndpointAction struct {
	Handler func(d *Daemon, r *http.Request) response.Response
}
