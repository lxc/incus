//go:build linux && cgo && !agent

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/go-logr/logr"
	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/network/ovn"
	model "github.com/lxc/incus/v7/internal/server/network/ovn/schema/ovn-nb"
	"github.com/lxc/incus/v7/shared/api"
)

func recoveryTestNB(t *testing.T, cluster *db.Cluster) *ovn.NB {
	t.Helper()
	server, err := exec.LookPath("ovsdb-server")
	if err != nil {
		t.Skip("ovsdb-server is required for real recovery NB tests")
	}

	tool, err := exec.LookPath("ovsdb-tool")
	if err != nil {
		t.Skip("ovsdb-tool is required for real recovery NB tests")
	}

	dir, err := os.MkdirTemp("", "ovn-recovery-")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, os.RemoveAll(dir))
		_, err := os.Stat(dir)
		require.True(t, os.IsNotExist(err))
		t.Logf("owned recovery NB fixture root removed: %s", dir)
	})
	schema, err := json.Marshal(model.Schema())
	require.NoError(t, err)
	schemaPath := filepath.Join(dir, "schema")
	require.NoError(t, os.WriteFile(schemaPath, schema, 0o600))
	dbPath := filepath.Join(dir, "db")
	output, err := exec.Command(tool, "create", dbPath, schemaPath).CombinedOutput()
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

	cmd := exec.Command(server, dbPath, "--remote=punix:"+socket, "--unixctl="+filepath.Join(dir, "control"), "--pidfile="+filepath.Join(dir, "pid"), "--no-chdir")
	cmd.Stdout, cmd.Stderr = logFile, logFile
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		err := cmd.Process.Signal(syscall.SIGTERM)
		if err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Errorf("Failed stopping owned recovery ovsdb-server: %v", err)
		}

		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			require.NotNil(t, cmd.ProcessState)
			if err != nil {
				require.Equal(t, syscall.SIGTERM, cmd.ProcessState.Sys().(syscall.WaitStatus).Signal(), logOutput())
			}

		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Error("owned recovery ovsdb-server required forced teardown")
		}

		require.NotNil(t, cmd.ProcessState)
		t.Logf("owned recovery ovsdb-server PID=%d reaped: %s", cmd.Process.Pid, cmd.ProcessState)
		if t.Failed() {
			t.Log(logOutput())
		}
	})
	require.Eventually(t, func() bool { _, err := os.Stat(socket); return err == nil }, 5*time.Second, 10*time.Millisecond, logOutput())
	m, err := model.FullDatabaseModel()
	require.NoError(t, err)
	discard := logr.Discard()
	raw, err := ovsdbClient.NewOVSDBClient(m, ovsdbClient.WithEndpoint("unix:"+socket), ovsdbClient.WithLogger(&discard))
	require.NoError(t, err)
	t.Cleanup(raw.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, raw.Connect(ctx))
	ops := []ovsdb.Operation{{Op: ovsdb.OperationInsert, Table: "NB_Global", Row: ovsdb.Row{"external_ids": ovsdb.OvsMap{GoMap: map[any]any{}}}}}
	reply, err := raw.Transact(ctx, ops...)
	require.NoError(t, err)
	_, err = ovsdb.CheckOperationResults(reply, ops)
	require.NoError(t, err)
	nb, err := ovn.NewNBWithRootAdmission("unix:"+socket, "", "", "", "recovery-fixture", func(ctx context.Context, root string) error {
		return cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.BindOVNReferenceRoot(ctx, root)
		})
	})
	require.NoError(t, err)
	t.Cleanup(nb.Close)
	require.Equal(t, reply[0].UUID.GoUUID, nb.BackendID())
	t.Logf("owned Unix-only recovery NB fixture pid=%d root=%s db=%s", cmd.Process.Pid, nb.BackendID(), dbPath)
	return nb
}

func TestOVNRecoveryRetainsSnapshotOnFailureAndPreservesCurrentWork(t *testing.T) {
	cluster := sharedReferenceTestCluster(t)
	ctx := context.Background()
	var previous db.OVNPreviousWork
	require.NoError(t, cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		err := tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "old", "old-token", "update")
		if err != nil {
			return err
		}

		previous, err = tx.SnapshotLocalOVNFencedWork(ctx)
		if err != nil {
			return err
		}

		return tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "current", "current-token", "update")
	}))
	require.Equal(t, []string{"old-token"}, previous.OriginTokens)
	acknowledgedNB := recoveryTestNB(t, cluster)
	require.NoError(t, cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.CheckOVNReferencePublication(ctx, acknowledgedNB.BackendID())
	}))
	assertTokens := func(oldToken string) {
		t.Helper()
		require.NoError(t, cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			old, err := tx.OVNNetworkOperationToken(ctx, api.ProjectDefaultName, "old")
			require.NoError(t, err)
			require.Equal(t, oldToken, old)
			current, err := tx.OVNNetworkOperationToken(ctx, api.ProjectDefaultName, "current")
			require.NoError(t, err)
			require.Equal(t, "current-token", current)
			return nil
		}))
	}

	// OVS/SB fencing remains a caller precondition; this fixture tests recovery publication and snapshot scope.
	d := &Daemon{db: &db.DB{Cluster: cluster}, shutdownCtx: ctx, ovnnb: acknowledgedNB, ovnsb: &ovn.SB{}, ovnPreviousWork: &previous}
	previous.NodeID++
	nb, sb, err := d.getOVN()
	require.EqualError(t, err, "Failed to connect to OVN: OVN previous work belongs to another member")
	require.Nil(t, nb)
	require.Nil(t, sb)
	require.Same(t, &previous, d.ovnPreviousWork)
	assertTokens("old-token")

	previous.NodeID--
	d.ovnnb = &ovn.NB{}
	nb, sb, err = d.getOVN()
	require.EqualError(t, err, "Failed to connect to OVN: OVN activated during resource publication; retry with the original backend")
	require.Nil(t, nb)
	require.Nil(t, sb)
	require.Same(t, &previous, d.ovnPreviousWork)
	assertTokens("old-token")

	d.ovnnb = acknowledgedNB
	nb, sb, err = d.getOVN()
	require.NoError(t, err)
	require.Same(t, acknowledgedNB, nb)
	require.Same(t, d.ovnsb, sb)
	require.Nil(t, d.ovnPreviousWork)
	assertTokens("")
}
