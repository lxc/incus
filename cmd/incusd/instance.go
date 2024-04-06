package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	internalInstance "github.com/lxc/incus/v6/internal/instance"
	"github.com/lxc/incus/v6/internal/revert"
	"github.com/lxc/incus/v6/internal/server/cluster"
	"github.com/lxc/incus/v6/internal/server/db"
	dbCluster "github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/db/operationtype"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/instance/operationlock"
	"github.com/lxc/incus/v6/internal/server/locking"
	"github.com/lxc/incus/v6/internal/server/operations"
	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/internal/server/state"
	storagePools "github.com/lxc/incus/v6/internal/server/storage"
	"github.com/lxc/incus/v6/internal/server/task"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/util"
)

// Helper functions

// instanceCreateAsEmpty creates an empty instance.
func instanceCreateAsEmpty(s *state.State, args db.InstanceArgs) (instance.Instance, error) {
	revert := revert.New()
	defer revert.Fail()

	// Create the instance record.
	inst, instOp, cleanup, err := instance.CreateInternal(s, args, true, true)
	if err != nil {
		return nil, fmt.Errorf("Failed creating instance record: %w", err)
	}

	revert.Add(cleanup)
	defer instOp.Done(err)

	pool, err := storagePools.LoadByInstance(s, inst)
	if err != nil {
		return nil, fmt.Errorf("Failed loading instance storage pool: %w", err)
	}

	err = pool.CreateInstance(inst, nil)
	if err != nil {
		return nil, fmt.Errorf("Failed creating instance: %w", err)
	}

	revert.Add(func() { _ = inst.Delete(true) })

	err = inst.UpdateBackupFile()
	if err != nil {
		return nil, err
	}

	revert.Success()
	return inst, nil
}

// instanceImageTransfer transfers an image from another cluster node.
func instanceImageTransfer(s *state.State, r *http.Request, projectName string, hash string, nodeAddress string) error {
	logger.Debugf("Transferring image %q from node %q", hash, nodeAddress)
	client, err := cluster.Connect(nodeAddress, s.Endpoints.NetworkCert(), s.ServerCert(), r, false)
	if err != nil {
		return err
	}

	client = client.UseProject(projectName)

	err = imageImportFromNode(filepath.Join(s.OS.VarDir, "images"), client, hash)
	if err != nil {
		return err
	}

	return nil
}

func ensureImageIsLocallyAvailable(ctx context.Context, s *state.State, r *http.Request, img *api.Image, projectName string, instanceType instancetype.Type) error {
	// Check if the image is available locally or it's on another member.
	// Ensure we are the only ones operating on this image. Otherwise another instance created at the same
	// time may also arrive at the conclusion that the image doesn't exist on this cluster member and then
	// think it needs to download the image and store the record in the database as well, which will lead to
	// duplicate record errors.
	unlock, err := imageOperationLock(ctx, img.Fingerprint)
	if err != nil {
		return err
	}

	defer unlock()

	var memberAddress string

	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		memberAddress, err = tx.LocateImage(ctx, img.Fingerprint)

		return err
	})
	if err != nil {
		return fmt.Errorf("Failed locating image %q: %w", img.Fingerprint, err)
	}

	if memberAddress != "" {
		// The image is available from another node, let's try to import it.
		err = instanceImageTransfer(s, r, projectName, img.Fingerprint, memberAddress)
		if err != nil {
			return fmt.Errorf("Failed transferring image %q from %q: %w", img.Fingerprint, memberAddress, err)
		}

		err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			// As the image record already exists in the project, just add the node ID to the image.
			return tx.AddImageToLocalNode(ctx, projectName, img.Fingerprint)
		})
		if err != nil {
			return fmt.Errorf("Failed adding transferred image %q record to local cluster member: %w", img.Fingerprint, err)
		}
	}

	return nil
}

