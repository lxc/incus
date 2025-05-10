//go:build linux && cgo && !agent

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	dbCluster "github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/shared/api"
)

const (
	networkPeerTypeLocal = iota
	networkPeerTypeRemote
)

var networkPeerTypeNames = map[int]string{
	networkPeerTypeLocal:  "local",
	networkPeerTypeRemote: "remote",
}

// CreateNetworkPeer creates a new Network Peer and returns its ID.
// If there is a mutual peering on the target network side the both peer entries are updated to link to each other's
// repspective network ID.
// Returns the local peer ID and true if a mutual peering has been created.
func (c *ClusterTx) CreateNetworkPeer(ctx context.Context, networkID int64, info *api.NetworkPeersPost) (int64, bool, error) {
	var err error
	var localPeerID int64
	var targetPeerNetworkID int64 = -1 // -1 means no mutual peering exists.

	dbPeer := dbCluster.NetworkPeer{
		NetworkID:   int64(networkID),
		Name:        info.Name,
		Description: info.Description,
	}

	switch info.Type {
	case "", networkPeerTypeNames[networkPeerTypeLocal]:
		dbPeer.Type = networkPeerTypeLocal
		dbPeer.TargetNetworkProject = sql.NullString{String: info.TargetProject, Valid: info.TargetProject != ""}
		dbPeer.TargetNetworkName = sql.NullString{String: info.TargetNetwork, Valid: info.TargetNetwork != ""}
	case networkPeerTypeNames[networkPeerTypeRemote]:
		if info.TargetIntegration == "" {
			return -1, false, fmt.Errorf("Missing network integration name")
		}

		networkIntegration, err := dbCluster.GetNetworkIntegration(ctx, c.tx, info.TargetIntegration)
		if err != nil {
			return -1, false, err
		}

		dbPeer.Type = networkPeerTypeRemote
		dbPeer.TargetNetworkIntegrationID = sql.NullInt64{Int64: int64(networkIntegration.ID), Valid: true}
	default:
		return -1, false, fmt.Errorf("Invalid network peer type %q", info.Type)
	}

	localPeerID, err = dbCluster.CreateNetworkPeer(ctx, c.tx, dbPeer)
	if err != nil {
		return -1, false, err
	}

	if dbPeer.Type == networkPeerTypeLocal {
		// Check if we are creating a mutual peering of an existing peer and if so then update both sides
		// with the respective network IDs.
		localNetworkName, localProjectName, err := c.GetNetworkNameAndProjectWithID(ctx, int(networkID))
		if err != nil {
			return -1, false, fmt.Errorf("Failed getting local network info: %w", err)
		}

		targetNetworkID, err := c.GetNetworkID(ctx, info.TargetProject, info.TargetNetwork)
		if err != nil {
			// Target network might not exist yet, which is fine.
			if errors.Is(err, sql.ErrNoRows) { // GetNetworkID returns ErrNoRows internally before wrapping
				return localPeerID, false, nil
			}

			return -1, false, fmt.Errorf("Failed getting target network ID: %w", err)
		}

		// Find potential target peer(s) pointing back at our local network.
		targetNetworkPeers, err := dbCluster.GetNetworkPeers(ctx, c.tx, dbCluster.NetworkPeerFilter{NetworkID: &targetNetworkID})
		if err != nil && !errors.Is(err, dbCluster.ErrNotFound) {
			return -1, false, fmt.Errorf("Failed looking up potential mutual peers: %w", err)
		}

		var targetPeer *dbCluster.NetworkPeer // The specific target peer we found.

		// Find the unlinked peer targeting our local network.
		for i := range targetNetworkPeers {
			p := targetNetworkPeers[i] // Use a pointer to the peer in the slice
			if !p.TargetNetworkID.Valid {
				if p.TargetNetworkProject.Valid && p.TargetNetworkProject.String == localProjectName && p.TargetNetworkName.Valid && p.TargetNetworkName.String == localNetworkName {
					if targetPeer != nil {
						// Should not happen, but guard against multiple matches.
						return -1, false, fmt.Errorf("Multiple unlinked mutual peers found")
					}

					targetPeer = &p
					targetPeerNetworkID = int64(p.NetworkID) // Capture the target peer's network ID.
				}
			}
		}

		// If a mutual peer was found, update both peer entries.
		if targetPeer != nil {
			localPeerUpdateFilter := dbCluster.NetworkPeerFilter{NetworkID: &networkID, ID: &localPeerID}
			localPeers, err := dbCluster.GetNetworkPeers(ctx, c.tx, localPeerUpdateFilter)
			if err != nil || len(localPeers) != 1 {
				return -1, false, fmt.Errorf("Failed to fetch local peer for update: %w", err)
			}

			localPeerToUpdate := localPeers[0]
			localPeerToUpdate.TargetNetworkID = sql.NullInt64{Int64: targetPeerNetworkID, Valid: true}
			localPeerToUpdate.TargetNetworkProject = sql.NullString{Valid: false}
			localPeerToUpdate.TargetNetworkName = sql.NullString{Valid: false}

			err = dbCluster.UpdateNetworkPeer(ctx, c.tx, localPeerToUpdate.Name, localPeerToUpdate)
			if err != nil {
				return -1, false, fmt.Errorf("Failed updating local peer for mutual peering: %w", err)
			}

			targetPeer.TargetNetworkID = sql.NullInt64{Int64: networkID, Valid: true}
			targetPeer.TargetNetworkProject = sql.NullString{Valid: false}
			targetPeer.TargetNetworkName = sql.NullString{Valid: false}

			err = dbCluster.UpdateNetworkPeer(ctx, c.tx, targetPeer.Name, *targetPeer)
			if err != nil {
				// Attempt to revert the update on the local peer.
				localPeerToUpdate.TargetNetworkID = sql.NullInt64{Valid: false}
				localPeerToUpdate.TargetNetworkProject = sql.NullString{String: info.TargetProject, Valid: info.TargetProject != ""}
				localPeerToUpdate.TargetNetworkName = sql.NullString{String: info.TargetNetwork, Valid: info.TargetNetwork != ""}
				_ = dbCluster.UpdateNetworkPeer(ctx, c.tx, localPeerToUpdate.Name, localPeerToUpdate)
				return -1, false, fmt.Errorf("Failed updating target peer for mutual peering: %w", err)
			}
		}
	}

	return localPeerID, targetPeerNetworkID > -1, nil
}

// NetworkPeer represents a peer connection.
type NetworkPeer struct {
	NetworkName string
	PeerName    string
}
