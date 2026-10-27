//go:build linux && cgo && !agent

package cluster

import "context"

// ProjectGenerated is an interface of generated methods for Project.
type ProjectGenerated interface {
	// GetProjectConfig returns all available Project Config
	// generator: project GetMany
	GetProjectConfig(ctx context.Context, db tx, projectID int, filters ...ConfigFilter) (map[string]string, error)

	// GetProjects returns all available projects.
	// generator: project GetMany
	GetProjects(ctx context.Context, db dbtx, filters ...ProjectFilter) ([]Project, error)

	// GetProject returns the project with the given key.
	// generator: project GetOne
	GetProject(ctx context.Context, db dbtx, name string) (*Project, error)

	// ProjectExists checks if a project with the given key exists.
	// generator: project Exists
	ProjectExists(ctx context.Context, db dbtx, name string) (bool, error)

	// CreateProjectConfig adds new project Config to the database.
	// generator: project Create
	CreateProjectConfig(ctx context.Context, db dbtx, projectID int64, config map[string]string) error

	// CreateProject adds a new project to the database.
	// generator: project Create
	CreateProject(ctx context.Context, db dbtx, object Project) (int64, error)

	// GetProjectID return the ID of the project with the given key.
	// generator: project ID
	GetProjectID(ctx context.Context, db tx, name string) (int64, error)

	// RenameProject renames the project matching the given key parameters.
	// generator: project Rename
	RenameProject(ctx context.Context, db dbtx, name string, to string) error

	// DeleteProject deletes the project matching the given key parameters.
	// generator: project DeleteOne-by-Name
	DeleteProject(ctx context.Context, db dbtx, name string) error
}
