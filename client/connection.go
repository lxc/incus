package incus

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-macaroon-bakery/macaroon-bakery/v3/httpbakery"
	"github.com/gorilla/websocket"
	"github.com/zitadel/oidc/v2/pkg/oidc"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/simplestreams"
)

// ConnectionArgs represents a set of common connection properties.
type ConnectionArgs struct {
	// TLS certificate of the remote server. If not specified, the system CA is used.
	TLSServerCert string

	// TLS certificate to use for client authentication.
	TLSClientCert string

	// TLS key to use for client authentication.
	TLSClientKey string

	// TLS CA to validate against when in PKI mode.
	TLSCA string

	// User agent string
	UserAgent string

	// Authentication type
	AuthType string

	// Authentication interactor
	AuthInteractor []httpbakery.Interactor

	// Custom proxy
	Proxy func(*http.Request) (*url.URL, error)

	// Custom HTTP Client (used as base for the connection)
	HTTPClient *http.Client

	// TransportWrapper wraps the *http.Transport set by lxd
	TransportWrapper func(*http.Transport) HTTPTransporter

	// Controls whether a client verifies the server's certificate chain and host name.
	InsecureSkipVerify bool

	// Cookie jar
	CookieJar http.CookieJar

	// OpenID Connect tokens
	OIDCTokens *oidc.Tokens[*oidc.IDTokenClaims]

	// Skip automatic GetServer request upon connection
	SkipGetServer bool

	// Caching support for image servers
	CachePath   string
	CacheExpiry time.Duration
}

// ConnectLXD lets you connect to a remote LXD daemon over HTTPs.
//
// A client certificate (TLSClientCert) and key (TLSClientKey) must be provided.
//
// If connecting to a LXD daemon running in PKI mode, the PKI CA (TLSCA) must also be provided.
//
// Unless the remote server is trusted by the system CA, the remote certificate must be provided (TLSServerCert).
func ConnectLXD(url string, args *ConnectionArgs) (InstanceServer, error) {
	return ConnectLXDWithContext(context.Background(), url, args)
}

// ConnectLXDWithContext lets you connect to a remote LXD daemon over HTTPs with context.Context.
//
// A client certificate (TLSClientCert) and key (TLSClientKey) must be provided.
//
// If connecting to a LXD daemon running in PKI mode, the PKI CA (TLSCA) must also be provided.
//
// Unless the remote server is trusted by the system CA, the remote certificate must be provided (TLSServerCert).
func ConnectLXDWithContext(ctx context.Context, url string, args *ConnectionArgs) (InstanceServer, error) {
	// Cleanup URL
	url = strings.TrimSuffix(url, "/")

	logger.Debug("Connecting to a remote LXD over HTTPS", logger.Ctx{"url": url})

	return httpsLXD(ctx, url, args)
}

// ConnectLXDHTTP lets you connect to a VM agent over a VM socket.
func ConnectLXDHTTP(args *ConnectionArgs, client *http.Client) (InstanceServer, error) {
	return ConnectLXDHTTPWithContext(context.Background(), args, client)
}

// ConnectLXDHTTPWithContext lets you connect to a VM agent over a VM socket with context.Context.
func ConnectLXDHTTPWithContext(ctx context.Context, args *ConnectionArgs, client *http.Client) (InstanceServer, error) {
	logger.Debug("Connecting to a VM agent over a VM socket")

	// Use empty args if not specified
	if args == nil {
		args = &ConnectionArgs{}
	}

	httpBaseURL, err := url.Parse("https://custom.socket")
	if err != nil {
		return nil, err
	}

	ctxConnected, ctxConnectedCancel := context.WithCancel(context.Background())

	// Initialize the client struct
	server := ProtocolLXD{
		ctx:                ctx,
		httpBaseURL:        *httpBaseURL,
		httpProtocol:       "custom",
		httpUserAgent:      args.UserAgent,
		ctxConnected:       ctxConnected,
		ctxConnectedCancel: ctxConnectedCancel,
		eventConns:         make(map[string]*websocket.Conn),
		eventListeners:     make(map[string][]*EventListener),
	}

	// Setup the HTTP client
	server.http = client

	// Test the connection and seed the server information
	if !args.SkipGetServer {
		serverStatus, _, err := server.GetServer()
		if err != nil {
			return nil, err
		}

		// Record the server certificate
		server.httpCertificate = serverStatus.Environment.Certificate
	}

	return &server, nil
}

