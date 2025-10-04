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

(storage-lvmcluster)=
## `lvmcluster` driver in Incus

A second `lvmcluster` driver is available for use within clusters.

This relies on the `lvmlockd` and `sanlock` daemons to provide distributed locking over a shared disk or set of disks.

It allows using a remote shared block device like a `FiberChannel LUN`, `NVMEoF/NVMEoTCP` disk or `iSCSI` drive as the backing for a LVM storage pool.

```{note}
Thin provisioning is incompatible with clustered LVM, so expect higher disk usage.
```

To use this with Incus, you must:

- Have a shared block device available on all your cluster members
- Install the relevant packages for `lvm`, `lvmlockd` and `sanlock`
- Enable `lvmlockd` by setting `use_lvmlockd = 1` in your `/etc/lvm/lvm.conf`
- Set a unique (within your cluster) `host_id` value in `/etc/lvm/lvmlocal.conf`
- Ensure that both `lvmlockd` and `sanlock` daemons are running

## Configuration options

The following configuration options are available for storage pools that use the `lvm` driver and for storage volumes in these pools.

(storage-lvm-pool-config)=
### Storage pool configuration

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group storage_lvm-common start -->
    :end-before: <!-- config group storage_lvm-common end -->
```

{{volume_configuration}}

(storage-lvm-vol-config)=
### Storage volume configuration

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group storage_volume_lvm-common start -->
    :end-before: <!-- config group storage_volume_lvm-common end -->
```

[^*]: {{snapshot_pattern_detail}}

### Storage bucket configuration

To enable storage buckets for local storage pool drivers and allow applications to access the buckets via the S3 protocol, you must configure the {config:option}`server-core:core.storage_buckets_address` server setting.

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group storage_bucket_lvm-common start -->
    :end-before: <!-- config group storage_bucket_lvm-common end -->
```
