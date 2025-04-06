//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"

	"github.com/lxc/incus/v6/shared/api"
)

// Code generation directives.
//
//generate-database:mapper target networks_address_sets.mapper.go
//generate-database:mapper reset -i -b "//go:build linux && cgo && !agent"
//
//generate-database:mapper stmt -e network_address_set objects table=networks_address_sets
//generate-database:mapper stmt -e network_address_set objects-by-ID table=networks_address_sets
//generate-database:mapper stmt -e network_address_set objects-by-Name table=networks_address_sets
//generate-database:mapper stmt -e network_address_set objects-by-Project table=networks_address_sets
//generate-database:mapper stmt -e network_address_set objects-by-Project-and-Name table=networks_address_sets
//generate-database:mapper stmt -e network_address_set id table=networks_address_sets
//generate-database:mapper stmt -e network_address_set create struct=NetworkAddressSet table=networks_address_sets
//generate-database:mapper stmt -e network_address_set rename table=networks_address_sets
//generate-database:mapper stmt -e network_address_set update struct=NetworkAddressSet table=networks_address_sets
//generate-database:mapper stmt -e network_address_set delete-by-Project-and-Name table=networks_address_sets
//
//generate-database:mapper method -i -e network_address_set ID struct=NetworkAddressSet table=networks_address_sets
//generate-database:mapper method -i -e network_address_set Exists struct=NetworkAddressSet table=networks_address_sets
//generate-database:mapper method -i -e network_address_set GetMany references=Config table=networks_address_sets
//generate-database:mapper method -i -e network_address_set GetOne struct=NetworkAddressSet table=networks_address_sets
//generate-database:mapper method -i -e network_address_set Create references=Config table=networks_address_sets
//generate-database:mapper method -i -e network_address_set Rename table=networks_address_sets
//generate-database:mapper method -i -e network_address_set Update struct=NetworkAddressSet references=Config table=networks_address_sets
//generate-database:mapper method -i -e network_address_set DeleteOne-by-Project-and-Name table=networks_address_sets

// NetworkAddressSet is a value object holding db-related details about a network address_set.
type NetworkAddressSet struct {
	ID          int
	ProjectID   int      `db:"omit=create,update"`
	Project     string   `db:"primary=yes&join=projects.name"`
	Name        string   `db:"primary=yes"`
	Description string   `db:"coalesce=''"`
	Addresses   []string `db:"marshal=json"`
}

// NetworkAddressSetFilter specifies potential query parameter fields.
type NetworkAddressSetFilter struct {
	ID      *int
	Name    *string
	Project *string
}

// ToAPI converts the DB records to an API record.
func (n *NetworkAddressSet) ToAPI(ctx context.Context, tx *sql.Tx) (*api.NetworkAddressSet, error) {
	// Get the config.
	config, err := GetNetworkAddressSetConfig(ctx, tx, n.ID)
	if err != nil {
		return nil, err
	}

	// Fill in the struct.
	resp := api.NetworkAddressSet{
		NetworkAddressSetPost: api.NetworkAddressSetPost{
			Name: n.Name,
		},
		NetworkAddressSetPut: api.NetworkAddressSetPut{
			Addresses:   n.Addresses,
			Description: n.Description,
			Config:      config,
		},
	}

	return &resp, nil
}
