//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"

	"github.com/lxc/incus/v6/shared/api"
)

// Code generation directives.
//
//generate-database:mapper target networks_integrations.mapper.go
//generate-database:mapper reset -i -b "//go:build linux && cgo && !agent"
//
//generate-database:mapper stmt -e network_integration objects
//generate-database:mapper stmt -e network_integration objects-by-Name
//generate-database:mapper stmt -e network_integration objects-by-ID
//generate-database:mapper stmt -e network_integration create struct=NetworkIntegration
//generate-database:mapper stmt -e network_integration id
//generate-database:mapper stmt -e network_integration rename
//generate-database:mapper stmt -e network_integration update struct=NetworkIntegration
//generate-database:mapper stmt -e network_integration delete-by-Name
//
//generate-database:mapper method -i -e network_integration GetMany references=Config
//generate-database:mapper method -i -e network_integration GetOne struct=NetworkIntegration
//generate-database:mapper method -i -e network_integration Exists struct=NetworkIntegration
//generate-database:mapper method -i -e network_integration Create references=Config
//generate-database:mapper method -i -e network_integration ID struct=NetworkIntegration
//generate-database:mapper method -i -e network_integration Rename
//generate-database:mapper method -i -e network_integration DeleteOne-by-Name
//generate-database:mapper method -i -e network_integration Update struct=NetworkIntegration references=Config

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
