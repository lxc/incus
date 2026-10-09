package network

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/shared/api"
)

// acquirePeerOperation excludes lifecycle changes on both routers, their children and newly defined targets.
func (n *ovn) acquirePeerOperation(operation string) (func() error, error) {
	token := uuid.NewString()
	err := n.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.AcquireOVNPeerOperation(ctx, token, operation, true)
	})
	if err != nil {
		return nil, err
	}

	release := func() error {
		return n.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.ReleaseOVNNetworkOperation(ctx, api.ProjectDefaultName, db.OVNPeerOperationName, token)
		})
	}

	unlock, err := LockOVNLifecycle(n.project, n.name)
	if err != nil {
		return nil, errors.Join(err, release())
	}

	err = n.peerReady()
	if err != nil {
		unlock()
		return nil, errors.Join(err, release())
	}

	n.ovnOperationToken = token
	n.ovnOperationUncertain = false
	return func() error {
		defer unlock()
		defer func() { n.ovnOperationToken = "" }()
		if n.ovnOperationUncertain {
			return errors.New("Retaining OVN peering reservation because an interconnect transaction has an uncertain outcome")
		}

		return release()
	}, nil
}

// peerReady checks current local readiness without initializing beneath the cluster-wide peer reservation.
func (n *ovn) peerReady() error {
	err := n.reload()
	if err != nil {
		return err
	}

	local := n.localState()
	if !local.started || local.deleting || !IsAvailable(n.project, n.name) {
		return api.StatusErrorf(http.StatusConflict, "OVN network %q is not ready on this member", n.name)
	}

	return n.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.ValidateOVNPeerReady(ctx, n.project, n.name, n.id)
	})
}

// peerTargetReady preserves pending definitions for missing targets and checks existing target readiness.
func (n *ovn) peerTargetReady(projectName string, networkName string) error {
	target, err := LoadByName(n.state, projectName, networkName)
	if api.StatusErrorCheck(err, http.StatusNotFound) {
		return nil
	}

	if err != nil {
		return err
	}

	ovnTarget, ok := target.(*ovn)
	if !ok {
		return api.StatusErrorf(http.StatusBadRequest, "Target network is not an OVN network")
	}

	return ovnTarget.peerReady()
}
