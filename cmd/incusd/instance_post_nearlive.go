package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"time"

	incus "github.com/lxc/incus/v7/client"
	internalInstance "github.com/lxc/incus/v7/internal/instance"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/operationtype"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/operations"
	"github.com/lxc/incus/v7/internal/server/response"
	"github.com/lxc/incus/v7/internal/server/state"
	storagePools "github.com/lxc/incus/v7/internal/server/storage"
	"github.com/lxc/incus/v7/internal/version"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
	"github.com/lxc/incus/v7/shared/revert"
)

// dependentDiskTransfer moves the volumes of an instance's dependent disks on local storage,
// as near-live migration of an instance whose root is on shared storage needs.
type dependentDiskTransfer struct {
	s                *state.State
	inst             instance.Instance
	target           incus.InstanceServer
	sourceMemberInfo *db.NodeInfo
	op               *operations.Operation

	devs       []deviceConfig.DeviceNamed
	snapPrefix string
	snapshots  []string
}

// start collects the dependent disks on local storage and moves them to the target member while the
// instance runs.
func (t *dependentDiskTransfer) start(ctx context.Context, reverter *revert.Reverter) error {
	err := t.inst.ForEachDependentDiskType(func(dev deviceConfig.DeviceNamed) error {
		diskPool, err := storagePools.LoadByName(t.s, dev.Config["pool"])
		if err != nil {
			return fmt.Errorf("Failed loading storage pool: %w", err)
		}

		if !diskPool.Driver().Info().Remote {
			t.devs = append(t.devs, dev)
		}

		return nil
	})
	if err != nil {
		return err
	}

	if len(t.devs) == 0 {
		return nil
	}

	t.snapPrefix, err = instance.MoveTemporaryName(t.inst)
	if err != nil {
		return err
	}

	// Undo the pre-copy if the move doesn't get as far as handing the instance over.
	reverter.Add(func() {
		for _, dev := range t.devs {
			poolName := dev.Config["pool"]
			volName, _ := internalInstance.SplitVolumeSource(dev.Config["source"])

			diskPool, err := storagePools.LoadByName(t.s, poolName)
			if err == nil {
				for _, snapName := range t.snapshots {
					err := diskPool.DeleteCustomVolumeSnapshot(t.inst.Project().Name, fmt.Sprintf("%s/%s", volName, snapName), t.op)
					if err != nil && !response.IsNotFoundError(err) {
						logger.Warn("Failed removing pre-copy snapshot from dependent volume", logger.Ctx{"project": t.inst.Project().Name, "instance": t.inst.Name(), "volume": volName, "snapshot": snapName, "err": err})
					}
				}
			} else {
				logger.Warn("Failed loading storage pool of dependent volume", logger.Ctx{"project": t.inst.Project().Name, "instance": t.inst.Name(), "pool": poolName, "err": err})
			}

			err = t.target.DeleteStoragePoolVolume(poolName, "custom", volName)
			if err != nil {
				logger.Warn("Failed removing pre-copied dependent volume from target member", logger.Ctx{"project": t.inst.Project().Name, "instance": t.inst.Name(), "volume": volName, "err": err})
			}
		}
	})

	for round := 1; round <= 2; round++ {
		err = t.transfer(ctx, fmt.Sprintf("%s-%d", t.snapPrefix, round), round > 1)
		if err != nil {
			return err
		}
	}

	return nil
}

// finalTransfer brings the pre-copied volumes fully up to date with the instance stopped.
func (t *dependentDiskTransfer) finalTransfer(ctx context.Context) error {
	if len(t.devs) == 0 {
		return nil
	}

	return t.transfer(ctx, "", true)
}

