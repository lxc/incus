package drivers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/device"
	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/internal/server/instance/drivers/qmp"
	"github.com/lxc/incus/v7/internal/server/instance/operationlock"
	"github.com/lxc/incus/v7/internal/server/ip"
	"github.com/lxc/incus/v7/internal/server/network"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/logger"
)

// cleanupStoppedDevices continues every selected device and preserves each
// actual stop failure. A usable legacy device can still stop after a validation
// warning; unsupported instance-type devices retain their original exemption.
func (d *common) cleanupStoppedDevices(entries []deviceConfig.DeviceNamed, selectEntry func(deviceConfig.DeviceNamed) bool,
	load func(deviceConfig.DeviceNamed) (device.Device, error), stop func(device.Device) error,
) error {
	var result error
	for _, entry := range entries {
		if !selectEntry(entry) {
			continue
		}

		dev, err := load(entry)
		if err != nil {
			if errors.Is(err, device.ErrUnsupportedDevType) {
				continue
			}

			d.logger.Error("Failed stop validation for device", logger.Ctx{"device": entry.Name, "err": err})
		}

		if dev == nil {
			if err == nil {
				err = errors.New("Device cleanup loader returned no usable device")
			}

			result = errors.Join(result, fmt.Errorf("Failed loading cleanup device %q: %w", entry.Name, err))
			continue
		}

		err = stop(dev)
		if err != nil {
			d.logger.Error("Failed to stop device", logger.Ctx{"device": dev.Name(), "err": err})
			result = errors.Join(result, fmt.Errorf("Failed cleaning device %q: %w", dev.Name(), err))
		}
	}

	return result
}

// ensureOVNStopCleanupComplete is called only at the existing terminal teardown
// boundary, before restart/ephemeral deletion and operation success. No process
// exit or heartbeat observation is treated as completion of external effects.
func (d *common) ensureOVNStopCleanupComplete() error {
	return d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		if d.ovnMigrationSource != "" {
			return tx.EnsureOVNNICMigrationSourceComplete(ctx, d.ovnMigrationSource, d.localConfig["volatile.uuid"])
		}

		return tx.EnsureOVNNICCleanupCompleteForInstance(ctx, d.localConfig["volatile.uuid"])
	})
}

func (d *common) acknowledgeOVNNICSourceTerminal(terminalErr error) error {
	if terminalErr != nil {
		return terminalErr
	}

	return d.recordOVNNICSourceTerminal()
}

// recordOVNNICSourceTerminal records only successful actual source runtime teardown.
func (d *common) recordOVNNICSourceTerminal() error {
	return d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		operation, err := tx.OVNNICMigrationSourceHook(ctx, d.id, d.localConfig["volatile.uuid"], d.localConfig)
		if err != nil {
			return err
		}

		if d.ovnMigrationSource != "" && operation != d.ovnMigrationSource {
			return errors.New("Original source terminal operation changed")
		}

		if operation == "" {
			return nil
		}

		return tx.RecordOVNNICMigrationSourceTerminal(ctx, operation, d.id, d.localConfig["volatile.uuid"], d.localConfig)
	})
}

// normalizeOVNSnapshotConfig discards host allocations belonging to the
// snapshot's original run. It never edits the snapshot or its cleanup records.
func normalizeOVNSnapshotConfig(config map[string]string, devices deviceConfig.Devices) map[string]string {
	result := maps.Clone(config)
	owned := map[string]bool{}
	for name, dev := range devices {
		if dev["type"] == "nic" && dev["nictype"] == "ovn" {
			owned[name] = true
		}
	}

	for key, value := range config {
		if value == "" || !strings.HasPrefix(key, "volatile.") {
			continue
		}

		for _, suffix := range []string{".last_state.ovn.host", ".last_state.ovn.physical"} {
			if strings.HasSuffix(key, suffix) {
				name := strings.TrimSuffix(strings.TrimPrefix(key, "volatile."), suffix)
				if name != "" {
					owned[name] = true
				}
			}
		}
	}

	for name := range owned {
		for _, key := range db.OVNNICStopVolatileKeys() {
			delete(result, "volatile."+name+"."+key)
		}
	}

	return result
}

