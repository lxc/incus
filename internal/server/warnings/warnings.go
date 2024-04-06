package warnings

import (
	"context"
	"fmt"
	"time"

	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/db/warningtype"
)

// ResolveWarningsByLocalNodeOlderThan resolves all warnings which are older than the provided time.
func ResolveWarningsByLocalNodeOlderThan(dbCluster *db.Cluster, date time.Time) error {
	var err error
	var localName string

	err = dbCluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		localName, err = tx.GetLocalNodeName(ctx)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed getting local member name: %w", err)
	}

	if localName == "" {
		return fmt.Errorf("Local member name not available")
	}

	err = dbCluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		warnings, err := cluster.GetWarnings(ctx, tx.Tx())
		if err != nil {
			return err
		}

		for _, w := range warnings {
			if w.Node != localName {
				continue
			}

			if w.LastSeenDate.Before(date) {
				err = tx.UpdateWarningStatus(w.UUID, warningtype.StatusResolved)
				if err != nil {
					return err
				}
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to resolve warnings: %w", err)
	}

	return nil
}

// ResolveWarningsByLocalNodeAndType resolves warnings with the local member and type code.
// Returns error if no local member name.
func ResolveWarningsByLocalNodeAndType(dbCluster *db.Cluster, typeCode warningtype.Type) error {
	var err error
	var localName string

	err = dbCluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		localName, err = tx.GetLocalNodeName(ctx)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed getting local member name: %w", err)
	}

	if localName == "" {
		return fmt.Errorf("Local member name not available")
	}

	return ResolveWarningsByNodeAndType(dbCluster, localName, typeCode)
}

// ResolveWarningsByNodeAndType resolves warnings with the given node and type code.
func ResolveWarningsByNodeAndType(dbCluster *db.Cluster, nodeName string, typeCode warningtype.Type) error {
	err := dbCluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		filter := cluster.WarningFilter{
			TypeCode: &typeCode,
			Node:     &nodeName,
		}

		warnings, err := cluster.GetWarnings(ctx, tx.Tx(), filter)
		if err != nil {
			return err
		}

		for _, w := range warnings {
			err = tx.UpdateWarningStatus(w.UUID, warningtype.StatusResolved)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to resolve warnings: %w", err)
	}

	return nil
}

// ResolveWarningsByNodeAndProjectAndType resolves warnings with the given node, project and type code.
func ResolveWarningsByNodeAndProjectAndType(dbCluster *db.Cluster, nodeName string, projectName string, typeCode warningtype.Type) error {
	err := dbCluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		filter := cluster.WarningFilter{
			TypeCode: &typeCode,
			Node:     &nodeName,
			Project:  &projectName,
		}

		warnings, err := cluster.GetWarnings(ctx, tx.Tx(), filter)
		if err != nil {
			return err
		}

		for _, w := range warnings {
			err = tx.UpdateWarningStatus(w.UUID, warningtype.StatusResolved)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to resolve warnings: %w", err)
	}

	return nil
}

// ResolveWarningsByLocalNodeAndProjectAndType resolves warnings with the given project and type code.
func ResolveWarningsByLocalNodeAndProjectAndType(dbCluster *db.Cluster, projectName string, typeCode warningtype.Type) error {
	var err error
	var localName string

	err = dbCluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		localName, err = tx.GetLocalNodeName(ctx)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed getting local member name: %w", err)
	}

	if localName == "" {
		return fmt.Errorf("Local member name not available")
	}

	return ResolveWarningsByNodeAndProjectAndType(dbCluster, localName, projectName, typeCode)
}

// ResolveWarningsByNodeAndProjectAndTypeAndEntity resolves warnings with the given node, project, type code, and entity.
func ResolveWarningsByNodeAndProjectAndTypeAndEntity(dbCluster *db.Cluster, nodeName string, projectName string, typeCode warningtype.Type, entityTypeCode int, entityID int) error {
	err := dbCluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		filter := cluster.WarningFilter{
			TypeCode:       &typeCode,
			Node:           &nodeName,
			Project:        &projectName,
			EntityTypeCode: &entityTypeCode,
			EntityID:       &entityID,
		}

		warnings, err := cluster.GetWarnings(ctx, tx.Tx(), filter)
		if err != nil {
			return err
		}

		for _, w := range warnings {
			err = tx.UpdateWarningStatus(w.UUID, warningtype.StatusResolved)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to resolve warnings: %w", err)
	}

	return nil
}

// ResolveWarningsByLocalNodeAndProjectAndTypeAndEntity resolves warnings with the given project, type code, and entity.
func ResolveWarningsByLocalNodeAndProjectAndTypeAndEntity(dbCluster *db.Cluster, projectName string, typeCode warningtype.Type, entityTypeCode int, entityID int) error {
	var err error
	var localName string

	err = dbCluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		localName, err = tx.GetLocalNodeName(ctx)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed getting local member name: %w", err)
	}

	if localName == "" {
		return fmt.Errorf("Local member name not available")
	}

	return ResolveWarningsByNodeAndProjectAndTypeAndEntity(dbCluster, localName, projectName, typeCode, entityTypeCode, entityID)
}

// DeleteWarningsByNodeAndProjectAndTypeAndEntity deletes warnings with the given node, project, type code, and entity.
func DeleteWarningsByNodeAndProjectAndTypeAndEntity(dbCluster *db.Cluster, nodeName string, projectName string, typeCode warningtype.Type, entityTypeCode int, entityID int) error {
	err := dbCluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		filter := cluster.WarningFilter{
			TypeCode:       &typeCode,
			Node:           &nodeName,
			Project:        &projectName,
			EntityTypeCode: &entityTypeCode,
			EntityID:       &entityID,
		}

		warnings, err := cluster.GetWarnings(ctx, tx.Tx(), filter)
		if err != nil {
			return err
		}

		for _, w := range warnings {
			err = cluster.DeleteWarning(ctx, tx.Tx(), w.UUID)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to delete warnings: %w", err)
	}

	return nil
}

// DeleteWarningsByLocalNodeAndProjectAndTypeAndEntity resolves warnings with the given project, type code, and entity.
func DeleteWarningsByLocalNodeAndProjectAndTypeAndEntity(dbCluster *db.Cluster, projectName string, typeCode warningtype.Type, entityTypeCode int, entityID int) error {
	var err error
	var localName string

	err = dbCluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		localName, err = tx.GetLocalNodeName(ctx)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed getting local member name: %w", err)
	}

	if localName == "" {
		return fmt.Errorf("Local member name not available")
	}

	return DeleteWarningsByNodeAndProjectAndTypeAndEntity(dbCluster, localName, projectName, typeCode, entityTypeCode, entityID)
}
