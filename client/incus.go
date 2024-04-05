package incus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/tcp"
)

// ProtocolIncus represents an Incus API server.
type ProtocolIncus struct {
	ctx                context.Context
	server             *api.Server
	ctxConnected       context.Context
	ctxConnectedCancel context.CancelFunc

	// eventConns contains event listener connections associated to a project name (or empty for all projects).
	eventConns map[string]*websocket.Conn

	// eventConnsLock controls write access to the eventConns.
	eventConnsLock sync.Mutex

	// eventListeners is a slice of event listeners associated to a project name (or empty for all projects).
	eventListeners     map[string][]*EventListener
	eventListenersLock sync.Mutex

	http            *http.Client
	httpCertificate string
	httpBaseURL     neturl.URL
	httpUnixPath    string
	httpProtocol    string
	httpUserAgent   string

	requireAuthenticated bool

	clusterTarget string
	project       string

	oidcClient *oidcClient
}

// Disconnect gets rid of any background goroutines.
func (r *ProtocolIncus) Disconnect() {
	if r.ctxConnected.Err() != nil {
		r.ctxConnectedCancel()
	}
}

// GetConnectionInfo returns the basic connection information used to interact with the server.
func (r *ProtocolIncus) GetConnectionInfo() (*ConnectionInfo, error) {
	info := ConnectionInfo{}
	info.Certificate = r.httpCertificate
	info.Protocol = "incus"
	info.URL = r.httpBaseURL.String()
	info.SocketPath = r.httpUnixPath

	info.Project = r.project
	if info.Project == "" {
		info.Project = api.ProjectDefaultName
	}

	info.Target = r.clusterTarget
	if info.Target == "" && r.server != nil {
		info.Target = r.server.Environment.ServerName
	}

	urls := []string{}
	if r.httpProtocol == "https" {
		urls = append(urls, r.httpBaseURL.String())
	}

	if r.server != nil && len(r.server.Environment.Addresses) > 0 {
		for _, addr := range r.server.Environment.Addresses {
			if strings.HasPrefix(addr, ":") {
				continue
			}

			url := fmt.Sprintf("https://%s", addr)
			if !slices.Contains(urls, url) {
				urls = append(urls, url)
			}
		}
	}

	info.Addresses = urls

	return &info, nil
}

// isSameServer compares the calling ProtocolIncus object with the provided server object to check if they are the same server.
// It verifies the equality based on their connection information (Protocol, Certificate, Project, and Target).
func (r *ProtocolIncus) isSameServer(server Server) bool {
	// Short path checking if the two structs are identical.
	if r == server {
		return true
	}

	// Short path if either of the structs are nil.
	if r == nil || server == nil {
		return false
	}

	// When dealing with uninitialized servers, we can't safely compare.
	if r.server == nil {
		return false
	}

	// Get the connection info from both servers.
	srcInfo, err := r.GetConnectionInfo()
	if err != nil {
		return false
	}

	dstInfo, err := server.GetConnectionInfo()
	if err != nil {
		return false
	}

	// Check whether we're dealing with the same server.
	return srcInfo.Protocol == dstInfo.Protocol && srcInfo.Certificate == dstInfo.Certificate &&
		srcInfo.Project == dstInfo.Project && srcInfo.Target == dstInfo.Target
}

// GetHTTPClient returns the http client used for the connection. This can be used to set custom http options.
func (r *ProtocolIncus) GetHTTPClient() (*http.Client, error) {
	if r.http == nil {
		return nil, fmt.Errorf("HTTP client isn't set, bad connection")
	}

	return r.http, nil
}

// DoHTTP performs a Request, using OIDC authentication if set.
func (r *ProtocolIncus) DoHTTP(req *http.Request) (*http.Response, error) {
	r.addClientHeaders(req)

	if r.oidcClient != nil {
		return r.oidcClient.do(req)
	}

	return r.http.Do(req)
}

