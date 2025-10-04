(storage-dir)=
# Directory - `dir`

The directory storage driver is a basic backend that stores its data in a standard file and directory structure.
This driver is quick to set up and allows inspecting the files directly on the disk, which can be convenient for testing.
However, Incus operations are {ref}`not optimized <storage-drivers-features>` for this driver.

## `dir` driver in Incus

The `dir` driver in Incus is fully functional and provides the same set of features as other drivers.
However, it is much slower than all the other drivers because it must unpack images and do instant copies of instances, snapshots and images.

Unless specified differently during creation (with the `source` configuration option), the data is stored in the `/var/lib/incus/storage-pools/` directory.

(storage-dir-quotas)=
### Quotas

<!-- Include start dir quotas -->
The `dir` driver supports storage quotas when running on either ext4 or XFS with project quotas enabled at the file system level.
<!-- Include end dir quotas -->

## Configuration options

The following configuration options are available for storage pools that use the `dir` driver and for storage volumes in these pools.

### Storage pool configuration

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group storage_dir-common start -->
    :end-before: <!-- config group storage_dir-common end -->
```

{{volume_configuration}}

### Storage volume configuration

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group storage_volume_dir-common start -->
    :end-before: <!-- config group storage_volume_dir-common end -->
```

[^*]: {{snapshot_pattern_detail}}

### Storage bucket configuration

To enable storage buckets for local storage pool drivers and allow applications to access the buckets via the S3 protocol, you must configure the {config:option}`server-core:core.storage_buckets_address` server setting.

Storage buckets do not have any configuration for `dir` pools.
Unlike the other storage pool drivers, the `dir` driver does not support bucket quotas via the `size` setting.
