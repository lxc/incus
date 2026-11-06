package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lxc/incus/v7/internal/server/auth"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/db/warningtype"
	"github.com/lxc/incus/v7/internal/server/instance"
	instanceDrivers "github.com/lxc/incus/v7/internal/server/instance/drivers"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/project"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/internal/server/warnings"
	internalUtil "github.com/lxc/incus/v7/internal/util"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
	"github.com/lxc/incus/v7/shared/util"
)

var instancesCmd = APIEndpoint{
	Name: "instances",
	Path: "instances",

	Get:  APIEndpointAction{Handler: instancesGet, AccessHandler: allowAuthenticated},
	Post: APIEndpointAction{Handler: instancesPost, AccessHandler: allowPermission(auth.ObjectTypeProject, auth.EntitlementCanCreateInstances), LargeRequest: true},
	Put:  APIEndpointAction{Handler: instancesPut, AccessHandler: allowAuthenticated},
}

var instanceCmd = APIEndpoint{
	Name: "instance",
	Path: "instances/{name}",

	Get:    APIEndpointAction{Handler: instanceGet, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanView, "name")},
	Put:    APIEndpointAction{Handler: instancePut, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanEdit, "name")},
	Delete: APIEndpointAction{Handler: instanceDelete, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanEdit, "name")},
	Post:   APIEndpointAction{Handler: instancePost, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanEdit, "name")},
	Patch:  APIEndpointAction{Handler: instancePatch, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanEdit, "name")},
}

var instanceRebuildCmd = APIEndpoint{
	Name: "instanceRebuild",
	Path: "instances/{name}/rebuild",

	Post: APIEndpointAction{Handler: instanceRebuildPost, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanEdit, "name")},
}

var instanceStateCmd = APIEndpoint{
	Name: "instanceState",
	Path: "instances/{name}/state",

	Get: APIEndpointAction{Handler: instanceState, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanView, "name")},
	Put: APIEndpointAction{Handler: instanceStatePut, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanUpdateState, "name")},
}

var instanceSFTPCmd = APIEndpoint{
	Name: "instanceFile",
	Path: "instances/{name}/sftp",

	Get: APIEndpointAction{Handler: instanceSFTPHandler, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanConnectSFTP, "name")},
}

var instanceNBDCmd = APIEndpoint{
	Name: "instanceNBD",
	Path: "instances/{name}/nbd",

	Get: APIEndpointAction{Handler: instanceNBDHandler, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanConnectNBD, "name")},
}

var instancePortForwardCmd = APIEndpoint{
	Name: "instancePortForward",
	Path: "instances/{name}/port-forward",

	Post: APIEndpointAction{Handler: instancePortForwardHandler, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanConnectTCP, "name")},
}

var instanceFileCmd = APIEndpoint{
	Name: "instanceFile",
	Path: "instances/{name}/files",

	Get:    APIEndpointAction{Handler: instanceFileHandler, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanAccessFiles, "name")},
	Head:   APIEndpointAction{Handler: instanceFileHandler, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanAccessFiles, "name")},
	Post:   APIEndpointAction{Handler: instanceFileHandler, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanAccessFiles, "name"), LargeRequest: true},
	Delete: APIEndpointAction{Handler: instanceFileHandler, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanAccessFiles, "name")},
}

var instanceSnapshotsCmd = APIEndpoint{
	Name: "instanceSnapshots",
	Path: "instances/{name}/snapshots",

	Get:  APIEndpointAction{Handler: instanceSnapshotsGet, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanView, "name")},
	Post: APIEndpointAction{Handler: instanceSnapshotsPost, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanManageSnapshots, "name")},
}

var instanceSnapshotCmd = APIEndpoint{
	Name: "instanceSnapshot",
	Path: "instances/{name}/snapshots/{snapshotName}",

	Get:    APIEndpointAction{Handler: instanceSnapshotHandler, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanView, "name")},
	Post:   APIEndpointAction{Handler: instanceSnapshotHandler, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanManageSnapshots, "name")},
	Delete: APIEndpointAction{Handler: instanceSnapshotHandler, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanManageSnapshots, "name")},
	Patch:  APIEndpointAction{Handler: instanceSnapshotHandler, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanManageSnapshots, "name")},
	Put:    APIEndpointAction{Handler: instanceSnapshotHandler, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanManageSnapshots, "name")},
}