// addClientHeaders sets headers from client settings.
// User-Agent (if r.httpUserAgent is set).
// X-Incus-authenticated (if r.requireAuthenticated is set).
// OIDC Authorization header (if r.oidcClient is set).
func (r *ProtocolIncus) addClientHeaders(req *http.Request) {
	if r.httpUserAgent != "" {
		req.Header.Set("User-Agent", r.httpUserAgent)
	}

	if r.requireAuthenticated {
		req.Header.Set("X-Incus-authenticated", "true")
	}

	if r.oidcClient != nil {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", r.oidcClient.getAccessToken()))
	}
}

// RequireAuthenticated sets whether we expect to be authenticated with the server.
func (r *ProtocolIncus) RequireAuthenticated(authenticated bool) {
	r.requireAuthenticated = authenticated
}

// RawQuery allows directly querying the Incus API
//
// This should only be used by internal Incus tools.
func (r *ProtocolIncus) RawQuery(method string, path string, data any, ETag string) (*api.Response, string, error) {
	// Generate the URL
	url := fmt.Sprintf("%s%s", r.httpBaseURL.String(), path)

	return r.rawQuery(method, url, data, ETag)
}

// RawWebsocket allows directly connection to Incus API websockets
//
// This should only be used by internal Incus tools.
func (r *ProtocolIncus) RawWebsocket(path string) (*websocket.Conn, error) {
	return r.websocket(path)
}

// RawOperation allows direct querying of an Incus API endpoint returning
// background operations.
func (r *ProtocolIncus) RawOperation(method string, path string, data any, ETag string) (Operation, string, error) {
	return r.queryOperation(method, path, data, ETag)
}

// Internal functions.
func incusParseResponse(resp *http.Response) (*api.Response, string, error) {
	// Get the ETag
	etag := resp.Header.Get("ETag")

	// Decode the response
	decoder := json.NewDecoder(resp.Body)
	response := api.Response{}

	err := decoder.Decode(&response)
	if err != nil {
		// Check the return value for a cleaner error
		if resp.StatusCode != http.StatusOK {
			return nil, "", fmt.Errorf("Failed to fetch %s: %s", resp.Request.URL.String(), resp.Status)
		}

		return nil, "", err
	}

	// Handle errors
	if response.Type == api.ErrorResponse {
		return &response, "", api.StatusErrorf(resp.StatusCode, response.Error)
	}

	return &response, etag, nil
}

// rawQuery is a method that sends an HTTP request to the Incus server with the provided method, URL, data, and ETag.
// It processes the request based on the data's type and handles the HTTP response, returning parsed results or an error if it occurs.
func (r *ProtocolIncus) rawQuery(method string, url string, data any, ETag string) (*api.Response, string, error) {
	var req *http.Request
	var err error

	// Log the request
	logger.Debug("Sending request to Incus", logger.Ctx{
		"method": method,
		"url":    url,
		"etag":   ETag,
	})

	// Get a new HTTP request setup
	if data != nil {
		switch data := data.(type) {
		case io.Reader:
			// Some data to be sent along with the request
			req, err = http.NewRequestWithContext(r.ctx, method, url, data)
			if err != nil {
				return nil, "", err
			}

			// Set the encoding accordingly
			req.Header.Set("Content-Type", "application/octet-stream")
		default:
			// Encode the provided data
			buf := bytes.Buffer{}
			err := json.NewEncoder(&buf).Encode(data)
			if err != nil {
				return nil, "", err
			}

			// Some data to be sent along with the request
			// Use a reader since the request body needs to be seekable
			req, err = http.NewRequestWithContext(r.ctx, method, url, bytes.NewReader(buf.Bytes()))
			if err != nil {
				return nil, "", err
			}

			// Set the encoding accordingly
			req.Header.Set("Content-Type", "application/json")

			// Log the data
			logger.Debugf(logger.Pretty(data))
		}
	} else {
		// No data to be sent along with the request
		req, err = http.NewRequestWithContext(r.ctx, method, url, nil)
		if err != nil {
			return nil, "", err
		}
	}

	// Set the ETag
	if ETag != "" {
		req.Header.Set("If-Match", ETag)
	}

	// Send the request
	resp, err := r.DoHTTP(req)
	if err != nil {
		return nil, "", err
	}

	defer func() { _ = resp.Body.Close() }()

	return incusParseResponse(resp)
}

