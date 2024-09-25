//go:build linux && cgo && !agent

package db

import (
	"context"

	"github.com/lxc/incus/v6/internal/server/db/cluster"
)

// GetProject returns the project with the given key.
func (db *DB) GetProject(ctx context.Context, projectName string) (*cluster.Project, error) {
	var err error
	var p *cluster.Project
	err = db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		p, err = cluster.GetProject(ctx, tx.Tx(), projectName)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return p, nil
}
