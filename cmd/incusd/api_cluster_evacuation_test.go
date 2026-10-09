//go:build linux && cgo && !agent

package main

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	dbCluster "github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/instance"
	instanceDrivers "github.com/lxc/incus/v7/internal/server/instance/drivers"
	"github.com/lxc/incus/v7/internal/server/network"
	"github.com/lxc/incus/v7/internal/server/operations"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
)

func newEvacuationAdmissionState(t *testing.T, priorState int) (*state.State, string) {
	t.Helper()

	cluster, cleanup := db.NewTestCluster(t)
	t.Cleanup(cleanup)
	// This path only needs the isolated database, avoiding OS and firewall initialization.
	s := &state.State{DB: &db.DB{Cluster: cluster}}
	name := "evacuation-admission"
	err := cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		err := tx.RenameNode(ctx, "none", name)
		if err != nil {
			return err
		}

		member, err := tx.GetNodeByName(ctx, name)
		if err != nil {
			return err
		}

		return tx.UpdateNodeStatus(member.ID, priorState)
	})
	require.NoError(t, err)

	return s, name
}

func evacuationAdmissionState(t *testing.T, s *state.State, name string) int {
	t.Helper()

	var memberState int
	err := s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.Tx().QueryRowContext(ctx, "SELECT state FROM nodes WHERE name=?", name).Scan(&memberState)
	})
	require.NoError(t, err)

	return memberState
}

func TestEvacuateClusterMemberEarlyErrorAdmission(t *testing.T) {
	cases := []struct {
		name       string
		priorState int
		wantState  int
	}{
		{"created", db.ClusterMemberStateCreated, db.ClusterMemberStateCreated},
		{"evacuating", db.ClusterMemberStateEvacuating, db.ClusterMemberStateEvacuating},
		{"evacuated", db.ClusterMemberStateEvacuated, db.ClusterMemberStateEvacuating},
		{"restoring", db.ClusterMemberStateRestoring, db.ClusterMemberStateEvacuating},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s, name := newEvacuationAdmissionState(t, tt.priorState)
			// No instances exist; a nil migration callback fails before maintenance effects.
			for attempt := 1; attempt <= 2; attempt++ {
				err := evacuateClusterMember(context.Background(), s, nil, name, "auto", nil, nil)
				require.EqualError(t, err, "Missing migration callback function", "attempt %d must release the maintenance lock", attempt)
				require.Equal(t, tt.wantState, evacuationAdmissionState(t, s, name), "attempt %d must preserve admission", attempt)
			}
		})
	}
}