// captureOVNStopSource uses the existing driver loader/capabilities. Desired
// selection is not a certificate of absent historical devices; pending source
// debt remains separately visible at the terminal and preparation boundaries.
func (d *common) captureOVNStopSource(inst instance.Instance) error {
	return d.captureOVNStopDevices(d.expandedDevices.Reversed(), func(entry deviceConfig.DeviceNamed) (device.Device, error) {
		dev, err := d.deviceLoad(inst, entry.Name, entry.Config, false)
		if dev != nil {
			capabilityErr := d.authorizeOVNDeviceUpdate(dev)
			if capabilityErr != nil {
				return nil, errors.Join(err, capabilityErr)
			}
		}

		return dev, err
	})
}

// captureOVNStopDevices stops before workload dispatch on failed publication.
// Already published entries are retained if a later entry fails; this is input
// preservation, never an acknowledgment or rollback of external effects.
func (d *common) captureOVNStopDevices(entries []deviceConfig.DeviceNamed, load func(deviceConfig.DeviceNamed) (device.Device, error)) error {
	for _, entry := range entries {
		if entry.Config["type"] != "nic" || entry.Config["network"] == "" {
			continue
		}

		dev, err := load(entry)
		if errors.Is(err, device.ErrUnsupportedDevType) {
			continue
		}

		if dev == nil {
			if err == nil {
				err = errors.New("Source capture loader returned no usable device")
			}

			return fmt.Errorf("Failed loading source capture device %q: %w", entry.Name, err)
		}

		if err != nil {
			d.logger.Error("Failed source capture validation for device", logger.Ctx{"device": entry.Name, "err": err})
		}

		capture, ok := dev.(interface{ OVNStopCleanupCapture() error })
		if !ok {
			continue
		}

		err = capture.OVNStopCleanupCapture()
		if err != nil {
			return fmt.Errorf("Failed preserving original OVN NIC %q before workload effects: %w", entry.Name, err)
		}
	}

	return nil
}

// pendingOVNStopSources enumerates durable source-owned debt rather than the
// current instance device list. A missing record is not legacy absence proof.
func (d *common) pendingOVNStopSources() ([]db.OVNNICCleanup, error) {
	var sources []db.OVNNICCleanup
	err := d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		all, err := tx.OVNNICCleanups(ctx)
		if err != nil {
			return err
		}

		for _, a := range all {
			if a.InstanceUUID == d.localConfig["volatile.uuid"] {
				sources = append(sources, a)
			}
		}

		return nil
	})
	return sources, err
}

// retryOVNStopBeforeStart completes original NIC cleanup that an unclean stop left pending, using the
// ordinary stopped retry; a start never proceeds past unacknowledged debt. It only acts on recorded
// incomplete cleanup or on stored claims from an earlier kernel boot, such as after a host crash, and
// never on this possibly stale in-memory configuration, which a restart's own stop already resolved.
func (d *common) retryOVNStopBeforeStart(retry func() error) error {
	sources, err := d.pendingOVNStopSources()
	if err != nil {
		return err
	}

	if len(sources) == 0 {
		boot, err := ip.CurrentBootID()
		if err != nil {
			return err
		}

		earlierBoot := false
		err = d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			rows, err := tx.Tx().QueryContext(ctx, "SELECT value FROM instances_config WHERE instance_id=? AND (key LIKE 'volatile.%.last_state.ovn.host' OR key LIKE 'volatile.%.last_state.ovn.physical')", d.id)
			if err != nil {
				return err
			}

			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var raw string
				err = rows.Scan(&raw)
				if err != nil {
					return err
				}

				// Physical claims record the boot of their representor.
				var claim struct {
					BootID      string
					Representor struct{ BootID string }
				}

				if json.Unmarshal([]byte(raw), &claim) == nil {
					for _, id := range []string{claim.BootID, claim.Representor.BootID} {
						if id != "" && id != boot {
							earlierBoot = true
						}
					}
				}
			}

			return rows.Err()
		})
		if err != nil {
			return err
		}

		if !earlierBoot {
			return nil
		}
	}

	err = retry()
	if err != nil && !errors.Is(err, ErrInstanceIsStopped) {
		return fmt.Errorf("Failed completing original OVN NIC cleanup before start: %w", err)
	}

	return nil
}

