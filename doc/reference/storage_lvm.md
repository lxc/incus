(storage-lvm)=
# LVM - `lvm`

{abbr}`LVM (Logical Volume Manager)` is a storage management framework rather than a file system.
It is used to manage physical storage devices, allowing you to create a number of logical storage volumes that use and virtualize the underlying physical storage devices.

Note that it is possible to over-commit the physical storage in the process, to allow flexibility for scenarios where not all available storage is in use at the same time.

To use LVM, make sure you have `lvm2` installed on your machine.

## Terminology

LVM can combine several physical storage devices into a *volume group*.
You can then allocate *logical volumes* of different types from this volume group.

One supported volume type is a *thin pool*, which allows over-committing the resources by creating  thinly provisioned volumes whose total allowed maximum size is larger than the available physical storage.
Another type is a *volume snapshot*, which captures a specific state of a logical volume.

## `lvm` driver in Incus

The `lvm` driver in Incus uses logical volumes for images, and volume snapshots for instances and snapshots.

Incus assumes that it has full control over the volume group.
Therefore, you should not maintain any file system entities that are not owned by Incus in an LVM volume group, because Incus might delete them.
However, if you need to reuse an existing volume group (for example, because your setup has only one volume group), you can do so by setting the [`lvm.vg.force_reuse`](storage-lvm-pool-config) configuration.

By default, LVM storage pools use an LVM thin pool and create logical volumes for all Incus storage entities (images, instances and custom volumes) in there.
This behavior can be changed by setting [`lvm.use_thinpool`](storage-lvm-pool-config) to `false` when you create the pool.
In this case, Incus uses "normal" logical volumes for all storage entities that are not snapshots.
Note that this entails serious performance and space reductions for the `lvm` driver (close to the `dir` driver both in speed and storage usage).
The reason for this is that most storage operations must fall back to using `rsync`, because logical volumes that are not thin pools do not support snapshots of snapshots.
In addition, non-thin snapshots take up much more storage space than thin snapshots, because they must reserve space for their maximum size at creation time.
Therefore, this option should only be chosen if the use case requires it.

For environments with a high instance turnover (for example, continuous integration) you should tweak the backup `retain_min` and `retain_days` settings in `/etc/lvm/lvm.conf` to avoid slowdowns when interacting with Incus.

## Configuration options

The following configuration options are available for storage pools that use the `lvm` driver and for storage volumes in these pools.

(storage-lvm-pool-config)=
### Storage pool configuration

Key                           | Type                          | Default                                 | Description
:--                           | :---                          | :------                                 | :----------
`lvm.thinpool_name`           | string                        | `IncusThinPool`                           | Thin pool where volumes are created
`lvm.thinpool_metadata_size`  | string                        | `0` (auto)                              | The size of the thin pool metadata volume (the default is to let LVM calculate an appropriate size)
`lvm.use_thinpool`            | bool                          | `true`                                  | Whether the storage pool uses a thin pool for logical volumes
`lvm.vg.force_reuse`          | bool                          | `false`                                 | Force using an existing non-empty volume group
`lvm.vg_name`                 | string                        | name of the pool                        | Name of the volume group to create
`rsync.bwlimit`               | string                        | `0` (no limit)                          | The upper limit to be placed on the socket I/O when `rsync` must be used to transfer storage entities
`rsync.compression`           | bool                          | `true`                                  | Whether to use compression while migrating storage pools
`size`                        | string                        | auto (20% of free disk space, >= 5 GiB and <= 30 GiB) | Size of the storage pool when creating loop-based pools (in bytes, suffixes supported, can be increased to grow storage pool)
`source`                      | string                        | -                                       | Path to an existing block device, loop file or LVM volume group
`source.wipe`                 | bool                          | `false`                                 | Wipe the block device specified in `source` prior to creating the storage pool

{{volume_configuration}}

(storage-lvm-vol-config)=
### Storage volume configuration

Key                     | Type      | Condition     | Default                                        | Description
:--                     | :---      | :------       | :------                                        | :----------
`block.filesystem`      | string    | block-based volume with content type `filesystem` | same as `volume.block.filesystem`              | {{block_filesystem}}
`block.mount_options`   | string    | block-based volume with content type `filesystem` | same as `volume.block.mount_options`           | Mount options for block-backed file system volumes
`lvm.stripes`           | string    |               | same as `volume.lvm.stripes`                   | Number of stripes to use for new volumes (or thin pool volume)
`lvm.stripes.size`      | string    |               | same as `volume.lvm.stripes.size`              | Size of stripes to use (at least 4096 bytes and multiple of 512 bytes)
`security.shifted`      | bool      | custom volume | same as `volume.security.shifted` or `false`   | {{enable_ID_shifting}}
`security.unmapped`     | bool      | custom volume | same as `volume.security.unmapped` or `false`  | Disable ID mapping for the volume
`size`                  | string    |               | same as `volume.size`                          | Size/quota of the storage volume
`snapshots.expiry`      | string    | custom volume | same as `volume.snapshots.expiry`              | {{snapshot_expiry_format}}
`snapshots.pattern`     | string    | custom volume | same as `volume.snapshots.pattern` or `snap%d` | {{snapshot_pattern_format}} [^*]
`snapshots.schedule`    | string    | custom volume | same as `volume.snapshots.schedule`            | {{snapshot_schedule_format}}

[^*]: {{snapshot_pattern_detail}}

### Storage bucket configuration

To enable storage buckets for local storage pool drivers and allow applications to access the buckets via the S3 protocol, you must configure the {config:option}`server-core:core.storage_buckets_address` server setting.

Key                     | Type      | Condition                 | Default                                        | Description
:--                     | :---      | :--------                 | :------                                        | :----------
`size`                  | string    | appropriate driver        | same as `volume.size`                          | Size/quota of the storage bucket
