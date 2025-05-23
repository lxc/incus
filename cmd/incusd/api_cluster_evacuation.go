package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"golang.org/x/sync/errgroup"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/internal/server/cluster"
	"github.com/lxc/incus/v6/internal/server/db"
	dbCluster "github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/db/operationtype"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/lifecycle"
	"github.com/lxc/incus/v6/internal/server/operations"
	"github.com/lxc/incus/v6/internal/server/response"
	"github.com/lxc/incus/v6/internal/server/scriptlet"
	"github.com/lxc/incus/v6/internal/server/state"
	storagePools "github.com/lxc/incus/v6/internal/server/storage"
	"github.com/lxc/incus/v6/internal/server/task"
	"github.com/lxc/incus/v6/shared/api"
	apiScriptlet "github.com/lxc/incus/v6/shared/api/scriptlet"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/osarch"
	"github.com/lxc/incus/v6/shared/revert"
	"github.com/lxc/incus/v6/shared/subprocess"
)

type (
	evacuateStopFunc    func(inst instance.Instance, action string) error
	evacuateMigrateFunc func(ctx context.Context, s *state.State, inst instance.Instance, sourceMemberInfo *db.NodeInfo, targetMemberInfo *db.NodeInfo, live bool, startInstance bool, metadata map[string]any, op *operations.Operation) error
)

type evacuateOpts struct {
	s               *state.State
	instances       []instance.Instance
	mode            string
	srcMemberName   string
	stopInstance    evacuateStopFunc
	migrateInstance evacuateMigrateFunc
	op              *operations.Operation
}

func evacuateClusterSetState(s *state.State, name string, newState int) error {
	return s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the node.
		node, err := tx.GetNodeByName(ctx, name)
		if err != nil {
			return fmt.Errorf("Failed to get cluster member by name: %w", err)
		}

		if node.State == db.ClusterMemberStatePending {
			return errors.New("Cannot evacuate or restore a pending cluster member")
		}

		// Do nothing if the node is already in expected state.
		if node.State == newState {
			if newState == db.ClusterMemberStateEvacuated {
				return errors.New("Cluster member is already evacuated")
			} else if newState == db.ClusterMemberStateCreated {
				return errors.New("Cluster member is already restored")
			}

			return errors.New("Cluster member is already in requested state")
		}

		// Set node status to requested value.
		err = tx.UpdateNodeStatus(node.ID, newState)
		if err != nil {
			return fmt.Errorf("Failed to update cluster member status: %w", err)
		}

		return nil
	})
}

// evacuateHostShutdownDefaultTimeout default timeout (in seconds) for waiting for clean shutdown to complete.
const evacuateHostShutdownDefaultTimeout = 30

