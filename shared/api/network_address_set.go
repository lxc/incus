package api

import (
	"strings"
)

// NetworkAddressSetPost used for renaming an address set.
//
// swagger:model
//
// API extension: network_address_set.
type NetworkAddressSetPost struct {
	// The new name of the address set
	// Example: "bar"
	Name string `json:"name" yaml:"name"`
}

// NetworkAddressSetPut used for updating an address set.
//
// swagger:model
//
// API extension: network_address_set.
type NetworkAddressSetPut struct {
	// List of addresses in the set
	// Example: ["192.0.0.1", "2001:0db8:1234::1"]
	Addresses []string `json:"addresses" yaml:"addresses"`

	// Address set configuration map (refer to doc/network-address-sets.md)
	// Example: {"user.mykey": "foo"}
	Config ConfigMap `json:"config,omitempty" yaml:"config,omitempty"`

	// Description of the address set
	// Example: Web servers
	Description string `json:"description" yaml:"description"`
}

// NetworkAddressSetsPost used for creating a new address set.
//
// swagger:model
//
// API extension: network_address_set.
type NetworkAddressSetsPost struct {
	NetworkAddressSetPut  `yaml:",inline"`
	NetworkAddressSetPost `yaml:",inline"`
}

// NetworkAddressSet represents an address set.
// Refer to doc/howto/network_address_sets.md for details.
//
// swagger:model
//
// API extension: network_address_set.
type NetworkAddressSet struct {
	NetworkAddressSetPut  `yaml:",inline"`
	NetworkAddressSetPost `yaml:",inline"`

	// List of URLs of objects using this profile
	// Read only: true
	// Example: ["/1.0/network-acls/foo", "/1.0/network-acls/bar", "/1.0/network-acls/baz"]
	UsedBy []string `json:"used_by" yaml:"used_by"`

	// Project name
	// Example: project1
	Project string `json:"project" yaml:"project"`
}

// Normalise normalises fields in the NetworkAddressSet so that comparisons are consistent.
func (as *NetworkAddressSet) Normalise() {
	as.Name = strings.TrimSpace(as.Name)

	trimmedAddresses := make([]string, 0, len(as.Addresses))
	for _, addr := range as.Addresses {
		trimmedAddresses = append(trimmedAddresses, strings.TrimSpace(addr))
	}

	as.Addresses = trimmedAddresses
	if as.Config != nil {
		normalized := make(map[string]string, len(as.Config))
		for k, v := range as.Config {
			normalized[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}

		as.Config = normalized
	}
}

// Writable converts a full NetworkAddressSet struct into a NetworkAddressSetPut struct (filters read-only fields).
func (as *NetworkAddressSet) Writable() NetworkAddressSetPut {
	return NetworkAddressSetPut{
		Addresses:   as.Addresses,
		Description: as.Description,
		Config:      as.Config,
	}
}
