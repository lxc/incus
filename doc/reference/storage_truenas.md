(storage-truenas)=
# TrueNAS - `truenas`
## The `truenas` storage driver in Incus

The `truenas` storage driver enables an Incus node to use a remote
TrueNAS storage server to host one or more Incus storage pools. When the
node is part of a cluster, all cluster members can access the storage
pool simultaneously, making it ideal for use cases such as live
migrating virtual machines (VMs) between nodes.

The driver operates in a block-based manner, meaning that all Incus
volumes are created as ZFS Volume block devices on the remote TrueNAS
server. These ZFS Volume block devices are accessed on the local Incus
node via iSCSI.

Modeled after the existing ZFS driver, the `truenas` driver supports
most standard ZFS functionality, but operates on remote TrueNAS servers.
For instance, a local VM can be snapshotted and cloned, with the
snapshot and clone operations performed on the remote server after
synchronizing the local file system. The clone is then activated through
iSCSI as necessary.

Each storage pool corresponds to a ZFS dataset on a remote TrueNAS host.
The dataset is created automatically if it does not exist. The driver
uses ZFS features available on the remote host to support efficient
image handling, copy operations, and snapshot management without
requiring nested ZFS (ZFS-on-ZFS).

To reference a remote dataset, the `source` property can be specified in the form:
`[<remote host>:]<remote pool>[[/<remote dataset>]...][/]`

If the path ends with a trailing `/`, the dataset name will be derived
from the Incus storage pool name (e.g., `tank/pool1`).

## Requirements

The driver relies on the
[`truenas_incus_ctl`](https://github.com/truenas/truenas_incus_ctl) tool
to interact with the TrueNAS API and perform actions on the remote
server. This tool also manages the activation and deactivation of remote
ZFS Volumes via `open-iscsi`. If `truenas_incus_ctl` is not installed or
available in the system's PATH, the driver will be disabled.

To install the required tool, download the latest version (v0.7.2+ is
required) from the [`truenas\_incus\_ctl` GitHub
page](https://github.com/truenas/truenas_incus_ctl). Additionally,
ensure that `open-iscsi` is installed on the system, which can be done
using:

    sudo apt install open-iscsi

## Logging in to the TrueNAS host

As an alternative to manually creating an API Key and supplying using the `truenas.api_key` property, you can instead `login` to the remote server using the `truenas_incus_ctl` tool.

    sudo truenas_incus_ctl config login

This will prompt you to provide connection details for the TrueNAS server, including authentication details, and will save the configuration to a local file. After logging in, you can verify the iSCSI setup with:

    sudo truenas_incus_ctl share iscsi setup --test

Once the tool is configured, you can use it to interact with remote datasets and create storage pools:

    incus storage create <poolname> truenas source=[host:]<pool>[/<dataset>]/[remote-poolname]

In this command:

* `source` refers to the location on the remote TrueNAS host where the storage pool will be created.
* `host` is optional, and can be specified using the `truenas.host` property, or by specifying a configuration with `truenas.config`
* If `remote-poolname` is not supplied, it will default to the name of the local pool.

## Configuration options

The following configuration options are available for storage pools that use the `truenas` driver and for storage volumes in these pools.

(storage-truenas-pool-config)=
### Storage pool configuration

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group storage_truenas-common start -->
    :end-before: <!-- config group storage_truenas-common end -->
```

{{volume_configuration}}

(storage-truenas-vol-config)=
### Storage volume configuration

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group storage_volume_truenas-common start -->
    :end-before: <!-- config group storage_volume_truenas-common end -->
```
