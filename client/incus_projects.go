package incus

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/lxc/incus/v6/shared/api"
)

// Project handling functions

// GetProjectNames returns a list of available project names.
func (r *ProtocolIncus) GetProjectNames() ([]string, error) {
	if !r.HasExtension("projects") {
		return nil, errors.New("The server is missing the required \"projects\" API extension")
	}

	// Fetch the raw URL values.
	urls := []string{}
	baseURL := "/projects"
	_, err := r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetProjects returns a list of available Project structs.
func (r *ProtocolIncus) GetProjects() ([]api.Project, error) {
	if !r.HasExtension("projects") {
		return nil, errors.New("The server is missing the required \"projects\" API extension")
	}

	projects := []api.Project{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", "/projects?recursion=1", nil, "", &projects)
	if err != nil {
		return nil, err
	}

	return projects, nil
}

// GetProjectsWithFilter returns a filtered list of projects as Project structs.
func (r *ProtocolIncus) GetProjectsWithFilter(filters []string) ([]api.Project, error) {
	if !r.HasExtension("projects") {
		return nil, errors.New("The server is missing the required \"projects\" API extension")
	}

	projects := []api.Project{}

	v := url.Values{}
	v.Set("recursion", "1")
	v.Set("filter", parseFilters(filters))

	_, err := r.queryStruct("GET", fmt.Sprintf("/projects?%s", v.Encode()), nil, "", &projects)
	if err != nil {
		return nil, err
	}

	return projects, nil
}

// GetProject returns a Project entry for the provided name.
func (r *ProtocolIncus) GetProject(name string) (*api.Project, string, error) {
	if !r.HasExtension("projects") {
		return nil, "", errors.New("The server is missing the required \"projects\" API extension")
	}

	project := api.Project{}

	// Fetch the raw value
	etag, err := r.queryStruct("GET", fmt.Sprintf("/projects/%s", url.PathEscape(name)), nil, "", &project)
	if err != nil {
		return nil, "", err
	}

	return &project, etag, nil
}

// GetProjectState returns a Project state for the provided name.
func (r *ProtocolIncus) GetProjectState(name string) (*api.ProjectState, error) {
	if !r.HasExtension("project_usage") {
		return nil, errors.New("The server is missing the required \"project_usage\" API extension")
	}

	projectState := api.ProjectState{}

	// Fetch the raw value
	_, err := r.queryStruct("GET", fmt.Sprintf("/projects/%s/state", url.PathEscape(name)), nil, "", &projectState)
	if err != nil {
		return nil, err
	}

	return &projectState, nil
}

// GetProjectAccess returns an Access entry for the specified project.
func (r *ProtocolIncus) GetProjectAccess(name string) (api.Access, error) {
	access := api.Access{}

	if !r.HasExtension("project_access") {
		return nil, errors.New("The server is missing the required \"project_access\" API extension")
	}

	// Fetch the raw value
	_, err := r.queryStruct("GET", fmt.Sprintf("/projects/%s/access", url.PathEscape(name)), nil, "", &access)
	if err != nil {
		return nil, err
	}

	return access, nil
}

// CreateProject defines a new project.
func (r *ProtocolIncus) CreateProject(project api.ProjectsPost) error {
	if !r.HasExtension("projects") {
		return errors.New("The server is missing the required \"projects\" API extension")
	}

	// Send the request
	_, _, err := r.query("POST", "/projects", project, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateProject updates the project to match the provided Project struct.
func (r *ProtocolIncus) UpdateProject(name string, project api.ProjectPut, ETag string) error {
	if !r.HasExtension("projects") {
		return errors.New("The server is missing the required \"projects\" API extension")
	}

	// Send the request
	_, _, err := r.query("PUT", fmt.Sprintf("/projects/%s", url.PathEscape(name)), project, ETag)
	if err != nil {
		return err
	}

	return nil
}

// RenameProject renames an existing project entry.
func (r *ProtocolIncus) RenameProject(name string, project api.ProjectPost) (Operation, error) {
	if !r.HasExtension("projects") {
		return nil, errors.New("The server is missing the required \"projects\" API extension")
	}

	// Send the request
	op, _, err := r.queryOperation("POST", fmt.Sprintf("/projects/%s", url.PathEscape(name)), project, "")
	if err != nil {
		return nil, err
	}

	return op, nil
}

// DeleteProject deletes a project.
func (r *ProtocolIncus) DeleteProject(name string) error {
	if !r.HasExtension("projects") {
		return errors.New("The server is missing the required \"projects\" API extension")
	}

	// Send the request
	_, _, err := r.query("DELETE", fmt.Sprintf("/projects/%s", url.PathEscape(name)), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// DeleteProjectForce deletes a project and everything inside of it.
func (r *ProtocolIncus) DeleteProjectForce(name string) error {
	if !r.HasExtension("projects_force_delete") {
		return errors.New("The server is missing the required \"projects_force_delete\" API extension")
	}

	// Send the request
	_, _, err := r.query("DELETE", fmt.Sprintf("/projects/%s?force=1", url.PathEscape(name)), nil, "")
	if err != nil {
		return err
	}

	return nil
}
