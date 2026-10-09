package network

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	dbCluster "github.com/lxc/incus/v7/internal/server/db/cluster"
	networkOVN "github.com/lxc/incus/v7/internal/server/network/ovn"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
)

func TestOVNPeerUncertainWriteRetainsRollbackState(t *testing.T) {
	s := &state.State{ShutdownCtx: context.Background()}
	n := &ovn{ovnOperationUncertain: true}
	n.netType = "ovn"
	rolledBack := false
	OVNRevert(s, n, func() { rolledBack = true })
	require.False(t, rolledBack)

	n.ovnOperationUncertain = false
	OVNRevert(s, n, func() { rolledBack = true })
	require.True(t, rolledBack)
}

func newPeerTestNetwork(t *testing.T) *ovn {
	t.Helper()
	s, cleanup := state.NewTestState(t)
	t.Cleanup(cleanup)
	// Pending peer definitions and lifecycle guards do not dispatch backend operations.
	s.OVN = func() (*networkOVN.NB, *networkOVN.SB, error) { return &networkOVN.NB{}, &networkOVN.SB{}, nil }
	var id int64
	err := s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		id, err = tx.CreateNetwork(ctx, api.ProjectDefaultName, "peer-test", "", db.NetworkTypeOVN, nil)
		if err != nil {
			return err
		}

		return tx.NetworkNodeCreated(id)
	})
	require.NoError(t, err)
	loaded, err := LoadByName(s, api.ProjectDefaultName, "peer-test")
	require.NoError(t, err)
	n, ok := loaded.(*ovn)
	require.True(t, ok)
	n.setLocalState(ovnLocalState{started: true})
	t.Cleanup(func() {
		ovnLocalStates.Lock()
		delete(ovnLocalStates.members, id)
		ovnLocalStates.Unlock()
	})
	return n
}

func TestOVNPeerPendingDefinitionAndMaintenanceGate(t *testing.T) {
	n := newPeerTestNetwork(t)
	peer := api.NetworkPeersPost{Name: "pending", TargetNetwork: "later"}
	require.NoError(t, n.PeerCreate(peer))
	require.NoError(t, n.PeerUpdate(peer.Name, api.NetworkPeerPut{Description: "pending target"}))

	ctx := context.Background()
	err := n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.UpdateNodeStatus(tx.GetNodeID(), db.ClusterMemberStateRestoring)
	})
	require.NoError(t, err)
	require.True(t, api.StatusErrorCheck(n.PeerDelete(peer.Name), http.StatusConflict))
	require.True(t, api.StatusErrorCheck(n.PeerUpdate(peer.Name, api.NetworkPeerPut{}), http.StatusConflict))
	require.True(t, api.StatusErrorCheck(n.PeerCreate(api.NetworkPeersPost{Name: "another", TargetNetwork: "later"}), http.StatusConflict))
	err = n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := dbCluster.GetNetworkPeer(ctx, tx.Tx(), n.id, peer.Name)
		if err != nil {
			return err
		}

		return tx.UpdateNodeStatus(tx.GetNodeID(), db.ClusterMemberStateCreated)
	})
	require.NoError(t, err)

	n.setLocalState(ovnLocalState{})
	require.True(t, api.StatusErrorCheck(n.PeerDelete(peer.Name), http.StatusConflict))
	n.setLocalState(ovnLocalState{started: true})
	require.NoError(t, n.PeerDelete(peer.Name))
}

func TestOVNPeerLegacyUncertainReservationSurvivesRestart(t *testing.T) {
	n := newPeerTestNetwork(t)
	release, err := n.acquirePeerOperation("peer-create")
	require.NoError(t, err)
	// Only pre-fencing origins retain raw interconnect transaction uncertainty.
	require.NoError(t, n.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_operations SET backend_fenced=0 WHERE token=?", n.ovnOperationToken)
		return err
	}))
	n.ovnOperationUncertain = true
	require.ErrorContains(t, release(), "uncertain outcome")
	err = n.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		err := tx.ClearLocalOVNNetworkOperations(ctx)
		if err != nil {
			return err
		}

		return tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "replacement", "replacement", "create")
	})
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
	err = n.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.WithOVNPeerIntegrationOperation(ctx, func() error { return nil })
	})
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
}

