//go:build linux && cgo && !agent

package db_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/shared/api"
)

func ovnCleanupFixture(t *testing.T, tx *db.ClusterTx) (db.OVNNICCleanup, map[int64]string) {
	t.Helper()
	ctx := context.Background()
	ids := []int64{}
	tokens := map[int64]string{}
	for _, name := range []string{"cleanup-child", "cleanup-peer"} {
		id, err := tx.CreateNetwork(ctx, api.ProjectDefaultName, name, "", db.NetworkTypeOVN, nil)
		require.NoError(t, err)
		require.NoError(t, tx.NetworkCreated(api.ProjectDefaultName, name))
		require.NoError(t, tx.NetworkNodeCreated(id))
		token := uuid.NewString()
		require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, name, token, "nic"))
		ids = append(ids, id)
		tokens[id] = token
	}

	return db.OVNNICCleanup{Generation: uuid.NewString(), SourceNodeID: tx.GetNodeID(), InstanceUUID: uuid.NewString(), DeviceName: "eth0", Version: 1, NetworkIDs: ids, Payload: `{"original_port":"original-row","original_routes":["route-row"]}`}, tokens
}

func TestOVNNICCleanupOriginalSourceAndCompletion(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()
	ctx := context.Background()
	var original db.OVNNICCleanup
	var tokens map[int64]string
	err := cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		original, tokens = ovnCleanupFixture(t, tx)
		return tx.CaptureOVNNICCleanup(ctx, original, tokens)
	})
	require.NoError(t, err)

	// A fresh transaction/reader sees the original source's payload without an instance/config lookup.
	err = cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		pending, err := tx.OVNNICCleanups(ctx)
		require.NoError(t, err)
		require.Equal(t, []db.OVNNICCleanup{original}, pending)
		require.True(t, api.StatusErrorCheck(tx.EnsureOVNNICCleanupComplete(ctx, original.NetworkIDs[1]), http.StatusConflict))
		changed := original
		changed.Payload = `{"original_port":"replacement-row"}`
		require.True(t, api.StatusErrorCheck(tx.CaptureOVNNICCleanup(ctx, changed, tokens), http.StatusConflict))
		require.True(t, api.StatusErrorCheck(tx.CompleteOVNNICCleanup(ctx, changed, tokens), http.StatusConflict))
		require.NoError(t, tx.CaptureOVNNICCleanup(ctx, original, tokens))
		return tx.CompleteOVNNICCleanup(ctx, original, tokens)
	})
	require.NoError(t, err)

	err = cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		pending, err := tx.OVNNICCleanups(ctx)
		require.NoError(t, err)
		require.Empty(t, pending)
		require.NoError(t, tx.EnsureOVNNICCleanupComplete(ctx, original.NetworkIDs[0]))
		completed, err := tx.OVNNICCleanupByGeneration(ctx, original.Generation)
		require.NoError(t, err)
		require.True(t, completed.Completed)
		// An uncertain completion can be reconciled after its reservations have ended.
		require.NoError(t, tx.CompleteOVNNICCleanup(ctx, original, nil))
		missing := original
		missing.Generation = uuid.NewString()
		require.ErrorIs(t, tx.CompleteOVNNICCleanup(ctx, missing, tokens), sql.ErrNoRows)
		return nil
	})
	require.NoError(t, err)
}

func TestOVNNICCleanupPreparedBarrier(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	original, tokens := ovnCleanupFixture(t, tx)
	require.NoError(t, tx.CaptureOVNNICCleanup(ctx, original, tokens))
	require.NoError(t, tx.UpdateNodeStatus(tx.GetNodeID(), db.ClusterMemberStateEvacuating))
	prepareTokens := map[int64]string{}
	for i, name := range []string{"cleanup-child", "cleanup-peer"} {
		id := original.NetworkIDs[i]
		require.NoError(t, tx.ReleaseOVNNetworkOperation(ctx, api.ProjectDefaultName, name, tokens[id]))
		token := uuid.NewString()
		require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, name, token, "prepare"))
		prepareTokens[id] = token
		// Both the child and captured peer refuse to prepare before the original effects are acknowledged.
		require.True(t, api.StatusErrorCheck(tx.OVNLocalPreparing(ctx, id, token), http.StatusConflict))
		require.NoError(t, tx.OVNLocalStopped(ctx, api.ProjectDefaultName, name, id, token))
		require.True(t, api.StatusErrorCheck(tx.OVNLocalPrepared(ctx, id, token), http.StatusConflict))
	}

	require.NoError(t, tx.CompleteOVNNICCleanup(ctx, original, prepareTokens))
	for i := range original.NetworkIDs {
		id := original.NetworkIDs[i]
		require.NoError(t, tx.OVNLocalPreparing(ctx, id, prepareTokens[id]))
		name := []string{"cleanup-child", "cleanup-peer"}[i]
		require.NoError(t, tx.OVNLocalStopped(ctx, api.ProjectDefaultName, name, id, prepareTokens[id]))
		require.NoError(t, tx.OVNLocalPrepared(ctx, id, prepareTokens[id]))
	}
}

