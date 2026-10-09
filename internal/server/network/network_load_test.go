//go:build linux && cgo && !agent

package network

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/v7/internal/server/db"
	networkOVN "github.com/lxc/incus/v7/internal/server/network/ovn"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
)

func TestLoadAllCreatedSkipsUnavailableOVNBeforeClientSetup(t *testing.T) {
	s, cleanup := state.NewTestState(t)
	defer cleanup()
	ctx := context.Background()
	require.NoError(t, s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		for name, kind := range map[string]db.NetworkType{"unavailable-ovn": db.NetworkTypeOVN, "other-network": db.NetworkTypePhysical} {
			_, err := tx.CreateNetwork(ctx, api.ProjectDefaultName, name, "", kind, nil)
			if err != nil {
				return err
			}

			err = tx.NetworkCreated(api.ProjectDefaultName, name)
			if err != nil {
				return err
			}
		}

		return nil
	}))

	connections := 0
	s.OVN = func() (*networkOVN.NB, *networkOVN.SB, error) {
		connections++
		return nil, nil, errors.New("backend unavailable")
	}

	loaded, err := LoadAllCreated(ctx, s, "ovn")
	require.NoError(t, err)
	require.Zero(t, connections)
	require.Len(t, loaded, 1)
	require.NotNil(t, loaded[ProjectNetwork{ProjectName: api.ProjectDefaultName, NetworkName: "other-network"}])

	loaded, err = LoadAllCreated(ctx, s)
	require.NoError(t, err)
	require.Equal(t, 1, connections)
	require.Contains(t, loaded, ProjectNetwork{ProjectName: api.ProjectDefaultName, NetworkName: "unavailable-ovn"})
	require.Nil(t, loaded[ProjectNetwork{ProjectName: api.ProjectDefaultName, NetworkName: "unavailable-ovn"}])
}
