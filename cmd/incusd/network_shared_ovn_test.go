//go:build linux && cgo && !agent

package main

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/cluster/request"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/network/acl"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
)

func TestSharedACLUpdatePreservesBackendFreeChangesAndRejectsPendingOVN(t *testing.T) {
	s, cleanup := state.NewTestState(t)
	defer cleanup()
	require.NoError(t, acl.Create(s, api.ProjectDefaultName, &api.NetworkACLsPost{NetworkACLPost: api.NetworkACLPost{Name: "shared"}}))

	// A detached ACL remains editable without an OVN client or an OVN backend recovery requirement.
	release, beforeOVN, err := networkReserveSharedOVN(s, api.ProjectDefaultName, "acl-config", request.ClientTypeNormal)
	require.NoError(t, err)
	item, err := acl.LoadByName(s, api.ProjectDefaultName, "shared")
	require.NoError(t, err)
	require.NoError(t, item.Update(&api.NetworkACLPut{Description: "original"}, request.ClientTypeNormal, beforeOVN))
	require.NoError(t, s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		pending, err := tx.HasLocalFencedOVNWork(ctx)
		require.False(t, pending)
		return err
	}))
	require.NoError(t, release())

	require.NoError(t, s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		_, err := tx.CreateNetwork(ctx, api.ProjectDefaultName, "pending-local", "", db.NetworkTypeOVN, map[string]string{"security.acls": "shared"})
		if err != nil {
			return err
		}

		return tx.NetworkCreated(api.ProjectDefaultName, "pending-local")
	}))
	release, beforeOVN, err = networkReserveSharedOVN(s, api.ProjectDefaultName, "acl-config", request.ClientTypeNormal)
	require.NoError(t, err)
	defer func() { require.NoError(t, release()) }()
	item, err = acl.LoadByName(s, api.ProjectDefaultName, "shared")
	require.NoError(t, err)
	err = item.Update(&api.NetworkACLPut{Description: "must roll back"}, request.ClientTypeNormal, beforeOVN)
	require.True(t, api.StatusErrorCheck(err, http.StatusConflict), "%v", err)
	item, err = acl.LoadByName(s, api.ProjectDefaultName, "shared")
	require.NoError(t, err)
	require.Equal(t, "original", item.Info().Description)
}