func TestOVNNICCleanupCannotReplacePendingGeneration(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	original, tokens := ovnCleanupFixture(t, tx)
	require.NoError(t, tx.CaptureOVNNICCleanup(ctx, original, tokens))
	replacement := original
	replacement.Generation = uuid.NewString()
	replacement.Payload = `{"original_port":"new-allocation"}`
	require.Error(t, tx.CaptureOVNNICCleanup(ctx, replacement, tokens))
	pending, err := tx.OVNNICCleanups(ctx)
	require.NoError(t, err)
	require.Equal(t, []db.OVNNICCleanup{original}, pending)
}

func TestOVNNICCleanupCaptureRefusals(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*db.OVNNICCleanup, map[int64]string)
	}{
		{"foreign-source", func(a *db.OVNNICCleanup, _ map[int64]string) { a.SourceNodeID++ }},
		{"missing-peer-token", func(a *db.OVNNICCleanup, tokens map[int64]string) { delete(tokens, a.NetworkIDs[1]) }},
		{"stale-peer-token", func(a *db.OVNNICCleanup, tokens map[int64]string) { tokens[a.NetworkIDs[1]] = "stale" }},
		{"future-version", func(a *db.OVNNICCleanup, _ map[int64]string) { a.Version++ }},
		{"malformed-payload", func(a *db.OVNNICCleanup, _ map[int64]string) { a.Payload = "{" }},
		{"duplicate-network", func(a *db.OVNNICCleanup, _ map[int64]string) {
			a.NetworkIDs = []int64{a.NetworkIDs[0], a.NetworkIDs[0]}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx, cleanup := db.NewTestClusterTx(t)
			defer cleanup()
			original, tokens := ovnCleanupFixture(t, tx)
			test.mutate(&original, tokens)
			require.Error(t, tx.CaptureOVNNICCleanup(context.Background(), original, tokens))
			pending, err := tx.OVNNICCleanups(context.Background())
			require.NoError(t, err)
			require.Empty(t, pending)
		})
	}
}

func TestOVNNICCleanupCompletionFailureRetainsDebt(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()
	ctx := context.Background()
	var original db.OVNNICCleanup
	var tokens map[int64]string
	err := cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		original, tokens = ovnCleanupFixture(t, tx)
		err := tx.CaptureOVNNICCleanup(ctx, original, tokens)
		if err != nil {
			return err
		}

		_, err = tx.Tx().ExecContext(ctx, `CREATE TRIGGER reject_cleanup_completion BEFORE DELETE ON networks_ovn_nic_cleanup_networks BEGIN SELECT RAISE(ABORT, 'injected completion persistence failure'); END`)
		return err
	})
	require.NoError(t, err)
	err = cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.CompleteOVNNICCleanup(ctx, original, tokens)
	})
	require.ErrorContains(t, err, "injected completion persistence failure")
	err = cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		pending, err := tx.OVNNICCleanups(ctx)
		require.NoError(t, err)
		require.Equal(t, []db.OVNNICCleanup{original}, pending)
		require.True(t, api.StatusErrorCheck(tx.EnsureOVNNICCleanupComplete(ctx, original.NetworkIDs[0]), http.StatusConflict))
		_, err = tx.Tx().ExecContext(ctx, "DROP TRIGGER reject_cleanup_completion")
		if err != nil {
			return err
		}

		return tx.CompleteOVNNICCleanup(ctx, original, tokens)
	})
	require.NoError(t, err)
}

func TestOVNNICCleanupSurvivesPlacementAndConfigLoss(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()
	ctx := context.Background()
	var original db.OVNNICCleanup
	var tokens map[int64]string
	err := cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		original, tokens = ovnCleanupFixture(t, tx)
		addContainer(t, tx, tx.GetNodeID(), "moving-source")
		addContainerConfig(t, tx, "moving-source", "volatile.uuid", original.InstanceUUID)
		err := tx.CaptureOVNNICCleanup(ctx, original, tokens)
		if err != nil {
			return err
		}

		target, err := tx.CreateNode("target-member", "192.0.2.10:8443")
		if err != nil {
			return err
		}

		_, err = tx.Tx().ExecContext(ctx, "UPDATE instances SET node_id=? WHERE name='moving-source'", target)
		if err != nil {
			return err
		}

		// Target-owned volatile writes must not overwrite the already acknowledged source payload.
		_, err = tx.Tx().ExecContext(ctx, "UPDATE instances_config SET value=? WHERE key='volatile.uuid'", uuid.NewString())
		return err
	})
	require.NoError(t, err)

	err = cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		pending, err := tx.OVNNICCleanups(ctx)
		require.NoError(t, err)
		require.Equal(t, []db.OVNNICCleanup{original}, pending)
		foreign := original
		foreign.SourceNodeID++
		require.True(t, api.StatusErrorCheck(tx.CompleteOVNNICCleanup(ctx, foreign, tokens), http.StatusConflict))
		_, err = tx.Tx().ExecContext(ctx, "DELETE FROM instances WHERE name='moving-source'")
		return err
	})
	require.NoError(t, err)
	err = cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		pending, err := tx.OVNNICCleanups(ctx)
		require.NoError(t, err)
		require.Equal(t, []db.OVNNICCleanup{original}, pending, "instance deletion must not erase original source cleanup")
		return nil
	})
	require.NoError(t, err)
	// Numeric network identity cannot disappear while the original attempt remains pending.
	err = cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.Tx().ExecContext(ctx, "DELETE FROM networks WHERE id=?", original.NetworkIDs[0])
		return err
	})
	require.ErrorContains(t, err, "FOREIGN KEY")
	err = cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		err := tx.CompleteOVNNICCleanup(ctx, original, tokens)
		if err != nil {
			return err
		}

		_, err = tx.Tx().ExecContext(ctx, "DELETE FROM networks WHERE id=?", original.NetworkIDs[0])
		return err
	})
	require.NoError(t, err)
}

