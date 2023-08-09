package shared

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lxc/incus/incus/instance/instancetype"
	"github.com/lxc/incus/shared/units"
	"github.com/lxc/incus/shared/validate"
)

// InstanceAction indicates the type of action being performed.
type InstanceAction string

// InstanceAction types.
const (
	Stop     InstanceAction = "stop"
	Start    InstanceAction = "start"
	Restart  InstanceAction = "restart"
	Freeze   InstanceAction = "freeze"
	Unfreeze InstanceAction = "unfreeze"
)

// ConfigVolatilePrefix indicates the prefix used for volatile config keys.
const ConfigVolatilePrefix = "volatile."

// IsRootDiskDevice returns true if the given device representation is configured as root disk for
// an instance. It typically get passed a specific entry of api.Instance.Devices.
func IsRootDiskDevice(device map[string]string) bool {
	// Root disk devices also need a non-empty "pool" property, but we can't check that here
	// because this function is used with clients talking to older servers where there was no
	// concept of a storage pool, and also it is used for migrating from old to new servers.
	// The validation of the non-empty "pool" property is done inside the disk device itself.
	if device["type"] == "disk" && device["path"] == "/" && device["source"] == "" {
		return true
	}

	return false
}

// ErrNoRootDisk means there is no root disk device found.
var ErrNoRootDisk = fmt.Errorf("No root device could be found")

// GetRootDiskDevice returns the instance device that is configured as root disk.
// Returns the device name and device config map.
func GetRootDiskDevice(devices map[string]map[string]string) (string, map[string]string, error) {
	var devName string
	var dev map[string]string

	for n, d := range devices {
		if IsRootDiskDevice(d) {
			if devName != "" {
				return "", nil, fmt.Errorf("More than one root device found")
			}

			devName = n
			dev = d
		}
	}

	if devName != "" {
		return devName, dev, nil
	}

	return "", nil, ErrNoRootDisk
}

// HugePageSizeKeys is a list of known hugepage size configuration keys.
var HugePageSizeKeys = [...]string{"limits.hugepages.64KB", "limits.hugepages.1MB", "limits.hugepages.2MB", "limits.hugepages.1GB"}

// HugePageSizeSuffix contains the list of known hugepage size suffixes.
var HugePageSizeSuffix = [...]string{"64KB", "1MB", "2MB", "1GB"}

// InstanceConfigKeysAny is a map of config key to validator. (keys applying to containers AND virtual machines).
var InstanceConfigKeysAny = map[string]func(value string) error{
	"boot.autostart":             validate.Optional(validate.IsBool),
	"boot.autostart.delay":       validate.Optional(validate.IsInt64),
	"boot.autostart.priority":    validate.Optional(validate.IsInt64),
	"boot.stop.priority":         validate.Optional(validate.IsInt64),
	"boot.host_shutdown_timeout": validate.Optional(validate.IsInt64),

	"cloud-init.network-config": validate.Optional(validate.IsYAML),
	"cloud-init.user-data":      validate.Optional(validate.IsCloudInitUserData),
	"cloud-init.vendor-data":    validate.Optional(validate.IsCloudInitUserData),

	"cluster.evacuate": validate.Optional(validate.IsOneOf("auto", "migrate", "live-migrate", "stop")),

	"limits.cpu":           validate.Optional(validate.IsValidCPUSet),
	"limits.cpu.nodes":     validate.Optional(validate.IsValidCPUSet),
	"limits.disk.priority": validate.Optional(validate.IsPriority),
	"limits.memory": func(value string) error {
		if value == "" {
			return nil
		}

		if strings.HasSuffix(value, "%") {
			num, err := strconv.ParseInt(strings.TrimSuffix(value, "%"), 10, 64)
			if err != nil {
				return err
			}

			if num == 0 {
				return errors.New("Memory limit can't be 0%")
			}

			return nil
		}

		num, err := units.ParseByteSizeString(value)
		if err != nil {
			return err
		}

		if num == 0 {
			return fmt.Errorf("Memory limit can't be 0")
		}

		return nil
	},
	"limits.network.priority": validate.Optional(validate.IsPriority),

	// Caller is responsible for full validation of any raw.* value.
	"raw.apparmor": validate.IsAny,
	"raw.idmap":    validate.IsAny,

	"security.guestapi":          validate.Optional(validate.IsBool),
	"security.protection.delete": validate.Optional(validate.IsBool),

	"snapshots.schedule":         validate.Optional(validate.IsCron([]string{"@hourly", "@daily", "@midnight", "@weekly", "@monthly", "@annually", "@yearly", "@startup", "@never"})),
	"snapshots.schedule.stopped": validate.Optional(validate.IsBool),
	"snapshots.pattern":          validate.IsAny,
	"snapshots.expiry": func(value string) error {
		// Validate expression
		_, err := GetExpiry(time.Time{}, value)
		return err
	},

	// Volatile keys.
	"volatile.apply_template":         validate.IsAny,
	"volatile.base_image":             validate.IsAny,
	"volatile.cloud-init.instance-id": validate.Optional(validate.IsUUID),
	"volatile.evacuate.origin":        validate.IsAny,
	"volatile.last_state.power":       validate.IsAny,
	"volatile.last_state.ready":       validate.IsBool,
	"volatile.apply_quota":            validate.IsAny,
	"volatile.uuid":                   validate.Optional(validate.IsUUID),
	"volatile.uuid.generation":        validate.Optional(validate.IsUUID),
}