// instanceCreateFromImage creates an instance from a rootfs image.
func instanceCreateFromImage(ctx context.Context, s *state.State, r *http.Request, img *api.Image, args db.InstanceArgs, op *operations.Operation) error {
	revert := revert.New()
	defer revert.Fail()

	// Validate the type of the image matches the type of the instance.
	imgType, err := instancetype.New(img.Type)
	if err != nil {
		return err
	}

	if imgType != args.Type {
		return fmt.Errorf("Requested image's type %q doesn't match instance type %q", imgType, args.Type)
	}

	// Set the "image.*" keys.
	if img.Properties != nil {
		for k, v := range img.Properties {
			args.Config[fmt.Sprintf("image.%s", k)] = v
		}
	}

	// Set the BaseImage field (regardless of previous value).
	args.BaseImage = img.Fingerprint

	// Create the instance.
	inst, instOp, cleanup, err := instance.CreateInternal(s, args, true, true)
	if err != nil {
		return fmt.Errorf("Failed creating instance record: %w", err)
	}

	revert.Add(cleanup)
	defer instOp.Done(nil)

	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		err = tx.UpdateImageLastUseDate(ctx, args.Project, img.Fingerprint, time.Now().UTC())
		if err != nil {
			return fmt.Errorf("Error updating image last use date: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	pool, err := storagePools.LoadByInstance(s, inst)
	if err != nil {
		return fmt.Errorf("Failed loading instance storage pool: %w", err)
	}

	err = pool.CreateInstanceFromImage(inst, img.Fingerprint, op)
	if err != nil {
		return fmt.Errorf("Failed creating instance from image: %w", err)
	}

	revert.Add(func() { _ = inst.Delete(true) })

	err = inst.UpdateBackupFile()
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

func instanceRebuildFromImage(ctx context.Context, s *state.State, r *http.Request, inst instance.Instance, img *api.Image, op *operations.Operation) error {
	// Validate the type of the image matches the type of the instance.
	imgType, err := instancetype.New(img.Type)
	if err != nil {
		return err
	}

	if imgType != inst.Type() {
		return fmt.Errorf("Requested image's type %q doesn't match instance type %q", imgType, inst.Type())
	}

	err = ensureImageIsLocallyAvailable(ctx, s, r, img, inst.Project().Name, inst.Type())
	if err != nil {
		return err
	}

	err = inst.Rebuild(img, op)
	if err != nil {
		return fmt.Errorf("Failed rebuilding instance from image: %w", err)
	}

	return nil
}

func instanceRebuildFromEmpty(s *state.State, inst instance.Instance, op *operations.Operation) error {
	err := inst.Rebuild(nil, op) // Rebuild as empty.
	if err != nil {
		return fmt.Errorf("Failed rebuilding as an empty instance: %w", err)
	}

	return nil
}

// instanceCreateAsCopyOpts options for copying an instance.
type instanceCreateAsCopyOpts struct {
	sourceInstance       instance.Instance // Source instance.
	targetInstance       db.InstanceArgs   // Configuration for new instance.
	instanceOnly         bool              // Only copy the instance and not it's snapshots.
	refresh              bool              // Refresh an existing target instance.
	applyTemplateTrigger bool              // Apply deferred TemplateTriggerCopy.
	allowInconsistent    bool              // Ignore some copy errors
}

// instanceCreateAsCopy create a new instance by copying from an existing instance.
func instanceCreateAsCopy(s *state.State, opts instanceCreateAsCopyOpts, op *operations.Operation) (instance.Instance, error) {
	var inst instance.Instance
	var instOp *operationlock.InstanceOperation
	var err error
	var cleanup revert.Hook

	revert := revert.New()
	defer revert.Fail()

	if opts.refresh {
		// Load the target instance.
		inst, err = instance.LoadByProjectAndName(s, opts.targetInstance.Project, opts.targetInstance.Name)
		if err != nil {
			opts.refresh = false // Instance doesn't exist, so switch to copy mode.
		}
	}

	// If we are not in refresh mode, then create a new instance as we are in copy mode.
	if !opts.refresh {
		// Create the instance.
		inst, instOp, cleanup, err = instance.CreateInternal(s, opts.targetInstance, true, false)
		if err != nil {
			return nil, fmt.Errorf("Failed creating instance record: %w", err)
		}

		revert.Add(cleanup)
	} else {
		instOp, err = inst.LockExclusive()
		if err != nil {
			return nil, fmt.Errorf("Failed getting exclusive access to target instance: %w", err)
		}
	}

	defer instOp.Done(err)

	// At this point we have already figured out the instance's root disk device so we can simply retrieve it
	// from the expanded devices.
	instRootDiskDeviceKey, instRootDiskDevice, err := internalInstance.GetRootDiskDevice(inst.ExpandedDevices().CloneNative())
	if err != nil {
		return nil, err
	}

	var snapshots []instance.Instance

	if !opts.instanceOnly {
		if opts.refresh {
			// Compare snapshots.
			sourceSnaps, err := opts.sourceInstance.Snapshots()
			if err != nil {
				return nil, err
			}

			sourceSnapshotComparable := make([]storagePools.ComparableSnapshot, 0, len(sourceSnaps))
			for _, sourceSnap := range sourceSnaps {
				_, sourceSnapName, _ := api.GetParentAndSnapshotName(sourceSnap.Name())

				sourceSnapshotComparable = append(sourceSnapshotComparable, storagePools.ComparableSnapshot{
					Name:         sourceSnapName,
					CreationDate: sourceSnap.CreationDate(),
				})
			}

			targetSnaps, err := inst.Snapshots()
			if err != nil {
				return nil, err
			}

			targetSnapshotsComparable := make([]storagePools.ComparableSnapshot, 0, len(targetSnaps))
			for _, targetSnap := range targetSnaps {
				_, targetSnapName, _ := api.GetParentAndSnapshotName(targetSnap.Name())

				targetSnapshotsComparable = append(targetSnapshotsComparable, storagePools.ComparableSnapshot{
					Name:         targetSnapName,
					CreationDate: targetSnap.CreationDate(),
				})
			}

			syncSourceSnapshotIndexes, deleteTargetSnapshotIndexes := storagePools.CompareSnapshots(sourceSnapshotComparable, targetSnapshotsComparable)

			// Delete extra snapshots first.
			for _, deleteTargetSnapIndex := range deleteTargetSnapshotIndexes {
				err := targetSnaps[deleteTargetSnapIndex].Delete(true)
				if err != nil {
					return nil, err
				}
			}

			// Only send the snapshots that need updating.
			snapshots = make([]instance.Instance, 0, len(syncSourceSnapshotIndexes))
			for _, syncSourceSnapIndex := range syncSourceSnapshotIndexes {
				snapshots = append(snapshots, sourceSnaps[syncSourceSnapIndex])
			}
		} else {
			// Get snapshots of source instance.
			snapshots, err = opts.sourceInstance.Snapshots()
			if err != nil {
				return nil, err
			}
		}

		for _, srcSnap := range snapshots {
			snapLocalDevices := srcSnap.LocalDevices().Clone()

			// Load snap root disk from expanded devices (in case it doesn't have its own root disk).
			snapExpandedRootDiskDevKey, snapExpandedRootDiskDev, err := internalInstance.GetRootDiskDevice(srcSnap.ExpandedDevices().CloneNative())
			if err == nil {
				// If the expanded devices has a root disk, but its pool doesn't match our new
				// parent instance's pool, then either modify the device if it is local or add a
				// new one to local devices if its coming from the profiles.
				if snapExpandedRootDiskDev["pool"] != instRootDiskDevice["pool"] {
					localRootDiskDev, found := snapLocalDevices[snapExpandedRootDiskDevKey]
					if found {
						// Modify exist local device's pool.
						localRootDiskDev["pool"] = instRootDiskDevice["pool"]
						snapLocalDevices[snapExpandedRootDiskDevKey] = localRootDiskDev
					} else {
						// Add a new local device using parent instance's pool.
						snapLocalDevices[instRootDiskDeviceKey] = map[string]string{
							"type": "disk",
							"path": "/",
							"pool": instRootDiskDevice["pool"],
						}
					}
				}
			} else if errors.Is(err, internalInstance.ErrNoRootDisk) {
				// If no root disk defined in either local devices or profiles, then add one to the
				// snapshot local devices using the same device name from the parent instance.
				snapLocalDevices[instRootDiskDeviceKey] = map[string]string{
					"type": "disk",
					"path": "/",
					"pool": instRootDiskDevice["pool"],
				}
			} else { //nolint:staticcheck // (keep the empty branch for the comment)
				// Snapshot has multiple root disk devices, we can't automatically fix this so
				// leave alone so we don't prevent copy.
			}

			fields := strings.SplitN(srcSnap.Name(), internalInstance.SnapshotDelimiter, 2)
			newSnapName := fmt.Sprintf("%s/%s", inst.Name(), fields[1])
			snapInstArgs := db.InstanceArgs{
				Architecture: srcSnap.Architecture(),
				Config:       srcSnap.LocalConfig(),
				Type:         opts.sourceInstance.Type(),
				Snapshot:     true,
				Devices:      snapLocalDevices,
				Description:  srcSnap.Description(),
				Ephemeral:    srcSnap.IsEphemeral(),
				Name:         newSnapName,
				Profiles:     srcSnap.Profiles(),
				Project:      opts.targetInstance.Project,
				ExpiryDate:   srcSnap.ExpiryDate(),
				CreationDate: srcSnap.CreationDate(),
			}

			// Create the snapshots.
			_, snapInstOp, cleanup, err := instance.CreateInternal(s, snapInstArgs, true, false)
			if err != nil {
				return nil, fmt.Errorf("Failed creating instance snapshot record %q: %w", newSnapName, err)
			}

			revert.Add(cleanup)
			defer snapInstOp.Done(err)
		}
	}

	// Copy the storage volume.
	pool, err := storagePools.LoadByInstance(s, inst)
	if err != nil {
		return nil, fmt.Errorf("Failed loading instance storage pool: %w", err)
	}

	if opts.refresh {
		err = pool.RefreshInstance(inst, opts.sourceInstance, snapshots, opts.allowInconsistent, op)
		if err != nil {
			return nil, fmt.Errorf("Refresh instance: %w", err)
		}
	} else {
		err = pool.CreateInstanceFromCopy(inst, opts.sourceInstance, !opts.instanceOnly, opts.allowInconsistent, op)
		if err != nil {
			return nil, fmt.Errorf("Create instance from copy: %w", err)
		}

		revert.Add(func() { _ = inst.Delete(true) })

		if opts.applyTemplateTrigger {
			// Trigger the templates on next start.
			err = inst.DeferTemplateApply(instance.TemplateTriggerCopy)
			if err != nil {
				return nil, err
			}
		}
	}

	err = inst.UpdateBackupFile()
	if err != nil {
		return nil, err
	}

	revert.Success()
	return inst, nil
}

// Load all instances of this nodes under the given project.
func instanceLoadNodeProjectAll(ctx context.Context, s *state.State, project string, instanceType instancetype.Type) ([]instance.Instance, error) {
	var err error
	var instances []instance.Instance

	filter := dbCluster.InstanceFilter{Type: instanceType.Filter(), Project: &project}
	if s.ServerName != "" {
		filter.Node = &s.ServerName
	}

	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.InstanceList(ctx, func(dbInst db.InstanceArgs, p api.Project) error {
			inst, err := instance.Load(s, dbInst, p)
			if err != nil {
				return fmt.Errorf("Failed loading instance %q in project %q: %w", dbInst.Name, dbInst.Project, err)
			}

			instances = append(instances, inst)

			return nil
		}, filter)
	})
	if err != nil {
		return nil, err
	}

	return instances, nil
}

func autoCreateInstanceSnapshots(ctx context.Context, s *state.State, instances []instance.Instance) error {
	// Make the snapshots.
	for _, inst := range instances {
		err := ctx.Err()
		if err != nil {
			return err
		}

		l := logger.AddContext(logger.Ctx{"project": inst.Project().Name, "instance": inst.Name()})

		snapshotName, err := instance.NextSnapshotName(s, inst, "snap%d")
		if err != nil {
			l.Error("Error retrieving next snapshot name", logger.Ctx{"err": err})
			return err
		}

		expiry, err := internalInstance.GetExpiry(time.Now(), inst.ExpandedConfig()["snapshots.expiry"])
		if err != nil {
			l.Error("Error getting snapshots.expiry date")
			return err
		}

		err = inst.Snapshot(snapshotName, expiry, false)
		if err != nil {
			l.Error("Error creating snapshot", logger.Ctx{"snapshot": snapshotName, "err": err})
			return err
		}
	}

	return nil
}

var instSnapshotsPruneRunning = sync.Map{}

func pruneExpiredInstanceSnapshots(ctx context.Context, s *state.State, snapshots []instance.Instance) error {
	// Find snapshots to delete
	for _, snapshot := range snapshots {
		err := ctx.Err()
		if err != nil {
			return err
		}

		_, loaded := instSnapshotsPruneRunning.LoadOrStore(snapshot.ID(), struct{}{})
		if loaded {
			continue // Deletion of this snapshot is already running, skip.
		}

		err = snapshot.Delete(true)
		instSnapshotsPruneRunning.Delete(snapshot.ID())
		if err != nil {
			return fmt.Errorf("Failed to delete expired instance snapshot %q in project %q: %w", snapshot.Name(), snapshot.Project().Name, err)
		}

		logger.Debug("Deleted instance snapshot", logger.Ctx{"project": snapshot.Project().Name, "snapshot": snapshot.Name()})
	}

	return nil
}

func pruneExpiredAndAutoCreateInstanceSnapshotsTask(d *Daemon) (task.Func, task.Schedule) {
	// `f` creates new scheduled instance snapshots and then, prune the expired ones
	f := func(ctx context.Context) {
		s := d.State()
		var instances, expiredSnapshotInstances []instance.Instance

		// Get list of expired instance snapshots for this local member.
		err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			expiredSnaps, err := tx.GetLocalExpiredInstanceSnapshots(ctx)
			if err != nil {
				return fmt.Errorf("Failed loading expired instance snapshots: %w", err)
			}

			if len(expiredSnaps) > 0 {
				expiredSnapshots := make([]dbCluster.Instance, 0, len(expiredSnaps))
				parents := make(map[string]*dbCluster.Instance, 0)

				// Enrich expired snapshot list with info from parent (opportunistically loading
				// the parent info from the DB if not already loaded).
				for _, snapshot := range expiredSnaps {
					parentInstanceKey := snapshot.Project + "/" + snapshot.Instance
					parent, ok := parents[parentInstanceKey]
					if !ok {
						parent, err = dbCluster.GetInstance(ctx, tx.Tx(), snapshot.Project, snapshot.Instance)
						if err != nil {
							return fmt.Errorf("Failed loading instance %q (project %q): %w", snapshot.Instance, snapshot.Project, err)
						}

						parents[parentInstanceKey] = parent
					}

					expiredSnapshots = append(expiredSnapshots, snapshot.ToInstance(parent.Name, parent.Node, parent.Type, parent.Architecture))
				}

				// Load expired snapshot configs.
				snapshotArgs, err := tx.InstancesToInstanceArgs(ctx, true, expiredSnapshots...)
				if err != nil {
					return fmt.Errorf("Failed loading expired instance snapshots info: %w", err)
				}

				projects := make(map[string]*api.Project)

				expiredSnapshotInstances = make([]instance.Instance, 0)
				for _, snapshotArg := range snapshotArgs {
					// Load project if not already loaded.
					p, found := projects[snapshotArg.Project]
					if !found {
						dbProject, err := dbCluster.GetProject(ctx, tx.Tx(), snapshotArg.Project)
						if err != nil {
							return fmt.Errorf("Failed loading project %q: %w", snapshotArg.Project, err)
						}

						p, err = dbProject.ToAPI(ctx, tx.Tx())
						if err != nil {
							return fmt.Errorf("Failed loading project %q config: %w", snapshotArg.Project, err)
						}

						projects[snapshotArg.Project] = p
					}

					inst, err := instance.Load(s, snapshotArg, *p)
					if err != nil {
						return fmt.Errorf("Failed loading instance snapshot %q (project %q) for prune task: %w", snapshotArg.Name, snapshotArg.Project, err)
					}

					logger.Debug("Scheduling instance snapshot expiry", logger.Ctx{"instance": inst.Name(), "project": inst.Project().Name})
					expiredSnapshotInstances = append(expiredSnapshotInstances, inst)
				}
			}

			return nil
		})
		if err != nil {
			logger.Error("Failed getting instance snapshot expiry info", logger.Ctx{"err": err})
			return
		}

		// Get list of instances on the local member that are due to have snaphots creating.
		filter := dbCluster.InstanceFilter{Node: &s.ServerName}

		err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.InstanceList(ctx, func(dbInst db.InstanceArgs, p api.Project) error {
				err = project.AllowSnapshotCreation(&p)
				if err != nil {
					return nil
				}

				inst, err := instance.Load(s, dbInst, p)
				if err != nil {
					return fmt.Errorf("Failed loading instance %q (project %q) for snapshot task: %w", dbInst.Name, dbInst.Project, err)
				}

				// Check if instance has snapshot schedule enabled.
				schedule, ok := inst.ExpandedConfig()["snapshots.schedule"]
				if !ok || schedule == "" {
					return nil
				}

				// Check if snapshot is scheduled.
				if !snapshotIsScheduledNow(schedule, int64(inst.ID())) {
					return nil
				}

				// If snapshot should only be taken if instance is running, check if running.
				if util.IsFalseOrEmpty(inst.ExpandedConfig()["snapshots.schedule.stopped"]) && !inst.IsRunning() {
					return nil
				}

				logger.Debug("Scheduling auto instance snapshot", logger.Ctx{"instance": inst.Name(), "project": inst.Project().Name})
				instances = append(instances, inst)

				return nil
			}, filter)
		})
		if err != nil {
			logger.Error("Failed getting instance snapshot schedule info", logger.Ctx{"err": err})
			return
		}

		// Handle snapshot expiry first before creating new ones to reduce the chances of running out of
		// disk space.
		if len(expiredSnapshotInstances) > 0 {
			opRun := func(op *operations.Operation) error {
				return pruneExpiredInstanceSnapshots(ctx, s, expiredSnapshotInstances)
			}

			op, err := operations.OperationCreate(s, "", operations.OperationClassTask, operationtype.SnapshotsExpire, nil, nil, opRun, nil, nil, nil)
			if err != nil {
				logger.Error("Failed creating instance snapshots expiry operation", logger.Ctx{"err": err})
			} else {
				logger.Info("Pruning expired instance snapshots")

				err = op.Start()
				if err != nil {
					logger.Error("Failed starting instance snapshots expiry operation", logger.Ctx{"err": err})
				} else {
					err = op.Wait(ctx)
					if err != nil {
						logger.Error("Failed pruning instance snapshots", logger.Ctx{"err": err})
					} else {
						logger.Info("Done pruning expired instance snapshots")
					}
				}
			}
		}

		// Handle snapshot auto creation.
		if len(instances) > 0 {
			opRun := func(op *operations.Operation) error {
				return autoCreateInstanceSnapshots(ctx, s, instances)
			}

			op, err := operations.OperationCreate(s, "", operations.OperationClassTask, operationtype.SnapshotCreate, nil, nil, opRun, nil, nil, nil)
			if err != nil {
				logger.Error("Failed creating scheduled instance snapshot operation", logger.Ctx{"err": err})
			} else {
				logger.Info("Creating scheduled instance snapshots")

				err = op.Start()
				if err != nil {
					logger.Error("Failed starting scheduled instance snapshot operation", logger.Ctx{"err": err})
				} else {
					err = op.Wait(ctx)
					if err != nil {
						logger.Error("Failed scheduled instance snapshots", logger.Ctx{"err": err})
					} else {
						logger.Info("Done creating scheduled instance snapshots")
					}
				}
			}
		}
	}

	first := true
	schedule := func() (time.Duration, error) {
		interval := time.Minute

		if first {
			first = false
			return interval, task.ErrSkip
		}

		return interval, nil
	}

	return f, schedule
}

