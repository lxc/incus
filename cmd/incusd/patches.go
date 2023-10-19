package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lxc/incus/internal/revert"
	"github.com/lxc/incus/internal/server/backup"
	"github.com/lxc/incus/internal/server/certificate"
	"github.com/lxc/incus/internal/server/cluster"
	"github.com/lxc/incus/internal/server/db"
	dbCluster "github.com/lxc/incus/internal/server/db/cluster"
	"github.com/lxc/incus/internal/server/db/query"
	"github.com/lxc/incus/internal/server/instance/instancetype"
	"github.com/lxc/incus/internal/server/network"
	"github.com/lxc/incus/internal/server/node"
	"github.com/lxc/incus/internal/server/project"
	storagePools "github.com/lxc/incus/internal/server/storage"
	storageDrivers "github.com/lxc/incus/internal/server/storage/drivers"
	internalUtil "github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/subprocess"
	"github.com/lxc/incus/shared/util"
)

type patchStage int

// Define the stages that patches can run at.
const (
	patchNoStageSet patchStage = iota
	patchPreDaemonStorage
	patchPostDaemonStorage
	patchPostNetworks
)

/*
Patches are one-time actions that are sometimes needed to update

	existing container configuration or move things around on the
	filesystem.

	Those patches are applied at startup time after the database schema
	has been fully updated. Patches can therefore assume a working database.

	At the time the patches are applied, the containers aren't started
	yet and the daemon isn't listening to requests.

	DO NOT use this mechanism for database update. Schema updates must be
	done through the separate schema update mechanism.


	Only append to the patches list, never remove entries and never re-order them.
*/
var patches = []patch{
	{name: "storage_lvm_skipactivation", stage: patchPostDaemonStorage, run: patchGenericStorage},
	{name: "clustering_drop_database_role", stage: patchPostDaemonStorage, run: patchClusteringDropDatabaseRole},
	{name: "network_clear_bridge_volatile_hwaddr", stage: patchPostDaemonStorage, run: patchGenericNetwork(patchNetworkClearBridgeVolatileHwaddr)},
	{name: "move_backups_instances", stage: patchPostDaemonStorage, run: patchMoveBackupsInstances},
	{name: "network_ovn_enable_nat", stage: patchPostDaemonStorage, run: patchGenericNetwork(patchNetworkOVNEnableNAT)},
	{name: "network_ovn_remove_routes", stage: patchPostDaemonStorage, run: patchGenericNetwork(patchNetworkOVNRemoveRoutes)},
	{name: "thinpool_typo_fix", stage: patchPostDaemonStorage, run: patchThinpoolTypoFix},
	{name: "vm_rename_uuid_key", stage: patchPostDaemonStorage, run: patchVMRenameUUIDKey},
	{name: "db_nodes_autoinc", stage: patchPreDaemonStorage, run: patchDBNodesAutoInc},
	{name: "network_acl_remove_defaults", stage: patchPostDaemonStorage, run: patchGenericNetwork(patchNetworkACLRemoveDefaults)},
	{name: "clustering_server_cert_trust", stage: patchPreDaemonStorage, run: patchClusteringServerCertTrust},
	{name: "warnings_remove_empty_node", stage: patchPostDaemonStorage, run: patchRemoveWarningsWithEmptyNode},
	{name: "dnsmasq_entries_include_device_name", stage: patchPostDaemonStorage, run: patchDnsmasqEntriesIncludeDeviceName},
	{name: "storage_missing_snapshot_records", stage: patchPostDaemonStorage, run: patchGenericStorage},
	{name: "storage_delete_old_snapshot_records", stage: patchPostDaemonStorage, run: patchGenericStorage},
	{name: "storage_zfs_drop_block_volume_filesystem_extension", stage: patchPostDaemonStorage, run: patchGenericStorage},
	{name: "storage_prefix_bucket_names_with_project", stage: patchPostDaemonStorage, run: patchGenericStorage},
	{name: "storage_move_custom_iso_block_volumes", stage: patchPostDaemonStorage, run: patchStorageRenameCustomISOBlockVolumes},
	{name: "zfs_set_content_type_user_property", stage: patchPostDaemonStorage, run: patchZfsSetContentTypeUserProperty},
	{name: "snapshots_rename", stage: patchPreDaemonStorage, run: patchSnapshotsRename},
	{name: "storage_zfs_unset_invalid_block_settings", stage: patchPostDaemonStorage, run: patchStorageZfsUnsetInvalidBlockSettings},
	{name: "storage_zfs_unset_invalid_block_settings_v2", stage: patchPostDaemonStorage, run: patchStorageZfsUnsetInvalidBlockSettingsV2},
}

