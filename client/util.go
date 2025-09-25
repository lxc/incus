package incus

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lxc/incus/v6/shared/proxy"
	localtls "github.com/lxc/incus/v6/shared/tls"
)

// tlsHTTPClient creates an HTTP client with a specified Transport Layer Security (TLS) configuration.
// It takes in parameters for client certificates, keys, Certificate Authority, server certificates,
// a boolean for skipping verification, a proxy function, and a transport wrapper function.
// It returns the HTTP client with the provided configurations and handles any errors that might occur during the setup process.
func tlsHTTPClient(client *http.Client, tlsClientCert string, tlsClientKey string, tlsCA string, tlsServerCert string, insecureSkipVerify bool, identicalCertificate bool, proxyFunc func(req *http.Request) (*url.URL, error), transportWrapper func(t *http.Transport) HTTPTransporter) (*http.Client, error) {
	// Get the TLS configuration
	tlsConfig, err := localtls.GetTLSConfigMem(tlsClientCert, tlsClientKey, tlsCA, tlsServerCert, insecureSkipVerify)
	if err != nil {
		return nil, err
	}

	// If asked for an exact match, skip normal validation.
	if identicalCertificate {
		tlsConfig.InsecureSkipVerify = true
	}

	// Define the http transport
	transport := &http.Transport{
		TLSClientConfig:       tlsConfig,
		Proxy:                 proxy.FromEnvironment,
		DisableKeepAlives:     true,
		ExpectContinueTimeout: time.Second * 30,
		ResponseHeaderTimeout: time.Second * 3600,
		TLSHandshakeTimeout:   time.Second * 5,
	}

	// Allow overriding the proxy
	if proxyFunc != nil {
		transport.Proxy = proxyFunc
	}

	// Special TLS handling
	transport.DialTLSContext = func(ctx context.Context, network string, addr string) (net.Conn, error) {
		tlsDial := func(network string, addr string, config *tls.Config, resetName bool) (net.Conn, error) {
			conn, err := localtls.RFC3493Dialer(ctx, network, addr)
			if err != nil {
				return nil, err
			}

			// Setup TLS
			if resetName {
				hostName, _, err := net.SplitHostPort(addr)
				if err != nil {
					hostName = addr
				}

				config = config.Clone()
				config.ServerName = hostName
			}

			tlsConn := tls.Client(conn, config)

			// Validate the connection
			err = tlsConn.Handshake()
			if err != nil {
				_ = conn.Close()
				return nil, err
			}

			if identicalCertificate {
				// Look for an exact match with the certificate provided.
				// But ignore any other issue (validity, scope, ...).
				cs := tlsConn.ConnectionState()

				if len(cs.PeerCertificates) < 1 {
					return nil, errors.New("Couldn't validate peer certificate")
				}

				if tlsServerCert == "" {
					return nil, errors.New("Peer certificate wasn't provided")
				}

				certBlock, _ := pem.Decode([]byte(tlsServerCert))
				if certBlock == nil {
					return nil, errors.New("Invalid remote certificate")
				}

				expectedRemoteCert, err := x509.ParseCertificate(certBlock.Bytes)
				if err != nil {
					return nil, err
				}

				if !cs.PeerCertificates[0].Equal(expectedRemoteCert) {
					return nil, errors.New("Remote certificate differs from expected")
				}
			}

			if !config.InsecureSkipVerify {
				// Check certificate validity.
				err := tlsConn.VerifyHostname(config.ServerName)
				if err != nil {
					_ = conn.Close()
					return nil, err
				}
			}

			return tlsConn, nil
		}

		conn, err := tlsDial(network, addr, transport.TLSClientConfig, false)
		if err != nil {
			// We may have gotten redirected to a non-Incus machine
			return tlsDial(network, addr, transport.TLSClientConfig, true)
		}

		return conn, nil
	}

	// Define the http client
	if client == nil {
		client = &http.Client{}
	}

	if transportWrapper != nil {
		client.Transport = transportWrapper(transport)
	} else {
		client.Transport = transport
	}

	// Setup redirect policy
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		// Replicate the headers
		req.Header = via[len(via)-1].Header

		return nil
	}

	return client, nil
}

