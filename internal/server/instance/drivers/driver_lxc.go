package drivers

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/checkpoint-restore/go-criu/v6/crit"
	"github.com/flosch/pongo2"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	liblxc "github.com/lxc/go-lxc"
	"github.com/pkg/sftp"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/proto"
	yaml "gopkg.in/yaml.v2"

	internalInstance "github.com/lxc/incus/internal/instance"
	"github.com/lxc/incus/internal/instancewriter"
	internalIO "github.com/lxc/incus/internal/io"
	"github.com/lxc/incus/internal/jmap"
	"github.com/lxc/incus/internal/linux"
	"github.com/lxc/incus/internal/migration"
	"github.com/lxc/incus/internal/netutils"
	"github.com/lxc/incus/internal/revert"
	"github.com/lxc/incus/internal/rsync"
	"github.com/lxc/incus/internal/server/apparmor"
	"github.com/lxc/incus/internal/server/cgroup"
	"github.com/lxc/incus/internal/server/daemon"
	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/db/cluster"
	"github.com/lxc/incus/internal/server/db/operationtype"
	"github.com/lxc/incus/internal/server/device"
	deviceConfig "github.com/lxc/incus/internal/server/device/config"
	"github.com/lxc/incus/internal/server/device/nictype"
	"github.com/lxc/incus/internal/server/instance"
	"github.com/lxc/incus/internal/server/instance/instancetype"
	"github.com/lxc/incus/internal/server/instance/operationlock"
	"github.com/lxc/incus/internal/server/lifecycle"
	"github.com/lxc/incus/internal/server/locking"
	"github.com/lxc/incus/internal/server/metrics"
	localMigration "github.com/lxc/incus/internal/server/migration"
	"github.com/lxc/incus/internal/server/network"
	"github.com/lxc/incus/internal/server/operations"
	"github.com/lxc/incus/internal/server/project"
	"github.com/lxc/incus/internal/server/response"
	"github.com/lxc/incus/internal/server/seccomp"
	"github.com/lxc/incus/internal/server/state"
	storagePools "github.com/lxc/incus/internal/server/storage"
	storageDrivers "github.com/lxc/incus/internal/server/storage/drivers"
	"github.com/lxc/incus/internal/server/template"
	localUtil "github.com/lxc/incus/internal/server/util"
	internalUtil "github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/idmap"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/osarch"
	"github.com/lxc/incus/shared/subprocess"
	"github.com/lxc/incus/shared/termios"
	"github.com/lxc/incus/shared/units"
	"github.com/lxc/incus/shared/util"
	"github.com/lxc/incus/shared/ws"
)

// Helper functions.
func lxcSetConfigItem(c *liblxc.Container, key string, value string) error {
	if c == nil {
		return fmt.Errorf("Uninitialized go-lxc struct")
	}

	if !liblxc.RuntimeLiblxcVersionAtLeast(liblxc.Version(), 2, 1, 0) {
		switch key {
		case "lxc.uts.name":
			key = "lxc.utsname"
		case "lxc.pty.max":
			key = "lxc.pts"
		case "lxc.tty.dir":
			key = "lxc.devttydir"
		case "lxc.tty.max":
			key = "lxc.tty"
		case "lxc.apparmor.profile":
			key = "lxc.aa_profile"
		case "lxc.apparmor.allow_incomplete":
			key = "lxc.aa_allow_incomplete"
		case "lxc.selinux.context":
			key = "lxc.se_context"
		case "lxc.mount.fstab":
			key = "lxc.mount"
		case "lxc.console.path":
			key = "lxc.console"
		case "lxc.seccomp.profile":
			key = "lxc.seccomp"
		case "lxc.signal.halt":
			key = "lxc.haltsignal"
		case "lxc.signal.reboot":
			key = "lxc.rebootsignal"
		case "lxc.signal.stop":
			key = "lxc.stopsignal"
		case "lxc.log.syslog":
			key = "lxc.syslog"
		case "lxc.log.level":
			key = "lxc.loglevel"
		case "lxc.log.file":
			key = "lxc.logfile"
		case "lxc.init.cmd":
			key = "lxc.init_cmd"
		case "lxc.init.uid":
			key = "lxc.init_uid"
		case "lxc.init.gid":
			key = "lxc.init_gid"
		case "lxc.idmap":
			key = "lxc.id_map"
		}
	}

	if strings.HasPrefix(key, "lxc.prlimit.") {
		if !liblxc.RuntimeLiblxcVersionAtLeast(liblxc.Version(), 2, 1, 0) {
			return fmt.Errorf(`Process limits require liblxc >= 2.1`)
		}
	}

	err := c.SetConfigItem(key, value)
	if err != nil {
		return fmt.Errorf("Failed to set LXC config: %s=%s", key, value)
	}

	return nil
}

func lxcStatusCode(state liblxc.State) api.StatusCode {
	return map[int]api.StatusCode{
		1: api.Stopped,
		2: api.Starting,
		3: api.Running,
		4: api.Stopping,
		5: api.Aborting,
		6: api.Freezing,
		7: api.Frozen,
		8: api.Thawed,
		9: api.Error,
	}[int(state)]
}

// lxcCreate creates the DB storage records and sets up instance devices.
// Returns a revert fail function that can be used to undo this function if a subsequent step fails.
func lxcCreate(s *state.State, args db.InstanceArgs, p api.Project) (instance.Instance, revert.Hook, error) {
	revert := revert.New()
	defer revert.Fail()

	// Create the container struct
	d := &lxc{
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

	// Cleanup the zero values
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
	err := d.init()
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

	// Setup the initial idmap config.
	var idmapSet *idmap.Set
	base := int64(0)
	if !d.IsPrivileged() {
		idmapSet, base, err = findIdmap(
			s,
			args.Name,
			d.expandedConfig["security.idmap.isolated"],
			d.expandedConfig["security.idmap.base"],
			d.expandedConfig["security.idmap.size"],
			d.expandedConfig["raw.idmap"],
		)

		if err != nil {
			return nil, nil, err
		}
	}

	idmapSetJSON, err := idmapSet.ToJSON()
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to encode ID map: %w", err)
	}

	v := map[string]string{
		"volatile.idmap.next": idmapSetJSON,
		"volatile.idmap.base": fmt.Sprintf("%v", base),
	}

	// Invalidate the idmap cache.
	d.idmapset = nil

	// Set last_state if not currently set.
	if d.localConfig["volatile.last_state.idmap"] == "" {
		v["volatile.last_state.idmap"] = "[]"
	}

	err = d.VolatileSet(v)
	if err != nil {
		return nil, nil, err
	}

	// Re-run init to update the idmap.
	err = d.init()
	if err != nil {
		return nil, nil, err
	}

	if !d.IsSnapshot() {
		// Add devices to container.
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
		// Add instance to authorizer.
		err = d.state.Authorizer.AddInstance(d.state.ShutdownCtx, d.project.Name, d.Name())
		if err != nil {
			logger.Error("Failed to add instance to authorizer", logger.Ctx{"instanceName": d.Name(), "projectName": d.project.Name, "error": err})
		}

		revert.Add(func() { d.state.Authorizer.DeleteInstance(d.state.ShutdownCtx, d.project.Name, d.Name()) })

		d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceCreated.Event(d, map[string]any{
			"type":         api.InstanceTypeContainer,
			"storage-pool": d.storagePool.Name(),
			"location":     d.Location(),
		}))
	}

	cleanup := revert.Clone().Fail
	revert.Success()
	return d, cleanup, err
}

func lxcLoad(s *state.State, args db.InstanceArgs, p api.Project) (instance.Instance, error) {
	// Create the container struct
	d := lxcInstantiate(s, args, nil, p)

	// Expand config and devices
	err := d.(*lxc).expandConfig()
	if err != nil {
		return nil, err
	}

	return d, nil
}

// Unload is called by the garbage collector.
func lxcUnload(d *lxc) {
	d.release()
}

// release releases any internal reference to a liblxc container, invalidating the go-lxc cache.
func (d *lxc) release() {
	d.cMu.Lock()
	defer d.cMu.Unlock()

	if d.c != nil {
		_ = d.c.Release()
		d.c = nil
	}
}

