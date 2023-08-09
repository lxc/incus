//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"
)

// ProjectGenerated is an interface of generated methods for Project.
type ProjectGenerated interface {
	// GetProjectConfig returns all available Project Config
	// generator: project GetMany
	GetProjectConfig(ctx context.Context, tx *sql.Tx, projectID int, filters ...ConfigFilter) (map[string]string, error)

	// GetProjects returns all available projects.
	// generator: project GetMany
	GetProjects(ctx context.Context, tx *sql.Tx, filters ...ProjectFilter) ([]Project, error)

	// GetProject returns the project with the given key.
	// generator: project GetOne
	GetProject(ctx context.Context, tx *sql.Tx, name string) (*Project, error)

	// ProjectExists checks if a project with the given key exists.
	// generator: project Exists
	ProjectExists(ctx context.Context, tx *sql.Tx, name string) (bool, error)

	// CreateProjectConfig adds new project Config to the database.
	// generator: project Create
	CreateProjectConfig(ctx context.Context, tx *sql.Tx, projectID int64, config map[string]string) error

	// CreateProject adds a new project to the database.
	// generator: project Create
	CreateProject(ctx context.Context, tx *sql.Tx, object Project) (int64, error)

	// GetProjectID return the ID of the project with the given key.
	// generator: project ID
	GetProjectID(ctx context.Context, tx *sql.Tx, name string) (int64, error)

	// RenameProject renames the project matching the given key parameters.
	// generator: project Rename
	RenameProject(ctx context.Context, tx *sql.Tx, name string, to string) error

	// DeleteProject deletes the project matching the given key parameters.
	// generator: project DeleteOne-by-Name
	DeleteProject(ctx context.Context, tx *sql.Tx, name string) error
}