func TestEvacuateClusterSetStateAdmission(t *testing.T) {
	t.Run("stale-expected-state", func(t *testing.T) {
		s, name := newEvacuationAdmissionState(t, db.ClusterMemberStateRestoring)
		err := evacuateClusterSetState(s, name, db.ClusterMemberStateCreated, db.ClusterMemberStateEvacuating)
		require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
		require.Equal(t, db.ClusterMemberStateRestoring, evacuationAdmissionState(t, s, name))
	})

	t.Run("stale-repeated-admission", func(t *testing.T) {
		s, name := newEvacuationAdmissionState(t, db.ClusterMemberStateEvacuating)
		err := evacuateClusterSetState(s, name, db.ClusterMemberStateEvacuating, db.ClusterMemberStateCreated)
		require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
		require.Equal(t, db.ClusterMemberStateEvacuating, evacuationAdmissionState(t, s, name))
	})

	t.Run("pending", func(t *testing.T) {
		s, name := newEvacuationAdmissionState(t, db.ClusterMemberStatePending)
		err := evacuateClusterSetState(s, name, db.ClusterMemberStateEvacuating, db.ClusterMemberStatePending)
		// Pending members are excluded by the caller's member lookup.
		require.True(t, api.StatusErrorCheck(err, http.StatusNotFound), "%v", err)
		require.Equal(t, db.ClusterMemberStatePending, evacuationAdmissionState(t, s, name))
	})

	for _, tt := range []struct {
		name      string
		operation string
		local     int
		refused   bool
	}{
		{"prepared-deletion", "delete", 6, true},
		{"prepared-other-operation", "update", 6, false},
		{"active-deletion", "delete", 1, false},
		{"no-operation", "", 6, false},
	} {
		t.Run("restore-"+tt.name, func(t *testing.T) {
			s, name := newEvacuationAdmissionState(t, db.ClusterMemberStateEvacuated)
			err := s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				member, err := tx.GetNodeByName(ctx, name)
				if err != nil {
					return err
				}

				_, err = tx.Tx().ExecContext(ctx, "INSERT INTO networks (id, project_id, name, description, state, type) VALUES (77, 1, 'deleting-net', '', 4, 0)")
				if err != nil {
					return err
				}

				_, err = tx.Tx().ExecContext(ctx, "INSERT INTO networks_nodes (network_id, node_id, state) VALUES (77, ?, ?)", member.ID, tt.local)
				if err != nil {
					return err
				}

				if tt.operation == "" {
					return nil
				}

				_, err = tx.Tx().ExecContext(ctx, "INSERT INTO networks_ovn_operations (project_id, name, node_id, token, operation) VALUES (1, 'deleting-net', ?, 'token', ?)", member.ID, tt.operation)
				return err
			})
			require.NoError(t, err)

			err = evacuateClusterSetState(s, name, db.ClusterMemberStateRestoring, db.ClusterMemberStateEvacuated)
			if tt.refused {
				require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
				require.ErrorContains(t, err, "deleting-net")
				require.Equal(t, db.ClusterMemberStateEvacuated, evacuationAdmissionState(t, s, name))
				return
			}

			require.NoError(t, err)
			require.Equal(t, db.ClusterMemberStateRestoring, evacuationAdmissionState(t, s, name))
		})
	}

	for _, tt := range []struct {
		name  string
		state int
	}{
		{"repeat-evacuating", db.ClusterMemberStateEvacuating},
		{"repeat-restoring", db.ClusterMemberStateRestoring},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, name := newEvacuationAdmissionState(t, tt.state)
			for attempt := 1; attempt <= 2; attempt++ {
				err := evacuateClusterSetState(s, name, tt.state, tt.state)
				require.NoError(t, err)
				require.Equal(t, tt.state, evacuationAdmissionState(t, s, name))
			}
		})
	}
}

// evacuationAdmissionInstance exposes only methods used before real migration or network effects.
type evacuationAdmissionInstance struct {
	instance.Instance
	name string
}

func (i *evacuationAdmissionInstance) Name() string {
	return i.name
}

func (i *evacuationAdmissionInstance) Project() api.Project {
	return api.Project{Name: api.ProjectDefaultName}
}

func (i *evacuationAdmissionInstance) CanMigrate() string {
	return "stop"
}

func (i *evacuationAdmissionInstance) IsRunning() bool {
	return true
}

func seedEvacuationAdmissionInstances(t *testing.T, s *state.State, memberName string, names ...string) *int {
	t.Helper()
	instances := make(map[string]instance.Instance, len(names))
	err := s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		for _, name := range names {
			_, err := dbCluster.CreateInstance(ctx, tx.Tx(), dbCluster.Instance{
				Project: api.ProjectDefaultName,
				Name:    name,
				Node:    memberName,
			})
			if err != nil {
				return err
			}

			instances[name] = &evacuationAdmissionInstance{name: name}
		}

		return nil
	})
	require.NoError(t, err)

	loaded := 0
	previousLoad := instance.Load
	instance.Load = func(gotState *state.State, args db.InstanceArgs, p api.Project) (instance.Instance, error) {
		if gotState != s || args.Project != api.ProjectDefaultName || p.Name != api.ProjectDefaultName {
			return nil, errors.New("Unexpected evacuation loader input")
		}

		inst, ok := instances[args.Name]
		if !ok {
			return nil, errors.New("Unexpected evacuation instance")
		}

		loaded++
		return inst, nil
	}

	t.Cleanup(func() { instance.Load = previousLoad })

	return &loaded
}

func newEvacuationAdmissionOperation(t *testing.T) *operations.Operation {
	t.Helper()
	require.Empty(t, operations.Clone(), "No scheduled operations may enter the fixture")
	op := &operations.Operation{}
	require.Empty(t, op.Metadata())
	// Zero status rejects both methods before metadata parsing, logging or events.
	require.EqualError(t, op.UpdateMetadata(make(chan int)), "Only pending or running operations can be updated")
	require.EqualError(t, op.ExtendMetadata(make(chan int)), "Only pending or running operations can be updated")
	require.Empty(t, op.Metadata())

	return op
}