func evacuateClusterMember(ctx context.Context, s *state.State, op *operations.Operation, name string, mode string, stopInstance evacuateStopFunc, migrateInstance evacuateMigrateFunc) error {
	// Get the instance list for the server being evacuated.
	var dbInstances []dbCluster.Instance
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		dbInstances, err = dbCluster.GetInstances(ctx, tx.Tx(), dbCluster.InstanceFilter{Node: &name})
		if err != nil {
			return fmt.Errorf("Failed to get instances: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Load the instance structs.
	instances := make([]instance.Instance, len(dbInstances))
	for i, dbInst := range dbInstances {
		inst, err := instance.LoadByProjectAndName(s, dbInst.Project, dbInst.Name)
		if err != nil {
			return fmt.Errorf("Failed to load instance: %w", err)
		}

		instances[i] = inst
	}

	// Setup a reverter.
	reverter := revert.New()
	defer reverter.Fail()

	// Set cluster member status to EVACUATED.
	err = evacuateClusterSetState(s, name, db.ClusterMemberStateEvacuated)
	if err != nil {
		return err
	}

	reverter.Add(func() {
		_ = evacuateClusterSetState(s, name, db.ClusterMemberStateCreated)
	})

	// Perform the evacuation.
	opts := evacuateOpts{
		s:               s,
		instances:       instances,
		mode:            mode,
		srcMemberName:   name,
		stopInstance:    stopInstance,
		migrateInstance: migrateInstance,
		op:              op,
	}

	err = evacuateInstances(ctx, opts)
	if err != nil {
		return err
	}

	// Stop networks after evacuation.
	networkShutdown(s)

	reverter.Success()

	if mode != "heal" {
		s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.ClusterMemberEvacuated.Event(name, op.Requestor(), nil))
	}

	return nil
}

func evacuateInstances(ctx context.Context, opts evacuateOpts) error {
	if opts.migrateInstance == nil {
		return errors.New("Missing migration callback function")
	}

	// Limit the number of concurrent evacuations to run at the same time
	numParallelEvacs := max(runtime.NumCPU()/16, 1)

	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(numParallelEvacs)

	for _, inst := range opts.instances {
		group.Go(func() error {
			return evacuateInstancesFunc(groupCtx, inst, opts)
		})
	}

	err := group.Wait()
	if err != nil {
		return fmt.Errorf("Failed to evacuate instances: %w", err)
	}

	return nil
}

func evacuateInstancesFunc(ctx context.Context, inst instance.Instance, opts evacuateOpts) error {
	metadata := make(map[string]any)

	instProject := inst.Project()
	l := logger.AddContext(logger.Ctx{"project": instProject.Name, "instance": inst.Name()})

	// Check if migratable.
	action := inst.CanMigrate()

	// Apply overrides.
	if opts.mode != "" {
		if opts.mode == "heal" {
			// Source server is dead, live-migration isn't an option.
			if action == "live-migrate" {
				action = "migrate"
			}

			if action != "migrate" {
				// We can only migrate instances or leave them as they are.
				return nil
			}
		} else if opts.mode != "auto" {
			action = opts.mode
		}
	}

	// Stop the instance if needed.
	isRunning := inst.IsRunning()
	if action != "live-migrate" {
		if opts.stopInstance != nil && isRunning {
			metadata["evacuation_progress"] = fmt.Sprintf("Stopping %q in project %q", inst.Name(), instProject.Name)
			_ = opts.op.UpdateMetadata(metadata)

			err := opts.stopInstance(inst, action)
			if err != nil {
				return err
			}
		}

		if action != "migrate" {
			// Done with this instance.
			return nil
		}
	} else if !isRunning {
		// Can't live migrate if we're stopped.
		action = "migrate"
	}

	// Find a new location for the instance.
	sourceMemberInfo, targetMemberInfo, err := evacuateClusterSelectTarget(ctx, opts.s, inst)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			// Skip migration if no target is available.
			l.Warn("No migration target available for instance")
			return nil
		}

		return err
	}

	// Start migrating the instance.
	metadata["evacuation_progress"] = fmt.Sprintf("Migrating %q in project %q to %q", inst.Name(), instProject.Name, targetMemberInfo.Name)
	_ = opts.op.UpdateMetadata(metadata)

	// Set origin server (but skip if already set as that suggests more than one server being evacuated).
	if inst.LocalConfig()["volatile.evacuate.origin"] == "" {
		_ = inst.VolatileSet(map[string]string{"volatile.evacuate.origin": opts.srcMemberName})
	}

	start := isRunning || instanceShouldAutoStart(inst)
	err = opts.migrateInstance(ctx, opts.s, inst, sourceMemberInfo, targetMemberInfo, action == "live-migrate", start, metadata, opts.op)
	if err != nil {
		return err
	}

	return nil
}

