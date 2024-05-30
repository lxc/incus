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
	"slices"
	"strings"

	"github.com/gorilla/mux"

	"github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/internal/jmap"
	"github.com/lxc/incus/v6/internal/server/auth"
	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/db/operationtype"
	"github.com/lxc/incus/v6/internal/server/lifecycle"
	"github.com/lxc/incus/v6/internal/server/network"
	"github.com/lxc/incus/v6/internal/server/operations"
	projecthelpers "github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/internal/server/request"
	"github.com/lxc/incus/v6/internal/server/response"
	"github.com/lxc/incus/v6/internal/server/state"
	localUtil "github.com/lxc/incus/v6/internal/server/util"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/util"
	"github.com/lxc/incus/v6/shared/validate"
)

var projectsCmd = APIEndpoint{
	Path: "projects",

	Get:  APIEndpointAction{Handler: projectsGet, AccessHandler: allowAuthenticated},
	Post: APIEndpointAction{Handler: projectsPost, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanCreateProjects)},
}

var projectCmd = APIEndpoint{
	Path: "projects/{name}",

	Delete: APIEndpointAction{Handler: projectDelete, AccessHandler: allowPermission(auth.ObjectTypeProject, auth.EntitlementCanEdit, "name")},
	Get:    APIEndpointAction{Handler: projectGet, AccessHandler: allowPermission(auth.ObjectTypeProject, auth.EntitlementCanView, "name")},
	Patch:  APIEndpointAction{Handler: projectPatch, AccessHandler: allowPermission(auth.ObjectTypeProject, auth.EntitlementCanEdit, "name")},
	Post:   APIEndpointAction{Handler: projectPost, AccessHandler: allowPermission(auth.ObjectTypeProject, auth.EntitlementCanEdit, "name")},
	Put:    APIEndpointAction{Handler: projectPut, AccessHandler: allowPermission(auth.ObjectTypeProject, auth.EntitlementCanEdit, "name")},
}

var projectStateCmd = APIEndpoint{
	Path: "projects/{name}/state",

	Get: APIEndpointAction{Handler: projectStateGet, AccessHandler: allowPermission(auth.ObjectTypeProject, auth.EntitlementCanView, "name")},
}

