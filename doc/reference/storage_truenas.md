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

| Key                      | Type    | Default | Description                                                                                                                                            |
| :---                     | :---    | :---    | :---                                                                                                                                                   |
| `source`                 | string  | -       | ZFS dataset to use on the remote TrueNAS host. Format: `[<host>:]<pool>[/<dataset>][/]`. If `host` is omitted here, it must be set via `truenas.host`. |
| `truenas.allow_insecure` | boolean | false   | If set to `true`, allows insecure (non-TLS) connections to the TrueNAS API.                                                                            |
| `truenas.api_key`        | string  | -       | API key used to authenticate with the TrueNAS host.                                                                                                    |
| `truenas.dataset`        | string  | -       | Remote dataset name. Typically inferred from `source`, but can be overridden.                                                                          |
| `truenas.host`           | string  | -       | Hostname or IP address of the remote TrueNAS system. Optional if included in the `source`, or a configuration is used.                                 |
| `truenas.initiator`      | string  | -       | iSCSI initiator name used during block volume attachment.                                                                                              |
| `truenas.portal`         | string  | -       | iSCSI portal address to use for block volume connections.                                                                                              |

{{volume_configuration}}

(storage-truenas-vol-config)=
### Storage volume configuration

| Key                        | Type   | Condition                                    | Default                                              | Description                                         |
| :---                       | :---   | :---                                         | :---                                                 | :---                                                |
| `block.filesystem`         | string |                                              | same as `volume.block.filesystem`                    | {{block_filesystem}}                                |
| `block.mount_options`      | string |                                              | same as `volume.block.mount_options`                 | Mount options for block-backed file system volumes  |
| `initial.gid`              | int    | custom volume with content type `filesystem` | same as `volume.initial.uid` or `0`                  | GID of the volume owner in the instance             |
| `initial.mode`             | int    | custom volume with content type `filesystem` | same as `volume.initial.mode` or `711`               | Mode  of the volume in the instance                 |
| `initial.uid`              | int    | custom volume with content type `filesystem` | same as `volume.initial.gid` or `0`                  | UID of the volume owner in the instance             |
| `security.shared`          | bool   | custom block volume                          | same as `volume.security.shared` or `false`          | Enable sharing the volume across multiple instances |
| `security.shifted`         | bool   | custom volume                                | same as `volume.security.shifted` or `false`         | {{enable_ID_shifting}}                              |
| `security.unmapped`        | bool   | custom volume                                | same as `volume.security.unmapped` or `false`        | Disable ID mapping for the volume                   |
| `size`                     | string |                                              | same as `volume.size`                                | Size/quota of the storage volume                    |
| `snapshots.expiry`         | string | custom volume                                | same as `volume.snapshots.expiry`                    | {{snapshot_expiry_format}}                          |
| `snapshots.expiry.manual`  | string | custom volume                                | same as `volume.snapshots.expiry.manual`             | {{snapshot_expiry_format}}                          |
| `snapshots.pattern`        | string | custom volume                                | same as `volume.snapshots.pattern` or `snap%d`       | {{snapshot_pattern_format}}                         |
| `snapshots.schedule`       | string | custom volume                                | same as `snapshots.schedule`                         | {{snapshot_schedule_format}}                        |
| `truenas.blocksize`        | string |                                              | same as `volume.truenas.blocksize`                   | Size of the ZFS block in range from 512 bytes to 16 MiB (must be power of 2) - for block volume, a maximum value of 128 KiB will be used even if a higher value is set |
| `truenas.remove_snapshots` | bool   |                                              | same as `volume.truenas.remove_snapshots` or `false` | Remove snapshots as needed                          |
| `truenas.use_refquota`     | bool   |                                              | same as `volume.truenas.use_refquota` or `false`     | Use `refquota` instead of `quota` for space         |
