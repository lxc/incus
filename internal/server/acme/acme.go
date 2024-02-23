package acme

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/go-acme/lego/v4/acme"
	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"

	"github.com/lxc/incus/internal/server/state"
	internalUtil "github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/util"
)

// retries describes the number of retries after which Incus will give up registering a user or
// issuing a new certificate. The number 5 was chosen because Let's Encrypt has a limit of 5
// failures per account, per hostname, per hour.
const retries = 5

// ClusterCertFilename describes the filename of the new certificate which is stored in case it
// cannot be distributed in a cluster due to offline members. Incus will try to distribute this
// certificate at a later stage.
const ClusterCertFilename = "cluster.crt.new"

// certificateNeedsUpdate returns true if the domain doesn't match the certificate's DNS names
// or it's valid for less than 30 days.
func certificateNeedsUpdate(domain string, cert *x509.Certificate) bool {
	return !slices.Contains(cert.DNSNames, domain) || time.Now().After(cert.NotAfter.Add(-30*24*time.Hour))
}

// UpdateCertificate updates the certificate.
func UpdateCertificate(s *state.State, provider HTTP01Provider, clustered bool, domain string, email string, caURL string, force bool) (*certificate.Resource, error) {
	clusterCertFilename := internalUtil.VarPath(ClusterCertFilename)

	l := logger.AddContext(logger.Ctx{"domain": domain, "caURL": caURL})

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
			return &certificate.Resource{
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

	// Generate new private key for user. This key needs to be different from the server's private key.
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("Failed generating private key for user account: %w", err)
	}

	user := user{
		Email: email,
		Key:   privateKey,
	}

	config := lego.NewConfig(&user)

	if caURL != "" {
		config.CADirURL = caURL
	} else {
		// Default URL for Let's Encrypt
		config.CADirURL = "https://acme-v02.api.letsencrypt.org/directory"
	}

	config.Certificate.KeyType = certcrypto.RSA2048

	client, err := lego.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("Failed to create new client: %w", err)
	}

	err = client.Challenge.SetHTTP01Provider(provider)
	if err != nil {
		return nil, fmt.Errorf("Failed setting HTTP-01 provider: %w", err)
	}

	var reg *registration.Resource

	// Registration might fail randomly (as seen in manual tests), so retry in that case.
	for i := 0; i < retries; i++ {
		reg, err = client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err == nil {
			break
		}

		// In case we were rate limited, don't try again.
		details, ok := err.(*acme.ProblemDetails)
		if ok && details.Type == "urn:ietf:params:acme:error:rateLimited" {
			break
		}

		l.Warn("Failed to register user, retrying in 10 seconds", logger.Ctx{"err": err})
		time.Sleep(10 * time.Second)
	}

	if err != nil {
		return nil, fmt.Errorf("Failed to register user: %w", err)
	}

	user.Registration = reg

	request := certificate.ObtainRequest{
		Domains:    []string{domain},
		Bundle:     true,
		PrivateKey: certInfo.KeyPair().PrivateKey,
	}

	var certificates *certificate.Resource

	l.Info("Issuing certificate")

	// Get new certificate.
	// This might fail randomly (as seen in manual tests), so retry in that case.
	for i := 0; i < retries; i++ {
		certificates, err = client.Certificate.Obtain(request)
		if err == nil {
			break
		}

		// In case we were rate limited, don't try again.
		details, ok := err.(*acme.ProblemDetails)
		if ok && details.Type == "urn:ietf:params:acme:error:rateLimited" {
			break
		}

		l.Warn("Failed to obtain certificate, retrying in 10 seconds", logger.Ctx{"err": err})
		time.Sleep(10 * time.Second)
	}

	if err != nil {
		return nil, fmt.Errorf("Failed to obtain certificate: %w", err)
	}

	l.Info("Finished issuing certificate")

	return certificates, nil
}
