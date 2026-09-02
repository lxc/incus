//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"

	"github.com/lxc/incus/v7/shared/api"
)

// Code generation directives.
//
//generate-database:mapper target network_peer_groups.mapper.go
//generate-database:mapper reset -i -b "//go:build linux && cgo && !agent"
//
//generate-database:mapper stmt -e network_peer_group objects table=network_peer_groups
//generate-database:mapper stmt -e network_peer_group objects-by-Name table=network_peer_groups
//generate-database:mapper stmt -e network_peer_group objects-by-ID table=network_peer_groups
//generate-database:mapper stmt -e network_peer_group create struct=NetworkPeerGroup table=network_peer_groups
//generate-database:mapper stmt -e network_peer_group id table=network_peer_groups
//generate-database:mapper stmt -e network_peer_group delete-by-Name table=network_peer_groups
//
//generate-database:mapper method -i -e network_peer_group GetMany table=network_peer_groups
//generate-database:mapper method -i -e network_peer_group GetOne struct=NetworkPeerGroup table=network_peer_groups
//generate-database:mapper method -i -e network_peer_group Exists struct=NetworkPeerGroup table=network_peer_groups
//generate-database:mapper method -i -e network_peer_group Create struct=NetworkPeerGroup table=network_peer_groups
//generate-database:mapper method -i -e network_peer_group ID struct=NetworkPeerGroup table=network_peer_groups
//generate-database:mapper method -i -e network_peer_group DeleteOne-by-Name table=network_peer_groups

// NetworkPeerGroup is a value object holding db-related details about a network peer group.
type NetworkPeerGroup struct {
	ID          int
	Name        string
	Description string
}

// ToAPI converts the DB record to an API record.
func (p *NetworkPeerGroup) ToAPI(ctx context.Context, tx *sql.Tx) (*api.NetworkPeerGroup, error) {
	resp := api.NetworkPeerGroup{
		Name: p.Name,
		NetworkPeerGroupPut: api.NetworkPeerGroupPut{
			Description: p.Description,
		},
	}

	return &resp, nil
}

// NetworkPeerGroupFilter specifies potential query parameter fields.
type NetworkPeerGroupFilter struct {
	ID   *int
	Name *string
}
