//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"

	"github.com/lxc/incus/v6/shared/api"
)

// Code generation directives.
//
//generate-database:mapper target networks_forwards.mapper.go
//generate-database:mapper reset -i -b "//go:build linux && cgo && !agent"
//
//generate-database:mapper stmt -e network_forward objects table=networks_forwards
//generate-database:mapper stmt -e network_forward objects-by-NetworkID table=networks_forwards
//generate-database:mapper stmt -e network_forward objects-by-NetworkID-and-ListenAddress table=networks_forwards
//generate-database:mapper stmt -e network_forward id table=networks_forwards
//generate-database:mapper stmt -e network_forward create table=networks_forwards
//generate-database:mapper stmt -e network_forward update table=networks_forwards
//generate-database:mapper stmt -e network_forward delete-by-NetworkID-and-ID table=networks_forwards
//
//generate-database:mapper method -i -e network_forward GetMany references=Config table=networks_forwards
//generate-database:mapper method -i -e network_forward GetOne table=networks_forwards
//generate-database:mapper method -i -e network_forward ID table=networks_forwards
//generate-database:mapper method -i -e network_forward Create references=Config table=networks_forwards
//generate-database:mapper method -i -e network_forward Update references=Config table=networks_forwards
//generate-database:mapper method -i -e network_forward DeleteOne-by-NetworkID-and-ID table=networks_forwards

// NetworkForward is the generated entity backing the networks_forwards table.
type NetworkForward struct {
	ID            int64
	NetworkID     int64         `db:"primary=yes&column=network_id"`
	NodeID        sql.NullInt64 `db:"column=node_id&nullable=true"`
	Location      *string       `db:"leftjoin=nodes.name&omit=create,update"`
	ListenAddress string        `db:"primary=yes"`
	Description   string
	Ports         []api.NetworkForwardPort `db:"marshal=json"`
}

// NetworkForwardFilter defines the optional WHERE-clause fields.
type NetworkForwardFilter struct {
	ID            *int64
	NetworkID     *int64
	NodeID        *int64
	ListenAddress *string
}

// ToAPI converts the DB record into the external API type.
func (n *NetworkForward) ToAPI(ctx context.Context, tx *sql.Tx) (*api.NetworkForward, error) {
	// Get the config.
	cfg, err := GetNetworkForwardConfig(ctx, tx, int(n.ID))
	if err != nil {
		return nil, err
	}

	// Fill in the struct.
	out := api.NetworkForward{
		NetworkForwardPut: api.NetworkForwardPut{
			Description: n.Description,
			Config:      cfg,
			Ports:       n.Ports,
		},

		ListenAddress: n.ListenAddress,
	}

	if n.Location != nil {
		out.Location = *n.Location
	}

	return &out, nil
}