func TestOVNNICCleanupReservationInheritance(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	original, tokens := ovnCleanupFixture(t, tx)
	peerID := original.NetworkIDs[1]
	require.NoError(t, tx.ReleaseOVNNetworkOperation(ctx, api.ProjectDefaultName, "cleanup-peer", tokens[peerID]))
	reservations, err := tx.AcquireOVNNICCleanupOperations(ctx, original.NetworkIDs, map[int64]string{original.NetworkIDs[0]: tokens[original.NetworkIDs[0]]})
	require.NoError(t, err)
	require.Len(t, reservations, 2)
	require.True(t, reservations[0].Inherited)
	require.False(t, reservations[1].Inherited)
	all := map[int64]string{}
	for _, reservation := range reservations {
		all[reservation.NetworkID] = reservation.Token
	}

	require.NoError(t, tx.CaptureOVNNICCleanup(ctx, original, all))
	require.NoError(t, tx.ReleaseOVNNICCleanupOperations(ctx, reservations))
	child, err := tx.OVNNetworkOperationToken(ctx, api.ProjectDefaultName, "cleanup-child")
	require.NoError(t, err)
	require.Equal(t, tokens[original.NetworkIDs[0]], child, "nested release must preserve its enclosing caller's token")
	peer, err := tx.OVNNetworkOperationToken(ctx, api.ProjectDefaultName, "cleanup-peer")
	require.NoError(t, err)
	require.Empty(t, peer)
	require.True(t, api.StatusErrorCheck(tx.EnsureOVNNICCleanupComplete(ctx, peerID), http.StatusConflict), "reservation release cannot acknowledge incomplete effects")
}

func TestOVNNICCleanupReservationConflictRollsBackSet(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()
	ctx := context.Background()
	var original db.OVNNICCleanup
	var tokens map[int64]string
	err := cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		original, tokens = ovnCleanupFixture(t, tx)
		return tx.ReleaseOVNNetworkOperation(ctx, api.ProjectDefaultName, "cleanup-child", tokens[original.NetworkIDs[0]])
	})
	require.NoError(t, err)
	err = cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.AcquireOVNNICCleanupOperations(ctx, original.NetworkIDs, nil)
		return err
	})
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
	err = cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		child, err := tx.OVNNetworkOperationToken(ctx, api.ProjectDefaultName, "cleanup-child")
		require.NoError(t, err)
		require.Empty(t, child, "the first new token must roll back when the later peer conflicts")
		peer, err := tx.OVNNetworkOperationToken(ctx, api.ProjectDefaultName, "cleanup-peer")
		require.NoError(t, err)
		require.Equal(t, tokens[original.NetworkIDs[1]], peer)
		pending, err := tx.OVNNICCleanups(ctx)
		require.NoError(t, err)
		require.Empty(t, pending)
		return nil
	})
	require.NoError(t, err)
}

func TestOVNNICCleanupReservationUsesOriginalNumericIdentity(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	original, tokens := ovnCleanupFixture(t, tx)
	require.NoError(t, tx.ReleaseOVNNetworkOperation(ctx, api.ProjectDefaultName, "cleanup-child", tokens[original.NetworkIDs[0]]))
	_, err := tx.Tx().ExecContext(ctx, "UPDATE networks SET name='renamed-original' WHERE id=?", original.NetworkIDs[0])
	require.NoError(t, err)
	replacementID, err := tx.CreateNetwork(ctx, api.ProjectDefaultName, "cleanup-child", "", db.NetworkTypeOVN, nil)
	require.NoError(t, err)
	reservations, err := tx.AcquireOVNNICCleanupOperations(ctx, original.NetworkIDs, map[int64]string{original.NetworkIDs[1]: tokens[original.NetworkIDs[1]]})
	require.NoError(t, err)
	require.Equal(t, original.NetworkIDs[0], reservations[0].NetworkID)
	require.Equal(t, "renamed-original", reservations[0].Name)
	require.NotEqual(t, replacementID, reservations[0].NetworkID)
	replacementToken, err := tx.OVNNetworkOperationToken(ctx, api.ProjectDefaultName, "cleanup-child")
	require.NoError(t, err)
	require.Empty(t, replacementToken, "name reuse must not acquire or clean the new network")
	all := map[int64]string{}
	for _, reservation := range reservations {
		all[reservation.NetworkID] = reservation.Token
	}

	require.NoError(t, tx.CaptureOVNNICCleanup(ctx, original, all))
	require.NoError(t, tx.EnsureOVNNICCleanupComplete(ctx, replacementID))
	require.True(t, api.StatusErrorCheck(tx.EnsureOVNNICCleanupComplete(ctx, original.NetworkIDs[0]), http.StatusConflict))
}

