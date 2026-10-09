//go:build linux && cgo && !agent

package main

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
)

type autostartAdmissionInstance struct {
	instance.Instance
	name       string
	config     map[string]string
	configRead atomic.Int64
	starts     atomic.Int64
}

func (i *autostartAdmissionInstance) ExpandedConfig() map[string]string {
	i.configRead.Add(1)
	return i.config
}

func (i *autostartAdmissionInstance) IsRunning() bool {
	return false
}

func (i *autostartAdmissionInstance) Project() api.Project {
	return api.Project{Name: "default"}
}

func (i *autostartAdmissionInstance) Name() string {
	return i.name
}

func (i *autostartAdmissionInstance) Start(_ bool) error {
	i.starts.Add(1)
	// HTTP 503 exits the real loop before retries, sleeps or warning writes.
	return api.StatusErrorf(http.StatusServiceUnavailable, "Isolated autostart admission test")
}

func newInstancesStartAdmissionState(t *testing.T, memberState int) *state.State {
	t.Helper()

	// Sequential fixtures share prepared statements and avoid OS/firewall setup.
	s, name := newEvacuationAdmissionState(t, memberState)
	s.ShutdownCtx = context.Background()
	s.ServerClustered = true
	s.ServerName = name
	return s
}

func newAutostartAdmissionInstances() ([]instance.Instance, []*autostartAdmissionInstance) {
	bulk := &autostartAdmissionInstance{
		name:   "bulk",
		config: map[string]string{"boot.autostart": "true"},
	}

	sequential := &autostartAdmissionInstance{
		name: "sequential",
		config: map[string]string{
			"boot.autostart":          "true",
			"boot.autostart.priority": "10",
			"boot.autostart.delay":    "30",
		},
	}

	return []instance.Instance{bulk, sequential}, []*autostartAdmissionInstance{bulk, sequential}
}

func requireAutostartAdmissionSkipped(t *testing.T, fakes []*autostartAdmissionInstance) {
	t.Helper()

	for _, fake := range fakes {
		require.Zero(t, fake.configRead.Load(), "%s queue must not be classified before admission", fake.name)
		require.Zero(t, fake.starts.Load(), "%s queue must not dispatch before admission", fake.name)
	}
}

func requireAutostartAdmissionEntered(t *testing.T, s *state.State, fakes []*autostartAdmissionInstance) {
	t.Helper()

	for _, fake := range fakes {
		require.Positive(t, fake.configRead.Load(), "%s queue must reach classification", fake.name)
		require.EqualValues(t, 1, fake.starts.Load(), "%s queue must call only the fake Start once", fake.name)
	}

	var warnings int
	err := s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.Tx().QueryRowContext(ctx, "SELECT COUNT(*) FROM warnings").Scan(&warnings)
	})
	require.NoError(t, err)
	require.Zero(t, warnings, "isolated HTTP 503 must leave warnings empty")
}

func TestInstancesStartMaintenance(t *testing.T) {
	for _, tt := range []struct {
		name  string
		state int
	}{
		{"evacuating", db.ClusterMemberStateEvacuating},
		{"evacuated", db.ClusterMemberStateEvacuated},
		{"restoring", db.ClusterMemberStateRestoring},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newInstancesStartAdmissionState(t, tt.state)
			instances, fakes := newAutostartAdmissionInstances()
			for attempt := 1; attempt <= 2; attempt++ {
				instancesStart(s, instances)
				requireAutostartAdmissionSkipped(t, fakes)
				require.Equal(t, tt.state, evacuationAdmissionState(t, s, s.ServerName))
			}
		})
	}
}

func TestInstancesStartMissingMemberAndRetry(t *testing.T) {
	s := newInstancesStartAdmissionState(t, db.ClusterMemberStateCreated)
	instances, fakes := newAutostartAdmissionInstances()
	localID := s.DB.Cluster.GetNodeID()
	s.DB.Cluster.NodeID(999)
	for attempt := 1; attempt <= 2; attempt++ {
		instancesStart(s, instances)
		requireAutostartAdmissionSkipped(t, fakes)
	}

	s.DB.Cluster.NodeID(localID)
	instancesStart(s, instances)
	requireAutostartAdmissionEntered(t, s, fakes)
}

func TestInstancesStartQueryErrorAndRetry(t *testing.T) {
	s := newInstancesStartAdmissionState(t, db.ClusterMemberStateCreated)
	instances, fakes := newAutostartAdmissionInstances()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.ShutdownCtx = ctx
	for attempt := 1; attempt <= 2; attempt++ {
		instancesStart(s, instances)
		requireAutostartAdmissionSkipped(t, fakes)
	}

	s.ShutdownCtx = context.Background()
	instancesStart(s, instances)
	requireAutostartAdmissionEntered(t, s, fakes)
}

func TestInstancesStartActive(t *testing.T) {
	for _, tt := range []struct {
		name      string
		clustered bool
	}{
		{"created", true},
		{"standalone", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newInstancesStartAdmissionState(t, db.ClusterMemberStateCreated)
			s.ServerClustered = tt.clustered
			if !tt.clustered {
				// Standalone retains the member-state lookup bypass.
				s.DB.Cluster.NodeID(999)
			}

			instances, fakes := newAutostartAdmissionInstances()
			instancesStart(s, instances)
			requireAutostartAdmissionEntered(t, s, fakes)
		})
	}
}
