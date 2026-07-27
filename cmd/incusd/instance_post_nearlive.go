package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"time"

	incus "github.com/lxc/incus/v7/client"
	internalInstance "github.com/lxc/incus/v7/internal/instance"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/state"
	storagePools "github.com/lxc/incus/v7/internal/server/storage"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
	"github.com/lxc/incus/v7/shared/revert"
)

// nearLiveMigrationSupported checks whether a stateless move of a running container can be performed as a near-live migration.
func nearLiveMigrationSupported(s *state.State, inst instance.Instance, req api.InstancePost, target string) error {
	if target == "" || req.Pool != "" || req.Project != "" || req.Name != "" || req.InstanceOnly {
		return errors.New("Instance must be stopped to be moved statelessly")
	}

	if inst.Type() != instancetype.Container {
		return errors.New("Near-live migration is only supported for containers")
	}

	// Dependent volumes can't be transferred incrementally, so there's nothing to pre-copy.
	dependent := false
	err := inst.ForEachDependentDiskType(func(dev deviceConfig.DeviceNamed) error {
		dependent = true
		return nil
	})
	if err != nil {
		return err
	}

	if dependent {
		return errors.New("Near-live migration isn't supported for instances with dependent volumes")
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

	driverName := pool.Driver().Info().Name
	if driverName == "dir" || driverName == "lvm" {
		return fmt.Errorf("Near-live migration isn't supported on %q storage pools", driverName)
	}

	return nil
}

// runNearLiveCopyRound performs a single copy of the instance onto the target member.
func runNearLiveCopyRound(ctx context.Context, inst instance.Instance, target incus.InstanceServer, targetName string, targetInstInfo *api.Instance, refresh bool, progressHandler func(newOp api.Operation)) error {
	instPut := targetInstInfo.Writable()

	// Never tell the target that the instance was running.
	instPut.Config = maps.Clone(instPut.Config)
	delete(instPut.Config, "volatile.last_state.power")

	op, err := target.CreateInstance(api.InstancesPost{
		Name:        targetName,
		InstancePut: instPut,
		Type:        api.InstanceType(targetInstInfo.Type),
		Source: api.InstanceSource{
			Type:    "copy",
			Source:  inst.Name(),
			Refresh: refresh,
		},
	})
	if err != nil {
		return fmt.Errorf("Failed requesting instance copy on destination: %w", err)
	}

	_, err = op.AddHandler(progressHandler)
	if err != nil {
		return err
	}

	err = op.WaitContext(ctx)
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
func migrateInstanceNearLive(ctx context.Context, s *state.State, inst instance.Instance, target incus.InstanceServer, targetInstInfo *api.Instance, progressHandler func(newOp api.Operation)) error {
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

	transfer := func(refresh bool) error {
		return runNearLiveCopyRound(ctx, inst, target, targetName, targetInstInfo, refresh, progressHandler)
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
				err = snapInst.Delete(true, false)
			}

			if err != nil {
				l.Warn("Failed removing near-live migration pre-copy snapshot from source instance", logger.Ctx{"snapshot": snapName, "err": err})
				return
			}

			l.Debug("Removed near-live migration pre-copy snapshot from source instance", logger.Ctx{"snapshot": snapName})
		})

		snapshotNames = append(snapshotNames, snapName)

		// The first round creates the instance on the target, later rounds refresh it.
		err = transfer(round > 1)
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

	// Final transfer, carrying everything written since the second snapshot.
	err = transfer(true)
	if err != nil {
		return err
	}

	l.Debug("Deleting source instance")

	reverter.Success()

	err = inst.Delete(true, false)
	if err != nil {
		return fmt.Errorf("Failed deleting source instance: %w", err)
	}

	l.Debug("Source instance deleted")

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
