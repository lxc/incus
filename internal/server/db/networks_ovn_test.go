//go:build linux && cgo && !agent

package db_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/shared/api"
)

func TestOVNInitializationClaimAndDeletionGuard(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	id, err := tx.CreateNetwork(ctx, api.ProjectDefaultName, "claim-test", "", db.NetworkTypeOVN, nil)
	require.NoError(t, err)
	err = tx.NetworkCreated(api.ProjectDefaultName, "claim-test")
	require.NoError(t, err)

	err = tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "claim-test", "delete-token", "delete")
	require.NoError(t, err)
	err = tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "claim-test", "other-token", "update")
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
	err = tx.ClaimOVNLocalInitialization(ctx, api.ProjectDefaultName, "claim-test", id, "")
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
	err = tx.ClaimOVNLocalInitialization(ctx, api.ProjectDefaultName, "claim-test", id, "delete-token")
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict))

	err = tx.ReleaseOVNNetworkOperation(ctx, api.ProjectDefaultName, "claim-test", "wrong-token")
	require.NoError(t, err)
	token, err := tx.OVNNetworkOperationToken(ctx, api.ProjectDefaultName, "claim-test")
	require.NoError(t, err)
	require.Equal(t, "delete-token", token)

	err = tx.ReleaseOVNNetworkOperation(ctx, api.ProjectDefaultName, "claim-test", "delete-token")
	require.NoError(t, err)
	err = tx.ClaimOVNLocalInitialization(ctx, api.ProjectDefaultName, "claim-test", id, "")
	require.NoError(t, err)
	_, _, nodes, err := tx.GetNetworkInAnyState(ctx, api.ProjectDefaultName, "claim-test")
	require.NoError(t, err)
	for memberID, node := range nodes {
		require.Equal(t, api.NetworkStatusStarting, db.NetworkStateToAPIStatus(node.State))
		err = tx.ValidateOVNDelete(ctx, id, []int64{memberID})
		require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
	}

	err = tx.NetworkDeleting(api.ProjectDefaultName, "claim-test")
	require.NoError(t, err)
	err = tx.ClaimOVNLocalInitialization(ctx, api.ProjectDefaultName, "claim-test", id, "")
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
	err = tx.ClaimOVNLocalInitialization(ctx, api.ProjectDefaultName, "claim-test", id, "delete-token")
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
}

func TestOVNNotificationOwnership(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	err := tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "receipt-test", "old-token", "update")
	require.NoError(t, err)
	err = tx.AddOVNNotification(ctx, "cancelled", "old-token")
	require.NoError(t, err)
	active, err := tx.CancelOVNNotification(ctx, "cancelled")
	require.NoError(t, err)
	require.False(t, active)
	err = tx.AcceptOVNNotification(ctx, "cancelled", "old-token")
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
	err = tx.AddOVNNotification(ctx, "entered", "old-token")
	require.NoError(t, err)
	err = tx.AcceptOVNNotification(ctx, "entered", "old-token")
	require.NoError(t, err)
	active, err = tx.CancelOVNNotification(ctx, "entered")
	require.NoError(t, err)
	require.True(t, active)
	err = tx.ReleaseOVNNetworkOperation(ctx, api.ProjectDefaultName, "receipt-test", "old-token")
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
	err = tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "receipt-test", "new-token", "delete")
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
	err = tx.FinishOVNNotification(ctx, "entered")
	require.NoError(t, err)
	err = tx.ReleaseOVNNetworkOperation(ctx, api.ProjectDefaultName, "receipt-test", "old-token")
	require.NoError(t, err)
	err = tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "receipt-test", "new-token", "delete")
	require.NoError(t, err)
}

func TestOVNPreparedRequiresExplicitRestore(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	id, err := tx.CreateNetwork(ctx, api.ProjectDefaultName, "prepare-test", "", db.NetworkTypeOVN, nil)
	require.NoError(t, err)
	require.NoError(t, tx.NetworkCreated(api.ProjectDefaultName, "prepare-test"))
	require.NoError(t, tx.NetworkNodeCreated(id))
	require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "prepare-test", "prepare-token", "prepare"))
	require.True(t, api.StatusErrorCheck(tx.OVNLocalPreparing(ctx, id, "prepare-token"), http.StatusConflict))
	require.NoError(t, tx.UpdateNodeStatus(tx.GetNodeID(), db.ClusterMemberStateEvacuating))
	require.NoError(t, tx.OVNLocalPreparing(ctx, id, "prepare-token"))
	require.True(t, api.StatusErrorCheck(tx.ClaimOVNLocalInitialization(ctx, api.ProjectDefaultName, "prepare-test", id, "prepare-token"), http.StatusConflict))
	require.True(t, api.StatusErrorCheck(tx.OVNLocalPrepared(ctx, id, "wrong-token"), http.StatusConflict))
	require.True(t, api.StatusErrorCheck(tx.OVNLocalPrepared(ctx, id, "prepare-token"), http.StatusConflict))
	require.NoError(t, tx.OVNLocalStopped(ctx, api.ProjectDefaultName, "prepare-test", id, "prepare-token"))
	require.NoError(t, tx.OVNLocalPrepared(ctx, id, "prepare-token"))
	require.NoError(t, tx.ReleaseOVNNetworkOperation(ctx, api.ProjectDefaultName, "prepare-test", "prepare-token"))
	require.NoError(t, tx.UpdateNodeStatus(tx.GetNodeID(), db.ClusterMemberStateEvacuated))
	require.NoError(t, tx.ValidateOVNDelete(ctx, id, []int64{tx.GetNodeID()}))
	require.NoError(t, tx.UpdateNodeStatus(tx.GetNodeID(), db.ClusterMemberStateCreated))
	require.True(t, api.StatusErrorCheck(tx.ValidateOVNDelete(ctx, id, []int64{tx.GetNodeID()}), http.StatusConflict))
	require.True(t, api.StatusErrorCheck(tx.ClaimOVNLocalInitialization(ctx, api.ProjectDefaultName, "prepare-test", id, ""), http.StatusConflict))
	require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "prepare-test", "restore-token", "restore"))
	require.NoError(t, tx.UpdateNodeStatus(tx.GetNodeID(), db.ClusterMemberStateRestoring))
	require.True(t, api.StatusErrorCheck(tx.ClaimOVNLocalInitialization(ctx, api.ProjectDefaultName, "prepare-test", id, "stale-token"), http.StatusConflict))
	require.NoError(t, tx.ClaimOVNLocalInitialization(ctx, api.ProjectDefaultName, "prepare-test", id, "restore-token"))
}