type patch struct {
	name  string
	stage patchStage
	run   func(name string, d *Daemon) error
}

func (p *patch) apply(d *Daemon) error {
	logger.Info("Applying patch", logger.Ctx{"name": p.name})

	err := p.run(p.name, d)
	if err != nil {
		return fmt.Errorf("Failed applying patch %q: %w", p.name, err)
	}

	err = d.db.Node.MarkPatchAsApplied(p.name)
	if err != nil {
		return fmt.Errorf("Failed marking patch applied %q: %w", p.name, err)
	}

	return nil
}

// Return the names of all available patches.
func patchesGetNames() []string {
	names := make([]string, len(patches))
	for i, patch := range patches {
		if patch.stage == patchNoStageSet {
			continue // Ignore any patch without explicitly set stage (it is defined incorrectly).
		}

		names[i] = patch.name
	}

	return names
}

// patchesApplyPostDaemonStorage applies the patches that need to run after the daemon storage is initialised.
func patchesApply(d *Daemon, stage patchStage) error {
	appliedPatches, err := d.db.Node.GetAppliedPatches()
	if err != nil {
		return err
	}

	for _, patch := range patches {
		if patch.stage == patchNoStageSet {
			return fmt.Errorf("Patch %q has no stage set: %d", patch.name, patch.stage)
		}

		if util.ValueInSlice(patch.name, appliedPatches) {
			continue
		}

		err := patch.apply(d)
		if err != nil {
			return err
		}
	}

	return nil
}

// Patches begin here

func patchDnsmasqEntriesIncludeDeviceName(name string, d *Daemon) error {
	err := network.UpdateDNSMasqStatic(d.State(), "")
	if err != nil {
		return err
	}

	return nil
}

func patchRemoveWarningsWithEmptyNode(name string, d *Daemon) error {
	err := d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		warnings, err := dbCluster.GetWarnings(ctx, tx.Tx())
		if err != nil {
			return err
		}

		for _, w := range warnings {
			if w.Node == "" {
				err = dbCluster.DeleteWarning(ctx, tx.Tx(), w.UUID)
				if err != nil {
					return err
				}
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

func patchClusteringServerCertTrust(name string, d *Daemon) error {
	clustered, err := cluster.Enabled(d.db.Node)
	if err != nil {
		return err
	}

	if !clustered {
		return nil
	}

	var serverName string
	err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		serverName, err = tx.GetLocalNodeName(ctx)
		return err
	})
	if err != nil {
		return err
	}

	// Add our server cert to DB trust store.
	serverCert, err := internalUtil.LoadServerCert(d.os.VarDir)
	if err != nil {
		return err
	}
	// Update our own entry in the nodes table.
	logger.Infof("Adding local server certificate to global trust store for %q patch", name)
	err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return cluster.EnsureServerCertificateTrusted(serverName, serverCert, tx)
	})
	if err != nil {
		return err
	}

	logger.Infof("Added local server certificate to global trust store for %q patch", name)

	// Check all other members have done the same.
	for {
		var err error
		var dbCerts []dbCluster.Certificate
		err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			dbCerts, err = dbCluster.GetCertificates(ctx, tx.Tx())
			return err
		})
		if err != nil {
			return err
		}

		trustedServerCerts := make(map[string]*dbCluster.Certificate)

		for _, c := range dbCerts {
			if c.Type == certificate.TypeServer {
				trustedServerCerts[c.Name] = &c
			}
		}

		var members []db.NodeInfo
		err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			members, err = tx.GetNodes(ctx)
			if err != nil {
				return fmt.Errorf("Failed getting cluster members: %w", err)
			}

			return nil
		})
		if err != nil {
			return err
		}

		missingCerts := false
		for _, member := range members {
			_, found := trustedServerCerts[member.Name]
			if !found {
				logger.Warnf("Missing trusted server certificate for cluster member %q", member.Name)
				missingCerts = true
				break
			}
		}

		if missingCerts {
			logger.Warnf("Waiting for %q patch to be applied on all cluster members", name)
			time.Sleep(time.Second)
			continue
		}

		logger.Infof("Trusted server certificates found in trust store for all cluster members")
		break
	}

	// Now switch to using our server certificate for intra-cluster communication and load the trusted server
	// certificates for the other members into the in-memory trusted cache.
	logger.Infof("Set client certificate to server certificate %v", serverCert.Fingerprint())
	d.serverCertInt = serverCert
	updateCertificateCache(d)

	return nil
}

