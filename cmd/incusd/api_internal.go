package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	runtimeDebug "runtime/debug"
	"slices"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"golang.org/x/sys/unix"

	internalInstance "github.com/lxc/incus/v6/internal/instance"
	"github.com/lxc/incus/v6/internal/jmap"
	"github.com/lxc/incus/v6/internal/revert"
	"github.com/lxc/incus/v6/internal/server/auth"
	"github.com/lxc/incus/v6/internal/server/backup"
	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/db/query"
	"github.com/lxc/incus/v6/internal/server/db/warningtype"
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/internal/server/request"
	"github.com/lxc/incus/v6/internal/server/response"
	"github.com/lxc/incus/v6/internal/server/state"
	storagePools "github.com/lxc/incus/v6/internal/server/storage"
	storageDrivers "github.com/lxc/incus/v6/internal/server/storage/drivers"
	internalSQL "github.com/lxc/incus/v6/internal/sql"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/osarch"
	"github.com/lxc/incus/v6/shared/units"
	"github.com/lxc/incus/v6/shared/util"
)

var apiInternal = []APIEndpoint{
	internalBGPStateCmd,
	internalClusterAcceptCmd,
	internalClusterAssignCmd,
	internalClusterHandoverCmd,
	internalClusterRaftNodeCmd,
	internalClusterRebalanceCmd,
	internalClusterHealCmd,
	internalContainerOnStartCmd,
	internalContainerOnStopCmd,
	internalContainerOnStopNSCmd,
	internalGarbageCollectorCmd,
	internalImageOptimizeCmd,
	internalImageRefreshCmd,
	internalRAFTSnapshotCmd,
	internalReadyCmd,
	internalShutdownCmd,
	internalSQLCmd,
	internalWarningCreateCmd,
}

