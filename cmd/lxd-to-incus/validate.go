package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/lxc/incus/internal/linux"
	"github.com/lxc/incus/internal/version"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/util"
)

var minLXDVersion = &version.DottedVersion{Major: 4, Minor: 0, Patch: 0}
var maxLXDVersion = &version.DottedVersion{Major: 5, Minor: 20, Patch: 0}

func (c *cmdMigrate) validate(source source, target target) error {
	srcClient, err := source.connect()
	if err != nil {
		return fmt.Errorf("Failed to connect to source: %v", err)
	}

	targetClient, err := target.connect()
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

	if !c.flagIgnoreVersionCheck {
		if srcVersion.Compare(maxLXDVersion) > 0 {
			return fmt.Errorf("LXD version is newer than maximum version %q", maxLXDVersion)
		}
	} else {
		fmt.Println("==> WARNING: User asked to bypass version check")
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
		names, err = srcClient.GetInstanceNames(api.InstanceTypeAny)
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
	targetCheckEmpty := func() (bool, string, error) {
		// Check if more than one project.
		names, err := targetClient.GetProjectNames()
		if err != nil {
			return false, "", err
		}

		if len(names) > 1 {
			return false, "projects", nil
		}

		// Check if more than one profile.
		names, err = targetClient.GetProfileNames()
		if err != nil {
			return false, "", err
		}

		if len(names) > 1 {
			return false, "profiles", nil
		}

		// Check if any instance is present.
		names, err = targetClient.GetInstanceNames(api.InstanceTypeAny)
		if err != nil {
			return false, "", err
		}

		if len(names) > 0 {
			return false, "instances", nil
		}

		// Check if any storage pool is present.
		names, err = targetClient.GetStoragePoolNames()
		if err != nil {
			return false, "", err
		}

		if len(names) > 0 {
			return false, "storage pools", nil
		}

		// Check if any network is present.
		networks, err := targetClient.GetNetworks()
		if err != nil {
			return false, "", err
		}

		for _, network := range networks {
			if network.Managed {
				return false, "networks", nil
			}
		}

		return true, "", nil
	}

	fmt.Println("=> Checking that the target server is empty")
	isEmpty, found, err := targetCheckEmpty()
	if err != nil {
		return fmt.Errorf("Failed to check target server: %w", err)
	}

	if !isEmpty {
		return fmt.Errorf("Target server isn't empty (%s found), can't proceed with migration.", found)
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
			errors = append(errors, fmt.Errorf("Source server is using deprecated server configuration key %q", key))
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
				errors = append(errors, fmt.Errorf("Source server has network %q using deprecated configuration key %q", network.Name, key))
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
		} else if pool.Driver == "lvm" || pool.Driver == "lvmcluster" {
			_, err = exec.LookPath("lvm")
			if err != nil {
				errors = append(errors, fmt.Errorf("Required command %q is missing for storage pool %q", "lvm", pool.Name))
			}

			if pool.Driver == "lvmcluster" {
				_, err = exec.LookPath("lvmlockctl")
				if err != nil {
					errors = append(errors, fmt.Errorf("Required command %q is missing for storage pool %q", "lvmlockctl", pool.Name))
				}
			}
		}
	}

	deprecatedInstanceConfigs := []string{
		"boot.debug_edk2",
		"limits.network.priority",
		"security.devlxd",
		"security.devlxd.images",
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

		instances, err := c.GetInstances(api.InstanceTypeAny)
		if err != nil {
			return fmt.Errorf("Couldn't list instances in project %q: %w", project.Name, err)
		}

		for _, inst := range instances {
			for _, key := range deprecatedInstanceConfigs {
				_, ok := inst.Config[key]
				if ok {
					errors = append(errors, fmt.Errorf("Source server has instance %q in project %q using deprecated configuration key %q", inst.Name, project.Name, key))
				}
			}

			for deviceName, device := range inst.Devices {
				for _, key := range deprecatedInstanceDeviceConfigs {
					_, ok := device[key]
					if ok {
						errors = append(errors, fmt.Errorf("Source server has device %q for instance %q in project %q using deprecated configuration key %q", deviceName, inst.Name, project.Name, key))
					}
				}
			}
		}

		profiles, err := c.GetProfiles()
		if err != nil {
			return fmt.Errorf("Couldn't list profiles in project %q: %w", project.Name, err)
		}

		for _, profile := range profiles {
			for _, key := range deprecatedInstanceConfigs {
				_, ok := profile.Config[key]
				if ok {
					errors = append(errors, fmt.Errorf("Source server has profile %q in project %q using deprecated configuration key %q", profile.Name, project.Name, key))
				}
			}

			for deviceName, device := range profile.Devices {
				for _, key := range deprecatedInstanceDeviceConfigs {
					_, ok := device[key]
					if ok {
						errors = append(errors, fmt.Errorf("Source server has device %q for profile %q in project %q using deprecated configuration key %q", deviceName, profile.Name, project.Name, key))
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
	targetPaths, err := target.paths()
	if err != nil {
		return fmt.Errorf("Failed to get target paths: %w", err)
	}

	sourcePaths, err := source.paths()
	if err != nil {
		return fmt.Errorf("Failed to get source paths: %w", err)
	}

	fi, err := os.Lstat(sourcePaths.daemon)
	if err != nil {
		return err
	}

	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("The source path %q is a symlink. Incus does not support its daemon directory being a symlink, please switch to a bind-mount.", sourcePaths.daemon)
	}

	if linux.IsMountPoint(targetPaths.daemon) {
		return fmt.Errorf("The target path %q is a mountpoint. This isn't currently supported as the target path needs to be deleted during the migration.", targetPaths.daemon)
	}

	srcFilesystem, _ := linux.DetectFilesystem(sourcePaths.daemon)
	targetFilesystem, _ := linux.DetectFilesystem(targetPaths.daemon)
	if srcFilesystem == "btrfs" && targetFilesystem != "btrfs" && !linux.IsMountPoint(sourcePaths.daemon) {
		return fmt.Errorf("Source daemon running on btrfs but being moved to non-btrfs target")
	}

	// Shiftfs check.
	if util.PathExists("/sys/module/shiftfs/") {
		fmt.Println("")
		fmt.Println("WARNING: The shiftfs kernel module was detected on your system.")
		fmt.Println("         This may indicate that your LXD installation is using shiftfs")
		fmt.Println("         to allow shifted passthrough of some disks to your instance.")
		fmt.Println("")
		fmt.Println("         Incus does not support shiftfs but instead relies on a recent")
		fmt.Println("         feature of the Linux kernel instead, VFS idmap.")
		fmt.Println("")
		fmt.Println("         If your instances actively rely on shiftfs today, you may need")
		fmt.Println("         to update to a more recent Linux kernel or ZFS version to keep")
		fmt.Println("         using this shifted passthrough features.")
		fmt.Println("")
	}

	return nil
}
