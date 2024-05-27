package drivers

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/flosch/pongo2"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/kballard/go-shellquote"
	"github.com/mdlayher/vsock"
	"github.com/pkg/sftp"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v2"

	"github.com/lxc/incus/v6/client"
	internalInstance "github.com/lxc/incus/v6/internal/instance"
	"github.com/lxc/incus/v6/internal/instancewriter"
	"github.com/lxc/incus/v6/internal/jmap"
	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/internal/migration"
	"github.com/lxc/incus/v6/internal/ports"
	"github.com/lxc/incus/v6/internal/revert"
	"github.com/lxc/incus/v6/internal/server/apparmor"
	"github.com/lxc/incus/v6/internal/server/cgroup"
	"github.com/lxc/incus/v6/internal/server/db"
	dbCluster "github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/device"
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	"github.com/lxc/incus/v6/internal/server/device/nictype"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/drivers/qmp"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/instance/operationlock"
	"github.com/lxc/incus/v6/internal/server/lifecycle"
	"github.com/lxc/incus/v6/internal/server/metrics"
	localMigration "github.com/lxc/incus/v6/internal/server/migration"
	"github.com/lxc/incus/v6/internal/server/network"
	"github.com/lxc/incus/v6/internal/server/operations"
	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/internal/server/resources"
	"github.com/lxc/incus/v6/internal/server/response"
	"github.com/lxc/incus/v6/internal/server/state"
	storagePools "github.com/lxc/incus/v6/internal/server/storage"
	storageDrivers "github.com/lxc/incus/v6/internal/server/storage/drivers"
	pongoTemplate "github.com/lxc/incus/v6/internal/server/template"
	localUtil "github.com/lxc/incus/v6/internal/server/util"
	localvsock "github.com/lxc/incus/v6/internal/server/vsock"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	agentAPI "github.com/lxc/incus/v6/shared/api/agent"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/osarch"
	"github.com/lxc/incus/v6/shared/subprocess"
	localtls "github.com/lxc/incus/v6/shared/tls"
	"github.com/lxc/incus/v6/shared/units"
	"github.com/lxc/incus/v6/shared/util"
)

// incus-agent files
//
//go:embed agent-loader/*
var incusAgentLoader embed.FS

// QEMUDefaultCPUCores defines the default number of cores a VM will get if no limit specified.
const QEMUDefaultCPUCores = 1

// QEMUDefaultMemSize is the default memory size for VMs if no limit specified.
const QEMUDefaultMemSize = "1GiB"

// qemuSerialChardevName is used to communicate state with QEMU via QMP.
const qemuSerialChardevName = "qemu_serial-chardev"

// qemuPCIDeviceIDStart is the first PCI slot used for user configurable devices.
const qemuPCIDeviceIDStart = 4

// qemuDeviceIDPrefix used as part of the name given QEMU devices generated from user added devices.
const qemuDeviceIDPrefix = "dev-incus_"

// qemuNetDevIDPrefix used as part of the name given QEMU netdevs generated from user added devices.
const qemuNetDevIDPrefix = "incus_"

// qemuBlockDevIDPrefix used as part of the name given QEMU blockdevs generated from user added devices.
const qemuBlockDevIDPrefix = "incus_"

// qemuMigrationNBDExportName is the name of the disk device export by the migration NBD server.
const qemuMigrationNBDExportName = "incus_root"

// OVMF firmwares.
type ovmfFirmware struct {
	code string
	vars string
}

var ovmfGenericFirmwares = []ovmfFirmware{
	{code: "OVMF_CODE.4MB.fd", vars: "OVMF_VARS.4MB.fd"},
	{code: "OVMF_CODE_4M.fd", vars: "OVMF_VARS_4M.fd"},
	{code: "OVMF_CODE.4m.fd", vars: "OVMF_VARS.4m.fd"},
	{code: "OVMF_CODE.2MB.fd", vars: "OVMF_VARS.2MB.fd"},
	{code: "OVMF_CODE.fd", vars: "OVMF_VARS.fd"},
	{code: "OVMF_CODE.fd", vars: "qemu.nvram"},
}

var ovmfSecurebootFirmwares = []ovmfFirmware{
	{code: "OVMF_CODE.4MB.fd", vars: "OVMF_VARS.4MB.ms.fd"},
	{code: "OVMF_CODE_4M.ms.fd", vars: "OVMF_VARS_4M.ms.fd"},
	{code: "OVMF_CODE_4M.secboot.fd", vars: "OVMF_VARS_4M.secboot.fd"},
	{code: "OVMF_CODE.secboot.4m.fd", vars: "OVMF_VARS.4m.fd"},
	{code: "OVMF_CODE.secboot.fd", vars: "OVMF_VARS.secboot.fd"},
	{code: "OVMF_CODE.secboot.fd", vars: "OVMF_VARS.fd"},
	{code: "OVMF_CODE.2MB.fd", vars: "OVMF_VARS.2MB.ms.fd"},
	{code: "OVMF_CODE.fd", vars: "OVMF_VARS.ms.fd"},
	{code: "OVMF_CODE.fd", vars: "qemu.nvram"},
}

var ovmfCSMFirmwares = []ovmfFirmware{
	{code: "seabios.bin", vars: "seabios.bin"},
	{code: "OVMF_CODE.4MB.CSM.fd", vars: "OVMF_VARS.4MB.CSM.fd"},
	{code: "OVMF_CODE.csm.4m.fd", vars: "OVMF_VARS.4m.fd"},
	{code: "OVMF_CODE.2MB.CSM.fd", vars: "OVMF_VARS.2MB.CSM.fd"},
	{code: "OVMF_CODE.CSM.fd", vars: "OVMF_VARS.CSM.fd"},
	{code: "OVMF_CODE.csm.fd", vars: "OVMF_VARS.fd"},
}

// qemuSparseUSBPorts is the amount of sparse USB ports for VMs.
// 4 are reserved, and the other 4 can be used for any USB device.
const qemuSparseUSBPorts = 8

var errQemuAgentOffline = fmt.Errorf("VM agent isn't currently running")

type monitorHook func(m *qmp.Monitor) error

// qemuLoad creates a Qemu instance from the supplied InstanceArgs.
func qemuLoad(s *state.State, args db.InstanceArgs, p api.Project) (instance.Instance, error) {
	// Create the instance struct.
	d := qemuInstantiate(s, args, nil, p)

	// Expand config and devices.
	err := d.expandConfig()
	if err != nil {
		return nil, err
	}

	return d, nil
}

// qemuInstantiate creates a Qemu struct without expanding config. The expandedDevices argument is
// used during device config validation when the devices have already been expanded and we do not
// have access to the profiles used to do it. This can be safely passed as nil if not required.
func qemuInstantiate(s *state.State, args db.InstanceArgs, expandedDevices deviceConfig.Devices, p api.Project) *qemu {
	d := &qemu{
		common: common{
			state: s,

			architecture: args.Architecture,
			creationDate: args.CreationDate,
			dbType:       args.Type,
			description:  args.Description,
			ephemeral:    args.Ephemeral,
			expiryDate:   args.ExpiryDate,
			id:           args.ID,
			lastUsedDate: args.LastUsedDate,
			localConfig:  args.Config,
			localDevices: args.Devices,
			logger:       logger.AddContext(logger.Ctx{"instanceType": args.Type, "instance": args.Name, "project": args.Project}),
			name:         args.Name,
			node:         args.Node,
			profiles:     args.Profiles,
			project:      p,
			isSnapshot:   args.Snapshot,
			stateful:     args.Stateful,
		},
	}

	// Get the architecture name.
	archName, err := osarch.ArchitectureName(d.architecture)
	if err == nil {
		d.architectureName = archName
	}

	// Cleanup the zero values.
	if d.expiryDate.IsZero() {
		d.expiryDate = time.Time{}
	}

	if d.creationDate.IsZero() {
		d.creationDate = time.Time{}
	}

	if d.lastUsedDate.IsZero() {
		d.lastUsedDate = time.Time{}
	}

	// This is passed during expanded config validation.
	if expandedDevices != nil {
		d.expandedDevices = expandedDevices
	}

	return d
}

// qemuCreate creates a new storage volume record and returns an initialized Instance.
// Returns a revert fail function that can be used to undo this function if a subsequent step fails.
func qemuCreate(s *state.State, args db.InstanceArgs, p api.Project) (instance.Instance, revert.Hook, error) {
	revert := revert.New()
	defer revert.Fail()

	// Create the instance struct.
	d := &qemu{
		common: common{
			state: s,

			architecture: args.Architecture,
			creationDate: args.CreationDate,
			dbType:       args.Type,
			description:  args.Description,
			ephemeral:    args.Ephemeral,
			expiryDate:   args.ExpiryDate,
			id:           args.ID,
			lastUsedDate: args.LastUsedDate,
			localConfig:  args.Config,
			localDevices: args.Devices,
			logger:       logger.AddContext(logger.Ctx{"instanceType": args.Type, "instance": args.Name, "project": args.Project}),
			name:         args.Name,
			node:         args.Node,
			profiles:     args.Profiles,
			project:      p,
			isSnapshot:   args.Snapshot,
			stateful:     args.Stateful,
		},
	}

	// Get the architecture name.
	archName, err := osarch.ArchitectureName(d.architecture)
	if err == nil {
		d.architectureName = archName
	}

	// Cleanup the zero values.
	if d.expiryDate.IsZero() {
		d.expiryDate = time.Time{}
	}

	if d.creationDate.IsZero() {
		d.creationDate = time.Time{}
	}

	if d.lastUsedDate.IsZero() {
		d.lastUsedDate = time.Time{}
	}

	if args.Snapshot {
		d.logger.Info("Creating instance snapshot", logger.Ctx{"ephemeral": d.ephemeral})
	} else {
		d.logger.Info("Creating instance", logger.Ctx{"ephemeral": d.ephemeral})
	}

	// Load the config.
	err = d.init()
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to expand config: %w", err)
	}

	// Validate expanded config (allows mixed instance types for profiles).
	err = instance.ValidConfig(s.OS, d.expandedConfig, true, instancetype.Any)
	if err != nil {
		return nil, nil, fmt.Errorf("Invalid config: %w", err)
	}

	err = instance.ValidDevices(s, d.project, d.Type(), d.localDevices, d.expandedDevices)
	if err != nil {
		return nil, nil, fmt.Errorf("Invalid devices: %w", err)
	}

	// Retrieve the instance's storage pool.
	_, rootDiskDevice, err := d.getRootDiskDevice()
	if err != nil {
		return nil, nil, fmt.Errorf("Failed getting root disk: %w", err)
	}

	if rootDiskDevice["pool"] == "" {
		return nil, nil, fmt.Errorf("The instance's root device is missing the pool property")
	}

	// Initialize the storage pool.
	d.storagePool, err = storagePools.LoadByName(d.state, rootDiskDevice["pool"])
	if err != nil {
		return nil, nil, fmt.Errorf("Failed loading storage pool: %w", err)
	}

	volType, err := storagePools.InstanceTypeToVolumeType(d.Type())
	if err != nil {
		return nil, nil, err
	}

	storagePoolSupported := false
	for _, supportedType := range d.storagePool.Driver().Info().VolumeTypes {
		if supportedType == volType {
			storagePoolSupported = true
			break
		}
	}

	if !storagePoolSupported {
		return nil, nil, fmt.Errorf("Storage pool does not support instance type")
	}

	if !d.IsSnapshot() {
		// Add devices to instance.
		cleanup, err := d.devicesAdd(d, false)
		if err != nil {
			return nil, nil, err
		}

		revert.Add(cleanup)
	}

	if d.isSnapshot {
		d.logger.Info("Created instance snapshot", logger.Ctx{"ephemeral": d.ephemeral})
	} else {
		d.logger.Info("Created instance", logger.Ctx{"ephemeral": d.ephemeral})
	}

	if d.isSnapshot {
		d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceSnapshotCreated.Event(d, nil))
	} else {
		err = d.state.Authorizer.AddInstance(d.state.ShutdownCtx, d.project.Name, d.Name())
		if err != nil {
			logger.Error("Failed to add instance to authorizer", logger.Ctx{"name": d.Name(), "project": d.project.Name, "error": err})
		}

		revert.Add(func() { d.state.Authorizer.DeleteInstance(d.state.ShutdownCtx, d.project.Name, d.Name()) })

		d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceCreated.Event(d, map[string]any{
			"type":         api.InstanceTypeVM,
			"storage-pool": d.storagePool.Name(),
			"location":     d.Location(),
		}))
	}

	cleanup := revert.Clone().Fail
	revert.Success()
	return d, cleanup, err
}

// qemu is the QEMU virtual machine driver.
type qemu struct {
	common

	// Cached handles.
	// Do not use these variables directly, instead use their associated get functions so they
	// will be initialized on demand.
	architectureName string

	// Stateful migration streams.
	migrationReceiveStateful map[string]io.ReadWriteCloser
}

// getAgentClient returns the current agent client handle.
// Callers should check that the instance is running (and therefore mounted) before caling this function,
// otherwise the qmp.Connect call will fail to use the monitor socket file.
func (d *qemu) getAgentClient() (*http.Client, error) {
	// Check if the agent is running.
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
	if err != nil {
		return nil, err
	}

	if !monitor.AgenStarted() {
		return nil, errQemuAgentOffline
	}

	// The connection uses mutual authentication, so use the server's key & cert for client.
	agentCert, _, clientCert, clientKey, err := d.generateAgentCert()
	if err != nil {
		return nil, err
	}

	// Existing vsock ID from volatile.
	vsockID, err := d.getVsockID()
	if err != nil {
		return nil, err
	}

	agent, err := localvsock.HTTPClient(vsockID, ports.HTTPSDefaultPort, clientCert, clientKey, agentCert)
	if err != nil {
		return nil, err
	}

	return agent, nil
}

func (d *qemu) getMonitorEventHandler() func(event string, data map[string]any) {
	// Create local variables from instance properties we need so as not to keep references to instance around
	// after we have returned the callback function.
	instProject := d.Project()
	instanceName := d.Name()
	state := d.state

	return func(event string, data map[string]any) {
		if !slices.Contains([]string{qmp.EventVMShutdown, qmp.EventAgentStarted}, event) {
			return // Don't bother loading the instance from DB if we aren't going to handle the event.
		}

		var err error
		var d *qemu // Redefine d as local variable inside callback to avoid keeping references around.

		inst := instanceRefGet(instProject.Name, instanceName)
		if inst == nil {
			inst, err = instance.LoadByProjectAndName(state, instProject.Name, instanceName)
			if err != nil {
				l := logger.AddContext(logger.Ctx{"project": instProject.Name, "instance": instanceName})
				// If DB not available, try loading from backup file.
				l.Warn("Failed loading instance from database to handle monitor event, trying backup file", logger.Ctx{"err": err})

				instancePath := filepath.Join(internalUtil.VarPath("virtual-machines"), project.Instance(instProject.Name, instanceName))
				inst, err = instance.LoadFromBackup(state, instProject.Name, instancePath, false)
				if err != nil {
					l.Error("Failed loading instance to handle monitor event", logger.Ctx{"err": err})
					return
				}
			}
		}

		d = inst.(*qemu)

		if event == qmp.EventAgentStarted {
			d.logger.Debug("Instance agent started")
			err := d.advertiseVsockAddress()
			if err != nil {
				d.logger.Warn("Failed to advertise vsock address to instance agent", logger.Ctx{"err": err})
				return
			}
		} else if event == qmp.EventVMShutdown {
			target := "stop"
			entry, ok := data["reason"]
			if ok && entry == "guest-reset" {
				target = "reboot"
			}

			if entry == qmp.EventVMShutdownReasonDisconnect {
				d.logger.Warn("Instance stopped", logger.Ctx{"target": target, "reason": data["reason"]})
			} else {
				d.logger.Debug("Instance stopped", logger.Ctx{"target": target, "reason": data["reason"]})
			}

			err = d.onStop(target)
			if err != nil {
				d.logger.Error("Failed to cleanly stop instance", logger.Ctx{"err": err})
				return
			}
		}
	}
}

// mount the instance's config volume if needed.
func (d *qemu) mount() (*storagePools.MountInfo, error) {
	var pool storagePools.Pool
	pool, err := d.getStoragePool()
	if err != nil {
		return nil, err
	}

	if d.IsSnapshot() {
		mountInfo, err := pool.MountInstanceSnapshot(d, nil)
		if err != nil {
			return nil, err
		}

		return mountInfo, nil
	}

	mountInfo, err := pool.MountInstance(d, nil)
	if err != nil {
		return nil, err
	}

	return mountInfo, nil
}

// unmount the instance's config volume if needed.
func (d *qemu) unmount() error {
	pool, err := d.getStoragePool()
	if err != nil {
		return err
	}

	err = pool.UnmountInstance(d, nil)
	if err != nil {
		return err
	}

	return nil
}

// generateAgentCert creates the necessary server key and certificate if needed.
func (d *qemu) generateAgentCert() (string, string, string, string, error) {
	agentCertFile := filepath.Join(d.Path(), "agent.crt")
	agentKeyFile := filepath.Join(d.Path(), "agent.key")
	clientCertFile := filepath.Join(d.Path(), "agent-client.crt")
	clientKeyFile := filepath.Join(d.Path(), "agent-client.key")

	// Create server certificate.
	err := localtls.FindOrGenCert(agentCertFile, agentKeyFile, false, false)
	if err != nil {
		return "", "", "", "", err
	}

	// Create client certificate.
	err = localtls.FindOrGenCert(clientCertFile, clientKeyFile, true, false)
	if err != nil {
		return "", "", "", "", err
	}

	// Read all the files
	agentCert, err := os.ReadFile(agentCertFile)
	if err != nil {
		return "", "", "", "", err
	}

	agentKey, err := os.ReadFile(agentKeyFile)
	if err != nil {
		return "", "", "", "", err
	}

	clientCert, err := os.ReadFile(clientCertFile)
	if err != nil {
		return "", "", "", "", err
	}

	clientKey, err := os.ReadFile(clientKeyFile)
	if err != nil {
		return "", "", "", "", err
	}

	return string(agentCert), string(agentKey), string(clientCert), string(clientKey), nil
}

// Freeze freezes the instance.
func (d *qemu) Freeze() error {
	// Connect to the monitor.
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
	if err != nil {
		return err
	}

	// Send the stop command.
	err = monitor.Pause()
	if err != nil {
		return err
	}

	d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstancePaused.Event(d, nil))
	return nil
}

// configDriveMountPath returns the path for the config drive bind mount.
func (d *qemu) configDriveMountPath() string {
	return filepath.Join(d.DevicesPath(), "config.mount")
}

// configDriveMountPathClear attempts to unmount the config drive bind mount and remove the directory.
func (d *qemu) configDriveMountPathClear() error {
	return device.DiskMountClear(d.configDriveMountPath())
}

// pidWait waits for the QEMU process to exit. Does this in a way that doesn't require the process to be a
// parent of the QEMU process (in order to allow for the daemon to be restarted after the VM was started).
// Returns true if process stopped, false if timeout was exceeded.
func (d *qemu) pidWait(timeout time.Duration) bool {
	waitUntil := time.Now().Add(timeout)
	for {
		pid, _ := d.pid()
		if pid <= 0 {
			break
		}

		if time.Now().After(waitUntil) {
			return false
		}

		time.Sleep(time.Millisecond * time.Duration(250))
	}

	return true
}

// onStop is run when the instance stops.
func (d *qemu) onStop(target string) error {
	d.logger.Debug("onStop hook started", logger.Ctx{"target": target})
	defer d.logger.Debug("onStop hook finished", logger.Ctx{"target": target})

	// Create/pick up operation.
	op, err := d.onStopOperationSetup(target)
	if err != nil {
		return err
	}

	// Unlock on return
	defer op.Done(nil)

	// Wait for QEMU process to end (to avoiding racing start when restarting).
	// Wait up to 5 minutes to allow for flushing any pending data to disk.
	d.logger.Debug("Waiting for VM process to finish")
	waitTimeout := time.Minute * 5
	if d.pidWait(waitTimeout) {
		d.logger.Debug("VM process finished")
	} else {
		// Log a warning, but continue clean up as best we can.
		d.logger.Error("VM process failed to stop", logger.Ctx{"timeout": waitTimeout})
	}

	// Record power state.
	err = d.VolatileSet(map[string]string{
		"volatile.last_state.power": instance.PowerStateStopped,
		"volatile.last_state.ready": "false",
	})
	if err != nil {
		// Don't return an error here as we still want to cleanup the instance even if DB not available.
		d.logger.Error("Failed recording last power state", logger.Ctx{"err": err})
	}

	// Cleanup.
	d.cleanupDevices() // Must be called before unmount.
	_ = os.Remove(d.pidFilePath())
	_ = os.Remove(d.monitorPath())

	// Stop the storage for the instance.
	err = d.unmount()
	if err != nil && !errors.Is(err, storageDrivers.ErrInUse) {
		err = fmt.Errorf("Failed unmounting instance: %w", err)
		op.Done(err)
		return err
	}

	// Unload the apparmor profile
	err = apparmor.InstanceUnload(d.state.OS, d)
	if err != nil {
		op.Done(err)
		return err
	}

	// Log and emit lifecycle if not user triggered.
	if op.GetInstanceInitiated() {
		d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceShutdown.Event(d, nil))
	} else {
		d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceStopped.Event(d, nil))
	}

	// Reboot the instance.
	if target == "reboot" {
		err = d.Start(false)
		if err != nil {
			op.Done(err)
			return err
		}

		d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceRestarted.Event(d, nil))
	} else if d.ephemeral {
		// Destroy ephemeral virtual machines.
		err = d.delete(true)
		if err != nil {
			op.Done(err)
			return err
		}
	}

	return nil
}

// Shutdown shuts the instance down.
func (d *qemu) Shutdown(timeout time.Duration) error {
	d.logger.Debug("Shutdown started", logger.Ctx{"timeout": timeout})
	defer d.logger.Debug("Shutdown finished", logger.Ctx{"timeout": timeout})

	// Must be run prior to creating the operation lock.
	statusCode := d.statusCode()
	if !d.isRunningStatusCode(statusCode) {
		if statusCode == api.Error {
			return fmt.Errorf("The instance cannot be cleanly shutdown as in %s status", statusCode)
		}

		return ErrInstanceIsStopped
	}

	// Setup a new operation.
	// Allow inheriting of ongoing restart operation (we are called from restartCommon).
	// Allow reuse when creating a new stop operation. This allows the Stop() function to inherit operation.
	// Allow reuse of a reusable ongoing stop operation as Shutdown() may be called earlier, which allows reuse
	// of its operations. This allow for multiple Shutdown() attempts.
	op, err := operationlock.CreateWaitGet(d.Project().Name, d.Name(), operationlock.ActionStop, []operationlock.Action{operationlock.ActionRestart}, true, true)
	if err != nil {
		if errors.Is(err, operationlock.ErrNonReusuableSucceeded) {
			// An existing matching operation has now succeeded, return.
			return nil
		}

		return err
	}

	// If frozen, resume so the signal can be handled.
	if d.IsFrozen() {
		err := d.Unfreeze()
		if err != nil {
			return err
		}
	}

	// Connect to the monitor.
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
	if err != nil {
		op.Done(err)
		return err
	}

	// Indicate to the onStop hook that if the VM stops it was due to a clean shutdown because the VM responded
	// to the powerdown request.
	op.SetInstanceInitiated(true)

	// Send the system_powerdown command.
	err = monitor.Powerdown()
	if err != nil {
		if err == qmp.ErrMonitorDisconnect {
			op.Done(nil)
			return nil
		}

		op.Done(err)
		return err
	}

	d.logger.Debug("Shutdown request sent to instance")

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Wait for operation lock to be Done or context to timeout. The operation lock is normally completed by
	// onStop which picks up the same lock and then marks it as Done after the instance stops and the devices
	// have been cleaned up. However if the operation has failed for another reason we collect the error here.
	err = op.Wait(ctx)
	status := d.statusCode()
	if status != api.Stopped {
		errPrefix := fmt.Errorf("Failed shutting down instance, status is %q", status)

		if err != nil {
			return fmt.Errorf("%s: %w", errPrefix.Error(), err)
		}

		return errPrefix
	}

	// Now handle errors from shutdown sequence and return to caller if wasn't completed cleanly.
	if err != nil {
		return err
	}

	return nil
}

// Restart restart the instance.
func (d *qemu) Restart(timeout time.Duration) error {
	return d.restartCommon(d, timeout)
}

// Rebuild rebuilds the instance using the supplied image fingerprint as source.
func (d *qemu) Rebuild(img *api.Image, op *operations.Operation) error {
	return d.rebuildCommon(d, img, op)
}

func (d *qemu) ovmfPath() string {
	if os.Getenv("INCUS_OVMF_PATH") != "" {
		return os.Getenv("INCUS_OVMF_PATH")
	}

	return "/usr/share/OVMF"
}

// killQemuProcess kills specified process. Optimistically attempts to wait for the process to fully exit, but does
// not return an error if the Wait call fails. This is because this function is used in scenarios where the daemon has
// been restarted after the VM has been started and is no longer the parent of the QEMU process.
// The caller should use another method to ensure that the QEMU process has fully exited instead.
// Returns an error if the Kill signal couldn't be sent to the process (for any other reason apart from the process
// not existing).
func (d *qemu) killQemuProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		if err == os.ErrProcessDone {
			return nil
		}

		return err
	}

	err = proc.Kill()
	if err != nil {
		if err == os.ErrProcessDone {
			return nil
		}

		return err
	}

	// Wait for process to exit, but don't return an error if this fails as it may be called when the daemon isn't
	// the parent of the process, and we have still sent the kill signal as per the function's description.
	_, err = proc.Wait()
	if err != nil {
		if strings.Contains(err.Error(), "no child processes") {
			return nil
		}

		d.logger.Warn("Failed to collect VM process exit status", logger.Ctx{"pid": pid, "err": err})
	}

	return nil
}

// restoreState restores the VM state from a file handle.
func (d *qemu) restoreStateHandle(ctx context.Context, monitor *qmp.Monitor, f *os.File) error {
	err := monitor.SendFile("migration", f)
	if err != nil {
		return err
	}

	err = monitor.MigrateIncoming(ctx, "fd:migration")
	if err != nil {
		return err
	}

	return nil
}

// restoreState restores VM state from state file or from migration source if d.migrationReceiveStateful set.
func (d *qemu) restoreState(monitor *qmp.Monitor) error {
	if d.migrationReceiveStateful != nil {
		stateConn := d.migrationReceiveStateful[api.SecretNameState]
		if stateConn == nil {
			return fmt.Errorf("Migration state connection is not initialized")
		}

		// Perform non-shared storage transfer if requested.
		filesystemConn := d.migrationReceiveStateful[api.SecretNameFilesystem]
		if filesystemConn != nil {
			nbdConn, err := monitor.NBDServerStart()
			if err != nil {
				return fmt.Errorf("Failed starting NBD server: %w", err)
			}

			d.logger.Debug("Migration NBD server started")

			defer func() {
				_ = nbdConn.Close()
				_ = monitor.NBDServerStop()
			}()

			err = monitor.NBDBlockExportAdd(qemuMigrationNBDExportName)
			if err != nil {
				return fmt.Errorf("Failed adding root disk to NBD server: %w", err)
			}

			go func() {
				d.logger.Debug("Migration storage NBD export starting")

				go func() { _, _ = io.Copy(filesystemConn, nbdConn) }()

				_, _ = io.Copy(nbdConn, filesystemConn)
				_ = nbdConn.Close()

				d.logger.Debug("Migration storage NBD export finished")
			}()

			defer func() { _ = filesystemConn.Close() }()
		}

		// Receive checkpoint from QEMU process on source.
		d.logger.Debug("Stateful migration checkpoint receive starting")
		pipeRead, pipeWrite, err := os.Pipe()
		if err != nil {
			return err
		}

		go func() {
			_, err := io.Copy(pipeWrite, stateConn)
			if err != nil {
				d.logger.Warn("Failed reading from state connection", logger.Ctx{"err": err})
			}

			_ = pipeRead.Close()
			_ = pipeWrite.Close()
		}()

		err = d.restoreStateHandle(context.Background(), monitor, pipeRead)
		if err != nil {
			return fmt.Errorf("Failed restoring checkpoint from source: %w", err)
		}

		d.logger.Debug("Stateful migration checkpoint receive finished")
	} else {
		statePath := d.StatePath()
		d.logger.Debug("Stateful checkpoint restore starting", logger.Ctx{"source": statePath})
		defer d.logger.Debug("Stateful checkpoint restore finished", logger.Ctx{"source": statePath})

		stateFile, err := os.Open(statePath)
		if err != nil {
			return fmt.Errorf("Failed opening state file %q: %w", statePath, err)
		}

		defer func() { _ = stateFile.Close() }()

		uncompressedState, err := gzip.NewReader(stateFile)
		if err != nil {
			return fmt.Errorf("Failed opening state gzip reader: %w", err)
		}

		defer func() { _ = uncompressedState.Close() }()

		pipeRead, pipeWrite, err := os.Pipe()
		if err != nil {
			return err
		}

		go func() {
			_, err := io.Copy(pipeWrite, uncompressedState)
			if err != nil {
				d.logger.Warn("Failed reading from state file", logger.Ctx{"path": statePath, "err": err})
			}

			_ = pipeRead.Close()
			_ = pipeWrite.Close()
		}()

		err = d.restoreStateHandle(context.Background(), monitor, pipeRead)
		if err != nil {
			return fmt.Errorf("Failed restoring state from %q: %w", stateFile.Name(), err)
		}
	}

	return nil
}

// saveStateHandle dumps the current VM state to a file handle.
// Once started, the VM is in a paused state and it's up to the caller to wait for the transfer to complete and
// resume or kill the VM guest.
func (d *qemu) saveStateHandle(monitor *qmp.Monitor, f *os.File) error {
	// Send the target file to qemu.
	err := monitor.SendFile("migration", f)
	if err != nil {
		return err
	}

	// Issue the migration command.
	err = monitor.Migrate("fd:migration")
	if err != nil {
		return err
	}

	return nil
}

// saveState dumps the current VM state to the state file.
// Once dumped, the VM is in a paused state and it's up to the caller to resume or kill it.
func (d *qemu) saveState(monitor *qmp.Monitor) error {
	statePath := d.StatePath()
	d.logger.Debug("Stateful checkpoint starting", logger.Ctx{"target": statePath})
	defer d.logger.Debug("Stateful checkpoint finished", logger.Ctx{"target": statePath})

	// Save the checkpoint to state file.
	_ = os.Remove(statePath)

	// Prepare the state file.
	stateFile, err := os.Create(statePath)
	if err != nil {
		return err
	}

	defer func() { _ = stateFile.Close() }()

	compressedState, err := gzip.NewWriterLevel(stateFile, gzip.BestSpeed)
	if err != nil {
		return err
	}

	defer func() { _ = compressedState.Close() }()

	pipeRead, pipeWrite, err := os.Pipe()
	if err != nil {
		return err
	}

	defer func() {
		_ = pipeRead.Close()
		_ = pipeWrite.Close()
	}()

	go func() { _, _ = io.Copy(compressedState, pipeRead) }()

	err = d.saveStateHandle(monitor, pipeWrite)
	if err != nil {
		return fmt.Errorf("Failed initializing state save to %q: %w", stateFile.Name(), err)
	}

	err = monitor.MigrateWait("completed")
	if err != nil {
		return fmt.Errorf("Failed saving state to %q: %w", stateFile.Name(), err)
	}

	return nil
}

// validateStartup checks any constraints that would prevent start up from succeeding under normal circumstances.
func (d *qemu) validateStartup(stateful bool, statusCode api.StatusCode) error {
	err := d.common.validateStartup(stateful, statusCode)
	if err != nil {
		return err
	}

	// Cannot perform stateful start unless config is appropriately set.
	if stateful && util.IsFalseOrEmpty(d.expandedConfig["migration.stateful"]) {
		return fmt.Errorf("Stateful start requires migration.stateful to be set to true")
	}

	// gendoc:generate(entity=image, group=requirements, key=requirements.secureboot)
	//
	// ---
	//  type: bool
	//  shortdesc: If set to `false`, indicates that the image cannot boot under secure boot.
	//
	// Ensure secureboot is turned off for images that are not secureboot enabled.
	if util.IsFalse(d.localConfig["image.requirements.secureboot"]) && util.IsTrueOrEmpty(d.expandedConfig["security.secureboot"]) {
		return fmt.Errorf("The image used by this instance is incompatible with secureboot. Please set security.secureboot=false on the instance")
	}

	// Ensure secureboot is turned off when CSM is on.
	if util.IsTrue(d.expandedConfig["security.csm"]) && util.IsTrueOrEmpty(d.expandedConfig["security.secureboot"]) {
		return fmt.Errorf("Secure boot can't be enabled while CSM is turned on. Please set security.secureboot=false on the instance")
	}

	// gendoc:generate(entity=image, group=requirements, key=requirements.cdrom_agent)
	//
	// ---
	//  type: bool
	//  shortdesc: If set to `true`, indicates that the VM requires an `agent:config` disk be added.
	//
	// Ensure an agent drive is present if the image requires it.
	if util.IsTrue(d.localConfig["image.requirements.cdrom_agent"]) {
		found := false
		for _, dev := range d.expandedDevices {
			if dev["type"] == "disk" && dev["source"] == "agent:config" {
				found = true
				break
			}
		}

		if !found {
			return fmt.Errorf("This virtual machine image requires an agent:config disk be added")
		}
	}

	return nil
}

func (d *qemu) checkStateStorage() error {
	// For some operations, the "size.state" of the instance root disk device must be larger than the instance memory.
	// Otherwise, there will not be enough disk space to write the instance state to disk during any subsequent stops.
	// (Only check when migration.stateful is true, otherwise the memory won't be dumped when this instance stops).

	_, rootDiskDevice, err := d.getRootDiskDevice()
	if err != nil {
		return err
	}

	// Don't access d.storagePool directly since it isn't populated at this stage.
	pool, err := d.getStoragePool()
	if err != nil {
		return err
	}

	stateDiskSizeStr := pool.Driver().Info().DefaultVMBlockFilesystemSize
	if rootDiskDevice["size.state"] != "" {
		stateDiskSizeStr = rootDiskDevice["size.state"]
	}

	stateDiskSize, err := units.ParseByteSizeString(stateDiskSizeStr)
	if err != nil {
		return err
	}

	memoryLimitStr := QEMUDefaultMemSize
	if d.expandedConfig["limits.memory"] != "" {
		memoryLimitStr = d.expandedConfig["limits.memory"]
	}

	memoryLimit, err := units.ParseByteSizeString(memoryLimitStr)
	if err != nil {
		return err
	}

	if stateDiskSize < memoryLimit {
		return fmt.Errorf("Stateful stop and snapshots require that the instance limits.memory is less than size.state on the root disk device")
	}

	return nil
}

// Start starts the instance.
func (d *qemu) Start(stateful bool) error {
	unlock, err := d.updateBackupFileLock(context.Background())
	if err != nil {
		return err
	}

	defer unlock()

	return d.start(stateful, nil)
}

