//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"fmt"
	"net/http"

	"github.com/lxc/incus/v6/shared/api"
)

// Code generation directives.
//generate-database:mapper target networks_acls.mapper.go
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
func (n *NetworkACL) ToAPI(ctx context.Context, db tx) (*api.NetworkACL, error) {
	cfg, err := GetNetworkACLConfig(ctx, db, n.ID)
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

// GetNetworkACLAPI returns the Network ACL API struct for the ACL with the given name in the given project.
func GetNetworkACLAPI(ctx context.Context, db tx, projectName string, name string) (int, *api.NetworkACL, error) {
	acls, err := GetNetworkACLs(ctx, db, NetworkACLFilter{Project: &projectName, Name: &name})
	if err != nil {
		return -1, nil, err
	}

	if len(acls) == 0 {
		return -1, nil, api.StatusErrorf(http.StatusNotFound, "Network ACL not found")
	}

	acl := acls[0]
	apiACL, err := acl.ToAPI(ctx, db)
	if err != nil {
		return -1, nil, fmt.Errorf("Failed loading config: %w", err)
	}

	return acl.ID, apiACL, nil
}

// UpdateNetworkACLAPI updates the Network ACL with the given ID using the provided API struct.
func UpdateNetworkACLAPI(ctx context.Context, db tx, id int64, put *api.NetworkACLPut) error {
	// Fetch existing to recover project and name.
	idInt := int(id)
	acls, err := GetNetworkACLs(ctx, db, NetworkACLFilter{ID: &idInt})
	if err != nil {
		return err
	}

	if len(acls) == 0 {
		return api.StatusErrorf(http.StatusNotFound, "Network ACL not found")
	}

	curr := acls[0]
	upd := NetworkACL{
		Project:     curr.Project,
		Name:        curr.Name,
		Description: put.Description,
		Ingress:     put.Ingress,
		Egress:      put.Egress,
	}

	err = UpdateNetworkACL(ctx, db, curr.Project, curr.Name, upd)
	if err != nil {
		return err
	}

	err = UpdateNetworkACLConfig(ctx, db, id, put.Config)
	if err != nil {
		return err
	}

	return nil
}