var instanceConsoleCmd = APIEndpoint{
	Name: "instanceConsole",
	Path: "instances/{name}/console",

	Get:    APIEndpointAction{Handler: instanceConsoleLogGet, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanView, "name")},
	Post:   APIEndpointAction{Handler: instanceConsolePost, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanAccessConsole, "name")},
	Delete: APIEndpointAction{Handler: instanceConsoleLogDelete, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanEdit, "name")},
}

var instanceExecCmd = APIEndpoint{
	Name: "instanceExec",
	Path: "instances/{name}/exec",

	Post: APIEndpointAction{Handler: instanceExecPost, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanExec, "name")},
}

var instanceMetadataCmd = APIEndpoint{
	Name: "instanceMetadata",
	Path: "instances/{name}/metadata",

	Get:   APIEndpointAction{Handler: instanceMetadataGet, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanView, "name")},
	Patch: APIEndpointAction{Handler: instanceMetadataPatch, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanEdit, "name")},
	Put:   APIEndpointAction{Handler: instanceMetadataPut, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanEdit, "name")},
}

var instanceMetadataTemplatesCmd = APIEndpoint{
	Name: "instanceMetadataTemplates",
	Path: "instances/{name}/metadata/templates",

	Get:    APIEndpointAction{Handler: instanceMetadataTemplatesGet, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanView, "name")},
	Post:   APIEndpointAction{Handler: instanceMetadataTemplatesPost, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanEdit, "name")},
	Delete: APIEndpointAction{Handler: instanceMetadataTemplatesDelete, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanEdit, "name")},
}

var instanceBackupsCmd = APIEndpoint{
	Name: "instanceBackups",
	Path: "instances/{name}/backups",

	Get:  APIEndpointAction{Handler: instanceBackupsGet, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanView, "name")},
	Post: APIEndpointAction{Handler: instanceBackupsPost, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanManageBackups, "name")},
}

var instanceBackupCmd = APIEndpoint{
	Name: "instanceBackup",
	Path: "instances/{name}/backups/{backupName}",

	Get:    APIEndpointAction{Handler: instanceBackupGet, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanView, "name")},
	Post:   APIEndpointAction{Handler: instanceBackupPost, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanManageBackups, "name")},
	Delete: APIEndpointAction{Handler: instanceBackupDelete, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanManageBackups, "name")},
}

var instanceBackupExportCmd = APIEndpoint{
	Name: "instanceBackupExport",
	Path: "instances/{name}/backups/{backupName}/export",

	Get: APIEndpointAction{Handler: instanceBackupExportGet, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanManageBackups, "name")},
}

var instanceBitmapsCmd = APIEndpoint{
	Name: "instanceBitmaps",
	Path: "instances/{name}/bitmaps",

	Post: APIEndpointAction{Handler: instanceBitmapsPost, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanEdit, "name")},
}

var instanceAccessCmd = APIEndpoint{
	Name: "access",
	Path: "instances/{name}/access",

	Get: APIEndpointAction{Handler: instanceAccess, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanView, "name")},
}

var instanceDebugMemoryCmd = APIEndpoint{
	Name: "instanceDebugMemory",
	Path: "instances/{name}/debug/memory",

	Get: APIEndpointAction{Handler: instanceDebugMemoryGet, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanEdit, "name")},
}

var instanceDebugRepairCmd = APIEndpoint{
	Name: "instanceDebugRepair",
	Path: "instances/{name}/debug/repair",

	Post: APIEndpointAction{Handler: instanceDebugRepairPost, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanEdit, "name")},
}

var instanceNVRAMCmd = APIEndpoint{
	Name: "instanceNVRAM",
	Path: "instances/{name}/nvram",

	Get:   APIEndpointAction{Handler: instanceNVRAMGet, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanView, "name")},
	Patch: APIEndpointAction{Handler: instanceNVRAMPatch, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanEdit, "name")},
}

var instanceNVRAMGUIDCmd = APIEndpoint{
	Name: "instanceNVRAM",
	Path: "instances/{name}/nvram/{guid}",

	Get: APIEndpointAction{Handler: instanceNVRAMGUIDGet, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanView, "name")},
}