func TestOVNNICCleanupVolatileRetirement(t *testing.T) {
	for _, name := range []string{"matching", "repeated", "new-allocation-after-retirement", "changed-host", "changed-late-key", "moved", "deleted", "replacement-uuid", "stale-token", "delete-failure", "receipt-failure", "wrong-source", "changed-payload", "unsupported-payload"} {
		t.Run(name, func(t *testing.T) {
			cluster, cleanup := db.NewTestCluster(t)
			t.Cleanup(cleanup)
			ctx := context.Background()
			var a db.OVNNICCleanup
			var tokens map[int64]string
			var instanceID int64
			original := map[string]string{"host_name": "original-host", "last_state.pci.driver": "original-driver"}
			require.NoError(t, cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				a, tokens = ovnCleanupFixture(t, tx)
				result, err := tx.Tx().ExecContext(ctx, "INSERT INTO instances (node_id,name,architecture,type,description,project_id) VALUES (?, 'source-instance',1,0,'',(SELECT id FROM projects WHERE name='default'))", tx.GetNodeID())
				if err != nil {
					return err
				}

				instanceID, err = result.LastInsertId()
				if err != nil {
					return err
				}

				values := map[string]string{"volatile.uuid": a.InstanceUUID, "volatile.eth0.host_name": original["host_name"], "volatile.eth0.last_state.pci.driver": original["last_state.pci.driver"], "volatile.sibling.host_name": "sibling-host", "user.keep": "unrelated"}
				for key, value := range values {
					_, err = tx.Tx().ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,?,?)", instanceID, key, value)
					if err != nil {
						return err
					}
				}

				payload, err := json.Marshal(map[string]any{"Kind": "incus-ovn-nic-stop", "Version": 1, "InstanceID": instanceID, "HostVolatile": original})
				if err != nil {
					return err
				}

				a.Payload = string(payload)
				if name == "unsupported-payload" {
					a.Payload = "{\"original\":42}"
				}

				return tx.CaptureOVNNICCleanup(ctx, a, tokens)
			}))
			require.NoError(t, cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				var err error
				switch name {
				case "changed-host":
					_, err = tx.Tx().ExecContext(ctx, "UPDATE instances_config SET value='replacement-host' WHERE instance_id=? AND key='volatile.eth0.host_name'", instanceID)
				case "changed-late-key":
					_, err = tx.Tx().ExecContext(ctx, "UPDATE instances_config SET value='replacement-driver' WHERE instance_id=? AND key='volatile.eth0.last_state.pci.driver'", instanceID)
				case "moved":
					result, e := tx.Tx().ExecContext(ctx, "INSERT INTO nodes(name,address,schema,api_extensions,description,arch) VALUES ('target-member','192.0.2.99',84,0,'',1)")
					if e != nil {
						return e
					}

					targetID, e := result.LastInsertId()
					if e != nil {
						return e
					}

					_, err = tx.Tx().ExecContext(ctx, "UPDATE instances SET node_id=? WHERE id=?", targetID, instanceID)
				case "deleted":
					_, err = tx.Tx().ExecContext(ctx, "DELETE FROM instances WHERE id=?", instanceID)
				case "replacement-uuid":
					_, err = tx.Tx().ExecContext(ctx, "UPDATE instances_config SET value=? WHERE instance_id=? AND key='volatile.uuid'", uuid.NewString(), instanceID)
				case "delete-failure":
					_, err = tx.Tx().ExecContext(ctx, "CREATE TRIGGER fail_retirement_delete BEFORE DELETE ON instances_config WHEN OLD.key='volatile.eth0.last_state.pci.driver' BEGIN SELECT RAISE(ABORT,'injected delete failure'); END")
				case "receipt-failure":
					_, err = tx.Tx().ExecContext(ctx, "CREATE TRIGGER fail_retirement_receipt BEFORE INSERT ON networks_ovn_nic_cleanup_retirement BEGIN SELECT RAISE(ABORT,'injected receipt failure'); END")
				}

				return err
			}))
			request := a
			if name == "wrong-source" {
				request.SourceNodeID++
			}

			if name == "changed-payload" {
				request.Payload = "{\"changed\":true}"
			}

			if name == "stale-token" {
				tokens[a.NetworkIDs[len(a.NetworkIDs)-1]] = uuid.NewString()
			}

			var cleared bool
			err := cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				var err error
				cleared, err = tx.RetireOVNNICCleanupVolatile(ctx, request, tokens)
				return err
			})
			success := name == "matching" || name == "repeated" || name == "new-allocation-after-retirement" || name == "moved" || name == "deleted" || name == "replacement-uuid"
			if success {
				require.NoError(t, err)
				require.Equal(t, name != "moved" && name != "deleted" && name != "replacement-uuid", cleared)
			} else {
				require.Error(t, err)
			}

			require.NoError(t, cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				pending, e := tx.OVNNICCleanupByGeneration(ctx, a.Generation)
				require.NoError(t, e)
				require.False(t, pending.Completed)
				require.Equal(t, a.Payload, pending.Payload)
				require.Error(t, tx.EnsureOVNNICCleanupComplete(ctx, a.NetworkIDs[0]))
				var receipts int
				require.NoError(t, tx.Tx().QueryRowContext(ctx, "SELECT COUNT(*) FROM networks_ovn_nic_cleanup_retirement WHERE generation=?", a.Generation).Scan(&receipts))
				if success {
					require.Equal(t, 1, receipts)
				} else {
					require.Zero(t, receipts)
				}

				if name != "deleted" {
					var host, driver, sibling, keep string
					e := tx.Tx().QueryRowContext(ctx, "SELECT COALESCE((SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.eth0.host_name'),''),COALESCE((SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.eth0.last_state.pci.driver'),''),(SELECT value FROM instances_config WHERE instance_id=? AND key='volatile.sibling.host_name'),(SELECT value FROM instances_config WHERE instance_id=? AND key='user.keep')", instanceID, instanceID, instanceID, instanceID).Scan(&host, &driver, &sibling, &keep)
					require.NoError(t, e)
					require.Equal(t, "sibling-host", sibling)
					require.Equal(t, "unrelated", keep)
					if cleared && success {
						require.Empty(t, host)
						require.Empty(t, driver)
					} else {
						wantHost, wantDriver := "original-host", "original-driver"
						if name == "changed-host" {
							wantHost = "replacement-host"
						}

						if name == "changed-late-key" {
							wantDriver = "replacement-driver"
						}

						require.Equal(t, wantHost, host)
						require.Equal(t, wantDriver, driver)
					}
				}

				return nil
			}))
			if name == "repeated" || name == "new-allocation-after-retirement" {
				if name == "new-allocation-after-retirement" {
					require.NoError(t, cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
						_, err := tx.Tx().ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,'volatile.eth0.host_name','new-host')", instanceID)
						return err
					}))
				}

				err := cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					var err error
					cleared, err = tx.RetireOVNNICCleanupVolatile(ctx, a, tokens)
					return err
				})
				if name == "repeated" {
					require.NoError(t, err)
					require.True(t, cleared)
				} else {
					require.Error(t, err)
				}
			}
		})
	}
}

