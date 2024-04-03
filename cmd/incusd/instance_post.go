package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	"github.com/lxc/incus/client"
	internalInstance "github.com/lxc/incus/internal/instance"
	"github.com/lxc/incus/internal/server/auth"
	"github.com/lxc/incus/internal/server/cluster"
	"github.com/lxc/incus/internal/server/db"
	dbCluster "github.com/lxc/incus/internal/server/db/cluster"
	"github.com/lxc/incus/internal/server/db/operationtype"
	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/operations"
	"github.com/lxc/incus/internal/server/project"
	"github.com/lxc/incus/internal/server/request"
	"github.com/lxc/incus/internal/server/response"
	"github.com/lxc/incus/internal/server/scriptlet"
	"github.com/lxc/incus/internal/server/state"
	storagePools "github.com/lxc/incus/internal/server/storage"
	"github.com/lxc/incus/internal/version"
	"github.com/lxc/incus/shared/api"
	apiScriptlet "github.com/lxc/incus/shared/api/scriptlet"
	"github.com/lxc/incus/shared/logger"
)

// swagger:operation POST /1.0/instances/{name} instances instance_post
//
//	Rename or move/migrate an instance
//
//	Renames, moves an instance between pools or migrates an instance to another server.
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
//	    name: migration
//	    description: Migration request
//	    schema:
//	      $ref: "#/definitions/InstancePost"
//	responses:
//	  "202":
//	    $ref: "#/responses/Operation"
//	  "400":
//	    $ref: "#/responses/BadRequest"
//	  "403":
//	    $ref: "#/responses/Forbidden"
//	  "500":
//	    $ref: "#/responses/InternalServerError"
func instancePost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	// Don't mess with instance while in setup mode.
	<-d.waitReady.Done()

	// Parse the request URL.
	instanceType, err := urlInstanceTypeDetect(r)
	if err != nil {
		return response.SmartError(err)
	}

	projectName := request.ProjectParam(r)
	target := request.QueryParam(r, "target")

	name, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// Quick checks.
	if internalInstance.IsSnapshot(name) {
		return response.BadRequest(fmt.Errorf("Invalid instance name"))
	}

	if target != "" && !s.ServerClustered {
		return response.BadRequest(fmt.Errorf("Target only allowed when clustered"))
	}

	// Check if the server the instance is running on is currently online.
	var sourceMemberInfo *db.NodeInfo
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Load source node.
		sourceAddress, err := tx.GetNodeAddressOfInstance(ctx, projectName, name, instanceType)
		if err != nil {
			return fmt.Errorf("Failed to get address of instance's member: %w", err)
		}

		if sourceAddress == "" {
			// Local node.
			return nil
		}

		info, err := tx.GetNodeByAddress(ctx, sourceAddress)
		if err != nil {
			return fmt.Errorf("Failed to get source member for %q: %w", sourceAddress, err)
		}

		sourceMemberInfo = &info

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// More checks.
	if target == "" && sourceMemberInfo != nil && sourceMemberInfo.IsOffline(s.GlobalConfig.OfflineThreshold()) {
		return response.BadRequest(fmt.Errorf("Can't perform action as server is currently offline"))
	}

	// Handle request forwarding.
	if sourceMemberInfo != nil && sourceMemberInfo.IsOffline(s.GlobalConfig.OfflineThreshold()) {
		// Current location of the instance isn't available and we've been asked to relocate it, forward to target.
		resp := forwardedResponseIfTargetIsRemote(s, r)
		if resp != nil {
			return resp
		}
	} else if target == "" || sourceMemberInfo == nil || !sourceMemberInfo.IsOffline(s.GlobalConfig.OfflineThreshold()) {
		// Forward the request to the instance's current location (if not local).
		resp, err := forwardedResponseIfInstanceIsRemote(s, r, projectName, name, instanceType)
		if err != nil {
			return response.SmartError(err)
		}

		if resp != nil {
			return resp
		}
	}

	// Parse the request.
	req := api.InstancePost{}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Target instance properties.
	instProject := projectName
	instLocation := target

	// Clear instance name if it's the same.
	if req.Name != "" && req.Name == name {
		req.Name = ""
	}

	// Validate the new target project (if provided).
	if req.Project != "" {
		// Confirm access to target project.
		err := s.Authorizer.CheckPermission(r.Context(), r, auth.ObjectProject(req.Project), auth.EntitlementCanCreateInstances)
		if err != nil {
			return response.SmartError(err)
		}

		instProject = req.Project
	}

	// Validate the new instance name (if provided).
	if req.Name != "" {
		// Check the new instance name is valid.
		err = instance.ValidName(req.Name, false)
		if err != nil {
			return response.BadRequest(err)
		}

		// Check that the new isn't already in use.
		var id int
		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Check that the name isn't already in use.
			id, _ = tx.GetInstanceID(ctx, instProject, req.Name)

			return nil
		})
		if id > 0 {
			return response.Conflict(fmt.Errorf("Name %q already in use", req.Name))
		}
	}

	// Load the local instance.
	inst, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	// Handle simple instance renaming.
	if !req.Migration {
		run := func(*operations.Operation) error {
			return inst.Rename(req.Name, true)
		}

		resources := map[string][]api.URL{}
		resources["instances"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", name)}

		op, err := operations.OperationCreate(s, projectName, operations.OperationClassTask, operationtype.InstanceRename, resources, nil, run, nil, nil, r)
		if err != nil {
			return response.InternalError(err)
		}

		return operations.OperationResponse(op)
	}

	// Start handling migrations.
	if inst.IsSnapshot() {
		return response.BadRequest(fmt.Errorf("Instance snapshots cannot be moved on their own"))
	}

	// Checks for running instances.
	if inst.IsRunning() && (req.Pool != "" || req.Project != "" || target != "") {
		// Stateless migrations need the instance stopped.
		if !req.Live {
			return response.BadRequest(fmt.Errorf("Instance must be stopped to be moved statelessly"))
		}

		// Storage pool changes require a stopped instance.
		if req.Pool != "" {
			return response.BadRequest(fmt.Errorf("Instance must be stopped to be moved across storage pools"))
		}

		// Project changes require a stopped instance.
		if req.Project != "" {
			return response.BadRequest(fmt.Errorf("Instance must be stopped to be moved across projects"))
		}

		// Name changes require a stopped instance.
		if req.Name != "" {
			return response.BadRequest(fmt.Errorf("Instance must be stopped to change their names"))
		}
	} else {
		// Clear Live flag if instance isn't running.
		req.Live = false
	}

	// Check for offline sources.
	if sourceMemberInfo != nil && sourceMemberInfo.IsOffline(s.GlobalConfig.OfflineThreshold()) && (req.Pool != "" || req.Project != "" || req.Name != "") {
		return response.BadRequest(fmt.Errorf("Instance server is currently offline"))
	}

	// When in a cluster, default to keeping current location.
	if instLocation == "" && inst.Location() != "" {
		instLocation = inst.Location()
	}

	// If clustered, consider a new location for the instance.
	var targetMemberInfo *db.NodeInfo
	var targetCandidates []db.NodeInfo
	if s.ServerClustered && (target != "" || req.Project != "") {
		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			var targetGroupName string

			// Load the target project.
			p, err := dbCluster.GetProject(ctx, tx.Tx(), instProject)
			if err != nil {
				return err
			}

			targetProject, err := p.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			// Load the cluster members.
			allMembers, err := tx.GetNodes(ctx)
			if err != nil {
				return fmt.Errorf("Failed getting cluster members: %w", err)
			}

			// Check if the current location is fine.
			targetMemberInfo, _, err = project.CheckTarget(ctx, s.Authorizer, r, tx, targetProject, instLocation, allMembers)
			if err == nil && targetMemberInfo != nil {
				return nil
			}

			// If we must change location, validate access to requested member/group (if provided).
			targetMemberInfo, targetGroupName, err = project.CheckTarget(ctx, s.Authorizer, r, tx, targetProject, target, allMembers)
			if err != nil {
				return err
			}

			// If no specific server, get a list of allowed candidates.
			if targetMemberInfo == nil {
				clusterGroupsAllowed := project.GetRestrictedClusterGroups(targetProject)

				targetCandidates, err = tx.GetCandidateMembers(ctx, allMembers, []int{inst.Architecture()}, targetGroupName, clusterGroupsAllowed, s.GlobalConfig.OfflineThreshold())
				if err != nil {
					return err
				}
			}

			return nil
		})
		if err != nil {
			return response.SmartError(err)
		}

		// If no specific server and a placement scriplet exists, call it with the candidates.
		if targetMemberInfo == nil && s.GlobalConfig.InstancesPlacementScriptlet() != "" {
			leaderAddress, err := d.gateway.LeaderAddress()
			if err != nil {
				return response.InternalError(err)
			}

			req := apiScriptlet.InstancePlacement{
				InstancesPost: api.InstancesPost{
					Name: name,
					Type: api.InstanceType(instanceType.String()),
					InstancePut: api.InstancePut{
						Config:  inst.ExpandedConfig(),
						Devices: inst.ExpandedDevices().CloneNative(),
					},
				},
				Project: projectName,
				Reason:  apiScriptlet.InstancePlacementReasonRelocation,
			}

			targetMemberInfo, err = scriptlet.InstancePlacementRun(r.Context(), logger.Log, s, &req, targetCandidates, leaderAddress)
			if err != nil {
				return response.BadRequest(fmt.Errorf("Failed instance placement scriptlet: %w", err))
			}
		}

		// If no member was selected yet, pick the member with the least number of instances.
		if targetMemberInfo == nil {
			var filteredCandidateMembers []db.NodeInfo

			// The instance might already be placed on the node with least number of instances.
			// Therefore remove it from the list of possible candidates if existent.
			for _, candidateMember := range targetCandidates {
				if candidateMember.Name != inst.Location() {
					filteredCandidateMembers = append(filteredCandidateMembers, candidateMember)
				}
			}

			err := s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
				targetMemberInfo, err = tx.GetNodeWithLeastInstances(ctx, filteredCandidateMembers)
				return err
			})
			if err != nil {
				return response.SmartError(err)
			}
		}

		if targetMemberInfo.IsOffline(s.GlobalConfig.OfflineThreshold()) {
			return response.BadRequest(fmt.Errorf("Target cluster member is offline"))
		}
	}

	// Check that we're not requested to move to the same location we're currently on.
	if target != "" && targetMemberInfo.Name == inst.Location() {
		return response.BadRequest(fmt.Errorf("Requested target server is the same as current server"))
	}

	// If the instance needs to move, make sure it doesn't have backups.
	if targetMemberInfo != nil && targetMemberInfo.Name != inst.Location() {
		// Check if instance has backups.
		var backups []string
		err := s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			backups, err = tx.GetInstanceBackups(ctx, projectName, name)
			return err
		})
		if err != nil {
			err = fmt.Errorf("Failed to fetch instance's backups: %w", err)
			return response.SmartError(err)
		}

		if len(backups) > 0 {
			return response.BadRequest(fmt.Errorf("Instances with backups cannot be moved"))
		}
	}

	// Server-side instance migration.
	if req.Pool != "" || req.Project != "" || target != "" {
		// Clear targetMemberInfo if no target change required.
		if targetMemberInfo != nil && inst.Location() == targetMemberInfo.Name {
			targetMemberInfo = nil
		}

		// Setup the instance move operation.
		run := func(op *operations.Operation) error {
			return migrateInstance(context.TODO(), s, inst, req, sourceMemberInfo, targetMemberInfo, op)
		}

		resources := map[string][]api.URL{}
		resources["instances"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", name)}
		op, err := operations.OperationCreate(s, projectName, operations.OperationClassTask, operationtype.InstanceMigrate, resources, nil, run, nil, nil, r)
		if err != nil {
			return response.InternalError(err)
		}

		return operations.OperationResponse(op)
	}

	// Cross-server instance migration.
	ws, err := newMigrationSource(inst, req.Live, req.InstanceOnly, req.AllowInconsistent, "", req.Target)
	if err != nil {
		return response.InternalError(err)
	}

	resources := map[string][]api.URL{}
	resources["instances"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", name)}
	run := func(op *operations.Operation) error {
		return ws.Do(s, op)
	}

	cancel := func(op *operations.Operation) error {
		ws.disconnect()
		return nil
	}

	if req.Target != nil {
		// Push mode.
		op, err := operations.OperationCreate(s, projectName, operations.OperationClassTask, operationtype.InstanceMigrate, resources, nil, run, nil, nil, r)
		if err != nil {
			return response.InternalError(err)
		}

		return operations.OperationResponse(op)
	}

	// Pull mode.
	op, err := operations.OperationCreate(s, projectName, operations.OperationClassWebsocket, operationtype.InstanceMigrate, resources, ws.Metadata(), run, cancel, ws.Connect, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// Perform the server-side migration.
func migrateInstance(ctx context.Context, s *state.State, inst instance.Instance, req api.InstancePost, sourceMemberInfo *db.NodeInfo, targetMemberInfo *db.NodeInfo, op *operations.Operation) error {
	// Load the instance storage pool.
	sourcePool, err := storagePools.LoadByInstance(s, inst)
	if err != nil {
		return fmt.Errorf("Failed loading instance storage pool: %w", err)
	}

	// Get the DB volume type for the instance.
	volType, err := storagePools.InstanceTypeToVolumeType(inst.Type())
	if err != nil {
		return err
	}

	volDBType, err := storagePools.VolumeTypeToDBType(volType)
	if err != nil {
		return err
	}

	// Handle migration of an instance away from an offline server (on shared storage).
	if targetMemberInfo != nil && sourceMemberInfo != nil && sourceMemberInfo.IsOffline(s.GlobalConfig.OfflineThreshold()) && sourcePool.Driver().Info().Remote {
		// Update the database records.
		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			err := tx.UpdateInstanceNode(ctx, inst.Project().Name, inst.Name(), inst.Name(), targetMemberInfo.Name, sourcePool.ID(), volDBType)
			if err != nil {
				return fmt.Errorf("Failed updating cluster member to %q for instance %q: %w", targetMemberInfo.Name, inst.Name(), err)
			}

			return nil
		})
		if err != nil {
			return fmt.Errorf("Failed to relink instance database data: %w", err)
		}

		if err != nil {
			return fmt.Errorf("Failed creating mount point of instance on target node: %w", err)
		}

		// Import the instance into the storage.
		_, err = sourcePool.ImportInstance(inst, nil, nil)
		if err != nil {
			return fmt.Errorf("Failed creating mount point of instance on target node: %w", err)
		}

		// Perform any remaining instance rename.
		if req.Name != "" {
			err = inst.Rename(req.Name, true)
			if err != nil {
				return err
			}
		}

		return nil
	}

	// Save the original value of the "volatile.apply_template" config key,
	// since we'll want to preserve it in the copied container.
	instVolatileApplyTemplate := inst.LocalConfig()["volatile.apply_template"]

	// Get the current instance info.
	instInfoRaw, _, err := inst.Render()
	if err != nil {
		return fmt.Errorf("Failed getting source instance info: %w", err)
	}

	targetInstInfo, ok := instInfoRaw.(*api.Instance)
	if !ok {
		return fmt.Errorf("Unexpected result from source instance render: %w", err)
	}

	// Apply the config overrides.
	for k, v := range req.Config {
		targetInstInfo.Config[k] = v
	}

	// Apply the device overrides.
	for k, v := range req.Devices {
		targetInstInfo.Devices[k] = v
	}

	// Apply the profile overrides.
	if req.Profiles != nil {
		targetInstInfo.Profiles = req.Profiles
	}

	// Handle storage pool override.
	if req.Pool != "" {
		rootDevKey, rootDev, err := internalInstance.GetRootDiskDevice(inst.ExpandedDevices().CloneNative())
		if err != nil {
			return err
		}

		// Apply the override.
		rootDev["pool"] = req.Pool

		// Add the device to local config.
		targetInstInfo.Devices[rootDevKey] = rootDev
	}

	// Handle local changes (name, project, storage).

	// Handle the renames first.
	if req.Name != "" {
		err := inst.Rename(req.Name, true)
		if err != nil {
			return err
		}

		inst, err = instance.LoadByProjectAndName(s, inst.Project().Name, req.Name)
		if err != nil {
			return err
		}

		// Clear the rename part of the request.
		req.Name = ""
	}

	// Handle pool and project moves.
	if req.Project != "" || req.Pool != "" {
		// Get a local client.
		target, err := incus.ConnectIncusUnix(s.OS.GetUnixSocket(), nil)
		if err != nil {
			return err
		}

		if targetMemberInfo != nil {
			target = target.UseTarget(targetMemberInfo.Name)
		} else if s.ServerClustered {
			target = target.UseTarget(inst.Location())
		}

		targetProject := inst.Project().Name
		if req.Project != "" {
			target = target.UseProject(req.Project)
			targetProject = req.Project
		}

		// Check if we have a root disk in local config.
		_, _, err = internalInstance.GetRootDiskDevice(targetInstInfo.Devices)
		if err != nil && req.Project != "" {
			// If not and we're dealing with project copy, let's get one.
			var newRootDev map[string]string

			// Get current root disk.
			currentRootDevKey, currentRootDev, err := internalInstance.GetRootDiskDevice(inst.ExpandedDevices().CloneNative())
			if err != nil {
				return err
			}

			// Load the profiles.
			profiles := []api.Profile{}

			err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				rawProfiles, err := dbCluster.GetProfilesIfEnabled(ctx, tx.Tx(), targetProject, targetInstInfo.Profiles)
				if err != nil {
					return err
				}

				for _, profile := range rawProfiles {
					apiProfile, err := profile.ToAPI(ctx, tx.Tx())
					if err != nil {
						return err
					}

					profiles = append(profiles, *apiProfile)
				}

				return nil
			})
			if err != nil {
				return err
			}

			// Go through expected profiles and look for a root disk.
			for _, profile := range profiles {
				_, dev, err := internalInstance.GetRootDiskDevice(profile.Devices)
				if err != nil {
					continue
				}

				newRootDev = dev
				break
			}

			// Check if root disk coming from profiles is suitable, if not, copy the current one.
			if newRootDev == nil ||
				newRootDev["pool"] != currentRootDev["pool"] ||
				newRootDev["size"] != currentRootDev["size"] ||
				newRootDev["size.state"] != currentRootDev["size.state"] {
				targetInstInfo.Devices[currentRootDevKey] = currentRootDev
			}
		}

		// Use a temporary instance name if needed.
		targetInstName := inst.Name()
		if req.Project == "" {
			targetInstName, err = instance.MoveTemporaryName(inst)
			if err != nil {
				return err
			}
		}

		// Create the target instance.
		destOp, err := target.CreateInstance(api.InstancesPost{
			Name:        targetInstName,
			InstancePut: targetInstInfo.Writable(),
			Type:        api.InstanceType(targetInstInfo.Type),
			Source: api.InstanceSource{
				Type:         "copy",
				Source:       inst.Name(),
				Project:      inst.Project().Name,
				InstanceOnly: req.InstanceOnly,
			},
		})
		if err != nil {
			return fmt.Errorf("Failed requesting instance create on destination: %w", err)
		}

		// Setup a progress handler.
		handler := func(newOp api.Operation) {
			_ = op.UpdateMetadata(newOp.Metadata)
		}

		_, err = destOp.AddHandler(handler)
		if err != nil {
			return err
		}

		// Wait for the migration to complete.
		err = destOp.Wait()
		if err != nil {
			return fmt.Errorf("Instance move to destination failed: %w", err)
		}

		// Delete the source instance.
		err = inst.Delete(true)
		if err != nil {
			return err
		}

		// If using a temporary name, rename it.
		if targetInstName != inst.Name() {
			op, err := target.RenameInstance(targetInstName, api.InstancePost{Name: inst.Name()})
			if err != nil {
				return err
			}

			err = op.Wait()
			if err != nil {
				return err
			}
		}

		// Reload the instance.
		inst, err = instance.LoadByProjectAndName(s, targetProject, inst.Name())
		if err != nil {
			return err
		}

		// Clear the pool and project part of the request.
		req.Pool = ""
		req.Project = ""
	}

	// Handle remote migrations (location changes).
	if targetMemberInfo != nil && inst.Location() != targetMemberInfo.Name {
		// Get the client.
		networkCert := s.Endpoints.NetworkCert()
		target, err := cluster.Connect(targetMemberInfo.Address, networkCert, s.ServerCert(), nil, true)
		if err != nil {
			return fmt.Errorf("Failed to connect to destination server %q: %w", targetMemberInfo.Address, err)
		}

		target = target.UseProject(inst.Project().Name)
		if targetMemberInfo != nil {
			target = target.UseTarget(targetMemberInfo.Name)
		}

		// Get the source member info if missing.
		if sourceMemberInfo == nil {
			err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				// Get the source member info.
				srcMember, err := tx.GetNodeByName(ctx, inst.Location())
				if err != nil {
					return fmt.Errorf("Failed getting current cluster member of instance %q", inst.Name())
				}

				sourceMemberInfo = &srcMember
				return nil
			})
			if err != nil {
				return err
			}
		}

		// Get the current instance snapshot list.
		snapshots, err := inst.Snapshots()
		if err != nil {
			return fmt.Errorf("Failed getting source instance snapshots: %w", err)
		}

		// Setup a new migration source.
		sourceMigration, err := newMigrationSource(inst, req.Live, false, req.AllowInconsistent, inst.Name(), nil)
		if err != nil {
			return fmt.Errorf("Failed setting up instance migration on source: %w", err)
		}

		run := func(op *operations.Operation) error {
			return sourceMigration.Do(s, op)
		}

		cancel := func(op *operations.Operation) error {
			sourceMigration.disconnect()
			return nil
		}

		resources := map[string][]api.URL{}
		resources["instances"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", inst.Name())}
		sourceOp, err := operations.OperationCreate(s, inst.Project().Name, operations.OperationClassWebsocket, operationtype.InstanceMigrate, resources, sourceMigration.Metadata(), run, cancel, sourceMigration.Connect, nil)
		if err != nil {
			return err
		}

		// Start the migration source.
		err = sourceOp.Start()
		if err != nil {
			return fmt.Errorf("Failed starting migration source operation: %w", err)
		}

		// Extract the migration secrets.
		sourceSecrets := make(map[string]string, len(sourceMigration.conns))
		for connName, conn := range sourceMigration.conns {
			sourceSecrets[connName] = conn.Secret()
		}

		// Create the target instance.
		destOp, err := target.CreateInstance(api.InstancesPost{
			Name:        inst.Name(),
			InstancePut: targetInstInfo.Writable(),
			Type:        api.InstanceType(targetInstInfo.Type),
			Source: api.InstanceSource{
				Type:        "migration",
				Mode:        "pull",
				Operation:   fmt.Sprintf("https://%s%s", sourceMemberInfo.Address, sourceOp.URL()),
				Websockets:  sourceSecrets,
				Certificate: string(networkCert.PublicKey()),
				Live:        req.Live,
				Source:      inst.Name(),
			},
		})
		if err != nil {
			return fmt.Errorf("Failed requesting instance create on destination: %w", err)
		}

		// Setup a progress handler.
		handler := func(newOp api.Operation) {
			_ = op.UpdateMetadata(newOp.Metadata)
		}

		_, err = destOp.AddHandler(handler)
		if err != nil {
			return err
		}

		// Wait for the migration to complete.
		err = destOp.Wait()
		if err != nil {
			return fmt.Errorf("Instance move to destination failed: %w", err)
		}

		err = sourceOp.Wait(context.Background())
		if err != nil {
			return fmt.Errorf("Instance move to destination failed on source: %w", err)
		}

		// Update the database post-migration.
		err = s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Update instance DB record to indicate its location on the new cluster member.
			err = tx.UpdateInstanceNode(ctx, inst.Project().Name, inst.Name(), inst.Name(), targetMemberInfo.Name, sourcePool.ID(), volDBType)
			if err != nil {
				return fmt.Errorf("Failed updating cluster member to %q for instance %q: %w", targetMemberInfo.Name, inst.Name(), err)
			}

			// Restore the original value of "volatile.apply_template".
			id, err := dbCluster.GetInstanceID(ctx, tx.Tx(), inst.Project().Name, inst.Name())
			if err != nil {
				return fmt.Errorf("Failed to get ID of moved instance: %w", err)
			}

			err = tx.DeleteInstanceConfigKey(ctx, id, "volatile.apply_template")
			if err != nil {
				return fmt.Errorf("Failed to remove volatile.apply_template config key: %w", err)
			}

			if instVolatileApplyTemplate != "" {
				config := map[string]string{
					"volatile.apply_template": instVolatileApplyTemplate,
				}

				err = tx.CreateInstanceConfig(ctx, int(id), config)
				if err != nil {
					return fmt.Errorf("Failed to set volatile.apply_template config key: %w", err)
				}
			}

			return nil
		})
		if err != nil {
			return err
		}

		// Cleanup instance paths on source member if using remote shared storage.
		if sourcePool.Driver().Info().Remote {
			err = sourcePool.CleanupInstancePaths(inst, nil)
			if err != nil {
				return fmt.Errorf("Failed cleaning up instance paths on source member: %w", err)
			}
		} else {
			// Delete the instance on source member if pool isn't remote shared storage.
			// We cannot use the normal delete process, as that would remove the instance DB record.
			// So instead we need to delete just the local storage volume(s) for the instance.
			snapshotCount := len(snapshots)
			for k := range snapshots {
				// Delete the snapshots in reverse order.
				k = snapshotCount - 1 - k

				err = sourcePool.DeleteInstanceSnapshot(snapshots[k], nil)
				if err != nil {
					return fmt.Errorf("Failed delete instance snapshot %q on source member: %w", snapshots[k].Name(), err)
				}
			}

			err = sourcePool.DeleteInstance(inst, nil)
			if err != nil {
				return fmt.Errorf("Failed deleting instance on source member: %w", err)
			}
		}
	}

	return nil
}