var instanceNVRAMGUIDVarCmd = APIEndpoint{
	Name: "instanceNVRAM",
	Path: "instances/{name}/nvram/{guid}/{var}",

	Delete: APIEndpointAction{Handler: instanceNVRAMGUIDVarDelete, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanEdit, "name")},
	Get:    APIEndpointAction{Handler: instanceNVRAMGUIDVarGet, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanView, "name")},
	Put:    APIEndpointAction{Handler: instanceNVRAMGUIDVarPut, AccessHandler: allowPermission(auth.ObjectTypeInstance, auth.EntitlementCanEdit, "name")},
}

type instanceAutostartList []instance.Instance

func (slice instanceAutostartList) Len() int {
	return len(slice)
}

func (slice instanceAutostartList) Less(i, j int) bool {
	iOrder := slice[i].ExpandedConfig()["boot.autostart.priority"]
	jOrder := slice[j].ExpandedConfig()["boot.autostart.priority"]

	if iOrder != jOrder {
		iOrderInt, _ := strconv.Atoi(iOrder)
		jOrderInt, _ := strconv.Atoi(jOrder)
		return iOrderInt > jOrderInt
	}

	return slice[i].Name() < slice[j].Name()
}

func (slice instanceAutostartList) Swap(i, j int) {
	slice[i], slice[j] = slice[j], slice[i]
}

var instancesStartMu sync.Mutex

// bulkStartInstances splits instances into those that can be started in parallel
// and those that must be started sequentially.
func bulkStartInstances(instances []instance.Instance) ([]instance.Instance, []instance.Instance) {
	var bulkStart, rest []instance.Instance
	for _, inst := range instances {
		config := inst.ExpandedConfig()
		autoStartDelay := config["boot.autostart.delay"]
		autoStartPriority := config["boot.autostart.priority"]

		if autoStartDelay == "" && autoStartPriority == "" {
			bulkStart = append(bulkStart, inst)
		} else {
			rest = append(rest, inst)
		}
	}

	return bulkStart, rest
}

// instanceShouldAutoStart returns whether the instance should be auto-started.
// Returns true if boot.autostart is enabled or boot.autostart is not set and instance was previously running.
func instanceShouldAutoStart(inst instance.Instance) bool {
	config := inst.ExpandedConfig()
	autoStart := config["boot.autostart"]
	lastState := config["volatile.last_state.power"]

	return util.IsTrue(autoStart) || ((autoStart == "" || autoStart == "last-state") && lastState == instance.PowerStateRunning)
}

// instanceStart starts a single instance and returns whether it got started.
func instanceStart(s *state.State, inst instance.Instance) bool {
	// Let's make up to 3 attempts to start instances.
	maxAttempts := 3

	if !instanceShouldAutoStart(inst) {
		return false
	}

	// If already running, we're done.
	if inst.IsRunning() {
		return false
	}

	// Get the instance config.
	config := inst.ExpandedConfig()
	shutdownAction := config["boot.host_shutdown_action"]

	instLogger := logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})

	// Try to start the instance.
	attempt := 0
	for {
		attempt++

		var err error
		if shutdownAction == "stateful-stop" {
			// Attempt to restore state.
			err = inst.Start(true)
		} else {
			// Normal startup.
			err = inst.Start(false)
		}

		if err != nil {
			if api.StatusErrorCheck(err, http.StatusServiceUnavailable) {
				return false // Don't log or retry instances that are not ready to start yet.
			}

			instLogger.Warn("Failed auto start instance attempt", logger.Ctx{"attempt": attempt, "maxAttempts": maxAttempts, "err": err})

			if attempt >= maxAttempts {
				warnErr := s.DB.Cluster.Transaction(s.ShutdownCtx, func(ctx context.Context, tx *db.ClusterTx) error {
					// If unable to start after 3 tries, record a warning.
					return tx.UpsertWarning(ctx, s.ServerName, inst.Project().Name, cluster.TypeInstance, inst.ID(), warningtype.InstanceAutostartFailure, fmt.Sprintf("%v", err))
				})
				if warnErr != nil {
					instLogger.Warn("Failed to create instance autostart failure warning", logger.Ctx{"err": warnErr})
				}

				instLogger.Error("Failed to auto start instance", logger.Ctx{"err": err})

				return false
			}

			time.Sleep(5 * time.Second)

			continue
		}

		// Resolve any previous warning.
		warnErr := warnings.ResolveWarningsByNodeAndProjectAndTypeAndEntity(s.DB.Cluster, s.ServerName, inst.Project().Name, warningtype.InstanceAutostartFailure, cluster.TypeInstance, inst.ID())
		if warnErr != nil {
			instLogger.Warn("Failed to resolve instance autostart failure warning", logger.Ctx{"err": warnErr})
		}

		return true
	}
}

