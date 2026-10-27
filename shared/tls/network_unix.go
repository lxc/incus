//go:build !windows

package tls

import (
	"crypto/x509"
)

func systemCertPool() (*x509.CertPool, error) {
	// Get the system pool
	pool, err := x509.SystemCertPool()
	if err != nil {
		return nil, err
	}

	return pool, nil
}
