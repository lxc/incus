//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"

	"github.com/lxc/incus/v6/shared/api"
)

// Code generation directives.
//
//go:generate -command mapper incus-generate db mapper -t networks_integrations.mapper.go
//go:generate mapper reset -i -b "//go:build linux && cgo && !agent"
//
//go:generate mapper stmt -e network_integration objects
//go:generate mapper stmt -e network_integration objects-by-Name
//go:generate mapper stmt -e network_integration objects-by-ID
//go:generate mapper stmt -e network_integration create struct=NetworkIntegration
//go:generate mapper stmt -e network_integration id
//go:generate mapper stmt -e network_integration rename
//go:generate mapper stmt -e network_integration update struct=NetworkIntegration
//go:generate mapper stmt -e network_integration delete-by-Name
//
//go:generate mapper method -i -e network_integration GetMany references=Config
//go:generate mapper method -i -e network_integration GetOne struct=NetworkIntegration
//go:generate mapper method -i -e network_integration Exists struct=NetworkIntegration
//go:generate mapper method -i -e network_integration Create references=Config
//go:generate mapper method -i -e network_integration ID struct=NetworkIntegration
//go:generate mapper method -i -e network_integration Rename
//go:generate mapper method -i -e network_integration DeleteOne-by-Name
//go:generate mapper method -i -e network_integration Update struct=NetworkIntegration references=Config

const (
	// NetworkIntegrationTypeOVN represents an OVN network integration.
	NetworkIntegrationTypeOVN = iota
)

// NetworkIntegrationTypeNames is a map between DB type to their string representation.
var NetworkIntegrationTypeNames = map[int]string{
	NetworkIntegrationTypeOVN: "ovn",
}

// NetworkIntegration is a value object holding db-related details about a network integration.
type NetworkIntegration struct {
	ID          int
	Name        string
	Description string
	Type        int
}

// ToAPI converts the DB records to an API record.
func (n *NetworkIntegration) ToAPI(ctx context.Context, tx *sql.Tx) (*api.NetworkIntegration, error) {
	// Get the config.
	config, err := GetNetworkIntegrationConfig(ctx, tx, n.ID)
	if err != nil {
		return nil, err
	}

	// Fill in the struct.
	resp := api.NetworkIntegration{
		Name: n.Name,
		Type: NetworkIntegrationTypeNames[n.Type],
		NetworkIntegrationPut: api.NetworkIntegrationPut{
			Description: n.Description,
			Config:      config,
		},
	}

	return &resp, nil
}

// NetworkIntegrationFilter specifies potential query parameter fields.
type NetworkIntegrationFilter struct {
	ID   *int
	Name *string
}
