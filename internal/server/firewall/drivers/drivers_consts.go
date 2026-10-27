package drivers

import (
	"net"
)

// FeatureOpts specify how firewall features are setup.
type FeatureOpts struct {
	ICMPDHCPDNSAccess bool // Add rules to allow ICMP, DHCP and DNS access.
	ForwardingAllow   bool // Add rules to allow IP forwarding. Blocked if false.
}

// SNATOpts specify how SNAT rules are setup.
type SNATOpts struct {
	Append      bool       // Append rules (has no effect if driver doesn't support it).
	Subnet      *net.IPNet // Subnet of source network used to identify candidate traffic.
	SNATAddress net.IP     // SNAT IP address to use. If nil then MASQUERADE is used.
}

// Opts for setting up the firewall.
type Opts struct {
	FeaturesV4 *FeatureOpts // Enable IPv4 firewall with specified options. Off if not provided.
	FeaturesV6 *FeatureOpts // Enable IPv6 firewall with specified options. Off if not provided.
	SNATV4     *SNATOpts    // Enable IPv4 SNAT with specified options. Off if not provided.
	SNATV6     *SNATOpts    // Enable IPv6 SNAT with specified options. Off if not provided.
	ACL        bool         // Enable ACL during setup.
	AddressSet bool         // Enable address sets, only for netfilter.
}

// ACLRule represents an ACL rule that can be added to a firewall.
type ACLRule struct {
	Direction       string // Either "ingress" or "egress.
	Action          string
	Log             bool   // Whether or not to log matched packets.
	LogName         string // Log label name (requires Log be true).
	Source          string
	Destination     string
	Protocol        string
	SourcePort      string
	DestinationPort string
	ICMPType        string
	ICMPCode        string
}

// AddressForward represents a NAT address forward.
type AddressForward struct {
	ListenAddress net.IP
	TargetAddress net.IP
	Protocol      string
	ListenPorts   []uint64
	TargetPorts   []uint64
	SNAT          bool
}

// AddressSet represent an address set.
type AddressSet struct {
	Name      string
	Addresses []string
}

// NftListSetsOutput structure to read JSON output of set listing.
type NftListSetsOutput struct {
	Nftables []NftListSetsEntry `json:"nftables"`
}

// NftListSetsEntry structure to read JSON output of nft set listing.
type NftListSetsEntry struct {
	Set *NftSet `json:"set,omitempty"`
}

// NftSet structure to parse the JSON of a set returned by nft -j list sets.
type NftSet struct {
	Family string `json:"family"`
	Name   string `json:"name"`
	Table  string `json:"table"`
}
