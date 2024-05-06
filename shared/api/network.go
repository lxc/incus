package api

// NetworksPost represents the fields of a new network
//
// swagger:model
//
// API extension: network.
type NetworksPost struct {
	NetworkPut `yaml:",inline"`

	// The name of the new network
	// Example: mybr1
	Name string `json:"name" yaml:"name"`

	// The network type (refer to doc/networks.md)
	// Example: bridge
	Type string `json:"type" yaml:"type"`
}

// NetworkPost represents the fields required to rename a network
//
// swagger:model
//
// API extension: network.
type NetworkPost struct {
	// The new name for the network
	// Example: mybr1
	Name string `json:"name" yaml:"name"`
}

// NetworkPut represents the modifiable fields of a network
//
// swagger:model
//
// API extension: network.
type NetworkPut struct {
	// Network configuration map (refer to doc/networks.md)
	// Example: {"ipv4.address": "10.0.0.1/24", "ipv4.nat": "true", "ipv6.address": "none"}
	Config map[string]string `json:"config" yaml:"config"`

	// Description of the profile
	// Example: My new bridge
	//
	// API extension: entity_description
	Description string `json:"description" yaml:"description"`
}

// NetworkStatusPending network is pending creation on other cluster nodes.
const NetworkStatusPending = "Pending"

// NetworkStatusCreated network is fully created.
const NetworkStatusCreated = "Created"

// NetworkStatusErrored network is in error status.
const NetworkStatusErrored = "Errored"

// NetworkStatusUnknown network is in unknown status.
const NetworkStatusUnknown = "Unknown"

// NetworkStatusUnavailable network failed to initialize.
const NetworkStatusUnavailable = "Unavailable"

// Network represents a network
//
// swagger:model
type Network struct {
	NetworkPut `yaml:",inline"`

	// The network name
	// Read only: true
	// Example: mybr0
	Name string `json:"name" yaml:"name"`

	// The network type
	// Read only: true
	// Example: bridge
	Type string `json:"type" yaml:"type"`

	// List of URLs of objects using this profile
	// Read only: true
	// Example: ["/1.0/profiles/default", "/1.0/instances/c1"]
	UsedBy []string `json:"used_by" yaml:"used_by"`

	// Whether this is a managed network
	// Read only: true
	// Example: true
	//
	// API extension: network
	Managed bool `json:"managed" yaml:"managed"`

	// The state of the network (for managed network in clusters)
	// Read only: true
	// Example: Created
	//
	// API extension: clustering
	Status string `json:"status" yaml:"status"`

	// Cluster members on which the network has been defined
	// Read only: true
	// Example: ["server01", "server02", "server03"]
	//
	// API extension: clustering
	Locations []string `json:"locations" yaml:"locations"`

	// Project name
	// Example: project1
	//
	// API extension: networks_all_projects
	Project string `json:"project" yaml:"project"`
}

// Writable converts a full Network struct into a NetworkPut struct (filters read-only fields).
func (network *Network) Writable() NetworkPut {
	return network.NetworkPut
}

// NetworkLease represents a DHCP lease
//
// swagger:model
//
// API extension: network_leases.
type NetworkLease struct {
	// The hostname associated with the record
	// Example: c1
	Hostname string `json:"hostname" yaml:"hostname"`

	// The MAC address
	// Example: 00:16:3e:2c:89:d9
	Hwaddr string `json:"hwaddr" yaml:"hwaddr"`

	// The IP address
	// Example: 10.0.0.98
	Address string `json:"address" yaml:"address"`

	// The type of record (static or dynamic)
	// Example: dynamic
	Type string `json:"type" yaml:"type"`

	// What cluster member this record was found on
	// Example: server01
	//
	// API extension: network_leases_location
	Location string `json:"location" yaml:"location"`
}

// NetworkState represents the network state
//
// swagger:model
type NetworkState struct {
	// List of addresses
	Addresses []NetworkStateAddress `json:"addresses" yaml:"addresses"`

	// Interface counters
	Counters NetworkStateCounters `json:"counters" yaml:"counters"`

	// MAC address
	// Example: 00:16:3e:5a:83:57
	Hwaddr string `json:"hwaddr" yaml:"hwaddr"`

	// MTU
	// Example: 1500
	Mtu int `json:"mtu" yaml:"mtu"`

	// Link state
	// Example: up
	State string `json:"state" yaml:"state"`

	// Interface type
	// Example: broadcast
	Type string `json:"type" yaml:"type"`

	// Additional bond interface information
	//
	// API extension: network_state_bond_bridge
	Bond *NetworkStateBond `json:"bond" yaml:"bond"`

	// Additional bridge interface information
	//
	// API extension: network_state_bond_bridge
	Bridge *NetworkStateBridge `json:"bridge" yaml:"bridge"`

	// Additional vlan interface information
	//
	// API extension: network_state_vlan
	VLAN *NetworkStateVLAN `json:"vlan" yaml:"vlan"`

	// Additional OVN network information
	//
	// API extension: network_state_ovn
	OVN *NetworkStateOVN `json:"ovn" yaml:"ovn"`
}