// Create a container struct without initializing it.
func lxcInstantiate(s *state.State, args db.InstanceArgs, expandedDevices deviceConfig.Devices, p api.Project) instance.Instance {
	d := &lxc{
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

	// Cleanup the zero values
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

// The LXC container driver.
type lxc struct {
	common

	// Config handling.
	fromHook bool

	cMu        sync.Mutex
	cFinalizer sync.Once

	// Cached handles.
	// Do not use these variables directly, instead use their associated get functions so they
	// will be initialised on demand.
	c *liblxc.Container // Use d.initLXC() instead of accessing this directly.

	cConfig  bool
	idmapset *idmap.Set
}

func idmapSize(state *state.State, isolatedStr string, size string) (int64, error) {
	isolated := false
	if util.IsTrue(isolatedStr) {
		isolated = true
	}

	var idMapSize int64
	if size == "" || size == "auto" {
		if isolated {
			idMapSize = 65536
		} else {
			if len(state.OS.IdmapSet.Entries) != 2 {
				return 0, fmt.Errorf("Bad initial idmap: %v", state.OS.IdmapSet)
			}

			idMapSize = state.OS.IdmapSet.Entries[0].MapRange
		}
	} else {
		size, err := strconv.ParseInt(size, 10, 64)
		if err != nil {
			return 0, err
		}

		idMapSize = size
	}

	return idMapSize, nil
}

var idmapLock sync.Mutex

func findIdmap(s *state.State, cName string, isolatedStr string, configBase string, configSize string, rawIdmap string) (*idmap.Set, int64, error) {
	isolated := false
	if util.IsTrue(isolatedStr) {
		isolated = true
	}

	rawMaps, err := idmap.NewSetFromIncusIDMap(rawIdmap)
	if err != nil {
		return nil, 0, err
	}

	if !isolated {
		newIdmapset := idmap.Set{Entries: make([]idmap.Entry, len(s.OS.IdmapSet.Entries))}
		copy(newIdmapset.Entries, s.OS.IdmapSet.Entries)

		for _, ent := range rawMaps.Entries {
			err := newIdmapset.AddSafe(ent)
			if err != nil && err == idmap.ErrHostIDIsSubID {
				return nil, 0, err
			}
		}

		return &newIdmapset, 0, nil
	}

	size, err := idmapSize(s, isolatedStr, configSize)
	if err != nil {
		return nil, 0, err
	}

	mkIdmap := func(offset int64, size int64) (*idmap.Set, error) {
		set := &idmap.Set{Entries: []idmap.Entry{
			{IsUID: true, NSID: 0, HostID: offset, MapRange: size},
			{IsGID: true, NSID: 0, HostID: offset, MapRange: size},
		}}

		for _, ent := range rawMaps.Entries {
			err := set.AddSafe(ent)
			if err != nil && err == idmap.ErrHostIDIsSubID {
				return nil, err
			}
		}

		return set, nil
	}

	if configBase != "" {
		offset, err := strconv.ParseInt(configBase, 10, 64)
		if err != nil {
			return nil, 0, err
		}

		set, err := mkIdmap(offset, size)
		if err != nil && err == idmap.ErrHostIDIsSubID {
			return nil, 0, err
		}

		return set, offset, nil
	}

	idmapLock.Lock()
	defer idmapLock.Unlock()

	cts, err := instance.LoadNodeAll(s, instancetype.Container)
	if err != nil {
		return nil, 0, err
	}

	offset := s.OS.IdmapSet.Entries[0].HostID + 65536

	mapentries := idmap.ByHostID{}
	for _, container := range cts {
		if container.Type() != instancetype.Container {
			continue
		}

		name := container.Name()

		/* Don't change our map Just Because. */
		if name == cName {
			continue
		}

		if container.IsPrivileged() {
			continue
		}

		if util.IsFalseOrEmpty(container.ExpandedConfig()["security.idmap.isolated"]) {
			continue
		}

		cBase := int64(0)
		if container.ExpandedConfig()["volatile.idmap.base"] != "" {
			cBase, err = strconv.ParseInt(container.ExpandedConfig()["volatile.idmap.base"], 10, 64)
			if err != nil {
				return nil, 0, err
			}
		}

		cSize, err := idmapSize(s, container.ExpandedConfig()["security.idmap.isolated"], container.ExpandedConfig()["security.idmap.size"])
		if err != nil {
			return nil, 0, err
		}

		mapentries.Entries = append(mapentries.Entries, idmap.Entry{HostID: int64(cBase), MapRange: cSize})
	}

	sort.Sort(mapentries)

	for i := range mapentries.Entries {
		if i == 0 {
			if mapentries.Entries[0].HostID < offset+size {
				offset = mapentries.Entries[0].HostID + mapentries.Entries[0].MapRange
				continue
			}

			set, err := mkIdmap(offset, size)
			if err != nil && err == idmap.ErrHostIDIsSubID {
				return nil, 0, err
			}

			return set, offset, nil
		}

		if mapentries.Entries[i-1].HostID+mapentries.Entries[i-1].MapRange > offset {
			offset = mapentries.Entries[i-1].HostID + mapentries.Entries[i-1].MapRange
			continue
		}

		offset = mapentries.Entries[i-1].HostID + mapentries.Entries[i-1].MapRange
		if offset+size < mapentries.Entries[i].HostID {
			set, err := mkIdmap(offset, size)
			if err != nil && err == idmap.ErrHostIDIsSubID {
				return nil, 0, err
			}

			return set, offset, nil
		}

		offset = mapentries.Entries[i].HostID + mapentries.Entries[i].MapRange
	}

	if offset+size < s.OS.IdmapSet.Entries[0].HostID+s.OS.IdmapSet.Entries[0].MapRange {
		set, err := mkIdmap(offset, size)
		if err != nil && err == idmap.ErrHostIDIsSubID {
			return nil, 0, err
		}

		return set, offset, nil
	}

	return nil, 0, fmt.Errorf("Not enough uid/gid available for the container")
}

func (d *lxc) init() error {
	// Compute the expanded config and device list
	err := d.expandConfig()
	if err != nil {
		return err
	}

	return nil
}

func (d *lxc) initLXC(config bool) (*liblxc.Container, error) {
	d.cMu.Lock()
	defer d.cMu.Unlock()

	// No need to go through all that for snapshots
	if d.IsSnapshot() {
		return nil, nil
	}

	// Check if being called from a hook
	if d.fromHook {
		return nil, fmt.Errorf("You can't use go-lxc from inside a LXC hook")
	}

	// Check if already initialized
	if d.c != nil && (!config || d.cConfig) {
		return d.c, nil
	}

	// As we are now going to be initialising a liblxc.Container reference, set the finalizer so that it is
	// cleaned up (if needed) when the garbage collector destroys this instance struct.
	d.cFinalizer.Do(func() { runtime.SetFinalizer(d, lxcUnload) })

	revert := revert.New()
	defer revert.Fail()

	// Load the go-lxc struct
	cname := project.Instance(d.Project().Name, d.Name())
	cc, err := liblxc.NewContainer(cname, d.state.OS.LxcPath)
	if err != nil {
		return nil, err
	}

	revert.Add(func() {
		_ = cc.Release()
	})

	// Load cgroup abstraction
	cg, err := d.cgroup(cc, false)
	if err != nil {
		return nil, err
	}

	// Setup logging
	logfile := d.LogFilePath()
	err = lxcSetConfigItem(cc, "lxc.log.file", logfile)
	if err != nil {
		return nil, err
	}

	logLevel := "warn"
	if daemon.Debug {
		logLevel = "trace"
	} else if daemon.Verbose {
		logLevel = "info"
	}

	err = lxcSetConfigItem(cc, "lxc.log.level", logLevel)
	if err != nil {
		return nil, err
	}

	if liblxc.RuntimeLiblxcVersionAtLeast(liblxc.Version(), 3, 0, 0) {
		// Default size log buffer
		err = lxcSetConfigItem(cc, "lxc.console.buffer.size", "auto")
		if err != nil {
			return nil, err
		}

		err = lxcSetConfigItem(cc, "lxc.console.size", "auto")
		if err != nil {
			return nil, err
		}

		// File to dump ringbuffer contents to when requested or
		// container shutdown.
		consoleBufferLogFile := d.ConsoleBufferLogPath()
		err = lxcSetConfigItem(cc, "lxc.console.logfile", consoleBufferLogFile)
		if err != nil {
			return nil, err
		}
	}

	if d.state.OS.ContainerCoreScheduling {
		err = lxcSetConfigItem(cc, "lxc.sched.core", "1")
		if err != nil {
			return nil, err
		}
	} else if d.state.OS.CoreScheduling {
		err = lxcSetConfigItem(cc, "lxc.hook.start-host", fmt.Sprintf("/proc/%d/exe forkcoresched 1", os.Getpid()))
		if err != nil {
			return nil, err
		}
	}

	// Allow for lightweight init
	d.cConfig = config
	if !config {
		if d.c != nil {
			_ = d.c.Release()
		}

		d.c = cc

		revert.Success()
		return cc, err
	}

	if d.IsPrivileged() {
		// Base config
		toDrop := "sys_time sys_module sys_rawio"
		if !d.state.OS.AppArmorStacking || d.state.OS.AppArmorStacked {
			toDrop = toDrop + " mac_admin mac_override"
		}

		err = lxcSetConfigItem(cc, "lxc.cap.drop", toDrop)
		if err != nil {
			return nil, err
		}
	}

	// Set an appropriate /proc, /sys/ and /sys/fs/cgroup
	mounts := []string{}
	if d.IsPrivileged() && !d.state.OS.RunningInUserNS {
		mounts = append(mounts, "proc:mixed")
		mounts = append(mounts, "sys:mixed")
	} else {
		mounts = append(mounts, "proc:rw")
		mounts = append(mounts, "sys:rw")
	}

	cgInfo := cgroup.GetInfo()
	if cgInfo.Namespacing {
		if cgInfo.Layout == cgroup.CgroupsUnified {
			mounts = append(mounts, "cgroup:rw:force")
		} else {
			mounts = append(mounts, "cgroup:mixed")
		}
	} else {
		mounts = append(mounts, "cgroup:mixed")
	}

	err = lxcSetConfigItem(cc, "lxc.mount.auto", strings.Join(mounts, " "))
	if err != nil {
		return nil, err
	}

	err = lxcSetConfigItem(cc, "lxc.autodev", "1")
	if err != nil {
		return nil, err
	}

	err = lxcSetConfigItem(cc, "lxc.pty.max", "1024")
	if err != nil {
		return nil, err
	}

	bindMounts := []string{
		"/dev/fuse",
		"/dev/net/tun",
		"/proc/sys/fs/binfmt_misc",
		"/sys/firmware/efi/efivars",
		"/sys/fs/fuse/connections",
		"/sys/fs/pstore",
		"/sys/kernel/config",
		"/sys/kernel/debug",
		"/sys/kernel/security",
		"/sys/kernel/tracing",
	}

	// Pass in /dev/zfs to the container if delegation is supported on the system.
	// This is only done for unprivileged containers as delegation is tied to the user namespace.
	if !d.IsPrivileged() && storageDrivers.ZFSSupportsDelegation() && util.PathExists("/dev/zfs") {
		bindMounts = append(bindMounts, "/dev/zfs")
	}

	if d.IsPrivileged() && !d.state.OS.RunningInUserNS {
		err = lxcSetConfigItem(cc, "lxc.mount.entry", "mqueue dev/mqueue mqueue rw,relatime,create=dir,optional 0 0")
		if err != nil {
			return nil, err
		}
	} else {
		bindMounts = append(bindMounts, "/dev/mqueue")
	}

	for _, mnt := range bindMounts {
		if !util.PathExists(mnt) {
			continue
		}

		if internalUtil.IsDir(mnt) {
			err = lxcSetConfigItem(cc, "lxc.mount.entry", fmt.Sprintf("%s %s none rbind,create=dir,optional 0 0", mnt, strings.TrimPrefix(mnt, "/")))
			if err != nil {
				return nil, err
			}
		} else {
			err = lxcSetConfigItem(cc, "lxc.mount.entry", fmt.Sprintf("%s %s none bind,create=file,optional 0 0", mnt, strings.TrimPrefix(mnt, "/")))
			if err != nil {
				return nil, err
			}
		}
	}

	// For lxcfs
	templateConfDir := os.Getenv("INCUS_LXC_TEMPLATE_CONFIG")
	if templateConfDir == "" {
		templateConfDir = "/usr/share/lxc/config"
	}

	if util.PathExists(fmt.Sprintf("%s/common.conf.d/", templateConfDir)) {
		err = lxcSetConfigItem(cc, "lxc.include", fmt.Sprintf("%s/common.conf.d/", templateConfDir))
		if err != nil {
			return nil, err
		}
	}

	// Configure devices cgroup
	if d.IsPrivileged() && !d.state.OS.RunningInUserNS && d.state.OS.CGInfo.Supports(cgroup.Devices, cg) {
		if d.state.OS.CGInfo.Layout == cgroup.CgroupsUnified {
			err = lxcSetConfigItem(cc, "lxc.cgroup2.devices.deny", "a")
		} else {
			err = lxcSetConfigItem(cc, "lxc.cgroup.devices.deny", "a")
		}

		if err != nil {
			return nil, err
		}

		devices := []string{
			"b *:* m",      // Allow mknod of block devices
			"c *:* m",      // Allow mknod of char devices
			"c 136:* rwm",  // /dev/pts devices
			"c 1:3 rwm",    // /dev/null
			"c 1:5 rwm",    // /dev/zero
			"c 1:7 rwm",    // /dev/full
			"c 1:8 rwm",    // /dev/random
			"c 1:9 rwm",    // /dev/urandom
			"c 5:0 rwm",    // /dev/tty
			"c 5:1 rwm",    // /dev/console
			"c 5:2 rwm",    // /dev/ptmx
			"c 10:229 rwm", // /dev/fuse
			"c 10:200 rwm", // /dev/net/tun
		}

		if storageDrivers.ZFSSupportsDelegation() {
			devices = append(devices, "c 10:249 rwm")
		}

		for _, dev := range devices {
			if d.state.OS.CGInfo.Layout == cgroup.CgroupsUnified {
				err = lxcSetConfigItem(cc, "lxc.cgroup2.devices.allow", dev)
			} else {
				err = lxcSetConfigItem(cc, "lxc.cgroup.devices.allow", dev)
			}

			if err != nil {
				return nil, err
			}
		}
	}

	if d.IsNesting() {
		/*
		 * mount extra /proc and /sys to work around kernel
		 * restrictions on remounting them when covered
		 */
		err = lxcSetConfigItem(cc, "lxc.mount.entry", "proc dev/.lxc/proc proc create=dir,optional 0 0")
		if err != nil {
			return nil, err
		}

		err = lxcSetConfigItem(cc, "lxc.mount.entry", "sys dev/.lxc/sys sysfs create=dir,optional 0 0")
		if err != nil {
			return nil, err
		}
	}

	// Setup architecture
	personality, err := osarch.ArchitecturePersonality(d.architecture)
	if err != nil {
		personality, err = osarch.ArchitecturePersonality(d.state.OS.Architectures[0])
		if err != nil {
			return nil, err
		}
	}

	err = lxcSetConfigItem(cc, "lxc.arch", personality)
	if err != nil {
		return nil, err
	}

	// Setup the hooks
	err = lxcSetConfigItem(cc, "lxc.hook.version", "1")
	if err != nil {
		return nil, err
	}

	// Call the onstart hook on start.
	err = lxcSetConfigItem(cc, "lxc.hook.pre-start", fmt.Sprintf("/proc/%d/exe callhook %s %s %s start", os.Getpid(), internalUtil.VarPath(""), strconv.Quote(d.Project().Name), strconv.Quote(d.Name())))
	if err != nil {
		return nil, err
	}

	// Call the onstopns hook on stop but before namespaces are unmounted.
	err = lxcSetConfigItem(cc, "lxc.hook.stop", fmt.Sprintf("%s callhook %s %s %s stopns", d.state.OS.ExecPath, internalUtil.VarPath(""), strconv.Quote(d.Project().Name), strconv.Quote(d.Name())))
	if err != nil {
		return nil, err
	}

	// Call the onstop hook on stop.
	err = lxcSetConfigItem(cc, "lxc.hook.post-stop", fmt.Sprintf("%s callhook %s %s %s stop", d.state.OS.ExecPath, internalUtil.VarPath(""), strconv.Quote(d.Project().Name), strconv.Quote(d.Name())))
	if err != nil {
		return nil, err
	}

	// Setup the console
	err = lxcSetConfigItem(cc, "lxc.tty.max", "0")
	if err != nil {
		return nil, err
	}

	// Setup the hostname
	err = lxcSetConfigItem(cc, "lxc.uts.name", d.Name())
	if err != nil {
		return nil, err
	}

	// Setup devIncus
	if util.IsTrueOrEmpty(d.expandedConfig["security.guestapi"]) {
		err = lxcSetConfigItem(cc, "lxc.mount.entry", fmt.Sprintf("%s dev/incus none bind,create=dir 0 0", internalUtil.VarPath("guestapi")))
		if err != nil {
			return nil, err
		}
	}

	// Setup AppArmor
	if d.state.OS.AppArmorAvailable {
		if d.state.OS.AppArmorConfined || !d.state.OS.AppArmorAdmin {
			// If confined but otherwise able to use AppArmor, use our own profile
			curProfile := localUtil.AppArmorProfile()
			curProfile = strings.TrimSuffix(curProfile, " (enforce)")
			err := lxcSetConfigItem(cc, "lxc.apparmor.profile", curProfile)
			if err != nil {
				return nil, err
			}
		} else {
			// If not currently confined, use the container's profile
			profile := apparmor.InstanceProfileName(d)

			/* In the nesting case, we want to enable the inside
			 * daemon to load its profile. Unprivileged containers can
			 * load profiles, but privileged containers cannot, so
			 * let's not use a namespace so they can fall back to
			 * the old way of nesting, i.e. using the parent's
			 * profile.
			 */
			if d.state.OS.AppArmorStacking && !d.state.OS.AppArmorStacked {
				profile = fmt.Sprintf("%s//&:%s:", profile, apparmor.InstanceNamespaceName(d))
			}

			err := lxcSetConfigItem(cc, "lxc.apparmor.profile", profile)
			if err != nil {
				return nil, err
			}
		}
	} else {
		// Make sure that LXC won't try to apply an apparmor profile.
		// This may fail on liblxc compiled without apparmor, so ignore errors.
		_ = lxcSetConfigItem(cc, "lxc.apparmor.profile", "unconfined")
	}

	// Setup Seccomp if necessary
	if seccomp.InstanceNeedsPolicy(d) {
		err = lxcSetConfigItem(cc, "lxc.seccomp.profile", seccomp.ProfilePath(d))
		if err != nil {
			return nil, err
		}

		// Setup notification socket
		// System requirement errors are handled during policy generation instead of here
		ok, err := seccomp.InstanceNeedsIntercept(d.state, d)
		if err == nil && ok {
			err = lxcSetConfigItem(cc, "lxc.seccomp.notify.proxy", fmt.Sprintf("unix:%s", internalUtil.RunPath("seccomp.socket")))
			if err != nil {
				return nil, err
			}
		}
	}

	// Setup idmap
	idmapset, err := d.NextIdmap()
	if err != nil {
		return nil, err
	}

	if idmapset != nil {
		lines := idmapset.ToLXCString()
		for _, line := range lines {
			err := lxcSetConfigItem(cc, "lxc.idmap", line)
			if err != nil {
				return nil, err
			}
		}
	}

	// Setup environment
	for k, v := range d.expandedConfig {
		// gendoc:generate(entity=instance, group=miscellaneous, key=environment.*)
		// The specified key/value environment variables are exported to the instance and set for `incus exec`.

		// ---
		//  type: string
		//  liveupdate: yes (exec)
		//  shortdesc: Environment variables to export
		if strings.HasPrefix(k, "environment.") {
			err = lxcSetConfigItem(cc, "lxc.environment", fmt.Sprintf("%s=%s", strings.TrimPrefix(k, "environment."), v))
			if err != nil {
				return nil, err
			}
		}
	}

	// Setup NVIDIA runtime
	if util.IsTrue(d.expandedConfig["nvidia.runtime"]) {
		hookDir := os.Getenv("INCUS_LXC_HOOK")
		if hookDir == "" {
			hookDir = "/usr/share/lxc/hooks"
		}

		hookPath := filepath.Join(hookDir, "nvidia")
		if !util.PathExists(hookPath) {
			return nil, fmt.Errorf("The NVIDIA LXC hook couldn't be found")
		}

		_, err := exec.LookPath("nvidia-container-cli")
		if err != nil {
			return nil, fmt.Errorf("The NVIDIA container tools couldn't be found")
		}

		err = lxcSetConfigItem(cc, "lxc.environment", "NVIDIA_VISIBLE_DEVICES=none")
		if err != nil {
			return nil, err
		}

		nvidiaDriver := d.expandedConfig["nvidia.driver.capabilities"]
		if nvidiaDriver == "" {
			err = lxcSetConfigItem(cc, "lxc.environment", "NVIDIA_DRIVER_CAPABILITIES=compute,utility")
			if err != nil {
				return nil, err
			}
		} else {
			err = lxcSetConfigItem(cc, "lxc.environment", fmt.Sprintf("NVIDIA_DRIVER_CAPABILITIES=%s", nvidiaDriver))
			if err != nil {
				return nil, err
			}
		}

		nvidiaRequireCuda := d.expandedConfig["nvidia.require.cuda"]
		if nvidiaRequireCuda == "" {
			err = lxcSetConfigItem(cc, "lxc.environment", fmt.Sprintf("NVIDIA_REQUIRE_CUDA=%s", nvidiaRequireCuda))
			if err != nil {
				return nil, err
			}
		}

		nvidiaRequireDriver := d.expandedConfig["nvidia.require.driver"]
		if nvidiaRequireDriver == "" {
			err = lxcSetConfigItem(cc, "lxc.environment", fmt.Sprintf("NVIDIA_REQUIRE_DRIVER=%s", nvidiaRequireDriver))
			if err != nil {
				return nil, err
			}
		}

		err = lxcSetConfigItem(cc, "lxc.hook.mount", hookPath)
		if err != nil {
			return nil, err
		}
	}

	// Memory limits
	if d.state.OS.CGInfo.Supports(cgroup.Memory, cg) {
		memory := d.expandedConfig["limits.memory"]
		memoryEnforce := d.expandedConfig["limits.memory.enforce"]
		memorySwap := d.expandedConfig["limits.memory.swap"]
		memorySwapPriority := d.expandedConfig["limits.memory.swap.priority"]

		// Configure the memory limits
		if memory != "" {
			var valueInt int64
			if strings.HasSuffix(memory, "%") {
				percent, err := strconv.ParseInt(strings.TrimSuffix(memory, "%"), 10, 64)
				if err != nil {
					return nil, err
				}

				memoryTotal, err := linux.DeviceTotalMemory()
				if err != nil {
					return nil, err
				}

				valueInt = int64((memoryTotal / 100) * percent)
			} else {
				valueInt, err = units.ParseByteSizeString(memory)
				if err != nil {
					return nil, err
				}
			}

			if memoryEnforce == "soft" {
				err = cg.SetMemorySoftLimit(valueInt)
				if err != nil {
					return nil, err
				}
			} else {
				if d.state.OS.CGInfo.Supports(cgroup.MemorySwap, cg) {
					err = cg.SetMemoryLimit(valueInt)
					if err != nil {
						return nil, err
					}

					if util.IsFalse(memorySwap) {
						err = cg.SetMemorySwapLimit(0)
						if err != nil {
							return nil, err
						}
					} else {
						err = cg.SetMemorySwapLimit(valueInt)
						if err != nil {
							return nil, err
						}
					}
				} else {
					err = cg.SetMemoryLimit(valueInt)
					if err != nil {
						return nil, err
					}
				}

				// Set soft limit to value 10% less than hard limit
				err = cg.SetMemorySoftLimit(int64(float64(valueInt) * 0.9))
				if err != nil {
					return nil, err
				}
			}
		}

		if d.state.OS.CGInfo.Supports(cgroup.MemorySwappiness, cg) {
			// Configure the swappiness
			if util.IsFalse(memorySwap) {
				err = cg.SetMemorySwappiness(0)
				if err != nil {
					return nil, err
				}
			} else if memorySwapPriority != "" {
				priority, err := strconv.Atoi(memorySwapPriority)
				if err != nil {
					return nil, err
				}

				// Maximum priority (10) should be default swappiness (60).
				err = cg.SetMemorySwappiness(int64(70 - priority))
				if err != nil {
					return nil, err
				}
			}
		}
	}

	// CPU limits
	cpuPriority := d.expandedConfig["limits.cpu.priority"]
	cpuAllowance := d.expandedConfig["limits.cpu.allowance"]

	if (cpuPriority != "" || cpuAllowance != "") && d.state.OS.CGInfo.Supports(cgroup.CPU, cg) {
		cpuShares, cpuCfsQuota, cpuCfsPeriod, err := cgroup.ParseCPU(cpuAllowance, cpuPriority)
		if err != nil {
			return nil, err
		}

		if cpuShares != 1024 {
			err = cg.SetCPUShare(cpuShares)
			if err != nil {
				return nil, err
			}
		}

		if cpuCfsPeriod != -1 && cpuCfsQuota != -1 {
			err = cg.SetCPUCfsLimit(cpuCfsPeriod, cpuCfsQuota)
			if err != nil {
				return nil, err
			}
		}
	}

	// Disk priority limits.
	diskPriority := d.ExpandedConfig()["limits.disk.priority"]
	if diskPriority != "" {
		if d.state.OS.CGInfo.Supports(cgroup.BlkioWeight, nil) {
			priorityInt, err := strconv.Atoi(diskPriority)
			if err != nil {
				return nil, err
			}

			priority := priorityInt * 100

			// Minimum valid value is 10
			if priority == 0 {
				priority = 10
			}

			err = cg.SetBlkioWeight(int64(priority))
			if err != nil {
				return nil, err
			}
		} else {
			return nil, fmt.Errorf("Cannot apply limits.disk.priority as blkio.weight cgroup controller is missing")
		}
	}

	// Processes
	if d.state.OS.CGInfo.Supports(cgroup.Pids, cg) {
		processes := d.expandedConfig["limits.processes"]
		if processes != "" {
			valueInt, err := strconv.ParseInt(processes, 10, 64)
			if err != nil {
				return nil, err
			}

			err = cg.SetMaxProcesses(valueInt)
			if err != nil {
				return nil, err
			}
		}
	}

	// Hugepages
	if d.state.OS.CGInfo.Supports(cgroup.Hugetlb, cg) {
		for i, key := range internalInstance.HugePageSizeKeys {
			value := d.expandedConfig[key]
			if value != "" {
				value, err := units.ParseByteSizeString(value)
				if err != nil {
					return nil, err
				}

				err = cg.SetHugepagesLimit(internalInstance.HugePageSizeSuffix[i], value)
				if err != nil {
					return nil, err
				}
			}
		}
	}

	// Setup process limits
	for k, v := range d.expandedConfig {
		if strings.HasPrefix(k, "limits.kernel.") {
			prlimitSuffix := strings.TrimPrefix(k, "limits.kernel.")
			prlimitKey := fmt.Sprintf("lxc.prlimit.%s", prlimitSuffix)
			err = lxcSetConfigItem(cc, prlimitKey, v)
			if err != nil {
				return nil, err
			}
		}
	}

	// Setup sysctls
	for k, v := range d.expandedConfig {
		// gendoc:generate(entity=instance, group=miscellaneous, key=linux.sysctl.*)
		//
		// ---
		//  type: string
		//  liveupdate: no
		//  condition: container
		//  shortdesc: Override for the corresponding `sysctl` setting in the container
		if strings.HasPrefix(k, "linux.sysctl.") {
			sysctlSuffix := strings.TrimPrefix(k, "linux.sysctl.")
			sysctlKey := fmt.Sprintf("lxc.sysctl.%s", sysctlSuffix)
			err = lxcSetConfigItem(cc, sysctlKey, v)
			if err != nil {
				return nil, err
			}
		}
	}

	// Setup shmounts
	if d.state.OS.LXCFeatures["mount_injection_file"] {
		err = lxcSetConfigItem(cc, "lxc.mount.auto", fmt.Sprintf("shmounts:%s:/dev/.incus-mounts", d.ShmountsPath()))
	} else {
		err = lxcSetConfigItem(cc, "lxc.mount.entry", fmt.Sprintf("%s dev/.incus-mounts none bind,create=dir 0 0", d.ShmountsPath()))
	}

	if err != nil {
		return nil, err
	}

	if d.c != nil {
		_ = d.c.Release()
	}

	d.c = cc
	revert.Success()
	return cc, err
}

var idmappedStorageMap map[unix.Fsid]idmap.IdmapStorageType = map[unix.Fsid]idmap.IdmapStorageType{}
var idmappedStorageMapString map[string]idmap.IdmapStorageType = map[string]idmap.IdmapStorageType{}
var idmappedStorageMapLock sync.Mutex

// IdmappedStorage determines if the container can use idmapped mounts.
func (d *lxc) IdmappedStorage(path string, fstype string) idmap.IdmapStorageType {
	var mode idmap.IdmapStorageType = idmap.IdmapStorageNone
	var bindMount bool = fstype == "none" || fstype == ""

	if !d.state.OS.LXCFeatures["idmapped_mounts_v2"] || !d.state.OS.IdmappedMounts {
		return mode
	}

	buf := &unix.Statfs_t{}

	if bindMount {
		err := unix.Statfs(path, buf)
		if err != nil {
			d.logger.Error("Failed to statfs", logger.Ctx{"path": path, "err": err})
			return mode
		}
	}

	idmappedStorageMapLock.Lock()
	defer idmappedStorageMapLock.Unlock()

	if bindMount {
		val, ok := idmappedStorageMap[buf.Fsid]
		if ok {
			// Return recorded idmapping type.
			return val
		}
	} else {
		val, ok := idmappedStorageMapString[fstype]
		if ok {
			// Return recorded idmapping type.
			return val
		}
	}

	if idmap.CanIdmapMount(path, fstype) {
		// Use idmapped mounts.
		mode = idmap.IdmapStorageIdmapped
	}

	if bindMount {
		idmappedStorageMap[buf.Fsid] = mode
	} else {
		idmappedStorageMapString[fstype] = mode
	}

	return mode
}

func (d *lxc) devIncusEventSend(eventType string, eventMessage map[string]any) error {
	event := jmap.Map{}
	event["type"] = eventType
	event["timestamp"] = time.Now()
	event["metadata"] = eventMessage

	return d.state.DevIncusEvents.Send(d.ID(), eventType, eventMessage)
}

// RegisterDevices calls the Register() function on all of the instance's devices.
func (d *lxc) RegisterDevices() {
	d.devicesRegister(d)
}

// deviceStart loads a new device and calls its Start() function.
func (d *lxc) deviceStart(dev device.Device, instanceRunning bool) (*deviceConfig.RunConfig, error) {
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

	// If runConf supplied, perform any container specific setup of device.
	if runConf != nil {
		// Shift device file ownership if needed before mounting into container.
		// This needs to be done whether or not container is running.
		if len(runConf.Mounts) > 0 {
			err := d.deviceStaticShiftMounts(runConf.Mounts)
			if err != nil {
				return nil, err
			}
		}

		// If container is running and then live attach device.
		if instanceRunning {
			// Attach mounts if requested.
			if len(runConf.Mounts) > 0 {
				err = d.deviceHandleMounts(runConf.Mounts)
				if err != nil {
					return nil, err
				}
			}

			// Add cgroup rules if requested.
			if len(runConf.CGroups) > 0 {
				err = d.deviceAddCgroupRules(runConf.CGroups)
				if err != nil {
					return nil, err
				}
			}

			// Attach network interface if requested.
			if len(runConf.NetworkInterface) > 0 {
				err = d.deviceAttachNIC(configCopy, runConf.NetworkInterface)
				if err != nil {
					return nil, err
				}
			}

			// If running, run post start hooks now (if not running, they will be run
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

// deviceStaticShiftMounts statically shift device mount files ownership to active idmap if needed.
func (d *lxc) deviceStaticShiftMounts(mounts []deviceConfig.MountEntryItem) error {
	idmapSet, err := d.CurrentIdmap()
	if err != nil {
		return fmt.Errorf("Failed to get idmap for device: %s", err)
	}

	// If there is an idmap being applied and the daemon is not running in a user namespace then shift the
	// device files before they are mounted.
	if idmapSet != nil && !d.state.OS.RunningInUserNS {
		for _, mount := range mounts {
			// Skip UID/GID shifting if OwnerShift mode is not static, or the host-side
			// DevPath is empty (meaning an unmount request that doesn't need shifting).
			if mount.OwnerShift != deviceConfig.MountOwnerShiftStatic || mount.DevPath == "" {
				continue
			}

			err := idmapSet.ShiftPath(mount.DevPath, nil)
			if err != nil {
				// uidshift failing is weird, but not a big problem. Log and proceed.
				d.logger.Debug("Failed to uidshift device", logger.Ctx{"mountDevPath": mount.DevPath, "err": err})
			}
		}
	}

	return nil
}

// deviceAddCgroupRules live adds cgroup rules to a container.
func (d *lxc) deviceAddCgroupRules(cgroups []deviceConfig.RunConfigItem) error {
	cc, err := d.initLXC(false)
	if err != nil {
		return err
	}

	cg, err := d.cgroup(cc, true)
	if err != nil {
		return err
	}

	for _, rule := range cgroups {
		// Only apply devices cgroup rules if container is running privileged and host has devices cgroup controller.
		if strings.HasPrefix(rule.Key, "devices.") && (!d.isCurrentlyPrivileged() || d.state.OS.RunningInUserNS || !d.state.OS.CGInfo.Supports(cgroup.Devices, cg)) {
			continue
		}

		// Add the new device cgroup rule.
		err := d.CGroupSet(rule.Key, rule.Value)
		if err != nil {
			return fmt.Errorf("Failed to add cgroup rule for device: %w", err)
		}
	}

	return nil
}

// deviceAttachNIC live attaches a NIC device to a container.
func (d *lxc) deviceAttachNIC(configCopy map[string]string, netIF []deviceConfig.RunConfigItem) error {
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

	// Load the go-lxc struct.
	cc, err := d.initLXC(false)
	if err != nil {
		return err
	}

	// Add the interface to the container.
	err = cc.AttachInterface(devName, configCopy["name"])
	if err != nil {
		return fmt.Errorf("Failed to attach interface: %s to %s: %w", devName, configCopy["name"], err)
	}

	return nil
}

// deviceStop loads a new device and calls its Stop() function.
// Accepts a stopHookNetnsPath argument which is required when run from the onStopNS hook before the
// container's network namespace is unmounted (which is required for NIC device cleanup).
func (d *lxc) deviceStop(dev device.Device, instanceRunning bool, stopHookNetnsPath string) error {
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

	if runConf != nil {
		// If network interface settings returned, then detach NIC from container.
		if len(runConf.NetworkInterface) > 0 {
			err = d.deviceDetachNIC(configCopy, runConf.NetworkInterface, instanceRunning, stopHookNetnsPath)
			if err != nil {
				return err
			}
		}

		// Add cgroup rules if requested and container is running.
		if len(runConf.CGroups) > 0 && instanceRunning {
			err = d.deviceAddCgroupRules(runConf.CGroups)
			if err != nil {
				return err
			}
		}

		// Detach mounts if requested and container is running.
		if len(runConf.Mounts) > 0 && instanceRunning {
			err = d.deviceHandleMounts(runConf.Mounts)
			if err != nil {
				return err
			}
		}

		// Run post stop hooks irrespective of run state of instance.
		err = d.runHooks(runConf.PostHooks)
		if err != nil {
			return err
		}
	}

	return nil
}

// deviceDetachNIC detaches a NIC device from a container.
// Accepts a stopHookNetnsPath argument which is required when run from the onStopNS hook before the
// container's network namespace is unmounted (which is required for NIC device cleanup).
func (d *lxc) deviceDetachNIC(configCopy map[string]string, netIF []deviceConfig.RunConfigItem, instanceRunning bool, stopHookNetnsPath string) error {
	// Get requested device name to detach interface back to on the host.
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

	// If container is running, perform live detach of interface back to host.
	if instanceRunning {
		// For some reason, having network config confuses detach, so get our own go-lxc struct.
		cname := project.Instance(d.Project().Name, d.Name())
		cc, err := liblxc.NewContainer(cname, d.state.OS.LxcPath)
		if err != nil {
			return err
		}

		defer func() { _ = cc.Release() }()

		// Get interfaces inside container.
		ifaces, err := cc.Interfaces()
		if err != nil {
			return fmt.Errorf("Failed to list network interfaces: %w", err)
		}

		// If interface doesn't exist inside container, cannot proceed.
		if !util.ValueInSlice(configCopy["name"], ifaces) {
			return nil
		}

		err = cc.DetachInterfaceRename(configCopy["name"], devName)
		if err != nil {
			return fmt.Errorf("Failed to detach interface: %q to %q: %w", configCopy["name"], devName, err)
		}
	} else {
		// Currently liblxc does not move devices back to the host on stop that were added
		// after the container was started. For this reason we utilise the lxc.hook.stop
		// hook so that we can capture the netns path, enter the namespace and move the nics
		// back to the host and rename them if liblxc hasn't already done it.
		// We can only move back devices that have an expected host_name record and where
		// that device doesn't already exist on the host as if a device exists on the host
		// we can't know whether that is because liblxc has moved it back already or whether
		// it is a conflicting device.
		if !util.PathExists(fmt.Sprintf("/sys/class/net/%s", devName)) {
			if stopHookNetnsPath == "" {
				return fmt.Errorf("Cannot detach NIC device %q without stopHookNetnsPath being provided", devName)
			}

			err := d.detachInterfaceRename(stopHookNetnsPath, configCopy["name"], devName)
			if err != nil {
				return fmt.Errorf("Failed to detach interface: %q to %q: %w", configCopy["name"], devName, err)
			}

			d.logger.Debug("Detached NIC device interface", logger.Ctx{"name": configCopy["name"], "device": devName})
		}
	}

	return nil
}

// deviceHandleMounts live attaches or detaches mounts on a container.
// If the mount DevPath is empty the mount action is treated as unmount.
func (d *lxc) deviceHandleMounts(mounts []deviceConfig.MountEntryItem) error {
	for _, mount := range mounts {
		if mount.DevPath != "" {
			flags := 0

			// Convert options into flags.
			for _, opt := range mount.Opts {
				if opt == "bind" {
					flags |= unix.MS_BIND
				} else if opt == "rbind" {
					flags |= unix.MS_BIND | unix.MS_REC
				} else if opt == "ro" {
					flags |= unix.MS_RDONLY
				}
			}

			var idmapType idmap.IdmapStorageType = idmap.IdmapStorageNone
			if !d.IsPrivileged() && mount.OwnerShift == deviceConfig.MountOwnerShiftDynamic {
				idmapType = d.IdmappedStorage(mount.DevPath, mount.FSType)
				if idmapType == idmap.IdmapStorageNone {
					return fmt.Errorf("Required idmapping abilities not available")
				}
			}

			// Mount it into the container.
			err := d.insertMount(mount.DevPath, mount.TargetPath, mount.FSType, flags, idmapType)
			if err != nil {
				return fmt.Errorf("Failed to add mount for device inside container: %s", err)
			}
		} else {
			relativeTargetPath := strings.TrimPrefix(mount.TargetPath, "/")

			// Connect to files API.
			files, err := d.FileSFTP()
			if err != nil {
				return err
			}

			defer func() { _ = files.Close() }()

			_, err = files.Lstat(relativeTargetPath)
			if err == nil {
				err := d.removeMount(mount.TargetPath)
				if err != nil {
					return fmt.Errorf("Error unmounting the device path inside container: %s", err)
				}

				err = files.Remove(relativeTargetPath)
				if err != nil {
					// Only warn here and don't fail as removing a directory
					// mount may fail if there was already files inside
					// directory before it was mouted over preventing delete.
					d.logger.Warn("Could not remove the device path inside container", logger.Ctx{"err": err})
				}
			}
		}
	}

	return nil
}

// DeviceEventHandler actions the results of a RunConfig after an event has occurred on a device.
func (d *lxc) DeviceEventHandler(runConf *deviceConfig.RunConfig) error {
	// Device events can only be processed when the container is running.
	// We use InitPID here rather than IsRunning because this task can be triggered during the
	// container startup process, which is during the time that the start lock is held, which causes
	// IsRunning to return false (because the container hasn't fully started yet).
	if d.InitPID() <= 0 {
		return nil
	}

	if runConf == nil {
		return nil
	}

	// Shift device file ownership if needed before mounting devices into container.
	if len(runConf.Mounts) > 0 {
		err := d.deviceStaticShiftMounts(runConf.Mounts)
		if err != nil {
			return err
		}

		err = d.deviceHandleMounts(runConf.Mounts)
		if err != nil {
			return err
		}
	}

	// Add cgroup rules if requested.
	if len(runConf.CGroups) > 0 {
		err := d.deviceAddCgroupRules(runConf.CGroups)
		if err != nil {
			return err
		}
	}

	// Run any post hooks requested.
	err := d.runHooks(runConf.PostHooks)
	if err != nil {
		return err
	}

	// Generate uevent inside container if requested.
	if len(runConf.Uevents) > 0 {
		pidFdNr, pidFd := d.inheritInitPidFd()
		if pidFdNr >= 0 {
			defer func() { _ = pidFd.Close() }()
		}

		for _, eventParts := range runConf.Uevents {
			ueventArray := make([]string, 6)
			ueventArray[0] = "forkuevent"
			ueventArray[1] = "inject"
			ueventArray[2] = "--"
			ueventArray[3] = fmt.Sprintf("%d", d.InitPID())
			ueventArray[4] = fmt.Sprintf("%d", pidFdNr)
			length := 0
			for _, part := range eventParts {
				length = length + len(part) + 1
			}

			ueventArray[5] = fmt.Sprintf("%d", length)
			ueventArray = append(ueventArray, eventParts...)
			_, _, err := subprocess.RunCommandSplit(context.TODO(), nil, []*os.File{pidFd}, d.state.OS.ExecPath, ueventArray...)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (d *lxc) handleIdmappedStorage() (idmap.IdmapStorageType, *idmap.Set, error) {
	diskIdmap, err := d.DiskIdmap()
	if err != nil {
		return idmap.IdmapStorageNone, nil, fmt.Errorf("Set last ID map: %w", err)
	}

	nextIdmap, err := d.NextIdmap()
	if err != nil {
		return idmap.IdmapStorageNone, nil, fmt.Errorf("Set ID map: %w", err)
	}

	// Identical on-disk idmaps so no changes required.
	if nextIdmap.Equals(diskIdmap) {
		return idmap.IdmapStorageNone, nextIdmap, nil
	}

	// There's no on-disk idmap applied and the container can use idmapped
	// storage.
	idmapType := d.IdmappedStorage(d.RootfsPath(), "none")
	if diskIdmap == nil && idmapType != idmap.IdmapStorageNone {
		return idmapType, nextIdmap, nil
	}

	// We need to change the on-disk idmap but the container is protected
	// against idmap changes.
	if util.IsTrue(d.expandedConfig["security.protection.shift"]) {
		return idmap.IdmapStorageNone, nil, fmt.Errorf("Container is protected against filesystem shifting")
	}

	d.logger.Debug("Container idmap changed, remapping")
	d.updateProgress("Remapping container filesystem")

	storageType, err := d.getStorageType()
	if err != nil {
		return idmap.IdmapStorageNone, nil, fmt.Errorf("Storage type: %w", err)
	}

	// Revert the currently applied on-disk idmap.
	if diskIdmap != nil {
		if storageType == "zfs" {
			err = diskIdmap.UnshiftPath(d.RootfsPath(), storageDrivers.ShiftZFSSkipper)
		} else if storageType == "btrfs" {
			err = storageDrivers.UnshiftBtrfsRootfs(d.RootfsPath(), diskIdmap)
		} else {
			err = diskIdmap.UnshiftPath(d.RootfsPath(), nil)
		}

		if err != nil {
			return idmap.IdmapStorageNone, nil, err
		}
	}

	jsonDiskIdmap := "[]"

	// If the container can't use idmapped storage apply the new on-disk
	// idmap of the container now. Otherwise we will later instruct LXC to
	// make use of idmapped storage.
	if nextIdmap != nil && idmapType == idmap.IdmapStorageNone {
		if storageType == "zfs" {
			err = nextIdmap.ShiftPath(d.RootfsPath(), storageDrivers.ShiftZFSSkipper)
		} else if storageType == "btrfs" {
			err = storageDrivers.ShiftBtrfsRootfs(d.RootfsPath(), nextIdmap)
		} else {
			err = nextIdmap.ShiftPath(d.RootfsPath(), nil)
		}

		if err != nil {
			return idmap.IdmapStorageNone, nil, err
		}

		idmapJSON, err := nextIdmap.ToJSON()
		if err != nil {
			return idmap.IdmapStorageNone, nil, err
		}

		jsonDiskIdmap = idmapJSON
	}

	err = d.VolatileSet(map[string]string{"volatile.last_state.idmap": jsonDiskIdmap})
	if err != nil {
		return idmap.IdmapStorageNone, nextIdmap, fmt.Errorf("Set volatile.last_state.idmap config key on container %q (id %d): %w", d.name, d.id, err)
	}

	d.updateProgress("")
	return idmapType, nextIdmap, nil
}

// Start functions.
func (d *lxc) startCommon() (string, []func() error, error) {
	postStartHooks := []func() error{}

	revert := revert.New()
	defer revert.Fail()

	// Check if idmap needs changing.
	if !d.IsPrivileged() {
		nextMap, err := d.NextIdmap()
		if err != nil {
			return "", nil, err
		}

		// Check if we need to change idmap.
		if nextMap != nil && d.state.OS.IdmapSet != nil && !d.state.OS.IdmapSet.Includes(nextMap) {
			// Update the idmap.
			idmapSet, base, err := findIdmap(
				d.state,
				d.Name(),
				d.expandedConfig["security.idmap.isolated"],
				d.expandedConfig["security.idmap.base"],
				d.expandedConfig["security.idmap.size"],
				d.expandedConfig["raw.idmap"],
			)
			if err != nil {
				return "", nil, fmt.Errorf("Failed to get ID map: %w", err)
			}

			idmapSetJSON, err := idmapSet.ToJSON()
			if err != nil {
				return "", nil, fmt.Errorf("Failed to encode ID map: %w", err)
			}

			err = d.VolatileSet(map[string]string{
				"volatile.idmap.next": idmapSetJSON,
				"volatile.idmap.base": fmt.Sprintf("%v", base),
			})
			if err != nil {
				return "", nil, fmt.Errorf("Failed to update volatile idmap: %w", err)
			}

			// Invalidate the idmap cache.
			d.idmapset = nil
		}
	}

	// Load the go-lxc struct
	cc, err := d.initLXC(true)
	if err != nil {
		return "", nil, fmt.Errorf("Load go-lxc struct: %w", err)
	}

	// Ensure cgroup v1 configuration is set appropriately with the image using systemd
	if d.localConfig["image.requirements.cgroup"] == "v1" && !util.PathExists("/sys/fs/cgroup/systemd") {
		return "", nil, fmt.Errorf("The image used by this instance requires a CGroupV1 host system")
	}

	// Ensure privileged is turned off for images that cannot work privileged
	if util.IsFalse(d.localConfig["image.requirements.privileged"]) && util.IsTrue(d.expandedConfig["security.privileged"]) {
		return "", nil, fmt.Errorf("The image used by this instance is incompatible with privileged containers. Please unset security.privileged on the instance")
	}

	// Load any required kernel modules
	kernelModules := d.expandedConfig["linux.kernel_modules"]
	if kernelModules != "" {
		for _, module := range strings.Split(kernelModules, ",") {
			module = strings.TrimPrefix(module, " ")
			err := linux.LoadModule(module)
			if err != nil {
				return "", nil, fmt.Errorf("Failed to load kernel module '%s': %w", module, err)
			}
		}
	}

	// Rotate the log file.
	logfile := d.LogFilePath()
	if util.PathExists(logfile) {
		_ = os.Remove(logfile + ".old")
		err := os.Rename(logfile, logfile+".old")
		if err != nil && !os.IsNotExist(err) {
			return "", nil, err
		}
	}

	// Wait for any file operations to complete.
	// This is to avoid having an active mount by forkfile and so all file operations
	// from this point will use the container's namespace rather than a chroot.
	d.stopForkfile(false)

	// Mount instance root volume.
	mountInfo, err := d.mount()
	if err != nil {
		return "", nil, err
	}

	// Handle post hooks.
	postStartHooks = append(postStartHooks, func() error {
		for _, hook := range mountInfo.PostHooks {
			err := hook(d)
			if err != nil {
				return err
			}
		}

		return nil
	})

	revert.Add(func() { _ = d.unmount() })

	idmapType, nextIdmap, err := d.handleIdmappedStorage()
	if err != nil {
		return "", nil, fmt.Errorf("Failed to handle idmapped storage: %w", err)
	}

	nextIdmapJSON, err := nextIdmap.ToJSON()
	if err != nil {
		return "", nil, fmt.Errorf("Failed to encode ID map: %w", err)
	}

	if d.localConfig["volatile.idmap.current"] != nextIdmapJSON {
		err = d.VolatileSet(map[string]string{"volatile.idmap.current": nextIdmapJSON})
		if err != nil {
			return "", nil, fmt.Errorf("Set volatile.idmap.current config key on container %q (id %d): %w", d.name, d.id, err)
		}
	}

	// Generate the Seccomp profile
	err = seccomp.CreateProfile(d.state, d)
	if err != nil {
		return "", nil, err
	}

	// Cleanup any existing leftover devices
	_ = d.removeUnixDevices()
	_ = d.removeDiskDevices()

	// Create any missing directories.
	err = os.MkdirAll(d.LogPath(), 0700)
	if err != nil {
		return "", nil, err
	}

	err = os.MkdirAll(d.RunPath(), 0700)
	if err != nil {
		return "", nil, err
	}

	err = os.MkdirAll(d.DevicesPath(), 0711)
	if err != nil {
		return "", nil, err
	}

	err = os.MkdirAll(d.ShmountsPath(), 0711)
	if err != nil {
		return "", nil, err
	}

	volatileSet := make(map[string]string)

	// Generate UUID if not present (do this before UpdateBackupFile() call).
	instUUID := d.localConfig["volatile.uuid"]
	if instUUID == "" {
		instUUID = uuid.New().String()
		volatileSet["volatile.uuid"] = instUUID
	}

	// For a container instance, we must also set the generation UUID.
	genUUID := d.localConfig["volatile.uuid.generation"]
	if genUUID == "" {
		genUUID = instUUID
		volatileSet["volatile.uuid.generation"] = genUUID
	}

	// Apply any volatile changes that need to be made.
	err = d.VolatileSet(volatileSet)
	if err != nil {
		return "", nil, fmt.Errorf("Failed setting volatile keys: %w", err)
	}

	// Create the devices
	nicID := -1
	nvidiaDevices := []string{}

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

			return "", nil, fmt.Errorf("Failed start validation for device %q: %w", entry.Name, err)
		}

		// Run pre-start of check all devices before starting any device to avoid expensive revert.
		err = dev.PreStartCheck()
		if err != nil {
			return "", nil, fmt.Errorf("Failed pre-start check for device %q: %w", dev.Name(), err)
		}

		startDevices = append(startDevices, dev)
	}

	// Start devices in order.
	for i := range startDevices {
		dev := startDevices[i] // Local var for revert.

		// Start the device.
		runConf, err := d.deviceStart(dev, false)
		if err != nil {
			return "", nil, fmt.Errorf("Failed to start device %q: %w", dev.Name(), err)
		}

		// Stop device on failure to setup container.
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

		// Process rootfs setup.
		if runConf.RootFS.Path != "" {
			if !liblxc.RuntimeLiblxcVersionAtLeast(liblxc.Version(), 2, 1, 0) {
				// Set the rootfs backend type if supported (must happen before any other lxc.rootfs)
				err := lxcSetConfigItem(cc, "lxc.rootfs.backend", "dir")
				if err == nil {
					value := cc.ConfigItem("lxc.rootfs.backend")
					if len(value) == 0 || value[0] != "dir" {
						_ = lxcSetConfigItem(cc, "lxc.rootfs.backend", "")
					}
				}
			}

			// Get an absolute path for the rootfs (avoid constantly traversing the symlink).
			absoluteRootfs, err := filepath.EvalSymlinks(runConf.RootFS.Path)
			if err != nil {
				return "", nil, fmt.Errorf("Unable to resolve container rootfs: %w", err)
			}

			if liblxc.RuntimeLiblxcVersionAtLeast(liblxc.Version(), 2, 1, 0) {
				rootfsPath := fmt.Sprintf("dir:%s", absoluteRootfs)
				err = lxcSetConfigItem(cc, "lxc.rootfs.path", rootfsPath)
			} else {
				err = lxcSetConfigItem(cc, "lxc.rootfs", absoluteRootfs)
			}

			if err != nil {
				return "", nil, fmt.Errorf("Failed to setup device rootfs %q: %w", dev.Name(), err)
			}

			if len(runConf.RootFS.Opts) > 0 {
				err = lxcSetConfigItem(cc, "lxc.rootfs.options", strings.Join(runConf.RootFS.Opts, ","))
				if err != nil {
					return "", nil, fmt.Errorf("Failed to setup device rootfs %q: %w", dev.Name(), err)
				}
			}

			if !d.IsPrivileged() && idmapType == idmap.IdmapStorageIdmapped {
				err = lxcSetConfigItem(cc, "lxc.rootfs.options", "idmap=container")
				if err != nil {
					return "", nil, fmt.Errorf("Failed to set \"idmap=container\" rootfs option: %w", err)
				}
			}
		}

		// Pass any cgroups rules into LXC.
		if len(runConf.CGroups) > 0 {
			for _, rule := range runConf.CGroups {
				if strings.HasPrefix(rule.Key, "devices.") && (!d.isCurrentlyPrivileged() || d.state.OS.RunningInUserNS) {
					continue
				}

				if d.state.OS.CGInfo.Layout == cgroup.CgroupsUnified {
					err = lxcSetConfigItem(cc, fmt.Sprintf("lxc.cgroup2.%s", rule.Key), rule.Value)
				} else {
					err = lxcSetConfigItem(cc, fmt.Sprintf("lxc.cgroup.%s", rule.Key), rule.Value)
				}

				if err != nil {
					return "", nil, fmt.Errorf("Failed to setup device cgroup %q: %w", dev.Name(), err)
				}
			}
		}

		// Pass any mounts into LXC.
		if len(runConf.Mounts) > 0 {
			escapePathFstab := func(path string) string {
				r := strings.NewReplacer(
					" ", "\\040",
					"\t", "\\011",
					"\n", "\\012",
					"\\", "\\\\")
				return r.Replace(path)
			}

			for _, mount := range runConf.Mounts {
				if util.ValueInSlice("propagation", mount.Opts) && !liblxc.RuntimeLiblxcVersionAtLeast(liblxc.Version(), 3, 0, 0) {
					return "", nil, fmt.Errorf("Failed to setup device mount %q: %w", dev.Name(), fmt.Errorf("liblxc 3.0 is required for mount propagation configuration"))
				}

				mntOptions := strings.Join(mount.Opts, ",")

				if !d.IsPrivileged() && mount.OwnerShift == deviceConfig.MountOwnerShiftDynamic {
					switch d.IdmappedStorage(mount.DevPath, mount.FSType) {
					case idmap.IdmapStorageIdmapped:
						mntOptions = strings.Join([]string{mntOptions, "idmap=container"}, ",")
					case idmap.IdmapStorageNone:
						return "", nil, fmt.Errorf("Failed to setup device mount %q: %w", dev.Name(), fmt.Errorf("idmapping abilities are required but aren't supported on system"))
					}
				}

				mntVal := fmt.Sprintf("%s %s %s %s %d %d", escapePathFstab(mount.DevPath), escapePathFstab(mount.TargetPath), mount.FSType, mntOptions, mount.Freq, mount.PassNo)
				err = lxcSetConfigItem(cc, "lxc.mount.entry", mntVal)
				if err != nil {
					return "", nil, fmt.Errorf("Failed to setup device mount %q: %w", dev.Name(), err)
				}
			}
		}

		// Pass any network setup config into LXC.
		if len(runConf.NetworkInterface) > 0 {
			// Increment nicID so that LXC network index is unique per device.
			nicID++

			networkKeyPrefix := "lxc.net"
			if !liblxc.RuntimeLiblxcVersionAtLeast(liblxc.Version(), 2, 1, 0) {
				networkKeyPrefix = "lxc.network"
			}

			for _, nicItem := range runConf.NetworkInterface {
				err = lxcSetConfigItem(cc, fmt.Sprintf("%s.%d.%s", networkKeyPrefix, nicID, nicItem.Key), nicItem.Value)
				if err != nil {
					return "", nil, fmt.Errorf("Failed to setup device network interface %q: %w", dev.Name(), err)
				}
			}
		}

		// Add any post start hooks.
		if len(runConf.PostHooks) > 0 {
			postStartHooks = append(postStartHooks, runConf.PostHooks...)
		}

		// Build list of NVIDIA GPUs (used for MIG).
		if len(runConf.GPUDevice) > 0 {
			for _, entry := range runConf.GPUDevice {
				if entry.Key == device.GPUNvidiaDeviceKey {
					nvidiaDevices = append(nvidiaDevices, entry.Value)
				}
			}
		}
	}

	// Override NVIDIA_VISIBLE_DEVICES if we have devices that need it.
	if len(nvidiaDevices) > 0 {
		err = lxcSetConfigItem(cc, "lxc.environment", fmt.Sprintf("NVIDIA_VISIBLE_DEVICES=%s", strings.Join(nvidiaDevices, ",")))
		if err != nil {
			return "", nil, fmt.Errorf("Unable to set NVIDIA_VISIBLE_DEVICES in LXC environment: %w", err)
		}
	}

	// Load the LXC raw config.
	err = d.loadRawLXCConfig(cc)
	if err != nil {
		return "", nil, err
	}

	// Generate the LXC config
	configPath := filepath.Join(d.RunPath(), "lxc.conf")
	err = cc.SaveConfigFile(configPath)
	if err != nil {
		_ = os.Remove(configPath)
		return "", nil, err
	}

	// Set ownership to match container root
	currentIdmapset, err := d.CurrentIdmap()
	if err != nil {
		return "", nil, err
	}

	uid := int64(0)
	if currentIdmapset != nil {
		uid, _ = currentIdmapset.ShiftFromNS(0, 0)
	}

	err = os.Chown(d.Path(), int(uid), 0)
	if err != nil {
		return "", nil, err
	}

	// We only need traversal by root in the container
	err = os.Chmod(d.Path(), 0100)
	if err != nil {
		return "", nil, err
	}

	// If starting stateless, wipe state
	if !d.IsStateful() && util.PathExists(d.StatePath()) {
		_ = os.RemoveAll(d.StatePath())
	}

	// Snapshot if needed.
	snapName, expiry, err := d.getStartupSnapNameAndExpiry(d)
	if err != nil {
		return "", nil, fmt.Errorf("Failed getting startup snapshot info: %w", err)
	}

	if snapName != "" && expiry != nil {
		err := d.snapshot(snapName, *expiry, false)
		if err != nil {
			return "", nil, fmt.Errorf("Failed taking startup snapshot: %w", err)
		}
	}

	// Update the backup.yaml file just before starting the instance process, but after all devices have been
	// setup, so that the backup file contains the volatile keys used for this instance start, so that they
	// can be used for instance cleanup.
	err = d.UpdateBackupFile()
	if err != nil {
		return "", nil, err
	}

	revert.Success()
	return configPath, postStartHooks, nil
}

// detachInterfaceRename enters the container's network namespace and moves the named interface
// in ifName back to the network namespace of the running process as the name specified in hostName.
func (d *lxc) detachInterfaceRename(netns string, ifName string, hostName string) error {
	daemonPID := os.Getpid()

	// Run forknet detach
	_, err := subprocess.RunCommand(
		d.state.OS.ExecPath,
		"forknet",
		"detach",
		"--",
		netns,
		fmt.Sprintf("%d", daemonPID),
		ifName,
		hostName,
	)

	// Process forknet detach response
	if err != nil {
		return err
	}

	return nil
}

// Start starts the instance.
func (d *lxc) Start(stateful bool) error {
	unlock, err := d.updateBackupFileLock(context.Background())
	if err != nil {
		return err
	}

	defer unlock()

	d.logger.Debug("Start started", logger.Ctx{"stateful": stateful})
	defer d.logger.Debug("Start finished", logger.Ctx{"stateful": stateful})

	// Check that we are startable before creating an operation lock.
	// Must happen before creating operation Start lock to avoid the status check returning Stopped due to the
	// existence of a Start operation lock.
	err = d.validateStartup(stateful, d.statusCode())
	if err != nil {
		return err
	}

	// Setup a new operation.
	op, err := operationlock.CreateWaitGet(d.Project().Name, d.Name(), operationlock.ActionStart, []operationlock.Action{operationlock.ActionRestart, operationlock.ActionRestore}, false, false)
	if err != nil {
		if errors.Is(err, operationlock.ErrNonReusuableSucceeded) {
			// An existing matching operation has now succeeded, return.
			return nil
		}

		return fmt.Errorf("Failed to create instance start operation: %w", err)
	}

	defer op.Done(nil)

	if !daemon.SharedMountsSetup {
		err = fmt.Errorf("Daemon failed to setup shared mounts base. Does security.nesting need to be turned on?")
		op.Done(err)
		return err
	}

	ctxMap := logger.Ctx{
		"action":    op.Action(),
		"created":   d.creationDate,
		"ephemeral": d.ephemeral,
		"used":      d.lastUsedDate,
		"stateful":  stateful}

	if op.Action() == "start" {
		d.logger.Info("Starting instance", ctxMap)
	}

	// If stateful, restore now.
	if stateful && d.stateful {
		d.logger.Info("Restoring stateful checkpoint")

		criuMigrationArgs := instance.CriuMigrationArgs{
			Cmd:          liblxc.MIGRATE_RESTORE,
			StateDir:     d.StatePath(),
			Function:     "snapshot",
			Stop:         false,
			ActionScript: false,
			DumpDir:      "",
			PreDumpDir:   "",
		}

		err = d.migrate(&criuMigrationArgs)
		if err != nil && !d.IsRunning() {
			op.Done(err)
			return fmt.Errorf("Failed restoring stateful checkpoint: %w", err)
		}

		_ = os.RemoveAll(d.StatePath())
		d.stateful = false

		err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.UpdateInstanceStatefulFlag(ctx, d.id, false)
		})
		if err != nil {
			op.Done(err)
			return fmt.Errorf("Failed clearing instance stateful flag: %w", err)
		}

		if op.Action() == "start" {
			d.logger.Info("Started instance", ctxMap)
			d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceStarted.Event(d, nil))
		}

		return nil
	} else if d.stateful {
		/* stateless start required when we have state, let's delete it */
		err := os.RemoveAll(d.StatePath())
		if err != nil {
			op.Done(err)
			return err
		}

		d.stateful = false
		err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.UpdateInstanceStatefulFlag(ctx, d.id, false)
		})
		if err != nil {
			op.Done(err)
			return fmt.Errorf("Failed clearing instance stateful flag: %w", err)
		}
	}

	// Run the shared start code.
	configPath, postStartHooks, err := d.startCommon()
	if err != nil {
		op.Done(err)
		return err
	}

	name := project.Instance(d.Project().Name, d.name)

	// Start the LXC container
	_, err = subprocess.RunCommand(
		d.state.OS.ExecPath,
		"forkstart",
		name,
		d.state.OS.LxcPath,
		configPath)
	if err != nil && !d.IsRunning() {
		// Attempt to extract the LXC errors
		lxcLog := ""
		logPath := filepath.Join(d.LogPath(), "lxc.log")
		if util.PathExists(logPath) {
			logContent, err := os.ReadFile(logPath)
			if err == nil {
				for _, line := range strings.Split(string(logContent), "\n") {
					fields := strings.Fields(line)
					if len(fields) < 4 {
						continue
					}

					// We only care about errors
					if fields[2] != "ERROR" {
						continue
					}

					// Prepend the line break
					if len(lxcLog) == 0 {
						lxcLog += "\n"
					}

					lxcLog += fmt.Sprintf("  %s\n", strings.Join(fields[0:], " "))
				}
			}
		}

		d.logger.Error("Failed starting instance", ctxMap)

		// Return the actual error
		op.Done(err)
		return err
	}

	// Run any post start hooks.
	err = d.runHooks(postStartHooks)
	if err != nil {
		op.Done(err) // Must come before Stop() otherwise stop will not proceed.

		// Attempt to stop container.
		_ = d.Stop(false)

		return err
	}

	if op.Action() == "start" {
		d.logger.Info("Started instance", ctxMap)
		d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceStarted.Event(d, nil))
	}

	return nil
}