// patchNetworkACLRemoveDefaults removes the "default.action" and "default.logged" settings from network ACLs.
// It was decided that the user experience of having the default actions at the ACL level was confusing when using
// multiple ACLs, and that the interplay between conflicting default actions on multiple ACLs was difficult to
// understand. Instead it will be replace with a network and NIC level defaults settings.
func patchNetworkACLRemoveDefaults(name string, d *Daemon) error {
	var err error
	var projectNames []string

	// Get projects.
	err = d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		projectNames, err = dbCluster.GetProjectNames(ctx, tx.Tx())
		return err
	})
	if err != nil {
		return err
	}

	// Get ACLs in projects.
	for _, projectName := range projectNames {
		aclNames, err := d.db.Cluster.GetNetworkACLs(projectName)
		if err != nil {
			return err
		}

		for _, aclName := range aclNames {
			aclID, acl, err := d.db.Cluster.GetNetworkACL(projectName, aclName)
			if err != nil {
				return err
			}

			modified := false

			// Remove the offending keys if found.
			_, found := acl.Config["default.action"]
			if found {
				delete(acl.Config, "default.action")
				modified = true
			}

			_, found = acl.Config["default.logged"]
			if found {
				delete(acl.Config, "default.logged")
				modified = true
			}

			// Write back modified config if needed.
			if modified {
				err = d.db.Cluster.UpdateNetworkACL(aclID, &acl.NetworkACLPut)
				if err != nil {
					return fmt.Errorf("Failed updating network ACL %d: %w", aclID, err)
				}
			}
		}
	}

	return nil
}

// patchDBNodesAutoInc re-creates the nodes table id column as AUTOINCREMENT.
// Its done as a patch rather than a schema update so we can use PRAGMA foreign_keys = OFF without a transaction.
func patchDBNodesAutoInc(name string, d *Daemon) error {
	for {
		// Only apply patch if schema needs it.
		var schemaSQL string
		row := d.State().DB.Cluster.DB().QueryRow("SELECT sql FROM sqlite_master WHERE name = 'nodes'")
		err := row.Scan(&schemaSQL)
		if err != nil {
			return err
		}

		if strings.Contains(schemaSQL, "id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL") {
			logger.Debugf(`Skipping %q patch as "nodes" table id column already AUTOINCREMENT`, name)
			return nil // Nothing to do.
		}

		// Only apply patch on leader, otherwise wait for it to be applied.
		var localConfig *node.Config
		err = d.db.Node.Transaction(context.TODO(), func(ctx context.Context, tx *db.NodeTx) error {
			localConfig, err = node.ConfigLoad(ctx, tx)
			return err
		})
		if err != nil {
			return err
		}

		leaderAddress, err := d.gateway.LeaderAddress()
		if err != nil {
			if errors.Is(err, cluster.ErrNodeIsNotClustered) {
				break // Apply change on standalone node.
			}

			return err
		}

		if localConfig.ClusterAddress() == leaderAddress {
			break // Apply change on leader node.
		}

		logger.Warnf("Waiting for %q patch to be applied on leader cluster member", name)
		time.Sleep(time.Second)
	}

	// Apply patch.
	_, err := d.State().DB.Cluster.DB().Exec(`
PRAGMA foreign_keys=OFF; -- So that integrity doesn't get in the way for now.
PRAGMA legacy_alter_table = ON; -- So that views referencing this table don't block change.

CREATE TABLE nodes_new (
	id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
	name TEXT NOT NULL,
	description TEXT DEFAULT '',
	address TEXT NOT NULL,
	schema INTEGER NOT NULL,
	api_extensions INTEGER NOT NULL,
	heartbeat DATETIME DEFAULT CURRENT_TIMESTAMP,
	state INTEGER NOT NULL DEFAULT 0,
	arch INTEGER NOT NULL DEFAULT 0 CHECK (arch > 0),
	failure_domain_id INTEGER DEFAULT NULL REFERENCES nodes_failure_domains (id) ON DELETE SET NULL,
	UNIQUE (name),
	UNIQUE (address)
);

INSERT INTO nodes_new (id, name, description, address, schema, api_extensions, heartbeat, state, arch, failure_domain_id)
	SELECT id, name, description, address, schema, api_extensions, heartbeat, state, arch, failure_domain_id FROM nodes;

DROP TABLE nodes;
ALTER TABLE nodes_new RENAME TO nodes;

PRAGMA foreign_keys=ON; -- Make sure we turn integrity checks back on.
PRAGMA legacy_alter_table = OFF; -- So views check integrity again.
`)

	return err
}

