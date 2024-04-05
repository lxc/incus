//go:build linux && cgo && !agent

package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/db/operationtype"
)

// Add, get and remove an operation.
func TestOperation(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	projectID, err := cluster.GetProjectID(context.Background(), tx.Tx(), "default")
	require.NoError(t, err)
	nodeID := tx.GetNodeID()
	uuid := "abcd"

	opInfo := cluster.Operation{
		NodeID:    nodeID,
		Type:      operationtype.InstanceCreate,
		UUID:      uuid,
		ProjectID: &projectID,
	}

	id, err := cluster.CreateOrReplaceOperation(context.TODO(), tx.Tx(), opInfo)
	require.NoError(t, err)
	assert.Equal(t, int64(1), id)

	filter := cluster.OperationFilter{NodeID: &nodeID}
	operations, err := cluster.GetOperations(context.TODO(), tx.Tx(), filter)
	require.NoError(t, err)
	assert.Len(t, operations, 1)
	assert.Equal(t, operations[0].UUID, "abcd")

	filter = cluster.OperationFilter{UUID: &uuid}
	ops, err := cluster.GetOperations(context.TODO(), tx.Tx(), filter)
	require.NoError(t, err)
	assert.Equal(t, len(ops), 1)
	operation := ops[0]
	assert.Equal(t, id, operation.ID)
	assert.Equal(t, operationtype.InstanceCreate, operation.Type)

	filter = cluster.OperationFilter{NodeID: &nodeID}
	ops, err = cluster.GetOperations(context.TODO(), tx.Tx(), filter)
	require.NoError(t, err)
	assert.Equal(t, "abcd", ops[0].UUID)

	err = cluster.DeleteOperation(context.TODO(), tx.Tx(), "abcd")
	require.NoError(t, err)

	filter = cluster.OperationFilter{UUID: &uuid}
	ops, err = cluster.GetOperations(context.TODO(), tx.Tx(), filter)
	require.NoError(t, err)
	assert.Equal(t, len(ops), 0)
}

// Add, get and remove an operation not associated with any project.
func TestOperationNoProject(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()

	nodeID := tx.GetNodeID()
	uuid := "abcd"

	opInfo := cluster.Operation{
		NodeID: nodeID,
		Type:   operationtype.InstanceCreate,
		UUID:   uuid,
	}

	id, err := cluster.CreateOrReplaceOperation(context.TODO(), tx.Tx(), opInfo)
	require.NoError(t, err)
	assert.Equal(t, int64(1), id)

	filter := cluster.OperationFilter{NodeID: &nodeID}
	operations, err := cluster.GetOperations(context.TODO(), tx.Tx(), filter)
	require.NoError(t, err)
	assert.Len(t, operations, 1)
	assert.Equal(t, operations[0].UUID, "abcd")

	filter = cluster.OperationFilter{UUID: &uuid}
	ops, err := cluster.GetOperations(context.TODO(), tx.Tx(), filter)
	require.NoError(t, err)
	assert.Equal(t, len(ops), 1)
	operation := ops[0]
	require.NoError(t, err)
	assert.Equal(t, id, operation.ID)
	assert.Equal(t, operationtype.InstanceCreate, operation.Type)

	filter = cluster.OperationFilter{NodeID: &nodeID}
	ops, err = cluster.GetOperations(context.TODO(), tx.Tx(), filter)
	require.NoError(t, err)
	assert.Equal(t, "abcd", ops[0].UUID)

	err = cluster.DeleteOperation(context.TODO(), tx.Tx(), "abcd")
	require.NoError(t, err)

	filter = cluster.OperationFilter{UUID: &uuid}
	ops, err = cluster.GetOperations(context.TODO(), tx.Tx(), filter)
	require.NoError(t, err)
	assert.Equal(t, len(ops), 0)
}
