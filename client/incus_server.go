package incus

import (
	"fmt"
	"io"
	"net/http"

	"github.com/gorilla/websocket"

	"github.com/lxc/incus/shared/api"
	localtls "github.com/lxc/incus/shared/tls"
)

// Server handling functions

// GetServer returns the server status as a Server struct.
func (r *ProtocolIncus) GetServer() (*api.Server, string, error) {
	server := api.Server{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", "", nil, "", &server)
	if err != nil {
		return nil, "", err
	}

	// Fill in certificate fingerprint if not provided
	if server.Environment.CertificateFingerprint == "" && server.Environment.Certificate != "" {
		var err error
		server.Environment.CertificateFingerprint, err = localtls.CertFingerprintStr(server.Environment.Certificate)
		if err != nil {
			return nil, "", err
		}
	}

	if !server.Public && len(server.AuthMethods) == 0 {
		// TLS is always available for Incus servers
		server.AuthMethods = []string{"tls"}
	}

	// Add the value to the cache
	r.server = &server

	return &server, etag, nil
}

// UpdateServer updates the server status to match the provided Server struct.
func (r *ProtocolIncus) UpdateServer(server api.ServerPut, ETag string) error {
	// Send the request
	_, _, err := r.query("PUT", "", server, ETag)
	if err != nil {
		return err
	}

	return nil
}

// HasExtension returns true if the server supports a given API extension.
func (r *ProtocolIncus) HasExtension(extension string) bool {
	// If no cached API information, just assume we're good
	// This is needed for those rare cases where we must avoid a GetServer call
	if r.server == nil {
		return true
	}

	for _, entry := range r.server.APIExtensions {
		if entry == extension {
			return true
		}
	}

	return false
}

// CheckExtension checks if the server has the specified extension.
func (r *ProtocolIncus) CheckExtension(extensionName string) error {
	if !r.HasExtension(extensionName) {
		return fmt.Errorf("The server is missing the required %q API extension", extensionName)
	}

	return nil
}

// IsClustered returns true if the server is part of an Incus cluster.
func (r *ProtocolIncus) IsClustered() bool {
	return r.server.Environment.ServerClustered
}

// GetServerResources returns the resources available to a given Incus server.
func (r *ProtocolIncus) GetServerResources() (*api.Resources, error) {
	if !r.HasExtension("resources") {
		return nil, fmt.Errorf("The server is missing the required \"resources\" API extension")
	}

	resources := api.Resources{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", "/resources", nil, "", &resources)
	if err != nil {
		return nil, err
	}

	return &resources, nil
}

// UseProject returns a client that will use a specific project.
func (r *ProtocolIncus) UseProject(name string) InstanceServer {
	return &ProtocolIncus{
		ctx:                  r.ctx,
		ctxConnected:         r.ctxConnected,
		ctxConnectedCancel:   r.ctxConnectedCancel,
		server:               r.server,
		http:                 r.http,
		httpCertificate:      r.httpCertificate,
		httpBaseURL:          r.httpBaseURL,
		httpProtocol:         r.httpProtocol,
		httpUserAgent:        r.httpUserAgent,
		requireAuthenticated: r.requireAuthenticated,
		clusterTarget:        r.clusterTarget,
		project:              name,
		eventConns:           make(map[string]*websocket.Conn),  // New project specific listener conns.
		eventListeners:       make(map[string][]*EventListener), // New project specific listeners.
		oidcClient:           r.oidcClient,
	}
}

// UseTarget returns a client that will target a specific cluster member.
// Use this member-specific operations such as specific container
// placement, preparing a new storage pool or network, ...
func (r *ProtocolIncus) UseTarget(name string) InstanceServer {
	return &ProtocolIncus{
		ctx:                  r.ctx,
		ctxConnected:         r.ctxConnected,
		ctxConnectedCancel:   r.ctxConnectedCancel,
		server:               r.server,
		http:                 r.http,
		httpCertificate:      r.httpCertificate,
		httpBaseURL:          r.httpBaseURL,
		httpProtocol:         r.httpProtocol,
		httpUserAgent:        r.httpUserAgent,
		requireAuthenticated: r.requireAuthenticated,
		project:              r.project,
		eventConns:           make(map[string]*websocket.Conn),  // New target specific listener conns.
		eventListeners:       make(map[string][]*EventListener), // New target specific listeners.
		oidcClient:           r.oidcClient,
		clusterTarget:        name,
	}
}

// IsAgent returns true if the server is an Incus agent.
func (r *ProtocolIncus) IsAgent() bool {
	return r.server != nil && r.server.Environment.Server == "incus-agent"
}

// GetMetrics returns the text OpenMetrics data.
func (r *ProtocolIncus) GetMetrics() (string, error) {
	// Check that the server supports it.
	if !r.HasExtension("metrics") {
		return "", fmt.Errorf("The server is missing the required \"metrics\" API extension")
	}

	// Prepare the request.
	requestURL, err := r.setQueryAttributes(fmt.Sprintf("%s/1.0/metrics", r.httpBaseURL.String()))
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return "", err
	}

	// Send the request.
	resp, err := r.DoHTTP(req)
	if err != nil {
		return "", err
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Bad HTTP status: %d", resp.StatusCode)
	}

	// Get the content.
	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(content), nil
}
