package cluster

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"time"

	"github.com/lxc/incus/internal/server/certificate"
	localUtil "github.com/lxc/incus/internal/server/util"
	"github.com/lxc/incus/shared/logger"
	localtls "github.com/lxc/incus/shared/tls"
)

// Return a TLS configuration suitable for establishing intra-member network connections using the server cert.
func tlsClientConfig(networkCert *localtls.CertInfo, serverCert *localtls.CertInfo) (*tls.Config, error) {
	if networkCert == nil {
		return nil, fmt.Errorf("Invalid networkCert")
	}

	if serverCert == nil {
		return nil, fmt.Errorf("Invalid serverCert")
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

		// Check the trusted server certficates list provided.
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
func tlsTransport(config *tls.Config) (*http.Transport, func()) {
	transport := &http.Transport{
		TLSClientConfig:       config,
		DisableKeepAlives:     true,
		MaxIdleConns:          0,
		ExpectContinueTimeout: time.Second * 30,
		ResponseHeaderTimeout: time.Second * 3600,
		TLSHandshakeTimeout:   time.Second * 5,
	}

	return transport, transport.CloseIdleConnections
}