// OnHook is the top-level hook handler.
func (d *lxc) OnHook(hookName string, args map[string]string) error {
	switch hookName {
	case instance.HookStart:
		return d.onStart(args)
	case instance.HookStopNS:
		return d.onStopNS(args)
	case instance.HookStop:
		return d.onStop(args)
	default:
		return instance.ErrNotImplemented
	}
}

// onStart implements the start hook.
func (d *lxc) onStart(_ map[string]string) error {
	// Make sure we can't call go-lxc functions by mistake
	d.fromHook = true

	// Load the container AppArmor profile
	err := apparmor.InstanceLoad(d.state.OS, d, nil)
	if err != nil {
		return err
	}

	// Template anything that needs templating
	key := "volatile.apply_template"
	if d.localConfig[key] != "" {
		// Run any template that needs running
		err = d.templateApplyNow(instance.TemplateTrigger(d.localConfig[key]))
		if err != nil {
			_ = apparmor.InstanceUnload(d.state.OS, d)
			return err
		}

		err := d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			// Remove the volatile key from the DB
			return tx.DeleteInstanceConfigKey(ctx, int64(d.id), key)
		})
		if err != nil {
			_ = apparmor.InstanceUnload(d.state.OS, d)
			return err
		}
	}

	err = d.templateApplyNow("start")
	if err != nil {
		_ = apparmor.InstanceUnload(d.state.OS, d)
		return err
	}

	// Trigger a rebalance
	cgroup.TaskSchedulerTrigger("container", d.name, "started")

	// Record last start state.
	err = d.recordLastState()
	if err != nil {
		return err
	}

	return nil
}

// Stop functions.
func (d *lxc) Stop(stateful bool) error {
	d.logger.Debug("Stop started", logger.Ctx{"stateful": stateful})
	defer d.logger.Debug("Stop finished", logger.Ctx{"stateful": stateful})

	// Must be run prior to creating the operation lock.
	if !d.IsRunning() {
		return ErrInstanceIsStopped
	}

	// Setup a new operation
	op, err := operationlock.CreateWaitGet(d.Project().Name, d.Name(), operationlock.ActionStop, []operationlock.Action{operationlock.ActionRestart, operationlock.ActionRestore}, false, true)
	if err != nil {
		if errors.Is(err, operationlock.ErrNonReusuableSucceeded) {
			// An existing matching operation has now succeeded, return.
			return nil
		}

		return err
	}

	ctxMap := logger.Ctx{
		"action":    op.Action(),
		"created":   d.creationDate,
		"ephemeral": d.ephemeral,
		"used":      d.lastUsedDate,
		"stateful":  stateful}

	if op.Action() == "stop" {
		d.logger.Info("Stopping instance", ctxMap)
	}

	// Forcefully stop any forkfile process if running.
	d.stopForkfile(true)

	// Release liblxc container once done.
	defer func() {
		d.release()
	}()

	// Load the go-lxc struct
	var cc *liblxc.Container
	if d.expandedConfig["raw.lxc"] != "" {
		cc, err = d.initLXC(true)
		if err != nil {
			op.Done(err)
			return err
		}

		err = d.loadRawLXCConfig(cc)
		if err != nil {
			op.Done(err)
			return err
		}
	} else {
		cc, err = d.initLXC(false)
		if err != nil {
			op.Done(err)
			return err
		}
	}

	// Handle stateful stop
	if stateful {
		// Cleanup any existing state
		stateDir := d.StatePath()
		_ = os.RemoveAll(stateDir)

		err := os.MkdirAll(stateDir, 0700)
		if err != nil {
			op.Done(err)
			return err
		}

		criuMigrationArgs := instance.CriuMigrationArgs{
			Cmd:          liblxc.MIGRATE_DUMP,
			StateDir:     stateDir,
			Function:     "snapshot",
			Stop:         true,
			ActionScript: false,
			DumpDir:      "",
			PreDumpDir:   "",
		}

		// Checkpoint
		err = d.migrate(&criuMigrationArgs)
		if err != nil {
			op.Done(err)
			return err
		}

		err = op.Wait(context.Background())
		if err != nil && d.IsRunning() {
			return err
		}

		d.stateful = true

		err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
			return tx.UpdateInstanceStatefulFlag(ctx, d.id, true)
		})
		if err != nil {
			return fmt.Errorf("Failed updating instance stateful flag: %w", err)
		}

		d.logger.Info("Stopped instance", ctxMap)
		d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceStopped.Event(d, nil))

		return nil
	} else if util.PathExists(d.StatePath()) {
		_ = os.RemoveAll(d.StatePath())
	}

	// Load cgroup abstraction
	cg, err := d.cgroup(cc, true)
	if err != nil {
		op.Done(err)
		return err
	}

	// Fork-bomb mitigation, prevent forking from this point on
	if d.state.OS.CGInfo.Supports(cgroup.Pids, cg) {
		// Attempt to disable forking new processes
		_ = cg.SetMaxProcesses(0)
	} else if d.state.OS.CGInfo.Supports(cgroup.Freezer, cg) {
		// Attempt to freeze the container
		freezer := make(chan bool, 1)
		go func() {
			_ = d.Freeze()
			freezer <- true
		}()

		select {
		case <-freezer:
		case <-time.After(time.Second * 5):
			_ = d.Unfreeze()
		}
	}

	err = cc.Stop()
	if err != nil {
		op.Done(err)
		return err
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
	} else if op.Action() == "stop" {
		// If instance stopped, send lifecycle event (even if there has been an error cleaning up).
		d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceStopped.Event(d, nil))
	}

	// Now handle errors from stop sequence and return to caller if wasn't completed cleanly.
	if err != nil {
		return err
	}

	return nil
}

