//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lxc/incus/v6/shared/api"
)

// Code generation directives.
//
//generate-database:mapper target networks_peers.mapper.go
//generate-database:mapper reset -i -b "//go:build linux && cgo && !agent"
//
//generate-database:mapper stmt -e network_peer objects
//generate-database:mapper stmt -e network_peer objects-by-Name
//generate-database:mapper stmt -e network_peer objects-by-ID
//generate-database:mapper stmt -e network_peer objects-by-NetworkID
//generate-database:mapper stmt -e network_peer objects-by-TargetNetworkID
//generate-database:mapper stmt -e network_peer objects-by-NetworkID-and-Name
//generate-database:mapper stmt -e network_peer objects-by-NetworkID-and-ID
//generate-database:mapper stmt -e network_peer objects-by-NetworkID-and-TargetNetworkProject-and-TargetNetworkName
//generate-database:mapper stmt -e network_peer objects-by-Type-and-TargetNetworkProject-and-TargetNetworkName
//generate-database:mapper stmt -e network_peer create struct=NetworkPeer
//generate-database:mapper stmt -e network_peer id
//generate-database:mapper stmt -e network_peer update struct=NetworkPeer
//generate-database:mapper stmt -e network_peer delete-by-NetworkID-and-ID
//
//generate-database:mapper method -i -e network_peer GetMany references=Config
//generate-database:mapper method -i -e network_peer GetOne struct=NetworkPeer
//generate-database:mapper method -i -e network_peer Exists struct=NetworkPeer
//generate-database:mapper method -i -e network_peer Create references=Config
//generate-database:mapper method -i -e network_peer ID struct=NetworkPeer
//generate-database:mapper method -i -e network_peer DeleteOne-by-NetworkID-and-ID
//generate-database:mapper method -i -e network_peer Update struct=NetworkPeer references=Config

const (
	// NetworkPeerTypeLocal represents a local peer connection.
	NetworkPeerTypeLocal = iota

	// NetworkPeerTypeRemote represents a remote peer connection.
	NetworkPeerTypeRemote
)

// NetworkPeerTypeNames maps peer types (integers) to their API representation (string).
var NetworkPeerTypeNames = map[int]string{
	NetworkPeerTypeLocal:  "local",
	NetworkPeerTypeRemote: "remote",
}

// NetworkPeerTypes maps peer strings to their internal representation (integers).
var NetworkPeerTypes = map[string]int{
	NetworkPeerTypeNames[NetworkPeerTypeLocal]:  NetworkPeerTypeLocal,
	NetworkPeerTypeNames[NetworkPeerTypeRemote]: NetworkPeerTypeRemote,
}

// NetworkPeer is a value object holding db-related details about a network peer.
// Fields correspond to the columns in the networks_peers table.
// generate-database will create CRUD methods and config helpers automatically.
type NetworkPeer struct {
	ID                         int64
	NetworkID                  int64  `db:"primary=yes&column=network_id"`
	Name                       string `db:"primary=yes"`
	Description                string
	Type                       int
	TargetNetworkProject       sql.NullString
	TargetNetworkName          sql.NullString
	TargetNetworkIntegrationID sql.NullInt64
	TargetNetworkID            sql.NullInt64
}

// NetworkPeerFilter specifies potential query parameter fields.
type NetworkPeerFilter struct {
	ID        *int64
	NetworkID *int64
	Name      *string
	Type      *int

	TargetNetworkProject       *string
	TargetNetworkName          *string
	TargetNetworkIntegrationID *int64
	TargetNetworkID            *int64
}

// NetworkPeerConnection represents a peer connection.
type NetworkPeerConnection struct {
	NetworkName string
	PeerName    string
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
		Name:          n.Name,
		TargetProject: n.TargetNetworkProject.String,
		TargetNetwork: n.TargetNetworkName.String,
		Type:          NetworkPeerTypeNames[n.Type],
		UsedBy:        []string{},
	}

	if n.TargetNetworkID.Valid {
		// This is a workaround until networks themselves are ported over to the generator.
		dest := func(scan func(dest ...any) error) error {
			err := scan(&resp.TargetNetwork, &resp.TargetProject)
			if err != nil {
				return err
			}

			return nil
		}

		err := scan(ctx, tx, "SELECT networks.name, projects.name FROM networks JOIN projects ON networks.project_id=projects.id WHERE networks.id=?", dest, n.TargetNetworkID)
		if err != nil {
			return nil, fmt.Errorf("Failed to fetch from \"networks\" table: %w", err)
		}
	}

	// Get the target integration name if needed.
	if n.Type == NetworkPeerTypeRemote {
		idInt := int(n.TargetNetworkIntegrationID.Int64)
		integrations, err := GetNetworkIntegrations(ctx, tx, NetworkIntegrationFilter{ID: &idInt})
		if err != nil {
			return nil, err
		}

		if len(integrations) != 1 {
			return nil, errors.New("Couldn't find network integration")
		}

		resp.TargetIntegration = integrations[0].Name
		resp.Status = api.NetworkStatusCreated
	} else {
		// Peer has mutual peering from target network.
		if n.TargetNetworkName.String != "" && n.TargetNetworkProject.String != "" {
			if n.TargetNetworkID.Valid {
				// Peer is in a conflicting state with both the peer network ID and net/project names set.
				// Peer net/project names should only be populated before the peer is linked with a peer network ID.
				resp.Status = api.NetworkStatusErrored
			} else {
				// Peer isn't linked to a mutual peer on the target network yet but has joining details.
				resp.Status = api.NetworkStatusPending
			}
		} else {
			if n.TargetNetworkID.Valid {
				// Peer is linked to an mutual peer on the target network.
				resp.Status = api.NetworkStatusCreated
			} else {
				// Peer isn't linked to a mutual peer on the target network yet and has no joining details.
				// Perhaps it was formerly joined (and had its joining details cleared) and subsequently
				// the target peer removed its peering entry.
				resp.Status = api.NetworkStatusErrored
			}
		}
	}

	return &resp, nil
}
