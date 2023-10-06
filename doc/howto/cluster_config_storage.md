(cluster-config-storage)=
# How to configure storage for a cluster

All members of a cluster must have identical storage pools.
The only configuration keys that may differ between pools on different members are [`source`](storage-drivers), [`size`](storage-drivers), [`zfs.pool_name`](storage-zfs-pool-config), [`lvm.thinpool_name`](storage-lvm-pool-config) and [`lvm.vg_name`](storage-lvm-pool-config).
See {ref}`clustering-member-config` for more information.

Incus creates a default `local` storage pool for each cluster member during initialization.

Creating additional storage pools is a two-step process:

1. Define and configure the new storage pool across all cluster members.
   For example, for a cluster that has three members:

       incus storage create --target server1 data zfs source=/dev/vdb1
       incus storage create --target server2 data zfs source=/dev/vdc1
       incus storage create --target server3 data zfs source=/dev/vdb1 size=10GiB

   ```{note}
   You can pass only the member-specific configuration keys `source`, `size`, `zfs.pool_name`, `lvm.thinpool_name` and `lvm.vg_name`.
   Passing other configuration keys results in an error.
   ```

   These commands define the storage pool, but they don't create it.
   If you run [`incus storage list`](incus_storage_list.md), you can see that the pool is marked as "pending".
1. Run the following command to instantiate the storage pool on all cluster members:

       incus storage create data zfs

   ```{note}
   You can add configuration keys that are not member-specific to this command.
   ```

   If you missed a cluster member when defining the storage pool, or if a cluster member is down, you get an error.

Also see {ref}`storage-pools-cluster`.

## View member-specific pool configuration

Running [`incus storage show <pool_name>`](incus_storage_show.md) shows the cluster-wide configuration of the storage pool.

To view the member-specific configuration, use the `--target` flag.
For example:

    incus storage show data --target server2

## Create storage volumes

For most storage drivers (all except for Ceph-based storage drivers), storage volumes are not replicated across the cluster and exist only on the member for which they were created.
Run [`incus storage volume list <pool_name>`](incus_storage_volume_list.md) to see on which member a certain volume is located.

When creating a storage volume, use the `--target` flag to create a storage volume on a specific cluster member.
Without the flag, the volume is created on the cluster member on which you run the command.
For example, to create a volume on the current cluster member `server1`:

    incus storage volume create local vol1

To create a volume with the same name on another cluster member:

    incus storage volume create local vol1 --target server2

Different volumes can have the same name as long as they live on different cluster members.
Typical examples for this are image volumes.

You can manage storage volumes in a cluster in the same way as you do in non-clustered deployments, except that you must pass the `--target` flag to your commands if more than one cluster member has a volume with the given name.
For example, to show information about the storage volumes:

    incus storage volume show local vol1 --target server1
    incus storage volume show local vol1 --target server2
