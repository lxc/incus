package main

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	cowsqlDriver "github.com/cowsql/go-cowsql/driver"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/cluster/request"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/network/acl"
	addressset "github.com/lxc/incus/v7/internal/server/network/address-set"
	networkOVN "github.com/lxc/incus/v7/internal/server/network/ovn"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
)

func sharedReferenceTestCluster(t *testing.T) *db.Cluster {
	t.Helper()
	dir, store, serverCleanup := db.NewTestCowsqlServer(t)
	members, err := store.Get(context.Background())
	require.NoError(t, err)
	c, err := db.OpenCluster(context.Background(), "test.db", store, "1", dir, 5*time.Second, cowsqlDriver.WithDialFunc(func(ctx context.Context, address string) (net.Conn, error) { return net.Dial("unix", address) }))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, c.Close())
		serverCleanup()
		_, err := os.Stat(dir)
		require.True(t, os.IsNotExist(err))
		for _, member := range members {
			conn, err := net.Dial("unix", member.Address)
			if conn != nil {
				_ = conn.Close()
			}

			require.Error(t, err)
		}

		t.Logf("owned in-process cowsql closed; Unix endpoints refused new connections and root removed: %s", dir)
	})
	t.Logf("owned in-process cowsql PID=%d root=%s database=test.db Unix-members=%v", os.Getpid(), dir, members)
	return c
}

func TestFreshSharedPublicCallersDefaultWithoutOVNService(t *testing.T) {
	ctx := context.Background()
	c := sharedReferenceTestCluster(t)
	calls := 0
	s := &state.State{ShutdownCtx: ctx, DB: &db.DB{Cluster: c}, OVN: func() (*networkOVN.NB, *networkOVN.SB, error) {
		calls++
		return nil, nil, errors.New("owned fixture has no OVN service")
	}}
	require.NoError(t, acl.Create(s, "default", &api.NetworkACLsPost{NetworkACLPost: api.NetworkACLPost{Name: "ordinary"}}))
	require.NoError(t, addressset.Create(s, "default", &api.NetworkAddressSetsPost{NetworkAddressSetPost: api.NetworkAddressSetPost{Name: "ordinary"}}))
	for _, kind := range []string{"acl-config", "address-set-config"} {
		release, beforeOVN, err := networkReserveSharedOVN(s, "default", kind, request.ClientTypeNormal)
		require.NoError(t, err)
		if kind == "acl-config" {
			item, err := acl.LoadByName(s, "default", "ordinary")
			require.NoError(t, err)
			require.NoError(t, item.Update(&api.NetworkACLPut{Description: "ordinary"}, request.ClientTypeNormal, beforeOVN))
			require.NoError(t, item.Rename("renamed"))
			require.NoError(t, item.Delete())
		} else {
			item, err := addressset.LoadByName(s, "default", "ordinary")
			require.NoError(t, err)
			require.NoError(t, item.Update(&api.NetworkAddressSetPut{Description: "ordinary"}, request.ClientTypeNormal, beforeOVN))
			require.NoError(t, item.Rename("renamed"))
			require.NoError(t, item.Delete())
		}

		require.NoError(t, c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			never, err := tx.OVNReferencesNeverActivated(ctx)
			require.True(t, never)
			fenced, e := tx.HasLocalFencedOVNWork(ctx)
			require.NoError(t, e)
			require.False(t, fenced)
			return err
		}))
		require.NoError(t, release())
	}

	require.Zero(t, calls)
	t.Log("Actual networkReserveSharedOVN + public object Update/Rename/Delete + release preserved fresh never with no OVS/NB service or backend-fenced work")
}
