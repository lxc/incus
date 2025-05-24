//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"

	"github.com/lxc/incus/v6/shared/api"
)

// Code generation directives.
//
//generate-database:mapper target network_peers.mapper.go
//generate-database:mapper reset -i -b "//go:build linux && cgo && !agent"
//
//generate-database:mapper stmt -e network_peer objects
//generate-database:mapper stmt -e network_peer names
//generate-database:mapper stmt -e network_peer objects-by-Name
//generate-database:mapper stmt -e network_peer objects-by-ID
//generate-database:mapper stmt -e network_peer objects-by-NetworkID
//generate-database:mapper stmt -e network_peer objects-by-NetworkID-and-Name
//generate-database:mapper stmt -e network_peer objects-by-NetworkID-and-ID
//generate-database:mapper stmt -e network_peer names-by-NetworkID
//generate-database:mapper stmt -e network_peer create struct=NetworkPeer
//generate-database:mapper stmt -e network_peer id
//generate-database:mapper stmt -e network_peer update struct=NetworkPeer
//generate-database:mapper stmt -e network_peer delete-by-NetworkID-and-ID
//
//generate-database:mapper method -i -e network_peer GetMany references=Config
//generate-database:mapper method -i -e network_peer GetOne struct=NetworkPeer
//generate-database:mapper method -i -e network_peer GetNames-by-NetworkID
//generate-database:mapper method -i -e network_peer Exists struct=NetworkPeer
//generate-database:mapper method -i -e network_peer Create references=Config
//generate-database:mapper method -i -e network_peer ID struct=NetworkPeer
//generate-database:mapper method -i -e network_peer DeleteOne-by-NetworkID-and-ID
//generate-database:mapper method -i -e network_peer Update struct=NetworkPeer references=Config

const (
	networkPeerTypeLocal = iota
	networkPeerTypeRemote
)

var networkPeerTypeNames = map[int]string{
	networkPeerTypeLocal:  "local",
	networkPeerTypeRemote: "remote",
}

// NetworkPeer is a value object holding db-related details about a network peer.
// Fields correspond to the columns in the networks_peers table.
// generate-database will create CRUD methods and config helpers automatically.
type NetworkPeer struct {
	ID                         int64          `db:"id"`
	NetworkID                  int64          `db:"network_id"`
	Name                       string         `db:"name"`
	Description                string         `db:"description"`
	Type                       int            `db:"type"`
	TargetNetworkProject       sql.NullString `db:"target_network_project"`
	TargetNetworkName          sql.NullString `db:"target_network_name"`
	TargetNetworkIntegrationID sql.NullInt64  `db:"target_network_integration_id"`
	TargetNetworkID            sql.NullInt64  `db:"target_network_id"`
}

// NetworkPeerFilter specifies potential query parameter fields.
type NetworkPeerFilter struct {
	ID        *int64
	Name      *string
	NetworkID *int64
}

// ToAPI converts the database NetworkPeer to API type.
func (n *NetworkPeer) ToAPI(ctx context.Context, tx *sql.Tx) (*api.NetworkPeer, error) {
	configMap, err := GetNetworkPeerConfig(ctx, tx, int(n.ID))
	if err != nil {
		return nil, err
	}

	resp := api.NetworkPeer{
		NetworkPeerPut: api.NetworkPeerPut{
			Description: n.Description,
			Config:      configMap,
		},
		Name:              n.Name,
		TargetProject:     n.TargetNetworkProject.String,
		TargetNetwork:     n.TargetNetworkName.String,
		Type:              networkPeerTypeNames[n.Type],
		Status:            "",
		UsedBy:            []string{},
		TargetIntegration: "",
	}

	return &resp, nil
}