func TestOVNStoppedPreservesNeverInitializedProof(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	id, err := tx.CreateNetwork(ctx, api.ProjectDefaultName, "untouched-test", "", db.NetworkTypeOVN, nil)
	require.NoError(t, err)
	require.NoError(t, tx.EnableOVNLocalInitialization(ctx, id))
	require.NoError(t, tx.NetworkCreated(api.ProjectDefaultName, "untouched-test"))
	require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "untouched-test", "stop-token", "stop"))
	require.NoError(t, tx.OVNLocalStopped(ctx, api.ProjectDefaultName, "untouched-test", id, "stop-token"))
	require.NoError(t, tx.ValidateOVNDelete(ctx, id, []int64{tx.GetNodeID()}))
	require.NoError(t, tx.ReleaseOVNNetworkOperation(ctx, api.ProjectDefaultName, "untouched-test", "stop-token"))
	require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "untouched-test", "init-token", "initialize"))
	require.NoError(t, tx.ClaimOVNLocalInitialization(ctx, api.ProjectDefaultName, "untouched-test", id, "init-token"))
	require.NoError(t, tx.OVNLocalStopped(ctx, api.ProjectDefaultName, "untouched-test", id, "init-token"))
	require.True(t, api.StatusErrorCheck(tx.ValidateOVNDelete(ctx, id, []int64{tx.GetNodeID()}), http.StatusConflict))
}

func TestOVNJoinedMemberClaimHasNoDeletionSkip(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	id, err := tx.CreateNetwork(ctx, api.ProjectDefaultName, "joined-test", "", db.NetworkTypeOVN, nil)
	require.NoError(t, err)
	require.NoError(t, tx.NetworkCreated(api.ProjectDefaultName, "joined-test"))
	_, err = tx.Tx().ExecContext(ctx, "DELETE FROM networks_nodes WHERE network_id=?", id)
	require.NoError(t, err)
	require.True(t, api.StatusErrorCheck(tx.ValidateOVNDelete(ctx, id, []int64{tx.GetNodeID()}), http.StatusConflict))
	require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "joined-test", "join-init", "initialize"))
	require.NoError(t, tx.ClaimOVNLocalInitialization(ctx, api.ProjectDefaultName, "joined-test", id, "join-init"))
	_, _, nodes, err := tx.GetNetworkInAnyState(ctx, api.ProjectDefaultName, "joined-test")
	require.NoError(t, err)
	require.Equal(t, api.NetworkStatusStarting, db.NetworkStateToAPIStatus(nodes[tx.GetNodeID()].State))
	require.True(t, api.StatusErrorCheck(tx.ValidateOVNDelete(ctx, id, []int64{tx.GetNodeID()}), http.StatusConflict))
}

func TestOVNRecipientRestartReleasesAbandonedOrigin(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "restart-order", "old-origin", "delete"))
	require.NoError(t, tx.AddOVNNotification(ctx, "old-recipient", "old-origin"))
	require.NoError(t, tx.AcceptOVNNotification(ctx, "old-recipient", "old-origin"))
	// Model an already restarted foreign origin while this recipient still has accepted work.
	_, err := tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_operations SET node_id=?, abandoned=1 WHERE token=?", tx.GetNodeID()+1, "old-origin")
	require.NoError(t, err)
	require.True(t, api.StatusErrorCheck(tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "restart-order", "new-origin", "delete"), http.StatusConflict))
	require.NoError(t, tx.ClearLocalOVNNetworkOperations(ctx))
	require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "restart-order", "new-origin", "delete"))
}

func TestOVNRestartRequiresBackendFence(t *testing.T) {
	for _, originFirst := range []bool{true, false} {
		name := "recipient-first"
		if originFirst {
			name = "origin-first"
		}

		t.Run(name, func(t *testing.T) {
			tx, cleanup := db.NewTestClusterTx(t)
			defer cleanup()
			ctx := context.Background()
			localID := tx.GetNodeID()
			require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "restart", "old", "update"))
			require.NoError(t, tx.AddOVNNotification(ctx, "accepted", "old"))
			require.NoError(t, tx.AcceptOVNNotification(ctx, "accepted", "old"))
			if originFirst {
				_, err := tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_notifications SET node_id=?", localID+1)
				require.NoError(t, err)
			} else {
				_, err := tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_operations SET node_id=?", localID+1)
				require.NoError(t, err)
			}

			pending, err := tx.HasLocalOVNNetworkOperations(ctx)
			require.NoError(t, err)
			require.True(t, pending)
			var abandoned int
			require.NoError(t, tx.Tx().QueryRowContext(ctx, "SELECT abandoned FROM networks_ovn_operations WHERE token='old'").Scan(&abandoned))
			require.Zero(t, abandoned)
			pending, err = tx.OVNNotificationsPending(ctx, "old")
			require.NoError(t, err)
			require.True(t, pending)
			require.True(t, api.StatusErrorCheck(tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "restart", "new", "delete"), http.StatusConflict))

			// Only the first member's completed backend fence authorizes this startup cleanup.
			require.NoError(t, tx.ClearLocalOVNNetworkOperations(ctx))
			require.True(t, api.StatusErrorCheck(tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "restart", "new", "delete"), http.StatusConflict))
			tx.NodeID(localID + 1)
			// The second member's completed fence permits the remaining ownership to be released.
			require.NoError(t, tx.ClearLocalOVNNetworkOperations(ctx))
			pending, err = tx.HasLocalOVNNetworkOperations(ctx)
			require.NoError(t, err)
			require.False(t, pending)
			require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "restart", "new", "delete"))
		})
	}
}

