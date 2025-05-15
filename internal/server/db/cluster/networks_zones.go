//go:build linux && cgo && !agent

package cluster

import (
	"context"

	"github.com/lxc/incus/v6/shared/api"
)

// Code generation directives.
//
//generate-database:mapper target networks_zones.mapper.go
//generate-database:mapper reset -i -b "//go:build linux && cgo && !agent"
//
// Statements:
//generate-database:mapper stmt -e NetworkZone objects table=networks_zones
//generate-database:mapper stmt -e NetworkZone objects-by-ID table=networks_zones
//generate-database:mapper stmt -e NetworkZone objects-by-Name table=networks_zones
//generate-database:mapper stmt -e NetworkZone objects-by-Project table=networks_zones
//generate-database:mapper stmt -e NetworkZone objects-by-Project-and-Name table=networks_zones
//generate-database:mapper stmt -e NetworkZone id table=networks_zones
//generate-database:mapper stmt -e NetworkZone create table=networks_zones
//generate-database:mapper stmt -e NetworkZone rename table=networks_zones
//generate-database:mapper stmt -e NetworkZone update table=networks_zones
//generate-database:mapper stmt -e NetworkZone delete-by-ID table=networks_zones
//
// Methods:
//generate-database:mapper method -i -e NetworkZone GetMany references=Config table=networks_zones
//generate-database:mapper method -i -e NetworkZone GetOne table=networks_zones
//generate-database:mapper method -i -e NetworkZone Exists table=networks_zones
//generate-database:mapper method -i -e NetworkZone Create references=Config table=networks_zones
//generate-database:mapper method -i -e NetworkZone ID table=networks_zones
//generate-database:mapper method -i -e NetworkZone Rename table=networks_zones
//generate-database:mapper method -i -e NetworkZone Update references=Config table=networks_zones
//generate-database:mapper method -i -e NetworkZone DeleteOne-by-ID table=networks_zones

// NetworkZone is a value object holding db-related details about a network zone (DNS).
type NetworkZone struct {
	ID          int    `db:"order=yes"`
	ProjectID   int    `db:"omit=create,update"`
	Project     string `db:"primary=yes&join=projects.name"`
	Name        string `db:"primary=yes"`
	Description string
}

// NetworkZoneFilter specifies potential query parameter fields.
type NetworkZoneFilter struct {
	ID      *int
	Name    *string
	Project *string
}

// ToAPI converts the DB records to an API record.
func (n *NetworkZone) ToAPI(ctx context.Context, db tx) (*api.NetworkZone, error) {
	// Get the config.
	config, err := GetNetworkZoneConfig(ctx, db, n.ID)
	if err != nil {
		return nil, err
	}

	// Fill in the struct.
	out := api.NetworkZone{
		Name:    n.Name,
		Project: n.Project,
		NetworkZonePut: api.NetworkZonePut{
			Description: n.Description,
			Config:      config,
		},
	}

	return &out, nil
}
