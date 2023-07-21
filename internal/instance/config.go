package instance

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/units"
	"github.com/lxc/incus/shared/validate"
)

// IsUserConfig returns true if the config key is a user configuration.
func IsUserConfig(key string) bool {
	return strings.HasPrefix(key, "user.")
}

// ConfigVolatilePrefix indicates the prefix used for volatile config keys.
const ConfigVolatilePrefix = "volatile."

// HugePageSizeKeys is a list of known hugepage size configuration keys.
var HugePageSizeKeys = [...]string{"limits.hugepages.64KB", "limits.hugepages.1MB", "limits.hugepages.2MB", "limits.hugepages.1GB"}

// HugePageSizeSuffix contains the list of known hugepage size suffixes.
var HugePageSizeSuffix = [...]string{"64KB", "1MB", "2MB", "1GB"}

// InstanceConfigKeysAny is a map of config key to validator. (keys applying to containers AND virtual machines).
var InstanceConfigKeysAny = map[string]func(value string) error{
	// gendoc:generate(group=instance-boot, key=boot.autostart)
	//
	// ---
	//  type: bool
	//  liveupdate: no
	//  shortdesc: Controls whether to always start the instance when LXD starts (if not set, restore the last state)
	"boot.autostart": validate.Optional(validate.IsBool),
	// gendoc:generate(group=instance-boot, key=boot.autostart.delay)
	//
	// ---
	//  type: integer
	//  default: 0
	//  liveupdate: no
	//  shortdesc: Number of seconds to wait after the instance started before starting the next one
	"boot.autostart.delay": validate.Optional(validate.IsInt64),
	// gendoc:generate(group=instance-boot, key=boot.autostart.priority)
	//
	// ---
	//  type: integer
	//  default: 0
	//  liveupdate: no
	//  shortdesc: What order to start the instances in (starting with the highest value)
	"boot.autostart.priority": validate.Optional(validate.IsInt64),
	// gendoc:generate(group=instance-boot, key=boot.stop.priority)
	//
	// ---
	//  type: integer
	//  default: 0
	//  liveupdate: no
	//  shortdesc: What order to shut down the instances in (starting with the highest value)
	"boot.stop.priority": validate.Optional(validate.IsInt64),
	// gendoc:generate(group=instance-boot, key=boot.host_shutdown_timeout)
	//
	// ---
	//  type: integer
	//  default: 30
	//  liveupdate: yes
	//  shortdesc: Seconds to wait for the instance to shut down before it is force-stopped
	"boot.host_shutdown_timeout": validate.Optional(validate.IsInt64),

	// gendoc:generate(group=instance-cloud-init, key=cloud-init.network-config)
	//
	// ---
	//  type: string
	//  default: `DHCP on eth0`
	//  liveupdate: no
	//  condition: If supported by image
	//  shortdesc: Network configuration for `cloud-init` (content is used as seed value)
	"cloud-init.network-config": validate.Optional(validate.IsYAML),
	// gendoc:generate(group=instance-cloud-init, key=cloud-init.user-data)
	//
	// ---
	//  type: string
	//  default: `#cloud-config`
	//  liveupdate: no
	//  condition: If supported by image
	//  shortdesc: User data for `cloud-init` (content is used as seed value)
	"cloud-init.user-data": validate.Optional(validate.IsCloudInitUserData),
	// gendoc:generate(group=instance-cloud-init, key=cloud-init.vendor-data)
	//
	// ---
	//  type: string
	//  default: `#cloud-config`
	//  liveupdate: no
	//  condition: If supported by image
	//  shortdesc: Vendor data for `cloud-init` (content is used as seed value)
	"cloud-init.vendor-data": validate.Optional(validate.IsCloudInitUserData),

	// gendoc:generate(group=instance-cloud-init, key=user.network-config)
	//
	// ---
	//  type: string
	//  default: `DHCP on eth0`
	//  liveupdate: no
	//  condition: If supported by image
	//  shortdesc: Legacy version of `cloud-init.network-config`

	// gendoc:generate(group=instance-cloud-init, key=user.user-data)
	//
	// ---
	//  type: string
	//  default: `#cloud-config`
	//  liveupdate: no
	//  condition: If supported by image
	//  shortdesc: Legacy version of `cloud-init.user-data`

	// gendoc:generate(group=instance-cloud-init, key=user.vendor-data)
	//
	// ---
	//  type: string
	//  default: `#cloud-config`
	//  liveupdate: no
	//  condition: If supported by image
	//  shortdesc: Legacy version of `cloud-init.vendor-data`

	// gendoc:generate(group=instance-miscellaneous, key=cluster.evacuate)
	//
	// ---
	//  type: string
	//  default: `auto`
	//  liveupdate: no
	//  shortdesc: Controls what to do when evacuating the instance (`auto`, `migrate`, `live-migrate`, or `stop`)
	"cluster.evacuate": validate.Optional(validate.IsOneOf("auto", "migrate", "live-migrate", "stop")),

	// gendoc:generate(group=instance-resource-limits, key=limits.cpu)
	//
	// ---
	//  type: string
	//  default: for VMs: 1 CPU
	//  liveupdate: yes
	//  shortdesc: Number or range of CPUs to expose to the instance; see {ref}`instance-options-limits-cpu`
	"limits.cpu": validate.Optional(validate.IsValidCPUSet),
	// gendoc:generate(group=instance-resource-limits, key=limits.cpu.nodes)
	//
	// ---
	//  type: string
	//  liveupdate: yes
	//  shortdesc: Comma-separated list of NUMA node IDs or ranges to place the instance CPUs on; see {ref}`instance-options-limits-cpu-container`
	"limits.cpu.nodes": validate.Optional(validate.IsValidCPUSet),
	// gendoc:generate(group=instance-resource-limits, key=limits.disk.priority)
	//
	// ---
	//  type: integer
	//  default: `5` (medium)
	//  liveupdate: yes
	//  shortdesc: Controls how much priority to give to the instance’s I/O requests when under load (integer between 0 and 10)
	"limits.disk.priority": validate.Optional(validate.IsPriority),
	// gendoc:generate(group=instance-resource-limits, key=limits.memory)
	//
	// ---
	//  type: string
	//  default: for VMs: `1Gib`
	//  liveupdate: yes
	//  shortdesc: Percentage of the host's memory or fixed value in bytes (various suffixes supported, see {ref}`instances-limit-units`)
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
	// gendoc:generate(group=instance-resource-limits, key=limits.network.priority)
	//
	// ---
	//  type: integer
	//  default: `0` (minimum)
	//  liveupdate: yes
	//  shortdesc: Controls how much priority to give to the instance’s network requests when under load (integer between 0 and 10)
	"limits.network.priority": validate.Optional(validate.IsPriority),

	// Caller is responsible for full validation of any raw.* value.

	// gendoc:generate(group=instance-raw, key=raw.apparmor)
	//
	// ---
	//  type: blob
	//  liveupdate: yes
	//  shortdesc: AppArmor profile entries to be appended to the generated profile
	"raw.apparmor": validate.IsAny,
	// gendoc:generate(group=instance-raw, key=raw.idmap)
	//
	// ---
	//  type: blob
	//  liveupdate: no
	//  condition: unprivileged container
	//  shortdesc: Raw idmap configuration (for example, `both 1000 1000`)
	"raw.idmap": validate.IsAny,

	// gendoc:generate(group=instance-security, key=security.guestapi)
	//
	// ---
	//  type: bool
	//  default: `true`
	//  liveupdate: no
	//  shortdesc: Controls the presence of `/dev/incus` in the instance
	"security.guestapi": validate.Optional(validate.IsBool),
	// gendoc:generate(group=instance-security, key=security.protection.delete)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: yes
	//  shortdesc: Prevents the instance from being deleted
	"security.protection.delete": validate.Optional(validate.IsBool),

	// gendoc:generate(group=instance-snapshots, key=snapshots.schedule)
	//
	// ---
	//  type: string
	//  liveupdate: no
	//  shortdesc: Cron expression (`<minute> <hour> <dom> <month> <dow>`), a comma-separated list of schedule aliases (`@hourly`, `@daily`, `@midnight`, `@weekly`, `@monthly`, `@annually`, `@yearly`), or empty to disable automatic snapshots (the default)
	"snapshots.schedule": validate.Optional(validate.IsCron([]string{"@hourly", "@daily", "@midnight", "@weekly", "@monthly", "@annually", "@yearly", "@startup", "@never"})),
	// gendoc:generate(group=instance-snapshots, key=snapshots.schedule.stopped)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: no
	//  shortdesc: Controls whether to automatically snapshot stopped instances
	"snapshots.schedule.stopped": validate.Optional(validate.IsBool),
	// gendoc:generate(group=instance-snapshots, key=snapshots.pattern)
	//
	// ---
	//  type: string
	//  default: `snap%d`
	//  liveupdate: no
	//  shortdesc: Pongo2 template string that represents the snapshot name (used for scheduled snapshots and unnamed snapshots); see {ref}`instance-options-snapshots-names`
	"snapshots.pattern": validate.IsAny,
	// gendoc:generate(group=instance-snapshots, key=snapshots.expiry)
	//
	// ---
	//  type: string
	//  liveupdate: no
	//  shortdesc: Controls when snapshots are to be deleted (expects an expression like `1M 2H 3d 4w 5m 6y`)
	"snapshots.expiry": func(value string) error {
		// Validate expression
		_, err := GetExpiry(time.Time{}, value)
		return err
	},

	// Volatile keys.

	// gendoc:generate(group=instance-volatile, key=volatile.apply_template)
	//
	// ---
	//  type: string
	//  shortdesc: The name of a template hook that should be triggered upon next startup
	"volatile.apply_template": validate.IsAny,
	// gendoc:generate(group=instance-volatile, key=volatile.base_image)
	//
	// ---
	//  type: string
	//  shortdesc: The hash of the image the instance was created from (if any)
	"volatile.base_image": validate.IsAny,
	// gendoc:generate(group=instance-volatile, key=volatile.cloud_init.instance-id)
	//
	// ---
	//  type: string
	//  shortdesc: The `instance-id` (UUID) exposed to `cloud-init`
	"volatile.cloud-init.instance-id": validate.Optional(validate.IsUUID),
	// gendoc:generate(group=instance-volatile, key=volatile.evacuate.origin)
	//
	// ---
	//  type: string
	//  shortdesc: The origin (cluster member) of the evacuated instance
	"volatile.evacuate.origin": validate.IsAny,
	// gendoc:generate(group=instance-volatile, key=volatile.last_state.power)
	//
	// ---
	//  type: string
	//  shortdesc: Instance state as of last host shutdown
	"volatile.last_state.power": validate.IsAny,
	"volatile.last_state.ready": validate.IsBool,
	"volatile.apply_quota":      validate.IsAny,
	// gendoc:generate(group=instance-volatile, key=volatile.uuid)
	//
	// ---
	//  type: string
	//  shortdesc: Instance UUID (globally unique across all servers and projects)
	"volatile.uuid": validate.Optional(validate.IsUUID),
	// gendoc:generate(group=instance-volatile, key=volatile.uuid.generation)
	//
	// ---
	//  type: string
	//  shortdesc: Instance generation UUID that will change whenever the instance’s place in time moves backwards (globally unique across all servers and projects)
	"volatile.uuid.generation": validate.Optional(validate.IsUUID),
}