func TestOVNPreviousWorkRequiresBothMemberFences(t *testing.T) {
	for _, originFirst := range []bool{true, false} {
		name := "recipient-first"
		if originFirst {
			name = "origin-first"
		}

		t.Run(name, func(t *testing.T) {
			tx, cleanup := db.NewTestClusterTx(t)
			defer cleanup()
			ctx := context.Background()
			origin := tx.GetNodeID()
			recipient, err := tx.CreateNode("recipient", "192.0.2.2")
			require.NoError(t, err)
			require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "restart", "old", "update"))
			require.NoError(t, tx.AddOVNNotification(ctx, "receipt", "old"))
			tx.NodeID(recipient)
			require.NoError(t, tx.AcceptOVNNotification(ctx, "receipt", "old"))
			recipientWork, err := tx.SnapshotLocalOVNFencedWork(ctx)
			require.NoError(t, err)
			require.Empty(t, recipientWork.OriginTokens)
			require.Equal(t, []db.OVNPreviousReceipt{{ID: "receipt", Token: "old"}}, recipientWork.Receipts)
			tx.NodeID(origin)
			originWork, err := tx.SnapshotLocalOVNFencedWork(ctx)
			require.NoError(t, err)
			require.Equal(t, []string{"old"}, originWork.OriginTokens)
			require.Empty(t, originWork.Receipts)
			first, second := recipientWork, originWork
			if originFirst {
				first, second = originWork, recipientWork
			}

			tx.NodeID(first.NodeID)
			require.NoError(t, tx.ClearPreviousOVNFencedWork(ctx, first))
			require.True(t, api.StatusErrorCheck(tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "restart", "new", "delete"), http.StatusConflict))
			tx.NodeID(second.NodeID)
			require.NoError(t, tx.ClearPreviousOVNFencedWork(ctx, second))
			require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "restart", "new", "delete"))
		})
	}
}

func TestOVNPreviousWorkPreservesNewOriginsAndAcceptedReceipts(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "old-network", "old", "update"))
	require.NoError(t, tx.AddOVNNotification(ctx, "old-accepted", "old"))
	require.NoError(t, tx.AcceptOVNNotification(ctx, "old-accepted", "old"))
	require.NoError(t, tx.AddOVNNotification(ctx, "late-accepted", "old"))
	require.NoError(t, tx.AddOVNNotification(ctx, "old-unaccepted", "old"))
	previous, err := tx.SnapshotLocalOVNFencedWork(ctx)
	require.NoError(t, err)
	require.Equal(t, []db.OVNPreviousReceipt{{ID: "old-accepted", Token: "old"}}, previous.Receipts)
	var abandoned int
	require.NoError(t, tx.Tx().QueryRowContext(ctx, "SELECT abandoned FROM networks_ovn_operations WHERE token='old'").Scan(&abandoned))
	require.Zero(t, abandoned)

	// These origins and accepted callbacks belong to the new daemon, after its startup snapshot.
	require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "new-network", "new", "create"))
	require.NoError(t, tx.AddOVNNotification(ctx, "new-accepted", "new"))
	require.NoError(t, tx.AcceptOVNNotification(ctx, "new-accepted", "new"))
	require.NoError(t, tx.AddOVNNotification(ctx, "new-unaccepted", "new"))
	require.NoError(t, tx.AcceptOVNNotification(ctx, "late-accepted", "old"))
	require.NoError(t, tx.ClearPreviousOVNFencedWork(ctx, previous))
	require.NoError(t, tx.ClearPreviousOVNFencedWork(ctx, previous))
	for _, id := range []string{"old-accepted", "old-unaccepted", "late-accepted", "new-accepted", "new-unaccepted"} {
		var retained bool
		require.NoError(t, tx.Tx().QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM networks_ovn_notifications WHERE id=?)", id).Scan(&retained))
		require.Equal(t, id != "old-accepted" && id != "old-unaccepted", retained)
	}

	require.NoError(t, tx.Tx().QueryRowContext(ctx, "SELECT abandoned FROM networks_ovn_operations WHERE token='old'").Scan(&abandoned))
	require.Equal(t, 1, abandoned)
	require.NoError(t, tx.Tx().QueryRowContext(ctx, "SELECT abandoned FROM networks_ovn_operations WHERE token='new'").Scan(&abandoned))
	require.Zero(t, abandoned)
	require.NoError(t, tx.FinishOVNNotification(ctx, "late-accepted"))
	token, err := tx.OVNNetworkOperationToken(ctx, api.ProjectDefaultName, "old-network")
	require.NoError(t, err)
	require.Empty(t, token)
	token, err = tx.OVNNetworkOperationToken(ctx, api.ProjectDefaultName, "new-network")
	require.NoError(t, err)
	require.Equal(t, "new", token)
}

func TestOVNPreviousWorkRetainsLegacyAndChangedIdentities(t *testing.T) {
	for _, changed := range []string{"legacy", "member", "receipt-token", "receipt-member", "receipt-provenance", "origin-member"} {
		t.Run(changed, func(t *testing.T) {
			tx, cleanup := db.NewTestClusterTx(t)
			defer cleanup()
			ctx := context.Background()
			require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "network", "old", "update"))
			require.NoError(t, tx.AddOVNNotification(ctx, "accepted", "old"))
			require.NoError(t, tx.AcceptOVNNotification(ctx, "accepted", "old"))
			if changed == "legacy" {
				_, err := tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_operations SET backend_fenced=0")
				require.NoError(t, err)
				_, err = tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_notifications SET backend_fenced=0")
				require.NoError(t, err)
			}

			previous, err := tx.SnapshotLocalOVNFencedWork(ctx)
			require.NoError(t, err)
			switch changed {
			case "legacy":
				require.Empty(t, previous.OriginTokens)
				require.Empty(t, previous.Receipts)
			case "member":
				tx.NodeID(tx.GetNodeID() + 1)
			case "receipt-token":
				_, err = tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_notifications SET token='replacement' WHERE id='accepted'")
			case "receipt-member":
				_, err = tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_notifications SET node_id=? WHERE id='accepted'", tx.GetNodeID()+1)
			case "receipt-provenance":
				_, err = tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_notifications SET backend_fenced=0 WHERE id='accepted'")
			case "origin-member":
				_, err = tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_operations SET node_id=? WHERE token='old'", tx.GetNodeID()+1)
			}

			require.NoError(t, err)
			err = tx.ClearPreviousOVNFencedWork(ctx, previous)
			if changed == "member" {
				require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
			} else {
				require.NoError(t, err)
			}

			var retained bool
			require.NoError(t, tx.Tx().QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM networks_ovn_notifications WHERE id='accepted')").Scan(&retained))
			require.Equal(t, changed != "origin-member", retained)
			if changed == "legacy" || changed == "member" || changed == "origin-member" {
				token, err := tx.OVNNetworkOperationToken(ctx, api.ProjectDefaultName, "network")
				require.NoError(t, err)
				require.Equal(t, "old", token)
			}
		})
	}
}

