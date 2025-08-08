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

// RFC3493Dialer connects to the specified server and returns the connection.
// If the connection cannot be established then an error with the connectErrorPrefix is returned.
func RFC3493Dialer(_ context.Context, network string, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}

	addrs, err := net.LookupHost(host)
	if err != nil {
		return nil, err
	}

	var errs []error
	for _, a := range addrs {
		c, err := net.DialTimeout(network, net.JoinHostPort(a, port), 10*time.Second)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		tc, ok := c.(*net.TCPConn)
		if ok {
			_ = tc.SetKeepAlive(true)
			_ = tc.SetKeepAlivePeriod(3 * time.Second)
		}

		return c, nil
	}

	return nil, fmt.Errorf("%s: %s (%v)", connectErrorPrefix, address, errs)
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

		// Trust the certificate even if it's expired.
		tlsConfig.Time = func() time.Time {
			if tlsRemoteCert.NotBefore.IsZero() && !tlsRemoteCert.NotAfter.IsZero() {
				// If the value is zero, it will be overwritten with time.Now, so use the epoch time instead.
				return time.Unix(0, 0)
			}

			return tlsRemoteCert.NotBefore
		}

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