// InstanceConfigKeysContainer is a map of config key to validator. (keys applying to containers only).
var InstanceConfigKeysContainer = map[string]func(value string) error{
	// gendoc:generate(group=instance-resource-limits, key=limits.cpu.allowance)
	//
	// ---
	//  type: string
	//  default: 100%
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Controls how much of the CPU can be used: either a percentage (`50%`) for a soft limit or a chunk of time (`25ms/100ms`) for a hard limit; see {ref}`instance-options-limits-cpu-container`
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
	// gendoc:generate(group=instance-resource-limits, key=limits.cpu.priority)
	//
	// ---
	//  type: integer
	//  default: `10` (maximum)
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: CPU scheduling priority compared to other instances sharing the same CPUs when overcommitting resources (integer between 0 and 10); see {ref}`instance-options-limits-cpu-container`
	"limits.cpu.priority": validate.Optional(validate.IsPriority),
	// gendoc:generate(group=instance-resource-limits, key=limits.hugepages.64KB)
	//
	// ---
	//  type: string
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Fixed value in bytes (various suffixes supported, see {ref}`instances-limit-units`) to limit number of 64 KB huge pages; see {ref}`instance-options-limits-hugepages`
	"limits.hugepages.64KB": validate.Optional(validate.IsSize),
	// gendoc:generate(group=instance-resource-limits, key=limits.hugepages.1MB)
	//
	// ---
	//  type: string
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Fixed value in bytes (various suffixes supported, see {ref}`instances-limit-units`) to limit number of 1 MB huge pages; see {ref}`instance-options-limits-hugepages`
	"limits.hugepages.1MB": validate.Optional(validate.IsSize),
	// gendoc:generate(group=instance-resource-limits, key=limits.hugepages.2MB)
	//
	// ---
	//  type: string
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Fixed value in bytes (various suffixes supported, see {ref}`instances-limit-units`) to limit number of 2 MB huge pages; see {ref}`instance-options-limits-hugepages`
	"limits.hugepages.2MB": validate.Optional(validate.IsSize),
	// gendoc:generate(group=instance-resource-limits, key=limits.hugepages.1GB)
	//
	// ---
	//  type: string
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Fixed value in bytes (various suffixes supported, see {ref}`instances-limit-units`) to limit number of 1 GB huge pages; see {ref}`instance-options-limits-hugepages`
	"limits.hugepages.1GB": validate.Optional(validate.IsSize),
	// gendoc:generate(group=instance-resource-limits, key=limits.memory.enforce)
	//
	// ---
	//  type: string
	//  default: `hard`
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: If `hard`, the instance cannot exceed its memory limit; if `soft`, the instance can exceed its memory limit when extra host memory is available
	"limits.memory.enforce": validate.Optional(validate.IsOneOf("soft", "hard")),

	// gendoc:generate(group=instance-resource-limits, key=limits.memory.swap)
	//
	// ---
	//  type: bool
	//  default: `true`
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Controls whether to encourage/discourage swapping less used pages for this instance
	"limits.memory.swap": validate.Optional(validate.IsBool),
	// gendoc:generate(group=instance-resource-limits, key=limits.memory.swap.priority)
	//
	// ---
	//  type: integer
	//  default: `10` (maximum)
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Prevents the instance from being swapped to disk (integer between 0 and 10; the higher the value, the less likely the instance is to be swapped to disk)
	"limits.memory.swap.priority": validate.Optional(validate.IsPriority),
	// gendoc:generate(group=instance-resource-limits, key=limits.processes)
	//
	// ---
	//  type: integer
	//  default: -(max)
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Maximum number of processes that can run in the instance
	"limits.processes": validate.Optional(validate.IsInt64),

	// gendoc:generate(group=instance-miscellaneous, key=linux.kernel_modules)
	//
	// ---
	//  type: string
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Comma-separated list of kernel modules to load before starting the instance
	"linux.kernel_modules": validate.IsAny,

	// gendoc:generate(group=instance-migration, key=migration.incremental.memory)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Controls whether to use incremental memory transfer of the instance’s memory to reduce downtime
	"migration.incremental.memory": validate.Optional(validate.IsBool),
	// gendoc:generate(group=instance-migration, key=migration.incremental.memory.iterations)
	//
	// ---
	//  type: integer
	//  default: `10`
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Maximum number of transfer operations to go through before stopping the instance
	"migration.incremental.memory.iterations": validate.Optional(validate.IsUint32),
	// gendoc:generate(group=instance-migration, key=migration.incremental.memory.goal)
	//
	// ---
	//  type: integer
	//  default: `70`
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Percentage of memory to have in sync before stopping the instance
	"migration.incremental.memory.goal": validate.Optional(validate.IsUint32),

	// gendoc:generate(group=instance-nvidia, key=nvidia.runtime)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Controls whether to pass the host NVIDIA and CUDA runtime libraries into the instance
	"nvidia.runtime": validate.Optional(validate.IsBool),
	// gendoc:generate(group=instance-nvidia, key=nvidia.driver.capabilities)
	//
	// ---
	//  type: string
	//  default: `compute,utility`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: What driver capabilities the instance needs (sets `libnvidia-container NVIDIA_DRIVER_CAPABILITIES`)
	"nvidia.driver.capabilities": validate.IsAny,
	// gendoc:generate(group=instance-nvidia, key=nvidia.require.cuda)
	//
	// ---
	//  type: string
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Version expression for the required CUDA version (sets `libnvidia-container NVIDIA_REQUIRE_CUDA`)
	"nvidia.require.cuda": validate.IsAny,
	// gendoc:generate(group=instance-nvidia, key=nvidia.require.driver)
	//
	// ---
	//  type: string
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Version expression for the required driver version (sets `libnvidia-container NVIDIA_REQUIRE_DRIVER`)
	"nvidia.require.driver": validate.IsAny,

	// Caller is responsible for full validation of any raw.* value.

	// gendoc:generate(group=instance-raw, key=raw.lxc)
	//
	// ---
	//  type: blob
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Raw LXC configuration to be appended to the generated one
	"raw.lxc": validate.IsAny,
	// gendoc:generate(group=instance-raw, key=raw.seccomp)
	//
	// ---
	//  type: blob
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Raw Seccomp configuration
	"raw.seccomp": validate.IsAny,

	// gendoc:generate(group=instance-security, key=security.guestapi.images)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Controls the availability of the `/1.0/images` API over `guestapi`
	"security.guestapi.images": validate.Optional(validate.IsBool),
	// gendoc:generate(group=instance-security, key=security.idmap.base)
	//
	// ---
	//  type: integer
	//  liveupdate: no
	//  condition: unprivileged container
	//  shortdesc: The base host ID to use for the allocation (overrides auto-detection)
	"security.idmap.base": validate.Optional(validate.IsUint32),
	// gendoc:generate(group=instance-security, key=security.idmap.isolated)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: no
	//  condition: unprivileged container
	//  shortdesc: Controls whether to use an idmap for this instance that is unique among instances with isolated set
	"security.idmap.isolated": validate.Optional(validate.IsBool),
	// gendoc:generate(group=instance-security, key=security.idmap.size)
	//
	// ---
	//  type: integer
	//  liveupdate: no
	//  condition: unprivileged container
	//  shortdesc: The size of the idmap to use
	"security.idmap.size": validate.Optional(validate.IsUint32),

	// gendoc:generate(group=instance-security, key=security.nesting)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Controls whether to support running Incus (nested) inside the instance
	"security.nesting": validate.Optional(validate.IsBool),
	// gendoc:generate(group=instance-security, key=security.privileged)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Controls whether to run the instance in privileged mode
	"security.privileged": validate.Optional(validate.IsBool),
	// gendoc:generate(group=instance-security, key=security.protection.shift)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Prevents the instance’s file system from being UID/GID shifted on startup
	"security.protection.shift": validate.Optional(validate.IsBool),

	// gendoc:generate(group=instance-security, key=security.syscalls.allow)
	//
	// ---
	//  type: string
	//  liveupdate: no
	//  condition: container
	//  shortdesc: A `\n`-separated list of syscalls to allow (mutually exclusive with `security.syscalls.deny*`)
	"security.syscalls.allow": validate.IsAny,
	// gendoc:generate(group=instance-security, key=security.syscalls.blacklist_default)
	//
	// ---
	//  type: string
	//  liveupdate: no
	//  condition: container
	//  shortdesc: A `\n`-separated list of syscalls to allow (mutually exclusive with `security.syscalls.deny*`)
	"security.syscalls.blacklist_default": validate.Optional(validate.IsBool),
	"security.syscalls.blacklist_compat":  validate.Optional(validate.IsBool),
	"security.syscalls.blacklist":         validate.IsAny,
	// gendoc:generate(group=instance-security, key=security.syscalls.deny_default)
	//
	// ---
	//  type: bool
	//  default: `true`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Controls whether to enable the default syscall deny
	"security.syscalls.deny_default": validate.Optional(validate.IsBool),
	// gendoc:generate(group=instance-security, key=security.syscalls.deny_compat)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: On `x86_64`, controls whether to block `compat_*` syscalls (no-op on other architectures)
	"security.syscalls.deny_compat": validate.Optional(validate.IsBool),
	// gendoc:generate(group=instance-security, key=security.syscalls.deny)
	//
	// ---
	//  type: string
	//  liveupdate: no
	//  condition: container
	//  shortdesc: A `\n`-separated list of syscalls to deny
	"security.syscalls.deny": validate.IsAny,
	// gendoc:generate(group=instance-security, key=security.syscalls.intercept.bpf)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Controls whether to handle the bpf system call
	"security.syscalls.intercept.bpf": validate.Optional(validate.IsBool),
	// gendoc:generate(group=instance-security, key=security.syscalls.intercept.bpf.devices)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Controls whether to allow bpf programs for the devices cgroup in the unified hierarchy to be loaded
	"security.syscalls.intercept.bpf.devices": validate.Optional(validate.IsBool),
	// gendoc:generate(group=instance-security, key=security.syscalls.intercept.mknod)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Controls whether to handle the `mknod` and `mknodat` system calls (allows creation of a limited subset of char/block devices)
	"security.syscalls.intercept.mknod": validate.Optional(validate.IsBool),
	// gendoc:generate(group=instance-security, key=security.syscalls.intercept.mount)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Controls whether to handle the `mount` system call
	"security.syscalls.intercept.mount": validate.Optional(validate.IsBool),
	// gendoc:generate(group=instance-security, key=security.syscalls.intercept.mount.allowed)
	//
	// ---
	//  type: string
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: A comma-separated list of file systems that are safe to mount for processes inside the instance
	"security.syscalls.intercept.mount.allowed": validate.IsAny,
	// gendoc:generate(group=instance-security, key=security.syscalls.intercept.mount.fuse)
	//
	// ---
	//  type: string
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Mounts of a given file system that should be redirected to their FUSE implementation (for example, `ext4=fuse2fs`)
	"security.syscalls.intercept.mount.fuse": validate.IsAny,
	// gendoc:generate(group=instance-security, key=security.syscalls.intercept.mount.shift)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Controls whether to mount `shiftfs` on top of file systems handled through mount syscall interception
	"security.syscalls.intercept.mount.shift": validate.Optional(validate.IsBool),
	// gendoc:generate(group=instance-security, key=security.syscalls.intercept.sched_setcheduler)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Controls whether to handle the `sched_setscheduler` system call (allows increasing process priority)
	"security.syscalls.intercept.sched_setscheduler": validate.Optional(validate.IsBool),
	// gendoc:generate(group=instance-security, key=security.syscalls.intercept.setxattr)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Controls whether to handle the `setxattr` system call (allows setting a limited subset of restricted extended attributes)
	"security.syscalls.intercept.setxattr": validate.Optional(validate.IsBool),
	// gendoc:generate(group=instance-security, key=security.syscalls.intercept.sysinfo)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Controls whether to handle the `sysinfo` system call (to get cgroup-based resource usage information)
	"security.syscalls.intercept.sysinfo": validate.Optional(validate.IsBool),
	"security.syscalls.whitelist":         validate.IsAny,

	// gendoc:generate(group=instance-volatile, key=volatile.last_state.idmap)
	//
	// ---
	//  type: string
	//  shortdesc: Serialized instance UID/GID map
	"volatile.last_state.idmap": validate.IsAny,
	// gendoc:generate(group=instance-volatile, key=volatile.idmap.base)
	//
	// ---
	//  type: integer
	//  shortdesc: The first ID in the instance’s primary idmap range
	"volatile.idmap.base": validate.IsAny,
	// gendoc:generate(group=instance-volatile, key=volatile.idmap.current)
	//
	// ---
	//  type: string
	//  shortdesc: The idmap currently in use by the instance
	"volatile.idmap.current": validate.IsAny,
	// gendoc:generate(group=instance-volatile, key=volatile.idmap.next)
	//
	// ---
	//  type: string
	//  shortdesc: The idmap to use the next time the instance starts
	"volatile.idmap.next": validate.IsAny,
}