// transfer copies the collected disks to the target member.
func (t *dependentDiskTransfer) transfer(ctx context.Context, snapName string, refresh bool) error {
	projectName := t.inst.Project().Name

	if snapName != "" {
		t.snapshots = append(t.snapshots, snapName)
	}

	for _, dev := range t.devs {
		poolName := dev.Config["pool"]
		volName, _ := internalInstance.SplitVolumeSource(dev.Config["source"])

		diskPool, err := storagePools.LoadByName(t.s, poolName)
		if err != nil {
			return fmt.Errorf("Failed loading storage pool: %w", err)
		}

		// Snapshot the volume.
		if snapName != "" {
			err = diskPool.CreateCustomVolumeSnapshot(projectName, volName, snapName, time.Time{}, false, t.op)
			if err != nil {
				return fmt.Errorf("Failed creating pre-copy snapshot %q for volume %q: %w", snapName, volName, err)
			}
		}

		srcMigration, err := newStorageMigrationSource(false, nil)
		if err != nil {
			return fmt.Errorf("Failed setting up migration of dependent volume %q on source: %w", volName, err)
		}

		run := func(_ *operations.Operation) error {
			return srcMigration.DoStorage(t.s, projectName, poolName, volName, t.op)
		}

		cancel := func(_ *operations.Operation) error {
			srcMigration.disconnect()
			return nil
		}

		resources := map[string][]api.URL{}
		resources["storage_volumes"] = []api.URL{*api.NewURL().Path(version.APIVersion, "storage-pools", poolName, "volumes", "custom", volName)}
		srcOp, err := operations.OperationCreate(t.s, projectName, operations.OperationClassWebsocket, operationtype.VolumeMigrate, resources, srcMigration.Metadata(), run, cancel, srcMigration.Connect, nil)
		if err != nil {
			return err
		}

		srcOp.CopyRequestor(t.op)

		err = srcOp.Start()
		if err != nil {
			return fmt.Errorf("Failed starting migration source operation for dependent volume %q: %w", volName, err)
		}

		sourceSecrets := make(map[string]string, len(srcMigration.conns))
		for connName, conn := range srcMigration.conns {
			sourceSecrets[connName] = conn.Secret()
		}

		err = t.target.CreateStoragePoolVolume(poolName, api.StorageVolumesPost{
			Name: volName,
			Type: "custom",
			Source: api.StorageVolumeSource{
				Type:        "migration",
				Mode:        "pull",
				Operation:   fmt.Sprintf("https://%s%s", t.sourceMemberInfo.Address, srcOp.URL()),
				Websockets:  sourceSecrets,
				Certificate: string(t.s.Endpoints.NetworkCert().PublicKey()),
				Name:        volName,
				Pool:        poolName,
				Refresh:     refresh,
			},
		})
		if err != nil {
			return fmt.Errorf("Failed requesting create of dependent volume %q on destination: %w", volName, err)
		}

		// The source only finishes once the target has confirmed the transfer, so this covers both ends.
		err = srcOp.Wait(ctx)
		if err != nil {
			return fmt.Errorf("Transfer of dependent volume %q to destination failed: %w", volName, err)
		}
	}

	return nil
}

// diskNames returns the disk devices whose volumes were transferred.
func (t *dependentDiskTransfer) diskNames() []string {
	names := make([]string, 0, len(t.devs))
	for _, dev := range t.devs {
		names = append(names, dev.Name)
	}

	return names
}

// deleteSnapshots removes the temporary snapshots from the volumes on the target member.
func (t *dependentDiskTransfer) deleteSnapshots() {
	for _, dev := range t.devs {
		poolName := dev.Config["pool"]
		volName, _ := internalInstance.SplitVolumeSource(dev.Config["source"])

		for _, snapName := range t.snapshots {
			snapOp, err := t.target.DeleteStoragePoolVolumeSnapshot(poolName, "custom", volName, snapName)
			if err == nil {
				err = snapOp.Wait()
			}

			if err != nil {
				logger.Warn("Failed removing pre-copy snapshot after migration", logger.Ctx{"project": t.inst.Project().Name, "instance": t.inst.Name(), "volume": volName, "snapshot": snapName, "err": err})
			}
		}
	}
}

// nearLiveMigrationSupported checks whether a stateless move of a running container can be performed as a near-live migration.
func nearLiveMigrationSupported(s *state.State, inst instance.Instance, req api.InstancePost, target string) error {
	if target == "" || req.Pool != "" || req.Project != "" || req.Name != "" || req.InstanceOnly {
		return errors.New("Instance must be stopped to be moved statelessly")
	}

	if inst.Type() != instancetype.Container {
		return errors.New("Near-live migration is only supported for containers")
	}

	err := inst.ForEachDependentDiskType(func(dev deviceConfig.DeviceNamed) error {
		diskPool, err := storagePools.LoadByName(s, dev.Config["pool"])
		if err != nil {
			return fmt.Errorf("Failed loading storage pool: %w", err)
		}

		diskPoolInfo := diskPool.Driver().Info()
		if !diskPoolInfo.Remote && !diskPoolInfo.NearLiveMigration {
			return fmt.Errorf("Near-live migration isn't supported for dependent disk %q on %q storage pools", dev.Name, diskPoolInfo.Name)
		}

		return nil
	})
	if err != nil {
		return err
	}

	_, rootDiskConf, err := internalInstance.GetRootDiskDevice(inst.ExpandedDevices().CloneNative())
	if err != nil {
		return fmt.Errorf("Failed getting instance root disk: %w", err)
	}

	if rootDiskConf["pool"] == "" {
		return errors.New("The instance's root device is missing the pool property")
	}

	pool, err := storagePools.LoadByName(s, rootDiskConf["pool"])
	if err != nil {
		return fmt.Errorf("Failed loading instance storage pool: %w", err)
	}

	poolInfo := pool.Driver().Info()
	if !poolInfo.Remote && !poolInfo.NearLiveMigration {
		return fmt.Errorf("Near-live migration isn't supported on %q storage pools", poolInfo.Name)
	}

	return nil
}