// instanceAutostartDelay waits for the boot.autostart.delay of the instance.
func instanceAutostartDelay(ctx context.Context, inst instance.Instance) {
	delay, err := strconv.Atoi(inst.ExpandedConfig()["boot.autostart.delay"])
	if err != nil || delay <= 0 {
		return
	}

	select {
	case <-ctx.Done():
	case <-time.After(time.Duration(delay) * time.Second):
	}
}

// concurrentInstanceActions runs fn on every instance, limit bounds how many run at once.
// Each instance runs to completion regardless of other failures, all of which are reported at the end.
func concurrentInstanceActions(limit chan struct{}, instances []instance.Instance, prefix string, fn func(inst instance.Instance) error) error {
	var wg sync.WaitGroup
	var failuresLock sync.Mutex
	failures := []string{}

	for _, inst := range instances {
		wg.Go(func() {
			limit <- struct{}{}
			defer func() { <-limit }()

			err := fn(inst)
			if err != nil {
				failuresLock.Lock()
				failures = append(failures, fmt.Sprintf("%s/%s: %v", inst.Project().Name, inst.Name(), err))
				failuresLock.Unlock()
			}
		})
	}

	wg.Wait()

	if len(failures) > 0 {
		sort.Strings(failures)

		return fmt.Errorf("%s:\n - %s", prefix, strings.Join(failures, "\n - "))
	}

	return nil
}

// instancesPerProject runs fn on the instances of each project in parallel.
// The boot order only applies within a project so that one project can't hold up another.
func instancesPerProject(instances []instance.Instance, fn func(projectInstances []instance.Instance) error) error {
	projects := map[string][]instance.Instance{}
	for _, inst := range instances {
		projects[inst.Project().Name] = append(projects[inst.Project().Name], inst)
	}

	var wg sync.WaitGroup
	var failuresLock sync.Mutex
	var failures []error

	for _, projectInstances := range projects {
		wg.Go(func() {
			err := fn(projectInstances)
			if err != nil {
				failuresLock.Lock()
				failures = append(failures, err)
				failuresLock.Unlock()
			}
		})
	}

	wg.Wait()

	return errors.Join(failures...)
}

// instancesInStartOrder runs fn on the instances in startup order, fn returns whether it started the instance.
// The delay of a started instance is held without using up the limit and is skipped if nothing follows it.
func instancesInStartOrder(ctx context.Context, limit chan struct{}, instances []instance.Instance, prefix string, fn func(inst instance.Instance) (bool, error)) error {
	bulkInstances, sequentialInstances := bulkStartInstances(instances)

	// Start instances that support bulk startup.
	var failures []error
	err := concurrentInstanceActions(limit, bulkInstances, prefix, func(inst instance.Instance) error {
		_, err := fn(inst)

		return err
	})
	if err != nil {
		failures = append(failures, err)
	}

	// Sort based on instance boot priority.
	sort.Sort(instanceAutostartList(sequentialInstances))

	for i, inst := range sequentialInstances {
		if ctx.Err() != nil {
			failures = append(failures, ctx.Err())
			break
		}

		var started bool
		err := concurrentInstanceActions(limit, []instance.Instance{inst}, prefix, func(inst instance.Instance) error {
			var err error
			started, err = fn(inst)

			return err
		})
		if err != nil {
			failures = append(failures, err)
		}

		if started && i < len(sequentialInstances)-1 {
			instanceAutostartDelay(ctx, inst)
		}
	}

	return errors.Join(failures...)
}