func TestOVNNICCleanupPhysicalPreclaimBarrier(t *testing.T) {
	for _, mode := range []string{"no-row", "moved-no-row", "completed-original", "completed-different-claim", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			tx, cleanup := db.NewTestClusterTx(t)
			defer cleanup()
			ctx := context.Background()
			a, tokens := ovnCleanupFixture(t, tx)
			res, err := tx.Tx().ExecContext(ctx, "INSERT INTO instances(node_id,name,architecture,type,description,project_id) VALUES (?,'partial-start',1,0,'',(SELECT id FROM projects WHERE name='default'))", tx.GetNodeID())
			require.NoError(t, err)
			id, err := res.LastInsertId()
			require.NoError(t, err)
			raw, err := json.Marshal(map[string]any{"Version": 1, "NetworkID": a.NetworkIDs[0], "SourceNodeID": tx.GetNodeID(), "InstanceUUID": a.InstanceUUID, "InstanceID": id, "DeviceName": a.DeviceName, "Representor": map[string]string{"Alias": "original-generation"}})
			require.NoError(t, err)
			for key, value := range map[string]string{"volatile.uuid": a.InstanceUUID, "volatile.eth0.last_state.ovn.physical": string(raw)} {
				_, err = tx.Tx().ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,?,?)", id, key, value)
				require.NoError(t, err)
			}
			// There is no Desired device and no cleanup attempt in the failed-Start case.
			require.Error(t, tx.EnsureOVNNICCleanupComplete(ctx, a.NetworkIDs[0]))
			require.Error(t, tx.EnsureOVNNICCleanupCompleteForInstance(ctx, a.InstanceUUID))
			require.NoError(t, tx.EnsureOVNNICCleanupComplete(ctx, a.NetworkIDs[1]))
			require.NoError(t, tx.EnsureOVNNICCleanupCompleteForInstance(ctx, uuid.NewString()))
			if mode == "malformed" {
				_, err = tx.Tx().ExecContext(ctx, "UPDATE instances_config SET value='{}' WHERE key='volatile.eth0.last_state.ovn.physical'")
				require.NoError(t, err)
				require.Error(t, tx.EnsureOVNNICCleanupComplete(ctx, a.NetworkIDs[0]))
				return
			}

			if mode == "moved-no-row" {
				_, err = tx.Tx().ExecContext(ctx, "INSERT INTO nodes(name,address,schema,api_extensions,description,arch) VALUES ('moved-target','192.0.2.200',84,0,'',1)")
				require.NoError(t, err)
				_, err = tx.Tx().ExecContext(ctx, "UPDATE instances SET node_id=(SELECT id FROM nodes WHERE name='moved-target') WHERE id=?", id)
				require.NoError(t, err)
				require.Error(t, tx.EnsureOVNNICCleanupComplete(ctx, a.NetworkIDs[0]), "current placement cannot erase original source quarantine")
				return
			}

			if mode == "no-row" {
				return
			}

			payload, err := json.Marshal(map[string]any{"Kind": "incus-ovn-nic-stop", "Version": 1, "InstanceID": id, "HostVolatile": map[string]string{"last_state.ovn.physical": string(raw)}})
			require.NoError(t, err)
			a.Payload = string(payload)
			require.NoError(t, tx.CaptureOVNNICCleanup(ctx, a, tokens))
			require.NoError(t, tx.CompleteOVNNICCleanup(ctx, a, tokens))
			require.NoError(t, tx.EnsureOVNNICCleanupComplete(ctx, a.NetworkIDs[0]), "the exact original tombstone acknowledges retained moved-source metadata")
			if mode == "completed-different-claim" {
				_, err = tx.Tx().ExecContext(ctx, "UPDATE instances_config SET value=REPLACE(value,'original-generation','replacement-generation') WHERE key='volatile.eth0.last_state.ovn.physical'")
				require.NoError(t, err)
				require.Error(t, tx.EnsureOVNNICCleanupComplete(ctx, a.NetworkIDs[0]), "a completed receipt cannot authorize another allocation")
				require.Error(t, tx.EnsureOVNNICCleanupCompleteForInstance(ctx, a.InstanceUUID))
			}
		})
	}
}

