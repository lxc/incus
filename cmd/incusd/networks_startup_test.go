//go:build linux && cgo && !agent

package main

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/network/ovs"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
)

func newNetworkStartupAdmissionState(t *testing.T, memberState int) (*state.State, *int) {
	t.Helper()

	// Reuse the isolated database fixture, without OS or firewall initialization.
	s, name := newEvacuationAdmissionState(t, memberState)
	s.ShutdownCtx = context.Background()
	s.ServerName = name
	calls := 0
	s.OVS = func() (*ovs.VSwitch, error) {
		calls++
		// Stop before opening a real OVS backend; startup ignores unavailable OVS.
		return nil, errors.New("OVS unavailable in isolated startup test")
	}

	// Empty networks and warnings keep the subsequent shared startup path isolated.
	err := s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		for _, table := range []string{"networks", "warnings"} {
			var count int
			err := tx.Tx().QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count)
			if err != nil {
				return err
			}

			require.Zero(t, count, "%s fixture must be empty", table)
		}

		return nil
	})
	require.NoError(t, err)

	return s, &calls
}

func TestNetworkStartupOrdinaryMaintenance(t *testing.T) {
	for _, tt := range []struct {
		name  string
		state int
	}{
		{"evacuating", db.ClusterMemberStateEvacuating},
		{"evacuated", db.ClusterMemberStateEvacuated},
		{"restoring", db.ClusterMemberStateRestoring},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, calls := newNetworkStartupAdmissionState(t, tt.state)
			for attempt := 1; attempt <= 2; attempt++ {
				require.NoError(t, networkStartupOrdinary(s, true))
				require.Zero(t, *calls, "attempt %d must not enter OVS cleanup", attempt)
				require.Equal(t, tt.state, evacuationAdmissionState(t, s, s.ServerName))
			}
		})
	}
}

func TestNetworkStartupOrdinaryMissingMemberAndRetry(t *testing.T) {
	s, calls := newNetworkStartupAdmissionState(t, db.ClusterMemberStateCreated)
	localID := s.DB.Cluster.GetNodeID()
	// A nonexistent local member exercises the real lookup, without deleting rows.
	s.DB.Cluster.NodeID(999)
	for attempt := 1; attempt <= 2; attempt++ {
		err := networkStartupOrdinary(s, true)
		require.Zero(t, *calls, "attempt %d must reject unknown state before OVS cleanup", attempt)
		require.ErrorContains(t, err, "Failed to read local cluster member state before network startup")
		require.True(t, api.StatusErrorCheck(err, http.StatusNotFound), "%v", err)
	}

	// Repairing the fixture permits a subsequent ordinary startup attempt.
	s.DB.Cluster.NodeID(localID)
	require.NoError(t, networkStartupOrdinary(s, true))
	require.Equal(t, 1, *calls)
}

func TestNetworkStartupOrdinaryQueryErrorAndRetry(t *testing.T) {
	s, calls := newNetworkStartupAdmissionState(t, db.ClusterMemberStateCreated)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.ShutdownCtx = ctx
	for attempt := 1; attempt <= 2; attempt++ {
		err := networkStartupOrdinary(s, true)
		require.Zero(t, *calls, "attempt %d must reject a failed transaction before OVS cleanup", attempt)
		require.ErrorContains(t, err, "Failed to read local cluster member state before network startup")
		require.ErrorIs(t, err, context.Canceled)
	}

	s.ShutdownCtx = context.Background()
	require.NoError(t, networkStartupOrdinary(s, true))
	require.Equal(t, 1, *calls)
}

func TestNetworkStartupOrdinaryActive(t *testing.T) {
	for _, tt := range []struct {
		name      string
		clustered bool
	}{
		{"created", true},
		{"standalone", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, calls := newNetworkStartupAdmissionState(t, db.ClusterMemberStateCreated)
			if !tt.clustered {
				// Standalone preserves its original cluster-state lookup bypass.
				s.DB.Cluster.NodeID(999)
			}

			require.NoError(t, networkStartupOrdinary(s, tt.clustered))
			require.Equal(t, 1, *calls, "active startup must reach the isolated OVS callback")
		})
	}
}
