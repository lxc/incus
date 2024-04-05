//go:build linux && cgo && !agent

package db

import (
	"context"
	"fmt"

	"github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/db/query"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// ClusterGroupToAPI is a convenience to convert a ClusterGroup db struct into
// an API cluster group struct.
func ClusterGroupToAPI(clusterGroup *cluster.ClusterGroup, nodes []string) *api.ClusterGroup {
	c := &api.ClusterGroup{
		ClusterGroupPut: api.ClusterGroupPut{
			Description: clusterGroup.Description,
			Members:     nodes,
		},
		ClusterGroupPost: api.ClusterGroupPost{
			Name: clusterGroup.Name,
		},
	}

	return c
}

// GetClusterGroupNodes returns a list of nodes of the given cluster group.
func (c *ClusterTx) GetClusterGroupNodes(ctx context.Context, groupName string) ([]string, error) {
	q := `SELECT nodes.name FROM nodes_cluster_groups
JOIN nodes ON nodes.id = nodes_cluster_groups.node_id
JOIN cluster_groups ON cluster_groups.id = nodes_cluster_groups.group_id
WHERE cluster_groups.name = ?`

	return query.SelectStrings(ctx, c.tx, q, groupName)
}

// GetClusterGroupURIs returns all available ClusterGroup URIs.
// generator: ClusterGroup URIs
func (c *ClusterTx) GetClusterGroupURIs(ctx context.Context, filter cluster.ClusterGroupFilter) ([]string, error) {
	var args []any
	var sql string
	if filter.Name != nil && filter.ID == nil {
		sql = `SELECT cluster_groups.name FROM cluster_groups
WHERE cluster_groups.name = ? ORDER BY cluster_groups.name
`
		args = []any{
			filter.Name,
		}
	} else if filter.ID == nil && filter.Name == nil {
		sql = `SELECT cluster_groups.name FROM cluster_groups ORDER BY cluster_groups.name`
		args = []any{}
	} else {
		return nil, fmt.Errorf("No statement exists for the given Filter")
	}

	names, err := query.SelectStrings(ctx, c.tx, sql, args...)
	if err != nil {
		return nil, err
	}

	uris := make([]string, len(names))
	for i, name := range names {
		uris[i] = api.NewURL().Path(version.APIVersion, "cluster", "groups", name).String()
	}

	return uris, nil
}

// AddNodeToClusterGroup adds a given node to the given cluster group.
func (c *ClusterTx) AddNodeToClusterGroup(ctx context.Context, groupName string, nodeName string) error {
	groupID, err := cluster.GetClusterGroupID(ctx, c.tx, groupName)
	if err != nil {
		return fmt.Errorf("Failed to get cluster group ID: %w", err)
	}

	nodeInfo, err := c.GetNodeByName(ctx, nodeName)
	if err != nil {
		return fmt.Errorf("Failed to get node info: %w", err)
	}

	_, err = c.tx.Exec(`INSERT INTO nodes_cluster_groups (node_id, group_id) VALUES(?, ?)`, nodeInfo.ID, groupID)
	if err != nil {
		return err
	}

	return nil
}

// RemoveNodeFromClusterGroup removes a given node from the given group name.
func (c *ClusterTx) RemoveNodeFromClusterGroup(ctx context.Context, groupName string, nodeName string) error {
	groupID, err := cluster.GetClusterGroupID(ctx, c.tx, groupName)
	if err != nil {
		return fmt.Errorf("Failed to get cluster group ID: %w", err)
	}

	nodeInfo, err := c.GetNodeByName(ctx, nodeName)
	if err != nil {
		return fmt.Errorf("Failed to get node info: %w", err)
	}

	_, err = c.tx.Exec(`DELETE FROM nodes_cluster_groups WHERE node_id = ? AND group_id = ?`, nodeInfo.ID, groupID)
	if err != nil {
		return err
	}

	return nil
}

// GetClusterGroupsWithNode returns a list of cluster group names the given node belongs to.
func (c *ClusterTx) GetClusterGroupsWithNode(ctx context.Context, nodeName string) ([]string, error) {
	q := `SELECT cluster_groups.name FROM nodes_cluster_groups
JOIN cluster_groups ON cluster_groups.id = nodes_cluster_groups.group_id
JOIN nodes ON nodes.id = nodes_cluster_groups.node_id
WHERE nodes.name = ?`

	return query.SelectStrings(ctx, c.tx, q, nodeName)
}