// runNearLiveCopyRound performs a single copy of the instance onto the target member.
func runNearLiveCopyRound(ctx context.Context, s *state.State, inst instance.Instance, target incus.InstanceServer, targetName string, targetInstInfo *api.Instance, sourceMemberInfo *db.NodeInfo, sharedDisks []string, final bool, refresh bool, op *operations.Operation, progressHandler func(newOp api.Operation)) error {
	instPut := targetInstInfo.Writable()

	// Never tell the target that the instance was running.
	instPut.Config = maps.Clone(instPut.Config)
	delete(instPut.Config, "volatile.last_state.power")

	// Dependent volumes on shared storage stay attached to the source while it runs, so the copy
	// only gets their devices with the final transfer.
	if !final {
		instPut.Devices = maps.Clone(instPut.Devices)
		for _, devName := range sharedDisks {
			delete(instPut.Devices, devName)
		}
	}

	// Setup a new migration source.
	sourceMigration, err := newMigrationSource(inst, false, false, false, inst.Name(), "", instPut.Devices, nil, nil)
	if err != nil {
		return fmt.Errorf("Failed setting up instance migration on source: %w", err)
	}

	run := func(_ *operations.Operation) error {
		return sourceMigration.do(op)
	}

	cancel := func(_ *operations.Operation) error {
		sourceMigration.disconnect()
		return nil
	}

	resources := map[string][]api.URL{}
	resources["instances"] = []api.URL{*api.NewURL().Path(version.APIVersion, "instances", inst.Name())}
	sourceOp, err := operations.OperationCreate(s, inst.Project().Name, operations.OperationClassWebsocket, operationtype.InstanceMigrate, resources, sourceMigration.Metadata(), run, cancel, sourceMigration.Connect, nil)
	if err != nil {
		return err
	}

	sourceOp.CopyRequestor(op)

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

	destOp, err := target.CreateInstance(api.InstancesPost{
		Name:        targetName,
		InstancePut: instPut,
		Type:        api.InstanceType(targetInstInfo.Type),
		Source: api.InstanceSource{
			Type:        "migration",
			Mode:        "pull",
			Operation:   fmt.Sprintf("https://%s%s", sourceMemberInfo.Address, sourceOp.URL()),
			Websockets:  sourceSecrets,
			Certificate: string(s.Endpoints.NetworkCert().PublicKey()),
			Source:      inst.Name(),
			Refresh:     refresh,
		},
	})
	if err != nil {
		return fmt.Errorf("Failed requesting instance copy on destination: %w", err)
	}

	_, err = destOp.AddHandler(progressHandler)
	if err != nil {
		return err
	}

	err = sourceOp.Wait(ctx)
	if err != nil {
		return fmt.Errorf("Instance copy to destination failed on source: %w", err)
	}

	err = destOp.WaitContext(ctx)
	if err != nil {
		return fmt.Errorf("Instance copy to destination failed: %w", err)
	}

	return nil
}

