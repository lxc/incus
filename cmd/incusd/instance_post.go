package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	internalInstance "github.com/lxc/incus/internal/instance"
	"github.com/lxc/incus/internal/jmap"
	"github.com/lxc/incus/internal/server/auth"
	"github.com/lxc/incus/internal/server/cluster"
	"github.com/lxc/incus/internal/server/db"
	dbCluster "github.com/lxc/incus/internal/server/db/cluster"
	"github.com/lxc/incus/internal/server/db/operationtype"
	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/instance/instancetype"
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
	// Don't mess with instance while in setup mode.
	<-d.waitReady.Done()

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

	// Flag indicating whether the node running the container is offline.
	sourceNodeOffline := false

	var targetProject *api.Project
	var targetMemberInfo *db.NodeInfo
	var candidateMembers []db.NodeInfo

	target := request.QueryParam(r, "target")
	if !s.ServerClustered && target != "" {
		return response.BadRequest(fmt.Errorf("Target only allowed when clustered"))
	}

	// A POST to /instances/<name>?target=<member> is meant to be used to
	// move an instance from one member to another within a cluster.
	//
	// Determine if either the source node (the one currently
	// running the instance) or the target node are offline.
	//
	// If the target node is offline, we return an error.
	//
	// If the source node is offline and the instance is backed by
	// ceph, we'll just assume that the instance is not running
	// and it's safe to move it.
	//
	// TODO: add some sort of "force" flag to the API, to signal
	//       that the user really wants to move the instance even
	//       if we can't know for sure that it's indeed not
	//       running?
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Load source node.
		sourceAddress, err := tx.GetNodeAddressOfInstance(ctx, projectName, name, instanceType)
		if err != nil {
			return fmt.Errorf("Failed to get address of instance's member: %w", err)
		}

		if sourceAddress == "" {
			// Local node.
			sourceNodeOffline = false
			return nil
		}

		sourceMemberInfo, err := tx.GetNodeByAddress(ctx, sourceAddress)
		if err != nil {
			return fmt.Errorf("Failed to get source member for %q: %w", sourceAddress, err)
		}

		sourceNodeOffline = sourceMemberInfo.IsOffline(s.GlobalConfig.OfflineThreshold())

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	// Check whether to forward the request to the node that is running the
	// instance. Here are the possible cases:
	//
	// 1. No "?target=<member>" parameter was passed. In this case this is
	//    just an instance rename, with no move, and we want the request to be
	//    handled by the node which is actually running the instance.
	//
	// 2. The "?target=<member>" parameter was set and the node running the
	//    instance is online. In this case we want to forward the request to
	//    that node, which might do things like unmapping the RBD volume for
	//    ceph instances.
	//
	// 3. The "?target=<member>" parameter was set but the node running the
	//    instance is offline. We don't want to forward to the request to
	//    that node and we don't want to load the instance here (since
	//    it's not a local instance): we'll be able to handle the request
	//    at all only if the instance is backed by ceph. We'll check for
	//    that just below.
	//
	// Cases 1. and 2. are the ones for which the conditional will be true
	// and we'll either forward the request or load the instance.
	if target == "" || !sourceNodeOffline {
		// Handle requests targeted to a container on a different node.
		resp, err := forwardedResponseIfInstanceIsRemote(s, r, projectName, name, instanceType)
		if err != nil {
			return response.SmartError(err)
		}

		if resp != nil {
			return resp
		}
	} else if sourceNodeOffline {
		// If a target was specified, forward the request to the relevant node.
		resp := forwardedResponseIfTargetIsRemote(s, r)
		if resp != nil {
			return resp
		}
	}

	inst, err := instance.LoadByProjectAndName(s, projectName, name)
	if err != nil {
		return response.SmartError(err)
	}

	// Run the cluster placement after potentially forwarding the request to another member.
	if target != "" && s.ServerClustered {
		err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
			p, err := dbCluster.GetProject(ctx, tx.Tx(), projectName)
			if err != nil {
				return err
			}

			targetProject, err = p.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			allMembers, err := tx.GetNodes(ctx)
			if err != nil {
				return fmt.Errorf("Failed getting cluster members: %w", err)
			}

			var targetGroupName string

			targetMemberInfo, targetGroupName, err = project.CheckTarget(ctx, s.Authorizer, r, tx, targetProject, target, allMembers)
			if err != nil {
				return err
			}

			if targetMemberInfo == nil {
				clusterGroupsAllowed := project.GetRestrictedClusterGroups(targetProject)

				candidateMembers, err = tx.GetCandidateMembers(ctx, allMembers, []int{inst.Architecture()}, targetGroupName, clusterGroupsAllowed, s.GlobalConfig.OfflineThreshold())
				if err != nil {
					return err
				}
			}

			return nil
		})
		if err != nil {
			return response.SmartError(err)
		}

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

			targetMemberInfo, err = scriptlet.InstancePlacementRun(r.Context(), logger.Log, s, &req, candidateMembers, leaderAddress)
			if err != nil {
				return response.BadRequest(fmt.Errorf("Failed instance placement scriptlet: %w", err))
			}
		}

		// If no member was selected yet, pick the member with the least number of instances.
		if targetMemberInfo == nil {
			var filteredCandidateMembers []db.NodeInfo

			// The instance might already be placed on the node with least number of instances.
			// Therefore remove it from the list of possible candidates if existent.
			for _, candidateMember := range candidateMembers {
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

	req := api.InstancePost{}
	err = json.NewDecoder(rdr2).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	// Check if stateful indicator supplied and default to true if not (for backward compatibility).
	_, err = reqRaw.GetBool("live")
	if err != nil {
		req.Live = true
	}

	// If new instance name not supplied, assume it will be keeping its current name.
	if req.Name == "" {
		req.Name = inst.Name()
	}

	// Check the new instance name is valid.
	err = instance.ValidName(req.Name, false)
	if err != nil {
		return response.BadRequest(err)
	}

	if req.Migration {
		// Server-side instance migration.
		if req.Pool != "" || req.Project != "" {
			// Check if user has access to target project.
			if req.Project != "" {
				err := s.Authorizer.CheckPermission(r.Context(), r, auth.ObjectProject(req.Project), auth.EntitlementCanCreateInstances)
				if err != nil {
					return response.SmartError(err)
				}
			}

			// Setup the instance move operation.
			run := func(op *operations.Operation) error {
				return instancePostMigration(s, inst, req.Name, req.Pool, req.Project, req.Config, req.Devices, req.Profiles, req.InstanceOnly, req.Live, req.AllowInconsistent, op)
			}

			resources := map[string][]api.URL{}
			resources["instances"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", name)}
			op, err := operations.OperationCreate(s, projectName, operations.OperationClassTask, operationtype.InstanceMigrate, resources, nil, run, nil, nil, r)
			if err != nil {
				return response.InternalError(err)
			}

			return operations.OperationResponse(op)
		}

		if targetMemberInfo != nil {
			var backups []string

			// Check if instance has backups.
			err := s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
				backups, err = tx.GetInstanceBackups(ctx, projectName, name)
				return err
			})
			if err != nil {
				err = fmt.Errorf("Failed to fetch instance's backups: %w", err)
				return response.SmartError(err)
			}

			if len(backups) > 0 {
				return response.BadRequest(fmt.Errorf("Instance has backups"))
			}

			run := func(op *operations.Operation) error {
				return migrateInstance(s, r, inst, targetMemberInfo.Name, req, op)
			}

			resources := map[string][]api.URL{}
			resources["instances"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", name)}

			if inst.Type() == instancetype.Container {
				resources["containers"] = resources["instances"]
			}

			op, err := operations.OperationCreate(s, projectName, operations.OperationClassTask, operationtype.InstanceMigrate, resources, nil, run, nil, nil, r)
			if err != nil {
				return response.InternalError(err)
			}

			return operations.OperationResponse(op)
		}

		instanceOnly := req.InstanceOnly
		ws, err := newMigrationSource(inst, req.Live, instanceOnly, req.AllowInconsistent, "", req.Target)
		if err != nil {
			return response.InternalError(err)
		}

		resources := map[string][]api.URL{}
		resources["instances"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", name)}

		if inst.Type() == instancetype.Container {
			resources["containers"] = resources["instances"]
		}

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

	var id int

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Check that the name isn't already in use.
		id, _ = tx.GetInstanceID(ctx, projectName, req.Name)

		return nil
	})
	if id > 0 {
		return response.Conflict(fmt.Errorf("Name %q already in use", req.Name))
	}

	run := func(*operations.Operation) error {
		return inst.Rename(req.Name, true)
	}

	resources := map[string][]api.URL{}
	resources["instances"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", name)}

	if inst.Type() == instancetype.Container {
		resources["containers"] = resources["instances"]
	}

	op, err := operations.OperationCreate(s, projectName, operations.OperationClassTask, operationtype.InstanceRename, resources, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

// Move an instance.
func instancePostMigration(s *state.State, inst instance.Instance, newName string, newPool string, newProject string, config map[string]string, devices map[string]map[string]string, profiles []string, instanceOnly bool, stateful bool, allowInconsistent bool, op *operations.Operation) error {
	if inst.IsSnapshot() {
		return fmt.Errorf("Instance snapshots cannot be moved between pools")
	}

	if newProject == "" {
		newProject = inst.Project().Name
	}

	statefulStart := false
	if inst.IsRunning() {
		if stateful {
			statefulStart = true
			err := inst.Stop(true)
			if err != nil {
				return err
			}
		} else {
			return api.StatusErrorf(http.StatusBadRequest, "Instance must be stopped to move between pools statelessly")
		}
	}

	// Copy config from instance to avoid modifying it.
	localConfig := make(map[string]string)
	for k, v := range inst.LocalConfig() {
		localConfig[k] = v
	}

	// Set user defined configuration entries.
	for k, v := range config {
		localConfig[k] = v
	}

	// Get instance local devices and then set user defined devices.
	localDevices := inst.LocalDevices().Clone()
	for devName, dev := range devices {
		localDevices[devName] = dev
	}

	// Apply previous profiles, if provided profiles are nil.
	if profiles == nil {
		profiles = make([]string, 0, len(inst.Profiles()))
		for _, p := range inst.Profiles() {
			profiles = append(profiles, p.Name)
		}
	}

	apiProfiles := []api.Profile{}
	if len(profiles) > 0 {
		err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			profiles, err := dbCluster.GetProfilesIfEnabled(ctx, tx.Tx(), newProject, profiles)
			if err != nil {
				return err
			}

			apiProfiles = make([]api.Profile, 0, len(profiles))
			for _, profile := range profiles {
				apiProfile, err := profile.ToAPI(ctx, tx.Tx())
				if err != nil {
					return err
				}

				apiProfiles = append(apiProfiles, *apiProfile)
			}

			return nil
		})
		if err != nil {
			return err
		}
	}

	// Check if root disk device is present in the instance config. If instance config has no
	// root disk device configured, check if the same root disk device will be applied with new
	// profiles in the target project. If the new root disk device differs from the existing
	// one, add the existing one as a local device to the instance (we don't want to move root
	// disk device if not necessary, as this is an expensive operation).
	rootDevKey, rootDev, err := internalInstance.GetRootDiskDevice(localDevices.CloneNative())
	if err != nil && !errors.Is(err, internalInstance.ErrNoRootDisk) {
		return err
	} else if errors.Is(err, internalInstance.ErrNoRootDisk) {
		// Find currently applied root disk device from expanded devices.
		rootDevKey, rootDev, err = internalInstance.GetRootDiskDevice(inst.ExpandedDevices().CloneNative())
		if err != nil {
			return err
		}

		// Iterate over profiles that will be applied in the target project and find
		// the new root disk device. Iterate in reverse order to respect profile
		// precedence.
		var profileRootDev map[string]string
		for i := len(apiProfiles) - 1; i >= 0; i-- {
			_, profileRootDev, err = internalInstance.GetRootDiskDevice(apiProfiles[i].Devices)
			if err == nil {
				break
			}
		}

		// If current root disk device would be replaced according to the new profiles,
		// add current root disk device to local instance devices (to retain it).
		if profileRootDev == nil ||
			profileRootDev["pool"] != rootDev["pool"] ||
			profileRootDev["size"] != rootDev["size"] ||
			profileRootDev["size.state"] != rootDev["size.state"] {
			localDevices[rootDevKey] = rootDev
		}
	}

	// Set specific storage pool for the instance, if provided.
	if newPool != "" {
		rootDev["pool"] = newPool
		localDevices[rootDevKey] = rootDev
	}

	// Specify the target instance config with the new name.
	args := db.InstanceArgs{
		Name:         newName,
		BaseImage:    localConfig["volatile.base_image"],
		Config:       localConfig,
		Devices:      localDevices,
		Profiles:     apiProfiles,
		Project:      newProject,
		Type:         inst.Type(),
		Architecture: inst.Architecture(),
		Description:  inst.Description(),
		Ephemeral:    inst.IsEphemeral(),
		Stateful:     inst.IsStateful(),
	}

	// If we are moving the instance to a new pool but keeping the same instance name, then we need to create
	// the copy of the instance on the new pool with a temporary name that is different from the source to
	// avoid conflicts. Then after the source instance has been deleted we will rename the new instance back
	// to the original name.
	if newName == inst.Name() && newProject == inst.Project().Name {
		args.Name, err = instance.MoveTemporaryName(inst)
		if err != nil {
			return err
		}
	}

	// Copy instance to new target instance.
	targetInst, err := instanceCreateAsCopy(s, instanceCreateAsCopyOpts{
		sourceInstance:       inst,
		targetInstance:       args,
		instanceOnly:         instanceOnly,
		applyTemplateTrigger: false, // Don't apply templates when moving.
		allowInconsistent:    allowInconsistent,
	}, op)
	if err != nil {
		return err
	}

	// Delete original instance.
	err = inst.Delete(true)
	if err != nil {
		return err
	}

	// Rename copy from temporary name to original name if needed.
	if newName == inst.Name() && newProject == inst.Project().Name {
		err = targetInst.Rename(newName, false) // Don't apply templates when moving.
		if err != nil {
			return err
		}
	}

	if statefulStart {
		err = targetInst.Start(true)
		if err != nil {
			return err
		}
	}

	return nil
}

// Move a non-ceph container to another cluster node.
func instancePostClusteringMigrate(s *state.State, r *http.Request, srcPool storagePools.Pool, srcInst instance.Instance, newInstName string, srcMember db.NodeInfo, newMember db.NodeInfo, stateful bool, allowInconsistent bool) (func(op *operations.Operation) error, error) {
	srcMemberOffline := srcMember.IsOffline(s.GlobalConfig.OfflineThreshold())

	// Make sure that the source member is online if we end up being called from another member after a
	// redirection due to the source member being offline.
	if srcMemberOffline {
		return nil, fmt.Errorf("The cluster member hosting the instance is offline")
	}

	// Save the original value of the "volatile.apply_template" config key,
	// since we'll want to preserve it in the copied container.
	origVolatileApplyTemplate := srcInst.LocalConfig()["volatile.apply_template"]

	// Check we can convert the instance to the volume types needed.
	volType, err := storagePools.InstanceTypeToVolumeType(srcInst.Type())
	if err != nil {
		return nil, err
	}

	volDBType, err := storagePools.VolumeTypeToDBType(volType)
	if err != nil {
		return nil, err
	}

	run := func(op *operations.Operation) error {
		srcInstName := srcInst.Name()
		projectName := srcInst.Project().Name

		if newInstName == "" {
			newInstName = srcInstName
		}

		networkCert := s.Endpoints.NetworkCert()

		// Connect to the destination member, i.e. the member to migrate the instance to.
		// Use the notify argument to indicate to the destination that we are moving an instance between
		// cluster members.
		dest, err := cluster.Connect(newMember.Address, networkCert, s.ServerCert(), r, true)
		if err != nil {
			return fmt.Errorf("Failed to connect to destination server %q: %w", newMember.Address, err)
		}

		dest = dest.UseTarget(newMember.Name).UseProject(projectName)

		resources := map[string][]api.URL{}
		resources["instances"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", srcInstName)}

		srcInstRunning := srcInst.IsRunning()
		live := stateful && srcInstRunning

		// During a stateful migration we expect the migration process to stop the instance on the source
		// once the migration is complete. However when doing a stateless migration and the instance is
		// running we must forcefully stop the instance on the source before starting the migration copy
		// so that it is as consistent as possible.
		if !stateful && srcInstRunning {
			err := srcInst.Stop(false)
			if err != nil {
				return fmt.Errorf("Failed statelessly stopping instance %q: %w", srcInstName, err)
			}
		}

		// Rename instance if requested.
		if newInstName != srcInstName {
			err = srcInst.Rename(newInstName, true)
			if err != nil {
				return fmt.Errorf("Failed renaming instance %q to %q: %w", srcInstName, newInstName, err)
			}

			srcInst, err = instance.LoadByProjectAndName(s, projectName, newInstName)
			if err != nil {
				return fmt.Errorf("Failed loading renamed instance: %w", err)
			}

			srcInstName = srcInst.Name()
		}

		snapshots, err := srcInst.Snapshots()
		if err != nil {
			return fmt.Errorf("Failed getting source instance snapshots: %w", err)
		}

		// Setup migration source.
		srcRenderRes, _, err := srcInst.Render()
		if err != nil {
			return fmt.Errorf("Failed getting source instance info: %w", err)
		}

		srcInstInfo, ok := srcRenderRes.(*api.Instance)
		if !ok {
			return fmt.Errorf("Unexpected result from source instance render: %w", err)
		}

		srcMigration, err := newMigrationSource(srcInst, live, false, allowInconsistent, srcInstName, nil)
		if err != nil {
			return fmt.Errorf("Failed setting up instance migration on source: %w", err)
		}

		run := func(op *operations.Operation) error {
			return srcMigration.Do(s, op)
		}

		cancel := func(op *operations.Operation) error {
			srcMigration.disconnect()
			return nil
		}

		srcOp, err := operations.OperationCreate(s, projectName, operations.OperationClassWebsocket, operationtype.InstanceMigrate, resources, srcMigration.Metadata(), run, cancel, srcMigration.Connect, r)
		if err != nil {
			return err
		}

		err = srcOp.Start()
		if err != nil {
			return fmt.Errorf("Failed starting migration source operation: %w", err)
		}

		sourceSecrets := make(map[string]string, len(srcMigration.conns))
		for connName, conn := range srcMigration.conns {
			sourceSecrets[connName] = conn.Secret()
		}

		// Request pull mode migration on destination.
		destOp, err := dest.CreateInstance(api.InstancesPost{
			Name:        newInstName,
			InstancePut: srcInstInfo.Writable(),
			Type:        api.InstanceType(srcInstInfo.Type),
			Source: api.InstanceSource{
				Type:        "migration",
				Mode:        "pull",
				Operation:   fmt.Sprintf("https://%s%s", srcMember.Address, srcOp.URL()),
				Websockets:  sourceSecrets,
				Certificate: string(networkCert.PublicKey()),
				Live:        live,
				Source:      srcInstName,
			},
		})
		if err != nil {
			return fmt.Errorf("Failed requesting instance create on destination: %w", err)
		}

		handler := func(newOp api.Operation) {
			_ = op.UpdateMetadata(newOp.Metadata)
		}

		_, err = destOp.AddHandler(handler)
		if err != nil {
			return err
		}

		err = destOp.Wait()
		if err != nil {
			return fmt.Errorf("Instance move to destination failed: %w", err)
		}

		err = srcOp.Wait(context.Background())
		if err != nil {
			return fmt.Errorf("Instance move to destination failed on source: %w", err)
		}

		err = s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Update instance DB record to indicate its location on the new cluster member.
			err = tx.UpdateInstanceNode(ctx, projectName, srcInstName, newInstName, newMember.Name, srcPool.ID(), volDBType)
			if err != nil {
				return fmt.Errorf("Failed updating cluster member to %q for instance %q: %w", newMember.Name, newInstName, err)
			}

			// Restore the original value of "volatile.apply_template".
			id, err := dbCluster.GetInstanceID(ctx, tx.Tx(), projectName, newInstName)
			if err != nil {
				return fmt.Errorf("Failed to get ID of moved instance: %w", err)
			}

			err = tx.DeleteInstanceConfigKey(ctx, id, "volatile.apply_template")
			if err != nil {
				return fmt.Errorf("Failed to remove volatile.apply_template config key: %w", err)
			}

			if origVolatileApplyTemplate != "" {
				config := map[string]string{
					"volatile.apply_template": origVolatileApplyTemplate,
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
		if srcPool.Driver().Info().Remote {
			err = srcPool.CleanupInstancePaths(srcInst, nil)
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

				err = srcPool.DeleteInstanceSnapshot(snapshots[k], nil)
				if err != nil {
					return fmt.Errorf("Failed delete instance snapshot %q on source member: %w", snapshots[k].Name(), err)
				}
			}

			err = srcPool.DeleteInstance(srcInst, nil)
			if err != nil {
				return fmt.Errorf("Failed deleting instance on source member: %w", err)
			}
		}

		if !stateful && srcInstRunning {
			req := api.InstanceStatePut{
				Action: "start",
			}

			op, err := dest.UpdateInstanceState(newInstName, req, "")
			if err != nil {
				return err
			}

			err = op.Wait()
			if err != nil {
				return fmt.Errorf("Failed starting instance %q: %w", newInstName, err)
			}
		}

		return nil
	}

	return run, nil
}

// instancePostClusteringMigrateWithCeph handles moving a ceph instance from a source member that is offline.
// This function must be run on the target cluster member to move the instance to.
func instancePostClusteringMigrateWithCeph(s *state.State, r *http.Request, srcPool storagePools.Pool, srcInst instance.Instance, newInstName string, newMember db.NodeInfo, stateful bool) (func(op *operations.Operation) error, error) {
	// Sense checks to avoid unexpected behaviour.
	if srcPool.Driver().Info().Name != "ceph" {
		return nil, fmt.Errorf("Source instance's storage pool is not of type ceph")
	}

	// Check this function is only run on the target member.
	if s.ServerName != newMember.Name {
		return nil, fmt.Errorf("Ceph instance move when source member is offline must be run on target member")
	}

	// Check we can convert the instance to the volume types needed.
	volType, err := storagePools.InstanceTypeToVolumeType(srcInst.Type())
	if err != nil {
		return nil, err
	}

	volDBType, err := storagePools.VolumeTypeToDBType(volType)
	if err != nil {
		return nil, err
	}

	run := func(op *operations.Operation) error {
		projectName := srcInst.Project().Name
		srcInstName := srcInst.Name()

		// Re-link the database entries against the new member name.
		err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			err := tx.UpdateInstanceNode(ctx, projectName, srcInstName, srcInstName, newMember.Name, srcPool.ID(), volDBType)
			if err != nil {
				return fmt.Errorf("Failed updating cluster member to %q for instance %q: %w", newMember.Name, srcInstName, err)
			}

			return nil
		})
		if err != nil {
			return fmt.Errorf("Failed to relink instance database data: %w", err)
		}

		if srcInstName != newInstName {
			err = srcInst.Rename(newInstName, true)
			if err != nil {
				return fmt.Errorf("Failed renaming instance %q to %q: %w", srcInstName, newInstName, err)
			}

			srcInst, err = instance.LoadByProjectAndName(s, projectName, newInstName)
			if err != nil {
				return fmt.Errorf("Failed loading renamed instance: %w", err)
			}

			srcInstName = srcInst.Name()
		}

		_, err = srcPool.ImportInstance(srcInst, nil, nil)
		if err != nil {
			return fmt.Errorf("Failed creating mount point of instance on target node: %w", err)
		}

		return nil
	}

	return run, nil
}

func migrateInstance(s *state.State, r *http.Request, inst instance.Instance, targetNode string, req api.InstancePost, op *operations.Operation) error {
	// If target isn't the same as the instance's location.
	if targetNode == inst.Location() {
		return fmt.Errorf("Target must be different than instance's current location")
	}

	var err error
	var srcMember, newMember db.NodeInfo

	// If the source member is online then get its address so we can connect to it and see if the
	// instance is running later.
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		srcMember, err = tx.GetNodeByName(ctx, inst.Location())
		if err != nil {
			return fmt.Errorf("Failed getting current cluster member of instance %q", inst.Name())
		}

		newMember, err = tx.GetNodeByName(ctx, targetNode)
		if err != nil {
			return fmt.Errorf("Failed loading new cluster member for instance: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Check if we are migrating a ceph-based instance.
	srcPool, err := storagePools.LoadByInstance(s, inst)
	if err != nil {
		return fmt.Errorf("Failed loading instance storage pool: %w", err)
	}

	// Only use instancePostClusteringMigrateWithCeph when source member is offline.
	if srcMember.IsOffline(s.GlobalConfig.OfflineThreshold()) && srcPool.Driver().Info().Name == "ceph" {
		f, err := instancePostClusteringMigrateWithCeph(s, r, srcPool, inst, req.Name, newMember, req.Live)
		if err != nil {
			return err
		}

		return f(op)
	}

	f, err := instancePostClusteringMigrate(s, r, srcPool, inst, req.Name, srcMember, newMember, req.Live, req.AllowInconsistent)
	if err != nil {
		return err
	}

	return f(op)
}