func instancesStart(s *state.State, instances []instance.Instance) {
	// Check if the cluster is currently evacuated.
	if s.ServerClustered && s.DB.Cluster.LocalNodeIsEvacuated() {
		return
	}

	// Acquire startup lock.
	instancesStartMu.Lock()
	defer instancesStartMu.Unlock()

	// Limit the number of concurrent tasks.
	limit := make(chan struct{}, max(runtime.NumCPU()/4, 1))

	_ = instancesPerProject(instances, func(projectInstances []instance.Instance) error {
		return instancesInStartOrder(s.ShutdownCtx, limit, projectInstances, "Failed to start instances", func(inst instance.Instance) (bool, error) {
			return instanceStart(s, inst), nil
		})
	})
}

type instanceStopList []instance.Instance

func (slice instanceStopList) Len() int {
	return len(slice)
}

func (slice instanceStopList) Less(i, j int) bool {
	iOrder := slice[i].ExpandedConfig()["boot.stop.priority"]
	jOrder := slice[j].ExpandedConfig()["boot.stop.priority"]

	if iOrder != jOrder {
		iOrderInt, _ := strconv.Atoi(iOrder)
		jOrderInt, _ := strconv.Atoi(jOrder)
		return iOrderInt > jOrderInt // check this line (prob <)
	}

	return slice[i].Name() < slice[j].Name()
}

func (slice instanceStopList) Swap(i, j int) {
	slice[i], slice[j] = slice[j], slice[i]
}

// Return all local instances on disk (if instance is running, it will attempt to populate the instance's local
// and expanded config using the backup.yaml file). It will clear the instance's profiles property to avoid needing
// to enrich them from the database.
func instancesOnDisk(s *state.State) ([]instance.Instance, error) {
	var err error

	instancePaths := map[instancetype.Type]string{
		instancetype.Container: internalUtil.VarPath("containers"),
		instancetype.VM:        internalUtil.VarPath("virtual-machines"),
	}

	instanceTypeNames := make(map[instancetype.Type][]os.DirEntry, 2)

	instanceTypeNames[instancetype.Container], err = os.ReadDir(instancePaths[instancetype.Container])
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	instanceTypeNames[instancetype.VM], err = os.ReadDir(instancePaths[instancetype.VM])
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	instances := make([]instance.Instance, 0, len(instanceTypeNames[instancetype.Container])+len(instanceTypeNames[instancetype.VM]))
	for instanceType, instanceNames := range instanceTypeNames {
		for _, file := range instanceNames {
			// Convert file name to project name and instance name.
			projectName, instanceName := project.InstanceParts(file.Name())

			inst, err := instanceLoadFromDisk(s, instanceType, projectName, filepath.Join(instancePaths[instanceType], file.Name()))
			if err != nil {
				logger.Warn("Failed loading instance", logger.Ctx{"project": projectName, "instance": instanceName, "err": err})
				continue
			}

			instances = append(instances, inst)
		}
	}

	return instances, nil
}

// instanceLoadFromDisk loads an instance from its backup file, falling back to a basic config without the database.
func instanceLoadFromDisk(s *state.State, instanceType instancetype.Type, projectName string, instancePath string) (instance.Instance, error) {
	_, instanceName := project.InstanceParts(filepath.Base(instancePath))

	// Try and parse the backup file (if instance is running).
	// This allows us to stop VMs which require access to the vsock ID and volatile UUID.
	// Also generally it ensures that all devices are stopped cleanly too.
	backupYamlPath := filepath.Join(instancePath, "backup.yaml")
	if util.PathExists(backupYamlPath) {
		inst, err := instance.LoadFromBackup(s, projectName, instancePath, false)
		if err == nil {
			return inst, nil
		}

		logger.Warn("Failed loading instance from backup file", logger.Ctx{"project": projectName, "instance": instanceName, "backup_file": backupYamlPath, "err": err})
	}

	// Initialize dbArgs with a very basic config.
	// This will not be sufficient to stop an instance cleanly.
	instDBArgs := &db.InstanceArgs{
		Type:    instanceType,
		Project: projectName,
		Name:    instanceName,
		Config:  make(map[string]string),
	}

	emptyProject := api.Project{
		Name: projectName,
	}

	return instance.Load(s, *instDBArgs, emptyProject)
}

