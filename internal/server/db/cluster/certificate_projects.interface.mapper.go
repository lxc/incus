//go:build linux && cgo && !agent

package cluster

import "context"

// CertificateProjectGenerated is an interface of generated methods for CertificateProject.
type CertificateProjectGenerated interface {
	// GetCertificateProjects returns all available Projects for the Certificate.
	// generator: certificate_project GetMany
	GetCertificateProjects(ctx context.Context, db tx, certificateID int) ([]Project, error)

	// DeleteCertificateProjects deletes the certificate_project matching the given key parameters.
	// generator: certificate_project DeleteMany
	DeleteCertificateProjects(ctx context.Context, db tx, certificateID int) error

	// CreateCertificateProjects adds a new certificate_project to the database.
	// generator: certificate_project Create
	CreateCertificateProjects(ctx context.Context, db tx, objects []CertificateProject) error

	// UpdateCertificateProjects updates the certificate_project matching the given key parameters.
	// generator: certificate_project Update
	UpdateCertificateProjects(ctx context.Context, db tx, certificateID int, projectNames []string) error
}