// retryStoppedOVNStop serializes a non-stateful stopped retry using the existing
// Stop operation policy. It rechecks state and rereads source debt after locking.
// No process observation or completed Stop operation acknowledges pending debt.
func (d *common) retryStoppedOVNStop(inst instance.Instance, status func() api.StatusCode, stop func(device.Device) error, terminal func() error, sourceTerminal ...func() error) error {
	verify := d.ensureOVNStopCleanupComplete
	if len(sourceTerminal) > 0 {
		verify = sourceTerminal[0]
	}

	sources, err := d.pendingOVNStopSources()
	if err != nil {
		return err
	}

	claims, err := d.pendingOVNStartClaims(sources)
	if err != nil {
		return err
	}

	if len(sources) == 0 && len(claims) == 0 {
		err = verify()
		if err != nil {
			return err
		}

		return ErrInstanceIsStopped
	}

	op, err := operationlock.CreateWaitGet(d.Project().Name, d.Name(), d.op, operationlock.ActionStop, d.stopInheritableActions(), false, true)
	if err != nil {
		if errors.Is(err, operationlock.ErrNonReusuableSucceeded) {
			return verify()
		}

		return err
	}

	if status() != api.Stopped {
		err = errors.New("Instance state changed before original NIC cleanup retry")
		op.Done(err)
		return err
	}

	sources, err = d.pendingOVNStopSources()
	if err == nil {
		err = d.retryOriginalOVNStopDevices(sources, func(source *network.OVNInstanceNICStopOpts) (device.Device, error) {
			n, err := d.loadOVNStopNetwork(source.NetworkID)
			if err != nil {
				return nil, err
			}

			dev, err := device.NewOVNStopCleanup(inst, d.state, n, source)
			if dev != nil {
				capabilityErr := d.authorizeOVNDeviceUpdate(dev)
				if capabilityErr != nil {
					return nil, errors.Join(err, capabilityErr)
				}
			}

			return dev, err
		}, stop)
	}

	if err == nil {
		claims, err = d.pendingOVNStartClaims(sources)
		if err == nil {
			err = d.retryOVNStartClaimDevices(inst, claims, stop)
		}
	}

	if err == nil {
		err = d.acknowledgeOVNNICSourceTerminal(terminal())
	}

	if err == nil {
		err = d.finishOVNStopCleanupWithBarrier(verify)
	}

	err = errors.Join(err, verify())
	op.Done(err)
	return err
}

// pendingOVNStartClaims returns local allocation claims of NICs that have no
// recorded source cleanup, such as one left by a container that failed to spawn.
func (d *common) pendingOVNStartClaims(sources []db.OVNNICCleanup) (map[string]int64, error) {
	recorded := map[string]bool{}
	for _, a := range sources {
		recorded[a.DeviceName] = true
	}

	claims := map[string]int64{}
	for key, raw := range d.localConfig {
		if raw == "" || !strings.HasPrefix(key, "volatile.") || (!strings.HasSuffix(key, ".last_state.ovn.host") && !strings.HasSuffix(key, ".last_state.ovn.physical")) {
			continue
		}

		var claim struct {
			Version      int
			NetworkID    int64
			SourceNodeID int64
			InstanceUUID string
			InstanceID   int
			DeviceName   string
		}

		err := json.Unmarshal([]byte(raw), &claim)
		if err != nil || claim.Version != 1 || claim.NetworkID <= 0 || claim.DeviceName == "" || !strings.HasPrefix(key, "volatile."+claim.DeviceName+".last_state.ovn.") {
			return nil, fmt.Errorf("Original NIC claim %q is invalid", key)
		}

		if claim.SourceNodeID != d.state.DB.Cluster.GetNodeID() || claim.InstanceUUID != d.localConfig["volatile.uuid"] || claim.InstanceID != d.id {
			return nil, fmt.Errorf("Original NIC claim %q belongs to another member or instance", key)
		}

		if recorded[claim.DeviceName] {
			continue
		}

		previous, exists := claims[claim.DeviceName]
		if exists && previous != claim.NetworkID {
			return nil, fmt.Errorf("Original NIC claims of device %q disagree", claim.DeviceName)
		}

		claims[claim.DeviceName] = claim.NetworkID
	}

	return claims, nil
}