// start starts the instance and can use an existing InstanceOperation lock.
func (d *qemu) start(stateful bool, op *operationlock.InstanceOperation) error {
	d.logger.Debug("Start started", logger.Ctx{"stateful": stateful})
	defer d.logger.Debug("Start finished", logger.Ctx{"stateful": stateful})

	// Check that we are startable before creating an operation lock.
	// Must happen before creating operation Start lock to avoid the status check returning Stopped due to the
	// existence of a Start operation lock.
	err := d.validateStartup(stateful, d.statusCode())
	if err != nil {
		return err
	}

	// Setup a new operation if needed.
	if op == nil {
		op, err = operationlock.CreateWaitGet(d.Project().Name, d.Name(), operationlock.ActionStart, []operationlock.Action{operationlock.ActionRestart, operationlock.ActionRestore}, false, false)
		if err != nil {
			if errors.Is(err, operationlock.ErrNonReusuableSucceeded) {
				// An existing matching operation has now succeeded, return.
				return nil
			}

			return fmt.Errorf("Failed to create instance start operation: %w", err)
		}
	}

	defer op.Done(err)

	// Assign a NUMA node if needed.
	if d.expandedConfig["limits.cpu.nodes"] == "balanced" {
		err := d.setNUMANode()
		if err != nil {
			op.Done(err)
			return err
		}
	}

	// Ensure the correct vhost_vsock kernel module is loaded before establishing the vsock.
	err = linux.LoadModule("vhost_vsock")
	if err != nil {
		op.Done(err)
		return err
	}

	revert := revert.New()
	defer revert.Fail()

	// Rotate the log file.
	logfile := d.LogFilePath()
	if util.PathExists(logfile) {
		_ = os.Remove(logfile + ".old")
		err := os.Rename(logfile, logfile+".old")
		if err != nil && !os.IsNotExist(err) {
			op.Done(err)
			return err
		}
	}

	// Remove old pid file if needed.
	if util.PathExists(d.pidFilePath()) {
		err = os.Remove(d.pidFilePath())
		if err != nil {
			op.Done(err)
			return fmt.Errorf("Failed removing old PID file %q: %w", d.pidFilePath(), err)
		}
	}

	// Mount the instance's config volume.
	mountInfo, err := d.mount()
	if err != nil {
		op.Done(err)
		return err
	}

	revert.Add(func() { _ = d.unmount() })

	// Define a set of files to open and pass their file descriptors to QEMU command.
	fdFiles := make([]*os.File, 0)

	// Ensure passed files are closed after start has returned (either because QEMU has started or on error).
	defer func() {
		for _, file := range fdFiles {
			_ = file.Close()
		}
	}()

	// New or existing vsock ID from volatile.
	vsockID, vsockF, err := d.nextVsockID()
	if err != nil {
		return err
	}

	// Add allocated QEMU vhost file descriptor.
	vsockFD := d.addFileDescriptor(&fdFiles, vsockF)

	volatileSet := make(map[string]string)

	// Update vsock ID in volatile if needed for recovery (do this before UpdateBackupFile() call).
	oldVsockID := d.localConfig["volatile.vsock_id"]
	newVsockID := strconv.FormatUint(uint64(vsockID), 10)
	if oldVsockID != newVsockID {
		volatileSet["volatile.vsock_id"] = newVsockID
	}

	// Generate UUID if not present (do this before UpdateBackupFile() call).
	instUUID := d.localConfig["volatile.uuid"]
	if instUUID == "" {
		instUUID = uuid.New().String()
		volatileSet["volatile.uuid"] = instUUID
	}

	// For a VM instance, we must also set the VM generation ID.
	vmGenUUID := d.localConfig["volatile.uuid.generation"]
	if vmGenUUID == "" {
		vmGenUUID = instUUID
		volatileSet["volatile.uuid.generation"] = vmGenUUID
	}

	// Generate the config drive.
	err = d.generateConfigShare()
	if err != nil {
		op.Done(err)
		return err
	}

	// Create all needed paths.
	err = os.MkdirAll(d.LogPath(), 0700)
	if err != nil {
		op.Done(err)
		return err
	}

	err = os.MkdirAll(d.RunPath(), 0700)
	if err != nil {
		op.Done(err)
		return err
	}

	err = os.MkdirAll(d.DevicesPath(), 0711)
	if err != nil {
		op.Done(err)
		return err
	}

	err = os.MkdirAll(d.ShmountsPath(), 0711)
	if err != nil {
		op.Done(err)
		return err
	}

	// Copy OVMF settings firmware to nvram file if needed.
	// This firmware file can be modified by the VM so it must be copied from the defaults.
	if d.architectureSupportsUEFI(d.architecture) && (!util.PathExists(d.nvramPath()) || util.IsTrue(d.localConfig["volatile.apply_nvram"])) {
		err = d.setupNvram()
		if err != nil {
			op.Done(err)
			return err
		}
	}

	// Clear volatile.apply_nvram if set.
	if d.localConfig["volatile.apply_nvram"] != "" {
		volatileSet["volatile.apply_nvram"] = ""
	}

	// Apply any volatile changes that need to be made.
	err = d.VolatileSet(volatileSet)
	if err != nil {
		err = fmt.Errorf("Failed setting volatile keys: %w", err)
		op.Done(err)
		return err
	}

	devConfs := make([]*deviceConfig.RunConfig, 0, len(d.expandedDevices))
	postStartHooks := []func() error{}

	sortedDevices := d.expandedDevices.Sorted()
	startDevices := make([]device.Device, 0, len(sortedDevices))

	// Load devices in sorted order, this ensures that device mounts are added in path order.
	// Loading all devices first means that validation of all devices occurs before starting any of them.
	for _, entry := range sortedDevices {
		dev, err := d.deviceLoad(d, entry.Name, entry.Config)
		if err != nil {
			if errors.Is(err, device.ErrUnsupportedDevType) {
				continue // Skip unsupported device (allows for mixed instance type profiles).
			}

			err = fmt.Errorf("Failed start validation for device %q: %w", entry.Name, err)
			op.Done(err)
			return err
		}

		// Run pre-start of check all devices before starting any device to avoid expensive revert.
		err = dev.PreStartCheck()
		if err != nil {
			op.Done(err)
			return fmt.Errorf("Failed pre-start check for device %q: %w", dev.Name(), err)
		}

		startDevices = append(startDevices, dev)
	}

	// Start devices in order.
	for i := range startDevices {
		dev := startDevices[i] // Local var for revert.

		// Start the device.
		runConf, err := d.deviceStart(dev, false)
		if err != nil {
			err = fmt.Errorf("Failed to start device %q: %w", dev.Name(), err)
			op.Done(err)
			return err
		}

		revert.Add(func() {
			err := d.deviceStop(dev, false, "")
			if err != nil {
				d.logger.Error("Failed to cleanup device", logger.Ctx{"device": dev.Name(), "err": err})
			}
		})

		if runConf == nil {
			continue
		}

		if runConf.Revert != nil {
			revert.Add(runConf.Revert)
		}

		// Add post-start hooks
		if len(runConf.PostHooks) > 0 {
			postStartHooks = append(postStartHooks, runConf.PostHooks...)
		}

		devConfs = append(devConfs, runConf)
	}

	// Setup the config drive readonly bind mount. Important that this come after the root disk device start.
	// in order to allow unmounts triggered by deferred resizes of the root volume.
	configMntPath := d.configDriveMountPath()
	err = d.configDriveMountPathClear()
	if err != nil {
		err = fmt.Errorf("Failed cleaning config drive mount path %q: %w", configMntPath, err)
		op.Done(err)
		return err
	}

	err = os.Mkdir(configMntPath, 0700)
	if err != nil {
		err = fmt.Errorf("Failed creating device mount path %q for config drive: %w", configMntPath, err)
		op.Done(err)
		return err
	}

	revert.Add(func() { _ = d.configDriveMountPathClear() })

	// Mount the config drive device as readonly. This way it will be readonly irrespective of whether its
	// exported via 9p for virtio-fs.
	configSrcPath := filepath.Join(d.Path(), "config")
	err = device.DiskMount(configSrcPath, configMntPath, false, "", []string{"ro"}, "none")
	if err != nil {
		err = fmt.Errorf("Failed mounting device mount path %q for config drive: %w", configMntPath, err)
		op.Done(err)
		return err
	}

	// Get qemu configuration and check qemu is installed.
	qemuPath, qemuBus, err := d.qemuArchConfig(d.architecture)
	if err != nil {
		op.Done(err)
		return err
	}

	// Snapshot if needed.
	snapName, expiry, err := d.getStartupSnapNameAndExpiry(d)
	if err != nil {
		err = fmt.Errorf("Failed getting startup snapshot info: %w", err)
		op.Done(err)
		return err
	}

	if snapName != "" && expiry != nil {
		err := d.snapshot(snapName, *expiry, false)
		if err != nil {
			err = fmt.Errorf("Failed taking startup snapshot: %w", err)
			op.Done(err)
			return err
		}
	}

	// Get CPU information.
	cpuInfo, err := d.cpuTopology(d.expandedConfig["limits.cpu"])
	if err != nil {
		return err
	}

	// Determine additional CPU flags.
	cpuExtensions := []string{}

	if d.architecture == osarch.ARCH_64BIT_INTEL_X86 {
		// If using Linux 5.10 or later, use HyperV optimizations.
		minVer, _ := version.NewDottedVersion("5.10.0")
		if d.state.OS.KernelVersion.Compare(minVer) >= 0 && util.IsFalseOrEmpty(d.expandedConfig["migration.stateful"]) {
			// x86_64 can use hv_time to improve Windows guest performance.
			cpuExtensions = append(cpuExtensions, "hv_passthrough")
		}

		// x86_64 requires the use of topoext when SMT is used.
		if cpuInfo.threads > 1 {
			cpuExtensions = append(cpuExtensions, "topoext")
		}
	}

	cpuType := "host"
	if len(cpuExtensions) > 0 {
		cpuType += "," + strings.Join(cpuExtensions, ",")
	}

	// Generate the QEMU configuration.
	confFile, monHooks, err := d.generateQemuConfigFile(cpuInfo, mountInfo, qemuBus, vsockFD, devConfs, &fdFiles)
	if err != nil {
		op.Done(err)
		return err
	}

	// Start QEMU.
	qemuCmd := []string{
		"--",
		qemuPath,
		"-S",
		"-name", d.Name(),
		"-uuid", instUUID,
		"-daemonize",
		"-cpu", cpuType,
		"-nographic",
		"-serial", "chardev:console",
		"-nodefaults",
		"-no-user-config",
		"-sandbox", "on,obsolete=deny,elevateprivileges=allow,spawn=allow,resourcecontrol=deny",
		"-readconfig", confFile,
		"-spice", d.spiceCmdlineConfig(),
		"-pidfile", d.pidFilePath(),
		"-D", d.LogFilePath(),
	}

	// If stateful, restore now.
	if stateful {
		if d.stateful {
			qemuCmd = append(qemuCmd, "-incoming", "defer")
		} else {
			// No state to restore, just start as normal.
			stateful = false
		}
	} else if d.stateful {
		// Stateless start requested but state is present, delete it.
		err := os.Remove(d.StatePath())
		if err != nil && !os.IsNotExist(err) {
			op.Done(err)
			return err
		}

		d.stateful = false
		err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.UpdateInstanceStatefulFlag(ctx, d.id, false)
		})
		if err != nil {
			op.Done(err)
			return fmt.Errorf("Error updating instance stateful flag: %w", err)
		}
	}

	// SMBIOS only on x86_64 and aarch64.
	if d.architectureSupportsUEFI(d.architecture) {
		qemuCmd = append(qemuCmd, "-smbios", "type=2,manufacturer=LinuxContainers,product=Incus")
	}

	// Attempt to drop privileges (doesn't work when restoring state).
	if !stateful && d.state.OS.UnprivUser != "" {
		qemuCmd = append(qemuCmd, "-runas", d.state.OS.UnprivUser)

		nvRAMPath := d.nvramPath()
		if d.architectureSupportsUEFI(d.architecture) && util.PathExists(nvRAMPath) {
			// Ensure UEFI nvram file is writable by the QEMU process.
			// This is needed when doing stateful snapshots because the QEMU process will reopen the
			// file for writing.
			err = os.Chown(nvRAMPath, int(d.state.OS.UnprivUID), -1)
			if err != nil {
				op.Done(err)
				return err
			}

			err = os.Chmod(nvRAMPath, 0600)
			if err != nil {
				op.Done(err)
				return err
			}
		}

		// Change ownership of config directory files so they are accessible to the
		// unprivileged qemu process so that the 9p share can work.
		//
		// Security note: The 9P share will present the UID owner of these files on the host
		// to the VM. In order to ensure that non-root users in the VM cannot access these
		// files be sure to mount the 9P share in the VM with the "access=0" option to allow
		// only root user in VM to access the mounted share.
		err := filepath.Walk(filepath.Join(d.Path(), "config"),
			func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}

				err = os.Chown(path, int(d.state.OS.UnprivUID), -1)
				if err != nil {
					return err
				}

				return nil
			})
		if err != nil {
			op.Done(err)
			return err
		}
	}

	// Handle hugepages on architectures where we don't set NUMA nodes.
	if d.architecture != osarch.ARCH_64BIT_INTEL_X86 && util.IsTrue(d.expandedConfig["limits.memory.hugepages"]) {
		hugetlb, err := localUtil.HugepagesPath()
		if err != nil {
			op.Done(err)
			return err
		}

		qemuCmd = append(qemuCmd, "-mem-path", hugetlb, "-mem-prealloc")
	}

	if d.expandedConfig["raw.qemu"] != "" {
		fields, err := shellquote.Split(d.expandedConfig["raw.qemu"])
		if err != nil {
			op.Done(err)
			return err
		}

		qemuCmd = append(qemuCmd, fields...)
	}

	// Run the qemu command via forklimits so we can selectively increase ulimits.
	forkLimitsCmd := []string{
		"forklimits",
	}

	if !d.state.OS.RunningInUserNS {
		// Required for PCI passthrough.
		forkLimitsCmd = append(forkLimitsCmd, "limit=memlock:unlimited:unlimited")
	}

	for i := range fdFiles {
		// Pass through any file descriptors as 3+i (as first 3 file descriptors are taken as standard).
		forkLimitsCmd = append(forkLimitsCmd, fmt.Sprintf("fd=%d", 3+i))
	}

	// Setup background process.
	p, err := subprocess.NewProcess(d.state.OS.ExecPath, append(forkLimitsCmd, qemuCmd...), d.EarlyLogFilePath(), d.EarlyLogFilePath())
	if err != nil {
		op.Done(err)
		return err
	}

	// Load the AppArmor profile
	err = apparmor.InstanceLoad(d.state.OS, d, []string{qemuPath})
	if err != nil {
		op.Done(err)
		return err
	}

	p.SetApparmor(apparmor.InstanceProfileName(d))

	// Update the backup.yaml file just before starting the instance process, but after all devices have been
	// setup, so that the backup file contains the volatile keys used for this instance start, so that they can
	// be used for instance cleanup.
	err = d.UpdateBackupFile()
	if err != nil {
		err = fmt.Errorf("Failed updating backup file: %w", err)
		op.Done(err)
		return err
	}

	err = p.StartWithFiles(context.Background(), fdFiles)
	if err != nil {
		op.Done(err)
		return err
	}

	_, err = p.Wait(context.Background())
	if err != nil {
		stderr, _ := os.ReadFile(d.EarlyLogFilePath())
		err = fmt.Errorf("Failed to run: %s: %s: %w", strings.Join(p.Args, " "), string(stderr), err)
		op.Done(err)
		return err
	}

	pid, err := d.pid()
	if err != nil || pid <= 0 {
		d.logger.Error("Failed to get VM process ID", logger.Ctx{"err": err, "pid": pid})
		op.Done(err)
		return err
	}

	revert.Add(func() {
		_ = d.killQemuProcess(pid)
	})

	// Start QMP monitoring.
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
	if err != nil {
		op.Done(err)
		return err
	}

	// Don't allow the monitor to trigger a disconnection shutdown event until cleanly started so that the
	// onStop hook isn't triggered prematurely (as this function's reverter will clean up on failure to start).
	monitor.SetOnDisconnectEvent(false)

	// Apply CPU pinning.
	if cpuInfo.vcpus == nil {
		if d.architectureSupportsCPUHotplug() && cpuInfo.cores > 1 {
			// Hotplug the CPUs.
			err := d.setCPUs(cpuInfo.cores)
			if err != nil {
				err = fmt.Errorf("Failed to add CPUs: %w", err)
				op.Done(err)
				return err
			}
		}
	} else {
		// Get the list of PIDs from the VM.
		pids, err := monitor.GetCPUs()
		if err != nil {
			op.Done(err)
			return err
		}

		// Confirm nothing weird is going on.
		if len(cpuInfo.vcpus) != len(pids) {
			err = fmt.Errorf("QEMU has less vCPUs than configured")
			op.Done(err)
			return err
		}

		// Apply the CPU pins.
		for i, pid := range pids {
			set := unix.CPUSet{}
			set.Set(int(cpuInfo.vcpus[uint64(i)]))

			// Apply the pin.
			err := unix.SchedSetaffinity(pid, &set)
			if err != nil {
				op.Done(err)
				return err
			}
		}

		// Create a core scheduling group.
		err = d.setCoreSched(pids)
		if err != nil {
			err = fmt.Errorf("Failed to allocate new core scheduling domain for vCPU threads: %w", err)
			op.Done(err)
			return err
		}
	}

	// Run monitor hooks from devices.
	for _, monHook := range monHooks {
		err = monHook(monitor)
		if err != nil {
			op.Done(err)
			return fmt.Errorf("Failed setting up device via monitor: %w", err)
		}
	}

	// Due to a bug in QEMU, devices added using QMP's device_add command do not have their bootindex option
	// respected (even if added before emuation is started). To workaround this we must reset the VM in order
	// for it to rebuild its boot config and to take into account the devices bootindex settings.
	// This also means we cannot start the QEMU process with the -no-reboot flag, so we set the same reboot
	// action below after this call.
	err = monitor.Reset()
	if err != nil {
		op.Done(err)
		return fmt.Errorf("Failed resetting VM: %w", err)
	}

	// Set the equivalent of the -no-reboot flag (which we can't set because of the reset bug above) via QMP.
	// This ensures that if the guest initiates a reboot that the SHUTDOWN event is generated instead with the
	// reason set to "guest-reset" so that the event handler returned from getMonitorEventHandler() can restart
	// the guest instead.
	actions := map[string]string{
		"shutdown": "poweroff",
		"reboot":   "shutdown", // Don't reset on reboot. Let us handle reboots.
		"panic":    "pause",    // Pause on panics to allow investigation.
	}

	err = monitor.SetAction(actions)
	if err != nil {
		op.Done(err)
		return fmt.Errorf("Failed setting reboot action: %w", err)
	}

	// Restore the state.
	if stateful {
		err = d.restoreState(monitor)
		if err != nil {
			op.Done(err)
			return err
		}
	}

	// Start the VM.
	err = monitor.Start()
	if err != nil {
		err = fmt.Errorf("Failed starting VM: %w", err)
		op.Done(err)
		return err
	}

	// Finish handling stateful start.
	if stateful {
		// Cleanup state.
		_ = os.Remove(d.StatePath())
		d.stateful = false

		err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.UpdateInstanceStatefulFlag(ctx, d.id, false)
		})
		if err != nil {
			op.Done(err)
			return fmt.Errorf("Error updating instance stateful flag: %w", err)
		}
	}

	// Record last start state.
	err = d.recordLastState()
	if err != nil {
		op.Done(err)
		return err
	}

	revert.Success()

	// Run any post-start hooks.
	err = d.runHooks(postStartHooks)
	if err != nil {
		op.Done(err) // Must come before Stop() otherwise stop will not proceed.

		// Shut down the VM if hooks fail.
		_ = d.Stop(false)
		return err
	}

	if op.Action() == "start" {
		d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceStarted.Event(d, nil))
	}

	// The VM started cleanly so now enable the unexpected disconnection event to ensure the onStop hook is
	// run if QMP unexpectedly disconnects.
	monitor.SetOnDisconnectEvent(true)
	op.Done(nil)
	return nil
}

func (d *qemu) setupSEV(fdFiles *[]*os.File) (*qemuSevOpts, error) {
	if d.architecture != osarch.ARCH_64BIT_INTEL_X86 {
		return nil, errors.New("AMD SEV support is only available on x86_64 systems")
	}

	// Get the QEMU features to check if AMD SEV is supported.
	info := DriverStatuses()[instancetype.VM].Info
	_, smeFound := info.Features["sme"]
	sev, sevFound := info.Features["sev"]
	if !smeFound || !sevFound {
		return nil, errors.New("AMD SEV is not supported by the host")
	}

	// Get the SEV guest `cbitpos` and `reducedPhysBits`.
	sevCapabilities, ok := sev.(qmp.AMDSEVCapabilities)
	if !ok {
		return nil, errors.New(`Failed to get the guest "sev" capabilities`)
	}

	cbitpos := sevCapabilities.CBitPos
	reducedPhysBits := sevCapabilities.ReducedPhysBits

	// Write user's dh-cert and session-data to file descriptors.
	var dhCertFD, sessionDataFD int
	if d.expandedConfig["security.sev.session.dh"] != "" {
		dhCert, err := os.CreateTemp("", "incus_sev_dh_cert_")
		if err != nil {
			return nil, err
		}

		err = os.Remove(dhCert.Name())
		if err != nil {
			return nil, err
		}

		_, err = dhCert.WriteString(d.expandedConfig["security.sev.session.dh"])
		if err != nil {
			return nil, err
		}

		dhCertFD = d.addFileDescriptor(fdFiles, dhCert)
	}

	if d.expandedConfig["security.sev.session.data"] != "" {
		sessionData, err := os.CreateTemp("", "incus_sev_session_data_")
		if err != nil {
			return nil, err
		}

		err = os.Remove(sessionData.Name())
		if err != nil {
			return nil, err
		}

		_, err = sessionData.WriteString(d.expandedConfig["security.sev.session.data"])
		if err != nil {
			return nil, err
		}

		sessionDataFD = d.addFileDescriptor(fdFiles, sessionData)
	}

	sevOpts := &qemuSevOpts{}
	sevOpts.cbitpos = cbitpos
	sevOpts.reducedPhysBits = reducedPhysBits
	if dhCertFD > 0 && sessionDataFD > 0 {
		sevOpts.dhCertFD = fmt.Sprintf("/proc/self/fd/%d", dhCertFD)
		sevOpts.sessionDataFD = fmt.Sprintf("/proc/self/fd/%d", sessionDataFD)
	}

	if util.IsTrue(d.expandedConfig["security.sev.policy.es"]) {
		_, sevES := info.Features["sev-es"]
		if sevES {
			// This bit mask is used to specify a guest policy. '0x5' is for SEV-ES. The details of the available policies can be found in the link below (see chapter 3)
			// https://www.amd.com/system/files/TechDocs/55766_SEV-KM_API_Specification.pdf
			sevOpts.policy = "0x5"
		} else {
			return nil, errors.New("AMD SEV-ES is not supported by the host")
		}
	} else {
		// '0x1' is for a regular SEV policy.
		sevOpts.policy = "0x1"
	}

	return sevOpts, nil
}

// getAgentConnectionInfo returns the connection info the agent needs to connect to the server.
func (d *qemu) getAgentConnectionInfo() (*agentAPI.API10Put, error) {
	addr := d.state.Endpoints.VsockAddress()
	if addr == nil {
		return nil, nil
	}

	vsockaddr, ok := addr.(*vsock.Addr)
	if !ok {
		return nil, fmt.Errorf("Listen address is not vsock.Addr")
	}

	req := agentAPI.API10Put{
		Certificate: string(d.state.Endpoints.NetworkCert().PublicKey()),
		DevIncus:    util.IsTrueOrEmpty(d.expandedConfig["security.guestapi"]),
		CID:         vsock.Host, // Always tell the agent to connect to the server using the Host Context ID to support nesting.
		Port:        vsockaddr.Port,
	}

	return &req, nil
}

// advertiseVsockAddress advertises the CID and port to the VM.
func (d *qemu) advertiseVsockAddress() error {
	client, err := d.getAgentClient()
	if err != nil {
		return fmt.Errorf("Failed getting agent client handle: %w", err)
	}

	agentArgs := &incus.ConnectionArgs{SkipGetServer: true}
	agent, err := incus.ConnectIncusHTTP(agentArgs, client)
	if err != nil {
		return fmt.Errorf("Failed connecting to the agent: %w", err)
	}

	defer agent.Disconnect()

	connInfo, err := d.getAgentConnectionInfo()
	if err != nil {
		return err
	}

	if connInfo == nil {
		return nil
	}

	_, _, err = agent.RawQuery("PUT", "/1.0", connInfo, "")
	if err != nil {
		return fmt.Errorf("Failed sending host vsock information to the agent: %w", err)
	}

	return nil
}

// AgentCertificate returns the server certificate of the agent.
func (d *qemu) AgentCertificate() *x509.Certificate {
	agentCert := filepath.Join(d.Path(), "config", "agent.crt")
	if !util.PathExists(agentCert) {
		return nil
	}

	cert, err := localtls.ReadCert(agentCert)
	if err != nil {
		return nil
	}

	return cert
}

func (d *qemu) architectureSupportsUEFI(arch int) bool {
	return slices.Contains([]int{osarch.ARCH_64BIT_INTEL_X86, osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN}, arch)
}

