//go:build linux && cgo && !agent

package db

import (
	"context"
	"database/sql"

	"github.com/lxc/incus/v6/internal/server/db/query"
)

// GetAppliedPatches returns the names of all patches currently applied on this node.
func (n *Node) GetAppliedPatches() ([]string, error) {
	var response []string
	err := query.Transaction(context.TODO(), n.db, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		response, err = query.SelectStrings(ctx, tx, "SELECT name FROM patches")
		return err
	})
	if err != nil {
		return []string{}, err
	}

	return response, nil
}

// MarkPatchAsApplied marks the patch with the given name as applied on this node.
func (n *Node) MarkPatchAsApplied(patch string) error {
	stmt := `INSERT INTO patches (name, applied_at) VALUES (?, strftime("%s"))`
	_, err := n.db.Exec(stmt, patch)
	return err
}
