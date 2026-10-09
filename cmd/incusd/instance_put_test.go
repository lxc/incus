//go:build linux && cgo && !agent

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	dbCluster "github.com/lxc/incus/v7/internal/server/db/cluster"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/internal/server/operations"
	"github.com/lxc/incus/v7/internal/server/request"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/osarch"
)

// These tests call the real snapshot helper with private SQL and a serialized Load
// hook. They never schedule an operation or call a storage/instance driver.
type snapshotAdmissionInstance struct {
	instance.Instance
	running         bool
	config          map[string]string
	operation       *operations.Operation
	operationSet    int
	restoreCalls    int
	restoreSource   instance.Instance
	restoreStateful bool
	restoreDiskOnly bool
	restoreErr      error
}

func (i *snapshotAdmissionInstance) SetOperation(op *operations.Operation) {
	i.operation = op
	i.operationSet++
}

func (i *snapshotAdmissionInstance) IsRunning() bool {
	return i.running
}

func (i *snapshotAdmissionInstance) Profiles() []api.Profile {
	return nil
}

func (i *snapshotAdmissionInstance) LocalConfig() map[string]string {
	return i.config
}

func (i *snapshotAdmissionInstance) LocalDevices() deviceConfig.Devices {
	return deviceConfig.Devices{"root": {"type": "disk", "path": "/", "pool": "fixture"}}
}

func (i *snapshotAdmissionInstance) Restore(source instance.Instance, stateful bool, diskOnly bool) error {
	i.restoreCalls++
	i.restoreSource = source
	i.restoreStateful = stateful
	i.restoreDiskOnly = diskOnly
	return i.restoreErr
}

type snapshotAdmissionFixture struct {
	s        *state.State
	parent   *snapshotAdmissionInstance
	snapshot *snapshotAdmissionInstance
	loaded   []string
}

func newSnapshotAdmissionFixture(t *testing.T, memberState int, running bool) *snapshotAdmissionFixture {
	t.Helper()
	cluster, cleanup := db.NewTestCluster(t)
	t.Cleanup(cleanup)
	f := &snapshotAdmissionFixture{
		s:        &state.State{DB: &db.DB{Cluster: cluster}, ShutdownCtx: context.Background(), ServerClustered: true},
		parent:   &snapshotAdmissionInstance{running: running, config: map[string]string{"volatile.uuid.generation": "parent-generation"}},
		snapshot: &snapshotAdmissionInstance{config: map[string]string{"volatile.uuid.generation": "snapshot-generation"}},
	}

	err := cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		member, err := tx.GetNodeByName(ctx, "none")
		if err != nil {
			return err
		}

		err = tx.UpdateNodeStatus(member.ID, memberState)
		if err != nil {
			return err
		}

		_, err = dbCluster.CreateInstance(ctx, tx.Tx(), dbCluster.Instance{Project: api.ProjectDefaultName, Name: "parent", Node: member.Name, Architecture: osarch.ARCH_64BIT_INTEL_X86})
		if err != nil {
			return err
		}

		_, err = dbCluster.CreateInstanceSnapshot(ctx, tx.Tx(), dbCluster.InstanceSnapshot{Project: api.ProjectDefaultName, Instance: "parent", Name: "snap"})
		return err
	})
	require.NoError(t, err)

	previousLoad := instance.Load
	instance.Load = func(s *state.State, args db.InstanceArgs, p api.Project) (instance.Instance, error) {
		if s != f.s || args.Project != api.ProjectDefaultName || p.Name != api.ProjectDefaultName {
			return nil, errors.New("Unexpected snapshot loader state or project")
		}

		f.loaded = append(f.loaded, args.Name)
		if args.Name == "parent" && !args.Snapshot {
			return f.parent, nil
		}

		if args.Name == "parent/snap" && args.Snapshot {
			return f.snapshot, nil
		}

		return nil, errors.New("Unexpected snapshot loader name or snapshot flag")
	}

	t.Cleanup(func() { instance.Load = previousLoad })
	return f
}

func snapshotAdmissionRequest(protocol string, forwarded *string) *http.Request {
	r := httptest.NewRequest(http.MethodPut, "/1.0/instances/parent", nil)
	ctx := r.Context()
	if protocol != "" {
		ctx = context.WithValue(ctx, request.CtxProtocol, protocol)
	}

	if forwarded != nil {
		ctx = context.WithValue(ctx, request.CtxForwardedProtocol, *forwarded)
	}

	return r.WithContext(ctx)
}

func (f *snapshotAdmissionFixture) restore(r *http.Request, stateful bool, diskOnly bool) error {
	return instanceSnapRestore(f.s, r, api.ProjectDefaultName, "parent", "snap", stateful, diskOnly, nil)
}

func (f *snapshotAdmissionFixture) assertDenied(t *testing.T, generation string) {
	t.Helper()
	require.Zero(t, f.parent.restoreCalls, "denial must precede Restore")
	require.Equal(t, generation, f.snapshot.config["volatile.uuid.generation"], "denial must not allocate a new generation")
	require.Equal(t, []string{"parent", "parent/snap"}, f.loaded)
	require.Equal(t, 1, f.parent.operationSet)
	require.Equal(t, 1, f.snapshot.operationSet)
	require.Nil(t, f.parent.operation)
	require.Nil(t, f.snapshot.operation)
}