// Shutdown stops the instance.
func (d *lxc) Shutdown(timeout time.Duration) error {
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

	// Setup a new operation
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

	ctxMap := logger.Ctx{
		"action":    "shutdown",
		"created":   d.creationDate,
		"ephemeral": d.ephemeral,
		"used":      d.lastUsedDate,
		"timeout":   timeout}

	if op.Action() == "stop" {
		d.logger.Info("Shutting down instance", ctxMap)
	}

	// Release liblxc container once done.
	defer func() {
		d.release()
	}()

	// Load the go-lxc struct
	var cc *liblxc.Container
	if d.expandedConfig["raw.lxc"] != "" {
		cc, err = d.initLXC(true)
		if err != nil {
			op.Done(err)
			return err
		}

		err = d.loadRawLXCConfig(cc)
		if err != nil {
			op.Done(err)
			return err
		}
	} else {
		cc, err = d.initLXC(false)
		if err != nil {
			op.Done(err)
			return err
		}
	}

	// Request shutdown, but don't wait for container to stop. If call fails then cancel operation with error,
	// otherwise expect the onStop() hook to cancel operation when done (when the container has stopped).
	err = cc.Shutdown(0)
	if err != nil {
		op.Done(err)
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
	} else if op.Action() == "stop" {
		// If instance stopped, send lifecycle event (even if there has been an error cleaning up).
		d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceShutdown.Event(d, nil))
	}

	// Now handle errors from shutdown sequence and return to caller if wasn't completed cleanly.
	if err != nil {
		return err
	}

	return nil
}

// Restart restart the instance.
func (d *lxc) Restart(timeout time.Duration) error {
	return d.restartCommon(d, timeout)
}

// Rebuild rebuilds the instance using the supplied image fingerprint as source.
func (d *lxc) Rebuild(img *api.Image, op *operations.Operation) error {
	return d.rebuildCommon(d, img, op)
}

// onStopNS is triggered by LXC's stop hook once a container is shutdown but before the container's
// namespaces have been closed. The netns path of the stopped container is provided.
func (d *lxc) onStopNS(args map[string]string) error {
	target := args["target"]
	netns := args["netns"]

	// Validate target.
	if !util.ValueInSlice(target, []string{"stop", "reboot"}) {
		d.logger.Error("Container sent invalid target to OnStopNS", logger.Ctx{"target": target})
		return fmt.Errorf("Invalid stop target %q", target)
	}

	// Create/pick up operation, but don't complete it as we leave operation running for the onStop hook below.
	_, err := d.onStopOperationSetup(target)
	if err != nil {
		return err
	}

	// Clean up devices.
	d.cleanupDevices(false, netns)

	return nil
}

// onStop is triggered by LXC's post-stop hook once a container is shutdown and after the
// container's namespaces have been closed.
func (d *lxc) onStop(args map[string]string) error {
	target := args["target"]

	// Validate target
	if !util.ValueInSlice(target, []string{"stop", "reboot"}) {
		d.logger.Error("Container sent invalid target to OnStop", logger.Ctx{"target": target})
		return fmt.Errorf("Invalid stop target: %s", target)
	}

	// Create/pick up operation.
	op, err := d.onStopOperationSetup(target)
	if err != nil {
		return err
	}

	// Make sure we can't call go-lxc functions by mistake
	d.fromHook = true

	// Record power state.
	err = d.VolatileSet(map[string]string{
		"volatile.last_state.power": instance.PowerStateStopped,
		"volatile.last_state.ready": "false",
	})
	if err != nil {
		// Don't return an error here as we still want to cleanup the instance even if DB not available.
		d.logger.Error("Failed recording last power state", logger.Ctx{"err": err})
	}

	go func(d *lxc, target string, op *operationlock.InstanceOperation) {
		d.fromHook = false
		err = nil

		// Unlock on return
		defer op.Done(nil)

		d.logger.Debug("Instance stopped, cleaning up")

		// Wait for any file operations to complete.
		// This is to required so we can actually unmount the container.
		d.stopForkfile(false)

		// Clean up devices.
		d.cleanupDevices(false, "")

		// Remove directory ownership (to avoid issue if uidmap is re-used)
		err := os.Chown(d.Path(), 0, 0)
		if err != nil {
			op.Done(fmt.Errorf("Failed clearing ownership: %w", err))
			return
		}

		err = os.Chmod(d.Path(), 0100)
		if err != nil {
			op.Done(fmt.Errorf("Failed clearing permissions: %w", err))
			return
		}

		// Stop the storage for this container
		err = d.unmount()
		if err != nil && !errors.Is(err, storageDrivers.ErrInUse) {
			err = fmt.Errorf("Failed unmounting instance: %w", err)
			op.Done(err)
			return
		}

		// Unload the apparmor profile
		err = apparmor.InstanceUnload(d.state.OS, d)
		if err != nil {
			op.Done(fmt.Errorf("Failed to destroy apparmor namespace: %w", err))
			return
		}

		// Clean all the unix devices
		err = d.removeUnixDevices()
		if err != nil {
			op.Done(fmt.Errorf("Failed to remove unix devices: %w", err))
			return
		}

		// Clean all the disk devices
		err = d.removeDiskDevices()
		if err != nil {
			op.Done(fmt.Errorf("Failed to remove disk devices: %w", err))
			return
		}

		// Log and emit lifecycle if not user triggered
		if op.GetInstanceInitiated() {
			ctxMap := logger.Ctx{
				"action":    target,
				"created":   d.creationDate,
				"ephemeral": d.ephemeral,
				"used":      d.lastUsedDate,
				"stateful":  false,
			}

			d.logger.Info("Shut down instance", ctxMap)
			d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceShutdown.Event(d, nil))
		}

		// Reboot the container
		if target == "reboot" {
			// Start the container again
			err = d.Start(false)
			if err != nil {
				op.Done(fmt.Errorf("Failed restarting instance: %w", err))
				return
			}

			d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceRestarted.Event(d, nil))

			return
		}

		// Trigger a rebalance
		cgroup.TaskSchedulerTrigger("container", d.name, "stopped")

		// Destroy ephemeral containers
		if d.ephemeral {
			err = d.delete(true)
			if err != nil {
				op.Done(fmt.Errorf("Failed deleting ephemeral instance: %w", err))
				return
			}
		}
	}(d, target, op)

	return nil
}

// cleanupDevices performs any needed device cleanup steps when container is stopped.
// Accepts a stopHookNetnsPath argument which is required when run from the onStopNS hook before the
// container's network namespace is unmounted (which is required for NIC device cleanup).
func (d *lxc) cleanupDevices(instanceRunning bool, stopHookNetnsPath string) {
	for _, entry := range d.expandedDevices.Reversed() {
		// Only stop NIC devices when run from the onStopNS hook, and stop all other devices when run from
		// the onStop hook. This way disk devices are stopped after the instance has been fully stopped.
		if (stopHookNetnsPath != "" && entry.Config["type"] != "nic") || (stopHookNetnsPath == "" && entry.Config["type"] == "nic") {
			continue
		}

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
			err = d.deviceStop(dev, instanceRunning, stopHookNetnsPath)
			if err != nil {
				d.logger.Error("Failed to stop device", logger.Ctx{"device": dev.Name(), "err": err})
			}
		}
	}
}

// Freeze functions.
func (d *lxc) Freeze() error {
	ctxMap := logger.Ctx{
		"created":   d.creationDate,
		"ephemeral": d.ephemeral,
		"used":      d.lastUsedDate}

	// Check that we're running
	if !d.IsRunning() {
		return fmt.Errorf("The instance isn't running")
	}

	// Load the go-lxc struct
	cc, err := d.initLXC(false)
	if err != nil {
		ctxMap["err"] = err
		d.logger.Error("Failed freezing container", ctxMap)
		return err
	}

	cg, err := d.cgroup(cc, true)
	if err != nil {
		return err
	}

	// Check if the CGroup is available
	if !d.state.OS.CGInfo.Supports(cgroup.Freezer, cg) {
		d.logger.Info("Unable to freeze container (lack of kernel support)", ctxMap)
		return nil
	}

	// Check that we're not already frozen
	if d.IsFrozen() {
		return fmt.Errorf("The container is already frozen")
	}

	d.logger.Info("Freezing container", ctxMap)

	err = cc.Freeze()
	if err != nil {
		ctxMap["err"] = err
		d.logger.Error("Failed freezing container", ctxMap)
		return err
	}

	d.logger.Info("Froze container", ctxMap)
	d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstancePaused.Event(d, nil))

	return err
}

// Unfreeze unfreezes the instance.
func (d *lxc) Unfreeze() error {
	ctxMap := logger.Ctx{
		"created":   d.creationDate,
		"ephemeral": d.ephemeral,
		"used":      d.lastUsedDate}

	// Check that we're running
	if !d.IsRunning() {
		return fmt.Errorf("The container isn't running")
	}

	// Load the go-lxc struct
	cc, err := d.initLXC(false)
	if err != nil {
		d.logger.Error("Failed unfreezing container", ctxMap)
		return err
	}

	cg, err := d.cgroup(cc, true)
	if err != nil {
		return err
	}

	// Check if the CGroup is available
	if !d.state.OS.CGInfo.Supports(cgroup.Freezer, cg) {
		d.logger.Info("Unable to unfreeze container (lack of kernel support)", ctxMap)
		return nil
	}

	// Check that we're frozen
	if !d.IsFrozen() {
		return fmt.Errorf("The container is already running")
	}

	d.logger.Info("Unfreezing container", ctxMap)

	err = cc.Unfreeze()
	if err != nil {
		d.logger.Error("Failed unfreezing container", ctxMap)
	}

	d.logger.Info("Unfroze container", ctxMap)
	d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceResumed.Event(d, nil))

	return err
}

// Get lxc container state, with 1 second timeout.
// If we don't get a reply, assume the lxc monitor is unresponsive.
func (d *lxc) getLxcState() (liblxc.State, error) {
	if d.IsSnapshot() {
		return liblxc.StateMap["STOPPED"], nil
	}

	// Load the go-lxc struct
	cc, err := d.initLXC(false)
	if err != nil {
		return liblxc.StateMap["STOPPED"], err
	}

	monitor := make(chan liblxc.State, 1)

	go func(c *liblxc.Container) {
		monitor <- c.State()
	}(cc)

	select {
	case state := <-monitor:
		return state, nil
	case <-time.After(5 * time.Second):
		return liblxc.StateMap["FROZEN"], fmt.Errorf("Monitor is unresponsive")
	}
}

// Render renders the state of the instance.
func (d *lxc) Render(options ...func(response any) error) (any, any, error) {
	// Ignore err as the arch string on error is correct (unknown)
	architectureName, _ := osarch.ArchitectureName(d.architecture)
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
			Size:            -1, // Default to uninitialised/error state (0 means no CoW usage).
		}

		snapState.Architecture = architectureName
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
	instState.Architecture = architectureName
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

// RenderFull renders the full state of the instance.
func (d *lxc) RenderFull(hostInterfaces []net.Interface) (*api.InstanceFull, any, error) {
	if d.IsSnapshot() {
		return nil, nil, fmt.Errorf("RenderFull only works with containers")
	}

	// Get the Container struct
	base, etag, err := d.Render()
	if err != nil {
		return nil, nil, err
	}

	// Convert to ContainerFull
	ct := api.InstanceFull{Instance: *base.(*api.Instance)}

	// Add the ContainerState
	ct.State, err = d.renderState(ct.StatusCode, hostInterfaces)
	if err != nil {
		return nil, nil, err
	}

	// Add the ContainerSnapshots
	snaps, err := d.Snapshots()
	if err != nil {
		return nil, nil, err
	}

	for _, snap := range snaps {
		render, _, err := snap.Render()
		if err != nil {
			return nil, nil, err
		}

		if ct.Snapshots == nil {
			ct.Snapshots = []api.InstanceSnapshot{}
		}

		ct.Snapshots = append(ct.Snapshots, *render.(*api.InstanceSnapshot))
	}

	// Add the ContainerBackups
	backups, err := d.Backups()
	if err != nil {
		return nil, nil, err
	}

	for _, backup := range backups {
		render := backup.Render()

		if ct.Backups == nil {
			ct.Backups = []api.InstanceBackup{}
		}

		ct.Backups = append(ct.Backups, *render)
	}

	return &ct, etag, nil
}

// renderState renders just the running state of the instance.
func (d *lxc) renderState(statusCode api.StatusCode, hostInterfaces []net.Interface) (*api.InstanceState, error) {
	status := api.InstanceState{
		Status:     statusCode.String(),
		StatusCode: statusCode,
	}

	pid := d.InitPID()
	processesState, _ := d.processesState(pid)

	if d.isRunningStatusCode(statusCode) {
		status.CPU = d.cpuState()
		status.Memory = d.memoryState()
		status.Network = d.networkState(hostInterfaces)
		status.Pid = int64(pid)
		status.Processes = processesState
	}

	status.Disk = d.diskState()

	d.release()

	return &status, nil
}

// RenderState renders just the running state of the instance.
func (d *lxc) RenderState(hostInterfaces []net.Interface) (*api.InstanceState, error) {
	return d.renderState(d.statusCode(), hostInterfaces)
}

// snapshot creates a snapshot of the instance.
func (d *lxc) snapshot(name string, expiry time.Time, stateful bool) error {
	// Deal with state.
	if stateful {
		// Quick checks.
		if !d.IsRunning() {
			return fmt.Errorf("Unable to create a stateful snapshot. The instance isn't running")
		}

		_, err := exec.LookPath("criu")
		if err != nil {
			return fmt.Errorf("Unable to create a stateful snapshot. CRIU isn't installed")
		}

		// Cleanup any existing state
		stateDir := d.StatePath()
		_ = os.RemoveAll(stateDir)

		// Create the state path and make sure we don't keep state around after the snapshot has been made.
		err = os.MkdirAll(stateDir, 0700)
		if err != nil {
			return err
		}

		defer func() { _ = os.RemoveAll(stateDir) }()

		// Release liblxc container once done.
		defer func() {
			d.release()
		}()

		// Load the go-lxc struct
		if d.expandedConfig["raw.lxc"] != "" {
			cc, err := d.initLXC(true)
			if err != nil {
				return err
			}

			err = d.loadRawLXCConfig(cc)
			if err != nil {
				return err
			}
		} else {
			_, err = d.initLXC(false)
			if err != nil {
				return err
			}
		}

		/* TODO: ideally we would freeze here and unfreeze below after
		 * we've copied the filesystem, to make sure there are no
		 * changes by the container while snapshotting. Unfortunately
		 * there is abug in CRIU where it doesn't leave the container
		 * in the same state it found it w.r.t. freezing, i.e. CRIU
		 * freezes too, and then /always/ thaws, even if the container
		 * was frozen. Until that's fixed, all calls to Unfreeze()
		 * after snapshotting will fail.
		 */
		criuMigrationArgs := instance.CriuMigrationArgs{
			Cmd:          liblxc.MIGRATE_DUMP,
			StateDir:     stateDir,
			Function:     "snapshot",
			Stop:         false,
			ActionScript: false,
			DumpDir:      "",
			PreDumpDir:   "",
		}

		// Dump the state.
		err = d.migrate(&criuMigrationArgs)
		if err != nil {
			return fmt.Errorf("Failed taking stateful checkpoint: %w", err)
		}
	}

	// Wait for any file operations to complete to have a more consistent snapshot.
	d.stopForkfile(false)

	return d.snapshotCommon(d, name, expiry, stateful)
}

// Snapshot takes a new snapshot.
func (d *lxc) Snapshot(name string, expiry time.Time, stateful bool) error {
	unlock, err := d.updateBackupFileLock(context.Background())
	if err != nil {
		return err
	}

	defer unlock()

	return d.snapshot(name, expiry, stateful)
}

// Restore restores a snapshot.
func (d *lxc) Restore(sourceContainer instance.Instance, stateful bool) error {
	var ctxMap logger.Ctx

	op, err := operationlock.Create(d.Project().Name, d.Name(), operationlock.ActionRestore, false, false)
	if err != nil {
		return fmt.Errorf("Failed to create instance restore operation: %w", err)
	}

	defer op.Done(nil)

	// Stop the container.
	wasRunning := d.IsRunning()
	if wasRunning {
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

		// This will unmount the container storage.
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
		"source":    sourceContainer.Name()}

	d.logger.Info("Restoring instance", ctxMap)

	// Wait for any file operations to complete.
	// This is required so we can actually unmount the container and restore its rootfs.
	d.stopForkfile(false)

	// Initialize storage interface for the container and mount the rootfs for criu state check.
	pool, err := storagePools.LoadByInstance(d.state, d)
	if err != nil {
		op.Done(err)
		return err
	}

	d.logger.Debug("Mounting instance to check for CRIU state path existence")

	revert := revert.New()
	defer revert.Fail()

	// Ensure that storage is mounted for state path checks and for backup.yaml updates.
	_, err = d.mount()
	if err != nil {
		op.Done(err)
		return err
	}

	revert.Add(func() { _ = d.unmount() })

	// Check for CRIU if necessary, before doing a bunch of filesystem manipulations.
	// Requires container be mounted to check StatePath exists.
	if util.PathExists(d.StatePath()) {
		_, err := exec.LookPath("criu")
		if err != nil {
			err = fmt.Errorf("Failed to restore container state. CRIU isn't installed")
			op.Done(err)
			return err
		}
	}

	err = d.unmount()
	if err != nil {
		op.Done(err)
		return err
	}

	revert.Success()

	// Restore the rootfs.
	err = pool.RestoreInstanceSnapshot(d, sourceContainer, nil)
	if err != nil {
		op.Done(err)
		return err
	}

	// Restore the configuration.
	args := db.InstanceArgs{
		Architecture: sourceContainer.Architecture(),
		Config:       sourceContainer.LocalConfig(),
		Description:  sourceContainer.Description(),
		Devices:      sourceContainer.LocalDevices(),
		Ephemeral:    sourceContainer.IsEphemeral(),
		Profiles:     sourceContainer.Profiles(),
		Project:      sourceContainer.Project().Name,
		Type:         sourceContainer.Type(),
		Snapshot:     sourceContainer.IsSnapshot(),
	}

	// Don't pass as user-requested as there's no way to fix a bad config.
	// This will call d.UpdateBackupFile() to ensure snapshot list is up to date.
	err = d.Update(args, false)
	if err != nil {
		op.Done(err)
		return err
	}

	// If the container wasn't running but was stateful, should we restore it as running?
	if stateful {
		if !util.PathExists(d.StatePath()) {
			err = fmt.Errorf("Stateful snapshot restore requested but snapshot is stateless")
			op.Done(err)
			return err
		}

		d.logger.Debug("Performing stateful restore", ctxMap)
		d.stateful = true

		criuMigrationArgs := instance.CriuMigrationArgs{
			Cmd:          liblxc.MIGRATE_RESTORE,
			StateDir:     d.StatePath(),
			Function:     "snapshot",
			Stop:         false,
			ActionScript: false,
			DumpDir:      "",
			PreDumpDir:   "",
		}

		// Checkpoint.
		err = d.migrate(&criuMigrationArgs)
		if err != nil {
			op.Done(err)
			return fmt.Errorf("Failed taking stateful checkpoint: %w", err)
		}

		// Remove the state from the parent container; we only keep this in snapshots.
		err2 := os.RemoveAll(d.StatePath())
		if err2 != nil && !os.IsNotExist(err) {
			op.Done(err)
			return err
		}

		if err != nil {
			op.Done(err)
			return err
		}

		d.logger.Debug("Performed stateful restore", ctxMap)
		d.logger.Info("Restored instance", ctxMap)
		return nil
	}

	// Restart the container.
	if wasRunning {
		d.logger.Debug("Starting instance after snapshot restore")
		err = d.Start(false)
		if err != nil {
			op.Done(err)
			return err
		}
	}

	d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceRestored.Event(d, map[string]any{"snapshot": sourceContainer.Name()}))
	d.logger.Info("Restored instance", ctxMap)

	return nil
}

func (d *lxc) cleanup() {
	// Unmount any leftovers
	_ = d.removeUnixDevices()
	_ = d.removeDiskDevices()

	// Remove the security profiles
	_ = apparmor.InstanceDelete(d.state.OS, d)
	seccomp.DeleteProfile(d)

	// Remove the devices path
	_ = os.Remove(d.DevicesPath())

	// Remove the shmounts path
	_ = os.RemoveAll(d.ShmountsPath())
}