// migrateInstanceNearLive moves a running container to another cluster member with minimal downtime.
//
// The container is copied to the target member under a temporary name while it keeps running, then
// refreshed once more, so the final copy, which does require the container to be stopped, only has
// to carry the changes written since the last snapshot. Once the final transfer lands, the source
// is deleted and the copy is renamed to the real name and started.
func migrateInstanceNearLive(ctx context.Context, s *state.State, inst instance.Instance, target incus.InstanceServer, targetInstInfo *api.Instance, sourceMemberInfo *db.NodeInfo, deviceOverrides api.DevicesMap, op *operations.Operation, progressHandler func(newOp api.Operation)) error {
	reverter := revert.New()
	defer reverter.Fail()

	instName := inst.Name()
	projectName := inst.Project().Name

	sourcePool, err := storagePools.LoadByInstance(s, inst)
	if err != nil {
		return fmt.Errorf("Failed loading instance storage pool: %w", err)
	}

	if sourcePool.Driver().Info().Remote {
		return errors.New("Near-live migration pre-copy cannot be used on shared storage")
	}

	// Attach the progress handler.
	_, err = target.GetEvents()
	if err != nil {
		return err
	}

	defer target.Disconnect()

	targetName, err := instance.MoveTemporaryName(inst)
	if err != nil {
		return err
	}

	l := logger.AddContext(logger.Ctx{"project": projectName, "instance": instName, "target": targetName})

	l.Debug("Near-live migration started")
	defer l.Debug("Near-live migration finished")

	// Collect the dependent disks whose volumes are on shared storage.
	sharedDisks := []string{}

	localDevices := inst.LocalDevices()
	err = inst.ForEachDependentDiskType(func(dev deviceConfig.DeviceNamed) error {
		diskPool, err := storagePools.LoadByName(s, dev.Config["pool"])
		if err != nil {
			return fmt.Errorf("Failed loading storage pool: %w", err)
		}

		if !diskPool.Driver().Info().Remote {
			return nil
		}

		_, ok := localDevices[dev.Name]
		if !ok {
			return fmt.Errorf("Dependent disk %q isn't a local device", dev.Name)
		}

		sharedDisks = append(sharedDisks, dev.Name)

		return nil
	})
	if err != nil {
		return err
	}

	transfer := func(final bool, refresh bool) error {
		return runNearLiveCopyRound(ctx, s, inst, target, targetName, targetInstInfo, sourceMemberInfo, sharedDisks, final, refresh, op, progressHandler)
	}

	snapshotNames := []string{}

	for round := 1; round <= 2; round++ {
		snapName := fmt.Sprintf("%s-%d", targetName, round)

		err = inst.Snapshot(snapName, time.Time{}, false)
		if err != nil {
			return fmt.Errorf("Failed creating pre-copy snapshot %q: %w", snapName, err)
		}

		// Remove the pre-copy snapshot from the source instance on failure.
		reverter.Add(func() {
			snapInst, err := instance.LoadByProjectAndName(s, projectName, instName+internalInstance.SnapshotDelimiter+snapName)
			if err == nil {
				err = snapInst.Delete(true, true)
			}

			if err != nil {
				l.Warn("Failed removing near-live migration pre-copy snapshot from source instance", logger.Ctx{"snapshot": snapName, "err": err})
				return
			}

			l.Debug("Removed near-live migration pre-copy snapshot from source instance", logger.Ctx{"snapshot": snapName})
		})

		snapshotNames = append(snapshotNames, snapName)

		// The first round creates the instance on the target, later rounds refresh it.
		err = transfer(false, round > 1)
		if err != nil {
			return err
		}

		// From now on a failure leaves a full copy behind on the target member.
		if round == 1 {
			reverter.Add(func() {
				op, err := target.DeleteInstance(targetName)
				if err == nil {
					err = op.Wait()
				}

				if err != nil {
					l.Warn("Failed removing partial copy after failed near-live migration", logger.Ctx{"err": err})
					return
				}

				l.Debug("Removed partial copy after failed near-live migration")
			})
		}
	}

	// Stop the instance for the final transfer.
	err = instanceShutdownOrForceStop(inst)
	if err != nil {
		return err
	}

	l.Debug("Instance stopped for the final near-live migration transfer")

	reverter.Add(func() {
		err := inst.Start(false)
		if err != nil {
			l.Error("Failed restarting instance after failed near-live migration", logger.Ctx{"err": err})
		}
	})

	// Final transfer, carrying everything written since the second snapshot. This is also what hands
	// the shared dependent volumes over, by giving the copy their devices.
	err = transfer(true, true)
	if err != nil {
		return err
	}

	l.Debug("Deleting source instance")

	reverter.Success()

	// The dependent volumes now belong to the copy, so leave them and their snapshots alone.
	err = inst.Delete(true, false)
	if err != nil {
		return fmt.Errorf("Failed deleting source instance: %w", err)
	}

	l.Debug("Source instance deleted")

	// Remove the dependent volumes that were transferred.
	err = cleanupDependentDisks(s, inst, deviceOverrides, op)
	if err != nil {
		return fmt.Errorf("Failed deleting instance dependent volumes on source member: %w", err)
	}

	// Remove the pre-copy snapshots from the instance copy on the target member.
	for _, snapName := range snapshotNames {
		op, err := target.DeleteInstanceSnapshot(targetName, snapName)
		if err == nil {
			err = op.Wait()
		}

		if err != nil {
			l.Warn("Failed removing near-live migration pre-copy snapshot", logger.Ctx{"snapshot": snapName, "err": err})
			continue
		}

		l.Debug("Removed near-live migration pre-copy snapshot", logger.Ctx{"snapshot": snapName})
	}

	l.Debug("Renaming instance copy on target member to its real name")

	renameOp, err := target.RenameInstance(targetName, api.InstancePost{Name: instName})
	if err != nil {
		return fmt.Errorf("Failed renaming instance copy on target member: %w", err)
	}

	err = renameOp.Wait()
	if err != nil {
		return fmt.Errorf("Failed renaming instance copy on target member: %w", err)
	}

	l.Debug("Starting instance on target member")

	startOp, err := target.UpdateInstanceState(instName, api.InstanceStatePut{Action: "start"}, "")
	if err == nil {
		err = startOp.Wait()
	}

	if err != nil {
		return fmt.Errorf("Failed starting instance on target member: %w", err)
	}

	l.Debug("Instance started on target member")

	return nil
}
