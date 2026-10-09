package cluster_test

import (
	"context"
	"errors"
	"testing"
	"time"

	cowsqlcluster "github.com/cowsql/go-cowsql/cluster"
	cowsqltls "github.com/cowsql/go-cowsql/cluster/tls"
	"github.com/stretchr/testify/require"

	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/internal/server/cluster"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/node"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/tls/tlstest"
)

type selectedNotifierGateway struct{}

func (selectedNotifierGateway) LeaderAddress() (string, error)         { return "", nil }
func (selectedNotifierGateway) IsLeader(context.Context) (bool, error) { return true, nil }
func (selectedNotifierGateway) NewNotifier(context.Context, cowsqltls.CertInfo, cowsqltls.CertInfo, cowsqlcluster.NotifierPolicy) (cowsqlcluster.Notifier, error) {
	return nil, errors.New("unfiltered notifier must not be used for selected members")
}

func TestNewNotifierForMembersFiltersBeforeAvailability(t *testing.T) {
	local, cleanup := db.NewTestNode(t)
	t.Cleanup(cleanup)
	database, cleanup := db.NewTestCluster(t)
	t.Cleanup(cleanup)
	s := &state.State{DB: &db.DB{Node: local, Cluster: database}, Cluster: selectedNotifierGateway{}}
	ctx := context.Background()
	require.NoError(t, local.Transaction(ctx, func(ctx context.Context, tx *db.NodeTx) error {
		config, err := node.ConfigLoad(ctx, tx)
		if err != nil {
			return err
		}

		_, err = config.Patch(map[string]string{"cluster.https_address": "127.0.0.1:8443"})
		s.LocalConfig = config
		return err
	}))
	var offlineID int64
	require.NoError(t, database.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		err := tx.BootstrapNode("local", "127.0.0.1:8443")
		if err != nil {
			return err
		}

		offlineID, err = tx.CreateNode("offline", "127.0.0.1:8444")
		if err != nil {
			return err
		}

		_, err = tx.Tx().ExecContext(ctx, "UPDATE nodes SET heartbeat=? WHERE id=?", time.Now().Add(-time.Hour), offlineID)
		return err
	}))
	cert := tlstest.TestingKeyPair(t)
	for _, test := range []struct {
		name     string
		selected []int64
		policy   cluster.NotifierPolicy
		want     int
	}{
		{"empty all excludes offline", nil, cluster.NotifyAll, 0},
		{"local only excludes offline", []int64{database.GetNodeID()}, cluster.NotifyAll, 0},
		{"alive excludes selected offline", []int64{offlineID}, cluster.NotifyAlive, 0},
		{"try all includes selected offline", []int64{offlineID}, cluster.NotifyTryAll, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			notifier, err := cluster.NewNotifierForMembers(s, cert, cert, test.policy, test.selected)
			require.NoError(t, err)
			calls := 0
			err = notifier(func(client incus.InstanceServer) error {
				info, err := client.GetConnectionInfo()
				require.NoError(t, err)
				require.Equal(t, "https://127.0.0.1:8444", info.URL)
				calls++
				return nil
			})
			require.NoError(t, err)
			require.Equal(t, test.want, calls)
		})
	}
}