// retryOVNStartClaimDevices stops each claimed NIC through its ordinary source
// capture and cleanup, only while it is still configured on the claimed network.
func (d *common) retryOVNStartClaimDevices(inst instance.Instance, claims map[string]int64, stop func(device.Device) error) error {
	var result error
	for name, networkID := range claims {
		entry, ok := d.expandedDevices[name]
		if !ok || entry["type"] != "nic" {
			result = errors.Join(result, fmt.Errorf("Original NIC claim device %q is no longer configured", name))
			continue
		}

		dev, err := d.deviceLoad(inst, name, entry, false)
		if err != nil {
			result = errors.Join(result, fmt.Errorf("Failed loading original NIC claim device %q: %w", name, err))
			continue
		}

		ovnDevice, ok := dev.(interface{ OVNNetwork() network.Network })
		if !ok || ovnDevice.OVNNetwork() == nil || ovnDevice.OVNNetwork().ID() != networkID {
			result = errors.Join(result, fmt.Errorf("Original NIC claim device %q network identity changed", name))
			continue
		}

		retire, ok := dev.(interface{ OVNRetireUnpublishedClaim() (bool, error) })
		if ok {
			retired, err := retire.OVNRetireUnpublishedClaim()
			if err != nil {
				result = errors.Join(result, fmt.Errorf("Failed retiring original NIC claim device %q: %w", name, err))
				continue
			}

			if retired {
				continue
			}
		}

		err = stop(dev)
		if err != nil {
			result = errors.Join(result, fmt.Errorf("Failed cleaning original NIC claim device %q: %w", name, err))
		}
	}

	return result
}

// retryOriginalOVNStopDevices requires the exact original primary network ID
// before dispatch. A missing/unsupported/replaced device is a failure, never a
// successful skip of source-owned cleanup. Independent entries still retry.
func (d *common) retryOriginalOVNStopDevices(sources []db.OVNNICCleanup,
	load func(*network.OVNInstanceNICStopOpts) (device.Device, error), stop func(device.Device) error,
) error {
	var result error
	for _, a := range sources {
		source, networkID, err := network.OVNNICCleanupSource(a, d.state.DB.Cluster.GetNodeID(), d.state.ServerName)
		if err == nil && a.InstanceUUID != d.localConfig["volatile.uuid"] {
			err = errors.New("Original NIC cleanup belongs to another instance UUID")
		}

		if err != nil {
			result = errors.Join(result, fmt.Errorf("Failed reading original cleanup device %q: %w", a.DeviceName, err))
			continue
		}

		dev, loadErr := load(source)
		if errors.Is(loadErr, device.ErrUnsupportedDevType) {
			result = errors.Join(result, fmt.Errorf("Original NIC cleanup device %q is unsupported: %w", a.DeviceName, loadErr))
			continue
		}

		if dev == nil {
			if loadErr == nil {
				loadErr = errors.New("Original NIC cleanup loader returned no usable device")
			}

			result = errors.Join(result, fmt.Errorf("Failed loading original cleanup device %q: %w", a.DeviceName, loadErr))
			continue
		}

		ovnDevice, ok := dev.(interface{ OVNNetwork() network.Network })
		if !ok || ovnDevice.OVNNetwork() == nil || ovnDevice.OVNNetwork().ID() != networkID || dev.Name() != a.DeviceName {
			result = errors.Join(result, fmt.Errorf("Original NIC cleanup device %q network/name identity changed", a.DeviceName))
			continue
		}

		if loadErr != nil {
			d.logger.Error("Failed original cleanup validation for usable device", logger.Ctx{"device": a.DeviceName, "err": loadErr})
		}

		selected, ok := dev.(interface{ OVNStopCleanupSelectGeneration(string) })
		if !ok {
			result = errors.Join(result, fmt.Errorf("Original NIC cleanup device %q cannot bind its source generation", a.DeviceName))
			continue
		}

		selected.OVNStopCleanupSelectGeneration(a.Generation)
		err = stop(dev)
		if err != nil {
			result = errors.Join(result, fmt.Errorf("Failed retrying original cleanup device %q: %w", a.DeviceName, err))
		}
	}

	return result
}

