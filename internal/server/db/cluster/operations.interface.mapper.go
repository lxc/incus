//go:build linux && cgo && !agent

package cluster

import "context"

// OperationGenerated is an interface of generated methods for Operation.
type OperationGenerated interface {
	// GetOperations returns all available operations.
	// generator: operation GetMany
	GetOperations(ctx context.Context, db dbtx, filters ...OperationFilter) ([]Operation, error)

	// CreateOrReplaceOperation adds a new operation to the database.
	// generator: operation CreateOrReplace
	CreateOrReplaceOperation(ctx context.Context, db dbtx, object Operation) (int64, error)

	// DeleteOperation deletes the operation matching the given key parameters.
	// generator: operation DeleteOne-by-UUID
	DeleteOperation(ctx context.Context, db dbtx, uuid string) error

	// DeleteOperations deletes the operation matching the given key parameters.
	// generator: operation DeleteMany-by-NodeID
	DeleteOperations(ctx context.Context, db dbtx, nodeID int64) error
}
