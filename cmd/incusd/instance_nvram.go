package main

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sort"
	"strconv"

	internalInstance "github.com/lxc/incus/v7/internal/instance"
	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/request"
	"github.com/lxc/incus/v7/internal/server/response"
	"github.com/lxc/incus/v7/internal/version"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/uefi"
)

func getNVRAMStore(d *Daemon, r *http.Request, projectName string, name string) (*uefi.Store, instance.VM, response.Response) {
	s := d.State()

	if internalInstance.IsSnapshot(name) {
		return nil, nil, response.BadRequest(errors.New("Invalid instance name"))
	}

	// Redirect to correct server if needed.
	resp, err := forwardedResponseIfInstanceIsRemote(s, r, projectName, name)
	if err != nil {
		return nil, nil, response.SmartError(err)
	}

	if resp != nil {
		return nil, nil, resp
	}

	// Load the instance.
	inst, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return nil, nil, response.SmartError(err)
	}

	if inst.Type() != instancetype.VM {
		return nil, nil, response.BadRequest(errors.New("NVRAM operations are only supported for virtual machines"))
	}

	v, ok := inst.(instance.VM)
	if !ok {
		return nil, nil, response.InternalError(errors.New("Failed to cast inst to VM"))
	}

	store, err := v.GetNVRAM()
	if err != nil {
		return nil, nil, response.SmartError(err)
	}

	return store, v, nil
}

// swagger:operation GET /1.0/instances/{name}/nvram instances instance_nvram_get
//
//  Get the NVRAM variable GUIDs
//
//  Returns a list of NVRAM variable GUIDs (URLs).
//
//  Only supported for VMs.
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: path
//      name: name
//      description: Instance name
//      type: string
//      required: true
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
//                "/1.0/instances/foo/nvram/8be4df61-93ca-11d2-aa0d-00e098032b8c",
//                "/1.0/instances/foo/nvram/d9bee56e-75dc-49d9-b4d7-b534210f637a",
//              ]
//    "400":
//      $ref: "#/responses/BadRequest"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "404":
//      $ref: "#/responses/NotFound"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/instances/{name}/nvram?recursion=1 instances instance_nvram_get_recursion1
//
//  Get the NVRAM variable GUIDs and names
//
//  Returns a map of NVRAM variable GUIDs and their associated names (URLs).
//
//  Only supported for VMs.
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: path
//      name: name
//      description: Instance name
//      type: string
//      required: true
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
//            type: object
//            description: UEFI variables
//            additionalProperties:
//              type: array
//              description: List of endpoints
//              items:
//                type: string
//            example: |-
//              {
//                "8be4df61-93ca-11d2-aa0d-00e098032b8c": [
//                  "/1.0/instances/foo/nvram/8be4df61-93ca-11d2-aa0d-00e098032b8c/Boot0000",
//                  "/1.0/instances/foo/nvram/8be4df61-93ca-11d2-aa0d-00e098032b8c/BootOrder"
//                ]
//              }
//    "400":
//      $ref: "#/responses/BadRequest"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "404":
//      $ref: "#/responses/NotFound"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/instances/{name}/nvram?recursion=2 instances instance_nvram_get_recursion2
//
//	Get the NVRAM variables
//
//	Returns a map of NVRAM variable GUIDs and their dissected values.
//
//	Only supported for VMs.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: path
//	    name: name
//	    description: Instance name
//	    type: string
//	    required: true
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	    description: NVRAM variables
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
//	          type: object
//	          description: UEFI variables
//	          additionalProperties:
//	            type: object
//	            description: Namespaced UEFI variables
//	            additionalProperties:
//	              $ref: "#/definitions/InstanceNVRAMVariable"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceNVRAMGet(d *Daemon, r *http.Request) response.Response {
	// Parse the recursion field.
	recursion, err := strconv.Atoi(r.FormValue("recursion"))
	if err != nil || recursion < 0 {
		recursion = 0
	}

	if recursion > 2 {
		recursion = 2
	}

	projectName := request.ProjectParam(r)
	name, err := pathVar(r, "name")
	if err != nil {
		return response.SmartError(err)
	}

	store, _, resp := getNVRAMStore(d, r, projectName, name)
	if resp != nil {
		return resp
	}

	var out any
	switch recursion {
	case 0:
		res := slices.Sorted(maps.Keys(store.Vars))
		for i, guid := range res {
			res[i] = api.NewURL().Path(version.APIVersion, "instances", name, "nvram", guid).Project(projectName).String()
		}

		out = res
	case 1:
		res := make(map[string][]string, len(store.Vars))
		for guid, vars := range store.Vars {
			names := make([]string, 0, len(vars))
			for varName := range vars {
				names = append(names, api.NewURL().Path(version.APIVersion, "instances", name, "nvram", guid, varName).Project(projectName).String())
			}

			sort.Strings(names)
			res[guid] = names
		}

		out = res
	case 2:
		for guid, vars := range store.Vars {
			for name, v := range vars {
				_ = uefi.Dissect(v, guid, name)
			}
		}

		out = store.Vars
	}

	return response.SyncResponse(true, out)
}