// OVNStopCleanupVolatileRetired updates the source driver's in-memory view only
// after SQL retirement. It never writes target/current SQL via VolatileSet.
// A changed memory allocation is a visible conflict and is left untouched.
func (d *common) OVNStopCleanupVolatileRetired(instanceUUID string, deviceName string, original map[string]string) error {
	if d.localConfig["volatile.uuid"] != instanceUUID {
		return errors.New("NIC volatile retirement instance UUID changed")
	}

	keys := db.OVNNICStopVolatileKeys()
	for _, key := range keys {
		fullKey := "volatile." + deviceName + "." + key
		value := d.localConfig[fullKey]
		expanded := d.expandedConfig[fullKey]
		if (value != "" && value != original[key]) || (expanded != "" && expanded != original[key]) {
			return fmt.Errorf("Original NIC volatile memory allocation %q changed", key)
		}
	}

	for _, key := range keys {
		fullKey := "volatile." + deviceName + "." + key
		delete(d.localConfig, fullKey)
		delete(d.expandedConfig, fullKey)
	}

	return nil
}

// stopAfterMigrationHandover preserves the Stop result even if process forcing
// succeeds. A stopped-process result still requires the durable cleanup barrier.
func stopAfterMigrationHandover(stop, forceStop, verifyCleanup func() error) error {
	stopErr := stop()
	var forceErr error
	if stopErr != nil {
		forceErr = forceStop()
	}

	if errors.Is(stopErr, ErrInstanceIsStopped) {
		stopErr = nil
	}

	return errors.Join(stopErr, forceErr, verifyCleanup())
}

// migrationSendSourceResult preserves committed handover before reporting
// source cleanup failure. Uncommitted transfer failures retain rollback.
func migrationSendSourceResult(committed bool, transferErr, cleanupErr error, preserveHandover func()) error {
	if transferErr != nil && !committed {
		return transferErr
	}

	preserveHandover()
	return cleanupErr
}

// statefulStopCleanupResult retains checkpoint metadata for a stopped workload
// while keeping teardown errors visible. Running failures do not mark stateful.
func statefulStopCleanupResult(waitErr error, running func() bool, persistStateful func() error) error {
	if waitErr != nil && running() {
		return waitErr
	}

	return errors.Join(waitErr, persistStateful())
}

// migrationDumpCleanupResult sends the existing restore result before waiting
// for an outstanding asynchronous final dump. A completed dump is not source cleanup ACK.
func migrationDumpCleanupResult(live, pendingFinalDump bool, transferErr error, restoreSuccess chan<- bool, dumpSuccess <-chan error, verifyCleanup func() error) error {
	if !live {
		return transferErr
	}

	restoreSuccess <- transferErr == nil
	if transferErr != nil {
		return transferErr
	}

	var dumpErr error
	if pendingFinalDump {
		dumpErr = <-dumpSuccess
	}

	return errors.Join(dumpErr, verifyCleanup())
}

// shutdownAfterPowerdown keeps hook ownership after a monitor disconnect.
// Disconnect alone does not finish an operation or acknowledge device cleanup.
func shutdownAfterPowerdown(requestErr error, failRequest func(error), repeatRequest func(), waitCleanup func() error) error {
	if requestErr != nil && !errors.Is(requestErr, qmp.ErrMonitorDisconnect) {
		failRequest(requestErr)
		return requestErr
	}

	if requestErr == nil {
		repeatRequest()
	}

	return waitCleanup()
}

// shutdownCleanupResult keeps prior wait/status errors and checks durable debt
// before graceful shutdown can report success.
func shutdownCleanupResult(waitErr error, status api.StatusCode, verifyCleanup func() error) error {
	if status != api.Stopped {
		errPrefix := fmt.Errorf("Failed shutting down instance, status is %q", status)
		if waitErr != nil {
			return fmt.Errorf("%s: %w", errPrefix.Error(), waitErr)
		}

		return errPrefix
	}

	if waitErr != nil {
		return waitErr
	}

	return verifyCleanup()
}