func (d *qemu) setupNvram() error {
	var err error

	d.logger.Debug("Generating NVRAM")

	// Cleanup existing variables.
	for _, firmwares := range [][]ovmfFirmware{ovmfGenericFirmwares, ovmfSecurebootFirmwares, ovmfCSMFirmwares} {
		for _, firmware := range firmwares {
			err := os.Remove(filepath.Join(d.Path(), firmware.vars))
			if err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}

	// Determine expected firmware.
	firmwares := ovmfGenericFirmwares
	if util.IsTrue(d.expandedConfig["security.csm"]) {
		firmwares = ovmfCSMFirmwares
	} else if util.IsTrueOrEmpty(d.expandedConfig["security.secureboot"]) {
		firmwares = ovmfSecurebootFirmwares
	}

	// Find the template file.
	var ovmfVarsPath string
	var ovmfVarsName string
	for _, firmware := range firmwares {
		varsPath := filepath.Join(d.ovmfPath(), firmware.vars)
		varsPath, err = filepath.EvalSymlinks(varsPath)
		if err != nil {
			continue
		}

		if util.PathExists(varsPath) {
			ovmfVarsPath = varsPath
			ovmfVarsName = firmware.vars
			break
		}
	}

	if ovmfVarsPath == "" {
		return fmt.Errorf("Couldn't find one of the required UEFI firmware files: %+v", firmwares)
	}

	// Copy the template.
	err = internalUtil.FileCopy(ovmfVarsPath, filepath.Join(d.Path(), ovmfVarsName))
	if err != nil {
		return err
	}

	// Generate a symlink if needed.
	// This is so qemu.nvram can always be assumed to be the OVMF vars file.
	// The real file name is then used to determine what firmware must be selected.
	if !util.PathExists(d.nvramPath()) {
		err = os.Symlink(ovmfVarsName, d.nvramPath())
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *qemu) qemuArchConfig(arch int) (string, string, error) {
	if arch == osarch.ARCH_64BIT_INTEL_X86 {
		path, err := exec.LookPath("qemu-system-x86_64")
		if err != nil {
			return "", "", err
		}

		return path, "pcie", nil
	} else if arch == osarch.ARCH_64BIT_ARMV8_LITTLE_ENDIAN {
		path, err := exec.LookPath("qemu-system-aarch64")
		if err != nil {
			return "", "", err
		}

		return path, "pcie", nil
	} else if arch == osarch.ARCH_64BIT_POWERPC_LITTLE_ENDIAN {
		path, err := exec.LookPath("qemu-system-ppc64")
		if err != nil {
			return "", "", err
		}

		return path, "pci", nil
	} else if arch == osarch.ARCH_64BIT_S390_BIG_ENDIAN {
		path, err := exec.LookPath("qemu-system-s390x")
		if err != nil {
			return "", "", err
		}

		return path, "ccw", nil
	}

	return "", "", fmt.Errorf("Architecture isn't supported for virtual machines")
}

// RegisterDevices calls the Register() function on all of the instance's devices.
func (d *qemu) RegisterDevices() {
	d.devicesRegister(d)
}

func (d *qemu) saveConnectionInfo(connInfo *agentAPI.API10Put) error {
	configDrivePath := filepath.Join(d.Path(), "config")

	f, err := os.Create(filepath.Join(configDrivePath, "agent.conf"))
	if err != nil {
		return err
	}

	defer func() {
		_ = f.Close()
	}()

	err = json.NewEncoder(f).Encode(connInfo)
	if err != nil {
		return err
	}

	return nil
}

// OnHook is the top-level hook handler.
func (d *qemu) OnHook(hookName string, args map[string]string) error {
	return instance.ErrNotImplemented
}

// deviceStart loads a new device and calls its Start() function.
func (d *qemu) deviceStart(dev device.Device, instanceRunning bool) (*deviceConfig.RunConfig, error) {
	configCopy := dev.Config()
	l := d.logger.AddContext(logger.Ctx{"device": dev.Name(), "type": configCopy["type"]})
	l.Debug("Starting device")

	revert := revert.New()
	defer revert.Fail()

	if instanceRunning && !dev.CanHotPlug() {
		return nil, fmt.Errorf("Device cannot be started when instance is running")
	}

	runConf, err := dev.Start()
	if err != nil {
		return nil, err
	}

	revert.Add(func() {
		runConf, _ := dev.Stop()
		if runConf != nil {
			_ = d.runHooks(runConf.PostHooks)
		}
	})

	// If runConf supplied, perform any instance specific setup of device.
	if runConf != nil {
		// If instance is running and then live attach device.
		if instanceRunning {
			// Attach network interface if requested.
			if len(runConf.NetworkInterface) > 0 {
				err = d.deviceAttachNIC(dev.Name(), configCopy, runConf.NetworkInterface)
				if err != nil {
					return nil, err
				}
			}

			for _, mount := range runConf.Mounts {
				if mount.FSType == "9p" {
					err = d.deviceAttachPath(dev.Name(), configCopy, mount)
					if err != nil {
						return nil, err
					}
				} else {
					err = d.deviceAttachBlockDevice(dev.Name(), configCopy, mount)
					if err != nil {
						return nil, err
					}
				}
			}

			for _, usbDev := range runConf.USBDevice {
				err = d.deviceAttachUSB(usbDev)
				if err != nil {
					return nil, err
				}
			}

			// If running, run post start hooks now (if not, they will be run
			// once the instance is started).
			err = d.runHooks(runConf.PostHooks)
			if err != nil {
				return nil, err
			}
		}
	}

	revert.Success()
	return runConf, nil
}

func (d *qemu) deviceAttachPath(deviceName string, configCopy map[string]string, mount deviceConfig.MountEntryItem) error {
	escapedDeviceName := linux.PathNameEncode(deviceName)
	deviceID := fmt.Sprintf("%s%s", qemuDeviceIDPrefix, escapedDeviceName)
	mountTag := fmt.Sprintf("incus_%s", deviceName)

	// Detect virtiofsd path.
	virtiofsdSockPath := filepath.Join(d.DevicesPath(), fmt.Sprintf("virtio-fs.%s.sock", deviceName))
	if !util.PathExists(virtiofsdSockPath) {
		return fmt.Errorf("Virtiofsd isn't running")
	}

	reverter := revert.New()
	defer reverter.Fail()

	// Check if the agent is running.
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
	if err != nil {
		return fmt.Errorf("Failed to connect to QMP monitor: %w", err)
	}

	addr, err := net.ResolveUnixAddr("unix", virtiofsdSockPath)
	if err != nil {
		return err
	}

	virtiofsSock, err := net.DialUnix("unix", nil, addr)
	if err != nil {
		return fmt.Errorf("Error connecting to virtiofs socket %q: %w", virtiofsdSockPath, err)
	}

	defer func() { _ = virtiofsSock.Close() }() // Close file after device has been added.

	virtiofsFile, err := virtiofsSock.File()
	if err != nil {
		return fmt.Errorf("Error opening virtiofs socket %q: %w", virtiofsdSockPath, err)
	}

	err = monitor.SendFile(virtiofsdSockPath, virtiofsFile)
	if err != nil {
		return fmt.Errorf("Failed to send virtiofs file descriptor: %w", err)
	}

	reverter.Add(func() { _ = monitor.CloseFile(virtiofsdSockPath) })

	err = monitor.AddCharDevice(map[string]any{
		"id": mountTag,
		"backend": map[string]any{
			"type": "socket",
			"data": map[string]any{
				"addr": map[string]any{
					"type": "fd",
					"data": map[string]any{
						"str": virtiofsdSockPath,
					},
				},
				"server": false,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("Failed to add the character device: %w", err)
	}

	reverter.Add(func() { _ = monitor.RemoveCharDevice(mountTag) })

	// Figure out a hotplug slot.
	pciDevID := qemuPCIDeviceIDStart

	// Iterate through all the instance devices in the same sorted order as is used when allocating the
	// boot time devices in order to find the PCI bus slot device we would have used at boot time.
	// Then attempt to use that same device, assuming it is available.
	for _, dev := range d.expandedDevices.Sorted() {
		if dev.Name == deviceName {
			break // Found our device.
		}

		pciDevID++
	}

	pciDeviceName := fmt.Sprintf("%s%d", busDevicePortPrefix, pciDevID)
	d.logger.Debug("Using PCI bus device to hotplug virtiofs into", logger.Ctx{"device": deviceName, "port": pciDeviceName})

	qemuDev := map[string]string{
		"driver":  "vhost-user-fs-pci",
		"bus":     pciDeviceName,
		"addr":    "00.0",
		"tag":     mountTag,
		"chardev": mountTag,
		"id":      deviceID,
	}

	err = monitor.AddDevice(qemuDev)
	if err != nil {
		return fmt.Errorf("Failed to add the virtiofs device: %w", err)
	}

	reverter.Success()
	return nil
}

func (d *qemu) deviceAttachBlockDevice(deviceName string, configCopy map[string]string, mount deviceConfig.MountEntryItem) error {
	// Check if the agent is running.
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
	if err != nil {
		return fmt.Errorf("Failed to connect to QMP monitor: %w", err)
	}

	monHook, err := d.addDriveConfig(nil, nil, mount)
	if err != nil {
		return fmt.Errorf("Failed to add drive config: %w", err)
	}

	err = monHook(monitor)
	if err != nil {
		return fmt.Errorf("Failed to call monitor hook for block device: %w", err)
	}

	return nil
}

func (d *qemu) deviceDetachPath(deviceName string, rawConfig deviceConfig.Device) error {
	escapedDeviceName := linux.PathNameEncode(deviceName)
	deviceID := fmt.Sprintf("%s%s", qemuDeviceIDPrefix, escapedDeviceName)
	mountTag := fmt.Sprintf("incus_%s", deviceName)

	// Check if the agent is running.
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
	if err != nil {
		return err
	}

	err = monitor.RemoveDevice(deviceID)
	if err != nil {
		return err
	}

	waitDuration := time.Duration(time.Second * time.Duration(10))
	waitUntil := time.Now().Add(waitDuration)
	for {
		err = monitor.RemoveCharDevice(mountTag)
		if err == nil {
			break
		}

		if api.StatusErrorCheck(err, http.StatusLocked) {
			time.Sleep(time.Second * time.Duration(2))
			continue
		}

		if time.Now().After(waitUntil) {
			return fmt.Errorf("Failed to detach path device after %v: %w", waitDuration, err)
		}
	}

	return nil
}

func (d *qemu) deviceDetachBlockDevice(deviceName string, rawConfig deviceConfig.Device) error {
	// Check if the agent is running.
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
	if err != nil {
		return err
	}

	escapedDeviceName := linux.PathNameEncode(deviceName)
	deviceID := fmt.Sprintf("%s%s", qemuDeviceIDPrefix, escapedDeviceName)
	blockDevName := d.blockNodeName(escapedDeviceName)

	err = monitor.RemoveFDFromFDSet(blockDevName)
	if err != nil {
		return err
	}

	err = monitor.RemoveDevice(deviceID)
	if err != nil {
		return err
	}

	waitDuration := time.Duration(time.Second * time.Duration(10))
	waitUntil := time.Now().Add(waitDuration)
	for {
		err = monitor.RemoveBlockDevice(blockDevName)
		if err == nil {
			break
		}

		if api.StatusErrorCheck(err, http.StatusLocked) {
			time.Sleep(time.Second * time.Duration(2))
			continue
		}

		if time.Now().After(waitUntil) {
			return fmt.Errorf("Failed to detach block device after %v: %w", waitDuration, err)
		}
	}

	return nil
}

// deviceAttachNIC live attaches a NIC device to the instance.
func (d *qemu) deviceAttachNIC(deviceName string, configCopy map[string]string, netIF []deviceConfig.RunConfigItem) error {
	devName := ""
	for _, dev := range netIF {
		if dev.Key == "link" {
			devName = dev.Value
			break
		}
	}

	if devName == "" {
		return fmt.Errorf("Device didn't provide a link property to use")
	}

	_, qemuBus, err := d.qemuArchConfig(d.architecture)
	if err != nil {
		return err
	}

	// Check if the agent is running.
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
	if err != nil {
		return err
	}

	qemuDev := make(map[string]string)

	// PCIe and PCI require a port device name to hotplug the NIC into.
	if slices.Contains([]string{"pcie", "pci"}, qemuBus) {
		pciDevID := qemuPCIDeviceIDStart

		// Iterate through all the instance devices in the same sorted order as is used when allocating the
		// boot time devices in order to find the PCI bus slot device we would have used at boot time.
		// Then attempt to use that same device, assuming it is available.
		for _, dev := range d.expandedDevices.Sorted() {
			if dev.Name == deviceName {
				break // Found our device.
			}

			pciDevID++
		}

		pciDeviceName := fmt.Sprintf("%s%d", busDevicePortPrefix, pciDevID)
		d.logger.Debug("Using PCI bus device to hotplug NIC into", logger.Ctx{"device": deviceName, "port": pciDeviceName})
		qemuDev["bus"] = pciDeviceName
		qemuDev["addr"] = "00.0"
	}

	monHook, err := d.addNetDevConfig(qemuBus, qemuDev, nil, netIF)
	if err != nil {
		return err
	}

	err = monHook(monitor)
	if err != nil {
		return err
	}

	return nil
}

// deviceStop loads a new device and calls its Stop() function.
func (d *qemu) deviceStop(dev device.Device, instanceRunning bool, _ string) error {
	configCopy := dev.Config()
	l := d.logger.AddContext(logger.Ctx{"device": dev.Name(), "type": configCopy["type"]})
	l.Debug("Stopping device")

	if instanceRunning && !dev.CanHotPlug() {
		return fmt.Errorf("Device cannot be stopped when instance is running")
	}

	runConf, err := dev.Stop()
	if err != nil {
		return err
	}

	if instanceRunning {
		// Detach NIC from running instance.
		if configCopy["type"] == "nic" {
			for _, usbDev := range runConf.USBDevice {
				err = d.deviceDetachUSB(usbDev)
				if err != nil {
					return err
				}
			}

			err = d.deviceDetachNIC(dev.Name())
			if err != nil {
				return err
			}
		}

		// Detach USB from running instance.
		if configCopy["type"] == "usb" && runConf != nil {
			for _, usbDev := range runConf.USBDevice {
				err = d.deviceDetachUSB(usbDev)
				if err != nil {
					return err
				}
			}
		}

		// Detach disk from running instance.
		if configCopy["type"] == "disk" {
			if configCopy["path"] != "" {
				err = d.deviceDetachPath(dev.Name(), configCopy)
				if err != nil {
					return err
				}
			} else {
				err = d.deviceDetachBlockDevice(dev.Name(), configCopy)
				if err != nil {
					return err
				}
			}
		}
	}

	if runConf != nil {
		// Run post stop hooks irrespective of run state of instance.
		err = d.runHooks(runConf.PostHooks)
		if err != nil {
			return err
		}
	}

	return nil
}

// deviceDetachNIC detaches a NIC device from a running instance.
func (d *qemu) deviceDetachNIC(deviceName string) error {
	// Check if the agent is running.
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
	if err != nil {
		return err
	}

	// pciDeviceExists checks if the deviceID exists as a bridged PCI device.
	pciDeviceExists := func(deviceID string) (bool, error) {
		pciDevs, err := monitor.QueryPCI()
		if err != nil {
			return false, err
		}

		for _, pciDev := range pciDevs {
			for _, bridgeDev := range pciDev.Bridge.Devices {
				if bridgeDev.DevID == deviceID {
					return true, nil
				}
			}
		}

		return false, nil
	}

	escapedDeviceName := linux.PathNameEncode(deviceName)
	deviceID := fmt.Sprintf("%s%s", qemuDeviceIDPrefix, escapedDeviceName)
	netDevID := fmt.Sprintf("%s%s", qemuNetDevIDPrefix, escapedDeviceName)

	// Request removal of device.
	err = monitor.RemoveDevice(deviceID)
	if err != nil {
		return fmt.Errorf("Failed removing NIC device: %w", err)
	}

	err = monitor.RemoveNIC(netDevID)
	if err != nil {
		return err
	}

	_, qemuBus, err := d.qemuArchConfig(d.architecture)
	if err != nil {
		return err
	}

	if slices.Contains([]string{"pcie", "pci"}, qemuBus) {
		// Wait until the device is actually removed (or we timeout waiting).
		waitDuration := time.Duration(time.Second * time.Duration(10))
		waitUntil := time.Now().Add(waitDuration)
		for {
			devExists, err := pciDeviceExists(deviceID)
			if err != nil {
				return fmt.Errorf("Failed getting PCI devices to check for NIC detach: %w", err)
			}

			if !devExists {
				break
			}

			if time.Now().After(waitUntil) {
				return fmt.Errorf("Failed to detach NIC after %v: %w", waitDuration, err)
			}

			d.logger.Debug("Waiting for NIC device to be detached", logger.Ctx{"device": deviceName})
			time.Sleep(time.Second * time.Duration(2))
		}
	}

	return nil
}

func (d *qemu) monitorPath() string {
	return filepath.Join(d.RunPath(), "qemu.monitor")
}

func (d *qemu) nvramPath() string {
	return filepath.Join(d.Path(), "qemu.nvram")
}

func (d *qemu) consolePath() string {
	return filepath.Join(d.RunPath(), "qemu.console")
}

func (d *qemu) spicePath() string {
	return filepath.Join(d.RunPath(), "qemu.spice")
}

func (d *qemu) spiceCmdlineConfig() string {
	return fmt.Sprintf("unix=on,disable-ticketing=on,addr=%s", d.spicePath())
}

// generateConfigShare generates the config share directory that will be exported to the VM via
// a 9P share. Due to the unknown size of templates inside the images this directory is created
// inside the VM's config volume so that it can be restricted by quota.
// Requires the instance be mounted before calling this function.
func (d *qemu) generateConfigShare() error {
	configDrivePath := filepath.Join(d.Path(), "config")

	// Create config drive dir if doesn't exist, if it does exist, leave it around so we don't regenerate all
	// files causing unnecessary config drive snapshot usage.
	err := os.MkdirAll(configDrivePath, 0500)
	if err != nil {
		return err
	}

	// Add the VM agent.
	agentSrcPath, _ := exec.LookPath("incus-agent")
	if util.PathExists(os.Getenv("INCUS_AGENT_PATH")) {
		// Install incus-agent script (loads from agent share).
		agentFile, err := incusAgentLoader.ReadFile("agent-loader/incus-agent")
		if err != nil {
			return err
		}

		err = os.WriteFile(filepath.Join(configDrivePath, "incus-agent"), agentFile, 0700)
		if err != nil {
			return err
		}

		// Legacy support.
		_ = os.Remove(filepath.Join(configDrivePath, "lxd-agent"))
		err = os.Symlink("incus-agent", filepath.Join(configDrivePath, "lxd-agent"))
		if err != nil {
			return err
		}
	} else if agentSrcPath != "" {
		// Install agent into config drive dir if found.
		agentSrcPath, err = filepath.EvalSymlinks(agentSrcPath)
		if err != nil {
			return err
		}

		agentSrcInfo, err := os.Stat(agentSrcPath)
		if err != nil {
			return fmt.Errorf("Failed getting info for incus-agent source %q: %w", agentSrcPath, err)
		}

		agentInstallPath := filepath.Join(configDrivePath, "incus-agent")
		agentNeedsInstall := true

		if util.PathExists(agentInstallPath) {
			agentInstallInfo, err := os.Stat(agentInstallPath)
			if err != nil {
				return fmt.Errorf("Failed getting info for existing incus-agent install %q: %w", agentInstallPath, err)
			}

			if agentInstallInfo.ModTime() == agentSrcInfo.ModTime() && agentInstallInfo.Size() == agentSrcInfo.Size() {
				agentNeedsInstall = false
			}
		}

		// Only install the agent into config drive if the existing one is different to the source one.
		// Otherwise we would end up copying it again and this can cause unnecessary snapshot usage.
		if agentNeedsInstall {
			d.logger.Debug("Installing incus-agent", logger.Ctx{"srcPath": agentSrcPath, "installPath": agentInstallPath})
			err = internalUtil.FileCopy(agentSrcPath, agentInstallPath)
			if err != nil {
				return err
			}

			err = os.Chmod(agentInstallPath, 0500)
			if err != nil {
				return err
			}

			err = os.Chown(agentInstallPath, 0, 0)
			if err != nil {
				return err
			}

			// Ensure we copy the source file's timestamps so they can be used for comparison later.
			err = os.Chtimes(agentInstallPath, agentSrcInfo.ModTime(), agentSrcInfo.ModTime())
			if err != nil {
				return fmt.Errorf("Failed setting incus-agent timestamps: %w", err)
			}
		} else {
			d.logger.Debug("Skipping incus-agent install as unchanged", logger.Ctx{"srcPath": agentSrcPath, "installPath": agentInstallPath})
		}

		// Legacy support.
		_ = os.Remove(filepath.Join(configDrivePath, "lxd-agent"))
		err = os.Symlink("incus-agent", filepath.Join(configDrivePath, "lxd-agent"))
		if err != nil {
			return err
		}
	} else {
		d.logger.Warn("incus-agent not found, skipping its inclusion in the VM config drive", logger.Ctx{"err": err})
	}

	agentCert, agentKey, clientCert, _, err := d.generateAgentCert()
	if err != nil {
		return err
	}

	err = os.WriteFile(filepath.Join(configDrivePath, "server.crt"), []byte(clientCert), 0400)
	if err != nil {
		return err
	}

	err = os.WriteFile(filepath.Join(configDrivePath, "agent.crt"), []byte(agentCert), 0400)
	if err != nil {
		return err
	}

	err = os.WriteFile(filepath.Join(configDrivePath, "agent.key"), []byte(agentKey), 0400)
	if err != nil {
		return err
	}

	// Systemd units.
	err = os.MkdirAll(filepath.Join(configDrivePath, "systemd"), 0500)
	if err != nil {
		return err
	}

	// Systemd unit for incus-agent. It ensures the incus-agent is copied from the shared filesystem before it is
	// started. The service is triggered dynamically via udev rules when certain virtio-ports are detected,
	// rather than being enabled at boot.
	agentFile, err := incusAgentLoader.ReadFile("agent-loader/systemd/incus-agent.service")
	if err != nil {
		return err
	}

	err = os.WriteFile(filepath.Join(configDrivePath, "systemd", "incus-agent.service"), agentFile, 0400)
	if err != nil {
		return err
	}

	// Setup script for incus-agent that is executed by the incus-agent systemd unit before incus-agent is started.
	// The script sets up a temporary mount point, copies data from the mount (including incus-agent binary),
	// and then unmounts it. It also ensures appropriate permissions for the Incus agent's runtime directory.
	agentFile, err = incusAgentLoader.ReadFile("agent-loader/incus-agent-setup")
	if err != nil {
		return err
	}

	err = os.WriteFile(filepath.Join(configDrivePath, "systemd", "incus-agent-setup"), agentFile, 0500)
	if err != nil {
		return err
	}

	err = os.MkdirAll(filepath.Join(configDrivePath, "udev"), 0500)
	if err != nil {
		return err
	}

	// Udev rules to start the incus-agent.service when QEMU serial devices (symlinks in virtio-ports) appear.
	agentFile, err = incusAgentLoader.ReadFile("agent-loader/systemd/incus-agent.rules")
	if err != nil {
		return err
	}

	err = os.WriteFile(filepath.Join(configDrivePath, "udev", "99-incus-agent.rules"), agentFile, 0400)
	if err != nil {
		return err
	}

	// Install script for manual installs.
	agentFile, err = incusAgentLoader.ReadFile("agent-loader/install.sh")
	if err != nil {
		return err
	}

	err = os.WriteFile(filepath.Join(configDrivePath, "install.sh"), agentFile, 0700)
	if err != nil {
		return err
	}

	// Templated files.
	templateFilesPath := filepath.Join(configDrivePath, "files")

	// Clear path and recreate.
	_ = os.RemoveAll(templateFilesPath)
	err = os.MkdirAll(templateFilesPath, 0500)
	if err != nil {
		return err
	}

	// Template anything that needs templating.
	key := "volatile.apply_template"
	if d.localConfig[key] != "" {
		// Run any template that needs running.
		err = d.templateApplyNow(instance.TemplateTrigger(d.localConfig[key]), templateFilesPath)
		if err != nil {
			return err
		}

		err := d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Remove the volatile key from the DB.
			return tx.DeleteInstanceConfigKey(ctx, int64(d.id), key)
		})
		if err != nil {
			return err
		}
	}

	err = d.templateApplyNow("start", templateFilesPath)
	if err != nil {
		return err
	}

	// Copy the template metadata itself too.
	metaPath := filepath.Join(d.Path(), "metadata.yaml")
	if util.PathExists(metaPath) {
		err = internalUtil.FileCopy(metaPath, filepath.Join(templateFilesPath, "metadata.yaml"))
		if err != nil {
			return err
		}
	}

	// Clear NICConfigDir to ensure that no leftover configuration is erroneously applied by the agent.
	nicConfigPath := filepath.Join(configDrivePath, deviceConfig.NICConfigDir)
	_ = os.RemoveAll(nicConfigPath)
	err = os.MkdirAll(nicConfigPath, 0500)
	if err != nil {
		return err
	}

	// Add the NIC config.
	if util.IsTrue(d.expandedConfig["agent.nic_config"]) {
		sortedDevices := d.expandedDevices.Sorted()
		for _, entry := range sortedDevices {
			if entry.Config["type"] != "nic" {
				continue // Only keep NIC devices.
			}

			dev, err := d.FillNetworkDevice(entry.Name, entry.Config)
			if err != nil {
				return err
			}

			err = d.writeNICDevConfig(dev["mtu"], entry.Name, dev["name"], dev["hwaddr"])
			if err != nil {
				return fmt.Errorf("Failed writing NIC config for device %q: %w", entry.Name, err)
			}
		}
	}

	// Writing the connection info the config drive allows the agent to start /dev/incus very
	// early. This is important for systemd services which want or require /dev/incus/sock.
	connInfo, err := d.getAgentConnectionInfo()
	if err != nil {
		return err
	}

	if connInfo != nil {
		err = d.saveConnectionInfo(connInfo)
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *qemu) templateApplyNow(trigger instance.TemplateTrigger, path string) error {
	// If there's no metadata, just return.
	fname := filepath.Join(d.Path(), "metadata.yaml")
	if !util.PathExists(fname) {
		return nil
	}

	// Parse the metadata.
	content, err := os.ReadFile(fname)
	if err != nil {
		return fmt.Errorf("Failed to read metadata: %w", err)
	}

	metadata := new(api.ImageMetadata)
	err = yaml.Unmarshal(content, &metadata)
	if err != nil {
		return fmt.Errorf("Could not parse %s: %w", fname, err)
	}

	// Figure out the instance architecture.
	arch, err := osarch.ArchitectureName(d.architecture)
	if err != nil {
		arch, err = osarch.ArchitectureName(d.state.OS.Architectures[0])
		if err != nil {
			return fmt.Errorf("Failed to detect system architecture: %w", err)
		}
	}

	// Generate the instance metadata.
	instanceMeta := make(map[string]string)
	instanceMeta["name"] = d.name
	instanceMeta["type"] = "virtual-machine"
	instanceMeta["architecture"] = arch

	if d.ephemeral {
		instanceMeta["ephemeral"] = "true"
	} else {
		instanceMeta["ephemeral"] = "false"
	}

	// Go through the templates.
	for tplPath, tpl := range metadata.Templates {
		err = func(tplPath string, tpl *api.ImageMetadataTemplate) error {
			var w *os.File

			// Check if the template should be applied now.
			found := false
			for _, tplTrigger := range tpl.When {
				if tplTrigger == string(trigger) {
					found = true
					break
				}
			}

			if !found {
				return nil
			}

			// Create the file itself.
			w, err = os.Create(filepath.Join(path, fmt.Sprintf("%s.out", tpl.Template)))
			if err != nil {
				return err
			}

			// Fix ownership and mode.
			err = w.Chmod(0644)
			if err != nil {
				return err
			}

			defer func() { _ = w.Close() }()

			// Read the template.
			tplString, err := os.ReadFile(filepath.Join(d.TemplatesPath(), tpl.Template))
			if err != nil {
				return fmt.Errorf("Failed to read template file: %w", err)
			}

			// Restrict filesystem access to within the instance's rootfs.
			tplSet := pongo2.NewSet(fmt.Sprintf("%s-%s", d.name, tpl.Template), pongoTemplate.ChrootLoader{Path: d.TemplatesPath()})
			tplRender, err := tplSet.FromString("{% autoescape off %}" + string(tplString) + "{% endautoescape %}")
			if err != nil {
				return fmt.Errorf("Failed to render template: %w", err)
			}

			configGet := func(confKey, confDefault *pongo2.Value) *pongo2.Value {
				val, ok := d.expandedConfig[confKey.String()]
				if !ok {
					return confDefault
				}

				return pongo2.AsValue(strings.TrimRight(val, "\r\n"))
			}

			// Render the template.
			err = tplRender.ExecuteWriter(pongo2.Context{"trigger": trigger,
				"path":       tplPath,
				"instance":   instanceMeta,
				"container":  instanceMeta, // FIXME: remove once most images have moved away.
				"config":     d.expandedConfig,
				"devices":    d.expandedDevices,
				"properties": tpl.Properties,
				"config_get": configGet}, w)
			if err != nil {
				return err
			}

			return w.Close()
		}(tplPath, tpl)
		if err != nil {
			return err
		}
	}

	return nil
}

// deviceBootPriorities returns a map keyed on device name containing the boot index to use.
// Qemu tries to boot devices in order of boot index (lowest first).
func (d *qemu) deviceBootPriorities(base int) (map[string]int, error) {
	type devicePrios struct {
		Name     string
		BootPrio uint32
	}

	devices := []devicePrios{}

	for _, dev := range d.expandedDevices.Sorted() {
		if dev.Config["type"] != "disk" && dev.Config["type"] != "nic" {
			continue
		}

		bootPrio := uint32(0) // Default to lowest priority.
		if dev.Config["boot.priority"] != "" {
			prio, err := strconv.ParseInt(dev.Config["boot.priority"], 10, 32)
			if err != nil {
				return nil, fmt.Errorf("Invalid boot.priority for device %q: %w", dev.Name, err)
			}

			bootPrio = uint32(prio)
		} else if dev.Config["path"] == "/" {
			bootPrio = 1 // Set boot priority of root disk higher than any device without a boot prio.
		}

		devices = append(devices, devicePrios{Name: dev.Name, BootPrio: bootPrio})
	}

	// Sort devices by priority (use SliceStable so that devices with the same boot priority stay in the same
	// order each boot based on the device order provided by the d.expandedDevices.Sorted() function).
	// This is important because as well as providing a predicable boot index order, the boot index number can
	// also be used for other properties (such as disk SCSI ID) which can result in it being given different
	// device names inside the guest based on the device order.
	sort.SliceStable(devices, func(i, j int) bool { return devices[i].BootPrio > devices[j].BootPrio })

	sortedDevs := make(map[string]int, len(devices))
	for bootIndex, dev := range devices {
		sortedDevs[dev.Name] = bootIndex + base
	}

	return sortedDevs, nil
}

// generateQemuConfigFile writes the qemu config file and returns its location.
// It writes the config file inside the VM's log path.
func (d *qemu) generateQemuConfigFile(cpuInfo *cpuTopology, mountInfo *storagePools.MountInfo, busName string, vsockFD int, devConfs []*deviceConfig.RunConfig, fdFiles *[]*os.File) (string, []monitorHook, error) {
	var monHooks []monitorHook

	cfg := qemuBase(&qemuBaseOpts{d.Architecture()})

	err := d.addCPUMemoryConfig(&cfg, cpuInfo)
	if err != nil {
		return "", nil, err
	}

	// Parse raw.qemu.
	rawOptions := []string{}
	if d.expandedConfig["raw.qemu"] != "" {
		rawOptions, err = shellquote.Split(d.expandedConfig["raw.qemu"])
		if err != nil {
			return "", nil, err
		}
	}

	// Allow disabling the UEFI firmware.
	if slices.Contains(rawOptions, "-bios") || slices.Contains(rawOptions, "-kernel") {
		d.logger.Warn("Starting VM without default firmware (-bios or -kernel in raw.qemu)")
	} else if d.architectureSupportsUEFI(d.architecture) {
		// Open the UEFI NVRAM file and pass it via file descriptor to QEMU.
		// This is so the QEMU process can still read/write the file after it has dropped its user privs.
		nvRAMFile, err := os.Open(d.nvramPath())
		if err != nil {
			return "", nil, fmt.Errorf("Failed opening NVRAM file: %w", err)
		}

		// Determine expected firmware.
		firmwares := ovmfGenericFirmwares
		if util.IsTrue(d.expandedConfig["security.csm"]) {
			firmwares = ovmfCSMFirmwares
		} else if util.IsTrueOrEmpty(d.expandedConfig["security.secureboot"]) {
			firmwares = ovmfSecurebootFirmwares
		}

		var ovmfCode string
		for _, firmware := range firmwares {
			if util.PathExists(filepath.Join(d.Path(), firmware.vars)) {
				ovmfCode = firmware.code
				break
			}
		}

		if ovmfCode == "" {
			return "", nil, fmt.Errorf("Unable to locate matching firmware: %+v", firmwares)
		}

		driveFirmwareOpts := qemuDriveFirmwareOpts{
			roPath:    filepath.Join(d.ovmfPath(), ovmfCode),
			nvramPath: fmt.Sprintf("/dev/fd/%d", d.addFileDescriptor(fdFiles, nvRAMFile)),
		}

		cfg = append(cfg, qemuDriveFirmware(&driveFirmwareOpts)...)
	}

	// QMP socket.
	cfg = append(cfg, qemuControlSocket(&qemuControlSocketOpts{d.monitorPath()})...)

	// Console output.
	cfg = append(cfg, qemuConsole(&qemuConsoleOpts{d.consolePath()})...)

	// Setup the bus allocator.
	bus := qemuNewBus(busName, &cfg)

	// Now add the fixed set of devices. The multi-function groups used for these fixed internal devices are
	// specifically chosen to ensure that we consume exactly 4 PCI bus ports (on PCIe bus). This ensures that
	// the first user device NIC added will use the 5th PCI bus port and will be consistently named enp5s0
	// on PCIe (which we need to maintain compatibility with network configuration in our existing VM images).
	// It's also meant to group all low-bandwidth internal devices onto a single address. PCIe bus allows a
	// total of 256 devices, but this assumes 32 chassis * 8 function. By using VFs for the internal fixed
	// devices we avoid consuming a chassis for each one. See also the qemuPCIDeviceIDStart constant.
	devBus, devAddr, multi := bus.allocate(busFunctionGroupGeneric)
	balloonOpts := qemuDevOpts{
		busName:       bus.name,
		devBus:        devBus,
		devAddr:       devAddr,
		multifunction: multi,
	}

	cfg = append(cfg, qemuBalloon(&balloonOpts)...)

	devBus, devAddr, multi = bus.allocate(busFunctionGroupGeneric)
	rngOpts := qemuDevOpts{
		busName:       bus.name,
		devBus:        devBus,
		devAddr:       devAddr,
		multifunction: multi,
	}

	cfg = append(cfg, qemuRNG(&rngOpts)...)

	devBus, devAddr, multi = bus.allocate(busFunctionGroupGeneric)
	keyboardOpts := qemuDevOpts{
		busName:       bus.name,
		devBus:        devBus,
		devAddr:       devAddr,
		multifunction: multi,
	}

	cfg = append(cfg, qemuKeyboard(&keyboardOpts)...)

	devBus, devAddr, multi = bus.allocate(busFunctionGroupGeneric)
	tabletOpts := qemuDevOpts{
		busName:       bus.name,
		devBus:        devBus,
		devAddr:       devAddr,
		multifunction: multi,
	}

	cfg = append(cfg, qemuTablet(&tabletOpts)...)

	// Existing vsock ID from volatile.
	vsockID, err := d.getVsockID()
	if err != nil {
		return "", nil, err
	}

	devBus, devAddr, multi = bus.allocate(busFunctionGroupGeneric)
	vsockOpts := qemuVsockOpts{
		dev: qemuDevOpts{
			busName:       bus.name,
			devBus:        devBus,
			devAddr:       devAddr,
			multifunction: multi,
		},
		vsockFD: vsockFD,
		vsockID: vsockID,
	}

	cfg = append(cfg, qemuVsock(&vsockOpts)...)

	devBus, devAddr, multi = bus.allocate(busFunctionGroupGeneric)
	serialOpts := qemuSerialOpts{
		dev: qemuDevOpts{
			busName:       bus.name,
			devBus:        devBus,
			devAddr:       devAddr,
			multifunction: multi,
		},
		charDevName:      qemuSerialChardevName,
		ringbufSizeBytes: qmp.RingbufSize,
	}

	cfg = append(cfg, qemuSerial(&serialOpts)...)

	// s390x doesn't really have USB.
	if d.architecture != osarch.ARCH_64BIT_S390_BIG_ENDIAN {
		devBus, devAddr, multi = bus.allocate(busFunctionGroupGeneric)
		usbOpts := qemuUSBOpts{
			devBus:        devBus,
			devAddr:       devAddr,
			multifunction: multi,
			ports:         qemuSparseUSBPorts,
		}

		cfg = append(cfg, qemuUSB(&usbOpts)...)
	}

	if util.IsTrue(d.expandedConfig["security.csm"]) {
		// Allocate a regular entry to keep things aligned normally (avoid NICs getting a different name).
		_, _, _ = bus.allocate(busFunctionGroupNone)

		// Allocate a direct entry so the SCSI controller can be seen by seabios.
		devBus, devAddr, multi = bus.allocateDirect()
	} else {
		devBus, devAddr, multi = bus.allocate(busFunctionGroupNone)
	}

	scsiOpts := qemuDevOpts{
		busName:       bus.name,
		devBus:        devBus,
		devAddr:       devAddr,
		multifunction: multi,
	}

	cfg = append(cfg, qemuSCSI(&scsiOpts)...)

	// Always export the config directory as a 9p config drive, in case the host or VM guest doesn't support
	// virtio-fs.
	devBus, devAddr, multi = bus.allocate(busFunctionGroup9p)
	driveConfig9pOpts := qemuDriveConfigOpts{
		dev: qemuDevOpts{
			busName:       bus.name,
			devBus:        devBus,
			devAddr:       devAddr,
			multifunction: multi,
		},
		name:     "config",
		protocol: "9p",
		path:     d.configDriveMountPath(),
	}

	cfg = append(cfg, qemuDriveConfig(&driveConfig9pOpts)...)

	// Pass in the agents if INCUS_AGENT_PATH is set.
	if util.PathExists(os.Getenv("INCUS_AGENT_PATH")) {
		devBus, devAddr, multi = bus.allocate(busFunctionGroup9p)
		driveConfig9pOpts := qemuDriveConfigOpts{
			dev: qemuDevOpts{
				busName:       bus.name,
				devBus:        devBus,
				devAddr:       devAddr,
				multifunction: multi,
			},
			name:     "agent",
			protocol: "9p",
			path:     os.Getenv("INCUS_AGENT_PATH"),
		}

		cfg = append(cfg, qemuDriveConfig(&driveConfig9pOpts)...)
	}

	// If user has requested AMD SEV, check if supported and add to QEMU config.
	if util.IsTrue(d.expandedConfig["security.sev"]) {
		sevOpts, err := d.setupSEV(fdFiles)
		if err != nil {
			return "", nil, err
		}

		if sevOpts != nil {
			for i := range cfg {
				if cfg[i].name == "machine" {
					cfg[i].entries = append(cfg[i].entries, cfgEntry{"memory-encryption", "sev0"})
					break
				}
			}

			cfg = append(cfg, qemuSEV(sevOpts)...)
		}
	}

	if util.IsTrue(d.expandedConfig["security.csm"]) {
		// Allocate a regular entry to keep things aligned normally (avoid NICs getting a different name).
		_, _, _ = bus.allocate(busFunctionGroupNone)

		// Allocate a direct entry so the GPU can be seen by seabios.
		devBus, devAddr, multi = bus.allocateDirect()
	} else {
		devBus, devAddr, multi = bus.allocate(busFunctionGroupNone)
	}

	gpuOpts := qemuGpuOpts{
		dev: qemuDevOpts{
			busName:       bus.name,
			devBus:        devBus,
			devAddr:       devAddr,
			multifunction: multi,
		},
		architecture: d.Architecture(),
	}

	cfg = append(cfg, qemuGPU(&gpuOpts)...)

	// Dynamic devices.
	base := 0
	if slices.Contains(rawOptions, "-kernel") {
		base = 1
	}

	bootIndexes, err := d.deviceBootPriorities(base)
	if err != nil {
		return "", nil, fmt.Errorf("Error calculating boot indexes: %w", err)
	}

	// Record the mounts we are going to do inside the VM using the agent.
	agentMounts := []instancetype.VMAgentMount{}

	// These devices are sorted so that NICs are added first to ensure that the first NIC can use the 5th
	// PCIe bus port and will be consistently named enp5s0 for compatibility with network configuration in our
	// existing VM images. Even on non-PCIe busses having NICs first means that their names won't change when
	// other devices are added.
	for _, runConf := range devConfs {
		// Add drive devices.
		if len(runConf.Mounts) > 0 {
			for _, drive := range runConf.Mounts {
				var monHook monitorHook

				// Check if the user has overridden the bus.
				busName := "virtio-scsi"
				for _, opt := range drive.Opts {
					if !strings.HasPrefix(opt, "bus=") {
						continue
					}

					busName = strings.TrimPrefix(opt, "bus=")
					break
				}

				qemuDev := make(map[string]string)
				if slices.Contains([]string{"nvme", "virtio-blk"}, busName) {
					// Allocate a PCI(e) port and write it to the config file so QMP can "hotplug" the
					// drive into it later.
					devBus, devAddr, multi := bus.allocate(busFunctionGroupNone)

					// Populate the qemu device with port info.
					qemuDev["bus"] = devBus
					qemuDev["addr"] = devAddr

					if multi {
						qemuDev["multifunction"] = "on"
					}
				}

				if drive.TargetPath == "/" {
					monHook, err = d.addRootDriveConfig(qemuDev, mountInfo, bootIndexes, drive)
				} else if drive.FSType == "9p" {
					err = d.addDriveDirConfig(&cfg, bus, fdFiles, &agentMounts, drive)
				} else {
					monHook, err = d.addDriveConfig(qemuDev, bootIndexes, drive)
				}

				if err != nil {
					return "", nil, fmt.Errorf("Failed setting up disk device %q: %w", drive.DevName, err)
				}

				if monHook != nil {
					monHooks = append(monHooks, monHook)
				}
			}
		}

		// Add network device.
		if len(runConf.NetworkInterface) > 0 {
			qemuDev := make(map[string]string)
			if slices.Contains([]string{"pcie", "pci"}, bus.name) {
				// Allocate a PCI(e) port and write it to the config file so QMP can "hotplug" the
				// NIC into it later.
				devBus, devAddr, multi := bus.allocate(busFunctionGroupNone)

				// Populate the qemu device with port info.
				qemuDev["bus"] = devBus
				qemuDev["addr"] = devAddr

				if multi {
					qemuDev["multifunction"] = "on"
				}
			}

			monHook, err := d.addNetDevConfig(bus.name, qemuDev, bootIndexes, runConf.NetworkInterface)
			if err != nil {
				return "", nil, err
			}

			monHooks = append(monHooks, monHook)
		}

		// Add GPU device.
		if len(runConf.GPUDevice) > 0 {
			err = d.addGPUDevConfig(&cfg, bus, runConf.GPUDevice)
			if err != nil {
				return "", nil, err
			}
		}

		// Add PCI device.
		if len(runConf.PCIDevice) > 0 {
			err = d.addPCIDevConfig(&cfg, bus, runConf.PCIDevice)
			if err != nil {
				return "", nil, err
			}
		}

		// Add USB devices.
		for _, usbDev := range runConf.USBDevice {
			monHook, err := d.addUSBDeviceConfig(usbDev)
			if err != nil {
				return "", nil, err
			}

			monHooks = append(monHooks, monHook)
		}

		// Add TPM device.
		if len(runConf.TPMDevice) > 0 {
			err = d.addTPMDeviceConfig(&cfg, runConf.TPMDevice)
			if err != nil {
				return "", nil, err
			}
		}
	}

	// VM generation ID is only available on x86.
	if d.architecture == osarch.ARCH_64BIT_INTEL_X86 {
		err = d.addVmgenDeviceConfig(&cfg, d.localConfig["volatile.uuid.generation"])
		if err != nil {
			return "", nil, err
		}
	}

	// Allocate 4 PCI slots for hotplug devices.
	for i := 0; i < 4; i++ {
		bus.allocate(busFunctionGroupNone)
	}

	// Write the agent mount config.
	agentMountJSON, err := json.Marshal(agentMounts)
	if err != nil {
		return "", nil, fmt.Errorf("Failed marshalling agent mounts to JSON: %w", err)
	}

	agentMountFile := filepath.Join(d.Path(), "config", "agent-mounts.json")
	err = os.WriteFile(agentMountFile, agentMountJSON, 0400)
	if err != nil {
		return "", nil, fmt.Errorf("Failed writing agent mounts file: %w", err)
	}

	// process any user-specified overrides
	cfg = qemuRawCfgOverride(cfg, d.expandedConfig)
	// Write the config file to disk.
	sb := qemuStringifyCfg(cfg...)
	configPath := filepath.Join(d.RunPath(), "qemu.conf")
	return configPath, monHooks, os.WriteFile(configPath, []byte(sb.String()), 0640)
}

// addCPUMemoryConfig adds the qemu config required for setting the number of virtualised CPUs and memory.
// If sb is nil then no config is written.
func (d *qemu) addCPUMemoryConfig(cfg *[]cfgSection, cpuInfo *cpuTopology) error {
	// Figure out what memory object layout we're going to use.
	// Before v6.0 or if version unknown, we use the "repeated" format, otherwise we use "indexed" format.
	qemuMemObjectFormat := "repeated"
	qemuVer6, _ := version.NewDottedVersion("6.0")
	qemuVer, _ := d.version()
	if qemuVer != nil && qemuVer.Compare(qemuVer6) >= 0 {
		qemuMemObjectFormat = "indexed"
	}

	cpuOpts := qemuCPUOpts{
		architecture:        d.architectureName,
		qemuMemObjectFormat: qemuMemObjectFormat,
	}

	cpuPinning := false

	hostNodes := []uint64{}
	if cpuInfo.vcpus == nil {
		// If not pinning, default to exposing cores.
		// Only one CPU will be added here, as the others will be hotplugged during start.
		if d.architectureSupportsCPUHotplug() {
			cpuOpts.cpuCount = 1
			cpuOpts.cpuCores = 1

			// Expose the total requested by the user already so the hotplug limit can be set higher if needed.
			cpuOpts.cpuRequested = cpuInfo.cores
		} else {
			cpuOpts.cpuCount = cpuInfo.cores
			cpuOpts.cpuCores = cpuInfo.cores
		}

		cpuOpts.cpuSockets = 1
		cpuOpts.cpuThreads = 1
		hostNodes = []uint64{0}

		// Handle NUMA restrictions.
		numaNodes := d.expandedConfig["limits.cpu.nodes"]
		if numaNodes != "" {
			if numaNodes == "balanced" {
				numaNodes = d.expandedConfig["volatile.cpu.nodes"]
			}

			// Parse the NUMA restriction.
			numaNodeSet, err := resources.ParseNumaNodeSet(numaNodes)
			if err != nil {
				return err
			}

			cpuOpts.memoryHostNodes = numaNodeSet
		}
	} else {
		cpuPinning = true

		// Figure out socket-id/core-id/thread-id for all vcpus.
		vcpuSocket := map[uint64]uint64{}
		vcpuCore := map[uint64]uint64{}
		vcpuThread := map[uint64]uint64{}
		vcpu := uint64(0)
		for i := 0; i < cpuInfo.sockets; i++ {
			for j := 0; j < cpuInfo.cores; j++ {
				for k := 0; k < cpuInfo.threads; k++ {
					vcpuSocket[vcpu] = uint64(i)
					vcpuCore[vcpu] = uint64(j)
					vcpuThread[vcpu] = uint64(k)
					vcpu++
				}
			}
		}

		// Prepare the NUMA map.
		numa := []qemuNumaEntry{}
		numaIDs := []uint64{}
		numaNode := uint64(0)
		for hostNode, entry := range cpuInfo.nodes {
			hostNodes = append(hostNodes, hostNode)

			numaIDs = append(numaIDs, numaNode)
			for _, vcpu := range entry {
				numa = append(numa, qemuNumaEntry{
					node:   numaNode,
					socket: vcpuSocket[vcpu],
					core:   vcpuCore[vcpu],
					thread: vcpuThread[vcpu],
				})
			}

			numaNode++
		}

		// Prepare context.
		cpuOpts.cpuCount = len(cpuInfo.vcpus)
		cpuOpts.cpuSockets = cpuInfo.sockets
		cpuOpts.cpuCores = cpuInfo.cores
		cpuOpts.cpuThreads = cpuInfo.threads
		cpuOpts.cpuNumaNodes = numaIDs
		cpuOpts.cpuNumaMapping = numa
		cpuOpts.cpuNumaHostNodes = hostNodes
	}

	// Configure memory limit.
	memSize := d.expandedConfig["limits.memory"]
	if memSize == "" {
		memSize = QEMUDefaultMemSize // Default if no memory limit specified.
	}

	memSizeBytes, err := units.ParseByteSizeString(memSize)
	if err != nil {
		return fmt.Errorf("limits.memory invalid: %w", err)
	}

	cpuOpts.hugepages = ""
	if util.IsTrue(d.expandedConfig["limits.memory.hugepages"]) {
		hugetlb, err := localUtil.HugepagesPath()
		if err != nil {
			return err
		}

		cpuOpts.hugepages = hugetlb
	}

	// Determine per-node memory limit.
	memSizeMB := memSizeBytes / 1024 / 1024
	nodeMemory := int64(memSizeMB / int64(len(hostNodes)))
	cpuOpts.memory = nodeMemory

	if cfg != nil {
		*cfg = append(*cfg, qemuMemory(&qemuMemoryOpts{memSizeMB})...)
		*cfg = append(*cfg, qemuCPU(&cpuOpts, cpuPinning)...)
	}

	return nil
}

// addFileDescriptor adds a file path to the list of files to open and pass file descriptor to qemu.
// Returns the file descriptor number that qemu will receive.
func (d *qemu) addFileDescriptor(fdFiles *[]*os.File, file *os.File) int {
	// Append the tap device file path to the list of files to be opened and passed to qemu.
	*fdFiles = append(*fdFiles, file)
	return 2 + len(*fdFiles) // Use 2+fdFiles count, as first user file descriptor is 3.
}

// addRootDriveConfig adds the qemu config required for adding the root drive.
func (d *qemu) addRootDriveConfig(qemuDev map[string]string, mountInfo *storagePools.MountInfo, bootIndexes map[string]int, rootDriveConf deviceConfig.MountEntryItem) (monitorHook, error) {
	if rootDriveConf.TargetPath != "/" {
		return nil, fmt.Errorf("Non-root drive config supplied")
	}

	if !d.storagePool.Driver().Info().Remote && mountInfo.DiskPath == "" {
		return nil, fmt.Errorf("No root disk path available from mount")
	}

	// Generate a new device config with the root device path expanded.
	driveConf := deviceConfig.MountEntryItem{
		DevName:    rootDriveConf.DevName,
		DevPath:    mountInfo.DiskPath,
		Opts:       rootDriveConf.Opts,
		TargetPath: rootDriveConf.TargetPath,
	}

	if d.storagePool.Driver().Info().Remote {
		vol := d.storagePool.GetVolume(storageDrivers.VolumeTypeVM, storageDrivers.ContentTypeBlock, project.Instance(d.project.Name, d.name), nil)

		if slices.Contains([]string{"ceph", "cephfs"}, d.storagePool.Driver().Info().Name) {
			config := d.storagePool.ToAPI().Config

			userName := config["ceph.user.name"]
			if userName == "" {
				userName = storageDrivers.CephDefaultUser
			}

			clusterName := config["ceph.cluster_name"]
			if clusterName == "" {
				clusterName = storageDrivers.CephDefaultUser
			}

			driveConf.DevPath = device.DiskGetRBDFormat(clusterName, userName, config["ceph.osd.pool_name"], vol.Name())
		}
	}

	return d.addDriveConfig(qemuDev, bootIndexes, driveConf)
}

// addDriveDirConfig adds the qemu config required for adding a supplementary drive directory share.
func (d *qemu) addDriveDirConfig(cfg *[]cfgSection, bus *qemuBus, fdFiles *[]*os.File, agentMounts *[]instancetype.VMAgentMount, driveConf deviceConfig.MountEntryItem) error {
	mountTag := fmt.Sprintf("incus_%s", driveConf.DevName)

	agentMount := instancetype.VMAgentMount{
		Source: mountTag,
		Target: driveConf.TargetPath,
		FSType: driveConf.FSType,
	}

	// If mount type is 9p, we need to specify to use the virtio transport to support more VM guest OSes.
	// Also set the msize to 32MB to allow for reasonably fast 9p access.
	if agentMount.FSType == "9p" {
		agentMount.Options = append(agentMount.Options, "trans=virtio,msize=33554432")
	}

	readonly := slices.Contains(driveConf.Opts, "ro")

	// Indicate to agent to mount this readonly. Note: This is purely to indicate to VM guest that this is
	// readonly, it should *not* be used as a security measure, as the VM guest could remount it R/W.
	if readonly {
		agentMount.Options = append(agentMount.Options, "ro")
	}

	// Record the 9p mount for the agent.
	*agentMounts = append(*agentMounts, agentMount)

	// Check if the disk device has provided a virtiofsd socket path.
	var virtiofsdSockPath string
	for _, opt := range driveConf.Opts {
		if strings.HasPrefix(opt, fmt.Sprintf("%s=", device.DiskVirtiofsdSockMountOpt)) {
			parts := strings.SplitN(opt, "=", 2)
			virtiofsdSockPath = parts[1]
		}
	}

	// If there is a virtiofsd socket path setup the virtio-fs share.
	if virtiofsdSockPath != "" {
		if !util.PathExists(virtiofsdSockPath) {
			return fmt.Errorf("virtiofsd socket path %q doesn't exist", virtiofsdSockPath)
		}

		devBus, devAddr, multi := bus.allocate(busFunctionGroup9p)

		// Add virtio-fs device as this will be preferred over 9p.
		driveDirVirtioOpts := qemuDriveDirOpts{
			dev: qemuDevOpts{
				busName:       bus.name,
				devBus:        devBus,
				devAddr:       devAddr,
				multifunction: multi,
			},
			devName:  driveConf.DevName,
			mountTag: mountTag,
			path:     virtiofsdSockPath,
			protocol: "virtio-fs",
		}
		*cfg = append(*cfg, qemuDriveDir(&driveDirVirtioOpts)...)
	}

	// Add 9p share config.
	devBus, devAddr, multi := bus.allocate(busFunctionGroup9p)

	fd, err := strconv.Atoi(driveConf.DevPath)
	if err != nil {
		return fmt.Errorf("Invalid file descriptor %q for drive %q: %w", driveConf.DevPath, driveConf.DevName, err)
	}

	proxyFD := d.addFileDescriptor(fdFiles, os.NewFile(uintptr(fd), driveConf.DevName))

	driveDir9pOpts := qemuDriveDirOpts{
		dev: qemuDevOpts{
			busName:       bus.name,
			devBus:        devBus,
			devAddr:       devAddr,
			multifunction: multi,
		},
		devName:  driveConf.DevName,
		mountTag: mountTag,
		proxyFD:  proxyFD, // Pass by file descriptor
		readonly: readonly,
		protocol: "9p",
	}
	*cfg = append(*cfg, qemuDriveDir(&driveDir9pOpts)...)

	return nil
}

// addDriveConfig adds the qemu config required for adding a supplementary drive.
func (d *qemu) addDriveConfig(qemuDev map[string]string, bootIndexes map[string]int, driveConf deviceConfig.MountEntryItem) (monitorHook, error) {
	aioMode := "native" // Use native kernel async IO and O_DIRECT by default.
	cacheMode := "none" // Bypass host cache, use O_DIRECT semantics by default.
	media := "disk"
	isRBDImage := strings.HasPrefix(driveConf.DevPath, device.RBDFormatPrefix)

	// Check supported features.
	// Use io_uring over native for added performance (if supported by QEMU and kernel is recent enough).
	// We've seen issues starting VMs when running with io_ring AIO mode on kernels before 5.13.
	info := DriverStatuses()[instancetype.VM].Info
	minVer, _ := version.NewDottedVersion("5.13.0")
	_, ioUring := info.Features["io_uring"]
	if slices.Contains(driveConf.Opts, device.DiskIOUring) && ioUring && d.state.OS.KernelVersion.Compare(minVer) >= 0 {
		aioMode = "io_uring"
	}

	var isBlockDev bool

	// Detect device caches and I/O modes.
	if isRBDImage {
		// For RBD, we want writeback to allow for the system-configured "rbd cache" to take effect if present.
		cacheMode = "writeback"
	} else {
		srcDevPath := driveConf.DevPath // This should not be used for passing to QEMU, only for probing.

		// Detect if existing file descriptor format is being supplied.
		if strings.HasPrefix(driveConf.DevPath, fmt.Sprintf("%s:", device.DiskFileDescriptorMountPrefix)) {
			// Expect devPath in format "fd:<fdNum>:<devPath>".
			devPathParts := strings.SplitN(driveConf.DevPath, ":", 3)
			if len(devPathParts) != 3 || !strings.HasPrefix(driveConf.DevPath, fmt.Sprintf("%s:", device.DiskFileDescriptorMountPrefix)) {
				return nil, fmt.Errorf("Unexpected devPath file descriptor format %q", driveConf.DevPath)
			}

			// Map the file descriptor to the file descriptor path it will be in the QEMU process.
			fd, err := strconv.Atoi(devPathParts[1])
			if err != nil {
				return nil, fmt.Errorf("Invalid file descriptor %q: %w", devPathParts[1], err)
			}

			// Extract original dev path for additional probing below.
			srcDevPath = devPathParts[2]
			if srcDevPath == "" {
				return nil, fmt.Errorf("Device source path is empty")
			}

			driveConf.DevPath = fmt.Sprintf("/proc/self/fd/%d", fd)
		} else if driveConf.TargetPath != "/" {
			// Only the root disk device is allowed to pass local devices to us without using an FD.
			return nil, fmt.Errorf("Invalid device path format %q", driveConf.DevPath)
		}

		srcDevPathInfo, err := os.Stat(srcDevPath)
		if err != nil {
			return nil, fmt.Errorf("Invalid source path %q: %w", srcDevPath, err)
		}

		isBlockDev = linux.IsBlockdev(srcDevPathInfo.Mode())

		// Handle I/O mode configuration.
		if !isBlockDev {
			// Disk dev path is a file, check what the backing filesystem is.
			fsType, err := linux.DetectFilesystem(srcDevPath)
			if err != nil {
				return nil, fmt.Errorf("Failed detecting filesystem type of %q: %w", srcDevPath, err)
			}

			// If backing FS is ZFS or BTRFS, avoid using direct I/O and use host page cache only.
			// We've seen ZFS lock up and BTRFS checksum issues when using direct I/O on image files.
			if fsType == "zfs" || fsType == "btrfs" {
				aioMode = "threads"
				cacheMode = "writeback" // Use host cache, with neither O_DSYNC nor O_DIRECT semantics.
			} else {
				// Use host cache, with neither O_DSYNC nor O_DIRECT semantics if filesystem
				// doesn't support Direct I/O.
				_, err := os.OpenFile(srcDevPath, unix.O_DIRECT|unix.O_RDONLY, 0)
				if err != nil {
					cacheMode = "writeback"
				}
			}

			if cacheMode == "writeback" && driveConf.FSType != "iso9660" {
				// Only warn about using writeback cache if the drive image is writable.
				d.logger.Warn("Using writeback cache I/O", logger.Ctx{"device": driveConf.DevName, "devPath": srcDevPath, "fsType": fsType})
			}
		} else if !slices.Contains(driveConf.Opts, device.DiskDirectIO) {
			// If drive config indicates we need to use unsafe I/O then use it.
			d.logger.Warn("Using unsafe cache I/O", logger.Ctx{"device": driveConf.DevName, "devPath": srcDevPath})
			aioMode = "threads"
			cacheMode = "unsafe" // Use host cache, but ignore all sync requests from guest.
		}
	}

	// Special case ISO images as cdroms.
	if driveConf.FSType == "iso9660" {
		media = "cdrom"
	}

	// Check if the user has overridden the bus.
	bus := "virtio-scsi"
	for _, opt := range driveConf.Opts {
		if !strings.HasPrefix(opt, "bus=") {
			continue
		}

		bus = strings.TrimPrefix(opt, "bus=")
		break
	}

	// Check if the user has overridden the cache mode.
	for _, opt := range driveConf.Opts {
		if !strings.HasPrefix(opt, "cache=") {
			continue
		}

		cacheMode = strings.TrimPrefix(opt, "cache=")
		break
	}

	// QMP uses two separate values for the cache.
	directCache := true   // Bypass host cache, use O_DIRECT semantics by default.
	noFlushCache := false // Don't ignore any flush requests for the device.

	if cacheMode == "unsafe" {
		directCache = false
		noFlushCache = true
	} else if cacheMode == "writeback" {
		directCache = false
	}

	escapedDeviceName := linux.PathNameEncode(driveConf.DevName)

	blockDev := map[string]any{
		"aio": aioMode,
		"cache": map[string]any{
			"direct":   directCache,
			"no-flush": noFlushCache,
		},
		"discard":   "unmap", // Forward as an unmap request. This is the same as `discard=on` in the qemu config file.
		"driver":    "file",
		"node-name": d.blockNodeName(escapedDeviceName),
		"read-only": false,
	}

	var rbdSecret string

	// If driver is "file", QEMU requires the file to be a regular file.
	// However, if the file is a character or block device, driver needs to be set to "host_device".
	if isBlockDev {
		blockDev["driver"] = "host_device"
	} else if isRBDImage {
		blockDev["driver"] = "rbd"

		_, volName, opts, err := device.DiskParseRBDFormat(driveConf.DevPath)
		if err != nil {
			return nil, fmt.Errorf("Failed parsing rbd string: %w", err)
		}

		// Driver and pool name arguments can be ignored as CephGetRBDImageName doesn't need them.
		volumeType := storageDrivers.VolumeTypeCustom
		volumeName := project.StorageVolume(d.project.Name, volName)

		// Handle different name for instance volumes.
		if driveConf.TargetPath == "/" {
			volumeType = storageDrivers.VolumeTypeVM
			volumeName = volName
		}

		// Identify the right content type.
		rbdContentType := storageDrivers.ContentTypeBlock
		if driveConf.FSType == "iso9660" {
			rbdContentType = storageDrivers.ContentTypeISO
		}

		// Get the RBD image name.
		vol := storageDrivers.NewVolume(nil, "", volumeType, rbdContentType, volumeName, nil, nil)
		rbdImageName := storageDrivers.CephGetRBDImageName(vol, "", false)

		// Parse the options (ceph credentials).
		userName := storageDrivers.CephDefaultUser
		clusterName := storageDrivers.CephDefaultCluster
		poolName := ""

		for _, option := range opts {
			fields := strings.Split(option, "=")
			if len(fields) != 2 {
				return nil, fmt.Errorf("Unexpected volume rbd option %q", option)
			}

			if fields[0] == "id" {
				userName = fields[1]
			} else if fields[0] == "pool" {
				poolName = fields[1]
			} else if fields[0] == "conf" {
				baseName := filepath.Base(fields[1])
				clusterName = strings.TrimSuffix(baseName, ".conf")
			}
		}

		if poolName == "" {
			return nil, fmt.Errorf("Missing pool name")
		}

		// The aio option isn't available when using the rbd driver.
		delete(blockDev, "aio")
		blockDev["pool"] = poolName
		blockDev["image"] = rbdImageName
		blockDev["user"] = userName
		blockDev["server"] = []map[string]string{}
		blockDev["conf"] = fmt.Sprintf("/etc/ceph/%s.conf", clusterName)

		// Setup the Ceph cluster config (monitors and keyring).
		monitors, err := storageDrivers.CephMonitors(clusterName)
		if err != nil {
			return nil, err
		}

		for _, monitor := range monitors {
			idx := strings.LastIndex(monitor, ":")
			host := monitor[:idx]
			port := monitor[idx+1:]

			blockDev["server"] = append(blockDev["server"].([]map[string]string), map[string]string{
				"host": strings.Trim(host, "[]"),
				"port": port,
			})
		}

		rbdSecret, err = storageDrivers.CephKeyring(clusterName, userName)
		if err != nil {
			return nil, err
		}
	}

	readonly := slices.Contains(driveConf.Opts, "ro")

	if readonly {
		blockDev["read-only"] = true
	}

	if !isRBDImage {
		blockDev["locking"] = "off"
	}

	if qemuDev == nil {
		qemuDev = map[string]string{}
	}

	qemuDev["id"] = fmt.Sprintf("%s%s", qemuDeviceIDPrefix, escapedDeviceName)
	qemuDev["drive"] = blockDev["node-name"].(string)
	qemuDev["serial"] = fmt.Sprintf("%s%s", qemuBlockDevIDPrefix, escapedDeviceName)

	if bus == "virtio-scsi" {
		qemuDev["channel"] = "0"
		qemuDev["lun"] = "1"
		qemuDev["bus"] = "qemu_scsi.0"

		if media == "disk" {
			qemuDev["driver"] = "scsi-hd"
		} else if media == "cdrom" {
			qemuDev["driver"] = "scsi-cd"
		}
	} else if slices.Contains([]string{"nvme", "virtio-blk"}, bus) {
		if qemuDev["bus"] == "" {
			// Figure out a hotplug slot.
			pciDevID := qemuPCIDeviceIDStart

			// Iterate through all the instance devices in the same sorted order as is used when allocating the
			// boot time devices in order to find the PCI bus slot device we would have used at boot time.
			// Then attempt to use that same device, assuming it is available.
			for _, dev := range d.expandedDevices.Sorted() {
				if dev.Name == driveConf.DevName {
					break // Found our device.
				}

				pciDevID++
			}

			pciDeviceName := fmt.Sprintf("%s%d", busDevicePortPrefix, pciDevID)
			d.logger.Debug("Using PCI bus device to hotplug drive into", logger.Ctx{"device": driveConf.DevName, "port": pciDeviceName})
			qemuDev["bus"] = pciDeviceName
			qemuDev["addr"] = "00.0"
		}

		qemuDev["driver"] = bus
	}

	if bootIndexes != nil {
		qemuDev["bootindex"] = strconv.Itoa(bootIndexes[driveConf.DevName])
	}

	monHook := func(m *qmp.Monitor) error {
		revert := revert.New()
		defer revert.Fail()

		nodeName := fmt.Sprintf("%s%s", qemuBlockDevIDPrefix, escapedDeviceName)

		if isRBDImage {
			secretID := fmt.Sprintf("pool_%s_%s", blockDev["pool"], blockDev["user"])

			err := m.AddSecret(secretID, rbdSecret)
			if err != nil {
				return err
			}

			blockDev["key-secret"] = secretID
		} else {
			permissions := unix.O_RDWR

			if readonly {
				permissions = unix.O_RDONLY
			}

			f, err := os.OpenFile(driveConf.DevPath, permissions, 0)
			if err != nil {
				return fmt.Errorf("Failed opening file descriptor for disk device %q: %w", driveConf.DevName, err)
			}

			defer func() { _ = f.Close() }()

			info, err := m.SendFileWithFDSet(nodeName, f, readonly)
			if err != nil {
				return fmt.Errorf("Failed sending file descriptor of %q for disk device %q: %w", f.Name(), driveConf.DevName, err)
			}

			revert.Add(func() {
				_ = m.RemoveFDFromFDSet(nodeName)
			})

			blockDev["filename"] = fmt.Sprintf("/dev/fdset/%d", info.ID)
		}

		err := m.AddBlockDevice(blockDev, qemuDev)
		if err != nil {
			return fmt.Errorf("Failed adding block device for disk device %q: %w", driveConf.DevName, err)
		}

		if driveConf.Limits != nil {
			err = m.SetBlockThrottle(qemuDev["id"], int(driveConf.Limits.ReadBytes), int(driveConf.Limits.WriteBytes), int(driveConf.Limits.ReadIOps), int(driveConf.Limits.WriteIOps))
			if err != nil {
				return fmt.Errorf("Failed applying limits for disk device %q: %w", driveConf.DevName, err)
			}
		}

		revert.Success()
		return nil
	}

	return monHook, nil
}

// addNetDevConfig adds the qemu config required for adding a network device.
// The qemuDev map is expected to be preconfigured with the settings for an existing port to use for the device.
func (d *qemu) addNetDevConfig(busName string, qemuDev map[string]string, bootIndexes map[string]int, nicConfig []deviceConfig.RunConfigItem) (monitorHook, error) {
	reverter := revert.New()
	defer reverter.Fail()

	var devName, nicName, devHwaddr, pciSlotName, pciIOMMUGroup, vDPADevName, vhostVDPAPath, maxVQP string
	for _, nicItem := range nicConfig {
		if nicItem.Key == "devName" {
			devName = nicItem.Value
		} else if nicItem.Key == "link" {
			nicName = nicItem.Value
		} else if nicItem.Key == "hwaddr" {
			devHwaddr = nicItem.Value
		} else if nicItem.Key == "pciSlotName" {
			pciSlotName = nicItem.Value
		} else if nicItem.Key == "pciIOMMUGroup" {
			pciIOMMUGroup = nicItem.Value
		} else if nicItem.Key == "vDPADevName" {
			vDPADevName = nicItem.Value
		} else if nicItem.Key == "vhostVDPAPath" {
			vhostVDPAPath = nicItem.Value
		} else if nicItem.Key == "maxVQP" {
			maxVQP = nicItem.Value
		}
	}

	escapedDeviceName := linux.PathNameEncode(devName)
	qemuDev["id"] = fmt.Sprintf("%s%s", qemuDeviceIDPrefix, escapedDeviceName)

	if len(bootIndexes) > 0 {
		bootIndex, found := bootIndexes[devName]
		if found {
			qemuDev["bootindex"] = strconv.Itoa(bootIndex)
		}
	}

	var monHook func(m *qmp.Monitor) error

	// configureQueues modifies qemuDev with the queue configuration based on vCPUs.
	// Returns the number of queues to use with NIC.
	configureQueues := func(cpuCount int) int {
		// Number of queues is the same as number of vCPUs. Run with a minimum of two queues.
		queueCount := cpuCount
		if queueCount < 2 {
			queueCount = 2
		}

		// Number of vectors is number of vCPUs * 2 (RX/TX) + 2 (config/control MSI-X).
		vectors := 2*queueCount + 2
		if vectors > 0 {
			qemuDev["mq"] = "on"
			if slices.Contains([]string{"pcie", "pci"}, busName) {
				qemuDev["vectors"] = strconv.Itoa(vectors)
			}
		}

		return queueCount
	}

	// tapMonHook is a helper function used as the monitor hook for macvtap and tap interfaces to open
	// multi-queue file handles to both the interface device and the vhost-net device and pass them to QEMU.
	tapMonHook := func(deviceFile func() (*os.File, error)) func(m *qmp.Monitor) error {
		return func(m *qmp.Monitor) error {
			reverter := revert.New()
			defer reverter.Fail()

			cpus, err := m.QueryCPUs()
			if err != nil {
				return fmt.Errorf("Failed getting CPU list for NIC queues")
			}

			queueCount := configureQueues(len(cpus))

			// Enable vhost_net offloading if available.
			info := DriverStatuses()[instancetype.VM].Info
			_, vhostNetEnabled := info.Features["vhost_net"]

			// Open the device once for each queue and pass to QEMU.
			fds := make([]string, 0, queueCount)
			vhostfds := make([]string, 0, queueCount)
			for i := 0; i < queueCount; i++ {
				devFile, err := deviceFile()
				if err != nil {
					return fmt.Errorf("Error opening netdev file for queue %d: %w", i, err)
				}

				defer func() { _ = devFile.Close() }() // Close file after device has been added.

				devFDName := fmt.Sprintf("%s.%d", devFile.Name(), i)
				err = m.SendFile(devFDName, devFile)
				if err != nil {
					return fmt.Errorf("Failed to send %q file descriptor for queue %d: %w", devFDName, i, err)
				}

				reverter.Add(func() { _ = m.CloseFile(devFDName) })

				fds = append(fds, devFDName)

				if vhostNetEnabled {
					// Open a vhost-net file handle for each device file handle.
					vhostFile, err := os.OpenFile("/dev/vhost-net", os.O_RDWR, 0)
					if err != nil {
						return fmt.Errorf("Error opening /dev/vhost-net for queue %d: %w", i, err)
					}

					defer func() { _ = vhostFile.Close() }() // Close file after device has been added.

					vhostFDName := fmt.Sprintf("%s.%d", vhostFile.Name(), i)
					err = m.SendFile(vhostFDName, vhostFile)
					if err != nil {
						return fmt.Errorf("Failed to send %q file descriptor for queue %d: %w", vhostFDName, i, err)
					}

					reverter.Add(func() { _ = m.CloseFile(vhostFDName) })

					vhostfds = append(vhostfds, vhostFDName)
				}
			}

			qemuNetDev := map[string]any{
				"id":    fmt.Sprintf("%s%s", qemuNetDevIDPrefix, escapedDeviceName),
				"type":  "tap",
				"vhost": vhostNetEnabled,
			}

			if slices.Contains([]string{"pcie", "pci"}, busName) {
				qemuDev["driver"] = "virtio-net-pci"
			} else if busName == "ccw" {
				qemuDev["driver"] = "virtio-net-ccw"
			}

			qemuNetDev["fds"] = strings.Join(fds, ":")

			if len(vhostfds) > 0 {
				qemuNetDev["vhostfds"] = strings.Join(vhostfds, ":")
			}

			qemuDev["netdev"] = qemuNetDev["id"].(string)
			qemuDev["mac"] = devHwaddr

			err = m.AddNIC(qemuNetDev, qemuDev)
			if err != nil {
				return fmt.Errorf("Failed setting up device %q: %w", devName, err)
			}

			reverter.Success()
			return nil
		}
	}

	// Detect MACVTAP interface types and figure out which tap device is being used.
	// This is so we can open a file handle to the tap device and pass it to the qemu process.
	if util.PathExists(fmt.Sprintf("/sys/class/net/%s/macvtap", nicName)) {
		content, err := os.ReadFile(fmt.Sprintf("/sys/class/net/%s/ifindex", nicName))
		if err != nil {
			return nil, fmt.Errorf("Error getting tap device ifindex: %w", err)
		}

		ifindex, err := strconv.Atoi(strings.TrimSpace(string(content)))
		if err != nil {
			return nil, fmt.Errorf("Error parsing tap device ifindex: %w", err)
		}

		devFile := func() (*os.File, error) {
			return os.OpenFile(fmt.Sprintf("/dev/tap%d", ifindex), os.O_RDWR, 0)
		}

		monHook = tapMonHook(devFile)
	} else if util.PathExists(fmt.Sprintf("/sys/class/net/%s/tun_flags", nicName)) {
		// Detect TAP interface and use IOCTL TUNSETIFF on /dev/net/tun to get the file handle to it.
		// This is so we can open a file handle to the tap device and pass it to the qemu process.
		devFile := func() (*os.File, error) {
			revert := revert.New()
			defer revert.Fail()

			f, err := os.OpenFile("/dev/net/tun", os.O_RDWR, 0)
			if err != nil {
				return nil, err
			}

			revert.Add(func() { _ = f.Close() })

			ifr, err := unix.NewIfreq(nicName)
			if err != nil {
				return nil, fmt.Errorf("Error creating new ifreq for %q: %w", nicName, err)
			}

			// These settings need to be compatible with what the device created the interface with
			// and what QEMU is expecting.
			ifr.SetUint16(unix.IFF_TAP | unix.IFF_NO_PI | unix.IFF_ONE_QUEUE | unix.IFF_MULTI_QUEUE | unix.IFF_VNET_HDR)

			// Sets the file handle to point to the requested NIC interface.
			err = unix.IoctlIfreq(int(f.Fd()), unix.TUNSETIFF, ifr)
			if err != nil {
				return nil, fmt.Errorf("Error getting TAP file handle for %q: %w", nicName, err)
			}

			revert.Success()
			return f, nil
		}

		monHook = tapMonHook(devFile)
	} else if util.PathExists(vhostVDPAPath) {
		monHook = func(m *qmp.Monitor) error {
			reverter := revert.New()
			defer reverter.Fail()

			vdpaDevFile, err := os.OpenFile(vhostVDPAPath, os.O_RDWR, 0)
			if err != nil {
				return fmt.Errorf("Error opening vDPA device file %q: %w", vdpaDevFile.Name(), err)
			}

			defer func() { _ = vdpaDevFile.Close() }() // Close file after device has been added.

			vDPADevFDName := fmt.Sprintf("%s.0", vdpaDevFile.Name())
			err = m.SendFile(vDPADevFDName, vdpaDevFile)
			if err != nil {
				return fmt.Errorf("Failed to send %q file descriptor: %w", vDPADevFDName, err)
			}

			reverter.Add(func() { _ = m.CloseFile(vDPADevFDName) })

			queues, err := strconv.Atoi(maxVQP)
			if err != nil {
				return fmt.Errorf("Failed to convert maxVQP (%q) to int: %w", maxVQP, err)
			}

			qemuNetDev := map[string]any{
				"id":      fmt.Sprintf("vhost-%s", vDPADevName),
				"type":    "vhost-vdpa",
				"vhostfd": vDPADevFDName,
				"queues":  queues,
			}

			if slices.Contains([]string{"pcie", "pci"}, busName) {
				qemuDev["driver"] = "virtio-net-pci"
			} else if busName == "ccw" {
				qemuDev["driver"] = "virtio-net-ccw"
			}

			qemuDev["netdev"] = qemuNetDev["id"].(string)
			qemuDev["page-per-vq"] = "on"
			qemuDev["iommu_platform"] = "on"
			qemuDev["disable-legacy"] = "on"

			err = m.AddNIC(qemuNetDev, qemuDev)
			if err != nil {
				return fmt.Errorf("Failed setting up device %q: %w", devName, err)
			}

			reverter.Success()
			return nil
		}
	} else if pciSlotName != "" {
		// Detect physical passthrough device.
		if slices.Contains([]string{"pcie", "pci"}, busName) {
			qemuDev["driver"] = "vfio-pci"
		} else if busName == "ccw" {
			qemuDev["driver"] = "vfio-ccw"
		}

		qemuDev["host"] = pciSlotName

		if d.state.OS.UnprivUser != "" {
			if pciIOMMUGroup == "" {
				return nil, fmt.Errorf("No PCI IOMMU group supplied")
			}

			vfioGroupFile := fmt.Sprintf("/dev/vfio/%s", pciIOMMUGroup)
			err := os.Chown(vfioGroupFile, int(d.state.OS.UnprivUID), -1)
			if err != nil {
				return nil, fmt.Errorf("Failed to chown vfio group device %q: %w", vfioGroupFile, err)
			}

			reverter.Add(func() { _ = os.Chown(vfioGroupFile, 0, -1) })
		}

		monHook = func(m *qmp.Monitor) error {
			err := m.AddNIC(nil, qemuDev)
			if err != nil {
				return fmt.Errorf("Failed setting up device %q: %w", devName, err)
			}

			return nil
		}
	}

	if monHook == nil {
		return nil, fmt.Errorf("Unrecognised device type")
	}

	reverter.Success()
	return monHook, nil
}

// writeNICDevConfig writes the NIC config for the specified device into the NICConfigDir.
// This will be used by the agent to rename the NIC interfaces inside the VM guest.
func (d *qemu) writeNICDevConfig(mtuStr string, devName string, nicName string, devHwaddr string) error {
	// Parse MAC address to ensure it is in a canonical form (avoiding casing/presentation differences).
	hw, err := net.ParseMAC(devHwaddr)
	if err != nil {
		return fmt.Errorf("Failed parsing MAC %q: %w", devHwaddr, err)
	}

	nicConfig := deviceConfig.NICConfig{
		DeviceName: devName,
		NICName:    nicName,
		MACAddress: hw.String(),
	}

	if mtuStr != "" {
		mtuInt, err := strconv.ParseUint(mtuStr, 10, 32)
		if err != nil {
			return fmt.Errorf("Failed parsing MTU: %w", err)
		}

		nicConfig.MTU = uint32(mtuInt)
	}

	nicConfigBytes, err := json.Marshal(nicConfig)
	if err != nil {
		return fmt.Errorf("Failed encoding NIC config: %w", err)
	}

	nicFile := filepath.Join(d.Path(), "config", deviceConfig.NICConfigDir, fmt.Sprintf("%s.json", linux.PathNameEncode(nicConfig.DeviceName)))

	err = os.WriteFile(nicFile, nicConfigBytes, 0700)
	if err != nil {
		return fmt.Errorf("Failed writing NIC config: %w", err)
	}

	return nil
}

// addPCIDevConfig adds the qemu config required for adding a raw PCI device.
func (d *qemu) addPCIDevConfig(cfg *[]cfgSection, bus *qemuBus, pciConfig []deviceConfig.RunConfigItem) error {
	var devName, pciSlotName string
	for _, pciItem := range pciConfig {
		if pciItem.Key == "devName" {
			devName = pciItem.Value
		} else if pciItem.Key == "pciSlotName" {
			pciSlotName = pciItem.Value
		}
	}

	devBus, devAddr, multi := bus.allocate(fmt.Sprintf("incus_%s", devName))
	pciPhysicalOpts := qemuPCIPhysicalOpts{
		dev: qemuDevOpts{
			busName:       bus.name,
			devBus:        devBus,
			devAddr:       devAddr,
			multifunction: multi,
		},
		devName:     devName,
		pciSlotName: pciSlotName,
	}
	*cfg = append(*cfg, qemuPCIPhysical(&pciPhysicalOpts)...)

	return nil
}

// addGPUDevConfig adds the qemu config required for adding a GPU device.
func (d *qemu) addGPUDevConfig(cfg *[]cfgSection, bus *qemuBus, gpuConfig []deviceConfig.RunConfigItem) error {
	var devName, pciSlotName, vgpu string
	for _, gpuItem := range gpuConfig {
		if gpuItem.Key == "devName" {
			devName = gpuItem.Value
		} else if gpuItem.Key == "pciSlotName" {
			pciSlotName = gpuItem.Value
		} else if gpuItem.Key == "vgpu" {
			vgpu = gpuItem.Value
		}
	}

	vgaMode := func() bool {
		// No VGA mode on mdev.
		if vgpu != "" {
			return false
		}

		// No VGA mode on non-x86.
		if d.architecture != osarch.ARCH_64BIT_INTEL_X86 {
			return false
		}

		// Only enable if present on the card.
		if !util.PathExists(filepath.Join("/sys/bus/pci/devices", pciSlotName, "boot_vga")) {
			return false
		}

		// Skip SRIOV VFs as those are shared with the host card.
		if util.PathExists(filepath.Join("/sys/bus/pci/devices", pciSlotName, "physfn")) {
			return false
		}

		return true
	}()

	devBus, devAddr, multi := bus.allocate(fmt.Sprintf("incus_%s", devName))
	gpuDevPhysicalOpts := qemuGPUDevPhysicalOpts{
		dev: qemuDevOpts{
			busName:       bus.name,
			devBus:        devBus,
			devAddr:       devAddr,
			multifunction: multi,
		},
		devName:     devName,
		pciSlotName: pciSlotName,
		vga:         vgaMode,
		vgpu:        vgpu,
	}

	// Add main GPU device in VGA mode to qemu config.
	*cfg = append(*cfg, qemuGPUDevPhysical(&gpuDevPhysicalOpts)...)

	var iommuGroupPath string

	if vgpu != "" {
		iommuGroupPath = filepath.Join("/sys/bus/mdev/devices", vgpu, "iommu_group", "devices")
	} else {
		// Add any other related IOMMU VFs as generic PCI devices.
		iommuGroupPath = filepath.Join("/sys/bus/pci/devices", pciSlotName, "iommu_group", "devices")
	}

	if util.PathExists(iommuGroupPath) {
		// Extract parent slot name by removing any virtual function ID.
		parts := strings.SplitN(pciSlotName, ".", 2)
		prefix := parts[0]

		// Iterate the members of the IOMMU group and override any that match the parent slot name prefix.
		err := filepath.Walk(iommuGroupPath, func(path string, _ os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			iommuSlotName := filepath.Base(path) // Virtual function's address is dir name.

			// Match any VFs that are related to the GPU device (but not the GPU device itself).
			if strings.HasPrefix(iommuSlotName, prefix) && iommuSlotName != pciSlotName {
				// Add VF device without VGA mode to qemu config.
				devBus, devAddr, multi := bus.allocate(fmt.Sprintf("incus_%s", devName))
				gpuDevPhysicalOpts := qemuGPUDevPhysicalOpts{
					dev: qemuDevOpts{
						busName:       bus.name,
						devBus:        devBus,
						devAddr:       devAddr,
						multifunction: multi,
					},
					// Generate associated device name by combining main device name and VF ID.
					devName:     fmt.Sprintf("%s_%s", devName, devAddr),
					pciSlotName: iommuSlotName,
					vga:         false,
					vgpu:        "",
				}

				*cfg = append(*cfg, qemuGPUDevPhysical(&gpuDevPhysicalOpts)...)
			}

			return nil
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *qemu) addUSBDeviceConfig(usbDev deviceConfig.USBDeviceItem) (monitorHook, error) {
	device := map[string]string{
		"id":     fmt.Sprintf("%s%s", qemuDeviceIDPrefix, usbDev.DeviceName),
		"driver": "usb-host",
		"bus":    "qemu_usb.0",
	}

	monHook := func(m *qmp.Monitor) error {
		revert := revert.New()
		defer revert.Fail()

		f, err := os.OpenFile(usbDev.HostDevicePath, unix.O_RDWR, 0)
		if err != nil {
			return fmt.Errorf("Failed to open host device: %w", err)
		}

		defer func() { _ = f.Close() }()

		info, err := m.SendFileWithFDSet(device["id"], f, false)
		if err != nil {
			return fmt.Errorf("Failed to send file descriptor: %w", err)
		}

		revert.Add(func() {
			_ = m.RemoveFDFromFDSet(device["id"])
		})

		device["hostdevice"] = fmt.Sprintf("/dev/fdset/%d", info.ID)

		err = m.AddDevice(device)
		if err != nil {
			return fmt.Errorf("Failed to add device: %w", err)
		}

		revert.Success()
		return nil
	}

	return monHook, nil
}

func (d *qemu) addTPMDeviceConfig(cfg *[]cfgSection, tpmConfig []deviceConfig.RunConfigItem) error {
	var devName, socketPath string

	for _, tpmItem := range tpmConfig {
		if tpmItem.Key == "path" {
			socketPath = tpmItem.Value
		} else if tpmItem.Key == "devName" {
			devName = tpmItem.Value
		}
	}

	tpmOpts := qemuTPMOpts{
		devName: devName,
		path:    socketPath,
	}
	*cfg = append(*cfg, qemuTPM(&tpmOpts)...)

	return nil
}

func (d *qemu) addVmgenDeviceConfig(cfg *[]cfgSection, guid string) error {
	vmgenIDOpts := qemuVmgenIDOpts{
		guid: guid,
	}
	*cfg = append(*cfg, qemuVmgen(&vmgenIDOpts)...)

	return nil
}

// pidFilePath returns the path where the qemu process should write its PID.
func (d *qemu) pidFilePath() string {
	return filepath.Join(d.RunPath(), "qemu.pid")
}

// pid gets the PID of the running qemu process. Returns 0 if PID file or process not found, and -1 if err non-nil.
func (d *qemu) pid() (int, error) {
	pidStr, err := os.ReadFile(d.pidFilePath())
	if os.IsNotExist(err) {
		return 0, nil // PID file has gone.
	}

	if err != nil {
		return -1, err
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidStr)))
	if err != nil {
		return -1, err
	}

	cmdLineProcFilePath := fmt.Sprintf("/proc/%d/cmdline", pid)
	cmdLine, err := os.ReadFile(cmdLineProcFilePath)
	if err != nil {
		return 0, nil // Process has gone.
	}

	qemuSearchString := []byte("qemu-system")
	instUUID := []byte(d.localConfig["volatile.uuid"])
	if !bytes.Contains(cmdLine, qemuSearchString) || !bytes.Contains(cmdLine, instUUID) {
		return -1, fmt.Errorf("PID doesn't match the running process")
	}

	return pid, nil
}

// forceStop kills the QEMU prorcess if running.
func (d *qemu) forceStop() error {
	pid, _ := d.pid()
	if pid > 0 {
		err := d.killQemuProcess(pid)
		if err != nil {
			return fmt.Errorf("Failed to stop VM process %d: %w", pid, err)
		}
	}

	return nil
}

// Stop the VM.
func (d *qemu) Stop(stateful bool) error {
	d.logger.Debug("Stop started", logger.Ctx{"stateful": stateful})
	defer d.logger.Debug("Stop finished", logger.Ctx{"stateful": stateful})

	// Must be run prior to creating the operation lock.
	// Allow to proceed if statusCode is Error or Frozen as we may need to forcefully kill the QEMU process.
	// Also Stop() is called from migrateSendLive in some cases, and instance status will be Frozen then.
	statusCode := d.statusCode()
	if !d.isRunningStatusCode(statusCode) && statusCode != api.Error && statusCode != api.Frozen {
		return ErrInstanceIsStopped
	}

	// Check for stateful.
	if stateful {
		// Confirm the instance has stateful migration enabled.
		if util.IsFalseOrEmpty(d.expandedConfig["migration.stateful"]) {
			return fmt.Errorf("Stateful stop requires migration.stateful to be set to true")
		}

		// Confirm the instance has sufficient reserved state space.
		err := d.checkStateStorage()
		if err != nil {
			return err
		}
	}

	// Setup a new operation.
	// Allow inheriting of ongoing restart or restore operation (we are called from restartCommon and Restore).
	// Don't allow reuse when creating a new stop operation. This prevents other operations from intefering.
	// Allow reuse of a reusable ongoing stop operation as Shutdown() may be called first, which allows reuse
	// of its operations. This allow for Stop() to inherit from Shutdown() where instance is stuck.
	op, err := operationlock.CreateWaitGet(d.Project().Name, d.Name(), operationlock.ActionStop, []operationlock.Action{operationlock.ActionRestart, operationlock.ActionRestore}, false, true)
	if err != nil {
		if errors.Is(err, operationlock.ErrNonReusuableSucceeded) {
			// An existing matching operation has now succeeded, return.
			return nil
		}

		return err
	}

	// Connect to the monitor.
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
	if err != nil {
		d.logger.Warn("Failed connecting to monitor, forcing stop", logger.Ctx{"err": err})

		// If we fail to connect, it's most likely because the VM is already off, but it could also be
		// because the qemu process is not responding, check if process still exists and kill it if needed.
		err = d.forceStop()
		if err != nil {
			op.Done(err)
			return err
		}

		// Wait for QEMU process to exit and perform device cleanup.
		err = d.onStop("stop")
		if err != nil {
			op.Done(err)
			return err
		}

		d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceStopped.Event(d, nil))

		op.Done(nil)
		return nil
	}

	// Handle stateful stop.
	if stateful {
		// Dump the state.
		err = d.saveState(monitor)
		if err != nil {
			op.Done(err)
			return err
		}

		d.stateful = true
		err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.UpdateInstanceStatefulFlag(ctx, d.id, true)
		})
		if err != nil {
			op.Done(err)
			return err
		}
	}

	// Get the wait channel.
	chDisconnect, err := monitor.Wait()
	if err != nil {
		d.logger.Warn("Failed getting monitor disconnection channel, forcing stop", logger.Ctx{"err": err})
		err = d.forceStop()
		if err != nil {
			op.Done(err)
			return err
		}
	} else {
		// Request the VM stop immediately.
		err = monitor.Quit()
		if err != nil {
			d.logger.Warn("Failed sending monitor quit command, forcing stop", logger.Ctx{"err": err})
			err = d.forceStop()
			if err != nil {
				op.Done(err)
				return err
			}
		}

		// Wait for QEMU to exit (can take a while if pending I/O).
		// As this is a forceful stop of the VM we don't wait as long as during a clean shutdown because
		// the QEMU process may be not responding correctly.
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
		defer cancel()

		select {
		case <-chDisconnect:
		case <-ctx.Done():
			d.logger.Warn("Timed out waiting for monitor to disconnect, forcing stop")

			err = d.forceStop()
			if err != nil {
				op.Done(err)
				return err
			}
		}
	}

	// Wait for operation lock to be Done. This is normally completed by onStop which picks up the same
	// operation lock and then marks it as Done after the instance stops and the devices have been cleaned up.
	// However if the operation has failed for another reason we will collect the error here.
	err = op.Wait(context.Background())
	status := d.statusCode()
	if status != api.Stopped {
		errPrefix := fmt.Errorf("Failed stopping instance, status is %q", status)

		if err != nil {
			return fmt.Errorf("%s: %w", errPrefix.Error(), err)
		}

		return errPrefix
	}

	// Now handle errors from stop sequence and return to caller if wasn't completed cleanly.
	if err != nil {
		return err
	}

	return nil
}

// Unfreeze restores the instance to running.
func (d *qemu) Unfreeze() error {
	// Connect to the monitor.
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
	if err != nil {
		return err
	}

	// Send the cont command.
	err = monitor.Start()
	if err != nil {
		return err
	}

	d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceResumed.Event(d, nil))
	return nil
}

// IsPrivileged does not apply to virtual machines. Always returns false.
func (d *qemu) IsPrivileged() bool {
	return false
}

// snapshot creates a snapshot of the instance.
func (d *qemu) snapshot(name string, expiry time.Time, stateful bool) error {
	var err error
	var monitor *qmp.Monitor

	// Deal with state.
	if stateful {
		// Confirm the instance has stateful migration enabled.
		if util.IsFalseOrEmpty(d.expandedConfig["migration.stateful"]) {
			return fmt.Errorf("Stateful snapshot requires migration.stateful to be set to true")
		}

		// Confirm the instance has sufficient reserved state space.
		err = d.checkStateStorage()
		if err != nil {
			return err
		}

		// Quick checks.
		if !d.IsRunning() {
			return fmt.Errorf("Unable to create a stateful snapshot. The instance isn't running")
		}

		// Connect to the monitor.
		monitor, err = qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
		if err != nil {
			return err
		}

		// Dump the state.
		err = d.saveState(monitor)
		if err != nil {
			return err
		}
	}

	// Create the snapshot.
	err = d.snapshotCommon(d, name, expiry, stateful)
	if err != nil {
		return err
	}

	// Resume the VM once the disk state has been saved.
	if stateful {
		// Remove the state from the main volume.
		err = os.Remove(d.StatePath())
		if err != nil {
			return err
		}

		err = monitor.Start()
		if err != nil {
			return err
		}
	}

	return nil
}

// Snapshot takes a new snapshot.
func (d *qemu) Snapshot(name string, expiry time.Time, stateful bool) error {
	unlock, err := d.updateBackupFileLock(context.Background())
	if err != nil {
		return err
	}

	defer unlock()

	return d.snapshot(name, expiry, stateful)
}

// Restore restores an instance snapshot.
func (d *qemu) Restore(source instance.Instance, stateful bool) error {
	op, err := operationlock.Create(d.Project().Name, d.Name(), operationlock.ActionRestore, false, false)
	if err != nil {
		return fmt.Errorf("Failed to create instance restore operation: %w", err)
	}

	defer op.Done(nil)

	var ctxMap logger.Ctx

	// Stop the instance.
	wasRunning := false
	if d.IsRunning() {
		wasRunning = true

		ephemeral := d.IsEphemeral()
		if ephemeral {
			// Unset ephemeral flag.
			args := db.InstanceArgs{
				Architecture: d.Architecture(),
				Config:       d.LocalConfig(),
				Description:  d.Description(),
				Devices:      d.LocalDevices(),
				Ephemeral:    false,
				Profiles:     d.Profiles(),
				Project:      d.Project().Name,
				Type:         d.Type(),
				Snapshot:     d.IsSnapshot(),
			}

			err := d.Update(args, false)
			if err != nil {
				op.Done(err)
				return err
			}

			// On function return, set the flag back on.
			defer func() {
				args.Ephemeral = ephemeral
				_ = d.Update(args, false)
			}()
		}

		// This will unmount the instance storage.
		err := d.Stop(false)
		if err != nil {
			op.Done(err)
			return err
		}

		// Refresh the operation as that one is now complete.
		op, err = operationlock.Create(d.Project().Name, d.Name(), operationlock.ActionRestore, false, false)
		if err != nil {
			return fmt.Errorf("Failed to create instance restore operation: %w", err)
		}

		defer op.Done(nil)
	}

	ctxMap = logger.Ctx{
		"created":   d.creationDate,
		"ephemeral": d.ephemeral,
		"used":      d.lastUsedDate,
		"source":    source.Name()}

	d.logger.Info("Restoring instance", ctxMap)

	// Load the storage driver.
	pool, err := storagePools.LoadByInstance(d.state, d)
	if err != nil {
		op.Done(err)
		return err
	}

	// Restore the rootfs.
	err = pool.RestoreInstanceSnapshot(d, source, nil)
	if err != nil {
		op.Done(err)
		return err
	}

	// Restore the configuration.
	args := db.InstanceArgs{
		Architecture: source.Architecture(),
		Config:       source.LocalConfig(),
		Description:  source.Description(),
		Devices:      source.LocalDevices(),
		Ephemeral:    source.IsEphemeral(),
		Profiles:     source.Profiles(),
		Project:      source.Project().Name,
		Type:         source.Type(),
		Snapshot:     source.IsSnapshot(),
	}

	// Don't pass as user-requested as there's no way to fix a bad config.
	// This will call d.UpdateBackupFile() to ensure snapshot list is up to date.
	err = d.Update(args, false)
	if err != nil {
		op.Done(err)
		return err
	}

	d.stateful = stateful

	// Restart the instance.
	if wasRunning || stateful {
		d.logger.Debug("Starting instance after snapshot restore")
		err := d.Start(stateful)
		if err != nil {
			op.Done(err)
			return err
		}
	}

	d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceRestored.Event(d, map[string]any{"snapshot": source.Name()}))
	d.logger.Info("Restored instance", ctxMap)
	return nil
}

