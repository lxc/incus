(storage-btrfs)=
# Btrfs - `btrfs`

{abbr}`Btrfs (B-tree file system)` is a local file system based on the {abbr}`COW (copy-on-write)` principle.
COW means that data is stored to a different block after it has been modified instead of overwriting the existing data, reducing the risk of data corruption.
Unlike other file systems, Btrfs is extent-based, which means that it stores data in contiguous areas of memory.

In addition to basic file system features, Btrfs offers RAID and volume management, pooling, snapshots, checksums, compression and other features.

To use Btrfs, make sure you have `btrfs-progs` installed on your machine.

## Terminology

A Btrfs file system can have *subvolumes*, which are named binary subtrees of the main tree of the file system with their own independent file and directory hierarchy.
A *Btrfs snapshot* is a special type of subvolume that captures a specific state of another subvolume.
Snapshots can be read-write or read-only.

## `btrfs` driver in Incus

The `btrfs` driver in Incus uses a subvolume per instance, image and snapshot.
When creating a new entity (for example, launching a new instance), it creates a Btrfs snapshot.

Btrfs doesn't natively support storing block devices.
Therefore, when using Btrfs for VMs, Incus creates a big file on disk to store the VM.
This approach is not very efficient and might cause issues when creating snapshots.

Btrfs can be used as a storage backend inside a container in a nested Incus environment.
In this case, the parent container itself must use Btrfs.
Note, however, that the nested Incus setup does not inherit the Btrfs quotas from the parent (see {ref}`storage-btrfs-quotas` below).

(storage-btrfs-quotas)=
### Quotas

Btrfs supports storage quotas via qgroups.
Btrfs qgroups are hierarchical, but new subvolumes will not automatically be added to the qgroups of their parent subvolumes.
This means that users can trivially escape any quotas that are set.
Therefore, if strict quotas are needed, you should consider using a different storage driver (for example, ZFS with `refquota` or LVM with Btrfs on top).

When using quotas, you must take into account that Btrfs extents are immutable.
When blocks are written, they end up in new extents.
The old extents remain until all their data is dereferenced or rewritten.
This means that a quota can be reached even if the total amount of space used by the current files in the subvolume is smaller than the quota.

```{note}
This issue is seen most often when using VMs on Btrfs, due to the random I/O nature of using raw disk image files on top of a Btrfs subvolume.

Therefore, you should never use VMs with Btrfs storage pools.

If you really need to use VMs with Btrfs storage pools, set the instance root disk's [`size.state`](devices-disk) property to twice the size of the root disk's size.
This configuration allows all blocks in the disk image file to be rewritten without reaching the qgroup quota.
The [`btrfs.mount_options=compress-force`](storage-btrfs-pool-config) storage pool option can also avoid this scenario, because a side effect of enabling compression is to reduce the maximum extent size such that block rewrites don't cause as much storage to be double-tracked.
However, this is a storage pool option, and it therefore affects all volumes on the pool.
```

## Configuration options

The following configuration options are available for storage pools that use the `btrfs` driver and for storage volumes in these pools.

(storage-btrfs-pool-config)=
### Storage pool configuration

Key                             | Type      | Default                    | Description
:--                             | :---      | :------                    | :----------
`btrfs.mount_options`           | string    | `user_subvol_rm_allowed`   | Mount options for block devices
`size`                          | string    | auto (20% of free disk space, >= 5 GiB and <= 30 GiB) | Size of the storage pool when creating loop-based pools (in bytes, suffixes supported, can be increased to grow storage pool)
`source`                        | string    | -                          | Path to an existing block device, loop file or Btrfs subvolume
`source.wipe`                   | bool      | `false`                    | Wipe the block device specified in `source` prior to creating the storage pool

{{volume_configuration}}

### Storage volume configuration

Key                     | Type      | Condition                 | Default                                       | Description
:--                     | :---      | :--------                 | :------                                       | :----------
`security.shifted`      | bool      | custom volume             | same as `volume.security.shifted` or `false`  | {{enable_ID_shifting}}
`security.unmapped`     | bool      | custom volume             | same as `volume.security.unmapped` or `false` | Disable ID mapping for the volume
`size`                  | string    | appropriate driver        | same as `volume.size`                         | Size/quota of the storage volume
`snapshots.expiry`      | string    | custom volume             | same as `volume.snapshots.expiry`             | {{snapshot_expiry_format}}
`snapshots.pattern`     | string    | custom volume             | same as `volume.snapshots.pattern` or `snap%d`| {{snapshot_pattern_format}} [^*]
`snapshots.schedule`    | string    | custom volume             | same as `volume.snapshots.schedule`           | {{snapshot_schedule_format}}

[^*]: {{snapshot_pattern_detail}}

### Storage bucket configuration

To enable storage buckets for local storage pool drivers and allow applications to access the buckets via the S3 protocol, you must configure the {config:option}`server-core:core.storage_buckets_address` server setting.

Key                     | Type      | Condition                 | Default                                        | Description
:--                     | :---      | :--------                 | :------                                        | :----------
`size`                  | string    | appropriate driver        | same as `volume.size`                          | Size/quota of the storage bucket