// finishOVNStopCleanup runs only after truthful terminal driver teardown.
// Resolve original primary networks by numeric ID, independently of Desired.
func (d *common) finishOVNStopCleanup() error {
	return d.finishOVNStopCleanupWithBarrier(d.ensureOVNStopCleanupComplete)
}

func (d *common) finishOVNStopCleanupWithBarrier(verify func() error) error {
	sources, err := d.pendingOVNStopSources()
	if err != nil {
		return err
	}

	var result error
	for _, a := range sources {
		_, id, err := network.OVNNICCleanupSource(a, d.state.DB.Cluster.GetNodeID(), d.state.ServerName)
		if err == nil {
			var n network.Network
			n, err = d.loadOVNStopNetwork(id)
			if err == nil {
				complete, ok := n.(interface {
					InstanceDevicePortStopComplete(context.Context, string, string, string) error
				})
				if !ok {
					err = errors.New("Original NIC terminal network capability changed")
				} else {
					err = complete.InstanceDevicePortStopComplete(context.Background(), a.InstanceUUID, a.DeviceName, a.Generation)
				}
			}
		}

		result = errors.Join(result, err)
	}

	return errors.Join(result, verify())
}

func (d *common) loadOVNStopNetwork(id int64) (network.Network, error) {
	var name, projectName string
	err := d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		name, projectName, err = tx.GetNetworkNameAndProjectWithID(ctx, int(id))
		return err
	})
	if err != nil {
		return nil, err
	}

	n, err := network.LoadByName(d.state, projectName, name)
	if err != nil {
		return nil, err
	}

	if n.ID() != id || n.Type() != "ovn" {
		return nil, errors.New("Original NIC numeric network identity changed")
	}

	return n, nil
}

func completeLiveOVNStop(dev device.Device, instanceRunning bool) error {
	if !instanceRunning {
		return nil
	}

	complete, ok := dev.(interface{ OVNStopCleanupComplete() error })
	if !ok {
		return nil
	}

	return complete.OVNStopCleanupComplete()
}

func (d *common) originalOVNCleanupRetry() (*db.OVNNICCleanupRetry, error) {
	var selected *db.OVNNICCleanupRetry
	err := d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		plans, err := tx.OVNNICCleanupRetries(ctx)
		if err != nil {
			return err
		}

		for _, plan := range plans {
			if plan.InstanceID != d.id || plan.InstanceUUID != d.localConfig["volatile.uuid"] {
				continue
			}

			if plan.Project != d.project.Name || plan.Name != d.name {
				return errors.New("Original cleanup instance identity changed")
			}

			selected = &plan
		}

		return nil
	})
	return selected, err
}