// Rename the instance. Accepts an argument to enable applying deferred TemplateTriggerRename.
func (d *qemu) Rename(newName string, applyTemplateTrigger bool) error {
	unlock, err := d.updateBackupFileLock(context.Background())
	if err != nil {
		return err
	}

	defer unlock()

	oldName := d.Name()
	ctxMap := logger.Ctx{
		"created":   d.creationDate,
		"ephemeral": d.ephemeral,
		"used":      d.lastUsedDate,
		"newname":   newName}

	d.logger.Info("Renaming instance", ctxMap)

	// Quick checks.
	err = instance.ValidName(newName, d.IsSnapshot())
	if err != nil {
		return err
	}

	if d.IsRunning() {
		return fmt.Errorf("Renaming of running instance not allowed")
	}

	// Clean things up.
	d.cleanup()

	pool, err := storagePools.LoadByInstance(d.state, d)
	if err != nil {
		return fmt.Errorf("Failed loading instance storage pool: %w", err)
	}

	if d.IsSnapshot() {
		_, newSnapName, _ := api.GetParentAndSnapshotName(newName)
		err = pool.RenameInstanceSnapshot(d, newSnapName, nil)
		if err != nil {
			return fmt.Errorf("Rename instance snapshot: %w", err)
		}
	} else {
		err = pool.RenameInstance(d, newName, nil)
		if err != nil {
			return fmt.Errorf("Rename instance: %w", err)
		}

		if applyTemplateTrigger {
			err = d.DeferTemplateApply(instance.TemplateTriggerRename)
			if err != nil {
				return err
			}
		}
	}

	if !d.IsSnapshot() {
		var results []string

		err := d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			var err error

			// Rename all the instance snapshot database entries.
			results, err = tx.GetInstanceSnapshotsNames(ctx, d.project.Name, oldName)
			if err != nil {
				d.logger.Error("Failed to get instance snapshots", ctxMap)
				return fmt.Errorf("Failed to get instance snapshots: Failed getting instance snapshot names: %w", err)
			}

			for _, sname := range results {
				// Rename the snapshot.
				oldSnapName := strings.SplitN(sname, internalInstance.SnapshotDelimiter, 2)[1]
				baseSnapName := filepath.Base(sname)

				err := dbCluster.RenameInstanceSnapshot(ctx, tx.Tx(), d.project.Name, oldName, oldSnapName, baseSnapName)
				if err != nil {
					d.logger.Error("Failed renaming snapshot", ctxMap)
					return err
				}
			}

			return nil
		})
		if err != nil {
			return err
		}
	}

	// Rename the instance database entry.
	err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		if d.IsSnapshot() {
			oldParts := strings.SplitN(oldName, internalInstance.SnapshotDelimiter, 2)
			newParts := strings.SplitN(newName, internalInstance.SnapshotDelimiter, 2)
			return dbCluster.RenameInstanceSnapshot(ctx, tx.Tx(), d.project.Name, oldParts[0], oldParts[1], newParts[1])
		}

		return dbCluster.RenameInstance(ctx, tx.Tx(), d.project.Name, oldName, newName)
	})
	if err != nil {
		d.logger.Error("Failed renaming instance", ctxMap)
		return err
	}

	// Rename the logging path.
	newFullName := project.Instance(d.Project().Name, d.Name())
	_ = os.RemoveAll(internalUtil.LogPath(newFullName))
	if util.PathExists(d.LogPath()) {
		err := os.Rename(d.LogPath(), internalUtil.LogPath(newFullName))
		if err != nil {
			d.logger.Error("Failed renaming instance", ctxMap)
			return err
		}
	}

	// Rename the runtime path.
	newFullName = project.Instance(d.Project().Name, d.Name())
	_ = os.RemoveAll(internalUtil.RunPath(newFullName))
	if util.PathExists(d.RunPath()) {
		err := os.Rename(d.RunPath(), internalUtil.RunPath(newFullName))
		if err != nil {
			d.logger.Error("Failed renaming instance", ctxMap)
			return err
		}
	}

	revert := revert.New()
	defer revert.Fail()

	// Set the new name in the struct.
	d.name = newName
	revert.Add(func() { d.name = oldName })

	// Rename the backups.
	backups, err := d.Backups()
	if err != nil {
		return err
	}

	for _, backup := range backups {
		b := backup
		oldName := b.Name()
		backupName := strings.Split(oldName, "/")[1]
		newName := fmt.Sprintf("%s/%s", newName, backupName)

		err = b.Rename(newName)
		if err != nil {
			return err
		}

		revert.Add(func() { _ = b.Rename(oldName) })
	}

	// Update lease files.
	err = network.UpdateDNSMasqStatic(d.state, "")
	if err != nil {
		return err
	}

	// Reset cloud-init instance-id (causes a re-run on name changes).
	if !d.IsSnapshot() {
		err = d.resetInstanceID()
		if err != nil {
			return err
		}
	}

	// Update the backup file.
	err = d.UpdateBackupFile()
	if err != nil {
		return err
	}

	d.logger.Info("Renamed instance", ctxMap)

	if d.isSnapshot {
		d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceSnapshotRenamed.Event(d, map[string]any{"old_name": oldName}))
	} else {
		err = d.state.Authorizer.RenameInstance(d.state.ShutdownCtx, d.project.Name, oldName, newName)
		if err != nil {
			logger.Error("Failed to rename instance in authorizer", logger.Ctx{"old_name": oldName, "new_name": newName, "project": d.project.Name, "error": err})
		}

		d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceRenamed.Event(d, map[string]any{"old_name": oldName}))
	}

	revert.Success()
	return nil
}

