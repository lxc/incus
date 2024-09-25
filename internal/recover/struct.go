package recover

import (
	"github.com/lxc/incus/v6/shared/api"
)

// ValidatePost is used to initiate a recovery validation scan.
type ValidatePost struct {
	Pools []api.StoragePoolsPost `json:"pools" yaml:"pools"`
}

// ValidateVolume provides info about a missing volume that the recovery validation scan found.
type ValidateVolume struct {
	Name          string `json:"name" yaml:"name"`                   // Name of volume.
	Type          string `json:"type" yaml:"type"`                   // Same as Type from StorageVolumesPost (container, custom or virtual-machine).
	SnapshotCount int    `json:"snapshotCount" yaml:"snapshotCount"` // Count of snapshots found for volume.
	Project       string `json:"project" yaml:"project"`             // Project the volume belongs to.
	Pool          string `json:"pool" yaml:"pool"`                   // Pool the volume belongs to.
}

// ValidateResult returns the result of the validation scan.
type ValidateResult struct {
	UnknownVolumes   []ValidateVolume // Volumes that could be imported.
	DependencyErrors []string         // Errors that are preventing import from proceeding.
}

// ImportPost is used to initiate a recovert import.
type ImportPost struct {
	Pools []api.StoragePoolsPost `json:"pools" yaml:"pools"`
}
