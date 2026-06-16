package incus

import (
	"errors"
	"net/http"

	"github.com/lxc/incus/v7/internal/oci"
)

// ProtocolOCI implements an OCI registry API client.
type ProtocolOCI struct {
	http            *http.Client
	httpHost        string
	httpUserAgent   string
	httpCertificate string

	// Cache for images.
	cache map[string]ociInfo

	// Error tracking for images.
	errors map[string]error

	// Lazily-initialized registry client (caches auth tokens across calls).
	registry *oci.Registry

	tempPath string
}

// getRegistry returns the registry client, creating it on first use.
func (r *ProtocolOCI) getRegistry() *oci.Registry {
	if r.registry == nil {
		r.registry = oci.NewRegistry(r.httpHost, r.http, r.httpUserAgent)
	}

	return r.registry
}

// Disconnect is a no-op for OCI.
func (r *ProtocolOCI) Disconnect() {
}

// GetConnectionInfo returns the basic connection information used to interact with the server.
func (r *ProtocolOCI) GetConnectionInfo() (*ConnectionInfo, error) {
	info := ConnectionInfo{}
	info.Addresses = []string{r.httpHost}
	info.Certificate = r.httpCertificate
	info.Protocol = "oci"
	info.URL = r.httpHost

	return &info, nil
}

// GetHTTPClient returns the http client used for the connection. This can be used to set custom http options.
func (r *ProtocolOCI) GetHTTPClient() (*http.Client, error) {
	if r.http == nil {
		return nil, errors.New("HTTP client isn't set, bad connection")
	}

	return r.http, nil
}

// DoHTTP performs a Request.
func (r *ProtocolOCI) DoHTTP(req *http.Request) (*http.Response, error) {
	// Set the user agent.
	if r.httpUserAgent != "" {
		req.Header.Set("User-Agent", r.httpUserAgent)
	}

	return r.http.Do(req)
}