func TestOVNNICAddRejectsMaintenanceBeforeBackendWrites(t *testing.T) {
	n := newPeerTestNetwork(t)
	err := n.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.UpdateNodeStatus(tx.GetNodeID(), db.ClusterMemberStateEvacuated)
	})
	require.NoError(t, err)
	require.True(t, api.StatusErrorCheck(n.InstanceDevicePortAdd("instance-uuid", "eth0", nil), http.StatusConflict))
}

func TestOVNNICCleanupPreservesParentReservationAndShutdown(t *testing.T) {
	n := newPeerTestNetwork(t)
	release, token, err := AcquireOVNOperation(n.state, n.project, n.name, "delete")
	require.NoError(t, err)
	unlock, err := LockOVNLifecycle(n.project, n.name)
	require.NoError(t, err)
	AuthorizeOVNInitialization(n, token)
	nestedRelease, err := n.waitLocalLifecycle()
	require.NoError(t, err)
	require.NoError(t, nestedRelease())
	err = n.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		current, err := tx.OVNNetworkOperationToken(ctx, n.project, n.name)
		require.NoError(t, err)
		require.Equal(t, token, current)
		return tx.UpdateNodeStatus(tx.GetNodeID(), db.ClusterMemberStateEvacuated)
	})
	require.NoError(t, err)
	n.ovnOperationToken = ""
	unlock()
	require.NoError(t, release())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n.state.ShutdownCtx = ctx
	release, err = n.waitLocalLifecycle()
	require.NoError(t, err)
	require.NoError(t, release())
}

func TestOVNNICDeviceGuardCoversNestedDriverAndSharedWrites(t *testing.T) {
	n := newPeerTestNetwork(t)
	release, err := AcquireOVNNICOperation(n, false)
	require.NoError(t, err)
	require.NoError(t, EnsureOVNLocal(n))
	token := n.ovnOperationToken
	require.NotEmpty(t, token)

	// The device keeps ownership before and after the nested driver call, covering its direct ACL writes.
	for _, phase := range []string{"before-driver", "after-driver"} {
		t.Run(phase, func(t *testing.T) {
			err := n.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				current, err := tx.OVNNetworkOperationToken(ctx, n.project, n.name)
				require.NoError(t, err)
				require.Equal(t, token, current)
				require.True(t, api.StatusErrorCheck(tx.AcquireOVNNetworkOperation(ctx, n.project, n.name, "delete", "delete"), http.StatusConflict))
				require.True(t, api.StatusErrorCheck(tx.AcquireOVNPeerOperation(ctx, "acl", "acl-config", false), http.StatusConflict))
				return nil
			})
			require.NoError(t, err)
		})
		if phase == "before-driver" {
			nestedRelease, err := n.waitOperation("nic")
			require.NoError(t, err)
			require.NoError(t, nestedRelease())
			require.Equal(t, token, n.ovnOperationToken)
		}
	}

	require.NoError(t, release())
	require.Empty(t, n.ovnOperationToken)
	err = n.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.AcquireOVNNetworkOperation(ctx, n.project, n.name, "delete", "delete")
	})
	require.NoError(t, err)
	// An ended parent token must not authorize a nested writer against its replacement.
	n.ovnOperationToken = token
	_, err = n.waitOperation("nic", true)
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
}

func TestOVNNICDeviceGuardRequiresReadinessOnlyForUpdates(t *testing.T) {
	n := newPeerTestNetwork(t)
	err := n.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.UpdateNodeStatus(tx.GetNodeID(), db.ClusterMemberStateEvacuated)
	})
	require.NoError(t, err)
	_, err = AcquireOVNNICOperation(n, false)
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
	require.Empty(t, n.ovnOperationToken)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n.state.ShutdownCtx = ctx
	release, err := AcquireOVNNICOperation(n, true)
	require.NoError(t, err)
	nestedRelease, err := n.waitOperation("nic", true)
	require.NoError(t, err)
	require.NoError(t, nestedRelease())
	require.NoError(t, release())
}
