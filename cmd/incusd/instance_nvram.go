package main

import (
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"time"

	internalInstance "github.com/lxc/incus/v7/internal/instance"
	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/request"
	"github.com/lxc/incus/v7/internal/server/response"
	localUtil "github.com/lxc/incus/v7/internal/server/util"
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
//      x-example: default
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
//            example:
//              - /1.0/instances/foo/nvram/8be4df61-93ca-11d2-aa0d-00e098032b8c
//              - /1.0/instances/foo/nvram/d9bee56e-75dc-49d9-b4d7-b534210f637a
//    "400":
//      $ref: "#/responses/BadRequest"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "404":
//      $ref: "#/responses/NotFound"
//    "409":
//      $ref: "#/responses/Conflict"
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
//      x-example: default
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
//            example:
//              8be4df61-93ca-11d2-aa0d-00e098032b8c:
//                - /1.0/instances/foo/nvram/8be4df61-93ca-11d2-aa0d-00e098032b8c/Boot0000
//                - /1.0/instances/foo/nvram/8be4df61-93ca-11d2-aa0d-00e098032b8c/BootOrder
//    "400":
//      $ref: "#/responses/BadRequest"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "404":
//      $ref: "#/responses/NotFound"
//    "409":
//      $ref: "#/responses/Conflict"
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
//	    x-example: default
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
//	  "409":
//	    $ref: "#/responses/Conflict"
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
//      x-example: default
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
//            example:
//              - /1.0/instances/foo/nvram/8be4df61-93ca-11d2-aa0d-00e098032b8c/Boot0000
//              - /1.0/instances/foo/nvram/8be4df61-93ca-11d2-aa0d-00e098032b8c/BootOrder
//    "400":
//      $ref: "#/responses/BadRequest"
//    "403":
//      $ref: "#/responses/Forbidden"
//    "404":
//      $ref: "#/responses/NotFound"
//    "409":
//      $ref: "#/responses/Conflict"
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
//	    x-example: default
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
//	  "409":
//	    $ref: "#/responses/Conflict"
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
//	    x-example: 8be4df61-93ca-11d2-aa0d-00e098032b8c
//	  - in: path
//	    name: var
//	    description: Variable name
//	    type: string
//	    required: true
//	    x-example: BootOrder
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    x-example: default
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
//	  "409":
//	    $ref: "#/responses/Conflict"
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

	v, ok := store.Get(guid, varName)
	if !ok {
		return response.SmartError(api.StatusErrorf(http.StatusNotFound, "Variable not found"))
	}

	if r.Header.Get("Accept") == "application/octet-stream" {
		attributes := map[string]string{"X-Incus-attributes": strconv.FormatUint(uint64(uefi.DumpAttributes(v.Attributes)), 10)}
		if v.Timestamp != nil {
			attributes["X-Incus-timestamp"] = strconv.FormatInt(v.Timestamp.Unix(), 10)
		}

		return response.DevIncusResponseHeaders(http.StatusOK, string(v.Binary), "raw", false, attributes)
	}

	etag := []any{
		v.Binary,
		v.Attributes,
		v.Timestamp,
	}

	_ = uefi.Dissect(v, guid, varName)
	return response.SyncResponseETag(true, v, etag)
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
//	    x-example: 8be4df61-93ca-11d2-aa0d-00e098032b8c
//	  - in: path
//	    name: var
//	    description: Variable name
//	    type: string
//	    required: true
//	    x-example: BootOrder
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    x-example: default
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "409":
//	    $ref: "#/responses/Conflict"
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
		return response.BadRequest(errors.New("UEFI variables cannot be deleted on running VMs"))
	}

	if !store.Unset(guid, varName) {
		return response.SmartError(api.StatusErrorf(http.StatusNotFound, "Variable not found"))
	}

	err = inst.SetNVRAM(store)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// swagger:operation PUT /1.0/instances/{name}/nvram/{guid}/{var} instances instance_nvram_guid_var_put