// Delete deletes the instance.
func (d *lxc) Delete(force bool) error {
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

// Delete deletes the instance without creating an operation lock.
func (d *lxc) delete(force bool) error {
	ctxMap := logger.Ctx{
		"created":   d.creationDate,
		"ephemeral": d.ephemeral,
		"used":      d.lastUsedDate}

	if d.isSnapshot {
		d.logger.Info("Deleting instance snapshot", ctxMap)
	} else {
		d.logger.Info("Deleting instance", ctxMap)
	}

	if !force && util.IsTrue(d.expandedConfig["security.protection.delete"]) && !d.IsSnapshot() {
		err := fmt.Errorf("Instance is protected")
		d.logger.Warn("Failed to delete instance", logger.Ctx{"err": err})
		return err
	}

	// Wait for any file operations to complete.
	// This is required so we can actually unmount the container and delete it.
	if !d.IsSnapshot() {
		d.stopForkfile(false)
	}

	// Delete any persistent warnings for instance.
	err := d.warningsDelete()
	if err != nil {
		return err
	}

	pool, err := storagePools.LoadByInstance(d.state, d)
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
				return snapInst.(*lxc).delete(true) // Internal delete function that doesn't lock.
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
		return tx.DeleteInstance(ctx, d.project.Name, d.Name())
	})
	if err != nil {
		d.logger.Error("Failed deleting instance entry", logger.Ctx{"err": err})
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

// Rename renames the instance. Accepts an argument to enable applying deferred TemplateTriggerRename.
func (d *lxc) Rename(newName string, applyTemplateTrigger bool) error {
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

				err := cluster.RenameInstanceSnapshot(ctx, tx.Tx(), d.project.Name, oldName, oldSnapName, baseSnapName)
				if err != nil {
					d.logger.Error("Failed renaming snapshot", ctxMap)
					return fmt.Errorf("Failed renaming snapshot: %w", err)
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
			return cluster.RenameInstanceSnapshot(ctx, tx.Tx(), d.project.Name, oldParts[0], oldParts[1], newParts[1])
		}

		return cluster.RenameInstance(ctx, tx.Tx(), d.project.Name, oldName, newName)
	})
	if err != nil {
		d.logger.Error("Failed renaming instance", ctxMap)
		return fmt.Errorf("Failed renaming instance: %w", err)
	}

	// Rename the logging path.
	newFullName := project.Instance(d.Project().Name, d.Name())
	_ = os.RemoveAll(internalUtil.LogPath(newFullName))
	if util.PathExists(d.LogPath()) {
		err := os.Rename(d.LogPath(), internalUtil.LogPath(newFullName))
		if err != nil {
			d.logger.Error("Failed renaming instance", ctxMap)
			return fmt.Errorf("Failed renaming instance: %w", err)
		}
	}

	// Rename the runtime path.
	newFullName = project.Instance(d.Project().Name, d.Name())
	_ = os.RemoveAll(internalUtil.RunPath(newFullName))
	if util.PathExists(d.RunPath()) {
		err := os.Rename(d.RunPath(), internalUtil.RunPath(newFullName))
		if err != nil {
			d.logger.Error("Failed renaming instance", ctxMap)
			return fmt.Errorf("Failed renaming instance: %w", err)
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

	// Invalidate the go-lxc cache.
	d.release()

	d.cConfig = false

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

// CGroupSet sets a cgroup value for the instance.
func (d *lxc) CGroupSet(key string, value string) error {
	// Load the go-lxc struct
	cc, err := d.initLXC(false)
	if err != nil {
		return err
	}

	// Make sure the container is running.
	// We use InitPID here rather than IsRunning because this task can be triggered during the container's
	// startup process, which is during the time that the start lock is held, which causes IsRunning to
	// return false (because the container hasn't fully started yet) but it is sufficiently started to
	// have its cgroup disk limits set.
	if d.InitPID() <= 0 {
		return fmt.Errorf("Can't set cgroups on a stopped container")
	}

	err = cc.SetCgroupItem(key, value)
	if err != nil {
		return fmt.Errorf("Failed to set cgroup %s=\"%s\": %w", key, value, err)
	}

	return nil
}

// Update applies updated config.
func (d *lxc) Update(args db.InstanceArgs, userRequested bool) error {
	unlock, err := d.updateBackupFileLock(context.Background())
	if err != nil {
		return err
	}

	defer unlock()

	// Setup a new operation
	op, err := operationlock.CreateWaitGet(d.Project().Name, d.Name(), operationlock.ActionUpdate, []operationlock.Action{operationlock.ActionRestart, operationlock.ActionRestore}, false, false)
	if err != nil {
		return fmt.Errorf("Failed to create instance update operation: %w", err)
	}

	defer op.Done(nil)

	// Set sane defaults for unset keys
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
		// Validate the new config
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
		// Validate the new profiles
		profiles, err = tx.GetProfileNames(ctx, args.Project)

		return err
	})
	if err != nil {
		return fmt.Errorf("Failed to get profiles: %w", err)
	}

	checkedProfiles := []string{}
	for _, profile := range args.Profiles {
		if !util.ValueInSlice(profile.Name, profiles) {
			return fmt.Errorf("Requested profile '%s' doesn't exist", profile.Name)
		}

		if util.ValueInSlice(profile.Name, checkedProfiles) {
			return fmt.Errorf("Duplicate profile found in request")
		}

		checkedProfiles = append(checkedProfiles, profile.Name)
	}

	// Validate the new architecture
	if args.Architecture != 0 {
		_, err = osarch.ArchitectureName(args.Architecture)
		if err != nil {
			return fmt.Errorf("Invalid architecture id: %s", err)
		}
	}

	// Get a copy of the old configuration
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

	// Define a function which reverts everything.  Defer this function
	// so that it doesn't need to be explicitly called in every failing
	// return path.  Track whether or not we want to undo the changes
	// using a closure.
	undoChanges := true
	defer func() {
		if undoChanges {
			d.description = oldDescription
			d.architecture = oldArchitecture
			d.ephemeral = oldEphemeral
			d.expandedConfig = oldExpandedConfig
			d.expandedDevices = oldExpandedDevices
			d.localConfig = oldLocalConfig
			d.localDevices = oldLocalDevices
			d.profiles = oldProfiles
			d.expiryDate = oldExpiryDate
			d.release()
			d.cConfig = false
			_, _ = d.initLXC(true)
			cgroup.TaskSchedulerTrigger("container", d.name, "changed")
		}
	}()

	// Apply the various changes
	d.description = args.Description
	d.architecture = args.Architecture
	d.ephemeral = args.Ephemeral
	d.localConfig = args.Config
	d.localDevices = args.Devices
	d.profiles = args.Profiles
	d.expiryDate = args.ExpiryDate

	// Expand the config and refresh the LXC config
	err = d.expandConfig()
	if err != nil {
		return err
	}

	// Diff the configurations
	changedConfig := []string{}
	for key := range oldExpandedConfig {
		if oldExpandedConfig[key] != d.expandedConfig[key] {
			if !util.ValueInSlice(key, changedConfig) {
				changedConfig = append(changedConfig, key)
			}
		}
	}

	for key := range d.expandedConfig {
		if oldExpandedConfig[key] != d.expandedConfig[key] {
			if !util.ValueInSlice(key, changedConfig) {
				changedConfig = append(changedConfig, key)
			}
		}
	}

	// Diff the devices
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
		// Look for deleted idmap keys.
		protectedKeys := []string{
			"volatile.idmap.base",
			"volatile.idmap.current",
			"volatile.idmap.next",
			"volatile.last_state.idmap",
		}

		for _, k := range changedConfig {
			if !util.ValueInSlice(k, protectedKeys) {
				continue
			}

			_, ok := d.expandedConfig[k]
			if !ok {
				return fmt.Errorf("Volatile idmap keys can't be deleted by the user")
			}
		}

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

	// Run through initLXC to catch anything we missed
	if userRequested {
		d.release()
		d.cConfig = false
		_, err = d.initLXC(true)
		if err != nil {
			return fmt.Errorf("Initialize LXC: %w", err)
		}
	}

	// If raw.lxc changed, re-validate the config.
	if util.ValueInSlice("raw.lxc", changedConfig) && d.expandedConfig["raw.lxc"] != "" {
		// Get a new liblxc instance.
		cc, err := liblxc.NewContainer(d.name, d.state.OS.LxcPath)
		if err != nil {
			return err
		}

		err = d.loadRawLXCConfig(cc)
		if err != nil {
			// Release the liblxc instance.
			_ = cc.Release()
			return err
		}

		// Release the liblxc instance.
		_ = cc.Release()
	}

	// If apparmor changed, re-validate the apparmor profile (even if not running).
	if util.ValueInSlice("raw.apparmor", changedConfig) || util.ValueInSlice("security.nesting", changedConfig) {
		err = apparmor.InstanceValidate(d.state.OS, d, nil)
		if err != nil {
			return fmt.Errorf("Parse AppArmor profile: %w", err)
		}
	}

	if util.ValueInSlice("security.idmap.isolated", changedConfig) || util.ValueInSlice("security.idmap.base", changedConfig) || util.ValueInSlice("security.idmap.size", changedConfig) || util.ValueInSlice("raw.idmap", changedConfig) || util.ValueInSlice("security.privileged", changedConfig) {
		var idmapSet *idmap.Set
		base := int64(0)
		if !d.IsPrivileged() {
			// Update the idmap.
			idmapSet, base, err = findIdmap(
				d.state,
				d.Name(),
				d.expandedConfig["security.idmap.isolated"],
				d.expandedConfig["security.idmap.base"],
				d.expandedConfig["security.idmap.size"],
				d.expandedConfig["raw.idmap"],
			)
			if err != nil {
				return fmt.Errorf("Failed to get ID map: %w", err)
			}
		}

		jsonIdmap, err := idmapSet.ToJSON()
		if err != nil {
			return fmt.Errorf("Failed to encode ID map: %w", err)
		}

		d.localConfig["volatile.idmap.next"] = jsonIdmap
		d.localConfig["volatile.idmap.base"] = fmt.Sprintf("%v", base)

		// Invalidate the idmap cache.
		d.idmapset = nil
	}

	isRunning := d.IsRunning()

	// Use the device interface to apply update changes.
	err = d.devicesUpdate(d, removeDevices, addDevices, updateDevices, oldExpandedDevices, isRunning, userRequested)
	if err != nil {
		return err
	}

	// Apply the live changes
	if isRunning {
		cc, err := d.initLXC(false)
		if err != nil {
			return err
		}

		cg, err := d.cgroup(cc, true)
		if err != nil {
			return err
		}

		// Live update the container config
		for _, key := range changedConfig {
			value := d.expandedConfig[key]

			if key == "raw.apparmor" || key == "security.nesting" {
				// Update the AppArmor profile
				err = apparmor.InstanceLoad(d.state.OS, d, nil)
				if err != nil {
					return err
				}
			} else if key == "security.guestapi" {
				if util.IsTrueOrEmpty(value) {
					err = d.insertMount(internalUtil.VarPath("guestapi"), "/dev/incus", "none", unix.MS_BIND, idmap.IdmapStorageNone)
					if err != nil {
						return err
					}
				} else {
					// Connect to files API.
					files, err := d.FileSFTP()
					if err != nil {
						return err
					}

					defer func() { _ = files.Close() }()

					_, err = files.Lstat("/dev/incus")
					if err == nil {
						err = d.removeMount("/dev/incus")
						if err != nil {
							return err
						}

						err = files.Remove("/dev/incus")
						if err != nil {
							return err
						}
					}
				}
			} else if key == "linux.kernel_modules" && value != "" {
				for _, module := range strings.Split(value, ",") {
					module = strings.TrimPrefix(module, " ")
					err := linux.LoadModule(module)
					if err != nil {
						return fmt.Errorf("Failed to load kernel module '%s': %w", module, err)
					}
				}
			} else if key == "limits.disk.priority" {
				if !d.state.OS.CGInfo.Supports(cgroup.Blkio, cg) {
					continue
				}

				priorityInt := 5
				diskPriority := d.expandedConfig["limits.disk.priority"]
				if diskPriority != "" {
					priorityInt, err = strconv.Atoi(diskPriority)
					if err != nil {
						return err
					}
				}

				// Minimum valid value is 10
				priority := int64(priorityInt * 100)
				if priority == 0 {
					priority = 10
				}

				err = cg.SetBlkioWeight(priority)
				if err != nil {
					return err
				}
			} else if key == "limits.memory" || strings.HasPrefix(key, "limits.memory.") {
				// Skip if no memory CGroup
				if !d.state.OS.CGInfo.Supports(cgroup.Memory, cg) {
					continue
				}

				// Set the new memory limit
				memory := d.expandedConfig["limits.memory"]
				memoryEnforce := d.expandedConfig["limits.memory.enforce"]
				memorySwap := d.expandedConfig["limits.memory.swap"]
				var memoryInt int64

				// Parse memory
				if memory == "" {
					memoryInt = -1
				} else if strings.HasSuffix(memory, "%") {
					percent, err := strconv.ParseInt(strings.TrimSuffix(memory, "%"), 10, 64)
					if err != nil {
						return err
					}

					memoryTotal, err := linux.DeviceTotalMemory()
					if err != nil {
						return err
					}

					memoryInt = int64((memoryTotal / 100) * percent)
				} else {
					memoryInt, err = units.ParseByteSizeString(memory)
					if err != nil {
						return err
					}
				}

				// Store the old values for revert
				oldMemswLimit := int64(-1)
				if d.state.OS.CGInfo.Supports(cgroup.MemorySwap, cg) {
					oldMemswLimit, err = cg.GetMemorySwapLimit()
					if err != nil {
						oldMemswLimit = -1
					}
				}
				oldLimit, err := cg.GetMemoryLimit()
				if err != nil {
					oldLimit = -1
				}

				oldSoftLimit, err := cg.GetMemorySoftLimit()
				if err != nil {
					oldSoftLimit = -1
				}

				revertMemory := func() {
					if oldSoftLimit != -1 {
						_ = cg.SetMemorySoftLimit(oldSoftLimit)
					}

					if oldLimit != -1 {
						_ = cg.SetMemoryLimit(oldLimit)
					}

					if oldMemswLimit != -1 {
						_ = cg.SetMemorySwapLimit(oldMemswLimit)
					}
				}

				// Reset everything
				if d.state.OS.CGInfo.Supports(cgroup.MemorySwap, cg) {
					err = cg.SetMemorySwapLimit(-1)
					if err != nil {
						revertMemory()
						return err
					}
				}

				err = cg.SetMemoryLimit(-1)
				if err != nil {
					revertMemory()
					return err
				}

				err = cg.SetMemorySoftLimit(-1)
				if err != nil {
					revertMemory()
					return err
				}

				// Set the new values
				if memoryEnforce == "soft" {
					// Set new limit
					err = cg.SetMemorySoftLimit(memoryInt)
					if err != nil {
						revertMemory()
						return err
					}
				} else {
					if d.state.OS.CGInfo.Supports(cgroup.MemorySwap, cg) {
						err = cg.SetMemoryLimit(memoryInt)
						if err != nil {
							revertMemory()
							return err
						}

						if util.IsFalse(memorySwap) {
							err = cg.SetMemorySwapLimit(0)
							if err != nil {
								revertMemory()
								return err
							}
						} else {
							err = cg.SetMemorySwapLimit(memoryInt)
							if err != nil {
								revertMemory()
								return err
							}
						}
					} else {
						err = cg.SetMemoryLimit(memoryInt)
						if err != nil {
							revertMemory()
							return err
						}
					}

					// Set soft limit to value 10% less than hard limit
					err = cg.SetMemorySoftLimit(int64(float64(memoryInt) * 0.9))
					if err != nil {
						revertMemory()
						return err
					}
				}

				if !d.state.OS.CGInfo.Supports(cgroup.MemorySwappiness, cg) {
					continue
				}

				// Configure the swappiness
				if key == "limits.memory.swap" || key == "limits.memory.swap.priority" {
					memorySwap := d.expandedConfig["limits.memory.swap"]
					memorySwapPriority := d.expandedConfig["limits.memory.swap.priority"]
					if util.IsFalse(memorySwap) {
						err = cg.SetMemorySwappiness(0)
						if err != nil {
							return err
						}
					} else {
						priority := 10
						if memorySwapPriority != "" {
							priority, err = strconv.Atoi(memorySwapPriority)
							if err != nil {
								return err
							}
						}

						// Maximum priority (10) should be default swappiness (60).
						err = cg.SetMemorySwappiness(int64(70 - priority))
						if err != nil {
							return err
						}
					}
				}
			} else if key == "limits.cpu" || key == "limits.cpu.nodes" {
				// Trigger a scheduler re-run
				cgroup.TaskSchedulerTrigger("container", d.name, "changed")
			} else if key == "limits.cpu.priority" || key == "limits.cpu.allowance" {
				// Skip if no cpu CGroup
				if !d.state.OS.CGInfo.Supports(cgroup.CPU, cg) {
					continue
				}

				// Apply new CPU limits
				cpuShares, cpuCfsQuota, cpuCfsPeriod, err := cgroup.ParseCPU(d.expandedConfig["limits.cpu.allowance"], d.expandedConfig["limits.cpu.priority"])
				if err != nil {
					return err
				}

				err = cg.SetCPUShare(cpuShares)
				if err != nil {
					return err
				}

				err = cg.SetCPUCfsLimit(cpuCfsPeriod, cpuCfsQuota)
				if err != nil {
					return err
				}
			} else if key == "limits.processes" {
				if !d.state.OS.CGInfo.Supports(cgroup.Pids, cg) {
					continue
				}

				if value == "" {
					err = cg.SetMaxProcesses(-1)
					if err != nil {
						return err
					}
				} else {
					valueInt, err := strconv.ParseInt(value, 10, 64)
					if err != nil {
						return err
					}

					err = cg.SetMaxProcesses(valueInt)
					if err != nil {
						return err
					}
				}
			} else if strings.HasPrefix(key, "limits.hugepages.") {
				if !d.state.OS.CGInfo.Supports(cgroup.Hugetlb, cg) {
					continue
				}

				pageType := ""

				switch key {
				case "limits.hugepages.64KB":
					pageType = "64KB"
				case "limits.hugepages.1MB":
					pageType = "1MB"
				case "limits.hugepages.2MB":
					pageType = "2MB"
				case "limits.hugepages.1GB":
					pageType = "1GB"
				}

				valueInt := int64(-1)
				if value != "" {
					valueInt, err = units.ParseByteSizeString(value)
					if err != nil {
						return err
					}
				}

				err = cg.SetHugepagesLimit(pageType, valueInt)
				if err != nil {
					return err
				}
			}
		}
	}

	// Re-generate the instance-id if needed.
	if !d.IsSnapshot() && d.needsNewInstanceID(changedConfig, oldExpandedDevices) {
		err = d.resetInstanceID()
		if err != nil {
			return err
		}
	}

	// Finally, apply the changes to the database
	err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Snapshots should update only their descriptions and expiry date.
		if d.IsSnapshot() {
			return tx.UpdateInstanceSnapshot(d.id, d.description, d.expiryDate)
		}

		object, err := cluster.GetInstance(ctx, tx.Tx(), d.project.Name, d.name)
		if err != nil {
			return err
		}

		object.Description = d.description
		object.Architecture = d.architecture
		object.Ephemeral = d.ephemeral
		object.ExpiryDate = sql.NullTime{Time: d.expiryDate, Valid: true}

		err = cluster.UpdateInstance(ctx, tx.Tx(), d.project.Name, d.name, *object)
		if err != nil {
			return err
		}

		err = cluster.UpdateInstanceConfig(ctx, tx.Tx(), int64(object.ID), d.localConfig)
		if err != nil {
			return err
		}

		devices, err := cluster.APIToDevices(d.localDevices.CloneNative())
		if err != nil {
			return err
		}

		err = cluster.UpdateInstanceDevices(ctx, tx.Tx(), int64(object.ID), devices)
		if err != nil {
			return err
		}

		profileNames := make([]string, 0, len(d.profiles))
		for _, profile := range d.profiles {
			profileNames = append(profileNames, profile.Name)
		}

		return cluster.UpdateInstanceProfiles(ctx, tx.Tx(), object.ID, object.Project, profileNames)
	})
	if err != nil {
		return fmt.Errorf("Failed to update database: %w", err)
	}

	err = d.UpdateBackupFile()
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Failed to write backup file: %w", err)
	}

	// Send devIncus notifications
	if isRunning {
		// Config changes (only for user.* keys
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

	// Success, update the closure to mark that the changes should be kept.
	undoChanges = false

	if userRequested {
		if d.isSnapshot {
			d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceSnapshotUpdated.Event(d, nil))
		} else {
			d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceUpdated.Event(d, nil))
		}
	}

	return nil
}

// Export backs up the instance.
func (d *lxc) Export(w io.Writer, properties map[string]string, expiration time.Time) (api.ImageMetadata, error) {
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
	_, err := d.mount()
	if err != nil {
		d.logger.Error("Failed exporting instance", ctxMap)
		return meta, err
	}

	defer func() { _ = d.unmount() }()

	// Get IDMap to unshift container as the tarball is created.
	idmap, err := d.DiskIdmap()
	if err != nil {
		d.logger.Error("Failed exporting instance", ctxMap)
		return meta, err
	}

	// Create the tarball.
	tarWriter := instancewriter.NewInstanceTarWriter(w, idmap)

	// Keep track of the first path we saw for each path with nlink>1.
	cDir := d.Path()

	// Path inside the tar image is the pathname starting after cDir.
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

		tmpOffset := len(path.Dir(fnam)) + 1
		err = tarWriter.WriteFile(fnam[tmpOffset:], fnam, fi, false)
		if err != nil {
			_ = tarWriter.Close()
			d.logger.Debug("Error writing to tarfile", logger.Ctx{"err": err})
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
			tmpOffset := len(path.Dir(fnam)) + 1
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

	// Include all the rootfs files.
	fnam = d.RootfsPath()
	err = filepath.Walk(fnam, writeToTar)
	if err != nil {
		d.logger.Error("Failed exporting instance", ctxMap)
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

	d.logger.Info("Exported instance", ctxMap)
	return meta, nil
}

func collectCRIULogFile(d instance.Instance, imagesDir string, function string, method string) error {
	t := time.Now().Format(time.RFC3339)
	newPath := filepath.Join(d.LogPath(), fmt.Sprintf("%s_%s_%s.log", function, method, t))
	return internalUtil.FileCopy(filepath.Join(imagesDir, fmt.Sprintf("%s.log", method)), newPath)
}

func getCRIULogErrors(imagesDir string, method string) (string, error) {
	f, err := os.Open(path.Join(imagesDir, fmt.Sprintf("%s.log", method)))
	if err != nil {
		return "", err
	}

	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	ret := []string{}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "Error") || strings.Contains(line, "Warn") {
			ret = append(ret, scanner.Text())
		}
	}

	return strings.Join(ret, "\n"), nil
}

// Check if CRIU supports pre-dumping and number of pre-dump iterations.
func (d *lxc) migrationSendCheckForPreDumpSupport() (bool, int) {
	// Check if this architecture/kernel/criu combination supports pre-copy dirty memory tracking feature.
	_, err := subprocess.RunCommand("criu", "check", "--feature", "mem_dirty_track")
	if err != nil {
		// CRIU says it does not know about dirty memory tracking.
		// This means the rest of this function is irrelevant.
		return false, 0
	}

	// CRIU says it can actually do pre-dump. Let's set it to true
	// unless the user wants something else.
	usePreDumps := true

	// What does the configuration say about pre-copy
	tmp := d.ExpandedConfig()["migration.incremental.memory"]

	if tmp != "" {
		usePreDumps = util.IsTrue(tmp)
	}

	var maxIterations int

	// migration.incremental.memory.iterations is the value after which the
	// container will be definitely migrated, even if the remaining number
	// of memory pages is below the defined threshold.
	tmp = d.ExpandedConfig()["migration.incremental.memory.iterations"]
	if tmp != "" {
		maxIterations, _ = strconv.Atoi(tmp)
	} else {
		// default to 10
		maxIterations = 10
	}

	if maxIterations > 999 {
		// the pre-dump directory is hardcoded to a string
		// with maximal 3 digits. 999 pre-dumps makes no
		// sense at all, but let's make sure the number
		// is not higher than this.
		maxIterations = 999
	}

	logger.Debugf("Using maximal %d iterations for pre-dumping", maxIterations)

	return usePreDumps, maxIterations
}

func (d *lxc) migrationSendWriteActionScript(directory string, operation string, secret string, execPath string) error {
	script := fmt.Sprintf(`#!/bin/sh -e
if [ "$CRTOOLS_SCRIPT_ACTION" = "post-dump" ]; then
	%s migratedumpsuccess %s %s
fi
`, execPath, operation, secret)

	f, err := os.Create(filepath.Join(directory, "action.sh"))
	if err != nil {
		return err
	}

	err = f.Chmod(0500)
	if err != nil {
		return err
	}

	_, err = f.WriteString(script)
	if err != nil {
		return err
	}

	return f.Close()
}

func (d *lxc) MigrateSend(args instance.MigrateSendArgs) error {
	d.logger.Info("Migration send starting")
	defer d.logger.Info("Migration send stopped")

	// Wait for essential migration connections before negotiation.
	connectionsCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	filesystemConn, err := args.FilesystemConn(connectionsCtx)
	if err != nil {
		return err
	}

	var stateConn io.ReadWriteCloser
	if args.Live {
		stateConn, err = args.StateConn(connectionsCtx)
		if err != nil {
			return err
		}
	}

	pool, err := storagePools.LoadByInstance(d.state, d)
	if err != nil {
		return fmt.Errorf("Failed loading instance: %w", err)
	}

	// The refresh argument passed to MigrationTypes() is always set to false here.
	// The migration source/sender doesn't need to care whether or not it's doing a refresh as the migration
	// sink/receiver will know this, and adjust the migration types accordingly.
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

	// Add CRIU and predump info to source header.
	maxDumpIterations := 0
	if args.Live {
		var offerUsePreDumps bool
		offerUsePreDumps, maxDumpIterations = d.migrationSendCheckForPreDumpSupport()
		offerHeader.Predump = proto.Bool(offerUsePreDumps)
		offerHeader.Criu = migration.CRIUType_CRIU_RSYNC.Enum()
	} else {
		offerHeader.Predump = proto.Bool(false)

		if d.IsRunning() {
			// Indicate instance is running to target (can trigger MultiSync mode).
			offerHeader.Criu = migration.CRIUType_NONE.Enum()
		}
	}

	// Add idmap info to source header for containers.
	idmapset, err := d.DiskIdmap()
	if err != nil {
		return fmt.Errorf("Failed getting container disk idmap: %w", err)
	} else if idmapset != nil {
		offerHeader.Idmap = make([]*migration.IDMapType, 0, len(idmapset.Entries))
		for _, ctnIdmap := range idmapset.Entries {
			idmap := migration.IDMapType{
				Isuid:    proto.Bool(ctnIdmap.IsUID),
				Isgid:    proto.Bool(ctnIdmap.IsGID),
				Hostid:   proto.Int32(int32(ctnIdmap.HostID)),
				Nsid:     proto.Int32(int32(ctnIdmap.NSID)),
				Maprange: proto.Int32(int32(ctnIdmap.MapRange)),
			}

			offerHeader.Idmap = append(offerHeader.Idmap, &idmap)
		}
	}

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

	// Send offer to target.
	d.logger.Debug("Sending migration offer to target")
	err = args.ControlSend(offerHeader)
	if err != nil {
		return fmt.Errorf("Failed sending migration offer: %w", err)
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
			if util.ValueInSlice(allSnapshots[i].Name, volSourceArgs.Snapshots) {
				volSourceArgs.Info.Config.VolumeSnapshots = append(volSourceArgs.Info.Config.VolumeSnapshots, allSnapshots[i])
			}
		}
	}

	// If s.live is true or Criu is set to CRIUType_NONE rather than nil, it indicates that the source instance
	// is running, and if we are doing a non-optimized transfer (i.e using rsync or raw block transfer) then we
	// should do a two stage transfer to minimize downtime.
	instanceRunning := args.Live || (respHeader.Criu != nil && *respHeader.Criu == migration.CRIUType_NONE)
	nonOptimizedMigration := volSourceArgs.MigrationType.FSType == migration.MigrationFSType_RSYNC || volSourceArgs.MigrationType.FSType == migration.MigrationFSType_BLOCK_AND_RSYNC
	if instanceRunning && nonOptimizedMigration {
		// Indicate this info to the storage driver so that it can alter its behaviour if needed.
		volSourceArgs.MultiSync = true
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

	restoreSuccess := make(chan bool, 1)
	defer close(restoreSuccess)

	// Don't defer close this one as its needed potentially after this function has ended.
	dumpSuccess := make(chan error, 1)

	g.Go(func() error {
		d.logger.Debug("Migrate send transfer started")
		defer d.logger.Debug("Migrate send transfer finished")

		var err error

		d.logger.Debug("Starting storage migration phase")

		err = pool.MigrateInstance(d, filesystemConn, volSourceArgs, d.op)
		if err != nil {
			return err
		}

		d.logger.Debug("Finished storage migration phase")

		if args.Live {
			d.logger.Debug("Starting live migration phase")

			// Setup rsync options (used for CRIU state transfers).
			rsyncBwlimit := pool.Driver().Config()["rsync.bwlimit"]
			rsyncFeatures := respHeader.GetRsyncFeaturesSlice()
			if !util.ValueInSlice("bidirectional", rsyncFeatures) {
				// If no bi-directional support, assume 3.7 level.
				// NOTE: Do NOT extend this list of arguments.
				rsyncFeatures = []string{"xattrs", "delete", "compress"}
			}

			if respHeader.Criu == nil {
				return fmt.Errorf("Got no CRIU socket type for live migration")
			} else if *respHeader.Criu != migration.CRIUType_CRIU_RSYNC {
				return fmt.Errorf("Formats other than criu rsync not understood (%q)", respHeader.Criu)
			}

			checkpointDir, err := os.MkdirTemp("", "incus_checkpoint_")
			if err != nil {
				return err
			}

			if liblxc.RuntimeLiblxcVersionAtLeast(liblxc.Version(), 2, 0, 4) {
				// What happens below is slightly convoluted. Due to various complications
				// with networking, there's no easy way for criu to exit and leave the
				// container in a frozen state for us to somehow resume later.
				// Instead, we use what criu calls an "action-script", which is basically a
				// callback that lets us know when the dump is done. (Unfortunately, we
				// can't pass arguments, just an executable path, so we write a custom
				// action script with the real command we want to run.)
				// This script then blocks until the migration operation either finishes
				// successfully or fails, and exits 1 or 0, which causes criu to either
				// leave the container running or kill it as we asked.
				dumpDone := make(chan bool, 1)
				actionScriptOpSecret, err := internalUtil.RandomHexString(32)
				if err != nil {
					_ = os.RemoveAll(checkpointDir)
					return err
				}

				actionScriptOp, err := operations.OperationCreate(
					d.state,
					d.Project().Name,
					operations.OperationClassWebsocket,
					operationtype.InstanceLiveMigrate,
					nil,
					nil,
					func(op *operations.Operation) error {
						result := <-restoreSuccess
						if !result {
							return fmt.Errorf("restore failed, failing CRIU")
						}

						return nil
					},
					nil,
					func(op *operations.Operation, r *http.Request, w http.ResponseWriter) error {
						secret := r.FormValue("secret")
						if secret == "" {
							return fmt.Errorf("Missing action script secret")
						}

						if secret != actionScriptOpSecret {
							return os.ErrPermission
						}

						c, err := ws.Upgrader.Upgrade(w, r, nil)
						if err != nil {
							return err
						}

						dumpDone <- true

						closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
						return c.WriteMessage(websocket.CloseMessage, closeMsg)
					},
					nil,
				)
				if err != nil {
					_ = os.RemoveAll(checkpointDir)
					return err
				}

				err = d.migrationSendWriteActionScript(checkpointDir, actionScriptOp.URL(), actionScriptOpSecret, d.state.OS.ExecPath)
				if err != nil {
					_ = os.RemoveAll(checkpointDir)
					return err
				}

				preDumpCounter := 0
				preDumpDir := ""

				// Check if the other side knows about pre-dumping and the associated
				// rsync protocol.
				if respHeader.GetPredump() {
					d.logger.Debug("The other side does support pre-copy")
					final := false
					for !final {
						preDumpCounter++
						if preDumpCounter < maxDumpIterations {
							final = false
						} else {
							final = true
						}

						dumpDir := fmt.Sprintf("%03d", preDumpCounter)
						loopArgs := preDumpLoopArgs{
							stateConn:     stateConn,
							checkpointDir: checkpointDir,
							bwlimit:       rsyncBwlimit,
							preDumpDir:    preDumpDir,
							dumpDir:       dumpDir,
							final:         final,
							rsyncFeatures: rsyncFeatures,
						}

						final, err = d.migrateSendPreDumpLoop(&loopArgs)
						if err != nil {
							_ = os.RemoveAll(checkpointDir)
							return err
						}

						preDumpDir = fmt.Sprintf("%03d", preDumpCounter)
						preDumpCounter++
					}
				} else {
					d.logger.Debug("The other side does not support pre-copy")
				}

				err = actionScriptOp.Start()
				if err != nil {
					_ = os.RemoveAll(checkpointDir)
					return err
				}

				go func() {
					d.logger.Debug("Final CRIU dump started")
					defer d.logger.Debug("Final CRIU dump stopped")
					criuMigrationArgs := instance.CriuMigrationArgs{
						Cmd:          liblxc.MIGRATE_DUMP,
						Stop:         true,
						ActionScript: true,
						PreDumpDir:   preDumpDir,
						DumpDir:      "final",
						StateDir:     checkpointDir,
						Function:     "migration",
					}

					// Do the final CRIU dump. This is needs no special handling if
					// pre-dumps are used or not.
					dumpSuccess <- d.migrate(&criuMigrationArgs)
					_ = os.RemoveAll(checkpointDir)
				}()

				select {
				// The checkpoint failed, let's just abort.
				case err = <-dumpSuccess:
					return err
				// The dump finished, let's continue on to the restore.
				case <-dumpDone:
					d.logger.Debug("Dump finished, continuing with restore...")
				}
			} else {
				d.logger.Debug("The version of liblxc is older than 2.0.4 and the live migration will probably fail")
				defer func() { _ = os.RemoveAll(checkpointDir) }()
				criuMigrationArgs := instance.CriuMigrationArgs{
					Cmd:          liblxc.MIGRATE_DUMP,
					StateDir:     checkpointDir,
					Function:     "migration",
					Stop:         true,
					ActionScript: false,
					DumpDir:      "final",
					PreDumpDir:   "",
				}

				err = d.migrate(&criuMigrationArgs)
				if err != nil {
					return err
				}
			}

			// We do the transfer serially right now, but there's really no reason for us to;
			// since we have separate websockets, we can do it in parallel if we wanted to.
			// However assuming we're network bound, there's really no reason to do these in.
			// parallel. In the future when we're using p.haul's protocol, it will make sense
			// to do these in parallel.
			ctName, _, _ := api.GetParentAndSnapshotName(d.Name())
			err = rsync.Send(ctName, internalUtil.AddSlash(checkpointDir), stateConn, nil, rsyncFeatures, rsyncBwlimit, d.state.OS.ExecPath)
			if err != nil {
				return err
			}

			d.logger.Debug("Finished live migration phase")
		}

		// Perform final sync if in multi sync mode.
		if volSourceArgs.MultiSync {
			d.logger.Debug("Starting final storage migration phase")

			// Indicate to the storage driver we are doing final sync and because of this don't send
			// snapshots as they don't need to have a final sync as not being modified.
			volSourceArgs.FinalSync = true
			volSourceArgs.Snapshots = nil
			volSourceArgs.Info.Config.VolumeSnapshots = nil

			err = pool.MigrateInstance(d, filesystemConn, volSourceArgs, d.op)
			if err != nil {
				return err
			}

			d.logger.Debug("Finished final storage migration phase")
		}

		return nil
	})

	{
		// Wait for routines to finish and collect first error.
		err := g.Wait()

		if args.Live {
			restoreSuccess <- err == nil

			if err == nil {
				err := <-dumpSuccess
				if err != nil {
					d.logger.Error("Dump failed after successful restore", logger.Ctx{"err": err})
				}
			}
		}

		return err
	}
}

