package incus

import (
	"fmt"
	"net/http"
)

// ProtocolOCI implements an OCI registry API client.
type ProtocolOCI struct {
	http            *http.Client
	httpHost        string
	httpUserAgent   string
	httpCertificate string

	// Cache for images.
	cache map[string]ociInfo
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
		return nil, fmt.Errorf("HTTP client isn't set, bad connection")
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
