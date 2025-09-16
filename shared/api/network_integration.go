package api

// NetworkIntegrationsPost represents the fields of a new network integration
//
// swagger:model
//
// API extension: network_integrations.
type NetworkIntegrationsPost struct {
	NetworkIntegrationPut `yaml:",inline"`

	// The name of the integration
	// Example: region1
	Name string `json:"name" yaml:"name"`

	// The type of integration
	// Example: ovn
	Type string `json:"type" yaml:"type"`
}

// NetworkIntegrationPut represents the modifiable fields of a network integration
//
// swagger:model
//
// API extension: network_integrations.
type NetworkIntegrationPut struct {
	// Description of the network integration
	// Example: OVN interconnection for region1
	Description string `json:"description" yaml:"description"`

	// Integration configuration map (refer to doc/network-integrations.md)
	// Example: {"user.mykey": "foo"}
	Config ConfigMap `json:"config" yaml:"config"`
}

// NetworkIntegration represents a network integration.
//
// swagger:model
//
// API extension: network_integrations.
type NetworkIntegration struct {
	NetworkIntegrationPut `yaml:",inline"`

	// The name of the integration
	// Example: region1
	Name string `json:"name" yaml:"name"`

	// The type of integration
	// Example: ovn
	Type string `json:"type" yaml:"type"`

	// List of URLs of objects using this network integration
	// Read only: true
	// Example: ["/1.0/networks/foo", "/1.0/networks/bar"]
	UsedBy []string `json:"used_by" yaml:"used_by"` // Resources that use the integration.
}

// Writable converts a full NetworkIntegration struct into a NetworkIntegrationPut struct (filters read-only fields).
func (f *NetworkIntegration) Writable() NetworkIntegrationPut {
	return f.NetworkIntegrationPut
}

// NetworkIntegrationPost represents the fields required to rename a network integration
//
// swagger:model
//
// API extension: network_integrations.
type NetworkIntegrationPost struct {
	// The new name for the network integration
	// Example: region2
	Name string `json:"name" yaml:"name"`
}
