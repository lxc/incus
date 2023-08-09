package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v2"

	"github.com/lxc/incus/incusd/backup/config"
	"github.com/lxc/incus/incusd/db"
	"github.com/lxc/incus/incusd/db/cluster"
	deviceConfig "github.com/lxc/incus/incusd/device/config"
	"github.com/lxc/incus/incusd/instance/instancetype"
	"github.com/lxc/incus/incusd/state"
	"github.com/lxc/incus/shared"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/osarch"
)

// ConfigToInstanceDBArgs converts the instance config in the backup config to DB InstanceArgs.
func ConfigToInstanceDBArgs(state *state.State, c *config.Config, projectName string, applyProfiles bool) (*db.InstanceArgs, error) {
	if c.Container == nil {
		return nil, nil
	}

	arch, _ := osarch.ArchitectureId(c.Container.Architecture)
	instanceType, _ := instancetype.New(c.Container.Type)

	inst := &db.InstanceArgs{
		Project:      projectName,
		Architecture: arch,
		BaseImage:    c.Container.Config["volatile.base_image"],
		Config:       c.Container.Config,
		CreationDate: c.Container.CreatedAt,
		Type:         instanceType,
		Description:  c.Container.Description,
		Devices:      deviceConfig.NewDevices(c.Container.Devices),
		Ephemeral:    c.Container.Ephemeral,
		LastUsedDate: c.Container.LastUsedAt,
		Name:         c.Container.Name,
		Stateful:     c.Container.Stateful,
	}

	if applyProfiles {
		err := state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			inst.Profiles = make([]api.Profile, 0, len(c.Container.Profiles))
			profiles, err := cluster.GetProfilesIfEnabled(ctx, tx.Tx(), projectName, c.Container.Profiles)
			if err != nil {
				return err
			}

			for _, profile := range profiles {
				apiProfile, err := profile.ToAPI(ctx, tx.Tx())
				if err != nil {
					return err
				}

				inst.Profiles = append(inst.Profiles, *apiProfile)
			}

			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	return inst, nil
}

// ParseConfigYamlFile decodes the YAML file at path specified into a Config.
func ParseConfigYamlFile(path string) (*config.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	backupConf := config.Config{}
	err = yaml.Unmarshal(data, &backupConf)
	if err != nil {
		return nil, err
	}

	// Default to container if type not specified in backup config.
	if backupConf.Container != nil && backupConf.Container.Type == "" {
		backupConf.Container.Type = string(api.InstanceTypeContainer)
	}

	return &backupConf, nil
}

// updateRootDevicePool updates the root disk device in the supplied list of devices to the pool
// specified. Returns true if a root disk device has been found and updated otherwise false.
func updateRootDevicePool(devices map[string]map[string]string, poolName string) bool {
	if devices != nil {
		devName, _, err := shared.GetRootDiskDevice(devices)
		if err == nil {
			devices[devName]["pool"] = poolName
			return true
		}
	}

	return false
}

// UpdateInstanceConfigStoragePool changes the pool information in the backup.yaml to the pool specified in b.Pool.
func UpdateInstanceConfigStoragePool(c *db.Cluster, b Info, mountPath string) error {
	// Load the storage pool.
	_, pool, _, err := c.GetStoragePool(b.Pool)
	if err != nil {
		return err
	}

	f := func(path string) error {
		// Read in the backup.yaml file.
		backup, err := ParseConfigYamlFile(path)
		if err != nil {
			return err
		}

		rootDiskDeviceFound := false

		// Change the pool in the backup.yaml.
		backup.Pool = pool

		if updateRootDevicePool(backup.Container.Devices, pool.Name) {
			rootDiskDeviceFound = true
		}

		if updateRootDevicePool(backup.Container.ExpandedDevices, pool.Name) {
			rootDiskDeviceFound = true
		}

		for _, snapshot := range backup.Snapshots {
			updateRootDevicePool(snapshot.Devices, pool.Name)
			updateRootDevicePool(snapshot.ExpandedDevices, pool.Name)
		}

		if !rootDiskDeviceFound {
			return fmt.Errorf("No root device could be found")
		}

		file, err := os.Create(path)
		if err != nil {
			return err
		}

		defer func() { _ = file.Close() }()

		data, err := yaml.Marshal(&backup)
		if err != nil {
			return err
		}

		_, err = file.Write(data)
		if err != nil {
			return err
		}

		return file.Close()
	}

	err = f(filepath.Join(mountPath, "backup.yaml"))
	if err != nil {
		return err
	}

	return nil
}
