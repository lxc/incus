(storage-nfs)=
# NFS - `nfs`

Network File System is a distributed file system protocol. It is used to serve and access files over a computer network.

To use NFS one need to setup a NFS file system following documentation from your Linux distribution of choice.

## `nfs` driver in Incus

The `nfs` driver in Incus only supports NFS version 4.2 and has a couple of limitations.

UID/GID squashing should be enabled. This can be done by explicitly setting `no_root_squash` and `no_all_squash` in `/etc/export`.

Note that it is not recommended to use `nfs` driver as container or virtual machine storage volumes as it is unclear how well it works.

## Configuration options

The following configuration options are available for storage pools that use the `nfs` driver and for storage volumes in these pools.

### Storage pool configuration

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group storage_nfs-common start -->
    :end-before: <!-- config group storage_nfs-common end -->
```

{{volume_configuration}}
