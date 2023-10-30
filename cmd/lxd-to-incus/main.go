package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/canonical/lxd/client"
	lxdAPI "github.com/canonical/lxd/shared/api"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/lxc/incus/client"
	cli "github.com/lxc/incus/internal/cmd"
	"github.com/lxc/incus/internal/linux"
	"github.com/lxc/incus/internal/version"
	incusAPI "github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/subprocess"
)

var minLXDVersion = &version.DottedVersion{4, 0, 0}
var maxLXDVersion = &version.DottedVersion{5, 19, 0}

type cmdGlobal struct {
	asker cli.Asker

	flagHelp    bool
	flagVersion bool
}

func main() {
	// Setup command line parser.
	migrateCmd := cmdMigrate{}

	app := migrateCmd.Command()
	app.Use = "lxd-to-incus"
	app.Short = "LXD to Incus migration tool"
	app.Long = `Description:
  LXD to Incus migration tool

  This tool allows an existing LXD user to move all their data over to Incus.
`
	app.SilenceUsage = true
	app.CompletionOptions = cobra.CompletionOptions{DisableDefaultCmd: true}

	// Global flags.
	globalCmd := cmdGlobal{asker: cli.NewAsker(bufio.NewReader(os.Stdin))}
	migrateCmd.global = globalCmd
	app.PersistentFlags().BoolVar(&globalCmd.flagVersion, "version", false, "Print version number")
	app.PersistentFlags().BoolVarP(&globalCmd.flagHelp, "help", "h", false, "Print help")

	// Version handling.
	app.SetVersionTemplate("{{.Version}}\n")
	app.Version = version.Version

	// Run the main command and handle errors.
	err := app.Execute()
	if err != nil {
		os.Exit(1)
	}
}

type cmdMigrate struct {
	global cmdGlobal

	flagYes           bool
	flagClusterMember bool
}

func (c *cmdMigrate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "lxd-to-incus"
	cmd.RunE = c.Run
	cmd.PersistentFlags().BoolVar(&c.flagYes, "yes", false, "Migrate without prompting")
	cmd.PersistentFlags().BoolVar(&c.flagClusterMember, "cluster-member", false, "Used internally for cluster migrations")

	return cmd
}

func (c *cmdMigrate) validate(srcClient lxd.InstanceServer, targetClient incus.InstanceServer) error {
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

	return nil
}

