package drivers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	cowsqlDriver "github.com/cowsql/go-cowsql/driver"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"

	clusterConfig "github.com/lxc/incus/v7/internal/server/cluster/config"
	"github.com/lxc/incus/v7/internal/server/cluster/request"
	"github.com/lxc/incus/v7/internal/server/db"
	dbCluster "github.com/lxc/incus/v7/internal/server/db/cluster"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/network/acl"
	addressset "github.com/lxc/incus/v7/internal/server/network/address-set"
	networkOVN "github.com/lxc/incus/v7/internal/server/network/ovn"
	ovnModel "github.com/lxc/incus/v7/internal/server/network/ovn/schema/ovn-nb"
	"github.com/lxc/incus/v7/internal/server/operations"
	"github.com/lxc/incus/v7/internal/server/project"
	"github.com/lxc/incus/v7/internal/server/state"
	storagePools "github.com/lxc/incus/v7/internal/server/storage"
	"github.com/lxc/incus/v7/internal/server/sys"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
)

func retirementStringMap(input map[string]string) ovsdb.OvsMap {
	wire := ovsdb.OvsMap{GoMap: map[any]any{}}
	for k, v := range input {
		wire.GoMap[k] = v
	}

	return wire
}

func retirementTestNB(t *testing.T) (*networkOVN.NB, ovsdbClient.Client) {
	t.Helper()
	dir, err := os.MkdirTemp("", "rt-")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, os.RemoveAll(dir))
		_, err := os.Stat(dir)
		require.True(t, os.IsNotExist(err))
		t.Logf("owned fixture root removed: %s", dir)
	})
	schema, err := json.Marshal(ovnModel.Schema())
	require.NoError(t, err)
	schemaPath := filepath.Join(dir, "schema")
	require.NoError(t, os.WriteFile(schemaPath, schema, 0o600))
	dbPath := filepath.Join(dir, "db")
	output, err := exec.Command("/usr/bin/ovsdb-tool", "create", dbPath, schemaPath).CombinedOutput()
	require.NoError(t, err, string(output))
	socket := filepath.Join(dir, "socket")
	logPath := filepath.Join(dir, "process-log")
	logFile, err := os.Create(logPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, logFile.Close()) })
	logOutput := func() string {
		data, err := os.ReadFile(logPath)
		require.NoError(t, err)
		return string(data)
	}

	cmd := exec.Command("/usr/sbin/ovsdb-server", dbPath, "--remote=punix:"+socket, "--unixctl="+filepath.Join(dir, "control"), "--pidfile="+filepath.Join(dir, "pid"), "--log-file="+filepath.Join(dir, "log"), "--no-chdir")
	cmd.Stdout, cmd.Stderr = logFile, logFile
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		require.NoError(t, cmd.Process.Signal(syscall.SIGTERM))
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			require.Error(t, err, logOutput())
			require.Equal(t, syscall.SIGTERM, cmd.ProcessState.Sys().(syscall.WaitStatus).Signal())
			t.Logf("owned ovsdb-server PID=%d reaped after requested SIGTERM; root path %s", cmd.Process.Pid, dir)
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Error("owned ovsdb-server required forced teardown")
		}

		require.NotNil(t, cmd.ProcessState)
	})
	require.Eventually(t, func() bool { _, err := os.Stat(socket); return err == nil }, 5*time.Second, 10*time.Millisecond, logOutput())
	m, err := ovnModel.FullDatabaseModel()
	require.NoError(t, err)
	discard := logr.Discard()
	raw, err := ovsdbClient.NewOVSDBClient(m, ovsdbClient.WithEndpoint("unix:"+socket), ovsdbClient.WithLogger(&discard))
	require.NoError(t, err)
	t.Cleanup(raw.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, raw.Connect(ctx))
	retirementExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "NB_Global", Row: ovsdb.Row{"external_ids": retirementStringMap(map[string]string{})}})
	nb, err := networkOVN.NewNB("unix:"+socket, "", "", "", "reference-fixture")
	require.NoError(t, err)

	t.Cleanup(nb.Close)
	t.Logf("owned Unix-only NB fixture pid=%d root=%s db=%s schema=%s schema-sha256=%x", cmd.Process.Pid, nb.BackendID(), dbPath, schemaPath, sha256.Sum256(schema))
	return nb, raw
}

func retirementExec(t *testing.T, c ovsdbClient.Client, ops ...ovsdb.Operation) []ovsdb.OperationResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := c.Transact(ctx, ops...)
	require.NoError(t, err)
	_, err = ovsdb.CheckOperationResults(result, ops)
	require.NoError(t, err, "results: %#v", result)
	return result
}