func restoreClusterMember(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	originName, err := url.PathUnescape(mux.Vars(r)["name"])
	if err != nil {
		return response.SmartError(err)
	}

	// List the instances.
	var dbInstances []dbCluster.Instance
	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbInstances, err = dbCluster.GetInstances(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed to get instances: %w", err)
		}

		return nil
	})
	if err != nil {
		return response.SmartError(err)
	}

	instances := make([]instance.Instance, 0)
	localInstances := make([]instance.Instance, 0)

	for _, dbInst := range dbInstances {
		inst, err := instance.LoadByProjectAndName(s, dbInst.Project, dbInst.Name)
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed to load instance: %w", err))
		}

		if dbInst.Node == originName {
			localInstances = append(localInstances, inst)
			continue
		}

		// Only consider instances where volatile.evacuate.origin is set to the node which needs to be restored.
		val, ok := inst.LocalConfig()["volatile.evacuate.origin"]
		if !ok || val != originName {
			continue
		}

		instances = append(instances, inst)
	}

	run := func(op *operations.Operation) error {
		// Setup a reverter.
		reverter := revert.New()
		defer reverter.Fail()

		// Set node status to CREATED.
		err := evacuateClusterSetState(s, originName, db.ClusterMemberStateCreated)
		if err != nil {
			return err
		}

		// Ensure node is put into its previous state if anything fails.
		reverter.Add(func() {
			_ = evacuateClusterSetState(s, originName, db.ClusterMemberStateEvacuated)
		})

		// Restart the networks.
		err = networkStartup(d.State())
		if err != nil {
			return err
		}

		// Restart the local instances.
		for _, inst := range localInstances {
			// Don't start instances which were stopped by the user.
			if inst.LocalConfig()["volatile.last_state.power"] != instance.PowerStateRunning {
				continue
			}

			// Don't attempt to start instances which are already running.
			if inst.IsRunning() {
				continue
			}

			// Start the instance.
			metadata := make(map[string]any)
			metadata["evacuation_progress"] = fmt.Sprintf("Starting %q in project %q", inst.Name(), inst.Project().Name)
			_ = op.UpdateMetadata(metadata)

			// If configured for stateful stop, try restoring its state.
			action := inst.CanMigrate()
			if action == "stateful-stop" {
				err = inst.Start(true)
			} else {
				err = inst.Start(false)
			}

			if err != nil {
				return fmt.Errorf("Failed to start instance %q: %w", inst.Name(), err)
			}
		}

		// Limit the number of concurrent migrations to run at the same time
		numParallelMigrations := max(runtime.NumCPU()/16, 1)

		group := &errgroup.Group{}
		group.SetLimit(numParallelMigrations)

		// Migrate back the remote instances.
		for _, inst := range instances {
			group.Go(func() error {
				return restoreClusterMemberFunc(inst, op, originName, r, s)
			})
		}

		err = group.Wait()
		if err != nil {
			return fmt.Errorf("Failed to restore instances: %w", err)
		}

		reverter.Success()

		s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.ClusterMemberRestored.Event(originName, op.Requestor(), nil))

		return nil
	}

	op, err := operations.OperationCreate(s, "", operations.OperationClassTask, operationtype.ClusterMemberRestore, nil, nil, run, nil, nil, r)
	if err != nil {
		return response.InternalError(err)
	}

	return operations.OperationResponse(op)
}