func TestEvacuateClusterMemberPartialErrorAdmission(t *testing.T) {
	for _, tt := range []struct {
		name        string
		instances   []string
		mode        string
		cancelAfter bool
		wantError   string
	}{
		{
			name:      "completed-stop-and-failed-stop",
			instances: []string{"completed", "failed"},
			mode:      "stop",
			wantError: "Failed to evacuate instances:\n - default/failed: Counted stop failure",
		},
		{
			name:      "effectful-stop-returns-error",
			instances: []string{"failed"},
			mode:      "auto",
			wantError: "Failed to evacuate instances:\n - default/failed: Counted stop failure",
		},
		{
			name:        "successful-stop-before-target-query-failure",
			instances:   []string{"completed"},
			mode:        "migrate",
			cancelAfter: true,
			wantError:   "Failed to evacuate instances:\n - default/completed: Failed to begin transaction: context canceled",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, name := newEvacuationAdmissionState(t, db.ClusterMemberStateCreated)
			loaded := seedEvacuationAdmissionInstances(t, s, name, tt.instances...)
			op := newEvacuationAdmissionOperation(t)
			var completed, failed, migrations atomic.Int32
			for attempt := 1; attempt <= 2; attempt++ {
				ctx, cancel := context.WithCancel(context.Background())
				completedEffect := make(chan struct{})
				stop := func(inst instance.Instance, action string) error {
					if action != tt.mode && (tt.mode != "auto" || action != "stop") {
						return errors.New("Unexpected evacuation action")
					}

					if inst.Name() == "failed" {
						if len(tt.instances) == 2 {
							// DB name ordering also makes this safe with one worker.
							<-completedEffect
						}

						failed.Add(1)
						return errors.New("Counted stop failure")
					}

					completed.Add(1)
					close(completedEffect)
					if tt.cancelAfter {
						// The actual target query rejects this context before placement or drivers.
						cancel()
					}

					return nil
				}

				migrate := func(context.Context, *state.State, instance.Instance, *db.NodeInfo, *db.NodeInfo, string, bool, *operations.Operation) error {
					migrations.Add(1)
					return errors.New("Unexpected migration dispatch")
				}

				err := evacuateClusterMember(ctx, s, op, name, tt.mode, stop, migrate)
				cancel()
				require.EqualError(t, err, tt.wantError, "attempt %d must release the maintenance lock", attempt)
				require.Equal(t, db.ClusterMemberStateEvacuating, evacuationAdmissionState(t, s, name), "attempt %d must keep admission closed after possible effects", attempt)
				require.True(t, s.DB.Cluster.LocalNodeIsEvacuated())
				require.Equal(t, attempt*len(tt.instances), *loaded)
				require.EqualValues(t, 0, migrations.Load())
				require.Empty(t, op.Metadata())
				require.Empty(t, operations.Clone())
			}

			require.EqualValues(t, 2*len(tt.instances), completed.Load()+failed.Load())
			if len(tt.instances) == 2 || tt.cancelAfter {
				require.EqualValues(t, 2, completed.Load())
			}
		})
	}
}

func TestEvacuateClusterMemberPopulatedPreflightAdmission(t *testing.T) {
	s, name := newEvacuationAdmissionState(t, db.ClusterMemberStateCreated)
	loaded := seedEvacuationAdmissionInstances(t, s, name, "preflight")
	op := newEvacuationAdmissionOperation(t)
	var effects atomic.Int32
	for attempt := 1; attempt <= 2; attempt++ {
		err := evacuateClusterMember(context.Background(), s, op, name, "stop", func(instance.Instance, string) error {
			effects.Add(1)
			return errors.New("Unexpected preflight effect")
		}, nil)
		require.EqualError(t, err, "Missing migration callback function")
		require.Equal(t, db.ClusterMemberStateCreated, evacuationAdmissionState(t, s, name))
		require.False(t, s.DB.Cluster.LocalNodeIsEvacuated())
		require.Equal(t, attempt, *loaded)
		require.EqualValues(t, 0, effects.Load())
		require.Empty(t, op.Metadata())
		require.Empty(t, operations.Clone())
	}
}