// instanceShutdown stops a single instance as part of the server shutdown.
func instanceShutdown(s *state.State, inst instance.Instance, forceCh <-chan struct{}, forceTimeout time.Duration) {
	// Determine how long to wait for the instance to shutdown cleanly.
	timeoutSeconds := 30
	value, ok := inst.ExpandedConfig()["boot.host_shutdown_timeout"]
	if ok {
		timeoutSeconds, _ = strconv.Atoi(value)
	}

	timeout := time.Second * time.Duration(timeoutSeconds)

	// Record a forced stop, blocking the clean stop path from resolving the warning.
	var forcedMu sync.Mutex
	forced := false
	markForced := func(message string) {
		forcedMu.Lock()
		defer forcedMu.Unlock()

		forced = true
		instanceForcedStopWarn(s, inst, message)
	}

	// Shutdown the instance without waiting past the timeouts on an unresponsive instance.
	done := make(chan struct{})
	go func() {
		defer close(done)

		// Save and restore the ephemeral bit (if DB is available).
		if inst.ID() > 0 {
			ephemeral := inst.IsEphemeral()
			if ephemeral {
				// Unset ephemeral flag if present.
				args := db.InstanceArgs{
					Architecture: inst.Architecture(),
					Config:       inst.LocalConfig(),
					Description:  inst.Description(),
					Devices:      inst.LocalDevices(),
					Ephemeral:    false,
					Profiles:     inst.Profiles(),
					Project:      inst.Project().Name,
					Type:         inst.Type(),
					Snapshot:     inst.IsSnapshot(),
				}

				err := inst.Update(args, false)
				if err == nil {
					// On function return, set the flag back on.
					defer func() {
						args.Ephemeral = ephemeral
						_ = inst.Update(args, false)
					}()
				}
			}
		}

		// Perform the shutdown action.
		action := inst.ExpandedConfig()["boot.host_shutdown_action"]

		// Regular shutdown with a forced stop as fallback.
		shutdown := func() {
			err := inst.Shutdown(timeout)
			if err != nil {
				logger.Warn("Failed shutting down instance, forcefully stopping", logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "timeout": timeout, "err": err})
				markForced(fmt.Sprintf("Didn't shut down within %s during server shutdown", timeout))
				err = inst.Stop(false)
				if err != nil {
					logger.Warn("Failed forcefully stopping instance", logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "err": err})
				}
			}
		}

		switch action {
		case "stateful-stop":
			err := inst.Stop(true)
			if err != nil {
				logger.Warn("Failed statefully stopping instance, shutting down instead", logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "err": err})
				shutdown()
			}

		case "force-stop":
			err := inst.Stop(false)
			if err != nil {
				logger.Warn("Failed forcefully stopping instance", logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "err": err})
			}

		default:
			shutdown()
		}

		// Clear any earlier warning once the configured action completed on its own.
		forcedMu.Lock()
		defer forcedMu.Unlock()

		if !forced {
			instanceForcedStopResolve(s, inst)
		}
	}()

	wedged := false
	select {
	case <-done:
	case <-time.After(timeout + forceTimeout):
		logger.Error("Timed out stopping instance, forcefully stopping it", logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "timeout": timeout})
		markForced(fmt.Sprintf("Stop didn't complete within %s during server shutdown", timeout+forceTimeout))
		wedged = true
	case <-forceCh:
		logger.Warn("Forced shutdown, forcefully stopping instance", logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})
		markForced("Server shutdown was forced before the instance stopped")
		wedged = true
	}

	// Take over the wedged stop without waiting on it any further.
	if wedged {
		go func() {
			err := inst.Stop(false)
			if err != nil {
				logger.Error("Failed forcefully stopping instance", logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "err": err})
			}
		}()
	}

	if inst.ID() > 0 {
		// If DB was available then the instance shutdown process will have set
		// the last power state to STOPPED, so set that back to RUNNING so that
		// when the daemon restarts the instance will be started again.
		_ = inst.VolatileSet(map[string]string{"volatile.last_state.power": instance.PowerStateRunning})
	}
}