// InstanceConfigKeysContainer is a map of config key to validator. (keys applying to containers only).
var InstanceConfigKeysContainer = map[string]func(value string) error{
	"limits.cpu.allowance": func(value string) error {
		if value == "" {
			return nil
		}

		if strings.HasSuffix(value, "%") {
			// Percentage based allocation
			_, err := strconv.Atoi(strings.TrimSuffix(value, "%"))
			if err != nil {
				return err
			}

			return nil
		}

		// Time based allocation
		fields := strings.SplitN(value, "/", 2)
		if len(fields) != 2 {
			return fmt.Errorf("Invalid allowance: %s", value)
		}

		_, err := strconv.Atoi(strings.TrimSuffix(fields[0], "ms"))
		if err != nil {
			return err
		}

		_, err = strconv.Atoi(strings.TrimSuffix(fields[1], "ms"))
		if err != nil {
			return err
		}

		return nil
	},
	"limits.cpu.priority":   validate.Optional(validate.IsPriority),
	"limits.hugepages.64KB": validate.Optional(validate.IsSize),
	"limits.hugepages.1MB":  validate.Optional(validate.IsSize),
	"limits.hugepages.2MB":  validate.Optional(validate.IsSize),
	"limits.hugepages.1GB":  validate.Optional(validate.IsSize),
	"limits.memory.enforce": validate.Optional(validate.IsOneOf("soft", "hard")),

	"limits.memory.swap":          validate.Optional(validate.IsBool),
	"limits.memory.swap.priority": validate.Optional(validate.IsPriority),
	"limits.processes":            validate.Optional(validate.IsInt64),

	"linux.kernel_modules": validate.IsAny,

	"migration.incremental.memory":            validate.Optional(validate.IsBool),
	"migration.incremental.memory.iterations": validate.Optional(validate.IsUint32),
	"migration.incremental.memory.goal":       validate.Optional(validate.IsUint32),

	"nvidia.runtime":             validate.Optional(validate.IsBool),
	"nvidia.driver.capabilities": validate.IsAny,
	"nvidia.require.cuda":        validate.IsAny,
	"nvidia.require.driver":      validate.IsAny,

	// Caller is responsible for full validation of any raw.* value.
	"raw.lxc":     validate.IsAny,
	"raw.seccomp": validate.IsAny,

	"security.guestapi.images": validate.Optional(validate.IsBool),

	"security.idmap.base":     validate.Optional(validate.IsUint32),
	"security.idmap.isolated": validate.Optional(validate.IsBool),
	"security.idmap.size":     validate.Optional(validate.IsUint32),

	"security.nesting":          validate.Optional(validate.IsBool),
	"security.privileged":       validate.Optional(validate.IsBool),
	"security.protection.shift": validate.Optional(validate.IsBool),

	"security.syscalls.allow":                        validate.IsAny,
	"security.syscalls.blacklist_default":            validate.Optional(validate.IsBool),
	"security.syscalls.blacklist_compat":             validate.Optional(validate.IsBool),
	"security.syscalls.blacklist":                    validate.IsAny,
	"security.syscalls.deny_default":                 validate.Optional(validate.IsBool),
	"security.syscalls.deny_compat":                  validate.Optional(validate.IsBool),
	"security.syscalls.deny":                         validate.IsAny,
	"security.syscalls.intercept.bpf":                validate.Optional(validate.IsBool),
	"security.syscalls.intercept.bpf.devices":        validate.Optional(validate.IsBool),
	"security.syscalls.intercept.mknod":              validate.Optional(validate.IsBool),
	"security.syscalls.intercept.mount":              validate.Optional(validate.IsBool),
	"security.syscalls.intercept.mount.allowed":      validate.IsAny,
	"security.syscalls.intercept.mount.fuse":         validate.IsAny,
	"security.syscalls.intercept.mount.shift":        validate.Optional(validate.IsBool),
	"security.syscalls.intercept.sched_setscheduler": validate.Optional(validate.IsBool),
	"security.syscalls.intercept.setxattr":           validate.Optional(validate.IsBool),
	"security.syscalls.intercept.sysinfo":            validate.Optional(validate.IsBool),
	"security.syscalls.whitelist":                    validate.IsAny,

	"volatile.last_state.idmap": validate.IsAny,
	"volatile.idmap.base":       validate.IsAny,
	"volatile.idmap.current":    validate.IsAny,
	"volatile.idmap.next":       validate.IsAny,
}

