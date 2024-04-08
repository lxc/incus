package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/mux"

	internalInstance "github.com/lxc/incus/v6/internal/instance"
	"github.com/lxc/incus/v6/internal/jmap"
	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/db/operationtype"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/lifecycle"
	"github.com/lxc/incus/v6/internal/server/operations"
	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/internal/server/request"
	"github.com/lxc/incus/v6/internal/server/response"
	localUtil "github.com/lxc/incus/v6/internal/server/util"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// swagger:operation GET /1.0/instances/{name}/backups instances instance_backups_get
//
//  Get the backups
//
//  Returns a list of instance backups (URLs).
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: query
//      name: project
//      description: Project name
//      type: string
//      example: default
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
//                "/1.0/instances/foo/backups/backup0",
//                "/1.0/instances/foo/backups/backup1"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/instances/{name}/backups?recursion=1 instances instance_backups_get_recursion1
//
//	Get the backups
//
//	Returns a list of instance backups (structs).
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
//	          description: List of instance backups
//	          items:
//	            $ref: "#/definitions/InstanceBackup"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceBackupsGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := request.ProjectParam(r)
	cname, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if internalInstance.IsSnapshot(cname) {
		return response.BadRequest(fmt.Errorf("Invalid instance name"))
	}

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(s, r, projectName, cname, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	recursion := localUtil.IsRecursionRequest(r)

	c, err := instance.LoadByProjectAndName(s, projectName, cname)
	if err != nil {
		return response.SmartError(err)
	}

	backups, err := c.Backups()
	if err != nil {
		return response.SmartError(err)
	}

	resultString := []string{}
	resultMap := []*api.InstanceBackup{}

	for _, backup := range backups {
		if !recursion {
			url := fmt.Sprintf("/%s/instances/%s/backups/%s",
				version.APIVersion, cname, strings.Split(backup.Name(), "/")[1])
			resultString = append(resultString, url)
		} else {
			render := backup.Render()
			resultMap = append(resultMap, render)
		}
	}

	if !recursion {
		return response.SyncResponse(true, resultString)
	}

	return response.SyncResponse(true, resultMap)
}

