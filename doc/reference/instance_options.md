(instance-options)=
# Instance options

Instance options are configuration options that are directly related to the instance.

See {ref}`instances-configure-options` for instructions on how to set the instance options.

The key/value configuration is namespaced.
The following options are available:

- {ref}`instance-options-misc`
- {ref}`instance-options-boot`
- [`cloud-init` configuration](instance-options-cloud-init)
- {ref}`instance-options-limits`
- {ref}`instance-options-migration`
- {ref}`instance-options-nvidia`
- {ref}`instance-options-raw`
- {ref}`instance-options-security`
- {ref}`instance-options-snapshots`
- {ref}`instance-options-volatile`

Note that while a type is defined for each option, all values are stored as strings and should be exported over the REST API as strings (which makes it possible to support any extra values without breaking backward compatibility).

(instance-options-misc)=
## Miscellaneous options

In addition to the configuration options listed in the following sections, these instance options are supported:

% Include content from [../config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group instance-miscellaneous start -->
    :end-before: <!-- config group instance-miscellaneous end -->
```

```{config:option} environment.* instance-miscellaneous
:type: "string"
:liveupdate: "yes (exec)"
:shortdesc: "Environment variables for the instance"

You can export key/value environment variables to the instance.
These are then set for [`incus exec`](incus_exec.md).
```

(instance-options-boot)=
## Boot-related options

The following instance options control the boot-related behavior of the instance:

% Include content from [../config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group instance-boot start -->
    :end-before: <!-- config group instance-boot end -->
```

(instance-options-cloud-init)=
## `cloud-init` configuration

The following instance options control the [`cloud-init`](cloud-init) configuration of the instance:

% Include content from [../config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group instance-cloud-init start -->
    :end-before: <!-- config group instance-cloud-init end -->
```

Support for these options depends on the image that is used and is not guaranteed.

If you specify both `cloud-init.user-data` and `cloud-init.vendor-data`, the content of both options is merged.
Therefore, make sure that the `cloud-init` configuration you specify in those options does not contain the same keys.

(instance-options-limits)=
## Resource limits

The following instance options specify resource limits for the instance:

% Include content from [../config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group instance-resource-limits start -->
    :end-before: <!-- config group instance-resource-limits end -->
```

```{config:option} limits.kernel.* instance-resource-limits
:type: "string"
:liveupdate: "no"
:condition: "container"
:shortdesc: "Kernel resources per instance"

You can set kernel limits on an instance, for example, you can limit the number of open files.
See {ref}`instance-options-limits-kernel` for more information.
```

### CPU limits

You have different options to limit CPU usage:

- Set `limits.cpu` to restrict which CPUs the instance can see and use.
  See {ref}`instance-options-limits-cpu` for how to set this option.
- Set `limits.cpu.allowance` to restrict the load an instance can put on the available CPUs.
  This option is available only for containers.
  See {ref}`instance-options-limits-cpu-container` for how to set this option.

It is possible to set both options at the same time to restrict both which CPUs are visible to the instance and the allowed usage of those instances.
However, if you use `limits.cpu.allowance` with a time limit, you should avoid using `limits.cpu` in addition, because that puts a lot of constraints on the scheduler and might lead to less efficient allocations.

The CPU limits are implemented through a mix of the `cpuset` and `cpu` cgroup controllers.

(instance-options-limits-cpu)=
#### CPU pinning

`limits.cpu` results in CPU pinning through the `cpuset` controller.
You can specify either which CPUs or how many CPUs are visible and available to the instance:

- To specify which CPUs to use, set `limits.cpu` to either a set of CPUs (for example, `1,2,3`) or a CPU range (for example, `0-3`).

  To pin to a single CPU, use the range syntax (for example, `1-1`) to differentiate it from a number of CPUs.
- If you specify a number (for example, `4`) of CPUs, Incus will do dynamic load-balancing of all instances that aren't pinned to specific CPUs, trying to spread the load on the machine.
  Instances are re-balanced every time an instance starts or stops, as well as whenever a CPU is added to the system.

##### CPU limits for virtual machines

```{note}
Incus supports live-updating the `limits.cpu` option.
However, for virtual machines, this only means that the respective CPUs are hotplugged.
Depending on the guest operating system, you might need to either restart the instance or complete some manual actions to bring the new CPUs online.
```

Incus virtual machines default to having just one vCPU allocated, which shows up as matching the host CPU vendor and type, but has a single core and no threads.

When `limits.cpu` is set to a single integer, Incus allocates multiple vCPUs and exposes them to the guest as full cores.
Those vCPUs are not pinned to specific physical cores on the host.
The number of vCPUs can be updated while the VM is running.

When `limits.cpu` is set to a range or comma-separated list of CPU IDs (as provided by [`incus info --resources`](incus_info.md)), the vCPUs are pinned to those physical cores.
In this scenario, Incus checks whether the CPU configuration lines up with a realistic hardware topology and if it does, it replicates that topology in the guest.
When doing CPU pinning, it is not possible to change the configuration while the VM is running.

For example, if the pinning configuration includes eight threads, with each pair of thread coming from the same core and an even number of cores spread across two CPUs, the guest will show two CPUs, each with two cores and each core with two threads.
The NUMA layout is similarly replicated and in this scenario, the guest would most likely end up with two NUMA nodes, one for each CPU socket.

In such an environment with multiple NUMA nodes, the memory is similarly divided across NUMA nodes and be pinned accordingly on the host and then exposed to the guest.

All this allows for very high performance operations in the guest as the guest scheduler can properly reason about sockets, cores and threads as well as consider NUMA topology when sharing memory or moving processes across NUMA nodes.

(instance-options-limits-cpu-container)=
#### Allowance and priority (container only)

`limits.cpu.allowance` drives either the CFS scheduler quotas when passed a time constraint, or the generic CPU shares mechanism when passed a percentage value:

- The time constraint (for example, `20ms/50ms`) is a hard limit.
  For example, if you want to allow the container to use a maximum of one CPU, set `limits.cpu.allowance` to a value like `100ms/100ms`.
  The value is relative to one CPU worth of time, so to restrict to two CPUs worth of time, use something like `100ms/50ms` or `200ms/100ms`.
- When using a percentage value, the limit is a soft limit that is applied only when under load.
  It is used to calculate the scheduler priority for the instance, relative to any other instance that is using the same CPU or CPUs.
  For example, to limit the CPU usage of the container to one CPU when under load, set `limits.cpu.allowance` to `100%`.

`limits.cpu.nodes` can be used to restrict the CPUs that the instance can use to a specific set of NUMA nodes.
To specify which NUMA nodes to use, set `limits.cpu.nodes` to either a set of NUMA node IDs (for example, `0,1`) or a set of NUMA node ranges (for example, `0-1,2-4`).

`limits.cpu.priority` is another factor that is used to compute the scheduler priority score when a number of instances sharing a set of CPUs have the same percentage of CPU assigned to them.

(instance-options-limits-hugepages)=
### Huge page limits

Incus allows to limit the number of huge pages available to a container through the `limits.hugepage.[size]` key.

Architectures often expose multiple huge-page sizes.
The available huge-page sizes depend on the architecture.

Setting limits for huge pages is especially useful when Incus is configured to intercept the `mount` syscall for the `hugetlbfs` file system in unprivileged containers.
When Incus intercepts a `hugetlbfs` `mount` syscall, it mounts the `hugetlbfs` file system for a container with correct `uid` and `gid` values as mount options.
This makes it possible to use huge pages from unprivileged containers.
However, it is recommended to limit the number of huge pages available to the container through `limits.hugepages.[size]` to stop the container from being able to exhaust the huge pages available to the host.

Limiting huge pages is done through the `hugetlb` cgroup controller, which means that the host system must expose the `hugetlb` controller in the legacy or unified cgroup hierarchy for these limits to apply.

(instance-options-limits-kernel)=
### Kernel resource limits

Incus exposes a generic namespaced key `limits.kernel.*` that can be used to set resource limits for an instance.

It is generic in the sense that Incus does not perform any validation on the resource that is specified following the `limits.kernel.*` prefix.
Incus cannot know about all the possible resources that a given kernel supports.
Instead, Incus simply passes down the corresponding resource key after the `limits.kernel.*` prefix and its value to the kernel.
The kernel does the appropriate validation.
This allows users to specify any supported limit on their system.

Some common limits are:

Key                       | Resource          | Description
:--                       | :---              | :----------
`limits.kernel.as`        | `RLIMIT_AS`       | Maximum size of the process's virtual memory
`limits.kernel.core`      | `RLIMIT_CORE`     | Maximum size of the process's core dump file
`limits.kernel.cpu`       | `RLIMIT_CPU`      | Limit in seconds on the amount of CPU time the process can consume
`limits.kernel.data`      | `RLIMIT_DATA`     | Maximum size of the process's data segment
`limits.kernel.fsize`     | `RLIMIT_FSIZE`    | Maximum size of files the process may create
`limits.kernel.locks`     | `RLIMIT_LOCKS`    | Limit on the number of file locks that this process may establish
`limits.kernel.memlock`   | `RLIMIT_MEMLOCK`  | Limit on the number of bytes of memory that the process may lock in RAM
`limits.kernel.nice`      | `RLIMIT_NICE`     | Maximum value to which the process's nice value can be raised
`limits.kernel.nofile`    | `RLIMIT_NOFILE`   | Maximum number of open files for the process
`limits.kernel.nproc`     | `RLIMIT_NPROC`    | Maximum number of processes that can be created for the user of the calling process
`limits.kernel.rtprio`    | `RLIMIT_RTPRIO`   | Maximum value on the real-time-priority that may be set for this process
`limits.kernel.sigpending`| `RLIMIT_SIGPENDING` | Maximum number of signals that may be queued for the user of the calling process

A full list of all available limits can be found in the manpages for the `getrlimit(2)`/`setrlimit(2)` system calls.

To specify a limit within the `limits.kernel.*` namespace, use the resource name in lowercase without the `RLIMIT_` prefix.
For example, `RLIMIT_NOFILE` should be specified as `nofile`.

A limit is specified as two colon-separated values that are either numeric or the word `unlimited` (for example, `limits.kernel.nofile=1000:2000`).
A single value can be used as a shortcut to set both soft and hard limit to the same value (for example, `limits.kernel.nofile=3000`).

A resource with no explicitly configured limit will inherit its limit from the process that starts up the instance.
Note that this inheritance is not enforced by Incus but by the kernel.

(instance-options-migration)=
## Migration options

The following instance options control the behavior if the instance is {ref}`moved from one Incus server to another <move-instances>`:

% Include content from [../config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group instance-migration start -->
    :end-before: <!-- config group instance-migration end -->
```

(instance-options-nvidia)=
## NVIDIA and CUDA configuration

The following instance options specify the NVIDIA and CUDA configuration of the instance:

% Include content from [../config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group instance-nvidia start -->
    :end-before: <!-- config group instance-nvidia end -->
```

(instance-options-raw)=
## Raw instance configuration overrides

The following instance options allow direct interaction with the backend features that Incus itself uses:

% Include content from [../config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group instance-raw start -->
    :end-before: <!-- config group instance-raw end -->
```

```{important}
Setting these `raw.*` keys might break Incus in non-obvious ways.
Therefore, you should avoid setting any of these keys.
```

(instance-options-qemu)=
### Override QEMU configuration

For VM instances, Incus configures QEMU through a configuration file that is passed to QEMU with the `-readconfig` command-line option.
This configuration file is generated for each instance before boot.
It can be found at `/var/log/incus/<instance_name>/qemu.conf`.

The default configuration works fine for Incus' most common use case: modern UEFI guests with VirtIO devices.
In some situations, however, you might need to override the generated configuration.
For example:

- To run an old guest OS that doesn't support UEFI.
- To specify custom virtual devices when VirtIO is not supported by the guest OS.
- To add devices that are not supported by Incus before the machines boots.
- To remove devices that conflict with the guest OS.

To override the configuration, set the `raw.qemu.conf` option.
It supports a format similar to `qemu.conf`, with some additions.
Since it is a multi-line configuration option, you can use it to modify multiple sections or keys.

- To replace a section or key in the generated configuration file, add a section with a different value.

  For example, use the following section to override the default `virtio-gpu-pci` GPU driver:

  ```
  raw.qemu.conf: |-
      [device "qemu_gpu"]
      driver = "qxl-vga"
  ```

- To remove a section, specify a section without any keys.
  For example:

  ```
  raw.qemu.conf: |-
      [device "qemu_gpu"]
  ```

- To remove a key, specify an empty string as the value.
  For example:

  ```
  raw.qemu.conf: |-
      [device "qemu_gpu"]
      driver = ""
  ```

- To add a new section, specify a section name that is not present in the configuration file.

The configuration file format used by QEMU allows multiple sections with the same name.
Here's a piece of the configuration generated by Incus:

```
[global]
driver = "ICH9-LPC"
property = "disable_s3"
value = "1"

[global]
driver = "ICH9-LPC"
property = "disable_s4"
value = "1"
```

To specify which section to override, specify an index.
For example:

```
raw.qemu.conf: |-
    [global][1]
    value = "0"
```

Section indexes start at 0 (which is the default value when not specified), so the above example would generate the following configuration:

```
[global]
driver = "ICH9-LPC"
property = "disable_s3"
value = "1"

[global]
driver = "ICH9-LPC"
property = "disable_s4"
value = "0"
```

(instance-options-security)=
## Security policies

The following instance options control the {ref}`security` policies of the instance:

% Include content from [../config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group instance-security start -->
    :end-before: <!-- config group instance-security end -->
```

(instance-options-snapshots)=
## Snapshot scheduling and configuration

The following instance options control the creation and expiry of {ref}`instance snapshots <instances-snapshots>`:

% Include content from [../config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group instance-snapshots start -->
    :end-before: <!-- config group instance-snapshots end -->
```

(instance-options-snapshots-names)=
### Automatic snapshot names

{{snapshot_pattern_detail}}

(instance-options-volatile)=
## Volatile internal data

The following volatile keys are currently used internally by Incus to store internal data specific to an instance:

% Include content from [../config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group instance-volatile start -->
    :end-before: <!-- config group instance-volatile end --
```

```{note}
Volatile keys cannot be set by the user.
```