func (f *snapshotAdmissionFixture) assertRestored(t *testing.T, stateful bool, diskOnly bool) {
	t.Helper()
	require.Equal(t, 1, f.parent.restoreCalls)
	require.Same(t, f.snapshot, f.parent.restoreSource)
	require.Equal(t, stateful, f.parent.restoreStateful)
	require.Equal(t, diskOnly, f.parent.restoreDiskOnly)
	_, err := uuid.Parse(f.snapshot.config["volatile.uuid.generation"])
	require.NoError(t, err)
}

func TestSnapshotRestoreMaintenanceDenied(t *testing.T) {
	states := map[string]int{"evacuating": db.ClusterMemberStateEvacuating, "evacuated": db.ClusterMemberStateEvacuated, "restoring": db.ClusterMemberStateRestoring}
	for stateName, memberState := range states {
		for _, mode := range []struct {
			name                        string
			running, stateful, diskOnly bool
		}{
			{name: "stateful-stopped", stateful: true},
			{name: "stateless-running", running: true},
			{name: "disk-only-running", running: true, diskOnly: true},
		} {
			t.Run(stateName+"/"+mode.name, func(t *testing.T) {
				f := newSnapshotAdmissionFixture(t, memberState, mode.running)
				err := f.restore(snapshotAdmissionRequest("tls", nil), mode.stateful, mode.diskOnly)
				f.assertDenied(t, "snapshot-generation")
				require.True(t, api.StatusErrorCheck(err, http.StatusForbidden), "%v", err)
			})
		}
	}
}

func TestSnapshotRestoreMaintenanceProtocol(t *testing.T) {
	for _, test := range []struct {
		name, transport, original string
		forwarded, allow          bool
	}{
		{name: "tls", transport: "tls"},
		{name: "unix", transport: "unix"},
		{name: "missing"},
		{name: "cluster-original", transport: "cluster", allow: true},
		{name: "forwarded-tls", transport: "cluster", original: "tls", forwarded: true},
		{name: "forwarded-unix", transport: "cluster", original: "unix", forwarded: true},
		{name: "forwarded-cluster", transport: "tls", original: "cluster", forwarded: true, allow: true},
		{name: "empty-forwarded-cluster", transport: "cluster", forwarded: true, allow: true},
		{name: "empty-forwarded-external", transport: "tls", forwarded: true},
		{name: "unknown-forwarded", transport: "cluster", original: "unexpected", forwarded: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newSnapshotAdmissionFixture(t, db.ClusterMemberStateEvacuating, false)
			var forwarded *string
			if test.forwarded {
				forwarded = &test.original
			}

			err := f.restore(snapshotAdmissionRequest(test.transport, forwarded), true, false)
			if test.allow {
				require.NoError(t, err)
				f.assertRestored(t, true, false)
			} else {
				f.assertDenied(t, "snapshot-generation")
				require.True(t, api.StatusErrorCheck(err, http.StatusForbidden), "%v", err)
			}
		})
	}
}

func TestSnapshotRestoreMaintenanceControls(t *testing.T) {
	for _, test := range []struct {
		name                                                                   string
		memberState                                                            int
		running, stateful, diskOnly, standalone, clusterOriginal, canceledHTTP bool
	}{
		{name: "created-stateful", stateful: true},
		{name: "created-running", running: true},
		{name: "created-disk-only-running", running: true, diskOnly: true},
		{name: "standalone", memberState: db.ClusterMemberStateEvacuating, running: true, stateful: true, standalone: true},
		{name: "cluster-evacuating", memberState: db.ClusterMemberStateEvacuating, stateful: true, clusterOriginal: true},
		{name: "cluster-evacuated", memberState: db.ClusterMemberStateEvacuated, stateful: true, clusterOriginal: true},
		{name: "cluster-restoring", memberState: db.ClusterMemberStateRestoring, stateful: true, clusterOriginal: true},
		{name: "stopped-evacuating", memberState: db.ClusterMemberStateEvacuating},
		{name: "stopped-evacuated", memberState: db.ClusterMemberStateEvacuated},
		{name: "stopped-restoring", memberState: db.ClusterMemberStateRestoring},
		{name: "stopped-disk-only", memberState: db.ClusterMemberStateEvacuating, diskOnly: true},
		{name: "canceled-http-created", stateful: true, canceledHTTP: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newSnapshotAdmissionFixture(t, test.memberState, test.running)
			f.s.ServerClustered = !test.standalone
			protocol := "tls"
			if test.clusterOriginal {
				protocol = "cluster"
			}

			r := snapshotAdmissionRequest(protocol, nil)
			if test.canceledHTTP {
				ctx, cancel := context.WithCancel(r.Context())
				cancel()
				r = r.WithContext(ctx)
			}

			require.NoError(t, f.restore(r, test.stateful, test.diskOnly))
			f.assertRestored(t, test.stateful, test.diskOnly)
		})
	}
}