// unixHTTPClient creates an HTTP client that communicates over a Unix socket.
// It takes in the connection arguments and the Unix socket path as parameters.
// The function sets up a Unix socket dialer, configures the HTTP transport, and returns the HTTP client with the specified configurations.
// Any errors encountered during the setup process are also handled by the function.
func unixHTTPClient(args *ConnectionArgs, path string) (*http.Client, error) {
	// Setup a Unix socket dialer
	unixDial := func(_ context.Context, _ string, _ string) (net.Conn, error) {
		raddr, err := net.ResolveUnixAddr("unix", path)
		if err != nil {
			return nil, err
		}

		return net.DialUnix("unix", nil, raddr)
	}

	if args == nil {
		args = &ConnectionArgs{}
	}

	// Define the http transport
	transport := &http.Transport{
		DialContext:           unixDial,
		DisableKeepAlives:     true,
		Proxy:                 args.Proxy,
		ExpectContinueTimeout: time.Second * 30,
		ResponseHeaderTimeout: time.Second * 3600,
		TLSHandshakeTimeout:   time.Second * 5,
	}

	// Define the http client
	client := args.HTTPClient
	if client == nil {
		client = &http.Client{}
	}

	client.Transport = transport

	// Setup redirect policy
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		// Replicate the headers
		req.Header = via[len(via)-1].Header

		return nil
	}

	return client, nil
}

// remoteOperationResult used for storing the error that occurred for a particular remote URL.
type remoteOperationResult struct {
	URL   string
	Error error
}

func remoteOperationError(msg string, errorOperationResults []remoteOperationResult) error {
	// Check if empty
	if len(errorOperationResults) == 0 {
		return nil
	}

	// Check if all identical
	var err error
	for _, entry := range errorOperationResults {
		if err != nil && entry.Error.Error() != err.Error() {
			errorStrings := make([]string, 0, len(errorOperationResults))
			for _, operationResult := range errorOperationResults {
				errorStrings = append(errorStrings, fmt.Sprintf("%s: %v", operationResult.URL, operationResult.Error))
			}

			return fmt.Errorf("%s:\n - %s", msg, strings.Join(errorStrings, "\n - "))
		}

		err = entry.Error
	}

	// Check if successful
	if err != nil {
		return fmt.Errorf("%s: %w", msg, err)
	}

	return nil
}

// Set the value of a query parameter in the given URI.
func setQueryParam(uri, param, value string) (string, error) {
	fields, err := url.Parse(uri)
	if err != nil {
		return "", err
	}

	values := fields.Query()
	values.Set(param, url.QueryEscape(value))

	fields.RawQuery = values.Encode()

	return fields.String(), nil
}

// urlsToResourceNames returns a list of resource names extracted from one or more URLs of the same resource type.
// The resource type path prefix to match is provided by the matchPathPrefix argument.
func urlsToResourceNames(matchPathPrefix string, urls ...string) ([]string, error) {
	resourceNames := make([]string, 0, len(urls))

	for _, urlRaw := range urls {
		u, err := url.Parse(urlRaw)
		if err != nil {
			return nil, fmt.Errorf("Failed parsing URL %q: %w", urlRaw, err)
		}

		_, after, found := strings.Cut(u.Path, fmt.Sprintf("%s/", matchPathPrefix))
		if !found {
			return nil, fmt.Errorf("Unexpected URL path %q", u)
		}

		resourceNames = append(resourceNames, after)
	}

	return resourceNames, nil
}

// parseFilters translates filters passed at client side to form acceptable by server-side API.
func parseFilters(filters []string) string {
	var result []string
	for _, filter := range filters {
		if strings.Contains(filter, "=") {
			membs := strings.SplitN(filter, "=", 2)
			result = append(result, fmt.Sprintf("%s eq %s", membs[0], membs[1]))
		}
	}
	return strings.Join(result, " and ")
}

// HTTPTransporter represents a wrapper around *http.Transport.
// It is used to add some pre and postprocessing logic to http requests / responses.
type HTTPTransporter interface {
	http.RoundTripper

	// Transport what this struct wraps
	Transport() *http.Transport
}