func TestEvacuateInstancesPreparationBoundary(t *testing.T) {
	for _, tt := range []struct {
		name        string
		mode        string
		empty       bool
		missing     bool
		fail        bool
		wantMarker  int32
		wantEffects int32
		wantError   string
	}{
		{name: "empty", mode: "stop", empty: true},
		{name: "missing-callback", mode: "stop", missing: true, wantError: "Missing migration callback function"},
		{name: "stop", mode: "stop", wantMarker: 1, wantEffects: 2},
		{name: "auto", mode: "auto", wantMarker: 1, wantEffects: 2},
		{name: "heal-skips-stop", mode: "heal", wantMarker: 1},
		{name: "callback-errors", mode: "stop", fail: true, wantMarker: 1, wantEffects: 2, wantError: "Failed to evacuate instances:\n - default/first: Counted batch failure\n - default/second: Counted batch failure"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			op := newEvacuationAdmissionOperation(t)
			var marker, effects, migrations atomic.Int32
			opts := evacuateOpts{
				instances: []instance.Instance{&evacuationAdmissionInstance{name: "first"}, &evacuationAdmissionInstance{name: "second"}},
				mode:      tt.mode,
				op:        op,
				beforeInstanceActions: func() {
					marker.Add(1)
				},
				stopInstance: func(instance.Instance, string) error {
					if marker.Load() != 1 {
						return errors.New("Preparation marker must precede workload dispatch")
					}

					effects.Add(1)
					if tt.fail {
						return errors.New("Counted batch failure")
					}

					return nil
				},
				migrateInstance: func(context.Context, *state.State, instance.Instance, *db.NodeInfo, *db.NodeInfo, string, bool, *operations.Operation) error {
					migrations.Add(1)
					return errors.New("Unexpected migration dispatch")
				},
			}

			if tt.empty {
				opts.instances = nil
			}

			if tt.missing {
				opts.migrateInstance = nil
			}

			err := evacuateInstances(context.Background(), opts)
			if tt.wantError != "" {
				require.EqualError(t, err, tt.wantError)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, tt.wantMarker, marker.Load())
			require.Equal(t, tt.wantEffects, effects.Load())
			require.EqualValues(t, 0, migrations.Load())
			require.Empty(t, op.Metadata())
			require.Empty(t, operations.Clone())
		})
	}
}

type restoreIntentInstance struct {
	instance.Instance
	canMigrate string
	startErr   error
	volatile   map[string]string
	setErr     error
	stateful   []bool
}

func (i *restoreIntentInstance) CanMigrate() string { return i.canMigrate }

func (i *restoreIntentInstance) Start(stateful bool) error {
	i.stateful = append(i.stateful, stateful)
	if i.startErr != nil {
		// A failed start runs the stop hook, which records the instance as stopped.
		i.volatile["volatile.last_state.power"] = instance.PowerStateStopped
	}

	return i.startErr
}

func (i *restoreIntentInstance) VolatileSet(changes map[string]string) error {
	if i.setErr != nil {
		return i.setErr
	}

	for key, value := range changes {
		i.volatile[key] = value
	}

	return nil
}

func TestRestoreLocalInstanceStartKeepsIntent(t *testing.T) {
	ok := &restoreIntentInstance{canMigrate: "stateful-stop", volatile: map[string]string{"volatile.last_state.power": instance.PowerStateRunning}}
	require.NoError(t, restoreLocalInstanceStart(ok))
	require.Equal(t, []bool{true}, ok.stateful)

	startErr := errors.New("start failed")
	failed := &restoreIntentInstance{canMigrate: "stop", startErr: startErr, volatile: map[string]string{"volatile.last_state.power": instance.PowerStateRunning}}
	err := restoreLocalInstanceStart(failed)
	require.ErrorIs(t, err, startErr)
	require.Equal(t, []bool{false}, failed.stateful)
	require.Equal(t, instance.PowerStateRunning, failed.volatile["volatile.last_state.power"])

	setErr := errors.New("volatile write failed")
	lost := &restoreIntentInstance{canMigrate: "stop", startErr: startErr, setErr: setErr, volatile: map[string]string{"volatile.last_state.power": instance.PowerStateRunning}}
	err = restoreLocalInstanceStart(lost)
	require.ErrorIs(t, err, startErr)
	require.ErrorIs(t, err, setErr)
	require.Equal(t, instance.PowerStateStopped, lost.volatile["volatile.last_state.power"])
}

func TestNetworkOVNMaintenanceDeletedExactID(t *testing.T) {
	s, _ := newEvacuationAdmissionState(t, db.ClusterMemberStateEvacuating)
	err := s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.Tx().ExecContext(ctx, "INSERT INTO networks (id, project_id, name, description, state, type) VALUES (78, 1, 'reused', '', 1, 0)")
		return err
	})
	require.NoError(t, err)
	s.ShutdownCtx = context.Background()

	// The same name under a new ID never makes the old ID look present.
	require.False(t, networkOVNMaintenanceDeleted(s, 78))
	require.True(t, networkOVNMaintenanceDeleted(s, 77))
}

