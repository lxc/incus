//go:build linux && cgo && !agent

package network

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	networkOVN "github.com/lxc/incus/v7/internal/server/network/ovn"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
)

func newOVNAdmissionTestNetwork(t *testing.T) *ovn {
	t.Helper()
	s, cleanup := state.NewTestState(t)
	t.Cleanup(cleanup)
	// These tests stop at selection or maintenance admission, before Start.
	s.OVN = func() (*networkOVN.NB, *networkOVN.SB, error) { return &networkOVN.NB{}, &networkOVN.SB{}, nil }
	var id int64
	err := s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		id, err = tx.CreateNetwork(ctx, api.ProjectDefaultName, "admission-test", "", db.NetworkTypeOVN, nil)
		if err != nil {
			return err
		}

		return tx.NetworkNodeCreated(id)
	})
	require.NoError(t, err)
	loaded, err := LoadByName(s, api.ProjectDefaultName, "admission-test")
	require.NoError(t, err)
	n, ok := loaded.(*ovn)
	require.True(t, ok)
	require.Equal(t, api.NetworkStatusCreated, n.Status())
	ovnLocalStates.Lock()
	previousLocal, hadLocal := ovnLocalStates.members[id]
	ovnLocalStates.Unlock()
	pn := ProjectNetwork{ProjectName: n.project, NetworkName: n.name}
	unavailableNetworksMu.Lock()
	_, wasUnavailable := unavailableNetworks[pn]
	unavailableNetworksMu.Unlock()
	t.Cleanup(func() {
		ovnLocalStates.Lock()
		if hadLocal {
			ovnLocalStates.members[id] = previousLocal
		} else {
			delete(ovnLocalStates.members, id)
		}

		ovnLocalStates.Unlock()
		unavailableNetworksMu.Lock()
		if wasUnavailable {
			unavailableNetworks[pn] = struct{}{}
		} else {
			delete(unavailableNetworks, pn)
		}

		unavailableNetworksMu.Unlock()
	})
	n.setLocalState(ovnLocalState{started: true})
	n.setAvailable()
	return n
}

func TestOVNNeedsInitializationReadiness(t *testing.T) {
	n := newOVNAdmissionTestNetwork(t)
	localID := n.state.DB.Cluster.GetNodeID()
	for _, tc := range []struct {
		name      string
		started   bool
		available bool
		want      bool
	}{
		{"created-without-local-start", false, true, true},
		{"created-unavailable", true, false, true},
		{"created-without-start-and-unavailable", false, false, true},
		{"created-ready", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n.setLocalState(ovnLocalState{started: tc.started})
			if tc.available {
				n.setAvailable()
			} else {
				n.setUnavailable()
			}

			// The DB's Created record alone is not local readiness.
			require.Equal(t, api.NetworkStatusCreated, db.NetworkStateToAPIStatus(n.nodes[localID].State))
			if tc.want {
				require.Equal(t, api.NetworkStatusUnavailable, n.LocalStatus())
			} else {
				require.Equal(t, api.NetworkStatusCreated, n.LocalStatus())
			}

			require.Equal(t, tc.want, OVNNeedsInitialization(n))
		})
	}
}

func TestOVNNeedsInitializationAdmissionMatrix(t *testing.T) {
	n := newOVNAdmissionTestNetwork(t)
	localID := n.state.DB.Cluster.GetNodeID()
	for _, global := range []string{api.NetworkStatusCreated, api.NetworkStatusPending, api.NetworkStatusErrored, api.NetworkStatusDeleting} {
		t.Run(global, func(t *testing.T) {
			n.status = global
			for _, tc := range []struct {
				status  string
				state   db.NetworkState
				missing bool
				want    bool
			}{
				{api.NetworkStatusPending, 0, false, true},
				{api.NetworkStatusCreated, 1, false, true},
				{api.NetworkStatusErrored, 2, false, false},
				{api.NetworkStatusStarting, 3, false, true},
				{api.NetworkStatusDeleting, 4, false, false},
				{api.NetworkStatusPreparing, 5, false, false},
				{api.NetworkStatusPrepared, 6, false, false},
				{api.NetworkStatusStopped, 7, false, true},
				{api.NetworkStatusUnknown, 0, true, true},
			} {
				t.Run(tc.status, func(t *testing.T) {
					if tc.missing {
						delete(n.nodes, localID)
					} else {
						require.Equal(t, tc.status, db.NetworkStateToAPIStatus(tc.state))
						n.nodes[localID] = db.NetworkNode{ID: localID, State: tc.state}
					}

					for _, available := range []bool{true, false} {
						for _, started := range []bool{true, false} {
							n.setLocalState(ovnLocalState{started: started})
							if available {
								n.setAvailable()
							} else {
								n.setUnavailable()
							}

							want := global == api.NetworkStatusCreated && tc.want && (tc.status != api.NetworkStatusCreated || !started || !available)
							require.Equal(t, want, OVNNeedsInitialization(n), "started=%v available=%v", started, available)
						}
					}
				})
			}
		})
	}

	bridge := &bridge{}
	bridge.status = api.NetworkStatusCreated
	require.False(t, OVNNeedsInitialization(bridge))
}

func TestOVNInitializationMaintenanceAdmission(t *testing.T) {
	for _, member := range []struct {
		name  string
		state int
	}{
		{"evacuating", db.ClusterMemberStateEvacuating},
		{"evacuated", db.ClusterMemberStateEvacuated},
		{"restoring", db.ClusterMemberStateRestoring},
	} {
		t.Run(member.name, func(t *testing.T) {
			for _, started := range []bool{false, true} {
				t.Run(map[bool]string{false: "unready", true: "ready"}[started], func(t *testing.T) {
					n := newOVNAdmissionTestNetwork(t)
					n.setLocalState(ovnLocalState{started: started})
					ctx := context.Background()
					require.NoError(t, n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
						return tx.UpdateNodeStatus(tx.GetNodeID(), member.state)
					}))
					// Heartbeat quietly skips maintenance, while workloads must refuse it.
					require.NoError(t, EnsureOVNReturning(n))
					require.True(t, api.StatusErrorCheck(EnsureOVNLocal(n), http.StatusConflict))
					require.NoError(t, n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
						nodes, err := tx.NetworkNodes(ctx, n.id)
						if err != nil {
							return err
						}

						require.Equal(t, api.NetworkStatusCreated, db.NetworkStateToAPIStatus(nodes[tx.GetNodeID()].State))
						operation, err := tx.OVNNetworkOperation(ctx, n.project, n.name)
						require.NoError(t, err)
						require.Empty(t, operation)
						token, err := tx.OVNNetworkOperationToken(ctx, n.project, n.name)
						require.NoError(t, err)
						require.Empty(t, token)
						return nil
					}))
				})
			}
		})
	}
}
