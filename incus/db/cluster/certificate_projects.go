//go:build linux && cgo && !agent

package cluster

// Code generation directives.
//
//go:generate -command mapper incus-generate db mapper -t certificate_projects.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e certificate_project objects
//go:generate mapper stmt -e certificate_project objects-by-CertificateID
//go:generate mapper stmt -e certificate_project create struct=CertificateProject
//go:generate mapper stmt -e certificate_project delete-by-CertificateID
//
//go:generate mapper method -i -e certificate_project GetMany struct=Certificate
//go:generate mapper method -i -e certificate_project DeleteMany struct=Certificate
//go:generate mapper method -i -e certificate_project Create struct=Certificate
//go:generate mapper method -i -e certificate_project Update struct=Certificate

// CertificateProject is an association table struct that associates
// Certificates to Projects.
type CertificateProject struct {
	CertificateID int `db:"primary=yes"`
	ProjectID     int
}

// CertificateProjectFilter specifies potential query parameter fields.
type CertificateProjectFilter struct {
	CertificateID *int
	ProjectID     *int
}
