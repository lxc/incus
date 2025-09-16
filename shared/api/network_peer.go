package api

// NetworkPeersPost represents the fields of a new network peering
//
// swagger:model
//
// API extension: network_peer.
type NetworkPeersPost struct {
	NetworkPeerPut `yaml:",inline"`

	// Name of the peer
	// Example: project1-network1
	Name string `json:"name" yaml:"name"`

	// Name of the target project
	// Example: project1
	TargetProject string `json:"target_project,omitempty" yaml:"target_project,omitempty"`

	// Name of the target network
	// Example: network1
	TargetNetwork string `json:"target_network,omitempty" yaml:"target_network,omitempty"`

	// Type of peer
	// Example: local
	//
	// API extension: network_integrations.
	Type string `json:"type" yaml:"type"`

	// Name of the target integration
	// Example: ovn-ic1
	//
	// API extension: network_integrations.
	TargetIntegration string `json:"target_integration,omitempty" yaml:"target_integration,omitempty"`
}

// NetworkPeerPut represents the modifiable fields of a network peering
//
// swagger:model
//
// API extension: network_peer.
type NetworkPeerPut struct {
	// Description of the peer
	// Example: Peering with network1 in project1
	Description string `json:"description" yaml:"description"`

	// Peer configuration map (refer to doc/network-peers.md)
	// Example: {"user.mykey": "foo"}
	Config ConfigMap `json:"config" yaml:"config"`
}

// NetworkPeer used for displaying a network peering.
//
// swagger:model
//
// API extension: network_forward.
type NetworkPeer struct {
	NetworkPeerPut `yaml:",inline"`

	// Name of the peer
	// Read only: true
	// Example: project1-network1
	Name string `json:"name" yaml:"name"`

	// Name of the target project
	// Read only: true
	// Example: project1
	TargetProject string `json:"target_project,omitempty" yaml:"target_project,omitempty"`

	// Name of the target network
	// Read only: true
	// Example: network1
	TargetNetwork string `json:"target_network,omitempty" yaml:"target_network,omitempty"`

	// The state of the peering
	// Read only: true
	// Example: Pending
	Status string `json:"status" yaml:"status"`

	// List of URLs of objects using this network peering
	// Read only: true
	// Example: ["/1.0/network-acls/test", "/1.0/network-acls/foo"]
	UsedBy []string `json:"used_by" yaml:"used_by"`

	// Type of peer
	// Example: local
	//
	// API extension: network_integrations.
	Type string `json:"type" yaml:"type"`

	// Name of the target integration
	// Example: ovn-ic1
	//
	// API extension: network_integrations.
	TargetIntegration string `json:"target_integration,omitempty" yaml:"target_integration,omitempty"`
}

// Etag returns the values used for etag generation.
func (p *NetworkPeer) Etag() []any {
	return []any{p.Name, p.Description, p.Config}
}

// Writable converts a full NetworkPeer struct into a NetworkPeerPut struct (filters read-only fields).
func (p *NetworkPeer) Writable() NetworkPeerPut {
	return p.NetworkPeerPut
}