//
//	Update the NVRAM variable
//
//	If the `Content-Type` header is set to `application/octet-stream`, this sets the raw binary
//	value of the variable.
//
//	Only supported for VMs.
//
//	---
//	consumes:
//	  - application/json
//	  - application/octet-stream
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
//	    x-example: 8be4df61-93ca-11d2-aa0d-00e098032b8c
//	  - in: path
//	    name: var
//	    description: Variable name
//	    type: string
//	    required: true
//	    x-example: BootOrder
//	  - in: query
//	    name: project
//	    description: Project name
//	    type: string
//	    x-example: default
//	  - in: header
//	    name: X-Incus-attributes
//	    description: Raw UEFI variable attributes to set
//	    type: integer
//	  - in: header
//	    name: X-Incus-timestamp
//	    description: Raw UEFI variable UNIX timestamp (in seconds) to set
//	    type: integer
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "201":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "409":
//	    $ref: "#/responses/Conflict"
//	  "412":
//	    $ref: "#/responses/PreconditionFailed"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceNVRAMGUIDVarPut(d *Daemon, r *http.Request) response.Response {
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
		return response.BadRequest(errors.New("UEFI variables cannot be modified on running VMs"))
	}

	oldV, updated := store.Get(guid, varName)
	if updated {
		// Validate ETag
		etag := []any{
			oldV.Binary,
			oldV.Attributes,
			oldV.Timestamp,
		}

		err = localUtil.EtagCheck(r, etag)
		if err != nil {
			return response.PreconditionFailed(err)
		}
	}

	var v api.InstanceNVRAMVariable
	if r.Header.Get("Content-Type") == "application/octet-stream" {
		v.Attributes = []string{"NON_VOLATILE", "BOOTSERVICE_ACCESS", "RUNTIME_ACCESS"}
		rawAttributesStr := r.Header.Get("X-Incus-attributes")
		if rawAttributesStr != "" {
			rawAttributes, err := strconv.ParseUint(rawAttributesStr, 0, 32)
			if err != nil {
				return response.BadRequest(errors.New("X-Incus-attributes header must be an integer"))
			}

			v.Attributes = uefi.ParseAttributes(uint32(rawAttributes))
		}

		rawTimestampStr := r.Header.Get("X-Incus-timestamp")
		if rawTimestampStr != "" {
			if !slices.Contains(v.Attributes, "TIME_BASED_AUTHENTICATED_WRITE_ACCESS") {
				return response.BadRequest(errors.New("X-Incus-timestamp header can only be set on variables with TIME_BASED_AUTHENTICATED_WRITE_ACCESS"))
			}

			rawTimestamp, err := strconv.ParseInt(rawTimestampStr, 10, 64)
			if err != nil {
				return response.BadRequest(errors.New("X-Incus-timestamp header must be an integer"))
			}

			timestamp := time.Unix(rawTimestamp, 0).UTC()
			v.Timestamp = &timestamp
		}

		v.Binary, err = io.ReadAll(r.Body)
		if err != nil {
			return response.SmartError(err)
		}
	} else {
		err := json.NewDecoder(r.Body).Decode(&v.InstanceNVRAMVariablePut)
		if err != nil {
			return response.BadRequest(err)
		}
	}

	if !slices.Contains(v.Attributes, "NON_VOLATILE") {
		return response.BadRequest(errors.New("Volatile UEFI variables cannot be stored in the NVRAM"))
	}

	err = store.Set(guid, varName, v)
	if err != nil {
		return response.SmartError(err)
	}

	err = inst.SetNVRAM(store)
	if err != nil {
		return response.SmartError(err)
	}

	if updated {
		return response.EmptySyncResponse
	}

	return response.SyncResponseLocation(true, nil, r.URL.String())
}

// swagger:operation PATCH /1.0/instances/{name}/nvram instances instance_nvram_patch
//
//	Bulk modify NVRAM variables.
//
//	This consumes nested objects keyed on GUID, then variable name, deleting the corresponding
//	UEFI variables if the objects are `null`, and updating them otherwise.
//
//	Only supported for VMs.
//
//	---
//	consumes:
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
//	    x-example: default
//	  - in: body
//	    name: UEFI variables map
//	    description: Load Balancer
//	    required: true
//	    schema:
//	       type: object
//	       description: UEFI variables
//	       additionalProperties:
//	         type: object
//	         description: Namespaced UEFI variables
//	         additionalProperties:
//	           $ref: "#/definitions/InstanceNVRAMVariablePut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "404":
//	    $ref: "#/responses/NotFound"
//	  "409":
//	    $ref: "#/responses/Conflict"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instanceNVRAMPatch(d *Daemon, r *http.Request) response.Response {
	projectName := request.ProjectParam(r)
	name, err := pathVar(r, "name")
	if err != nil {
		return response.SmartError(err)
	}

	store, inst, resp := getNVRAMStore(d, r, projectName, name)
	if resp != nil {
		return resp
	}

	if inst.IsRunning() {
		return response.BadRequest(errors.New("UEFI variables cannot be modified on running VMs"))
	}

	var patched map[string]map[string]*api.InstanceNVRAMVariablePut
	err = json.NewDecoder(r.Body).Decode(&patched)
	if err != nil {
		return response.BadRequest(err)
	}

	for guid, vars := range patched {
		for varName, v := range vars {
			if v == nil {
				store.Unset(guid, varName)
				continue
			}

			err = store.Set(guid, varName, api.InstanceNVRAMVariable{InstanceNVRAMVariablePut: *v})
			if err != nil {
				return response.SmartError(err)
			}
		}
	}

	err = inst.SetNVRAM(store)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}
