package instancetype

import (
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
)

// VMAgentMount defines mounts to perform inside VM via agent.
type VMAgentMount struct {
	Source  string   `json:"source"`
	Target  string   `json:"target"`
	FSType  string   `json:"fstype"`
	Options []string `json:"options"`
}

// VMAgentData represents the instance data exposed to the VM agent.
type VMAgentData struct {
	Name        string                         `json:"name"`
	CloudInitID string                         `json:"cloud_init_id"`
	Location    string                         `json:"location"`
	Config      map[string]string              `json:"config,omitempty"`
	Devices     map[string]deviceConfig.Device `json:"devices,omitempty"`
}