// patchVMRenameUUIDKey renames the volatile.vm.uuid key to volatile.uuid in instance and snapshot configs.
func patchVMRenameUUIDKey(name string, d *Daemon) error {
	oldUUIDKey := "volatile.vm.uuid"
	newUUIDKey := "volatile.uuid"

	return d.State().DB.Cluster.InstanceList(context.TODO(), func(inst db.InstanceArgs, p api.Project) error {
		if inst.Type != instancetype.VM {
			return nil
		}

		return d.State().DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			uuid := inst.Config[oldUUIDKey]
			if uuid != "" {
				changes := map[string]string{
					oldUUIDKey: "",
					newUUIDKey: uuid,
				}

				logger.Debugf("Renaming config key %q to %q for VM %q (Project %q)", oldUUIDKey, newUUIDKey, inst.Name, inst.Project)
				err := tx.UpdateInstanceConfig(inst.ID, changes)
				if err != nil {
					return fmt.Errorf("Failed renaming config key %q to %q for VM %q (Project %q): %w", oldUUIDKey, newUUIDKey, inst.Name, inst.Project, err)
				}
			}

			snaps, err := tx.GetInstanceSnapshotsWithName(ctx, inst.Project, inst.Name)
			if err != nil {
				return err
			}

			for _, snap := range snaps {
				config, err := dbCluster.GetInstanceConfig(ctx, tx.Tx(), snap.ID)
				if err != nil {
					return err
				}

				uuid := config[oldUUIDKey]
				if uuid != "" {
					changes := map[string]string{
						oldUUIDKey: "",
						newUUIDKey: uuid,
					}

					logger.Debugf("Renaming config key %q to %q for VM %q (Project %q)", oldUUIDKey, newUUIDKey, snap.Name, snap.Project)
					err = tx.UpdateInstanceSnapshotConfig(snap.ID, changes)
					if err != nil {
						return fmt.Errorf("Failed renaming config key %q to %q for VM %q (Project %q): %w", oldUUIDKey, newUUIDKey, snap.Name, snap.Project, err)
					}
				}
			}

			return nil
		})
	})
}

// patchThinpoolTypoFix renames any config incorrectly set config file entries due to the lvm.thinpool_name typo.
func patchThinpoolTypoFix(name string, d *Daemon) error {
	revert := revert.New()
	defer revert.Fail()

	// Setup a transaction.
	tx, err := d.db.Cluster.Begin()
	if err != nil {
		return fmt.Errorf("Failed to begin transaction: %w", err)
	}

	revert.Add(func() { _ = tx.Rollback() })

	// Fetch the IDs of all existing nodes.
	nodeIDs, err := query.SelectIntegers(context.TODO(), tx, "SELECT id FROM nodes")
	if err != nil {
		return fmt.Errorf("Failed to get IDs of current nodes: %w", err)
	}

	// Fetch the IDs of all existing lvm pools.
	poolIDs, err := query.SelectIntegers(context.TODO(), tx, "SELECT id FROM storage_pools WHERE driver='lvm'")
	if err != nil {
		return fmt.Errorf("Failed to get IDs of current lvm pools: %w", err)
	}

	for _, poolID := range poolIDs {
		// Fetch the config for this lvm pool and check if it has the lvm.thinpool_name.
		config, err := query.SelectConfig(context.TODO(), tx, "storage_pools_config", "storage_pool_id=? AND node_id IS NULL", poolID)
		if err != nil {
			return fmt.Errorf("Failed to fetch of lvm pool config: %w", err)
		}

		value, ok := config["lvm.thinpool_name"]
		if !ok {
			continue
		}

		// Delete the current key
		_, err = tx.Exec(`
DELETE FROM storage_pools_config WHERE key='lvm.thinpool_name' AND storage_pool_id=? AND node_id IS NULL
`, poolID)
		if err != nil {
			return fmt.Errorf("Failed to delete lvm.thinpool_name config: %w", err)
		}

		// Add the config entry for each node
		for _, nodeID := range nodeIDs {
			_, err := tx.Exec(`
INSERT INTO storage_pools_config(storage_pool_id, node_id, key, value)
  VALUES(?, ?, 'lvm.thinpool_name', ?)
`, poolID, nodeID, value)
			if err != nil {
				return fmt.Errorf("Failed to create lvm.thinpool_name node config: %w", err)
			}
		}
	}

	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("Failed to commit transaction: %w", err)
	}

	revert.Success()
	return nil
}

