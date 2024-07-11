package instance

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/units"
	"github.com/lxc/incus/v6/shared/validate"
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
	// gendoc:generate(entity=instance, group=boot, key=boot.autostart)
	// If set to `false`, restore the last state.
	// ---
	//  type: bool
	//  liveupdate: no
	//  shortdesc: Whether to always start the instance when the daemon starts
	"boot.autostart": validate.Optional(validate.IsBool),

	// gendoc:generate(entity=instance, group=boot, key=boot.autostart.delay)
	// The number of seconds to wait after the instance started before starting the next one.
	// ---
	//  type: integer
	//  defaultdesc: 0
	//  liveupdate: no
	//  shortdesc: Delay after starting the instance
	"boot.autostart.delay": validate.Optional(validate.IsInt64),

	// gendoc:generate(entity=instance, group=boot, key=boot.autostart.priority)
	// The instance with the highest value is started first.
	// ---
	//  type: integer
	//  defaultdesc: 0
	//  liveupdate: no
	//  shortdesc: What order to start the instances in
	"boot.autostart.priority": validate.Optional(validate.IsInt64),

	// gendoc:generate(entity=instance, group=boot, key=boot.stop.priority)
	// The instance with the highest value is shut down first.
	// ---
	//  type: integer
	//  defaultdesc: 0
	//  liveupdate: no
	//  shortdesc: What order to shut down the instances in
	"boot.stop.priority": validate.Optional(validate.IsInt64),

	// gendoc:generate(entity=instance, group=boot, key=boot.host_shutdown_action)
	// Action to take on host shut down
	// ---
	//  type: integer
	//  defaultdesc: stop
	//  liveupdate: yes
	//  shortdesc: What action to take on the instance when the host is shut down
	"boot.host_shutdown_action": validate.Optional(validate.IsOneOf("stop", "force-stop", "stateful-stop")),

	// gendoc:generate(entity=instance, group=boot, key=boot.host_shutdown_timeout)
	// Number of seconds to wait for the instance to shut down before it is force-stopped.
	// ---
	//  type: integer
	//  defaultdesc: 30
	//  liveupdate: yes
	//  shortdesc: How long to wait for the instance to shut down
	"boot.host_shutdown_timeout": validate.Optional(validate.IsInt64),

	// gendoc:generate(entity=instance, group=cloud-init, key=cloud-init.network-config)
	// The content is used as seed value for `cloud-init`.
	// ---
	//  type: string
	//  defaultdesc: `DHCP on eth0`
	//  liveupdate: no
	//  condition: If supported by image
	//  shortdesc: Network configuration for `cloud-init`
	"cloud-init.network-config": validate.Optional(validate.IsYAML),

	// gendoc:generate(entity=instance, group=cloud-init, key=cloud-init.user-data)
	// The content is used as seed value for `cloud-init`.
	// ---
	//  type: string
	//  defaultdesc: `#cloud-config`
	//  liveupdate: no
	//  condition: If supported by image
	//  shortdesc: User data for `cloud-init`
	"cloud-init.user-data": validate.Optional(validate.IsCloudInitUserData),

	// gendoc:generate(entity=instance, group=cloud-init, key=cloud-init.vendor-data)
	// The content is used as seed value for `cloud-init`.
	// ---
	//  type: string
	//  defaultdesc: `#cloud-config`
	//  liveupdate: no
	//  condition: If supported by image
	//  shortdesc: Vendor data for `cloud-init`
	"cloud-init.vendor-data": validate.Optional(validate.IsCloudInitUserData),

	// gendoc:generate(entity=instance, group=cloud-init, key=user.network-config)
	//
	// ---
	//  type: string
	//  defaultdesc: `DHCP on eth0`
	//  liveupdate: no
	//  condition: If supported by image
	//  shortdesc: Legacy version of `cloud-init.network-config`

	// gendoc:generate(entity=instance, group=cloud-init, key=user.user-data)
	//
	// ---
	//  type: string
	//  defaultdesc: `#cloud-config`
	//  liveupdate: no
	//  condition: If supported by image
	//  shortdesc: Legacy version of `cloud-init.user-data`

	// gendoc:generate(entity=instance, group=cloud-init, key=user.vendor-data)
	//
	// ---
	//  type: string
	//  defaultdesc: `#cloud-config`
	//  liveupdate: no
	//  condition: If supported by image
	//  shortdesc: Legacy version of `cloud-init.vendor-data`

	// gendoc:generate(entity=instance, group=miscellaneous, key=cluster.evacuate)
	// The `cluster.evacuate` provides control over how instances are handled when a cluster member is being
	// evacuated.
	//
	// Available Modes:
	//   - `auto` *(default)*: The system will automatically decide the best evacuation method based on the
	//      instance's type and configured devices:
	//     + If any device is not suitable for migration, the instance will not be migrated (only stopped).
	//     + Live migration will be used only for virtual machines with the `migration.stateful` setting
	//       enabled and for which all its devices can be migrated as well.
	//   - `live-migrate`: Instances are live-migrated to another server. This means the instance remains running
	//      and operational during the migration process, ensuring minimal disruption.
	//   - `migrate`: In this mode, instances are migrated to another server in the cluster. The migration
	//      process will not be live, meaning there will be a brief downtime for the instance during the
	//      migration.
	//   -  `stop`: Instances are not migrated. Instead, they are stopped on the current server.
	//   -  `stateful-stop`: Instances are not migrated. Instead, they are stopped on the current server
	//      but with their runtime state (memory) stored on disk for resuming on restore.
	//   -  `force-stop`: Instances are not migrated. Instead, they are forcefully stopped.
	//
	// See {ref}`cluster-evacuate` for more information.
	// ---
	//  type: string
	//  defaultdesc: `auto`
	//  liveupdate: no
	//  shortdesc: What to do when evacuating the instance
	"cluster.evacuate": validate.Optional(validate.IsOneOf("auto", "migrate", "live-migrate", "stop", "stateful-stop", "force-stop")),

	// gendoc:generate(entity=instance, group=resource-limits, key=limits.cpu)
	// A number or a specific range of CPUs to expose to the instance.
	//
	// See {ref}`instance-options-limits-cpu` for more information.
	// ---
	//  type: string
	//  defaultdesc: 1 (VMs)
	//  liveupdate: yes
	//  shortdesc: Which CPUs to expose to the instance
	"limits.cpu": validate.Optional(validate.IsValidCPUSet),

	// gendoc:generate(entity=instance, group=resource-limits, key=limits.cpu.nodes)
	// A comma-separated list of NUMA node IDs or ranges to place the instance CPUs on.
	// Alternatively, the value `balanced` may be used to have Incus pick the least busy NUMA node on startup.
	//
	// See {ref}`instance-options-limits-cpu-container` for more information.
	// ---
	//  type: string
	//  liveupdate: yes
	//  shortdesc: Which NUMA nodes to place the instance CPUs on
	"limits.cpu.nodes": validate.Optional(validate.Or(validate.IsValidCPUSet, validate.IsOneOf("balanced"))),

	// gendoc:generate(entity=instance, group=resource-limits, key=limits.disk.priority)
	// Controls how much priority to give to the instance's I/O requests when under load.
	//
	// Specify an integer between 0 and 10.
	// ---
	//  type: integer
	//  defaultdesc: `5` (medium)
	//  liveupdate: yes
	//  shortdesc: Priority of the instance's I/O requests
	"limits.disk.priority": validate.Optional(validate.IsPriority),

	// gendoc:generate(entity=instance, group=resource-limits, key=limits.memory)
	// Percentage of the host's memory or a fixed value in bytes.
	// Various suffixes are supported.
	//
	// See {ref}`instances-limit-units` for details.
	// ---
	//  type: string
	//  defaultdesc: `1Gib` (VMs)
	//  liveupdate: yes
	//  shortdesc: Usage limit for the host's memory
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

	// gendoc:generate(entity=instance, group=migration, key=migration.stateful)
	// Enabling this option prevents the use of some features that are incompatible with it.
	// ---
	//  type: bool
	//  defaultdesc: `false`
	//  liveupdate: no
	//  shortdesc: Whether to allow for stateful stop/start and snapshots
	"migration.stateful": validate.Optional(validate.IsBool),

	// Caller is responsible for full validation of any raw.* value.

	// gendoc:generate(entity=instance, group=raw, key=raw.apparmor)
	// The specified entries are appended to the generated profile.
	// ---
	//  type: blob
	//  liveupdate: yes
	//  shortdesc: AppArmor profile entries
	"raw.apparmor": validate.IsAny,

	// gendoc:generate(entity=instance, group=raw, key=raw.idmap)
	// For example: `both 1000 1000`
	// ---
	//  type: blob
	//  liveupdate: no
	//  condition: unprivileged container
	//  shortdesc: Raw idmap configuration
	"raw.idmap": validate.IsAny,

	// gendoc:generate(entity=instance, group=security, key=security.guestapi)
	// See {ref}`dev-incus` for more information.
	// ---
	//  type: bool
	//  defaultdesc: `true`
	//  liveupdate: no
	//  shortdesc: Whether `/dev/incus` is present in the instance
	"security.guestapi": validate.Optional(validate.IsBool),

	// gendoc:generate(entity=instance, group=security, key=security.protection.delete)
	//
	// ---
	//  type: bool
	//  defaultdesc: `false`
	//  liveupdate: yes
	//  shortdesc: Prevents the instance from being deleted
	"security.protection.delete": validate.Optional(validate.IsBool),

	// gendoc:generate(entity=instance, group=snapshots, key=snapshots.schedule)
	// Specify either a cron expression (`<minute> <hour> <dom> <month> <dow>`), a comma-separated list of schedule aliases (`@hourly`, `@daily`, `@midnight`, `@weekly`, `@monthly`, `@annually`, `@yearly`), or leave empty to disable automatic snapshots.
	//
	// ---
	//  type: string
	//  defaultdesc: empty
	//  liveupdate: no
	//  shortdesc: Schedule for automatic instance snapshots
	"snapshots.schedule": validate.Optional(validate.IsCron([]string{"@hourly", "@daily", "@midnight", "@weekly", "@monthly", "@annually", "@yearly", "@startup", "@never"})),

	// gendoc:generate(entity=instance, group=snapshots, key=snapshots.schedule.stopped)
	//
	// ---
	//  type: bool
	//  defaultdesc: `false`
	//  liveupdate: no
	//  shortdesc: Whether to automatically snapshot stopped instances
	"snapshots.schedule.stopped": validate.Optional(validate.IsBool),

	// gendoc:generate(entity=instance, group=snapshots, key=snapshots.pattern)
	// Specify a Pongo2 template string that represents the snapshot name.
	// This template is used for scheduled snapshots and for unnamed snapshots.
	//
	// See {ref}`instance-options-snapshots-names` for more information.
	// ---
	//  type: string
	//  defaultdesc: `snap%d`
	//  liveupdate: no
	//  shortdesc: Template for the snapshot name
	"snapshots.pattern": validate.IsAny,

	// gendoc:generate(entity=instance, group=snapshots, key=snapshots.expiry)
	// Specify an expression like `1M 2H 3d 4w 5m 6y`.
	// ---
	//  type: string
	//  liveupdate: no
	//  shortdesc: When snapshots are to be deleted
	"snapshots.expiry": func(value string) error {
		// Validate expression
		_, err := GetExpiry(time.Time{}, value)
		return err
	},

	// Volatile keys.

	// gendoc:generate(entity=instance, group=volatile, key=volatile.apply_template)
	// The template with the given name is triggered upon next startup.
	// ---
	//  type: string
	//  shortdesc: Template hook
	"volatile.apply_template": validate.IsAny,

	// gendoc:generate(entity=instance, group=volatile, key=volatile.base_image)
	// The hash of the image that the instance was created from (empty if the instance was not created from an image).
	// ---
	//  type: string
	//  shortdesc: Hash of the base image
	"volatile.base_image": validate.IsAny,

	// gendoc:generate(entity=instance, group=volatile, key=volatile.cloud_init.instance-id)
	//
	// ---
	//  type: string
	//  shortdesc: `instance-id` (UUID) exposed to `cloud-init`
	"volatile.cloud-init.instance-id": validate.Optional(validate.IsUUID),

	// gendoc:generate(entity=instance, group=volatile, key=volatile.cluster.group)
	// The cluster group(s) that the instance was restricted to at creation time.
	// This is used during re-scheduling events like an evacuation to keep the instance within the requested set.
	// ---
	//  type: string
	//  shortdesc: The original cluster group for the instance
	"volatile.cluster.group": validate.IsAny,

	// gendoc:generate(entity=instance, group=volatile, key=volatile.cpu.nodes)
	// The NUMA node that was selected for the instance.
	// ---
	//  type: string
	//  shortdesc: Instance NUMA node
	"volatile.cpu.nodes": validate.Optional(validate.IsValidCPUSet),

	// gendoc:generate(entity=instance, group=volatile, key=volatile.evacuate.origin)
	// The cluster member that the instance lived on before evacuation.
	// ---
	//  type: string
	//  shortdesc: The origin of the evacuated instance
	"volatile.evacuate.origin": validate.IsAny,

	// gendoc:generate(entity=instance, group=volatile, key=volatile.last_state.power)
	//
	// ---
	//  type: string
	//  shortdesc: Instance state as of last host shutdown
	"volatile.last_state.power": validate.IsAny,

	// gendoc:generate(entity=instance, group=volatile, key=volatile.last_state.ready)
	//
	// ---
	//  type: string
	//  shortdesc: Instance marked itself as ready
	"volatile.last_state.ready": validate.IsBool,

	// gendoc:generate(entity=instance, group=volatile, key=volatile.uuid)
	// The instance UUID is globally unique across all servers and projects.
	// ---
	//  type: string
	//  shortdesc: Instance UUID
	"volatile.uuid": validate.Optional(validate.IsUUID),

	// gendoc:generate(entity=instance, group=volatile, key=volatile.uuid.generation)
	// The instance generation UUID changes whenever the instance's place in time moves backwards.
	// It is globally unique across all servers and projects.
	// ---
	//  type: string
	//  shortdesc: Instance generation UUID
	"volatile.uuid.generation": validate.Optional(validate.IsUUID),
}