var internalShutdownCmd = APIEndpoint{
	Path: "shutdown",

	Put: APIEndpointAction{Handler: internalShutdown, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

var internalReadyCmd = APIEndpoint{
	Path: "ready",

	Get: APIEndpointAction{Handler: internalWaitReady, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

var internalContainerOnStartCmd = APIEndpoint{
	Path: "containers/{instanceRef}/onstart",

	Get: APIEndpointAction{Handler: internalContainerOnStart, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

var internalContainerOnStopNSCmd = APIEndpoint{
	Path: "containers/{instanceRef}/onstopns",

	Get: APIEndpointAction{Handler: internalContainerOnStopNS, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

var internalContainerOnStopCmd = APIEndpoint{
	Path: "containers/{instanceRef}/onstop",

	Get: APIEndpointAction{Handler: internalContainerOnStop, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

var internalSQLCmd = APIEndpoint{
	Path: "sql",

	Get:  APIEndpointAction{Handler: internalSQLGet, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
	Post: APIEndpointAction{Handler: internalSQLPost, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

var internalGarbageCollectorCmd = APIEndpoint{
	Path: "gc",

	Get: APIEndpointAction{Handler: internalGC, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

var internalRAFTSnapshotCmd = APIEndpoint{
	Path: "raft-snapshot",

	Get: APIEndpointAction{Handler: internalRAFTSnapshot, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

var internalImageRefreshCmd = APIEndpoint{
	Path: "testing/image-refresh",

	Get: APIEndpointAction{Handler: internalRefreshImage, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

var internalImageOptimizeCmd = APIEndpoint{
	Path: "image-optimize",

	Post: APIEndpointAction{Handler: internalOptimizeImage, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

var internalWarningCreateCmd = APIEndpoint{
	Path: "testing/warnings",

	Post: APIEndpointAction{Handler: internalCreateWarning, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

var internalBGPStateCmd = APIEndpoint{
	Path: "testing/bgp",

	Get: APIEndpointAction{Handler: internalBGPState, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

type internalImageOptimizePost struct {
	Image api.Image `json:"image" yaml:"image"`
	Pool  string    `json:"pool"  yaml:"pool"`
}

type internalWarningCreatePost struct {
	Location       string `json:"location"         yaml:"location"`
	Project        string `json:"project"          yaml:"project"`
	EntityTypeCode int    `json:"entity_type_code" yaml:"entity_type_code"`
	EntityID       int    `json:"entity_id"        yaml:"entity_id"`
	TypeCode       int    `json:"type_code"        yaml:"type_code"`
	Message        string `json:"message"          yaml:"message"`
}

// internalCreateWarning creates a warning, and is used for testing only.
func internalCreateWarning(d *Daemon, r *http.Request) response.Response {
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

	req := internalWarningCreatePost{}
	err = json.NewDecoder(rdr2).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	req.EntityTypeCode, _ = reqRaw.GetInt("entity_type_code")
	req.EntityID, _ = reqRaw.GetInt("entity_id")

	// Check if the entity exists, and fail if it doesn't.
	_, ok := cluster.EntityNames[req.EntityTypeCode]
	if req.EntityTypeCode != -1 && !ok {
		return response.SmartError(fmt.Errorf("Invalid entity type"))
	}

	err = d.State().DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.UpsertWarning(ctx, req.Location, req.Project, req.EntityTypeCode, req.EntityID, warningtype.Type(req.TypeCode), req.Message)
	})
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to create warning: %w", err))
	}

	return response.EmptySyncResponse
}

func internalOptimizeImage(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	req := &internalImageOptimizePost{}

	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	err = imageCreateInPool(s, &req.Image, req.Pool)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func internalRefreshImage(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	err := autoUpdateImages(s.ShutdownCtx, s)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func internalWaitReady(d *Daemon, r *http.Request) response.Response {
	// Check that we're not shutting down.
	isClosing := d.State().ShutdownCtx.Err() != nil
	if isClosing {
		return response.Unavailable(fmt.Errorf("Daemon is shutting down"))
	}

	if d.waitReady.Err() == nil {
		return response.Unavailable(fmt.Errorf("Daemon not ready yet"))
	}

	return response.EmptySyncResponse
}

func internalShutdown(d *Daemon, r *http.Request) response.Response {
	force := request.QueryParam(r, "force")
	logger.Info("Asked to shutdown by API", logger.Ctx{"force": force})

	if d.State().ShutdownCtx.Err() != nil {
		return response.SmartError(api.StatusErrorf(http.StatusTooManyRequests, "Shutdown already in progress"))
	}

	forceCtx, forceCtxCancel := context.WithCancel(context.Background())

	if force == "true" {
		forceCtxCancel() // Don't wait for operations to finish.
	}

	return response.ManualResponse(func(w http.ResponseWriter) error {
		defer forceCtxCancel()

		<-d.setupChan // Wait for daemon to start.

		// Run shutdown sequence synchronously.
		stopErr := d.Stop(forceCtx, unix.SIGPWR)
		err := response.SmartError(stopErr).Render(w)
		if err != nil {
			return err
		}

		// Send the response before the daemon process ends.
		f, ok := w.(http.Flusher)
		if ok {
			f.Flush()
		} else {
			return fmt.Errorf("http.ResponseWriter is not type http.Flusher")
		}

		// Send result of d.Stop() to cmdDaemon so that process stops with correct exit code from Stop().
		go func() {
			<-r.Context().Done() // Wait until request is finished.
			d.shutdownDoneCh <- stopErr
		}()

		return nil
	})
}

// internalContainerHookLoadFromRequestReference loads the container from the instance reference in the request.
// It detects whether the instance reference is an instance ID or instance name and loads instance accordingly.
func internalContainerHookLoadFromReference(s *state.State, r *http.Request) (instance.Instance, error) {
	var inst instance.Instance
	instanceRef, err := url.PathUnescape(mux.Vars(r)["instanceRef"])
	if err != nil {
		return nil, err
	}

	projectName := request.ProjectParam(r)

	instanceID, err := strconv.Atoi(instanceRef)
	if err == nil {
		inst, err = instance.LoadByID(s, instanceID)
		if err != nil {
			return nil, err
		}
	} else {
		inst, err = instance.LoadByProjectAndName(s, projectName, instanceRef)
		if err != nil {
			if api.StatusErrorCheck(err, http.StatusNotFound) {
				return nil, err
			}

			// If DB not available, try loading from backup file.
			logger.Warn("Failed loading instance from database, trying backup file", logger.Ctx{"project": projectName, "instance": instanceRef, "err": err})

			instancePath := filepath.Join(internalUtil.VarPath("containers"), project.Instance(projectName, instanceRef))
			inst, err = instance.LoadFromBackup(s, projectName, instancePath, false)
			if err != nil {
				return nil, err
			}
		}
	}

	if inst.Type() != instancetype.Container {
		return nil, fmt.Errorf("Instance is not container type")
	}

	return inst, nil
}

func internalContainerOnStart(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	inst, err := internalContainerHookLoadFromReference(s, r)
	if err != nil {
		logger.Error("The start hook failed to load", logger.Ctx{"err": err})
		return response.SmartError(err)
	}

	err = inst.OnHook(instance.HookStart, nil)
	if err != nil {
		logger.Error("The start hook failed", logger.Ctx{"instance": inst.Name(), "err": err})
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func internalContainerOnStopNS(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	inst, err := internalContainerHookLoadFromReference(s, r)
	if err != nil {
		logger.Error("The stopns hook failed to load", logger.Ctx{"err": err})
		return response.SmartError(err)
	}

	target := request.QueryParam(r, "target")
	if target == "" {
		target = "unknown"
	}

	netns := request.QueryParam(r, "netns")

	args := map[string]string{
		"target": target,
		"netns":  netns,
	}

	err = inst.OnHook(instance.HookStopNS, args)
	if err != nil {
		logger.Error("The stopns hook failed", logger.Ctx{"instance": inst.Name(), "err": err})
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func internalContainerOnStop(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	inst, err := internalContainerHookLoadFromReference(s, r)
	if err != nil {
		logger.Error("The stop hook failed to load", logger.Ctx{"err": err})
		return response.SmartError(err)
	}

	target := request.QueryParam(r, "target")
	if target == "" {
		target = "unknown"
	}

	args := map[string]string{
		"target": target,
	}

	err = inst.OnHook(instance.HookStop, args)
	if err != nil {
		logger.Error("The stop hook failed", logger.Ctx{"instance": inst.Name(), "err": err})
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

// Perform a database dump.
func internalSQLGet(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	database := r.FormValue("database")

	if !slices.Contains([]string{"local", "global"}, database) {
		return response.BadRequest(fmt.Errorf("Invalid database"))
	}

	schemaFormValue := r.FormValue("schema")
	schemaOnly, err := strconv.Atoi(schemaFormValue)
	if err != nil {
		schemaOnly = 0
	}

	var db *sql.DB
	if database == "global" {
		db = s.DB.Cluster.DB()
	} else {
		db = s.DB.Node.DB()
	}

	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed to start transaction: %w", err))
	}

	defer func() { _ = tx.Rollback() }()

	dump, err := query.Dump(r.Context(), tx, schemaOnly == 1)
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed dump database %s: %w", database, err))
	}

	return response.SyncResponse(true, internalSQL.SQLDump{Text: dump})
}

// Execute queries.
func internalSQLPost(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	req := &internalSQL.SQLQuery{}
	// Parse the request.
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	if !slices.Contains([]string{"local", "global"}, req.Database) {
		return response.BadRequest(fmt.Errorf("Invalid database"))
	}

	if req.Query == "" {
		return response.BadRequest(fmt.Errorf("No query provided"))
	}

	var db *sql.DB
	if req.Database == "global" {
		db = s.DB.Cluster.DB()
	} else {
		db = s.DB.Node.DB()
	}

	batch := internalSQL.SQLBatch{}

	if req.Query == ".sync" {
		d.gateway.Sync()
		return response.SyncResponse(true, batch)
	}

	for _, query := range strings.Split(req.Query, ";") {
		query = strings.TrimLeft(query, " ")

		if query == "" {
			continue
		}

		result := internalSQL.SQLResult{}

		tx, err := db.Begin()
		if err != nil {
			return response.SmartError(err)
		}

		if strings.HasPrefix(strings.ToUpper(query), "SELECT") {
			err = internalSQLSelect(tx, query, &result)
			_ = tx.Rollback()
		} else {
			err = internalSQLExec(tx, query, &result)
			if err != nil {
				_ = tx.Rollback()
			} else {
				err = tx.Commit()
			}
		}
		if err != nil {
			return response.SmartError(err)
		}

		batch.Results = append(batch.Results, result)
	}

	return response.SyncResponse(true, batch)
}

func internalSQLSelect(tx *sql.Tx, query string, result *internalSQL.SQLResult) error {
	result.Type = "select"

	rows, err := tx.Query(query)
	if err != nil {
		return fmt.Errorf("Failed to execute query: %w", err)
	}

	defer func() { _ = rows.Close() }()

	result.Columns, err = rows.Columns()
	if err != nil {
		return fmt.Errorf("Failed to fetch colume names: %w", err)
	}

	for rows.Next() {
		row := make([]any, len(result.Columns))
		rowPointers := make([]any, len(result.Columns))
		for i := range row {
			rowPointers[i] = &row[i]
		}

		err := rows.Scan(rowPointers...)
		if err != nil {
			return fmt.Errorf("Failed to scan row: %w", err)
		}

		for i, column := range row {
			// Convert bytes to string. This is safe as
			// long as we don't have any BLOB column type.
			data, ok := column.([]byte)
			if ok {
				row[i] = string(data)
			}
		}

		result.Rows = append(result.Rows, row)
	}

	err = rows.Err()
	if err != nil {
		return fmt.Errorf("Got a row error: %w", err)
	}

	return nil
}

func internalSQLExec(tx *sql.Tx, query string, result *internalSQL.SQLResult) error {
	result.Type = "exec"
	r, err := tx.Exec(query)
	if err != nil {
		return fmt.Errorf("Failed to exec query: %w", err)
	}

	result.RowsAffected, err = r.RowsAffected()
	if err != nil {
		return fmt.Errorf("Failed to fetch affected rows: %w", err)
	}

	return nil
}

// internalImportFromBackup creates instance, storage pool and volume DB records from an instance's backup file.
// It expects the instance volume to be mounted so that the backup.yaml file is readable.
func internalImportFromBackup(ctx context.Context, s *state.State, projectName string, instName string, allowNameOverride bool) error {
	if instName == "" {
		return fmt.Errorf("The name of the instance is required")
	}

	storagePoolsPath := internalUtil.VarPath("storage-pools")
	storagePoolsDir, err := os.Open(storagePoolsPath)
	if err != nil {
		return err
	}

	// Get a list of all storage pools.
	storagePoolNames, err := storagePoolsDir.Readdirnames(-1)
	if err != nil {
		_ = storagePoolsDir.Close()
		return err
	}

	_ = storagePoolsDir.Close()

	// Check whether the instance exists on any of the storage pools as either a container or a VM.
	instanceMountPoints := []string{}
	instancePoolName := ""
	instanceType := instancetype.Container
	instanceVolType := storageDrivers.VolumeTypeContainer
	instanceDBVolType := db.StoragePoolVolumeTypeContainer

	for _, volType := range []storageDrivers.VolumeType{storageDrivers.VolumeTypeVM, storageDrivers.VolumeTypeContainer} {
		for _, poolName := range storagePoolNames {
			volStorageName := project.Instance(projectName, instName)
			instanceMntPoint := storageDrivers.GetVolumeMountPath(poolName, volType, volStorageName)

			if util.PathExists(instanceMntPoint) {
				instanceMountPoints = append(instanceMountPoints, instanceMntPoint)
				instancePoolName = poolName
				instanceVolType = volType

				if volType == storageDrivers.VolumeTypeVM {
					instanceType = instancetype.VM
					instanceDBVolType = db.StoragePoolVolumeTypeVM
				} else {
					instanceType = instancetype.Container
					instanceDBVolType = db.StoragePoolVolumeTypeContainer
				}
			}
		}
	}

	// Quick checks.
	if len(instanceMountPoints) > 1 {
		return fmt.Errorf(`The instance %q seems to exist on multiple storage pools`, instName)
	} else if len(instanceMountPoints) != 1 {
		return fmt.Errorf(`The instance %q does not seem to exist on any storage pool`, instName)
	}

	// User needs to make sure that we can access the directory where backup.yaml lives.
	instanceMountPoint := instanceMountPoints[0]
	isEmpty, err := internalUtil.PathIsEmpty(instanceMountPoint)
	if err != nil {
		return err
	}

	if isEmpty {
		return fmt.Errorf(`The instance's directory %q appears to be empty. Please ensure that the instance's storage volume is mounted`, instanceMountPoint)
	}

	// Read in the backup.yaml file.
	backupYamlPath := filepath.Join(instanceMountPoint, "backup.yaml")
	backupConf, err := backup.ParseConfigYamlFile(backupYamlPath)
	if err != nil {
		return err
	}

	if allowNameOverride && instName != "" {
		backupConf.Container.Name = instName
	}

	if instName != backupConf.Container.Name {
		return fmt.Errorf("Instance name requested %q doesn't match instance name in backup config %q", instName, backupConf.Container.Name)
	}

	if backupConf.Pool == nil {
		// We don't know what kind of storage type the pool is.
		return fmt.Errorf("No storage pool struct in the backup file found. The storage pool needs to be recovered manually")
	}

	// Try to retrieve the storage pool the instance supposedly lives on.
	pool, err := storagePools.LoadByName(s, instancePoolName)
	if response.IsNotFoundError(err) {
		// Create the storage pool db entry if it doesn't exist.
		_, err = storagePoolDBCreate(ctx, s, instancePoolName, "", backupConf.Pool.Driver, backupConf.Pool.Config)
		if err != nil {
			return fmt.Errorf("Create storage pool database entry: %w", err)
		}

		pool, err = storagePools.LoadByName(s, instancePoolName)
		if err != nil {
			return fmt.Errorf("Load storage pool database entry: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("Find storage pool database entry: %w", err)
	}

	if backupConf.Pool.Name != instancePoolName {
		return fmt.Errorf(`The storage pool %q the instance was detected on does not match the storage pool %q specified in the backup file`, instancePoolName, backupConf.Pool.Name)
	}

	if backupConf.Pool.Driver != pool.Driver().Info().Name {
		return fmt.Errorf(`The storage pool's %q driver %q conflicts with the driver %q recorded in the instance's backup file`, instancePoolName, pool.Driver().Info().Name, backupConf.Pool.Driver)
	}

	// Check snapshots are consistent.
	existingSnapshots, err := pool.CheckInstanceBackupFileSnapshots(backupConf, projectName, false, nil)
	if err != nil {
		return fmt.Errorf("Failed checking snapshots: %w", err)
	}

	// Check if a storage volume entry for the instance already exists.
	var dbVolume *db.StorageVolume
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		dbVolume, err = tx.GetStoragePoolVolume(ctx, pool.ID(), projectName, instanceDBVolType, backupConf.Container.Name, true)
		if err != nil && !response.IsNotFoundError(err) {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	if dbVolume != nil {
		return fmt.Errorf(`Storage volume for instance %q already exists in the database`, backupConf.Container.Name)
	}

	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		// Check if an entry for the instance already exists in the db.
		_, err := tx.GetInstanceID(ctx, projectName, backupConf.Container.Name)

		return err
	})
	if err != nil && !response.IsNotFoundError(err) {
		return err
	}

	if err == nil {
		return fmt.Errorf(`Entry for instance %q already exists in the database`, backupConf.Container.Name)
	}

	if backupConf.Volume == nil {
		return fmt.Errorf(`No storage volume struct in the backup file found. The storage volume needs to be recovered manually`)
	}

	var profiles []api.Profile

	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		profiles, err = tx.GetProfiles(ctx, projectName, backupConf.Container.Profiles)

		return err
	})
	if err != nil {
		return fmt.Errorf("Failed loading profiles (%v) for instance: %w", strings.Join(backupConf.Container.Profiles, ", "), err)
	}

	// Add root device if needed.
	if backupConf.Container.Devices == nil {
		backupConf.Container.Devices = make(map[string]map[string]string, 0)
	}

	if backupConf.Container.ExpandedDevices == nil {
		backupConf.Container.ExpandedDevices = make(map[string]map[string]string, 0)
	}

	internalImportRootDevicePopulate(instancePoolName, backupConf.Container.Devices, backupConf.Container.ExpandedDevices, profiles)

	revert := revert.New()
	defer revert.Fail()

	if backupConf.Container == nil {
		return fmt.Errorf("No instance config in backup config")
	}

	instDBArgs, err := backup.ConfigToInstanceDBArgs(s, backupConf, projectName, true)
	if err != nil {
		return err
	}

	_, instOp, cleanup, err := instance.CreateInternal(s, *instDBArgs, true, true)
	if err != nil {
		return fmt.Errorf("Failed creating instance record: %w", err)
	}

	revert.Add(cleanup)
	defer instOp.Done(err)

	instancePath := storagePools.InstancePath(instanceType, projectName, backupConf.Container.Name, false)
	isPrivileged := false
	if backupConf.Container.Config["security.privileged"] == "" {
		isPrivileged = true
	}

	err = storagePools.CreateContainerMountpoint(instanceMountPoint, instancePath, isPrivileged)
	if err != nil {
		return err
	}

	for _, snap := range existingSnapshots {
		snapInstName := fmt.Sprintf("%s%s%s", backupConf.Container.Name, internalInstance.SnapshotDelimiter, snap.Name)

		snapErr := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			// Check if an entry for the snapshot already exists in the db.
			_, err := tx.GetInstanceSnapshotID(ctx, projectName, backupConf.Container.Name, snap.Name)

			return err
		})
		if snapErr != nil && !response.IsNotFoundError(snapErr) {
			return snapErr
		}

		if snapErr == nil {
			return fmt.Errorf(`Entry for snapshot %q already exists in the database`, snapInstName)
		}

		// Check if a storage volume entry for the snapshot already exists.
		var dbVolume *db.StorageVolume
		err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			dbVolume, err = tx.GetStoragePoolVolume(ctx, pool.ID(), projectName, instanceDBVolType, snapInstName, true)
			if err != nil && !response.IsNotFoundError(err) {
				return err
			}

			return nil
		})
		if err != nil {
			return err
		}

		// If a storage volume entry exists only proceed if force was specified.
		if dbVolume != nil {
			return fmt.Errorf(`Storage volume for snapshot %q already exists in the database`, snapInstName)
		}

		if snapErr == nil {
			err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.DeleteInstance(ctx, projectName, snapInstName)
			})
			if err != nil {
				return err
			}
		}

		if dbVolume != nil {
			err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.RemoveStoragePoolVolume(ctx, projectName, snapInstName, instanceDBVolType, pool.ID())
			})
			if err != nil {
				return err
			}
		}

		baseImage := snap.Config["volatile.base_image"]

		arch, err := osarch.ArchitectureId(snap.Architecture)
		if err != nil {
			return err
		}

		err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			profiles, err = tx.GetProfiles(ctx, projectName, snap.Profiles)

			return err
		})
		if err != nil {
			return fmt.Errorf("Failed loading profiles (%v) for instance snapshot %q: %w", strings.Join(snap.Profiles, ", "), snapInstName, err)
		}

		// Add root device if needed.
		if snap.Devices == nil {
			snap.Devices = make(map[string]map[string]string, 0)
		}

		if snap.ExpandedDevices == nil {
			snap.ExpandedDevices = make(map[string]map[string]string, 0)
		}

		internalImportRootDevicePopulate(instancePoolName, snap.Devices, snap.ExpandedDevices, profiles)

		_, snapInstOp, cleanup, err := instance.CreateInternal(s, db.InstanceArgs{
			Project:      projectName,
			Architecture: arch,
			BaseImage:    baseImage,
			Config:       snap.Config,
			CreationDate: snap.CreatedAt,
			Type:         instanceType,
			Snapshot:     true,
			Devices:      deviceConfig.NewDevices(snap.Devices),
			Ephemeral:    snap.Ephemeral,
			LastUsedDate: snap.LastUsedAt,
			Name:         snapInstName,
			Profiles:     profiles,
			Stateful:     snap.Stateful,
		}, true, true)
		if err != nil {
			return fmt.Errorf("Failed creating instance snapshot record %q: %w", snap.Name, err)
		}

		revert.Add(cleanup)
		defer snapInstOp.Done(err)

		// Recreate missing mountpoints and symlinks.
		volStorageName := project.Instance(projectName, snapInstName)
		snapshotMountPoint := storageDrivers.GetVolumeMountPath(instancePoolName, instanceVolType, volStorageName)
		snapshotPath := storagePools.InstancePath(instanceType, projectName, backupConf.Container.Name, true)
		snapshotTargetPath := storageDrivers.GetVolumeSnapshotDir(instancePoolName, instanceVolType, volStorageName)

		err = storagePools.CreateSnapshotMountpoint(snapshotMountPoint, snapshotTargetPath, snapshotPath)
		if err != nil {
			return err
		}
	}

	revert.Success()
	return nil
}

// internalImportRootDevicePopulate considers the local and expanded devices from backup.yaml as well as the
// expanded devices in the current profiles and if needed will populate localDevices with a new root disk config
// to attempt to maintain the same effective config as specified in backup.yaml. Where possible no new root disk
// device will be added, if the root disk config in the current profiles matches the effective backup.yaml config.
func internalImportRootDevicePopulate(instancePoolName string, localDevices map[string]map[string]string, expandedDevices map[string]map[string]string, profiles []api.Profile) {
	// First, check if localDevices from backup.yaml has a root disk.
	rootName, _, _ := internalInstance.GetRootDiskDevice(localDevices)
	if rootName != "" {
		localDevices[rootName]["pool"] = instancePoolName

		return // Local root disk device has been set to target pool.
	}

	// Next check if expandedDevices from backup.yaml has a root disk.
	expandedRootName, expandedRootConfig, _ := internalInstance.GetRootDiskDevice(expandedDevices)

	// Extract root disk from expanded profile devices.
	profileExpandedDevices := db.ExpandInstanceDevices(deviceConfig.NewDevices(localDevices), profiles)
	profileExpandedRootName, profileExpandedRootConfig, _ := internalInstance.GetRootDiskDevice(profileExpandedDevices.CloneNative())

	// Record whether we need to add a new local disk device.
	addLocalDisk := false

	// We need to add a local root disk if the profiles don't have a root disk.
	if profileExpandedRootName == "" {
		addLocalDisk = true
	} else {
		// Check profile expanded root disk is in the correct pool
		if profileExpandedRootConfig["pool"] != instancePoolName {
			addLocalDisk = true
		} else {
			// Check profile expanded root disk config matches the old expanded disk in backup.yaml.
			// Excluding the "pool" property, which we ignore, as we have already checked the new
			// profile root disk matches the target pool name.
			if expandedRootName != "" {
				for k := range expandedRootConfig {
					if k == "pool" {
						continue // Ignore old pool name.
					}

					if expandedRootConfig[k] != profileExpandedRootConfig[k] {
						addLocalDisk = true
						break
					}
				}

				for k := range profileExpandedRootConfig {
					if k == "pool" {
						continue // Ignore old pool name.
					}

					if expandedRootConfig[k] != profileExpandedRootConfig[k] {
						addLocalDisk = true
						break
					}
				}
			}
		}
	}

	// Add local root disk entry if needed.
	if addLocalDisk {
		rootDev := map[string]string{
			"type": "disk",
			"path": "/",
			"pool": instancePoolName,
		}

		// Inherit any extra root disk config from the expanded root disk from backup.yaml.
		if expandedRootName != "" {
			for k, v := range expandedRootConfig {
				_, found := rootDev[k]
				if !found {
					rootDev[k] = v
				}
			}
		}

		// If there is already a device called "root" in the instance's config, but it does not qualify as
		// a root disk, then try to find a free name for the new root disk device.
		rootDevName := "root"
		for i := 0; i < 100; i++ {
			if localDevices[rootDevName] == nil {
				break
			}

			rootDevName = fmt.Sprintf("root%d", i)
			continue
		}

		localDevices[rootDevName] = rootDev
	}
}

func internalGC(d *Daemon, r *http.Request) response.Response {
	logger.Infof("Started forced garbage collection run")
	runtime.GC()
	runtimeDebug.FreeOSMemory()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	logger.Infof("Heap allocated: %s", units.GetByteSizeStringIEC(int64(m.Alloc), 2))
	logger.Infof("Stack in use: %s", units.GetByteSizeStringIEC(int64(m.StackInuse), 2))
	logger.Infof("Requested from system: %s", units.GetByteSizeStringIEC(int64(m.Sys), 2))
	logger.Infof("Releasable to OS: %s", units.GetByteSizeStringIEC(int64(m.HeapIdle-m.HeapReleased), 2))

	logger.Infof("Completed forced garbage collection run")

	return response.EmptySyncResponse
}

func internalRAFTSnapshot(d *Daemon, r *http.Request) response.Response {
	logger.Warn("Forced RAFT snapshot not supported")

	return response.InternalError(fmt.Errorf("Not supported"))
}

func internalBGPState(d *Daemon, r *http.Request) response.Response {
	s := d.State()

	return response.SyncResponse(true, s.BGP.Debug())
}
