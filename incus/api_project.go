package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/mux"

	"github.com/cyphar/incus/incus/db"
	"github.com/cyphar/incus/incus/db/cluster"
	"github.com/cyphar/incus/incus/db/operationtype"
	"github.com/cyphar/incus/incus/lifecycle"
	"github.com/cyphar/incus/incus/network"
	"github.com/cyphar/incus/incus/operations"
	projecthelpers "github.com/cyphar/incus/incus/project"
	"github.com/cyphar/incus/incus/rbac"
	"github.com/cyphar/incus/incus/request"
	"github.com/cyphar/incus/incus/response"
	"github.com/cyphar/incus/incus/state"
	"github.com/cyphar/incus/incus/util"
	"github.com/cyphar/incus/shared"
	"github.com/cyphar/incus/shared/api"
	"github.com/cyphar/incus/shared/logger"
	"github.com/cyphar/incus/shared/validate"
	"github.com/cyphar/incus/shared/version"
)

var projectsCmd = APIEndpoint{
	Path: "projects",

	Get:  APIEndpointAction{Handler: projectsGet, AccessHandler: allowAuthenticated},
	Post: APIEndpointAction{Handler: projectsPost},
}

var projectCmd = APIEndpoint{
	Path: "projects/{name}",

	Delete: APIEndpointAction{Handler: projectDelete},
	Get:    APIEndpointAction{Handler: projectGet, AccessHandler: allowAuthenticated},
	Patch:  APIEndpointAction{Handler: projectPatch, AccessHandler: allowAuthenticated},
	Post:   APIEndpointAction{Handler: projectPost},
	Put:    APIEndpointAction{Handler: projectPut, AccessHandler: allowAuthenticated},
}

var projectStateCmd = APIEndpoint{
	Path: "projects/{name}/state",

	Get: APIEndpointAction{Handler: projectStateGet, AccessHandler: allowAuthenticated},
}

// swagger:operation GET /1.0/projects projects projects_get
//
//  Get the projects
//
//  Returns a list of projects (URLs).
//
//  ---
//  produces:
//    - application/json
//  responses:
//    "200":
//      description: API endpoints
//      schema:
//        type: object
//        description: Sync response
//        properties:
//          type:
//            type: string
//            description: Response type
//            example: sync
//          status:
//            type: string
//            description: Status description
//            example: Success
//          status_code:
//            type: integer
//            description: Status code
//            example: 200
//          metadata:
//            type: array
//            description: List of endpoints
//            items:
//              type: string
//            example: |-
//              [
//                "/1.0/projects/default",
//                "/1.0/projects/foo"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/projects?recursion=1 projects projects_get_recursion1
//
//	Get the projects
//
//	Returns a list of projects (structs).
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: API endpoints
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          type: array
//	          description: List of projects
//	          items:
//	            $ref: "#/definitions/Project"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func projectsGet(d *Daemon, r *http.Request) response.Response {
	recursion := util.IsRecursionRequest(r)

	var result any
	err := d.State().DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		projects, err := cluster.GetProjects(ctx, tx.Tx())
		if err != nil {
			return err
		}

		filtered := []api.Project{}
		for _, project := range projects {
			if !rbac.UserHasPermission(r, project.Name, "view") {
				continue
			}

			apiProject, err := project.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			apiProject.UsedBy, err = projectUsedBy(ctx, tx, &project)
			if err != nil {
				return err
			}

			filtered = append(filtered, *apiProject)
		}

		if recursion {
			result = filtered
		} else {
			urls := make([]string, len(filtered))
			for i, p := range filtered {
				urls[i] = p.URL(version.APIVersion).String()
			}

			result = urls
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, result)
}