func TestOVNRestartRetainsLegacyWork(t *testing.T) {
	for _, provenance := range []struct {
		name      string
		origin    int
		recipient int
	}{
		{name: "legacy-origin-and-recipient"},
		{name: "legacy-origin", recipient: 1},
		{name: "legacy-recipient", origin: 1},
	} {
		t.Run(provenance.name, func(t *testing.T) {
			tx, cleanup := db.NewTestClusterTx(t)
			defer cleanup()
			ctx := context.Background()
			require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "legacy", "old", "delete"))
			require.NoError(t, tx.AddOVNNotification(ctx, "accepted", "old"))
			require.NoError(t, tx.AcceptOVNNotification(ctx, "accepted", "old"))
			_, err := tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_operations SET backend_fenced=?, abandoned=?", provenance.origin, 1-provenance.origin)
			require.NoError(t, err)
			_, err = tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_notifications SET backend_fenced=?", provenance.recipient)
			require.NoError(t, err)
			require.NoError(t, tx.AddOVNNotification(ctx, "unaccepted", "old"))
			require.NoError(t, tx.ClearLocalOVNNetworkOperations(ctx))
			require.True(t, api.StatusErrorCheck(tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "legacy", "new", "delete"), http.StatusConflict))
			var count int
			require.NoError(t, tx.Tx().QueryRowContext(ctx, "SELECT COUNT(*) FROM networks_ovn_notifications WHERE id='unaccepted'").Scan(&count))
			require.Zero(t, count)
			require.NoError(t, tx.Tx().QueryRowContext(ctx, "SELECT COUNT(*) FROM networks_ovn_notifications WHERE id='accepted'").Scan(&count))
			require.Equal(t, 1-provenance.recipient, count)
			// A live recipient can acknowledge its completed legacy work, but not release an unfenced origin.
			require.NoError(t, tx.FinishOVNNotification(ctx, "accepted"))
			token, err := tx.OVNNetworkOperationToken(ctx, api.ProjectDefaultName, "legacy")
			require.NoError(t, err)
			if provenance.origin == 0 {
				require.Equal(t, "old", token)
			} else {
				require.Empty(t, token)
			}
		})
	}
}

func TestOVNReceiptCompletionCannotBypassOriginFence(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "origin-fence", "old", "update"))
	require.NoError(t, tx.AddOVNNotification(ctx, "accepted", "old"))
	require.NoError(t, tx.AcceptOVNNotification(ctx, "accepted", "old"))
	pending, err := tx.HasLocalOVNNetworkOperations(ctx)
	require.NoError(t, err)
	require.True(t, pending)
	// Completion racing the origin's backend fence must not release its still-unabandoned reservation.
	require.NoError(t, tx.FinishOVNNotification(ctx, "accepted"))
	require.True(t, api.StatusErrorCheck(tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "origin-fence", "new", "delete"), http.StatusConflict))
	require.NoError(t, tx.ClearLocalOVNNetworkOperations(ctx))
	require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "origin-fence", "new", "delete"))
}

func TestOVNBackendIdentitySurvivesMembershipChange(t *testing.T) {
	local, cleanup := db.NewTestNode(t)
	defer cleanup()
	ctx := context.Background()
	var id string
	require.NoError(t, local.Transaction(ctx, func(ctx context.Context, tx *db.NodeTx) error {
		var err error
		id, err = tx.OVNBackendID(ctx)
		return err
	}))
	_, err := uuid.Parse(id)
	require.NoError(t, err)

	for _, membership := range []string{"standalone", "joined"} {
		t.Run(membership, func(t *testing.T) {
			// Joining replaces the cluster database while the private local database survives.
			tx, cleanup := db.NewTestClusterTx(t)
			defer cleanup()
			if membership == "joined" {
				joinedID, err := tx.CreateNode("joined-member", "192.0.2.2")
				require.NoError(t, err)
				tx.NodeID(joinedID)
			}

			require.NoError(t, local.Transaction(ctx, func(ctx context.Context, localTx *db.NodeTx) error {
				retained, err := localTx.OVNBackendID(ctx)
				require.NoError(t, err)
				require.Equal(t, id, retained)
				return nil
			}))
			require.NoError(t, tx.RegisterOVNBackendID(ctx, id))
			_, err := tx.Tx().ExecContext(ctx, "UPDATE nodes SET name='renamed-member' WHERE id=?", tx.GetNodeID())
			require.NoError(t, err)
			require.NoError(t, tx.RegisterOVNBackendID(ctx, id))
			require.ErrorContains(t, tx.RegisterOVNBackendID(ctx, uuid.NewString()), "identity changed")
		})
	}
}