// swagger:operation POST /1.0/instances/{name}/backups instances instance_backups_post
//
//	Create a backup
//
//	Creates a new backup.
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
//	    name: backup
//	    description: Backup request
//	    required: false
//	    schema:
//	      $ref: "#/definitions/InstanceBackupsPost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceBackupsPost(d *Daemon, r *http.Request) response.Response {
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

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		err := project.AllowBackupCreation(tx, projectName)
		return err
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Handle requests targeted to a container on a different node.
	resp, err := forwardedResponseIfInstanceIsRemote(s, r, projectName, name, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	inst, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	rj := jmap.Map{}
	err = json.NewDecoder(r.Body).Decode(&rj)
	if err != nil {
		return response.InternalError(err)
	}

	expiry, _ := rj.GetString("expires_at")
	if expiry == "" {
		// Disable expiration by setting it to zero time.
		rj["expires_at"] = time.Date(1, time.January, 1, 0, 0, 0, 0, time.UTC)
	}

	// Create body with correct expiry.
	body, err := json.Marshal(rj)
	if err != nil {
		return response.InternalError(err)
	}

	req := api.InstanceBackupsPost{}

	err = json.Unmarshal(body, &req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Name == "" {
		// come up with a name.
		backups, err := inst.Backups()
		if err != nil {
			return response.BadRequest(err)
		}

		base := name + internalInstance.SnapshotDelimiter + "backup"
		length := len(base)
		max := 0

		for _, backup := range backups {
			// Ignore backups not containing base.
			if !strings.HasPrefix(backup.Name(), base) {
				continue
			}

			substr := backup.Name()[length:]
			var num int
			count, err := fmt.Sscanf(substr, "%d", &num)
			if err != nil || count != 1 {
				continue
			}

			if num >= max {
				max = num + 1
			}
		}

		req.Name = fmt.Sprintf("backup%d", max)
	}

	// Validate the name.
	if strings.Contains(req.Name, "/") {
		return response.BadRequest(fmt.Errorf("Backup names may not contain slashes"))
	}

	fullName := name + internalInstance.SnapshotDelimiter + req.Name
	instanceOnly := req.InstanceOnly

	backup := func(op *operations.Operation) error {
		args := db.InstanceBackup{
			Name:                 fullName,
			InstanceID:           inst.ID(),
			CreationDate:         time.Now(),
			ExpiryDate:           req.ExpiresAt,
			InstanceOnly:         instanceOnly,
			OptimizedStorage:     req.OptimizedStorage,
			CompressionAlgorithm: req.CompressionAlgorithm,
		}

		err := backupCreate(s, args, inst, op)
		if err != nil {
			return fmt.Errorf("Create backup: %w", err)
		}

		return nil
	}

	resources := map[string][]api.URL{}
	resources["instances"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", name)}
	resources["backups"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", name, "backups", req.Name)}

	op, err := operations.OperationCreate(s, projectName, operations.OperationClassTask,
		operationtype.BackupCreate, resources, nil, backup, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation GET /1.0/instances/{name}/backups/{backup} instances instance_backup_get
//
//	Get the backup
//
//	Gets a specific instance backup.
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
//	    description: Instance backup
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
//	          $ref: "#/definitions/InstanceBackup"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceBackupGet(d *Daemon, r *http.Request) response.Response {
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

	backupName, err := url.PathUnescape(mux.Vars(r)["backupName"])
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

	fullName := name + internalInstance.SnapshotDelimiter + backupName
	backup, err := instance.BackupLoadByName(s, projectName, fullName)
	if err != nil {
		return response.SmartError(err)
	}

	return response.SyncResponse(true, backup.Render())
}

// swagger:operation POST /1.0/instances/{name}/backups/{backup} instances instance_backup_post
//
//	Rename a backup
//
//	Renames an instance backup.
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
//	    name: backup
//	    description: Backup rename
//	    required: false
//	    schema:
//	      $ref: "#/definitions/InstanceBackupPost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceBackupPost(d *Daemon, r *http.Request) response.Response {
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

	backupName, err := url.PathUnescape(mux.Vars(r)["backupName"])
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

	req := api.InstanceBackupPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Validate the name
	if strings.Contains(req.Name, "/") {
		return response.BadRequest(fmt.Errorf("Backup names may not contain slashes"))
	}

	oldName := name + internalInstance.SnapshotDelimiter + backupName
	backup, err := instance.BackupLoadByName(s, projectName, oldName)
	if err != nil {
		return response.SmartError(err)
	}

	newName := name + internalInstance.SnapshotDelimiter + req.Name

	rename := func(op *operations.Operation) error {
		err := backup.Rename(newName)
		if err != nil {
			return err
		}

		return nil
	}

	resources := map[string][]api.URL{}
	resources["instances"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", name)}

	op, err := operations.OperationCreate(s, projectName, operations.OperationClassTask,
		operationtype.BackupRename, resources, nil, rename, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation DELETE /1.0/instances/{name}/backups/{backup} instances instance_backup_delete
//
//	Delete a backup
//
//	Deletes the instance backup.
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
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceBackupDelete(d *Daemon, r *http.Request) response.Response {
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

	backupName, err := url.PathUnescape(mux.Vars(r)["backupName"])
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

	fullName := name + internalInstance.SnapshotDelimiter + backupName
	backup, err := instance.BackupLoadByName(s, projectName, fullName)
	if err != nil {
		return response.SmartError(err)
	}

	remove := func(op *operations.Operation) error {
		err := backup.Delete()
		if err != nil {
			return err
		}

		return nil
	}

	resources := map[string][]api.URL{}
	resources["instances"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", name)}

	op, err := operations.OperationCreate(s, projectName, operations.OperationClassTask,
		operationtype.BackupRemove, resources, nil, remove, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation GET /1.0/instances/{name}/backups/{backup}/export instances instance_backup_export
//
//	Get the raw backup file(s)
//
//	Download the raw backup file(s) from the server.
//
//	---
//	produces:
//	  - application/octet-stream
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	    description: Raw image data
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceBackupExportGet(d *Daemon, r *http.Request) response.Response {
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

	backupName, err := url.PathUnescape(mux.Vars(r)["backupName"])
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

	fullName := name + internalInstance.SnapshotDelimiter + backupName
	backup, err := instance.BackupLoadByName(s, projectName, fullName)
	if err != nil {
		return response.SmartError(err)
	}

	ent := response.FileResponseEntry{
		Path: internalUtil.VarPath("backups", "instances", project.Instance(projectName, backup.Name())),
	}

	s.Events.SendLifecycle(projectName, lifecycle.InstanceBackupRetrieved.Event(fullName, backup.Instance(), nil))

	return response.FileResponse(r, []response.FileResponseEntry{ent}, nil)
}
