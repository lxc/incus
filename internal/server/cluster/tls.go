package cluster

import (
	"crypto/x509"
	"fmt"
	"net/http"

	localUtil "github.com/lxc/incus/v7/internal/server/util"
	"github.com/lxc/incus/v7/shared/logger"
	localtls "github.com/lxc/incus/v7/shared/tls"
)

// CheckCert checks certificate access, returns true if certificate is trusted.
func CheckCert(r *http.Request, networkCert *localtls.CertInfo, serverCert *localtls.CertInfo, trustedCerts map[string]x509.Certificate) bool {
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
		// Trust our own server certificate. This allows Cowsql to start with a connection back to this
		// member before the database is available. It also allows us to switch the server certificate to
		// the network certificate during cluster upgrade to per-server certificates, and it be trusted.
		trustedServerCert, _ := x509.ParseCertificate(serverCert.KeyPair().Certificate[0])
		trusted, _ := localUtil.CheckTrustState(*i, map[string]x509.Certificate{serverCert.Fingerprint(): *trustedServerCert}, networkCert, false)
		if trusted {
			return true
		}

		// Check the trusted server certificates list provided.
		trusted, _ = localUtil.CheckTrustState(*i, trustedCerts, networkCert, false)
		if trusted {
			return true
		}

		logger.Errorf("Invalid client certificate %v (%v) from %v", i.Subject, localtls.CertFingerprint(i), r.RemoteAddr)
	}

	return false
}
