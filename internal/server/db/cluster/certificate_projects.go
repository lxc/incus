//go:build linux && cgo && !agent

package cluster

// Code generation directives.
//
//generate-database:mapper target certificate_projects.mapper.go
//generate-database:mapper reset -i -b "//go:build linux && cgo && !agent"
//
//generate-database:mapper stmt -e certificate_project objects
//generate-database:mapper stmt -e certificate_project objects-by-CertificateID
//generate-database:mapper stmt -e certificate_project create struct=CertificateProject
//generate-database:mapper stmt -e certificate_project delete-by-CertificateID
//
//generate-database:mapper method -i -e certificate_project GetMany struct=Certificate
//generate-database:mapper method -i -e certificate_project DeleteMany struct=Certificate
//generate-database:mapper method -i -e certificate_project Create struct=Certificate
//generate-database:mapper method -i -e certificate_project Update struct=Certificate

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
