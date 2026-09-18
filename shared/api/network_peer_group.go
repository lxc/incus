package api

// NetworkPeerGroupsPost represents the fields of a new network peer group
//
// swagger:model
//
// API extension: network_peer_groups.
type NetworkPeerGroupsPost struct {
	NetworkPeerGroupPut `yaml:",inline"`

	// The name of the network peer group
	// Example: region1
	Name string `json:"name" yaml:"name"`
}

// NetworkPeerGroupPut represents the modifiable fields of a network peer group
//
// swagger:model
//
// API extension: network_peer_groups.
type NetworkPeerGroupPut struct {
	// Description of the network peer group
	// Example: Peer group for region1
	Description string `json:"description" yaml:"description"`

	// List of OVN networks that are members of this network peer group
	// Example: [{"name": "ovn1", "project": "default"}]
	Networks []NetworkPeerGroupNetwork `json:"networks" yaml:"networks"`
}

// NetworkPeerGroupNetwork represents a network that is a member of a network peer group.
//
// swagger:model
//
// API extension: network_peer_groups.
type NetworkPeerGroupNetwork struct {
	// The name of the network
	// Example: ovn1
	Name string `json:"name" yaml:"name"`

	// The project the network belongs to
	// Example: default
	Project string `json:"project" yaml:"project"`
}

// NetworkPeerGroup represents a network peer group.
//
// swagger:model
//
// API extension: network_peer_groups.
type NetworkPeerGroup struct {
	NetworkPeerGroupPut `yaml:",inline"`

	// The name of the network peer group
	// Example: region1
	Name string `json:"name" yaml:"name"`
}

// Writable converts a full NetworkPeerGroup struct into a NetworkPeerGroupPut struct (filters read-only fields).
func (p *NetworkPeerGroup) Writable() NetworkPeerGroupPut {
	return p.NetworkPeerGroupPut
}
