(storage-drivers)=
# Storage drivers

Incus supports the following storage drivers for storing images, instances and custom volumes:

```{toctree}
:maxdepth: 1

storage_dir
storage_btrfs
storage_lvm
storage_zfs
storage_ceph
storage_cephfs
storage_cephobject
```

See the corresponding pages for driver-specific information and configuration options.

(storage-drivers-features)=
## Feature comparison

Where possible, Incus uses the advanced features of each storage system to optimize operations.

Feature                                     | Directory | Btrfs | LVM   | ZFS     | Ceph RBD | CephFS | Ceph Object
:---                                        | :---      | :---  | :---  | :---    | :---     | :---   | :---
{ref}`storage-optimized-image-storage`      | no        | yes   | yes   | yes     | yes      | n/a    | n/a
Optimized instance creation                 | no        | yes   | yes   | yes     | yes      | n/a    | n/a
Optimized snapshot creation                 | no        | yes   | yes   | yes     | yes      | yes    | n/a
Optimized image transfer                    | no        | yes   | no    | yes     | yes      | n/a    | n/a
{ref}`storage-optimized-volume-transfer`    | no        | yes   | no    | yes     | yes      | n/a    | n/a
Copy on write                               | no        | yes   | yes   | yes     | yes      | yes    | n/a
Block based                                 | no        | no    | yes   | no      | yes      | no     | n/a
Instant cloning                             | no        | yes   | yes   | yes     | yes      | yes    | n/a
Storage driver usable inside a container    | yes       | yes   | no    | yes[^1] | no       | n/a    | n/a
Restore from older snapshots (not latest)   | yes       | yes   | yes   | no      | yes      | yes    | n/a
Storage quotas                              | yes[^2]   | yes   | yes   | yes     | yes      | yes    | yes
Available on `incus admin init`                     | yes       | yes   | yes   | yes     | yes      | no     | no
Object storage                              | yes       | yes   | yes   | yes     | no       | no     | yes

[^1]: Requires [`zfs.delegate`](storage-zfs-vol-config) to be enabled.
[^2]: % Include content from [storage_dir.md](storage_dir.md)

      ```{include} storage_dir.md
         :start-after: <!-- Include start dir quotas -->
         :end-before: <!-- Include end dir quotas -->
      ```

(storage-optimized-image-storage)=
### Optimized image storage

All storage drivers except for the directory driver have some kind of optimized image storage format.
To make instance creation near instantaneous, Incus clones a pre-made image volume when creating an instance rather than unpacking the image tarball from scratch.

To prevent preparing such a volume on a storage pool that might never be used with that image, the volume is generated on demand.
Therefore, the first instance takes longer to create than subsequent ones.

(storage-optimized-volume-transfer)=
### Optimized volume transfer

Btrfs, ZFS and Ceph RBD have an internal send/receive mechanism that allows for optimized volume transfer.

Incus uses this optimized transfer when transferring instances and snapshots between storage pools that use the same storage driver, if the storage driver supports optimized transfer and the optimized transfer is actually quicker.
Otherwise, Incus uses `rsync` to transfer container and file system volumes, or raw block transfer to transfer virtual machine and custom block volumes.

The optimized transfer uses the underlying storage driver's native functionality for transferring data, which is usually faster than using `rsync`.
However, the full potential of the optimized transfer becomes apparent when refreshing a copy of an instance or custom volume that uses periodic snapshots.
With optimized transfer, Incus bases the refresh on the latest snapshot, which means:

- When you take a first snapshot and refresh the copy, the transfer will take roughly the same time as a full copy.
  Incus transfers the new snapshot and the difference between the snapshot and the main volume.
- For subsequent snapshots, the transfer is considerably faster.
  Incus does not transfer the full new snapshot, but only the difference between the new snapshot and the latest snapshot that already exists on the target.
- When refreshing without a new snapshot, Incus transfers only the differences between the main volume and the latest snapshot on the target.
  This transfer is usually faster than using `rsync` (as long as the latest snapshot is not too outdated).

On the other hand, refreshing copies of instances without snapshots (either because the instance doesn't have any snapshots or because the refresh uses the `--instance-only` flag) would actually be slower than using `rsync`.
In such cases, the optimized transfer would transfer the difference between the (non-existent) latest snapshot and the main volume, thus the full volume.
Therefore, Incus uses `rsync` instead of the optimized transfer for refreshes without snapshots.

## Recommended setup

The two best options for use with Incus are ZFS and Btrfs.
They have similar functionalities, but ZFS is more reliable.

Whenever possible, you should dedicate a full disk or partition to your Incus storage pool.
Incus allows to create loop-based storage, but this isn't recommended for production use.
See {ref}`storage-location` for more information.

The directory backend should be considered as a last resort option.
It supports all main Incus features, but is slow and inefficient because it cannot perform instant copies or snapshots.
Therefore, it constantly copies the instance's full storage.

## Security considerations

Currently, the Linux kernel might silently ignore mount options and not apply them when a block-based file system (for example, `ext4`) is already mounted with different mount options.
This means when dedicated disk devices are shared between different storage pools with different mount options set, the second mount might not have the expected mount options.
This becomes security relevant when, for example, one storage pool is supposed to provide `acl` support and the second one is supposed to not provide `acl` support.

For this reason, it is currently recommended to either have dedicated disk devices per storage pool or to ensure that all storage pools that share the same dedicated disk device use the same mount options.
