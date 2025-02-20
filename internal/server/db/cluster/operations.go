//go:build linux && cgo && !agent

package cluster

import (
	"github.com/lxc/incus/v6/internal/server/db/operationtype"
)

// Code generation directives.
//
//generate-database:mapper target operations.mapper.go
//generate-database:mapper reset -i -b "//go:build linux && cgo && !agent"
//
//generate-database:mapper stmt -e operation objects
//generate-database:mapper stmt -e operation objects-by-NodeID
//generate-database:mapper stmt -e operation objects-by-ID
//generate-database:mapper stmt -e operation objects-by-UUID
//generate-database:mapper stmt -e operation create-or-replace
//generate-database:mapper stmt -e operation delete-by-UUID
//generate-database:mapper stmt -e operation delete-by-NodeID
//
//generate-database:mapper method -i -e operation GetMany
//generate-database:mapper method -i -e operation CreateOrReplace
//generate-database:mapper method -i -e operation DeleteOne-by-UUID
//generate-database:mapper method -i -e operation DeleteMany-by-NodeID

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
