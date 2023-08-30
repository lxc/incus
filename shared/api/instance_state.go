package api

// InstanceStatePut represents the modifiable fields of an instance's state.
//
// swagger:model
//
// API extension: instances.
type InstanceStatePut struct {
	// State change action (start, stop, restart, freeze, unfreeze)
	// Example: start
	Action string `json:"action" yaml:"action"`

	// How long to wait (in s) before giving up (when force isn't set)
	// Example: 30
	Timeout int `json:"timeout" yaml:"timeout"`

	// Whether to force the action (for stop and restart)
	// Example: false
	Force bool `json:"force" yaml:"force"`

	// Whether to store the runtime state (for stop)
	// Example: false
	Stateful bool `json:"stateful" yaml:"stateful"`
}

// InstanceState represents an instance's state.
//
// swagger:model
//
// API extension: instances.
type InstanceState struct {
	// Current status (Running, Stopped, Frozen or Error)
	// Example: Running
	Status string `json:"status" yaml:"status"`

	// Numeric status code (101, 102, 110, 112)
	// Example: 101
	StatusCode StatusCode `json:"status_code" yaml:"status_code"`

	// Disk usage key/value pairs
	Disk map[string]InstanceStateDisk `json:"disk" yaml:"disk"`

	// Memory usage information
	Memory InstanceStateMemory `json:"memory" yaml:"memory"`

	// Network usage key/value pairs
	Network map[string]InstanceStateNetwork `json:"network" yaml:"network"`

	// PID of the runtime
	// Example: 7281
	Pid int64 `json:"pid" yaml:"pid"`

	// Number of processes in the instance
	// Example: 50
	Processes int64 `json:"processes" yaml:"processes"`

	// CPU usage information
	CPU InstanceStateCPU `json:"cpu" yaml:"cpu"`
}

// InstanceStateDisk represents the disk information section of an instance's state.
//
// swagger:model
//
// API extension: instances.
type InstanceStateDisk struct {
	// Disk usage in bytes
	// Example: 502239232
	Usage int64 `json:"usage" yaml:"usage"`

	// Total size in bytes
	// Example: 502239232
	//
	// API extension: instances_state_total
	Total int64 `json:"total" yaml:"total"`
}

// InstanceStateCPU represents the cpu information section of an instance's state.
//
// swagger:model
//
// API extension: instances.
type InstanceStateCPU struct {
	// CPU usage in nanoseconds
	// Example: 3637691016
	Usage int64 `json:"usage" yaml:"usage"`
}

// InstanceStateMemory represents the memory information section of an instance's state.
//
// swagger:model
//
// API extension: instances.
type InstanceStateMemory struct {
	// Memory usage in bytes
	// Example: 73248768
	Usage int64 `json:"usage" yaml:"usage"`

	// Peak memory usage in bytes
	// Example: 73785344
	UsagePeak int64 `json:"usage_peak" yaml:"usage_peak"`

	// Total memory size in bytes
	// Example: 12297557
	//
	// API extension: instances_state_total
	Total int64 `json:"total" yaml:"total"`

	// SWAP usage in bytes
	// Example: 12297557
	SwapUsage int64 `json:"swap_usage" yaml:"swap_usage"`

	// Peak SWAP usage in bytes
	// Example: 12297557
	SwapUsagePeak int64 `json:"swap_usage_peak" yaml:"swap_usage_peak"`
}

// InstanceStateNetwork represents the network information section of an instance's state.
//
// swagger:model
//
// API extension: instances.
type InstanceStateNetwork struct {
	// List of IP addresses
	Addresses []InstanceStateNetworkAddress `json:"addresses" yaml:"addresses"`

	// Traffic counters
	Counters InstanceStateNetworkCounters `json:"counters" yaml:"counters"`

	// MAC address
	// Example: 00:16:3e:0c:ee:dd
	Hwaddr string `json:"hwaddr" yaml:"hwaddr"`

	// Name of the interface on the host
	// Example: vethbbcd39c7
	HostName string `json:"host_name" yaml:"host_name"`

	// MTU (maximum transmit unit) for the interface
	// Example: 1500
	Mtu int `json:"mtu" yaml:"mtu"`

	// Administrative state of the interface (up/down)
	// Example: up
	State string `json:"state" yaml:"state"`

	// Type of interface (broadcast, loopback, point-to-point, ...)
	// Example: broadcast
	Type string `json:"type" yaml:"type"`
}

// InstanceStateNetworkAddress represents a network address as part of the network section of an
// instance's state.
//
// swagger:model
//
// API extension: instances.
type InstanceStateNetworkAddress struct {
	// Network family (inet or inet6)
	// Example: inet6
	Family string `json:"family" yaml:"family"`

	// IP address
	// Example: fd42:4c81:5770:1eaf:216:3eff:fe0c:eedd
	Address string `json:"address" yaml:"address"`

	// Network mask
	// Example: 64
	Netmask string `json:"netmask" yaml:"netmask"`

	// Address scope (local, link or global)
	// Example: global
	Scope string `json:"scope" yaml:"scope"`
}

// InstanceStateNetworkCounters represents packet counters as part of the network section of an
// instance's state.
//
// swagger:model
//
// API extension: instances.
type InstanceStateNetworkCounters struct {
	// Number of bytes received
	// Example: 192021
	BytesReceived int64 `json:"bytes_received" yaml:"bytes_received"`

	// Number of bytes sent
	// Example: 10888579
	BytesSent int64 `json:"bytes_sent" yaml:"bytes_sent"`

	// Number of packets received
	// Example: 1748
	PacketsReceived int64 `json:"packets_received" yaml:"packets_received"`

	// Number of packets sent
	// Example: 964
	PacketsSent int64 `json:"packets_sent" yaml:"packets_sent"`

	// Number of errors received
	// Example: 14
	ErrorsReceived int64 `json:"errors_received" yaml:"errors_received"`

	// Number of errors sent
	// Example: 41
	ErrorsSent int64 `json:"errors_sent" yaml:"errors_sent"`

	// Number of outbound packets dropped
	// Example: 541
	PacketsDroppedOutbound int64 `json:"packets_dropped_outbound" yaml:"packets_dropped_outbound"`

	// Number of inbound packets dropped
	// Example: 179
	PacketsDroppedInbound int64 `json:"packets_dropped_inbound" yaml:"packets_dropped_inbound"`
}
