package api

import (
	"net"
	"strings"
)

// NetworkForwardPort represents a port specification in a network address forward
//
// swagger:model
//
// API extension: network_forward.
type NetworkForwardPort struct {
	// Description of the forward port
	// Example: My web server forward
	Description string `json:"description" yaml:"description"`

	// Protocol for port forward (either tcp or udp)
	// Example: tcp
	Protocol string `json:"protocol" yaml:"protocol"`

	// ListenPort(s) to forward (comma delimited ranges)
	// Example: 80,81,8080-8090
	ListenPort string `json:"listen_port" yaml:"listen_port"`

	// TargetPort(s) to forward ListenPorts to (allows for many-to-one)
	// Example: 80,81,8080-8090
	TargetPort string `json:"target_port" yaml:"target_port"`

	// TargetAddress to forward ListenPorts to
	// Example: 198.51.100.2
	TargetAddress string `json:"target_address" yaml:"target_address"`
}

// Normalise normalises the fields in the rule so that they are comparable with ones stored.
func (p *NetworkForwardPort) Normalise() {
	p.Description = strings.TrimSpace(p.Description)
	p.Protocol = strings.TrimSpace(p.Protocol)
	p.TargetAddress = strings.TrimSpace(p.TargetAddress)

	ip := net.ParseIP(p.TargetAddress)
	if ip != nil {
		p.TargetAddress = ip.String() // Replace with canonical form if specified.
	}

	// Remove space from ListenPort list.
	subjects := strings.Split(p.ListenPort, ",")
	for i, s := range subjects {
		subjects[i] = strings.TrimSpace(s)
	}

	p.ListenPort = strings.Join(subjects, ",")

	// Remove space from TargetPort list.
	subjects = strings.Split(p.TargetPort, ",")
	for i, s := range subjects {
		subjects[i] = strings.TrimSpace(s)
	}

	p.TargetPort = strings.Join(subjects, ",")
}

// NetworkForwardsPost represents the fields of a new network address forward
//
// swagger:model
//
// API extension: network_forward.
type NetworkForwardsPost struct {
	NetworkForwardPut `yaml:",inline"`

	// The listen address of the forward
	// Example: 192.0.2.1
	ListenAddress string `json:"listen_address" yaml:"listen_address"`
}

// Normalise normalises the fields in the rule so that they are comparable with ones stored.
func (f *NetworkForwardsPost) Normalise() {
	ip := net.ParseIP(f.ListenAddress)
	if ip != nil {
		f.ListenAddress = ip.String() // Replace with canonical form if specified.
	}

	f.NetworkForwardPut.Normalise()
}

// NetworkForwardPut represents the modifiable fields of a network address forward
//
// swagger:model
//
// API extension: network_forward.
type NetworkForwardPut struct {
	// Description of the forward listen IP
	// Example: My public IP forward
	Description string `json:"description" yaml:"description"`

	// Forward configuration map (refer to doc/network-forwards.md)
	// Example: {"user.mykey": "foo"}
	Config map[string]string `json:"config" yaml:"config"`

	// Port forwards (optional)
	Ports []NetworkForwardPort `json:"ports" yaml:"ports"`
}

// Normalise normalises the fields in the rule so that they are comparable with ones stored.
func (f *NetworkForwardPut) Normalise() {
	f.Description = strings.TrimSpace(f.Description)

	ip := net.ParseIP(f.Config["target_address"])
	if ip != nil {
		f.Config["target_address"] = ip.String() // Replace with canonical form if specified.
	}

	for i := range f.Ports {
		f.Ports[i].Normalise()
	}
}

// NetworkForward used for displaying an network address forward.
//
// swagger:model
//
// API extension: network_forward.
type NetworkForward struct {
	NetworkForwardPut `yaml:",inline"`

	// The listen address of the forward
	// Example: 192.0.2.1
	ListenAddress string `json:"listen_address" yaml:"listen_address"`

	// What cluster member this record was found on
	// Example: server01
	Location string `json:"location" yaml:"location"`
}

// Etag returns the values used for etag generation.
func (f *NetworkForward) Etag() []any {
	return []any{f.ListenAddress, f.Description, f.Config, f.Ports}
}

// Writable converts a full NetworkForward struct into a NetworkForwardPut struct (filters read-only fields).
func (f *NetworkForward) Writable() NetworkForwardPut {
	return f.NetworkForwardPut
}