func TestOVNBackendRootsPersistAndRejectUnsafeReplacement(t *testing.T) {
	local, cleanup := db.NewTestNode(t)
	defer cleanup()
	ctx := context.Background()
	var backendID string
	roots := map[string]string{"ovs": uuid.NewString(), "nb": uuid.NewString(), "sb": uuid.NewString()}
	require.NoError(t, local.Transaction(ctx, func(ctx context.Context, tx *db.NodeTx) error {
		_, err := tx.OVNBackendRoot(ctx, "ovs")
		require.ErrorIs(t, err, sql.ErrNoRows)
		backendID, err = tx.OVNBackendID(ctx)
		require.NoError(t, err)
		for kind, root := range roots {
			stored, err := tx.OVNBackendRoot(ctx, kind)
			require.NoError(t, err)
			require.Empty(t, stored)
			// A new client must bind before exposure, so an initial root has no prior fenced effects.
			require.NoError(t, tx.BindOVNBackendRoot(ctx, kind, "", root, true))
		}

		return nil
	}))

	require.NoError(t, local.Transaction(ctx, func(ctx context.Context, tx *db.NodeTx) error {
		retainedID, err := tx.OVNBackendID(ctx)
		require.NoError(t, err)
		require.Equal(t, backendID, retainedID)
		for kind, root := range roots {
			stored, err := tx.OVNBackendRoot(ctx, kind)
			require.NoError(t, err)
			require.Equal(t, root, stored)
			require.NoError(t, tx.BindOVNBackendRoot(ctx, kind, root, root, true))
			replacement := uuid.NewString()
			require.ErrorContains(t, tx.BindOVNBackendRoot(ctx, kind, root, replacement, true), "backend rebinding is blocked")
			stored, err = tx.OVNBackendRoot(ctx, kind)
			require.NoError(t, err)
			require.Equal(t, root, stored)
			require.NoError(t, tx.BindOVNBackendRoot(ctx, kind, root, replacement, false))
			require.ErrorContains(t, tx.BindOVNBackendRoot(ctx, kind, root, replacement, false), "binding changed")
			require.ErrorContains(t, tx.BindOVNBackendRoot(ctx, kind, "", root, false), "binding changed")
			roots[kind] = replacement
		}

		return nil
	}))

	require.NoError(t, local.Transaction(ctx, func(ctx context.Context, tx *db.NodeTx) error {
		retainedID, err := tx.OVNBackendID(ctx)
		require.NoError(t, err)
		require.Equal(t, backendID, retainedID)
		for kind, root := range roots {
			stored, err := tx.OVNBackendRoot(ctx, kind)
			require.NoError(t, err)
			require.Equal(t, root, stored)
		}

		_, err = tx.OVNBackendRoot(ctx, "unknown")
		require.ErrorContains(t, err, "backend kind")
		require.ErrorContains(t, tx.BindOVNBackendRoot(ctx, "unknown", "", uuid.NewString(), false), "backend kind")
		for _, root := range []string{"", "not-a-uuid", uuid.Nil.String()} {
			require.ErrorContains(t, tx.BindOVNBackendRoot(ctx, "ovs", roots["ovs"], root, false), "root UUID")
		}

		require.ErrorContains(t, tx.BindOVNBackendRoot(ctx, "ovs", "invalid", roots["ovs"], false), "root UUID")
		return nil
	}))
}

func TestOVNBackendWorkExcludesOnlyConfigurationOrigin(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	pending, err := tx.HasLocalOVNBackendWork(ctx)
	require.NoError(t, err)
	require.False(t, pending)
	require.NoError(t, tx.AcquireOVNPeerOperation(ctx, "config", "backend-config", true))
	pending, err = tx.HasLocalOVNNetworkOperations(ctx)
	require.NoError(t, err)
	require.True(t, pending)
	pending, err = tx.HasLocalOVNBackendWork(ctx)
	require.NoError(t, err)
	require.False(t, pending)

	require.NoError(t, tx.AddOVNNotification(ctx, "config-receipt", "config"))
	pending, err = tx.HasLocalOVNBackendWork(ctx)
	require.NoError(t, err)
	require.False(t, pending)
	require.NoError(t, tx.AcceptOVNNotification(ctx, "config-receipt", "config"))
	pending, err = tx.HasLocalOVNBackendWork(ctx)
	require.NoError(t, err)
	require.True(t, pending)
	require.NoError(t, tx.FinishOVNNotification(ctx, "config-receipt"))
	require.NoError(t, tx.ReleaseOVNNetworkOperation(ctx, api.ProjectDefaultName, db.OVNPeerOperationName, "config"))

	require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "pending", "work", "create"))
	pending, err = tx.HasLocalOVNBackendWork(ctx)
	require.NoError(t, err)
	require.True(t, pending)
	_, err = tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_operations SET backend_fenced=0 WHERE token='work'")
	require.NoError(t, err)
	pending, err = tx.HasLocalOVNBackendWork(ctx)
	require.NoError(t, err)
	require.True(t, pending)
}

func TestOVNBackendConfigRestartCleanupIsExact(t *testing.T) {
	for _, testCase := range []string{"local-config", "other-origin", "unaccepted-receipt", "accepted-receipt", "network-origin", "remote-peering", "other-name", "other-project", "legacy-config", "acl-config", "address-set-config", "promoted-acl-config", "promoted-address-set-config"} {
		t.Run(testCase, func(t *testing.T) {
			tx, cleanup := db.NewTestClusterTx(t)
			defer cleanup()
			ctx := context.Background()
			projectName, name, operation, fenced := api.ProjectDefaultName, db.OVNPeerOperationName, "backend-config", 1
			switch testCase {
			case "network-origin":
				name, operation = "network", "create"
			case "remote-peering":
				operation, fenced = "peer-create", 0
			case "other-name":
				name = "network"
			case "other-project":
				projectName = "other"
				_, err := tx.Tx().ExecContext(ctx, "INSERT INTO projects (name, description) VALUES (?, '')", projectName)
				require.NoError(t, err)
			case "legacy-config":
				fenced = 0
			case "acl-config", "address-set-config":
				operation, fenced = testCase, 0
			case "promoted-acl-config":
				operation = "acl-config"
			case "promoted-address-set-config":
				operation = "address-set-config"
			}

			origin := tx.GetNodeID()
			if testCase == "other-origin" {
				var err error
				origin, err = tx.CreateNode("other", "192.0.2.2")
				require.NoError(t, err)
			}

			_, err := tx.Tx().ExecContext(ctx, "INSERT INTO networks_ovn_operations (project_id, name, node_id, token, operation, backend_fenced) VALUES ((SELECT id FROM projects WHERE name=?), ?, ?, 'config', ?, ?)", projectName, name, origin, operation, fenced)
			require.NoError(t, err)
			if testCase == "unaccepted-receipt" || testCase == "accepted-receipt" {
				require.NoError(t, tx.AddOVNNotification(ctx, "receipt", "config"))
				if testCase == "accepted-receipt" {
					require.NoError(t, tx.AcceptOVNNotification(ctx, "receipt", "config"))
				}
			}

			require.NoError(t, tx.ClearLocalOVNBackendConfig(ctx))
			var retained bool
			require.NoError(t, tx.Tx().QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM networks_ovn_operations WHERE token='config')").Scan(&retained))
			require.Equal(t, testCase != "local-config" && testCase != "acl-config" && testCase != "address-set-config", retained)
		})
	}
}

