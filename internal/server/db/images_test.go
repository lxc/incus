//go:build linux && cgo && !agent

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/db/cluster"
)

func TestLocateImage(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	_ = cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		err := tx.CreateImage(ctx,
			"default", "abc", "x.gz", 16, false, false, "amd64", time.Now(), time.Now(), map[string]string{}, "container", nil)
		require.NoError(t, err)

		address, err := tx.LocateImage(ctx, "abc")
		require.NoError(t, err)
		assert.Equal(t, "", address)

		// Pretend that the function is being run on another node.
		tx.NodeID(2)

		address, err = tx.LocateImage(ctx, "abc")
		require.NoError(t, err)
		assert.Equal(t, "0.0.0.0", address)

		// Pretend that the target node is down
		err = tx.SetNodeHeartbeat("0.0.0.0", time.Now().Add(-time.Minute))
		require.NoError(t, err)

		address, err = tx.LocateImage(ctx, "abc")
		require.Equal(t, "", address)
		require.EqualError(t, err, "Image not available on any online member")

		return nil
	})
}

func TestImageExists(t *testing.T) {
	cluster, cleanup := db.NewTestCluster(t)
	defer cleanup()

	_ = cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		exists, err := tx.ImageExists(ctx, "default", "abc")
		require.NoError(t, err)

		assert.False(t, exists)

		err = tx.CreateImage(ctx,
			"default", "abc", "x.gz", 16, false, false, "amd64", time.Now(), time.Now(), map[string]string{}, "container", nil)
		require.NoError(t, err)

		exists, err = tx.ImageExists(ctx, "default", "abc")
		require.NoError(t, err)

		assert.True(t, exists)

		return nil
	})
}

func TestGetImage(t *testing.T) {
	dbCluster, cleanup := db.NewTestCluster(t)
	defer cleanup()
	project := "default"

	_ = dbCluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// public image with 'default' project
		err := tx.CreateImage(ctx, project, "abcd1", "x.gz", 16, true, false, "amd64", time.Now(), time.Now(), map[string]string{}, "container", nil)
		require.NoError(t, err)

		// 'public' is ignored if 'false'
		id, img, err := tx.GetImage(ctx, "a", cluster.ImageFilter{Project: &project})
		require.NoError(t, err)
		assert.Equal(t, img.Public, true)
		assert.NotEqual(t, id, -1)

		// non-public image with 'default' project
		err = tx.CreateImage(ctx, project, "abcd2", "x.gz", 16, false, false, "amd64", time.Now(), time.Now(), map[string]string{}, "container", nil)
		require.NoError(t, err)

		// empty project fails
		_, _, err = tx.GetImage(ctx, "a", cluster.ImageFilter{})
		require.Error(t, err)

		// 'public' is ignored if 'false', returning both entries
		_, _, err = tx.GetImage(ctx, "a", cluster.ImageFilter{Project: &project})
		require.Error(t, err)

		public := true
		id, img, err = tx.GetImage(ctx, "a", cluster.ImageFilter{Project: &project, Public: &public})
		require.NoError(t, err)
		assert.Equal(t, img.Public, true)
		assert.NotEqual(t, id, -1)

		return nil
	})
}