// InstanceConfigKeysVM is a map of config key to validator. (keys applying to VM only).
var InstanceConfigKeysVM = map[string]func(value string) error{
	// gendoc:generate(group=instance-resource-limits, key=limits.memory.hugepages)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: Controls whether to back the instance using huge pages rather than regular system memory
	"limits.memory.hugepages": validate.Optional(validate.IsBool),

	// gendoc:generate(group=instance-migration, key=migration.stateful)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: Controls whether to allow for stateful stop/start and snapshots (enabling this prevents the use of some features that are incompatible with it)
	"migration.stateful": validate.Optional(validate.IsBool),

	// Caller is responsible for full validation of any raw.* value.

	// gendoc:generate(group=instance-raw, key=raw.qemu)
	//
	// ---
	//  type: blob
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: Raw QEMU configuration to be appended to the generated command line
	"raw.qemu": validate.IsAny,
	// gendoc:generate(group=instance-raw, key=raw.qemu.conf)
	//
	// ---
	//  type: blob
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: Addition/override to the generated `qemu.conf` file (see {ref}`instance-options-qemu`)
	"raw.qemu.conf": validate.IsAny,

	// gendoc:generate(group=instance-security, key=security.agent.metrics)
	//
	// ---
	//  type: bool
	//  default: `true`
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: Controls whether the `incus-agent` is queried for state information and metrics
	"security.agent.metrics": validate.Optional(validate.IsBool),
	// gendoc:generate(group=instance-security, key=security.csm)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: Controls whether to use a firmware that supports UEFI-incompatible operating systems (when enabling this option, set `security.secureboot` to `false`)
	"security.csm": validate.Optional(validate.IsBool),
	// gendoc:generate(group=instance-security, key=security.secureboot)
	//
	// ---
	//  type: bool
	//  default: `true`
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: Controls whether UEFI secure boot is enabled with the default Microsoft keys (when disabling this option, consider enabling `security.csm`)
	"security.secureboot": validate.Optional(validate.IsBool),
	// gendoc:generate(group=instance-security, key=security.sev)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: Controls whether AMD SEV (Secure Encrypted Virtualization) is enabled for this VM
	"security.sev": validate.Optional(validate.IsBool),
	// gendoc:generate(group=instance-security, key=security.sev.policy.es)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: Controls whether AMD SEV-ES (SEV Encrypted State) is enabled for this VM
	"security.sev.policy.es": validate.Optional(validate.IsBool),
	// gendoc:generate(group=instance-security, key=security.sev.session.dh)
	//
	// ---
	//  type: string
	//  default: `true`
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: The guest owner’s `base64`-encoded Diffie-Hellman key
	"security.sev.session.dh": validate.Optional(validate.IsAny),
	// gendoc:generate(group=instance-security, key=security.sev.session.data)
	//
	// ---
	//  type: string
	//  default: `true`
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: The guest owner’s `base64`-encoded session blob
	"security.sev.session.data": validate.Optional(validate.IsAny),

	// gendoc:generate(group=instance-miscellaneous, key=user.*)
	//
	// ---
	//  type: string
	//  liveupdate: no
	//  shortdesc: Free-form user key/value storage (can be used in search)

	// gendoc:generate(group=instance-miscellaneous, key=agent.nic_config)
	//
	// ---
	//  type: bool
	//  default: `false`
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: Controls whether to set the name and MTU of the default network interfaces to be the same as the instance devices (this happens automatically for containers)
	"agent.nic_config": validate.Optional(validate.IsBool),

	// gendoc:generate(group=instance-volatile, key=volatile.apply_nvram)
	//
	// ---
	//  type: string
	//  shortdesc: Whether to regenerate VM NVRAM the next time the instance starts
	"volatile.apply_nvram": validate.Optional(validate.IsBool),
	// gendoc:generate(group=instance-volatile, key=volatile.vsock_id)
	//
	// ---
	//  type: string
	//  shortdesc: Instance `vsock ID` used as of last start
	"volatile.vsock_id": validate.Optional(validate.IsInt64),
}

