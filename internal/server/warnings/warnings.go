package warnings

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/db/warningtype"
)

// ResolveWarningsByNodeOlderThan resolves all warnings on the given node which are older than the provided time.
func ResolveWarningsByNodeOlderThan(dbCluster *db.Cluster, nodeName string, date time.Time) error {
	if nodeName == "" {
		return errors.New("Local member name not available")
	}

	err := dbCluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		warnings, err := cluster.GetWarnings(ctx, tx.Tx())
		if err != nil {
			return err
		}

		for _, w := range warnings {
			if w.Node != nodeName {
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
