package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	internalInstance "github.com/lxc/incus/internal/instance"
	"github.com/lxc/incus/internal/server/auth"
	"github.com/lxc/incus/internal/server/cluster"
	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/instance/instancetype"
	"github.com/lxc/incus/internal/server/operations"
	"github.com/lxc/incus/internal/server/request"
	"github.com/lxc/incus/internal/server/response"
	"github.com/lxc/incus/internal/version"
	"github.com/lxc/incus/shared/api"
)

func coalesceErrors(local bool, errors map[string]error) error {
	if len(errors) == 0 {
		return nil
	}

	var errorMsg string
	if local {
		errorMsg += "The following instances failed to update state:\n"
	}

	for instName, err := range errors {
		if local {
			errorMsg += fmt.Sprintf(" - Instance: %s: %v\n", instName, err)
		} else {
			errorMsg += strings.TrimSpace(fmt.Sprintf("%v\n", err))
		}
	}

	return fmt.Errorf("%s", errorMsg)
}

// swagger:operation PUT /1.0/instances instances instances_put
//
//	Bulk instance state update
//
//	Changes the running state of all instances.
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
//	    name: state
//	    description: State
//	    required: false
//	    schema:
//	      $ref: "#/definitions/InstancesPut"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instancesPut(d *Daemon, r *http.Request) response.Response {
	projectName := request.ProjectParam(r)

	// Don't mess with instances while in setup mode.
	<-d.waitReady.Done()

	s := d.State()

	c, err := instance.LoadNodeAll(s, instancetype.Any)
	if err != nil {
		return response.BadRequest(err)
	}

	req := api.InstancesPut{}
	req.State = &api.InstanceStatePut{}
	req.State.Timeout = -1
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	action := internalInstance.InstanceAction(req.State.Action)

	userHasPermission, err := s.Authorizer.GetPermissionChecker(r.Context(), r, auth.EntitlementCanUpdateState, auth.ObjectTypeInstance)
	if err != nil {
		return response.SmartError(err)
	}

	var names []string
	var instances []instance.Instance
	for _, inst := range c {
		if inst.Project().Name != projectName {
			continue
		}

		// Only allow changing the state of instances the user has permission for.
		if !userHasPermission(auth.ObjectInstance(inst.Project().Name, inst.Name())) {
			continue
		}

		switch action {
		case internalInstance.Freeze:
			if !inst.IsRunning() {
				continue
			}

		case internalInstance.Restart:
			if !inst.IsRunning() {
				continue
			}

		case internalInstance.Start:
			if inst.IsRunning() {
				continue
			}

		case internalInstance.Stop:
			if !inst.IsRunning() {
				continue
			}

		case internalInstance.Unfreeze:
			if inst.IsRunning() {
				continue
			}
		}

		instances = append(instances, inst)
		names = append(names, inst.Name())
	}

	// Determine operation type.
	opType, err := instanceActionToOptype(req.State.Action)
	if err != nil {
		return response.BadRequest(err)
	}

	// Batch the changes.
	do := func(op *operations.Operation) error {
		localAction := func(local bool) error {
			failures := map[string]error{}
			failuresLock := sync.Mutex{}
			wgAction := sync.WaitGroup{}

			for _, inst := range instances {
				wgAction.Add(1)
				go func(inst instance.Instance) {
					defer wgAction.Done()

					inst.SetOperation(op)
					err := doInstanceStatePut(inst, *req.State)
					if err != nil {
						failuresLock.Lock()
						failures[inst.Name()] = err
						failuresLock.Unlock()
					}
				}(inst)
			}

			wgAction.Wait()
			return coalesceErrors(local, failures)
		}

		// Only return the local data if asked by cluster member.
		if isClusterNotification(r) {
			return localAction(false)
		}

		// If not clustered, return the local data.
		if !s.ServerClustered {
			return localAction(true)
		}

		// Get all members in cluster.
		var members []db.NodeInfo
		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			var err error

			members, err = tx.GetNodes(ctx)
			if err != nil {
				return fmt.Errorf("Failed getting cluster members: %w", err)
			}

			return nil
		})
		if err != nil {
			return err
		}

		// Get local cluster address.
		localClusterAddress := s.LocalConfig.ClusterAddress()

		// Record the results.
		failures := map[string]error{}
		failuresLock := sync.Mutex{}
		wgAction := sync.WaitGroup{}

		networkCert := s.Endpoints.NetworkCert()
		for _, member := range members {
			wgAction.Add(1)
			go func(member db.NodeInfo) {
				defer wgAction.Done()

				// Special handling for the local member.
				if member.Address == localClusterAddress {
					err := localAction(false)
					if err != nil {
						failuresLock.Lock()
						failures[member.Name] = err
						failuresLock.Unlock()
					}

					return
				}

				// Connect to the remote server.
				client, err := cluster.Connect(member.Address, networkCert, s.ServerCert(), r, true)
				if err != nil {
					failuresLock.Lock()
					failures[member.Name] = err
					failuresLock.Unlock()
					return
				}

				client = client.UseProject(projectName)

				// Perform the action.
				op, err := client.UpdateInstances(req, "")
				if err != nil {
					failuresLock.Lock()
					failures[member.Name] = err
					failuresLock.Unlock()
					return
				}

				err = op.Wait()
				if err != nil {
					failuresLock.Lock()
					failures[member.Name] = err
					failuresLock.Unlock()
					return
				}
			}(member)
		}

		wgAction.Wait()
		return coalesceErrors(true, failures)
	}

	resources := map[string][]api.URL{}
	for _, instName := range names {
		resources["instances"] = append(resources["instances"], *api.NewURL().Path(version.APIVersion, "instances", instName))
	}

	op, err := operations.OperationCreate(s, projectName, operations.OperationClassTask, opType, resources, nil, do, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}
