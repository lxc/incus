//go:build linux && cgo && !agent

package db

import (
	"context"
	"fmt"

	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/db/query"
)

// networkPeerGroupLinkIndexSelf is the link_index reserved for the network peer group's own router port.
const networkPeerGroupLinkIndexSelf = 1

// networkPeerGroupLinkIndexMax is the highest link_index allocatable, per the /24 addressing scheme.
const networkPeerGroupLinkIndexMax = 254

// NetworkPeerGroupNetworkMembership is a network peer group network membership, including the
// network's DB ID and its allocated link_index (used for OVN peering-link addressing).
type NetworkPeerGroupNetworkMembership struct {
	NetworkID int64
	Name      string
	Project   string
	LinkIndex int
}

// GetNetworkPeerGroupNetworkMemberships returns the network memberships (including their allocated
// link index) of the given network peer group, ordered by link index.
func (c *ClusterTx) GetNetworkPeerGroupNetworkMemberships(ctx context.Context, networkPeerGroupName string) ([]NetworkPeerGroupNetworkMembership, error) {
	q := `SELECT networks.id, networks.name, projects.name, network_peer_groups_networks.link_index FROM network_peer_groups_networks
JOIN networks ON networks.id = network_peer_groups_networks.network_id
JOIN projects ON projects.id = networks.project_id
JOIN network_peer_groups ON network_peer_groups.id = network_peer_groups_networks.network_peer_group_id
WHERE network_peer_groups.name = ?
ORDER BY network_peer_groups_networks.link_index`

	memberships := []NetworkPeerGroupNetworkMembership{}
	err := query.Scan(ctx, c.tx, q, func(scan func(dest ...any) error) error {
		m := NetworkPeerGroupNetworkMembership{}

		err := scan(&m.NetworkID, &m.Name, &m.Project, &m.LinkIndex)
		if err != nil {
			return err
		}

		memberships = append(memberships, m)

		return nil
	}, networkPeerGroupName)
	if err != nil {
		return nil, err
	}

	return memberships, nil
}

// AddNetworkToNetworkPeerGroup adds a given network to the given network peer group, allocating the
// smallest unused link_index. Returns the allocated link_index and the network's DB ID.
func (c *ClusterTx) AddNetworkToNetworkPeerGroup(ctx context.Context, networkPeerGroupName string, projectName string, networkName string) (int, int64, error) {
	networkPeerGroupID, err := cluster.GetNetworkPeerGroupID(ctx, c.tx, networkPeerGroupName)
	if err != nil {
		return -1, -1, fmt.Errorf("Failed to get network peer group ID: %w", err)
	}

	networkID, err := c.GetNetworkID(ctx, projectName, networkName)
	if err != nil {
		return -1, -1, fmt.Errorf("Failed to get network ID: %w", err)
	}

	used, err := query.SelectIntegers(ctx, c.tx, `SELECT link_index FROM network_peer_groups_networks WHERE network_peer_group_id = ?`, networkPeerGroupID)
	if err != nil {
		return -1, -1, fmt.Errorf("Failed to get existing network peer group link indexes: %w", err)
	}

	usedSet := make(map[int]bool, len(used))
	for _, idx := range used {
		usedSet[idx] = true
	}

	linkIndex := networkPeerGroupLinkIndexSelf + 1
	for usedSet[linkIndex] {
		linkIndex++
	}

	if linkIndex > networkPeerGroupLinkIndexMax {
		return -1, -1, fmt.Errorf("Network peer group has reached its maximum number of member networks (%d)", networkPeerGroupLinkIndexMax-networkPeerGroupLinkIndexSelf)
	}

	_, err = c.tx.Exec(`INSERT INTO network_peer_groups_networks (network_peer_group_id, network_id, link_index) VALUES(?, ?, ?)`, networkPeerGroupID, networkID, linkIndex)
	if err != nil {
		return -1, -1, err
	}

	return linkIndex, networkID, nil
}

// RemoveNetworkFromNetworkPeerGroup removes a given network from the given network peer group.
func (c *ClusterTx) RemoveNetworkFromNetworkPeerGroup(ctx context.Context, networkPeerGroupName string, projectName string, networkName string) error {
	networkPeerGroupID, err := cluster.GetNetworkPeerGroupID(ctx, c.tx, networkPeerGroupName)
	if err != nil {
		return fmt.Errorf("Failed to get network peer group ID: %w", err)
	}

	networkID, err := c.GetNetworkID(ctx, projectName, networkName)
	if err != nil {
		return fmt.Errorf("Failed to get network ID: %w", err)
	}

	_, err = c.tx.Exec(`DELETE FROM network_peer_groups_networks WHERE network_peer_group_id = ? AND network_id = ?`, networkPeerGroupID, networkID)
	if err != nil {
		return err
	}

	return nil
}

// UpdateNetworkPeerGroupDescription updates the description of the given network peer group.
func (c *ClusterTx) UpdateNetworkPeerGroupDescription(ctx context.Context, networkPeerGroupName string, description string) error {
	_, err := c.tx.ExecContext(ctx, `UPDATE network_peer_groups SET description = ? WHERE name = ?`, description, networkPeerGroupName)
	return err
}
