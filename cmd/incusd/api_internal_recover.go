package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	internalInstance "github.com/lxc/incus/internal/instance"
	internalRecover "github.com/lxc/incus/internal/recover"
	"github.com/lxc/incus/internal/revert"
	"github.com/lxc/incus/internal/server/auth"
	"github.com/lxc/incus/internal/server/backup"
	backupConfig "github.com/lxc/incus/internal/server/backup/config"
	"github.com/lxc/incus/internal/server/cluster"
	"github.com/lxc/incus/internal/server/db"
	dbCluster "github.com/lxc/incus/internal/server/db/cluster"
	deviceConfig "github.com/lxc/incus/internal/server/device/config"
	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/instance/instancetype"
	"github.com/lxc/incus/internal/server/project"
	"github.com/lxc/incus/internal/server/response"
	"github.com/lxc/incus/internal/server/state"
	storagePools "github.com/lxc/incus/internal/server/storage"
	storageDrivers "github.com/lxc/incus/internal/server/storage/drivers"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/osarch"
	"github.com/lxc/incus/shared/util"
)

// Define API endpoints for recover actions.
var internalRecoverValidateCmd = APIEndpoint{
	Path: "recover/validate",

	Post: APIEndpointAction{Handler: internalRecoverValidate, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

var internalRecoverImportCmd = APIEndpoint{
	Path: "recover/import",

	Post: APIEndpointAction{Handler: internalRecoverImport, AccessHandler: allowPermission(auth.ObjectTypeServer, auth.EntitlementCanEdit)},
}

// init recover adds API endpoints to handler slice.
func init() {
	apiInternal = append(apiInternal, internalRecoverValidateCmd, internalRecoverImportCmd)
}

// internalRecoverScan provides the discovery and import functionality for both recovery validate and import steps.
func internalRecoverScan(s *state.State, userPools []api.StoragePoolsPost, validateOnly bool) response.Response {
	var err error
	var projects map[string]*api.Project
	var projectProfiles map[string][]*api.Profile
	var projectNetworks map[string]map[int64]api.Network

	// Retrieve all project, profile and network info in a single transaction so we can use it for all
	// imported instances and volumes, and avoid repeatedly querying the same information.
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Load list of projects for validation.
		ps, err := dbCluster.GetProjects(ctx, tx.Tx())
		if err != nil {
			return err
		}

		// Convert to map for lookups by name later.
		projects = make(map[string]*api.Project, len(ps))
		for i := range ps {
			project, err := ps[i].ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			projects[ps[i].Name] = project
		}

		// Load list of project/profile names for validation.
		profiles, err := dbCluster.GetProfiles(ctx, tx.Tx())
		if err != nil {
			return err
		}

		// Convert to map for lookups by project name later.
		projectProfiles = make(map[string][]*api.Profile)
		for _, profile := range profiles {
			if projectProfiles[profile.Project] == nil {
				projectProfiles[profile.Project] = []*api.Profile{}
			}

			apiProfile, err := profile.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			projectProfiles[profile.Project] = append(projectProfiles[profile.Project], apiProfile)
		}

		// Load list of project/network names for validation.
		projectNetworks, err = tx.GetCreatedNetworks(ctx)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return response.SmartError(fmt.Errorf("Failed getting validate dependency check info: %w", err))
	}

	isClustered, err := cluster.Enabled(s.DB.Node)
	if err != nil {
		return response.SmartError(err)
	}

	res := internalRecover.ValidateResult{}

	revert := revert.New()
	defer revert.Fail()

	// addDependencyError adds an error to the list of dependency errors if not already present in list.
	addDependencyError := func(err error) {
		errStr := err.Error()

		if !util.ValueInSlice(errStr, res.DependencyErrors) {
			res.DependencyErrors = append(res.DependencyErrors, errStr)
		}
	}

	// Used to store the unknown volumes for each pool & project.
	poolsProjectVols := make(map[string]map[string][]*backupConfig.Config)

	// Used to store a handle to each pool containing user supplied config.
	pools := make(map[string]storagePools.Pool)

	// Iterate the pools finding unknown volumes and perform validation.
	for _, p := range userPools {
		pool, err := storagePools.LoadByName(s, p.Name)
		if err != nil {
			if response.IsNotFoundError(err) {
				// If the pool DB record doesn't exist, and we are clustered, then don't proceed
				// any further as we do not support pool DB record recovery when clustered.
				if isClustered {
					return response.BadRequest(fmt.Errorf("Storage pool recovery not supported when clustered"))
				}

				// If pool doesn't exist in DB, initialise a temporary pool with the supplied info.
				poolInfo := api.StoragePool{
					Name:           p.Name,
					Driver:         p.Driver,
					StoragePoolPut: p.StoragePoolPut,
					Status:         api.StoragePoolStatusCreated,
				}

				pool, err = storagePools.NewTemporary(s, &poolInfo)
				if err != nil {
					return response.SmartError(fmt.Errorf("Failed to initialise unknown pool %q: %w", p.Name, err))
				}

				// Populate configuration with default values.
				err := pool.Driver().FillConfig()
				if err != nil {
					return response.SmartError(fmt.Errorf("Failed to evaluate the default configuration values for unknown pool %q: %w", p.Name, err))
				}

				err = pool.Driver().Validate(poolInfo.Config)
				if err != nil {
					return response.SmartError(fmt.Errorf("Failed config validation for unknown pool %q: %w", p.Name, err))
				}
			} else {
				return response.SmartError(fmt.Errorf("Failed loading existing pool %q: %w", p.Name, err))
			}
		}

		// Record this pool to be used during import stage, assuming validation passes.
		pools[p.Name] = pool

		// Try to mount the pool.
		ourMount, err := pool.Mount()
		if err != nil {
			return response.SmartError(fmt.Errorf("Failed mounting pool %q: %w", pool.Name(), err))
		}

		// Unmount pool when done if not existing in DB after function has finished.
		// This way if we are dealing with an existing pool or have successfully created the DB record then
		// we won't unmount it. As we should leave successfully imported pools mounted.
		if ourMount {
			defer func() {
				cleanupPool := pools[pool.Name()]
				if cleanupPool != nil && cleanupPool.ID() == storagePools.PoolIDTemporary {
					_, _ = cleanupPool.Unmount()
				}
			}()

			revert.Add(func() {
				cleanupPool := pools[pool.Name()]
				_, _ = cleanupPool.Unmount() // Defer won't do it if record exists, so unmount on failure.
			})
		}

		// Get list of unknown volumes on pool.
		poolProjectVols, err := pool.ListUnknownVolumes(nil)
		if err != nil {
			if errors.Is(err, storageDrivers.ErrNotSupported) {
				continue // Ignore unsupported storage drivers.
			}

			return response.SmartError(fmt.Errorf("Failed checking volumes on pool %q: %w", pool.Name(), err))
		}

		// Store for consumption after validation scan to avoid needing to reprocess.
		poolsProjectVols[p.Name] = poolProjectVols

		// Check dependencies are met for each volume.
		for projectName, poolVols := range poolProjectVols {
			// Check project exists in database.
			projectInfo := projects[projectName]

			// Look up effective project names for profiles and networks.
			var profileProjectname string
			var networkProjectName string

			if projectInfo != nil {
				profileProjectname = project.ProfileProjectFromRecord(projectInfo)
				networkProjectName = project.NetworkProjectFromRecord(projectInfo)
			} else {
				addDependencyError(fmt.Errorf("Project %q", projectName))
				continue // Skip further validation if project is missing.
			}

			for _, poolVol := range poolVols {
				if poolVol.Container == nil {
					continue // Skip dependency checks for non-instance volumes.
				}

				// Check that the instance's profile dependencies are met.
				for _, poolInstProfileName := range poolVol.Container.Profiles {
					foundProfile := false
					for _, profile := range projectProfiles[profileProjectname] {
						if profile.Name == poolInstProfileName {
							foundProfile = true
						}
					}

					if !foundProfile {
						addDependencyError(fmt.Errorf("Profile %q in project %q", poolInstProfileName, projectName))
					}
				}

				// Check that the instance's NIC network dependencies are met.
				for _, devConfig := range poolVol.Container.ExpandedDevices {
					if devConfig["type"] != "nic" {
						continue
					}

					if devConfig["network"] == "" {
						continue
					}

					foundNetwork := false
					for _, n := range projectNetworks[networkProjectName] {
						if n.Name == devConfig["network"] {
							foundNetwork = true
							break
						}
					}

					if !foundNetwork {
						addDependencyError(fmt.Errorf("Network %q in project %q", devConfig["network"], projectName))
					}
				}
			}
		}
	}

	// If in validation mode or if there are dependency errors, return discovered unknown volumes, along with
	// any dependency errors.
	if validateOnly || len(res.DependencyErrors) > 0 {
		for poolName, poolProjectVols := range poolsProjectVols {
			for projectName, poolVols := range poolProjectVols {
				for _, poolVol := range poolVols {
					var displayType, displayName string
					var displaySnapshotCount int

					// Build display fields for scan results.
					if poolVol.Container != nil {
						displayType = poolVol.Container.Type
						displayName = poolVol.Container.Name
						displaySnapshotCount = len(poolVol.Snapshots)
					} else if poolVol.Bucket != nil {
						displayType = "bucket"
						displayName = poolVol.Bucket.Name
						displaySnapshotCount = 0
					} else {
						displayType = "volume"
						displayName = poolVol.Volume.Name
						displaySnapshotCount = len(poolVol.VolumeSnapshots)
					}

					res.UnknownVolumes = append(res.UnknownVolumes, internalRecover.ValidateVolume{
						Pool:          poolName,
						Project:       projectName,
						Type:          displayType,
						Name:          displayName,
						SnapshotCount: displaySnapshotCount,
					})
				}
			}
		}

		return response.SyncResponse(true, &res)
	}

	// If in import mode and no dependency errors, then re-create missing DB records.

	for _, pool := range pools {
		// Create missing storage pool DB record if neeed.
		if pool.ID() == storagePools.PoolIDTemporary {
			var instPoolVol *backupConfig.Config // Instance volume used for new pool record.
			var poolID int64                     // Pool ID of created pool record.

			var poolVols []*backupConfig.Config
			for _, value := range poolsProjectVols[pool.Name()] {
				poolVols = append(poolVols, value...)
			}

			// Search unknown volumes looking for an instance volume that can be used to
			// restore the pool DB config from. This is preferable over using the user
			// supplied config as it will include any additional settings not supplied.
			for _, poolVol := range poolVols {
				if poolVol.Pool != nil && poolVol.Pool.Config != nil {
					instPoolVol = poolVol
					break // Stop search once we've found an instance with pool config.
				}
			}

			if instPoolVol != nil {
				// Create storage pool DB record from config in the instance.
				logger.Info("Creating storage pool DB record from instance config", logger.Ctx{"name": instPoolVol.Pool.Name, "description": instPoolVol.Pool.Description, "driver": instPoolVol.Pool.Driver, "config": instPoolVol.Pool.Config})
				poolID, err = dbStoragePoolCreateAndUpdateCache(s, instPoolVol.Pool.Name, instPoolVol.Pool.Description, instPoolVol.Pool.Driver, instPoolVol.Pool.Config)
				if err != nil {
					return response.SmartError(fmt.Errorf("Failed creating storage pool %q database entry: %w", pool.Name(), err))
				}
			} else {
				// Create storage pool DB record from config supplied by user if not
				// instance volume pool config found.
				poolDriverName := pool.Driver().Info().Name
				poolDriverConfig := pool.Driver().Config()
				logger.Info("Creating storage pool DB record from user config", logger.Ctx{"name": pool.Name(), "driver": poolDriverName, "config": poolDriverConfig})
				poolID, err = dbStoragePoolCreateAndUpdateCache(s, pool.Name(), "", poolDriverName, poolDriverConfig)
				if err != nil {
					return response.SmartError(fmt.Errorf("Failed creating storage pool %q database entry: %w", pool.Name(), err))
				}
			}

			revert.Add(func() {
				_ = dbStoragePoolDeleteAndUpdateCache(s, pool.Name())
			})

			// Set storage pool node to storagePoolCreated.
			// Must come before storage pool is loaded from the database.
			err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
				return tx.StoragePoolNodeCreated(poolID)
			})
			if err != nil {
				return response.SmartError(fmt.Errorf("Failed marking storage pool %q local status as created: %w", pool.Name(), err))
			}

			logger.Debug("Marked storage pool local status as created", logger.Ctx{"pool": pool.Name()})

			newPool, err := storagePools.LoadByName(s, pool.Name())
			if err != nil {
				return response.SmartError(fmt.Errorf("Failed loading created storage pool %q: %w", pool.Name(), err))
			}

			// Record this newly created pool so that defer doesn't unmount on return.
			pools[pool.Name()] = newPool
			pool = newPool // Replace temporary pool handle with proper one from DB.
		}

		// Create any missing instance, storage volume, and storage bucket records.
		for projectName, poolVols := range poolsProjectVols[pool.Name()] {
			projectInfo := projects[projectName]

			if projectInfo == nil {
				// Shouldn't happen as we validated this above, but be sure for safety.
				return response.SmartError(fmt.Errorf("Project %q not found", projectName))
			}

			profileProjectName := project.ProfileProjectFromRecord(projectInfo)
			customStorageProjectName := project.StorageVolumeProjectFromRecord(projectInfo, db.StoragePoolVolumeTypeCustom)

			// Recover unknown custom volumes (do this first before recovering instances so that any
			// instances that reference unknown custom volume disk devices can be created).
			for _, poolVol := range poolVols {
				if poolVol.Container != nil || poolVol.Bucket != nil {
					continue // Skip instance volumes and buckets.
				} else if poolVol.Container == nil && poolVol.Volume == nil {
					return response.SmartError(fmt.Errorf("Volume is neither instance nor custom volume"))
				}

				// Import custom volume and any snapshots.
				cleanup, err := pool.ImportCustomVolume(customStorageProjectName, poolVol, nil)
				if err != nil {
					return response.SmartError(fmt.Errorf("Failed importing custom volume %q in project %q: %w", poolVol.Volume.Name, projectName, err))
				}

				revert.Add(cleanup)
			}

			// Recover unknown instance volumes.
			for _, poolVol := range poolVols {
				if poolVol.Container == nil && (poolVol.Volume != nil || poolVol.Bucket != nil) {
					continue // Skip custom volumes, invalid volumes and buckets.
				}

				// Recover instance volumes and any snapshots.
				profiles := make([]api.Profile, 0, len(poolVol.Container.Profiles))
				for _, profileName := range poolVol.Container.Profiles {
					for i := range projectProfiles[profileProjectName] {
						if projectProfiles[profileProjectName][i].Name == profileName {
							profiles = append(profiles, *projectProfiles[profileProjectName][i])
						}
					}
				}

				inst, cleanup, err := internalRecoverImportInstance(s, pool, projectName, poolVol, profiles)
				if err != nil {
					return response.SmartError(fmt.Errorf("Failed creating instance %q record in project %q: %w", poolVol.Container.Name, projectName, err))
				}

				revert.Add(cleanup)

				// Recover instance volume snapshots.
				for _, poolInstSnap := range poolVol.Snapshots {
					profiles := make([]api.Profile, 0, len(poolInstSnap.Profiles))
					for _, profileName := range poolInstSnap.Profiles {
						for i := range projectProfiles[profileProjectName] {
							if projectProfiles[profileProjectName][i].Name == profileName {
								profiles = append(profiles, *projectProfiles[profileProjectName][i])
							}
						}
					}

					cleanup, err := internalRecoverImportInstanceSnapshot(s, pool, projectName, poolVol, poolInstSnap, profiles)
					if err != nil {
						return response.SmartError(fmt.Errorf("Failed creating instance %q snapshot %q record in project %q: %w", poolVol.Container.Name, poolInstSnap.Name, projectName, err))
					}

					revert.Add(cleanup)
				}

				// Recreate instance mount path and symlinks (must come after snapshot recovery).
				cleanup, err = pool.ImportInstance(inst, poolVol, nil)
				if err != nil {
					return response.SmartError(fmt.Errorf("Failed importing instance %q in project %q: %w", poolVol.Container.Name, projectName, err))
				}

				revert.Add(cleanup)

				// Reinitialise the instance's root disk quota even if no size specified (allows the storage driver the
				// opportunity to reinitialise the quota based on the new storage volume's DB ID).
				_, rootConfig, err := internalInstance.GetRootDiskDevice(inst.ExpandedDevices().CloneNative())
				if err == nil {
					err = pool.SetInstanceQuota(inst, rootConfig["size"], rootConfig["size.state"], nil)
					if err != nil {
						return response.SmartError(fmt.Errorf("Failed reinitializing root disk quota %q for instance %q in project %q: %w", rootConfig["size"], poolVol.Container.Name, projectName, err))
					}
				}
			}

			// Recover unknown buckets.
			for _, poolVol := range poolVols {
				// Skip non bucket volumes.
				if poolVol.Bucket == nil {
					continue
				}

				// Import bucket.
				cleanup, err := pool.ImportBucket(projectName, poolVol, nil)
				if err != nil {
					return response.SmartError(fmt.Errorf("Failed importing bucket %q in project %q: %w", poolVol.Bucket.Name, projectName, err))
				}

				revert.Add(cleanup)
			}
		}
	}

	revert.Success()
	return response.EmptySyncResponse
}