func (c *cmdMigrate) Run(app *cobra.Command, args []string) error {
	var err error
	var srcClient lxd.InstanceServer
	var targetClient incus.InstanceServer

	// Confirm that we're root.
	if os.Geteuid() != 0 {
		return fmt.Errorf("This tool must be run as root")
	}

	// Iterate through potential sources.
	fmt.Println("=> Looking for source server")
	var source Source
	for _, candidate := range sources {
		if !candidate.Present() {
			continue
		}

		source = candidate
		break
	}

	if source == nil {
		return fmt.Errorf("No source server could be found")
	}

	fmt.Printf("==> Detected: %s\n", source.Name())

	// Iterate through potential targets.
	fmt.Println("=> Looking for target server")
	var target Target
	for _, candidate := range targets {
		if !candidate.Present() {
			continue
		}

		target = candidate
		break
	}

	if target == nil {
		return fmt.Errorf("No target server could be found")
	}

	// Connect to the servers.
	clustered := c.flagClusterMember
	if !c.flagClusterMember {
		fmt.Println("=> Connecting to source server")
		srcClient, err = source.Connect()
		if err != nil {
			return fmt.Errorf("Failed to connect to the source: %w", err)
		}

		srcServerInfo, _, err := srcClient.GetServer()
		if err != nil {
			return fmt.Errorf("Failed to get source server info: %w", err)
		}

		clustered = srcServerInfo.Environment.ServerClustered
	}

	fmt.Println("=> Connecting to the target server")
	targetClient, err = target.Connect()
	if err != nil {
		return fmt.Errorf("Failed to connect to the target: %w", err)
	}

	// Configuration validation.
	if !c.flagClusterMember {
		err = c.validate(srcClient, targetClient)
		if err != nil {
			return err
		}
	}

	// Grab the path information.
	sourcePaths, err := source.Paths()
	if err != nil {
		return fmt.Errorf("Failed to get source paths: %w", err)
	}

	targetPaths, err := target.Paths()
	if err != nil {
		return fmt.Errorf("Failed to get target paths: %w", err)
	}

	// Mangle storage pool sources.
	rewriteStatements := []string{}

	if !c.flagClusterMember {
		var storagePools []lxdAPI.StoragePool
		if !clustered {
			storagePools, err = srcClient.GetStoragePools()
			if err != nil {
				return fmt.Errorf("Couldn't list storage pools: %w", err)
			}
		} else {
			clusterMembers, err := srcClient.GetClusterMembers()
			if err != nil {
				return fmt.Errorf("Failed to retrieve the list of cluster members")
			}

			for _, member := range clusterMembers {
				poolNames, err := srcClient.UseTarget(member.ServerName).GetStoragePoolNames()
				if err != nil {
					return fmt.Errorf("Couldn't list storage pools: %w", err)
				}

				for _, poolName := range poolNames {
					pool, _, err := srcClient.UseTarget(member.ServerName).GetStoragePool(poolName)
					if err != nil {
						return fmt.Errorf("Couldn't get storage pool: %w", err)
					}

					storagePools = append(storagePools, *pool)
				}
			}
		}

		for _, pool := range storagePools {
			source := pool.Config["source"]
			if source == "" || source[0] != byte('/') {
				continue
			}

			if !strings.HasPrefix(source, sourcePaths.Daemon) {
				continue
			}

			newSource := strings.Replace(source, sourcePaths.Daemon, targetPaths.Daemon, 1)
			rewriteStatements = append(rewriteStatements, fmt.Sprintf("UPDATE storage_pools_config SET value='%s' WHERE value='%s';", newSource, source))
		}
	}

	// Confirm migration.
	if !c.flagClusterMember && !c.flagYes {
		if !clustered {
			fmt.Println(`
The migration is now ready to proceed.
At this point, the source server and all its instances will be stopped.
Instances will come back online once the migration is complete.
`)

			ok, err := c.global.asker.AskBool("Proceed with the migration? [default=no]: ", "no")
			if err != nil {
				return err
			}

			if !ok {
				os.Exit(1)
			}
		} else {
			fmt.Println(`
The migration is now ready to proceed.

A cluster environment was detected.
Manual action will be needed on each of the server prior to Incus being functional.
The migration will begin by shutting down instances on all servers.
It will then convert the current server over to Incus and then wait for the other servers to be converted.

Do not attempt to manually run this tool on any of the other servers in the cluster.
Instead this tool will be providing specific commands for each of the servers.
`)

			ok, err := c.global.asker.AskBool("Proceed with the migration? [default=no]: ", "no")
			if err != nil {
				return err
			}

			if !ok {
				os.Exit(1)
			}
		}
	}

	// Cluster evacuation.
	if !c.flagClusterMember && clustered {
		fmt.Println("=> Stopping all workloads on the cluster")

		clusterMembers, err := srcClient.GetClusterMembers()
		if err != nil {
			return fmt.Errorf("Failed to retrieve the list of cluster members")
		}

		for _, member := range clusterMembers {
			fmt.Printf("==> Stopping all workloads on server %q\n", member.ServerName)

			op, err := srcClient.UpdateClusterMemberState(member.ServerName, lxdAPI.ClusterMemberStatePost{Action: "evacuate", Mode: "stop"})
			if err != nil {
				return fmt.Errorf("Failed to stop workloads %q: %w", member.ServerName, err)
			}

			err = op.Wait()
			if err != nil {
				return fmt.Errorf("Failed to stop workloads %q: %w", member.ServerName, err)
			}
		}
	}

	// Stop source.
	fmt.Println("=> Stopping the source server")
	err = source.Stop()
	if err != nil {
		fmt.Errorf("Failed to stop the source server: %w", err)
	}

	// Stop target.
	fmt.Println("=> Stopping the target server")
	err = target.Stop()
	if err != nil {
		fmt.Errorf("Failed to stop the target server: %w", err)
	}

	// Unmount potential mount points.
	for _, mount := range []string{"guestapi", "shmounts"} {
		_ = unix.Unmount(filepath.Join(targetPaths.Daemon, mount), unix.MNT_DETACH)
	}

	// Wipe the target.
	fmt.Println("=> Wiping the target server")

	err = os.RemoveAll(targetPaths.Logs)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Failed to remove %q: %w", targetPaths.Logs, err)
	}

	err = os.RemoveAll(targetPaths.Cache)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Failed to remove %q: %w", targetPaths.Cache, err)
	}

	err = os.RemoveAll(targetPaths.Daemon)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Failed to remove %q: %w", targetPaths.Daemon, err)
	}

	// Migrate data.
	fmt.Println("=> Migrating the data")

	_, err = subprocess.RunCommand("mv", sourcePaths.Logs, targetPaths.Logs)
	if err != nil {
		return fmt.Errorf("Failed to move %q to %q: %w", sourcePaths.Logs, targetPaths.Logs, err)
	}

	_, err = subprocess.RunCommand("mv", sourcePaths.Cache, targetPaths.Cache)
	if err != nil {
		return fmt.Errorf("Failed to move %q to %q: %w", sourcePaths.Cache, targetPaths.Cache, err)
	}

	if linux.IsMountPoint(sourcePaths.Daemon) {
		err = os.MkdirAll(targetPaths.Daemon, 0711)
		if err != nil {
			return fmt.Errorf("Failed to create target directory: %w", err)
		}

		err = unix.Mount(sourcePaths.Daemon, targetPaths.Daemon, "none", unix.MS_BIND|unix.MS_REC, "")
		if err != nil {
			return fmt.Errorf("Failed to bind mount %q to %q: %w", sourcePaths.Daemon, targetPaths.Daemon, err)
		}

		err = unix.Unmount(sourcePaths.Daemon, unix.MNT_DETACH)
		if err != nil {
			return fmt.Errorf("Failed to unmount source mount %q: %w", sourcePaths.Daemon, err)
		}

		fmt.Println("")
		fmt.Printf("WARNING: %s was detected to be a mountpoint.\n", sourcePaths.Daemon)
		fmt.Printf("The migration logic has moved this mount to the new target path at %s.\n", targetPaths.Daemon)
		fmt.Printf("However it is your responsability to modify your system settings to ensure this mount will be properly restored on reboot.\n")
		fmt.Println("")
	} else {
		_, err = subprocess.RunCommand("mv", sourcePaths.Daemon, targetPaths.Daemon)
		if err != nil {
			return fmt.Errorf("Failed to move %q to %q: %w", sourcePaths.Daemon, targetPaths.Daemon, err)
		}
	}

	// Migrate database format.
	fmt.Println("=> Migrating database")
	err = migrateDatabase(filepath.Join(targetPaths.Daemon, "database"))
	if err != nil {
		return fmt.Errorf("Failed to migrate database in %q: %w", filepath.Join(targetPaths.Daemon, "database"), err)
	}

	// Apply custom SQL statements.
	if !c.flagClusterMember {
		err = os.WriteFile(filepath.Join(targetPaths.Daemon, "database", "patch.global.sql"), []byte(strings.Join(rewriteStatements, "\n")+"\n"), 0600)
		if err != nil {
			return fmt.Errorf("Failed to write database path: %w", err)
		}
	}

	// Cleanup paths.
	fmt.Println("=> Cleaning up target paths")

	for _, dir := range []string{"backups", "images"} {
		// Remove any potential symlink (ignore errors for real directories).
		_ = os.Remove(filepath.Join(targetPaths.Daemon, dir))
	}

	for _, dir := range []string{"devices", "devlxd", "security", "shmounts"} {
		err = os.RemoveAll(filepath.Join(targetPaths.Daemon, dir))
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("Failed to delete %q: %w", dir, err)
		}
	}

	for _, dir := range []string{"containers", "containers-snapshots", "snapshots", "virtual-machines", "virtual-machines-snapshots"} {
		entries, err := os.ReadDir(filepath.Join(targetPaths.Daemon, dir))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			return fmt.Errorf("Failed to read entries in %q: %w", filepath.Join(targetPaths.Daemon, dir), err)
		}

		for _, entry := range entries {
			srcPath := filepath.Join(targetPaths.Daemon, dir, entry.Name())
			oldTarget, err := os.Readlink(srcPath)
			if err != nil {
				return fmt.Errorf("Failed to resolve symlink %q: %w", srcPath, err)
			}

			newTarget := strings.Replace(oldTarget, sourcePaths.Daemon, targetPaths.Daemon, 1)
			err = os.Remove(srcPath)
			if err != nil {
				return fmt.Errorf("Failed to delete symlink %q: %w", srcPath, err)
			}

			err = os.Symlink(newTarget, srcPath)
			if err != nil {
				return fmt.Errorf("Failed to create symlink %q: %w", srcPath, err)
			}
		}
	}

	// Start target.
	fmt.Println("=> Starting the target server")
	err = target.Start()
	if err != nil {
		return fmt.Errorf("Failed to start the target server: %w", err)
	}

	// Cluster handling.
	if clustered {
		if !c.flagClusterMember {
			fmt.Println("=> Waiting for other cluster servers\n")
			fmt.Printf("Please run `lxd-to-incus --cluster-member` on all other servers in the cluster\n\n")
			for {
				ok, err := c.global.asker.AskBool("The command has been started on all other servers? [default=no]: ", "no")
				if !ok || err != nil {
					continue
				}

				break
			}

			fmt.Println("")
		}

		// Wait long enough that we get accurate heartbeat information.
		fmt.Println("=> Waiting for cluster to be fully migrated")
		time.Sleep(30 * time.Second)

		for {
			clusterMembers, err := targetClient.GetClusterMembers()
			if err != nil {
				time.Sleep(30 * time.Second)
				continue
			}

			ready := true
			for _, member := range clusterMembers {
				info, _, err := targetClient.UseTarget(member.ServerName).GetServer()
				if err != nil || info.Environment.Server != "incus" {
					ready = false
					break
				}

				if member.Status == "Evacuated" && member.Message == "Unavailable due to maintenance" {
					continue
				}

				if member.Status == "Online" && member.Message == "Fully operational" {
					continue
				}

				ready = false
				break
			}

			if !ready {
				time.Sleep(30 * time.Second)
				continue
			}

			break
		}
	}

	// Validate target.
	fmt.Println("=> Checking the target server")
	_, _, err = targetClient.GetServer()
	if err != nil {
		return fmt.Errorf("Failed to get target server info: %w", err)
	}

	// Cluster restore.
	if !c.flagClusterMember && clustered {
		fmt.Println("=> Restoring the cluster")

		clusterMembers, err := targetClient.GetClusterMembers()
		if err != nil {
			return fmt.Errorf("Failed to retrieve the list of cluster members")
		}

		for _, member := range clusterMembers {
			fmt.Printf("==> Restoring workloads on server %q\n", member.ServerName)

			op, err := targetClient.UpdateClusterMemberState(member.ServerName, incusAPI.ClusterMemberStatePost{Action: "restore"})
			if err != nil {
				return fmt.Errorf("Failed to restore %q: %w", member.ServerName, err)
			}

			err = op.Wait()
			if err != nil {
				return fmt.Errorf("Failed to restore %q: %w", member.ServerName, err)
			}
		}
	}

	// Confirm uninstall.
	if !c.flagYes {
		ok, err := c.global.asker.AskBool("Uninstall the LXD package? [default=no]: ", "no")
		if err != nil {
			return err
		}

		if !ok {
			os.Exit(1)
		}
	}

	// Purge source.
	fmt.Println("=> Uninstalling the source server")
	err = source.Purge()
	if err != nil {
		return fmt.Errorf("Failed to uninstall the source server: %w", err)
	}

	return nil
}
