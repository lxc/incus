package vsock

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mdlayher/vsock"

	localtls "github.com/lxc/incus/shared/tls"
)

// Dial connects to a remote vsock.
func Dial(cid, port uint32) (net.Conn, error) {
	return vsock.Dial(cid, port, nil)
}

// HTTPClient provides an HTTP client for using over vsock.
func HTTPClient(vsockID uint32, port int, tlsClientCert string, tlsClientKey string, tlsServerCert string) (*http.Client, error) {
	client := &http.Client{}

	// Get the TLS configuration.
	tlsConfig, err := localtls.GetTLSConfigMem(tlsClientCert, tlsClientKey, "", tlsServerCert, false)
	if err != nil {
		return nil, err
	}

	client.Transport = &http.Transport{
		TLSClientConfig: tlsConfig,
		// Setup a VM socket dialer.
		DialContext: func(_ context.Context, network, addr string) (net.Conn, error) {
			var conn net.Conn
			var err error

			// Retry for up to 1s at 100ms interval to handle various failures.
			for i := 0; i < 10; i++ {
				conn, err = Dial(vsockID, uint32(port))
				if err == nil {
					break
				} else {
					// Handle some fatal errors.
					msg := err.Error()
					if strings.Contains(msg, "connection timed out") {
						// Retry once.
						conn, err = Dial(vsockID, uint32(port))
						break
					} else if strings.Contains(msg, "connection refused") {
						break
					}

					// Retry the rest.
				}

				time.Sleep(100 * time.Millisecond)
			}

			if err != nil {
				return nil, err
			}

			return conn, nil
		},
		DisableKeepAlives:     true,
		ExpectContinueTimeout: time.Second * 30,
		ResponseHeaderTimeout: time.Second * 3600,
		TLSHandshakeTimeout:   time.Second * 5,
	}

	// Setup redirect policy.
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		// Replicate the headers.
		req.Header = via[len(via)-1].Header

		return nil
	}

	return client, nil
}