// NetworkStateAddress represents a network address
//
// swagger:model
type NetworkStateAddress struct {
	// Address family
	// Example: inet
	Family string `json:"family" yaml:"family"`

	// IP address
	// Example: 10.0.0.1
	Address string `json:"address" yaml:"address"`

	// IP netmask (CIDR)
	// Example: 24
	Netmask string `json:"netmask" yaml:"netmask"`

	// Address scope
	// Example: global
	Scope string `json:"scope" yaml:"scope"`
}

// NetworkStateCounters represents packet counters
//
// swagger:model
type NetworkStateCounters struct {
	// Number of bytes received
	// Example: 250542118
	BytesReceived int64 `json:"bytes_received" yaml:"bytes_received"`

	// Number of bytes sent
	// Example: 17524040140
	BytesSent int64 `json:"bytes_sent" yaml:"bytes_sent"`

	// Number of packets received
	// Example: 1182515
	PacketsReceived int64 `json:"packets_received" yaml:"packets_received"`

	// Number of packets sent
	// Example: 1567934
	PacketsSent int64 `json:"packets_sent" yaml:"packets_sent"`
}

// NetworkStateBond represents bond specific state
//
// swagger:model
//
// API extension: network_state_bond_bridge.
type NetworkStateBond struct {
	// Bonding mode
	// Example: 802.3ad
	Mode string `json:"mode" yaml:"mode"`

	// Transmit balancing policy
	// Example: layer3+4
	TransmitPolicy string `json:"transmit_policy" yaml:"transmit_policy"`

	// Delay on link up (ms)
	// Example: 0
	UpDelay uint64 `json:"up_delay" yaml:"up_delay"`

	// Delay on link down (ms)
	// Example: 0
	DownDelay uint64 `json:"down_delay" yaml:"down_delay"`

	// How often to check for link state (ms)
	// Example: 100
	MIIFrequency uint64 `json:"mii_frequency" yaml:"mii_frequency"`

	// Bond link state
	// Example: up
	MIIState string `json:"mii_state" yaml:"mii_state"`

	// List of devices that are part of the bond
	// Example: ["eth0", "eth1"]
	LowerDevices []string `json:"lower_devices" yaml:"lower_devices"`
}

// NetworkStateBridge represents bridge specific state
//
// swagger:model
//
// API extension: network_state_bond_bridge.
type NetworkStateBridge struct {
	// Bridge ID
	// Example: 8000.0a0f7c6edbd9
	ID string `json:"id" yaml:"id"`

	// Whether STP is enabled
	// Example: false
	STP bool `json:"stp" yaml:"stp"`

	// Delay on port join (ms)
	// Example: 1500
	ForwardDelay uint64 `json:"forward_delay" yaml:"forward_delay"`

	// Default VLAN ID
	// Example: 1
	VLANDefault uint64 `json:"vlan_default" yaml:"vlan_default"`

	// Whether VLAN filtering is enabled
	// Example: false
	VLANFiltering bool `json:"vlan_filtering" yaml:"vlan_filtering"`

	// List of devices that are in the bridge
	// Example: ["eth0", "eth1"]
	UpperDevices []string `json:"upper_devices" yaml:"upper_devices"`
}

// NetworkStateVLAN represents VLAN specific state
//
// swagger:model
//
// API extension: network_state_vlan.
type NetworkStateVLAN struct {
	// Parent device
	// Example: eth0
	LowerDevice string `json:"lower_device" yaml:"lower_device"`

	// VLAN ID
	// Example: 100
	VID uint64 `json:"vid" yaml:"vid"`
}

// NetworkStateOVN represents OVN specific state
//
// swagger:model
//
// API extension: network_state_ovn.
type NetworkStateOVN struct {
	// OVN network chassis name
	// Example: server01
	Chassis string `json:"chassis" yaml:"chassis"`

	// OVN logical router name
	// Example: incus-net1-lr
	//
	// API extension: network_state_ovn_lr
	LogicalRouter string `json:"logical_router" yaml:"logical_router"`
}
