package util

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/lxc/incus/shared"
)

// LoadCert reads the server certificate from the given var dir.
//
// If a cluster certificate is found it will be loaded instead.
// If neither a server or cluster certfificate exists, a new server certificate will be generated.
func LoadCert(dir string) (*shared.CertInfo, error) {
	prefix := "server"
	if shared.PathExists(filepath.Join(dir, "cluster.crt")) {
		prefix = "cluster"
	}

	cert, err := shared.KeyPairAndCA(dir, prefix, shared.CertServer, true)
	if err != nil {
		return nil, fmt.Errorf("failed to load TLS certificate: %w", err)
	}

	return cert, nil
}

// LoadClusterCert reads the cluster certificate from the given var dir.
//
// If a cluster certificate doesn't exist, a new one is generated.
func LoadClusterCert(dir string) (*shared.CertInfo, error) {
	prefix := "cluster"

	cert, err := shared.KeyPairAndCA(dir, prefix, shared.CertServer, true)
	if err != nil {
		return nil, fmt.Errorf("failed to load cluster TLS certificate: %w", err)
	}

	return cert, nil
}

// LoadServerCert reads the server certificate from the given var dir.
func LoadServerCert(dir string) (*shared.CertInfo, error) {
	prefix := "server"
	cert, err := shared.KeyPairAndCA(dir, prefix, shared.CertServer, true)
	if err != nil {
		return nil, fmt.Errorf("failed to load TLS certificate: %w", err)
	}

	return cert, nil
}

// WriteCert writes the given material to the appropriate certificate files in
// the given directory.
func WriteCert(dir, prefix string, cert, key, ca []byte) error {
	err := os.WriteFile(filepath.Join(dir, prefix+".crt"), cert, 0644)
	if err != nil {
		return err
	}

	err = os.WriteFile(filepath.Join(dir, prefix+".key"), key, 0600)
	if err != nil {
		return err
	}

	if ca != nil {
		err = os.WriteFile(filepath.Join(dir, prefix+".ca"), ca, 0644)
		if err != nil {
			return err
		}
	}

	return nil
}