func TestOVNNICMalformedClaimScope(t *testing.T) {
	for _, raw := range []string{"broken-json", "{}", `{"Version":2,"NetworkID":41,"SourceNodeID":1}`} {
		t.Run(raw, func(t *testing.T) {
			tx, cleanup := db.NewTestClusterTx(t)
			defer cleanup()
			ctx := context.Background()
			identity := uuid.NewString()
			res, err := tx.Tx().ExecContext(ctx, "INSERT INTO instances(node_id,name,architecture,type,description,project_id) VALUES (?,'malformed-claim',1,0,'',(SELECT id FROM projects WHERE name='default'))", tx.GetNodeID())
			require.NoError(t, err)
			id, err := res.LastInsertId()
			require.NoError(t, err)
			for key, value := range map[string]string{"volatile.uuid": identity, "volatile.eth0.last_state.ovn.host": raw} {
				_, err = tx.Tx().ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,?,?)", id, key, value)
				require.NoError(t, err)
			}

			require.NoError(t, tx.EnsureOVNNICCleanupCompleteForInstance(ctx, uuid.NewString()), "an unrelated instance must still stop")
			require.Error(t, tx.EnsureOVNNICCleanupCompleteForInstance(ctx, identity), "the original malformed allocation stays quarantined")
			require.Error(t, tx.EnsureOVNNICCleanupComplete(ctx, 41), "local preparation cannot acknowledge an unclassifiable allocation")
			if raw != "broken-json" && raw != "{}" {
				require.NoError(t, tx.EnsureOVNNICCleanupComplete(ctx, 42), "a decoded different network is outside scope")
			}

			node, err := tx.CreateNode("malformed-foreign", "192.0.2.210")
			require.NoError(t, err)
			_, err = tx.Tx().ExecContext(ctx, "UPDATE instances SET node_id=? WHERE id=?", node, id)
			require.NoError(t, err)
			if raw == "broken-json" || raw == "{}" {
				require.NoError(t, tx.EnsureOVNNICCleanupComplete(ctx, 41), "another member's malformed allocation cannot block local preparation")
			} else {
				require.Error(t, tx.EnsureOVNNICCleanupComplete(ctx, 41), "moving the record cannot erase its decoded original source claim")
			}
		})
	}
}

