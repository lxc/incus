package config

import (
	"github.com/lxc/incus/shared/api"
)

// Config represents the config of a backup that can be stored in a backup.yaml file (or embedded in index.yaml).
type Config struct {
	Container       *api.Instance                `yaml:"container,omitempty"` // Used by VM backups too.
	Snapshots       []*api.InstanceSnapshot      `yaml:"snapshots,omitempty"`
	Pool            *api.StoragePool             `yaml:"pool,omitempty"`
	Profiles        []*api.Profile               `yaml:"profiles,omitempty"`
	Volume          *api.StorageVolume           `yaml:"volume,omitempty"`
	VolumeSnapshots []*api.StorageVolumeSnapshot `yaml:"volume_snapshots,omitempty"`
	Bucket          *api.StorageBucket           `yaml:"bucket,omitempty"`
	BucketKeys      []*api.StorageBucketKey      `yaml:"bucket_keys,omitempty"`
}
