package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/gorilla/mux"
	"gopkg.in/yaml.v2"

	internalInstance "github.com/lxc/incus/internal/instance"
	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/lifecycle"
	"github.com/lxc/incus/internal/server/request"
	"github.com/lxc/incus/internal/server/response"
	"github.com/lxc/incus/internal/server/state"
	storagePools "github.com/lxc/incus/internal/server/storage"
	localUtil "github.com/lxc/incus/internal/server/util"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/util"
)

// swagger:operation GET /1.0/instances/{name}/metadata instances instance_metadata_get
//
//	Get the instance image metadata
//
//	Gets the image metadata for the instance.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	    description: Image metadata
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
//	          $ref: "#/definitions/ImageMetadata"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceMetadataGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := request.ProjectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if internalInstance.IsSnapshot(name) {
		return response.BadRequest(fmt.Errorf("Invalid instance name"))
	}

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(s, r, projectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	// Load the container
	c, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	// Start the storage if needed
	pool, err := storagePools.LoadByInstance(s, c)
	if err != nil {
		return response.SmartError(err)
	}

	_, err = storagePools.InstanceMount(pool, c, nil)
	if err != nil {
		return response.SmartError(err)
	}

	defer func() { _ = storagePools.InstanceUnmount(pool, c, nil) }()

	// If missing, just return empty result
	metadataPath := filepath.Join(c.Path(), "metadata.yaml")
	if !util.PathExists(metadataPath) {
		return response.SyncResponse(true, api.ImageMetadata{})
	}

	// Read the metadata
	metadataFile, err := os.Open(metadataPath)
	if err != nil {
		return response.InternalError(err)
	}

	defer func() { _ = metadataFile.Close() }()

	data, err := io.ReadAll(metadataFile)
	if err != nil {
		return response.InternalError(err)
	}

	// Parse into the API struct
	metadata := api.ImageMetadata{}
	err = yaml.Unmarshal(data, &metadata)
	if err != nil {
		return response.SmartError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.InstanceMetadataRetrieved.Event(c, request.CreateRequestor(r), nil))

	return response.SyncResponseETag(true, metadata, metadata)
}

