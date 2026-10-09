//go:build linux && cgo && !agent

package main

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
)

// instanceMaintenanceAdmission checks the local member before ordinary workload effects.
// Callers retain their own exemptions; next can continue an admitted action.
func instanceMaintenanceAdmission(s *state.State, check bool, next func() error) error {
	if check {
		var maintenance bool
		err := s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
			name, err := tx.GetLocalNodeName(ctx)
			if err != nil {
				return err
			}

			member, err := tx.GetNodeByName(ctx, name)
			if err != nil {
				return err
			}

			maintenance = slices.Contains([]int{db.ClusterMemberStateEvacuating, db.ClusterMemberStateEvacuated, db.ClusterMemberStateRestoring}, member.State)
			return nil
		})
		if err != nil {
			return fmt.Errorf("Failed to read local cluster member state before workload admission: %w", err)
		}

		if maintenance {
			return api.StatusErrorf(http.StatusForbidden, "Cluster member is evacuated")
		}
	}

	if next != nil {
		return next()
	}

	return nil
}