// Update the instance config.
func (d *qemu) Update(args db.InstanceArgs, userRequested bool) error {
	unlock, err := d.updateBackupFileLock(context.Background())
	if err != nil {
		return err
	}

	defer unlock()

	// Setup a new operation.
	op, err := operationlock.CreateWaitGet(d.Project().Name, d.Name(), operationlock.ActionUpdate, []operationlock.Action{operationlock.ActionRestart, operationlock.ActionRestore}, false, false)
	if err != nil {
		return fmt.Errorf("Failed to create instance update operation: %w", err)
	}

	defer op.Done(nil)

	// Setup the reverter.
	revert := revert.New()
	defer revert.Fail()

	// Set sane defaults for unset keys.
	if args.Project == "" {
		args.Project = api.ProjectDefaultName
	}

	if args.Architecture == 0 {
		args.Architecture = d.architecture
	}

	if args.Config == nil {
		args.Config = map[string]string{}
	}

	if args.Devices == nil {
		args.Devices = deviceConfig.Devices{}
	}

	if args.Profiles == nil {
		args.Profiles = []api.Profile{}
	}

	if userRequested {
		// Validate the new config.
		err := instance.ValidConfig(d.state.OS, args.Config, false, d.dbType)
		if err != nil {
			return fmt.Errorf("Invalid config: %w", err)
		}

		// Validate the new devices without using expanded devices validation (expensive checks disabled).
		err = instance.ValidDevices(d.state, d.project, d.Type(), args.Devices, nil)
		if err != nil {
			return fmt.Errorf("Invalid devices: %w", err)
		}
	}

	var profiles []string

	err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Validate the new profiles.
		profiles, err = tx.GetProfileNames(ctx, args.Project)

		return err
	})
	if err != nil {
		return fmt.Errorf("Failed to get profiles: %w", err)
	}

	checkedProfiles := []string{}
	for _, profile := range args.Profiles {
		if !slices.Contains(profiles, profile.Name) {
			return fmt.Errorf("Requested profile '%s' doesn't exist", profile.Name)
		}

		if slices.Contains(checkedProfiles, profile.Name) {
			return fmt.Errorf("Duplicate profile found in request")
		}

		checkedProfiles = append(checkedProfiles, profile.Name)
	}

	// Validate the new architecture.
	if args.Architecture != 0 {
		_, err = osarch.ArchitectureName(args.Architecture)
		if err != nil {
			return fmt.Errorf("Invalid architecture ID: %s", err)
		}
	}

	// Get a copy of the old configuration.
	oldDescription := d.Description()
	oldArchitecture := 0
	err = util.DeepCopy(&d.architecture, &oldArchitecture)
	if err != nil {
		return err
	}

	oldEphemeral := false
	err = util.DeepCopy(&d.ephemeral, &oldEphemeral)
	if err != nil {
		return err
	}

	oldExpandedDevices := deviceConfig.Devices{}
	err = util.DeepCopy(&d.expandedDevices, &oldExpandedDevices)
	if err != nil {
		return err
	}

	oldExpandedConfig := map[string]string{}
	err = util.DeepCopy(&d.expandedConfig, &oldExpandedConfig)
	if err != nil {
		return err
	}

	oldLocalDevices := deviceConfig.Devices{}
	err = util.DeepCopy(&d.localDevices, &oldLocalDevices)
	if err != nil {
		return err
	}

	oldLocalConfig := map[string]string{}
	err = util.DeepCopy(&d.localConfig, &oldLocalConfig)
	if err != nil {
		return err
	}

	oldProfiles := []api.Profile{}
	err = util.DeepCopy(&d.profiles, &oldProfiles)
	if err != nil {
		return err
	}

	oldExpiryDate := d.expiryDate

	// Revert local changes if update fails.
	revert.Add(func() {
		d.description = oldDescription
		d.architecture = oldArchitecture
		d.ephemeral = oldEphemeral
		d.expandedConfig = oldExpandedConfig
		d.expandedDevices = oldExpandedDevices
		d.localConfig = oldLocalConfig
		d.localDevices = oldLocalDevices
		d.profiles = oldProfiles
		d.expiryDate = oldExpiryDate
	})

	// Apply the various changes to local vars.
	d.description = args.Description
	d.architecture = args.Architecture
	d.ephemeral = args.Ephemeral
	d.localConfig = args.Config
	d.localDevices = args.Devices
	d.profiles = args.Profiles
	d.expiryDate = args.ExpiryDate

	// Expand the config.
	err = d.expandConfig()
	if err != nil {
		return err
	}

	// Diff the configurations.
	changedConfig := []string{}
	for key := range oldExpandedConfig {
		if oldExpandedConfig[key] != d.expandedConfig[key] {
			if !slices.Contains(changedConfig, key) {
				changedConfig = append(changedConfig, key)
			}
		}
	}

	for key := range d.expandedConfig {
		if oldExpandedConfig[key] != d.expandedConfig[key] {
			if !slices.Contains(changedConfig, key) {
				changedConfig = append(changedConfig, key)
			}
		}
	}

	// Diff the devices.
	removeDevices, addDevices, updateDevices, allUpdatedKeys := oldExpandedDevices.Update(d.expandedDevices, func(oldDevice deviceConfig.Device, newDevice deviceConfig.Device) []string {
		// This function needs to return a list of fields that are excluded from differences
		// between oldDevice and newDevice. The result of this is that as long as the
		// devices are otherwise identical except for the fields returned here, then the
		// device is considered to be being "updated" rather than "added & removed".
		oldDevType, err := device.LoadByType(d.state, d.Project().Name, oldDevice)
		if err != nil {
			return []string{} // Couldn't create Device, so this cannot be an update.
		}

		newDevType, err := device.LoadByType(d.state, d.Project().Name, newDevice)
		if err != nil {
			return []string{} // Couldn't create Device, so this cannot be an update.
		}

		return newDevType.UpdatableFields(oldDevType)
	})

	// Prevent adding or updating device initial configuration.
	if util.StringPrefixInSlice("initial.", allUpdatedKeys) {
		for devName, newDev := range addDevices {
			for k, newVal := range newDev {
				if !strings.HasPrefix(k, "initial.") {
					continue
				}

				oldDev, ok := removeDevices[devName]
				if !ok {
					return fmt.Errorf("New device with initial configuration cannot be added once the instance is created")
				}

				oldVal, ok := oldDev[k]
				if !ok {
					return fmt.Errorf("Device initial configuration cannot be added once the instance is created")
				}

				// If newVal is an empty string it means the initial configuration
				// has been removed.
				if newVal != "" && newVal != oldVal {
					return fmt.Errorf("Device initial configuration cannot be modified once the instance is created")
				}
			}
		}
	}

	if userRequested {
		// Do some validation of the config diff (allows mixed instance types for profiles).
		err = instance.ValidConfig(d.state.OS, d.expandedConfig, true, instancetype.Any)
		if err != nil {
			return fmt.Errorf("Invalid expanded config: %w", err)
		}

		// Do full expanded validation of the devices diff.
		err = instance.ValidDevices(d.state, d.project, d.Type(), d.localDevices, d.expandedDevices)
		if err != nil {
			return fmt.Errorf("Invalid expanded devices: %w", err)
		}

		// Validate root device
		_, oldRootDev, oldErr := internalInstance.GetRootDiskDevice(oldExpandedDevices.CloneNative())
		_, newRootDev, newErr := internalInstance.GetRootDiskDevice(d.expandedDevices.CloneNative())
		if oldErr == nil && newErr == nil && oldRootDev["pool"] != newRootDev["pool"] {
			return fmt.Errorf("Cannot update root disk device pool name to %q", newRootDev["pool"])
		}

		// Ensure the instance has a root disk.
		if newErr != nil {
			return fmt.Errorf("Invalid root disk device: %w", newErr)
		}
	}

	// If apparmor changed, re-validate the apparmor profile (even if not running).
	if slices.Contains(changedConfig, "raw.apparmor") {
		qemuPath, _, err := d.qemuArchConfig(d.architecture)
		if err != nil {
			return err
		}

		err = apparmor.InstanceValidate(d.state.OS, d, []string{qemuPath})
		if err != nil {
			return fmt.Errorf("Parse AppArmor profile: %w", err)
		}
	}

	isRunning := d.IsRunning()

	// Use the device interface to apply update changes.
	err = d.devicesUpdate(d, removeDevices, addDevices, updateDevices, oldExpandedDevices, isRunning, userRequested)
	if err != nil {
		return err
	}

	if isRunning {
		// Only certain keys can be changed on a running VM.
		liveUpdateKeys := []string{
			"cluster.evacuate",
			"limits.memory",
			"security.agent.metrics",
			"security.csm",
			"security.protection.delete",
			"security.guestapi",
			"security.secureboot",
		}

		liveUpdateKeyPrefixes := []string{
			"boot.",
			"cloud-init.",
			"environment.",
			"image.",
			"snapshots.",
			"user.",
			"volatile.",
		}

		isLiveUpdatable := func(key string) bool {
			// Skip container config keys for VMs
			_, ok := internalInstance.InstanceConfigKeysContainer[key]
			if ok {
				return true
			}

			if key == "limits.cpu" {
				return d.architectureSupportsCPUHotplug()
			}

			if slices.Contains(liveUpdateKeys, key) {
				return true
			}

			if util.StringHasPrefix(key, liveUpdateKeyPrefixes...) {
				return true
			}

			return false
		}

		// Check only keys that support live update have changed.
		for _, key := range changedConfig {
			if !isLiveUpdatable(key) {
				return fmt.Errorf("Key %q cannot be updated when VM is running", key)
			}
		}

		// Apply live update for each key.
		for _, key := range changedConfig {
			value := d.expandedConfig[key]

			if key == "limits.cpu" {
				oldValue := oldExpandedConfig["limits.cpu"]

				if oldValue != "" {
					_, err := strconv.Atoi(oldValue)
					if err != nil {
						return fmt.Errorf("Cannot update key %q when using CPU pinning and the VM is running", key)
					}
				}

				// If the key is being unset, set it to default value.
				if value == "" {
					value = "1"
				}

				limit, err := strconv.Atoi(value)
				if err != nil {
					return fmt.Errorf("Cannot change CPU pinning when VM is running")
				}

				// Hotplug the CPUs.
				err = d.setCPUs(limit)
				if err != nil {
					return fmt.Errorf("Failed updating cpu limit: %w", err)
				}
			} else if key == "limits.memory" {
				err = d.updateMemoryLimit(value)
				if err != nil {
					if err != nil {
						return fmt.Errorf("Failed updating memory limit: %w", err)
					}
				}
			} else if key == "security.csm" {
				// Defer rebuilding nvram until next start.
				d.localConfig["volatile.apply_nvram"] = "true"
			} else if key == "security.secureboot" {
				// Defer rebuilding nvram until next start.
				d.localConfig["volatile.apply_nvram"] = "true"
			} else if key == "security.guestapi" {
				err = d.advertiseVsockAddress()
				if err != nil {
					return err
				}
			}
		}
	}

	if d.architectureSupportsUEFI(d.architecture) && (slices.Contains(changedConfig, "security.secureboot") || slices.Contains(changedConfig, "security.csm")) {
		// setupNvram() requires instance's config volume to be mounted.
		// The easiest way to detect that is to check if instance is running.
		// TODO: extend storage API to be able to check if volume is already mounted?
		if !isRunning {
			// Mount the instance's config volume.
			_, err := d.mount()
			if err != nil {
				return err
			}

			defer func() { _ = d.unmount() }()
		}

		// Re-generate the NVRAM.
		err = d.setupNvram()
		if err != nil {
			return err
		}
	}

	// Re-generate the instance-id if needed.
	if !d.IsSnapshot() && d.needsNewInstanceID(changedConfig, oldExpandedDevices) {
		err = d.resetInstanceID()
		if err != nil {
			return err
		}
	}

	// Finally, apply the changes to the database.
	err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Snapshots should update only their descriptions and expiry date.
		if d.IsSnapshot() {
			return tx.UpdateInstanceSnapshot(d.id, d.description, d.expiryDate)
		}

		object, err := dbCluster.GetInstance(ctx, tx.Tx(), d.project.Name, d.name)
		if err != nil {
			return err
		}

		object.Description = d.description
		object.Architecture = d.architecture
		object.Ephemeral = d.ephemeral
		object.ExpiryDate = sql.NullTime{Time: d.expiryDate, Valid: true}

		err = dbCluster.UpdateInstance(ctx, tx.Tx(), d.project.Name, d.name, *object)
		if err != nil {
			return err
		}

		err = dbCluster.UpdateInstanceConfig(ctx, tx.Tx(), int64(object.ID), d.localConfig)
		if err != nil {
			return err
		}

		devices, err := dbCluster.APIToDevices(d.localDevices.CloneNative())
		if err != nil {
			return err
		}

		err = dbCluster.UpdateInstanceDevices(ctx, tx.Tx(), int64(object.ID), devices)
		if err != nil {
			return err
		}

		profileNames := make([]string, 0, len(d.profiles))
		for _, profile := range d.profiles {
			profileNames = append(profileNames, profile.Name)
		}

		return dbCluster.UpdateInstanceProfiles(ctx, tx.Tx(), object.ID, object.Project, profileNames)
	})
	if err != nil {
		return fmt.Errorf("Failed to update database: %w", err)
	}

	err = d.UpdateBackupFile()
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Failed to write backup file: %w", err)
	}

	// Changes have been applied and recorded, do not revert if an error occurs from here.
	revert.Success()

	if isRunning {
		// Send devIncus notifications only for user.* key changes
		for _, key := range changedConfig {
			if !strings.HasPrefix(key, "user.") {
				continue
			}

			msg := map[string]any{
				"key":       key,
				"old_value": oldExpandedConfig[key],
				"value":     d.expandedConfig[key],
			}

			err = d.devIncusEventSend("config", msg)
			if err != nil {
				return err
			}
		}

		// Device changes
		for k, m := range removeDevices {
			msg := map[string]any{
				"action": "removed",
				"name":   k,
				"config": m,
			}

			err = d.devIncusEventSend("device", msg)
			if err != nil {
				return err
			}
		}

		for k, m := range updateDevices {
			msg := map[string]any{
				"action": "updated",
				"name":   k,
				"config": m,
			}

			err = d.devIncusEventSend("device", msg)
			if err != nil {
				return err
			}
		}

		for k, m := range addDevices {
			msg := map[string]any{
				"action": "added",
				"name":   k,
				"config": m,
			}

			err = d.devIncusEventSend("device", msg)
			if err != nil {
				return err
			}
		}
	}

	if userRequested {
		if d.isSnapshot {
			d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceSnapshotUpdated.Event(d, nil))
		} else {
			d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceUpdated.Event(d, nil))
		}
	}

	return nil
}