func restoreClusterMemberFunc(inst instance.Instance, op *operations.Operation, originName string, r *http.Request, s *state.State) error {
	var err error
	var source incus.InstanceServer
	var sourceNode db.NodeInfo
	metadata := make(map[string]any)

	l := logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})

	// Check the action.
	live := inst.CanMigrate() == "live-migrate"

	metadata["evacuation_progress"] = fmt.Sprintf("Migrating %q in project %q from %q", inst.Name(), inst.Project().Name, inst.Location())
	_ = op.UpdateMetadata(metadata)

	err = s.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		sourceNode, err = tx.GetNodeByName(ctx, inst.Location())
		if err != nil {
			return fmt.Errorf("Failed to get node %q: %w", inst.Location(), err)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to get node: %w", err)
	}

	source, err = cluster.Connect(sourceNode.Address, s.Endpoints.NetworkCert(), s.ServerCert(), r, true)
	if err != nil {
		return fmt.Errorf("Failed to connect to source: %w", err)
	}

	source = source.UseProject(inst.Project().Name)

	apiInst, _, err := source.GetInstance(inst.Name())
	if err != nil {
		return fmt.Errorf("Failed to get instance %q: %w", inst.Name(), err)
	}

	isRunning := apiInst.StatusCode == api.Running
	if isRunning && !live {
		metadata["evacuation_progress"] = fmt.Sprintf("Stopping %q in project %q", inst.Name(), inst.Project().Name)
		_ = op.UpdateMetadata(metadata)

		timeout := inst.ExpandedConfig()["boot.host_shutdown_timeout"]
		val, err := strconv.Atoi(timeout)
		if err != nil {
			val = evacuateHostShutdownDefaultTimeout
		}

		// Attempt a clean stop.
		stopOp, err := source.UpdateInstanceState(inst.Name(), api.InstanceStatePut{Action: "stop", Force: false, Timeout: val}, "")
		if err != nil {
			return fmt.Errorf("Failed to stop instance %q: %w", inst.Name(), err)
		}

		// Wait for the stop operation to complete or timeout.
		err = stopOp.Wait()
		if err != nil {
			l.Warn("Failed shutting down instance, forcing stop", logger.Ctx{"err": err})

			// On failure, attempt a forceful stop.
			stopOp, err = source.UpdateInstanceState(inst.Name(), api.InstanceStatePut{Action: "stop", Force: true}, "")
			if err != nil {
				// If this fails too, fail the whole operation.
				return fmt.Errorf("Failed to stop instance %q: %w", inst.Name(), err)
			}

			// Wait for the forceful stop to complete.
			err = stopOp.Wait()
			if err != nil && !strings.Contains(err.Error(), "The instance is already stopped") {
				return fmt.Errorf("Failed to stop instance %q: %w", inst.Name(), err)
			}
		}
	}

	req := api.InstancePost{
		Name:      inst.Name(),
		Migration: true,
		Live:      live,
	}

	source = source.UseTarget(originName)

	migrationOp, err := source.MigrateInstance(inst.Name(), req)
	if err != nil {
		return fmt.Errorf("Migration API failure: %w", err)
	}

	err = migrationOp.Wait()
	if err != nil {
		return fmt.Errorf("Failed to wait for migration to finish: %w", err)
	}

	// Reload the instance after migration.
	inst, err = instance.LoadByProjectAndName(s, inst.Project().Name, inst.Name())
	if err != nil {
		return fmt.Errorf("Failed to load instance: %w", err)
	}

	config := inst.LocalConfig()
	delete(config, "volatile.evacuate.origin")

	args := db.InstanceArgs{
		Architecture: inst.Architecture(),
		Config:       config,
		Description:  inst.Description(),
		Devices:      inst.LocalDevices(),
		Ephemeral:    inst.IsEphemeral(),
		Profiles:     inst.Profiles(),
		Project:      inst.Project().Name,
		ExpiryDate:   inst.ExpiryDate(),
	}

	err = inst.Update(args, false)
	if err != nil {
		return fmt.Errorf("Failed to update instance %q: %w", inst.Name(), err)
	}

	if !isRunning || live {
		return nil
	}

	metadata["evacuation_progress"] = fmt.Sprintf("Starting %q in project %q", inst.Name(), inst.Project().Name)
	_ = op.UpdateMetadata(metadata)

	err = inst.Start(false)
	if err != nil {
		return fmt.Errorf("Failed to start instance %q: %w", inst.Name(), err)
	}

	return nil
}

