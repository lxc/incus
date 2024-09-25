(howto-storage-buckets)=
# How to manage storage buckets and keys

See the following sections for instructions on how to create, configure, view and resize {ref}`storage-buckets` and how to manage storage bucket keys.

## Configure the S3 address

If you want to use storage buckets on local storage (thus in a `dir`, `btrfs`, `lvm`, or `zfs` pool), you must configure the S3 address for your Incus server.
This is the address that you can then use to access the buckets through the S3 protocol.

To configure the S3 address, set the {config:option}`server-core:core.storage_buckets_address` server configuration option.
For example:

    incus config set core.storage_buckets_address :8555

## Manage storage buckets

Storage buckets provide access to object storage exposed using the S3 protocol.

Unlike custom storage volumes, storage buckets are not added to an instance, but applications can instead access them directly via their URL.

See {ref}`storage-buckets` for detailed information.

### Create a storage bucket

Use the following command to create a storage bucket in a storage pool:

    incus storage bucket create <pool_name> <bucket_name> [configuration_options...]

See the {ref}`storage-drivers` documentation for a list of available storage bucket configuration options for each driver that supports object storage.

To add a storage bucket on a cluster member, add the `--target` flag:

    incus storage bucket create <pool_name> <bucket_name> --target=<cluster_member> [configuration_options...]

```{note}
For most storage drivers, storage buckets are not replicated across the cluster and exist only on the member for which they were created.
This behavior is different for `cephobject` storage pools, where buckets are available from any cluster member.
```

### Configure storage bucket settings

See the {ref}`storage-drivers` documentation for the available configuration options for each storage driver that supports object storage.

Use the following command to set configuration options for a storage bucket:

    incus storage bucket set <pool_name> <bucket_name> <key> <value>

For example, to set the quota size of a bucket, use the following command:

    incus storage bucket set my-pool my-bucket size 1MiB

You can also edit the storage bucket configuration by using the following command:

    incus storage bucket edit <pool_name> <bucket_name>

Use the following command to delete a storage bucket and its keys:

    incus storage bucket delete <pool_name> <bucket_name>

### View storage buckets

You can display a list of all available storage buckets in a storage pool and check their configuration.

To list all available storage buckets in a storage pool, use the following command:

    incus storage bucket list <pool_name>

To show detailed information about a specific bucket, use the following command:

    incus storage bucket show <pool_name> <bucket_name>

### Resize a storage bucket

By default, storage buckets do not have a quota applied.

To set or change a quota for a storage bucket, set its size configuration:

    incus storage bucket set <pool_name> <bucket_name> size <new_size>

```{important}
- Growing a storage bucket usually works (if the storage pool has sufficient storage).
- You cannot shrink a storage bucket below its current used size.

```

## Manage storage bucket keys

To access a storage bucket, applications must use a set of S3 credentials made up of an *access key* and a *secret key*.
You can create multiple sets of credentials for a specific bucket.

Each set of credentials is given a key name.
The key name is used only for reference and does not need to be provided to the application that uses the credentials.

Each set of credentials has a *role* that specifies what operations they can perform on the bucket.

The roles available are:

- `admin` - Full access to the bucket
- `read-only` - Read-only access to the bucket (list and get files only)

If the role is not specified when creating a bucket key, the role used is `read-only`.

### Create storage bucket keys

Use the following command to create a set of credentials for a storage bucket:

    incus storage bucket key create <pool_name> <bucket_name> <key_name> [configuration_options...]

Use the following command to create a set of credentials for a storage bucket with a specific role:

    incus storage bucket key create <pool_name> <bucket_name> <key_name> --role=admin [configuration_options...]

These commands will generate and display a random set of credential keys.

### Edit or delete storage bucket keys

Use the following command to edit an existing bucket key:

    incus storage bucket key edit <pool_name> <bucket_name> <key_name>

Use the following command to delete an existing bucket key:

    incus storage bucket key delete <pool_name> <bucket_name> <key_name>

### View storage bucket keys

Use the following command to see the keys defined for an existing bucket:

    incus storage bucket key list <pool_name> <bucket_name>

Use the following command to see a specific bucket key:

    incus storage bucket key show <pool_name> <bucket_name> <key_name>
