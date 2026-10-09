//go:build linux && cgo && !agent

package network

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/endpoints"
	"github.com/lxc/incus/v7/internal/server/node"
	"github.com/lxc/incus/v7/shared/api"
	localtls "github.com/lxc/incus/v7/shared/tls"
	"github.com/lxc/incus/v7/shared/tls/tlstest"
)

func newOVNDeleteAdmissionTestNetwork(t *testing.T) *ovn {
	t.Helper()
	n := newOVNAdmissionTestNetwork(t)
	cert := tlstest.TestingKeyPair(t)
	n.state.Endpoints = &endpoints.Endpoints{}
	n.state.Endpoints.NetworkUpdateCert(cert)
	n.state.ServerCert = func() *localtls.CertInfo { return cert }
	err := n.state.DB.Node.Transaction(context.Background(), func(ctx context.Context, tx *db.NodeTx) error {
		var err error
		n.state.LocalConfig, err = node.ConfigLoad(ctx, tx)
		return err
	})
	require.NoError(t, err)
	// Selection is tested through the real notifier, without connectivity or delivery.
	require.Empty(t, n.state.LocalConfig.ClusterAddress())
	require.NoError(t, n.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.AcquireOVNNetworkOperation(ctx, n.project, n.name, "delete-admission-token", "delete")
	}))
	return n
}

func TestOVNDeleteNotifierAdmission(t *testing.T) {
	for _, tc := range []struct {
		name    string
		state   db.NetworkState
		member  int
		marker  bool
		missing bool
		want    bool
	}{
		{"pending-with-marker", 0, db.ClusterMemberStateCreated, true, false, true},
		{"pending-with-marker-in-maintenance", 0, db.ClusterMemberStateEvacuated, true, false, true},
		{"legacy-pending-without-marker", 0, db.ClusterMemberStateCreated, false, false, false},
		{"missing-local-row", 0, db.ClusterMemberStateCreated, true, true, false},
		{"starting", 3, db.ClusterMemberStateCreated, true, false, false},
		{"stopped", 7, db.ClusterMemberStateCreated, true, false, false},
		{"preparing", 5, db.ClusterMemberStateEvacuating, true, false, false},
		{"prepared-evacuated-without-marker", 6, db.ClusterMemberStateEvacuated, false, false, true},
		{"prepared-evacuated-with-marker", 6, db.ClusterMemberStateEvacuated, true, false, true},
		{"prepared-restoring", 6, db.ClusterMemberStateRestoring, true, false, false},
		{"prepared-active", 6, db.ClusterMemberStateCreated, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := newOVNDeleteAdmissionTestNetwork(t)
			var memberID int64
			err := n.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				var err error
				memberID, err = tx.CreateNode("delete-member", "192.0.2.2")
				if err != nil {
					return err
				}

				if tc.marker {
					require.NoError(t, tx.EnableOVNLocalInitialization(ctx, n.id))
				}

				require.NoError(t, tx.UpdateNodeStatus(memberID, tc.member))
				if !tc.missing {
					_, err = tx.Tx().ExecContext(ctx, "INSERT INTO networks_nodes (network_id, node_id, state) VALUES (?, ?, ?)", n.id, memberID, tc.state)
				}

				return err
			})
			require.NoError(t, err)
			notifier, skipped, err := OVNDeleteNotifier(n.state, n)
			require.NoError(t, err)
			require.NotNil(t, notifier)
			if tc.want {
				require.Equal(t, []int64{memberID}, skipped)
			} else {
				require.Empty(t, skipped)
			}

			require.NoError(t, n.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				require.NoError(t, tx.ValidateOVNDelete(ctx, n.id, skipped))
				err := tx.ValidateOVNDelete(ctx, n.id, []int64{memberID})
				if tc.want {
					require.NoError(t, err)
				} else {
					require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
				}

				return nil
			}))
		})
	}
}

func TestOVNDeleteNotifierRechecksAdmission(t *testing.T) {
	for _, change := range []string{"initialization", "marker-removed", "maintenance-restore", "maintenance-active"} {
		t.Run(change, func(t *testing.T) {
			n := newOVNDeleteAdmissionTestNetwork(t)
			ctx := context.Background()
			require.NoError(t, n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				require.NoError(t, tx.EnableOVNLocalInitialization(ctx, n.id))
				state := 0
				if change == "maintenance-restore" || change == "maintenance-active" {
					state = 6
					require.NoError(t, tx.UpdateNodeStatus(tx.GetNodeID(), db.ClusterMemberStateEvacuated))
				}

				_, err := tx.Tx().ExecContext(ctx, "UPDATE networks_nodes SET state=? WHERE network_id=? AND node_id=?", state, n.id, tx.GetNodeID())
				return err
			}))
			_, skipped, err := OVNDeleteNotifier(n.state, n)
			require.NoError(t, err)
			require.Equal(t, []int64{n.state.DB.Cluster.GetNodeID()}, skipped)
			require.NoError(t, n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				switch change {
				case "initialization":
					// Controlled state change models a stale preselection; not backend work.
					_, err := tx.Tx().ExecContext(ctx, "UPDATE networks_nodes SET state=3 WHERE network_id=?", n.id)
					return err
				case "marker-removed":
					_, err := tx.Tx().ExecContext(ctx, "DELETE FROM networks_ovn_local_initialization WHERE network_id=?", n.id)
					return err
				case "maintenance-restore":
					return tx.UpdateNodeStatus(tx.GetNodeID(), db.ClusterMemberStateRestoring)
				default:
					return tx.UpdateNodeStatus(tx.GetNodeID(), db.ClusterMemberStateCreated)
				}
			}))
			err = n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				err := tx.ValidateOVNDelete(ctx, n.id, skipped)
				if err != nil {
					return err
				}

				return tx.DeleteNetwork(ctx, n.project, n.name)
			})
			require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
			require.NoError(t, n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				id, _, _, err := tx.GetNetworkInAnyState(ctx, n.project, n.name)
				require.Equal(t, n.id, id)
				return err
			}))
		})
	}
}

func TestOVNDeleteNotifierRequiresDeleteOrigin(t *testing.T) {
	n := newOVNDeleteAdmissionTestNetwork(t)
	ctx := context.Background()
	require.NoError(t, n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.ReleaseOVNNetworkOperation(ctx, n.project, n.name, "delete-admission-token")
	}))
	_, _, err := OVNDeleteNotifier(n.state, n)
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
	require.NoError(t, n.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.AcquireOVNNetworkOperation(ctx, n.project, n.name, "update-admission-token", "update")
	}))
	_, _, err = OVNDeleteNotifier(n.state, n)
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict))
}