// ConnectLXDUnix lets you connect to a remote LXD daemon over a local unix socket.
//
// If the path argument is empty, then $LXD_SOCKET will be used, if
// unset $LXD_DIR/unix.socket will be used and if that one isn't set
// either, then the path will default to /var/lib/lxd/unix.socket.
func ConnectLXDUnix(path string, args *ConnectionArgs) (InstanceServer, error) {
	return ConnectLXDUnixWithContext(context.Background(), path, args)
}

// ConnectLXDUnixWithContext lets you connect to a remote LXD daemon over a local unix socket with context.Context.
//
// If the path argument is empty, then $LXD_SOCKET will be used, if
// unset $LXD_DIR/unix.socket will be used and if that one isn't set
// either, then the path will default to /var/lib/lxd/unix.socket.
func ConnectLXDUnixWithContext(ctx context.Context, path string, args *ConnectionArgs) (InstanceServer, error) {
	logger.Debug("Connecting to a local LXD over a Unix socket")

	// Use empty args if not specified
	if args == nil {
		args = &ConnectionArgs{}
	}

	httpBaseURL, err := url.Parse("http://unix.socket")
	if err != nil {
		return nil, err
	}

	ctxConnected, ctxConnectedCancel := context.WithCancel(context.Background())

	// Initialize the client struct
	server := ProtocolLXD{
		ctx:                ctx,
		httpBaseURL:        *httpBaseURL,
		httpUnixPath:       path,
		httpProtocol:       "unix",
		httpUserAgent:      args.UserAgent,
		ctxConnected:       ctxConnected,
		ctxConnectedCancel: ctxConnectedCancel,
		eventConns:         make(map[string]*websocket.Conn),
		eventListeners:     make(map[string][]*EventListener),
	}

	// Determine the socket path
	if path == "" {
		path = os.Getenv("LXD_SOCKET")
		if path == "" {
			lxdDir := os.Getenv("LXD_DIR")
			if lxdDir == "" {
				lxdDir = "/var/lib/lxd"
			}

			path = filepath.Join(lxdDir, "unix.socket")
		}
	}

	path = shared.HostPath(path)

	// Setup the HTTP client
	httpClient, err := unixHTTPClient(args, path)
	if err != nil {
		return nil, err
	}

	server.http = httpClient

	// Test the connection and seed the server information
	if !args.SkipGetServer {
		serverStatus, _, err := server.GetServer()
		if err != nil {
			return nil, err
		}

		// Record the server certificate
		server.httpCertificate = serverStatus.Environment.Certificate
	}

	return &server, nil
}

// ConnectPublicLXD lets you connect to a remote public LXD daemon over HTTPs.
//
// Unless the remote server is trusted by the system CA, the remote certificate must be provided (TLSServerCert).
func ConnectPublicLXD(url string, args *ConnectionArgs) (ImageServer, error) {
	return ConnectPublicLXDWithContext(context.Background(), url, args)
}

// ConnectPublicLXDWithContext lets you connect to a remote public LXD daemon over HTTPs with context.Context.
//
// Unless the remote server is trusted by the system CA, the remote certificate must be provided (TLSServerCert).
func ConnectPublicLXDWithContext(ctx context.Context, url string, args *ConnectionArgs) (ImageServer, error) {
	logger.Debug("Connecting to a remote public LXD over HTTPS")

	// Cleanup URL
	url = strings.TrimSuffix(url, "/")

	return httpsLXD(ctx, url, args)
}

