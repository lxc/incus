package cluster

import (
	"context"

	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/state"
)

// ResolveTarget is a convenience for resolving a target member name to address.
// It returns the address of the given member, or the empty string if the given member is the local one.
func ResolveTarget(ctx context.Context, s *state.State, targetMember string) (string, error) {
	// Avoid starting a transaction if the requested target is this local server.
	if targetMember == s.ServerName {
		return "", nil
	}

	var memberAddress string
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		member, err := tx.GetNodeByName(ctx, targetMember)
		if err != nil {
			return err
		}

		memberAddress = member.Address

		return nil
	})

	return memberAddress, err
}