// internalRecoverImportInstance recreates the database records for an instance and returns the new instance.
// Returns a revert fail function that can be used to undo this function if a subsequent step fails.
func internalRecoverImportInstance(s *state.State, pool storagePools.Pool, projectName string, poolVol *backupConfig.Config, profiles []api.Profile) (instance.Instance, revert.Hook, error) {
	if poolVol.Container == nil {
		return nil, nil, fmt.Errorf("Pool volume is not an instance volume")
	}

	// Add root device if needed.
	if poolVol.Container.Devices == nil {
		poolVol.Container.Devices = make(map[string]map[string]string, 0)
	}

	if poolVol.Container.ExpandedDevices == nil {
		poolVol.Container.ExpandedDevices = make(map[string]map[string]string, 0)
	}

	internalImportRootDevicePopulate(pool.Name(), poolVol.Container.Devices, poolVol.Container.ExpandedDevices, profiles)

	dbInst, err := backup.ConfigToInstanceDBArgs(s, poolVol, projectName, true)
	if err != nil {
		return nil, nil, err
	}

	if dbInst.Type < 0 {
		return nil, nil, fmt.Errorf("Invalid instance type")
	}

	inst, instOp, cleanup, err := instance.CreateInternal(s, *dbInst, false)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed creating instance record: %w", err)
	}

	defer instOp.Done(err)

	return inst, cleanup, err
}