func TestOVNNICCleanupPhysicalPreclaimRetirementCAS(t *testing.T) {
	for _, mode := range []string{"success", "changed-allocation", "changed-uuid", "delete-failure", "committed-retirement-reconciliation"} {
		t.Run(mode, func(t *testing.T) {
			cluster, cleanup := db.NewTestCluster(t)
			defer cleanup()
			ctx := context.Background()
			instanceID := 0
			instanceUUID := uuid.NewString()
			var original map[string]string
			require.NoError(t, cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				res, err := tx.Tx().ExecContext(ctx, "INSERT INTO instances(node_id,name,architecture,type,description,project_id) VALUES (?,'prebackend-failure',1,0,'',(SELECT id FROM projects WHERE name='default'))", tx.GetNodeID())
				if err != nil {
					return err
				}

				id, err := res.LastInsertId()
				if err != nil {
					return err
				}

				instanceID = int(id)
				raw, err := json.Marshal(map[string]any{"Version": 1, "NetworkID": 41, "SourceNodeID": tx.GetNodeID(), "InstanceUUID": instanceUUID, "InstanceID": instanceID, "DeviceName": "eth0"})
				if err != nil {
					return err
				}

				original = map[string]string{"host_name": "original-vf", "last_state.ovn.physical": string(raw), "last_state.vf.id": "7"}
				values := map[string]string{"volatile.uuid": instanceUUID, "volatile.sibling.host_name": "sibling-vf", "user.keep": "foreign"}
				for key, value := range original {
					values["volatile.eth0."+key] = value
				}

				for key, value := range values {
					_, err = tx.Tx().ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,?,?)", instanceID, key, value)
					if err != nil {
						return err
					}
				}

				return nil
			}))
			require.NoError(t, cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				var err error
				switch mode {
				case "changed-allocation":
					_, err = tx.Tx().ExecContext(ctx, "UPDATE instances_config SET value='replacement-vf' WHERE key='volatile.eth0.host_name'")
				case "changed-uuid":
					_, err = tx.Tx().ExecContext(ctx, "UPDATE instances_config SET value=? WHERE key='volatile.uuid'", uuid.NewString())
				case "delete-failure":
					_, err = tx.Tx().ExecContext(ctx, "CREATE TRIGGER preclaim_delete_failure BEFORE DELETE ON instances_config WHEN OLD.key='volatile.eth0.last_state.vf.id' BEGIN SELECT RAISE(ABORT,'injected preclaim retirement failure'); END")
				}

				return err
			}))
			err := cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.RetireOVNNICPreclaim(ctx, instanceID, instanceUUID, "eth0", original)
			})
			if mode == "success" || mode == "committed-retirement-reconciliation" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}

			require.NoError(t, cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				var host, marker, sibling, foreign string
				err := tx.Tx().QueryRowContext(ctx, "SELECT COALESCE((SELECT value FROM instances_config WHERE key='volatile.eth0.host_name'),''), COALESCE((SELECT value FROM instances_config WHERE key='volatile.eth0.last_state.ovn.physical'),''),(SELECT value FROM instances_config WHERE key='volatile.sibling.host_name'),(SELECT value FROM instances_config WHERE key='user.keep')").Scan(&host, &marker, &sibling, &foreign)
				require.Equal(t, "sibling-vf", sibling)
				require.Equal(t, "foreign", foreign)
				if mode == "success" || mode == "committed-retirement-reconciliation" {
					require.Empty(t, host)
					require.Empty(t, marker)
					require.NoError(t, tx.EnsureOVNNICPreclaimRetired(ctx, instanceID, instanceUUID, "eth0"))
					require.NoError(t, tx.EnsureOVNNICCleanupComplete(ctx, 41))
				} else {
					require.NotEmpty(t, marker)
					require.Error(t, tx.EnsureOVNNICPreclaimRetired(ctx, instanceID, instanceUUID, "eth0"))
					if mode == "changed-allocation" {
						require.Equal(t, "replacement-vf", host)
					} else {
						require.Equal(t, "original-vf", host)
					}
				}

				return err
			}))
		})
	}
}

func TestOVNNICCleanupTwoNICProducerAdmission(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	instanceUUID := uuid.NewString()
	res, err := tx.Tx().ExecContext(ctx, "INSERT INTO instances(node_id,name,architecture,type,description,project_id) VALUES (?,'two-nic-start',1,0,'',(SELECT id FROM projects WHERE name='default'))", tx.GetNodeID())
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	_, err = tx.Tx().ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,'volatile.uuid',?)", id, instanceUUID)
	require.NoError(t, err)
	claims := map[string]map[string]string{}
	for _, device := range []string{"eth0", "eth1"} {
		require.NoError(t, tx.EnsureOVNNICCleanupStart(ctx, instanceUUID, device, 0, nil), "a sibling's live claim must not block a fresh device")
		raw, err := json.Marshal(map[string]any{"Version": 1, "NetworkID": 41, "SourceNodeID": tx.GetNodeID(), "InstanceUUID": instanceUUID, "InstanceID": id, "DeviceName": device, "Alias": "incus-ovn-" + uuid.NewString()})
		require.NoError(t, err)
		original := map[string]string{"host_name": "original-" + device, "last_state.ovn.host": string(raw)}
		claims[device] = original
		for key, value := range original {
			_, err = tx.Tx().ExecContext(ctx, "INSERT INTO instances_config(instance_id,key,value) VALUES (?,?,?)", id, "volatile."+device+"."+key, value)
			require.NoError(t, err)
		}

		require.NoError(t, tx.EnsureOVNNICCleanupStart(ctx, instanceUUID, device, 41, original), "publication must accept only this exact durable producer claim")
		require.Error(t, tx.EnsureOVNNICCleanupStart(ctx, instanceUUID, device, 0, nil), "a later Start cannot adopt the retained claim")
		require.Error(t, tx.EnsureOVNNICCleanupStart(ctx, instanceUUID, device, 42, original), "numeric replacement network cannot publish")
		require.Error(t, tx.EnsureOVNNICCleanupCompleteForInstance(ctx, instanceUUID), "empty cleanup rows do not acknowledge a live or partial producer allocation")
		require.Error(t, tx.EnsureOVNNICCleanupComplete(ctx, 41))
	}

	require.NoError(t, tx.EnsureOVNNICCleanupDebtCompleteForInstance(ctx, instanceUUID), "publication's debt-only gate does not certify terminal cleanup")
	require.NoError(t, tx.RetireOVNNICPreclaim(ctx, int(id), instanceUUID, "eth1", claims["eth1"]))
	require.NoError(t, tx.EnsureOVNNICCleanupStart(ctx, instanceUUID, "eth1", 0, nil), "positively proved pre-backend rollback permits a fresh retry")
	require.Error(t, tx.EnsureOVNNICCleanupCompleteForInstance(ctx, instanceUUID), "the first NIC remains owned")
	_, err = tx.Tx().ExecContext(ctx, "UPDATE instances_config SET value='replacement-generation' WHERE key='volatile.eth0.last_state.ovn.host'")
	require.NoError(t, err)
	require.Error(t, tx.EnsureOVNNICCleanupStart(ctx, instanceUUID, "eth0", 41, claims["eth0"]))
	require.Error(t, tx.RetireOVNNICPreclaim(ctx, int(id), instanceUUID, "eth0", claims["eth0"]))
	var current string
	require.NoError(t, tx.Tx().QueryRowContext(ctx, "SELECT value FROM instances_config WHERE key='volatile.eth0.last_state.ovn.host'").Scan(&current))
	require.Equal(t, "replacement-generation", current)
}