// InstanceConfigKeysVM is a map of config key to validator. (keys applying to VM only).
var InstanceConfigKeysVM = map[string]func(value string) error{
	"limits.memory.hugepages": validate.Optional(validate.IsBool),

	"migration.stateful": validate.Optional(validate.IsBool),

	// Caller is responsible for full validation of any raw.* value.
	"raw.qemu":      validate.IsAny,
	"raw.qemu.conf": validate.IsAny,

	"security.agent.metrics":    validate.Optional(validate.IsBool),
	"security.csm":              validate.Optional(validate.IsBool),
	"security.secureboot":       validate.Optional(validate.IsBool),
	"security.sev":              validate.Optional(validate.IsBool),
	"security.sev.policy.es":    validate.Optional(validate.IsBool),
	"security.sev.session.dh":   validate.Optional(validate.IsAny),
	"security.sev.session.data": validate.Optional(validate.IsAny),

	"agent.nic_config": validate.Optional(validate.IsBool),

	"volatile.apply_nvram": validate.Optional(validate.IsBool),
	"volatile.vsock_id":    validate.Optional(validate.IsInt64),
}

// ConfigKeyChecker returns a function that will check whether or not
// a provide value is valid for the associate config key.  Returns an
// error if the key is not known.  The checker function only performs
// syntactic checking of the value, semantic and usage checking must
// be done by the caller.  User defined keys are always considered to
// be valid, e.g. user.* and environment.* keys.
func ConfigKeyChecker(key string, instanceType instancetype.Type) (func(value string) error, error) {
	f, ok := InstanceConfigKeysAny[key]
	if ok {
		return f, nil
	}

	if instanceType == instancetype.Any || instanceType == instancetype.Container {
		f, ok := InstanceConfigKeysContainer[key]
		if ok {
			return f, nil
		}
	}

	if instanceType == instancetype.Any || instanceType == instancetype.VM {
		f, ok := InstanceConfigKeysVM[key]
		if ok {
			return f, nil
		}
	}

	if strings.HasPrefix(key, ConfigVolatilePrefix) {
		if strings.HasSuffix(key, ".hwaddr") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".name") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".host_name") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".mtu") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".created") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".id") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".vlan") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".spoofcheck") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".last_state.vf.parent") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".last_state.ip_addresses") {
			return validate.IsListOf(validate.IsNetworkAddress), nil
		}

		if strings.HasSuffix(key, ".apply_quota") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".ceph_rbd") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".driver") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".uuid") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".last_state.ready") {
			return validate.IsBool, nil
		}
	}

	if strings.HasPrefix(key, "environment.") {
		return validate.IsAny, nil
	}

	if strings.HasPrefix(key, "user.") {
		return validate.IsAny, nil
	}

	if strings.HasPrefix(key, "image.") {
		return validate.IsAny, nil
	}

	if strings.HasPrefix(key, "limits.kernel.") &&
		(len(key) > len("limits.kernel.")) {
		return validate.IsAny, nil
	}

	if (instanceType == instancetype.Any || instanceType == instancetype.Container) &&
		strings.HasPrefix(key, "linux.sysctl.") {
		return validate.IsAny, nil
	}

	return nil, fmt.Errorf("Unknown configuration key: %s", key)
}

// InstanceIncludeWhenCopying is used to decide whether to include a config item or not when copying an instance.
// The remoteCopy argument indicates if the copy is remote (i.e between LXD nodes) as this affects the keys kept.
func InstanceIncludeWhenCopying(configKey string, remoteCopy bool) bool {
	if configKey == "volatile.base_image" {
		return true // Include volatile.base_image always as it can help optimize copies.
	}

	if configKey == "volatile.last_state.idmap" && !remoteCopy {
		return true // Include volatile.last_state.idmap when doing local copy to avoid needless remapping.
	}

	if strings.HasPrefix(configKey, ConfigVolatilePrefix) {
		return false // Exclude all other volatile keys.
	}

	return true // Keep all other keys.
}