// getSourceImageFromInstanceSource returns the image to use for an instance source.
func getSourceImageFromInstanceSource(ctx context.Context, s *state.State, tx *db.ClusterTx, project string, source api.InstanceSource, imageRef *string, instType string) (*api.Image, error) {
	// Resolve the image.
	sourceImageRefUpdate, err := instance.ResolveImage(ctx, tx, project, source)
	if err != nil {
		return nil, err
	}

	*imageRef = sourceImageRefUpdate
	sourceImageHash := *imageRef

	// If a remote server is being used, check whether we have a cached image for the alias.
	// If so then use the cached image fingerprint for loading the cache image profiles.
	// As its possible for a remote cached image to have its profiles modified after download.
	if source.Server != "" {
		for _, architecture := range s.OS.Architectures {
			cachedFingerprint, err := tx.GetCachedImageSourceFingerprint(ctx, source.Server, source.Protocol, *imageRef, instType, architecture)
			if err == nil && cachedFingerprint != sourceImageHash {
				sourceImageHash = cachedFingerprint
				break
			}
		}
	}

	// Check if image has an entry in the database.
	_, sourceImage, err := tx.GetImageByFingerprintPrefix(ctx, sourceImageHash, dbCluster.ImageFilter{Project: &project})
	if err != nil {
		return nil, err
	}

	return sourceImage, nil
}

// instanceOperationLock acquires a lock for operating on an instance and returns the unlock function.
func instanceOperationLock(ctx context.Context, projectName string, instanceName string) (locking.UnlockFunc, error) {
	l := logger.AddContext(logger.Ctx{"project": projectName, "instance": instanceName})
	l.Debug("Acquiring lock for instance")
	defer l.Debug("Lock acquired for instance")

	return locking.Lock(ctx, fmt.Sprintf("InstanceOperation_%s", project.Instance(projectName, instanceName)))
}