func TestOVNBackendFenceEligibilityRetainsUnfencedWork(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	eligible, err := tx.HasLocalFencedOVNWork(ctx)
	require.NoError(t, err)
	require.False(t, eligible)
	require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "network", "work", "create"))
	eligible, err = tx.HasLocalFencedOVNWork(ctx)
	require.NoError(t, err)
	require.True(t, eligible)
	_, err = tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_operations SET backend_fenced=0 WHERE token='work'")
	require.NoError(t, err)
	eligible, err = tx.HasLocalFencedOVNWork(ctx)
	require.NoError(t, err)
	require.False(t, eligible)
	pending, err := tx.HasLocalOVNNetworkOperations(ctx)
	require.NoError(t, err)
	require.True(t, pending)

	require.NoError(t, tx.AddOVNNotification(ctx, "receipt", "work"))
	require.NoError(t, tx.AcceptOVNNotification(ctx, "receipt", "work"))
	eligible, err = tx.HasLocalFencedOVNWork(ctx)
	require.NoError(t, err)
	require.True(t, eligible)
	_, err = tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_notifications SET backend_fenced=0 WHERE id='receipt'")
	require.NoError(t, err)
	eligible, err = tx.HasLocalFencedOVNWork(ctx)
	require.NoError(t, err)
	require.False(t, eligible)

	other, err := tx.CreateNode("other", "192.0.2.2")
	require.NoError(t, err)
	_, err = tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_operations SET node_id=?, backend_fenced=1 WHERE token='work'", other)
	require.NoError(t, err)
	_, err = tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_notifications SET node_id=?, backend_fenced=1 WHERE id='receipt'", other)
	require.NoError(t, err)
	eligible, err = tx.HasLocalFencedOVNWork(ctx)
	require.NoError(t, err)
	require.False(t, eligible)
}

func TestOVNBackendRebindingRequiresEmptyClusterState(t *testing.T) {
	for _, testCase := range []string{"empty", "bridge", "config-only", "pending-definition", "created-definition", "errored-definition", "other-project-definition", "remote-origin", "legacy-origin", "unaccepted-receipt", "legacy-receipt"} {
		t.Run(testCase, func(t *testing.T) {
			tx, cleanup := db.NewTestClusterTx(t)
			defer cleanup()
			ctx := context.Background()
			projectName := api.ProjectDefaultName
			if testCase == "other-project-definition" {
				projectName = "other"
				_, err := tx.Tx().ExecContext(ctx, "INSERT INTO projects (name, description) VALUES (?, '')", projectName)
				require.NoError(t, err)
			}

			switch testCase {
			case "bridge", "pending-definition", "created-definition", "errored-definition", "other-project-definition":
				networkType := db.NetworkTypeOVN
				if testCase == "bridge" {
					networkType = db.NetworkTypeBridge
				}

				_, err := tx.CreateNetwork(ctx, projectName, "network", "", networkType, nil)
				require.NoError(t, err)
				switch testCase {
				case "created-definition":
					require.NoError(t, tx.NetworkCreated(projectName, "network"))
				case "errored-definition":
					require.NoError(t, tx.NetworkErrored(projectName, "network"))
				}

			case "config-only", "unaccepted-receipt", "legacy-receipt":
				require.NoError(t, tx.AcquireOVNPeerOperation(ctx, "config", "backend-config", true))
				if testCase != "config-only" {
					require.NoError(t, tx.AddOVNNotification(ctx, "receipt", "config"))
					if testCase == "legacy-receipt" {
						require.NoError(t, tx.AcceptOVNNotification(ctx, "receipt", "config"))
						_, err := tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_notifications SET backend_fenced=0 WHERE id='receipt'")
						require.NoError(t, err)
					}
				}
			case "remote-origin", "legacy-origin":
				require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, projectName, "network", "work", "create"))
				other, err := tx.CreateNode("other", "192.0.2.2")
				require.NoError(t, err)
				_, err = tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_operations SET node_id=? WHERE token='work'", other)
				require.NoError(t, err)
				if testCase == "legacy-origin" {
					_, err = tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_operations SET backend_fenced=0 WHERE token='work'")
					require.NoError(t, err)
				}
			}

			blocked, err := tx.OVNBackendRebindBlocked(ctx)
			require.NoError(t, err)
			require.Equal(t, testCase != "empty" && testCase != "bridge" && testCase != "config-only", blocked)
		})
	}
}

func TestOVNBackendRegistrationRejectsMissingProvenance(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	id := uuid.NewString()
	require.NoError(t, tx.RegisterOVNBackendID(ctx, id))
	require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "identity", "old", "create"))
	_, err := tx.Tx().ExecContext(ctx, "DELETE FROM networks_ovn_members WHERE node_id=?", tx.GetNodeID())
	require.NoError(t, err)
	require.ErrorContains(t, tx.RegisterOVNBackendID(ctx, id), "identity is missing")
	require.NoError(t, tx.AddOVNNotification(ctx, "accepted", "old"))
	require.NoError(t, tx.AcceptOVNNotification(ctx, "accepted", "old"))
	_, err = tx.Tx().ExecContext(ctx, "UPDATE networks_ovn_operations SET node_id=?", tx.GetNodeID()+1)
	require.NoError(t, err)
	require.ErrorContains(t, tx.RegisterOVNBackendID(ctx, id), "identity is missing")
	var count int
	require.NoError(t, tx.Tx().QueryRowContext(ctx, "SELECT COUNT(*) FROM networks_ovn_members").Scan(&count))
	require.Zero(t, count)
}