// InstanceConfigKeysContainer is a map of config key to validator. (keys applying to containers only).
var InstanceConfigKeysContainer = map[string]func(value string) error{
	// gendoc:generate(entity=instance, group=resource-limits, key=limits.cpu.allowance)
	// To control how much of the CPU can be used, specify either a percentage (`50%`) for a soft limit
	// or a chunk of time (`25ms/100ms`) for a hard limit.
	//
	// See {ref}`instance-options-limits-cpu-container` for more information.
	// ---
	//  type: string
	//  defaultdesc: 100%
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: How much of the CPU can be used
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

	// gendoc:generate(entity=instance, group=resource-limits, key=limits.cpu.priority)
	// When overcommitting resources, specify the CPU scheduling priority compared to other instances that share the same CPUs.
	// Specify an integer between 0 and 10.
	//
	// See {ref}`instance-options-limits-cpu-container` for more information.
	// ---
	//  type: integer
	//  defaultdesc: `10` (maximum)
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: CPU scheduling priority compared to other instances
	"limits.cpu.priority": validate.Optional(validate.IsPriority),

	// gendoc:generate(entity=instance, group=resource-limits, key=limits.hugepages.64KB)
	// Fixed value (in bytes) to limit the number of 64 KB huge pages.
	// Various suffixes are supported (see {ref}`instances-limit-units`).
	//
	// See {ref}`instance-options-limits-hugepages` for more information.
	// ---
	//  type: string
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Limit for the number of 64 KB huge pages
	"limits.hugepages.64KB": validate.Optional(validate.IsSize),

	// gendoc:generate(entity=instance, group=resource-limits, key=limits.hugepages.1MB)
	// Fixed value (in bytes) to limit the number of 1 MB huge pages.
	// Various suffixes are supported (see {ref}`instances-limit-units`).
	//
	// See {ref}`instance-options-limits-hugepages` for more information.
	// ---
	//  type: string
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Limit for the number of 1 MB huge pages
	"limits.hugepages.1MB": validate.Optional(validate.IsSize),

	// gendoc:generate(entity=instance, group=resource-limits, key=limits.hugepages.2MB)
	// Fixed value (in bytes) to limit the number of 2 MB huge pages.
	// Various suffixes are supported (see {ref}`instances-limit-units`).
	//
	// See {ref}`instance-options-limits-hugepages` for more information.
	// ---
	//  type: string
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Limit for the number of 2 MB huge pages
	"limits.hugepages.2MB": validate.Optional(validate.IsSize),

	// gendoc:generate(entity=instance, group=resource-limits, key=limits.hugepages.1GB)
	// Fixed value (in bytes) to limit the number of 1 GB huge pages.
	// Various suffixes are supported (see {ref}`instances-limit-units`).
	//
	// See {ref}`instance-options-limits-hugepages` for more information.
	// ---
	//  type: string
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Limit for the number of 1 GB huge pages
	"limits.hugepages.1GB": validate.Optional(validate.IsSize),

	// gendoc:generate(entity=instance, group=resource-limits, key=limits.memory.enforce)
	// If the instance's memory limit is `hard`, the instance cannot exceed its limit.
	// If it is `soft`, the instance can exceed its memory limit when extra host memory is available.
	// ---
	//  type: string
	//  defaultdesc: `hard`
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Whether the memory limit is `hard` or `soft`
	"limits.memory.enforce": validate.Optional(validate.IsOneOf("soft", "hard")),

	// gendoc:generate(entity=instance, group=resource-limits, key=limits.memory.swap)
	// When set to `true` or `false`, it controls whether the container is likely to get some of
	// its memory swapped by the kernel. Alternatively, it can be set to a bytes value which will
	// then allow the container to make use of additional memory through swap.
	// ---
	//  type: string
	//  defaultdesc: `true`
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Control swap usage by the instance
	"limits.memory.swap": validate.Optional(validate.Or(validate.IsBool, validate.IsSize)),

	// gendoc:generate(entity=instance, group=resource-limits, key=limits.memory.swap.priority)
	// Specify an integer between 0 and 10.
	// The higher the value, the less likely the instance is to be swapped to disk.
	// ---
	//  type: integer
	//  defaultdesc: `10` (maximum)
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Prevents the instance from being swapped to disk
	"limits.memory.swap.priority": validate.Optional(validate.IsPriority),

	// gendoc:generate(entity=instance, group=resource-limits, key=limits.processes)
	// If left empty, no limit is set.
	// ---
	//  type: integer
	//  defaultdesc: empty
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Maximum number of processes that can run in the instance
	"limits.processes": validate.Optional(validate.IsInt64),

	// gendoc:generate(entity=instance, group=miscellaneous, key=linux.kernel_modules)
	// Specify the kernel modules as a comma-separated list.
	// ---
	//  type: string
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Kernel modules to load before starting the instance
	"linux.kernel_modules": validate.IsAny,

	// gendoc:generate(entity=instance, group=migration, key=migration.incremental.memory)
	// Using incremental memory transfer of the instance's memory can reduce downtime.
	// ---
	//  type: bool
	//  defaultdesc: `false`
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Whether to use incremental memory transfer
	"migration.incremental.memory": validate.Optional(validate.IsBool),

	// gendoc:generate(entity=instance, group=migration, key=migration.incremental.memory.iterations)
	//
	// ---
	//  type: integer
	//  defaultdesc: `10`
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Maximum number of transfer operations to go through before stopping the instance
	"migration.incremental.memory.iterations": validate.Optional(validate.IsUint32),

	// gendoc:generate(entity=instance, group=migration, key=migration.incremental.memory.goal)
	//
	// ---
	//  type: integer
	//  defaultdesc: `70`
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Percentage of memory to have in sync before stopping the instance
	"migration.incremental.memory.goal": validate.Optional(validate.IsUint32),

	// gendoc:generate(entity=instance, group=nvidia, key=nvidia.runtime)
	//
	// ---
	//  type: bool
	//  defaultdesc: `false`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Whether to pass the host NVIDIA and CUDA runtime libraries into the instance
	"nvidia.runtime": validate.Optional(validate.IsBool),

	// gendoc:generate(entity=instance, group=nvidia, key=nvidia.driver.capabilities)
	// The specified driver capabilities are used to set `libnvidia-container NVIDIA_DRIVER_CAPABILITIES`.
	// ---
	//  type: string
	//  defaultdesc: `compute,utility`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: What driver capabilities the instance needs
	"nvidia.driver.capabilities": validate.IsAny,

	// gendoc:generate(entity=instance, group=nvidia, key=nvidia.require.cuda)
	// The specified version expression is used to set `libnvidia-container NVIDIA_REQUIRE_CUDA`.
	// ---
	//  type: string
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Required CUDA version
	"nvidia.require.cuda": validate.IsAny,

	// gendoc:generate(entity=instance, group=nvidia, key=nvidia.require.driver)
	// The specified version expression is used to set `libnvidia-container NVIDIA_REQUIRE_DRIVER`.
	// ---
	//  type: string
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Required driver version
	"nvidia.require.driver": validate.IsAny,

	// Caller is responsible for full validation of any raw.* value.

	// gendoc:generate(entity=instance, group=raw, key=raw.lxc)
	//
	// ---
	//  type: blob
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Raw LXC configuration to be appended to the generated one
	"raw.lxc": validate.IsAny,

	// gendoc:generate(entity=instance, group=raw, key=raw.seccomp)
	//
	// ---
	//  type: blob
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Raw Seccomp configuration
	"raw.seccomp": validate.IsAny,

	// gendoc:generate(entity=instance, group=security, key=security.guestapi.images)
	//
	// ---
	//  type: bool
	//  defaultdesc: `false`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Controls the availability of the `/1.0/images` API over `guestapi`
	"security.guestapi.images": validate.Optional(validate.IsBool),

	// gendoc:generate(entity=instance, group=security, key=security.idmap.base)
	// Setting this option overrides auto-detection.
	// ---
	//  type: integer
	//  liveupdate: no
	//  condition: unprivileged container
	//  shortdesc: The base host ID to use for the allocation
	"security.idmap.base": validate.Optional(validate.IsUint32),

	// gendoc:generate(entity=instance, group=security, key=security.idmap.isolated)
	// If specified, the idmap used for this instance is unique among instances that have this option set.
	// ---
	//  type: bool
	//  defaultdesc: `false`
	//  liveupdate: no
	//  condition: unprivileged container
	//  shortdesc: Whether to use a unique idmap for this instance
	"security.idmap.isolated": validate.Optional(validate.IsBool),

	// gendoc:generate(entity=instance, group=security, key=security.idmap.size)
	//
	// ---
	//  type: integer
	//  liveupdate: no
	//  condition: unprivileged container
	//  shortdesc: The size of the idmap to use
	"security.idmap.size": validate.Optional(validate.IsUint32),

	// gendoc:generate(entity=instance, group=security, key=security.nesting)
	//
	// ---
	//  type: bool
	//  defaultdesc: `false`
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Whether to support running Incus (nested) inside the instance
	"security.nesting": validate.Optional(validate.IsBool),

	// gendoc:generate(entity=instance, group=security, key=security.privileged)
	//
	// ---
	//  type: bool
	//  defaultdesc: `false`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Whether to run the instance in privileged mode
	"security.privileged": validate.Optional(validate.IsBool),

	// gendoc:generate(entity=instance, group=security, key=security.protection.shift)
	// Set this option to `true` to prevent the instance's file system from being UID/GID shifted on startup.
	// ---
	//  type: bool
	//  defaultdesc: `false`
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Whether to protect the file system from being UID/GID shifted
	"security.protection.shift": validate.Optional(validate.IsBool),

	// gendoc:generate(entity=instance, group=security, key=security.syscalls.allow)
	// A `\n`-separated list of syscalls to allow.
	// This list must be mutually exclusive with `security.syscalls.deny*`.
	// ---
	//  type: string
	//  liveupdate: no
	//  condition: container
	//  shortdesc: List of syscalls to allow
	"security.syscalls.allow": validate.IsAny,

	// Legacy configuration keys (old names).
	"security.syscalls.blacklist_default": validate.Optional(validate.IsBool),
	"security.syscalls.blacklist_compat":  validate.Optional(validate.IsBool),
	"security.syscalls.blacklist":         validate.IsAny,

	// gendoc:generate(entity=instance, group=security, key=security.syscalls.deny_default)
	//
	// ---
	//  type: bool
	//  defaultdesc: `true`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Whether to enable the default syscall deny
	"security.syscalls.deny_default": validate.Optional(validate.IsBool),

	// gendoc:generate(entity=instance, group=security, key=security.syscalls.deny_compat)
	// On `x86_64`, this option controls whether to block `compat_*` syscalls.
	// On other architectures, the option is ignored.
	// ---
	//  type: bool
	//  defaultdesc: `false`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Whether to block `compat_*` syscalls (`x86_64` only)
	"security.syscalls.deny_compat": validate.Optional(validate.IsBool),

	// gendoc:generate(entity=instance, group=security, key=security.syscalls.deny)
	// A `\n`-separated list of syscalls to deny.
	// This list must be mutually exclusive with `security.syscalls.allow`.
	// ---
	//  type: string
	//  liveupdate: no
	//  condition: container
	//  shortdesc: List of syscalls to deny
	"security.syscalls.deny": validate.IsAny,

	// gendoc:generate(entity=instance, group=security, key=security.syscalls.intercept.bpf)
	//
	// ---
	//  type: bool
	//  defaultdesc: `false`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Whether to handle the `bpf()` system call
	"security.syscalls.intercept.bpf": validate.Optional(validate.IsBool),

	// gendoc:generate(entity=instance, group=security, key=security.syscalls.intercept.bpf.devices)
	// This option controls whether to allow BPF programs for the devices cgroup in the unified hierarchy to be loaded.
	// ---
	//  type: bool
	//  defaultdesc: `false`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Whether to allow BPF programs
	"security.syscalls.intercept.bpf.devices": validate.Optional(validate.IsBool),

	// gendoc:generate(entity=instance, group=security, key=security.syscalls.intercept.mknod)
	// These system calls allow creation of a limited subset of char/block devices.
	// ---
	//  type: bool
	//  defaultdesc: `false`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Whether to handle the `mknod` and `mknodat` system calls
	"security.syscalls.intercept.mknod": validate.Optional(validate.IsBool),

	// gendoc:generate(entity=instance, group=security, key=security.syscalls.intercept.mount)
	//
	// ---
	//  type: bool
	//  defaultdesc: `false`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Whether to handle the `mount` system call
	"security.syscalls.intercept.mount": validate.Optional(validate.IsBool),

	// gendoc:generate(entity=instance, group=security, key=security.syscalls.intercept.mount.allowed)
	// Specify a comma-separated list of file systems that are safe to mount for processes inside the instance.
	// ---
	//  type: string
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: File systems that can be mounted
	"security.syscalls.intercept.mount.allowed": validate.IsAny,

	// gendoc:generate(entity=instance, group=security, key=security.syscalls.intercept.mount.fuse)
	// Specify the mounts of a given file system that should be redirected to their FUSE implementation (for example, `ext4=fuse2fs`).
	// ---
	//  type: string
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: File system that should be redirected to FUSE implementation
	"security.syscalls.intercept.mount.fuse": validate.IsAny,

	// gendoc:generate(entity=instance, group=security, key=security.syscalls.intercept.mount.shift)
	//
	// ---
	//  type: bool
	//  defaultdesc: `false`
	//  liveupdate: yes
	//  condition: container
	//  shortdesc: Whether to use idmapped mounts for syscall interception
	"security.syscalls.intercept.mount.shift": validate.Optional(validate.IsBool),

	// gendoc:generate(entity=instance, group=security, key=security.syscalls.intercept.sched_setcheduler)
	// This system call allows increasing process priority.
	// ---
	//  type: bool
	//  defaultdesc: `false`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Whether to handle the `sched_setscheduler` system call
	"security.syscalls.intercept.sched_setscheduler": validate.Optional(validate.IsBool),

	// gendoc:generate(entity=instance, group=security, key=security.syscalls.intercept.setxattr)
	// This system call allows setting a limited subset of restricted extended attributes.
	// ---
	//  type: bool
	//  defaultdesc: `false`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Whether to handle the `setxattr` system call
	"security.syscalls.intercept.setxattr": validate.Optional(validate.IsBool),

	// gendoc:generate(entity=instance, group=security, key=security.syscalls.intercept.sysinfo)
	// This system call can be used to get cgroup-based resource usage information.
	// ---
	//  type: bool
	//  defaultdesc: `false`
	//  liveupdate: no
	//  condition: container
	//  shortdesc: Whether to handle the `sysinfo` system call
	"security.syscalls.intercept.sysinfo": validate.Optional(validate.IsBool),

	"security.syscalls.whitelist": validate.IsAny,

	// gendoc:generate(entity=instance, group=volatile, key=volatile.container.oci)
	//
	// ---
	//  type: bool
	//  defaultdesc: `false`
	//  shortdesc: Whether the container is an OCI application container
	"volatile.container.oci": validate.IsBool,

	// gendoc:generate(entity=instance, group=volatile, key=volatile.last_state.idmap)
	//
	// ---
	//  type: string
	//  shortdesc: Serialized instance UID/GID map
	"volatile.last_state.idmap": validate.IsAny,

	// gendoc:generate(entity=instance, group=volatile, key=volatile.idmap.base)
	//
	// ---
	//  type: integer
	//  shortdesc: The first ID in the instance's primary idmap range
	"volatile.idmap.base": validate.IsAny,

	// gendoc:generate(entity=instance, group=volatile, key=volatile.idmap.current)
	//
	// ---
	//  type: string
	//  shortdesc: The idmap currently in use by the instance
	"volatile.idmap.current": validate.IsAny,

	// gendoc:generate(entity=instance, group=volatile, key=volatile.idmap.next)
	//
	// ---
	//  type: string
	//  shortdesc: The idmap to use the next time the instance starts
	"volatile.idmap.next": validate.IsAny,
}

