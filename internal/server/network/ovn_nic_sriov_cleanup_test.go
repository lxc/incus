package network

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
)

func TestNICSRIOVPendingSourceReservation(t *testing.T) {
	for _, name := range []string{"pending-source", "replaced-pf", "unknown-physical-identity", "wrong-source", "completed-source"} {
		t.Run(name, func(t *testing.T) {
			cluster, closeCluster := db.NewTestCluster(t)
			t.Cleanup(closeCluster)
			previous := sysClassNet
			sysClassNet = t.TempDir()
			t.Cleanup(func() { sysClassNet = previous })
			pf := filepath.Join(sysClassNet, "pci-pf")
			vf := filepath.Join(sysClassNet, "pci-vf")
			require.NoError(t, os.MkdirAll(filepath.Join(vf, "net", "renamed-original-vf"), 0o700))
			require.NoError(t, os.MkdirAll(pf, 0o700))
			require.NoError(t, os.MkdirAll(filepath.Join(sysClassNet, "parent-original"), 0o700))
			require.NoError(t, os.Symlink(pf, filepath.Join(sysClassNet, "parent-original", "device")))
			require.NoError(t, os.Symlink(vf, filepath.Join(pf, "virtfn3")))
			var ps, vs syscall.Stat_t
			require.NoError(t, syscall.Stat(pf, &ps))
			require.NoError(t, syscall.Stat(vf, &vs))
			physical := map[string]any{"Version": 1, "Parent": "parent-original", "VFID": 3, "PFPath": pf, "PFInode": ps.Ino, "VFPath": vf, "VFInode": vs.Ino}
			if name == "replaced-pf" {
				physical["PFInode"] = ps.Ino + 1
			}

			raw, err := json.Marshal(physical)
			require.NoError(t, err)
			if name == "unknown-physical-identity" {
				raw = nil
			}

			sourceName := "source-member"
			if name == "wrong-source" {
				sourceName = "other-member"
			}

			payload, err := json.Marshal(map[string]any{"Kind": "incus-ovn-nic-stop", "Version": 1, "DeviceConfig": map[string]string{"type": "nic", "acceleration": "sriov"}, "HostVolatile": map[string]string{"host_name": "old-original-vf", "last_state.vf.parent": "parent-original", "last_state.vf.id": "3", "last_state.ovn.physical": string(raw)}, "Port": map[string]string{"Source": sourceName}})
			require.NoError(t, err)
			token := uuid.NewString()
			var a db.OVNNICCleanup
			require.NoError(t, cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				id, err := tx.CreateNetwork(ctx, api.ProjectDefaultName, "original-physical", "", db.NetworkTypeOVN, nil)
				if err != nil {
					return err
				}

				if err = tx.NetworkCreated(api.ProjectDefaultName, "original-physical"); err != nil {
					return err
				}

				if err = tx.NetworkNodeCreated(id); err != nil {
					return err
				}

				if err = tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "original-physical", token, "nic"); err != nil {
					return err
				}

				a = db.OVNNICCleanup{Generation: uuid.NewString(), InstanceUUID: uuid.NewString(), DeviceName: "removed-source-device", Version: 1, SourceNodeID: cluster.GetNodeID(), NetworkIDs: []int64{id}, Payload: string(payload)}
				err = tx.CaptureOVNNICCleanup(ctx, a, map[int64]string{id: token})
				if err == nil && name == "completed-source" {
					err = tx.CompleteOVNNICCleanup(ctx, a, map[int64]string{id: token})
				}

				return err
			}))
			reserved, err := SRIOVGetHostDevicesInUse(&state.State{DB: &db.DB{Cluster: cluster}, ServerName: "source-member"})
			if name == "replaced-pf" || name == "unknown-physical-identity" || name == "wrong-source" {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			if name == "completed-source" {
				require.NotContains(t, reserved, "old-original-vf")
				require.NotContains(t, reserved, "renamed-original-vf")
			} else {
				require.Contains(t, reserved, "old-original-vf")
				require.Contains(t, reserved, "renamed-original-vf")
			}

			require.NotContains(t, reserved, "unrelated-sibling-vf")
		})
	}
}