func TestSnapshotRestoreMaintenanceFailureRetry(t *testing.T) {
	for _, failure := range []string{"missing-local-member", "canceled-shutdown", "maintenance"} {
		t.Run(failure, func(t *testing.T) {
			f := newSnapshotAdmissionFixture(t, db.ClusterMemberStateCreated, false)
			localID := f.s.DB.Cluster.GetNodeID()
			switch failure {
			case "missing-local-member":
				f.s.DB.Cluster.NodeID(999999)
			case "canceled-shutdown":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				f.s.ShutdownCtx = ctx
			case "maintenance":
				err := f.s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
					return tx.UpdateNodeStatus(localID, db.ClusterMemberStateEvacuating)
				})
				require.NoError(t, err)
			}

			err := f.restore(snapshotAdmissionRequest("tls", nil), true, false)
			f.assertDenied(t, "snapshot-generation")
			require.Error(t, err)
			if failure == "maintenance" {
				require.True(t, api.StatusErrorCheck(err, http.StatusForbidden))
			} else {
				require.True(t, strings.HasPrefix(err.Error(), "Failed to read local cluster member state before workload admission: "), "%v", err)
				if failure == "canceled-shutdown" {
					require.ErrorIs(t, err, context.Canceled)
				}

				if failure == "missing-local-member" {
					require.True(t, api.StatusErrorCheck(err, http.StatusNotFound))
					require.ErrorContains(t, err, "Cluster member not found")
				}
			}

			f.s.DB.Cluster.NodeID(localID)
			f.s.ShutdownCtx = context.Background()
			err = f.s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.UpdateNodeStatus(localID, db.ClusterMemberStateCreated)
			})
			require.NoError(t, err)
			require.NoError(t, f.restore(snapshotAdmissionRequest("tls", nil), true, false))
			f.assertRestored(t, true, false)
		})
	}
}

func TestSnapshotRestoreMaintenanceValidation(t *testing.T) {
	t.Run("missing-target", func(t *testing.T) {
		f := newSnapshotAdmissionFixture(t, db.ClusterMemberStateEvacuating, false)
		err := instanceSnapRestore(f.s, snapshotAdmissionRequest("tls", nil), api.ProjectDefaultName, "absent", "snap", true, false, nil)
		require.True(t, api.StatusErrorCheck(err, http.StatusNotFound), "%v", err)
		require.Empty(t, f.loaded)
		require.Zero(t, f.parent.restoreCalls)
	})
	t.Run("missing-snapshot", func(t *testing.T) {
		f := newSnapshotAdmissionFixture(t, db.ClusterMemberStateEvacuating, false)
		err := instanceSnapRestore(f.s, snapshotAdmissionRequest("tls", nil), api.ProjectDefaultName, "parent", "absent", true, false, nil)
		require.EqualError(t, err, "Snapshot parent/absent does not exist")
		require.Equal(t, []string{"parent"}, f.loaded)
		require.Zero(t, f.parent.restoreCalls)
	})
	for _, deniedByProject := range []bool{true, false} {
		name := "restricted-volatile-maintenance"
		if deniedByProject {
			name = "project-restriction-before-maintenance"
		}

		t.Run(name, func(t *testing.T) {
			f := newSnapshotAdmissionFixture(t, db.ClusterMemberStateEvacuating, false)
			err := f.s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				id, err := dbCluster.GetProjectID(ctx, tx.Tx(), api.ProjectDefaultName)
				if err != nil {
					return err
				}

				return dbCluster.CreateProjectConfig(ctx, tx.Tx(), id, map[string]string{"restricted": "true", "restricted.containers.privilege": "allow"})
			})
			require.NoError(t, err)
			if deniedByProject {
				f.snapshot.config["raw.lxc"] = "lxc.apparmor.profile=unconfined"
			}

			err = f.restore(snapshotAdmissionRequest("tls", nil), true, false)
			t.Logf("Snapshot validation result: %v", err)
			// Existing project validation replaces this unsafe volatile key before admission.
			f.assertDenied(t, "parent-generation")
			if deniedByProject {
				require.ErrorContains(t, err, "Failed checking if instance update allowed")
				require.ErrorContains(t, err, `Use of low-level config "raw.lxc"`)
				require.False(t, api.StatusErrorCheck(err, http.StatusForbidden))
			} else {
				require.True(t, api.StatusErrorCheck(err, http.StatusForbidden), "%v", err)
			}
		})
	}

	t.Run("restore-error-and-qualified-source", func(t *testing.T) {
		f := newSnapshotAdmissionFixture(t, db.ClusterMemberStateCreated, true)
		failure := errors.New("fake Restore failure")
		f.parent.restoreErr = failure
		err := instanceSnapRestore(f.s, snapshotAdmissionRequest("tls", nil), api.ProjectDefaultName, "parent", "parent/snap", false, true, nil)
		require.ErrorIs(t, err, failure)
		require.Same(t, failure, err)
		f.assertRestored(t, false, true)
	})
}