func TestOVNBackendFenceMigrationRetainsLegacyProvenance(t *testing.T) {
	schema := cluster.Schema()
	database, err := schema.ExerciseUpdate(81, func(database *sql.DB) {
		_, err := database.Exec("INSERT OR IGNORE INTO projects (id, name, description) VALUES (1, 'default', '')")
		require.NoError(t, err)
		_, err = database.Exec("INSERT INTO networks_ovn_operations (project_id, name, node_id, token, operation, abandoned) VALUES (1, 'legacy', 1, 'old', 'delete', 1)")
		require.NoError(t, err)
		_, err = database.Exec("INSERT INTO networks_ovn_notifications (id, token, node_id) VALUES ('accepted', 'old', 2)")
		require.NoError(t, err)
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, database.Close()) }()
	var origin int
	var recipient int
	require.NoError(t, database.QueryRow("SELECT backend_fenced FROM networks_ovn_operations WHERE token='old'").Scan(&origin))
	require.NoError(t, database.QueryRow("SELECT backend_fenced FROM networks_ovn_notifications WHERE id='accepted'").Scan(&recipient))
	require.Zero(t, origin)
	require.Zero(t, recipient)
}

func TestOVNPeerReservationExcludesLifecycle(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()

	require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "child", "child-origin", "nic"))
	require.True(t, api.StatusErrorCheck(tx.AcquireOVNPeerOperation(ctx, "peer", "peer-create", true), http.StatusConflict))
	require.NoError(t, tx.ReleaseOVNNetworkOperation(ctx, api.ProjectDefaultName, "child", "child-origin"))
	require.NoError(t, tx.AcquireOVNPeerOperation(ctx, "peer", "peer-create", true))
	require.True(t, api.StatusErrorCheck(tx.AcquireOVNPeerOperation(ctx, "another-peer", "peer-delete", true), http.StatusConflict))
	for _, operation := range []string{"create", "delete", "prepare", "restore", "nic", "update"} {
		require.True(t, api.StatusErrorCheck(tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "unrelated", operation, operation), http.StatusConflict))
	}

	require.NoError(t, tx.ClearLocalOVNNetworkOperations(ctx))
	require.NoError(t, tx.AcquireOVNPeerOperation(ctx, "remote", "peer-delete", false))
	require.NoError(t, tx.ClearLocalOVNNetworkOperations(ctx))
	token, err := tx.OVNNetworkOperationToken(ctx, api.ProjectDefaultName, db.OVNPeerOperationName)
	require.NoError(t, err)
	require.Equal(t, "remote", token)
	require.True(t, api.StatusErrorCheck(tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "replacement", "replacement", "create"), http.StatusConflict))
	require.True(t, api.StatusErrorCheck(tx.AcquireOVNPeerOperation(ctx, "integration", "peer-integration", true), http.StatusConflict))

	// Only the live operation's acknowledged completion releases an unfenced reservation.
	require.NoError(t, tx.ReleaseOVNNetworkOperation(ctx, api.ProjectDefaultName, db.OVNPeerOperationName, "wrong-token"))
	token, err = tx.OVNNetworkOperationToken(ctx, api.ProjectDefaultName, db.OVNPeerOperationName)
	require.NoError(t, err)
	require.Equal(t, "remote", token)
	require.NoError(t, tx.ReleaseOVNNetworkOperation(ctx, api.ProjectDefaultName, db.OVNPeerOperationName, "remote"))
	require.NoError(t, tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "replacement", "replacement", "create"))
}

func TestOVNPeerReadinessRequiresCreatedActiveMember(t *testing.T) {
	tx, cleanup := db.NewTestClusterTx(t)
	defer cleanup()
	ctx := context.Background()
	id, err := tx.CreateNetwork(ctx, api.ProjectDefaultName, "peering", "", db.NetworkTypeOVN, nil)
	require.NoError(t, err)
	require.NoError(t, tx.NetworkCreated(api.ProjectDefaultName, "peering"))
	require.True(t, api.StatusErrorCheck(tx.ValidateOVNPeerReady(ctx, api.ProjectDefaultName, "peering", id), http.StatusConflict))
	require.NoError(t, tx.NetworkNodeCreated(id))
	require.NoError(t, tx.ValidateOVNPeerReady(ctx, api.ProjectDefaultName, "peering", id))
	require.True(t, api.StatusErrorCheck(tx.ValidateOVNPeerReady(ctx, api.ProjectDefaultName, "peering", id+1), http.StatusConflict))

	for state := db.NetworkState(0); state <= 7; state++ {
		if db.NetworkStateToAPIStatus(state) == api.NetworkStatusCreated {
			continue
		}

		t.Run(db.NetworkStateToAPIStatus(state), func(t *testing.T) {
			_, err := tx.Tx().ExecContext(ctx, "UPDATE networks_nodes SET state=? WHERE network_id=?", state, id)
			require.NoError(t, err)
			require.True(t, api.StatusErrorCheck(tx.ValidateOVNPeerReady(ctx, api.ProjectDefaultName, "peering", id), http.StatusConflict))
			require.NoError(t, tx.NetworkNodeCreated(id))
			_, err = tx.Tx().ExecContext(ctx, "UPDATE networks SET state=? WHERE id=?", state, id)
			require.NoError(t, err)
			require.True(t, api.StatusErrorCheck(tx.ValidateOVNPeerReady(ctx, api.ProjectDefaultName, "peering", id), http.StatusConflict))
			require.NoError(t, tx.NetworkCreated(api.ProjectDefaultName, "peering"))
		})
	}

	for _, state := range []int{db.ClusterMemberStatePending, db.ClusterMemberStateEvacuating, db.ClusterMemberStateEvacuated, db.ClusterMemberStateRestoring} {
		require.NoError(t, tx.UpdateNodeStatus(tx.GetNodeID(), state))
		require.True(t, api.StatusErrorCheck(tx.ValidateOVNPeerReady(ctx, api.ProjectDefaultName, "peering", id), http.StatusConflict))
	}

	require.NoError(t, tx.UpdateNodeStatus(tx.GetNodeID(), db.ClusterMemberStateCreated))
	require.NoError(t, tx.ValidateOVNPeerReady(ctx, api.ProjectDefaultName, "peering", id))
}