func TestNICSRIOVStagedTargetReservation(t *testing.T) {
	for _, name := range []string{"pending-target", "handed-over-not-placed", "ambiguous-ovs", "missing-claim", "replaced-vf", "wrong-target-envelope", "completed-tombstone"} {
		t.Run(name, func(t *testing.T) {
			cluster, closeCluster := db.NewTestCluster(t)
			t.Cleanup(closeCluster)
			previous := sysClassNet
			sysClassNet = t.TempDir()
			t.Cleanup(func() { sysClassNet = previous })
			pf, vf := filepath.Join(sysClassNet, "pci-pf"), filepath.Join(sysClassNet, "pci-vf")
			require.NoError(t, os.MkdirAll(filepath.Join(vf, "net", "renamed-target-vf"), 0o700))
			require.NoError(t, os.MkdirAll(pf, 0o700))
			require.NoError(t, os.MkdirAll(filepath.Join(sysClassNet, "parent-target"), 0o700))
			require.NoError(t, os.Symlink(pf, filepath.Join(sysClassNet, "parent-target", "device")))
			require.NoError(t, os.Symlink(vf, filepath.Join(pf, "virtfn3")))
			var ps, vs syscall.Stat_t
			require.NoError(t, syscall.Stat(pf, &ps))
			require.NoError(t, syscall.Stat(vf, &vs))
			op, gen, identity := uuid.NewString(), uuid.NewString(), uuid.NewString()
			target := cluster.GetNodeID()
			require.NoError(t, cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				source, err := tx.CreateNode("source-staged", "192.0.2.30:8443")
				if err != nil {
					return err
				}

				_, err = tx.Tx().ExecContext(ctx, "UPDATE nodes SET name='target-staged' WHERE id=?", target)
				if err != nil {
					return err
				}

				result, err := tx.Tx().ExecContext(ctx, "INSERT INTO instances(node_id,name,architecture,type,description,project_id) VALUES (?,'moving',1,0,'',1)", source)
				if err != nil {
					return err
				}

				id, err := result.LastInsertId()
				if err != nil {
					return err
				}

				physical := map[string]any{"Version": 1, "NetworkID": 41, "SourceNodeID": target, "InstanceID": id, "InstanceUUID": identity, "DeviceName": "eth0", "Parent": "parent-target", "VFID": 3, "PFPath": pf, "PFInode": ps.Ino, "VFPath": vf, "VFInode": vs.Ino, "Representor": map[string]string{"Alias": "target-original-generation"}}
				if name == "replaced-vf" {
					physical["VFInode"] = vs.Ino + 1
				}

				if name == "wrong-target-envelope" {
					physical["SourceNodeID"] = source
				}

				raw, err := json.Marshal(physical)
				if err != nil {
					return err
				}

				if name == "missing-claim" {
					raw = nil
				}

				claim := map[string]string{"host_name": "target-original-vf", "last_state.vf.parent": "parent-target", "last_state.vf.id": "3", "last_state.ovn.physical": string(raw)}
				targetRaw, err := json.Marshal(claim)
				if err != nil {
					return err
				}

				original, err := json.Marshal(map[string]any{"DeviceConfig": map[string]string{"acceleration": "sriov"}})
				if err != nil {
					return err
				}

				_, err = tx.Tx().ExecContext(ctx, "INSERT INTO networks_ovn_nic_cleanup(generation,source_node_id,instance_uuid,device_name,version,network_ids,payload) VALUES (?,?,?,'eth0',1,'[41]',?)", gen, source, identity, string(original))
				if err != nil {
					return err
				}

				phase := "authorized"
				if name == "handed-over-not-placed" {
					phase = "handover"
				}

				if name == "completed-tombstone" {
					phase = "placed"
				}

				_, err = tx.Tx().ExecContext(ctx, "INSERT INTO networks_ovn_nic_migrations(operation,project_id,instance_id,instance_uuid,source_node_id,target_node_id,phase) VALUES (?,1,?,?,?,?,?)", op, id, identity, source, target, phase)
				if err != nil {
					return err
				}

				attempted := 0
				if name == "ambiguous-ovs" {
					attempted = 1
				}

				_, err = tx.Tx().ExecContext(ctx, "INSERT INTO networks_ovn_nic_migration_devices(operation,device_name,generation,target_volatile,ovs_attempted) VALUES (?,'eth0',?,?,?)", op, gen, string(targetRaw), attempted)
				return err
			}))
			// No target Desired or instance volatile allocation exists before placement.
			reserved, err := SRIOVGetHostDevicesInUse(&state.State{DB: &db.DB{Cluster: cluster}, ServerName: "target-staged"})
			if name == "missing-claim" || name == "replaced-vf" || name == "wrong-target-envelope" {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			if name == "completed-tombstone" {
				require.NotContains(t, reserved, "target-original-vf")
				return
			}

			require.Contains(t, reserved, "target-original-vf")
			require.Contains(t, reserved, "renamed-target-vf")
			require.NotContains(t, reserved, "unrelated-sibling-vf")
		})
	}
}