// ConfigKeyChecker returns a function that will check whether or not
// a provide value is valid for the associate config key.  Returns an
// error if the key is not known.  The checker function only performs
// syntactic checking of the value, semantic and usage checking must
// be done by the caller.  User defined keys are always considered to
// be valid, e.g. user.* and environment.* keys.
func ConfigKeyChecker(key string, instanceType api.InstanceType) (func(value string) error, error) {
	f, ok := InstanceConfigKeysAny[key]
	if ok {
		return f, nil
	}

	if instanceType == api.InstanceTypeAny || instanceType == api.InstanceTypeContainer {
		f, ok := InstanceConfigKeysContainer[key]
		if ok {
			return f, nil
		}
	}

	if instanceType == api.InstanceTypeAny || instanceType == api.InstanceTypeVM {
		f, ok := InstanceConfigKeysVM[key]
		if ok {
			return f, nil
		}
	}

	if strings.HasPrefix(key, ConfigVolatilePrefix) {
		// gendoc:generate(group=instance-volatile, key=volatile.<name>.last_state.hwaddr)
		//
		// ---
		//  type: string
		//  shortdesc: Network device original MAC used when moving a physical device into an instance

		// gendoc:generate(group=instance-volatile, key=volatile.<name>.hwaddr)
		//
		// ---
		//  type: string
		//  shortdesc: Network device MAC address (when no `hwaddr` property is set on the device itself)

		// gendoc:generate(group=instance-volatile, key=volatile.<name>.last_state.vf.hwaddr)
		//
		// ---
		//  type: string
		//  shortdesc: SR-IOV virtual function original MAC used when moving a VF into an instance
		if strings.HasSuffix(key, ".hwaddr") {
			return validate.IsAny, nil
		}

		// gendoc:generate(group=instance-volatile, key=volatile.<name>.last_state.vdpa.name)
		//
		// ---
		//  type: string
		//  shortdesc: VDPA device name used when moving a VDPA device file descriptor into an instance
		if strings.HasSuffix(key, ".name") {
			return validate.IsAny, nil
		}

		// gendoc:generate(group=instance-volatile, key=volatile.<name>.host_name)
		//
		// ---
		//  type: string
		//  shortdesc: Network device name on the host
		if strings.HasSuffix(key, ".host_name") {
			return validate.IsAny, nil
		}

		// gendoc:generate(group=instance-volatile, key=volatile.<name>.last_state.mtu)
		//
		// ---
		//  type: string
		//  shortdesc: Network device original MTU used when moving a physical device into an instance
		if strings.HasSuffix(key, ".mtu") {
			return validate.IsAny, nil
		}

		// gendoc:generate(group=instance-volatile, key=volatile.<name>.last_state.created)
		//
		// ---
		//  type: string
		//  shortdesc: Whether the network device physical device was created (`true` or `false`)
		if strings.HasSuffix(key, ".created") {
			return validate.IsAny, nil
		}

		// gendoc:generate(group=instance-volatile, key=volatile.<name>.last_state.vf.id)
		//
		// ---
		//  type: string
		//  shortdesc: SR-IOV virtual function ID used when moving a VF into an instance
		if strings.HasSuffix(key, ".id") {
			return validate.IsAny, nil
		}

		// gendoc:generate(group=instance-volatile, key=volatile.<name>.last_state.vf.vlan)
		//
		// ---
		//  type: string
		//  shortdesc: SR-IOV virtual function original VLAN used when moving a VF into an instance
		if strings.HasSuffix(key, ".vlan") {
			return validate.IsAny, nil
		}

		// gendoc:generate(group=instance-volatile, key=volatile.<name>.last_state.vf.spoofcheck)
		//
		// ---
		//  type: string
		//  shortdesc: SR-IOV virtual function original spoof check setting used when moving a VF into an instance
		if strings.HasSuffix(key, ".spoofcheck") {
			return validate.IsAny, nil
		}

		if strings.HasSuffix(key, ".last_state.vf.parent") {
			return validate.IsAny, nil
		}

		// gendoc:generate(group=instance-volatile, key=volatile.<name>.last_state.ip_addresses)
		//
		// ---
		//  type: string
		//  shortdesc: Network device comma-separated list of last used IP addresses
		if strings.HasSuffix(key, ".last_state.ip_addresses") {
			return validate.IsListOf(validate.IsNetworkAddress), nil
		}

		// gendoc:generate(group=instance-volatile, key=volatile.<name>.apply_quota)
		//
		// ---
		//  type: string
		//  shortdesc: Disk quota to be applied the next time the instance starts
		if strings.HasSuffix(key, ".apply_quota") {
			return validate.IsAny, nil
		}

		// gendoc:generate(group=instance-volatile, key=volatile.<name>.ceph_rbd)
		//
		// ---
		//  type: string
		//  shortdesc: RBD device path for Ceph disk devices
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

	if (instanceType == api.InstanceTypeAny || instanceType == api.InstanceTypeContainer) &&
		strings.HasPrefix(key, "linux.sysctl.") {
		return validate.IsAny, nil
	}

	return nil, fmt.Errorf("Unknown configuration key: %s", key)
}

// InstanceIncludeWhenCopying is used to decide whether to include a config item or not when copying an instance.
// The remoteCopy argument indicates if the copy is remote (i.e between servers) as this affects the keys kept.
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
