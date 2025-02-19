//go:build linux && cgo && !agent

package cluster

// Code generation directives.
//
//go:generate -command mapper generate-database db mapper -t certificate_projects.mapper.go
//go:generate mapper generate -i -b "//go:build linux && cgo && !agent"
//
//generate-database:mapper stmt certificate_project objects
//generate-database:mapper stmt certificate_project objects-by-CertificateID
//generate-database:mapper stmt certificate_project create struct=CertificateProject
//generate-database:mapper stmt certificate_project delete-by-CertificateID
//
//generate-database:mapper method certificate_project GetMany struct=Certificate
//generate-database:mapper method certificate_project DeleteMany struct=Certificate
//generate-database:mapper method certificate_project Create struct=Certificate
//generate-database:mapper method certificate_project Update struct=Certificate

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
