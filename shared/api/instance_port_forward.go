package api

// InstancePortForwardPost represents an instance port forwarding request.
//
// swagger:model
//
// API extension: instance_port_forward.
type InstancePortForwardPost struct {
	// Address to connect to inside of the instance
	// Example: 127.0.0.1
	Address string `json:"address" yaml:"address"`

	// TCP port to connect to inside of the instance
	// Example: 80
	Port int `json:"port" yaml:"port"`
}
