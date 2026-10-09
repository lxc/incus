package network

import (
	"context"
	"maps"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/cluster/request"
	"github.com/lxc/incus/v7/internal/server/db"
	dbCluster "github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
)

// A supported admission refusal must precede backend publication or generic setup rollback.
func TestOVNParentRefusalBeforeBackendEffects(t *testing.T) {
	for _, consumer := range []string{"forward", "load-balancer"} {
		t.Run(consumer, func(t *testing.T) {
			cluster, cleanup := db.NewTestCluster(t)
			t.Cleanup(cleanup)
			config := map[string]string{"parent": "original-parent", "ipv4.address": "192.0.2.1/24", "ipv6.address": "none"}
			var id int64
			err := cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				var err error
				id, err = tx.CreateNetwork(ctx, api.ProjectDefaultName, "parent-refusal-child", "", db.NetworkTypeOVN, config)
				if err != nil {
					return err
				}

				if consumer == "forward" {
					_, err = dbCluster.CreateNetworkForward(ctx, tx.Tx(), dbCluster.NetworkForward{NetworkID: id, ListenAddress: "198.51.100.2"})
				} else {
					_, err = dbCluster.CreateNetworkLoadBalancer(ctx, tx.Tx(), dbCluster.NetworkLoadBalancer{NetworkID: id, ListenAddress: "198.51.100.2"})
				}

				return err
			})
			require.NoError(t, err)
			n := &ovn{common: common{
				logger: logger.AddContext(logger.Ctx{"test": t.Name()}),
				state:  &state.State{DB: &db.DB{Cluster: cluster}, ShutdownCtx: context.Background()},
				id:     id, project: api.ProjectDefaultName, name: "parent-refusal-child", netType: "ovn", status: api.NetworkStatusCreated,
				config: maps.Clone(config),
			}}
			proposed := maps.Clone(config)
			proposed["parent"] = "new-parent"
			// No NB/SB client exists. The pure admission check must reject before any constructor,
			// producer invalidation, setup rollback, callback changes or DB configuration write.
			require.NotPanics(t, func() {
				err = n.Update(api.NetworkPut{Config: proposed}, "", request.ClientTypeNormal)
			})
			if consumer == "forward" {
				require.ErrorContains(t, err, "Network has 1 address forward(s)")
			} else {
				require.ErrorContains(t, err, "Network has 1 load balancer(s)")
			}

			require.Nil(t, n.ovnnb)
			require.Nil(t, n.ovnsb)
			require.Equal(t, config, n.config)
			var actualID int64
			var actualConfig map[string]string
			require.NoError(t, cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
				id, info, _, err := tx.GetNetworkInAnyState(ctx, n.project, n.name)
				if err != nil {
					return err
				}

				actualID, actualConfig = id, info.Config
				return nil
			}))
			require.Equal(t, id, actualID)
			require.Equal(t, config, actualConfig)
		})
	}
}