// swagger:operation PATCH /1.0/instances/{name}/metadata instances instance_metadata_patch
//
//	Partially update the image metadata
//
//	Updates a subset of the instance image metadata.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: metadata
//	    description: Image metadata
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ImageMetadata"
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
func instanceMetadataPatch(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := request.ProjectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if internalInstance.IsSnapshot(name) {
		return response.BadRequest(fmt.Errorf("Invalid instance name"))
	}

	// Handle requests targeted to an instance on a different node.
	resp, err := forwardedResponseIfInstanceIsRemote(s, r, projectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	// Load the instance.
	inst, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	// Start the storage if needed.
	pool, err := storagePools.LoadByInstance(s, inst)
	if err != nil {
		return response.SmartError(err)
	}

	_, err = storagePools.InstanceMount(pool, inst, nil)
	if err != nil {
		return response.SmartError(err)
	}

	defer func() { _ = storagePools.InstanceUnmount(pool, inst, nil) }()

	// Read the existing data.
	metadataPath := filepath.Join(inst.Path(), "metadata.yaml")
	metadata := api.ImageMetadata{}
	if util.PathExists(metadataPath) {
		metadataFile, err := os.Open(metadataPath)
		if err != nil {
			return response.InternalError(err)
		}

		defer func() { _ = metadataFile.Close() }()

		data, err := io.ReadAll(metadataFile)
		if err != nil {
			return response.InternalError(err)
		}

		// Parse into the API struct
		err = yaml.Unmarshal(data, &metadata)
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Validate ETag
	err = localUtil.EtagCheck(r, metadata)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	// Apply the new metadata on top.
	err = json.NewDecoder(r.Body).Decode(&metadata)
	if err != nil {
		return response.BadRequest(err)
	}

	// Update the file.
	return doInstanceMetadataUpdate(s, inst, metadata, r)
}

// swagger:operation PUT /1.0/instances/{name}/metadata instances instance_metadata_put
//
//	Update the image metadata
//
//	Updates the instance image metadata.
//
//	---
//	consumes:
//	  - application/json
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: metadata
//	    description: Image metadata
//	    required: true
//	    schema:
//	      $ref: "#/definitions/ImageMetadata"
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
func instanceMetadataPut(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := request.ProjectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if internalInstance.IsSnapshot(name) {
		return response.BadRequest(fmt.Errorf("Invalid instance name"))
	}

	// Handle requests targeted to an instance on a different node.
	resp, err := forwardedResponseIfInstanceIsRemote(s, r, projectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	// Read the new metadata.
	metadata := api.ImageMetadata{}
	err = json.NewDecoder(r.Body).Decode(&metadata)
	if err != nil {
		return response.BadRequest(err)
	}

	// Load the instance.
	inst, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	// Start the storage if needed.
	pool, err := storagePools.LoadByInstance(s, inst)
	if err != nil {
		return response.SmartError(err)
	}

	_, err = storagePools.InstanceMount(pool, inst, nil)
	if err != nil {
		return response.SmartError(err)
	}

	defer func() { _ = storagePools.InstanceUnmount(pool, inst, nil) }()

	return doInstanceMetadataUpdate(s, inst, metadata, r)
}

func doInstanceMetadataUpdate(s *state.State, inst instance.Instance, metadata api.ImageMetadata, r *http.Request) response.Response {
	// Convert YAML.
	data, err := yaml.Marshal(metadata)
	if err != nil {
		return response.BadRequest(err)
	}

	// Update the metadata.
	metadataPath := filepath.Join(inst.Path(), "metadata.yaml")
	err = os.WriteFile(metadataPath, data, 0644)
	if err != nil {
		return response.InternalError(err)
	}

	s.Events.SendLifecycle(inst.Project().Name, lifecycle.InstanceMetadataUpdated.Event(inst, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/instances/{name}/metadata/templates instances instance_metadata_templates_get
//
//	Get the template file names or a specific
//
//	If no path specified, returns a list of template file names.
//	If a path is specified, returns the file content.
//
//	---
//	produces:
//	  - application/json
//	  - application/octet-stream
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: query
//	    name: path
//	    description: Template name
//	    type: string
//	    example: hostname.tpl
//	responses:
//	  "200":
//	     description: Raw template file or file listing
//	     content:
//	       application/octet-stream:
//	         schema:
//	           type: string
//	           example: some-text
//	       application/json:
//	         schema:
//	           type: array
//	           items:
//	             type: string
//	           example: |-
//	             [
//	               "hostname.tpl",
//	               "hosts.tpl"
//	             ]
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceMetadataTemplatesGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := request.ProjectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if internalInstance.IsSnapshot(name) {
		return response.BadRequest(fmt.Errorf("Invalid instance name"))
	}

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(s, r, projectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	// Load the container
	c, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	// Start the storage if needed
	pool, err := storagePools.LoadByInstance(s, c)
	if err != nil {
		return response.SmartError(err)
	}

	_, err = storagePools.InstanceMount(pool, c, nil)
	if err != nil {
		return response.SmartError(err)
	}

	defer func() { _ = storagePools.InstanceUnmount(pool, c, nil) }()

	// Look at the request
	templateName := r.FormValue("path")
	if templateName == "" {
		templates := []string{}
		if !util.PathExists(filepath.Join(c.Path(), "templates")) {
			return response.SyncResponse(true, templates)
		}

		// List templates
		templatesPath := filepath.Join(c.Path(), "templates")
		entries, err := os.ReadDir(templatesPath)
		if err != nil {
			return response.InternalError(err)
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				templates = append(templates, entry.Name())
			}
		}

		return response.SyncResponse(true, templates)
	}

	// Check if the template exists
	templatePath, err := getContainerTemplatePath(c, templateName)
	if err != nil {
		return response.SmartError(err)
	}

	if !util.PathExists(templatePath) {
		return response.NotFound(fmt.Errorf("Template %q not found", templateName))
	}

	// Create a temporary file with the template content (since the container
	// storage might not be available when the file is read from FileResponse)
	template, err := os.Open(templatePath)
	if err != nil {
		return response.SmartError(err)
	}

	defer func() { _ = template.Close() }()

	tempfile, err := os.CreateTemp("", "incus_template")
	if err != nil {
		return response.SmartError(err)
	}

	_, err = io.Copy(tempfile, template)
	if err != nil {
		return response.InternalError(err)
	}

	err = tempfile.Close()
	if err != nil {
		return response.InternalError(err)
	}

	files := make([]response.FileResponseEntry, 1)
	files[0].Identifier = templateName
	files[0].Path = tempfile.Name()
	files[0].Filename = templateName
	files[0].Cleanup = func() { _ = os.Remove(tempfile.Name()) }

	s.Events.SendLifecycle(projectName, lifecycle.InstanceMetadataTemplateRetrieved.Event(c, request.CreateRequestor(r), logger.Ctx{"path": templateName}))

	return response.FileResponse(r, files, nil)
}

// swagger:operation POST /1.0/instances/{name}/metadata/templates instances instance_metadata_templates_post
//
//	Create or replace a template file
//
//	Creates a new image template file for the instance.
//
//	---
//	consumes:
//	  - application/octet-stream
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: path
//	    description: Template name
//	    type: string
//	    example: default
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: raw_file
//	    description: Raw file content
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceMetadataTemplatesPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := request.ProjectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if internalInstance.IsSnapshot(name) {
		return response.BadRequest(fmt.Errorf("Invalid instance name"))
	}

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(s, r, projectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	// Load the container
	c, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	// Start the storage if needed
	pool, err := storagePools.LoadByInstance(s, c)
	if err != nil {
		return response.SmartError(err)
	}

	_, err = storagePools.InstanceMount(pool, c, nil)
	if err != nil {
		return response.SmartError(err)
	}

	defer func() { _ = storagePools.InstanceUnmount(pool, c, nil) }()

	// Look at the request
	templateName := r.FormValue("path")
	if templateName == "" {
		return response.BadRequest(fmt.Errorf("missing path argument"))
	}

	if !util.PathExists(filepath.Join(c.Path(), "templates")) {
		err := os.MkdirAll(filepath.Join(c.Path(), "templates"), 0711)
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Check if the template already exists
	templatePath, err := getContainerTemplatePath(c, templateName)
	if err != nil {
		return response.SmartError(err)
	}

	// Write the new template
	template, err := os.OpenFile(templatePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return response.SmartError(err)
	}

	_, err = io.Copy(template, r.Body)
	if err != nil {
		return response.InternalError(err)
	}

	err = template.Close()
	if err != nil {
		return response.InternalError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.InstanceMetadataTemplateCreated.Event(c, request.CreateRequestor(r), logger.Ctx{"path": templateName}))

	return response.EmptySyncResponse
}

// swagger:operation DELETE /1.0/instances/{name}/metadata/templates instances instance_metadata_templates_delete
//
//	Delete a template file
//
//	Removes the template file.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: query
//	    name: path
//	    description: Template name
//	    type: string
//	    example: default
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceMetadataTemplatesDelete(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := request.ProjectParam(r)

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if internalInstance.IsSnapshot(name) {
		return response.BadRequest(fmt.Errorf("Invalid instance name"))
	}

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(s, r, projectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	// Load the container
	c, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	// Start the storage if needed
	pool, err := storagePools.LoadByInstance(s, c)
	if err != nil {
		return response.SmartError(err)
	}

	_, err = storagePools.InstanceMount(pool, c, nil)
	if err != nil {
		return response.SmartError(err)
	}

	defer func() { _ = storagePools.InstanceUnmount(pool, c, nil) }()

	// Look at the request
	templateName := r.FormValue("path")
	if templateName == "" {
		return response.BadRequest(fmt.Errorf("missing path argument"))
	}

	templatePath, err := getContainerTemplatePath(c, templateName)
	if err != nil {
		return response.SmartError(err)
	}

	if !util.PathExists(templatePath) {
		return response.NotFound(fmt.Errorf("Template %q not found", templateName))
	}

	// Delete the template
	err = os.Remove(templatePath)
	if err != nil {
		return response.InternalError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.InstanceMetadataTemplateDeleted.Event(c, request.CreateRequestor(r), logger.Ctx{"path": templateName}))

	return response.EmptySyncResponse
}

// Return the full path of a container template.
func getContainerTemplatePath(c instance.Instance, filename string) (string, error) {
	if strings.Contains(filename, "/") {
		return "", fmt.Errorf("Invalid template filename")
	}

	return filepath.Join(c.Path(), "templates", filename), nil
}
