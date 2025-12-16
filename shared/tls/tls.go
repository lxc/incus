package tls

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/lxc/incus/v6/shared/util"
)

// connectErrorPrefix used as prefix to error returned from RFC3493Dialer.
const connectErrorPrefix = "Unable to connect to"

// happyEyeballsDelay is the delay between starting connection attempts per RFC 8305.
const happyEyeballsDelay = 250 * time.Millisecond

// happyEyeballsTimeout is the overall timeout for the Happy Eyeballs algorithm.
const happyEyeballsTimeout = 30 * time.Second

// sortAddressesByFamily sorts addresses with IPv6 first (per RFC 8305 Happy Eyeballs).
// Within each family, the original order is preserved.
func sortAddressesByFamily(addrs []string) []string {
	var ipv6Addrs, ipv4Addrs []string
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			// If parsing fails, treat as IPv4
			ipv4Addrs = append(ipv4Addrs, addr)
			continue
		}

		if ip.To4() == nil {
			ipv6Addrs = append(ipv6Addrs, addr)
		} else {
			ipv4Addrs = append(ipv4Addrs, addr)
		}
	}

	// Return IPv6 addresses first, then IPv4
	return append(ipv6Addrs, ipv4Addrs...)
}

// RFC3493Dialer connects to the specified server and returns the connection.
// If the connection cannot be established then an error with the connectErrorPrefix is returned.
// This implementation uses Happy Eyeballs (RFC 8305) to handle dual-stack environments efficiently.
func RFC3493Dialer(ctx context.Context, network string, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}

	addrs, err := net.LookupHost(host)
	if err != nil {
		return nil, err
	}

	// Sort addresses with IPv6 first per RFC 8305
	addrs = sortAddressesByFamily(addrs)

	// If the context doesn't have a deadline, add one
	_, hasDeadline := ctx.Deadline()

	if !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, happyEyeballsTimeout)
		defer cancel()
	}

	type dialResult struct {
		conn net.Conn
		err  error
		addr string
	}

	results := make(chan dialResult, len(addrs))
	var pendingDials int

	// Use a dialer with the context for proper cancellation
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
	}

	// Start connection attempts with staggered delays per RFC 8305
	for i, addr := range addrs {
		// Wait for the staggered delay before starting this attempt,
		// but check if we already have a successful connection
		if i > 0 {
			select {
			case result := <-results:
				pendingDials--
				if result.err == nil {
					return configureConnection(result.conn)
				}
				// Put the error result back for later collection
				results <- result
				pendingDials++
			case <-time.After(happyEyeballsDelay):
				// Timeout elapsed, start next connection attempt
			case <-ctx.Done():
				return nil, fmt.Errorf("%s: %s (context cancelled)", connectErrorPrefix, address)
			}
		}

		pendingDials++
		go func(addr string) {
			target := net.JoinHostPort(addr, port)
			conn, err := dialer.DialContext(ctx, network, target)
			results <- dialResult{conn: conn, err: err, addr: addr}
		}(addr)
	}

	// Collect results
	var errs []error
	var connections []net.Conn

	for pendingDials > 0 {
		select {
		case result := <-results:
			pendingDials--
			if result.err != nil {
				errs = append(errs, result.err)
			} else {
				connections = append(connections, result.conn)
			}

		case <-ctx.Done():
			// Close any connections we've established
			for _, conn := range connections {
				_ = conn.Close()
			}

			return nil, fmt.Errorf("%s: %s (context cancelled)", connectErrorPrefix, address)
		}
	}

	// Return the first successful connection, close the rest
	if len(connections) > 0 {
		for i := 1; i < len(connections); i++ {
			_ = connections[i].Close()
		}

		return configureConnection(connections[0])
	}

	return nil, fmt.Errorf("%s: %s (%v)", connectErrorPrefix, address, errs)
}

