package network

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/cowsql/go-cowsql"
	cowsqlDriver "github.com/cowsql/go-cowsql/driver"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/bgp"
	clusterConfig "github.com/lxc/incus/v7/internal/server/cluster/config"
	"github.com/lxc/incus/v7/internal/server/cluster/request"
	"github.com/lxc/incus/v7/internal/server/db"
	dbCluster "github.com/lxc/incus/v7/internal/server/db/cluster"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/endpoints"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/network/acl"
	networkOVN "github.com/lxc/incus/v7/internal/server/network/ovn"
	ovnModel "github.com/lxc/incus/v7/internal/server/network/ovn/schema/ovn-nb"
	ovsClient "github.com/lxc/incus/v7/internal/server/network/ovs"
	ovsModel "github.com/lxc/incus/v7/internal/server/network/ovs/schema/ovs"
	"github.com/lxc/incus/v7/internal/server/project"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/internal/server/sys"
	"github.com/lxc/incus/v7/shared/api"
	localtls "github.com/lxc/incus/v7/shared/tls"
)

func callersStringMap(input map[string]string) ovsdb.OvsMap {
	wire := ovsdb.OvsMap{GoMap: map[any]any{}}
	for k, v := range input {
		wire.GoMap[k] = v
	}

	return wire
}

func callersTestNB(t *testing.T) (*networkOVN.NB, ovsdbClient.Client, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "rt-")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, os.RemoveAll(dir))
		_, err := os.Stat(dir)
		require.True(t, os.IsNotExist(err))
		t.Logf("owned fixture root removed: %s", dir)
	})
	schema, err := os.ReadFile("/usr/share/ovn/ovn-nb.ovsschema")
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
	callersExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "NB_Global", Row: ovsdb.Row{"external_ids": callersStringMap(map[string]string{})}})
	nb, err := networkOVN.NewNB("unix:"+socket, "", "", "", "reference-fixture")
	require.NoError(t, err)

	t.Cleanup(nb.Close)
	t.Logf("owned Unix-only NB fixture pid=%d root=%s db=%s schema=%s schema-sha256=%x", cmd.Process.Pid, nb.BackendID(), dbPath, schemaPath, sha256.Sum256(schema))
	return nb, raw, "unix:" + socket
}

func callersTestOVS(t *testing.T) (*ovsClient.VSwitch, ovsdbClient.Client) {
	t.Helper()
	dir, err := os.MkdirTemp("", "rt-")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, os.RemoveAll(dir))
		_, err := os.Stat(dir)
		require.True(t, os.IsNotExist(err))
		t.Logf("owned fixture root removed: %s", dir)
	})
	schema, err := json.Marshal(ovsModel.Schema())
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
	m, err := ovsModel.FullDatabaseModel()
	require.NoError(t, err)
	discard := logr.Discard()
	raw, err := ovsdbClient.NewOVSDBClient(m, ovsdbClient.WithEndpoint("unix:"+socket), ovsdbClient.WithLogger(&discard))
	require.NoError(t, err)
	t.Cleanup(raw.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, raw.Connect(ctx))
	callersExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Open_vSwitch", Row: ovsdb.Row{"external_ids": callersStringMap(map[string]string{"system-id": uuid.NewString()})}})
	nb, err := ovsClient.NewVSwitch("unix:"+socket, "reference-fixture")
	require.NoError(t, err)

	t.Cleanup(nb.Close)
	t.Logf("owned Unix-only OVS fixture pid=%d root=%s db=%s schema=%s schema-sha256=%x", cmd.Process.Pid, nb.BackendID(), dbPath, schemaPath, sha256.Sum256(schema))
	return nb, raw
}

func callersExec(t *testing.T, c ovsdbClient.Client, ops ...ovsdb.Operation) []ovsdb.OperationResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := c.Transact(ctx, ops...)
	require.NoError(t, err)
	_, err = ovsdb.CheckOperationResults(result, ops)
	require.NoError(t, err, "results: %#v", result)
	return result
}

