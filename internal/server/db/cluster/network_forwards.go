//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"

	"github.com/lxc/incus/v6/shared/api"
)

// Code generation directives.
//
//generate-database:mapper target network_forwards.mapper.go
//generate-database:mapper reset -i -b "//go:build linux && cgo && !agent"
//
//generate-database:mapper stmt -e network_forward objects table=networks_forwards
//generate-database:mapper stmt -e network_forward objects-by-NetworkID table=networks_forwards
//generate-database:mapper stmt -e network_forward objects-by-NetworkID-and-ListenAddress table=networks_forwards
//generate-database:mapper stmt -e network_forward create table=networks_forwards
//generate-database:mapper stmt -e network_forward delete-by-NetworkID-and-ID table=networks_forwards
//generate-database:mapper stmt -e network_forward id table=networks_forwards
//generate-database:mapper stmt -e network_forward update table=networks_forwards
//
//generate-database:mapper method -i -e network_forward GetMany references=Config table=networks_forwards
//generate-database:mapper method -i -e network_forward GetOne table=networks_forwards
//generate-database:mapper method -i -e network_forward ID table=networks_forwards
//generate-database:mapper method -i -e network_forward Exists table=networks_forwards
//generate-database:mapper method -i -e network_forward Create references=Config table=networks_forwards
//generate-database:mapper method -i -e network_forward Update references=Config table=networks_forwards
//generate-database:mapper method -i -e network_forward DeleteOne-by-NetworkID-and-ID table=networks_forwards

// NetworkForward is a value object holding db-related details about a network forward.
type NetworkForward struct {
	ID            int
	NetworkID     int           `db:"primary=yes&column=network_id"`
	NodeID        sql.NullInt64 `db:"column=node_id&nullable=true"`
	Location      *string       `db:"leftjoin=nodes.name&omit=create,update"`
	ListenAddress string        `db:"primary=yes"`
	Description   string
	Ports         []api.NetworkForwardPort `db:"marshal=json"`
}

// NetworkForwardFilter specifies potential query parameter fields.
type NetworkForwardFilter struct {
	ID            *int
	NetworkID     *int
	NodeID        *int
	ListenAddress *string
}

// ToAPI converts the DB records to an API record.
func (n *NetworkForward) ToAPI(ctx context.Context, tx *sql.Tx) (*api.NetworkForward, error) {
	// Get the config.
	config, err := GetNetworkForwardConfig(ctx, tx, n.ID)
	if err != nil {
		return nil, err
	}

	// Fill in the struct.
	resp := api.NetworkForward{
		NetworkForwardPut: api.NetworkForwardPut{
			Description: n.Description,
			Config:      config,
			Ports:       n.Ports,
		},
		ListenAddress: n.ListenAddress,
		Location:      *n.Location,
	}

	return &resp, nil
}

// UpdateNetworkForwardAPI updates the description and ports of the network forward.
func UpdateNetworkForwardAPI(ctx context.Context, db tx, curForwardID int64, curNetworkID int, curNodeID sql.NullInt64, curListenAddress string, newForward *api.NetworkForwardPut) error {
	newRecord := NetworkForward{
		NetworkID:     curNetworkID,
		NodeID:        curNodeID,
		ListenAddress: curListenAddress,
		Description:   newForward.Description,
		Ports:         newForward.Ports,
	}

	if newForward.Ports == nil {
		newRecord.Ports = []api.NetworkForwardPort{}
	}

	// Update the network forward
	err := UpdateNetworkForward(ctx, db, curNetworkID, curListenAddress, newRecord)
	if err != nil {
		return err
	}

	// Update the network forward config
	return UpdateNetworkForwardConfig(ctx, db, curForwardID, newForward.Config)
}
