package main

import (
	"fmt"
	"os"
	"os/exec"

	lxdAPI "github.com/canonical/lxd/shared/api"

	"github.com/lxc/incus/internal/linux"
	"github.com/lxc/incus/internal/version"
	incusAPI "github.com/lxc/incus/shared/api"
)

var minLXDVersion = &version.DottedVersion{4, 0, 0}
var maxLXDVersion = &version.DottedVersion{5, 19, 0}

func (c *cmdMigrate) validate(source Source, target Target) error {
	srcClient, err := source.Connect()
	if err != nil {
		return fmt.Errorf("Failed to connect to source: %v", err)
	}

	targetClient, err := target.Connect()
	if err != nil {
		return fmt.Errorf("Failed to connect to target: %v", err)
	}

	// Get versions.
	fmt.Println("=> Checking server versions")
	srcServerInfo, _, err := srcClient.GetServer()
	if err != nil {
		return fmt.Errorf("Failed getting source server info: %w", err)
	}

	targetServerInfo, _, err := targetClient.GetServer()
	if err != nil {
		return fmt.Errorf("Failed getting target server info: %w", err)
	}

	fmt.Printf("==> Source version: %s\n", srcServerInfo.Environment.ServerVersion)
	fmt.Printf("==> Target version: %s\n", targetServerInfo.Environment.ServerVersion)

	// Compare versions.
	fmt.Println("=> Validating version compatibility")
	srcVersion, err := version.Parse(srcServerInfo.Environment.ServerVersion)
	if err != nil {
		return fmt.Errorf("Couldn't parse source server version: %w", err)
	}

	if srcVersion.Compare(minLXDVersion) < 0 {
		return fmt.Errorf("LXD version is lower than minimal version %q", minLXDVersion)
	}

	if srcVersion.Compare(maxLXDVersion) > 0 {
		return fmt.Errorf("LXD version is newer than maximum version %q", maxLXDVersion)
	}

	// Validate source non-empty.
	srcCheckEmpty := func() (bool, error) {
		// Check if more than one project.
		names, err := srcClient.GetProjectNames()
		if err != nil {
			return false, err
		}

		if len(names) > 1 {
			return false, nil
		}

		// Check if more than one profile.
		names, err = srcClient.GetProfileNames()
		if err != nil {
			return false, err
		}

		if len(names) > 1 {
			return false, nil
		}

		// Check if any instance is persent.
		names, err = srcClient.GetInstanceNames(lxdAPI.InstanceTypeAny)
		if err != nil {
			return false, err
		}

		if len(names) > 0 {
			return false, nil
		}

		// Check if any storage pool is present.
		names, err = srcClient.GetStoragePoolNames()
		if err != nil {
			return false, err
		}

		if len(names) > 0 {
			return false, nil
		}

		// Check if any network is present.
		networks, err := srcClient.GetNetworks()
		if err != nil {
			return false, err
		}

		for _, network := range networks {
			if network.Managed {
				return false, nil
			}
		}

		return true, nil
	}

	fmt.Println("=> Checking that the source server isn't empty")
	isEmpty, err := srcCheckEmpty()
	if err != nil {
		return fmt.Errorf("Failed to check source server: %w", err)
	}

	if isEmpty {
		return fmt.Errorf("Source server is empty, migration not needed")
	}

	// Validate target empty.
	targetCheckEmpty := func() (bool, error) {
		// Check if more than one project.
		names, err := targetClient.GetProjectNames()
		if err != nil {
			return false, err
		}

		if len(names) > 1 {
			return false, nil
		}

		// Check if more than one profile.
		names, err = targetClient.GetProfileNames()
		if err != nil {
			return false, err
		}

		if len(names) > 1 {
			return false, nil
		}

		// Check if any instance is present.
		names, err = targetClient.GetInstanceNames(incusAPI.InstanceTypeAny)
		if err != nil {
			return false, err
		}

		if len(names) > 0 {
			return false, nil
		}

		// Check if any storage pool is present.
		names, err = targetClient.GetStoragePoolNames()
		if err != nil {
			return false, err
		}

		if len(names) > 0 {
			return false, nil
		}

		// Check if any network is present.
		networks, err := targetClient.GetNetworks()
		if err != nil {
			return false, err
		}

		for _, network := range networks {
			if network.Managed {
				return false, nil
			}
		}

		return true, nil
	}

	fmt.Println("=> Checking that the target server is empty")
	isEmpty, err = targetCheckEmpty()
	if err != nil {
		return fmt.Errorf("Failed to check target server: %w", err)
	}

	if !isEmpty {
		return fmt.Errorf("Target server isn't empty, can't proceed with migration.")
	}

	// Validate configuration.
	errors := []error{}

	fmt.Println("=> Validating source server configuration")
	deprecatedConfigs := []string{
		"candid.api.key",
		"candid.api.url",
		"candid.domains",
		"candid.expiry",
		"core.trust_password",
		"maas.api.key",
		"maas.api.url",
		"rbac.agent.url",
		"rbac.agent.username",
		"rbac.agent.private_key",
		"rbac.agent.public_key",
		"rbac.api.expiry",
		"rbac.api.key",
		"rbac.api.url",
		"rbac.expiry",
	}

	for _, key := range deprecatedConfigs {
		_, ok := srcServerInfo.Config[key]
		if ok {
			errors = append(errors, fmt.Errorf("Source server is using deprecated key %q", key))
		}
	}

	networks, err := srcClient.GetNetworks()
	if err != nil {
		return fmt.Errorf("Couldn't list source networks: %w", err)
	}

	deprecatedNetworkConfigs := []string{
		"bridge.mode",
		"fan.overlay_subnet",
		"fan.underlay_subnet",
		"fan.type",
	}

	for _, network := range networks {
		if !network.Managed {
			continue
		}

		for _, key := range deprecatedNetworkConfigs {
			_, ok := network.Config[key]
			if ok {
				errors = append(errors, fmt.Errorf("Source server has network %q using deprecated key %q", network.Name, key))
			}
		}
	}

	storagePools, err := srcClient.GetStoragePools()
	if err != nil {
		return fmt.Errorf("Couldn't list storage pools: %w", err)
	}

	for _, pool := range storagePools {
		if pool.Driver == "zfs" {
			_, err = exec.LookPath("zfs")
			if err != nil {
				errors = append(errors, fmt.Errorf("Required command %q is missing for storage pool %q", "zfs", pool.Name))
			}
		} else if pool.Driver == "btrfs" {
			_, err = exec.LookPath("btrfs")
			if err != nil {
				errors = append(errors, fmt.Errorf("Required command %q is missing for storage pool %q", "btrfs", pool.Name))
			}
		} else if pool.Driver == "ceph" || pool.Driver == "cephfs" || pool.Driver == "cephobject" {
			_, err = exec.LookPath("ceph")
			if err != nil {
				errors = append(errors, fmt.Errorf("Required command %q is missing for storage pool %q", "ceph", pool.Name))
			}
		} else if pool.Driver == "lvm" {
			_, err = exec.LookPath("lvm")
			if err != nil {
				errors = append(errors, fmt.Errorf("Required command %q is missing for storage pool %q", "lvm", pool.Name))
			}
		}
	}

	deprecatedInstanceConfigs := []string{
		"limits.network.priority",
	}

	deprecatedInstanceDeviceConfigs := []string{
		"maas.subnet.ipv4",
		"maas.subnet.ipv6",
	}

	projects, err := srcClient.GetProjects()
	if err != nil {
		return fmt.Errorf("Couldn't list source projects: %w", err)
	}

	for _, project := range projects {
		c := srcClient.UseProject(project.Name)

		instances, err := c.GetInstances(lxdAPI.InstanceTypeAny)
		if err != nil {
			fmt.Errorf("Couldn't list instances in project %q: %w", err)
		}

		for _, inst := range instances {
			for _, key := range deprecatedInstanceConfigs {
				_, ok := inst.Config[key]
				if ok {
					errors = append(errors, fmt.Errorf("Source server has instance %q in project %q using deprecated key %q", inst.Name, project.Name, key))
				}
			}

			for deviceName, device := range inst.Devices {
				for _, key := range deprecatedInstanceDeviceConfigs {
					_, ok := device[key]
					if ok {
						errors = append(errors, fmt.Errorf("Source server has device %q for instance %q in project %q using deprecated key %q", deviceName, inst.Name, project.Name, key))
					}
				}
			}
		}

		profiles, err := c.GetProfiles()
		if err != nil {
			fmt.Errorf("Couldn't list profiles in project %q: %w", err)
		}

		for _, profile := range profiles {
			for _, key := range deprecatedInstanceConfigs {
				_, ok := profile.Config[key]
				if ok {
					errors = append(errors, fmt.Errorf("Source server has profile %q in project %q using deprecated key %q", profile.Name, project.Name, key))
				}
			}

			for deviceName, device := range profile.Devices {
				for _, key := range deprecatedInstanceDeviceConfigs {
					_, ok := device[key]
					if ok {
						errors = append(errors, fmt.Errorf("Source server has device %q for profile %q in project %q using deprecated key %q", deviceName, profile.Name, project.Name, key))
					}
				}
			}
		}
	}

	// Cluster validation.
	if srcServerInfo.Environment.ServerClustered {
		clusterMembers, err := srcClient.GetClusterMembers()
		if err != nil {
			return fmt.Errorf("Failed to retrieve the list of cluster members")
		}

		for _, member := range clusterMembers {
			if member.Status != "Online" {
				if os.Getenv("CLUSTER_NO_STOP") == "1" && member.Status == "Evacuated" {
					continue
				}

				errors = append(errors, fmt.Errorf("Cluster member %q isn't in the online state", member.ServerName))
			}
		}
	}

	if len(errors) > 0 {
		fmt.Println("")
		fmt.Println("Source server uses obsolete features:")
		for _, err := range errors {
			fmt.Printf(" - %s\n", err.Error())
		}

		return fmt.Errorf("Source server is using incompatible configuration")
	}

	// Storage validation.
	targetPaths, err := target.Paths()
	if err != nil {
		return fmt.Errorf("Failed to get target paths: %w", err)
	}

	if linux.IsMountPoint(targetPaths.Daemon) {
		return fmt.Errorf("The target path %q is a mountpoint. This isn't currently supported as the target path needs to be deleted during the migration.", targetPaths.Daemon)
	}

	sourcePaths, err := source.Paths()
	if err != nil {
		return fmt.Errorf("Failed to get source paths: %w", err)
	}

	srcFilesystem, _ := linux.DetectFilesystem(sourcePaths.Daemon)
	targetFilesystem, _ := linux.DetectFilesystem(targetPaths.Daemon)
	if srcFilesystem == "btrfs" && targetFilesystem != "btrfs" && !linux.IsMountPoint(sourcePaths.Daemon) {
		return fmt.Errorf("Source daemon running on btrfs but being moved to non-btrfs target")
	}

	return nil
}