// ConnectSimpleStreams lets you connect to a remote SimpleStreams image server over HTTPs.
//
// Unless the remote server is trusted by the system CA, the remote certificate must be provided (TLSServerCert).
func ConnectSimpleStreams(url string, args *ConnectionArgs) (ImageServer, error) {
	logger.Debug("Connecting to a remote simplestreams server", logger.Ctx{"URL": url})

	// Cleanup URL
	url = strings.TrimSuffix(url, "/")

	// Use empty args if not specified
	if args == nil {
		args = &ConnectionArgs{}
	}

	// Initialize the client struct
	server := ProtocolSimpleStreams{
		httpHost:        url,
		httpUserAgent:   args.UserAgent,
		httpCertificate: args.TLSServerCert,
	}

	// Setup the HTTP client
	httpClient, err := tlsHTTPClient(args.HTTPClient, args.TLSClientCert, args.TLSClientKey, args.TLSCA, args.TLSServerCert, args.InsecureSkipVerify, args.Proxy, args.TransportWrapper)
	if err != nil {
		return nil, err
	}

	server.http = httpClient

	// Get simplestreams client
	ssClient := simplestreams.NewClient(url, *httpClient, args.UserAgent)
	server.ssClient = ssClient

	// Setup the cache
	if args.CachePath != "" {
		if !shared.PathExists(args.CachePath) {
			return nil, fmt.Errorf("Cache directory %q doesn't exist", args.CachePath)
		}

		hashedURL := fmt.Sprintf("%x", sha256.Sum256([]byte(url)))

		cachePath := filepath.Join(args.CachePath, hashedURL)
		cacheExpiry := args.CacheExpiry
		if cacheExpiry == 0 {
			cacheExpiry = time.Hour
		}

		if !shared.PathExists(cachePath) {
			err := os.Mkdir(cachePath, 0755)
			if err != nil {
				return nil, err
			}
		}

		ssClient.SetCache(cachePath, cacheExpiry)
	}

	return &server, nil
}

// Internal function called by ConnectLXD and ConnectPublicLXD.
func httpsLXD(ctx context.Context, requestURL string, args *ConnectionArgs) (InstanceServer, error) {
	// Use empty args if not specified
	if args == nil {
		args = &ConnectionArgs{}
	}

	httpBaseURL, err := url.Parse(requestURL)
	if err != nil {
		return nil, err
	}

	ctxConnected, ctxConnectedCancel := context.WithCancel(context.Background())

	// Initialize the client struct
	server := ProtocolLXD{
		ctx:                ctx,
		httpCertificate:    args.TLSServerCert,
		httpBaseURL:        *httpBaseURL,
		httpProtocol:       "https",
		httpUserAgent:      args.UserAgent,
		bakeryInteractor:   args.AuthInteractor,
		ctxConnected:       ctxConnected,
		ctxConnectedCancel: ctxConnectedCancel,
		eventConns:         make(map[string]*websocket.Conn),
		eventListeners:     make(map[string][]*EventListener),
	}

	if shared.StringInSlice(args.AuthType, []string{"candid", "oidc"}) {
		server.RequireAuthenticated(true)
	}

	// Setup the HTTP client
	httpClient, err := tlsHTTPClient(args.HTTPClient, args.TLSClientCert, args.TLSClientKey, args.TLSCA, args.TLSServerCert, args.InsecureSkipVerify, args.Proxy, args.TransportWrapper)
	if err != nil {
		return nil, err
	}

	if args.CookieJar != nil {
		httpClient.Jar = args.CookieJar
	}

	server.http = httpClient
	if args.AuthType == "candid" {
		server.setupBakeryClient()
	} else if args.AuthType == "oidc" {
		server.setupOIDCClient(args.OIDCTokens)
	}

	// Test the connection and seed the server information
	if !args.SkipGetServer {
		_, _, err := server.GetServer()
		if err != nil {
			return nil, err
		}
	}
	return &server, nil
}
