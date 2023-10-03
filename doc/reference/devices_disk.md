(devices-disk)=
# Type: `disk`

```{youtube} https://www.youtube.com/watch?v=JhRw2OYTgtg
```

```{note}
The `disk` device type is supported for both containers and VMs.
It supports hotplugging for both containers and VMs.
```

Disk devices supply additional storage to instances.

For containers, they are essentially mount points inside the instance (either as a bind-mount of an existing file or directory on the host, or, if the source is a block device, a regular mount).
Virtual machines share host-side mounts or directories through `9p` or `virtiofs` (if available), or as VirtIO disks for block-based disks.

(devices-disk-types)=
## Types of disk devices

You can create disk devices from different sources.
The value that you specify for the `source` option specifies the type of disk device that is added:

Storage volume
: The most common type of disk device is a storage volume.
  To add a storage volume, specify its name as the `source` of the device:

      lxc config device add <instance_name> <device_name> disk pool=<pool_name> source=<volume_name> [path=<path_in_instance>]

  The path is required for file system volumes, but not for block volumes.

  Alternatively, you can use the `lxc storage volume attach` command to {ref}`storage-attach-volume`.
  Both commands use the same mechanism to add a storage volume as a disk device.

Path on the host
: You can share a path on your host (either a file system or a block device) to your instance by adding it as a disk device with the host path as the `source`:

      lxc config device add <instance_name> <device_name> disk source=<path_on_host> [path=<path_in_instance>]

  The path is required for file systems, but not for block devices.

Ceph RBD
: LXD can use Ceph to manage an internal file system for the instance, but if you have an existing, externally managed Ceph RBD that you would like to use for an instance, you can add it with the following command:

      lxc config device add <instance_name> <device_name> disk source=ceph:<pool_name>/<volume_name> ceph.user_name=<user_name> ceph.cluster_name=<cluster_name> [path=<path_in_instance>]

  The path is required for file systems, but not for block devices.

CephFS
: LXD can use Ceph to manage an internal file system for the instance, but if you have an existing, externally managed Ceph file system that you would like to use for an instance, you can add it with the following command:

      lxc config device add <instance_name> <device_name> disk source=cephfs:<fs_name>/<path> ceph.user_name=<user_name> ceph.cluster_name=<cluster_name> path=<path_in_instance>

ISO file
: You can add an ISO file as a disk device for a virtual machine.
  It is added as a ROM device inside the VM.

  This source type is applicable only to VMs.

  To add an ISO file, specify its file path as the `source`:

      lxc config device add <instance_name> <device_name> disk source=<file_path_on_host>

VM `cloud-init`
: You can generate a `cloud-init` configuration ISO from the {config:option}`instance-cloud-init:cloud-init.vendor-data` and {config:option}`instance-cloud-init:cloud-init.user-data` configuration keys and attach it to a virtual machine.
  The `cloud-init` that is running inside the VM then detects the drive on boot and applies the configuration.

  This source type is applicable only to VMs.

  To add such a device, use the following command:

      lxc config device add <instance_name> <device_name> disk source=cloud-init:config

## Device options

`disk` devices have the following device options:

Key                 | Type      | Default   | Required  | Description
:--                 | :--       | :--       | :--       | :--
`boot.priority`     | integer   | -         | no        | Boot priority for VMs (higher value boots first)
`ceph.cluster_name` | string    | `ceph`    | no        | The cluster name of the Ceph cluster (required for Ceph or CephFS sources)
`ceph.user_name`    | string    | `admin`   | no        | The user name of the Ceph cluster (required for Ceph or CephFS sources)
`io.cache`          | string    | `none`    | no        | Only for VMs: Override the caching mode for the device (`none`, `writeback` or `unsafe`)
`limits.max`        | string    | -         | no        | I/O limit in byte/s or IOPS for both read and write (same as setting both `limits.read` and `limits.write`)
`limits.read`       | string    | -         | no        | I/O limit in byte/s (various suffixes supported, see {ref}`instances-limit-units`) or in IOPS (must be suffixed with `iops`) - see also {ref}`storage-configure-IO`
`limits.write`      | string    | -         | no        | I/O limit in byte/s (various suffixes supported, see {ref}`instances-limit-units`) or in IOPS (must be suffixed with `iops`) - see also {ref}`storage-configure-IO`
`path`              | string    | -         | yes       | Path inside the instance where the disk will be mounted (only for containers)
`pool`              | string    | -         | no        | The storage pool to which the disk device belongs (only applicable for storage volumes managed by LXD)
`propagation`       | string    | -         | no        | Controls how a bind-mount is shared between the instance and the host (can be one of `private`, the default, or `shared`, `slave`, `unbindable`,  `rshared`, `rslave`, `runbindable`,  `rprivate`; see the Linux Kernel [shared subtree](https://www.kernel.org/doc/Documentation/filesystems/sharedsubtree.txt) documentation for a full explanation) <!-- wokeignore:rule=slave -->
`raw.mount.options` | string    | -         | no        | File system specific mount options
`readonly`          | bool      | `false`   | no        | Controls whether to make the mount read-only
`recursive`         | bool      | `false`   | no        | Controls whether to recursively mount the source path
`required`          | bool      | `true`    | no        | Controls whether to fail if the source doesn't exist
`shift`             | bool      | `false`   | no        | Sets up a shifting overlay to translate the source UID/GID to match the instance (only for containers)
`size`              | string    | -         | no        | Disk size in bytes (various suffixes supported, see {ref}`instances-limit-units`) - only supported for the `rootfs` (`/`)
`size.state`        | string    | -         | no        | Same as `size`, but applies to the file-system volume used for saving runtime state in VMs
`source`            | string    | -         | yes       | Source of a file system or block device (see {ref}`devices-disk-types` for details)