// A restoring member starts workloads and must capture a failed start's NIC cleanup.
func TestOVNNICCleanupCaptureSourceMemberState(t *testing.T) {
	cases := []struct {
		name    string
		state   int
		allowed bool
	}{
		{"created", db.ClusterMemberStateCreated, true},
		{"evacuating", db.ClusterMemberStateEvacuating, true},
		{"restoring", db.ClusterMemberStateRestoring, true},
		{"evacuated", db.ClusterMemberStateEvacuated, false},
		{"pending", db.ClusterMemberStatePending, false},
	}

	for _, c := range cases {
		state, allowed := c.state, c.allowed
		t.Run(c.name, func(t *testing.T) {
			cluster, cleanup := db.NewTestCluster(t)
			defer cleanup()
			err := cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				a, tokens := ovnCleanupFixture(t, tx)
				require.NoError(t, tx.UpdateNodeStatus(tx.GetNodeID(), state))
				return tx.CaptureOVNNICCleanup(ctx, a, tokens)
			})
			if allowed {
				require.NoError(t, err)
			} else {
				require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
			}
		})
	}
}

func TestOVNNICTransferSourceAnyMember(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	instanceUUID := "7e9f1fd8-4cb0-4bb3-9d79-3d4b8d1c2a10"
	exec := func(query string, args ...any) {
		err := cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			_, err := tx.Tx().ExecContext(ctx, query, args...)
			return err
		})
		require.NoError(t, err)
	}

	check := func() error {
		return cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.EnsureOVNNICTransferSource(ctx, instanceUUID)
		})
	}

	require.NoError(t, check())
	// Another instance's debt does not block this transfer.
	exec("INSERT INTO networks_ovn_nic_cleanup (generation, source_node_id, instance_uuid, device_name, version, network_ids, payload) VALUES ('g-other', 77, 'other', 'eth0', 1, '[1]', '{}')")
	require.NoError(t, check())
	// Unacknowledged debt of the previous member blocks it, unlike the local-only debt check.
	exec("INSERT INTO networks_ovn_nic_cleanup (generation, source_node_id, instance_uuid, device_name, version, network_ids, payload) VALUES ('g-source', 77, ?, 'eth0', 1, '[1]', '{}')", instanceUUID)
	require.Error(t, check())
	err := cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.EnsureOVNNICCleanupDebtCompleteForInstance(ctx, instanceUUID)
	})
	require.NoError(t, err)
	exec("UPDATE networks_ovn_nic_cleanup SET completed=1 WHERE generation='g-source'")
	require.NoError(t, check())
}

func TestOVNNetworkCleanupFreeAnyMember(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	exec := func(query string, args ...any) {
		err := cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			_, err := tx.Tx().ExecContext(ctx, query, args...)
			return err
		})
		require.NoError(t, err)
	}

	check := func(id int64) error {
		return cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.EnsureOVNNetworkCleanupFree(ctx, id)
		})
	}

	exec("INSERT INTO nodes (id, name, address, schema, api_extensions, arch, description) VALUES (77, 'other', '10.0.0.77', 1, 1, 1, '')")
	exec("INSERT INTO networks (id, project_id, name, description, state, type) VALUES (61, 1, 'debt', '', 1, 0), (62, 1, 'clean', '', 1, 0)")
	require.NoError(t, check(61))
	exec("INSERT INTO networks_ovn_nic_cleanup (generation, source_node_id, instance_uuid, device_name, version, network_ids, payload) VALUES ('g-debt', 77, 'u', 'eth0', 1, '[61]', '{}')")
	exec("INSERT INTO networks_ovn_nic_cleanup_networks (generation, source_node_id, network_id) VALUES ('g-debt', 77, 61)")
	// Another member's pending debt refuses deletion before effects; other networks are unaffected.
	require.Error(t, check(61))
	require.NoError(t, check(62))
}
