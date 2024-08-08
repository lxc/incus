(devices-disk)=
# Type: `disk`

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

      incus config device add <instance_name> <device_name> disk pool=<pool_name> source=<volume_name> [path=<path_in_instance>]

  The path is required for file system volumes, but not for block volumes.

  Alternatively, you can use the [`incus storage volume attach`](incus_storage_volume_attach.md) command to {ref}`storage-attach-volume`.
  Both commands use the same mechanism to add a storage volume as a disk device.

  It's possible to attach a sub-path of a custom volume to an instance using the `source=<volume_name>/<sub_path>` syntax.

Path on the host
: You can share a path on your host (either a file system or a block device) to your instance by adding it as a disk device with the host path as the `source`:

      incus config device add <instance_name> <device_name> disk source=<path_on_host> [path=<path_in_instance>]

  The path is required for file systems, but not for block devices.

Ceph RBD
: Incus can use Ceph to manage an internal file system for the instance, but if you have an existing, externally managed Ceph RBD that you would like to use for an instance, you can add it with the following command:

      incus config device add <instance_name> <device_name> disk source=ceph:<pool_name>/<volume_name> ceph.user_name=<user_name> ceph.cluster_name=<cluster_name> [path=<path_in_instance>]

  The path is required for file systems, but not for block devices.

CephFS
: Incus can use Ceph to manage an internal file system for the instance, but if you have an existing, externally managed Ceph file system that you would like to use for an instance, you can add it with the following command:

      incus config device add <instance_name> <device_name> disk source=cephfs:<fs_name>/<path> ceph.user_name=<user_name> ceph.cluster_name=<cluster_name> path=<path_in_instance>

ISO file
: You can add an ISO file as a disk device for a virtual machine.
  It is added as a ROM device inside the VM.

  This source type is applicable only to VMs.

  To add an ISO file, specify its file path as the `source`:

      incus config device add <instance_name> <device_name> disk source=<file_path_on_host>

VM `cloud-init`
: You can generate a `cloud-init` configuration ISO from the {config:option}`instance-cloud-init:cloud-init.vendor-data` and {config:option}`instance-cloud-init:cloud-init.user-data` configuration keys and attach it to a virtual machine.
  The `cloud-init` that is running inside the VM then detects the drive on boot and applies the configuration.

  This source type is applicable only to VMs.

  To add such a device, use the following command:

      incus config device add <instance_name> <device_name> disk source=cloud-init:config

VM `agent`
: You can generate an `agent` configuration ISO which will contain the agent binary, configuration files and installation scripts.
  This is required for environments where `9p` isn't supported and where an alternative way to load the agent is required.

  This source type is applicable only to VMs.

  To add such a device, use the following command:

      incus config device add <instance_name> <device_name> disk source=agent:config

(devices-disk-initial-config)=
## Initial volume configuration for instance root disk devices

Initial volume configuration allows setting specific configurations for the root disk devices of new instances.
These settings are prefixed with `initial.` and are only applied when the instance is created.
This method allows creating instances that have unique configurations, independent of the default storage pool settings.

For example, you can add an initial volume configuration for `zfs.block_mode` to an existing profile, and this
will then take effect for each new instance you create using this profile:

    incus profile device set <profile_name> <device_name> initial.zfs.block_mode=true

You can also set an initial configuration directly when creating an instance. For example:

    incus init <image> <instance_name> --device <device_name>,initial.zfs.block_mode=true

Note that you cannot use initial volume configurations with custom volume options or to set the volume's size.

## Device options

`disk` devices have the following device options:

% Include content from [../config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group devices-disk start -->
    :end-before: <!-- config group devices-disk end -->
```
