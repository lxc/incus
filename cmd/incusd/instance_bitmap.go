package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	internalInstance "github.com/lxc/incus/v6/internal/instance"
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/request"
	"github.com/lxc/incus/v6/internal/server/response"
	"github.com/lxc/incus/v6/shared/api"
)

// swagger:operation POST /1.0/instances/{name}/bitmaps instances instance_bitmaps_post
//
//	Create a bitmap
//
//	Creates a new bitmap.
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
//	    name: bitmap
//	    description: Bitmap request
//	    required: false
//	    schema:
//	      $ref: "#/definitions/StorageVolumeBitmapsPost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceBitmapsPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	projectName := request.ProjectParam(r)
	cname, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	if internalInstance.IsSnapshot(cname) {
		return response.BadRequest(errors.New("Invalid instance name"))
	}

	// Handle requests targeted to an instance on a different node
	resp, err := forwardedResponseIfInstanceIsRemote(s, r, projectName, cname)
	if err != nil {
		return response.SmartError(err)
	}

	if resp != nil {
		return resp
	}

	inst, err := instance.LoadByProjectAndName(s, projectName, cname)
	if err != nil {
		return response.SmartError(err)
	}

	if !inst.IsRunning() {
		return response.BadRequest(fmt.Errorf("Creating bitmaps requires the instance to be running"))
	}

	if inst.Type() != instancetype.VM {
		return response.BadRequest(fmt.Errorf("Only VMs are supported."))
	}

	req := api.StorageVolumeBitmapsPost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	rootDiskName, _, err := internalInstance.GetRootDiskDevice(inst.ExpandedDevices().CloneNative())
	if err != nil {
		return response.BadRequest(fmt.Errorf("Failed getting instance root disk: %w", err))
	}

	devNames := []string{rootDiskName}
	err = inst.ForEachDependentDiskType(func(dev deviceConfig.DeviceNamed) error {
		devNames = append(devNames, dev.Name)

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	err = inst.CreateBitmap(devNames, req)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}
