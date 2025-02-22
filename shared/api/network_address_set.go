package api

import "strings"

// NetworkAddressSetPost used for renaming an Address Set.
//
// swagger:model
//
// API extension: network_address_set.
type NetworkAddressSetPost struct {
	// The new name of the address set
	// Example: "bar"
	Name string `json:"name" yaml:"name"`
}

// NetworkAddressSetPut used for updating an Address Set.
//
// swagger:model
//
// API extension: network_address_set.
type NetworkAddressSetPut struct {
	// Updated addresses
	Addresses []string `json:"addresses" yaml:"addresses"`

	// Updated external_ids
	ExternalIDs map[string]string `json:"external_ids,omitempty" yaml:"external_ids,omitempty"`

	// Description of the ACL
	// Example: Web servers
	Description string `json:"description" yaml:"description"`
}

// NetworkAddressSetsPost used for creating a new Address Set.
//
// swagger:model
//
// API extension: network_address_set.
type NetworkAddressSetsPost struct {
	NetworkAddressSetPut  `yaml:",inline"`
	NetworkAddressSetPost `yaml:",inline"`
}

// NetworkAddressSet represents an address set.
// Refer to doc/network-acls.md for details.
//
// swagger:model
//
// API extension: network_address_set.
type NetworkAddressSet struct {
	NetworkAddressSetPut  `yaml:",inline"`
	NetworkAddressSetPost `yaml:",inline"`

	// List of URLs of objects using this profile
	// Read only: true
	// Example: ["/1.0/instances/c1", "/1.0/instances/v1", "/1.0/networks/mybr0"]
	UsedBy []string `json:"used_by" yaml:"used_by"` // Resources that use the AddressSet.

	// Project name
	// Example: project1
	//
	// API extension: network_address_set_all_projects
	Project string `json:"project" yaml:"project"` // Project the AddressSet belongs to.
}

// Normalise normalises fields in the NetworkAddressSet so that comparisons are consistent.
func (as *NetworkAddressSet) Normalise() {
	as.Name = strings.TrimSpace(as.Name)

	trimmedAddresses := make([]string, 0, len(as.Addresses))
	for _, addr := range as.Addresses {
		trimmedAddresses = append(trimmedAddresses, strings.TrimSpace(addr))
	}
	as.Addresses = trimmedAddresses

	if as.ExternalIDs != nil {
		normalized := make(map[string]string, len(as.ExternalIDs))
		for k, v := range as.ExternalIDs {
			normalized[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
		as.ExternalIDs = normalized
	}
}

// Writable converts a full NetworkAddressSet struct into a NetworkAddressSetPut struct (filters read-only fields).
func (as *NetworkAddressSet) Writable() NetworkAddressSetPut {
	return NetworkAddressSetPut{
		Addresses:   as.Addresses,
		Description: as.Description,
		ExternalIDs: as.ExternalIDs,
	}
}