var projectAccessCmd = APIEndpoint{
	Path: "projects/{name}/access",

	Get: APIEndpointAction{Handler: projectAccess, AccessHandler: allowPermission(auth.ObjectTypeProject, auth.EntitlementCanEdit, "name")},
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
	s := d.State()

	recursion := localUtil.IsRecursionRequest(r)

	userHasPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), r, auth.EntitlementCanView, auth.ObjectTypeProject)
	if err != nil {
		return response.InternalError(err)
	}

	var result any
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		projects, err := cluster.GetProjects(ctx, tx.Tx())
		if err != nil {
			return err
		}

		filtered := []api.Project{}
		for _, project := range projects {
			if !userHasPermission(auth.ObjectProject(project.Name)) {
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

	for _, instance := range instances {
		apiInstance := api.Instance{Name: instance.Name}
		usedBy = append(usedBy, apiInstance.URL(version.APIVersion, project.Name).String())
	}

	images, err := cluster.GetImages(ctx, tx.Tx(), cluster.ImageFilter{Project: &project.Name})
	if err != nil {
		return nil, err
	}

	for _, image := range images {
		apiImage := api.Image{Fingerprint: image.Fingerprint}
		usedBy = append(usedBy, apiImage.URL(version.APIVersion, project.Name).String())
	}

	networks, err := tx.GetNetworkURIs(ctx, project.ID, project.Name)
	if err != nil {
		return nil, err
	}

	usedBy = append(usedBy, networks...)

	networkACLs, err := tx.GetNetworkACLURIs(ctx, project.ID, project.Name)
	if err != nil {
		return nil, err
	}

	usedBy = append(usedBy, networkACLs...)

	networkZones, err := tx.GetNetworkZoneURIs(ctx, project.ID, project.Name)
	if err != nil {
		return nil, err
	}

	usedBy = append(usedBy, networkZones...)

	profiles, err := cluster.GetProfiles(ctx, tx.Tx(), cluster.ProfileFilter{Project: &project.Name})
	if err != nil {
		return nil, err
	}

	for _, profile := range profiles {
		apiProfile := api.Profile{Name: profile.Name}
		usedBy = append(usedBy, apiProfile.URL(version.APIVersion, project.Name).String())
	}

	storageBuckets, err := tx.GetStorageBucketURIs(ctx, project.Name)
	if err != nil {
		return nil, err
	}

	usedBy = append(usedBy, storageBuckets...)

	storageVolumes, err := tx.GetStorageVolumeURIs(ctx, project.Name)
	if err != nil {
		return nil, err
	}

	usedBy = append(usedBy, storageVolumes...)

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
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		id, err = cluster.CreateProject(ctx, tx.Tx(), cluster.Project{Description: project.Description, Name: project.Name})
		if err != nil {
			return fmt.Errorf("Failed adding database record: %w", err)
		}

		err = cluster.CreateProjectConfig(ctx, tx.Tx(), id, project.Config)
		if err != nil {
			return fmt.Errorf("Unable to create project config for project %q: %w", project.Name, err)
		}

		if util.IsTrue(project.Config["features.profiles"]) {
			err = projectCreateDefaultProfile(ctx, tx, project.Name)
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

	err = s.Authorizer.AddProject(r.Context(), id, project.Name)
	if err != nil {
		return response.SmartError(err)
	}

	requestor := request.CreateRequestor(r)
	lc := lifecycle.ProjectCreated.Event(project.Name, requestor, nil)
	s.Events.SendLifecycle(project.Name, lc)

	return response.SyncResponseLocation(true, nil, lc.Source)
}

// Create the default profile of a project.
func projectCreateDefaultProfile(ctx context.Context, tx *db.ClusterTx, project string) error {
	// Create a default profile
	profile := cluster.Profile{}
	profile.Project = project
	profile.Name = api.ProjectDefaultName
	profile.Description = fmt.Sprintf("Default Incus profile for project %s", project)

	_, err := cluster.CreateProfile(ctx, tx.Tx(), profile)
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
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Get the database entry
	var project *api.Project
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
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

	// Get the current data
	var project *api.Project
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
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

	err = localUtil.EtagCheck(r, etag)
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

	return projectChange(r.Context(), s, project, req)
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

	// Get the current data
	var project *api.Project
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
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

	err = localUtil.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return response.InternalError(err)
	}

	rdr1 := io.NopCloser(bytes.NewBuffer(body))
	rdr2 := io.NopCloser(bytes.NewBuffer(body))

	reqRaw := jmap.Map{}
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

	return projectChange(r.Context(), s, project, req)
}

// Common logic between PUT and PATCH.
func projectChange(ctx context.Context, s *state.State, project *api.Project, req api.ProjectPut) response.Response {
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
		if project.Name == api.ProjectDefaultName {
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
				if util.IsTrue(project.Config[featureChanged]) {
					return response.BadRequest(fmt.Errorf("Project feature %q cannot be disabled on non-empty projects", featureChanged))
				}

				// If feature is currently disabled, and it is being changed in the request, it
				// must be being enabled. So check if feature can be enabled on non-empty projects.
				if util.IsFalse(project.Config[featureChanged]) && !cluster.ProjectFeatures[featureChanged].CanEnableNonEmpty {
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
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		err := projecthelpers.AllowProjectUpdate(tx, project.Name, req.Config, configChanged)
		if err != nil {
			return err
		}

		err = cluster.UpdateProject(ctx, tx.Tx(), project.Name, req)
		if err != nil {
			return fmt.Errorf("Persist profile changes: %w", err)
		}

		if slices.Contains(configChanged, "features.profiles") {
			if util.IsTrue(req.Config["features.profiles"]) {
				err = projectCreateDefaultProfile(ctx, tx, project.Name)
				if err != nil {
					return err
				}
			} else {
				// Delete the project-specific default profile.
				err = cluster.DeleteProfile(ctx, tx.Tx(), project.Name, api.ProjectDefaultName)
				if err != nil {
					return fmt.Errorf("Delete project default profile: %w", err)
				}
			}
		}

		if slices.Contains(configChanged, "features.images") && util.IsFalse(req.Config["features.images"]) && util.IsTrue(req.Config["features.profiles"]) {
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
	if name == api.ProjectDefaultName {
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

		err = s.Authorizer.RenameProject(s.ShutdownCtx, id, name, req.Name)
		if err != nil {
			return err
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
//	parameters:
//	  - in: query
//	    name: force
//	    description: Delete project and related artifacts
//	    type: boolean
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

	force := util.IsTrue(r.FormValue("force"))

	// Quick checks.
	if name == api.ProjectDefaultName {
		return response.Forbidden(fmt.Errorf("The 'default' project cannot be deleted"))
	}

	var id int64
	var usedBy []string
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		project, err := cluster.GetProject(ctx, tx.Tx(), name)
		if err != nil {
			return fmt.Errorf("Fetch project %q: %w", name, err)
		}

		if !force {
			empty, err := projectIsEmpty(ctx, project, tx)
			if err != nil {
				return err
			}

			if !empty {
				return fmt.Errorf("Only empty projects can be removed.")
			}
		} else {
			usedBy, err = projectUsedBy(ctx, tx, project)
			if err != nil {
				return err
			}
		}

		id, err = cluster.GetProjectID(ctx, tx.Tx(), name)
		if err != nil {
			return fmt.Errorf("Fetch project id %q: %w", name, err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Handle requests to empty the project.
	if force {
		// Parse used by list.
		defaultProfile := api.NewURL().Path(version.APIVersion, "profiles", api.ProjectDefaultName).Project(name).String()
		entries := map[string][]string{}
		var count int

		for _, u := range usedBy {
			// Skip the default profile.
			if u == defaultProfile {
				continue
			}

			// Parse the URL.
			uri, err := url.Parse(u)
			if err != nil {
				return response.InternalError(err)
			}

			elements := strings.Split(uri.Path, "/")
			if len(elements) < 4 {
				return response.InternalError(fmt.Errorf("Bad usedBy entry: %s", u))
			}

			if elements[2] == "storage-pools" {
				if elements[4] == "buckets" {
					if entries["storage-buckets"] == nil {
						entries["storage-buckets"] = []string{}
					}

					entry := fmt.Sprintf("%s/%s", elements[3], elements[5])
					target := uri.Query().Get("target")
					if target != "" {
						entry = fmt.Sprintf("%s/%s", entry, target)
					}

					entries["storage-buckets"] = append(entries["storage-buckets"], entry)
				} else if elements[4] == "volumes" {
					if entries["storage-volumes"] == nil {
						entries["storage-volumes"] = []string{}
					}

					entry := fmt.Sprintf("%s/%s", elements[3], elements[6])
					target := uri.Query().Get("target")
					if target != "" {
						entry = fmt.Sprintf("%s/%s", entry, target)
					}

					entries["storage-volumes"] = append(entries["storage-volumes"], entry)
				}
			} else {
				if entries[elements[2]] == nil {
					entries[elements[2]] = []string{}
				}

				entries[elements[2]] = append(entries[elements[2]], elements[3])
			}

			count++
		}

		// Connect to the local server.
		target, err := incus.ConnectIncusUnix(s.OS.GetUnixSocket(), nil)
		if err != nil {
			return response.InternalError(err)
		}

		target = target.UseProject(name)

		// Delete instances.
		for _, instName := range entries["instances"] {
			// Get current instance state.
			instState, _, err := target.GetInstance(instName)
			if err != nil {
				return response.InternalError(err)
			}

			// If running, force stop it.
			if instState.StatusCode != api.Stopped {
				req := api.InstanceStatePut{
					Action:  "stop",
					Timeout: -1,
					Force:   true,
				}

				op, err := target.UpdateInstanceState(instName, req, "")
				if err != nil {
					return response.InternalError(err)
				}

				err = op.Wait()
				if err != nil {
					return response.InternalError(err)
				}
			}

			// Get the instance configuration.
			inst, _, err := target.GetInstance(instName)
			if err != nil {
				return response.InternalError(err)
			}

			// Clear security.protection.delete if set.
			if util.IsTrue(inst.ExpandedConfig["security.protection.delete"]) {
				inst.Config["security.protection.delete"] = "false"
				op, err := target.UpdateInstance(instName, inst.Writable(), "")
				if err != nil {
					return response.InternalError(err)
				}

				err = op.Wait()
				if err != nil {
					return response.InternalError(err)
				}
			}

			// Delete the instance.
			op, err := target.DeleteInstance(instName)
			if err != nil {
				return response.InternalError(err)
			}

			err = op.Wait()
			if err != nil {
				return response.InternalError(err)
			}

			// Done deleting the instance.
			count--
		}

		// Delete profiles.
		for _, profileName := range entries["profiles"] {
			err := target.DeleteProfile(profileName)
			if err != nil {
				return response.InternalError(err)
			}

			// Done deleting the profile.
			count--
		}

		// Delete images.
		for _, imageFingerprint := range entries["images"] {
			op, err := target.DeleteImage(imageFingerprint)
			if err != nil {
				return response.InternalError(err)
			}

			err = op.Wait()
			if err != nil {
				return response.InternalError(err)
			}

			// Done deleting the image.
			count--
		}

		// Delete networks.
		for _, networkName := range entries["networks"] {
			err := target.DeleteNetwork(networkName)
			if err != nil {
				return response.InternalError(err)
			}

			// Done deleting the network.
			count--
		}

		// Delete network ACLs.
		for _, networkACLName := range entries["network-acls"] {
			err := target.DeleteNetworkACL(networkACLName)
			if err != nil {
				return response.InternalError(err)
			}

			// Done deleting the network ACL.
			count--
		}

		// Delete network zones.
		for _, networkZoneName := range entries["network-zones"] {
			err := target.DeleteNetworkZone(networkZoneName)
			if err != nil {
				return response.InternalError(err)
			}

			// Done deleting the network zone.
			count--
		}

		// Delete storage volumes.
		for _, volume := range entries["storage-volumes"] {
			fields := strings.Split(volume, "/")
			if len(fields) == 3 {
				target.UseTarget(fields[2])
			}

			err := target.DeleteStoragePoolVolume(fields[0], "custom", fields[1])
			if err != nil {
				return response.InternalError(err)
			}

			// Done deleting the storage volume.
			count--
		}

		// Delete storage buckets.
		for _, volume := range entries["storage-buckets"] {
			fields := strings.Split(volume, "/")
			if len(fields) == 3 {
				target.UseTarget(fields[2])
			}

			err := target.DeleteStoragePoolBucket(fields[0], fields[1])
			if err != nil {
				return response.InternalError(err)
			}

			// Done deleting the storage volume.
			count--
		}

		// Check if anything is left.
		if count != 0 {
			return response.BadRequest(fmt.Errorf("Project couldn't be automatically emptied"))
		}
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		return cluster.DeleteProject(ctx, tx.Tx(), name)
	})
	if err != nil {
		return response.SmartError(err)
	}

	err = s.Authorizer.DeleteProject(r.Context(), id, name)
	if err != nil {
		return response.SmartError(err)
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
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Setup the state struct.
	state := api.ProjectState{}

	// Get current limits and usage.
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
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
	usedBy, err := projectUsedBy(ctx, tx, project)
	if err != nil {
		return false, err
	}

	defaultProfile := api.NewURL().Path(version.APIVersion, "profiles", api.ProjectDefaultName).Project(project.Name).String()
	for _, entry := range usedBy {
		// Ignore the default profile.
		if entry == defaultProfile {
			continue
		}

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
		// gendoc:generate(entity=project, group=specific, key=backups.compression_algorithm)
		// Specify which compression algorithm to use for backups in this project.
		// Possible values are `bzip2`, `gzip`, `lzma`, `xz`, or `none`.
		// ---
		//  type: string
		//  shortdesc: Compression algorithm to use for backups
		"backups.compression_algorithm": validate.IsCompressionAlgorithm,

		// gendoc:generate(entity=project, group=features, key=features.profiles)
		//
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  initialvaluedesc: `true`
		//  shortdesc: Whether to use a separate set of profiles for the project
		"features.profiles": validate.Optional(validate.IsBool),

		// gendoc:generate(entity=project, group=features, key=features.images)
		// This setting applies to both images and image aliases.
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  initialvaluedesc: `true`
		//  shortdesc: Whether to use a separate set of images for the project
		"features.images": validate.Optional(validate.IsBool),

		// gendoc:generate(entity=project, group=features, key=features.storage.volumes)
		//
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  initialvaluedesc: `true`
		//  shortdesc: Whether to use a separate set of storage volumes for the project
		"features.storage.volumes": validate.Optional(validate.IsBool),

		// gendoc:generate(entity=project, group=features, key=features.storage.buckets)
		//
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  initialvaluedesc: `true`
		//  shortdesc: Whether to use a separate set of storage buckets for the project
		"features.storage.buckets": validate.Optional(validate.IsBool),

		// gendoc:generate(entity=project, group=features, key=features.networks)
		//
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  initialvaluedesc: `false`
		//  shortdesc: Whether to use a separate set of networks for the project
		"features.networks": validate.Optional(validate.IsBool),

		// gendoc:generate(entity=project, group=features, key=features.networks.zones)
		//
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  initialvaluedesc: `false`
		//  shortdesc: Whether to use a separate set of network zones for the project
		"features.networks.zones": validate.Optional(validate.IsBool),

		// gendoc:generate(entity=project, group=specific, key=images.auto_update_cached)
		//
		// ---
		//  type: bool
		//  shortdesc: Whether to automatically update cached images in the project
		"images.auto_update_cached": validate.Optional(validate.IsBool),

		// gendoc:generate(entity=project, group=specific, key=images.auto_update_interval)
		// Specify the interval in hours.
		// To disable looking for updates to cached images, set this option to `0`.
		// ---
		//  type: integer
		//  shortdesc: Interval at which to look for updates to cached images
		"images.auto_update_interval": validate.Optional(validate.IsInt64),

		// gendoc:generate(entity=project, group=specific, key=images.compression_algorithm)
		// Possible values are `bzip2`, `gzip`, `lzma`, `xz`, or `none`.
		// ---
		//  type: string
		//  shortdesc: Compression algorithm to use for new images in the project
		"images.compression_algorithm": validate.IsCompressionAlgorithm,

		// gendoc:generate(entity=project, group=specific, key=images.default_architecture)
		//
		// ---
		//  type: string
		//  shortdesc: Default architecture to use in a mixed-architecture cluster
		"images.default_architecture": validate.Optional(validate.IsArchitecture),

		// gendoc:generate(entity=project, group=specific, key=images.remote_cache_expiry)
		// Specify the number of days after which the unused cached image expires.
		// ---
		//  type: integer
		//  shortdesc: When an unused cached remote image is flushed in the project
		"images.remote_cache_expiry": validate.Optional(validate.IsInt64),

		// gendoc:generate(entity=project, group=limits, key=limits.instances)
		//
		// ---
		//  type: integer
		//  shortdesc: Maximum number of instances that can be created in the project
		"limits.instances": validate.Optional(validate.IsUint32),

		// gendoc:generate(entity=project, group=limits, key=limits.containers)
		//
		// ---
		//  type: integer
		//  shortdesc: Maximum number of containers that can be created in the project
		"limits.containers": validate.Optional(validate.IsUint32),

		// gendoc:generate(entity=project, group=limits, key=limits.virtual-machines)
		//
		// ---
		//  type: integer
		//  shortdesc: Maximum number of VMs that can be created in the project
		"limits.virtual-machines": validate.Optional(validate.IsUint32),

		// gendoc:generate(entity=project, group=limits, key=limits.memory)
		// The value is the maximum value for the sum of the individual {config:option}`instance-resource-limits:limits.memory` configurations set on the instances of the project.
		// ---
		//  type: string
		//  shortdesc: Usage limit for the host's memory for the project
		"limits.memory": validate.Optional(validate.IsSize),

		// gendoc:generate(entity=project, group=limits, key=limits.processes)
		// This value is the maximum value for the sum of the individual {config:option}`instance-resource-limits:limits.processes` configurations set on the instances of the project.
		// ---
		//  type: integer
		//  shortdesc: Maximum number of processes within the project
		"limits.processes": validate.Optional(validate.IsUint32),

		// gendoc:generate(entity=project, group=limits, key=limits.cpu)
		// This value is the maximum value for the sum of the individual {config:option}`instance-resource-limits:limits.cpu` configurations set on the instances of the project.
		// ---
		//  type: integer
		//  shortdesc: Maximum number of CPUs to use in the project
		"limits.cpu": validate.Optional(validate.IsUint32),

		// gendoc:generate(entity=project, group=limits, key=limits.disk)
		// This value is the maximum value of the aggregate disk space used by all instance volumes, custom volumes, and images of the project.
		// ---
		//  type: string
		//  shortdesc: Maximum disk space used by the project
		"limits.disk": validate.Optional(validate.IsSize),

		// gendoc:generate(entity=project, group=limits, key=limits.networks)
		//
		// ---
		//  type: integer
		//  shortdesc: Maximum number of networks that the project can have
		"limits.networks": validate.Optional(validate.IsUint32),

		// gendoc:generate(entity=project, group=restricted, key=restricted)
		// This option must be enabled to allow the `restricted.*` keys to take effect.
		// To temporarily remove the restrictions, you can disable this option instead of clearing the related keys.
		// ---
		//  type: bool
		//  defaultdesc: `false`
		//  shortdesc: Whether to block access to security-sensitive features
		"restricted": validate.Optional(validate.IsBool),

		// gendoc:generate(entity=project, group=restricted, key=restricted.backups)
		// Possible values are `allow` or `block`.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent creating instance or volume backups
		"restricted.backups": isEitherAllowOrBlock,

		// gendoc:generate(entity=project, group=restricted, key=restricted.cluster.groups)
		// If specified, this option prevents targeting cluster groups other than the provided ones.
		// ---
		//  type: string
		//  shortdesc: Cluster groups that can be targeted
		"restricted.cluster.groups": validate.Optional(validate.IsListOf(validate.IsAny)),

		// gendoc:generate(entity=project, group=restricted, key=restricted.cluster.target)
		// Possible values are `allow` or `block`.
		// When set to `allow`, this option allows targeting of cluster members (either directly or via a group) when creating or moving instances.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent targeting of cluster members
		"restricted.cluster.target": isEitherAllowOrBlock,

		// gendoc:generate(entity=project, group=restricted, key=restricted.containers.interception)
		// Possible values are `allow`, `block`, or `full`.
		// When set to `allow`, interception options that are usually safe are allowed.
		// File system mounting remains blocked.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent using system call interception options
		"restricted.containers.interception": validate.Optional(validate.IsOneOf("allow", "block", "full")),

		// gendoc:generate(entity=project, group=restricted, key=restricted.containers.nesting)
		// Possible values are `allow` or `block`.
		// When set to `allow`, {config:option}`instance-security:security.nesting` can be set to `true` for an instance.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent running nested Incus
		"restricted.containers.nesting": isEitherAllowOrBlock,

		// gendoc:generate(entity=project, group=restricted, key=restricted.containers.lowlevel)
		// Possible values are `allow` or `block`.
		// When set to `allow`, low-level container options like {config:option}`instance-raw:raw.lxc`, {config:option}`instance-raw:raw.idmap`, `volatile.*`, etc. can be used.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent using low-level container options
		"restricted.containers.lowlevel": isEitherAllowOrBlock,

		// gendoc:generate(entity=project, group=restricted, key=restricted.containers.privilege)
		// Possible values are `unprivileged`, `isolated`, and `allow`.
		//
		// - When set to `unpriviliged`, this option prevents setting {config:option}`instance-security:security.privileged` to `true`.
		// - When set to `isolated`, this option prevents setting {config:option}`instance-security:security.privileged` and {config:option}`instance-security:security.idmap.isolated` to `true`.
		// - When set to `allow`, there is no restriction.
		// ---
		//  type: string
		//  defaultdesc: `unprivileged`
		//  shortdesc: Which settings for privileged containers to prevent
		"restricted.containers.privilege": validate.Optional(validate.IsOneOf("allow", "unprivileged", "isolated")),

		// gendoc:generate(entity=project, group=restricted, key=restricted.virtual-machines.lowlevel)
		// Possible values are `allow` or `block`.
		// When set to `allow`, low-level VM options like {config:option}`instance-raw:raw.qemu`, `volatile.*`, etc. can be used.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent using low-level VM options
		"restricted.virtual-machines.lowlevel": isEitherAllowOrBlock,

		// gendoc:generate(entity=project, group=restricted, key=restricted.devices.unix-char)
		// Possible values are `allow` or `block`.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent using devices of type `unix-char`
		"restricted.devices.unix-char": isEitherAllowOrBlock,

		// gendoc:generate(entity=project, group=restricted, key=restricted.devices.unix-block)
		// Possible values are `allow` or `block`.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent using devices of type `unix-block`
		"restricted.devices.unix-block": isEitherAllowOrBlock,

		// gendoc:generate(entity=project, group=restricted, key=restricted.devices.unix-hotplug)
		// Possible values are `allow` or `block`.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent using devices of type `unix-hotplug`
		"restricted.devices.unix-hotplug": isEitherAllowOrBlock,

		// gendoc:generate(entity=project, group=restricted, key=restricted.devices.infiniband)
		// Possible values are `allow` or `block`.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent using devices of type `infiniband`
		"restricted.devices.infiniband": isEitherAllowOrBlock,

		// gendoc:generate(entity=project, group=restricted, key=restricted.devices.gpu)
		// Possible values are `allow` or `block`.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent using devices of type `gpu`
		"restricted.devices.gpu": isEitherAllowOrBlock,

		// gendoc:generate(entity=project, group=restricted, key=restricted.devices.usb)
		// Possible values are `allow` or `block`.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent using devices of type `usb`
		"restricted.devices.usb": isEitherAllowOrBlock,

		// gendoc:generate(entity=project, group=restricted, key=restricted.devices.pci)
		// Possible values are `allow` or `block`.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent using devices of type `pci`
		"restricted.devices.pci": isEitherAllowOrBlock,

		// gendoc:generate(entity=project, group=restricted, key=restricted.devices.proxy)
		// Possible values are `allow` or `block`.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent using devices of type `proxy`
		"restricted.devices.proxy": isEitherAllowOrBlock,

		// gendoc:generate(entity=project, group=restricted, key=restricted.devices.nic)
		// Possible values are `allow`, `block`, or `managed`.
		//
		// - When set to `block`, this option prevents using all network devices.
		// - When set to `managed`, this option allows using network devices only if `network=` is set.
		// - When set to `allow`, there is no restriction on which network devices can be used.
		// ---
		//  type: string
		//  defaultdesc: `managed`
		//  shortdesc: Which network devices can be used
		"restricted.devices.nic": isEitherAllowOrBlockOrManaged,

		// gendoc:generate(entity=project, group=restricted, key=restricted.devices.disk)
		// Possible values are `allow`, `block`, or `managed`.
		//
		// - When set to `block`, this option prevents using all disk devices except the root one.
		// - When set to `managed`, this option allows using disk devices only if `pool=` is set.
		// - When set to `allow`, there is no restriction on which disk devices can be used.
		// ---
		//  type: string
		//  defaultdesc: `managed`
		//  shortdesc: Which disk devices can be used
		"restricted.devices.disk": isEitherAllowOrBlockOrManaged,

		// gendoc:generate(entity=project, group=restricted, key=restricted.devices.disk.paths)
		// If {config:option}`project-restricted:restricted.devices.disk` is set to `allow`, this option controls which `source` can be used for `disk` devices.
		// Specify a comma-separated list of path prefixes that restrict the `source` setting.
		// If this option is left empty, all paths are allowed.
		// ---
		//  type: string
		//  shortdesc: Which `source` can be used for `disk` devices
		"restricted.devices.disk.paths": validate.Optional(validate.IsListOf(validate.IsAbsFilePath)),

		// gendoc:generate(entity=project, group=restricted, key=restricted.idmap.uid)
		// This option specifies the host UID ranges that are allowed in the instance's {config:option}`instance-raw:raw.idmap` setting.
		// ---
		//  type: string
		//  shortdesc: Which host UID ranges are allowed in `raw.idmap`
		"restricted.idmap.uid": validate.Optional(validate.IsListOf(validate.IsUint32Range)),

		// gendoc:generate(entity=project, group=restricted, key=restricted.idmap.gid)
		// This option specifies the host GID ranges that are allowed in the instance's {config:option}`instance-raw:raw.idmap` setting.
		// ---
		//  type: string
		//  shortdesc: Which host GID ranges are allowed in `raw.idmap`
		"restricted.idmap.gid": validate.Optional(validate.IsListOf(validate.IsUint32Range)),

		// gendoc:generate(entity=project, group=restricted, key=restricted.networks.access)
		// Specify a comma-delimited list of network names that are allowed for use in this project.
		// If this option is not set, all networks are accessible.
		//
		// Note that this setting depends on the {config:option}`project-restricted:restricted.devices.nic` setting.
		// ---
		//  type: string
		//  shortdesc: Which network names are allowed for use in this project
		"restricted.networks.access": validate.Optional(validate.IsListOf(validate.IsAny)),

		// gendoc:generate(entity=project, group=restricted, key=restricted.networks.integrations)
		// Specify a comma-delimited list of network integrations that can be used by networks in this project.
		// ---
		//  type: string
		//  shortdesc: Which network integrations can be used in this project
		"restricted.networks.integrations": validate.IsListOf(validate.IsAny),

		// gendoc:generate(entity=project, group=restricted, key=restricted.networks.uplinks)
		// Specify a comma-delimited list of network names that can be used as uplink for networks in this project.
		// ---
		//  type: string
		//  shortdesc: Which network names can be used as uplink in this project
		"restricted.networks.uplinks": validate.Optional(validate.IsListOf(validate.IsAny)),

		// gendoc:generate(entity=project, group=restricted, key=restricted.networks.subnets)
		// Specify a comma-delimited list of network subnets from the uplink networks that are allocated for use in this project.
		// Use the form `<uplink>:<subnet>`.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Which network subnets are allocated for use in this project
		"restricted.networks.subnets": validate.Optional(func(value string) error {
			return projectValidateRestrictedSubnets(s, value)
		}),

		// gendoc:generate(entity=project, group=restricted, key=restricted.networks.zones)
		// Specify a comma-delimited list of network zones that can be used (or something under them) in this project.
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Which network zones can be used in this project
		"restricted.networks.zones": validate.IsListOf(validate.IsAny),

		// gendoc:generate(entity=project, group=restricted, key=restricted.snapshots)
		//
		// ---
		//  type: string
		//  defaultdesc: `block`
		//  shortdesc: Whether to prevent creating instance or volume snapshots
		"restricted.snapshots": isEitherAllowOrBlock,
	}

	for k, v := range config {
		key := k

		// User keys are free for all.

		// gendoc:generate(entity=project, group=specific, key=user.*)
		//
		// ---
		//  type: string
		//  shortdesc: User-provided free-form key/value pairs
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
	if util.IsTrue(config["restricted"]) && util.IsFalse(config["features.profiles"]) {
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

	if slices.Contains([]string{".", ".."}, name) {
		return fmt.Errorf("Invalid project name %q", name)
	}

	return nil
}

// projectValidateRestrictedSubnets checks that the project's restricted.networks.subnets are properly formatted
// and are within the specified uplink network's routes.
func projectValidateRestrictedSubnets(s *state.State, value string) error {
	for _, subnetRaw := range util.SplitNTrimSpace(value, ",", -1, false) {
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

		var uplink *api.Network

		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Check uplink exists and load config to compare subnets.
			_, uplink, _, err = tx.GetNetworkInAnyState(ctx, api.ProjectDefaultName, uplinkName)

			return err
		})
		if err != nil {
			return fmt.Errorf("Invalid uplink network %q: %w", uplinkName, err)
		}

		// Parse uplink route subnets.
		var uplinkRoutes []*net.IPNet
		for _, k := range []string{"ipv4.routes", "ipv6.routes"} {
			if uplink.Config[k] == "" {
				continue
			}

			uplinkRoutes, err = network.SubnetParseAppend(uplinkRoutes, util.SplitNTrimSpace(uplink.Config[k], ",", -1, false)...)
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

// swagger:operation GET /1.0/projects/{name}/access projects project_access
//
//	Get who has access to a project
//
//	Gets the access information for the project.
//
//	---
//	produces:
//	  - application/json
//	responses:
//	  "200":
//	    description: Access
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
//	          $ref: "#/definitions/Access"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func projectAccess(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Quick checks.
	err = projectValidateName(name)
	if err != nil {
		return response.BadRequest(err)
	}

	// get the access struct
	access, err := s.Authorizer.GetProjectAccess(context.TODO(), name)

	if err != nil {
		return response.InternalError(err)
	}

	return response.SyncResponse(true, access)
}