// updateMemoryLimit live updates the VM's memory limit by reszing the balloon device.
func (d *qemu) updateMemoryLimit(newLimit string) error {
	if newLimit == "" {
		return nil
	}

	if util.IsTrue(d.expandedConfig["limits.memory.hugepages"]) {
		return fmt.Errorf("Cannot live update memory limit when using huge pages")
	}

	// Check new size string is valid and convert to bytes.
	newSizeBytes, err := units.ParseByteSizeString(newLimit)
	if err != nil {
		return fmt.Errorf("Invalid memory size: %w", err)
	}

	newSizeMB := newSizeBytes / 1024 / 1024

	// Connect to the monitor.
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
	if err != nil {
		return err // The VM isn't running as no monitor socket available.
	}

	baseSizeBytes, err := monitor.GetMemorySizeBytes()
	if err != nil {
		return err
	}

	baseSizeMB := baseSizeBytes / 1024 / 1024

	curSizeBytes, err := monitor.GetMemoryBalloonSizeBytes()
	if err != nil {
		return err
	}

	curSizeMB := curSizeBytes / 1024 / 1024

	if curSizeMB == newSizeMB {
		return nil
	} else if baseSizeMB < newSizeMB {
		return fmt.Errorf("Cannot increase memory size beyond boot time size when VM is running (Boot time size %dMiB, new size %dMiB)", baseSizeMB, newSizeMB)
	}

	// Set effective memory size.
	err = monitor.SetMemoryBalloonSizeBytes(newSizeBytes)
	if err != nil {
		return err
	}

	// Changing the memory balloon can take time, so poll the effectice size to check it has shrunk within 1%
	// of the target size, which we then take as success (it may still continue to shrink closer to target).
	for i := 0; i < 10; i++ {
		curSizeBytes, err = monitor.GetMemoryBalloonSizeBytes()
		if err != nil {
			return err
		}

		curSizeMB = curSizeBytes / 1024 / 1024

		var diff int64
		if curSizeMB < newSizeMB {
			diff = newSizeMB - curSizeMB
		} else {
			diff = curSizeMB - newSizeMB
		}

		if diff <= (newSizeMB / 100) {
			return nil // We reached to within 1% of our target size.
		}

		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("Failed setting memory to %dMiB (currently %dMiB) as it was taking too long", newSizeMB, curSizeMB)
}

func (d *qemu) removeUnixDevices() error {
	// Check that we indeed have devices to remove.
	if !util.PathExists(d.DevicesPath()) {
		return nil
	}

	// Load the directory listing.
	dents, err := os.ReadDir(d.DevicesPath())
	if err != nil {
		return err
	}

	for _, f := range dents {
		// Skip non-Unix devices.
		if !strings.HasPrefix(f.Name(), "forkmknod.unix.") && !strings.HasPrefix(f.Name(), "unix.") && !strings.HasPrefix(f.Name(), "infiniband.unix.") {
			continue
		}

		// Remove the entry
		devicePath := filepath.Join(d.DevicesPath(), f.Name())
		err := os.Remove(devicePath)
		if err != nil {
			d.logger.Error("Failed removing unix device", logger.Ctx{"err": err, "path": devicePath})
		}
	}

	return nil
}

func (d *qemu) removeDiskDevices() error {
	// Check that we indeed have devices to remove.
	if !util.PathExists(d.DevicesPath()) {
		return nil
	}

	// Load the directory listing.
	dents, err := os.ReadDir(d.DevicesPath())
	if err != nil {
		return err
	}

	for _, f := range dents {
		// Skip non-disk devices
		if !strings.HasPrefix(f.Name(), "disk.") {
			continue
		}

		// Always try to unmount the host side.
		_ = unix.Unmount(filepath.Join(d.DevicesPath(), f.Name()), unix.MNT_DETACH)

		// Remove the entry.
		diskPath := filepath.Join(d.DevicesPath(), f.Name())
		err := os.Remove(diskPath)
		if err != nil {
			d.logger.Error("Failed to remove disk device path", logger.Ctx{"err": err, "path": diskPath})
		}
	}

	return nil
}

func (d *qemu) cleanup() {
	// Unmount any leftovers
	_ = d.removeUnixDevices()
	_ = d.removeDiskDevices()

	// Remove the security profiles
	_ = apparmor.InstanceDelete(d.state.OS, d)

	// Remove the devices path
	_ = os.Remove(d.DevicesPath())

	// Remove the shmounts path
	_ = os.RemoveAll(d.ShmountsPath())
}

// cleanupDevices performs any needed device cleanup steps when instance is stopped.
// Must be called before root volume is unmounted.
func (d *qemu) cleanupDevices() {
	// Clear up the config drive mount.
	err := d.configDriveMountPathClear()
	if err != nil {
		d.logger.Warn("Failed cleaning up config drive mount", logger.Ctx{"err": err})
	}

	for _, entry := range d.expandedDevices.Reversed() {
		dev, err := d.deviceLoad(d, entry.Name, entry.Config)
		if err != nil {
			if errors.Is(err, device.ErrUnsupportedDevType) {
				continue // Skip unsupported device (allows for mixed instance type profiles).
			}

			// Just log an error, but still allow the device to be stopped if usable device returned.
			d.logger.Error("Failed stop validation for device", logger.Ctx{"device": entry.Name, "err": err})
		}

		// If a usable device was returned from deviceLoad try to stop anyway, even if validation fails.
		// This allows for the scenario where a new version has additional validation restrictions
		// than older versions and we still need to allow previously valid devices to be stopped even if
		// they are no longer considered valid.
		if dev != nil {
			err = d.deviceStop(dev, false, "")
			if err != nil {
				d.logger.Error("Failed to stop device", logger.Ctx{"device": dev.Name(), "err": err})
			}
		}
	}
}

func (d *qemu) init() error {
	// Compute the expanded config and device list.
	err := d.expandConfig()
	if err != nil {
		return err
	}

	return nil
}

// Delete the instance.
func (d *qemu) Delete(force bool) error {
	unlock, err := d.updateBackupFileLock(context.Background())
	if err != nil {
		return err
	}

	defer unlock()

	// Setup a new operation.
	op, err := operationlock.CreateWaitGet(d.Project().Name, d.Name(), operationlock.ActionDelete, nil, false, false)
	if err != nil {
		return fmt.Errorf("Failed to create instance delete operation: %w", err)
	}

	defer op.Done(nil)

	if d.IsRunning() {
		return api.StatusErrorf(http.StatusBadRequest, "Instance is running")
	}

	err = d.delete(force)
	if err != nil {
		return err
	}

	// If dealing with a snapshot, refresh the backup file on the parent.
	if d.IsSnapshot() {
		parentName, _, _ := api.GetParentAndSnapshotName(d.name)

		// Load the parent.
		parent, err := instance.LoadByProjectAndName(d.state, d.project.Name, parentName)
		if err != nil {
			return fmt.Errorf("Invalid parent: %w", err)
		}

		// Update the backup file.
		err = parent.UpdateBackupFile()
		if err != nil {
			return err
		}
	}

	return nil
}

// Delete the instance without creating an operation lock.
func (d *qemu) delete(force bool) error {
	ctxMap := logger.Ctx{
		"created":   d.creationDate,
		"ephemeral": d.ephemeral,
		"used":      d.lastUsedDate}

	if d.isSnapshot {
		d.logger.Info("Deleting instance snapshot", ctxMap)
	} else {
		d.logger.Info("Deleting instance", ctxMap)
	}

	// Check if instance is delete protected.
	if !force && util.IsTrue(d.expandedConfig["security.protection.delete"]) && !d.IsSnapshot() {
		return fmt.Errorf("Instance is protected")
	}

	// Delete any persistent warnings for instance.
	err := d.warningsDelete()
	if err != nil {
		return err
	}

	// Attempt to initialize storage interface for the instance.
	pool, err := d.getStoragePool()
	if err != nil && !response.IsNotFoundError(err) {
		return err
	} else if pool != nil {
		if d.IsSnapshot() {
			// Remove snapshot volume and database record.
			err = pool.DeleteInstanceSnapshot(d, nil)
			if err != nil {
				return err
			}
		} else {
			// Remove all snapshots.
			err := d.deleteSnapshots(func(snapInst instance.Instance) error {
				return snapInst.(*qemu).delete(true) // Internal delete function that doesn't lock.
			})
			if err != nil {
				return fmt.Errorf("Failed deleting instance snapshots: %w", err)
			}

			// Remove the storage volume and database records.
			err = pool.DeleteInstance(d, nil)
			if err != nil {
				return err
			}
		}
	}

	// Perform other cleanup steps if not snapshot.
	if !d.IsSnapshot() {
		// Remove all backups.
		backups, err := d.Backups()
		if err != nil {
			return err
		}

		for _, backup := range backups {
			err = backup.Delete()
			if err != nil {
				return err
			}
		}

		// Run device removal function for each device.
		d.devicesRemove(d)

		// Clean things up.
		d.cleanup()
	}

	err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Remove the database record of the instance or snapshot instance.
		return tx.DeleteInstance(ctx, d.Project().Name, d.Name())
	})
	if err != nil {
		d.logger.Error("Failed deleting instance entry", logger.Ctx{"project": d.Project().Name})
		return err
	}

	if d.isSnapshot {
		d.logger.Info("Deleted instance snapshot", ctxMap)
	} else {
		d.logger.Info("Deleted instance", ctxMap)
	}

	if d.isSnapshot {
		d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceSnapshotDeleted.Event(d, nil))
	} else {
		err = d.state.Authorizer.DeleteInstance(d.state.ShutdownCtx, d.project.Name, d.Name())
		if err != nil {
			logger.Error("Failed to remove instance from authorizer", logger.Ctx{"name": d.Name(), "project": d.project.Name, "error": err})
		}

		d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceDeleted.Event(d, nil))
	}

	return nil
}

// Export publishes the instance.
func (d *qemu) Export(w io.Writer, properties map[string]string, expiration time.Time) (api.ImageMetadata, error) {
	ctxMap := logger.Ctx{
		"created":   d.creationDate,
		"ephemeral": d.ephemeral,
		"used":      d.lastUsedDate}

	meta := api.ImageMetadata{}

	if d.IsRunning() {
		return meta, fmt.Errorf("Cannot export a running instance as an image")
	}

	d.logger.Info("Exporting instance", ctxMap)

	// Start the storage.
	mountInfo, err := d.mount()
	if err != nil {
		d.logger.Error("Failed exporting instance", ctxMap)
		return meta, err
	}

	defer func() { _ = d.unmount() }()

	// Create the tarball.
	tarWriter := instancewriter.NewInstanceTarWriter(w, nil)

	// Path inside the tar image is the pathname starting after cDir.
	cDir := d.Path()
	offset := len(cDir) + 1

	writeToTar := func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		err = tarWriter.WriteFile(path[offset:], path, fi, false)
		if err != nil {
			d.logger.Debug("Error tarring up", logger.Ctx{"path": path, "err": err})
			return err
		}

		return nil
	}

	// Look for metadata.yaml.
	fnam := filepath.Join(cDir, "metadata.yaml")
	if !util.PathExists(fnam) {
		// Generate a new metadata.yaml.
		tempDir, err := os.MkdirTemp("", "incus_metadata_")
		if err != nil {
			_ = tarWriter.Close()
			d.logger.Error("Failed exporting instance", ctxMap)
			return meta, err
		}

		defer func() { _ = os.RemoveAll(tempDir) }()

		// Get the instance's architecture.
		var arch string
		if d.IsSnapshot() {
			parentName, _, _ := api.GetParentAndSnapshotName(d.name)
			parent, err := instance.LoadByProjectAndName(d.state, d.project.Name, parentName)
			if err != nil {
				_ = tarWriter.Close()
				d.logger.Error("Failed exporting instance", ctxMap)
				return meta, err
			}

			arch, _ = osarch.ArchitectureName(parent.Architecture())
		} else {
			arch, _ = osarch.ArchitectureName(d.architecture)
		}

		if arch == "" {
			arch, err = osarch.ArchitectureName(d.state.OS.Architectures[0])
			if err != nil {
				d.logger.Error("Failed exporting instance", ctxMap)
				return meta, err
			}
		}

		// Fill in the metadata.
		meta.Architecture = arch
		meta.CreationDate = time.Now().UTC().Unix()
		meta.Properties = properties
		if !expiration.IsZero() {
			meta.ExpiryDate = expiration.UTC().Unix()
		}

		data, err := yaml.Marshal(&meta)
		if err != nil {
			_ = tarWriter.Close()
			d.logger.Error("Failed exporting instance", ctxMap)
			return meta, err
		}

		// Write the actual file.
		fnam = filepath.Join(tempDir, "metadata.yaml")
		err = os.WriteFile(fnam, data, 0644)
		if err != nil {
			_ = tarWriter.Close()
			d.logger.Error("Failed exporting instance", ctxMap)
			return meta, err
		}

		fi, err := os.Lstat(fnam)
		if err != nil {
			_ = tarWriter.Close()
			d.logger.Error("Failed exporting instance", ctxMap)
			return meta, err
		}

		tmpOffset := len(filepath.Dir(fnam)) + 1
		err = tarWriter.WriteFile(fnam[tmpOffset:], fnam, fi, false)
		if err != nil {
			_ = tarWriter.Close()
			d.logger.Error("Failed exporting instance", ctxMap)
			return meta, err
		}
	} else {
		// Parse the metadata.
		content, err := os.ReadFile(fnam)
		if err != nil {
			_ = tarWriter.Close()
			d.logger.Error("Failed exporting instance", ctxMap)
			return meta, err
		}

		err = yaml.Unmarshal(content, &meta)
		if err != nil {
			_ = tarWriter.Close()
			d.logger.Error("Failed exporting instance", ctxMap)
			return meta, err
		}

		if !expiration.IsZero() {
			meta.ExpiryDate = expiration.UTC().Unix()
		}

		if properties != nil {
			meta.Properties = properties
		}

		if properties != nil || !expiration.IsZero() {
			// Generate a new metadata.yaml.
			tempDir, err := os.MkdirTemp("", "incus_metadata_")
			if err != nil {
				_ = tarWriter.Close()
				d.logger.Error("Failed exporting instance", ctxMap)
				return meta, err
			}

			defer func() { _ = os.RemoveAll(tempDir) }()

			data, err := yaml.Marshal(&meta)
			if err != nil {
				_ = tarWriter.Close()
				d.logger.Error("Failed exporting instance", ctxMap)
				return meta, err
			}

			// Write the actual file.
			fnam = filepath.Join(tempDir, "metadata.yaml")
			err = os.WriteFile(fnam, data, 0644)
			if err != nil {
				_ = tarWriter.Close()
				d.logger.Error("Failed exporting instance", ctxMap)
				return meta, err
			}
		}

		// Include metadata.yaml in the tarball.
		fi, err := os.Lstat(fnam)
		if err != nil {
			_ = tarWriter.Close()
			d.logger.Debug("Error statting during export", logger.Ctx{"fileName": fnam})
			d.logger.Error("Failed exporting instance", ctxMap)
			return meta, err
		}

		if properties != nil || !expiration.IsZero() {
			tmpOffset := len(filepath.Dir(fnam)) + 1
			err = tarWriter.WriteFile(fnam[tmpOffset:], fnam, fi, false)
		} else {
			err = tarWriter.WriteFile(fnam[offset:], fnam, fi, false)
		}

		if err != nil {
			_ = tarWriter.Close()
			d.logger.Debug("Error writing to tarfile", logger.Ctx{"err": err})
			d.logger.Error("Failed exporting instance", ctxMap)
			return meta, err
		}
	}

	// Convert from raw to qcow2 and add to tarball.
	tmpPath, err := os.MkdirTemp(internalUtil.VarPath("images"), "incus_export_")
	if err != nil {
		return meta, err
	}

	defer func() { _ = os.RemoveAll(tmpPath) }()

	if mountInfo.DiskPath == "" {
		return meta, fmt.Errorf("No disk path available from mount")
	}

	fPath := fmt.Sprintf("%s/rootfs.img", tmpPath)

	// Convert to qcow2 image.
	cmd := []string{
		"nice", "-n19", // Run with low priority to reduce CPU impact on other processes.
		"qemu-img", "convert", "-f", "raw", "-O", "qcow2", "-c",
	}

	revert := revert.New()
	defer revert.Fail()

	// Check for Direct I/O support.
	from, err := os.OpenFile(mountInfo.DiskPath, unix.O_DIRECT|unix.O_RDONLY, 0)
	if err == nil {
		cmd = append(cmd, "-T", "none")
		_ = from.Close()
	}

	to, err := os.OpenFile(fPath, unix.O_DIRECT|unix.O_CREAT, 0600)
	if err == nil {
		cmd = append(cmd, "-t", "none")
		_ = to.Close()
	}

	revert.Add(func() { _ = os.Remove(fPath) })

	cmd = append(cmd, mountInfo.DiskPath, fPath)

	_, err = apparmor.QemuImg(d.state.OS, cmd, mountInfo.DiskPath, fPath)
	if err != nil {
		return meta, fmt.Errorf("Failed converting instance to qcow2: %w", err)
	}

	// Read converted file info and write file to tarball.
	fi, err := os.Lstat(fPath)
	if err != nil {
		return meta, err
	}

	imgOffset := len(tmpPath) + 1
	err = tarWriter.WriteFile(fPath[imgOffset:], fPath, fi, false)
	if err != nil {
		return meta, err
	}

	// Include all the templates.
	fnam = d.TemplatesPath()
	if util.PathExists(fnam) {
		err = filepath.Walk(fnam, writeToTar)
		if err != nil {
			d.logger.Error("Failed exporting instance", ctxMap)
			return meta, err
		}
	}

	err = tarWriter.Close()
	if err != nil {
		d.logger.Error("Failed exporting instance", ctxMap)
		return meta, err
	}

	revert.Success()
	d.logger.Info("Exported instance", ctxMap)
	return meta, nil
}

// MigrateSend is not currently supported.
func (d *qemu) MigrateSend(args instance.MigrateSendArgs) error {
	d.logger.Info("Migration send starting")
	defer d.logger.Info("Migration send stopped")

	// Check for stateful support.
	if args.Live && util.IsFalseOrEmpty(d.expandedConfig["migration.stateful"]) {
		return fmt.Errorf("Stateful migration requires migration.stateful to be set to true")
	}

	// Wait for essential migration connections before negotiation.
	connectionsCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	filesystemConn, err := args.FilesystemConn(connectionsCtx)
	if err != nil {
		return err
	}

	pool, err := storagePools.LoadByInstance(d.state, d)
	if err != nil {
		return fmt.Errorf("Failed loading instance: %w", err)
	}

	// The refresh argument passed to MigrationTypes() is always set
	// to false here. The migration source/sender doesn't need to care whether
	// or not it's doing a refresh as the migration sink/receiver will know
	// this, and adjust the migration types accordingly.
	poolMigrationTypes := pool.MigrationTypes(storagePools.InstanceContentType(d), false, args.Snapshots)
	if len(poolMigrationTypes) == 0 {
		return fmt.Errorf("No source migration types available")
	}

	// Convert the pool's migration type options to an offer header to target.
	// Populate the Fs, ZfsFeatures and RsyncFeatures fields.
	offerHeader := localMigration.TypesToHeader(poolMigrationTypes...)

	// Offer to send index header.
	indexHeaderVersion := localMigration.IndexHeaderVersion
	offerHeader.IndexHeaderVersion = &indexHeaderVersion

	// For VMs, send block device size hint in offer header so that target can create the volume the same size.
	blockSize, err := storagePools.InstanceDiskBlockSize(pool, d, d.op)
	if err != nil {
		return fmt.Errorf("Failed getting source disk size: %w", err)
	}

	d.logger.Debug("Set migration offer volume size", logger.Ctx{"blockSize": blockSize})
	offerHeader.VolumeSize = &blockSize

	srcConfig, err := pool.GenerateInstanceBackupConfig(d, args.Snapshots, d.op)
	if err != nil {
		return fmt.Errorf("Failed generating instance migration config: %w", err)
	}

	// If we are copying snapshots, retrieve a list of snapshots from source volume.
	if args.Snapshots {
		offerHeader.SnapshotNames = make([]string, 0, len(srcConfig.Snapshots))
		offerHeader.Snapshots = make([]*migration.Snapshot, 0, len(srcConfig.Snapshots))

		for i := range srcConfig.Snapshots {
			offerHeader.SnapshotNames = append(offerHeader.SnapshotNames, srcConfig.Snapshots[i].Name)
			offerHeader.Snapshots = append(offerHeader.Snapshots, instance.SnapshotToProtobuf(srcConfig.Snapshots[i]))
		}
	}

	// Offer QEMU to QEMU live state transfer state transfer feature.
	// If the request is for live migration, then offer that live QEMU to QEMU state transfer can proceed.
	// Otherwise we'll fallback to doing stateful stop, migrate, and then stateful start, which will still
	// fulfil the "live" part of the request, albeit with longer pause of the instance during the process.
	if args.Live {
		offerHeader.Criu = migration.CRIUType_VM_QEMU.Enum()
	}

	// Send offer to target.
	d.logger.Debug("Sending migration offer to target")
	err = args.ControlSend(offerHeader)
	if err != nil {
		return fmt.Errorf("Failed sending migration offer header: %w", err)
	}

	// Receive response from target.
	d.logger.Debug("Waiting for migration offer response from target")
	respHeader := &migration.MigrationHeader{}
	err = args.ControlReceive(respHeader)
	if err != nil {
		return fmt.Errorf("Failed receiving migration offer response: %w", err)
	}

	d.logger.Debug("Got migration offer response from target")

	// Negotiated migration types.
	migrationTypes, err := localMigration.MatchTypes(respHeader, migration.MigrationFSType_RSYNC, poolMigrationTypes)
	if err != nil {
		return fmt.Errorf("Failed to negotiate migration type: %w", err)
	}

	volSourceArgs := &localMigration.VolumeSourceArgs{
		IndexHeaderVersion: respHeader.GetIndexHeaderVersion(), // Enable index header frame if supported.
		Name:               d.Name(),
		MigrationType:      migrationTypes[0],
		Snapshots:          offerHeader.SnapshotNames,
		TrackProgress:      true,
		Refresh:            respHeader.GetRefresh(),
		AllowInconsistent:  args.AllowInconsistent,
		VolumeOnly:         !args.Snapshots,
		Info:               &localMigration.Info{Config: srcConfig},
		ClusterMove:        args.ClusterMoveSourceName != "",
	}

	// Only send the snapshots that the target requests when refreshing.
	if respHeader.GetRefresh() {
		volSourceArgs.Snapshots = respHeader.GetSnapshotNames()
		allSnapshots := volSourceArgs.Info.Config.VolumeSnapshots

		// Ensure that only the requested snapshots are included in the migration index header.
		volSourceArgs.Info.Config.VolumeSnapshots = make([]*api.StorageVolumeSnapshot, 0, len(volSourceArgs.Snapshots))
		for i := range allSnapshots {
			if slices.Contains(volSourceArgs.Snapshots, allSnapshots[i].Name) {
				volSourceArgs.Info.Config.VolumeSnapshots = append(volSourceArgs.Info.Config.VolumeSnapshots, allSnapshots[i])
			}
		}
	}

	// Detect whether the far side has chosen to use QEMU to QEMU live state transfer mode, and if so then
	// wait for the connection to be established.
	var stateConn io.ReadWriteCloser
	if args.Live && respHeader.Criu != nil && *respHeader.Criu == migration.CRIUType_VM_QEMU {
		stateConn, err = args.StateConn(connectionsCtx)
		if err != nil {
			return err
		}
	}

	g, ctx := errgroup.WithContext(context.Background())

	// Start control connection monitor.
	g.Go(func() error {
		d.logger.Debug("Migrate send control monitor started")
		defer d.logger.Debug("Migrate send control monitor finished")

		controlResult := make(chan error, 1) // Buffered to allow go routine to end if no readers.

		// This will read the result message from the target side and detect disconnections.
		go func() {
			resp := migration.MigrationControl{}
			err := args.ControlReceive(&resp)
			if err != nil {
				err = fmt.Errorf("Error reading migration control target: %w", err)
			} else if !resp.GetSuccess() {
				err = fmt.Errorf("Error from migration control target: %s", resp.GetMessage())
			}

			controlResult <- err
		}()

		// End as soon as we get control message/disconnection from the target side or a local error.
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case err = <-controlResult:
		}

		return err
	})

	// Start error monitoring routine, this will detect when an error is returned from the other routines,
	// and if that happens it will disconnect the migration connections which will trigger the other routines
	// to finish.
	go func() {
		<-ctx.Done()
		args.Disconnect()
	}()

	g.Go(func() error {
		d.logger.Debug("Migrate send transfer started")
		defer d.logger.Debug("Migrate send transfer finished")

		var err error

		// Start live state transfer using state connection if supported.
		if stateConn != nil {
			// When performing intra-cluster same-name move, take steps to prevent corruption
			// of volatile device config keys during start & stop of instance on source/target.
			if args.ClusterMoveSourceName == d.name {
				// Disable VolatileSet from persisting changes to the database.
				// This is so the volatile changes written by the running receiving member
				// are not lost when the source instance is stopped.
				d.volatileSetPersistDisable = true

				// Store a reference to this instance (which has the old volatile settings)
				// to allow the onStop hook to pick it up, which allows the devices being
				// stopped to access their volatile settings stored when the instance
				// originally started on this cluster member.
				instanceRefSet(d)
				defer instanceRefClear(d)
			}

			err = d.migrateSendLive(pool, args.ClusterMoveSourceName, blockSize, filesystemConn, stateConn, volSourceArgs)
			if err != nil {
				return err
			}
		} else {
			// Perform stateful stop if live state transfer is not supported by target.
			if args.Live {
				err = d.Stop(true)
				if err != nil {
					return fmt.Errorf("Failed statefully stopping instance: %w", err)
				}
			}

			err = pool.MigrateInstance(d, filesystemConn, volSourceArgs, d.op)
			if err != nil {
				return err
			}
		}

		return nil
	})

	// Wait for routines to finish and collect first error.
	{
		err := g.Wait()
		if err != nil {
			return err
		}

		return nil
	}
}

