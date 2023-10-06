(howto-storage-volumes)=
# How to manage storage volumes

See the following sections for instructions on how to create, configure, view and resize {ref}`storage-volumes`.

## Create a custom storage volume

When you create an instance, Incus automatically creates a storage volume that is used as the root disk for the instance.

You can add custom storage volumes to your instances.
Such custom storage volumes are independent of the instance, which means that they can be backed up separately and are retained until you delete them.
Custom storage volumes with content type `filesystem` can also be shared between different instances.

See {ref}`storage-volumes` for detailed information.

### Create the volume

Use the following command to create a custom storage volume of type `block` or `filesystem` in a storage pool:

    incus storage volume create <pool_name> <volume_name> [configuration_options...]

See the {ref}`storage-drivers` documentation for a list of available storage volume configuration options for each driver.

By default, custom storage volumes use the `filesystem` {ref}`content type <storage-content-types>`.
To create a custom storage volume with the content type `block`, add the `--type` flag:

    incus storage volume create <pool_name> <volume_name> --type=block [configuration_options...]

To add a custom storage volume on a cluster member, add the `--target` flag:

    incus storage volume create <pool_name> <volume_name> --target=<cluster_member> [configuration_options...]

```{note}
For most storage drivers, custom storage volumes are not replicated across the cluster and exist only on the member for which they were created.
This behavior is different for Ceph-based storage pools (`ceph` and `cephfs`), where volumes are available from any cluster member.
```

To create a custom storage volume of type `iso`, use the `import` command instead of the `create` command:

    incus storage volume import <pool_name> <iso_path> <volume_name> --type=iso

(storage-attach-volume)=
### Attach the volume to an instance

After creating a custom storage volume, you can add it to one or more instances as a {ref}`disk device <devices-disk>`.

The following restrictions apply:

- Custom storage volumes of {ref}`content type <storage-content-types>` `block` or `iso` cannot be attached to containers, but only to virtual machines.
- To avoid data corruption, storage volumes of {ref}`content type <storage-content-types>` `block` should never be attached to more than one virtual machine at a time.
- Storage volumes of {ref}`content type <storage-content-types>` `iso` are always read-only, and can therefore be attached to more than one virtual machine at a time without corrupting data.
- File system storage volumes can't be attached to virtual machines while they're running.

For custom storage volumes with the content type `filesystem`, use the following command, where `<location>` is the path for accessing the storage volume inside the instance (for example, `/data`):

    incus storage volume attach <pool_name> <filesystem_volume_name> <instance_name> <location>

Custom storage volumes with the content type `block` do not take a location:

    incus storage volume attach <pool_name> <block_volume_name> <instance_name>

By default, the custom storage volume is added to the instance with the volume name as the {ref}`device <devices>` name.
If you want to use a different device name, you can add it to the command:

    incus storage volume attach <pool_name> <filesystem_volume_name> <instance_name> <device_name> <location>
    incus storage volume attach <pool_name> <block_volume_name> <instance_name> <device_name>

#### Attach the volume as a device

The [`incus storage volume attach`](incus_storage_volume_attach.md) command is a shortcut for adding a disk device to an instance.
Alternatively, you can add a disk device for the storage volume in the usual way:

    incus config device add <instance_name> <device_name> disk pool=<pool_name> source=<volume_name> [path=<location>]

When using this way, you can add further configuration to the command if needed.
See {ref}`disk device <devices-disk>` for all available device options.

(storage-configure-IO)=
#### Configure I/O limits

When you attach a storage volume to an instance as a {ref}`disk device <devices-disk>`, you can configure I/O limits for it.
To do so, set the `limits.read`, `limits.write` or `limits.max` properties to the corresponding limits.
See the {ref}`devices-disk` reference for more information.

The limits are applied through the Linux `blkio` cgroup controller, which makes it possible to restrict I/O at the disk level (but nothing finer grained than that).

```{note}
Because the limits apply to a whole physical disk rather than a partition or path, the following restrictions apply:

- Limits will not apply to file systems that are backed by virtual devices (for example, device mapper).
- If a file system is backed by multiple block devices, each device will get the same limit.
- If two disk devices that are backed by the same disk are attached to the same instance, the limits of the two devices will be averaged.
```