// projectUsedBy returns a list of URLs for all instances, images, profiles,
// storage volumes, networks, and acls that use this project.
func projectUsedBy(ctx context.Context, tx *db.ClusterTx, project *cluster.Project) ([]string, error) {
	usedBy := []string{}
	instances, err := cluster.GetInstances(ctx, tx.Tx(), cluster.InstanceFilter{Project: &project.Name})
	if err != nil {
		return nil, err
	}

	profiles, err := cluster.GetProfiles(ctx, tx.Tx(), cluster.ProfileFilter{Project: &project.Name})
	if err != nil {
		return nil, err
	}

	images, err := cluster.GetImages(ctx, tx.Tx(), cluster.ImageFilter{Project: &project.Name})
	if err != nil {
		return nil, err
	}

	for _, instance := range instances {
		apiInstance := api.Instance{Name: instance.Name}
		usedBy = append(usedBy, apiInstance.URL(version.APIVersion, project.Name).String())
	}

	for _, profile := range profiles {
		apiProfile := api.Profile{Name: profile.Name}
		usedBy = append(usedBy, apiProfile.URL(version.APIVersion, project.Name).String())
	}

	for _, image := range images {
		apiImage := api.Image{Fingerprint: image.Fingerprint}
		usedBy = append(usedBy, apiImage.URL(version.APIVersion, project.Name).String())
	}

	volumes, err := tx.GetStorageVolumeURIs(ctx, project.Name)
	if err != nil {
		return nil, err
	}

	networks, err := tx.GetNetworkURIs(ctx, project.ID, project.Name)
	if err != nil {
		return nil, err
	}

	acls, err := tx.GetNetworkACLURIs(ctx, project.ID, project.Name)
	if err != nil {
		return nil, err
	}

	usedBy = append(usedBy, volumes...)
	usedBy = append(usedBy, networks...)
	usedBy = append(usedBy, acls...)

	return usedBy, nil
}

// swagger:operation POST /1.0/projects projects projects_post
//
//	Add a project
//
//	Creates a new project.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: project
//	    description: Project
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ProjectsPost"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func projectsPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Parse the request.
	project := api.ProjectsPost{}

	// Set default features.
	if project.Config == nil {
		project.Config = map[string]string{}
	}

	for featureName, featureInfo := range cluster.ProjectFeatures {
		_, ok := project.Config[featureName]
		if !ok && featureInfo.DefaultEnabled {
			project.Config[featureName] = "true"
		}
	}

	err := json.NewDecoder(r.Body).Decode(&project)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	err = projectValidateName(project.Name)
	if err != nil {
		return response.BadRequest(err)
	}

	// Validate the configuration.
	err = projectValidateConfig(s, project.Config)
	if err != nil {
		return response.BadRequest(err)
	}

	var id int64
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		id, err = cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Description: project.Description, Name: project.Name})
		if err != nil {
			return fmt.Errorf("Failed adding database record: %w", err)
		}

		err = cluster.CreateProjectConfig(ctx, tx.Tx(), id, project.Config)
		if err != nil {
			return fmt.Errorf("Unable to create project config for project %q: %w", project.Name, err)
		}

		if shared.IsTrue(project.Config["features.profiles"]) {
			err = projectCreateDefaultProfile(tx, project.Name)
			if err != nil {
				return err
			}

			if project.Config["features.images"] == "false" {
				err = cluster.InitProjectWithoutImages(ctx, tx.Tx(), project.Name)
				if err != nil {
					return err
				}
			}
		}

		return nil
	})
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed creating project %q: %w", project.Name, err))
	}

	if d.rbac != nil {
		err = d.rbac.AddProject(id, project.Name)
		if err != nil {
			return response.SmartError(err)
		}
	}

	requestor := request.CreateRequestor(r)
	lc := lifecycle.ProjectCreated.Event(project.Name, requestor, nil)
	s.Events.SendLifecycle(project.Name, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// Create the default profile of a project.
func projectCreateDefaultProfile(tx *db.ClusterTx, project string) error {
	// Create a default profile
	profile := cluster.Profile{}
	profile.Project = project
	profile.Name = projecthelpers.Default
	profile.Description = fmt.Sprintf("Default LXD profile for project %s", project)

	_, err := cluster.CreateProfile(context.TODO(), tx.Tx(), profile)
	if err != nil {
		return fmt.Errorf("Add default profile to database: %w", err)
	}

	return nil
}

// swagger:operation GET /1.0/projects/{name} projects project_get
//
//	Get the project
//
//	Gets a specific project.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Project
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          $ref: "#/definitions/Project"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func projectGet(d *Daemon, r *http.Request) response.Response {
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Check user permissions
	if !rbac.UserHasPermission(r, name, "view") {
		return response.Forbidden(nil)
	}

	// Get the database entry
	var project *api.Project
	err = d.State().DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbProject, err := cluster.GetProject(ctx, tx.Tx(), name)
		if err != nil {
			return err
		}

		project, err = dbProject.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		project.UsedBy, err = projectUsedBy(ctx, tx, dbProject)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	etag := []any{
		project.Description,
		project.Config,
	}

	return response.SyncResponseETag(true, project, etag)
}

// swagger:operation PUT /1.0/projects/{name} projects project_put
//
//	Update the project
//
//	Updates the entire project configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: project
//	    description: Project configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ProjectPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func projectPut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Check user permissions
	if !rbac.UserHasPermission(r, name, "manage-projects") {
		return response.Forbidden(nil)
	}

	// Get the current data
	var project *api.Project
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbProject, err := cluster.GetProject(ctx, tx.Tx(), name)
		if err != nil {
			return err
		}

		project, err = dbProject.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		project.UsedBy, err = projectUsedBy(ctx, tx, dbProject)
		if err != nil {
			return err
		}

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Validate ETag
	etag := []any{
		project.Description,
		project.Config,
	}

	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	// Parse the request
	req := api.ProjectPut{}

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	requestor := request.CreateRequestor(r)
	s.Events.SendLifecycle(project.Name, lifecycle.ProjectUpdated.Event(project.Name, requestor, nil))

	return projectChange(s, project, req)
}

