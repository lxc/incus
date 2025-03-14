package acme

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/lxc/incus/v6/internal/server/state"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/subprocess"
	localtls "github.com/lxc/incus/v6/shared/tls"
	"github.com/lxc/incus/v6/shared/util"
)

// retries describes the number of retries after which Incus will give up registering a user or
// issuing a new certificate. The number 5 was chosen because Let's Encrypt has a limit of 5
// failures per account, per hostname, per hour.
const retries = 5

// ClusterCertFilename describes the filename of the new certificate which is stored in case it
// cannot be distributed in a cluster due to offline members. Incus will try to distribute this
// certificate at a later stage.
const ClusterCertFilename = "cluster.crt.new"

// CertKeyPair describes a certificate and its private key.
type CertKeyPair struct {
	Certificate []byte `json:"-"`
	PrivateKey  []byte `json:"-"`
}

// certificateNeedsUpdate returns true if the domain doesn't match the certificate's DNS names
// or it's valid for less than 30 days.
func certificateNeedsUpdate(domain string, cert *x509.Certificate) bool {
	return !slices.Contains(cert.DNSNames, domain) || time.Now().After(cert.NotAfter.Add(-30*24*time.Hour))
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

		if !certificateNeedsUpdate(domain, cert) {
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

	if !force && !certificateNeedsUpdate(domain, cert) {
		l.Debug("Skipping certificate renewal as it is still valid for more than 30 days")
		return nil, nil
	}

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

	env := os.Environ()

	args := []string{
		"--accept-tos",
		"--domains", domain,
		"--email", email,
		"--path", tmpDir,
		"--server", caURL,
	}

	if challengeType == "DNS-01" {
		provider, environment, resolvers := s.GlobalConfig.ACMEDNS()

		env = append(env, environment...)

		if provider == "" {
			return nil, fmt.Errorf("DNS-01 challenge type requires acme.dns.provider configuration key to be set")
		}

		args = append(args, "--dns", provider)
		if len(resolvers) > 0 {
			for _, resolver := range resolvers {
				args = append(args, "--dns.resolvers", resolver)
			}
		}
	} else if challengeType == "HTTP-01" {
		args = append(args, "--http")

		port := s.GlobalConfig.ACMEHTTP()
		if port != "" {
			args = append(args, "--http.port", port)
		}
	}

	args = append(args, "run")

	_, _, err = subprocess.RunCommandSplit(context.TODO(), env, nil, "lego", args...)
	if err != nil {
		return nil, fmt.Errorf("Failed to run lego command: %w", err)
	}

	certInfo, err = localtls.KeyPairAndCA(tmpDir+"/certificates", domain, localtls.CertServer, true)
	if err != nil {
		return nil, fmt.Errorf("Failed to load certificate and key file: %w", err)
	}

	return &CertKeyPair{
		Certificate: certInfo.PublicKey(),
		PrivateKey:  certInfo.PrivateKey(),
	}, nil
}
