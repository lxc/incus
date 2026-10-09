package main

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/lxc/incus/v7/internal/server/cluster/request"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/network"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
)

// networkReserveSharedOVN excludes lifecycle writers before shared configuration or usage snapshots change.
func networkReserveSharedOVN(s *state.State, projectName string, operation string, clientType request.ClientType) (func() error, func(map[string]int64) error, error) {
	if clientType != request.ClientTypeNormal {
		// Cluster notifications apply bridge firewall state only; the origin owns all OVN writes.
		return func() error { return nil }, nil, nil
	}

	// Without any OVN network definition no OVN reference can exist; the publication check in the
	// mutating transaction still refuses an OVN network created meanwhile.
	var inapplicable bool
	err := s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		inapplicable, err = tx.OVNReferencesInapplicable(ctx)
		return err
	})
	if err != nil {
		return nil, nil, err
	}

	if inapplicable {
		beforeOVN := func(networks map[string]int64) error {
			if len(networks) > 0 {
				return api.StatusErrorf(http.StatusConflict, "OVN networks appeared during a change without OVN references; retry")
			}

			return nil
		}

		return func() error { return nil }, beforeOVN, nil
	}

	// Connect before reserving, so a replaced Northbound database of a cluster without OVN networks
	// can be bound without this change's own reservation blocking the rebind.
	_, _, err = s.OVN()
	if err != nil {
		return nil, nil, err
	}

	// Transient network and instance operations finish; wait for them as network operations do.
	token := uuid.NewString()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		err = s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.AcquireOVNPeerOperation(ctx, token, operation, false)
		})
		if err == nil || !api.StatusErrorCheck(err, http.StatusConflict) || time.Now().After(deadline) {
			break
		}

		time.Sleep(100 * time.Millisecond)
	}

	if err != nil {
		return nil, nil, err
	}

	release := func() error {
		return s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.ReleaseOVNNetworkOperation(ctx, api.ProjectDefaultName, db.OVNPeerOperationName, token)
		})
	}

	beforeOVN := func(networks map[string]int64) error {
		err := s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			for name, id := range networks {
				err := tx.ValidateOVNPeerReady(ctx, projectName, name, id)
				if err != nil {
					return err
				}
			}

			return nil
		})
		if err != nil {
			return err
		}

		for name, id := range networks {
			n, err := network.LoadByName(s, projectName, name)
			if err != nil {
				return err
			}

			if n.ID() != id || n.Type() != "ovn" || n.LocalStatus() != api.NetworkStatusCreated || !network.IsAvailable(projectName, name) {
				return api.StatusErrorf(http.StatusConflict, "OVN network %q is not ready on this member", name)
			}
		}

		// Bridge-only changes never need OVN clients or leave backend work to recover after a crash.
		return s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.PromoteOVNSharedOperation(ctx, token, operation)
		})
	}

	return release, beforeOVN, nil
}