// setURLQueryAttributes modifies the supplied URL's query string with the client's current target and project.
func (r *ProtocolIncus) setURLQueryAttributes(apiURL *neturl.URL) {
	// Extract query fields and update for cluster targeting or project
	values := apiURL.Query()
	if r.clusterTarget != "" {
		if values.Get("target") == "" {
			values.Set("target", r.clusterTarget)
		}
	}

	if r.project != "" {
		if values.Get("project") == "" && values.Get("all-projects") == "" {
			values.Set("project", r.project)
		}
	}

	apiURL.RawQuery = values.Encode()
}

func (r *ProtocolIncus) setQueryAttributes(uri string) (string, error) {
	// Parse the full URI
	fields, err := neturl.Parse(uri)
	if err != nil {
		return "", err
	}

	r.setURLQueryAttributes(fields)

	return fields.String(), nil
}

func (r *ProtocolIncus) query(method string, path string, data any, ETag string) (*api.Response, string, error) {
	// Generate the URL
	url := fmt.Sprintf("%s/1.0%s", r.httpBaseURL.String(), path)

	// Add project/target
	url, err := r.setQueryAttributes(url)
	if err != nil {
		return nil, "", err
	}

	// Run the actual query
	return r.rawQuery(method, url, data, ETag)
}

// queryStruct sends a query to the Incus server, then converts the response metadata into the specified target struct.
// The function logs the retrieved data, returns the etag of the response, and handles any errors during this process.
func (r *ProtocolIncus) queryStruct(method string, path string, data any, ETag string, target any) (string, error) {
	resp, etag, err := r.query(method, path, data, ETag)
	if err != nil {
		return "", err
	}

	err = resp.MetadataAsStruct(&target)
	if err != nil {
		return "", err
	}

	// Log the data
	logger.Debugf("Got response struct from Incus")
	logger.Debugf(logger.Pretty(target))

	return etag, nil
}

// queryOperation sends a query to the Incus server and then converts the response metadata into an Operation object.
// It sets up an early event listener, performs the query, processes the response, and manages the lifecycle of the event listener.
func (r *ProtocolIncus) queryOperation(method string, path string, data any, ETag string) (Operation, string, error) {
	// Attempt to setup an early event listener
	skipListener := false
	listener, err := r.GetEvents()
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusForbidden) {
			skipListener = true
		}

		listener = nil
	}

	// Send the query
	resp, etag, err := r.query(method, path, data, ETag)
	if err != nil {
		if listener != nil {
			listener.Disconnect()
		}

		return nil, "", err
	}

	// Get to the operation
	respOperation, err := resp.MetadataAsOperation()
	if err != nil {
		if listener != nil {
			listener.Disconnect()
		}

		return nil, "", err
	}

	// Setup an Operation wrapper
	op := operation{
		Operation:    *respOperation,
		r:            r,
		listener:     listener,
		skipListener: skipListener,
		chActive:     make(chan bool),
	}

	// Log the data
	logger.Debugf("Got operation from Incus")
	logger.Debugf(logger.Pretty(op.Operation))

	return &op, etag, nil
}

