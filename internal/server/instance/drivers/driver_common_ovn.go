package drivers

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/lxc/incus/v7/internal/server/db"
	dbCluster "github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/device"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/internal/server/locking"
	"github.com/lxc/incus/v7/internal/server/network"
	"github.com/lxc/incus/v7/internal/server/project"
	"github.com/lxc/incus/v7/shared/api"
)

type ovnDeviceNetwork struct {
	project string
	name    string
}

type ovnDeviceOperation struct {
	id    int64
	token string
}

type ovnDeviceUndo struct {
	run     func() error
	capture func()
}

// reserveOVNDeviceUpdate spans device validation, backend writes, instance commit and every deferred rollback.
func (d *common) reserveOVNDeviceUpdate(next deviceConfig.Devices) (func() error, error) {
	if d.isSnapshot {
		return func() error { return nil }, nil
	}

	if d.ovnDeviceUpdateOperations != nil {
		return nil, fmt.Errorf("Instance already owns OVN device update reservations")
	}

	names := map[string]struct{}{}
	additive := false
	for _, devices := range []deviceConfig.Devices{d.expandedDevices, next} {
		for name, config := range devices {
			if maps.Equal(d.expandedDevices[name], next[name]) || config["type"] != "nic" || config["network"] == "" {
				continue
			}

			names[config["network"]] = struct{}{}
		}
	}

	for name, config := range next {
		if !maps.Equal(d.expandedDevices[name], config) && config["type"] == "nic" && config["network"] != "" {
			additive = true
		}
	}

	if len(names) == 0 {
		return func() error { return nil }, nil
	}

	parent := d.state.ShutdownCtx
	if !additive {
		parent = context.WithoutCancel(parent)
	}

	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	var operations map[ovnDeviceNetwork]ovnDeviceOperation
	for {
		err := d.state.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
			operations = make(map[ovnDeviceNetwork]ovnDeviceOperation)
			projectRecord, err := dbCluster.GetProject(ctx, tx.Tx(), d.project.Name)
			if err != nil {
				return err
			}

			projectInfo, err := projectRecord.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			for name := range names {
				key := ovnDeviceNetwork{project: project.NetworkProjectForNameFromRecord(projectInfo, name), name: name}
				id, info, _, err := tx.GetNetworkInAnyState(ctx, key.project, key.name)
				if api.StatusErrorCheck(err, http.StatusNotFound) {
					continue
				} else if err != nil {
					return err
				}

				if info.Type != "ovn" {
					continue
				}

				token := uuid.NewString()
				err = tx.AcquireOVNNetworkOperation(ctx, key.project, key.name, token, "nic")
				if err != nil {
					return err
				}

				operations[key] = ovnDeviceOperation{id: id, token: token}
			}

			return nil
		})
		if err == nil {
			break
		}

		if !api.StatusErrorCheck(err, http.StatusConflict) {
			return nil, err
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	keys := slices.Collect(maps.Keys(operations))
	slices.SortFunc(keys, func(a ovnDeviceNetwork, b ovnDeviceNetwork) int {
		return cmp.Or(cmp.Compare(a.project, b.project), cmp.Compare(a.name, b.name))
	})
	var unlocks []locking.UnlockFunc
	release := func() error {
		defer func() {
			d.ovnDeviceUpdateOperations = nil
			d.ovnDeviceUpdateUndo = nil
			d.ovnDeviceUpdateRetain = false
			for i := len(unlocks) - 1; i >= 0; i-- {
				unlocks[i]()
			}
		}()
		if d.ovnDeviceUpdateRetain {
			return errors.New("OVN device update rollback is incomplete; network reservations are retained")
		}

		return d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			for _, key := range keys {
				err := tx.ReleaseOVNNetworkOperation(ctx, key.project, key.name, operations[key].token)
				if err != nil {
					return err
				}
			}

			return nil
		})
	}

	for _, key := range keys {
		unlock, err := network.WaitOVNLifecycle(ctx, key.project, key.name)
		if err != nil {
			return nil, errors.Join(err, release())
		}

		unlocks = append(unlocks, unlock)
	}

	d.ovnDeviceUpdateOperations = operations
	return release, nil
}

