//go:build linux && cgo && !agent

package cluster

import (
	"github.com/lxc/incus/v6/internal/server/db/operationtype"
)

// Code generation directives.
//
//go:generate -command mapper generate-database db mapper -t operations.mapper.go
//go:generate mapper generate -i -b "//go:build linux && cgo && !agent"
//
//generate-database:mapper stmt operation objects
//generate-database:mapper stmt operation objects-by-NodeID
//generate-database:mapper stmt operation objects-by-ID
//generate-database:mapper stmt operation objects-by-UUID
//generate-database:mapper stmt operation create-or-replace
//generate-database:mapper stmt operation delete-by-UUID
//generate-database:mapper stmt operation delete-by-NodeID
//
//generate-database:mapper method operation GetMany
//generate-database:mapper method operation CreateOrReplace
//generate-database:mapper method operation DeleteOne-by-UUID
//generate-database:mapper method operation DeleteMany-by-NodeID

// Operation holds information about a single operation running on a member in the cluster.
type Operation struct {
	ID          int64              `db:"primary=yes"`                               // Stable database identifier
	UUID        string             `db:"primary=yes"`                               // User-visible identifier
	NodeAddress string             `db:"join=nodes.address&omit=create-or-replace"` // Address of the node the operation is running on
	ProjectID   *int64             // ID of the project for the operation.
	NodeID      int64              // ID of the node the operation is running on
	Type        operationtype.Type // Type of the operation
}

// OperationFilter specifies potential query parameter fields.
type OperationFilter struct {
	ID     *int64
	NodeID *int64
	UUID   *string
}