// rawWebsocket creates a websocket connection to the provided URL using the underlying HTTP transport of the ProtocolIncus receiver.
// It sets up the request headers, manages the connection handshake, sets TCP timeouts, and handles any errors that may occur during these operations.
func (r *ProtocolIncus) rawWebsocket(url string) (*websocket.Conn, error) {
	// Grab the http transport handler
	httpTransport, err := r.getUnderlyingHTTPTransport()
	if err != nil {
		return nil, err
	}

	// Setup a new websocket dialer based on it
	dialer := websocket.Dialer{
		NetDialContext:   httpTransport.DialContext,
		TLSClientConfig:  httpTransport.TLSClientConfig,
		Proxy:            httpTransport.Proxy,
		HandshakeTimeout: time.Second * 5,
	}

	// Create temporary http.Request using the http url, not the ws one, so that we can add the client headers
	// for the websocket request.
	req := &http.Request{URL: &r.httpBaseURL, Header: http.Header{}}
	r.addClientHeaders(req)

	// Establish the connection
	conn, resp, err := dialer.Dial(url, req.Header)
	if err != nil {
		if resp != nil {
			_, _, err = incusParseResponse(resp)
		}

		return nil, err
	}

	// Set TCP timeout options.
	remoteTCP, _ := tcp.ExtractConn(conn.UnderlyingConn())
	if remoteTCP != nil {
		err = tcp.SetTimeouts(remoteTCP, 0)
		if err != nil {
			logger.Warn("Failed setting TCP timeouts on remote connection", logger.Ctx{"err": err})
		}
	}

	// Log the data
	logger.Debugf("Connected to the websocket: %v", url)

	return conn, nil
}

// websocket generates a websocket URL based on the provided path and the base URL of the ProtocolIncus receiver.
// It then leverages the rawWebsocket method to establish and return a websocket connection to the generated URL.
func (r *ProtocolIncus) websocket(path string) (*websocket.Conn, error) {
	// Generate the URL
	var url string
	if r.httpBaseURL.Scheme == "https" {
		url = fmt.Sprintf("wss://%s/1.0%s", r.httpBaseURL.Host, path)
	} else {
		url = fmt.Sprintf("ws://%s/1.0%s", r.httpBaseURL.Host, path)
	}

	return r.rawWebsocket(url)
}

// WithContext returns a client that will add context.Context.
func (r *ProtocolIncus) WithContext(ctx context.Context) InstanceServer {
	rr := r
	rr.ctx = ctx
	return rr
}

// getUnderlyingHTTPTransport returns the *http.Transport used by the http client. If the http
// client was initialized with a HTTPTransporter, it returns the wrapped *http.Transport.
func (r *ProtocolIncus) getUnderlyingHTTPTransport() (*http.Transport, error) {
	switch t := r.http.Transport.(type) {
	case *http.Transport:
		return t, nil
	case HTTPTransporter:
		return t.Transport(), nil
	default:
		return nil, fmt.Errorf("Unexpected http.Transport type, %T", r)
	}
}

// getSourceImageConnectionInfo returns the connection information for the source image.
// The returned `info` is nil if the source image is local. In this process, the `instSrc`
// is also updated with the minimal source fields.
func (r *ProtocolIncus) getSourceImageConnectionInfo(source ImageServer, image api.Image, instSrc *api.InstanceSource) (info *ConnectionInfo, err error) {
	// Set the minimal source fields
	instSrc.Type = "image"

	// Optimization for the local image case
	if r.isSameServer(source) {
		// Always use fingerprints for local case
		instSrc.Fingerprint = image.Fingerprint
		instSrc.Alias = ""
		return nil, nil
	}

	// Minimal source fields for remote image
	instSrc.Mode = "pull"

	// If we have an alias and the image is public, use that
	if instSrc.Alias != "" && image.Public {
		instSrc.Fingerprint = ""
	} else {
		instSrc.Fingerprint = image.Fingerprint
		instSrc.Alias = ""
	}

	// Get source server connection information
	info, err = source.GetConnectionInfo()
	if err != nil {
		return nil, err
	}

	instSrc.Protocol = info.Protocol
	instSrc.Certificate = info.Certificate

	// Generate secret token if needed
	if !image.Public {
		secret, err := source.GetImageSecret(image.Fingerprint)
		if err != nil {
			return nil, err
		}

		instSrc.Secret = secret
	}

	return info, nil
}