// internalRecoverImportInstance recreates the database records for an instance snapshot.
func internalRecoverImportInstanceSnapshot(s *state.State, pool storagePools.Pool, projectName string, poolVol *backupConfig.Config, snap *api.InstanceSnapshot, profiles []api.Profile) (revert.Hook, error) {
	if poolVol.Container == nil || snap == nil {
		return nil, fmt.Errorf("Pool volume is not an instance volume")
	}

	// Add root device if needed.
	if snap.Devices == nil {
		snap.Devices = make(map[string]map[string]string, 0)
	}

	if snap.ExpandedDevices == nil {
		snap.ExpandedDevices = make(map[string]map[string]string, 0)
	}

	internalImportRootDevicePopulate(pool.Name(), snap.Devices, snap.ExpandedDevices, profiles)

	arch, err := osarch.ArchitectureId(snap.Architecture)
	if err != nil {
		return nil, err
	}

	instanceType, err := instancetype.New(poolVol.Container.Type)
	if err != nil {
		return nil, err
	}

	_, snapInstOp, cleanup, err := instance.CreateInternal(s, db.InstanceArgs{
		Project:      projectName,
		Architecture: arch,
		BaseImage:    snap.Config["volatile.base_image"],
		Config:       snap.Config,
		CreationDate: snap.CreatedAt,
		Type:         instanceType,
		Snapshot:     true,
		Devices:      deviceConfig.NewDevices(snap.Devices),
		Ephemeral:    snap.Ephemeral,
		LastUsedDate: snap.LastUsedAt,
		Name:         poolVol.Container.Name + internalInstance.SnapshotDelimiter + snap.Name,
		Profiles:     profiles,
		Stateful:     snap.Stateful,
	}, false)
	if err != nil {
		return nil, fmt.Errorf("Failed creating instance snapshot record %q: %w", snap.Name, err)
	}

	defer snapInstOp.Done(err)

	return cleanup, err
}

// internalRecoverValidate validates the requested pools to be recovered.
func internalRecoverValidate(d *Daemon, r *http.Request) response.Response {
	// Parse the request.
	req := &internalRecover.ValidatePost{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	return internalRecoverScan(d.State(), req.Pools, true)
}

// internalRecoverImport performs the pool volume recovery.
func internalRecoverImport(d *Daemon, r *http.Request) response.Response {
	// Parse the request.
	req := &internalRecover.ImportPost{}
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		return response.BadRequest(err)
	}

	return internalRecoverScan(d.State(), req.Pools, false)
}
