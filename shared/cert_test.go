package shared_test

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/lxc/incus/shared"
)

// A new key pair is generated if none exists and saved to the appropriate
// files.
func TestKeyPairAndCA(t *testing.T) {
	dir, err := os.MkdirTemp("", "lxd-shared-test-")
	if err != nil {
		t.Errorf("failed to create temporary dir: %v", err)
	}

	defer func() { _ = os.RemoveAll(dir) }()

	info, err := shared.KeyPairAndCA(dir, "test", shared.CertServer, true)
	if err != nil {
		t.Errorf("initial call to KeyPairAndCA failed: %v", err)
	}

	if info.CA() != nil {
		t.Errorf("expected CA certificate to be nil")
	}

	if len(info.KeyPair().Certificate) == 0 {
		t.Errorf("expected key pair to be non-empty")
	}

	if !shared.PathExists(filepath.Join(dir, "test.crt")) {
		t.Errorf("no public key file was saved")
	}

	if !shared.PathExists(filepath.Join(dir, "test.key")) {
		t.Errorf("no secret key file was saved")
	}

	cert, err := x509.ParseCertificate(info.KeyPair().Certificate[0])
	if err != nil {
		t.Errorf("failed to parse generated public x509 key cert: %v", err)
	}

	if cert.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Errorf("expected to find server auth key usage extension")
	}

	block, _ := pem.Decode(info.PublicKey())
	if block == nil {
		t.Errorf("expected PublicKey to be decodable")
		return
	}

	_, err = x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Errorf("failed to parse encoded public x509 key cert: %v", err)
	}
}

func TestGenerateMemCert(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cert generation in short mode")
	}

	cert, key, err := shared.GenerateMemCert(false, true)
	if err != nil {
		t.Error(err)
		return
	}

	if cert == nil {
		t.Error("GenerateMemCert returned a nil cert")
		return
	}

	if key == nil {
		t.Error("GenerateMemCert returned a nil key")
		return
	}

	block, rest := pem.Decode(cert)
	if len(rest) != 0 {
		t.Errorf("GenerateMemCert returned a cert with trailing content: %q", string(rest))
	}

	if block.Type != "CERTIFICATE" {
		t.Errorf("GenerateMemCert returned a cert with Type %q not \"CERTIFICATE\"", block.Type)
	}

	block, rest = pem.Decode(key)
	if len(rest) != 0 {
		t.Errorf("GenerateMemCert returned a key with trailing content: %q", string(rest))
	}

	if block.Type != "EC PRIVATE KEY" {
		t.Errorf("GenerateMemCert returned a cert with Type %q not \"EC PRIVATE KEY\"", block.Type)
	}
}