func evacuateClusterSelectTarget(ctx context.Context, s *state.State, inst instance.Instance) (*db.NodeInfo, *db.NodeInfo, error) {
	var sourceMemberInfo *db.NodeInfo
	var targetMemberInfo *db.NodeInfo

	// Get candidate cluster members to move instances to.
	var candidateMembers []db.NodeInfo
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Get the source member info.
		srcMember, err := tx.GetNodeByName(ctx, inst.Location())
		if err != nil {
			return fmt.Errorf("Failed loading location details %q for instance %q in project %q: %w", inst.Location(), inst.Name(), inst.Project().Name, err)
		}

		sourceMemberInfo = &srcMember

		allMembers, err := tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting cluster members: %w", err)
		}

		// Filter candidates by group if needed.
		group := inst.LocalConfig()["volatile.cluster.group"]
		if group != "" {
			newMembers := make([]db.NodeInfo, 0, len(allMembers))
			for _, member := range allMembers {
				if !slices.Contains(member.Groups, group) {
					continue
				}

				newMembers = append(newMembers, member)
			}

			allMembers = newMembers
		}

		// Filter offline servers.
		candidateMembers, err = tx.GetCandidateMembers(ctx, allMembers, []int{inst.Architecture()}, "", nil, s.GlobalConfig.OfflineThreshold())
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	// Run instance placement scriptlet if enabled.
	if s.GlobalConfig.InstancesPlacementScriptlet() != "" {
		leaderAddress, err := s.Cluster.LeaderAddress()
		if err != nil {
			return nil, nil, err
		}

		// Copy request so we don't modify it when expanding the config.
		reqExpanded := apiScriptlet.InstancePlacement{
			InstancesPost: api.InstancesPost{
				Name: inst.Name(),
				Type: api.InstanceType(inst.Type().String()),
				InstancePut: api.InstancePut{
					Config:  inst.ExpandedConfig(),
					Devices: inst.ExpandedDevices().CloneNative(),
				},
			},
			Project: inst.Project().Name,
			Reason:  apiScriptlet.InstancePlacementReasonEvacuation,
		}

		reqExpanded.Architecture, err = osarch.ArchitectureName(inst.Architecture())
		if err != nil {
			return nil, nil, fmt.Errorf("Failed getting architecture for instance %q in project %q: %w", inst.Name(), inst.Project().Name, err)
		}

		for _, p := range inst.Profiles() {
			reqExpanded.Profiles = append(reqExpanded.Profiles, p.Name)
		}

		ctx, cancel := context.WithTimeout(ctx, time.Second*5)
		targetMemberInfo, err = scriptlet.InstancePlacementRun(ctx, logger.Log, s, &reqExpanded, candidateMembers, leaderAddress)
		if err != nil {
			cancel()
			return nil, nil, fmt.Errorf("Failed instance placement scriptlet for instance %q in project %q: %w", inst.Name(), inst.Project().Name, err)
		}

		cancel()
	}

	// If target member not specified yet, then find the least loaded cluster member which
	// supports the instance's architecture.
	if targetMemberInfo == nil && len(candidateMembers) > 0 {
		targetMemberInfo = &candidateMembers[0]
	}

	if targetMemberInfo == nil {
		return nil, nil, errors.New("Couldn't find a cluster member for the instance")
	}

	return sourceMemberInfo, targetMemberInfo, nil
}

