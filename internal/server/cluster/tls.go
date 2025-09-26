package cluster

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/lxc/incus/v6/internal/server/certificate"
	localUtil "github.com/lxc/incus/v6/internal/server/util"
	"github.com/lxc/incus/v6/shared/logger"
	localtls "github.com/lxc/incus/v6/shared/tls"
)

// Return a TLS configuration suitable for establishing intra-member network connections using the server cert.
func tlsClientConfig(networkCert *localtls.CertInfo, serverCert *localtls.CertInfo) (*tls.Config, error) {
	if networkCert == nil {
		return nil, errors.New("Invalid networkCert")
	}

	if serverCert == nil {
		return nil, errors.New("Invalid serverCert")
	}

	keypair := serverCert.KeyPair()
	config := localtls.InitTLSConfig()
	config.Certificates = []tls.Certificate{keypair}
	config.RootCAs = x509.NewCertPool()
	ca := serverCert.CA()
	if ca != nil {
		config.RootCAs.AddCert(ca)
	}

	// Since the same cluster keypair is used both as server and as client
	// cert, let's add it to the CA pool to make it trusted.
	networkKeypair := networkCert.KeyPair()
	netCert, err := x509.ParseCertificate(networkKeypair.Certificate[0])
	if err != nil {
		return nil, err
	}

	netCert.IsCA = true
	netCert.KeyUsage = x509.KeyUsageCertSign
	config.RootCAs.AddCert(netCert)

	// Always use network certificate's DNS name rather than server cert, so that it matches.
	if len(netCert.DNSNames) > 0 {
		config.ServerName = netCert.DNSNames[0]
	}

	return config, nil
}

// tlsCheckCert checks certificate access, returns true if certificate is trusted.
func tlsCheckCert(r *http.Request, networkCert *localtls.CertInfo, serverCert *localtls.CertInfo, trustedCerts map[certificate.Type]map[string]x509.Certificate) bool {
	_, err := x509.ParseCertificate(networkCert.KeyPair().Certificate[0])
	if err != nil {
		// Since we have already loaded this certificate, typically
		// using LoadX509KeyPair, an error should never happen, but
		// check for good measure.
		panic(fmt.Sprintf("Invalid keypair material: %v", err))
	}

	if r.TLS == nil {
		return false
	}

	for _, i := range r.TLS.PeerCertificates {
		// Trust our own server certificate. This allows Dqlite to start with a connection back to this
		// member before the database is available. It also allows us to switch the server certificate to
		// the network certificate during cluster upgrade to per-server certificates, and it be trusted.
		trustedServerCert, _ := x509.ParseCertificate(serverCert.KeyPair().Certificate[0])
		trusted, _ := localUtil.CheckTrustState(*i, map[string]x509.Certificate{serverCert.Fingerprint(): *trustedServerCert}, networkCert, false)
		if trusted {
			return true
		}

		// Check the trusted server certificates list provided.
		trusted, _ = localUtil.CheckTrustState(*i, trustedCerts[certificate.TypeServer], networkCert, false)
		if trusted {
			return true
		}

		logger.Errorf("Invalid client certificate %v (%v) from %v", i.Subject, localtls.CertFingerprint(i), r.RemoteAddr)
	}

	return false
}

// Return an http.Transport configured using the given configuration and a
// cleanup function to use to close all connections the transport has been
// used.
func tlsTransport(networkCert *localtls.CertInfo, serverCert *localtls.CertInfo) (*http.Transport, func(), error) {
	config, err := tlsClientConfig(networkCert, serverCert)
	if err != nil {
		return nil, nil, err
	}

	// Set InsecureSkipVerify as we're doing our own certificate validation below.
	config.InsecureSkipVerify = true

	transport := &http.Transport{
		TLSClientConfig:       config,
		DisableKeepAlives:     true,
		MaxIdleConns:          0,
		ExpectContinueTimeout: time.Second * 30,
		ResponseHeaderTimeout: time.Second * 3600,
		TLSHandshakeTimeout:   time.Second * 5,
	}

	transport.DialTLSContext = func(ctx context.Context, network string, addr string) (net.Conn, error) {
		// Establish the TCP connection.
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("Failed connecting to HTTPS endpoint %q: %w", addr, err)
		}

		// Get TLS connection.
		tlsConn := tls.Client(conn, config)

		// Validate the connection
		err = tlsConn.Handshake()
		if err != nil {
			_ = conn.Close()
			return nil, err
		}

		// Look for an exact match with the certificate provided.
		// But ignore any other issue (validity, scope, ...).
		cs := tlsConn.ConnectionState()

		if len(cs.PeerCertificates) < 1 {
			return nil, errors.New("Couldn't validate peer certificate")
		}

		certBlock, _ := pem.Decode([]byte(networkCert.PublicKey()))
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

		return tlsConn, nil
	}

	return transport, transport.CloseIdleConnections, nil
}
