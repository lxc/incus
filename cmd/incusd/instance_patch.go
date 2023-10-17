package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	internalInstance "github.com/lxc/incus/internal/instance"
	"github.com/lxc/incus/internal/jmap"
	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/db/cluster"
	deviceConfig "github.com/lxc/incus/internal/server/device/config"
	"github.com/lxc/incus/internal/server/instance"
	projecthelpers "github.com/lxc/incus/internal/server/project"
	"github.com/lxc/incus/internal/server/response"
	localUtil "github.com/lxc/incus/internal/server/util"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/osarch"
)

// swagger:operation PATCH /1.0/instances/{name} instances instance_patch
//
//	Partially update the instance
//
//	Updates a subset of the instance configuration
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
//	    name: instance
//	    description: Update request
//	    schema:
//	      $ref: "#/definitions/InstancePut"
//	responses:
//	  "200":
//	    $ref: "#/responses/EmptySyncResponse"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instancePatch(d *Daemon, r *http.Request) response.Response {
	// Don't mess with instance while in setup mode.
	<-d.waitReady.Done()

	s := d.State()

	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := projectParam(r)

	// Get the container
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

	unlock, err := instanceOperationLock(s.ShutdownCtx, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	defer unlock()

	c, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	// Validate the ETag
	etag := []any{c.Architecture(), c.LocalConfig(), c.LocalDevices(), c.IsEphemeral(), c.Profiles()}
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

	req := api.InstancePut{}
	err = json.NewDecoder(rdr2).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Restore != "" {
		return response.BadRequest(fmt.Errorf("Can't call PATCH in restore mode"))
	}

	// Check if architecture was passed
	var architecture int
	_, err = reqRaw.GetString("architecture")
	if err != nil {
		architecture = c.Architecture()
	} else {
		architecture, err = osarch.ArchitectureId(req.Architecture)
		if err != nil {
			architecture = 0
		}
	}

	// Check if ephemeral was passed
	_, err = reqRaw.GetBool("ephemeral")
	if err != nil {
		req.Ephemeral = c.IsEphemeral()
	}

	profileNames := make([]string, 0, len(c.Profiles()))
	for _, profile := range c.Profiles() {
		profileNames = append(profileNames, profile.Name)
	}

	// Check if profiles was passed
	if req.Profiles == nil {
		req.Profiles = profileNames
	}

	// Check if config was passed
	if req.Config == nil {
		req.Config = c.LocalConfig()
	} else {
		for k, v := range c.LocalConfig() {
			_, ok := req.Config[k]
			if !ok {
				req.Config[k] = v
			}
		}
	}

	// Check if devices was passed
	if req.Devices == nil {
		req.Devices = c.LocalDevices().CloneNative()
	} else {
		for k, v := range c.LocalDevices() {
			_, ok := req.Devices[k]
			if !ok {
				req.Devices[k] = v
			}
		}
	}

	// Check project limits.
	apiProfiles := make([]api.Profile, 0, len(req.Profiles))
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		profiles, err := cluster.GetProfilesIfEnabled(ctx, tx.Tx(), projectName, req.Profiles)
		if err != nil {
			return err
		}

		for _, profile := range profiles {
			apiProfile, err := profile.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			apiProfiles = append(apiProfiles, *apiProfile)
		}

		return projecthelpers.AllowInstanceUpdate(tx, projectName, name, req, c.LocalConfig())
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Update container configuration
	args := db.InstanceArgs{
		Architecture: architecture,
		Config:       req.Config,
		Description:  req.Description,
		Devices:      deviceConfig.NewDevices(req.Devices),
		Ephemeral:    req.Ephemeral,
		Profiles:     apiProfiles,
		Project:      projectName,
	}

	err = c.Update(args, true)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}