func autoHealClusterTask(d *Daemon) (task.Func, task.Schedule) {
	f := func(ctx context.Context) {
		s := d.State()
		healingThreshold := s.GlobalConfig.ClusterHealingThreshold()
		if healingThreshold == 0 {
			return // Skip healing if it's disabled.
		}

		leader, err := s.Cluster.LeaderAddress()
		if err != nil {
			if errors.Is(err, cluster.ErrNodeIsNotClustered) {
				return // Skip healing if not clustered.
			}

			logger.Error("Failed to get leader cluster member address", logger.Ctx{"err": err})
			return
		}

		if s.LocalConfig.ClusterAddress() != leader {
			return // Skip healing if not cluster leader.
		}

		var offlineMembers []db.NodeInfo
		{
			var members []db.NodeInfo
			err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				members, err = tx.GetNodes(ctx)
				if err != nil {
					return fmt.Errorf("Failed getting cluster members: %w", err)
				}

				return nil
			})
			if err != nil {
				logger.Error("Failed healing cluster instances", logger.Ctx{"err": err})
				return
			}

			for _, member := range members {
				// Ignore members which have been evacuated, and those which haven't exceeded the
				// healing offline trigger threshold.
				if member.State == db.ClusterMemberStateEvacuated || !member.IsOffline(healingThreshold) {
					continue
				}

				// As an extra safety net, make sure the dead system doesn't still respond on the network.
				hostAddress, _, err := net.SplitHostPort(member.Address)
				if err == nil {
					_, err := subprocess.RunCommand("ping", "-w1", "-c1", "-n", "-q", hostAddress)
					if err == nil {
						// Server isn't fully dead, not risking auto-healing.
						continue
					}
				}

				offlineMembers = append(offlineMembers, member)
			}
		}

		if len(offlineMembers) == 0 {
			return // Skip healing if there are no cluster members to evacuate.
		}

		opRun := func(op *operations.Operation) error {
			for _, member := range offlineMembers {
				err := healClusterMember(d, op, member.Name)
				if err != nil {
					logger.Error("Failed healing cluster instances", logger.Ctx{"server": member.Name, "err": err})
					return err
				}
			}

			return nil
		}

		op, err := operations.OperationCreate(s, "", operations.OperationClassTask, operationtype.ClusterHeal, nil, nil, opRun, nil, nil, nil)
		if err != nil {
			logger.Error("Failed creating cluster instances heal operation", logger.Ctx{"err": err})
			return
		}

		err = op.Start()
		if err != nil {
			logger.Error("Failed starting cluster instances heal operation", logger.Ctx{"err": err})
			return
		}

		err = op.Wait(ctx)
		if err != nil {
			logger.Error("Failed healing cluster instances", logger.Ctx{"err": err})
			return
		}
	}

	return f, task.Every(time.Minute)
}

func healClusterMember(d *Daemon, op *operations.Operation, name string) error {
	s := d.State()

	logger.Info("Starting cluster healing", logger.Ctx{"server": name})
	defer logger.Info("Completed cluster healing", logger.Ctx{"server": name})

	migrateFunc := func(ctx context.Context, s *state.State, inst instance.Instance, sourceMemberInfo *db.NodeInfo, targetMemberInfo *db.NodeInfo, live bool, startInstance bool, metadata map[string]any, op *operations.Operation) error {
		// This returns an error if the instance's storage pool is local.
		// Since we only care about remote backed instances, this can be ignored and return nil instead.
		poolName, err := inst.StoragePool()
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusNotFound) {
				return nil // We only care about remote backed instances.
			}

			return err
		}

		pool, err := storagePools.LoadByName(s, poolName)
		if err != nil {
			return err
		}

		// Ignore anything using a local storage pool.
		if !pool.Driver().Info().Remote {
			return nil
		}

		// Migrate the instance.
		req := api.InstancePost{
			Migration: true,
		}

		dest, err := cluster.Connect(targetMemberInfo.Address, s.Endpoints.NetworkCert(), s.ServerCert(), nil, true)
		if err != nil {
			return err
		}

		dest = dest.UseProject(inst.Project().Name)
		dest = dest.UseTarget(targetMemberInfo.Name)

		migrateOp, err := dest.MigrateInstance(inst.Name(), req)
		if err != nil {
			return err
		}

		err = migrateOp.Wait()
		if err != nil {
			return err
		}

		if !startInstance {
			return nil
		}

		// Start it back up on target.
		startOp, err := dest.UpdateInstanceState(inst.Name(), api.InstanceStatePut{Action: "start"}, "")
		if err != nil {
			return err
		}

		err = startOp.Wait()
		if err != nil {
			return err
		}

		return nil
	}

	// Attempt up to 5 evacuations.
	var err error
	for range 5 {
		err = evacuateClusterMember(context.Background(), s, op, name, "heal", nil, migrateFunc)
		if err == nil {
			s.Events.SendLifecycle(api.ProjectDefaultName, lifecycle.ClusterMemberHealed.Event(name, op.Requestor(), nil))

			return nil
		}
	}

	logger.Error("Failed to heal cluster member", logger.Ctx{"server": name, "err": err})
	return err
}