// retryTransferredOVNStop performs only original NIC work after positive source terminal teardown.
func (d *common) retryTransferredOVNStop(inst instance.Instance, plan db.OVNNICCleanupRetry,
	loadSource func(*network.OVNInstanceNICStopOpts) (device.Device, error), stop func(device.Device) error,
) (err error) {
	if !plan.Transferred || plan.Operation == "" || len(plan.Sources) == 0 || d.ovnDeviceUpdateOperations != nil {
		return errors.New("Original transferred cleanup retry authority is missing")
	}

	op, err := operationlock.CreateWaitGet(d.Project().Name, d.Name(), d.op, operationlock.ActionStop, d.stopInheritableActions(), false, true)
	if err != nil {
		return err
	}

	defer func() { op.Done(err) }()
	var reservations []db.OVNNICCleanupReservation
	ids := []int64{}
	for _, a := range plan.Sources {
		ids = append(ids, a.NetworkIDs...)
	}

	slices.Sort(ids)
	ids = slices.Compact(ids)
	err = d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		current, err := tx.OVNNICCleanupRetries(ctx)
		if err != nil {
			return err
		}

		matched := false
		for _, p := range current {
			if p.InstanceID != plan.InstanceID || p.InstanceUUID != plan.InstanceUUID {
				continue
			}

			if p.ProjectID != plan.ProjectID || p.Operation != plan.Operation || !p.Transferred || len(p.Sources) != len(plan.Sources) {
				return errors.New("Original transferred cleanup lineage changed before retry")
			}

			for i := range p.Sources {
				if p.Sources[i].Generation != plan.Sources[i].Generation || p.Sources[i].SourceNodeID != plan.Sources[i].SourceNodeID || p.Sources[i].InstanceUUID != plan.Sources[i].InstanceUUID || p.Sources[i].DeviceName != plan.Sources[i].DeviceName || p.Sources[i].Version != plan.Sources[i].Version || p.Sources[i].Payload != plan.Sources[i].Payload || !slices.Equal(p.Sources[i].NetworkIDs, plan.Sources[i].NetworkIDs) {
					return errors.New("Original transferred cleanup source changed before retry")
				}
			}

			matched = true
		}

		if !matched {
			return errors.New("Original transferred cleanup source disappeared before retry")
		}

		reservations, err = tx.AcquireOVNNICCleanupOperations(ctx, ids, nil)
		return err
	})
	if err != nil {
		return err
	}

	d.ovnDeviceUpdateOperations = map[ovnDeviceNetwork]ovnDeviceOperation{}
	for _, reservation := range reservations {
		d.ovnDeviceUpdateOperations[ovnDeviceNetwork{project: reservation.Project, name: reservation.Name}] = ovnDeviceOperation{id: reservation.NetworkID, token: reservation.Token}
	}

	defer func() {
		d.ovnDeviceUpdateOperations = nil
		releaseErr := d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.ReleaseOVNNICCleanupOperations(ctx, reservations)
		})
		err = errors.Join(err, releaseErr)
	}()
	devices := map[string]device.Device{}
	networks := map[string]network.Network{}
	for _, a := range plan.Sources {
		source, id, err := network.OVNNICCleanupSource(a, d.state.DB.Cluster.GetNodeID(), d.state.ServerName)
		if err != nil {
			return err
		}

		dev, err := loadSource(source)
		if err != nil {
			return err
		}

		bound, ok := dev.(interface{ OVNNetwork() network.Network })
		if !ok || bound.OVNNetwork() == nil || bound.OVNNetwork().ID() != id {
			return errors.New("Original retry network identity changed")
		}

		n := bound.OVNNetwork()
		validate, ok := n.(interface {
			InstanceDevicePortStopRetryValidate(context.Context, string, string, string) error
		})
		if !ok {
			return errors.New("Original transferred cleanup lacks rooted retry validation")
		}

		err = validate.InstanceDevicePortStopRetryValidate(context.Background(), a.InstanceUUID, a.DeviceName, a.Generation)
		if err != nil {
			return err
		}

		host, ok := dev.(interface{ OVNStopCleanupRetryValidate() error })
		if !ok {
			return errors.New("Original transferred cleanup lacks host retry validation")
		}

		err = host.OVNStopCleanupRetryValidate()
		if err != nil {
			return err
		}

		devices[a.DeviceName] = dev
		networks[a.DeviceName] = n
	}

	err = d.retryOriginalOVNStopDevices(plan.Sources, func(source *network.OVNInstanceNICStopOpts) (device.Device, error) {
		return devices[source.DeviceName], nil
	}, stop)
	if err != nil {
		return err
	}

	for _, a := range plan.Sources {
		complete, ok := networks[a.DeviceName].(interface {
			InstanceDevicePortStopComplete(context.Context, string, string, string) error
		})
		if !ok {
			return errors.New("Original transferred cleanup lacks terminal acknowledgment")
		}

		err = complete.InstanceDevicePortStopComplete(context.Background(), a.InstanceUUID, a.DeviceName, a.Generation)
		if err != nil {
			return err
		}
	}

	return d.state.DB.Cluster.Transaction(context.Background(), func(ctx context.Context, tx *db.ClusterTx) error {
		return tx.EnsureOVNNICMigrationSourceComplete(ctx, plan.Operation, plan.InstanceUUID)
	})
}