type preDumpLoopArgs struct {
	stateConn     io.ReadWriteCloser
	checkpointDir string
	bwlimit       string
	preDumpDir    string
	dumpDir       string
	final         bool
	rsyncFeatures []string
}

// migrateSendPreDumpLoop is the main logic behind the pre-copy migration.
// This function contains the actual pre-dump, the corresponding rsync transfer and it tells the outer loop to
// abort if the threshold of memory pages transferred by pre-dumping has been reached.
func (d *lxc) migrateSendPreDumpLoop(args *preDumpLoopArgs) (bool, error) {
	// Do a CRIU pre-dump
	criuMigrationArgs := instance.CriuMigrationArgs{
		Cmd:          liblxc.MIGRATE_PRE_DUMP,
		Stop:         false,
		ActionScript: false,
		PreDumpDir:   args.preDumpDir,
		DumpDir:      args.dumpDir,
		StateDir:     args.checkpointDir,
		Function:     "migration",
	}

	d.logger.Debug("Doing another CRIU pre-dump", logger.Ctx{"preDumpDir": args.preDumpDir})

	final := args.final

	if d.Type() != instancetype.Container {
		return false, fmt.Errorf("Instance is not container type")
	}

	err := d.migrate(&criuMigrationArgs)
	if err != nil {
		return final, fmt.Errorf("Failed sending instance: %w", err)
	}

	// Send the pre-dump.
	ctName, _, _ := api.GetParentAndSnapshotName(d.Name())
	err = rsync.Send(ctName, internalUtil.AddSlash(args.checkpointDir), args.stateConn, nil, args.rsyncFeatures, args.bwlimit, d.state.OS.ExecPath)
	if err != nil {
		return final, err
	}

	// The function readCriuStatsDump() reads the CRIU 'stats-dump' file
	// in path and returns the pages_written, pages_skipped_parent, error.
	readCriuStatsDump := func(path string) (uint64, uint64, error) {
		// Get dump statistics with crit
		dumpStats, err := crit.GetDumpStats(path)
		if err != nil {
			return 0, 0, fmt.Errorf("Failed to parse CRIU's 'stats-dump' file: %w", err)
		}

		return dumpStats.GetPagesWritten(), dumpStats.GetPagesSkippedParent(), nil
	}

	// Read the CRIU's 'stats-dump' file
	dumpPath := internalUtil.AddSlash(args.checkpointDir)
	dumpPath += internalUtil.AddSlash(args.dumpDir)
	written, skippedParent, err := readCriuStatsDump(dumpPath)
	if err != nil {
		return final, err
	}

	totalPages := written + skippedParent
	var percentageSkipped int
	if totalPages > 0 {
		percentageSkipped = int(100 - ((100 * written) / totalPages))
	}

	d.logger.Debug("CRIU pages", logger.Ctx{"pages": written, "skipped": skippedParent, "skippedPerc": percentageSkipped})

	// threshold is the percentage of memory pages that needs
	// to be pre-copied for the pre-copy migration to stop.
	var threshold int
	tmp := d.ExpandedConfig()["migration.incremental.memory.goal"]
	if tmp != "" {
		threshold, _ = strconv.Atoi(tmp)
	} else {
		// defaults to 70%
		threshold = 70
	}

	if percentageSkipped > threshold {
		d.logger.Debug("Memory pages skipped due to pre-copy is larger than threshold", logger.Ctx{"skippedPerc": percentageSkipped, "thresholdPerc": threshold})
		d.logger.Debug("This was the last pre-dump; next dump is the final dump")
		final = true
	}

	// If in pre-dump mode, the receiving side expects a message to know if this was the last pre-dump.
	logger.Debug("Sending another CRIU pre-dump header")
	sync := migration.MigrationSync{
		FinalPreDump: proto.Bool(final),
	}

	data, err := proto.Marshal(&sync)
	if err != nil {
		return false, err
	}

	_, err = args.stateConn.Write(data)
	if err != nil {
		return final, err
	}

	d.logger.Debug("Sending another CRIU pre-dump header done")

	return final, nil
}

