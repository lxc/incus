package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/mux"

	internalInstance "github.com/lxc/incus/internal/instance"
	"github.com/lxc/incus/internal/jmap"
	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/db/cluster"
	"github.com/lxc/incus/internal/server/db/operationtype"
	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/instance/instancetype"
	"github.com/lxc/incus/internal/server/operations"
	"github.com/lxc/incus/internal/server/project"
	"github.com/lxc/incus/internal/server/request"
	"github.com/lxc/incus/internal/server/response"
	"github.com/lxc/incus/internal/server/state"
	storagePools "github.com/lxc/incus/internal/server/storage"
	localUtil "github.com/lxc/incus/internal/server/util"
	"github.com/lxc/incus/internal/version"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/validate"
)

// swagger:operation GET /1.0/instances/{name}/snapshots instances instance_snapshots_get
//
//  Get the snapshots
//
//  Returns a list of instance snapshots (URLs).
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
//                "/1.0/instances/foo/snapshots/snap0",
//                "/1.0/instances/foo/snapshots/snap1"
//              ]
//    "403":
//      $ref: "#/responses/Forbidden"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/instances/{name}/snapshots?recursion=1 instances instance_snapshots_get_recursion1
//
//	Get the snapshots
//
//	Returns a list of instance snapshots (structs).
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
//	          description: List of instance snapshots
//	          items:
//	            $ref: "#/definitions/InstanceSnapshot"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceSnapshotsGet(d *Daemon, r *http.Request) response.Response {
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
	resultString := []string{}
	resultMap := []*api.InstanceSnapshot{}

	if !recursion {
		snaps, err := s.DB.Cluster.GetInstanceSnapshotsNames(projectName, cname)
		if err != nil {
			return response.SmartError(err)
		}

		for _, snap := range snaps {
			_, snapName, _ := api.GetParentAndSnapshotName(snap)
			if projectName == api.ProjectDefaultName {
				url := fmt.Sprintf("/%s/instances/%s/snapshots/%s", version.APIVersion, cname, snapName)
				resultString = append(resultString, url)
			} else {
				url := fmt.Sprintf("/%s/instances/%s/snapshots/%s?project=%s", version.APIVersion, cname, snapName, projectName)
				resultString = append(resultString, url)
			}
		}
	} else {
		c, err := instance.LoadByProjectAndName(s, projectName, cname)
		if err != nil {
			return response.SmartError(err)
		}

		snaps, err := c.Snapshots()
		if err != nil {
			return response.SmartError(err)
		}

		for _, snap := range snaps {
			render, _, err := snap.Render(storagePools.RenderSnapshotUsage(s, snap))
			if err != nil {
				continue
			}

			resultMap = append(resultMap, render.(*api.InstanceSnapshot))
		}
	}

	if !recursion {
		return response.SyncResponse(true, resultString)
	}

	return response.SyncResponse(true, resultMap)
}

