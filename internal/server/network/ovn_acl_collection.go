package network

import (
	"context"
	"errors"
	"fmt"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/network/acl"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
)

// OVNCollectNetworkACLGroups collects after instance commit without carrying an old NIC reservation.
func OVNCollectNetworkACLGroups(s *state.State, projectName, networkName string, projectID, networkID int64) error {
	err := s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		id, err := cluster.GetProjectID(ctx, tx.Tx(), projectName)
		if err != nil {
			return err
		}

		if projectID <= 0 || id != projectID {
			return errors.New("Committed ACL collection project identity changed")
		}

		return nil
	})
	if err != nil {
		return err
	}

	loaded, err := LoadByName(s, projectName, networkName)
	if err != nil {
		return err
	}

	n, ok := loaded.(*ovn)
	if !ok || networkID <= 0 || n.ID() != networkID || n.Status() != api.NetworkStatusCreated {
		return errors.New("Committed ACL collection network identity changed")
	}

	return n.collectUnusedACLGroups(projectID)
}

func (n *ovn) collectUnusedACLGroups(projectID int64) (err error) {
	release, err := n.waitOperation("update")
	if err != nil {
		return err
	}

	defer func() { err = errors.Join(err, release()) }()
	err = n.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		id, err := cluster.GetProjectID(ctx, tx.Tx(), n.project)
		if err != nil {
			return err
		}

		if id != projectID || n.Status() != api.NetworkStatusCreated {
			return fmt.Errorf("Committed ACL collection identity changed under reservation")
		}

		return nil
	})
	if err != nil {
		return err
	}

	return acl.OVNCollectNetworkACLGroups(n.state, n.ovnnb, n.project, n.name, n.ID(), n.parentID, n.ovnOperationToken)
}