type retirementBackupPool struct {
	storagePools.Pool
	run func(instance.Instance) error
}

func (p retirementBackupPool) UpdateInstanceBackupFile(inst instance.Instance, _ bool, _ *operations.Operation) error {
	return p.run(inst)
}

func TestOrdinaryOVNProfileRetirementBothDriverEntries(t *testing.T) {
	for _, kind := range []instancetype.Type{instancetype.Container, instancetype.VM} {
		for _, outcome := range []string{"success", "sql-failure", "backend-root-replacement"} {
			t.Run(kind.String()+"/"+outcome, func(t *testing.T) {
				ctx := context.Background()
				nb, raw := retirementTestNB(t)
				c := retirementTestCluster(t)
				cfg := map[string]string{"network": "none", "ipv4.address": "none", "ipv6.address": "none", "bridge.mtu": "1500"}
				nic := deviceConfig.Device{"type": "nic", "network": "retirement", "hwaddr": "00:11:22:33:44:55", "security.acls": "original"}
				before := api.ProfilePut{Config: map[string]string{}, Devices: map[string]map[string]string{"eth0": maps.Clone(nic)}}
				instanceUUID := uuid.NewString()
				local := map[string]string{"volatile.uuid": instanceUUID}
				var networkID, aclID, instanceID, profileID int64
				var member string
				var ownerProject *api.Project
				var global *clusterConfig.Config
				require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					var err error
					networkID, err = tx.CreateNetwork(ctx, "default", "retirement", "", db.NetworkTypeOVN, cfg)
					if err != nil {
						return err
					}

					require.NoError(t, tx.NetworkCreated("default", "retirement"))
					aclID, err = dbCluster.CreateNetworkACL(ctx, tx.Tx(), dbCluster.NetworkACL{Project: "default", Name: "original"})
					if err != nil {
						return err
					}

					profileID, err = dbCluster.CreateProfile(ctx, tx.Tx(), dbCluster.Profile{Project: "default", Name: "retirement"})
					if err != nil {
						return err
					}

					devices, err := dbCluster.APIToDevices(before.Devices)
					if err != nil {
						return err
					}

					require.NoError(t, dbCluster.UpdateProfileDevices(ctx, tx.Tx(), profileID, devices))
					require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=?`, tx.GetNodeID()).Scan(&member))
					instanceID, err = dbCluster.CreateInstance(ctx, tx.Tx(), dbCluster.Instance{Project: "default", Name: "retirement", Node: member, Type: kind, Architecture: 2, CreationDate: time.Now()})
					if err != nil {
						return err
					}

					require.NoError(t, dbCluster.UpdateInstanceConfig(ctx, tx.Tx(), instanceID, local))
					require.NoError(t, dbCluster.UpdateInstanceProfiles(ctx, tx.Tx(), int(instanceID), "default", []string{"retirement"}))
					p, err := dbCluster.GetProject(ctx, tx.Tx(), "default")
					if err != nil {
						return err
					}

					ownerProject, err = p.ToAPI(ctx, tx.Tx())
					if err != nil {
						return err
					}

					global, err = clusterConfig.Load(ctx, tx)
					return err
				}))
				require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					referenceErr := tx.BeginOVNReferenceActivation(ctx)
					if referenceErr != nil {
						return referenceErr
					}

					return tx.BindOVNReferenceRoot(ctx, nb.BackendID())
				}))
				s := &state.State{ShutdownCtx: ctx, DB: &db.DB{Cluster: c}, OS: &sys.OS{LxcPath: filepath.Join(os.Getenv("TMPDIR"), "native-lxc-unavailable")}, ServerName: member, GlobalConfig: global}

				ovnCalls := 0
				s.OVN = func() (*networkOVN.NB, *networkOVN.SB, error) {
					ovnCalls++
					if outcome != "success" && ovnCalls > 2 {
						return nil, nil, errors.New("Owned fixture backend refused rollback acquisition")
					}

					return nb, &networkOVN.SB{}, nil
				}

				sw := fmt.Sprintf("incus-net%d-ls-int", networkID)
				port := fmt.Sprintf("incus-net%d-instance-%s-eth0", networkID, instanceUUID)
				retirementExec(t, raw,
					ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "port", Row: ovsdb.Row{"name": port, "enabled": ovsdb.OvsSet{GoSet: []any{false}}, "addresses": ovsdb.OvsSet{GoSet: []any{"00:11:22:33:44:55"}}, "external_ids": retirementStringMap(map[string]string{"incus_switch": sw})}},
					ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "DNS", UUIDName: "dns", Row: ovsdb.Row{"external_ids": retirementStringMap(map[string]string{"incus_switch_port": port})}},
					ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch", Row: ovsdb.Row{"name": sw, "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "port"}}}, "dns_records": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "dns"}}}}},
					ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Port_Group", Row: ovsdb.Row{"name": fmt.Sprintf("incus_acl%d_all", aclID), "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "port"}}}, "external_ids": retirementStringMap(map[string]string{"incus_project_id": "1"})}})
				p := networkOVN.NICConfigPublication{Generation: uuid.NewString(), NetworkID: networkID, ProjectID: 1, InstanceUUID: instanceUUID, Device: "eth0", Source: member, Phase: "add", Input: networkOVN.NICConfigInputs(nic), ACLIDs: map[string]int64{"original": aclID}}
				require.NoError(t, nb.PublishNICConfig(ctx, networkOVN.OVNSwitch(sw), networkOVN.OVNSwitchPort(port), p))
				proposed := api.ProfilePut{Config: map[string]string{}, Devices: map[string]map[string]string{}}
				publish := func() error {
					return c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
						return project.CommitProfileNetworkUpdate(ctx, tx, "default", "retirement", profileID, before, proposed)
					})
				}

				require.Error(t, publish())
				backupCalled := false
				pool := retirementBackupPool{run: func(inst instance.Instance) error {
					backupCalled = true
					require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
						profiles, err := dbCluster.GetInstanceProfiles(ctx, tx.Tx(), int(instanceID))
						if err != nil {
							return err
						}

						require.Empty(t, profiles)
						var count int
						require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT count(*) FROM networks_ovn_operations`).Scan(&count))
						require.Equal(t, 1, count)
						return nil
					}))
					rows := retirementExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{}})
					require.Empty(t, rows[0].Rows)
					return nil
				}}
				base := common{state: s, architecture: 2, dbType: kind, id: int(instanceID), name: "retirement", node: member, project: *ownerProject, localConfig: maps.Clone(local), expandedConfig: maps.Clone(local), localDevices: deviceConfig.Devices{}, expandedDevices: deviceConfig.Devices{"eth0": nic.Clone()}, profiles: []api.Profile{{Name: "retirement", Project: "default", ProfilePut: before}}, logger: logger.AddContext(logger.Ctx{"fixture": "retirement"}), storagePool: pool}
				args := db.InstanceArgs{Project: "default", Architecture: 2, Config: maps.Clone(local), Devices: deviceConfig.Devices{}, Profiles: []api.Profile{}}
				if outcome == "sql-failure" {
					require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
						_, err := tx.Tx().ExecContext(ctx, `CREATE TRIGGER retirement_failure BEFORE UPDATE OF description ON instances BEGIN SELECT RAISE(FAIL,'owned fixture SQL commit refused'); END`)
						return err
					}))
				}

				if outcome == "backend-root-replacement" {
					retirementExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "NB_Global", Where: []ovsdb.Condition{{Column: "external_ids", Function: ovsdb.ConditionIncludes, Value: retirementStringMap(map[string]string{})}}}, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "NB_Global", Row: ovsdb.Row{"external_ids": retirementStringMap(map[string]string{})}})
				}

				var err error
				if kind == instancetype.Container {
					d := &lxc{common: base, fromHook: true}
					err = d.Update(args, false)
				} else {
					d := &qemu{common: base}
					err = d.Update(args, false)
				}

				if outcome == "success" {
					require.NoError(t, err)
					require.True(t, backupCalled)
					require.NoError(t, publish())
					require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
						var count int
						require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT count(*) FROM networks_ovn_operations`).Scan(&count))
						require.Zero(t, count)
						return nil
					}))
				} else {
					require.Error(t, err)
					require.False(t, backupCalled)
					require.Error(t, publish())
					require.Contains(t, err.Error(), "reservations are retained")
					require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
						var count int
						require.NoError(t, tx.Tx().QueryRowContext(ctx, `SELECT count(*) FROM networks_ovn_operations`).Scan(&count))
						require.Equal(t, 1, count)
						return nil
					}))
				}

				t.Logf("Actual %s.Update entry: %s; literal stopped hardware state and backup seam, private SQL/NB effects only", kind.String(), outcome)
			})
		}
	}
}

func retirementTestCluster(t *testing.T) *db.Cluster {
	t.Helper()
	dir, store, serverCleanup := db.NewTestCowsqlServer(t)
	members, err := store.Get(context.Background())
	require.NoError(t, err)
	c, err := db.OpenCluster(context.Background(), "test.db", store, "1", dir, 5*time.Second, cowsqlDriver.WithDialFunc(func(ctx context.Context, address string) (net.Conn, error) { return net.Dial("unix", address) }))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, c.Close())
		serverCleanup()
		_, err := os.Stat(dir)
		require.True(t, os.IsNotExist(err))
		for _, member := range members {
			conn, err := net.Dial("unix", member.Address)
			if conn != nil {
				_ = conn.Close()
			}

			require.Error(t, err)
		}

		t.Logf("owned in-process cowsql closed; Unix endpoints refused new connections and root removed: %s", dir)
	})
	t.Logf("owned in-process cowsql PID=%d root=%s database=test.db Unix-members=%v", os.Getpid(), dir, members)
	return c
}

func TestPublicSharedResourceCallersRetainOldPhysicalReferences(t *testing.T) {
	ctx := context.Background()
	nb, raw := retirementTestNB(t)
	c := retirementTestCluster(t)
	var aclID, setID int64
	require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		aclID, err = dbCluster.CreateNetworkACL(ctx, tx.Tx(), dbCluster.NetworkACL{Project: "default", Name: "original"})
		if err != nil {
			return err
		}

		setID, err = dbCluster.CreateNetworkAddressSet(ctx, tx.Tx(), dbCluster.NetworkAddressSet{Project: "default", Name: "original", Addresses: []string{"192.0.2.9"}})
		return err
	}))
	require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		referenceErr := tx.BeginOVNReferenceActivation(ctx)
		if referenceErr != nil {
			return referenceErr
		}

		return tx.BindOVNReferenceRoot(ctx, nb.BackendID())
	}))
	s := &state.State{ShutdownCtx: ctx, DB: &db.DB{Cluster: c}, OVN: func() (*networkOVN.NB, *networkOVN.SB, error) { return nb, &networkOVN.SB{}, nil }}
	// There are deliberately zero current OVN networks or NIC consumers in SQL.
	retirementExec(t, raw,
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch_Port", UUIDName: "port", Row: ovsdb.Row{"name": "old-disabled", "enabled": ovsdb.OvsSet{GoSet: []any{false}}}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch", Row: ovsdb.Row{"name": "old-switch", "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "port"}}}}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Port_Group", Row: ovsdb.Row{"name": fmt.Sprintf("incus_acl%d_all", aclID), "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "port"}}}, "external_ids": retirementStringMap(map[string]string{"incus_project_id": "1"})}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "ACL", UUIDName: "subject", Row: ovsdb.Row{"direction": "to-lport", "priority": 1, "action": "drop", "match": fmt.Sprintf("ip4.src == $incus_set%d_ip4", setID)}},
		ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Port_Group", Row: ovsdb.Row{"name": "foreign-subject-parent", "acls": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "subject"}}}}})
	a, err := acl.LoadByName(s, "default", "original")
	require.NoError(t, err)
	set, err := addressset.LoadByName(s, "default", "original")
	require.NoError(t, err)
	require.ErrorIs(t, a.Delete(), networkOVN.ErrPhysicalReference)
	require.ErrorIs(t, a.Rename("renamed"), networkOVN.ErrPhysicalReference)
	require.Error(t, a.Update(&api.NetworkACLPut{Description: "unsafe", Config: map[string]string{}}, request.ClientTypeNormal, nil))
	require.ErrorIs(t, set.Delete(), networkOVN.ErrPhysicalReference)
	require.ErrorIs(t, set.Rename("renamed"), networkOVN.ErrPhysicalReference)
	require.Error(t, set.Update(&api.NetworkAddressSetPut{Description: "unsafe", Addresses: []string{"192.0.2.19"}, Config: map[string]string{}}, request.ClientTypeNormal, nil))
	a, err = acl.LoadByName(s, "default", "original")
	require.NoError(t, err)
	require.Empty(t, a.Info().Description)
	set, err = addressset.LoadByName(s, "default", "original")
	require.NoError(t, err)
	require.Empty(t, set.Info().Description)
	require.Equal(t, []string{"192.0.2.9"}, set.Info().Addresses)
	t.Log("Actual public ACL/address-set Delete, Rename and Update refused old disabled/subject-only backend references with zero current SQL OVN consumers; catalog remained unchanged")
	// Only this owned fixture is emptied, then ordinary deletion is admitted.
	for _, table := range []string{"Port_Group", "ACL"} {
		retirementExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: table, Where: []ovsdb.Condition{{Column: "external_ids", Function: ovsdb.ConditionIncludes, Value: retirementStringMap(map[string]string{})}}})
	}

	require.NoError(t, a.Delete())
	require.NoError(t, set.Delete())
}

func TestPublicResourceFreshNoOVNServiceAndUnknownHistory(t *testing.T) {
	for _, history := range []string{"fresh", "upgrade-unknown", "activation-intent", "active-unavailable", "mixed-versions", "mixed-api", "malformed", "missing"} {
		t.Run(history, func(t *testing.T) {
			ctx := context.Background()
			c := retirementTestCluster(t)
			require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				_, err := dbCluster.CreateNetworkACL(ctx, tx.Tx(), dbCluster.NetworkACL{Project: "default", Name: "original"})
				if err != nil {
					return err
				}

				_, err = dbCluster.CreateNetworkAddressSet(ctx, tx.Tx(), dbCluster.NetworkAddressSet{Project: "default", Name: "original", Addresses: []string{"192.0.2.9"}})
				return err
			}))
			calls := 0
			s := &state.State{ShutdownCtx: ctx, DB: &db.DB{Cluster: c}, OVN: func() (*networkOVN.NB, *networkOVN.SB, error) {
				calls++
				return nil, nil, errors.New("owned fixture has no OVN service")
			}}
			require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				var err error
				switch history {
				case "upgrade-unknown":
					_, err = tx.Tx().ExecContext(ctx, `UPDATE ovn_reference_applicability SET state='unknown'`)
				case "activation-intent":
					err = tx.BeginOVNReferenceActivation(ctx)
				case "active-unavailable":
					err = tx.BeginOVNReferenceActivation(ctx)
					if err == nil {
						err = tx.BindOVNReferenceRoot(ctx, uuid.NewString())
					}

				case "malformed":
					_, err = tx.Tx().ExecContext(ctx, `UPDATE ovn_reference_applicability SET state='active',nb_root='invalid'`)
				case "mixed-api":
					_, err = tx.Tx().ExecContext(ctx, `UPDATE nodes SET api_extensions=api_extensions-1`)
				case "mixed-versions":
					_, err = tx.Tx().ExecContext(ctx, `UPDATE nodes SET schema=85`)
				case "missing":
					_, err = tx.Tx().ExecContext(ctx, `DELETE FROM ovn_reference_applicability`)
				}

				return err
			}))
			a, err := acl.LoadByName(s, "default", "original")
			require.NoError(t, err)
			set, err := addressset.LoadByName(s, "default", "original")
			require.NoError(t, err)
			au := a.Update(&api.NetworkACLPut{Description: "ordinary", Config: map[string]string{}}, request.ClientTypeNormal, nil)
			su := set.Update(&api.NetworkAddressSetPut{Description: "ordinary", Addresses: []string{"192.0.2.19"}, Config: map[string]string{}}, request.ClientTypeNormal, nil)
			createdACL := acl.Create(s, "default", &api.NetworkACLsPost{NetworkACLPost: api.NetworkACLPost{Name: "new-resource"}})
			createdSet := addressset.Create(s, "default", &api.NetworkAddressSetsPost{NetworkAddressSetPost: api.NetworkAddressSetPost{Name: "new-resource"}})
			// A cluster that never bound an OVN root and has no OVN network does not need OVN.
			if history == "fresh" || history == "upgrade-unknown" || history == "activation-intent" {
				require.NoError(t, createdACL)
				require.NoError(t, createdSet)
				require.NoError(t, au)
				require.NoError(t, su)
				require.NoError(t, a.Rename("renamed"))
				require.NoError(t, set.Rename("renamed"))
				require.NoError(t, a.Delete())
				require.NoError(t, set.Delete())
				require.Zero(t, calls)
			} else {
				require.Error(t, createdACL)
				require.Error(t, createdSet)
				require.Error(t, au)
				require.Error(t, su)
				require.Error(t, a.Rename("renamed"))
				require.Error(t, set.Rename("renamed"))
				require.Error(t, a.Delete())
				require.Error(t, set.Delete())
			}

			t.Logf("Actual ACL/address-set Create/Update/Rename/Delete %s with unavailable OVN service; backend acquisition calls=%d", history, calls)
		})
	}
}
