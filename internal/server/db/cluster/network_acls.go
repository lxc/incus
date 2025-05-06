//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"

	"github.com/lxc/incus/v6/shared/api"
)

// Code generation directives.
//generate-database:mapper target network_acls.mapper.go
//generate-database:mapper reset -i -b "//go:build linux && cgo && !agent"
//
// Statements:
//generate-database:mapper stmt -e NetworkACL objects table=networks_acls
//generate-database:mapper stmt -e NetworkACL objects-by-ID table=networks_acls
//generate-database:mapper stmt -e NetworkACL objects-by-Name table=networks_acls
//generate-database:mapper stmt -e NetworkACL objects-by-Project table=networks_acls
//generate-database:mapper stmt -e NetworkACL objects-by-Project-and-Name table=networks_acls
//generate-database:mapper stmt -e NetworkACL id table=networks_acls
//generate-database:mapper stmt -e NetworkACL create table=networks_acls
//generate-database:mapper stmt -e NetworkACL rename table=networks_acls
//generate-database:mapper stmt -e NetworkACL update table=networks_acls
//generate-database:mapper stmt -e NetworkACL delete-by-ID table=networks_acls
//
// Methods:
//generate-database:mapper method -i -e NetworkACL GetMany references=Config table=networks_acls
//generate-database:mapper method -i -e NetworkACL GetOne table=networks_acls
//generate-database:mapper method -i -e NetworkACL Exists table=networks_acls
//generate-database:mapper method -i -e NetworkACL Create references=Config table=networks_acls
//generate-database:mapper method -i -e NetworkACL ID table=networks_acls
//generate-database:mapper method -i -e NetworkACL Rename table=networks_acls
//generate-database:mapper method -i -e NetworkACL Update references=Config table=networks_acls
//generate-database:mapper method -i -e NetworkACL DeleteOne-by-ID table=networks_acls

// NetworkACL is a value object holding db-related details about a network ACL.
type NetworkACL struct {
	ID          int    `db:"order=yes"`
	ProjectID   int    `db:"omit=create,update"`
	Project     string `db:"primary=yes&join=projects.name"`
	Name        string `db:"primary=yes"`
	Description string
	Ingress     []api.NetworkACLRule `db:"marshal=json"`
	Egress      []api.NetworkACLRule `db:"marshal=json"`
}

// NetworkACLFilter specifies potential query parameter fields.
type NetworkACLFilter struct {
	ID      *int
	Name    *string
	Project *string
}

// ToAPI converts the DB record into the shared/api form.
func (n *NetworkACL) ToAPI(ctx context.Context, tx *sql.Tx) (*api.NetworkACL, error) {
	cfg, err := GetNetworkACLConfig(ctx, tx, n.ID)
	if err != nil {
		return nil, err
	}

	out := api.NetworkACL{
		NetworkACLPost: api.NetworkACLPost{
			Name: n.Name,
		},
		NetworkACLPut: api.NetworkACLPut{
			Description: n.Description,
			Config:      cfg,
			Ingress:     n.Ingress,
			Egress:      n.Egress,
		},
	}

	return &out, nil
}