// patchNetworkOVNRemoveRoutes removes the "ipv4.routes.external" and "ipv6.routes.external" settings from OVN
// networks. It was decided that the OVN NIC level equivalent settings were sufficient.
func patchNetworkOVNRemoveRoutes(name string, d *Daemon) error {
	err := d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		projectNetworks, err := tx.GetCreatedNetworks(ctx)
		if err != nil {
			return err
		}

		for _, networks := range projectNetworks {
			for networkID, network := range networks {
				if network.Type != "ovn" {
					continue
				}

				modified := false

				// Ensure existing behaviour of having NAT enabled if IP address was set.
				_, found := network.Config["ipv4.routes.external"]
				if found {
					modified = true
					delete(network.Config, "ipv4.routes.external")
				}

				_, found = network.Config["ipv6.routes.external"]
				if found {
					modified = true
					delete(network.Config, "ipv6.routes.external")
				}

				if modified {
					err = tx.UpdateNetwork(networkID, network.Description, network.Config)
					if err != nil {
						return fmt.Errorf("Failed removing OVN external route settings for %q (%d): %w", network.Name, networkID, err)
					}

					logger.Debugf("Removing external route settings for OVN network %q (%d)", network.Name, networkID)
				}
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

// patchNetworkOVNEnableNAT adds "ipv4.nat" and "ipv6.nat" keys set to "true" to OVN networks if not present.
// This is to ensure existing networks retain the old behaviour of always having NAT enabled as we introduce
// the new NAT settings which default to disabled if not specified.
// patchNetworkCearBridgeVolatileHwaddr removes the unsupported `volatile.bridge.hwaddr` config key from networks.
func patchNetworkOVNEnableNAT(name string, d *Daemon) error {
	err := d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		projectNetworks, err := tx.GetCreatedNetworks(ctx)
		if err != nil {
			return err
		}

		for _, networks := range projectNetworks {
			for networkID, network := range networks {
				if network.Type != "ovn" {
					continue
				}

				modified := false

				// Ensure existing behaviour of having NAT enabled if IP address was set.
				if network.Config["ipv4.address"] != "" && network.Config["ipv4.nat"] == "" {
					modified = true
					network.Config["ipv4.nat"] = "true"
				}

				if network.Config["ipv6.address"] != "" && network.Config["ipv6.nat"] == "" {
					modified = true
					network.Config["ipv6.nat"] = "true"
				}

				if modified {
					err = tx.UpdateNetwork(networkID, network.Description, network.Config)
					if err != nil {
						return fmt.Errorf("Failed saving OVN NAT settings for %q (%d): %w", network.Name, networkID, err)
					}

					logger.Debugf("Enabling NAT for OVN network %q (%d)", network.Name, networkID)
				}
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

// Moves backups from internalUtil.VarPath("backups") to internalUtil.VarPath("backups", "instances").
func patchMoveBackupsInstances(name string, d *Daemon) error {
	if !util.PathExists(internalUtil.VarPath("backups")) {
		return nil // Nothing to do, no backups directory.
	}

	backupsPath := internalUtil.VarPath("backups", "instances")

	err := os.MkdirAll(backupsPath, 0700)
	if err != nil {
		return fmt.Errorf("Failed creating instances backup directory %q: %w", backupsPath, err)
	}

	backups, err := os.ReadDir(internalUtil.VarPath("backups"))
	if err != nil {
		return fmt.Errorf("Failed listing existing backup directory %q: %w", internalUtil.VarPath("backups"), err)
	}

	for _, backupDir := range backups {
		if backupDir.Name() == "instances" || strings.HasPrefix(backupDir.Name(), backup.WorkingDirPrefix) {
			continue // Don't try and move our new instances directory or temporary directories.
		}

		oldPath := internalUtil.VarPath("backups", backupDir.Name())
		newPath := filepath.Join(backupsPath, backupDir.Name())
		logger.Debugf("Moving backup from %q to %q", oldPath, newPath)
		err = os.Rename(oldPath, newPath)
		if err != nil {
			return fmt.Errorf("Failed moving backup from %q to %q: %w", oldPath, newPath, err)
		}
	}

	return nil
}

func patchGenericStorage(name string, d *Daemon) error {
	return storagePools.Patch(d.State(), name)
}

func patchGenericNetwork(f func(name string, d *Daemon) error) func(name string, d *Daemon) error {
	return func(name string, d *Daemon) error {
		err := network.PatchPreCheck()
		if err != nil {
			return err
		}

		return f(name, d)
	}
}

func patchClusteringDropDatabaseRole(name string, d *Daemon) error {
	return d.State().DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		members, err := tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting cluster members: %w", err)
		}

		for _, member := range members {
			err := tx.UpdateNodeRoles(member.ID, nil)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// patchNetworkClearBridgeVolatileHwaddr removes the unsupported `volatile.bridge.hwaddr` config key from networks.
func patchNetworkClearBridgeVolatileHwaddr(name string, d *Daemon) error {
	// Use project.Default, as bridge networks don't support projects.
	projectName := project.Default

	// Get the list of networks.
	networks, err := d.db.Cluster.GetNetworks(projectName)
	if err != nil {
		return fmt.Errorf("Failed loading networks for network_clear_bridge_volatile_hwaddr patch: %w", err)
	}

	for _, networkName := range networks {
		_, net, _, err := d.db.Cluster.GetNetworkInAnyState(projectName, networkName)
		if err != nil {
			return fmt.Errorf("Failed loading network %q for network_clear_bridge_volatile_hwaddr patch: %w", networkName, err)
		}

		if net.Config["volatile.bridge.hwaddr"] != "" {
			delete(net.Config, "volatile.bridge.hwaddr")
			err = d.db.Cluster.UpdateNetwork(projectName, net.Name, net.Description, net.Config)
			if err != nil {
				return fmt.Errorf("Failed updating network %q for network_clear_bridge_volatile_hwaddr patch: %w", networkName, err)
			}
		}
	}

	return nil
}

// patchStorageRenameCustomISOBlockVolumes renames existing custom ISO volumes by adding the ".iso" suffix so they can be distinguished from regular custom block volumes.
// This patch doesn't use the patchGenericStorage function because the storage drivers themselves aren't aware of custom ISO volumes.
func patchStorageRenameCustomISOBlockVolumes(name string, d *Daemon) error {
	s := d.State()

	// Get all storage pool names.
	pools, err := s.DB.Cluster.GetStoragePoolNames()
	if err != nil {
		// Skip the rest of the patch if no storage pools were found.
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil
		}

		return fmt.Errorf("Failed getting storage pool names: %w", err)
	}

	volTypeCustom := db.StoragePoolVolumeTypeCustom
	customPoolVolumes := make(map[string][]*db.StorageVolume, 0)

	err = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		for _, pool := range pools {
			// Get storage pool ID.
			poolID, err := tx.GetStoragePoolID(ctx, pool)
			if err != nil {
				return fmt.Errorf("Failed getting storage pool ID of pool %q: %w", pool, err)
			}

			// Get the pool's custom storage volumes.
			customVolumes, err := tx.GetStoragePoolVolumes(ctx, poolID, false, db.StorageVolumeFilter{Type: &volTypeCustom})
			if err != nil {
				return fmt.Errorf("Failed getting custom storage volumes of pool %q: %w", pool, err)
			}

			if customPoolVolumes[pool] == nil {
				customPoolVolumes[pool] = []*db.StorageVolume{}
			}

			customPoolVolumes[pool] = append(customPoolVolumes[pool], customVolumes...)
		}

		return nil
	})
	if err != nil {
		return err
	}

	for poolName, volumes := range customPoolVolumes {
		// Load storage pool.
		p, err := storagePools.LoadByName(s, poolName)
		if err != nil {
			return fmt.Errorf("Failed loading pool %q: %w", poolName, err)
		}

		for _, vol := range volumes {
			// Exclude non-ISO custom volumes.
			if vol.ContentType != db.StoragePoolVolumeContentTypeNameISO {
				continue
			}

			// We need to use ContentTypeBlock here in order for the driver to figure out the correct (old) location.
			oldVol := storageDrivers.NewVolume(p.Driver(), p.Name(), storageDrivers.VolumeTypeCustom, storageDrivers.ContentTypeBlock, project.StorageVolume(vol.Project, vol.Name), nil, nil)

			err = p.Driver().RenameVolume(oldVol, fmt.Sprintf("%s.iso", oldVol.Name()), nil)
			if err != nil {
				return fmt.Errorf("Failed renaming volume: %w", err)
			}
		}
	}

	return nil
}

// patchZfsSetContentTypeUserProperty adds the `incus:content_type` user property to custom storage volumes. In case of recovery, this allows for proper detection of block-mode enabled volumes.
func patchZfsSetContentTypeUserProperty(name string, d *Daemon) error {
	s := d.State()

	// Get all storage pool names.
	pools, err := s.DB.Cluster.GetStoragePoolNames()
	if err != nil {
		// Skip the rest of the patch if no storage pools were found.
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil
		}

		return fmt.Errorf("Failed getting storage pool names: %w", err)
	}

	volTypeCustom := db.StoragePoolVolumeTypeCustom
	customPoolVolumes := make(map[string][]*db.StorageVolume, 0)

	err = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		for _, pool := range pools {
			// Get storage pool ID.
			poolID, err := tx.GetStoragePoolID(ctx, pool)
			if err != nil {
				return fmt.Errorf("Failed getting storage pool ID of pool %q: %w", pool, err)
			}

			// Get the pool's custom storage volumes.
			customVolumes, err := tx.GetStoragePoolVolumes(ctx, poolID, false, db.StorageVolumeFilter{Type: &volTypeCustom})
			if err != nil {
				return fmt.Errorf("Failed getting custom storage volumes of pool %q: %w", pool, err)
			}

			if customPoolVolumes[pool] == nil {
				customPoolVolumes[pool] = []*db.StorageVolume{}
			}

			customPoolVolumes[pool] = append(customPoolVolumes[pool], customVolumes...)
		}

		return nil
	})
	if err != nil {
		return err
	}

	for poolName, volumes := range customPoolVolumes {
		// Load storage pool.
		p, err := storagePools.LoadByName(s, poolName)
		if err != nil {
			return fmt.Errorf("Failed loading pool %q: %w", poolName, err)
		}

		if p.Driver().Info().Name != "zfs" {
			continue
		}

		for _, vol := range volumes {
			zfsPoolName := p.Driver().Config()["zfs.pool_name"]
			if zfsPoolName != "" {
				poolName = zfsPoolName
			}

			zfsVolName := fmt.Sprintf("%s/%s/%s", poolName, storageDrivers.VolumeTypeCustom, project.StorageVolume(vol.Project, vol.Name))

			_, err = subprocess.RunCommand("zfs", "set", fmt.Sprintf("incus:content_type=%s", vol.ContentType), zfsVolName)
			if err != nil {
				logger.Debug("Failed setting incus:content_type property", logger.Ctx{"name": zfsVolName, "err": err})
			}
		}
	}

	return nil
}

// patchSnapshotsRename renames the "snapshots" directory to "container-snapshots".
func patchSnapshotsRename(name string, d *Daemon) error {
	// Remove what should be an empty directory.
	os.Remove(internalUtil.VarPath("containers-snapshots"))

	return os.Rename(internalUtil.VarPath("snapshots"), internalUtil.VarPath("containers-snapshots"))
}

// patchStorageZfsUnsetInvalidBlockSettings removes invalid block settings from volumes.
func patchStorageZfsUnsetInvalidBlockSettings(_ string, d *Daemon) error {
	s := d.State()

	// Get all storage pool names.
	pools, err := s.DB.Cluster.GetStoragePoolNames()
	if err != nil {
		// Skip the rest of the patch if no storage pools were found.
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil
		}

		return fmt.Errorf("Failed getting storage pool names: %w", err)
	}

	volTypeCustom := db.StoragePoolVolumeTypeCustom
	volTypeVM := db.StoragePoolVolumeTypeVM

	poolIDNameMap := make(map[int64]string, 0)
	poolVolumes := make(map[int64][]*db.StorageVolume, 0)

	err = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		for _, pool := range pools {
			// Get storage pool ID.
			poolID, err := tx.GetStoragePoolID(ctx, pool)
			if err != nil {
				return fmt.Errorf("Failed getting storage pool ID of pool %q: %w", pool, err)
			}

			driverName, err := tx.GetStoragePoolDriver(ctx, poolID)
			if err != nil {
				return fmt.Errorf("Failed getting storage pool driver of pool %q: %w", pool, err)
			}

			if driverName != "zfs" {
				continue
			}

			// Get the pool's custom storage volumes.
			volumes, err := tx.GetStoragePoolVolumes(ctx, poolID, false, db.StorageVolumeFilter{Type: &volTypeCustom}, db.StorageVolumeFilter{Type: &volTypeVM})
			if err != nil {
				return fmt.Errorf("Failed getting custom storage volumes of pool %q: %w", pool, err)
			}

			if poolVolumes[poolID] == nil {
				poolVolumes[poolID] = []*db.StorageVolume{}
			}

			poolIDNameMap[poolID] = pool
			poolVolumes[poolID] = append(poolVolumes[poolID], volumes...)
		}

		return nil
	})
	if err != nil {
		return err
	}

	var volType int

	for pool, volumes := range poolVolumes {
		for _, vol := range volumes {
			if vol.Location != s.ServerName {
				continue
			}

			config := vol.Config

			if util.IsTrue(config["zfs.block_mode"]) {
				continue
			}

			update := false
			for _, k := range []string{"block.filesystem", "block.mount_options"} {
				_, found := config[k]
				if found {
					delete(config, k)
					update = true
				}
			}

			if !update {
				continue
			}

			if vol.Type == db.StoragePoolVolumeTypeNameVM {
				volType = volTypeVM
			} else if vol.Type == db.StoragePoolVolumeTypeNameCustom {
				volType = volTypeCustom
			} else {
				// Should not happen.
				continue
			}

			err = s.DB.Cluster.UpdateStoragePoolVolume(vol.Project, vol.Name, volType, pool, vol.Description, config)
			if err != nil {
				return fmt.Errorf("Failed updating volume %q in project %q on pool %q: %w", vol.Name, vol.Project, poolIDNameMap[pool], err)
			}
		}
	}

	return nil
}