// swagger:operation PATCH /1.0/projects/{name} projects project_patch
//
//	Partially update the project
//
//	Updates a subset of the project configuration.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: project
//	    description: Project configuration
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ProjectPut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func projectPatch(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Check user permissions
	if !rbac.UserHasPermission(r, name, "manage-projects") {
		return response.Forbidden(nil)
	}

	// Get the current data
	var project *api.Project
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbProject, err := cluster.GetProject(ctx, tx.Tx(), name)
		if err != nil {
			return err
		}

		project, err = dbProject.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		project.UsedBy, err = projectUsedBy(ctx, tx, dbProject)
		if err != nil {
			return err
		}

		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Validate ETag
	etag := []any{
		project.Description,
		project.Config,
	}

	err = util.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return response.InternalError(err)
	}

	rdr1 := io.NopCloser(bytes.NewBuffer(body))
	rdr2 := io.NopCloser(bytes.NewBuffer(body))

	reqRaw := shared.Jmap{}
	err = json.NewDecoder(rdr1).Decode(&reqRaw)
	if err != nil {
		return response.BadRequest(err)
	}

	req := api.ProjectPut{}
	err = json.NewDecoder(rdr2).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check what was actually set in the query
	_, err = reqRaw.GetString("description")
	if err != nil {
		req.Description = project.Description
	}

	config, err := reqRaw.GetMap("config")
	if err != nil {
		req.Config = project.Config
	} else {
		for k, v := range project.Config {
			_, ok := config[k]
			if !ok {
				config[k] = v
			}
		}
	}

	requestor := request.CreateRequestor(r)
	s.Events.SendLifecycle(project.Name, lifecycle.ProjectUpdated.Event(project.Name, requestor, nil))

	return projectChange(s, project, req)
}

