(storage-linstor)=
# LINSTOR - `linstor`

[LINSTOR](https://linbit.com/linstor/) is an open-source software-defined storage solution that is typically used to manage {abbr}`DRBD (Distributed Replicated Block Device)` replicated storage volumes. It provides both highly available and high performance volumes while focusing on operational simplicity.

LINSTOR does not manage the underlying storage by itself, and instead relies on other components such as ZFS or LVM to provision block devices. These block devices are then replicated using [DRBD](https://linbit.com/drbd/) to provide fault tolerance and the ability to mount the volumes on any cluster node, regardless of its storage capabilities. Since volumes are replicated using the DRBD kernel module, the data path for the replication is kept entirely on kernel space, reducing its overhead when compared to solutions implemented in user space.

## Terminology

A LINSTOR cluster is composed of two main components: *controllers* and *satellites*. The LINSTOR controller manages the database and keeps track of the cluster state and configuration, while satellites provide storage and ability to mount volumes across the cluster. Clients interact only with the controller, which is responsible for orchestrating operations across satellites to fulfill the user's request.

LINSTOR takes a somewhat object-oriented approach to its internal concepts. This manifests itself in the hierarchical nature of concepts and the fact that lower level objects can inherit properties from higher level ones.

LINSTOR has the concept of a *storage pool*, which describes physical storage that can be consumed by LINSTOR to create volumes. A storage pool defines its backend driver (such as LVM or ZFS), the cluster node in which it exists and properties that can be applied to either the storage pool itself or its backend storage.

In LINSTOR, a *resource* is the representation of a storage unit that can be consumed by instances. A resource is most often a DRBD replicated block device, and in that case represents one replica of that device. Resources can be grouped into *resource definitions*, which define common properties that should be inherited by all their child resources. Similarly, *resource groups* define common properties that are applied to their child resource definitions. Resource groups also define placement rules that define how many replicas should be created for a given resource definition, which storage pool should be used, how to spread the replicas among different availability zones, etc. The usual way to interact with LINSTOR is by defining a resource group with the desired properties and then *spawning* resources from it.

## `linstor` driver in Incus

```{note}
LINSTOR can only move and mount volumes between its satellite nodes. Therefore, to ensure that all Incus cluster members can access volumes, all Incus nodes must also be LINSTOR satellite nodes. In other words, each node running the `incus` service should also run an `linstor-satellite` service.

Note, however, that this does not mean that Incus nodes must also provide storage. It is still possible to use LINSTOR while using separated storage and compute nodes by deploying "diskless" satellites on Incus nodes. Diskless nodes do not provide storage, but are still able to mount DRBD devices and perform IO over the network.
```

Unlike other storage drivers, this driver does not set up the storage system but assumes that you already have a LINSTOR cluster installed. The driver requires the {config:option}`server-miscellaneous:storage.linstor.controller_connection` option to be set to the endpoint of a LINSTOR controller that will be used by Incus.

This driver also behaves differently than other drivers in that it can provide both remote and local storage. If a diskful replica of the volume is available on the node, reads and writes can be performed locally to reduce latency (although writes must be synchronously replicated across replicas, so network latency still has an impact). At the same time, a diskless replica performs all IO over the network, enabling volumes to be mounted and used on any node regardless of its physical storage. These hybrid capabilities enable LINSTOR to provide low latency storage while retaining the flexibility of moving volumes across cluster nodes when needed.

The `linstor` driver in Incus uses resource groups to manage and spawn resources. The following table describes the mapping between Incus and LINSTOR concepts:

Incus concept | LINSTOR concept
:--           | :--
Storage pool  | Resource group
Volume        | Resource definition
Snapshot      | Snapshot

Incus assumes that it has full control over the LINSTOR resource group.
Therefore, you should never maintain any entities that are not owned by Incus in an Incus LINSTOR resource group, because Incus might delete them.

When managing resources, Incus needs to be able to determine which LINSTOR satellite node corresponds to a given Incus node. By default, Incus assumes that its node names match LINSTOR's (e.g. `incus cluster list` and `linstor node list` show the same node names). When Incus is running as a standalone server (i.e. not clustered), the hostname is used as the node name. If node names between Incus and LINSTOR do not match, the {config:option}`server-miscellaneous:storage.linstor.satellite.name` can be set on each Incus node to the appropriate LINSTOR satellite node name.

### Limitations

The `linstor` driver has the following limitations:

Sharing custom volumes between instances
: Custom storage volumes with {ref}`content type <storage-content-types>` `filesystem` can usually be shared between multiple instances different cluster members.
  However, because the LINSTOR driver "simulates" volumes with content type `filesystem` by putting a file system on top of an DRBD replicated device, custom storage volumes can only be assigned to a single instance at a time.

Sharing the resource group between installations
: Sharing the same LINSTOR resource group between multiple Incus installations is not supported.

Restoring from older snapshots
: LINSTOR doesn't support restoring from snapshots other than the latest one.
  You can, however, create new instances from older snapshots.
  This method makes it possible to confirm whether a specific snapshot contains what you need.
  After determining the correct snapshot, you can {ref}`remove the newer snapshots <storage-edit-snapshots>` so that the snapshot you need is the latest one and you can restore it.

## Configuration options

The following configuration options are available for storage pools that use the `linstor` driver and for storage volumes in these pools.

(storage-linstor-pool-config)=
### Storage pool configuration

Key                                   | Type           | Default           | Description
:--                                   | :---           | :------           | :----------
`linstor.resource_group.name`         | string         | `incus`           | Name of the LINSTOR resource group that will be used for the storage pool
`linstor.resource_group.place_count`  | int            | 2                 | Number of diskful replicas that should be created for resources in the resource group. Increasing the value of this option on a pool that already has volumes will result in LINSTOR creating new diskful replicas for all existing resources to match the new value
`linstor.resource_group.storage_pool` | string         | -                 | The storage pool name in which resources should be placed on satellite nodes
`linstor.volume.prefix`               | string         | `incus-volume-`   | The prefix to use for the internal names of LINSTOR-managed volumes. Cannot be updated after the storage pool is created
`drbd.on_no_quorum`                   | string         | -                 | The DRBD policy to use on resources when quorum is lost (applied to the resource group)
`drbd.auto_diskful`                   | string         | -                 | A duration string describing the time after which a primary diskless resource can be converted to diskful if storage is available on the node (applied to the resource group)
`drbd.auto_add_quorum_tiebreaker`     | bool           | `true`            | Whether to allow LINSTOR to automatically create diskless resources to act as quorum tiebreakers if needed (applied to the resource group)

{{volume_configuration}}

(storage-linstor-vol-config)=
### Storage volume configuration

Key                               | Type      | Condition                                         | Default                                        | Description
:--                               | :---      | :--------                                         | :------                                        | :----------
`block.filesystem`                | string    | block-based volume with content type `filesystem` | same as `volume.block.filesystem`              | {{block_filesystem}}
`block.mount_options`             | string    | block-based volume with content type `filesystem` | same as `volume.block.mount_options`           | Mount options for block-backed file system volumes
`initial.gid`                     | int       | custom volume with content type `filesystem`      | same as `volume.initial.uid` or `0`            | GID of the volume owner in the instance
`initial.mode`                    | int       | custom volume with content type `filesystem`      | same as `volume.initial.mode` or `711`         | Mode of the volume in the instance
`initial.uid`                     | int       | custom volume with content type `filesystem`      | same as `volume.initial.gid` or `0`            | UID of the volume owner in the instance
`security.shared`                 | bool      | custom block volume                               | same as `volume.security.shared` or `false`    | Enable sharing the volume across multiple instances
`security.shifted`                | bool      | custom volume                                     | same as `volume.security.shifted` or `false`   | {{enable_ID_shifting}}
`security.unmapped`               | bool      | custom volume                                     | same as `volume.security.unmapped` or `false`  | Disable ID mapping for the volume
`size`                            | string    |                                                   | same as `volume.size`                          | Size/quota of the storage volume
`snapshots.expiry`                | string    | custom volume                                     | same as `volume.snapshots.expiry`              | {{snapshot_expiry_format}}
`snapshots.pattern`               | string    | custom volume                                     | same as `volume.snapshots.pattern` or `snap%d` | {{snapshot_pattern_format}} [^*]
`snapshots.schedule`              | string    | custom volume                                     | same as `volume.snapshots.schedule`            | {{snapshot_schedule_format}}
`drbd.on_no_quorum`               | string    |                                                   | -                                              | The DRBD policy to use on resources when quorum is lost (applied to the resource definition)
`drbd.auto_diskful`               | string    |                                                   | -                                              | A duration string describing the time after which a primary diskless resource can be converted to diskful if storage is available on the node (applied to the resource definition)
`drbd.auto_add_quorum_tiebreaker` | bool      |                                                   | `true`                                         | Whether to allow LINSTOR to automatically create diskless resources to act as quorum tiebreakers if needed (applied to the resource definition)

[^*]: {{snapshot_pattern_detail}}

```{toctree}
:maxdepth: 1
:hidden:

Setup LINSTOR </howto/storage_linstor_setup>
Driver internals <storage_linstor_internals>
```