// swagger:operation GET /1.0/instances/{name}/nvram/{guid} instances instance_nvram_guid_get
//
//  Get the NVRAM variable names under the given GUID
//
//  Returns a map of variable names (URLs).
//
//  Only supported for VMs.
//
//  ---
//  produces:
//    - application/json
//  parameters:
//    - in: path
//      name: name
//      description: Instance name
//      type: string
//      required: true
//    - in: path
//      name: guid
//      description: GUID
//      type: string
//      required: true
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
//                "/1.0/instances/foo/nvram/8be4df61-93ca-11d2-aa0d-00e098032b8c/Boot0000",
//                "/1.0/instances/foo/nvram/8be4df61-93ca-11d2-aa0d-00e098032b8c/BootOrder"
//              ]
//    "400":
//      $ref: "#/responses/BadRequest"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "404":
//      $ref: "#/responses/NotFound"
//    "500":
//      $ref: "#/responses/InternalServerError"

// swagger:operation GET /1.0/instances/{name}/nvram/{guid}?recursion=1 instances instance_nvram_guid_get_recursion1
//
//	Get the NVRAM variables under the given GUID
//
//	Returns a map of NVRAM variable GUIDs and their dissected values.
//
//	Only supported for VMs.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: path
//	    name: name
//	    description: Instance name
//	    type: string
//	    required: true
//	  - in: path
//	    name: guid
//	    description: GUID
//	    type: string
//	    required: true
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	    description: NVRAM variables
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
//	          type: object
//	          description: Namespaced UEFI variables
//	          additionalProperties:
//	            $ref: "#/definitions/InstanceNVRAMVariable"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceNVRAMGUIDGet(d *Daemon, r *http.Request) response.Response {
	// Parse the recursion field.
	recursion, err := strconv.Atoi(r.FormValue("recursion"))
	if err != nil || recursion < 0 {
		recursion = 0
	}

	if recursion > 1 {
		recursion = 1
	}

	projectName := request.ProjectParam(r)
	name, err := pathVar(r, "name")
	if err != nil {
		return response.SmartError(err)
	}

	guid, err := pathVar(r, "guid")
	if err != nil {
		return response.SmartError(err)
	}

	store, _, resp := getNVRAMStore(d, r, projectName, name)
	if resp != nil {
		return resp
	}

	vars, ok := store.Vars[guid]
	if !ok {
		return response.SmartError(api.StatusErrorf(http.StatusNotFound, "GUID not found"))
	}

	var out any
	switch recursion {
	case 0:
		res := make([]string, 0, len(vars))
		for varName := range vars {
			res = append(res, api.NewURL().Path(version.APIVersion, "instances", name, "nvram", guid, varName).Project(projectName).String())
		}

		sort.Strings(res)
		out = res
	case 1:
		for name, v := range vars {
			_ = uefi.Dissect(v, guid, name)
		}

		out = vars
	}

	return response.SyncResponse(true, out)
}

