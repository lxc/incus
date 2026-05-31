package tls

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lxc/incus/v7/shared/subprocess"
	"github.com/lxc/incus/v7/shared/util"
)

// CertificateNeedsUpdate returns true if the domain doesn't match the certificate's DNS names
// or it's valid for less than the threshold.
func CertificateNeedsUpdate(domain string, cert *x509.Certificate, threshold time.Duration) bool {
	if time.Now().After(cert.NotAfter.Add(-threshold)) {
		return true
	}

	domains := util.SplitNTrimSpace(domain, ",", -1, false)
	for _, entry := range domains {
		if !slices.Contains(cert.DNSNames, entry) {
			return true
		}
	}

	return false
}

// RunACMEChallenge runs an ACME challenge to fetch updated certificates with `lego`.
func RunACMEChallenge(ctx context.Context, dir, caURL, domain, email, challengeType, provider, port, proxy string, resolvers, environment []string) ([]byte, []byte, error) {
	env := os.Environ()

	// Detect the installed lego command line interface as it changed in lego v5.
	//
	// Lego v5 broke its CLI backward compatibility by moving some flags
	// from the root to the "run" sub-command as well as renaming other
	// options.
	//
	// Unfortunately because "lego --version" is often overridden by
	// packagers and other distributors, parsing the version string doesn't
	// really work (Debian reports "dev" for example). Instead, we need to look
	// at the help message...
	stdout, _, err := subprocess.RunCommandSplit(ctx, env, nil, "lego", "run", "--help")
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to determine lego version: %w", err)
	}

	legoV5 := strings.Contains(stdout, "--http.address")

	args := []string{
		"--accept-tos",
		"--email", email,
		"--path", dir,
		"--server", caURL,
	}

	domains := util.SplitNTrimSpace(domain, ",", -1, false)
	for _, entry := range domains {
		args = append(args, "-d", entry)
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
			// The "--http.port" flag was renamed to "--http.address" in lego v5.
			if legoV5 {
				args = append(args, "--http.address", port)
			} else {
				args = append(args, "--http.port", port)
			}
		}
	}

	// In lego v5 the "run" command must precede its flags, whereas in earlier
	// versions the flags were global and had to come before the "run" command.
	if legoV5 {
		args = append([]string{"run"}, args...)
	} else {
		args = append(args, "run")
	}

	if proxy != "" {
		env = append(env, "https_proxy="+proxy)
	}

	_, _, err = subprocess.RunCommandSplit(ctx, env, nil, "lego", args...)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to run lego command: %w", err)
	}

	// Load the generated certificate.
	certData, err := os.ReadFile(filepath.Join(dir, "certificates", fmt.Sprintf("%s.crt", domains[0])))
	if err != nil {
		return nil, nil, err
	}

	caData, err := os.ReadFile(filepath.Join(dir, "certificates", fmt.Sprintf("%s.issuer.crt", domains[0])))
	if err != nil {
		return nil, nil, err
	}

	keyData, err := os.ReadFile(filepath.Join(dir, "certificates", fmt.Sprintf("%s.key", domains[0])))
	if err != nil {
		return nil, nil, err
	}

	return append(certData, caData...), keyData, nil
}
