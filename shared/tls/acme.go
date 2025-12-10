package tls

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/lxc/incus/v6/shared/subprocess"
)

// CertificateNeedsUpdate returns true if the domain doesn't match the certificate's DNS names
// or it's valid for less than the threshold.
func CertificateNeedsUpdate(domain string, cert *x509.Certificate, threshold time.Duration) bool {
	return !slices.Contains(cert.DNSNames, domain) || time.Now().After(cert.NotAfter.Add(-threshold))
}

// RunACMEChallenge runs an ACME challenge to fetch updated certificates with `lego`.
func RunACMEChallenge(ctx context.Context, dir, caURL, domain, email, challengeType, provider, port, proxy string, resolvers, environment []string) ([]byte, []byte, error) {
	env := os.Environ()

	args := []string{
		"--accept-tos",
		"--domains", domain,
		"--email", email,
		"--path", dir,
		"--server", caURL,
	}

	switch challengeType {
	case "DNS-01":
		env = append(env, environment...)
		if provider == "" {
			return nil, nil, errors.New("DNS-01 challenge type requires acme.dns.provider configuration key to be set")
		}

		args = append(args, "--dns", provider)
		if len(resolvers) > 0 {
			for _, resolver := range resolvers {
				args = append(args, "--dns.resolvers", resolver)
			}
		}

	case "HTTP-01":
		args = append(args, "--http")
		if port != "" {
			args = append(args, "--http.port", port)
		}
	}

	args = append(args, "run")
	if proxy != "" {
		env = append(env, "https_proxy="+proxy)
	}

	_, _, err := subprocess.RunCommandSplit(ctx, env, nil, "lego", args...)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to run lego command: %w", err)
	}

	// Load the generated certificate.
	certData, err := os.ReadFile(filepath.Join(dir, "certificates", fmt.Sprintf("%s.crt", domain)))
	if err != nil {
		return nil, nil, err
	}

	caData, err := os.ReadFile(filepath.Join(dir, "certificates", fmt.Sprintf("%s.issuer.crt", domain)))
	if err != nil {
		return nil, nil, err
	}

	keyData, err := os.ReadFile(filepath.Join(dir, "certificates", fmt.Sprintf("%s.key", domain)))
	if err != nil {
		return nil, nil, err
	}

	return append(certData, caData...), keyData, nil
}