// patchStorageZfsUnsetInvalidBlockSettingsV2 removes invalid block settings from volumes.
// This patch fixes the previous one.
// - Handle non-clusted environments correctly.
// - Always remove block.* settings from VMs.
func patchStorageZfsUnsetInvalidBlockSettingsV2(_ string, d *Daemon) error {
	s := d.State()

	// Get all storage pool names.
	pools, err := s.DB.Cluster.GetStoragePoolNames()
	if err != nil {
		// Skip the rest of the patch if no storage pools were found.
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil
		}

		return fmt.Errorf("Failed getting storage pool names: %w", err)
	}

	volTypeCustom := db.StoragePoolVolumeTypeCustom
	volTypeVM := db.StoragePoolVolumeTypeVM

	poolIDNameMap := make(map[int64]string, 0)
	poolVolumes := make(map[int64][]*db.StorageVolume, 0)

	err = s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
		for _, pool := range pools {
			// Get storage pool ID.
			poolID, err := tx.GetStoragePoolID(ctx, pool)
			if err != nil {
				return fmt.Errorf("Failed getting storage pool ID of pool %q: %w", pool, err)
			}

			driverName, err := tx.GetStoragePoolDriver(ctx, poolID)
			if err != nil {
				return fmt.Errorf("Failed getting storage pool driver of pool %q: %w", pool, err)
			}

			if driverName != "zfs" {
				continue
			}

			// Get the pool's custom storage volumes.
			volumes, err := tx.GetStoragePoolVolumes(ctx, poolID, false, db.StorageVolumeFilter{Type: &volTypeCustom}, db.StorageVolumeFilter{Type: &volTypeVM})
			if err != nil {
				return fmt.Errorf("Failed getting custom storage volumes of pool %q: %w", pool, err)
			}

			if poolVolumes[poolID] == nil {
				poolVolumes[poolID] = []*db.StorageVolume{}
			}

			poolIDNameMap[poolID] = pool
			poolVolumes[poolID] = append(poolVolumes[poolID], volumes...)
		}

		return nil
	})
	if err != nil {
		return err
	}

	var volType int

	for pool, volumes := range poolVolumes {
		for _, vol := range volumes {
			// In a non-clusted environment ServerName will be empty.
			if s.ServerName != "" && vol.Location != s.ServerName {
				continue
			}

			config := vol.Config

			// Only check zfs.block_mode for custom volumes. VMs should never have any block.* settings
			// regardless of the zfs.block_mode setting.
			if util.IsTrue(config["zfs.block_mode"]) && vol.Type == db.StoragePoolVolumeTypeNameCustom {
				continue
			}

			update := false
			for _, k := range []string{"block.filesystem", "block.mount_options"} {
				_, found := config[k]
				if found {
					delete(config, k)
					update = true
				}
			}

			if !update {
				continue
			}

			if vol.Type == db.StoragePoolVolumeTypeNameVM {
				volType = volTypeVM
			} else if vol.Type == db.StoragePoolVolumeTypeNameCustom {
				volType = volTypeCustom
			} else {
				// Should not happen.
				continue
			}

			err = s.DB.Cluster.UpdateStoragePoolVolume(vol.Project, vol.Name, volType, pool, vol.Description, config)
			if err != nil {
				return fmt.Errorf("Failed updating volume %q in project %q on pool %q: %w", vol.Name, vol.Project, poolIDNameMap[pool], err)
			}
		}
	}

	return nil
}

// Patches end here
