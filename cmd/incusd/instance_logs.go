package main

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/gorilla/mux"

	internalInstance "github.com/lxc/incus/v6/internal/instance"
	"github.com/lxc/incus/v6/internal/revert"
	"github.com/lxc/incus/v6/internal/server/auth"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/lifecycle"
	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/internal/server/request"
	"github.com/lxc/incus/v6/internal/server/response"
	"github.com/lxc/incus/v6/internal/server/storage"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/internal/version"
)

var instanceLogCmd = APIEndpoint{
	Name: "instanceLog",
	Path: "instances/{name}/logs/{file}",

	Delete: APIEndpointAction{Handler: instanceLogDelete, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanEdit, "name")},
	Get:    APIEndpointAction{Handler: instanceLogGet, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanView, "name")},
}

var instanceLogsCmd = APIEndpoint{
	Name: "instanceLogs",
	Path: "instances/{name}/logs",

	Get: APIEndpointAction{Handler: instanceLogsGet, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanView, "name")},
}

var instanceExecOutputCmd = APIEndpoint{
	Name: "instanceExecOutput",
	Path: "instances/{name}/logs/exec-output/{file}",

	Delete: APIEndpointAction{Handler: instanceExecOutputDelete, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanExec, "name")},
	Get:    APIEndpointAction{Handler: instanceExecOutputGet, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanExec, "name")},
}

var instanceExecOutputsCmd = APIEndpoint{
	Name: "instanceExecOutputs",
	Path: "instances/{name}/logs/exec-output",

	Get: APIEndpointAction{Handler: instanceExecOutputsGet, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanExec, "name")},
}

