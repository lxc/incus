package main

import (
	"bufio"
	"fmt"
	"os"
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
	"github.com/lxc/incus/shared/util"
)

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

	flagYes                bool
	flagClusterMember      bool
	flagIgnoreVersionCheck bool
}

func (c *cmdMigrate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "lxd-to-incus"
	cmd.RunE = c.Run
	cmd.PersistentFlags().BoolVar(&c.flagYes, "yes", false, "Migrate without prompting")
	cmd.PersistentFlags().BoolVar(&c.flagClusterMember, "cluster-member", false, "Used internally for cluster migrations")
	cmd.PersistentFlags().BoolVar(&c.flagIgnoreVersionCheck, "ignore-version-check", false, "Bypass source version check")

	return cmd
}

func (c *cmdMigrate) Run(app *cobra.Command, args []string) error {
	var err error
	var srcClient lxd.InstanceServer
	var targetClient incus.InstanceServer

	// Confirm that we're root.
	if os.Geteuid() != 0 {
		return fmt.Errorf("This tool must be run as root")
	}

	// Create log file.
	logFile, err := os.Create(fmt.Sprintf("/var/log/lxd-to-incus.%d.log", os.Getpid()))
	if err != nil {
		return fmt.Errorf("Failed to create log file: %w", err)
	}

	defer logFile.Close()

	err = logFile.Chmod(0600)
	if err != nil {
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
		return fmt.Errorf("Failed to set permissions on log file: %w", err)
	}

	if c.flagClusterMember {
		_, _ = logFile.WriteString("Running in cluster member mode\n")
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
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
		return fmt.Errorf("No source server could be found")
	}

	fmt.Printf("==> Detected: %s\n", source.Name())
	_, _ = logFile.WriteString(fmt.Sprintf("Source server: %s\n", source.Name()))

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
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
		return fmt.Errorf("No target server could be found")
	}

	fmt.Printf("==> Detected: %s\n", target.Name())
	_, _ = logFile.WriteString(fmt.Sprintf("Target server: %s\n", target.Name()))

	// Connect to the servers.
	clustered := c.flagClusterMember
	if !c.flagClusterMember {
		fmt.Println("=> Connecting to source server")
		srcClient, err = source.Connect()
		if err != nil {
			_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
			return fmt.Errorf("Failed to connect to the source: %w", err)
		}

		srcServerInfo, _, err := srcClient.GetServer()
		if err != nil {
			_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
			return fmt.Errorf("Failed to get source server info: %w", err)
		}

		clustered = srcServerInfo.Environment.ServerClustered
	}

	if clustered {
		_, _ = logFile.WriteString("Source server is a cluster\n")
	}

	fmt.Println("=> Connecting to the target server")
	targetClient, err = target.Connect()
	if err != nil {
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
		return fmt.Errorf("Failed to connect to the target: %w", err)
	}

	// Configuration validation.
	if !c.flagClusterMember {
		err = c.validate(source, target)
		if err != nil {
			_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
			return err
		}
	}

	// Grab the path information.
	sourcePaths, err := source.Paths()
	if err != nil {
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
		return fmt.Errorf("Failed to get source paths: %w", err)
	}

	_, _ = logFile.WriteString(fmt.Sprintf("Source server paths: %+v\n", sourcePaths))

	targetPaths, err := target.Paths()
	if err != nil {
		return fmt.Errorf("Failed to get target paths: %w", err)
	}

	_, _ = logFile.WriteString(fmt.Sprintf("Target server paths: %+v\n", targetPaths))

	// Mangle storage pool sources.
	rewriteStatements := []string{}
	rewriteCommands := [][]string{}

	if !c.flagClusterMember {
		var storagePools []lxdAPI.StoragePool
		if !clustered {
			storagePools, err = srcClient.GetStoragePools()
			if err != nil {
				_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
				return fmt.Errorf("Couldn't list storage pools: %w", err)
			}
		} else {
			clusterMembers, err := srcClient.GetClusterMembers()
			if err != nil {
				_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
				return fmt.Errorf("Failed to retrieve the list of cluster members")
			}

			for _, member := range clusterMembers {
				poolNames, err := srcClient.UseTarget(member.ServerName).GetStoragePoolNames()
				if err != nil {
					_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
					return fmt.Errorf("Couldn't list storage pools: %w", err)
				}

				for _, poolName := range poolNames {
					pool, _, err := srcClient.UseTarget(member.ServerName).GetStoragePool(poolName)
					if err != nil {
						_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
						return fmt.Errorf("Couldn't get storage pool: %w", err)
					}

					storagePools = append(storagePools, *pool)
				}
			}
		}

		rbdRenamed := []string{}
		for _, pool := range storagePools {
			if pool.Driver == "ceph" {
				cluster, ok := pool.Config["ceph.cluster_name"]
				if !ok {
					cluster = "ceph"
				}

				client, ok := pool.Config["ceph.user.name"]
				if !ok {
					client = "admin"
				}

				rbdPool, ok := pool.Config["ceph.osd.pool_name"]
				if !ok {
					rbdPool = pool.Name
				}

				renameCmd := []string{"rbd", "rename", "--cluster", cluster, "--name", fmt.Sprintf("client.%s", client), fmt.Sprintf("%s/lxd_%s", rbdPool, rbdPool), fmt.Sprintf("%s/incus_%s", rbdPool, rbdPool)}
				if !util.ValueInSlice(pool.Name, rbdRenamed) {
					rewriteCommands = append(rewriteCommands, renameCmd)
					rbdRenamed = append(rbdRenamed, pool.Name)
				}
			}

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

	// Mangle OVN.
	if !c.flagClusterMember {
		srcServerInfo, _, err := srcClient.GetServer()
		if err != nil {
			_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
			return fmt.Errorf("Failed to get source server info: %w", err)
		}

		ovnNB, ok := srcServerInfo.Config["network.ovn.northbound_connection"].(string)
		if ok && ovnNB != "" {
			if !c.flagClusterMember {
				out, err := subprocess.RunCommand("ovs-vsctl", "get", "open_vswitch", ".", "external_ids:ovn-remote")
				if err != nil {
					_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
					return fmt.Errorf("Failed to get OVN southbound database address: %w", err)
				}

				ovnSB := strings.TrimSpace(strings.Replace(out, "\"", "", -1))

				commands, err := ovnConvert(ovnNB, ovnSB)
				if err != nil {
					_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
					return fmt.Errorf("Failed to prepare OVN conversion: %v", err)
				}

				rewriteCommands = append(rewriteCommands, commands...)

				err = ovnBackup(ovnNB, ovnSB, "/var/backups/")
				if err != nil {
					_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
					return fmt.Errorf("Failed to backup the OVN database: %v", err)
				}
			}
		}
	}

	// Mangle profiles and projects.
	if !c.flagClusterMember {
		rewriteStatements = append(rewriteStatements, fmt.Sprintf("UPDATE profiles SET description='Default Incus profile' WHERE description='Default LXD profile';"))
		rewriteStatements = append(rewriteStatements, fmt.Sprintf("UPDATE projects SET description='Default Incus project' WHERE description='Default LXD project';"))
	}

	// Log rewrite actions.
	_, _ = logFile.WriteString("Rewrite SQL statements:\n")
	for _, entry := range rewriteStatements {
		_, _ = logFile.WriteString(fmt.Sprintf(" - %s\n", entry))
	}

	_, _ = logFile.WriteString("Rewrite commands:\n")
	for _, entry := range rewriteCommands {
		_, _ = logFile.WriteString(fmt.Sprintf(" - %s\n", strings.Join(entry, " ")))
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
				_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
				return err
			}

			if !ok {
				_, _ = logFile.WriteString("User aborted migration\n")
				os.Exit(1)
			}
		} else {
			fmt.Println(`
The migration is now ready to proceed.

A cluster environment was detected.
Manual action will be needed on each of the server prior to Incus being functional.`)

			if os.Getenv("CLUSTER_NO_STOP") != "1" {
				fmt.Println("The migration will begin by shutting down instances on all servers.")
			}

			fmt.Println(`
It will then convert the current server over to Incus and then wait for the other servers to be converted.

Do not attempt to manually run this tool on any of the other servers in the cluster.
Instead this tool will be providing specific commands for each of the servers.
`)

			ok, err := c.global.asker.AskBool("Proceed with the migration? [default=no]: ", "no")
			if err != nil {
				_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
				return err
			}

			if !ok {
				os.Exit(1)
			}
		}
	}

	_, _ = logFile.WriteString("Migration started\n")

	// Cluster evacuation.
	if os.Getenv("CLUSTER_NO_STOP") == "1" {
		_, _ = logFile.WriteString("WARN: User requested no instance stop during migration\n")
	}

	if !c.flagClusterMember && clustered && os.Getenv("CLUSTER_NO_STOP") != "1" {
		fmt.Println("=> Stopping all workloads on the cluster")

		clusterMembers, err := srcClient.GetClusterMembers()
		if err != nil {
			_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
			return fmt.Errorf("Failed to retrieve the list of cluster members")
		}

		for _, member := range clusterMembers {
			fmt.Printf("==> Stopping all workloads on server %q\n", member.ServerName)
			_, _ = logFile.WriteString(fmt.Sprintf("Stopping instances on server %qn\n", member.ServerName))

			op, err := srcClient.UpdateClusterMemberState(member.ServerName, lxdAPI.ClusterMemberStatePost{Action: "evacuate", Mode: "stop"})
			if err != nil {
				_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
				return fmt.Errorf("Failed to stop workloads %q: %w", member.ServerName, err)
			}

			err = op.Wait()
			if err != nil {
				_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
				return fmt.Errorf("Failed to stop workloads %q: %w", member.ServerName, err)
			}
		}
	}

	// Stop source.
	fmt.Println("=> Stopping the source server")
	_, _ = logFile.WriteString("Stopping the source server\n")
	err = source.Stop()
	if err != nil {
		fmt.Errorf("Failed to stop the source server: %w", err)
	}

	// Stop target.
	fmt.Println("=> Stopping the target server")
	_, _ = logFile.WriteString("Stopping the target server\n")
	err = target.Stop()
	if err != nil {
		fmt.Errorf("Failed to stop the target server: %w", err)
	}

	// Unmount potential mount points.
	for _, mount := range []string{"guestapi", "shmounts"} {
		_, _ = logFile.WriteString(fmt.Sprintf("Unmounting %q\n", filepath.Join(targetPaths.Daemon, mount)))
		_ = unix.Unmount(filepath.Join(targetPaths.Daemon, mount), unix.MNT_DETACH)
	}

	// Wipe the target.
	fmt.Println("=> Wiping the target server")
	_, _ = logFile.WriteString("Wiping the target server\n")

	err = os.RemoveAll(targetPaths.Logs)
	if err != nil && !os.IsNotExist(err) {
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
		return fmt.Errorf("Failed to remove %q: %w", targetPaths.Logs, err)
	}

	err = os.RemoveAll(targetPaths.Cache)
	if err != nil && !os.IsNotExist(err) {
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
		return fmt.Errorf("Failed to remove %q: %w", targetPaths.Cache, err)
	}

	err = os.RemoveAll(targetPaths.Daemon)
	if err != nil && !os.IsNotExist(err) {
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
		return fmt.Errorf("Failed to remove %q: %w", targetPaths.Daemon, err)
	}

	// Migrate data.
	fmt.Println("=> Migrating the data")
	_, _ = logFile.WriteString("Migrating the data\n")

	_, err = subprocess.RunCommand("mv", sourcePaths.Logs, targetPaths.Logs)
	if err != nil {
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
		return fmt.Errorf("Failed to move %q to %q: %w", sourcePaths.Logs, targetPaths.Logs, err)
	}

	_, err = subprocess.RunCommand("mv", sourcePaths.Cache, targetPaths.Cache)
	if err != nil {
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
		return fmt.Errorf("Failed to move %q to %q: %w", sourcePaths.Cache, targetPaths.Cache, err)
	}

	if linux.IsMountPoint(sourcePaths.Daemon) {
		_, _ = logFile.WriteString("Source daemon path is a mountpoint\n")

		err = os.MkdirAll(targetPaths.Daemon, 0711)
		if err != nil {
			_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
			return fmt.Errorf("Failed to create target directory: %w", err)
		}

		_, _ = logFile.WriteString("Creating bind-mount of daemon path\n")
		err = unix.Mount(sourcePaths.Daemon, targetPaths.Daemon, "none", unix.MS_BIND|unix.MS_REC, "")
		if err != nil {
			_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
			return fmt.Errorf("Failed to bind mount %q to %q: %w", sourcePaths.Daemon, targetPaths.Daemon, err)
		}

		_, _ = logFile.WriteString("Unmounting former mountpoint\n")
		err = unix.Unmount(sourcePaths.Daemon, unix.MNT_DETACH)
		if err != nil {
			_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
			return fmt.Errorf("Failed to unmount source mount %q: %w", sourcePaths.Daemon, err)
		}

		fmt.Println("")
		fmt.Printf("WARNING: %s was detected to be a mountpoint.\n", sourcePaths.Daemon)
		fmt.Printf("The migration logic has moved this mount to the new target path at %s.\n", targetPaths.Daemon)
		fmt.Printf("However it is your responsability to modify your system settings to ensure this mount will be properly restored on reboot.\n")
		fmt.Println("")
	} else {
		_, _ = logFile.WriteString("Moving data over\n")

		_, err = subprocess.RunCommand("mv", sourcePaths.Daemon, targetPaths.Daemon)
		if err != nil {
			_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
			return fmt.Errorf("Failed to move %q to %q: %w", sourcePaths.Daemon, targetPaths.Daemon, err)
		}
	}

	// Migrate database format.
	fmt.Println("=> Migrating database")
	_, _ = logFile.WriteString("Migrating database files\n")

	_, err = subprocess.RunCommand("cp", "-R", filepath.Join(targetPaths.Daemon, "database"), filepath.Join(targetPaths.Daemon, "database.pre-migrate"))
	if err != nil {
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
		return fmt.Errorf("Failed to backup the database: %w", err)
	}

	err = migrateDatabase(filepath.Join(targetPaths.Daemon, "database"))
	if err != nil {
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
		return fmt.Errorf("Failed to migrate database in %q: %w", filepath.Join(targetPaths.Daemon, "database"), err)
	}

	// Apply custom migration statements.
	if len(rewriteStatements) > 0 {
		fmt.Println("=> Writing database patch")
		_, _ = logFile.WriteString("Writing the database patch\n")

		err = os.WriteFile(filepath.Join(targetPaths.Daemon, "database", "patch.global.sql"), []byte(strings.Join(rewriteStatements, "\n")+"\n"), 0600)
		if err != nil {
			_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
			return fmt.Errorf("Failed to write database path: %w", err)
		}
	}

	if len(rewriteCommands) > 0 {
		fmt.Println("=> Running data migration commands")
		_, _ = logFile.WriteString("Running data migration commands:\n")

		failures := 0
		for _, cmd := range rewriteCommands {
			_, _ = logFile.WriteString(fmt.Sprintf(" - %+v\n", cmd))

			_, err := subprocess.RunCommand(cmd[0], cmd[1:]...)
			if err != nil {
				_, _ = logFile.WriteString(fmt.Sprintf("Failed to run command: %v\n", err))
				failures++
			}
		}

		if failures > 0 {
			fmt.Printf("==> WARNING: %d commands out of %d succeeded (%d failures)\n", len(rewriteCommands)-failures, len(rewriteCommands), failures)
			fmt.Println("    Please review the log file for details.")
			fmt.Println("    Note that in OVN environments, it's normal to see some failures")
			fmt.Println("    related to Flow Rules and Switch Ports as those often change during the migration.")
		}
	}

	// Cleanup paths.
	fmt.Println("=> Cleaning up target paths")
	_, _ = logFile.WriteString("Cleaning up target paths\n")

	for _, dir := range []string{"backups", "images"} {
		_, _ = logFile.WriteString(fmt.Sprintf("Cleaning up path %q\n", filepath.Join(targetPaths.Daemon, dir)))

		// Remove any potential symlink (ignore errors for real directories).
		_ = os.Remove(filepath.Join(targetPaths.Daemon, dir))
	}

	for _, dir := range []string{"devices", "devlxd", "security", "shmounts"} {
		_, _ = logFile.WriteString(fmt.Sprintf("Cleaning up path %q\n", filepath.Join(targetPaths.Daemon, dir)))

		_ = unix.Unmount(filepath.Join(targetPaths.Daemon, dir), unix.MNT_DETACH)
		err = os.RemoveAll(filepath.Join(targetPaths.Daemon, dir))
		if err != nil && !os.IsNotExist(err) {
			_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
			return fmt.Errorf("Failed to delete %q: %w", dir, err)
		}
	}

	for _, dir := range []string{"containers", "containers-snapshots", "snapshots", "virtual-machines", "virtual-machines-snapshots"} {
		entries, err := os.ReadDir(filepath.Join(targetPaths.Daemon, dir))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
			return fmt.Errorf("Failed to read entries in %q: %w", filepath.Join(targetPaths.Daemon, dir), err)
		}

		_, _ = logFile.WriteString("Rewrite symlinks:\n")

		for _, entry := range entries {
			srcPath := filepath.Join(targetPaths.Daemon, dir, entry.Name())

			if entry.Type()&os.ModeSymlink != os.ModeSymlink {
				continue
			}

			oldTarget, err := os.Readlink(srcPath)
			if err != nil {
				_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
				return fmt.Errorf("Failed to resolve symlink %q: %w", srcPath, err)
			}

			newTarget := strings.Replace(oldTarget, sourcePaths.Daemon, targetPaths.Daemon, 1)
			err = os.Remove(srcPath)
			if err != nil {
				_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
				return fmt.Errorf("Failed to delete symlink %q: %w", srcPath, err)
			}

			_, _ = logFile.WriteString(fmt.Sprintf(" - %q to %q\n", newTarget, srcPath))

			err = os.Symlink(newTarget, srcPath)
			if err != nil {
				_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
				return fmt.Errorf("Failed to create symlink %q: %w", srcPath, err)
			}
		}
	}

	// Start target.
	fmt.Println("=> Starting the target server")
	_, _ = logFile.WriteString("Starting the target server\n")

	err = target.Start()
	if err != nil {
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
		return fmt.Errorf("Failed to start the target server: %w", err)
	}

	// Cluster handling.
	if clustered {
		if !c.flagClusterMember {
			_, _ = logFile.WriteString("Waiting for user to run command on other cluster members\n")

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
			_, _ = logFile.WriteString("User confirmed command was run on other members\n")
		}

		// Wait long enough that we get accurate heartbeat information.
		fmt.Println("=> Waiting for cluster to be fully migrated")
		_, _ = logFile.WriteString("Waiting for cluster to come back online\n")
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
	_, _ = logFile.WriteString("Checking target server\n")

	targetServerInfo, _, err := targetClient.GetServer()
	if err != nil {
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
		return fmt.Errorf("Failed to get target server info: %w", err)
	}

	// Fix OVS.
	ovnNB, ok := targetServerInfo.Config["network.ovn.northbound_connection"]
	if ok && ovnNB != "" {
		commands, err := ovsConvert()
		if err != nil {
			_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
			return fmt.Errorf("Failed to prepare OVS conversion: %v", err)
		}

		_, _ = logFile.WriteString("Running OVS conversion commands:\n")
		for _, cmd := range commands {
			_, _ = logFile.WriteString(fmt.Sprintf(" - %+v\n", cmd))

			_, err := subprocess.RunCommand(cmd[0], cmd[1:]...)
			if err != nil {
				_, _ = logFile.WriteString(fmt.Sprintf("Failed to run command: %v\n", err))
				fmt.Fprintf(os.Stderr, "Failed to run command: %v\n", err)
			}
		}
	}

	// Cluster restore.
	if !c.flagClusterMember && clustered {
		fmt.Println("=> Restoring the cluster")
		_, _ = logFile.WriteString("Restoring cluster state\n")

		clusterMembers, err := targetClient.GetClusterMembers()
		if err != nil {
			_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
			return fmt.Errorf("Failed to retrieve the list of cluster members")
		}

		for _, member := range clusterMembers {
			fmt.Printf("==> Restoring workloads on server %q\n", member.ServerName)
			_, _ = logFile.WriteString(fmt.Sprintf("Restoring workloads on %q\n", member.ServerName))

			op, err := targetClient.UpdateClusterMemberState(member.ServerName, incusAPI.ClusterMemberStatePost{Action: "restore"})
			if err != nil {
				_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
				return fmt.Errorf("Failed to restore %q: %w", member.ServerName, err)
			}

			err = op.Wait()
			if err != nil {
				_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
				return fmt.Errorf("Failed to restore %q: %w", member.ServerName, err)
			}
		}
	}

	// Confirm uninstall.
	if !c.flagYes {
		ok, err := c.global.asker.AskBool("Uninstall the LXD package? [default=no]: ", "no")
		if err != nil {
			_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
			return err
		}

		if !ok {
			_, _ = logFile.WriteString("User decided not ro remove the package\n")
			os.Exit(0)
		}
	}

	// Purge source.
	fmt.Println("=> Uninstalling the source server")
	_, _ = logFile.WriteString("Uninstalling the source package\n")

	err = source.Purge()
	if err != nil {
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
		return fmt.Errorf("Failed to uninstall the source server: %w", err)
	}

	return nil
}