func (d *lxc) resetContainerDiskIdmap(srcIdmap *idmap.Set) error {
	dstIdmap, err := d.DiskIdmap()
	if err != nil {
		return err
	}

	if dstIdmap == nil {
		dstIdmap = new(idmap.Set)
	}

	if !srcIdmap.Equals(dstIdmap) {
		jsonIdmap, err := srcIdmap.ToJSON()
		if err != nil {
			return fmt.Errorf("Failed to encode ID map: %w", err)
		}

		d.logger.Debug("Setting new volatile.last_state.idmap from source instance", logger.Ctx{"sourceIdmap": srcIdmap})
		err = d.VolatileSet(map[string]string{"volatile.last_state.idmap": jsonIdmap})
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *lxc) MigrateReceive(args instance.MigrateReceiveArgs) error {
	d.logger.Info("Migration receive starting")
	defer d.logger.Info("Migration receive stopped")

	// Wait for essential migration connections before negotiation.
	connectionsCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	filesystemConn, err := args.FilesystemConn(connectionsCtx)
	if err != nil {
		return err
	}

	var stateConn io.ReadWriteCloser
	if args.Live {
		stateConn, err = args.StateConn(connectionsCtx)
		if err != nil {
			return err
		}
	}

	// Receive offer from source.
	d.logger.Debug("Waiting for migration offer from source")
	offerHeader := &migration.MigrationHeader{}
	err = args.ControlReceive(offerHeader)
	if err != nil {
		return fmt.Errorf("Failed receiving migration offer from source: %w", err)
	}

	criuType := migration.CRIUType_CRIU_RSYNC.Enum()
	if offerHeader.Criu != nil && *offerHeader.Criu == migration.CRIUType_NONE {
		criuType = migration.CRIUType_NONE.Enum()
	} else {
		if !args.Live {
			criuType = nil
		}
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

	// Add CRIU info to response.
	respHeader.Criu = criuType

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

	if offerHeader.GetPredump() {
		// If the other side wants pre-dump and if this side supports it, let's use it.
		respHeader.Predump = proto.Bool(true)
	} else {
		respHeader.Predump = proto.Bool(false)
	}

	// Get rsync options from sender, these are passed into mySink function as part of
	// MigrationSinkArgs below.
	rsyncFeatures := respHeader.GetRsyncFeaturesSlice()

	// Send response to source.
	d.logger.Debug("Sending migration response to source")
	err = args.ControlSend(respHeader)
	if err != nil {
		return fmt.Errorf("Failed sending migration response to source: %w", err)
	}

	d.logger.Debug("Sent migration response to source")

	srcIdmap := new(idmap.Set)
	for _, idmapSet := range offerHeader.Idmap {
		e := idmap.Entry{
			IsUID:    *idmapSet.Isuid,
			IsGID:    *idmapSet.Isgid,
			NSID:     int64(*idmapSet.Nsid),
			HostID:   int64(*idmapSet.Hostid),
			MapRange: int64(*idmapSet.Maprange),
		}

		srcIdmap.Entries = append(srcIdmap.Entries, e)
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

	// Start filesystem transfer routine and initialise a channel that is closed when the routine finishes.
	fsTransferDone := make(chan struct{})
	g.Go(func() error {
		defer close(fsTransferDone)

		d.logger.Debug("Migrate receive filesystem transfer started")
		defer d.logger.Debug("Migrate receive filesystem transfer finished")

		var err error

		// We do the fs receive in parallel so we don't have to reason about when to receive
		// what. The sending side is smart enough to send the filesystem bits that it can
		// before it seizes the container to start checkpointing, so the total transfer time
		// will be minimized even if we're dumb here.
		snapshots := []*migration.Snapshot{}

		// Legacy: we only sent the snapshot names, so we just copy the container's
		// config over, same as we used to do.
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

		// Default to not expecting to receive the final rootfs sync.
		sendFinalFsDelta := false

		// If we are doing a stateful live transfer or the CRIU type indicates we
		// are doing a stateless transfer with a running instance then we should
		// expect the source to send us a final rootfs sync.
		if args.Live {
			sendFinalFsDelta = true
		} else if criuType != nil && *criuType == migration.CRIUType_NONE {
			sendFinalFsDelta = true
		}

		volTargetArgs := localMigration.VolumeTargetArgs{
			IndexHeaderVersion:    respHeader.GetIndexHeaderVersion(),
			Name:                  d.Name(),
			MigrationType:         respTypes[0],
			Refresh:               args.Refresh,                // Indicate to receiver volume should exist.
			TrackProgress:         true,                        // Use a progress tracker on receiver to get in-cluster progress information.
			Live:                  sendFinalFsDelta,            // Indicates we will get a final rootfs sync.
			VolumeSize:            offerHeader.GetVolumeSize(), // Block size setting override.
			VolumeOnly:            !args.Snapshots,
			ClusterMoveSourceName: args.ClusterMoveSourceName,
		}

		// At this point we have already figured out the parent container's root
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

					// Ensure that snapshot and parent container have the same
					// storage pool in their local root disk device. If the root
					// disk device for the snapshot comes from a profile on the
					// new instance as well we don't need to do anything.
					if snapArgs.Devices != nil {
						snapLocalRootDiskDeviceKey, _, _ := internalInstance.GetRootDiskDevice(snapArgs.Devices.CloneNative())
						if snapLocalRootDiskDeviceKey != "" {
							snapArgs.Devices[snapLocalRootDiskDeviceKey]["pool"] = parentStoragePool
						}
					}

					// Create the snapshot instance.
					_, snapInstOp, cleanup, err := instance.CreateInternal(d.state, *snapArgs, true)
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

		isRemoteClusterMove := args.ClusterMoveSourceName != "" && pool.Driver().Info().Remote

		// Only delete all instance volumes on error if the pool volume creation has succeeded to
		// avoid deleting an existing conflicting volume.
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

		// For containers, the fs map of the source is sent as part of the migration
		// stream, then at the end we need to record that map as last_state so that
		// shifting can happen on startup if needed.
		err = d.resetContainerDiskIdmap(srcIdmap)
		if err != nil {
			return err
		}

		if args.ClusterMoveSourceName != d.name {
			err = d.DeferTemplateApply(instance.TemplateTriggerCopy)
			if err != nil {
				return err
			}
		}

		return nil
	})

	// Start live state transfer routine (if required) and initialise a channel that is closed when the
	// routine finishes. It is never closed if the routine is not started.
	stateTransferDone := make(chan struct{})
	if args.Live {
		g.Go(func() error {
			d.logger.Debug("Migrate receive state transfer started")
			defer d.logger.Debug("Migrate receive state transfer finished")

			defer close(stateTransferDone)

			imagesDir, err := os.MkdirTemp("", "incus_restore_")
			if err != nil {
				return err
			}

			defer func() { _ = os.RemoveAll(imagesDir) }()

			sync := &migration.MigrationSync{
				FinalPreDump: proto.Bool(false),
			}

			if respHeader.GetPredump() {
				for !sync.GetFinalPreDump() {
					d.logger.Debug("Waiting to receive pre-dump rsync")

					// Transfer a CRIU pre-dump.
					err = rsync.Recv(internalUtil.AddSlash(imagesDir), stateConn, nil, rsyncFeatures)
					if err != nil {
						return fmt.Errorf("Failed receiving pre-dump rsync: %w", err)
					}

					d.logger.Debug("Done receiving pre-dump rsync")

					d.logger.Debug("Waiting to receive pre-dump header")

					// We can't use io.ReadAll here because sender doesn't call Close() to
					// send the frame end indicator after writing the pre-dump header.
					// So define a small buffer sufficient to fit migration.MigrationSync and
					// then read what we have into it.
					buf := make([]byte, 128)
					n, err := stateConn.Read(buf)
					if err != nil {
						return fmt.Errorf("Failed receiving pre-dump header: %w", err)
					}

					err = proto.Unmarshal(buf[:n], sync)
					if err != nil {
						return fmt.Errorf("Failed unmarshalling pre-dump header: %w (%v)", err, string(buf))
					}

					d.logger.Debug("Done receiving pre-dump header")
				}
			}

			// Final CRIU dump.
			d.logger.Debug("About to receive final dump rsync")
			err = rsync.Recv(internalUtil.AddSlash(imagesDir), stateConn, nil, rsyncFeatures)
			if err != nil {
				return fmt.Errorf("Failed receiving final dump rsync: %w", err)
			}

			d.logger.Debug("Done receiving final dump rsync")

			// Wait until filesystem transfer is done before starting final state sync and restore.
			<-fsTransferDone

			// But only proceed if no errors have occurred thus far.
			err = ctx.Err()
			if err != nil {
				return err
			}

			criuMigrationArgs := instance.CriuMigrationArgs{
				Cmd:          liblxc.MIGRATE_RESTORE,
				StateDir:     imagesDir,
				Function:     "migration",
				Stop:         false,
				ActionScript: false,
				DumpDir:      "final",
				PreDumpDir:   "",
			}

			// Currently we only do a single CRIU pre-dump so we can hardcode "final"
			// here since we know that "final" is the folder for CRIU's final dump.
			err = d.migrate(&criuMigrationArgs)
			if err != nil {
				return err
			}

			return nil
		})
	}

	{
		// Wait until the filesystem transfer and state transfer routines have finished.
		<-fsTransferDone
		if args.Live {
			<-stateTransferDone
		}

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

// Migrate migrates the instance to another node.
func (d *lxc) migrate(args *instance.CriuMigrationArgs) error {
	ctxMap := logger.Ctx{
		"created":      d.creationDate,
		"ephemeral":    d.ephemeral,
		"used":         d.lastUsedDate,
		"statedir":     args.StateDir,
		"actionscript": args.ActionScript,
		"predumpdir":   args.PreDumpDir,
		"features":     args.Features,
		"stop":         args.Stop}

	_, err := exec.LookPath("criu")
	if err != nil {
		return localMigration.ErrNoLiveMigration
	}

	d.logger.Info("Migrating container", ctxMap)

	prettyCmd := ""
	switch args.Cmd {
	case liblxc.MIGRATE_PRE_DUMP:
		prettyCmd = "pre-dump"
	case liblxc.MIGRATE_DUMP:
		prettyCmd = "dump"
	case liblxc.MIGRATE_RESTORE:
		prettyCmd = "restore"
	default:
		prettyCmd = "unknown"
		d.logger.Warn("Unknown migrate call", logger.Ctx{"cmd": args.Cmd})
	}

	pool, err := d.getStoragePool()
	if err != nil {
		return err
	}

	preservesInodes := pool.Driver().Info().PreservesInodes

	/* This feature was only added in 2.0.1, let's not ask for it
	 * before then or migrations will fail.
	 */
	if !liblxc.RuntimeLiblxcVersionAtLeast(liblxc.Version(), 2, 0, 1) {
		preservesInodes = false
	}

	finalStateDir := args.StateDir
	var migrateErr error

	/* For restore, we need an extra fork so that we daemonize the monitor
	 * instead of having it be a child. So let's hijack the command
	 * here and do the extra fork.
	 */
	if args.Cmd == liblxc.MIGRATE_RESTORE {
		// Check that we're not already running.
		if d.IsRunning() {
			return fmt.Errorf("The container is already running")
		}

		// Run the shared start code.
		configPath, postStartHooks, err := d.startCommon()
		if err != nil {
			if args.Op != nil {
				args.Op.Done(err)
			}

			return err
		}

		/*
		 * For unprivileged containers we need to shift the
		 * perms on the images images so that they can be
		 * opened by the process after it is in its user
		 * namespace.
		 */
		idmapset, err := d.CurrentIdmap()
		if err != nil {
			return err
		}

		if idmapset != nil {
			storageType, err := d.getStorageType()
			if err != nil {
				return fmt.Errorf("Storage type: %w", err)
			}

			if storageType == "zfs" {
				err = idmapset.ShiftPath(args.StateDir, storageDrivers.ShiftZFSSkipper)
			} else if storageType == "btrfs" {
				err = storageDrivers.ShiftBtrfsRootfs(args.StateDir, idmapset)
			} else {
				err = idmapset.ShiftPath(args.StateDir, nil)
			}

			if err != nil {
				return err
			}
		}

		if args.DumpDir != "" {
			finalStateDir = fmt.Sprintf("%s/%s", args.StateDir, args.DumpDir)
		}

		_, migrateErr = subprocess.RunCommand(
			d.state.OS.ExecPath,
			"forkmigrate",
			d.name,
			d.state.OS.LxcPath,
			configPath,
			finalStateDir,
			fmt.Sprintf("%v", preservesInodes),
		)

		if migrateErr == nil {
			// Run any post start hooks.
			err = d.runHooks(postStartHooks)
			if err != nil {
				if args.Op != nil {
					args.Op.Done(err) // Must come before Stop() otherwise stop will not proceed.
				}

				// Attempt to stop container.
				_ = d.Stop(false)

				return err
			}
		}
	} else {
		// Load the go-lxc struct
		var cc *liblxc.Container
		if d.expandedConfig["raw.lxc"] != "" {
			cc, err = d.initLXC(true)
			if err != nil {
				return err
			}

			err = d.loadRawLXCConfig(cc)
			if err != nil {
				return err
			}
		} else {
			cc, err = d.initLXC(false)
			if err != nil {
				return err
			}
		}

		script := ""
		if args.ActionScript {
			script = filepath.Join(args.StateDir, "action.sh")
		}

		if args.DumpDir != "" {
			finalStateDir = fmt.Sprintf("%s/%s", args.StateDir, args.DumpDir)
		}

		// TODO: make this configurable? Ultimately I think we don't
		// want to do that; what we really want to do is have "modes"
		// of criu operation where one is "make this succeed" and the
		// other is "make this fast". Anyway, for now, let's choose a
		// really big size so it almost always succeeds, even if it is
		// slow.
		ghostLimit := uint64(256 * 1024 * 1024)

		opts := liblxc.MigrateOptions{
			Stop:            args.Stop,
			Directory:       finalStateDir,
			Verbose:         true,
			PreservesInodes: preservesInodes,
			ActionScript:    script,
			GhostLimit:      ghostLimit,
		}

		if args.PreDumpDir != "" {
			opts.PredumpDir = fmt.Sprintf("../%s", args.PreDumpDir)
		}

		if !d.IsRunning() {
			// otherwise the migration will needlessly fail
			args.Stop = false
		}

		migrateErr = cc.Migrate(args.Cmd, opts)
	}

	collectErr := collectCRIULogFile(d, finalStateDir, args.Function, prettyCmd)
	if collectErr != nil {
		d.logger.Error("Error collecting checkpoint log file", logger.Ctx{"err": collectErr})
	}

	if migrateErr != nil {
		log, err2 := getCRIULogErrors(finalStateDir, prettyCmd)
		if err2 == nil {
			d.logger.Info("Failed migrating container", ctxMap)
			migrateErr = fmt.Errorf("%s %s failed\n%s", args.Function, prettyCmd, log)
		}

		return migrateErr
	}

	d.logger.Info("Migrated container", ctxMap)

	return nil
}

func (d *lxc) templateApplyNow(trigger instance.TemplateTrigger) error {
	// If there's no metadata, just return
	fname := filepath.Join(d.Path(), "metadata.yaml")
	if !util.PathExists(fname) {
		return nil
	}

	// Parse the metadata
	content, err := os.ReadFile(fname)
	if err != nil {
		return fmt.Errorf("Failed to read metadata: %w", err)
	}

	metadata := new(api.ImageMetadata)
	err = yaml.Unmarshal(content, &metadata)

	if err != nil {
		return fmt.Errorf("Could not parse %s: %w", fname, err)
	}

	// Find rootUID and rootGID
	idmapset, err := d.DiskIdmap()
	if err != nil {
		return fmt.Errorf("Failed to set ID map: %w", err)
	}

	rootUID := int64(0)
	rootGID := int64(0)

	// Get the right uid and gid for the container
	if idmapset != nil {
		rootUID, rootGID = idmapset.ShiftIntoNS(0, 0)
	}

	// Figure out the container architecture
	arch, err := osarch.ArchitectureName(d.architecture)
	if err != nil {
		arch, err = osarch.ArchitectureName(d.state.OS.Architectures[0])
		if err != nil {
			return fmt.Errorf("Failed to detect system architecture: %w", err)
		}
	}

	// Generate the container metadata
	containerMeta := make(map[string]string)
	containerMeta["name"] = d.name
	containerMeta["type"] = "container"
	containerMeta["architecture"] = arch

	if d.ephemeral {
		containerMeta["ephemeral"] = "true"
	} else {
		containerMeta["ephemeral"] = "false"
	}

	if d.IsPrivileged() {
		containerMeta["privileged"] = "true"
	} else {
		containerMeta["privileged"] = "false"
	}

	// Go through the templates
	for tplPath, tpl := range metadata.Templates {
		err = func(tplPath string, tpl *api.ImageMetadataTemplate) error {
			var w *os.File

			// Check if the template should be applied now
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

			// Open the file to template, create if needed
			fullpath := filepath.Join(d.RootfsPath(), strings.TrimLeft(tplPath, "/"))
			if util.PathExists(fullpath) {
				if tpl.CreateOnly {
					return nil
				}

				// Open the existing file
				w, err = os.Create(fullpath)
				if err != nil {
					return fmt.Errorf("Failed to create template file: %w", err)
				}
			} else {
				// Create the directories leading to the file
				err = internalUtil.MkdirAllOwner(path.Dir(fullpath), 0755, int(rootUID), int(rootGID))
				if err != nil {
					return err
				}

				// Create the file itself
				w, err = os.Create(fullpath)
				if err != nil {
					return err
				}

				// Fix ownership and mode
				err = w.Chown(int(rootUID), int(rootGID))
				if err != nil {
					return err
				}

				err = w.Chmod(0644)
				if err != nil {
					return err
				}
			}
			defer func() { _ = w.Close() }()

			// Read the template
			tplString, err := os.ReadFile(filepath.Join(d.TemplatesPath(), tpl.Template))
			if err != nil {
				return fmt.Errorf("Failed to read template file: %w", err)
			}

			// Restrict filesystem access to within the container's rootfs
			tplSet := pongo2.NewSet(fmt.Sprintf("%s-%s", d.name, tpl.Template), template.ChrootLoader{Path: d.RootfsPath()})

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

			// Render the template
			err = tplRender.ExecuteWriter(pongo2.Context{"trigger": trigger,
				"path":       tplPath,
				"container":  containerMeta,
				"instance":   containerMeta,
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

func (d *lxc) inheritInitPidFd() (int, *os.File) {
	if d.state.OS.PidFds {
		pidFdFile, err := d.InitPidFd()
		if err != nil {
			return -1, nil
		}

		return 3, pidFdFile
	}

	return -1, nil
}

// FileSFTPConn returns a connection to the forkfile handler.
func (d *lxc) FileSFTPConn() (net.Conn, error) {
	// Lock to avoid concurrent spawning.
	spawnUnlock, err := locking.Lock(context.TODO(), fmt.Sprintf("forkfile_%d", d.id))
	if err != nil {
		return nil, err
	}

	defer spawnUnlock()

	// Create any missing directories in case the instance has never been started before.
	err = os.MkdirAll(d.RunPath(), 0700)
	if err != nil {
		return nil, err
	}

	// Trickery to handle paths > 108 chars.
	dirFile, err := os.Open(d.RunPath())
	if err != nil {
		return nil, err
	}

	defer func() { _ = dirFile.Close() }()

	forkfileAddr, err := net.ResolveUnixAddr("unix", fmt.Sprintf("/proc/self/fd/%d/forkfile.sock", dirFile.Fd()))
	if err != nil {
		return nil, err
	}

	// Attempt to connect on existing socket.
	forkfilePath := filepath.Join(d.RunPath(), "forkfile.sock")
	forkfileConn, err := net.DialUnix("unix", nil, forkfileAddr)
	if err == nil {
		// Found an existing server.
		return forkfileConn, nil
	}

	// Check for ongoing operations (that may involve shifting or replacing the root volume) so as to avoid
	// allowing SFTP access while the container's filesystem setup is in flux.
	// If there is an update operation ongoing and the instance is running then do not wait for the operation
	// to complete before continuing as it is possible that a disk device is being removed that requires SFTP
	// to clean up the path inside the container. Also it is not possible to be shifting/replacing the root
	// volume when the instance is running, so there should be no reason to wait for the operation to finish.
	op := operationlock.Get(d.Project().Name, d.Name())
	if op.Action() != operationlock.ActionUpdate || !d.IsRunning() {
		_ = op.Wait(context.Background())
	}

	// Setup reverter.
	revert := revert.New()
	defer revert.Fail()

	// Create the listener.
	_ = os.Remove(forkfilePath)
	forkfileListener, err := net.ListenUnix("unix", forkfileAddr)
	if err != nil {
		return nil, err
	}

	revert.Add(func() {
		_ = forkfileListener.Close()
		_ = os.Remove(forkfilePath)
	})

	// Spawn forkfile in a Go routine.
	chReady := make(chan error)
	go func() {
		// Lock to avoid concurrent running forkfile.
		runUnlock, err := locking.Lock(context.TODO(), d.forkfileRunningLockName())
		if err != nil {
			chReady <- err
			return
		}

		defer runUnlock()

		// Mount the filesystem if needed.
		if !d.IsRunning() {
			// Mount the root filesystem if required.
			_, err := d.mount()
			if err != nil {
				chReady <- err
				return
			}

			defer func() { _ = d.unmount() }()
		}

		// Start building the command.
		args := []string{
			d.state.OS.ExecPath,
			"forkfile",
			"--",
		}

		extraFiles := []*os.File{}

		// Get the listener file.
		forkfileFile, err := forkfileListener.File()
		if err != nil {
			chReady <- err
			return
		}

		defer func() { _ = forkfileFile.Close() }()

		args = append(args, "3")
		extraFiles = append(extraFiles, forkfileFile)

		// Get the rootfs.
		rootfsFile, err := os.Open(d.RootfsPath())
		if err != nil {
			chReady <- err
			return
		}

		defer func() { _ = rootfsFile.Close() }()

		args = append(args, "4")
		extraFiles = append(extraFiles, rootfsFile)

		// Get the pidfd.
		pidFdNr, pidFd := d.inheritInitPidFd()
		if pidFdNr >= 0 {
			defer func() { _ = pidFd.Close() }()
			args = append(args, "5")
			extraFiles = append(extraFiles, pidFd)
		} else {
			args = append(args, "-1")
		}

		// Finalize the args.
		args = append(args, fmt.Sprintf("%d", d.InitPID()))

		// Prepare sftp server.
		forkfile := exec.Cmd{
			Path:       d.state.OS.ExecPath,
			Args:       args,
			ExtraFiles: extraFiles,
		}

		var stderr bytes.Buffer
		forkfile.Stderr = &stderr

		if !d.IsRunning() {
			// Get the disk idmap.
			idmapset, err := d.DiskIdmap()
			if err != nil {
				chReady <- err
				return
			}

			if idmapset != nil {
				forkfile.SysProcAttr = &syscall.SysProcAttr{
					Cloneflags: syscall.CLONE_NEWUSER,
					Credential: &syscall.Credential{
						Uid: uint32(0),
						Gid: uint32(0),
					},
					UidMappings: idmapset.ToUIDMappings(),
					GidMappings: idmapset.ToGIDMappings(),
				}
			}
		}

		// Start the server.
		err = forkfile.Start()
		if err != nil {
			chReady <- fmt.Errorf("Failed to run forkfile: %w: %s", err, strings.TrimSpace(stderr.String()))
			return
		}

		// Write PID file.
		pidFile := filepath.Join(d.RunPath(), "forkfile.pid")
		err = os.WriteFile(pidFile, []byte(fmt.Sprintf("%d\n", forkfile.Process.Pid)), 0600)
		if err != nil {
			chReady <- fmt.Errorf("Failed to write forkfile PID: %w", err)
			return
		}

		// Close the listener and delete the socket immediately after forkfile exits to avoid clients
		// thinking a listener is available while other deferred calls are being processed.
		defer func() {
			_ = forkfileListener.Close()
			_ = os.Remove(forkfilePath)
			_ = os.Remove(pidFile)
		}()

		// Indicate the process was spawned without error.
		close(chReady)

		// Wait for completion.
		err = forkfile.Wait()
		if err != nil {
			d.logger.Error("SFTP server stopped with error", logger.Ctx{"err": err, "stderr": strings.TrimSpace(stderr.String())})
			return
		}
	}()

	// Wait for forkfile to have been spawned.
	err = <-chReady
	if err != nil {
		return nil, err
	}

	// Connect to the new server.
	forkfileConn, err = net.DialUnix("unix", nil, forkfileAddr)
	if err != nil {
		return nil, err
	}

	// All done.
	revert.Success()
	return forkfileConn, nil
}

// FileSFTP returns an SFTP connection to the forkfile handler.
func (d *lxc) FileSFTP() (*sftp.Client, error) {
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

// stopForkFile attempts to send SIGTERM (if force is true) or SIGINT to forkfile then waits for it to exit.
func (d *lxc) stopForkfile(force bool) {
	// Make sure that when the function exits, no forkfile is running by acquiring the lock (which indicates
	// that forkfile isn't running and holding the lock) and then releasing it.
	defer func() {
		unlock, err := locking.Lock(context.TODO(), d.forkfileRunningLockName())
		if err != nil {
			return
		}

		unlock()
	}()

	content, err := os.ReadFile(filepath.Join(d.RunPath(), "forkfile.pid"))
	if err != nil {
		return
	}

	pid, err := strconv.ParseInt(strings.TrimSpace(string(content)), 10, 64)
	if err != nil {
		return
	}

	d.logger.Debug("Stopping forkfile", logger.Ctx{"pid": pid, "force": force})

	if force {
		// Forcefully kill the running process.
		_ = unix.Kill(int(pid), unix.SIGTERM)
	} else {
		// Try to send SIGINT to forkfile to indicate it should not accept any new connection.
		_ = unix.Kill(int(pid), unix.SIGINT)
	}
}

// Console attaches to the instance console.
func (d *lxc) Console(protocol string) (*os.File, chan error, error) {
	if protocol != instance.ConsoleTypeConsole {
		return nil, nil, fmt.Errorf("Container instances don't support %q output", protocol)
	}

	chDisconnect := make(chan error, 1)

	args := []string{
		d.state.OS.ExecPath,
		"forkconsole",
		project.Instance(d.Project().Name, d.Name()),
		d.state.OS.LxcPath,
		filepath.Join(d.RunPath(), "lxc.conf"),
		"tty=0",
		"escape=-1"}

	idmapset, err := d.CurrentIdmap()
	if err != nil {
		return nil, nil, err
	}

	var rootUID, rootGID int64
	if idmapset != nil {
		rootUID, rootGID = idmapset.ShiftIntoNS(0, 0)
	}

	// Create a PTS pair.
	ptx, pty, err := linux.OpenPty(rootUID, rootGID)
	if err != nil {
		return nil, nil, err
	}

	// Switch the console file descriptor into raw mode.
	_, err = termios.MakeRaw(int(ptx.Fd()))
	if err != nil {
		return nil, nil, err
	}

	cmd := exec.Cmd{}
	cmd.Path = d.state.OS.ExecPath
	cmd.Args = args
	cmd.Stdin = pty
	cmd.Stdout = pty
	cmd.Stderr = pty

	err = cmd.Start()
	if err != nil {
		return nil, nil, err
	}

	go func() {
		err = cmd.Wait()
		_ = ptx.Close()
		_ = pty.Close()
	}()

	go func() {
		<-chDisconnect
		_ = cmd.Process.Kill()
	}()

	d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceConsole.Event(d, logger.Ctx{"type": instance.ConsoleTypeConsole}))

	return ptx, chDisconnect, nil
}

// ConsoleLog returns console log.
func (d *lxc) ConsoleLog(opts liblxc.ConsoleLogOptions) (string, error) {
	cc, err := d.initLXC(false)
	if err != nil {
		return "", err
	}

	msg, err := cc.ConsoleLog(opts)
	if err != nil {
		return "", err
	}

	if opts.ClearLog {
		d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceConsoleReset.Event(d, nil))
	} else if opts.ReadLog && opts.WriteToLogFile {
		d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceConsoleRetrieved.Event(d, nil))
	}

	return string(msg), nil
}

// Exec executes a command inside the instance.
func (d *lxc) Exec(req api.InstanceExecPost, stdin *os.File, stdout *os.File, stderr *os.File) (instance.Cmd, error) {
	// Generate the LXC config if missing.
	configPath := filepath.Join(d.RunPath(), "lxc.conf")
	if !util.PathExists(configPath) {
		cc, err := d.initLXC(true)
		if err != nil {
			return nil, fmt.Errorf("Load go-lxc struct: %w", err)
		}

		err = cc.SaveConfigFile(configPath)
		if err != nil {
			_ = os.Remove(configPath)
			return nil, err
		}
	}

	// Prepare the environment
	envSlice := []string{}

	for k, v := range req.Environment {
		envSlice = append(envSlice, fmt.Sprintf("%s=%s", k, v))
	}

	// Setup logfile
	logPath := filepath.Join(d.LogPath(), "forkexec.log")
	logFile, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_SYNC, 0644)
	if err != nil {
		return nil, err
	}

	defer func() { _ = logFile.Close() }()

	// Prepare the subcommand
	cname := project.Instance(d.Project().Name, d.Name())
	args := []string{
		d.state.OS.ExecPath,
		"forkexec",
		cname,
		d.state.OS.LxcPath,
		filepath.Join(d.RunPath(), "lxc.conf"),
		req.Cwd,
		fmt.Sprintf("%d", req.User),
		fmt.Sprintf("%d", req.Group),
	}

	if d.state.OS.CoreScheduling && !d.state.OS.ContainerCoreScheduling {
		args = append(args, "1")
	} else {
		args = append(args, "0")
	}

	args = append(args, "--")
	args = append(args, "env")
	args = append(args, envSlice...)

	args = append(args, "--")
	args = append(args, "cmd")
	args = append(args, req.Command...)

	cmd := exec.Cmd{}
	cmd.Path = d.state.OS.ExecPath
	cmd.Args = args

	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	// Mitigation for CVE-2019-5736
	useRexec := false
	if d.expandedConfig["raw.idmap"] != "" {
		err := instance.AllowedUnprivilegedOnlyMap(d.expandedConfig["raw.idmap"])
		if err != nil {
			useRexec = true
		}
	}

	if util.IsTrue(d.expandedConfig["security.privileged"]) {
		useRexec = true
	}

	if useRexec {
		cmd.Env = append(os.Environ(), "LXC_MEMFD_REXEC=1")
	}

	// Setup communication PIPE
	rStatus, wStatus, err := os.Pipe()
	defer func() { _ = rStatus.Close() }()
	if err != nil {
		return nil, err
	}

	cmd.ExtraFiles = []*os.File{stdin, stdout, stderr, wStatus}
	err = cmd.Start()
	_ = wStatus.Close()
	if err != nil {
		return nil, err
	}

	attachedPid := linux.ReadPid(rStatus)
	if attachedPid <= 0 {
		_ = cmd.Wait()
		d.logger.Error("Failed to retrieve PID of executing child process")
		return nil, fmt.Errorf("Failed to retrieve PID of executing child process")
	}

	d.logger.Debug("Retrieved PID of executing child process", logger.Ctx{"attachedPid": attachedPid})

	d.state.Events.SendLifecycle(d.project.Name, lifecycle.InstanceExec.Event(d, logger.Ctx{"command": req.Command}))

	instCmd := &lxcCmd{
		cmd:              &cmd,
		attachedChildPid: int(attachedPid),
	}

	return instCmd, nil
}

func (d *lxc) cpuState() api.InstanceStateCPU {
	cpu := api.InstanceStateCPU{}

	cc, err := d.initLXC(false)
	if err != nil {
		return cpu
	}

	// CPU usage in seconds
	cg, err := d.cgroup(cc, true)
	if err != nil {
		return cpu
	}

	if !d.state.OS.CGInfo.Supports(cgroup.CPUAcct, cg) {
		return cpu
	}

	value, err := cg.GetCPUAcctUsage()
	if err != nil {
		cpu.Usage = -1
		return cpu
	}

	cpu.Usage = value

	return cpu
}

func (d *lxc) diskState() map[string]api.InstanceStateDisk {
	disk := map[string]api.InstanceStateDisk{}

	for _, dev := range d.expandedDevices.Sorted() {
		if dev.Config["type"] != "disk" {
			continue
		}

		var usage *storagePools.VolumeUsage

		if dev.Config["path"] == "/" {
			pool, err := storagePools.LoadByInstance(d.state, d)
			if err != nil {
				d.logger.Error("Error loading storage pool", logger.Ctx{"err": err})
				continue
			}

			usage, err = pool.GetInstanceUsage(d)
			if err != nil {
				if !errors.Is(err, storageDrivers.ErrNotSupported) {
					d.logger.Error("Error getting disk usage", logger.Ctx{"err": err})
				}

				continue
			}
		} else if dev.Config["pool"] != "" {
			pool, err := storagePools.LoadByName(d.state, dev.Config["pool"])
			if err != nil {
				d.logger.Error("Error loading storage pool", logger.Ctx{"poolName": dev.Config["pool"], "err": err})
				continue
			}

			usage, err = pool.GetCustomVolumeUsage(d.Project().Name, dev.Config["source"])
			if err != nil {
				if !errors.Is(err, storageDrivers.ErrNotSupported) {
					d.logger.Error("Error getting volume usage", logger.Ctx{"volume": dev.Config["source"], "err": err})
				}

				continue
			}
		} else {
			continue
		}

		state := api.InstanceStateDisk{}
		if usage != nil {
			state.Usage = usage.Used
			state.Total = usage.Total
		}

		disk[dev.Name] = state
	}

	return disk
}

func (d *lxc) memoryState() api.InstanceStateMemory {
	memory := api.InstanceStateMemory{}

	cc, err := d.initLXC(false)
	if err != nil {
		return memory
	}

	cg, err := d.cgroup(cc, true)
	if err != nil {
		return memory
	}

	if !d.state.OS.CGInfo.Supports(cgroup.Memory, cg) {
		return memory
	}

	// Memory in bytes
	value, err := cg.GetMemoryUsage()
	if err == nil {
		memory.Usage = value
	}

	// Memory peak in bytes
	if d.state.OS.CGInfo.Supports(cgroup.MemoryMaxUsage, cg) {
		value, err = cg.GetMemoryMaxUsage()
		if err == nil {
			memory.UsagePeak = value
		}
	}

	// Memory total in bytes
	value, err = cg.GetEffectiveMemoryLimit()
	if err == nil {
		memory.Total = value
	}

	if d.state.OS.CGInfo.Supports(cgroup.MemorySwapUsage, cg) {
		// Swap in bytes
		if memory.Usage > 0 {
			value, err := cg.GetMemorySwapUsage()
			if err == nil {
				memory.SwapUsage = value
			}
		}

		// Swap peak in bytes
		if memory.UsagePeak > 0 {
			value, err = cg.GetMemorySwapMaxUsage()
			if err == nil {
				memory.SwapUsagePeak = value
			}
		}
	}

	return memory
}

func (d *lxc) networkState(hostInterfaces []net.Interface) map[string]api.InstanceStateNetwork {
	result := map[string]api.InstanceStateNetwork{}

	pid := d.InitPID()
	if pid < 1 {
		return result
	}

	couldUseNetnsGetifaddrs := d.state.OS.NetnsGetifaddrs
	if couldUseNetnsGetifaddrs {
		nw, err := netutils.NetnsGetifaddrs(int32(pid), hostInterfaces)
		if err != nil {
			couldUseNetnsGetifaddrs = false
			d.logger.Warn("Failed to retrieve network information via netlink", logger.Ctx{"pid": pid})
		} else {
			result = nw
		}
	}

	if !couldUseNetnsGetifaddrs {
		pidFdNr, pidFd := d.inheritInitPidFd()
		if pidFdNr >= 0 {
			defer func() { _ = pidFd.Close() }()
		}

		// Get the network state from the container
		out, _, err := subprocess.RunCommandSplit(
			context.TODO(),
			nil,
			[]*os.File{pidFd},
			d.state.OS.ExecPath,
			"forknet",
			"info",
			"--",
			fmt.Sprintf("%d", pid),
			fmt.Sprintf("%d", pidFdNr))

		// Process forkgetnet response
		if err != nil {
			d.logger.Error("Error calling 'forknet", logger.Ctx{"err": err, "pid": pid})
			return result
		}

		// If we can use netns_getifaddrs() but it failed and the setns() +
		// netns_getifaddrs() succeeded we should just always fallback to the
		// setns() + netns_getifaddrs() style retrieval.
		d.state.OS.NetnsGetifaddrs = false

		nw := map[string]api.InstanceStateNetwork{}
		err = json.Unmarshal([]byte(out), &nw)
		if err != nil {
			d.logger.Error("Failure to read forknet json", logger.Ctx{"err": err})
			return result
		}

		result = nw
	}

	// Get host_name from volatile data if not set already.
	for name, dev := range result {
		if dev.HostName == "" {
			dev.HostName = d.localConfig[fmt.Sprintf("volatile.%s.host_name", name)]
			result[name] = dev
		}
	}

	return result
}

func (d *lxc) processesState(pid int) (int64, error) {
	// Return 0 if not running
	if pid == -1 {
		return 0, fmt.Errorf("PID of LXC instance could not be initialized")
	}

	cc, err := d.initLXC(false)
	if err != nil {
		return -1, err
	}

	cg, err := d.cgroup(cc, true)
	if err != nil {
		return 0, err
	}

	if d.state.OS.CGInfo.Supports(cgroup.Pids, cg) {
		value, err := cg.GetProcessesUsage()
		if err != nil {
			return -1, err
		}

		return value, nil
	}

	pids := []int64{int64(pid)}

	// Go through the pid list, adding new pids at the end so we go through them all
	for i := 0; i < len(pids); i++ {
		fname := fmt.Sprintf("/proc/%d/task/%d/children", pids[i], pids[i])
		fcont, err := os.ReadFile(fname)
		if err != nil {
			// the process terminated during execution of this loop
			continue
		}

		content := strings.Split(string(fcont), " ")
		for j := 0; j < len(content); j++ {
			pid, err := strconv.ParseInt(content[j], 10, 64)
			if err == nil {
				pids = append(pids, pid)
			}
		}
	}

	return int64(len(pids)), nil
}

// getStorageType returns the storage type of the instance's storage pool.
func (d *lxc) getStorageType() (string, error) {
	pool, err := d.getStoragePool()
	if err != nil {
		return "", err
	}

	return pool.Driver().Info().Name, nil
}

// mount the instance's rootfs volume if needed.
func (d *lxc) mount() (*storagePools.MountInfo, error) {
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

// unmount the instance's rootfs volume if needed.
func (d *lxc) unmount() error {
	pool, err := d.getStoragePool()
	if err != nil {
		return err
	}

	if d.IsSnapshot() {
		err = pool.UnmountInstanceSnapshot(d, nil)
		if err != nil {
			return err
		}

		return nil
	}

	err = pool.UnmountInstance(d, nil)
	if err != nil {
		return err
	}

	return nil
}

// insertMountGo inserts a mount into a container.
// This function is used for the seccomp notifier and so cannot call any
// functions that would cause LXC to talk to the container's monitor. Otherwise
// we'll have a deadlock (with a timeout but still). The InitPID() call here is
// the exception since the seccomp notifier will make sure to always pass a
// valid PID.
func (d *lxc) insertMountGo(source, target, fstype string, flags int, mntnsPID int, idmapType idmap.IdmapStorageType) error {
	pid := mntnsPID
	if pid <= 0 {
		// Get the init PID
		pid = d.InitPID()
		if pid == -1 {
			// Container isn't running
			return fmt.Errorf("Can't insert mount into stopped container")
		}
	}

	// Create the temporary mount target
	var tmpMount string
	var err error
	if internalUtil.IsDir(source) {
		tmpMount, err = os.MkdirTemp(d.ShmountsPath(), "incus_mount_")
		if err != nil {
			return fmt.Errorf("Failed to create shmounts path: %s", err)
		}
	} else {
		f, err := os.CreateTemp(d.ShmountsPath(), "incus_mount_")
		if err != nil {
			return fmt.Errorf("Failed to create shmounts path: %s", err)
		}

		tmpMount = f.Name()
		_ = f.Close()
	}

	defer func() { _ = os.Remove(tmpMount) }()

	// Mount the filesystem
	err = unix.Mount(source, tmpMount, fstype, uintptr(flags), "")
	if err != nil {
		return fmt.Errorf("Failed to setup temporary mount: %s", err)
	}

	defer func() { _ = unix.Unmount(tmpMount, unix.MNT_DETACH) }()

	// Ensure that only flags modifying mount _properties_ make it through.
	// Strip things such as MS_BIND which would cause the creation of a
	// shifted mount to be skipped.
	// (Fyi, this is just one of the reasons why multiplexers are bad;
	// specifically when they do heinous things such as confusing flags
	// with commands.)

	// This is why multiplexers are bad
	shiftfsFlags := (flags & (unix.MS_RDONLY |
		unix.MS_NOSUID |
		unix.MS_NODEV |
		unix.MS_NOEXEC |
		unix.MS_DIRSYNC |
		unix.MS_NOATIME |
		unix.MS_NODIRATIME))

	// Move the mount inside the container
	mntsrc := filepath.Join("/dev/.incus-mounts", filepath.Base(tmpMount))
	pidStr := fmt.Sprintf("%d", pid)

	pidFdNr, pidFd := seccomp.MakePidFd(pid, d.state)
	if pidFdNr >= 0 {
		defer func() { _ = pidFd.Close() }()
	}

	if !strings.HasPrefix(target, "/") {
		target = "/" + target
	}

	_, err = subprocess.RunCommandInheritFds(
		context.Background(),
		[]*os.File{pidFd},
		d.state.OS.ExecPath,
		"forkmount",
		"go-mount",
		"--",
		pidStr,
		fmt.Sprintf("%d", pidFdNr),
		mntsrc,
		target,
		string(idmapType),
		fmt.Sprintf("%d", shiftfsFlags))
	if err != nil {
		return err
	}

	return nil
}

func (d *lxc) insertMountLXC(source, target, fstype string, flags int) error {
	cname := project.Instance(d.Project().Name, d.Name())
	configPath := filepath.Join(d.RunPath(), "lxc.conf")
	if fstype == "" {
		fstype = "none"
	}

	if !strings.HasPrefix(target, "/") {
		target = "/" + target
	}

	_, err := subprocess.RunCommand(
		d.state.OS.ExecPath,
		"forkmount",
		"lxc-mount",
		"--",
		cname,
		d.state.OS.LxcPath,
		configPath,
		source,
		target,
		fstype,
		fmt.Sprintf("%d", flags))
	if err != nil {
		return err
	}

	return nil
}

func (d *lxc) moveMount(source, target, fstype string, flags int, idmapType idmap.IdmapStorageType) error {
	// Get the init PID
	pid := d.InitPID()
	if pid == -1 {
		// Container isn't running
		return fmt.Errorf("Can't insert mount into stopped container")
	}

	switch idmapType {
	case idmap.IdmapStorageIdmapped:
	case idmap.IdmapStorageNone:
	default:
		return fmt.Errorf("Invalid idmap value specified")
	}

	pidFdNr, pidFd := seccomp.MakePidFd(pid, d.state)
	if pidFdNr >= 0 {
		defer func() { _ = pidFd.Close() }()
	}

	pidStr := fmt.Sprintf("%d", pid)

	if !strings.HasPrefix(target, "/") {
		target = "/" + target
	}

	_, err := subprocess.RunCommandInheritFds(
		context.Background(),
		[]*os.File{pidFd},
		d.state.OS.ExecPath,
		"forkmount",
		"move-mount",
		"--",
		pidStr,
		fmt.Sprintf("%d", pidFdNr),
		fstype,
		source,
		target,
		string(idmapType),
		fmt.Sprintf("%d", flags))
	if err != nil {
		return err
	}

	return nil
}

func (d *lxc) insertMount(source, target, fstype string, flags int, idmapType idmap.IdmapStorageType) error {
	if d.state.OS.IdmappedMounts && idmapType == idmap.IdmapStorageIdmapped {
		return d.moveMount(source, target, fstype, flags, idmapType)
	}

	if d.state.OS.LXCFeatures["mount_injection_file"] && idmapType == idmap.IdmapStorageNone {
		return d.insertMountLXC(source, target, fstype, flags)
	}

	return d.insertMountGo(source, target, fstype, flags, -1, idmapType)
}

func (d *lxc) removeMount(mount string) error {
	// Get the init PID
	pid := d.InitPID()
	if pid == -1 {
		// Container isn't running
		return fmt.Errorf("Can't remove mount from stopped container")
	}

	if d.state.OS.LXCFeatures["mount_injection_file"] {
		configPath := filepath.Join(d.RunPath(), "lxc.conf")
		cname := project.Instance(d.Project().Name, d.Name())

		if !strings.HasPrefix(mount, "/") {
			mount = "/" + mount
		}

		_, err := subprocess.RunCommand(
			d.state.OS.ExecPath,
			"forkmount",
			"lxc-umount",
			"--",
			cname,
			d.state.OS.LxcPath,
			configPath,
			mount)
		if err != nil {
			return err
		}
	} else {
		// Remove the mount from the container
		pidFdNr, pidFd := d.inheritInitPidFd()
		if pidFdNr >= 0 {
			defer func() { _ = pidFd.Close() }()
		}

		_, err := subprocess.RunCommandInheritFds(
			context.TODO(),
			[]*os.File{pidFd},
			d.state.OS.ExecPath,
			"forkmount",
			"go-umount",
			"--",
			fmt.Sprintf("%d", pid),
			fmt.Sprintf("%d", pidFdNr),
			mount)
		if err != nil {
			return err
		}
	}

	return nil
}

// InsertSeccompUnixDevice inserts a seccomp device.
func (d *lxc) InsertSeccompUnixDevice(prefix string, m deviceConfig.Device, pid int) error {
	if pid < 0 {
		return fmt.Errorf("Invalid request PID specified")
	}

	rootLink := fmt.Sprintf("/proc/%d/root", pid)
	rootPath, err := os.Readlink(rootLink)
	if err != nil {
		return err
	}

	uid, gid, _, _, err := seccomp.TaskIDs(pid)
	if err != nil {
		return err
	}

	idmapset, err := d.CurrentIdmap()
	if err != nil {
		return err
	}

	nsuid, nsgid := idmapset.ShiftFromNS(uid, gid)
	m["uid"] = fmt.Sprintf("%d", nsuid)
	m["gid"] = fmt.Sprintf("%d", nsgid)

	if !path.IsAbs(m["path"]) {
		cwdLink := fmt.Sprintf("/proc/%d/cwd", pid)
		prefixPath, err := os.Readlink(cwdLink)
		if err != nil {
			return err
		}

		prefixPath = strings.TrimPrefix(prefixPath, rootPath)
		m["path"] = filepath.Join(rootPath, prefixPath, m["path"])
	} else {
		m["path"] = filepath.Join(rootPath, m["path"])
	}

	idmapSet, err := d.CurrentIdmap()
	if err != nil {
		return err
	}

	dev, err := device.UnixDeviceCreate(d.state, idmapSet, d.DevicesPath(), prefix, m, true)
	if err != nil {
		return fmt.Errorf("Failed to setup device: %s", err)
	}

	devPath := dev.HostPath
	tgtPath := dev.RelativePath

	// Bind-mount it into the container
	defer func() { _ = os.Remove(devPath) }()
	return d.insertMountGo(devPath, tgtPath, "none", unix.MS_BIND, pid, idmap.IdmapStorageNone)
}

func (d *lxc) removeUnixDevices() error {
	// Check that we indeed have devices to remove
	if !util.PathExists(d.DevicesPath()) {
		return nil
	}

	// Load the directory listing
	dents, err := os.ReadDir(d.DevicesPath())
	if err != nil {
		return err
	}

	// Go through all the unix devices
	for _, f := range dents {
		// Skip non-Unix devices
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

// FillNetworkDevice takes a nic or infiniband device type and enriches it with automatically
// generated name and hwaddr properties if these are missing from the device.
func (d *lxc) FillNetworkDevice(name string, m deviceConfig.Device) (deviceConfig.Device, error) {
	var err error
	newDevice := m.Clone()

	// Function to try and guess an available name
	nextInterfaceName := func() (string, error) {
		devNames := []string{}

		// Include all static interface names
		for _, dev := range d.expandedDevices.Sorted() {
			if dev.Config["name"] != "" && !util.ValueInSlice(dev.Config["name"], devNames) {
				devNames = append(devNames, dev.Config["name"])
			}
		}

		// Include all currently allocated interface names
		for k, v := range d.expandedConfig {
			if !strings.HasPrefix(k, internalInstance.ConfigVolatilePrefix) {
				continue
			}

			fields := strings.SplitN(k, ".", 3)
			if len(fields) != 3 {
				continue
			}

			if fields[2] != "name" || util.ValueInSlice(v, devNames) {
				continue
			}

			devNames = append(devNames, v)
		}

		// Attempt to include all existing interfaces
		cname := project.Instance(d.Project().Name, d.Name())
		cc, err := liblxc.NewContainer(cname, d.state.OS.LxcPath)
		if err == nil {
			defer func() { _ = cc.Release() }()

			interfaces, err := cc.Interfaces()
			if err == nil {
				for _, name := range interfaces {
					if util.ValueInSlice(name, devNames) {
						continue
					}

					devNames = append(devNames, name)
				}
			}
		}

		i := 0
		name := ""
		for {
			if m["type"] == "infiniband" {
				name = fmt.Sprintf("ib%d", i)
			} else {
				name = fmt.Sprintf("eth%d", i)
			}

			// Find a free device name
			if !util.ValueInSlice(name, devNames) {
				return name, nil
			}

			i++
		}
	}

	nicType, err := nictype.NICType(d.state, d.Project().Name, m)
	if err != nil {
		return nil, err
	}

	// Fill in the MAC address.
	if !util.ValueInSlice(nicType, []string{"physical", "ipvlan", "sriov"}) && m["hwaddr"] == "" {
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

	// Fill in the interface name.
	if m["name"] == "" {
		configKey := fmt.Sprintf("volatile.%s.name", name)
		volatileName := d.localConfig[configKey]
		if volatileName == "" {
			// Generate a new interface name.
			volatileName, err = nextInterfaceName()
			if err != nil || volatileName == "" {
				return nil, fmt.Errorf("Failed generating %q: %w", configKey, err)
			}

			// Update the database and update volatileName with stored value.
			volatileName, err = d.insertConfigkey(configKey, volatileName)
			if err != nil {
				return nil, fmt.Errorf("Failed storing generated config key %q: %w", configKey, err)
			}

			// Set stored value into current instance config.
			d.localConfig[configKey] = volatileName
			d.expandedConfig[configKey] = volatileName
		}

		if volatileName == "" {
			return nil, fmt.Errorf("Failed getting %q", configKey)
		}

		newDevice["name"] = volatileName
	}

	return newDevice, nil
}

func (d *lxc) removeDiskDevices() error {
	// Check that we indeed have devices to remove
	if !util.PathExists(d.DevicesPath()) {
		return nil
	}

	// Load the directory listing
	dents, err := os.ReadDir(d.DevicesPath())
	if err != nil {
		return err
	}

	// Go through all the unix devices
	for _, f := range dents {
		// Skip non-disk devices
		if !strings.HasPrefix(f.Name(), "disk.") {
			continue
		}

		// Always try to unmount the host side
		_ = unix.Unmount(filepath.Join(d.DevicesPath(), f.Name()), unix.MNT_DETACH)

		// Remove the entry
		diskPath := filepath.Join(d.DevicesPath(), f.Name())
		err := os.Remove(diskPath)
		if err != nil {
			d.logger.Error("Failed to remove disk device path", logger.Ctx{"err": err, "path": diskPath})
		}
	}

	return nil
}

// IsFrozen returns if instance is frozen.
func (d *lxc) IsFrozen() bool {
	return d.statusCode() == api.Frozen
}

// IsNesting returns if instance is nested.
func (d *lxc) IsNesting() bool {
	return util.IsTrue(d.expandedConfig["security.nesting"])
}

func (d *lxc) isCurrentlyPrivileged() bool {
	if !d.IsRunning() {
		return d.IsPrivileged()
	}

	idmap, err := d.CurrentIdmap()
	if err != nil {
		return d.IsPrivileged()
	}

	return idmap == nil
}

// IsPrivileged returns if instance is privileged.
func (d *lxc) IsPrivileged() bool {
	return util.IsTrue(d.expandedConfig["security.privileged"])
}

// IsRunning returns if instance is running.
func (d *lxc) IsRunning() bool {
	return d.isRunningStatusCode(d.statusCode())
}

// CanMigrate returns whether the instance can be migrated.
func (d *lxc) CanMigrate() string {
	return d.canMigrate(d)
}

// LockExclusive attempts to get exlusive access to the instance's root volume.
func (d *lxc) LockExclusive() (*operationlock.InstanceOperation, error) {
	if d.IsRunning() {
		return nil, fmt.Errorf("Instance is running")
	}

	// Prevent concurrent operations the instance.
	op, err := operationlock.Create(d.Project().Name, d.Name(), operationlock.ActionCreate, false, false)
	if err != nil {
		return nil, err
	}

	// Stop forkfile as otherwise it will hold the root volume open preventing unmount.
	d.stopForkfile(false)

	return op, err
}

// InitPID returns PID of init process.
func (d *lxc) InitPID() int {
	// Load the go-lxc struct
	cc, err := d.initLXC(false)
	if err != nil {
		return -1
	}

	return cc.InitPid()
}

// InitPidFd returns pidfd of init process.
func (d *lxc) InitPidFd() (*os.File, error) {
	// Load the go-lxc struct
	cc, err := d.initLXC(false)
	if err != nil {
		return nil, err
	}

	return cc.InitPidFd()
}

// DevptsFd returns dirfd of devpts mount.
func (d *lxc) DevptsFd() (*os.File, error) {
	// Load the go-lxc struct
	cc, err := d.initLXC(false)
	if err != nil {
		return nil, err
	}

	defer d.release()

	if !liblxc.HasAPIExtension("devpts_fd") {
		return nil, fmt.Errorf("Missing devpts_fd extension")
	}

	return cc.DevptsFd()
}

// CurrentIdmap returns current IDMAP.
func (d *lxc) CurrentIdmap() (*idmap.Set, error) {
	jsonIdmap, ok := d.LocalConfig()["volatile.idmap.current"]
	if !ok {
		return d.DiskIdmap()
	}

	return idmap.NewSetFromJSON(jsonIdmap)
}

// DiskIdmap returns DISK IDMAP.
func (d *lxc) DiskIdmap() (*idmap.Set, error) {
	jsonIdmap, ok := d.LocalConfig()["volatile.last_state.idmap"]
	if !ok {
		return nil, nil
	}

	return idmap.NewSetFromJSON(jsonIdmap)
}

// NextIdmap returns next IDMAP.
func (d *lxc) NextIdmap() (*idmap.Set, error) {
	jsonIdmap, ok := d.LocalConfig()["volatile.idmap.next"]
	if !ok {
		return d.CurrentIdmap()
	}

	return idmap.NewSetFromJSON(jsonIdmap)
}

// statusCode returns instance status code.
func (d *lxc) statusCode() api.StatusCode {
	// Shortcut to avoid spamming liblxc during ongoing operations.
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

	state, err := d.getLxcState()
	if err != nil {
		return api.Error
	}

	statusCode := lxcStatusCode(state)

	if statusCode == api.Running && util.IsTrue(d.LocalConfig()["volatile.last_state.ready"]) {
		return api.Ready
	}

	return statusCode
}

// State returns instance state.
func (d *lxc) State() string {
	return strings.ToUpper(d.statusCode().String())
}

// LogFilePath log file path.
func (d *lxc) LogFilePath() string {
	return filepath.Join(d.LogPath(), "lxc.log")
}

func (d *lxc) CGroup() (*cgroup.CGroup, error) {
	// Load the go-lxc struct
	cc, err := d.initLXC(false)
	if err != nil {
		return nil, err
	}

	return d.cgroup(cc, true)
}

func (d *lxc) cgroup(cc *liblxc.Container, running bool) (*cgroup.CGroup, error) {
	if cc == nil {
		return nil, fmt.Errorf("Container not initialized for cgroup")
	}

	rw := lxcCgroupReadWriter{}
	rw.cc = cc
	rw.running = running

	cg, err := cgroup.New(&rw)
	if err != nil {
		return nil, err
	}

	cg.UnifiedCapable = liblxc.HasAPIExtension("cgroup2")
	return cg, nil
}

type lxcCgroupReadWriter struct {
	cc      *liblxc.Container
	running bool
}

func (rw *lxcCgroupReadWriter) Get(version cgroup.Backend, controller string, key string) (string, error) {
	if !rw.running {
		lxcKey := fmt.Sprintf("lxc.cgroup.%s", key)

		if version == cgroup.V2 {
			lxcKey = fmt.Sprintf("lxc.cgroup2.%s", key)
		}

		return strings.Join(rw.cc.ConfigItem(lxcKey), "\n"), nil
	}

	return strings.Join(rw.cc.CgroupItem(key), "\n"), nil
}

func (rw *lxcCgroupReadWriter) Set(version cgroup.Backend, controller string, key string, value string) error {
	if !rw.running {
		if version == cgroup.V1 {
			return lxcSetConfigItem(rw.cc, fmt.Sprintf("lxc.cgroup.%s", key), value)
		}

		return lxcSetConfigItem(rw.cc, fmt.Sprintf("lxc.cgroup2.%s", key), value)
	}

	return rw.cc.SetCgroupItem(key, value)
}

// UpdateBackupFile writes the instance's backup.yaml file to storage.
func (d *lxc) UpdateBackupFile() error {
	pool, err := d.getStoragePool()
	if err != nil {
		return err
	}

	return pool.UpdateInstanceBackupFile(d, true, nil)
}

// Info returns "lxc" and the currently loaded version of LXC.
func (d *lxc) Info() instance.Info {
	return instance.Info{
		Name:    "lxc",
		Version: liblxc.Version(),
		Type:    instancetype.Container,
		Error:   nil,
	}
}

func (d *lxc) Metrics(hostInterfaces []net.Interface) (*metrics.MetricSet, error) {
	out := metrics.NewMetricSet(map[string]string{"project": d.project.Name, "name": d.name, "type": instancetype.Container.String()})

	if !d.IsRunning() {
		return nil, ErrInstanceIsStopped
	}

	cc, err := d.initLXC(false)
	if err != nil {
		return nil, err
	}

	// Load cgroup abstraction
	cg, err := d.cgroup(cc, true)
	if err != nil {
		return nil, err
	}

	// Get Memory limit.
	memoryLimit, err := cg.GetEffectiveMemoryLimit()
	if err != nil {
		d.logger.Warn("Failed getting effective memory limit", logger.Ctx{"err": err})
	}

	memoryCached := int64(0)

	// Get memory stats.
	memStats, err := cg.GetMemoryStats()
	if err != nil {
		d.logger.Warn("Failed to get memory stats", logger.Ctx{"err": err})
	} else {
		for k, v := range memStats {
			var metricType metrics.MetricType

			switch k {
			case "active_anon":
				metricType = metrics.MemoryActiveAnonBytes
			case "active_file":
				metricType = metrics.MemoryActiveFileBytes
			case "active":
				metricType = metrics.MemoryActiveBytes
			case "inactive_anon":
				metricType = metrics.MemoryInactiveAnonBytes
			case "inactive_file":
				metricType = metrics.MemoryInactiveFileBytes
			case "inactive":
				metricType = metrics.MemoryInactiveBytes
			case "unevictable":
				metricType = metrics.MemoryUnevictableBytes
			case "writeback":
				metricType = metrics.MemoryWritebackBytes
			case "dirty":
				metricType = metrics.MemoryDirtyBytes
			case "mapped":
				metricType = metrics.MemoryMappedBytes
			case "rss":
				metricType = metrics.MemoryRSSBytes
			case "shmem":
				metricType = metrics.MemoryShmemBytes
			case "cache":
				metricType = metrics.MemoryCachedBytes
				memoryCached = int64(v)
			}

			out.AddSamples(metricType, metrics.Sample{Value: float64(v)})
		}
	}

	// Get memory usage.
	memoryUsage, err := cg.GetMemoryUsage()
	if err != nil {
		d.logger.Warn("Failed to get memory usage", logger.Ctx{"err": err})
	}

	if memoryLimit > 0 {
		out.AddSamples(metrics.MemoryMemTotalBytes, metrics.Sample{Value: float64(memoryLimit)})
		out.AddSamples(metrics.MemoryMemAvailableBytes, metrics.Sample{Value: float64(memoryLimit - memoryUsage + memoryCached)})
		out.AddSamples(metrics.MemoryMemFreeBytes, metrics.Sample{Value: float64(memoryLimit - memoryUsage)})
	}

	// Get oom kills.
	oomKills, err := cg.GetOOMKills()
	if err != nil {
		d.logger.Warn("Failed to get oom kills", logger.Ctx{"err": err})
	}

	out.AddSamples(metrics.MemoryOOMKillsTotal, metrics.Sample{Value: float64(oomKills)})

	// Handle swap.
	if d.state.OS.CGInfo.Supports(cgroup.MemorySwapUsage, cg) {
		swapUsage, err := cg.GetMemorySwapUsage()
		if err != nil {
			d.logger.Warn("Failed to get swap usage", logger.Ctx{"err": err})
		} else {
			out.AddSamples(metrics.MemorySwapBytes, metrics.Sample{Value: float64(swapUsage)})
		}
	}

	// Get CPU stats
	usage, err := cg.GetCPUAcctUsageAll()
	if err != nil {
		d.logger.Warn("Failed to get CPU usage", logger.Ctx{"err": err})
	} else {
		for cpu, stats := range usage {
			cpuID := strconv.Itoa(int(cpu))

			out.AddSamples(metrics.CPUSecondsTotal, metrics.Sample{Value: float64(stats.System) / 1000000000, Labels: map[string]string{"mode": "system", "cpu": cpuID}})
			out.AddSamples(metrics.CPUSecondsTotal, metrics.Sample{Value: float64(stats.User) / 1000000000, Labels: map[string]string{"mode": "user", "cpu": cpuID}})
		}
	}

	// Get CPUs.
	CPUs, err := cg.GetEffectiveCPUs()
	if err != nil {
		d.logger.Warn("Failed to get CPUs", logger.Ctx{"err": err})
	} else {
		out.AddSamples(metrics.CPUs, metrics.Sample{Value: float64(CPUs)})
	}

	// Get disk stats
	diskStats, err := cg.GetIOStats()
	if err != nil {
		d.logger.Warn("Failed to get disk stats", logger.Ctx{"err": err})
	} else {
		for disk, stats := range diskStats {
			labels := map[string]string{"device": disk}

			out.AddSamples(metrics.DiskReadBytesTotal, metrics.Sample{Value: float64(stats.ReadBytes), Labels: labels})
			out.AddSamples(metrics.DiskReadsCompletedTotal, metrics.Sample{Value: float64(stats.ReadsCompleted), Labels: labels})
			out.AddSamples(metrics.DiskWrittenBytesTotal, metrics.Sample{Value: float64(stats.WrittenBytes), Labels: labels})
			out.AddSamples(metrics.DiskWritesCompletedTotal, metrics.Sample{Value: float64(stats.WritesCompleted), Labels: labels})
		}
	}

	// Get filesystem stats
	fsStats, err := d.getFSStats()
	if err != nil {
		d.logger.Warn("Failed to get fs stats", logger.Ctx{"err": err})
	} else {
		out.Merge(fsStats)
	}

	// Get network stats
	networkState := d.networkState(hostInterfaces)

	for name, state := range networkState {
		labels := map[string]string{"device": name}

		out.AddSamples(metrics.NetworkReceiveBytesTotal, metrics.Sample{Value: float64(state.Counters.BytesReceived), Labels: labels})
		out.AddSamples(metrics.NetworkReceivePacketsTotal, metrics.Sample{Value: float64(state.Counters.PacketsReceived), Labels: labels})
		out.AddSamples(metrics.NetworkTransmitBytesTotal, metrics.Sample{Value: float64(state.Counters.BytesSent), Labels: labels})
		out.AddSamples(metrics.NetworkTransmitPacketsTotal, metrics.Sample{Value: float64(state.Counters.PacketsSent), Labels: labels})
		out.AddSamples(metrics.NetworkReceiveErrsTotal, metrics.Sample{Value: float64(state.Counters.ErrorsReceived), Labels: labels})
		out.AddSamples(metrics.NetworkTransmitErrsTotal, metrics.Sample{Value: float64(state.Counters.ErrorsSent), Labels: labels})
		out.AddSamples(metrics.NetworkReceiveDropTotal, metrics.Sample{Value: float64(state.Counters.PacketsDroppedInbound), Labels: labels})
		out.AddSamples(metrics.NetworkTransmitDropTotal, metrics.Sample{Value: float64(state.Counters.PacketsDroppedOutbound), Labels: labels})
	}

	// Get number of processes
	pids, err := d.processesState(d.InitPID())
	if err != nil {
		d.logger.Warn("Failed to get total number of processes", logger.Ctx{"err": err})
	} else {
		out.AddSamples(metrics.ProcsTotal, metrics.Sample{Value: float64(pids)})
	}

	return out, nil
}

func (d *lxc) getFSStats() (*metrics.MetricSet, error) {
	type mountInfo struct {
		Mountpoint string
		FSType     string
	}

	out := metrics.NewMetricSet(nil)

	mounts, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return nil, fmt.Errorf("Failed to read /proc/mounts: %w", err)
	}

	mountMap := make(map[string]mountInfo)
	scanner := bufio.NewScanner(bytes.NewReader(mounts))

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())

		mountMap[fields[0]] = mountInfo{Mountpoint: fields[1], FSType: fields[2]}
	}

	// Get disk devices
	for _, dev := range d.expandedDevices {
		if dev["type"] != "disk" || dev["path"] == "" {
			continue
		}

		var statfs *unix.Statfs_t
		labels := make(map[string]string)
		realDev := ""

		if dev["pool"] != "" {
			// Expected volume name.
			var volName string
			var volType storageDrivers.VolumeType
			if dev["source"] != "" {
				volName = project.StorageVolume(d.project.Name, dev["source"])
				volType = storageDrivers.VolumeTypeCustom
			} else {
				volName = project.Instance(d.project.Name, d.name)
				volType = storageDrivers.VolumeTypeContainer
			}

			// Check that we have a mountpoint.
			mountpoint := storageDrivers.GetVolumeMountPath(dev["pool"], volType, volName)
			if mountpoint == "" || !util.PathExists(mountpoint) {
				continue
			}

			// Grab the filesystem information.
			statfs, err = linux.StatVFS(mountpoint)
			if err != nil {
				return nil, fmt.Errorf("Failed to stat %s: %w", mountpoint, err)
			}

			isMounted := false

			// Check if mountPath is in mountMap
			for mountDev, mountInfo := range mountMap {
				if mountInfo.Mountpoint != mountpoint {
					continue
				}

				isMounted = true
				realDev = mountDev
				break
			}

			if !isMounted {
				realDev = dev["source"]
			}
		} else {
			source := dev["source"]

			statfs, err = linux.StatVFS(source)
			if err != nil {
				return nil, fmt.Errorf("Failed to stat %s: %w", dev["source"], err)
			}

			isMounted := false

			// Check if mountPath is in mountMap
			for mountDev, mountInfo := range mountMap {
				if mountInfo.Mountpoint != source {
					continue
				}

				isMounted = true
				stat := unix.Stat_t{}

				// Check if dev has a backing file
				err = unix.Stat(source, &stat)
				if err != nil {
					return nil, fmt.Errorf("Failed to stat %s: %w", dev["source"], err)
				}

				backingFilePath := fmt.Sprintf("/sys/dev/block/%d:%d/loop/backing_file", unix.Major(uint64(stat.Dev)), unix.Minor(uint64(stat.Dev)))

				if util.PathExists(backingFilePath) {
					// Read backing file
					backingFile, err := os.ReadFile(backingFilePath)
					if err != nil {
						return nil, fmt.Errorf("Failed to read %s: %w", backingFilePath, err)
					}

					realDev = string(backingFile)
				} else {
					// Use dev as device
					realDev = mountDev
				}

				break
			}

			if !isMounted {
				realDev = dev["source"]
			}
		}

		// Add labels
		labels["device"] = realDev
		labels["mountpoint"] = dev["path"]

		fsType, err := linux.FSTypeToName(int32(statfs.Type))
		if err == nil {
			labels["fstype"] = fsType
		}

		// Add sample
		statfsBsize := uint64(statfs.Bsize)
		out.AddSamples(metrics.FilesystemSizeBytes, metrics.Sample{Value: float64(statfs.Blocks * statfsBsize), Labels: labels})
		out.AddSamples(metrics.FilesystemAvailBytes, metrics.Sample{Value: float64(statfs.Bavail * statfsBsize), Labels: labels})
		out.AddSamples(metrics.FilesystemFreeBytes, metrics.Sample{Value: float64(statfs.Bfree * statfsBsize), Labels: labels})
	}

	return out, nil
}

func (d *lxc) loadRawLXCConfig(cc *liblxc.Container) error {
	// Load the LXC raw config.
	lxcConfig, ok := d.expandedConfig["raw.lxc"]
	if !ok {
		return nil
	}

	// Write to temp config file.
	f, err := os.CreateTemp("", "incus_config_")
	if err != nil {
		return err
	}

	err = internalIO.WriteAll(f, []byte(lxcConfig))
	if err != nil {
		return err
	}

	err = f.Close()
	if err != nil {
		return err
	}

	// Load the config.
	err = cc.LoadConfigFile(f.Name())
	if err != nil {
		return fmt.Errorf("Failed to load config file %q: %w", f.Name(), err)
	}

	_ = os.Remove(f.Name())

	return nil
}

// forfileRunningLockName returns the forkfile-running_ID lock name.
func (d *common) forkfileRunningLockName() string {
	return fmt.Sprintf("forkfile-running_%d", d.id)
}