// swagger:operation POST /1.0/instances/{name}/snapshots instances instance_snapshots_post
//
//	Create a snapshot
//
//	Creates a new snapshot.
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
//	    name: snapshot
//	    description: Snapshot request
//	    required: false
//	    schema:
//	      $ref: "#/definitions/InstanceSnapshotsPost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceSnapshotsPost(d *Daemon, r *http.Request) response.Response {
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

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbProject, err := cluster.GetProject(context.Background(), tx.Tx(), projectName)
		if err != nil {
			return err
		}

		p, err := dbProject.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		err = project.AllowSnapshotCreation(p)
		if err != nil {
			return err
		}

		return nil
	})
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

	/*
	 * snapshot is a three step operation:
	 * 1. choose a new name
	 * 2. copy the database info over
	 * 3. copy over the rootfs
	 */
	inst, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	req := api.InstanceSnapshotsPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Name == "" {
		req.Name, err = instance.NextSnapshotName(s, inst, "snap%d")
		if err != nil {
			return response.SmartError(err)
		}
	}

	// Validate the name
	err = validate.IsURLSegmentSafe(req.Name)
	if err != nil {
		return response.BadRequest(fmt.Errorf("Invalid snapshot name: %w", err))
	}

	var expiry time.Time
	if req.ExpiresAt != nil {
		expiry = *req.ExpiresAt
	} else {
		expiry, err = internalInstance.GetExpiry(time.Now(), inst.ExpandedConfig()["snapshots.expiry"])
		if err != nil {
			return response.BadRequest(err)
		}
	}

	snapshot := func(op *operations.Operation) error {
		inst.SetOperation(op)
		return inst.Snapshot(req.Name, expiry, req.Stateful)
	}

	resources := map[string][]api.URL{}
	resources["instances"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", name)}
	resources["instances_snapshots"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", name, "snapshots", req.Name)}

	if inst.Type() == instancetype.Container {
		resources["containers"] = resources["instances"]
	}

	op, err := operations.OperationCreate(s, projectName, operations.OperationClassTask, operationtype.SnapshotCreate, resources, nil, snapshot, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func instanceSnapshotHandler(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := request.ProjectParam(r)
	instName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	snapshotName, err := url.PathUnescape(mux.Vars(r)["snapshotName"])
	if err != nil {
		return response.SmartError(err)
	}

	resp, err := forwardedResponseIfInstanceIsRemote(s, r, projectName, instName, instanceType)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	snapshotName, err = url.QueryUnescape(snapshotName)
	if err != nil {
		return response.SmartError(err)
	}

	snapInst, err := instance.LoadByProjectAndName(s, projectName, instName+internalInstance.SnapshotDelimiter+snapshotName)
	if err != nil {
		return response.SmartError(err)
	}

	switch r.Method {
	case "GET":
		return snapshotGet(s, snapInst)
	case "POST":
		return snapshotPost(s, r, snapInst)
	case "DELETE":
		return snapshotDelete(s, r, snapInst)
	case "PUT":
		return snapshotPut(s, r, snapInst)
	case "PATCH":
		return snapshotPatch(s, r, snapInst)
	default:
		return response.NotFound(fmt.Errorf("Method %q not found", r.Method))
	}
}

// swagger:operation PATCH /1.0/instances/{name}/snapshots/{snapshot} instances instance_snapshot_patch
//
//	Partially update snapshot
//
//	Updates a subset of the snapshot config.
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
//	    name: snapshot
//	    description: Snapshot update
//	    required: false
//	    schema:
//	      $ref: "#/definitions/InstanceSnapshotPut"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func snapshotPatch(s *state.State, r *http.Request, snapInst instance.Instance) response.Response {
	// Only expires_at is currently editable, so PATCH is equivalent to PUT.
	return snapshotPut(s, r, snapInst)
}

// swagger:operation PUT /1.0/instances/{name}/snapshots/{snapshot} instances instance_snapshot_put
//
//	Update snapshot
//
//	Updates the snapshot config.
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
//	    name: snapshot
//	    description: Snapshot update
//	    required: false
//	    schema:
//	      $ref: "#/definitions/InstanceSnapshotPut"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func snapshotPut(s *state.State, r *http.Request, snapInst instance.Instance) response.Response {
	// Validate the ETag
	etag := []any{snapInst.ExpiryDate()}
	err := localUtil.EtagCheck(r, etag)
	if err != nil {
		return response.PreconditionFailed(err)
	}

	rj := jmap.Map{}

	err = json.NewDecoder(r.Body).Decode(&rj)
	if err != nil {
		return response.InternalError(err)
	}

	var do func(op *operations.Operation) error

	_, err = rj.GetString("expires_at")
	if err != nil {
		// Skip updating the snapshot since the requested key wasn't provided
		do = func(op *operations.Operation) error {
			return nil
		}
	} else {
		body, err := json.Marshal(rj)
		if err != nil {
			return response.InternalError(err)
		}

		configRaw := api.InstanceSnapshotPut{}

		err = json.Unmarshal(body, &configRaw)
		if err != nil {
			return response.BadRequest(err)
		}

		// Update instance configuration
		do = func(op *operations.Operation) error {
			args := db.InstanceArgs{
				Architecture: snapInst.Architecture(),
				Config:       snapInst.LocalConfig(),
				Description:  snapInst.Description(),
				Devices:      snapInst.LocalDevices(),
				Ephemeral:    snapInst.IsEphemeral(),
				Profiles:     snapInst.Profiles(),
				Project:      snapInst.Project().Name,
				ExpiryDate:   configRaw.ExpiresAt,
				Type:         snapInst.Type(),
				Snapshot:     snapInst.IsSnapshot(),
			}

			err = snapInst.Update(args, false)
			if err != nil {
				return err
			}

			return nil
		}
	}

	opType := operationtype.SnapshotUpdate
	parentName, snapName, _ := api.GetParentAndSnapshotName(snapInst.Name())

	resources := map[string][]api.URL{}
	resources["instances"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", parentName)}
	resources["instances_snapshots"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", parentName, "snapshots", snapName)}

	if snapInst.Type() == instancetype.Container {
		resources["containers"] = resources["instances"]
	}

	op, err := operations.OperationCreate(s, snapInst.Project().Name, operations.OperationClassTask, opType, resources, nil, do, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation GET /1.0/instances/{name}/snapshots/{snapshot} instances instance_snapshot_get
//
//	Get the snapshot
//
//	Gets a specific instance snapshot.
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
//	    description: Instance snapshot
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
//	          $ref: "#/definitions/InstanceSnapshot"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func snapshotGet(s *state.State, snapInst instance.Instance) response.Response {
	render, _, err := snapInst.Render(storagePools.RenderSnapshotUsage(s, snapInst))
	if err != nil {
		return response.SmartError(err)
	}

	etag := []any{snapInst.ExpiryDate()}
	return response.SyncResponseETag(true, render.(*api.InstanceSnapshot), etag)
}

// swagger:operation POST /1.0/instances/{name}/snapshots/{snapshot} instances instance_snapshot_post
//
//	Rename or move/migrate a snapshot
//
//	Renames or migrates an instance snapshot to another server.
//
//	The returned operation metadata will vary based on what's requested.
//	For rename or move within the same server, this is a simple background operation with progress data.
//	For migration, in the push case, this will similarly be a background
//	operation with progress data, for the pull case, it will be a websocket
//	operation with a number of secrets to be passed to the target server.
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
//	    name: snapshot
//	    description: Snapshot migration
//	    required: false
//	    schema:
//	      $ref: "#/definitions/InstanceSnapshotPost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func snapshotPost(s *state.State, r *http.Request, snapInst instance.Instance) response.Response {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return response.InternalError(err)
	}

	rdr1 := io.NopCloser(bytes.NewBuffer(body))

	raw := jmap.Map{}
	err = json.NewDecoder(rdr1).Decode(&raw)
	if err != nil {
		return response.BadRequest(err)
	}

	parentName, snapName, _ := api.GetParentAndSnapshotName(snapInst.Name())

	migration, err := raw.GetBool("migration")
	if err == nil && migration {
		rdr2 := io.NopCloser(bytes.NewBuffer(body))
		rdr3 := io.NopCloser(bytes.NewBuffer(body))

		req := api.InstancePost{}
		err = json.NewDecoder(rdr2).Decode(&req)
		if err != nil {
			return response.BadRequest(err)
		}

		reqNew := api.InstanceSnapshotPost{}
		err = json.NewDecoder(rdr3).Decode(&reqNew)
		if err != nil {
			return response.BadRequest(err)
		}

		if reqNew.Name == "" {
			return response.BadRequest(fmt.Errorf("A new name for the instance must be provided"))
		}

		if reqNew.Live {
			if parentName != reqNew.Name {
				return response.BadRequest(fmt.Errorf("Instance name cannot be changed during stateful copy (%q to %q)", parentName, reqNew.Name))
			}
		}

		ws, err := newMigrationSource(snapInst, reqNew.Live, true, false, "", req.Target)
		if err != nil {
			return response.SmartError(err)
		}

		resources := map[string][]api.URL{}
		resources["instances"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", parentName)}
		resources["instances_snapshots"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", parentName, "snapshots", snapName)}

		if snapInst.Type() == instancetype.Container {
			resources["containers"] = resources["instances"]
		}

		run := func(op *operations.Operation) error {
			return ws.Do(s, op)
		}

		if req.Target != nil {
			// Push mode.
			op, err := operations.OperationCreate(s, snapInst.Project().Name, operations.OperationClassTask, operationtype.SnapshotTransfer, resources, nil, run, nil, nil, r)
			if err != nil {
				return response.InternalError(err)
			}

			return operations.OperationResponse(op)
		}

		// Pull mode.
		op, err := operations.OperationCreate(s, snapInst.Project().Name, operations.OperationClassWebsocket, operationtype.SnapshotTransfer, resources, ws.Metadata(), run, nil, ws.Connect, r)
		if err != nil {
			return response.InternalError(err)
		}

		return operations.OperationResponse(op)
	}

	newName, err := raw.GetString("name")
	if err != nil {
		return response.BadRequest(err)
	}

	// Validate the name
	err = validate.IsURLSegmentSafe(newName)
	if err != nil {
		return response.BadRequest(fmt.Errorf("Invalid snapshot name: %w", err))
	}

	fullName := parentName + internalInstance.SnapshotDelimiter + newName

	// Check that the name isn't already in use
	id, _ := s.DB.Cluster.GetInstanceSnapshotID(snapInst.Project().Name, parentName, newName)
	if id > 0 {
		return response.Conflict(fmt.Errorf("Name '%s' already in use", fullName))
	}

	rename := func(op *operations.Operation) error {
		return snapInst.Rename(fullName, false)
	}

	resources := map[string][]api.URL{}
	resources["instances"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", parentName)}
	resources["instances_snapshots"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", parentName, "snapshots", snapName)}

	if snapInst.Type() == instancetype.Container {
		resources["containers"] = resources["instances"]
	}

	op, err := operations.OperationCreate(s, snapInst.Project().Name, operations.OperationClassTask, operationtype.SnapshotRename, resources, nil, rename, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// swagger:operation DELETE /1.0/instances/{name}/snapshots/{snapshot} instances instance_snapshot_delete
//
//	Delete a snapshot
//
//	Deletes the instance snapshot.
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
func snapshotDelete(s *state.State, r *http.Request, snapInst instance.Instance) response.Response {
	remove := func(op *operations.Operation) error {
		return snapInst.Delete(false)
	}

	parentName, snapName, _ := api.GetParentAndSnapshotName(snapInst.Name())

	resources := map[string][]api.URL{}
	resources["instances"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", parentName)}
	resources["instances_snapshots"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", parentName, "snapshots", snapName)}

	if snapInst.Type() == instancetype.Container {
		resources["containers"] = resources["instances"]
	}

	op, err := operations.OperationCreate(s, snapInst.Project().Name, operations.OperationClassTask, operationtype.SnapshotDelete, resources, nil, remove, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}