func callersTestCluster(t *testing.T) *db.Cluster {
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

func TestActualNICProducerReplayAndRetirementPrivateBackend(t *testing.T) {
	for _, stopMode := range []string{"completed-producer", "pending-source-stop"} {
		t.Run(stopMode, func(t *testing.T) {
			ctx := context.Background()
			c := callersTestCluster(t)
			nb, raw, _ := callersTestNB(t)
			vswitch, ovsRaw := callersTestOVS(t)
			cfg := map[string]string{"network": "none", "ipv4.address": "192.0.2.1/24", "ipv4.dhcp": "false", "ipv6.address": "none", "bridge.mtu": "1500", "dns.mode": "none"}
			nic := deviceConfig.Device{"type": "nic", "network": "caller", "hwaddr": "00:11:22:33:44:55", "ipv4.address": "none", "ipv6.address": "none"}
			instanceUUID := uuid.NewString()
			var id, instID, aclA, aclB int64
			var member string
			var global *clusterConfig.Config
			require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				var err error
				aclA, err = dbCluster.CreateNetworkACL(ctx, tx.Tx(), dbCluster.NetworkACL{Project: "default", Name: "network-a"})
				if err != nil {
					return err
				}

				aclB, err = dbCluster.CreateNetworkACL(ctx, tx.Tx(), dbCluster.NetworkACL{Project: "default", Name: "network-b"})
				if err != nil {
					return err
				}

				id, err = tx.CreateNetwork(ctx, "default", "caller", "", db.NetworkTypeOVN, cfg)
				if err != nil {
					return err
				}

				err = tx.NetworkCreated("default", "caller")
				if err != nil {
					return err
				}

				err = tx.NetworkNodeCreated(id)
				if err != nil {
					return err
				}

				err = tx.Tx().QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=?`, tx.GetNodeID()).Scan(&member)
				if err != nil {
					return err
				}

				instID, err = dbCluster.CreateInstance(ctx, tx.Tx(), dbCluster.Instance{Project: "default", Name: "caller", Node: member, Type: instancetype.Container, Architecture: 2, CreationDate: time.Now()})
				if err != nil {
					return err
				}

				err = dbCluster.UpdateInstanceConfig(ctx, tx.Tx(), instID, map[string]string{"volatile.uuid": instanceUUID})
				if err != nil {
					return err
				}

				devices, err := dbCluster.APIToDevices(map[string]map[string]string{"eth0": maps.Clone(nic)})
				if err != nil {
					return err
				}

				err = dbCluster.UpdateInstanceDevices(ctx, tx.Tx(), instID, devices)
				if err != nil {
					return err
				}

				global, err = clusterConfig.Load(ctx, tx)
				return err
			}))
			s := &state.State{ShutdownCtx: ctx, BGP: bgp.NewServer(), Endpoints: &endpoints.Endpoints{}, ServerCert: func() *localtls.CertInfo { return nil }, OVS: func() (*ovsClient.VSwitch, error) { return vswitch, nil }, DB: &db.DB{Cluster: c}, OS: &sys.OS{VarDir: t.TempDir(), LxcPath: filepath.Join(os.Getenv("TMPDIR"), "unavailable-native-lxc")}, ServerName: member, GlobalConfig: global}
			s.OVN = func() (*networkOVN.NB, *networkOVN.SB, error) {
				err := c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					referenceErr := tx.BeginOVNReferenceActivation(ctx)
					if referenceErr != nil {
						return referenceErr
					}

					return tx.BindOVNReferenceRoot(ctx, nb.BackendID())
				})
				return nb, &networkOVN.SB{}, err
			}

			loaded, err := LoadByName(s, "default", "caller")
			require.NoError(t, err)
			n, validNetwork := loaded.(*ovn)
			require.True(t, validNetwork)
			n.setLocalState(ovnLocalState{started: true, config: maps.Clone(cfg)})
			t.Cleanup(func() { n.setLocalState(ovnLocalState{}) })
			sw := fmt.Sprintf("incus-net%d-ls-int", id)
			callersExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Logical_Switch", Row: ovsdb.Row{"name": sw}}, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Port_Group", Row: ovsdb.Row{"name": fmt.Sprintf("incus_net%d", id), "external_ids": callersStringMap(map[string]string{"incus_project_id": "1"})}}, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Address_Set", Row: ovsdb.Row{"name": fmt.Sprintf("incus_net%d_routes_ip4", id)}}, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Address_Set", Row: ovsdb.Row{"name": fmt.Sprintf("incus_net%d_routes_ip6", id)}})
			require.NoError(t, n.setup(true))
			require.NoError(t, n.InstanceDevicePortAdd(instanceUUID, "eth0", nic))
			targets := func() map[networkOVN.OVNSwitchPort]networkOVN.NICConfigPublication {
				var targets map[networkOVN.OVNSwitchPort]networkOVN.NICConfigPublication
				require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					var err error
					targets, err = project.OVNNICReplayTargets(ctx, tx, "default", id)
					return err
				}))
				return targets
			}
			// Shared Update admits the disabled Add producer while physical Delete remains strict.
			changed := maps.Clone(cfg)
			changed["dns.domain"] = "changed.example"
			changed["security.acls"] = "network-a"
			require.NoError(t, n.Update(api.NetworkPut{Config: changed}, member, request.ClientTypeNormal))
			require.ErrorIs(t, n.Delete(request.ClientTypeNormal), networkOVN.ErrPhysicalReference)
			_, _, err = n.InstanceDevicePortStart(&OVNInstanceNICSetupOpts{InstanceUUID: instanceUUID, DeviceName: "eth0", DeviceConfig: nic, DNSName: "caller"}, nil)
			require.NoError(t, err)
			active, err := nb.CheckNetworkNICReplay(ctx, id, string(n.getRouterIntPortName()), targets())
			require.NoError(t, err)
			require.Len(t, active, 1)
			if stopMode == "pending-source-stop" {
				callersExec(t, ovsRaw,
					ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Interface", UUIDName: "iface", Row: ovsdb.Row{"name": "caller-host", "external_ids": callersStringMap(map[string]string{"iface-id": string(n.getInstanceDevicePortName(instanceUUID, "eth0"))})}},
					ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Port", UUIDName: "port", Row: ovsdb.Row{"name": "caller-host", "interfaces": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "iface"}}}}},
					ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "Bridge", UUIDName: "bridge", Row: ovsdb.Row{"name": "caller-bridge", "ports": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "port"}}}}},
					ovsdb.Operation{Op: ovsdb.OperationUpdate, Table: "Open_vSwitch", Where: []ovsdb.Condition{{Column: "external_ids", Function: ovsdb.ConditionIncludes, Value: callersStringMap(map[string]string{})}}, Row: ovsdb.Row{"bridges": ovsdb.OvsSet{GoSet: []any{ovsdb.UUID{GoUUID: "bridge"}}}}})
				ovsPlan, err := vswitch.CaptureNICPortCleanup(ctx, "caller-bridge", "caller-host", string(n.getInstanceDevicePortName(instanceUUID, "eth0")))
				require.NoError(t, err)
				release, token, err := AcquireOVNOperation(s, n.Project(), n.Name(), "nic")
				require.NoError(t, err)
				n.ovnOperationToken = token
				require.NoError(t, n.InstanceDevicePortStop("", &OVNInstanceNICStopOpts{InstanceUUID: instanceUUID, InstanceID: int(instID), DeviceName: "eth0", DeviceConfig: nic, OVS: &ovsPlan}))
				n.ovnOperationToken = ""
				require.NoError(t, release())
			} else {
				require.NoError(t, nb.UpdateLogicalSwitchPortEnabled(ctx, n.getInstanceDevicePortName(instanceUUID, "eth0"), false))
			}

			portName := string(n.getInstanceDevicePortName(instanceUUID, "eth0"))
			readPort := func() ovsdb.Row {
				result := callersExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Logical_Switch_Port", Where: []ovsdb.Condition{{Column: "name", Function: ovsdb.ConditionEqual, Value: portName}}})
				require.Len(t, result[0].Rows, 1)
				return result[0].Rows[0]
			}

			stoppedPort := readPort()
			for _, targetNode := range []string{"", member} {
				changed["dns.domain"] = "changed" + targetNode + ".example"
				changed["security.acls"] = "network-b"
				changed["security.acls.default.ingress.action"] = "allow"
				release, token, err := AcquireOVNOperation(s, n.Project(), n.Name(), "update")
				require.NoError(t, err)
				n.ovnOperationToken = token
				updateErr := n.Update(api.NetworkPut{Config: maps.Clone(changed)}, targetNode, request.ClientTypeNormal)
				n.ovnOperationToken = ""
				require.NoError(t, release())
				require.NoError(t, updateErr)
				require.Equal(t, stoppedPort["enabled"], readPort()["enabled"])
				require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					_, info, _, err := tx.GetNetworkInAnyState(ctx, "default", "caller")
					require.NoError(t, err)
					require.Equal(t, changed, map[string]string(info.Config))
					return err
				}))
			}

			stoppedTargets, err := nb.CheckNetworkNICReplay(ctx, id, string(n.getRouterIntPortName()), targets())
			require.NoError(t, err)
			require.Equal(t, "start", stoppedTargets[networkOVN.OVNSwitchPort(portName)].Publication.Phase)
			require.False(t, stoppedTargets[networkOVN.OVNSwitchPort(portName)].Enabled)
			require.Equal(t, map[string]int64{"network-b": aclB}, stoppedTargets[networkOVN.OVNSwitchPort(portName)].Publication.ACLIDs)
			groups := callersExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "Port_Group", Where: []ovsdb.Condition{}})[0].Rows
			for _, group := range groups {
				if group["name"] == string(acl.OVNACLDirectionalPortGroups(aclB).IngressReversed) || group["name"] == string(acl.OVNACLDirectionalPortGroups(aclB).All) {
					require.Contains(t, fmt.Sprint(group["ports"]), stoppedTargets[networkOVN.OVNSwitchPort(portName)].Publication.PortUUID)
				}

				if group["name"] == string(acl.OVNACLDirectionalPortGroups(aclA).All) {
					require.NotContains(t, fmt.Sprint(group["ports"]), stoppedTargets[networkOVN.OVNSwitchPort(portName)].Publication.PortUUID)
				}
			}

			a, err := acl.LoadByName(s, "default", "network-b")
			require.NoError(t, err)
			var aclRelease func() error
			require.NoError(t, a.Update(&api.NetworkACLPut{Ingress: []api.NetworkACLRule{{Action: "drop", Source: "192.0.2.1", State: "enabled"}}, Config: map[string]string{}}, request.ClientTypeNormal, func(networks map[string]int64) error {
				require.Equal(t, map[string]int64{"caller": id}, networks)
				var err error
				aclRelease, _, err = AcquireOVNOperation(s, n.Project(), n.Name(), "acl-update")
				return err
			}))
			require.NotNil(t, aclRelease)
			require.NoError(t, aclRelease())
			originalRules := callersExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "ACL", Where: []ovsdb.Condition{}})[0].Rows
			require.ErrorContains(t, a.Update(&api.NetworkACLPut{Ingress: []api.NetworkACLRule{{Action: "reject", Source: "192.0.2.2", State: "enabled"}}, Config: map[string]string{}}, request.ClientTypeNormal, func(map[string]int64) error { return fmt.Errorf("owned fixture downstream reservation failure") }), "downstream reservation failure")
			revertedACL, err := acl.LoadByName(s, "default", "network-b")
			require.NoError(t, err)
			require.Equal(t, "192.0.2.1", revertedACL.Info().Ingress[0].Source)
			require.ElementsMatch(t, originalRules, callersExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "ACL", Where: []ovsdb.Condition{}})[0].Rows)
			require.ErrorIs(t, nb.CheckACLPhysicalUnused(ctx, 1, aclB), networkOVN.ErrPhysicalReference)

			if stopMode == "pending-source-stop" {
				_, _, err = n.InstanceDevicePortStart(&OVNInstanceNICSetupOpts{InstanceUUID: instanceUUID, DeviceName: "eth0", DeviceConfig: nic, DNSName: "caller"}, nil)
				require.ErrorContains(t, err, "unacknowledged source OVN NIC cleanup")
				require.Equal(t, stoppedPort["enabled"], readPort()["enabled"])
				t.Log("Actual Stop source remains pending after backend-only cleanup; latest Start stays strict until actual host/driver terminal acknowledgment")
				return
			}

			_, _, err = n.InstanceDevicePortStart(&OVNInstanceNICSetupOpts{InstanceUUID: instanceUUID, DeviceName: "eth0", DeviceConfig: nic, DNSName: "caller"}, nil)
			require.NoError(t, err)
			restarted, err := nb.CheckNetworkNICReplay(ctx, id, string(n.getRouterIntPortName()), targets())
			require.NoError(t, err)
			require.True(t, restarted[networkOVN.OVNSwitchPort(portName)].Enabled)
			require.Equal(t, map[string]int64{"network-b": aclB}, restarted[networkOVN.OVNSwitchPort(portName)].Publication.ACLIDs)
			t.Log("Actual Created Update admits disabled Add/Start, swaps ACL/default memberships with phase+disabled state retained, supports repeat global/targeted updates and stopped ACL rule reload; latest ordinary Start is enabled with current ACL receipt")

			require.NoError(t, nb.UpdateLogicalSwitchPortEnabled(ctx, n.getInstanceDevicePortName(instanceUUID, "eth0"), false))
			require.NoError(t, n.InstanceDevicePortRemove(instanceUUID, "eth0", nic, false))
			nic = nic.Clone()
			nic["security.acls"] = "network-a"
			require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				devices, err := dbCluster.APIToDevices(map[string]map[string]string{"eth0": maps.Clone(nic)})
				if err != nil {
					return err
				}

				return dbCluster.UpdateInstanceDevices(ctx, tx.Tx(), instID, devices)
			}))
			require.NoError(t, n.InstanceDevicePortAdd(instanceUUID, "eth0", nic))
			changed = maps.Clone(n.Config())
			changed["security.acls.default.ingress.logged"] = "true"
			require.NoError(t, n.Update(api.NetworkPut{Config: changed}, member, request.ClientTypeNormal))
			nicChanged, err := nb.CheckNetworkNICReplay(ctx, id, string(n.getRouterIntPortName()), targets())
			require.NoError(t, err)
			require.Equal(t, "add", nicChanged[networkOVN.OVNSwitchPort(portName)].Publication.Phase)
			require.False(t, nicChanged[networkOVN.OVNSwitchPort(portName)].Enabled)
			require.Equal(t, map[string]int64{"network-a": aclA, "network-b": aclB}, nicChanged[networkOVN.OVNSwitchPort(portName)].Publication.ACLIDs)

			beforeFailure := maps.Clone(n.Config())
			require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				_, err := tx.Tx().ExecContext(ctx, `CREATE TRIGGER latest_config_failure BEFORE UPDATE OF description ON networks BEGIN SELECT RAISE(FAIL,'owned fixture network SQL publication failed'); END`)
				return err
			}))
			failedConfig := maps.Clone(beforeFailure)
			failedConfig["dns.domain"] = "must-rollback.example"
			require.ErrorContains(t, n.Update(api.NetworkPut{Config: failedConfig}, member, request.ClientTypeNormal), "network SQL publication failed")
			require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				_, info, _, err := tx.GetNetworkInAnyState(ctx, "default", "caller")
				if err != nil {
					return err
				}

				require.Equal(t, beforeFailure, map[string]string(info.Config))
				_, err = tx.Tx().ExecContext(ctx, `DROP TRIGGER latest_config_failure`)
				return err
			}))
			require.Error(t, n.Update(api.NetworkPut{Config: failedConfig}, member, request.ClientTypeNormal))
			require.Equal(t, stoppedPort["enabled"], readPort()["enabled"])
			require.ErrorIs(t, nb.CheckACLPhysicalUnused(ctx, 1, aclA), networkOVN.ErrPhysicalReference)
			t.Log("Actual SQL publication failure keeps old catalog/current NIC ACL protection and pending receipt; replay retry remains strict until an actual producer completes")
			_, _, err = n.InstanceDevicePortStart(&OVNInstanceNICSetupOpts{InstanceUUID: instanceUUID, DeviceName: "eth0", DeviceConfig: nic, DNSName: "caller"}, nil)
			require.NoError(t, err)
			t.Log("Stopped NIC ACL producer retirement/re-add plus SQL config publication, shared default reload and latest Start preserve current NIC+network ACL identities; enclosing d.Update remains a separate driver/runtime qualification")
			require.Error(t, n.InstanceDevicePortRemove(instanceUUID, "eth0", nic, false))
			require.NoError(t, nb.UpdateLogicalSwitchPortEnabled(ctx, n.getInstanceDevicePortName(instanceUUID, "eth0"), false))
			require.NoError(t, n.InstanceDevicePortRemove(instanceUUID, "eth0", nic, false))
			require.NoError(t, OVNCheckPhysicalUnused(ctx, n))
			t.Log("Actual Add/Start/Stop/Update/Start/Remove backend workflow passed; physical Delete remains refused until exact Remove")
		})
	}
}

func TestDurableNBRootAdmissionBeforeConstructorEffects(t *testing.T) {
	for _, mode := range []string{"success-restart", "root-replacement-race", "admission-refusal"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c := callersTestCluster(t)
			original, raw, endpoint := callersTestNB(t)
			require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error { return tx.BeginOVNReferenceActivation(ctx) }))
			owner := uuid.NewString()
			admitted := false
			opened, err := networkOVN.NewNBWithRootAdmission(endpoint, "", "", "", owner, func(ctx context.Context, root string) error {
				admitted = true
				require.Equal(t, original.BackendID(), root)
				rows := callersExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "NB_Global", Where: []ovsdb.Condition{}})
				ids := rows[0].Rows[0]["external_ids"].(ovsdb.OvsMap).GoMap
				require.NotContains(t, ids, "incus:ovn-lifecycle:"+owner)
				if mode == "admission-refusal" {
					return fmt.Errorf("owned durable admission refusal")
				}

				referenceErr := c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error { return tx.BindOVNReferenceRoot(ctx, root) })
				if referenceErr != nil {
					return referenceErr
				}

				if mode == "root-replacement-race" {
					callersExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "NB_Global", Where: []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: ovsdb.UUID{GoUUID: root}}}}, ovsdb.Operation{Op: ovsdb.OperationInsert, Table: "NB_Global", Row: ovsdb.Row{"external_ids": callersStringMap(map[string]string{})}})
				}

				return nil
			})
			require.True(t, admitted)
			if mode != "success-restart" {
				require.Error(t, err)
				require.Nil(t, opened)
				rows := callersExec(t, raw, ovsdb.Operation{Op: ovsdb.OperationSelect, Table: "NB_Global", Where: []ovsdb.Condition{}})
				require.NotContains(t, rows[0].Rows[0]["external_ids"].(ovsdb.OvsMap).GoMap, "incus:ovn-lifecycle:"+owner)
			} else {
				require.NoError(t, err)
				t.Cleanup(opened.Close)
				// Durable root is re-read on a fresh constructor; the original active root cannot be replaced.
				reopened, err := networkOVN.NewNBWithRootAdmission(endpoint, "", "", "", owner, func(ctx context.Context, root string) error {
					return c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error { return tx.BindOVNReferenceRoot(ctx, root) })
				})
				require.NoError(t, err)
				t.Cleanup(reopened.Close)
				require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					require.NoError(t, tx.CheckOVNReferencePublication(ctx, original.BackendID()))
					_, err := tx.CreateNetwork(ctx, "default", "bound-ovn", "", db.NetworkTypeOVN, nil)
					require.NoError(t, err)
					require.Error(t, tx.BindOVNReferenceRoot(ctx, uuid.NewString()))
					return nil
				}))
			}

			t.Logf("Actual admitted NB constructor %s: admission preceded fencing, exact original root retained", mode)
		})
	}
}

func TestFreshResourcePublicationSerializesBeforeActivation(t *testing.T) {
	ctx := context.Background()
	c := callersTestCluster(t)
	var aclID int64
	require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		aclID, err = dbCluster.CreateNetworkACL(ctx, tx.Tx(), dbCluster.NetworkACL{Project: "default", Name: "serialization"})
		return err
	}))
	// A stale preflight cannot grant the later publication once first activation committed.
	require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		never, err := tx.OVNReferencesNeverActivated(ctx)
		require.True(t, never)
		return err
	}))
	require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		err := tx.BeginOVNReferenceActivation(ctx)
		if err != nil {
			return err
		}

		_, err = tx.CreateNetwork(ctx, "default", "first-ovn", "", db.NetworkTypeOVN, nil)
		return err
	}))
	require.Error(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		referenceErr := tx.CheckOVNReferencePublication(ctx, "")
		if referenceErr != nil {
			return referenceErr
		}

		return dbCluster.UpdateNetworkACLAPI(ctx, tx.Tx(), aclID, &api.NetworkACLPut{Description: "stale publication"})
	}))
	require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		_, apiACL, err := dbCluster.GetNetworkACLAPI(ctx, tx.Tx(), "default", "serialization")
		require.Empty(t, apiACL.Description)
		return err
	}))
	t.Log("Fresh no-NB publication authority is checked in the same actual SQL mutation transaction; completed activation rejects the stale preflight before catalog writes")
}

func TestDurableApplicabilitySurvivesOwnedCowSQLRestart(t *testing.T) {
	ctx := context.Background()
	nb, _, endpoint := callersTestNB(t)
	dir, err := os.MkdirTemp("", "restart-cowsql-")
	require.NoError(t, err)
	listener, err := net.Listen("unix", "")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	store, err := cowsqlDriver.DefaultNodeStore(":memory:")
	require.NoError(t, err)
	require.NoError(t, store.Set(ctx, []cowsqlDriver.NodeInfo{{Address: address}}))
	var server *cowsql.Node
	var c *db.Cluster
	open := func() {
		var err error
		server, err = cowsql.New(1, address, filepath.Join(dir, "global"), cowsql.WithBindAddress(address))
		require.NoError(t, err)
		require.NoError(t, server.Start())
		c, err = db.OpenCluster(ctx, "test.db", store, "1", dir, 5*time.Second, cowsqlDriver.WithDialFunc(func(ctx context.Context, address string) (net.Conn, error) { return net.Dial("unix", address) }))
		require.NoError(t, err)
	}

	require.NoError(t, os.Mkdir(filepath.Join(dir, "global"), 0o700))
	open()
	t.Cleanup(func() {
		require.NoError(t, c.Close())
		require.NoError(t, server.Close())
		conn, err := net.Dial("unix", address)
		if conn != nil {
			_ = conn.Close()
		}

		require.Error(t, err)
		require.NoError(t, os.RemoveAll(dir))
		_, err = os.Stat(dir)
		require.True(t, os.IsNotExist(err))
		t.Logf("owned restarted cowsql closed; endpoint refused and root removed: %s", dir)
	})
	require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		referenceErr := tx.BeginOVNReferenceActivation(ctx)
		if referenceErr != nil {
			return referenceErr
		}

		return tx.BindOVNReferenceRoot(ctx, nb.BackendID())
	}))
	require.NoError(t, c.Close())
	require.NoError(t, server.Close())
	open()
	require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		never, err := tx.OVNReferencesNeverActivated(ctx)
		require.NoError(t, err)
		require.False(t, never)
		require.NoError(t, tx.CheckOVNReferencePublication(ctx, nb.BackendID()))
		require.Error(t, tx.CheckOVNReferencePublication(ctx, ""))
		// The active root can only be replaced while no OVN definition or work exists.
		_, err = tx.CreateNetwork(ctx, "default", "bound-ovn", "", db.NetworkTypeOVN, nil)
		require.NoError(t, err)
		require.Error(t, tx.BindOVNReferenceRoot(ctx, uuid.NewString()))
		return nil
	}))
	reopened, err := networkOVN.NewNBWithRootAdmission(endpoint, "", "", "", uuid.NewString(), func(ctx context.Context, root string) error {
		return c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error { return tx.BindOVNReferenceRoot(ctx, root) })
	})
	require.NoError(t, err)
	t.Cleanup(reopened.Close)
	t.Logf("Actual private CowSQL server close/reopen retained active original NB root %s; fresh NB admitted constructor verified it after restart", nb.BackendID())
}