// InstanceConfigKeysVM is a map of config key to validator. (keys applying to VM only).
var InstanceConfigKeysVM = map[string]func(value string) error{
	// gendoc:generate(entity=instance, group=resource-limits, key=limits.memory.hugepages)
	// If this option is set to `false`, regular system memory is used.
	// ---
	//  type: bool
	//  defaultdesc: `false`
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: Whether to back the instance using huge pages
	"limits.memory.hugepages": validate.Optional(validate.IsBool),

	// Caller is responsible for full validation of any raw.* value.

	// gendoc:generate(entity=instance, group=raw, key=raw.qemu)
	//
	// ---
	//  type: blob
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: Raw QEMU configuration to be appended to the generated command line
	"raw.qemu": validate.IsAny,

	// gendoc:generate(entity=instance, group=raw, key=raw.qemu.conf)
	// See {ref}`instance-options-qemu` for more information.
	// ---
	//  type: blob
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: Addition/override to the generated `qemu.conf` file
	"raw.qemu.conf": validate.IsAny,

	// gendoc:generate(entity=instance, group=security, key=security.agent.metrics)
	//
	// ---
	//  type: bool
	//  defaultdesc: `true`
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: Whether the `incus-agent` is queried for state information and metrics
	"security.agent.metrics": validate.Optional(validate.IsBool),

	// gendoc:generate(entity=instance, group=security, key=security.csm)
	// When enabling this option, set {config:option}`instance-security:security.secureboot` to `false`.
	// ---
	//  type: bool
	//  defaultdesc: `false`
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: Whether to use a firmware that supports UEFI-incompatible operating systems
	"security.csm": validate.Optional(validate.IsBool),

	// gendoc:generate(entity=instance, group=security, key=security.secureboot)
	// When disabling this option, consider enabling {config:option}`instance-security:security.csm`.
	// ---
	//  type: bool
	//  defaultdesc: `true`
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: Whether UEFI secure boot is enabled with the default Microsoft keys
	"security.secureboot": validate.Optional(validate.IsBool),

	// gendoc:generate(entity=instance, group=security, key=security.sev)
	//
	// ---
	//  type: bool
	//  defaultdesc: `false`
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: Whether AMD SEV (Secure Encrypted Virtualization) is enabled for this VM
	"security.sev": validate.Optional(validate.IsBool),

	// gendoc:generate(entity=instance, group=security, key=security.sev.policy.es)
	//
	// ---
	//  type: bool
	//  defaultdesc: `false`
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: Whether AMD SEV-ES (SEV Encrypted State) is enabled for this VM
	"security.sev.policy.es": validate.Optional(validate.IsBool),

	// gendoc:generate(entity=instance, group=security, key=security.sev.session.dh)
	//
	// ---
	//  type: string
	//  defaultdesc: `true`
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: The guest owner's `base64`-encoded Diffie-Hellman key
	"security.sev.session.dh": validate.Optional(validate.IsAny),

	// gendoc:generate(entity=instance, group=security, key=security.sev.session.data)
	//
	// ---
	//  type: string
	//  defaultdesc: `true`
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: The guest owner's `base64`-encoded session blob
	"security.sev.session.data": validate.Optional(validate.IsAny),

	// gendoc:generate(entity=instance, group=miscellaneous, key=user.*)
	// User keys can be used in search.
	// ---
	//  type: string
	//  liveupdate: no
	//  shortdesc: Free-form user key/value storage

	// gendoc:generate(entity=instance, group=miscellaneous, key=agent.nic_config)
	// For containers, the name and MTU of the default network interfaces is used for the instance devices.
	// For virtual machines, set this option to `true` to set the name and MTU of the default network interfaces to be the same as the instance devices.
	// ---
	//  type: bool
	//  defaultdesc: `false`
	//  liveupdate: no
	//  condition: virtual machine
	//  shortdesc: Whether to use the name and MTU of the default network interfaces
	"agent.nic_config": validate.Optional(validate.IsBool),

	// gendoc:generate(entity=instance, group=volatile, key=volatile.apply_nvram)
	//
	// ---
	//  type: bool
	//  shortdesc: Whether to regenerate VM NVRAM the next time the instance starts
	"volatile.apply_nvram": validate.Optional(validate.IsBool),

	// gendoc:generate(entity=instance, group=volatile, key=volatile.vsock_id)
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
		// gendoc:generate(entity=instance, group=volatile, key=volatile.<name>.apply_quota)
		// The disk quota is applied the next time the instance starts.
		// ---
		//  type: string
		//  shortdesc: Disk quota
		if strings.HasSuffix(key, ".apply_quota") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=instance, group=volatile, key=volatile.<name>.ceph_rbd)
		//
		// ---
		//  type: string
		//  shortdesc: RBD device path for Ceph disk devices
		if strings.HasSuffix(key, ".ceph_rbd") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=instance, group=volatile, key=volatile.<name>.host_name)
		//
		// ---
		//  type: string
		//  shortdesc: Network device name on the host
		if strings.HasSuffix(key, ".host_name") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=instance, group=volatile, key=volatile.<name>.hwaddr)
		// The network device MAC address is used when no `hwaddr` property is set on the device itself.
		// ---
		//  type: string
		//  shortdesc: Network device MAC address
		if strings.HasSuffix(key, ".hwaddr") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=instance, group=volatile, key=volatile.<name>.mig.uuid)
		// The NVIDIA MIG instance UUID.
		// ---
		//  type: string
		//  shortdesc: MIG instance UUID
		if strings.HasSuffix(key, ".mig.uuid") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=instance, group=volatile, key=volatile.<name>.name)
		// The network interface name inside of the instance when no `name` property is set on the device itself.
		// ---
		//  type: string
		//  shortdesc: Network interface name inside of the instance
		if strings.HasSuffix(key, ".name") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=instance, group=volatile, key=volatile.<name>.vgpu.uuid)
		// The NVIDIA virtual GPU instance UUID.
		// ---
		//  type: string
		//  shortdesc: virtual GPU instance UUID
		if strings.HasSuffix(key, ".vgpu.uuid") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=instance, group=volatile, key=volatile.<name>.last_state.created)
		// Possible values are `true` or `false`.
		// ---
		//  type: string
		//  shortdesc: Whether the network device physical device was created
		if strings.HasSuffix(key, ".last_state.created") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=instance, group=volatile, key=volatile.<name>.last_state.hwaddr)
		// The original MAC that was used when moving a physical device into an instance.
		// ---
		//  type: string
		//  shortdesc: Network device original MAC
		if strings.HasSuffix(key, ".last_state.hwaddr") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=instance, group=volatile, key=volatile.<name>.last_state.ip_addresses)
		// Comma-separated list of the last used IP addresses of the network device.
		// ---
		//  type: string
		//  shortdesc: Last used IP addresses
		if strings.HasSuffix(key, ".last_state.ip_addresses") {
			return validate.IsListOf(validate.IsNetworkAddress), nil
		}

		// gendoc:generate(entity=instance, group=volatile, key=volatile.<name>.last_state.mtu)
		// The original MTU that was used when moving a physical device into an instance.
		// ---
		//  type: string
		//  shortdesc: Network device original MTU
		if strings.HasSuffix(key, ".last_state.mtu") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=instance, group=volatile, key=volatile.<name>.last_state.pci.driver)
		// The original host driver for the PCI device.
		// ---
		//  type: string
		//  shortdesc: PCI original host driver
		if strings.HasSuffix(key, ".last_state.pci.driver") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=instance, group=volatile, key=volatile.<name>.last_state.pci.parent)
		// The parent host device used when allocating a PCI device to an instance.
		// ---
		//  type: string
		//  shortdesc: PCI parent host device
		if strings.HasSuffix(key, ".last_state.pci.parent") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=instance, group=volatile, key=volatile.<name>.last_state.pci.slot.name)
		// The parent host device PCI slot name.
		// ---
		//  type: string
		//  shortdesc: PCI parent slot name
		if strings.HasSuffix(key, ".last_state.pci.slot.name") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=instance, group=volatile, key=volatile.<name>.last_state.usb.bus)
		// The original USB bus address.
		// ---
		//  type: string
		//  shortdesc: USB bus address
		if strings.HasSuffix(key, ".last_state.usb.bus") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=instance, group=volatile, key=volatile.<name>.last_state.usb.device)
		// The original USB device identifier.
		// ---
		//  type: string
		//  shortdesc: USB device identifier
		if strings.HasSuffix(key, ".last_state.usb.device") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=instance, group=volatile, key=volatile.<name>.last_state.vdpa.name)
		// The VDPA device name used when moving a VDPA device file descriptor into an instance.
		// ---
		//  type: string
		//  shortdesc: VDPA device name
		if strings.HasSuffix(key, ".last_state.vdpa.name") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=instance, group=volatile, key=volatile.<name>.last_state.vf.hwaddr)
		// The original MAC used when moving a VF into an instance.
		// ---
		//  type: string
		//  shortdesc: SR-IOV virtual function original MAC
		if strings.HasSuffix(key, ".last_state.vf.hwaddr") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=instance, group=volatile, key=volatile.<name>.last_state.vf.id)
		// The ID used when moving a VF into an instance.
		// ---
		//  type: string
		//  shortdesc: SR-IOV virtual function ID
		if strings.HasSuffix(key, ".last_state.vf.id") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=instance, group=volatile, key=volatile.<name>.last_state.vf.parent)
		// The parent host device used when allocating a VF into an instance.
		// ---
		//  type: string
		//  shortdesc: SR-IOV parent host device
		if strings.HasSuffix(key, ".last_state.vf.parent") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=instance, group=volatile, key=volatile.<name>.last_state.vf.spoofcheck)
		// The original spoof check setting used when moving a VF into an instance.
		// ---
		//  type: string
		//  shortdesc: SR-IOV virtual function original spoof check setting
		if strings.HasSuffix(key, ".last_state.vf.spoofcheck") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=instance, group=volatile, key=volatile.<name>.last_state.vf.vlan)
		// The original VLAN used when moving a VF into an instance.
		// ---
		//  type: string
		//  shortdesc: SR-IOV virtual function original VLAN
		if strings.HasSuffix(key, ".last_state.vf.vlan") {
			return validate.IsAny, nil
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

	if strings.HasPrefix(key, "limits.kernel.") {
		// gendoc:generate(entity=kernel, group=limits, key=limits.kernel.as)
		//
		// ---
		//  type: string
		//  resource: `RLIMIT_AS`
		//  shortdesc: Maximum size of the process's virtual memory
		if strings.HasSuffix(key, ".as") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=kernel, group=limits, key=limits.kernel.core)
		//
		// ---
		//  type: string
		//  resource: `RLIMIT_CORE`
		//  shortdesc: Maximum size of the process's core dump file
		if strings.HasSuffix(key, ".core") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=kernel, group=limits, key=limits.kernel.cpu)
		//
		// ---
		//  type: string
		//  resource: `RLIMIT_CPU`
		//  shortdesc: Limit in seconds on the amount of CPU time the process can consume
		if strings.HasSuffix(key, ".cpu") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=kernel, group=limits, key=limits.kernel.data)
		//
		// ---
		//  type: string
		//  resource: `RLIMIT_DATA`
		//  shortdesc: Maximum size of the process's data segment
		if strings.HasSuffix(key, ".data") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=kernel, group=limits, key=limits.kernel.fsize)
		//
		// ---
		//  type: string
		//  resource: `RLIMIT_FSIZE`
		//  shortdesc: Maximum size of files the process may create
		if strings.HasSuffix(key, ".fsize") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=kernel, group=limits, key=limits.kernel.locks)
		//
		// ---
		//  type: string
		//  resource: `RLIMIT_LOCKS`
		//  shortdesc: Limit on the number of file locks that this process may establish
		if strings.HasSuffix(key, ".locks") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=kernel, group=limits, key=limits.kernel.memlock)
		//
		// ---
		//  type: string
		//  resource: `RLIMIT_MEMLOCK`
		//  shortdesc: Limit on the number of bytes of memory that the process may lock in RAM
		if strings.HasSuffix(key, ".memlock") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=kernel, group=limits, key=limits.kernel.nice)
		//
		// ---
		//  type: string
		//  resource: `RLIMIT_NICE`
		//  shortdesc: Maximum value to which the process's nice value can be raised
		if strings.HasSuffix(key, ".nice") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=kernel, group=limits, key=limits.kernel.nofile)
		//
		// ---
		//  type: string
		//  resource: `RLIMIT_NOFILE`
		//  shortdesc: Maximum number of open files for the process
		if strings.HasSuffix(key, ".nofile") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=kernel, group=limits, key=limits.kernel.nproc)
		//
		// ---
		//  type: string
		//  resource: `RLIMIT_NPROC`
		//  shortdesc: Maximum number of processes that can be created for the user of the calling process
		if strings.HasSuffix(key, ".nproc") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=kernel, group=limits, key=limits.kernel.rtprio)
		//
		// ---
		//  type: string
		//  resource: `RLIMIT_RTPRIO`
		//  shortdesc: Maximum value on the real-time-priority that may be set for this process
		if strings.HasSuffix(key, ".rtprio") {
			return validate.IsAny, nil
		}

		// gendoc:generate(entity=kernel, group=limits, key=limits.kernel.sigpending)
		//
		// ---
		//  type: string
		//  resource: `RLIMIT_SIGPENDING`
		//  shortdesc: Limit on the number of bytes of memory that the process may lock in RAM
		if strings.HasSuffix(key, ".sigpending") {
			return validate.IsAny, nil
		}

		if len(key) > len("limits.kernel.") {
			return validate.IsAny, nil
		}
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
