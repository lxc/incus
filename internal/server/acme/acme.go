package acme

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/internal/server/cluster"
	"github.com/lxc/incus/v7/internal/server/state"
	internalUtil "github.com/lxc/incus/v7/internal/util"
	"github.com/lxc/incus/v7/shared/logger"
	incustls "github.com/lxc/incus/v7/shared/tls"
	"github.com/lxc/incus/v7/shared/util"
)

// ClusterCertFilename describes the filename of the new certificate which is stored in case it
// cannot be distributed in a cluster due to offline members. Incus will try to distribute this
// certificate at a later stage.
const ClusterCertFilename = "cluster.crt.new"

// CertKeyPair describes a certificate and its private key.
type CertKeyPair struct {
	Certificate []byte `json:"-"`
	PrivateKey  []byte `json:"-"`
}

// UpdateCertificate updates the certificate.
func UpdateCertificate(s *state.State, challengeType string, clustered bool, domain string, email string, caURL string, force bool) (*CertKeyPair, error) {
	clusterCertFilename := internalUtil.VarPath(ClusterCertFilename)

	l := logger.AddContext(logger.Ctx{"domain": domain, "caURL": caURL, "challenge": challengeType})

	// If clusterCertFilename exists, it means that a previously issued certificate couldn't be
	// distributed to all cluster members and was therefore kept back. In this case, don't issue
	// a new certificate but return the previously issued one.
	if !force && clustered && util.PathExists(clusterCertFilename) {
		keyFilename := internalUtil.VarPath("cluster.key")

		clusterCert, err := os.ReadFile(clusterCertFilename)
		if err != nil {
			return nil, fmt.Errorf("Failed reading cluster certificate file: %w", err)
		}

		key, err := os.ReadFile(keyFilename)
		if err != nil {
			return nil, fmt.Errorf("Failed reading cluster key file: %w", err)
		}

		keyPair, err := tls.X509KeyPair(clusterCert, key)
		if err != nil {
			return nil, fmt.Errorf("Failed to get keypair: %w", err)
		}

		cert, err := x509.ParseCertificate(keyPair.Certificate[0])
		if err != nil {
			return nil, fmt.Errorf("Failed to parse certificate: %w", err)
		}

		if !incustls.CertificateNeedsUpdate(domain, cert) {
			return &CertKeyPair{
				Certificate: clusterCert,
				PrivateKey:  key,
			}, nil
		}
	}

	if util.PathExists(clusterCertFilename) {
		_ = os.Remove(clusterCertFilename)
	}

	// Load the certificate.
	certInfo, err := internalUtil.LoadCert(s.OS.VarDir)
	if err != nil {
		return nil, fmt.Errorf("Failed to load certificate and key file: %w", err)
	}

	cert, err := x509.ParseCertificate(certInfo.KeyPair().Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("Failed to parse certificate: %w", err)
	}

	if !force && !incustls.CertificateNeedsUpdate(domain, cert) {
		l.Debug("Skipping certificate renewal as it hasn't reached 80% of its validity period")
		return nil, nil
	}

	port := s.GlobalConfig.ACMEHTTP()
	provider, environment, resolvers := s.GlobalConfig.ACMEDNS()
	eabKid, eabHmac := s.GlobalConfig.ACMEEAB()
	proxy := s.GlobalConfig.ProxyHTTPS()

	tmpDir, err := os.MkdirTemp("", "lego")
	if err != nil {
		return nil, fmt.Errorf("Failed to create temporary directory: %w", err)
	}

	defer func() {
		err := os.RemoveAll(tmpDir)
		if err != nil {
			logger.Warn("Failed to remove temporary directory", logger.Ctx{"err": err})
		}
	}()

	// Have the other cluster members forward HTTP-01 challenges for their own names.
	if clustered && challengeType == "HTTP-01" {
		notifier, err := cluster.NewNotifier(s, s.Endpoints.NetworkCert(), s.ServerCert(), cluster.NotifyAlive)
		if err != nil {
			return nil, err
		}

		notify := func(method string) {
			err := notifier(func(client incus.InstanceServer) error {
				_, _, err := client.RawQuery(method, "/internal/acme/challenge", nil, "")
				if err != nil {
					info, _ := client.GetConnectionInfo()
					l.Warn("Failed to update the ACME challenge listener on a cluster member", logger.Ctx{"method": method, "member": info.URL, "err": err})
				}

				return nil
			})
			if err != nil {
				l.Warn("Failed to notify the cluster members of the ACME challenge", logger.Ctx{"method": method, "err": err})
			}
		}

		notify("POST")
		defer notify("DELETE")
	}

	certBytes, keyBytes, err := incustls.RunACMEChallenge(context.TODO(), tmpDir, caURL, domain, email, challengeType, provider, port, proxy, eabKid, eabHmac, resolvers, environment)
	if err != nil {
		return nil, err
	}

	return &CertKeyPair{
		Certificate: certBytes,
		PrivateKey:  keyBytes,
	}, nil
}