All I/O limits only apply to actual block device access.
Therefore, consider the file system's own overhead when setting limits.
Access to cached data is not affected by the limit.

(storage-volume-special)=
### Use the volume for backups or images

Instead of attaching a custom volume to an instance as a disk device, you can also use it as a special kind of volume to store {ref}`backups <backups>` or {ref}`images <about-images>`.

To do so, you must set the corresponding {ref}`server configuration <server-options-misc>`:

- To use a custom volume to store the backup tarballs:

      incus config set storage.backups_volume <pool_name>/<volume_name>

- To use a custom volume to store the image tarballs:

      incus config set storage.images_volume <pool_name>/<volume_name>

(storage-configure-volume)=
## Configure storage volume settings

See the {ref}`storage-drivers` documentation for the available configuration options for each storage driver.

Use the following command to set configuration options for a storage volume:

    incus storage volume set <pool_name> [<volume_type>/]<volume_name> <key> <value>

The default {ref}`storage volume type <storage-volume-types>` is `custom`, so you can leave out the `<volume_type>/` when configuring a custom storage volume.

For example, to set the size of your custom storage volume `my-volume` to 1 GiB, use the following command:

    incus storage volume set my-pool my-volume size=1GiB

To set the snapshot expiry time for your virtual machine `my-vm` to one month, use the following command:

    incus storage volume set my-pool virtual-machine/my-vm snapshots.expiry 1M

You can also edit the storage volume configuration by using the following command:

    incus storage volume edit <pool_name> [<volume_type>/]<volume_name>

(storage-configure-vol-default)=
### Configure default values for storage volumes

You can define default volume configurations for a storage pool.
To do so, set a storage pool configuration with a `volume` prefix, thus `volume.<VOLUME_CONFIGURATION>=<VALUE>`.

This value is then used for all new storage volumes in the pool, unless it is set explicitly for a volume or an instance.
In general, the defaults set on a storage pool level (before the volume was created) can be overridden through the volume configuration, and the volume configuration can be overridden through the instance configuration (for storage volumes of {ref}`type <storage-volume-types>` `container` or `virtual-machine`).

For example, to set a default volume size for a storage pool, use the following command:

    incus storage set [<remote>:]<pool_name> volume.size <value>

## View storage volumes

You can display a list of all available storage volumes in a storage pool and check their configuration.

To list all available storage volumes in a storage pool, use the following command:

    incus storage volume list <pool_name>

To display the storage volumes for all projects (not only the default project), add the `--all-projects` flag.

The resulting table contains the {ref}`storage volume type <storage-volume-types>` and the {ref}`content type <storage-content-types>` for each storage volume in the pool.

```{note}
Custom storage volumes might use the same name as instance volumes (for example, you might have a container named `c1` with a container storage volume named `c1` and a custom storage volume named `c1`).
Therefore, to distinguish between instance storage volumes and custom storage volumes, all instance storage volumes must be referred to as `<volume_type>/<volume_name>` (for example, `container/c1` or `virtual-machine/vm`) in commands.
```

To show detailed configuration information about a specific volume, use the following command:

    incus storage volume show <pool_name> [<volume_type>/]<volume_name>

To show state information about a specific volume, use the following command:

    incus storage volume info <pool_name> [<volume_type>/]<volume_name>

In both commands, the default {ref}`storage volume type <storage-volume-types>` is `custom`, so you can leave out the `<volume_type>/` when displaying information about a custom storage volume.

## Resize a storage volume

If you need more storage in a volume, you can increase the size of your storage volume.
In some cases, it is also possible to reduce the size of a storage volume.

To resize a storage volume, set its size configuration:

    incus storage volume set <pool_name> <volume_name> size <new_size>

```{important}
- Growing a storage volume usually works (if the storage pool has sufficient storage).
- Shrinking a storage volume is only possible for storage volumes with content type `filesystem`.
  It is not guaranteed to work though, because you cannot shrink storage below its current used size.
- Shrinking a storage volume with content type `block` is not possible.

```
