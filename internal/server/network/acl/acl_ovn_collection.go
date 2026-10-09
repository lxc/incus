package acl

import (
	"context"
	"slices"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/network/ovn"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/logger"
)

// OVNCollectNetworkACLGroups checks every current owner under a fresh normal update reservation.
func OVNCollectNetworkACLGroups(s *state.State, client *ovn.NB, projectName, networkName string, networkID, parentID int64, token string) error {
	candidates, err := client.NetworkACLPortGroupIDs(context.Background(), networkID)
	if err != nil {
		return err
	}

	var names []string
	err = s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		acls, err := cluster.GetNetworkACLs(ctx, tx.Tx(), cluster.NetworkACLFilter{Project: &projectName})
		if err != nil {
			return err
		}

		for _, acl := range acls {
			if slices.Contains(candidates, int64(acl.ID)) {
				names = append(names, acl.Name)
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	retained, err := OVNRetireNetworkACLGroups(s, client, projectName, networkName, networkID, parentID, token, names, nil)
	if err != nil {
		return err
	}

	return OVNPortGroupDeleteIfUnused(s, logger.AddContext(logger.Ctx{"network": networkName, "project": projectName}), client, projectName, nil, "", retained...)
}
