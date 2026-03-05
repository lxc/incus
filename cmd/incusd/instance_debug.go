package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"

	"github.com/gorilla/mux"

	internalInstance "github.com/lxc/incus/v6/internal/instance"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/operations"
	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/internal/server/request"
	"github.com/lxc/incus/v6/internal/server/response"
	"github.com/lxc/incus/v6/internal/server/state"
	storagePools "github.com/lxc/incus/v6/internal/server/storage"
	storageDrivers "github.com/lxc/incus/v6/internal/server/storage/drivers"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/subprocess"
)

// swagger:operation GET /1.0/instances/{name}/debug/memory instances instance_debug_memory_get
//
//	Get memory debug information of an instance
//
//	Returns memory debug information of a running instance.
//	Only supported for VMs.
//
//	---
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: query
//	    name: format
//	    description: Memory dump format
//	    type: string
//	    example: elf
//	responses:
//	  "200":
//	    description: Success
//	    content:
//	      application/octet-stream:
//	        schema:
//	          type: string
//	          example: raw memory dump
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceDebugMemoryGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	format := request.QueryParam(r, "format")

	projectName := request.ProjectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if internalInstance.IsSnapshot(name) {
		return response.BadRequest(errors.New("Invalid instance name"))
	}

	// Handle requests targeted to a container on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(s, r, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	// Ensure instance exists.
	inst, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	if inst.Type() != instancetype.VM {
		return response.BadRequest(errors.New("Memory dumps are only supported for virtual machines"))
	}

	if !inst.IsRunning() {
		return response.BadRequest(errors.New("Instance must be running to dump memory"))
	}

	v, ok := inst.(instance.VM)
	if !ok {
		return response.InternalError(errors.New("Failed to cast inst to VM"))
	}

	// Wrap up the request.
	return response.ManualResponse(func(w http.ResponseWriter) error {
		// Start streaming back to the client.
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/octet-stream")

		// Setup a PIPE for the data.
		reader, writer, err := os.Pipe()
		if err != nil {
			return err
		}

		defer reader.Close()
		defer writer.Close()

		chCopy := make(chan error)

		go func() {
			_, err := io.Copy(w, reader)
			chCopy <- err
		}()

		// Start dumping into the PIPE.
		err = v.DumpGuestMemory(writer, format)
		if err != nil {
			return err
		}

		err = <-chCopy
		if err != nil {
			return err
		}

		return nil
	})
}

// swagger:operation GET /1.0/instances/{name}/debug/repair instances instance_debug_repair_post
//
//	Trigger a repair action on the instance.
//
//	Runs an internal repair action on the instance.
//
//	---
//	parameters:
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	  - in: body
//	    name: state
//	    description: State
//	    required: false
//	    schema:
//	      $ref: "#/definitions/InstanceDebugRepairPost"
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
func instanceDebugRepairPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)
	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if internalInstance.IsSnapshot(name) {
		return response.BadRequest(errors.New("Invalid instance name"))
	}

	// Handle requests targeted to an instance on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(s, r, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	// Parse the request.
	req := api.InstanceDebugRepairPost{}

	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Validate the repair action.
	if !slices.Contains([]string{"rebuild-config-volume"}, req.Action) {
		return response.BadRequest(fmt.Errorf("Invalid repair action %q", req.Action))
	}

	// Load the instance.
	inst, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	// Running the action.
	switch req.Action {
	case "rebuild-config-volume":
		err := instanceDebugRepairRebuildConfigVolume(s, inst)
		if err != nil {
			return response.SmartError(err)
		}
	}

	return response.EmptySyncResponse
}

func instanceDebugRepairRebuildConfigVolume(s *state.State, inst instance.Instance) error {
	// Initial validation.
	if inst.Type() != instancetype.VM {
		return errors.New("Config volume rebuild is only possible on VMs")
	}

	if inst.IsRunning() {
		return errors.New("Config volume rebuild is only possible on stopped VMs")
	}

	// Load the storage pool.
	pool, err := storagePools.LoadByInstance(s, inst)
	if err != nil {
		return err
	}

	// Load the volume.
	dbVol, err := storagePools.VolumeDBGet(pool, inst.Project().Name, inst.Name(), storageDrivers.VolumeTypeVM)
	if err != nil {
		return err
	}

	if dbVol.Config["block.type"] != "qcow2" {
		return errors.New("Config volume rebuild is only possible on QCOW2 backed VMs")
	}

	volStorageName := project.Instance(inst.Project().Name, inst.Name())
	vol := pool.GetVolume(storageDrivers.VolumeTypeVM, storageDrivers.ContentTypeFS, volStorageName, dbVol.Config)

	// Re-create the filesystem.
	err = pool.Driver().ActivateTask(vol, func(devPath string, op *operations.Operation) error {
		_, err = subprocess.RunCommand("mkfs.btrfs", "-f", devPath)
		if err != nil {
			return err
		}

		return nil
	}, nil)
	if err != nil {
		return err
	}

	// Re-configure the sub-volumes.
	err = storageDrivers.Qcow2CreateConfig(vol, nil)
	if err != nil {
		return err
	}

	snaps, err := inst.Snapshots()
	if err != nil {
		return err
	}

	for _, snap := range snaps {
		snapVolStorageName := project.Instance(snap.Project().Name, snap.Name())
		snapVol := pool.GetVolume(storageDrivers.VolumeTypeVM, storageDrivers.ContentTypeFS, snapVolStorageName, dbVol.Config)

		err = storageDrivers.Qcow2CreateConfigSnapshot(vol, snapVol, nil)
		if err != nil {
			return err
		}
	}

	return nil
}