// Common logic between PUT and PATCH.
func projectChange(s *state.State, project *api.Project, req api.ProjectPut) response.Response {
	// Make a list of config keys that have changed.
	configChanged := []string{}
	for key := range project.Config {
		if req.Config[key] != project.Config[key] {
			configChanged = append(configChanged, key)
		}
	}

	for key := range req.Config {
		_, ok := project.Config[key]
		if !ok {
			configChanged = append(configChanged, key)
		}
	}

	// Record which features have been changed.
	var featuresChanged []string
	for _, configKeyChanged := range configChanged {
		_, isFeature := cluster.ProjectFeatures[configKeyChanged]
		if isFeature {
			featuresChanged = append(featuresChanged, configKeyChanged)
		}
	}

	// Quick checks.
	if len(featuresChanged) > 0 {
		if project.Name == projecthelpers.Default {
			return response.BadRequest(fmt.Errorf("You can't change the features of the default project"))
		}

		// Consider the project empty if it is only used by the default profile.
		usedByLen := len(project.UsedBy)
		projectInUse := usedByLen > 1 || (usedByLen == 1 && !strings.Contains(project.UsedBy[0], "/profiles/default"))
		if projectInUse {
			// Check if feature is allowed to be changed.
			for _, featureChanged := range featuresChanged {
				// If feature is currently enabled, and it is being changed in the request, it
				// must be being disabled. So prevent it on non-empty projects.
				if shared.IsTrue(project.Config[featureChanged]) {
					return response.BadRequest(fmt.Errorf("Project feature %q cannot be disabled on non-empty projects", featureChanged))
				}

				// If feature is currently disabled, and it is being changed in the request, it
				// must be being enabled. So check if feature can be enabled on non-empty projects.
				if shared.IsFalse(project.Config[featureChanged]) && !cluster.ProjectFeatures[featureChanged].CanEnableNonEmpty {
					return response.BadRequest(fmt.Errorf("Project feature %q cannot be enabled on non-empty projects", featureChanged))
				}
			}
		}
	}

	// Validate the configuration.
	err := projectValidateConfig(s, req.Config)
	if err != nil {
		return response.BadRequest(err)
	}

	// Update the database entry.
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		err := projecthelpers.AllowProjectUpdate(tx, project.Name, req.Config, configChanged)
		if err != nil {
			return err
		}

		err = cluster.UpdateProject(ctx, tx.Tx(), project.Name, req)
		if err != nil {
			return fmt.Errorf("Persist profile changes: %w", err)
		}

		if shared.StringInSlice("features.profiles", configChanged) {
			if shared.IsTrue(req.Config["features.profiles"]) {
				err = projectCreateDefaultProfile(tx, project.Name)
				if err != nil {
					return err
				}
			} else {
				// Delete the project-specific default profile.
				err = cluster.DeleteProfile(ctx, tx.Tx(), project.Name, projecthelpers.Default)
				if err != nil {
					return fmt.Errorf("Delete project default profile: %w", err)
				}
			}
		}

		if shared.StringInSlice("features.images", configChanged) && shared.IsFalse(req.Config["features.images"]) && shared.IsTrue(req.Config["features.profiles"]) {
			err = cluster.InitProjectWithoutImages(ctx, tx.Tx(), project.Name)
			if err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// swagger:operation POST /1.0/projects/{name} projects project_post
//
//	Rename the project
//
//	Renames an existing project.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: body
//	    name: project
//	    description: Project rename request
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ProjectPost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func projectPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Parse the request.
	req := api.ProjectPost{}

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Quick checks.
	if name == projecthelpers.Default {
		return response.Forbidden(fmt.Errorf("The 'default' project cannot be renamed"))
	}

	// Perform the rename.
	run := func(op *operations.Operation) error {
		var id int64
		err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			project, err := cluster.GetProject(ctx, tx.Tx(), req.Name)
			if err != nil && !response.IsNotFoundError(err) {
				return fmt.Errorf("Failed checking if project %q exists: %w", req.Name, err)
			}

			if project != nil {
				return fmt.Errorf("A project named %q already exists", req.Name)
			}

			project, err = cluster.GetProject(ctx, tx.Tx(), name)
			if err != nil {
				return fmt.Errorf("Failed loading project %q: %w", name, err)
			}

			empty, err := projectIsEmpty(ctx, project, tx)
			if err != nil {
				return err
			}

			if !empty {
				return fmt.Errorf("Only empty projects can be renamed")
			}

			id, err = cluster.GetProjectID(ctx, tx.Tx(), name)
			if err != nil {
				return fmt.Errorf("Failed getting project ID for project %q: %w", name, err)
			}

			err = projectValidateName(req.Name)
			if err != nil {
				return err
			}

			return cluster.RenameProject(ctx, tx.Tx(), name, req.Name)
		})
		if err != nil {
			return err
		}

		if d.rbac != nil {
			err = d.rbac.RenameProject(id, req.Name)
			if err != nil {
				return err
			}
		}

		requestor := request.CreateRequestor(r)
		s.Events.SendLifecycle(req.Name, lifecycle.ProjectRenamed.Event(req.Name, requestor, logger.Ctx{"old_name": name}))

		return nil
	}

	op, err := operations.OperationCreate(s, "", operations.OperationClassTask, operationtype.ProjectRename, nil, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation DELETE /1.0/projects/{name} projects project_delete
//
//	Delete the project
//
//	Removes the project.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func projectDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Quick checks.
	if name == projecthelpers.Default {
		return response.Forbidden(fmt.Errorf("The 'default' project cannot be deleted"))
	}

	var id int64
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		project, err := cluster.GetProject(ctx, tx.Tx(), name)
		if err != nil {
			return fmt.Errorf("Fetch project %q: %w", name, err)
		}

		empty, err := projectIsEmpty(ctx, project, tx)
		if err != nil {
			return err
		}

		if !empty {
			return fmt.Errorf("Only empty projects can be removed")
		}

		id, err = cluster.GetProjectID(ctx, tx.Tx(), name)
		if err != nil {
			return fmt.Errorf("Fetch project id %q: %w", name, err)
		}

		return cluster.DeleteProject(ctx, tx.Tx(), name)
	})

	if err != nil {
		return response.SmartError(err)
	}

	if d.rbac != nil {
		err = d.rbac.DeleteProject(id)
		if err != nil {
			return response.SmartError(err)
		}
	}

	requestor := request.CreateRequestor(r)
	s.Events.SendLifecycle(name, lifecycle.ProjectDeleted.Event(name, requestor, nil))

	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/projects/{name}/state projects project_state_get