type restoreCreatingNetwork struct {
	network.Network
	id     int64
	status string
	local  string
}

func (n *restoreCreatingNetwork) ID() int64           { return n.id }
func (n *restoreCreatingNetwork) Name() string        { return "creating" }
func (n *restoreCreatingNetwork) Project() string     { return api.ProjectDefaultName }
func (n *restoreCreatingNetwork) Status() string      { return n.status }
func (n *restoreCreatingNetwork) LocalStatus() string { return n.local }

func TestNetworkOVNCreatingExactID(t *testing.T) {
	s, _ := newEvacuationAdmissionState(t, db.ClusterMemberStateRestoring)
	s.ShutdownCtx = context.Background()
	err := s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.Tx().ExecContext(ctx, "INSERT INTO networks (id, project_id, name, description, state, type) VALUES (79, 1, 'creating', '', 2, 0)")
		if err != nil {
			return err
		}

		_, err = tx.Tx().ExecContext(ctx, "INSERT INTO networks_ovn_operations (project_id, name, node_id, token, operation) VALUES (1, 'creating', 1, 'token', 'create')")
		return err
	})
	require.NoError(t, err)

	require.True(t, networkOVNCreating(s, &restoreCreatingNetwork{id: 79, status: api.NetworkStatusErrored, local: api.NetworkStatusPending}))
	// A replaced identity, an initialized member row or a non-creating status are not skipped.
	require.False(t, networkOVNCreating(s, &restoreCreatingNetwork{id: 80, status: api.NetworkStatusErrored, local: api.NetworkStatusPending}))
	require.False(t, networkOVNCreating(s, &restoreCreatingNetwork{id: 79, status: api.NetworkStatusErrored, local: api.NetworkStatusPrepared}))
	require.False(t, networkOVNCreating(s, &restoreCreatingNetwork{id: 79, status: api.NetworkStatusCreated, local: api.NetworkStatusPending}))
	err = s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_operations SET operation='update'")
		return err
	})
	require.NoError(t, err)
	require.False(t, networkOVNCreating(s, &restoreCreatingNetwork{id: 79, status: api.NetworkStatusErrored, local: api.NetworkStatusPending}))
}

type evacuationStoppedInstance struct {
	evacuationAdmissionInstance
	stopErr error
	stops   int
}

func (i *evacuationStoppedInstance) IsRunning() bool { return false }

func (i *evacuationStoppedInstance) Stop(stateful bool) error {
	i.stops++
	return i.stopErr
}

func TestEvacuateStoppedInstanceCleanupRetry(t *testing.T) {
	for _, tt := range []struct {
		name      string
		mode      string
		stopErr   error
		wantStops int
		wantError string
	}{
		{name: "nothing-pending", mode: "stop", stopErr: instanceDrivers.ErrInstanceIsStopped, wantStops: 1},
		{name: "retried", mode: "stop", wantStops: 1},
		{name: "retry-fails", mode: "stop", stopErr: errors.New("cleanup refused"), wantStops: 1, wantError: "Failed to evacuate instances:\n - default/stopped: Failed retrying cleanup of stopped instance: cleanup refused"},
		{name: "heal-skips", mode: "heal", stopErr: errors.New("must not run"), wantStops: 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			inst := &evacuationStoppedInstance{evacuationAdmissionInstance: evacuationAdmissionInstance{name: "stopped"}, stopErr: tt.stopErr}
			var effects atomic.Int32
			opts := evacuateOpts{
				instances: []instance.Instance{inst},
				mode:      tt.mode,
				op:        newEvacuationAdmissionOperation(t),
				stopInstance: func(instance.Instance, string) error {
					effects.Add(1)
					return nil
				},
				migrateInstance: func(context.Context, *state.State, instance.Instance, *db.NodeInfo, *db.NodeInfo, string, bool, *operations.Operation) error {
					return errors.New("Unexpected migration dispatch")
				},
			}

			err := evacuateInstances(context.Background(), opts)
			if tt.wantError != "" {
				require.EqualError(t, err, tt.wantError)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, tt.wantStops, inst.stops)
			// The running-instance stop action is never applied to an already stopped instance.
			require.Zero(t, effects.Load())
		})
	}
}
