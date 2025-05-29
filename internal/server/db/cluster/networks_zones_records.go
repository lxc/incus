//go:build linux && cgo && !agent

package cluster

import (
	"context"

	"github.com/lxc/incus/v6/shared/api"
)

// Code generation directives.
//
//generate-database:mapper target networks_zones_records.mapper.go
//generate-database:mapper reset -i -b "//go:build linux && cgo && !agent"
//
// Statements:
//generate-database:mapper stmt -e NetworkZoneRecord objects table=networks_zones_records
//generate-database:mapper stmt -e NetworkZoneRecord objects-by-ID table=networks_zones_records
//generate-database:mapper stmt -e NetworkZoneRecord objects-by-Name table=networks_zones_records
//generate-database:mapper stmt -e NetworkZoneRecord objects-by-NetworkZoneID table=networks_zones_records
//generate-database:mapper stmt -e NetworkZoneRecord objects-by-NetworkZoneID-and-Name table=networks_zones_records
//generate-database:mapper stmt -e NetworkZoneRecord objects-by-NetworkZoneID-and-ID table=networks_zones_records
//generate-database:mapper stmt -e NetworkZoneRecord id table=networks_zones_records
//generate-database:mapper stmt -e NetworkZoneRecord create table=networks_zones_records
//generate-database:mapper stmt -e NetworkZoneRecord rename table=networks_zones_records
//generate-database:mapper stmt -e NetworkZoneRecord update table=networks_zones_records
//generate-database:mapper stmt -e NetworkZoneRecord delete-by-NetworkZoneID-and-ID table=networks_zones_records
//
// Methods:
//generate-database:mapper method -i -e NetworkZoneRecord GetMany references=Config table=networks_zones_records
//generate-database:mapper method -i -e NetworkZoneRecord GetOne table=networks_zones_records
//generate-database:mapper method -i -e NetworkZoneRecord Exists table=networks_zones_records
//generate-database:mapper method -i -e NetworkZoneRecord Create references=Config table=networks_zones_records
//generate-database:mapper method -i -e NetworkZoneRecord ID table=networks_zones_records
//generate-database:mapper method -i -e NetworkZoneRecord Rename table=networks_zones_records
//generate-database:mapper method -i -e NetworkZoneRecord Update references=Config table=networks_zones_records
//generate-database:mapper method -i -e NetworkZoneRecord DeleteOne-by-NetworkZoneID-and-ID table=networks_zones_records

// NetworkZoneRecord is a value object holding db-related details about a DNS record in a network zone.
type NetworkZoneRecord struct {
	ID            int    `db:"order=yes"`
	NetworkZoneID int    `db:"primary=yes"`
	Name          string `db:"primary=yes"`
	Description   string
	Entries       []api.NetworkZoneRecordEntry `db:"marshal=json"`
}

// NetworkZoneRecordFilter defines the optional WHERE-clause fields.
type NetworkZoneRecordFilter struct {
	ID            *int
	Name          *string
	NetworkZoneID *int
}

// ToAPI converts the DB record into external API type.
func (r *NetworkZoneRecord) ToAPI(ctx context.Context, db tx) (*api.NetworkZoneRecord, error) {
	config, err := GetNetworkZoneRecordConfig(ctx, db, r.ID)
	if err != nil {
		return nil, err
	}

	out := api.NetworkZoneRecord{
		Name: r.Name,
		NetworkZoneRecordPut: api.NetworkZoneRecordPut{
			Description: r.Description,
			Entries:     r.Entries,
			Config:      config,
		},
	}

	return &out, nil
}