// migrateSendLive performs live migration send process.
func (d *qemu) migrateSendLive(pool storagePools.Pool, clusterMoveSourceName string, rootDiskSize int64, filesystemConn io.ReadWriteCloser, stateConn io.ReadWriteCloser, volSourceArgs *localMigration.VolumeSourceArgs) error {
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
	if err != nil {
		return err
	}

	rootDiskName := "incus_root"                  // Name of source disk device to sync from
	nbdTargetDiskName := "incus_root_nbd"         // Name of NBD disk device added to local VM to sync to.
	rootSnapshotDiskName := "incus_root_snapshot" // Name of snapshot disk device to use.

	// If we are performing an intra-cluster member move on a Ceph storage pool then we can treat this as
	// shared storage and avoid needing to sync the root disk.
	sharedStorage := clusterMoveSourceName != "" && pool.Driver().Info().Remote

	revert := revert.New()

	// Non-shared storage snapshot setup.
	if !sharedStorage {
		// Setup migration capabilities.
		capabilities := map[string]bool{
			// Automatically throttle down the guest to speed up convergence of RAM migration.
			"auto-converge": true,

			// Allow the migration to be paused after the source qemu releases the block devices but
			// before the serialisation of the device state, to avoid a race condition between
			// migration and blockdev-mirror. This requires that the migration be continued after it
			// has reached the "pre-switchover" status.
			"pause-before-switchover": true,

			// During storage migration encode blocks of zeroes efficiently.
			"zero-blocks": true,
		}

		err = monitor.MigrateSetCapabilities(capabilities)
		if err != nil {
			return fmt.Errorf("Failed setting migration capabilities: %w", err)
		}

		// Create snapshot of the root disk.
		// We use the VM's config volume for this so that the maximum size of the snapshot can be limited
		// by setting the root disk's `size.state` property.
		snapshotFile := filepath.Join(d.Path(), "migration_snapshot.qcow2")

		// Ensure there are no existing migration snapshot files.
		err = os.Remove(snapshotFile)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}

		// Create qcow2 disk image with the maximum size set to the instance's root disk size for use as
		// a CoW target for the migration snapshot. This will be used during migration to store writes in
		// the guest whilst the storage driver is transferring the root disk and snapshots to the taget.
		_, err = subprocess.RunCommand("qemu-img", "create", "-f", "qcow2", snapshotFile, fmt.Sprintf("%d", rootDiskSize))
		if err != nil {
			return fmt.Errorf("Failed opening file image for migration storage snapshot %q: %w", snapshotFile, err)
		}

		defer func() { _ = os.Remove(snapshotFile) }()

		// Pass the snapshot file to the running QEMU process.
		snapFile, err := os.OpenFile(snapshotFile, unix.O_RDWR, 0)
		if err != nil {
			return fmt.Errorf("Failed opening file descriptor for migration storage snapshot %q: %w", snapshotFile, err)
		}

		defer func() { _ = snapFile.Close() }()

		// Remove the snapshot file as we don't want to sync this to the target.
		err = os.Remove(snapshotFile)
		if err != nil {
			return err
		}

		info, err := monitor.SendFileWithFDSet(rootSnapshotDiskName, snapFile, false)
		if err != nil {
			return fmt.Errorf("Failed sending file descriptor of %q for migration storage snapshot: %w", snapFile.Name(), err)
		}

		defer func() { _ = monitor.RemoveFDFromFDSet(rootSnapshotDiskName) }()

		_ = snapFile.Close() // Don't prevent clean unmount when instance is stopped.

		// Add the snapshot file as a block device (not visible to the guest OS).
		err = monitor.AddBlockDevice(map[string]any{
			"driver":    "qcow2",
			"node-name": rootSnapshotDiskName,
			"read-only": false,
			"file": map[string]any{
				"driver":   "file",
				"filename": fmt.Sprintf("/dev/fdset/%d", info.ID),
			},
		}, nil)
		if err != nil {
			return fmt.Errorf("Failed adding migration storage snapshot block device: %w", err)
		}

		defer func() {
			_ = monitor.RemoveBlockDevice(rootSnapshotDiskName)
		}()

		// Take a snapshot of the root disk and redirect writes to the snapshot disk.
		err = monitor.BlockDevSnapshot(rootDiskName, rootSnapshotDiskName)
		if err != nil {
			return fmt.Errorf("Failed taking temporary migration storage snapshot: %w", err)
		}

		revert.Add(func() {
			// Resume guest (this is needed as it will prevent merging the snapshot if paused).
			err = monitor.Start()
			if err != nil {
				d.logger.Warn("Failed resuming instance", logger.Ctx{"err": err})
			}

			// Try and merge snapshot back to the source disk on failure so we don't lose writes.
			err = monitor.BlockCommit(rootSnapshotDiskName)
			if err != nil {
				d.logger.Error("Failed merging migration storage snapshot", logger.Ctx{"err": err})
			}
		})

		defer revert.Fail() // Run the revert fail before the earlier defers.

		d.logger.Debug("Setup temporary migration storage snapshot")
	} else {
		// Still set some options for shared storage.
		capabilities := map[string]bool{
			// Automatically throttle down the guest to speed up convergence of RAM migration.
			"auto-converge": true,
		}

		err = monitor.MigrateSetCapabilities(capabilities)
		if err != nil {
			return fmt.Errorf("Failed setting migration capabilities: %w", err)
		}
	}

	// Perform storage transfer while instance is still running.
	// For shared storage the storage driver will likely not do much here, but we still call it anyway for the
	// sense checks it performs.
	// We enable AllowInconsistent mode as this allows for transferring the VM storage whilst it is running
	// and the snapshot we took earlier is designed to provide consistency anyway.
	volSourceArgs.AllowInconsistent = true
	err = pool.MigrateInstance(d, filesystemConn, volSourceArgs, d.op)
	if err != nil {
		return err
	}

	// Derive the effective storage project name from the instance config's project.
	storageProjectName, err := project.StorageVolumeProject(d.state.DB.Cluster, d.project.Name, db.StoragePoolVolumeTypeCustom)
	if err != nil {
		return err
	}

	// Notify the shared disks that they're going to be accessed from another system.
	for _, dev := range d.expandedDevices.Sorted() {
		if dev.Config["type"] != "disk" || dev.Config["path"] == "/" {
			continue
		}

		// Load the pool for the disk.
		diskPool, err := storagePools.LoadByName(d.state, dev.Config["pool"])
		if err != nil {
			return fmt.Errorf("Failed loading storage pool: %w", err)
		}

		// Setup the volume entry.
		extraSourceArgs := &localMigration.VolumeSourceArgs{
			ClusterMove: true,
		}

		vol := diskPool.GetVolume(storageDrivers.VolumeTypeCustom, storageDrivers.ContentTypeBlock, project.StorageVolume(storageProjectName, dev.Config["source"]), nil)

		// Call MigrateVolume on the source.
		err = diskPool.Driver().MigrateVolume(vol, nil, extraSourceArgs, nil)
		if err != nil {
			return fmt.Errorf("Failed to prepare device %q for migration: %w", dev.Name, err)
		}
	}

	// Non-shared storage snapshot transfer.
	if !sharedStorage {
		listener, err := net.Listen("unix", "")
		if err != nil {
			return fmt.Errorf("Failed creating NBD unix listener: %w", err)
		}

		defer func() { _ = listener.Close() }()

		go func() {
			d.logger.Debug("NBD listener waiting for accept")
			nbdConn, err := listener.Accept()
			if err != nil {
				d.logger.Error("Failed accepting connection to NBD client unix listener", logger.Ctx{"err": err})
				return
			}

			defer func() { _ = nbdConn.Close() }()

			d.logger.Debug("NBD connection on source started")
			go func() { _, _ = io.Copy(filesystemConn, nbdConn) }()

			_, _ = io.Copy(nbdConn, filesystemConn)
			d.logger.Debug("NBD connection on source finished")
		}()

		// Connect to NBD migration target and add it the source instance as a disk device.
		d.logger.Debug("Connecting to migration NBD storage target")
		err = monitor.AddBlockDevice(map[string]any{
			"node-name": nbdTargetDiskName,
			"driver":    "raw",
			"file": map[string]any{
				"driver": "nbd",
				"export": qemuMigrationNBDExportName,
				"server": map[string]any{
					"type":     "unix",
					"abstract": true,
					"path":     strings.TrimPrefix(listener.Addr().String(), "@"),
				},
			},
		}, nil)
		if err != nil {
			return fmt.Errorf("Failed adding NBD device: %w", err)
		}

		revert.Add(func() {
			time.Sleep(time.Second) // Wait for it to be released.
			err := monitor.RemoveBlockDevice(nbdTargetDiskName)
			if err != nil {
				d.logger.Warn("Failed removing NBD storage target device", logger.Ctx{"err": err})
			}
		})

		d.logger.Debug("Connected to migration NBD storage target")

		// Begin transferring any writes that occurred during the storage migration by transferring the
		// contents of the (top) migration snapshot to the target disk to bring them into sync.
		// Once this has completed the guest OS will be paused.
		d.logger.Debug("Migration storage snapshot transfer started")
		err = monitor.BlockDevMirror(rootSnapshotDiskName, nbdTargetDiskName)
		if err != nil {
			return fmt.Errorf("Failed transferring migration storage snapshot: %w", err)
		}

		revert.Add(func() {
			err = monitor.BlockJobCancel(rootSnapshotDiskName)
			if err != nil {
				d.logger.Error("Failed cancelling block job", logger.Ctx{"err": err})
			}
		})

		d.logger.Debug("Migration storage snapshot transfer finished")
	}

	d.logger.Debug("Stateful migration checkpoint send starting")

	// Send checkpoint to QEMU process on target. This will pause the guest OS (if not already paused).
	pipeRead, pipeWrite, err := os.Pipe()
	if err != nil {
		return err
	}

	defer func() {
		_ = pipeRead.Close()
		_ = pipeWrite.Close()
	}()

	go func() { _, _ = io.Copy(stateConn, pipeRead) }()

	err = d.saveStateHandle(monitor, pipeWrite)
	if err != nil {
		return fmt.Errorf("Failed starting state transfer to target: %w", err)
	}

	// Non-shared storage snapshot transfer finalization.
	if !sharedStorage {
		// Wait until state transfer has reached pre-switchover state (the guest OS will remain paused).
		err = monitor.MigrateWait("pre-switchover")
		if err != nil {
			return fmt.Errorf("Failed waiting for state transfer to reach pre-switchover stage: %w", err)
		}

		d.logger.Debug("Stateful migration checkpoint reached pre-switchover phase")

		// Complete the migration snapshot sync process (the guest OS will remain paused).
		d.logger.Debug("Migration storage snapshot transfer commit started")
		err = monitor.BlockJobCancel(rootSnapshotDiskName)
		if err != nil {
			return fmt.Errorf("Failed cancelling block job: %w", err)
		}

		d.logger.Debug("Migration storage snapshot transfer commit finished")

		// Finalise the migration state transfer (the guest OS will remain paused).
		err = monitor.MigrateContinue("pre-switchover")
		if err != nil {
			return fmt.Errorf("Failed continuing state transfer: %w", err)
		}

		d.logger.Debug("Stateful migration checkpoint send continuing")
	}

	// Wait until the migration state transfer has completed (the guest OS will remain paused).
	err = monitor.MigrateWait("completed")
	if err != nil {
		return fmt.Errorf("Failed waiting for state transfer to reach completed stage: %w", err)
	}

	d.logger.Debug("Stateful migration checkpoint send finished")

	if clusterMoveSourceName != "" {
		// If doing an intra-cluster member move then we will be deleting the instance on the source,
		// so lets just stop it after migration is completed.
		err = d.Stop(false)
		if err != nil {
			return fmt.Errorf("Failed stopping instance: %w", err)
		}
	} else {
		// Remove the NBD client disk.
		err := monitor.RemoveBlockDevice(nbdTargetDiskName)
		if err != nil {
			d.logger.Warn("Failed removing NBD storage target device", logger.Ctx{"err": err})
		}

		d.logger.Debug("Removed NBD storage target device")

		// Resume guest.
		err = monitor.Start()
		if err != nil {
			return fmt.Errorf("Failed resuming instance: %w", err)
		}

		d.logger.Debug("Resumed instance")

		// Merge snapshot back to the source disk so we don't lose the writes.
		d.logger.Debug("Merge migration storage snapshot on source started")
		err = monitor.BlockCommit(rootSnapshotDiskName)
		if err != nil {
			return fmt.Errorf("Failed merging migration storage snapshot: %w", err)
		}

		d.logger.Debug("Merge migration storage snapshot on source finished")
	}

	revert.Success()
	return nil
}

func (d *qemu) MigrateReceive(args instance.MigrateReceiveArgs) error {
	d.logger.Info("Migration receive starting")
	defer d.logger.Info("Migration receive stopped")

	// Wait for essential migration connections before negotiation.
	connectionsCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	filesystemConn, err := args.FilesystemConn(connectionsCtx)
	if err != nil {
		return err
	}

	// Receive offer from source.
	d.logger.Debug("Waiting for migration offer from source")
	offerHeader := &migration.MigrationHeader{}
	err = args.ControlReceive(offerHeader)
	if err != nil {
		return fmt.Errorf("Failed receiving migration offer from source: %w", err)
	}

	// When doing a cluster same-name move we cannot load the storage pool using the instance's volume DB
	// record because it may be associated to the wrong cluster member. Instead we ascertain the pool to load
	// using the instance's root disk device.
	if args.ClusterMoveSourceName == d.name {
		_, rootDiskDevice, err := d.getRootDiskDevice()
		if err != nil {
			return fmt.Errorf("Failed getting root disk: %w", err)
		}

		if rootDiskDevice["pool"] == "" {
			return fmt.Errorf("The instance's root device is missing the pool property")
		}

		// Initialize the storage pool cache.
		d.storagePool, err = storagePools.LoadByName(d.state, rootDiskDevice["pool"])
		if err != nil {
			return fmt.Errorf("Failed loading storage pool: %w", err)
		}
	}

	pool, err := storagePools.LoadByInstance(d.state, d)
	if err != nil {
		return err
	}

	// The source will never set Refresh in the offer header.
	// However, to determine the correct migration type Refresh needs to be set.
	offerHeader.Refresh = &args.Refresh

	// Extract the source's migration type and then match it against our pool's supported types and features.
	// If a match is found the combined features list will be sent back to requester.
	contentType := storagePools.InstanceContentType(d)
	respTypes, err := localMigration.MatchTypes(offerHeader, storagePools.FallbackMigrationType(contentType), pool.MigrationTypes(contentType, args.Refresh, args.Snapshots))
	if err != nil {
		return err
	}

	// The migration header to be sent back to source with our target options.
	// Convert response type to response header and copy snapshot info into it.
	respHeader := localMigration.TypesToHeader(respTypes...)

	// Respond with our maximum supported header version if the requested version is higher than ours.
	// Otherwise just return the requested header version to the source.
	indexHeaderVersion := offerHeader.GetIndexHeaderVersion()
	if indexHeaderVersion > localMigration.IndexHeaderVersion {
		indexHeaderVersion = localMigration.IndexHeaderVersion
	}

	respHeader.IndexHeaderVersion = &indexHeaderVersion
	respHeader.SnapshotNames = offerHeader.SnapshotNames
	respHeader.Snapshots = offerHeader.Snapshots
	respHeader.Refresh = &args.Refresh

	if args.Refresh {
		// Get the remote snapshots on the source.
		sourceSnapshots := offerHeader.GetSnapshots()
		sourceSnapshotComparable := make([]storagePools.ComparableSnapshot, 0, len(sourceSnapshots))
		for _, sourceSnap := range sourceSnapshots {
			sourceSnapshotComparable = append(sourceSnapshotComparable, storagePools.ComparableSnapshot{
				Name:         sourceSnap.GetName(),
				CreationDate: time.Unix(sourceSnap.GetCreationDate(), 0),
			})
		}

		// Get existing snapshots on the local target.
		targetSnapshots, err := d.Snapshots()
		if err != nil {
			return err
		}

		targetSnapshotsComparable := make([]storagePools.ComparableSnapshot, 0, len(targetSnapshots))
		for _, targetSnap := range targetSnapshots {
			_, targetSnapName, _ := api.GetParentAndSnapshotName(targetSnap.Name())

			targetSnapshotsComparable = append(targetSnapshotsComparable, storagePools.ComparableSnapshot{
				Name:         targetSnapName,
				CreationDate: targetSnap.CreationDate(),
			})
		}

		// Compare the two sets.
		syncSourceSnapshotIndexes, deleteTargetSnapshotIndexes := storagePools.CompareSnapshots(sourceSnapshotComparable, targetSnapshotsComparable)

		// Delete the extra local snapshots first.
		for _, deleteTargetSnapshotIndex := range deleteTargetSnapshotIndexes {
			err := targetSnapshots[deleteTargetSnapshotIndex].Delete(true)
			if err != nil {
				return err
			}
		}

		// Only request to send the snapshots that need updating.
		syncSnapshotNames := make([]string, 0, len(syncSourceSnapshotIndexes))
		syncSnapshots := make([]*migration.Snapshot, 0, len(syncSourceSnapshotIndexes))
		for _, syncSourceSnapshotIndex := range syncSourceSnapshotIndexes {
			syncSnapshotNames = append(syncSnapshotNames, sourceSnapshots[syncSourceSnapshotIndex].GetName())
			syncSnapshots = append(syncSnapshots, sourceSnapshots[syncSourceSnapshotIndex])
		}

		respHeader.Snapshots = syncSnapshots
		respHeader.SnapshotNames = syncSnapshotNames
		offerHeader.Snapshots = syncSnapshots
		offerHeader.SnapshotNames = syncSnapshotNames
	}

	// Negotiate support for QEMU to QEMU live state transfer.
	// If the request is for live migration, then respond that live QEMU to QEMU state transfer can proceed.
	// Otherwise we'll fallback to doing stateful stop, migrate, and then stateful start, which will still
	// fulfil the "live" part of the request, albeit with longer pause of the instance during the process.
	poolInfo := pool.Driver().Info()
	var useStateConn bool
	if args.Live && offerHeader.Criu != nil && *offerHeader.Criu == migration.CRIUType_VM_QEMU {
		respHeader.Criu = migration.CRIUType_VM_QEMU.Enum()
		useStateConn = true
	}

	// Send response to source.
	d.logger.Debug("Sending migration response to source")
	err = args.ControlSend(respHeader)
	if err != nil {
		return fmt.Errorf("Failed sending migration response to source: %w", err)
	}

	d.logger.Debug("Sent migration response to source")

	// Establish state transfer connection if needed.
	var stateConn io.ReadWriteCloser
	if args.Live && useStateConn {
		stateConn, err = args.StateConn(connectionsCtx)
		if err != nil {
			return err
		}
	}

	revert := revert.New()
	defer revert.Fail()

	g, ctx := errgroup.WithContext(context.Background())

	// Start control connection monitor.
	g.Go(func() error {
		d.logger.Debug("Migrate receive control monitor started")
		defer d.logger.Debug("Migrate receive control monitor finished")

		controlResult := make(chan error, 1) // Buffered to allow go routine to end if no readers.

		// This will read the result message from the source side and detect disconnections.
		go func() {
			resp := migration.MigrationControl{}
			err := args.ControlReceive(&resp)
			if err != nil {
				err = fmt.Errorf("Error reading migration control source: %w", err)
			} else if !resp.GetSuccess() {
				err = fmt.Errorf("Error from migration control source: %s", resp.GetMessage())
			}

			controlResult <- err
		}()

		// End as soon as we get control message/disconnection from the source side or a local error.
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case err = <-controlResult:
		}

		return err
	})

	// Start error monitoring routine, this will detect when an error is returned from the other routines,
	// and if that happens it will disconnect the migration connections which will trigger the other routines
	// to finish.
	go func() {
		<-ctx.Done()
		args.Disconnect()
	}()

	// Start filesystem transfer routine and initialize a channel that is closed when the routine finishes.
	fsTransferDone := make(chan struct{})
	g.Go(func() error {
		defer close(fsTransferDone)

		d.logger.Debug("Migrate receive transfer started")
		defer d.logger.Debug("Migrate receive transfer finished")

		var err error

		snapshots := make([]*migration.Snapshot, 0)

		// Legacy: we only sent the snapshot names, so we just copy the instances's config over,
		// same as we used to do.
		if len(offerHeader.SnapshotNames) != len(offerHeader.Snapshots) {
			// Convert the instance to an api.InstanceSnapshot.

			profileNames := make([]string, 0, len(d.Profiles()))
			for _, p := range d.Profiles() {
				profileNames = append(profileNames, p.Name)
			}

			architectureName, _ := osarch.ArchitectureName(d.Architecture())
			apiInstSnap := &api.InstanceSnapshot{
				InstanceSnapshotPut: api.InstanceSnapshotPut{
					ExpiresAt: time.Time{},
				},
				Architecture: architectureName,
				CreatedAt:    d.CreationDate(),
				LastUsedAt:   d.LastUsedDate(),
				Config:       d.LocalConfig(),
				Devices:      d.LocalDevices().CloneNative(),
				Ephemeral:    d.IsEphemeral(),
				Stateful:     d.IsStateful(),
				Profiles:     profileNames,
			}

			for _, name := range offerHeader.SnapshotNames {
				base := instance.SnapshotToProtobuf(apiInstSnap)
				base.Name = &name
				snapshots = append(snapshots, base)
			}
		} else {
			snapshots = offerHeader.Snapshots
		}

		volTargetArgs := localMigration.VolumeTargetArgs{
			IndexHeaderVersion:    respHeader.GetIndexHeaderVersion(),
			Name:                  d.Name(),
			MigrationType:         respTypes[0],
			Refresh:               args.Refresh,                // Indicate to receiver volume should exist.
			TrackProgress:         true,                        // Use a progress tracker on receiver to get in-cluster progress information.
			Live:                  false,                       // Indicates we won't get a final rootfs sync.
			VolumeSize:            offerHeader.GetVolumeSize(), // Block size setting override.
			VolumeOnly:            !args.Snapshots,
			ClusterMoveSourceName: args.ClusterMoveSourceName,
		}

		// At this point we have already figured out the parent instances's root
		// disk device so we can simply retrieve it from the expanded devices.
		parentStoragePool := ""
		parentExpandedDevices := d.ExpandedDevices()
		parentLocalRootDiskDeviceKey, parentLocalRootDiskDevice, _ := internalInstance.GetRootDiskDevice(parentExpandedDevices.CloneNative())
		if parentLocalRootDiskDeviceKey != "" {
			parentStoragePool = parentLocalRootDiskDevice["pool"]
		}

		if parentStoragePool == "" {
			return fmt.Errorf("Instance's root device is missing the pool property")
		}

		// A zero length Snapshots slice indicates volume only migration in
		// VolumeTargetArgs. So if VolumeOnly was requested, do not populate them.
		if args.Snapshots {
			volTargetArgs.Snapshots = make([]string, 0, len(snapshots))
			for _, snap := range snapshots {
				volTargetArgs.Snapshots = append(volTargetArgs.Snapshots, *snap.Name)

				// Only create snapshot instance DB records if not doing a cluster same-name move.
				// As otherwise the DB records will already exist.
				if args.ClusterMoveSourceName != d.name {
					snapArgs, err := instance.SnapshotProtobufToInstanceArgs(d.state, d, snap)
					if err != nil {
						return err
					}

					// Ensure that snapshot and parent instance have the same storage pool in
					// their local root disk device. If the root disk device for the snapshot
					// comes from a profile on the new instance as well we don't need to do
					// anything.
					if snapArgs.Devices != nil {
						snapLocalRootDiskDeviceKey, _, _ := internalInstance.GetRootDiskDevice(snapArgs.Devices.CloneNative())
						if snapLocalRootDiskDeviceKey != "" {
							snapArgs.Devices[snapLocalRootDiskDeviceKey]["pool"] = parentStoragePool
						}
					}

					// Create the snapshot instance.
					_, snapInstOp, cleanup, err := instance.CreateInternal(d.state, *snapArgs, true, false)
					if err != nil {
						return fmt.Errorf("Failed creating instance snapshot record %q: %w", snapArgs.Name, err)
					}

					revert.Add(cleanup)
					defer snapInstOp.Done(err)
				}
			}
		}

		err = pool.CreateInstanceFromMigration(d, filesystemConn, volTargetArgs, d.op)
		if err != nil {
			return fmt.Errorf("Failed creating instance on target: %w", err)
		}

		// Derive the effective storage project name from the instance config's project.
		storageProjectName, err := project.StorageVolumeProject(d.state.DB.Cluster, d.project.Name, db.StoragePoolVolumeTypeCustom)
		if err != nil {
			return err
		}

		// Notify the shared disks that they're going to be accessed from another system.
		for _, dev := range d.expandedDevices.Sorted() {
			if dev.Config["type"] != "disk" || dev.Config["path"] == "/" {
				continue
			}

			// Load the pool for the disk.
			diskPool, err := storagePools.LoadByName(d.state, dev.Config["pool"])
			if err != nil {
				return fmt.Errorf("Failed loading storage pool: %w", err)
			}

			// Setup the volume entry.
			extraTargetArgs := localMigration.VolumeTargetArgs{
				ClusterMoveSourceName: args.ClusterMoveSourceName,
			}

			vol := diskPool.GetVolume(storageDrivers.VolumeTypeCustom, storageDrivers.ContentTypeBlock, project.StorageVolume(storageProjectName, dev.Config["source"]), nil)

			// Call MigrateVolume on the source.
			err = diskPool.Driver().CreateVolumeFromMigration(vol, nil, extraTargetArgs, nil, nil)
			if err != nil {
				return fmt.Errorf("Failed to prepare device %q for migration: %w", dev.Name, err)
			}
		}

		// Only delete all instance volumes on error if the pool volume creation has succeeded to
		// avoid deleting an existing conflicting volume.
		isRemoteClusterMove := args.ClusterMoveSourceName != "" && poolInfo.Remote
		if !volTargetArgs.Refresh && !isRemoteClusterMove {
			revert.Add(func() {
				snapshots, _ := d.Snapshots()
				snapshotCount := len(snapshots)
				for k := range snapshots {
					// Delete the snapshots in reverse order.
					k = snapshotCount - 1 - k
					_ = pool.DeleteInstanceSnapshot(snapshots[k], nil)
				}

				_ = pool.DeleteInstance(d, nil)
			})
		}

		if args.ClusterMoveSourceName != d.name {
			err = d.DeferTemplateApply(instance.TemplateTriggerCopy)
			if err != nil {
				return err
			}
		}

		if args.Live {
			// Start live state transfer using state connection if supported.
			if stateConn != nil {
				d.migrationReceiveStateful = map[string]io.ReadWriteCloser{
					api.SecretNameState: stateConn,
				}

				// Populate the filesystem connection handle if doing non-shared storage migration.
				sharedStorage := args.ClusterMoveSourceName != "" && poolInfo.Remote
				if !sharedStorage {
					d.migrationReceiveStateful[api.SecretNameFilesystem] = filesystemConn
				}
			}

			// Although the instance technically isn't considered stateful, we set this to allow
			// starting from the migrated state file or migration state connection.
			d.stateful = true

			err = d.start(true, args.InstanceOperation)
			if err != nil {
				return err
			}
		}

		return nil
	})

	{
		// Wait until the filesystem transfer routine has finished.
		<-fsTransferDone

		// If context is cancelled by this stage, then an error has occurred.
		// Wait for all routines to finish and collect the first error that occurred.
		if ctx.Err() != nil {
			err := g.Wait()

			// Send failure response to source.
			msg := migration.MigrationControl{
				Success: proto.Bool(err == nil),
			}

			if err != nil {
				msg.Message = proto.String(err.Error())
			}

			d.logger.Debug("Sending migration failure response to source", logger.Ctx{"err": err})
			sendErr := args.ControlSend(&msg)
			if sendErr != nil {
				d.logger.Warn("Failed sending migration failure to source", logger.Ctx{"err": sendErr})
			}

			return err
		}

		// Send success response to source to control as nothing has gone wrong so far.
		msg := migration.MigrationControl{
			Success: proto.Bool(true),
		}

		d.logger.Debug("Sending migration success response to source", logger.Ctx{"success": msg.GetSuccess()})
		err := args.ControlSend(&msg)
		if err != nil {
			d.logger.Warn("Failed sending migration success to source", logger.Ctx{"err": err})
			return fmt.Errorf("Failed sending migration success to source: %w", err)
		}

		// Wait for all routines to finish (in this case it will be the control monitor) but do
		// not collect the error, as it will just be a disconnect error from the source.
		_ = g.Wait()

		revert.Success()
		return nil
	}
}

// CGroupSet is not implemented for VMs.
func (d *qemu) CGroup() (*cgroup.CGroup, error) {
	return nil, instance.ErrNotImplemented
}

// FileSFTPConn returns a connection to the agent SFTP endpoint.
func (d *qemu) FileSFTPConn() (net.Conn, error) {
	// VMs, unlike containers, cannot perform file operations if not running and using the agent.
	if !d.IsRunning() {
		return nil, fmt.Errorf("Instance is not running")
	}

	// Connect to the agent.
	client, err := d.getAgentClient()
	if err != nil {
		return nil, err
	}

	// Get the HTTP transport.
	httpTransport := client.Transport.(*http.Transport)

	// Send the upgrade request.
	u, err := url.Parse("https://custom.socket/1.0/sftp")
	if err != nil {
		return nil, err
	}

	req := &http.Request{
		Method:     http.MethodGet,
		URL:        u,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Host:       u.Host,
	}

	req.Header["Upgrade"] = []string{"sftp"}
	req.Header["Connection"] = []string{"Upgrade"}

	conn, err := httpTransport.DialContext(context.Background(), "tcp", "8443")
	if err != nil {
		return nil, err
	}

	tlsConn := tls.Client(conn, httpTransport.TLSClientConfig)
	err = tlsConn.Handshake()
	if err != nil {
		return nil, err
	}

	err = req.Write(tlsConn)
	if err != nil {
		return nil, err
	}

	resp, err := http.ReadResponse(bufio.NewReader(tlsConn), req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusSwitchingProtocols {
		return nil, fmt.Errorf("Dialing failed: expected status code 101 got %d", resp.StatusCode)
	}

	if resp.Header.Get("Upgrade") != "sftp" {
		return nil, fmt.Errorf("Missing or unexpected Upgrade header in response")
	}

	return tlsConn, nil
}

// FileSFTP returns an SFTP connection to the agent endpoint.
func (d *qemu) FileSFTP() (*sftp.Client, error) {
	// Connect to the forkfile daemon.
	conn, err := d.FileSFTPConn()
	if err != nil {
		return nil, err
	}

	// Get a SFTP client.
	client, err := sftp.NewClientPipe(conn, conn)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	go func() {
		// Wait for the client to be done before closing the connection.
		_ = client.Wait()
		_ = conn.Close()
	}()

	return client, nil
}

// Console gets access to the instance's console.
func (d *qemu) Console(protocol string) (*os.File, chan error, error) {
	var path string
	switch protocol {
	case instance.ConsoleTypeConsole:
		path = d.consolePath()
	case instance.ConsoleTypeVGA:
		path = d.spicePath()
	default:
		return nil, nil, fmt.Errorf("Unknown protocol %q", protocol)
	}

	// Disconnection notification.
	chDisconnect := make(chan error, 1)

	// Open the console socket.
	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil, nil, fmt.Errorf("Connect to console socket %q: %w", path, err)
	}

	file, err := (conn.(*net.UnixConn)).File()
	if err != nil {
		return nil, nil, fmt.Errorf("Get socket file: %w", err)
	}

	_ = conn.Close()

	d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceConsole.Event(d, logger.Ctx{"type": protocol}))

	return file, chDisconnect, nil
}

// Exec a command inside the instance.
func (d *qemu) Exec(req api.InstanceExecPost, stdin *os.File, stdout *os.File, stderr *os.File) (instance.Cmd, error) {
	revert := revert.New()
	defer revert.Fail()

	client, err := d.getAgentClient()
	if err != nil {
		return nil, err
	}

	agent, err := incus.ConnectIncusHTTP(nil, client)
	if err != nil {
		d.logger.Error("Failed to connect to the agent", logger.Ctx{"err": err})
		return nil, fmt.Errorf("Failed to connect to the agent")
	}

	revert.Add(agent.Disconnect)

	dataDone := make(chan bool)
	controlSendCh := make(chan api.InstanceExecControl)
	controlResCh := make(chan error)

	// This is the signal control handler, it receives signals from lxc CLI and forwards them to the VM agent.
	controlHandler := func(control *websocket.Conn) {
		closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
		defer func() { _ = control.WriteMessage(websocket.CloseMessage, closeMsg) }()

		for {
			select {
			case cmd := <-controlSendCh:
				controlResCh <- control.WriteJSON(cmd)
			case <-dataDone:
				return
			}
		}
	}

	args := incus.InstanceExecArgs{
		Stdin:    stdin,
		Stdout:   stdout,
		Stderr:   stderr,
		DataDone: dataDone,
		Control:  controlHandler,
	}

	// Always needed for VM exec, as even for non-websocket requests from the client we need to connect the
	// websockets for control and for capturing output to a file on the server.
	req.WaitForWS = true

	// Similarly, output recording is performed on the host rather than in the guest, so clear that bit from the request.
	req.RecordOutput = false

	op, err := agent.ExecInstance("", req, &args)
	if err != nil {
		return nil, err
	}

	instCmd := &qemuCmd{
		cmd:              op,
		attachedChildPid: 0, // Process is not running on the host.
		dataDone:         args.DataDone,
		cleanupFunc:      revert.Clone().Fail, // Pass revert function clone as clean up function.
		controlSendCh:    controlSendCh,
		controlResCh:     controlResCh,
	}

	d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceExec.Event(d, logger.Ctx{"command": req.Command}))

	revert.Success()
	return instCmd, nil
}

// Render returns info about the instance.
func (d *qemu) Render(options ...func(response any) error) (any, any, error) {
	profileNames := make([]string, 0, len(d.profiles))
	for _, profile := range d.profiles {
		profileNames = append(profileNames, profile.Name)
	}

	if d.IsSnapshot() {
		// Prepare the ETag
		etag := []any{d.expiryDate}

		snapState := api.InstanceSnapshot{
			CreatedAt:       d.creationDate,
			ExpandedConfig:  d.expandedConfig,
			ExpandedDevices: d.expandedDevices.CloneNative(),
			LastUsedAt:      d.lastUsedDate,
			Name:            strings.SplitN(d.name, "/", 2)[1],
			Stateful:        d.stateful,
			Size:            -1, // Default to uninitialized/error state (0 means no CoW usage).
		}

		snapState.Architecture = d.architectureName
		snapState.Config = d.localConfig
		snapState.Devices = d.localDevices.CloneNative()
		snapState.Ephemeral = d.ephemeral
		snapState.Profiles = profileNames
		snapState.ExpiresAt = d.expiryDate

		for _, option := range options {
			err := option(&snapState)
			if err != nil {
				return nil, nil, err
			}
		}

		return &snapState, etag, nil
	}

	// Prepare the ETag
	etag := []any{d.architecture, d.localConfig, d.localDevices, d.ephemeral, d.profiles}
	statusCode := d.statusCode()

	instState := api.Instance{
		ExpandedConfig:  d.expandedConfig,
		ExpandedDevices: d.expandedDevices.CloneNative(),
		Name:            d.name,
		Status:          statusCode.String(),
		StatusCode:      statusCode,
		Location:        d.node,
		Type:            d.Type().String(),
	}

	instState.Description = d.description
	instState.Architecture = d.architectureName
	instState.Config = d.localConfig
	instState.CreatedAt = d.creationDate
	instState.Devices = d.localDevices.CloneNative()
	instState.Ephemeral = d.ephemeral
	instState.LastUsedAt = d.lastUsedDate
	instState.Profiles = profileNames
	instState.Stateful = d.stateful
	instState.Project = d.project.Name

	for _, option := range options {
		err := option(&instState)
		if err != nil {
			return nil, nil, err
		}
	}

	return &instState, etag, nil
}

// RenderFull returns all info about the instance.
func (d *qemu) RenderFull(hostInterfaces []net.Interface) (*api.InstanceFull, any, error) {
	if d.IsSnapshot() {
		return nil, nil, fmt.Errorf("RenderFull doesn't work with snapshots")
	}

	// Get the Instance struct.
	base, etag, err := d.Render()
	if err != nil {
		return nil, nil, err
	}

	// Convert to InstanceFull.
	vmState := api.InstanceFull{Instance: *base.(*api.Instance)}

	// Add the InstanceState.
	vmState.State, err = d.renderState(vmState.StatusCode)
	if err != nil {
		return nil, nil, err
	}

	// Add the InstanceSnapshots.
	snaps, err := d.Snapshots()
	if err != nil {
		return nil, nil, err
	}

	for _, snap := range snaps {
		render, _, err := snap.Render()
		if err != nil {
			return nil, nil, err
		}

		if vmState.Snapshots == nil {
			vmState.Snapshots = []api.InstanceSnapshot{}
		}

		vmState.Snapshots = append(vmState.Snapshots, *render.(*api.InstanceSnapshot))
	}

	// Add the InstanceBackups.
	backups, err := d.Backups()
	if err != nil {
		return nil, nil, err
	}

	for _, backup := range backups {
		render := backup.Render()

		if vmState.Backups == nil {
			vmState.Backups = []api.InstanceBackup{}
		}

		vmState.Backups = append(vmState.Backups, *render)
	}

	return &vmState, etag, nil
}

// renderState returns just state info about the instance.
func (d *qemu) renderState(statusCode api.StatusCode) (*api.InstanceState, error) {
	var err error

	status := &api.InstanceState{}
	pid, _ := d.pid()

	if d.isRunningStatusCode(statusCode) {
		if d.agentMetricsEnabled() {
			// Try and get state info from agent.
			status, err = d.agentGetState()
			if err != nil {
				if !errors.Is(err, errQemuAgentOffline) {
					d.logger.Warn("Could not get VM state from agent", logger.Ctx{"err": err})
				}

				// Fallback data if agent is not reachable.
				status = &api.InstanceState{}
				status.Processes = -1

				status.Network, err = d.getNetworkState()
				if err != nil {
					return nil, err
				}
			}
		} else {
			status.Processes = -1

			status.Network, err = d.getNetworkState()
			if err != nil {
				return nil, err
			}
		}

		// Populate host_name for network devices.
		for k, m := range d.ExpandedDevices() {
			// We only care about nics.
			if m["type"] != "nic" {
				continue
			}

			// Get hwaddr from static or volatile config.
			hwaddr := m["hwaddr"]
			if hwaddr == "" {
				hwaddr = d.localConfig[fmt.Sprintf("volatile.%s.hwaddr", k)]
			}

			// We have to match on hwaddr as device name can be different from the configured device
			// name when reported from the agent inside the VM (due to the guest OS choosing name).
			for netName, netStatus := range status.Network {
				if netStatus.Hwaddr == hwaddr {
					if netStatus.HostName == "" {
						netStatus.HostName = d.localConfig[fmt.Sprintf("volatile.%s.host_name", k)]
						status.Network[netName] = netStatus
					}
				}
			}
		}

		status.Pid = int64(pid)
		status.StartedAt, err = d.processStartedAt(d.InitPID())
		if err != nil {
			return status, err
		}
	}

	status.Status = statusCode.String()
	status.StatusCode = statusCode
	status.Disk, err = d.diskState()
	if err != nil && !errors.Is(err, storageDrivers.ErrNotSupported) {
		d.logger.Warn("Error getting disk usage", logger.Ctx{"err": err})
	}

	return status, nil
}

// RenderState returns just state info about the instance.
func (d *qemu) RenderState(hostInterfaces []net.Interface) (*api.InstanceState, error) {
	return d.renderState(d.statusCode())
}

// diskState gets disk usage info.
func (d *qemu) diskState() (map[string]api.InstanceStateDisk, error) {
	pool, err := d.getStoragePool()
	if err != nil {
		return nil, err
	}

	// Get the root disk device config.
	rootDiskName, _, err := d.getRootDiskDevice()
	if err != nil {
		return nil, err
	}

	usage, err := pool.GetInstanceUsage(d)
	if err != nil {
		return nil, err
	}

	disk := map[string]api.InstanceStateDisk{}
	disk[rootDiskName] = api.InstanceStateDisk{
		Usage: usage.Used,
		Total: usage.Total,
	}

	return disk, nil
}

