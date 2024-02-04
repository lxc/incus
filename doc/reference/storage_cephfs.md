(storage-cephfs)=
# CephFS - `cephfs`

% Include content from [storage_ceph.md](storage_ceph.md)
```{include} storage_ceph.md
    :start-after: <!-- Include start Ceph intro -->
    :end-before: <!-- Include end Ceph intro -->
```

{abbr}`CephFS (Ceph File System)` is Ceph's file system component that provides a robust, fully-featured POSIX-compliant distributed file system.
Internally, it maps files to Ceph objects and stores file metadata (for example, file ownership, directory paths, access permissions) in a separate data pool.

## Terminology

% Include content from [storage_ceph.md](storage_ceph.md)
```{include} storage_ceph.md
    :start-after: <!-- Include start Ceph terminology -->
    :end-before: <!-- Include end Ceph terminology -->
```

A *CephFS file system* consists of two OSD storage pools, one for the actual data and one for the file metadata.

## `cephfs` driver in Incus

```{note}
The `cephfs` driver can only be used for custom storage volumes with content type `filesystem`.

For other storage volumes, use the {ref}`Ceph <storage-ceph>` driver.
That driver can also be used for custom storage volumes with content type `filesystem`, but it implements them through Ceph RBD images.
```

% Include content from [storage_ceph.md](storage_ceph.md)
```{include} storage_ceph.md
    :start-after: <!-- Include start Ceph driver cluster -->
    :end-before: <!-- Include end Ceph driver cluster -->
```

You can either create the CephFS file system that you want to use beforehand and specify it through the [`source`](storage-cephfs-pool-config) option, or specify the [`cephfs.create_missing`](storage-cephfs-pool-config) option to automatically create the file system and the data and metadata OSD pools (with the names given in [`cephfs.data_pool`](storage-cephfs-pool-config) and [`cephfs.meta_pool`](storage-cephfs-pool-config)).

% Include content from [storage_ceph.md](storage_ceph.md)
```{include} storage_ceph.md
    :start-after: <!-- Include start Ceph driver remote -->
    :end-before: <!-- Include end Ceph driver remote -->
```

% Include content from [storage_ceph.md](storage_ceph.md)
```{include} storage_ceph.md
    :start-after: <!-- Include start Ceph driver control -->
    :end-before: <!-- Include end Ceph driver control -->
```

The `cephfs` driver in Incus supports snapshots if snapshots are enabled on the server side.

## Configuration options

The following configuration options are available for storage pools that use the `cephfs` driver and for storage volumes in these pools.

(storage-cephfs-pool-config)=
### Storage pool configuration

Key                           | Type                          | Default                                 | Description
:--                           | :---                          | :------                                 | :----------
`cephfs.cluster_name`         | string                        | `ceph`                                  | Name of the Ceph cluster that contains the CephFS file system
`cephfs.create_missing`       | bool                          | `false`                                 | Create the file system and the missing data and metadata OSD pools
`cephfs.data_pool`            | string                        | -                                       | Data OSD pool name to create for the file system
`cephfs.fscache`              | bool                          | `false`                                 | Enable use of kernel `fscache` and `cachefilesd`
`cephfs.meta_pool`            | string                        | -                                       | Metadata OSD pool name to create for the file system
`cephfs.osd_pg_num`           | string                        | -                                       | OSD pool `pg_num` to use when creating missing OSD pools
`cephfs.path`                 | string                        | `/`                                     | The base path for the CephFS mount
`cephfs.user.name`            | string                        | `admin`                                 | The Ceph user to use
`source`                      | string                        | -                                       | Existing CephFS file system or file system path to use
`volatile.pool.pristine`      | string                        | `true`                                  | Whether the CephFS file system was empty on creation time

{{volume_configuration}}

### Storage volume configuration

Key                     | Type      | Condition                 | Default                                        | Description
:--                     | :---      | :--------                 | :------                                        | :----------
`security.shared`       | bool      | custom block volume       | same as `volume.security.shared` or `false`    | Enable sharing the volume across multiple instances
`security.shifted`      | bool      | custom volume             | same as `volume.security.shifted` or `false`   | {{enable_ID_shifting}}
`security.unmapped`     | bool      | custom volume             | same as `volume.security.unmapped` or `false`  | Disable ID mapping for the volume
`size`                  | string    | appropriate driver        | same as `volume.size`                          | Size/quota of the storage volume
`snapshots.expiry`      | string    | custom volume             | same as `volume.snapshots.expiry`              | {{snapshot_expiry_format}}
`snapshots.pattern`     | string    | custom volume             | same as `volume.snapshots.pattern` or `snap%d` | {{snapshot_pattern_format}} [^*]
`snapshots.schedule`    | string    | custom volume             | same as `volume.snapshots.schedule`            | {{snapshot_schedule_format}}

[^*]: {{snapshot_pattern_detail}}
