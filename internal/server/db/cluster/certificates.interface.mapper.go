//go:build linux && cgo && !agent

package cluster

import (
	"context"

	"github.com/lxc/incus/v6/internal/server/certificate"
)

// CertificateGenerated is an interface of generated methods for Certificate.
type CertificateGenerated interface {
	// GetCertificates returns all available certificates.
	// generator: certificate GetMany
	GetCertificates(ctx context.Context, db dbtx, filters ...CertificateFilter) ([]Certificate, error)

	// GetCertificate returns the certificate with the given key.
	// generator: certificate GetOne
	GetCertificate(ctx context.Context, db dbtx, fingerprint string) (*Certificate, error)

	// GetCertificateID return the ID of the certificate with the given key.
	// generator: certificate ID
	GetCertificateID(ctx context.Context, db tx, fingerprint string) (int64, error)

	// CertificateExists checks if a certificate with the given key exists.
	// generator: certificate Exists
	CertificateExists(ctx context.Context, db dbtx, fingerprint string) (bool, error)

	// CreateCertificate adds a new certificate to the database.
	// generator: certificate Create
	CreateCertificate(ctx context.Context, db dbtx, object Certificate) (int64, error)

	// DeleteCertificate deletes the certificate matching the given key parameters.
	// generator: certificate DeleteOne-by-Fingerprint
	DeleteCertificate(ctx context.Context, db dbtx, fingerprint string) error

	// DeleteCertificates deletes the certificate matching the given key parameters.
	// generator: certificate DeleteMany-by-Name-and-Type
	DeleteCertificates(ctx context.Context, db dbtx, name string, certificateType certificate.Type) error

	// UpdateCertificate updates the certificate matching the given key parameters.
	// generator: certificate Update
	UpdateCertificate(ctx context.Context, db tx, fingerprint string, object Certificate) error
}
