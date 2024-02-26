package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/lxc/incus/client"
	cli "github.com/lxc/incus/internal/cmd"
	"github.com/lxc/incus/internal/linux"
	"github.com/lxc/incus/internal/version"
	"github.com/lxc/incus/shared/api"
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

// Command generates the command definition.
func (c *cmdMigrate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "lxd-to-incus"
	cmd.RunE = c.Run
	cmd.PersistentFlags().BoolVar(&c.flagYes, "yes", false, "Migrate without prompting")
	cmd.PersistentFlags().BoolVar(&c.flagClusterMember, "cluster-member", false, "Used internally for cluster migrations")
	cmd.PersistentFlags().BoolVar(&c.flagIgnoreVersionCheck, "ignore-version-check", false, "Bypass source version check")

	return cmd
}

// Run runs the actual command logic.
func (c *cmdMigrate) Run(app *cobra.Command, args []string) error {
	var err error
	var srcClient incus.InstanceServer
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
	var source source
	for _, candidate := range sources {
		if !candidate.present() {
			continue
		}

		source = candidate
		break
	}

	if source == nil {
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
		return fmt.Errorf("No source server could be found")
	}

	fmt.Printf("==> Detected: %s\n", source.name())
	_, _ = logFile.WriteString(fmt.Sprintf("Source server: %s\n", source.name()))

	// Iterate through potential targets.
	fmt.Println("=> Looking for target server")
	var target target
	for _, candidate := range targets {
		if !candidate.present() {
			continue
		}

		target = candidate
		break
	}

	if target == nil {
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
		return fmt.Errorf("No target server could be found")
	}

	fmt.Printf("==> Detected: %s\n", target.name())
	_, _ = logFile.WriteString(fmt.Sprintf("Target server: %s\n", target.name()))

	// Connect to the servers.
	clustered := c.flagClusterMember
	if !c.flagClusterMember {
		fmt.Println("=> Connecting to source server")
		srcClient, err = source.connect()
		if err != nil {
			_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
			return fmt.Errorf("Failed to connect to the source: %w", err)
		}

		// Look for API incompatibility (bool in /1.0 config).
		resp, _, err := srcClient.RawQuery("GET", "/1.0", nil, "")
		if err != nil {
			_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
			return fmt.Errorf("Failed to get source server info: %w", err)
		}

		type lxdServer struct {
			Config map[string]any `json:"config"`
		}

		s := lxdServer{}

		err = json.Unmarshal(resp.Metadata, &s)
		if err != nil {
			_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
			return fmt.Errorf("Failed to parse source server config: %w", err)
		}

		badEntries := []string{}
		for k, v := range s.Config {
			_, ok := v.(string)
			if !ok {
				badEntries = append(badEntries, k)
			}
		}

		if len(badEntries) > 0 {
			fmt.Println("")
			fmt.Println("The source server (LXD) has the following configuration keys that are incompatible with Incus:")

			for _, k := range badEntries {
				fmt.Printf(" - %s\n", k)
			}

			fmt.Println("")
			fmt.Println("The present migration tool cannot properly connect to the LXD server with those configuration keys present.")
			fmt.Println("Please unset those configuration keys through the `lxc config unset` command and retry `lxd-to-incus`.")
			fmt.Println("")

			_, _ = logFile.WriteString(fmt.Sprintf("ERROR: Bad config keys: %v\n", badEntries))
			return fmt.Errorf("Unable to interact with the source server")
		}

		// Get the source server info.
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
	targetClient, err = target.connect()
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
	sourcePaths, err := source.paths()
	if err != nil {
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
		return fmt.Errorf("Failed to get source paths: %w", err)
	}

	_, _ = logFile.WriteString(fmt.Sprintf("Source server paths: %+v\n", sourcePaths))

	targetPaths, err := target.paths()
	if err != nil {
		return fmt.Errorf("Failed to get target paths: %w", err)
	}

	_, _ = logFile.WriteString(fmt.Sprintf("Target server paths: %+v\n", targetPaths))

	// Mangle storage pool sources.
	rewriteStatements := []string{}
	rewriteCommands := [][]string{}

	if !c.flagClusterMember {
		var storagePools []api.StoragePool
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
				cephCluster, ok := pool.Config["ceph.cluster_name"]
				if !ok {
					cephCluster = "ceph"
				}

				cephUser, ok := pool.Config["ceph.user.name"]
				if !ok {
					cephUser = "admin"
				}

				cephPool, ok := pool.Config["ceph.osd.pool_name"]
				if !ok {
					cephPool = pool.Name
				}

				renameCmd := []string{"rbd", "rename", "--cluster", cephCluster, "--name", fmt.Sprintf("client.%s", cephUser), fmt.Sprintf("%s/lxd_%s", cephPool, cephPool), fmt.Sprintf("%s/incus_%s", cephPool, cephPool)}
				if !slices.Contains(rbdRenamed, pool.Name) {
					rewriteCommands = append(rewriteCommands, renameCmd)
					rbdRenamed = append(rbdRenamed, pool.Name)
				}
			}

			source := pool.Config["source"]
			if source == "" || source[0] != byte('/') {
				continue
			}

			if !strings.HasPrefix(source, sourcePaths.daemon) {
				continue
			}

			newSource := strings.Replace(source, sourcePaths.daemon, targetPaths.daemon, 1)
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

		ovnNB, ok := srcServerInfo.Config["network.ovn.northbound_connection"]
		if !ok && util.PathExists("/run/ovn/ovnnb_db.sock") {
			ovnNB = "unix:/run/ovn/ovnnb_db.sock"
		}

		if ovnNB != "" {
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
		rewriteStatements = append(rewriteStatements, "UPDATE profiles SET description='Default Incus profile' WHERE description='Default LXD profile';")
		rewriteStatements = append(rewriteStatements, "UPDATE projects SET description='Default Incus project' WHERE description='Default LXD project';")
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
Instances will come back online once the migration is complete.`)

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
Instead this tool will be providing specific commands for each of the servers.`)

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

			op, err := srcClient.UpdateClusterMemberState(member.ServerName, api.ClusterMemberStatePost{Action: "evacuate", Mode: "stop"})
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
	err = source.stop()
	if err != nil {
		return fmt.Errorf("Failed to stop the source server: %w", err)
	}

	// Stop target.
	fmt.Println("=> Stopping the target server")
	_, _ = logFile.WriteString("Stopping the target server\n")
	err = target.stop()
	if err != nil {
		return fmt.Errorf("Failed to stop the target server: %w", err)
	}

	// Unmount potential mount points.
	for _, mount := range []string{"devlxd", "shmounts"} {
		_, _ = logFile.WriteString(fmt.Sprintf("Unmounting %q\n", filepath.Join(targetPaths.daemon, mount)))
		_ = unix.Unmount(filepath.Join(targetPaths.daemon, mount), unix.MNT_DETACH)
	}

	// Wipe the target.
	fmt.Println("=> Wiping the target server")
	_, _ = logFile.WriteString("Wiping the target server\n")

	err = os.RemoveAll(targetPaths.logs)
	if err != nil && !os.IsNotExist(err) {
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
		return fmt.Errorf("Failed to remove %q: %w", targetPaths.logs, err)
	}

	err = os.RemoveAll(targetPaths.cache)
	if err != nil && !os.IsNotExist(err) {
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
		return fmt.Errorf("Failed to remove %q: %w", targetPaths.cache, err)
	}

	err = os.RemoveAll(targetPaths.daemon)
	if err != nil && !os.IsNotExist(err) {
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
		return fmt.Errorf("Failed to remove %q: %w", targetPaths.daemon, err)
	}

	// Migrate data.
	fmt.Println("=> Migrating the data")
	_, _ = logFile.WriteString("Migrating the data\n")

	_, err = subprocess.RunCommand("mv", sourcePaths.logs, targetPaths.logs)
	if err != nil {
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
		return fmt.Errorf("Failed to move %q to %q: %w", sourcePaths.logs, targetPaths.logs, err)
	}

	_, err = subprocess.RunCommand("mv", sourcePaths.cache, targetPaths.cache)
	if err != nil {
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
		return fmt.Errorf("Failed to move %q to %q: %w", sourcePaths.cache, targetPaths.cache, err)
	}

	if linux.IsMountPoint(sourcePaths.daemon) {
		_, _ = logFile.WriteString("Source daemon path is a mountpoint\n")

		err = os.MkdirAll(targetPaths.daemon, 0711)
		if err != nil {
			_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
			return fmt.Errorf("Failed to create target directory: %w", err)
		}

		_, _ = logFile.WriteString("Creating bind-mount of daemon path\n")
		err = unix.Mount(sourcePaths.daemon, targetPaths.daemon, "none", unix.MS_BIND|unix.MS_REC, "")
		if err != nil {
			_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
			return fmt.Errorf("Failed to bind mount %q to %q: %w", sourcePaths.daemon, targetPaths.daemon, err)
		}

		_, _ = logFile.WriteString("Unmounting former mountpoint\n")
		err = unix.Unmount(sourcePaths.daemon, unix.MNT_DETACH)
		if err != nil {
			_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
			return fmt.Errorf("Failed to unmount source mount %q: %w", sourcePaths.daemon, err)
		}

		fmt.Println("")
		fmt.Printf("WARNING: %s was detected to be a mountpoint.\n", sourcePaths.daemon)
		fmt.Printf("The migration logic has moved this mount to the new target path at %s.\n", targetPaths.daemon)
		fmt.Printf("However it is your responsibility to modify your system settings to ensure this mount will be properly restored on reboot.\n")
		fmt.Println("")
	} else {
		_, _ = logFile.WriteString("Moving data over\n")

		_, err = subprocess.RunCommand("mv", sourcePaths.daemon, targetPaths.daemon)
		if err != nil {
			_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
			return fmt.Errorf("Failed to move %q to %q: %w", sourcePaths.daemon, targetPaths.daemon, err)
		}
	}

	// Migrate database format.
	fmt.Println("=> Migrating database")
	_, _ = logFile.WriteString("Migrating database files\n")

	_, err = subprocess.RunCommand("cp", "-R", filepath.Join(targetPaths.daemon, "database"), filepath.Join(targetPaths.daemon, "database.pre-migrate"))
	if err != nil {
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
		return fmt.Errorf("Failed to backup the database: %w", err)
	}

	err = migrateDatabase(filepath.Join(targetPaths.daemon, "database"))
	if err != nil {
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
		return fmt.Errorf("Failed to migrate database in %q: %w", filepath.Join(targetPaths.daemon, "database"), err)
	}

	// Apply custom migration statements.
	if len(rewriteStatements) > 0 {
		fmt.Println("=> Writing database patch")
		_, _ = logFile.WriteString("Writing the database patch\n")

		err = os.WriteFile(filepath.Join(targetPaths.daemon, "database", "patch.global.sql"), []byte(strings.Join(rewriteStatements, "\n")+"\n"), 0600)
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
		_, _ = logFile.WriteString(fmt.Sprintf("Cleaning up path %q\n", filepath.Join(targetPaths.daemon, dir)))

		// Remove any potential symlink (ignore errors for real directories).
		_ = os.Remove(filepath.Join(targetPaths.daemon, dir))
	}

	for _, dir := range []string{"devices", "devlxd", "security", "shmounts"} {
		_, _ = logFile.WriteString(fmt.Sprintf("Cleaning up path %q\n", filepath.Join(targetPaths.daemon, dir)))

		_ = unix.Unmount(filepath.Join(targetPaths.daemon, dir), unix.MNT_DETACH)
		err = os.RemoveAll(filepath.Join(targetPaths.daemon, dir))
		if err != nil && !os.IsNotExist(err) {
			_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
			return fmt.Errorf("Failed to delete %q: %w", dir, err)
		}
	}

	for _, dir := range []string{"containers", "containers-snapshots", "snapshots", "virtual-machines", "virtual-machines-snapshots"} {
		entries, err := os.ReadDir(filepath.Join(targetPaths.daemon, dir))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
			return fmt.Errorf("Failed to read entries in %q: %w", filepath.Join(targetPaths.daemon, dir), err)
		}

		_, _ = logFile.WriteString("Rewrite symlinks:\n")

		for _, entry := range entries {
			srcPath := filepath.Join(targetPaths.daemon, dir, entry.Name())

			if entry.Type()&os.ModeSymlink != os.ModeSymlink {
				continue
			}

			oldTarget, err := os.Readlink(srcPath)
			if err != nil {
				_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
				return fmt.Errorf("Failed to resolve symlink %q: %w", srcPath, err)
			}

			newTarget := strings.Replace(oldTarget, sourcePaths.daemon, targetPaths.daemon, 1)
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

	// Cleanup the cache.
	cacheEntries, err := os.ReadDir(targetPaths.cache)
	if err == nil {
		for _, entry := range cacheEntries {
			if !entry.IsDir() {
				continue
			}

			err := os.RemoveAll(filepath.Join(targetPaths.cache, entry.Name()))
			if err != nil {
				_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
				return fmt.Errorf("Failed to clear cache file %q: %w", filepath.Join(targetPaths.cache, entry.Name()), err)
			}
		}
	}

	// Start target.
	fmt.Println("=> Starting the target server")
	_, _ = logFile.WriteString("Starting the target server\n")

	err = target.start()
	if err != nil {
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
		return fmt.Errorf("Failed to start the target server: %w", err)
	}

	// Cluster handling.
	if clustered {
		if !c.flagClusterMember {
			_, _ = logFile.WriteString("Waiting for user to run command on other cluster members\n")

			fmt.Println("=> Waiting for other cluster servers")
			fmt.Println("")
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

			op, err := targetClient.UpdateClusterMemberState(member.ServerName, api.ClusterMemberStatePost{Action: "restore"})
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

	// Writing completion stamp file.
	completeFile, err := os.Create(filepath.Join(targetPaths.daemon, ".migrated-from-lxd"))
	if err != nil {
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
	}

	defer completeFile.Close()

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

	err = source.purge()
	if err != nil {
		_, _ = logFile.WriteString(fmt.Sprintf("ERROR: %v\n", err))
		return fmt.Errorf("Failed to uninstall the source server: %w", err)
	}

	return nil
}