// configureConnection sets up TCP keep-alive on the connection.
func configureConnection(c net.Conn) (net.Conn, error) {
	tc, ok := c.(*net.TCPConn)
	if ok {
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(3 * time.Second)
	}

	return c, nil
}

// IsConnectionError returns true if the given error is due to the dialer not being able to connect to the target.
func IsConnectionError(err error) bool {
	// FIXME: Unfortunately the client currently does not provide a way to differentiate between errors.
	return strings.Contains(err.Error(), connectErrorPrefix)
}

// InitTLSConfig returns a tls.Config populated with default encryption
// parameters. This is used as baseline config for both client and server
// certificates.
func InitTLSConfig() *tls.Config {
	config := &tls.Config{}

	// Restrict to TLS 1.3 unless INCUS_INSECURE_TLS is set.
	if util.IsFalseOrEmpty(os.Getenv("INCUS_INSECURE_TLS")) {
		config.MinVersion = tls.VersionTLS13
	} else {
		config.MinVersion = tls.VersionTLS12
	}

	return config
}

// TLSConfigWithTrustedCert sets the given remote certificate as a CA and assigns the certificate's first DNS Name as the tls.Config ServerName.
// This lets us maintain default verification without strictly matching a request URL to the certificate SANs.
func TLSConfigWithTrustedCert(tlsConfig *tls.Config, tlsRemoteCert *x509.Certificate) {
	// Setup RootCA
	if tlsConfig.RootCAs == nil {
		tlsConfig.RootCAs, _ = systemCertPool()
	}

	// Trusted certificates
	if tlsRemoteCert != nil {
		if tlsConfig.RootCAs == nil {
			tlsConfig.RootCAs = x509.NewCertPool()
		}

		// Make it a valid RootCA
		tlsRemoteCert.IsCA = true
		tlsRemoteCert.KeyUsage = x509.KeyUsageCertSign

		// Setup the pool
		tlsConfig.RootCAs.AddCert(tlsRemoteCert)

		// Set the ServerName
		if tlsRemoteCert.DNSNames != nil {
			tlsConfig.ServerName = tlsRemoteCert.DNSNames[0]
		}
	}
}

// GetTLSConfig returns the TLS config for the provided remote certificate.
func GetTLSConfig(tlsRemoteCert *x509.Certificate) (*tls.Config, error) {
	tlsConfig := InitTLSConfig()

	TLSConfigWithTrustedCert(tlsConfig, tlsRemoteCert)

	return tlsConfig, nil
}

// GetTLSConfigMem returns the TLS config for the provided client and server certificates.
func GetTLSConfigMem(tlsClientCert string, tlsClientKey string, tlsClientCA string, tlsRemoteCertPEM string, insecureSkipVerify bool) (*tls.Config, error) {
	tlsConfig := InitTLSConfig()

	// Client authentication
	if tlsClientCert != "" && tlsClientKey != "" {
		cert, err := tls.X509KeyPair([]byte(tlsClientCert), []byte(tlsClientKey))
		if err != nil {
			return nil, err
		}

		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	var tlsRemoteCert *x509.Certificate
	if tlsRemoteCertPEM != "" {
		// Ignore any content outside of the PEM bytes we care about
		certBlock, _ := pem.Decode([]byte(tlsRemoteCertPEM))
		if certBlock == nil {
			return nil, errors.New("Invalid remote certificate")
		}

		var err error
		tlsRemoteCert, err = x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			return nil, err
		}
	}

	if tlsClientCA != "" {
		caPool := x509.NewCertPool()
		caPool.AppendCertsFromPEM([]byte(tlsClientCA))

		tlsConfig.RootCAs = caPool
	}

	TLSConfigWithTrustedCert(tlsConfig, tlsRemoteCert)

	// Only skip TLS verification if no remote certificate is available.
	if tlsRemoteCert == nil {
		tlsConfig.InsecureSkipVerify = insecureSkipVerify
	}

	return tlsConfig, nil
}
