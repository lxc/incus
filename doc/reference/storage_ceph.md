(storage-ceph)=
# Ceph RBD - `ceph`

<!-- Include start Ceph intro -->
[Ceph](https://ceph.io/en/) is an open-source storage platform that stores its data in a storage cluster based on {abbr}`RADOS (Reliable Autonomic Distributed Object Store)`.
It is highly scalable and, as a distributed system without a single point of failure, very reliable.

```{tip}
If you want to quickly set up a basic Ceph cluster, check out [MicroCeph](https://microcloud.is).
```

Ceph provides different components for block storage and for file systems.
<!-- Include end Ceph intro -->

Ceph {abbr}`RBD (RADOS Block Device)` is Ceph's block storage component that distributes data and workload across the Ceph cluster.
It uses thin provisioning, which means that it is possible to over-commit resources.

## Terminology

<!-- Include start Ceph terminology -->
Ceph uses the term *object* for the data that it stores.
The daemon that is responsible for storing and managing data is the *Ceph {abbr}`OSD (Object Storage Daemon)`*.
Ceph's storage is divided into *pools*, which are logical partitions for storing objects.
They are also referred to as *data pools*, *storage pools* or *OSD pools*.
<!-- Include end Ceph terminology -->

Ceph block devices are also called *RBD images*, and you can create *snapshots* and *clones* of these RBD images.

## `ceph` driver in Incus

```{note}
To use the Ceph RBD driver, you must specify it as `ceph`.
This is slightly misleading, because it uses only Ceph RBD (block storage) functionality, not full Ceph functionality.
For storage volumes with content type `filesystem` (images, containers and custom file-system volumes), the `ceph` driver uses Ceph RBD images with a file system on top (see [`block.filesystem`](storage-ceph-vol-config)).

Alternatively, you can use the {ref}`CephFS <storage-cephfs>` driver to create storage volumes with content type `filesystem`.
```

<!-- Include start Ceph driver cluster -->
Unlike other storage drivers, this driver does not set up the storage system but assumes that you already have a Ceph cluster installed.
<!-- Include end Ceph driver cluster -->

<!-- Include start Ceph driver remote -->
This driver also behaves differently than other drivers in that it provides remote storage.
As a result and depending on the internal network, storage access might be a bit slower than for local storage.
On the other hand, using remote storage has big advantages in a cluster setup, because all cluster members have access to the same storage pools with the exact same contents, without the need to synchronize storage pools.
<!-- Include end Ceph driver remote -->

The `ceph` driver in Incus uses RBD images for images, and snapshots and clones to create instances and snapshots.

<!-- Include start Ceph driver control -->
Incus assumes that it has full control over the OSD storage pool.
Therefore, you should never maintain any file system entities that are not owned by Incus in a Incus OSD storage pool, because Incus might delete them.
<!-- Include end Ceph driver control -->

Due to the way copy-on-write works in Ceph RBD, parent RBD images can't be removed until all children are gone.
As a result, Incus automatically renames any objects that are removed but still referenced.
Such objects are kept with a  `zombie_` prefix until all references are gone and the object can safely be removed.

### Limitations

The `ceph` driver has the following limitations:

Sharing custom volumes between instances
: Custom storage volumes with {ref}`content type <storage-content-types>` `filesystem` can usually be shared between multiple instances different cluster members.
  However, because the Ceph RBD driver "simulates" volumes with content type `filesystem` by putting a file system on top of an RBD image, custom storage volumes can only be assigned to a single instance at a time.
  If you need to share a custom volume with content type `filesystem`, use the {ref}`CephFS <storage-cephfs>` driver instead.

Sharing the OSD storage pool between installations
: Sharing the same OSD storage pool between multiple Incus installations is not supported.

Using an OSD pool of type "erasure"
: To use a Ceph OSD pool of type "erasure", you must create the OSD pool beforehand.
  You must also create a separate OSD pool of type "replicated" that will be used for storing metadata.
  This is required because Ceph RBD does not support `omap`.
  To specify which pool is "erasure coded", set the [`ceph.osd.data_pool_name`](storage-ceph-pool-config) configuration option to the erasure coded pool name and the [`source`](storage-ceph-pool-config) configuration option to the replicated pool name.

## Configuration options

The following configuration options are available for storage pools that use the `ceph` driver and for storage volumes in these pools.

(storage-ceph-pool-config)=
### Storage pool configuration

Key                           | Type                          | Default                                 | Description
:--                           | :---                          | :------                                 | :----------
`ceph.cluster_name`           | string                        | `ceph`                                  | Name of the Ceph cluster in which to create new storage pools
`ceph.osd.data_pool_name`     | string                        | -                                       | Name of the OSD data pool
`ceph.osd.pg_num`             | string                        | `32`                                    | Number of placement groups for the OSD storage pool
`ceph.osd.pool_name`          | string                        | name of the pool                        | Name of the OSD storage pool
`ceph.rbd.clone_copy`         | bool                          | `true`                                  | Whether to use RBD lightweight clones rather than full dataset copies
`ceph.rbd.du`                 | bool                          | `true`                                  | Whether to use RBD `du` to obtain disk usage data for stopped instances
`ceph.rbd.features`           | string                        | `layering`                              | Comma-separated list of RBD features to enable on the volumes
`ceph.user.name`              | string                        | `admin`                                 | The Ceph user to use when creating storage pools and volumes
`source`                      | string                        | -                                       | Existing OSD storage pool to use
`volatile.pool.pristine`      | string                        | `true`                                  | Whether the pool was empty on creation time

{{volume_configuration}}

(storage-ceph-vol-config)=
### Storage volume configuration

Key                     | Type      | Condition                 | Default                                        | Description
:--                     | :---      | :--------                 | :------                                        | :----------
`block.filesystem`      | string    | block-based volume with content type `filesystem` | same as `volume.block.filesystem`              | {{block_filesystem}}
`block.mount_options`   | string    | block-based volume with content type `filesystem` | same as `volume.block.mount_options`           | Mount options for block-backed file system volumes
`security.shifted`      | bool      | custom volume             | same as `volume.security.shifted` or `false`   | {{enable_ID_shifting}}
`security.unmapped`     | bool      | custom volume             | same as `volume.security.unmapped` or `false`  | Disable ID mapping for the volume
`size`                  | string    |                           | same as `volume.size`                          | Size/quota of the storage volume
`snapshots.expiry`      | string    | custom volume             | same as `volume.snapshots.expiry`              | {{snapshot_expiry_format}}
`snapshots.pattern`     | string    | custom volume             | same as `volume.snapshots.pattern` or `snap%d` | {{snapshot_pattern_format}} [^*]
`snapshots.schedule`    | string    | custom volume             | same as `volume.snapshots.schedule`            | {{snapshot_schedule_format}}

[^*]: {{snapshot_pattern_detail}}