//
//	Get the project state
//
//	Gets a specific project resource consumption information.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Project state
//	    schema:
//	      type: object
//	      description: Sync response
//	      properties:
//	        type:
//	          type: string
//	          description: Response type
//	          example: sync
//	        status:
//	          type: string
//	          description: Status description
//	          example: Success
//	        status_code:
//	          type: integer
//	          description: Status code
//	          example: 200
//	        metadata:
//	          $ref: "#/definitions/ProjectState"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func projectStateGet(d *Daemon, r *http.Request) response.Response {
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Check user permissions.
	if !rbac.UserHasPermission(r, name, "view") {
		return response.Forbidden(nil)
	}

	// Setup the state struct.
	state := api.ProjectState{}

	// Get current limits and usage.
	err = d.State().DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		result, err := projecthelpers.GetCurrentAllocations(ctx, tx, name)
		if err != nil {
			return err
		}

		state.Resources = result

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, &state)
}

// Check if a project is empty.
func projectIsEmpty(ctx context.Context, project *cluster.Project, tx *db.ClusterTx) (bool, error) {
	instances, err := cluster.GetInstances(ctx, tx.Tx(), cluster.InstanceFilter{Project: &project.Name})
	if err != nil {
		return false, err
	}

	if len(instances) > 0 {
		return false, nil
	}

	images, err := cluster.GetImages(ctx, tx.Tx(), cluster.ImageFilter{Project: &project.Name})
	if err != nil {
		return false, err
	}

	if len(images) > 0 {
		return false, nil
	}

	profiles, err := cluster.GetProfiles(ctx, tx.Tx(), cluster.ProfileFilter{Project: &project.Name})
	if err != nil {
		return false, err
	}

	if len(profiles) > 0 {
		// Consider the project empty if it is only used by the default profile.
		if len(profiles) == 1 && profiles[0].Name == "default" {
			return true, nil
		}

		return false, nil
	}

	volumes, err := tx.GetStorageVolumeURIs(ctx, project.Name)
	if err != nil {
		return false, err
	}

	if len(volumes) > 0 {
		return false, nil
	}

	networks, err := tx.GetNetworkURIs(ctx, project.ID, project.Name)
	if err != nil {
		return false, err
	}

	if len(networks) > 0 {
		return false, nil
	}

	acls, err := tx.GetNetworkACLURIs(ctx, project.ID, project.Name)
	if err != nil {
		return false, err
	}

	if len(acls) > 0 {
		return false, nil
	}

	return true, nil
}