func TestOVNPeerIntegrationReservationIsTransactionLocal(t *testing.T) {
	database, cleanup := db.NewTestCluster(t)
	defer cleanup()
	ctx := context.Background()
	update := func(name string, failure error) error {
		return database.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.WithOVNPeerIntegrationOperation(ctx, func() error {
				_, err := cluster.CreateNetworkIntegration(ctx, tx.Tx(), cluster.NetworkIntegration{Name: name, Type: 0})
				if err != nil {
					return err
				}

				return failure
			})
		})
	}

	require.NoError(t, update("successful", nil))
	failure := errors.New("integration transaction failed")
	require.ErrorIs(t, update("rolled-back", failure), failure)

	err := database.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		pending, err := tx.HasLocalOVNNetworkOperations(ctx)
		require.NoError(t, err)
		require.False(t, pending)
		_, err = cluster.GetNetworkIntegration(ctx, tx.Tx(), "successful")
		require.NoError(t, err)
		_, err = cluster.GetNetworkIntegration(ctx, tx.Tx(), "rolled-back")
		require.Error(t, err)
		return tx.AcquireOVNPeerOperation(ctx, "uncertain-remote", "peer-create", false)
	})
	require.NoError(t, err)
	require.True(t, api.StatusErrorCheck(update("blocked", nil), http.StatusConflict))
	err = database.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := cluster.GetNetworkIntegration(ctx, tx.Tx(), "blocked")
		require.Error(t, err)
		token, err := tx.OVNNetworkOperationToken(ctx, api.ProjectDefaultName, db.OVNPeerOperationName)
		require.NoError(t, err)
		require.Equal(t, "uncertain-remote", token)
		return nil
	})
	require.NoError(t, err)
}

func TestOVNDeleteSkippedMemberProvenance(t *testing.T) {
	for _, tc := range []struct {
		name    string
		state   db.NetworkState
		member  int
		marker  bool
		missing bool
		want    bool
	}{
		{"pending-with-marker", 0, db.ClusterMemberStateCreated, true, false, true},
		{"pending-in-maintenance-with-marker", 0, db.ClusterMemberStateEvacuated, true, false, true},
		{"legacy-pending-without-marker", 0, db.ClusterMemberStateCreated, false, false, false},
		{"missing-local-row", 0, db.ClusterMemberStateCreated, true, true, false},
		{"created", 1, db.ClusterMemberStateCreated, true, false, false},
		{"starting", 3, db.ClusterMemberStateCreated, true, false, false},
		{"stopped", 7, db.ClusterMemberStateCreated, true, false, false},
		{"preparing", 5, db.ClusterMemberStateEvacuating, true, false, false},
		{"prepared-evacuated-without-marker", 6, db.ClusterMemberStateEvacuated, false, false, true},
		{"prepared-evacuated-with-marker", 6, db.ClusterMemberStateEvacuated, true, false, true},
		{"prepared-evacuating", 6, db.ClusterMemberStateEvacuating, true, false, false},
		{"prepared-restoring", 6, db.ClusterMemberStateRestoring, true, false, false},
		{"prepared-active", 6, db.ClusterMemberStateCreated, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, cleanup := db.NewTestClusterTx(t)
			defer cleanup()
			ctx := context.Background()
			id, err := tx.CreateNetwork(ctx, api.ProjectDefaultName, "delete-provenance", "", db.NetworkTypeOVN, nil)
			require.NoError(t, err)
			if tc.marker {
				require.NoError(t, tx.EnableOVNLocalInitialization(ctx, id))
			}

			require.NoError(t, tx.UpdateNodeStatus(tx.GetNodeID(), tc.member))
			if tc.missing {
				_, err = tx.Tx().ExecContext(ctx, "DELETE FROM networks_nodes WHERE network_id=?", id)
			} else {
				_, err = tx.Tx().ExecContext(ctx, "UPDATE networks_nodes SET state=? WHERE network_id=?", tc.state, id)
			}

			require.NoError(t, err)
			err = tx.ValidateOVNDelete(ctx, id, []int64{tx.GetNodeID()})
			if tc.want {
				require.NoError(t, err)
			} else {
				require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
			}
		})
	}
}

func TestOVNDeleteInitializationAdmissionOrder(t *testing.T) {
	for _, initializeFirst := range []bool{true, false} {
		name := "delete-first"
		if initializeFirst {
			name = "initialize-first"
		}

		t.Run(name, func(t *testing.T) {
			c, cleanup := db.NewTestCluster(t)
			defer cleanup()
			ctx := context.Background()
			var id int64
			require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				var err error
				id, err = tx.CreateNetwork(ctx, api.ProjectDefaultName, "delete-order", "", db.NetworkTypeOVN, nil)
				if err != nil {
					return err
				}

				return tx.EnableOVNLocalInitialization(ctx, id)
			}))
			if initializeFirst {
				require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					return tx.ClaimOVNLocalInitialization(ctx, api.ProjectDefaultName, "delete-order", id, "")
				}))
			}

			err := c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				err := tx.AcquireOVNNetworkOperation(ctx, api.ProjectDefaultName, "delete-order", "delete-token", "delete")
				if err != nil {
					return err
				}

				err = tx.ValidateOVNDelete(ctx, id, []int64{tx.GetNodeID()})
				if err != nil {
					return err
				}

				return tx.NetworkDeleting(api.ProjectDefaultName, "delete-order")
			})
			if initializeFirst {
				require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
				require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					actualID, info, nodes, err := tx.GetNetworkInAnyState(ctx, api.ProjectDefaultName, "delete-order")
					require.NoError(t, err)
					require.Equal(t, id, actualID)
					require.Equal(t, api.NetworkStatusCreated, info.Status)
					require.Equal(t, api.NetworkStatusStarting, db.NetworkStateToAPIStatus(nodes[tx.GetNodeID()].State))
					token, err := tx.OVNNetworkOperationToken(ctx, api.ProjectDefaultName, "delete-order")
					require.NoError(t, err)
					require.Empty(t, token)
					return nil
				}))
			} else {
				require.NoError(t, err)
				err = c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					return tx.ClaimOVNLocalInitialization(ctx, api.ProjectDefaultName, "delete-order", id, "")
				})
				require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
				require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
					actualID, info, nodes, err := tx.GetNetworkInAnyState(ctx, api.ProjectDefaultName, "delete-order")
					require.NoError(t, err)
					require.Equal(t, id, actualID)
					require.Equal(t, api.NetworkStatusDeleting, info.Status)
					require.Equal(t, api.NetworkStatusPending, db.NetworkStateToAPIStatus(nodes[tx.GetNodeID()].State))
					require.NoError(t, tx.ValidateOVNDelete(ctx, id, []int64{tx.GetNodeID()}))
					return nil
				}))
			}
		})
	}
}
