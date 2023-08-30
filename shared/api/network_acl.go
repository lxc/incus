package api

import (
	"strings"
)

// NetworkACLRule represents a single rule in an ACL ruleset.
// Refer to doc/network-acls.md for details.
//
// swagger:model
//
// API extension: network_acl.
type NetworkACLRule struct {
	// Action to perform on rule match
	// Example: allow
	Action string `json:"action" yaml:"action"`

	// Source address
	// Example: @internal
	Source string `json:"source,omitempty" yaml:"source,omitempty"`

	// Destination address
	// Example: 8.8.8.8/32,8.8.4.4/32
	Destination string `json:"destination,omitempty" yaml:"destination,omitempty"`

	// Protocol
	// Example: udp
	Protocol string `json:"protocol,omitempty" yaml:"protocol,omitempty"`

	// Source port
	// Example: 1234
	SourcePort string `json:"source_port,omitempty" yaml:"source_port,omitempty"`

	// Destination port
	// Example: 53
	DestinationPort string `json:"destination_port,omitempty" yaml:"destination_port,omitempty"`

	// Type of ICMP message (for ICMP protocol)
	// Example: 8
	ICMPType string `json:"icmp_type,omitempty" yaml:"icmp_type,omitempty"`

	// ICMP message code (for ICMP protocol)
	// Example: 0
	ICMPCode string `json:"icmp_code,omitempty" yaml:"icmp_code,omitempty"`

	// Description of the rule
	// Example: Allow DNS queries to Google DNS
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// State of the rule
	// Example: enabled
	State string `json:"state" yaml:"state"`
}

// Normalise normalises the fields in the rule so that they are comparable with ones stored.
func (r *NetworkACLRule) Normalise() {
	r.Action = strings.TrimSpace(r.Action)
	r.Protocol = strings.TrimSpace(r.Protocol)
	r.ICMPType = strings.TrimSpace(r.ICMPType)
	r.ICMPCode = strings.TrimSpace(r.ICMPCode)
	r.Description = strings.TrimSpace(r.Description)
	r.State = strings.TrimSpace(r.State)

	// Remove space from Source subject list.
	subjects := strings.Split(r.Source, ",")
	for i, s := range subjects {
		subjects[i] = strings.TrimSpace(s)
	}

	r.Source = strings.Join(subjects, ",")

	// Remove space from Destination subject list.
	subjects = strings.Split(r.Destination, ",")
	for i, s := range subjects {
		subjects[i] = strings.TrimSpace(s)
	}

	r.Destination = strings.Join(subjects, ",")

	// Remove space from SourcePort port list.
	ports := strings.Split(r.SourcePort, ",")
	for i, s := range ports {
		ports[i] = strings.TrimSpace(s)
	}

	r.SourcePort = strings.Join(ports, ",")

	// Remove space from DestinationPort port list.
	ports = strings.Split(r.DestinationPort, ",")
	for i, s := range ports {
		ports[i] = strings.TrimSpace(s)
	}

	r.DestinationPort = strings.Join(ports, ",")
}

// NetworkACLPost used for renaming an ACL.
//
// swagger:model
//
// API extension: network_acl.
type NetworkACLPost struct {
	// The new name for the ACL
	// Example: bar
	Name string `json:"name" yaml:"name"` // Name of ACL.
}

// NetworkACLPut used for updating an ACL.
//
// swagger:model
//
// API extension: network_acl.
type NetworkACLPut struct {
	// Description of the ACL
	// Example: Web servers
	Description string `json:"description" yaml:"description"`

	// List of egress rules (order independent)
	Egress []NetworkACLRule `json:"egress" yaml:"egress"`

	// List of ingress rules (order independent)
	Ingress []NetworkACLRule `json:"ingress" yaml:"ingress"`

	// ACL configuration map (refer to doc/network-acls.md)
	// Example: {"user.mykey": "foo"}
	Config map[string]string `json:"config" yaml:"config"`
}

// NetworkACL used for displaying an ACL.
//
// swagger:model
//
// API extension: network_acl.
type NetworkACL struct {
	NetworkACLPost `yaml:",inline"`
	NetworkACLPut  `yaml:",inline"`

	// List of URLs of objects using this profile
	// Read only: true
	// Example: ["/1.0/instances/c1", "/1.0/instances/v1", "/1.0/networks/mybr0"]
	UsedBy []string `json:"used_by" yaml:"used_by"` // Resources that use the ACL.
}

// Writable converts a full NetworkACL struct into a NetworkACLPut struct (filters read-only fields).
func (acl *NetworkACL) Writable() NetworkACLPut {
	return acl.NetworkACLPut
}

// NetworkACLsPost used for creating an ACL.
//
// swagger:model
//
// API extension: network_acl.
type NetworkACLsPost struct {
	NetworkACLPost `yaml:",inline"`
	NetworkACLPut  `yaml:",inline"`
}
