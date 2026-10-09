package network

import (
	"context"

	"github.com/lxc/incus/v7/internal/server/db"
	networkOVN "github.com/lxc/incus/v7/internal/server/network/ovn"
	"github.com/lxc/incus/v7/shared/api"
)

// newPeerICNB records the exact fenced backend before the reserved peer operation can write to it.
func (n *ovn) newPeerICNB(integration *api.NetworkIntegration) (*networkOVN.ICNB, error) {
	var owner string
	err := n.state.DB.Node.Transaction(context.Background(), func(ctx context.Context, tx *db.NodeTx) error {
		var err error
		owner, err = tx.OVNBackendID(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}

	client, err := networkOVN.NewICNB(integration.Config["ovn.northbound_connection"], integration.Config["ovn.ca_cert"], integration.Config["ovn.client_cert"], integration.Config["ovn.client_key"], owner)
	if err != nil {
		return nil, err
	}

	err = n.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.RecordOVNInterconnectOperation(ctx, n.ovnOperationToken, integration.Name, client.BackendID())
	})
	if err != nil {
		return nil, err
	}

	return client, nil
}
