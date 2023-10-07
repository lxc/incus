(storage-cephobject)=
# Ceph Object - `cephobject`

% Include content from [storage_ceph.md](storage_ceph.md)
```{include} storage_ceph.md
    :start-after: <!-- Include start Ceph intro -->
    :end-before: <!-- Include end Ceph intro -->
```

[Ceph Object Gateway](https://docs.ceph.com/en/latest/radosgw/) is an object storage interface built on top of [`librados`](https://docs.ceph.com/en/latest/rados/api/librados-intro/) to provide applications with a RESTful gateway to [Ceph Storage Clusters](https://docs.ceph.com/en/latest/rados/).
It provides object storage functionality with an interface that is compatible with a large subset of the Amazon S3 RESTful API.

## Terminology

% Include content from [storage_ceph.md](storage_ceph.md)
```{include} storage_ceph.md
    :start-after: <!-- Include start Ceph terminology -->
    :end-before: <!-- Include end Ceph terminology -->
```

A *Ceph Object Gateway* consists of several OSD pools and one or more *Ceph Object Gateway daemon* (`radosgw`) processes that provide object gateway functionality.

## `cephobject` driver in Incus

```{note}
The `cephobject` driver can only be used for buckets.

For storage volumes, use the {ref}`Ceph <storage-ceph>` or {ref}`CephFS <storage-cephfs>` drivers.
```

% Include content from [storage_ceph.md](storage_ceph.md)
```{include} storage_ceph.md
    :start-after: <!-- Include start Ceph driver cluster -->
    :end-before: <!-- Include end Ceph driver cluster -->
```

You must set up a `radosgw` environment beforehand and ensure that its HTTP/HTTPS endpoint URL is reachable from the Incus server or servers.
See [Manual Deployment](https://docs.ceph.com/en/latest/install/manual-deployment/) for information on how to set up a Ceph cluster and [Ceph Object Gateway](https://docs.ceph.com/en/latest/radosgw/) on how to set up a `radosgw` environment.

The `radosgw` URL can be specified at pool creation time using the [`cephobject.radosgw.endpoint`](storage-cephobject-pool-config) option.

Incus uses the `radosgw-admin` command to manage buckets. So this command must be available and operational on the Incus servers.

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

## Configuration options

The following configuration options are available for storage pools that use the `cephobject` driver and for storage buckets in these pools.

(storage-cephobject-pool-config)=
### Storage pool configuration

Key                                      | Type                          | Default | Description
:--                                      | :---                          | :------ | :----------
`cephobject.bucket.name_prefix`          | string                        | -       | Prefix to add to bucket names in Ceph
`cephobject.cluster_name`                | string                        | `ceph`  | The Ceph cluster to use
`cephobject.radosgw.endpoint`            | string                        | -       | URL of the `radosgw` gateway process
`cephobject.radosgw.endpoint_cert_file`  | string                        | -       | Path to the file containing the TLS client certificate to use for endpoint communication
`cephobject.user.name`                   | string                        | `admin` | The Ceph user to use
`volatile.pool.pristine`                 | string                        | `true`  | Whether the `radosgw` `incus-admin` user existed at creation time

### Storage bucket configuration

Key    | Type   | Default                | Description
:--    | :---   | :------                | :----------
`size` | string | -                      | Quota of the storage bucket