// instancesInStopOrder runs fn on the instances in shutdown order, finishing each priority before the next.
func instancesInStopOrder(limit chan struct{}, instances []instance.Instance, prefix string, fn func(inst instance.Instance) error) error {
	sorted := slices.Clone(instances)
	sort.Sort(instanceStopList(sorted))

	stopPriority := func(inst instance.Instance) int {
		priority, _ := strconv.Atoi(inst.ExpandedConfig()["boot.stop.priority"])

		return priority
	}

	var failures []error
	for len(sorted) > 0 {
		priority := stopPriority(sorted[0])
		end := slices.IndexFunc(sorted, func(inst instance.Instance) bool {
			return stopPriority(inst) != priority
		})
		if end < 0 {
			end = len(sorted)
		}

		logger.Info("Stopping instances", logger.Ctx{"project": sorted[0].Project().Name, "stopPriority": priority})

		err := concurrentInstanceActions(limit, sorted[:end], prefix, fn)
		if err != nil {
			failures = append(failures, err)
		}

		sorted = sorted[end:]
	}

	return errors.Join(failures...)
}

func instancesShutdown(ctx context.Context, s *state.State, instances []instance.Instance, forceTimeout time.Duration) {
	// A shutdown forced from the start still gets the regular timeouts.
	forceCh := ctx.Done()
	if ctx.Err() != nil {
		forceCh = nil
	}

	// Skip stopped instances.
	running := slices.DeleteFunc(slices.Clone(instances), func(inst instance.Instance) bool {
		return !inst.IsRunning()
	})

	// Limit shutdown concurrency to the number of CPU cores.
	limit := make(chan struct{}, runtime.NumCPU())

	_ = instancesPerProject(running, func(projectInstances []instance.Instance) error {
		return instancesInStopOrder(limit, projectInstances, "Failed to stop instances", func(inst instance.Instance) error {
			instanceShutdown(s, inst, forceCh, forceTimeout)

			return nil
		})
	})
}

// instanceShutdownDefaultTimeout is the default timeout (in seconds) to wait for a clean shutdown.
const instanceShutdownDefaultTimeout = 30

// instanceShutdownOrForceStop cleanly shuts the instance down, falling back to a forced stop if that fails.
func instanceShutdownOrForceStop(s *state.State, inst instance.Instance, during string) error {
	val, err := strconv.Atoi(inst.ExpandedConfig()["boot.host_shutdown_timeout"])
	if err != nil {
		val = instanceShutdownDefaultTimeout
	}

	timeout := time.Duration(val) * time.Second

	err = inst.Shutdown(timeout)
	if err != nil {
		logger.Warn("Failed shutting down instance, forcing stop", logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "timeout": timeout, "err": err})
		instanceForcedStopWarn(s, inst, fmt.Sprintf("Didn't shut down within %s during %s", timeout, during))

		err = inst.Stop(false)
		if err != nil && !errors.Is(err, instanceDrivers.ErrInstanceIsStopped) {
			return fmt.Errorf("Failed to stop instance %q in project %q: %w", inst.Name(), inst.Project().Name, err)
		}

		return nil
	}

	instanceForcedStopResolve(s, inst)

	return nil
}

// instanceForcedStopWarn records that the instance had to be forcefully stopped.
func instanceForcedStopWarn(s *state.State, inst instance.Instance, message string) {
	if inst.ID() <= 0 {
		return
	}

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.UpsertWarning(ctx, inst.Location(), inst.Project().Name, cluster.TypeInstance, inst.ID(), warningtype.InstanceForcedStop, message)
	})
	if err != nil {
		logger.Warn("Failed to record forced stop warning", logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "err": err})
	}
}

// instanceForcedStopResolve resolves any forced stop warning after the instance stopped cleanly.
func instanceForcedStopResolve(s *state.State, inst instance.Instance) {
	if inst.ID() <= 0 {
		return
	}

	err := warnings.ResolveWarningsByNodeAndProjectAndTypeAndEntity(s.DB.Cluster, inst.Location(), inst.Project().Name, warningtype.InstanceForcedStop, cluster.TypeInstance, inst.ID())
	if err != nil {
		logger.Warn("Failed to resolve forced stop warning", logger.Ctx{"project": inst.Project().Name, "instance": inst.Name(), "err": err})
	}
}