// authorizeOVNDeviceUpdate passes the enclosing reservation into each freshly loaded NIC driver.
func (d *common) authorizeOVNDeviceUpdate(dev device.Device) error {
	if d.ovnDeviceUpdateOperations == nil || dev == nil {
		return nil
	}

	ovnDevice, ok := dev.(interface{ OVNNetwork() network.Network })
	if !ok {
		return nil
	}

	n := ovnDevice.OVNNetwork()
	if n == nil {
		return nil
	}

	operation, exists := d.ovnDeviceUpdateOperations[ovnDeviceNetwork{project: n.Project(), name: n.Name()}]
	if !exists || operation.id != n.ID() {
		return api.StatusErrorf(http.StatusConflict, "OVN network %q changed during instance device update", n.Name())
	}

	network.AuthorizeOVNInitialization(n, operation.token)
	cleanupTokens := make(map[int64]string, len(d.ovnDeviceUpdateOperations))
	for _, parentOperation := range d.ovnDeviceUpdateOperations {
		{
			previous, exists := cleanupTokens[parentOperation.id]
			if exists && previous != parentOperation.token {
				return errors.New("Enclosing OVN device update has conflicting cleanup capabilities")
			}
		}

		cleanupTokens[parentOperation.id] = parentOperation.token
	}

	return network.AuthorizeOVNNICCleanupReservations(n, cleanupTokens)
}

// addOVNDeviceUndo keeps OVN reversal acknowledgments until the enclosing instance database commit.
func (d *common) addOVNDeviceUndo(dev device.Device, undo func() error) bool {
	if d.ovnDeviceUpdateOperations == nil || dev == nil {
		return false
	}

	ovnDevice, ok := dev.(interface{ OVNNetwork() network.Network })
	if !ok || ovnDevice.OVNNetwork() == nil {
		return false
	}

	d.ovnDeviceUpdateUndo = append(d.ovnDeviceUpdateUndo, ovnDeviceUndo{run: undo})
	return true
}

func (d *common) addOVNDeviceCleanupUndo(dev device.Device, undo func() error) bool {
	if !d.addOVNDeviceUndo(dev, undo) {
		return false
	}

	volatileDevice, ok := dev.(interface {
		OVNUndoVolatile() func(func() error) error
	})
	if !ok {
		return true
	}

	var withVolatile func(func() error) error
	entry := &d.ovnDeviceUpdateUndo[len(d.ovnDeviceUpdateUndo)-1]
	entry.capture = func() { withVolatile = volatileDevice.OVNUndoVolatile() }
	entry.run = func() error {
		if withVolatile == nil {
			return errors.New("OVN device rollback has no runtime allocation snapshot")
		}

		previous := d.deviceVolatileGetFunc(dev.Name())()
		err := withVolatile(undo)
		restore := d.deviceVolatileGetFunc(dev.Name())()
		for key := range restore {
			restore[key] = ""
		}

		maps.Copy(restore, previous)
		return errors.Join(err, d.deviceVolatileSetFunc(dev.Name())(restore))
	}

	return true
}

func (d *common) captureOVNDeviceUndo() {
	for _, undo := range d.ovnDeviceUpdateUndo {
		if undo.capture != nil {
			undo.capture()
		}
	}
}

// finishOVNDeviceUpdate runs after local instance rollback and preserves reservations if backend reversal fails.
func (d *common) finishOVNDeviceUpdate(committed bool, release func() error) error {
	var undoErr error
	if !committed {
		for i := len(d.ovnDeviceUpdateUndo) - 1; i >= 0; i-- {
			undoErr = errors.Join(undoErr, d.ovnDeviceUpdateUndo[i].run())
		}
	}

	d.ovnDeviceUpdateRetain = undoErr != nil
	return errors.Join(undoErr, release())
}

func (d *common) loadOVNDeviceUndo(inst instance.Instance, name string, config deviceConfig.Device) (device.Device, error) {
	dev, err := d.deviceLoad(inst, name, config, false)
	if err != nil {
		return nil, err
	}

	err = d.authorizeOVNDeviceUpdate(dev)
	return dev, err
}

func (d *common) restoreOVNDevice(inst instance.Instance, name string, config deviceConfig.Device, running bool, manager deviceManager) error {
	dev, err := d.loadOVNDeviceUndo(inst, name, config)
	if err != nil {
		return err
	}

	err = d.deviceAdd(dev, running)
	if err != nil {
		return err
	}

	if running {
		err = dev.PreStartCheck()
		if err != nil {
			return err
		}

		_, err = manager.deviceStart(dev, running)
		return err
	}

	return nil
}
