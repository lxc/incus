package scriptlet

import (
	"github.com/lxc/incus/v6/shared/api"
)

// InstancePlacementReasonNew is when a new instance request is received.
const InstancePlacementReasonNew = "new"

// InstancePlacementReasonRelocation is when an existing instance is temporarily migrated because a cluster member is down.
const InstancePlacementReasonRelocation = "relocation"

// InstancePlacementReasonEvacuation is when an existing instance is temporarily migrated because a cluster member is being evacuated.
const InstancePlacementReasonEvacuation = "evacuation"

// InstanceResources represents the required resources for an instance.
//
// API extension: instances_placement_scriptlet.
type InstanceResources struct {
	CPUCores     uint64 `json:"cpu_cores"`
	MemorySize   uint64 `json:"memory_size"`
	RootDiskSize uint64 `json:"root_disk_size"`
}

// InstancePlacement represents the instance placement request.
//
// API extension: instances_placement_scriptlet.
type InstancePlacement struct {
	api.InstancesPost `yaml:",inline"`

	Reason  string `json:"reason"`
	Project string `json:"project"`
}
