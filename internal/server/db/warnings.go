//go:build linux && cgo && !agent

package db

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/lxc/incus/internal/server/db/cluster"
	"github.com/lxc/incus/internal/server/db/warningtype"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/util"
)

var warningCreate = cluster.RegisterStmt(`
INSERT INTO warnings (node_id, project_id, entity_type_code, entity_id, uuid, type_code, status, first_seen_date, last_seen_date, updated_date, last_message, count)
  VALUES ((SELECT nodes.id FROM nodes WHERE nodes.name = ?), (SELECT projects.id FROM projects WHERE projects.name = ?), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`)

// UpsertWarningLocalNode creates or updates a warning for the local member. Returns error if no local member name.
func (c *ClusterTx) UpsertWarningLocalNode(ctx context.Context, projectName string, entityTypeCode int, entityID int, typeCode warningtype.Type, message string) error {
	localName, err := c.GetLocalNodeName(ctx)
	if err != nil {
		return fmt.Errorf("Failed getting local member name: %w", err)
	}

	if localName == "" {
		return fmt.Errorf("Local member name not available")
	}

	return c.UpsertWarning(ctx, localName, projectName, entityTypeCode, entityID, typeCode, message)
}

// UpsertWarning creates or updates a warning.
func (c *ClusterTx) UpsertWarning(ctx context.Context, nodeName string, projectName string, entityTypeCode int, entityID int, typeCode warningtype.Type, message string) error {
	// Validate
	_, err := c.GetURIFromEntity(ctx, entityTypeCode, entityID)
	if err != nil {
		return fmt.Errorf("Failed to get URI for entity ID %d with entity type code %d: %w", entityID, entityTypeCode, err)
	}

	_, ok := warningtype.TypeNames[typeCode]
	if !ok {
		return fmt.Errorf("Unknown warning type code %d", typeCode)
	}

	now := time.Now().UTC()

	filter := cluster.WarningFilter{
		TypeCode:       &typeCode,
		Node:           &nodeName,
		Project:        &projectName,
		EntityTypeCode: &entityTypeCode,
		EntityID:       &entityID,
	}

	warnings, err := cluster.GetWarnings(ctx, c.tx, filter)
	if err != nil {
		return fmt.Errorf("Failed to retrieve warnings: %w", err)
	}

	if len(warnings) > 1 {
		// This shouldn't happen
		return fmt.Errorf("More than one warnings (%d) match the criteria: typeCode: %d, nodeName: %q, projectName: %q, entityTypeCode: %d, entityID: %d", len(warnings), typeCode, nodeName, projectName, entityTypeCode, entityID)
	} else if len(warnings) == 1 {
		// If there is a historical warning that was previously automatically resolved and the same
		// warning has now reoccurred then set the status back to warningtype.StatusNew so it shows as
		// a current active warning.
		newStatus := warnings[0].Status
		if newStatus == warningtype.StatusResolved {
			newStatus = warningtype.StatusNew
		}

		err = c.UpdateWarningState(warnings[0].UUID, message, newStatus)
	} else {
		warning := cluster.Warning{
			Node:           nodeName,
			Project:        projectName,
			EntityTypeCode: entityTypeCode,
			EntityID:       entityID,
			UUID:           uuid.New().String(),
			TypeCode:       typeCode,
			Status:         warningtype.StatusNew,
			FirstSeenDate:  now,
			LastSeenDate:   now,
			UpdatedDate:    time.Time{}.UTC(),
			LastMessage:    message,
			Count:          1,
		}

		_, err = c.createWarning(ctx, warning)
	}

	if err != nil {
		return err
	}

	return nil
}

// UpdateWarningStatus updates the status of the warning with the given UUID.
func (c *ClusterTx) UpdateWarningStatus(UUID string, status warningtype.Status) error {
	str := "UPDATE warnings SET status=?, updated_date=? WHERE uuid=?"
	res, err := c.tx.Exec(str, status, time.Now(), UUID)
	if err != nil {
		return fmt.Errorf("Failed to update warning status for warning %q: %w", UUID, err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed to get affected rows to update warning status %q: %w", UUID, err)
	}

	if rowsAffected == 0 {
		return api.StatusErrorf(http.StatusNotFound, "Warning not found")
	}

	return nil
}

// UpdateWarningState updates the warning message and status with the given ID.
func (c *ClusterTx) UpdateWarningState(UUID string, message string, status warningtype.Status) error {
	str := "UPDATE warnings SET last_message=?, last_seen_date=?, updated_date=?, status = ?, count=count+1 WHERE uuid=?"
	now := time.Now()

	res, err := c.tx.Exec(str, message, now, now, status, UUID)
	if err != nil {
		return fmt.Errorf("Failed to update warning %q: %w", UUID, err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed to get affected rows to update warning state %q: %w", UUID, err)
	}

	if rowsAffected == 0 {
		return api.StatusErrorf(http.StatusNotFound, "Warning not found")
	}

	return nil
}

// createWarning adds a new warning to the database.
func (c *ClusterTx) createWarning(ctx context.Context, object cluster.Warning) (int64, error) {
	// Check if a warning with the same key exists.
	exists, err := cluster.WarningExists(ctx, c.tx, object.UUID)
	if err != nil {
		return -1, fmt.Errorf("Failed to check for duplicates: %w", err)
	}

	if exists {
		return -1, fmt.Errorf("This warning already exists")
	}

	args := make([]any, 12)

	// Populate the statement arguments.
	if object.Node != "" {
		// Ensure node exists
		_, err = c.GetNodeByName(ctx, object.Node)
		if err != nil {
			return -1, fmt.Errorf("Failed to get node: %w", err)
		}

		args[0] = object.Node
	}

	if object.Project != "" {
		// Ensure project exists
		projects, err := cluster.GetProjectNames(context.Background(), c.tx)
		if err != nil {
			return -1, fmt.Errorf("Failed to get project names: %w", err)
		}

		if !util.ValueInSlice(object.Project, projects) {
			return -1, fmt.Errorf("Unknown project %q", object.Project)
		}

		args[1] = object.Project
	}

	if object.EntityTypeCode != -1 {
		args[2] = object.EntityTypeCode
	}

	if object.EntityID != -1 {
		args[3] = object.EntityID
	}

	args[4] = object.UUID
	args[5] = object.TypeCode
	args[6] = object.Status
	args[7] = object.FirstSeenDate
	args[8] = object.LastSeenDate
	args[9] = object.UpdatedDate
	args[10] = object.LastMessage
	args[11] = object.Count

	// Prepared statement to use.
	stmt, err := cluster.Stmt(c.tx, warningCreate)
	if err != nil {
		return -1, fmt.Errorf("Failed to get \"warningCreate\" prepared statement: %w", err)
	}

	// Execute the statement.
	result, err := stmt.Exec(args...)
	if err != nil {
		return -1, fmt.Errorf("Failed to create warning: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return -1, fmt.Errorf("Failed to fetch warning ID: %w", err)
	}

	return id, nil
}