// swagger:operation GET /1.0/instances/{name}/logs instances instance_logs_get
//
//	Get the log files
//
//	Returns a list of log files (URLs).
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
//	          description: List of endpoints
//	          items:
//	            type: string
//	          example: |-
//	            [
//	              "/1.0/instances/foo/logs/lxc.log"
//	            ]
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceLogsGet(d *Daemon, r *http.Request) response.Response {
	/* Let's explicitly *not* try to do a containerLoadByName here. In some
	 * cases (e.g. when container creation failed), the container won't
	 * exist in the DB but it does have some log files on disk.
	 *
	 * However, we should check this name and ensure it's a valid container
	 * name just so that people can't list arbitrary directories.
	 */

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
	resp, err := forwardedResponseIfInstanceIsRemote(d.State(), r, projectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	err = instance.ValidName(name, false)
	if err != nil {
		return response.BadRequest(err)
	}

	result := []string{}

	fullName := project.Instance(projectName, name)
	dents, err := os.ReadDir(internalUtil.LogPath(fullName))
	if err != nil {
		return response.SmartError(err)
	}

	for _, f := range dents {
		if !validLogFileName(f.Name()) {
			continue
		}

		result = append(result, fmt.Sprintf("/%s/instances/%s/logs/%s", version.APIVersion, name, f.Name()))
	}

	return response.SyncResponse(true, result)
}

// swagger:operation GET /1.0/instances/{name}/logs/{filename} instances instance_log_get
//
//	Get the log file
//
//	Gets the log file.
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
//	responses:
//	  "200":
//	     description: Raw file
//	     content:
//	       application/octet-stream:
//	         schema:
//	           type: string
//	           example: some-text
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceLogGet(d *Daemon, r *http.Request) response.Response {
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

	// Ensure instance exists.
	inst, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(s, r, projectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	file, err := url.PathUnescape(mux.Vars(r)["file"])
	if err != nil {
		return response.SmartError(err)
	}

	err = instance.ValidName(name, false)
	if err != nil {
		return response.BadRequest(err)
	}

	if !validLogFileName(file) {
		return response.BadRequest(fmt.Errorf("Log file name %q not valid", file))
	}

	ent := response.FileResponseEntry{
		Path:     internalUtil.LogPath(project.Instance(projectName, name), file),
		Filename: file,
	}

	s.Events.SendLifecycle(projectName, lifecycle.InstanceLogRetrieved.Event(file, inst, request.CreateRequestor(r), nil))

	return response.FileResponse(r, []response.FileResponseEntry{ent}, nil)
}

// swagger:operation DELETE /1.0/instances/{name}/logs/{filename} instances instance_log_delete
//
//	Delete the log file
//
//	Removes the log file.
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
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceLogDelete(d *Daemon, r *http.Request) response.Response {
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

	// Ensure instance exists.
	inst, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(s, r, projectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	file, err := url.PathUnescape(mux.Vars(r)["file"])
	if err != nil {
		return response.SmartError(err)
	}

	err = instance.ValidName(name, false)
	if err != nil {
		return response.BadRequest(err)
	}

	if !validLogFileName(file) {
		return response.BadRequest(fmt.Errorf("Log file name %q not valid", file))
	}

	if !strings.HasSuffix(file, ".log") || file == "lxc.log" || file == "qemu.log" {
		return response.BadRequest(fmt.Errorf("Only log files excluding qemu.log and lxc.log may be deleted"))
	}

	err = os.Remove(internalUtil.LogPath(project.Instance(projectName, name), file))
	if err != nil {
		return response.SmartError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.InstanceLogDeleted.Event(file, inst, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

// swagger:operation GET /1.0/instances/{name}/logs/exec-output instances instance_exec-outputs_get
//
//	Get the exec record-output files
//
//	Returns a list of exec record-output files (URLs).
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
//	          description: List of endpoints
//	          items:
//	            type: string
//	          example: |-
//	            [
//	              "/1.0/instances/foo/logs/exec-output/exec_d0a89537-0617-4ed6-a79b-c2e88a970965.stdout",
//	              "/1.0/instances/foo/logs/exec-output/exec_d0a89537-0617-4ed6-a79b-c2e88a970965.stderr",
//	            ]
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceExecOutputsGet(d *Daemon, r *http.Request) response.Response {
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

	// Ensure instance exists.
	inst, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(d.State(), r, projectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	err = instance.ValidName(name, false)
	if err != nil {
		return response.BadRequest(err)
	}

	// Mount the instance's root volume
	pool, err := storage.LoadByInstance(s, inst)
	if err != nil {
		return response.SmartError(err)
	}

	_, err = pool.MountInstance(inst, nil)
	if err != nil {
		return response.SmartError(err)
	}

	defer func() { _ = pool.UnmountInstance(inst, nil) }()

	// Read exec record-output files
	dents, err := os.ReadDir(inst.ExecOutputPath())
	if err != nil {
		return response.SmartError(err)
	}

	result := []string{}
	for _, f := range dents {
		if !validExecOutputFileName(f.Name()) {
			continue
		}

		result = append(result, fmt.Sprintf("/%s/instances/%s/logs/exec-output/%s", version.APIVersion, name, f.Name()))
	}

	return response.SyncResponse(true, result)
}

// swagger:operation GET /1.0/instances/{name}/logs/exec-output/{filename} instances instance_exec-output_get
//
//	Get the exec-output log file
//
//	Gets the exec-output file.
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
//	responses:
//	  "200":
//	     description: Raw file
//	     content:
//	       application/octet-stream:
//	         schema:
//	           type: string
//	           example: some-text
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceExecOutputGet(d *Daemon, r *http.Request) response.Response {
	revert := revert.New()
	defer revert.Fail()

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

	// Ensure instance exists.
	inst, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(s, r, projectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	file, err := url.PathUnescape(mux.Vars(r)["file"])
	if err != nil {
		return response.SmartError(err)
	}

	err = instance.ValidName(name, false)
	if err != nil {
		return response.BadRequest(err)
	}

	if !validExecOutputFileName(file) {
		return response.BadRequest(fmt.Errorf("Exec record-output file name %q not valid", file))
	}

	// Mount the instance's root volume
	pool, err := storage.LoadByInstance(s, inst)
	if err != nil {
		return response.SmartError(err)
	}

	_, err = pool.MountInstance(inst, nil)
	if err != nil {
		return response.SmartError(err)
	}

	revert.Add(func() { _ = pool.UnmountInstance(inst, nil) })
	cleanup := revert.Clone()
	revert.Success()

	ent := response.FileResponseEntry{
		Path:     filepath.Join(inst.ExecOutputPath(), file),
		Filename: file,
		Cleanup:  cleanup.Fail,
	}

	s.Events.SendLifecycle(projectName, lifecycle.InstanceLogRetrieved.Event(file, inst, request.CreateRequestor(r), nil))

	return response.FileResponse(r, []response.FileResponseEntry{ent}, nil)
}

// swagger:operation DELETE /1.0/instances/{name}/logs/exec-output/{filename} instances instance_exec-output_delete
//
//	Delete the exec record-output file
//
//	Removes the exec record-output file.
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
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceExecOutputDelete(d *Daemon, r *http.Request) response.Response {
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

	// Ensure instance exists.
	inst, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(s, r, projectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	file, err := url.PathUnescape(mux.Vars(r)["file"])
	if err != nil {
		return response.SmartError(err)
	}

	err = instance.ValidName(name, false)
	if err != nil {
		return response.BadRequest(err)
	}

	if !validExecOutputFileName(file) {
		return response.BadRequest(fmt.Errorf("Exec record-output file name %q not valid", file))
	}

	// Mount the instance's root volume
	pool, err := storage.LoadByInstance(s, inst)
	if err != nil {
		return response.SmartError(err)
	}

	_, err = pool.MountInstance(inst, nil)
	if err != nil {
		return response.SmartError(err)
	}

	defer func() { _ = pool.UnmountInstance(inst, nil) }()

	err = os.Remove(filepath.Join(inst.ExecOutputPath(), file))
	if err != nil {
		return response.SmartError(err)
	}

	s.Events.SendLifecycle(projectName, lifecycle.InstanceLogDeleted.Event(file, inst, request.CreateRequestor(r), nil))

	return response.EmptySyncResponse
}

func validLogFileName(fname string) bool {
	/* Let's just require that the paths be relative, so that we don't have
	 * to deal with any escaping or whatever.
	 */
	return fname == "lxc.log" ||
		fname == "qemu.log" ||
		strings.HasPrefix(fname, "migration_") ||
		strings.HasPrefix(fname, "snapshot_")
}

func validExecOutputFileName(fName string) bool {
	return (strings.HasSuffix(fName, ".stdout") || strings.HasSuffix(fName, ".stderr")) &&
		strings.HasPrefix(fName, "exec_")
}