// agentGetState connects to the agent inside of the VM and does
// an API call to get the current state.
func (d *qemu) agentGetState() (*api.InstanceState, error) {
	client, err := d.getAgentClient()
	if err != nil {
		return nil, err
	}

	agent, err := incus.ConnectIncusHTTP(nil, client)
	if err != nil {
		return nil, fmt.Errorf("Failed connecting to agent: %w", err)
	}

	defer agent.Disconnect()

	status, _, err := agent.GetInstanceState("")
	if err != nil {
		return nil, err
	}

	return status, nil
}

// IsRunning returns whether or not the instance is running.
func (d *qemu) IsRunning() bool {
	return d.isRunningStatusCode(d.statusCode())
}

// IsFrozen returns whether the instance frozen or not.
func (d *qemu) IsFrozen() bool {
	return d.statusCode() == api.Frozen
}

// CanMigrate returns whether the instance can be migrated.
func (d *qemu) CanMigrate() string {
	return d.canMigrate(d)
}

// LockExclusive attempts to get exlusive access to the instance's root volume.
func (d *qemu) LockExclusive() (*operationlock.InstanceOperation, error) {
	if d.IsRunning() {
		return nil, fmt.Errorf("Instance is running")
	}

	// Prevent concurrent operations the instance.
	op, err := operationlock.Create(d.Project().Name, d.Name(), operationlock.ActionCreate, false, false)
	if err != nil {
		return nil, err
	}

	return op, err
}

// DeviceEventHandler handles events occurring on the instance's devices.
func (d *qemu) DeviceEventHandler(runConf *deviceConfig.RunConfig) error {
	if !d.IsRunning() || runConf == nil {
		return nil
	}

	// Handle uevents.
	for _, uevent := range runConf.Uevents {
		for _, event := range uevent {
			fields := strings.SplitN(event, "=", 2)

			if fields[0] != "ACTION" {
				continue
			}

			switch fields[1] {
			case "add":
				for _, usbDev := range runConf.USBDevice {
					// This ensures that the device is actually removed from QEMU before adding it again.
					// In most cases the device will already be removed, but it is possible that the
					// device still exists in QEMU before trying to add it again.
					// If a USB device is physically detached from a running VM while the server
					// itself is stopped, QEMU in theory will not delete the device.
					err := d.deviceDetachUSB(usbDev)
					if err != nil {
						return err
					}

					err = d.deviceAttachUSB(usbDev)
					if err != nil {
						return err
					}
				}
			case "remove":
				for _, usbDev := range runConf.USBDevice {
					err := d.deviceDetachUSB(usbDev)
					if err != nil {
						return err
					}
				}
			}
		}
	}

	// Handle disk reconfiguration.
	for _, mount := range runConf.Mounts {
		if mount.Limits == nil {
			continue
		}

		// Get the QMP monitor.
		m, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
		if err != nil {
			return err
		}

		// Figure out the QEMU device ID.
		devID := fmt.Sprintf("%s%s", qemuDeviceIDPrefix, linux.PathNameEncode(mount.DevName))

		// Apply the limits.
		err = m.SetBlockThrottle(devID, int(mount.Limits.ReadBytes), int(mount.Limits.WriteBytes), int(mount.Limits.ReadIOps), int(mount.Limits.WriteIOps))
		if err != nil {
			return fmt.Errorf("Failed applying limits for disk device %q: %w", mount.DevName, err)
		}
	}

	return nil
}

// reservedVsockID returns true if the given vsockID equals 0, 1 or 2.
// Those are reserved and we cannot use them.
func (d *qemu) reservedVsockID(vsockID uint32) bool {
	return vsockID <= 2
}

// getVsockID returns the vsock Context ID for the VM.
func (d *qemu) getVsockID() (uint32, error) {
	existingVsockID, ok := d.localConfig["volatile.vsock_id"]
	if !ok {
		return 0, fmt.Errorf("Context ID not set in volatile.vsock_id")
	}

	vsockID, err := strconv.ParseUint(existingVsockID, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("Failed to parse volatile.vsock_id: %q: %w", existingVsockID, err)
	}

	if d.reservedVsockID(uint32(vsockID)) {
		return 0, fmt.Errorf("Failed to use reserved vsock Context ID: %q", vsockID)
	}

	return uint32(vsockID), nil
}

// acquireVsockID tries to occupy the given vsock Context ID.
// If the ID is free it returns the corresponding file handle.
func (d *qemu) acquireVsockID(vsockID uint32) (*os.File, error) {
	revert := revert.New()
	defer revert.Fail()

	vsockF, err := os.OpenFile("/dev/vhost-vsock", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("Failed to open vhost socket: %w", err)
	}

	revert.Add(func() { _ = vsockF.Close() })

	// The vsock Context ID cannot be supplied as type uint32.
	vsockIDInt := int(vsockID)

	// 0x4008AF60 = VHOST_VSOCK_SET_GUEST_CID = _IOW(VHOST_VIRTIO, 0x60, __u64)
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, vsockF.Fd(), 0x4008AF60, uintptr(unsafe.Pointer(&vsockIDInt)))
	if errno != 0 {
		if !errors.Is(errno, unix.EADDRINUSE) {
			return nil, fmt.Errorf("Failed ioctl syscall to vhost socket: %q", errno.Error())
		}

		// vsock Context ID is already in use.
		return nil, nil
	}

	revert.Success()
	return vsockF, nil
}

// acquireExistingVsockID tries to acquire an already existing vsock Context ID from volatile.
// It returns both the acquired ID and opened vsock file handle for QEMU.
func (d *qemu) acquireExistingVsockID() (uint32, *os.File, error) {
	vsockID, err := d.getVsockID()
	if err != nil {
		return 0, nil, err
	}

	// Check if the vsockID from last VM start is still not acquired in case the VM was stopped.
	f, err := d.acquireVsockID(vsockID)
	if err != nil {
		return 0, nil, err
	}

	return vsockID, f, nil
}

// nextVsockID tries to acquire the next free vsock Context ID for the VM.
// It returns both the acquired ID and opened vsock file handle for QEMU.
func (d *qemu) nextVsockID() (uint32, *os.File, error) {
	// Check if vsock ID from last VM start is present in volatile, then use that.
	// This allows a running VM to be recovered after DB record deletion and that an agent connection still works
	// after the VM's instance ID has changed.
	// Continue in case of error since the caller requires a valid vsockID in any case.
	vsockID, vsockF, _ := d.acquireExistingVsockID()
	if vsockID != 0 && vsockF != nil {
		return vsockID, vsockF, nil
	}

	// Ignore the error from before and start to acquire a new Context ID.
	instanceUUID, err := uuid.Parse(d.localConfig["volatile.uuid"])
	if err != nil {
		return 0, nil, fmt.Errorf("Failed to parse instance UUID from volatile.uuid: %w", err)
	}

	r, err := localUtil.GetStableRandomGenerator(instanceUUID.String())
	if err != nil {
		return 0, nil, fmt.Errorf("Failed generating stable random seed from instance UUID %q: %w", instanceUUID, err)
	}

	timeout := time.Now().Add(5 * time.Second)

	// Try to find a new Context ID.
	for {
		if time.Now().After(timeout) {
			return 0, nil, fmt.Errorf("Timeout exceeded whilst trying to acquire the next vsock Context ID")
		}

		candidateVsockID := r.Uint32()

		if d.reservedVsockID(candidateVsockID) {
			continue
		}

		vsockF, err := d.acquireVsockID(candidateVsockID)
		if err != nil {
			return 0, nil, err
		}

		if vsockF != nil {
			return candidateVsockID, vsockF, nil
		}
	}
}

// InitPID returns the instance's current process ID.
func (d *qemu) InitPID() int {
	pid, _ := d.pid()
	return pid
}

func (d *qemu) statusCode() api.StatusCode {
	// Shortcut to avoid spamming QMP during ongoing operations.
	op := operationlock.Get(d.Project().Name, d.Name())
	if op != nil {
		if op.Action() == operationlock.ActionStart {
			return api.Stopped
		}

		if op.Action() == operationlock.ActionStop {
			if util.IsTrue(d.LocalConfig()["volatile.last_state.ready"]) {
				return api.Ready
			}

			return api.Running
		}
	}

	// Connect to the monitor.
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
	if err != nil {
		// If cannot connect to monitor, but qemu process in pid file still exists, then likely qemu
		// is unresponsive and this instance is in an error state.
		pid, _ := d.pid()
		if pid > 0 {
			return api.Error
		}

		// If we fail to connect, chances are the VM isn't running.
		return api.Stopped
	}

	status, err := monitor.Status()
	if err != nil {
		if err == qmp.ErrMonitorDisconnect {
			// If cannot connect to monitor, but qemu process in pid file still exists, then likely
			// qemu is unresponsive and this instance is in an error state.
			pid, _ := d.pid()
			if pid > 0 {
				return api.Error
			}

			return api.Stopped
		}

		return api.Error
	}

	switch status {
	case "prelaunch", "running":
		if status == "running" && util.IsTrue(d.LocalConfig()["volatile.last_state.ready"]) {
			return api.Ready
		}

		return api.Running
	case "inmigrate", "postmigrate", "finish-migrate", "save-vm", "suspended", "paused":
		return api.Frozen
	default:
		return api.Error
	}
}

// State returns the instance's state code.
func (d *qemu) State() string {
	return strings.ToUpper(d.statusCode().String())
}

// EarlyLogFilePath returns the instance's early log path.
func (d *qemu) EarlyLogFilePath() string {
	return filepath.Join(d.LogPath(), "qemu.early.log")
}

// LogFilePath returns the instance's log path.
func (d *qemu) LogFilePath() string {
	return filepath.Join(d.LogPath(), "qemu.log")
}

// FillNetworkDevice takes a nic or infiniband device type and enriches it with automatically
// generated name and hwaddr properties if these are missing from the device.
func (d *qemu) FillNetworkDevice(name string, m deviceConfig.Device) (deviceConfig.Device, error) {
	var err error

	newDevice := m.Clone()

	nicType, err := nictype.NICType(d.state, d.Project().Name, m)
	if err != nil {
		return nil, err
	}

	// Fill in the MAC address.
	if !slices.Contains([]string{"physical", "ipvlan"}, nicType) && m["hwaddr"] == "" {
		configKey := fmt.Sprintf("volatile.%s.hwaddr", name)
		volatileHwaddr := d.localConfig[configKey]
		if volatileHwaddr == "" {
			// Generate a new MAC address.
			volatileHwaddr, err = instance.DeviceNextInterfaceHWAddr()
			if err != nil || volatileHwaddr == "" {
				return nil, fmt.Errorf("Failed generating %q: %w", configKey, err)
			}

			// Update the database and update volatileHwaddr with stored value.
			volatileHwaddr, err = d.insertConfigkey(configKey, volatileHwaddr)
			if err != nil {
				return nil, fmt.Errorf("Failed storing generated config key %q: %w", configKey, err)
			}

			// Set stored value into current instance config.
			d.localConfig[configKey] = volatileHwaddr
			d.expandedConfig[configKey] = volatileHwaddr
		}

		if volatileHwaddr == "" {
			return nil, fmt.Errorf("Failed getting %q", configKey)
		}

		newDevice["hwaddr"] = volatileHwaddr
	}

	return newDevice, nil
}

// UpdateBackupFile writes the instance's backup.yaml file to storage.
func (d *qemu) UpdateBackupFile() error {
	pool, err := d.getStoragePool()
	if err != nil {
		return err
	}

	return pool.UpdateInstanceBackupFile(d, true, nil)
}

type cpuTopology struct {
	sockets int
	cores   int
	threads int
	vcpus   map[uint64]uint64
	nodes   map[uint64][]uint64
}

// cpuTopology takes the CPU limit and computes the QEMU CPU topology.
func (d *qemu) cpuTopology(limit string) (*cpuTopology, error) {
	topology := &cpuTopology{}

	// Set default to 1 vCPU.
	if limit == "" {
		limit = "1"
	}

	// Check if pinned or floating.
	nrLimit, err := strconv.Atoi(limit)
	if err == nil {
		// We're not dealing with a pinned setup.
		topology.sockets = 1
		topology.cores = nrLimit
		topology.threads = 1

		return topology, nil
	}

	// Get CPU topology.
	cpus, err := resources.GetCPU()
	if err != nil {
		return nil, err
	}

	// Expand the pins.
	pins, err := resources.ParseCpuset(limit)
	if err != nil {
		return nil, err
	}

	// Match tracking.
	vcpus := map[uint64]uint64{}
	sockets := map[uint64][]uint64{}
	cores := map[uint64][]uint64{}
	numaNodes := map[uint64][]uint64{}

	// Go through the physical CPUs looking for matches.
	i := uint64(0)
	for _, cpu := range cpus.Sockets {
		for _, core := range cpu.Cores {
			for _, thread := range core.Threads {
				for _, pin := range pins {
					if thread.ID == int64(pin) {
						// Found a matching CPU.
						vcpus[i] = uint64(pin)

						// Track cores per socket.
						_, ok := sockets[cpu.Socket]
						if !ok {
							sockets[cpu.Socket] = []uint64{}
						}

						if !slices.Contains(sockets[cpu.Socket], core.Core) {
							sockets[cpu.Socket] = append(sockets[cpu.Socket], core.Core)
						}

						// Track threads per core.
						_, ok = cores[core.Core]
						if !ok {
							cores[core.Core] = []uint64{}
						}

						if !slices.Contains(cores[core.Core], thread.Thread) {
							cores[core.Core] = append(cores[core.Core], thread.Thread)
						}

						// Record NUMA node for thread.
						_, ok = cores[core.Core]
						if !ok {
							numaNodes[thread.NUMANode] = []uint64{}
						}

						numaNodes[thread.NUMANode] = append(numaNodes[thread.NUMANode], i)

						i++
					}
				}
			}
		}
	}

	// Confirm we're getting the expected number of CPUs.
	if len(pins) != len(vcpus) {
		return nil, fmt.Errorf("Unavailable CPUs requested: %s", limit)
	}

	// Validate the topology.
	valid := true
	nrSockets := 0
	nrCores := 0
	nrThreads := 0

	// Confirm that there is no balancing inconsistencies.
	countCores := -1
	for _, cores := range sockets {
		if countCores != -1 && len(cores) != countCores {
			valid = false
			break
		}

		countCores = len(cores)
	}

	countThreads := -1
	for _, threads := range cores {
		if countThreads != -1 && len(threads) != countThreads {
			valid = false
			break
		}

		countThreads = len(threads)
	}

	// Check against double listing of CPU.
	if len(sockets)*countCores*countThreads != len(vcpus) {
		valid = false
	}

	// Build up the topology.
	if valid {
		// Valid topology.
		nrSockets = len(sockets)
		nrCores = countCores
		nrThreads = countThreads
	} else {
		d.logger.Warn("Instance uses a CPU pinning profile which doesn't match hardware layout")

		// Fallback on pretending everything are cores.
		nrSockets = 1
		nrCores = len(vcpus)
		nrThreads = 1
	}

	// Prepare struct.
	topology.sockets = nrSockets
	topology.cores = nrCores
	topology.threads = nrThreads
	topology.vcpus = vcpus
	topology.nodes = numaNodes

	return topology, nil
}

func (d *qemu) devIncusEventSend(eventType string, eventMessage map[string]any) error {
	event := jmap.Map{}
	event["type"] = eventType
	event["timestamp"] = time.Now()
	event["metadata"] = eventMessage

	client, err := d.getAgentClient()
	if err != nil {
		// Don't fail if the VM simply doesn't have an agent.
		if err == errQemuAgentOffline {
			return nil
		}

		return err
	}

	agentArgs := &incus.ConnectionArgs{SkipGetServer: true}
	agent, err := incus.ConnectIncusHTTP(agentArgs, client)
	if err != nil {
		d.logger.Error("Failed to connect to the agent", logger.Ctx{"err": err})
		return fmt.Errorf("Failed to connect to the agent")
	}

	defer agent.Disconnect()

	_, _, err = agent.RawQuery("POST", "/1.0/events", &event, "")
	if err != nil {
		return err
	}

	return nil
}

// Info returns "qemu" and the currently loaded qemu version.
func (d *qemu) Info() instance.Info {
	data := instance.Info{
		Name:     "qemu",
		Features: make(map[string]any),
		Type:     instancetype.VM,
		Error:    fmt.Errorf("Unknown error"),
	}

	if !util.PathExists("/dev/kvm") {
		data.Error = fmt.Errorf("KVM support is missing (no /dev/kvm)")
		return data
	}

	err := linux.LoadModule("vhost_vsock")
	if err != nil {
		data.Error = fmt.Errorf("vhost_vsock kernel module not loaded")
		return data
	}

	if !util.PathExists("/dev/vsock") {
		data.Error = fmt.Errorf("Vsock support is missing (no /dev/vsock)")
		return data
	}

	hostArch, err := osarch.ArchitectureGetLocalID()
	if err != nil {
		logger.Errorf("Failed getting CPU architecture during QEMU initialization: %v", err)
		data.Error = fmt.Errorf("Failed getting CPU architecture")
		return data
	}

	qemuPath, _, err := d.qemuArchConfig(hostArch)
	if err != nil {
		data.Error = fmt.Errorf("QEMU command not available: %v", err)
		return data
	}

	out, err := exec.Command(qemuPath, "--version").Output()
	if err != nil {
		logger.Errorf("Failed getting version during QEMU initialization: %v", err)
		data.Error = fmt.Errorf("Failed getting QEMU version")
		return data
	}

	qemuOutput := strings.Fields(string(out))
	if len(qemuOutput) >= 4 {
		qemuVersion := strings.Fields(string(out))[3]
		data.Version = qemuVersion
	} else {
		data.Version = "unknown" // Not necessarily an error that should prevent us using driver.
	}

	data.Features, err = d.checkFeatures(hostArch, qemuPath)
	if err != nil {
		logger.Errorf("Unable to run feature checks during QEMU initialization: %v", err)
		data.Error = fmt.Errorf("QEMU failed to run feature checks")
		return data
	}

	data.Error = nil

	return data
}

func (d *qemu) checkFeatures(hostArch int, qemuPath string) (map[string]any, error) {
	monitorPath, err := os.CreateTemp("", "")
	if err != nil {
		return nil, err
	}

	defer func() { _ = os.Remove(monitorPath.Name()) }()

	qemuArgs := []string{
		qemuPath,
		"-S", // Do not start virtualisation.
		"-nographic",
		"-nodefaults",
		"-no-user-config",
		"-chardev", fmt.Sprintf("socket,id=monitor,path=%s,server=on,wait=off", monitorPath.Name()),
		"-mon", "chardev=monitor,mode=control",
		"-machine", qemuMachineType(hostArch),
	}

	if hostArch == osarch.ARCH_64BIT_INTEL_X86 {
		// On Intel, use KVM acceleration as it's needed for SEV detection.
		// This also happens to be less resource intensive but can't
		// trivially be performed on all architectures without extra care about the
		// machine type.
		qemuArgs = append(qemuArgs, "-accel", "kvm")
	}

	if d.architectureSupportsUEFI(hostArch) {
		// Try to locate a UEFI firmware.
		var ovmfPath string
		for _, entry := range ovmfGenericFirmwares {
			if util.PathExists(filepath.Join(d.ovmfPath(), entry.code)) {
				ovmfPath = filepath.Join(d.ovmfPath(), entry.code)
				break
			}
		}

		if ovmfPath == "" {
			return nil, fmt.Errorf("Unable to locate a UEFI firmware")
		}

		qemuArgs = append(qemuArgs, "-drive", fmt.Sprintf("if=pflash,format=raw,readonly=on,file=%s", ovmfPath))
	}

	var stderr bytes.Buffer

	checkFeature := exec.Cmd{
		Path:   qemuPath,
		Args:   qemuArgs,
		Stderr: &stderr,
	}

	err = checkFeature.Start()
	if err != nil {
		// QEMU not operational. VM support missing.
		return nil, fmt.Errorf("Failed starting QEMU: %w", err)
	}

	defer func() { _ = checkFeature.Process.Kill() }()

	// Start go routine that waits for QEMU to exit and captures the exit error (if any).
	errWaitCh := make(chan error, 1)
	go func() {
		errWaitCh <- checkFeature.Wait()
	}()

	// Start go routine that tries to connect to QEMU's QMP socket in a loop (giving QEMU a chance to open it).
	ctx, cancelMonitorConnect := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelMonitorConnect()

	errMonitorCh := make(chan error, 1)
	var monitor *qmp.Monitor
	go func() {
		var err error

		// Try and connect to QMP socket until cancelled.
		for {
			monitor, err = qmp.Connect(monitorPath.Name(), qemuSerialChardevName, nil)
			// QMP successfully connected or we have been cancelled.
			if err == nil || ctx.Err() != nil {
				break
			}

			time.Sleep(50 * time.Millisecond)
		}

		// Return last QMP connection error.
		errMonitorCh <- err
	}()

	// Wait for premature QEMU exit or QMP to connect or timeout.
	select {
	case errMonitor := <-errMonitorCh:
		// A non-nil error here means that QMP failed to connect before timing out.
		// The last connection error is returned.
		// A nil error means QMP successfully connected and we can continue.
		if errMonitor != nil {
			return nil, fmt.Errorf("QEMU monitor connect error: %w", errMonitor)
		}

	case errWait := <-errWaitCh:
		// Any sort of premature exit, even a non-error one is problematic here, and should not occur.
		return nil, fmt.Errorf("QEMU premature exit: %w (%v)", errWait, strings.TrimSpace(stderr.String()))
	}

	defer monitor.Disconnect()

	features := make(map[string]any)

	blockDevPath, err := os.CreateTemp("", "")
	if err != nil {
		return nil, err
	}

	defer func() { _ = os.Remove(blockDevPath.Name()) }()

	// Check io_uring feature.
	blockDev := map[string]any{
		"node-name": d.blockNodeName("feature-check"),
		"driver":    "file",
		"filename":  blockDevPath.Name(),
		"aio":       "io_uring",
	}

	err = monitor.AddBlockDevice(blockDev, nil)
	if err != nil {
		logger.Debug("Failed adding block device during VM feature check", logger.Ctx{"err": err})
	} else {
		features["io_uring"] = struct{}{}
	}

	// Check CPU hotplug feature.
	_, err = monitor.QueryHotpluggableCPUs()
	if err != nil {
		logger.Debug("Failed querying hotpluggable CPUs during VM feature check", logger.Ctx{"err": err})
	} else {
		features["cpu_hotplug"] = struct{}{}
	}

	// Check AMD SEV features (only for x86 architecture)
	if hostArch == osarch.ARCH_64BIT_INTEL_X86 {
		cmdline, err := os.ReadFile("/proc/cmdline")
		if err != nil {
			return nil, err
		}

		parts := strings.Split(string(cmdline), " ")

		// Check if SME is enabled in the kernel command line.
		if slices.Contains(parts, "mem_encrypt=on") {
			features["sme"] = struct{}{}
		}

		// Check if SEV/SEV-ES are enabled
		sev, err := os.ReadFile("/sys/module/kvm_amd/parameters/sev")
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		} else if strings.TrimSpace(string(sev)) == "Y" {
			// Host supports SEV, check if QEMU supports it as well.
			capabilities, err := monitor.SEVCapabilities()
			if err != nil {
				logger.Debug("Failed querying SEV capability during VM feature check", logger.Ctx{"err": err})
			} else {
				features["sev"] = capabilities

				// If SEV is enabled on host and supported by QEMU,
				// check if the SEV-ES extension is enabled.
				sevES, err := os.ReadFile("/sys/module/kvm_amd/parameters/sev_es")
				if err != nil {
					logger.Debug("Failed querying SEV-ES capability during VM feature check", logger.Ctx{"err": err})
				} else if strings.TrimSpace(string(sevES)) == "Y" {
					features["sev-es"] = struct{}{}
				}
			}
		}
	}

	// Check if vhost-net accelerator (for NIC CPU offloading) is available.
	if util.PathExists("/dev/vhost-net") {
		features["vhost_net"] = struct{}{}
	}

	return features, nil
}

// version returns the QEMU version.
func (d *qemu) version() (*version.DottedVersion, error) {
	info := DriverStatuses()[instancetype.VM].Info
	qemuVer, err := version.NewDottedVersion(info.Version)
	if err != nil {
		return nil, fmt.Errorf("Failed parsing QEMU version: %w", err)
	}

	return qemuVer, nil
}

func (d *qemu) Metrics(hostInterfaces []net.Interface) (*metrics.MetricSet, error) {
	if !d.IsRunning() {
		return nil, ErrInstanceIsStopped
	}

	if d.agentMetricsEnabled() {
		metrics, err := d.getAgentMetrics()
		if err != nil {
			if !errors.Is(err, errQemuAgentOffline) {
				d.logger.Warn("Could not get VM metrics from agent", logger.Ctx{"err": err})
			}

			// Fallback data if agent is not reachable.
			return d.getQemuMetrics()
		}

		return metrics, nil
	}

	return d.getQemuMetrics()
}

func (d *qemu) getAgentMetrics() (*metrics.MetricSet, error) {
	client, err := d.getAgentClient()
	if err != nil {
		return nil, err
	}

	agentArgs := &incus.ConnectionArgs{SkipGetServer: true}
	agent, err := incus.ConnectIncusHTTP(agentArgs, client)
	if err != nil {
		d.logger.Error("Failed to connect to the agent", logger.Ctx{"project": d.Project().Name, "instance": d.Name(), "err": err})
		return nil, fmt.Errorf("Failed to connect to the agent")
	}

	defer agent.Disconnect()

	resp, _, err := agent.RawQuery("GET", "/1.0/metrics", nil, "")
	if err != nil {
		return nil, err
	}

	var m metrics.Metrics

	err = json.Unmarshal(resp.Metadata, &m)
	if err != nil {
		return nil, err
	}

	metricSet, err := metrics.MetricSetFromAPI(&m, map[string]string{"project": d.project.Name, "name": d.name, "type": instancetype.VM.String()})
	if err != nil {
		return nil, err
	}

	return metricSet, nil
}

func (d *qemu) getNetworkState() (map[string]api.InstanceStateNetwork, error) {
	networks := map[string]api.InstanceStateNetwork{}
	for k, m := range d.ExpandedDevices() {
		if m["type"] != "nic" {
			continue
		}

		dev, err := d.deviceLoad(d, k, m)
		if err != nil {
			if errors.Is(err, device.ErrUnsupportedDevType) {
				continue // Skip unsupported device (allows for mixed instance type profiles).
			}

			d.logger.Warn("Failed state validation for device", logger.Ctx{"device": k, "err": err})
			continue
		}

		// Only some NIC types support fallback state mechanisms when there is no agent.
		nic, ok := dev.(device.NICState)
		if !ok {
			continue
		}

		network, err := nic.State()
		if err != nil {
			return nil, fmt.Errorf("Failed getting NIC state for %q: %w", k, err)
		}

		if network != nil {
			networks[k] = *network
		}
	}

	return networks, nil
}

func (d *qemu) agentMetricsEnabled() bool {
	return util.IsTrueOrEmpty(d.expandedConfig["security.agent.metrics"])
}

func (d *qemu) deviceAttachUSB(usbConf deviceConfig.USBDeviceItem) error {
	// Check if the agent is running.
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
	if err != nil {
		return err
	}

	monHook, err := d.addUSBDeviceConfig(usbConf)
	if err != nil {
		return err
	}

	err = monHook(monitor)
	if err != nil {
		return err
	}

	return nil
}

func (d *qemu) deviceDetachUSB(usbDev deviceConfig.USBDeviceItem) error {
	// Check if the agent is running.
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
	if err != nil {
		return err
	}

	deviceID := fmt.Sprintf("%s%s", qemuDeviceIDPrefix, usbDev.DeviceName)

	err = monitor.RemoveDevice(deviceID)
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return fmt.Errorf("Failed removing device: %w", err)
	}

	err = monitor.RemoveFDFromFDSet(deviceID)
	if err != nil {
		return fmt.Errorf("Failed removing FD set: %w", err)
	}

	return nil
}

// Block node names may only be up to 31 characters long, so use a hash if longer.
func (d *qemu) blockNodeName(name string) string {
	if len(name) > 27 {
		// If the name is too long, hash it as SHA-256 (32 bytes).
		// Then encode the SHA-256 binary hash as Base64 Raw URL format and trim down to 27 chars.
		// Raw URL avoids the use of "+" character and the padding "=" character which QEMU doesn't allow.
		hash := sha256.New()
		hash.Write([]byte(name))
		binaryHash := hash.Sum(nil)
		name = base64.RawURLEncoding.EncodeToString(binaryHash)
		name = name[0:27]
	}

	// Apply the prefix.
	return fmt.Sprintf("%s%s", qemuBlockDevIDPrefix, name)
}

func (d *qemu) setCPUs(count int) error {
	if count == 0 {
		return nil
	}

	// Check if the agent is running.
	monitor, err := qmp.Connect(d.monitorPath(), qemuSerialChardevName, d.getMonitorEventHandler())
	if err != nil {
		return err
	}

	cpus, err := monitor.QueryHotpluggableCPUs()
	if err != nil {
		return fmt.Errorf("Failed to query hotpluggable CPUs: %w", err)
	}

	var availableCPUs []qmp.HotpluggableCPU
	var hotpluggedCPUs []qmp.HotpluggableCPU

	// Count the available and hotplugged CPUs.
	for _, cpu := range cpus {
		// If qom-path is unset, the CPU is available.
		if cpu.QOMPath == "" {
			availableCPUs = append(availableCPUs, cpu)
		} else if strings.HasPrefix(cpu.QOMPath, "/machine/peripheral") {
			hotpluggedCPUs = append(hotpluggedCPUs, cpu)
		}
	}

	// The reserved CPUs includes both the hotplugged CPUs as well as the fixed one.
	totalReservedCPUs := len(hotpluggedCPUs) + 1

	// Nothing to do as the count matches the already reserved CPUs.
	if count == totalReservedCPUs {
		return nil
	}

	revert := revert.New()
	defer revert.Fail()

	// More CPUs requested.
	if count > totalReservedCPUs {
		// Cannot allocate more CPUs than the system provides.
		if count > len(cpus) {
			return fmt.Errorf("Cannot allocate more CPUs than available")
		}

		// This shouldn't trigger, but if it does, don't panic.
		if count-totalReservedCPUs > len(availableCPUs) {
			return fmt.Errorf("Unable to allocate more CPUs, not enough hotpluggable CPUs available")
		}

		// Only allocate the difference in CPUs.
		for i := 0; i < count-totalReservedCPUs; i++ {
			cpu := availableCPUs[i]

			devID := fmt.Sprintf("cpu%d%d%d", cpu.Props.SocketID, cpu.Props.CoreID, cpu.Props.ThreadID)

			dev := map[string]string{
				"id":      devID,
				"driver":  cpu.Type,
				"core-id": fmt.Sprintf("%d", cpu.Props.CoreID),
			}

			// No such thing as sockets and threads on s390x.
			if d.architecture != osarch.ARCH_64BIT_S390_BIG_ENDIAN {
				dev["socket-id"] = fmt.Sprintf("%d", cpu.Props.SocketID)
				dev["thread-id"] = fmt.Sprintf("%d", cpu.Props.ThreadID)
			}

			err := monitor.AddDevice(dev)
			if err != nil {
				return fmt.Errorf("Failed to add device: %w", err)
			}

			revert.Add(func() {
				err := monitor.RemoveDevice(devID)
				d.logger.Warn("Failed to remove CPU device", logger.Ctx{"err": err})
			})
		}
	} else {
		if totalReservedCPUs-count > len(hotpluggedCPUs) {
			// This shouldn't trigger, but if it does, don't panic.
			return fmt.Errorf("Unable to remove CPUs, not enough hotpluggable CPUs available")
		}

		// Less CPUs requested.
		for i := 0; i < totalReservedCPUs-count; i++ {
			cpu := hotpluggedCPUs[i]

			fields := strings.Split(cpu.QOMPath, "/")
			devID := fields[len(fields)-1]

			err := monitor.RemoveDevice(devID)
			if err != nil {
				return fmt.Errorf("Failed to remove CPU: %w", err)
			}

			revert.Add(func() {
				err := monitor.AddDevice(map[string]string{
					"id":        devID,
					"driver":    cpu.Type,
					"socket-id": fmt.Sprintf("%d", cpu.Props.SocketID),
					"core-id":   fmt.Sprintf("%d", cpu.Props.CoreID),
					"thread-id": fmt.Sprintf("%d", cpu.Props.ThreadID),
				})
				d.logger.Warn("Failed to add CPU device", logger.Ctx{"err": err})
			})
		}

		// QEMU doesn't immediately remove the thread from the vCPU list.
		// Wait a second to allow the thread to fully exit and disappear from the vCPU list.
		time.Sleep(time.Second)
	}

	revert.Success()

	// Run post-hotplug tasks.
	err = d.postCPUHotplug(monitor)
	if err != nil {
		return err
	}

	return nil
}

func (d *qemu) architectureSupportsCPUHotplug() bool {
	// Check supported features.
	info := DriverStatuses()[instancetype.VM].Info
	_, found := info.Features["cpu_hotplug"]
	return found
}

func (d *qemu) postCPUHotplug(monitor *qmp.Monitor) error {
	// Get the vCPU PID list.
	pids, err := monitor.GetCPUs()
	if err != nil {
		return err
	}

	// Handle NUMA node restrictions.
	numaNodes := d.expandedConfig["limits.cpu.nodes"]
	if numaNodes != "" {
		if numaNodes == "balanced" {
			numaNodes = d.expandedConfig["volatile.cpu.nodes"]
		}

		// Parse the NUMA restriction.
		numaNodeSet, err := resources.ParseNumaNodeSet(numaNodes)
		if err != nil {
			return err
		}

		// Get the CPU topology.
		cpusTopology, err := resources.GetCPU()
		if err != nil {
			return err
		}

		// Get the isolated CPU ids.
		isolatedCpusInt := resources.GetCPUIsolated()

		// Build a map of NUMA node to CPU threads.
		numaNodeToCPU := make(map[int64][]int64)
		for _, cpu := range cpusTopology.Sockets {
			for _, core := range cpu.Cores {
				for _, thread := range core.Threads {
					// Skip any isolated CPU thread.
					if slices.Contains(isolatedCpusInt, thread.ID) {
						continue
					}

					numaNodeToCPU[int64(thread.NUMANode)] = append(numaNodeToCPU[int64(thread.NUMANode)], thread.ID)
				}
			}
		}

		// Figure out the list of CPU threads for the NUMA node(s).
		set := unix.CPUSet{}
		for _, numaNode := range numaNodeSet {
			for _, id := range numaNodeToCPU[numaNode] {
				set.Set(int(id))
			}
		}

		// Apply the restriction.
		for _, pid := range pids {
			// Apply the pin.
			err := unix.SchedSetaffinity(pid, &set)
			if err != nil {
				return err
			}
		}
	}

	// Create a core scheduling group.
	err = d.setCoreSched(pids)
	if err != nil {
		return fmt.Errorf("Failed to allocate new core scheduling domain for vCPU threads: %w", err)
	}

	return nil
}
