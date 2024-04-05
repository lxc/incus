package node

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/shared/logger"
)

// Open the node-local database object.
func Open(dir string) (*sql.DB, error) {
	path := filepath.Join(dir, "local.db")
	db, err := sqliteOpen(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open node database: %w", err)
	}

	return db, nil
}

// EnsureSchema applies all relevant schema updates to the node-local
// database.
//
// Return the initial schema version found before starting the update, along
// with any error occurred.
func EnsureSchema(db *sql.DB, dir string) (int, error) {
	backupDone := false

	schema := Schema()
	schema.File(filepath.Join(dir, "patch.local.sql")) // Optional custom queries
	schema.Hook(func(ctx context.Context, version int, tx *sql.Tx) error {
		if !backupDone {
			logger.Infof("Updating the database schema. Backup made as \"local.db.bak\"")
			path := filepath.Join(dir, "local.db")
			err := internalUtil.FileCopy(path, path+".bak")
			if err != nil {
				return err
			}

			backupDone = true
		}

		if version == -1 {
			logger.Debugf("Running pre-update queries from file for local DB schema")
		} else {
			logger.Debugf("Updating DB schema from %d to %d", version, version+1)
		}

		return nil
	})
	return schema.Ensure(db)
}
