//go:build linux && cgo && !agent

package db

import (
	"context"

	"github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/db/operationtype"
	"github.com/lxc/incus/v6/internal/server/db/query"
)

// GetAllNodesWithOperations returns a list of nodes that have operations in any project.
func (c *ClusterTx) GetAllNodesWithOperations(ctx context.Context) ([]string, error) {
	stmt := `
SELECT DISTINCT nodes.address
  FROM operations
  JOIN nodes ON nodes.id = operations.node_id
	`
	return query.SelectStrings(ctx, c.tx, stmt)
}

// GetNodesWithOperations returns a list of nodes that have operations.
func (c *ClusterTx) GetNodesWithOperations(ctx context.Context, project string) ([]string, error) {
	stmt := `
SELECT DISTINCT nodes.address
  FROM operations
  LEFT OUTER JOIN projects ON projects.id = operations.project_id
  JOIN nodes ON nodes.id = operations.node_id
 WHERE projects.name = ? OR operations.project_id IS NULL
`
	return query.SelectStrings(ctx, c.tx, stmt, project)
}

// GetOperationsOfType returns a list operations that belong to the specified project and have the desired type.
func (c *ClusterTx) GetOperationsOfType(ctx context.Context, projectName string, opType operationtype.Type) ([]cluster.Operation, error) {
	var ops []cluster.Operation

	stmt := `
SELECT operations.id, operations.uuid, operations.type, nodes.address
  FROM operations
  LEFT JOIN projects on projects.id = operations.project_id
  JOIN nodes on nodes.id = operations.node_id
WHERE (projects.name = ? OR operations.project_id IS NULL) and operations.type = ?
`
	rows, err := c.tx.QueryContext(ctx, stmt, projectName, opType)
	if err != nil {
		return nil, err
	}

	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var op cluster.Operation
		err := rows.Scan(&op.ID, &op.UUID, &op.Type, &op.NodeAddress)
		if err != nil {
			return nil, err
		}

		ops = append(ops, op)
	}

	if rows.Err() != nil {
		return nil, err
	}

	return ops, nil
}