// swagger:operation GET /1.0/instances/{name}/nvram/{guid}/{var} instances instance_nvram_guid_var_get
//
//	Get the NVRAM variable
//
//	If the `Accept` header is set to `application/octet-stream`, the raw binary value of the variable
//	is returned.
//
//	Only supported for VMs.
//
//	---
//	produces:
//	  - application/json
//	  - application/octet-stream
//	parameters:
//	  - in: path
//	    name: name
//	    description: Instance name
//	    type: string
//	    required: true
//	  - in: path
//	    name: guid
//	    description: Variable GUID
//	    type: string
//	    required: true
//	    example: 8be4df61-93ca-11d2-aa0d-00e098032b8c
//	  - in: path
//	    name: var
//	    description: Variable name
//	    type: string
//	    example: BootOrder
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    example: default
//	responses:
//	  "200":
//	    description: NVRAM variable
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
//	          $ref: "#/definitions/InstanceNVRAMVariable"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceNVRAMGUIDVarGet(d *Daemon, r *http.Request) response.Response {
	projectName := request.ProjectParam(r)
	name, err := pathVar(r, "name")
	if err != nil {
		return response.SmartError(err)
	}

	guid, err := pathVar(r, "guid")
	if err != nil {
		return response.SmartError(err)
	}

	varName, err := pathVar(r, "var")
	if err != nil {
		return response.SmartError(err)
	}

	store, _, resp := getNVRAMStore(d, r, projectName, name)
	if resp != nil {
		return resp
	}

	vars, ok := store.Vars[guid]
	if !ok {
		return response.SmartError(api.StatusErrorf(http.StatusNotFound, "GUID not found"))
	}

	v, ok := vars[varName]
	if !ok {
		return response.SmartError(api.StatusErrorf(http.StatusNotFound, "Variable not found"))
	}

	if r.Header.Get("Accept") == "application/octet-stream" {
		return response.DevIncusResponse(http.StatusOK, string(v.Binary), "raw", false)
	}

	_ = uefi.Dissect(v, guid, varName)
	return response.SyncResponse(true, v)
}

// swagger:operation DELETE /1.0/instances/{name}/nvram/{guid}/{var} instances instance_nvram_guid_var_delete
//
//	Delete the NVRAM variable
//
//	Only supported for VMs.
//
//	---
//	produces:
//	  - application/json
//	parameters:
//	  - in: path
//	    name: name
//	    description: Instance name
//	    type: string
//	    required: true
//	  - in: path
//	    name: guid
//	    description: Variable GUID
//	    type: string
//	    required: true
//	    example: 8be4df61-93ca-11d2-aa0d-00e098032b8c
//	  - in: path
//	    name: var
//	    description: Variable name
//	    type: string
//	    example: BootOrder
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
func instanceNVRAMGUIDVarDelete(d *Daemon, r *http.Request) response.Response {
	projectName := request.ProjectParam(r)
	name, err := pathVar(r, "name")
	if err != nil {
		return response.SmartError(err)
	}

	guid, err := pathVar(r, "guid")
	if err != nil {
		return response.SmartError(err)
	}

	varName, err := pathVar(r, "var")
	if err != nil {
		return response.SmartError(err)
	}

	store, inst, resp := getNVRAMStore(d, r, projectName, name)
	if resp != nil {
		return resp
	}

	if inst.IsRunning() {
		return response.BadRequest(fmt.Errorf("UEFI variables cannot be deleted on running VMs"))
	}

	vars, ok := store.Vars[guid]
	if !ok {
		return response.SmartError(api.StatusErrorf(http.StatusNotFound, "GUID not found"))
	}

	_, ok = vars[varName]
	if !ok {
		return response.SmartError(api.StatusErrorf(http.StatusNotFound, "Variable not found"))
	}

	delete(store.Vars[guid], varName)
	err = inst.SetNVRAM(store)
	if err != nil {
		response.SmartError(err)
	}

	return response.EmptySyncResponse
}
