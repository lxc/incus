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

	"github.com/gorilla/websocket"
	"github.com/zitadel/oidc/v2/pkg/oidc"

	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/simplestreams"
	"github.com/lxc/incus/shared/util"
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

	// Custom proxy
	Proxy func(*http.Request) (*url.URL, error)

	// Custom HTTP Client (used as base for the connection)
	HTTPClient *http.Client

	// TransportWrapper wraps the *http.Transport set by Incus
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

// ConnectIncus lets you connect to a remote Incus daemon over HTTPs.
//
// A client certificate (TLSClientCert) and key (TLSClientKey) must be provided.
//
// If connecting to an Incus daemon running in PKI mode, the PKI CA (TLSCA) must also be provided.
//
// Unless the remote server is trusted by the system CA, the remote certificate must be provided (TLSServerCert).
func ConnectIncus(url string, args *ConnectionArgs) (InstanceServer, error) {
	return ConnectIncusWithContext(context.Background(), url, args)
}

// ConnectIncusWithContext lets you connect to a remote Incus daemon over HTTPs with context.Context.
//
// A client certificate (TLSClientCert) and key (TLSClientKey) must be provided.
//
// If connecting to an Incus daemon running in PKI mode, the PKI CA (TLSCA) must also be provided.
//
// Unless the remote server is trusted by the system CA, the remote certificate must be provided (TLSServerCert).
func ConnectIncusWithContext(ctx context.Context, url string, args *ConnectionArgs) (InstanceServer, error) {
	// Cleanup URL
	url = strings.TrimSuffix(url, "/")

	logger.Debug("Connecting to a remote Incus over HTTPS", logger.Ctx{"url": url})

	return httpsIncus(ctx, url, args)
}

// ConnectIncusHTTP lets you connect to a VM agent over a VM socket.
func ConnectIncusHTTP(args *ConnectionArgs, client *http.Client) (InstanceServer, error) {
	return ConnectIncusHTTPWithContext(context.Background(), args, client)
}

// ConnectIncusHTTPWithContext lets you connect to a VM agent over a VM socket with context.Context.
func ConnectIncusHTTPWithContext(ctx context.Context, args *ConnectionArgs, client *http.Client) (InstanceServer, error) {
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
	server := ProtocolIncus{
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

// ConnectIncusUnix lets you connect to a remote Incus daemon over a local unix socket.
//
// If the path argument is empty, then $INCUS_SOCKET will be used, if
// unset $INCUS_DIR/unix.socket will be used and if that one isn't set
// either, then the path will default to /var/lib/incus/unix.socket.
func ConnectIncusUnix(path string, args *ConnectionArgs) (InstanceServer, error) {
	return ConnectIncusUnixWithContext(context.Background(), path, args)
}

// ConnectIncusUnixWithContext lets you connect to a remote Incus daemon over a local unix socket with context.Context.
//
// If the path argument is empty, then $INCUS_SOCKET will be used, if
// unset $INCUS_DIR/unix.socket will be used and if that one isn't set
// either, then the path will default to /var/lib/incus/unix.socket.
func ConnectIncusUnixWithContext(ctx context.Context, path string, args *ConnectionArgs) (InstanceServer, error) {
	logger.Debug("Connecting to a local Incus over a Unix socket")

	// Use empty args if not specified
	if args == nil {
		args = &ConnectionArgs{}
	}

	httpBaseURL, err := url.Parse("http://unix.socket")
	if err != nil {
		return nil, err
	}

	ctxConnected, ctxConnectedCancel := context.WithCancel(context.Background())

	// Determine the socket path
	var projectName string
	if path == "" {
		path = os.Getenv("INCUS_SOCKET")
		if path == "" {
			incusDir := os.Getenv("INCUS_DIR")
			if incusDir == "" {
				incusDir = "/var/lib/incus"
			}

			path = filepath.Join(incusDir, "unix.socket")
			userPath := filepath.Join(incusDir, "unix.socket.user")
			if !util.PathIsWritable(path) && util.PathIsWritable(userPath) {
				// Handle the use of incus-user.
				path = userPath

				// When using incus-user, the project list is typically restricted.
				// So let's try to be smart about the project we're using.
				projectName = fmt.Sprintf("user-%d", os.Geteuid())
			}
		}
	}

	// Initialize the client struct
	server := ProtocolIncus{
		ctx:                ctx,
		httpBaseURL:        *httpBaseURL,
		httpUnixPath:       path,
		httpProtocol:       "unix",
		httpUserAgent:      args.UserAgent,
		ctxConnected:       ctxConnected,
		ctxConnectedCancel: ctxConnectedCancel,
		eventConns:         make(map[string]*websocket.Conn),
		eventListeners:     make(map[string][]*EventListener),
		project:            projectName,
	}

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

// ConnectPublicIncus lets you connect to a remote public Incus daemon over HTTPs.
//
// Unless the remote server is trusted by the system CA, the remote certificate must be provided (TLSServerCert).
func ConnectPublicIncus(url string, args *ConnectionArgs) (ImageServer, error) {
	return ConnectPublicIncusWithContext(context.Background(), url, args)
}

// ConnectPublicIncusWithContext lets you connect to a remote public Incus daemon over HTTPs with context.Context.
//
// Unless the remote server is trusted by the system CA, the remote certificate must be provided (TLSServerCert).
func ConnectPublicIncusWithContext(ctx context.Context, url string, args *ConnectionArgs) (ImageServer, error) {
	logger.Debug("Connecting to a remote public Incus over HTTPS")

	// Cleanup URL
	url = strings.TrimSuffix(url, "/")

	return httpsIncus(ctx, url, args)
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
		if !util.PathExists(args.CachePath) {
			return nil, fmt.Errorf("Cache directory %q doesn't exist", args.CachePath)
		}

		hashedURL := fmt.Sprintf("%x", sha256.Sum256([]byte(url)))

		cachePath := filepath.Join(args.CachePath, hashedURL)
		cacheExpiry := args.CacheExpiry
		if cacheExpiry == 0 {
			cacheExpiry = time.Hour
		}

		if !util.PathExists(cachePath) {
			err := os.Mkdir(cachePath, 0755)
			if err != nil {
				return nil, err
			}
		}

		ssClient.SetCache(cachePath, cacheExpiry)
	}

	return &server, nil
}

// Internal function called by ConnectIncus and ConnectPublicIncus.
func httpsIncus(ctx context.Context, requestURL string, args *ConnectionArgs) (InstanceServer, error) {
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
	server := ProtocolIncus{
		ctx:                ctx,
		httpCertificate:    args.TLSServerCert,
		httpBaseURL:        *httpBaseURL,
		httpProtocol:       "https",
		httpUserAgent:      args.UserAgent,
		ctxConnected:       ctxConnected,
		ctxConnectedCancel: ctxConnectedCancel,
		eventConns:         make(map[string]*websocket.Conn),
		eventListeners:     make(map[string][]*EventListener),
	}

	if util.ValueInSlice(args.AuthType, []string{api.AuthenticationMethodOIDC}) {
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
	if args.AuthType == api.AuthenticationMethodOIDC {
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