func isEitherAllowOrBlock(value string) error {
	return validate.Optional(validate.IsOneOf("block", "allow"))(value)
}

func isEitherAllowOrBlockOrManaged(value string) error {
	return validate.Optional(validate.IsOneOf("block", "allow", "managed"))(value)
}

func projectValidateConfig(s *state.State, config map[string]string) error {
	// Validate the project configuration.
	projectConfigKeys := map[string]func(value string) error{
		"backups.compression_algorithm":        validate.IsCompressionAlgorithm,
		"features.profiles":                    validate.Optional(validate.IsBool),
		"features.images":                      validate.Optional(validate.IsBool),
		"features.storage.volumes":             validate.Optional(validate.IsBool),
		"features.storage.buckets":             validate.Optional(validate.IsBool),
		"features.networks":                    validate.Optional(validate.IsBool),
		"features.networks.zones":              validate.Optional(validate.IsBool),
		"images.auto_update_cached":            validate.Optional(validate.IsBool),
		"images.auto_update_interval":          validate.Optional(validate.IsInt64),
		"images.compression_algorithm":         validate.IsCompressionAlgorithm,
		"images.default_architecture":          validate.Optional(validate.IsArchitecture),
		"images.remote_cache_expiry":           validate.Optional(validate.IsInt64),
		"limits.instances":                     validate.Optional(validate.IsUint32),
		"limits.containers":                    validate.Optional(validate.IsUint32),
		"limits.virtual-machines":              validate.Optional(validate.IsUint32),
		"limits.memory":                        validate.Optional(validate.IsSize),
		"limits.processes":                     validate.Optional(validate.IsUint32),
		"limits.cpu":                           validate.Optional(validate.IsUint32),
		"limits.disk":                          validate.Optional(validate.IsSize),
		"limits.networks":                      validate.Optional(validate.IsUint32),
		"restricted":                           validate.Optional(validate.IsBool),
		"restricted.backups":                   isEitherAllowOrBlock,
		"restricted.cluster.groups":            validate.Optional(validate.IsListOf(validate.IsAny)),
		"restricted.cluster.target":            isEitherAllowOrBlock,
		"restricted.containers.interception":   validate.Optional(validate.IsOneOf("allow", "block", "full")),
		"restricted.containers.nesting":        isEitherAllowOrBlock,
		"restricted.containers.lowlevel":       isEitherAllowOrBlock,
		"restricted.containers.privilege":      validate.Optional(validate.IsOneOf("allow", "unprivileged", "isolated")),
		"restricted.virtual-machines.lowlevel": isEitherAllowOrBlock,
		"restricted.devices.unix-char":         isEitherAllowOrBlock,
		"restricted.devices.unix-block":        isEitherAllowOrBlock,
		"restricted.devices.unix-hotplug":      isEitherAllowOrBlock,
		"restricted.devices.infiniband":        isEitherAllowOrBlock,
		"restricted.devices.gpu":               isEitherAllowOrBlock,
		"restricted.devices.usb":               isEitherAllowOrBlock,
		"restricted.devices.pci":               isEitherAllowOrBlock,
		"restricted.devices.proxy":             isEitherAllowOrBlock,
		"restricted.devices.nic":               isEitherAllowOrBlockOrManaged,
		"restricted.devices.disk":              isEitherAllowOrBlockOrManaged,
		"restricted.devices.disk.paths":        validate.Optional(validate.IsListOf(validate.IsAbsFilePath)),
		"restricted.idmap.uid":                 validate.Optional(validate.IsListOf(validate.IsUint32Range)),
		"restricted.idmap.gid":                 validate.Optional(validate.IsListOf(validate.IsUint32Range)),
		"restricted.networks.access":           validate.Optional(validate.IsListOf(validate.IsAny)),
		"restricted.networks.uplinks":          validate.Optional(validate.IsListOf(validate.IsAny)),
		"restricted.networks.subnets": validate.Optional(func(value string) error {
			return projectValidateRestrictedSubnets(s, value)
		}),
		"restricted.networks.zones": validate.IsListOf(validate.IsAny),
		"restricted.snapshots":      isEitherAllowOrBlock,
	}

	for k, v := range config {
		key := k

		// User keys are free for all.
		if strings.HasPrefix(key, "user.") {
			continue
		}

		// Then validate.
		validator, ok := projectConfigKeys[key]
		if !ok {
			return fmt.Errorf("Invalid project configuration key %q", k)
		}

		err := validator(v)
		if err != nil {
			return fmt.Errorf("Invalid project configuration key %q value: %w", k, err)
		}
	}

	// Ensure that restricted projects have their own profiles. Otherwise restrictions in this project could
	// be bypassed by settings from the default project's profiles that are not checked against this project's
	// restrictions when they are configured.
	if shared.IsTrue(config["restricted"]) && shared.IsFalse(config["features.profiles"]) {
		return fmt.Errorf("Projects without their own profiles cannot be restricted")
	}

	return nil
}

func projectValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("No name provided")
	}

	if strings.Contains(name, "/") {
		return fmt.Errorf("Project names may not contain slashes")
	}

	if strings.Contains(name, " ") {
		return fmt.Errorf("Project names may not contain spaces")
	}

	if strings.Contains(name, "_") {
		return fmt.Errorf("Project names may not contain underscores")
	}

	if strings.Contains(name, "'") || strings.Contains(name, `"`) {
		return fmt.Errorf("Project names may not contain quotes")
	}

	if name == "*" {
		return fmt.Errorf("Reserved project name")
	}

	if shared.StringInSlice(name, []string{".", ".."}) {
		return fmt.Errorf("Invalid project name %q", name)
	}

	return nil
}

// projectValidateRestrictedSubnets checks that the project's restricted.networks.subnets are properly formatted
// and are within the specified uplink network's routes.
func projectValidateRestrictedSubnets(s *state.State, value string) error {
	for _, subnetRaw := range shared.SplitNTrimSpace(value, ",", -1, false) {
		subnetParts := strings.SplitN(subnetRaw, ":", 2)
		if len(subnetParts) != 2 {
			return fmt.Errorf(`Subnet %q invalid, must be in the format of "<uplink network>:<subnet>"`, subnetRaw)
		}

		uplinkName := subnetParts[0]
		subnetStr := subnetParts[1]

		restrictedSubnetIP, restrictedSubnet, err := net.ParseCIDR(subnetStr)
		if err != nil {
			return err
		}

		if restrictedSubnetIP.String() != restrictedSubnet.IP.String() {
			return fmt.Errorf("Not an IP network address %q", subnetStr)
		}

		// Check uplink exists and load config to compare subnets.
		_, uplink, _, err := s.DB.Cluster.GetNetworkInAnyState(projecthelpers.Default, uplinkName)
		if err != nil {
			return fmt.Errorf("Invalid uplink network %q: %w", uplinkName, err)
		}

		// Parse uplink route subnets.
		var uplinkRoutes []*net.IPNet
		for _, k := range []string{"ipv4.routes", "ipv6.routes"} {
			if uplink.Config[k] == "" {
				continue
			}

			uplinkRoutes, err = network.SubnetParseAppend(uplinkRoutes, shared.SplitNTrimSpace(uplink.Config[k], ",", -1, false)...)
			if err != nil {
				return err
			}
		}

		foundMatch := false
		// Check that the restricted subnet is within one of the uplink's routes.
		for _, uplinkRoute := range uplinkRoutes {
			if network.SubnetContains(uplinkRoute, restrictedSubnet) {
				foundMatch = true
				break
			}
		}

		if !foundMatch {
			return fmt.Errorf("Uplink network %q doesn't contain %q in its routes", uplinkName, restrictedSubnet.String())
		}
	}

	return nil
}
