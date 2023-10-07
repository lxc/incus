# API extensions

The changes below were introduced to the Incus API after the 1.0 API was finalized.

They are all backward compatible and can be detected by client tools by
looking at the `api_extensions` field in `GET /1.0`.

## `storage_zfs_remove_snapshots`

A `storage.zfs_remove_snapshots` daemon configuration key was introduced.

It's a Boolean that defaults to `false` and that when set to `true` instructs Incus
to remove any needed snapshot when attempting to restore another.

This is needed as ZFS will only let you restore the latest snapshot.

## `container_host_shutdown_timeout`

A `boot.host_shutdown_timeout` container configuration key was introduced.

It's an integer which indicates how long Incus should wait for the container
to stop before killing it.

Its value is only used on clean Incus daemon shutdown. It defaults to 30s.

## `container_stop_priority`

A `boot.stop.priority` container configuration key was introduced.

It's an integer which indicates the priority of a container during shutdown.

Containers will shutdown starting with the highest priority level.

Containers with the same priority will shutdown in parallel.  It defaults to 0.

## `container_syscall_filtering`

A number of new syscalls related container configuration keys were introduced.

* `security.syscalls.blacklist_default` <!-- wokeignore:rule=blacklist -->
* `security.syscalls.blacklist_compat` <!-- wokeignore:rule=blacklist -->
* `security.syscalls.blacklist` <!-- wokeignore:rule=blacklist -->
* `security.syscalls.whitelist` <!-- wokeignore:rule=whitelist -->

See [Instance configuration](instance-config) for how to use them.

## `auth_pki`

This indicates support for PKI authentication mode.

In this mode, the client and server both must use certificates issued by the same PKI.

See [Security](security.md) for details.

## `container_last_used_at`

A `last_used_at` field was added to the `GET /1.0/containers/<name>` endpoint.

It is a timestamp of the last time the container was started.

If a container has been created but not started yet, `last_used_at` field
will be `1970-01-01T00:00:00Z`

## `etag`

Add support for the ETag header on all relevant endpoints.

This adds the following HTTP header on answers to GET:

* ETag (SHA-256 of user modifiable content)

And adds support for the following HTTP header on PUT requests:

* If-Match (ETag value retrieved through previous GET)

This makes it possible to GET a Incus object, modify it and PUT it without
risking to hit a race condition where Incus or another client modified the
object in the meantime.

## `patch`

Add support for the HTTP PATCH method.

PATCH allows for partial update of an object in place of PUT.

## `usb_devices`

Add support for USB hotplug.

## `https_allowed_credentials`

To use Incus API with all Web Browsers (via SPAs) you must send credentials
(certificate) with each XHR (in order for this to happen, you should set
[`withCredentials=true`](https://developer.mozilla.org/en-US/docs/Web/API/XMLHttpRequest/withCredentials)
flag to each XHR Request).

Some browsers like Firefox and Safari can't accept server response without
`Access-Control-Allow-Credentials: true` header. To ensure that the server will
return a response with that header, set `core.https_allowed_credentials=true`.

## `image_compression_algorithm`

This adds support for a `compression_algorithm` property when creating an image (`POST /1.0/images`).

Setting this property overrides the server default value (`images.compression_algorithm`).

## `directory_manipulation`

This allows for creating and listing directories via the Incus API, and exports
the file type via the X-Incus-type header, which can be either `file` or
`directory` right now.

## `container_cpu_time`

This adds support for retrieving CPU time for a running container.

## `storage_zfs_use_refquota`

Introduces a new server property `storage.zfs_use_refquota` which instructs Incus
to set the `refquota` property instead of `quota` when setting a size limit
on a container. Incus will also then use `usedbydataset` in place of `used`
when being queried about disk utilization.

This effectively controls whether disk usage by snapshots should be
considered as part of the container's disk space usage.

## `storage_lvm_mount_options`

Adds a new `storage.lvm_mount_options` daemon configuration option
which defaults to `discard` and allows the user to set addition mount
options for the file system used by the LVM LV.

## `network`

Network management API for Incus.

This includes:

* Addition of the `managed` property on `/1.0/networks` entries
* All the network configuration options (see [Network configuration](networks.md) for details)
* `POST /1.0/networks` (see [RESTful API](rest-api.md) for details)
* `PUT /1.0/networks/<entry>` (see [RESTful API](rest-api.md) for details)
* `PATCH /1.0/networks/<entry>` (see [RESTful API](rest-api.md) for details)
* `DELETE /1.0/networks/<entry>` (see [RESTful API](rest-api.md) for details)
* `ipv4.address` property on `nic` type devices (when `nictype` is `bridged`)
* `ipv6.address` property on `nic` type devices (when `nictype` is `bridged`)
* `security.mac_filtering` property on `nic` type devices (when `nictype` is `bridged`)

## `profile_usedby`

Adds a new `used_by` field to profile entries listing the containers that are using it.

## `container_push`

When a container is created in push mode, the client serves as a proxy between
the source and target server. This is useful in cases where the target server
is behind a NAT or firewall and cannot directly communicate with the source
server and operate in pull mode.

## `container_exec_recording`

Introduces a new Boolean `record-output`, parameter to
`/1.0/containers/<name>/exec` which when set to `true` and combined with
with `wait-for-websocket` set to `false`, will record stdout and stderr to
disk and make them available through the logs interface.

The URL to the recorded output is included in the operation metadata
once the command is done running.

That output will expire similarly to other log files, typically after 48 hours.

## `certificate_update`

Adds the following to the REST API:

* ETag header on GET of a certificate
* PUT of certificate entries
* PATCH of certificate entries

## `container_exec_signal_handling`

Adds support `/1.0/containers/<name>/exec` for forwarding signals sent to the
client to the processes executing in the container. Currently SIGTERM and
SIGHUP are forwarded. Further signals that can be forwarded might be added
later.

## `gpu_devices`

Enables adding GPUs to a container.

## `container_image_properties`

Introduces a new `image` configuration key space. Read-only, includes the properties of the parent image.

## `migration_progress`

Transfer progress is now exported as part of the operation, on both sending and receiving ends.
This shows up as a `fs_progress` attribute in the operation metadata.

## `id_map`

Enables setting the `security.idmap.isolated` and `security.idmap.isolated`,
`security.idmap.size`, and `raw.id_map` fields.

## `network_firewall_filtering`

Add two new keys, `ipv4.firewall` and `ipv6.firewall` which if set to
`false` will turn off the generation of `iptables` FORWARDING rules. NAT
rules will still be added so long as the matching `ipv4.nat` or
`ipv6.nat` key is set to `true`.

Rules necessary for `dnsmasq` to work (DHCP/DNS) will always be applied if
`dnsmasq` is enabled on the bridge.

## `network_routes`

Introduces `ipv4.routes` and `ipv6.routes` which allow routing additional subnets to a Incus bridge.

## `storage`

Storage management API for Incus.

This includes:

* `GET /1.0/storage-pools`
* `POST /1.0/storage-pools` (see [RESTful API](rest-api.md) for details)

* `GET /1.0/storage-pools/<name>` (see [RESTful API](rest-api.md) for details)
* `POST /1.0/storage-pools/<name>` (see [RESTful API](rest-api.md) for details)
* `PUT /1.0/storage-pools/<name>` (see [RESTful API](rest-api.md) for details)
* `PATCH /1.0/storage-pools/<name>` (see [RESTful API](rest-api.md) for details)
* `DELETE /1.0/storage-pools/<name>` (see [RESTful API](rest-api.md) for details)

* `GET /1.0/storage-pools/<name>/volumes` (see [RESTful API](rest-api.md) for details)

* `GET /1.0/storage-pools/<name>/volumes/<volume_type>` (see [RESTful API](rest-api.md) for details)
* `POST /1.0/storage-pools/<name>/volumes/<volume_type>` (see [RESTful API](rest-api.md) for details)

* `GET /1.0/storage-pools/<pool>/volumes/<volume_type>/<name>` (see [RESTful API](rest-api.md) for details)
* `POST /1.0/storage-pools/<pool>/volumes/<volume_type>/<name>` (see [RESTful API](rest-api.md) for details)
* `PUT /1.0/storage-pools/<pool>/volumes/<volume_type>/<name>` (see [RESTful API](rest-api.md) for details)
* `PATCH /1.0/storage-pools/<pool>/volumes/<volume_type>/<name>` (see [RESTful API](rest-api.md) for details)
* `DELETE /1.0/storage-pools/<pool>/volumes/<volume_type>/<name>` (see [RESTful API](rest-api.md) for details)

* All storage configuration options (see [Storage configuration](storage.md) for details)

## `file_delete`

Implements `DELETE` in `/1.0/containers/<name>/files`

## `file_append`

Implements the `X-Incus-write` header which can be one of `overwrite` or `append`.

## `network_dhcp_expiry`

Introduces `ipv4.dhcp.expiry` and `ipv6.dhcp.expiry` allowing to set the DHCP lease expiry time.

## `storage_lvm_vg_rename`

Introduces the ability to rename a volume group by setting `storage.lvm.vg_name`.

## `storage_lvm_thinpool_rename`

Introduces the ability to rename a thin pool name by setting `storage.thinpool_name`.

## `network_vlan`

This adds a new `vlan` property to `macvlan` network devices.

When set, this will instruct Incus to attach to the specified VLAN. Incus
will look for an existing interface for that VLAN on the host. If one
can't be found it will create one itself and then use that as the
macvlan parent.

## `image_create_aliases`

Adds a new `aliases` field to `POST /1.0/images` allowing for aliases to
be set at image creation/import time.

## `container_stateless_copy`

This introduces a new `live` attribute in `POST /1.0/containers/<name>`.
Setting it to `false` tells Incus not to attempt running state transfer.

## `container_only_migration`

Introduces a new Boolean `container_only` attribute. When set to `true` only the
container will be copied or moved.

## `storage_zfs_clone_copy`

Introduces a new Boolean `storage_zfs_clone_copy` property for ZFS storage
pools. When set to `false` copying a container will be done through `zfs send` and
receive. This will make the target container independent of its source
container thus avoiding the need to keep dependent snapshots in the ZFS pool
around. However, this also entails less efficient storage usage for the
affected pool.
The default value for this property is `true`, i.e. space-efficient snapshots
will be used unless explicitly set to `false`.

## `unix_device_rename`

Introduces the ability to rename the `unix-block`/`unix-char` device inside container by setting `path`,
and the `source` attribute is added to specify the device on host.
If `source` is set without a `path`, we should assume that `path` will be the same as `source`.
If `path` is set without `source` and `major`/`minor` isn't set,
we should assume that `source` will be the same as `path`.
So at least one of them must be set.

## `storage_rsync_bwlimit`

When `rsync` has to be invoked to transfer storage entities setting `rsync.bwlimit`
places an upper limit on the amount of socket I/O allowed.

## `network_vxlan_interface`

This introduces a new `tunnel.NAME.interface` option for networks.

This key control what host network interface is used for a VXLAN tunnel.

## `storage_btrfs_mount_options`

This introduces the `btrfs.mount_options` property for Btrfs storage pools.

This key controls what mount options will be used for the Btrfs storage pool.

## `entity_description`

This adds descriptions to entities like containers, snapshots, networks, storage pools and volumes.

## `image_force_refresh`

This allows forcing a refresh for an existing image.

## `storage_lvm_lv_resizing`

This introduces the ability to resize logical volumes by setting the `size`
property in the containers root disk device.

## `id_map_base`

This introduces a new `security.idmap.base` allowing the user to skip the
map auto-selection process for isolated containers and specify what host
UID/GID to use as the base.

## `file_symlinks`

This adds support for transferring symlinks through the file API.
X-Incus-type can now be `symlink` with the request content being the target path.

## `container_push_target`

This adds the `target` field to `POST /1.0/containers/<name>` which can be
used to have the source Incus host connect to the target during migration.

## `network_vlan_physical`

Allows use of `vlan` property with `physical` network devices.

When set, this will instruct Incus to attach to the specified VLAN on the `parent` interface.
Incus will look for an existing interface for that `parent` and VLAN on the host.
If one can't be found it will create one itself.
Then, Incus will directly attach this interface to the container.

## `storage_images_delete`

This enabled the storage API to delete storage volumes for images from
a specific storage pool.

## `container_edit_metadata`

This adds support for editing a container `metadata.yaml` and related templates
via API, by accessing URLs under `/1.0/containers/<name>/metadata`. It can be used
to edit a container before publishing an image from it.

## `container_snapshot_stateful_migration`

This enables migrating stateful container snapshots to new containers.

## `storage_driver_ceph`

This adds a Ceph storage driver.

## `storage_ceph_user_name`

This adds the ability to specify the Ceph user.

## `instance_types`

This adds the `instance_type` field to the container creation request.
Its value is expanded to Incus resource limits.

## `storage_volatile_initial_source`

This records the actual source passed to Incus during storage pool creation.

## `storage_ceph_force_osd_reuse`

This introduces the `ceph.osd.force_reuse` property for the Ceph storage
driver. When set to `true` Incus will reuse an OSD storage pool that is already in
use by another Incus instance.

## `storage_block_filesystem_btrfs`

This adds support for Btrfs as a storage volume file system, in addition to `ext4`
and `xfs`.

## `resources`

This adds support for querying a Incus daemon for the system resources it has
available.

## `kernel_limits`

This adds support for setting process limits such as maximum number of open
files for the container via `nofile`. The format is `limits.kernel.[limit name]`.

## `storage_api_volume_rename`

This adds support for renaming custom storage volumes.

## `network_sriov`

This adds support for SR-IOV enabled network devices.

## `console`

This adds support to interact with the container console device and console log.

## `restrict_dev_incus`

A new `security.guestapi` container configuration key was introduced.
The key controls whether the `/dev/incus` interface is made available to the container.
If set to `false`, this effectively prevents the container from interacting with the Incus daemon.

## `migration_pre_copy`

This adds support for optimized memory transfer during live migration.

## `infiniband`

This adds support to use InfiniBand network devices.

## `dev_incus_events`

This adds a WebSocket API to the `/dev/incus` socket.

When connecting to `/1.0/events` over the `/dev/incus` socket, you will now be
getting a stream of events over WebSocket.

## `proxy`

This adds a new `proxy` device type to containers, allowing forwarding
of connections between the host and container.

## `network_dhcp_gateway`

Introduces a new `ipv4.dhcp.gateway` network configuration key to set an alternate gateway.

## `file_get_symlink`

This makes it possible to retrieve symlinks using the file API.

## `network_leases`

Adds a new `/1.0/networks/NAME/leases` API endpoint to query the lease database on
bridges which run a Incus-managed DHCP server.

## `unix_device_hotplug`

This adds support for the `required` property for Unix devices.

## `storage_api_local_volume_handling`

This add the ability to copy and move custom storage volumes locally in the
same and between storage pools.

## `operation_description`

Adds a `description` field to all operations.

## `clustering`

Clustering API for Incus.

This includes the following new endpoints (see [RESTful API](rest-api.md) for details):

* `GET /1.0/cluster`
* `UPDATE /1.0/cluster`

* `GET /1.0/cluster/members`

* `GET /1.0/cluster/members/<name>`
* `POST /1.0/cluster/members/<name>`
* `DELETE /1.0/cluster/members/<name>`

The following existing endpoints have been modified:

* `POST /1.0/containers` accepts a new `target` query parameter
* `POST /1.0/storage-pools` accepts a new `target` query parameter
* `GET /1.0/storage-pool/<name>` accepts a new `target` query parameter
* `POST /1.0/storage-pool/<pool>/volumes/<type>` accepts a new `target` query parameter
* `GET /1.0/storage-pool/<pool>/volumes/<type>/<name>` accepts a new `target` query parameter
* `POST /1.0/storage-pool/<pool>/volumes/<type>/<name>` accepts a new `target` query parameter
* `PUT /1.0/storage-pool/<pool>/volumes/<type>/<name>` accepts a new `target` query parameter
* `PATCH /1.0/storage-pool/<pool>/volumes/<type>/<name>` accepts a new `target` query parameter
* `DELETE /1.0/storage-pool/<pool>/volumes/<type>/<name>` accepts a new `target` query parameter
* `POST /1.0/networks` accepts a new `target` query parameter
* `GET /1.0/networks/<name>` accepts a new `target` query parameter

## `event_lifecycle`

This adds a new `lifecycle` message type to the events API.

## `storage_api_remote_volume_handling`

This adds the ability to copy and move custom storage volumes between remote.

## `nvidia_runtime`

Adds a `nvidia_runtime` configuration option for containers, setting this to
`true` will have the NVIDIA runtime and CUDA libraries passed to the
container.

## `container_mount_propagation`

This adds a new `propagation` option to the disk device type, allowing
the configuration of kernel mount propagation.

## `container_backup`

Add container backup support.

This includes the following new endpoints (see [RESTful API](rest-api.md) for details):

* `GET /1.0/containers/<name>/backups`
* `POST /1.0/containers/<name>/backups`

* `GET /1.0/containers/<name>/backups/<name>`
* `POST /1.0/containers/<name>/backups/<name>`
* `DELETE /1.0/containers/<name>/backups/<name>`

* `GET /1.0/containers/<name>/backups/<name>/export`

The following existing endpoint has been modified:

* `POST /1.0/containers` accepts the new source type `backup`

## `dev_incus_images`

Adds a `security.guestapi.images` configuration option for containers which
controls the availability of a `/1.0/images/FINGERPRINT/export` API over
`/dev/incus`. This can be used by a container running nested Incus to access raw
images from the host.

## `container_local_cross_pool_handling`

This enables copying or moving containers between storage pools on the same Incus
instance.

## `proxy_unix`

Add support for both Unix sockets and abstract Unix sockets in proxy devices.
They can be used by specifying the address as `unix:/path/to/unix.sock` (normal
socket) or `unix:@/tmp/unix.sock` (abstract socket).

Supported connections are now:

* `TCP <-> TCP`
* `UNIX <-> UNIX`
* `TCP <-> UNIX`
* `UNIX <-> TCP`

## `proxy_udp`

Add support for UDP in proxy devices.

Supported connections are now:

* `TCP <-> TCP`
* `UNIX <-> UNIX`
* `TCP <-> UNIX`
* `UNIX <-> TCP`
* `UDP <-> UDP`
* `TCP <-> UDP`
* `UNIX <-> UDP`

## `clustering_join`

This makes `GET /1.0/cluster` return information about which storage pools and
networks are required to be created by joining nodes and which node-specific
configuration keys they are required to use when creating them. Likewise the `PUT
/1.0/cluster` endpoint now accepts the same format to pass information about
storage pools and networks to be automatically created before attempting to join
a cluster.

## `proxy_tcp_udp_multi_port_handling`

Adds support for forwarding traffic for multiple ports. Forwarding is allowed
between a range of ports if the port range is equal for source and target
(for example `1.2.3.4 0-1000 -> 5.6.7.8 1000-2000`) and between a range of source
ports and a single target port (for example `1.2.3.4 0-1000 -> 5.6.7.8 1000`).

## `network_state`

Adds support for retrieving a network's state.

This adds the following new endpoint (see [RESTful API](rest-api.md) for details):

* `GET /1.0/networks/<name>/state`

## `proxy_unix_dac_properties`

This adds support for GID, UID, and mode properties for non-abstract Unix
sockets.

## `container_protection_delete`

Enables setting the `security.protection.delete` field which prevents containers
from being deleted if set to `true`. Snapshots are not affected by this setting.

## `proxy_priv_drop`

Adds `security.uid` and `security.gid` for the proxy devices, allowing
privilege dropping and effectively changing the UID/GID used for
connections to Unix sockets too.

## `pprof_http`

This adds a new `core.debug_address` configuration option to start a debugging HTTP server.

That server currently includes a `pprof` API and replaces the old
`cpu-profile`, `memory-profile` and `print-goroutines` debug options.

## `proxy_haproxy_protocol`

Adds a `proxy_protocol` key to the proxy device which controls the use of the HAProxy PROXY protocol header.

## `network_hwaddr`

Adds a `bridge.hwaddr` key to control the MAC address of the bridge.

## `proxy_nat`

This adds optimized UDP/TCP proxying. If the configuration allows, proxying
will be done via `iptables` instead of proxy devices.

## `network_nat_order`

This introduces the `ipv4.nat.order` and `ipv6.nat.order` configuration keys for Incus bridges.
Those keys control whether to put the Incus rules before or after any pre-existing rules in the chain.

## `container_full`

This introduces a new `recursion=2` mode for `GET /1.0/containers` which allows for the retrieval of
all container structs, including the state, snapshots and backup structs.

This effectively allows for [`incus list`](incus_list.md) to get all it needs in one query.

## `backup_compression`

This introduces a new `backups.compression_algorithm` configuration key which
allows configuration of backup compression.

## `nvidia_runtime_config`

This introduces a few extra configuration keys when using `nvidia.runtime` and the `libnvidia-container` library.
Those keys translate pretty much directly to the matching NVIDIA container environment variables:

* `nvidia.driver.capabilities` => `NVIDIA_DRIVER_CAPABILITIES`
* `nvidia.require.cuda` => `NVIDIA_REQUIRE_CUDA`
* `nvidia.require.driver` => `NVIDIA_REQUIRE_DRIVER`

## `storage_api_volume_snapshots`

Add support for storage volume snapshots. They work like container snapshots,
only for volumes.

This adds the following new endpoint (see [RESTful API](rest-api.md) for details):

* `GET /1.0/storage-pools/<pool>/volumes/<type>/<name>/snapshots`
* `POST /1.0/storage-pools/<pool>/volumes/<type>/<name>/snapshots`

* `GET /1.0/storage-pools/<pool>/volumes/<type>/<volume>/snapshots/<name>`
* `PUT /1.0/storage-pools/<pool>/volumes/<type>/<volume>/snapshots/<name>`
* `POST /1.0/storage-pools/<pool>/volumes/<type>/<volume>/snapshots/<name>`
* `DELETE /1.0/storage-pools/<pool>/volumes/<type>/<volume>/snapshots/<name>`

## `storage_unmapped`

Introduces a new `security.unmapped` Boolean on storage volumes.

Setting it to `true` will flush the current map on the volume and prevent
any further idmap tracking and remapping on the volume.

This can be used to share data between isolated containers after
attaching it to the container which requires write access.

## `projects`

Add a new project API, supporting creation, update and deletion of projects.

Projects can hold containers, profiles or images at this point and let
you get a separate view of your Incus resources by switching to it.

## `network_vxlan_ttl`

This adds a new `tunnel.NAME.ttl` network configuration option which
makes it possible to raise the TTL on VXLAN tunnels.

## `container_incremental_copy`

This adds support for incremental container copy. When copying a container
using the `--refresh` flag, only the missing or outdated files will be
copied over. Should the target container not exist yet, a normal copy operation
is performed.

## `usb_optional_vendorid`

As the name implies, the `vendorid` field on USB devices attached to
containers has now been made optional, allowing for all USB devices to
be passed to a container (similar to what's done for GPUs).

## `snapshot_scheduling`

This adds support for snapshot scheduling. It introduces three new
configuration keys: `snapshots.schedule`, `snapshots.schedule.stopped`, and
`snapshots.pattern`. Snapshots can be created automatically up to every minute.

## `snapshots_schedule_aliases`

Snapshot schedule can be configured by a comma-separated list of schedule aliases.
Available aliases are `<@hourly> <@daily> <@midnight> <@weekly> <@monthly> <@annually> <@yearly> <@startup>` for instances,
and `<@hourly> <@daily> <@midnight> <@weekly> <@monthly> <@annually> <@yearly>` for storage volumes.

## `container_copy_project`

Introduces a `project` field to the container source JSON object, allowing for
copy/move of containers between projects.

## `clustering_server_address`

This adds support for configuring a server network address which differs from
the REST API client network address. When bootstrapping a new cluster, clients
can set the new `cluster.https_address` configuration key to specify the address of
the initial server. When joining a new server, clients can set the
`core.https_address` configuration key of the joining server to the REST API
address the joining server should listen at, and set the `server_address`
key in the `PUT /1.0/cluster` API to the address the joining server should
use for clustering traffic (the value of `server_address` will be
automatically copied to the `cluster.https_address` configuration key of the
joining server).

## `clustering_image_replication`

Enable image replication across the nodes in the cluster.
A new `cluster.images_minimal_replica` configuration key was introduced can be used
to specify to the minimal numbers of nodes for image replication.

## `container_protection_shift`

Enables setting the `security.protection.shift` option which prevents containers
from having their file system shifted.

## `snapshot_expiry`

This adds support for snapshot expiration. The task is run minutely. The configuration
option `snapshots.expiry` takes an expression in the form of `1M 2H 3d 4w 5m
6y` (1 minute, 2 hours, 3 days, 4 weeks, 5 months, 6 years), however not all
parts have to be used.

Snapshots which are then created will be given an expiry date based on the
expression. This expiry date, defined by `expires_at`, can be manually edited
using the API or [`incus config edit`](incus_config_edit.md). Snapshots with a valid expiry date will be
removed when the task in run. Expiry can be disabled by setting `expires_at` to
an empty string or `0001-01-01T00:00:00Z` (zero time). This is the default if
`snapshots.expiry` is not set.

This adds the following new endpoint (see [RESTful API](rest-api.md) for details):

* `PUT /1.0/containers/<name>/snapshots/<name>`

## `snapshot_expiry_creation`

Adds `expires_at` to container creation, allowing for override of a
snapshot's expiry at creation time.

## `network_leases_location`

Introduces a `Location` field in the leases list.
This is used when querying a cluster to show what node a particular
lease was found on.

## `resources_cpu_socket`

Add Socket field to CPU resources in case we get out of order socket information.

## `resources_gpu`

Add a new GPU struct to the server resources, listing all usable GPUs on the system.

## `resources_numa`

Shows the NUMA node for all CPUs and GPUs.

## `kernel_features`

Exposes the state of optional kernel features through the server environment.

## `id_map_current`

This introduces a new internal `volatile.idmap.current` key which is
used to track the current mapping for the container.

This effectively gives us:

* `volatile.last_state.idmap` => On-disk idmap
* `volatile.idmap.current` => Current kernel map
* `volatile.idmap.next` => Next on-disk idmap

This is required to implement environments where the on-disk map isn't
changed but the kernel map is (e.g. `shiftfs`).

## `event_location`

Expose the location of the generation of API events.

## `storage_api_remote_volume_snapshots`

This allows migrating storage volumes including their snapshots.

## `network_nat_address`

This introduces the `ipv4.nat.address` and `ipv6.nat.address` configuration keys for Incus bridges.
Those keys control the source address used for outbound traffic from the bridge.

## `container_nic_routes`

This introduces the `ipv4.routes` and `ipv6.routes` properties on `nic` type devices.
This allows adding static routes on host to container's NIC.

## `cluster_internal_copy`

This makes it possible to do a normal `POST /1.0/containers` to copy a
container between cluster nodes with Incus internally detecting whether a
migration is required.

## `seccomp_notify`

If the kernel supports `seccomp`-based syscall interception Incus can be notified
by a container that a registered syscall has been performed. Incus can then
decide to trigger various actions.

## `lxc_features`

This introduces the `lxc_features` section output from the [`incus info`](incus_info.md) command
via the `GET /1.0` route. It outputs the result of checks for key features being present in the
underlying LXC library.

## `container_nic_ipvlan`

This introduces the `ipvlan` `nic` device type.

## `network_vlan_sriov`

This introduces VLAN (`vlan`) and MAC filtering (`security.mac_filtering`) support for SR-IOV devices.

## `storage_cephfs`

Add support for CephFS as a storage pool driver. This can only be used
for custom volumes, images and containers should be on Ceph (RBD)
instead.

## `container_nic_ipfilter`

This introduces container IP filtering (`security.ipv4_filtering` and `security.ipv6_filtering`) support for `bridged` NIC devices.

## `resources_v2`

Rework the resources API at `/1.0/resources`, especially:

* CPU
   * Fix reporting to track sockets, cores and threads
   * Track NUMA node per core
   * Track base and turbo frequency per socket
   * Track current frequency per core
   * Add CPU cache information
   * Export the CPU architecture
   * Show online/offline status of threads
* Memory
   * Add huge-pages tracking
   * Track memory consumption per NUMA node too
* GPU
   * Split DRM information to separate struct
   * Export device names and nodes in DRM struct
   * Export device name and node in NVIDIA struct
   * Add SR-IOV VF tracking

## `container_exec_user_group_cwd`

Adds support for specifying `User`, `Group` and `Cwd` during `POST /1.0/containers/NAME/exec`.

## `container_syscall_intercept`

Adds the `security.syscalls.intercept.*` configuration keys to control
what system calls will be intercepted by Incus and processed with
elevated permissions.

## `container_disk_shift`

Adds the `shift` property on `disk` devices which controls the use of the `shiftfs` overlay.

## `storage_shifted`

Introduces a new `security.shifted` Boolean on storage volumes.

Setting it to `true` will allow multiple isolated containers to attach the
same storage volume while keeping the file system writable from all of
them.

This makes use of `shiftfs` as an overlay file system.

## `resources_infiniband`

Export InfiniBand character device information (`issm`, `umad`, `uverb`) as part of the resources API.

## `daemon_storage`

This introduces two new configuration keys `storage.images_volume` and
`storage.backups_volume` to allow for a storage volume on an existing
pool be used for storing the daemon-wide images and backups artifacts.

## `instances`

This introduces the concept of instances, of which currently the only type is `container`.

## `image_types`

This introduces support for a new Type field on images, indicating what type of images they are.

## `resources_disk_sata`

Extends the disk resource API struct to include:

* Proper detection of SATA devices (type)
* Device path
* Drive RPM
* Block size
* Firmware version
* Serial number

## `clustering_roles`

This adds a new `roles` attribute to cluster entries, exposing a list of
roles that the member serves in the cluster.

## `images_expiry`

This allows for editing of the expiry date on images.

## `resources_network_firmware`

Adds a `FirmwareVersion` field to network card entries.

## `backup_compression_algorithm`

This adds support for a `compression_algorithm` property when creating a backup (`POST /1.0/containers/<name>/backups`).

Setting this property overrides the server default value (`backups.compression_algorithm`).

## `ceph_data_pool_name`

This adds support for an optional argument (`ceph.osd.data_pool_name`) when creating
storage pools using Ceph RBD, when this argument is used the pool will store it's
actual data in the pool specified with `data_pool_name` while keeping the metadata
in the pool specified by `pool_name`.

## `container_syscall_intercept_mount`

Adds the `security.syscalls.intercept.mount`,
`security.syscalls.intercept.mount.allowed`, and
`security.syscalls.intercept.mount.shift` configuration keys to control whether
and how the `mount` system call will be intercepted by Incus and processed with
elevated permissions.

## `compression_squashfs`

Adds support for importing/exporting of images/backups using SquashFS file system format.

## `container_raw_mount`

This adds support for passing in raw mount options for disk devices.

## `container_nic_routed`

This introduces the `routed` `nic` device type.

## `container_syscall_intercept_mount_fuse`

Adds the `security.syscalls.intercept.mount.fuse` key. It can be used to
redirect file-system mounts to their fuse implementation. To this end, set e.g.
`security.syscalls.intercept.mount.fuse=ext4=fuse2fs`.

## `container_disk_ceph`

This allows for existing a Ceph RBD or CephFS to be directly connected to a Incus container.

## `virtual-machines`

Add virtual machine support.

## `image_profiles`

Allows a list of profiles to be applied to an image when launching a new container.

## `clustering_architecture`

This adds a new `architecture` attribute to cluster members which indicates a cluster
member's architecture.

## `resources_disk_id`

Add a new `device_id` field in the disk entries on the resources API.

## `storage_lvm_stripes`

This adds the ability to use LVM stripes on normal volumes and thin pool volumes.

## `vm_boot_priority`

Adds a `boot.priority` property on NIC and disk devices to control the boot order.

## `unix_hotplug_devices`

Adds support for Unix char and block device hotplugging.

## `api_filtering`

Adds support for filtering the result of a GET request for instances and images.

## `instance_nic_network`

Adds support for the `network` property on a NIC device to allow a NIC to be linked to a managed network.
This allows it to inherit some of the network's settings and allows better validation of IP settings.

## `clustering_sizing`

Support specifying a custom values for database voters and standbys.
The new `cluster.max_voters` and `cluster.max_standby` configuration keys were introduced
to specify to the ideal number of database voter and standbys.

## `firewall_driver`

Adds the `Firewall` property to the `ServerEnvironment` struct indicating the firewall driver being used.

## `storage_lvm_vg_force_reuse`

Introduces the ability to create a storage pool from an existing non-empty volume group.
This option should be used with care, as Incus can then not guarantee that volume name conflicts won't occur
with non-Incus created volumes in the same volume group.
This could also potentially lead to Incus deleting a non-Incus volume should name conflicts occur.

## `container_syscall_intercept_hugetlbfs`

When mount syscall interception is enabled and `hugetlbfs` is specified as an
allowed file system type Incus will mount a separate `hugetlbfs` instance for the
container with the UID and GID mount options set to the container's root UID
and GID. This ensures that processes in the container can use huge pages.

## `limits_hugepages`

This allows to limit the number of huge pages a container can use through the
`hugetlb` cgroup. This means the `hugetlb` cgroup needs to be available. Note, that
limiting huge pages is recommended when intercepting the mount syscall for the
`hugetlbfs` file system to avoid allowing the container to exhaust the host's
huge pages resources.

## `container_nic_routed_gateway`

This introduces the `ipv4.gateway` and `ipv6.gateway` NIC configuration keys that can take a value of either `auto` or
`none`. The default value for the key if unspecified is `auto`. This will cause the current behavior of a default
gateway being added inside the container and the same gateway address being added to the host-side interface.
If the value is set to `none` then no default gateway nor will the address be added to the host-side interface.
This allows multiple routed NIC devices to be added to a container.

## `projects_restrictions`

This introduces support for the `restricted` configuration key on project, which
can prevent the use of security-sensitive features in a project.

## `custom_volume_snapshot_expiry`

This allows custom volume snapshots to expiry.
Expiry dates can be set individually, or by setting the `snapshots.expiry` configuration key on the parent custom volume which then automatically applies to all created snapshots.

## `volume_snapshot_scheduling`

This adds support for custom volume snapshot scheduling. It introduces two new
configuration keys: `snapshots.schedule` and
`snapshots.pattern`. Snapshots can be created automatically up to every minute.

## `trust_ca_certificates`

This allows for checking client certificates trusted by the provided CA (`server.ca`).
It can be enabled by setting `core.trust_ca_certificates` to `true`.
If enabled, it will perform the check, and bypass the trusted password if `true`.
An exception will be made if the connecting client certificate is in the provided CRL (`ca.crl`).
In this case, it will ask for the password.

## `snapshot_disk_usage`

This adds a new `size` field to the output of `/1.0/instances/<name>/snapshots/<snapshot>` which represents the disk usage of the snapshot.

## `clustering_edit_roles`

This adds a writable endpoint for cluster members, allowing the editing of their roles.

## `container_nic_routed_host_address`

This introduces the `ipv4.host_address` and `ipv6.host_address` NIC configuration keys that can be used to control the
host-side `veth` interface's IP addresses. This can be useful when using multiple routed NICs at the same time and
needing a predictable next-hop address to use.

This also alters the behavior of `ipv4.gateway` and `ipv6.gateway` NIC configuration keys. When they are set to `auto`
the container will have its default gateway set to the value of `ipv4.host_address` or `ipv6.host_address` respectively.

The default values are:

`ipv4.host_address`: `169.254.0.1`
`ipv6.host_address`: `fe80::1`

This is backward compatible with the previous default behavior.

## `container_nic_ipvlan_gateway`

This introduces the `ipv4.gateway` and `ipv6.gateway` NIC configuration keys that can take a value of either `auto` or
`none`. The default value for the key if unspecified is `auto`. This will cause the current behavior of a default
gateway being added inside the container and the same gateway address being added to the host-side interface.
If the value is set to `none` then no default gateway nor will the address be added to the host-side interface.
This allows multiple IPVLAN NIC devices to be added to a container.

## `resources_usb_pci`

This adds USB and PCI devices to the output of `/1.0/resources`.

## `resources_cpu_threads_numa`

This indicates that the `numa_node` field is now recorded per-thread
rather than per core as some hardware apparently puts threads in
different NUMA domains.

## `resources_cpu_core_die`

Exposes the `die_id` information on each core.

## `api_os`

This introduces two new fields in `/1.0`, `os` and `os_version`.

Those are taken from the OS-release data on the system.

## `container_nic_routed_host_table`

This introduces the `ipv4.host_table` and `ipv6.host_table` NIC configuration keys that can be used to add static routes
for the instance's IPs to a custom policy routing table by ID.

## `container_nic_ipvlan_host_table`

This introduces the `ipv4.host_table` and `ipv6.host_table` NIC configuration keys that can be used to add static routes
for the instance's IPs to a custom policy routing table by ID.

## `container_nic_ipvlan_mode`

This introduces the `mode` NIC configuration key that can be used to switch the `ipvlan` mode into either `l2` or `l3s`.
If not specified, the default value is `l3s` (which is the old behavior).

In `l2` mode the `ipv4.address` and `ipv6.address` keys will accept addresses in either CIDR or singular formats.
If singular format is used, the default subnet size is taken to be /24 and /64 for IPv4 and IPv6 respectively.

In `l2` mode the `ipv4.gateway` and `ipv6.gateway` keys accept only a singular IP address.

## `resources_system`

This adds system information to the output of `/1.0/resources`.

## `images_push_relay`

This adds the push and relay modes to image copy.
It also introduces the following new endpoint:

* `POST 1.0/images/<fingerprint>/export`

## `network_dns_search`

This introduces the `dns.search` configuration option on networks.

## `container_nic_routed_limits`

This introduces `limits.ingress`, `limits.egress` and `limits.max` for routed NICs.

## `instance_nic_bridged_vlan`

This introduces the `vlan` and `vlan.tagged` settings for `bridged` NICs.

`vlan` specifies the non-tagged VLAN to join, and `vlan.tagged` is a comma-delimited list of tagged VLANs to join.

## `network_state_bond_bridge`

This adds a `bridge` and `bond` section to the `/1.0/networks/NAME/state` API.

Those contain additional state information relevant to those particular types.

Bond:

* Mode
* Transmit hash
* Up delay
* Down delay
* MII frequency
* MII state
* Lower devices

Bridge:

* ID
* Forward delay
* STP mode
* Default VLAN
* VLAN filtering
* Upper devices

## `resources_cpu_isolated`

Add an `Isolated` property on CPU threads to indicate if the thread is
physically `Online` but is configured not to accept tasks.

## `usedby_consistency`

This extension indicates that `UsedBy` should now be consistent with
suitable `?project=` and `?target=` when appropriate.

The 5 entities that have `UsedBy` are:

* Profiles
* Projects
* Networks
* Storage pools
* Storage volumes

## `custom_block_volumes`

This adds support for creating and attaching custom block volumes to instances.
It introduces the new `--type` flag when creating custom storage volumes, and accepts the values `fs` and `block`.

## `clustering_failure_domains`

This extension adds a new `failure_domain` field to the `PUT /1.0/cluster/<node>` API,
which can be used to set the failure domain of a node.

## `container_syscall_filtering_allow_deny_syntax`

A number of new syscalls related container configuration keys were updated.

* `security.syscalls.deny_default`
* `security.syscalls.deny_compat`
* `security.syscalls.deny`
* `security.syscalls.allow`

## `resources_gpu_mdev`

Expose available mediated device profiles and devices in `/1.0/resources`.

## `console_vga_type`

This extends the `/1.0/console` endpoint to take a `?type=` argument, which can
be set to `console` (default) or `vga` (the new type added by this extension).

When doing a `POST` to `/1.0/<instance name>/console?type=vga` the data WebSocket
returned by the operation in the metadata field will be a bidirectional proxy
attached to a SPICE Unix socket of the target virtual machine.

## `projects_limits_disk`

Add `limits.disk` to the available project configuration keys. If set, it limits
the total amount of disk space that instances volumes, custom volumes and images
volumes can use in the project.

## `network_type_macvlan`

Adds support for additional network type `macvlan` and adds `parent` configuration key for this network type to
specify which parent interface should be used for creating NIC device interfaces on top of.

Also adds `network` configuration key support for `macvlan` NICs to allow them to specify the associated network of
the same type that they should use as the basis for the NIC device.

## `network_type_sriov`

Adds support for additional network type `sriov` and adds `parent` configuration key for this network type to
specify which parent interface should be used for creating NIC device interfaces on top of.

Also adds `network` configuration key support for `sriov` NICs to allow them to specify the associated network of
the same type that they should use as the basis for the NIC device.

## `container_syscall_intercept_bpf_devices`

This adds support to intercept the `bpf` syscall in containers. Specifically, it allows to manage device cgroup `bpf` programs.

## `network_type_ovn`

Adds support for additional network type `ovn` with the ability to specify a `bridge` type network as the `parent`.

Introduces a new NIC device type of `ovn` which allows the `network` configuration key to specify which `ovn`
type network they should connect to.

Also introduces two new global configuration keys that apply to all `ovn` networks and NIC devices:

* `network.ovn.integration_bridge` - the OVS integration bridge to use.
* `network.ovn.northbound_connection` - the OVN northbound database connection string.

## `projects_networks`

Adds the `features.networks` configuration key to projects and the ability for a project to hold networks.

## `projects_networks_restricted_uplinks`

Adds the `restricted.networks.uplinks` project configuration key to indicate (as a comma-delimited list) which networks
the networks created inside the project can use as their uplink network.

## `custom_volume_backup`

Add custom volume backup support.

This includes the following new endpoints (see [RESTful API](rest-api.md) for details):

* `GET /1.0/storage-pools/<pool>/<type>/<volume>/backups`
* `POST /1.0/storage-pools/<pool>/<type>/<volume>/backups`

* `GET /1.0/storage-pools/<pool>/<type>/<volume>/backups/<name>`
* `POST /1.0/storage-pools/<pool>/<type>/<volume>/backups/<name>`
* `DELETE /1.0/storage-pools/<pool>/<type>/<volume>/backups/<name>`

* `GET /1.0/storage-pools/<pool>/<type>/<volume>/backups/<name>/export`

The following existing endpoint has been modified:

* `POST /1.0/storage-pools/<pool>/<type>/<volume>` accepts the new source type `backup`

## `backup_override_name`

Adds `Name` field to `InstanceBackupArgs` to allow specifying a different instance name when restoring a backup.

Adds `Name` and `PoolName` fields to `StoragePoolVolumeBackupArgs` to allow specifying a different volume name
when restoring a custom volume backup.

## `storage_rsync_compression`

Adds `rsync.compression` configuration key to storage pools. This key can be used
to disable compression in `rsync` while migrating storage pools.

## `network_type_physical`

Adds support for additional network type `physical` that can be used as an uplink for `ovn` networks.

The interface specified by `parent` on the `physical` network will be connected to the `ovn` network's gateway.

## `network_ovn_external_subnets`

Adds support for `ovn` networks to use external subnets from uplink networks.

Introduces the `ipv4.routes` and `ipv6.routes` setting on `physical` networks that defines the external routes
allowed to be used in child OVN networks in their `ipv4.routes.external` and `ipv6.routes.external` settings.

Introduces the `restricted.networks.subnets` project setting that specifies which external subnets are allowed to
be used by OVN networks inside the project (if not set then all routes defined on the uplink network are allowed).

## `network_ovn_nat`

Adds support for `ipv4.nat` and `ipv6.nat` settings on `ovn` networks.

When creating the network if these settings are unspecified, and an equivalent IP address is being generated for
the subnet, then the appropriate NAT setting will added set to `true`.

If the setting is missing then the value is taken as `false`.

## `network_ovn_external_routes_remove`

Removes the settings `ipv4.routes.external` and `ipv6.routes.external` from `ovn` networks.

The equivalent settings on the `ovn` NIC type can be used instead for this, rather than having to specify them
both at the network and NIC level.

## `tpm_device_type`

This introduces the `tpm` device type.

## `storage_zfs_clone_copy_rebase`

This introduces `rebase` as a value for `zfs.clone_copy` causing Incus to
track down any `image` dataset in the ancestry line and then perform
send/receive on top of that.

## `gpu_mdev`

This adds support for virtual GPUs. It introduces the `mdev` configuration key for GPU devices which takes
a supported `mdev` type, e.g. `i915-GVTg_V5_4`.

## `resources_pci_iommu`

This adds the `IOMMUGroup` field for PCI entries in the resources API.

## `resources_network_usb`

Adds the `usb_address` field to the network card entries in the resources API.

## `resources_disk_address`

Adds the `usb_address` and `pci_address` fields to the disk entries in the resources API.

## `network_physical_ovn_ingress_mode`

Adds `ovn.ingress_mode` setting for `physical` networks.

Sets the method that OVN NIC external IPs will be advertised on uplink network.

Either `l2proxy` (proxy ARP/NDP) or `routed`.

## `network_ovn_dhcp`

Adds `ipv4.dhcp` and `ipv6.dhcp` settings for `ovn` networks.

Allows DHCP (and RA for IPv6) to be disabled. Defaults to on.

## `network_physical_routes_anycast`

Adds `ipv4.routes.anycast` and `ipv6.routes.anycast` Boolean settings for `physical` networks. Defaults to `false`.

Allows OVN networks using physical network as uplink to relax external subnet/route overlap detection when used
with `ovn.ingress_mode=routed`.

## `projects_limits_instances`

Adds `limits.instances` to the available project configuration keys. If set, it
limits the total number of instances (VMs and containers) that can be used in the project.

## `network_state_vlan`

This adds a `vlan` section to the `/1.0/networks/NAME/state` API.

Those contain additional state information relevant to VLAN interfaces:

* `lower_device`
* `vid`

## `instance_nic_bridged_port_isolation`

This adds the `security.port_isolation` field for bridged NIC instances.

## `instance_bulk_state_change`

Adds the following endpoint for bulk state change (see [RESTful API](rest-api.md) for details):

* `PUT /1.0/instances`

## `network_gvrp`

This adds an optional `gvrp` property to `macvlan` and `physical` networks,
and to `ipvlan`, `macvlan`, `routed` and `physical` NIC devices.

When set, this specifies whether the VLAN should be registered using GARP VLAN
Registration Protocol. Defaults to `false`.

## `instance_pool_move`

This adds a `pool` field to the `POST /1.0/instances/NAME` API,
allowing for easy move of an instance root disk between pools.

## `gpu_sriov`

This adds support for SR-IOV enabled GPUs.
It introduces the `sriov` GPU type property.

## `pci_device_type`

This introduces the `pci` device type.

## `storage_volume_state`

Add new `/1.0/storage-pools/POOL/volumes/VOLUME/state` API endpoint to get usage data on a volume.

## `network_acl`

This adds the concept of network ACLs to API under the API endpoint prefix `/1.0/network-acls`.

## `migration_stateful`

Add a new `migration.stateful` configuration key.

## `disk_state_quota`

This introduces the `size.state` device configuration key on `disk` devices.

## `storage_ceph_features`

Adds a new `ceph.rbd.features` configuration key on storage pools to control the RBD features used for new volumes.

## `projects_compression`

Adds new `backups.compression_algorithm` and `images.compression_algorithm` configuration keys which
allows configuration of backup and image compression per-project.

## `projects_images_remote_cache_expiry`

Add new `images.remote_cache_expiry` configuration key to projects,
allowing for set number of days after which an unused cached remote image will be flushed.

## `certificate_project`

Adds a new `restricted` property to certificates in the API as well as
`projects` holding a list of project names that the certificate has
access to.

## `network_ovn_acl`

Adds a new `security.acls` property to OVN networks and OVN NICs, allowing Network ACLs to be applied.

## `projects_images_auto_update`

Adds new `images.auto_update_cached` and `images.auto_update_interval` configuration keys which
allows configuration of images auto update in projects

## `projects_restricted_cluster_target`

Adds new `restricted.cluster.target` configuration key to project which prevent the user from using --target
to specify what cluster member to place a workload on or the ability to move a workload between members.

## `images_default_architecture`

Adds new `images.default_architecture` global configuration key and matching per-project key which lets user tell Incus
what architecture to go with when no specific one is specified as part of the image request.

## `network_ovn_acl_defaults`

Adds new `security.acls.default.{in,e}gress.action` and `security.acls.default.{in,e}gress.logged` configuration keys for
OVN networks and NICs. This replaces the removed ACL `default.action` and `default.logged` keys.

## `gpu_mig`

This adds support for NVIDIA MIG. It introduces the `mig` GPU type and associated configuration keys.

## `project_usage`

Adds an API endpoint to get current resource allocations in a project.
Accessible at API `GET /1.0/projects/<name>/state`.

## `network_bridge_acl`

Adds a new `security.acls` configuration key to `bridge` networks, allowing Network ACLs to be applied.

Also adds `security.acls.default.{in,e}gress.action` and `security.acls.default.{in,e}gress.logged` configuration keys for
specifying the default behavior for unmatched traffic.

## `warnings`

Warning API for Incus.

This includes the following endpoints (see  [Restful API](rest-api.md) for details):

* `GET /1.0/warnings`

* `GET /1.0/warnings/<uuid>`
* `PUT /1.0/warnings/<uuid>`
* `DELETE /1.0/warnings/<uuid>`

## `projects_restricted_backups_and_snapshots`

Adds new `restricted.backups` and `restricted.snapshots` configuration keys to project which
prevents the user from creation of backups and snapshots.

## `clustering_join_token`

Adds `POST /1.0/cluster/members` API endpoint for requesting a join token used when adding new cluster members
without using the trust password.

## `clustering_description`

Adds an editable description to the cluster members.

## `server_trusted_proxy`

This introduces support for `core.https_trusted_proxy` which has Incus
parse a HAProxy style connection header on such connections and if
present, will rewrite the request's source address to that provided by
the proxy server.

## `clustering_update_cert`

Adds `PUT /1.0/cluster/certificate` endpoint for updating the cluster
certificate across the whole cluster

## `storage_api_project`

This adds support for copy/move custom storage volumes between projects.

## `server_instance_driver_operational`

This modifies the `driver` output for the `/1.0` endpoint to only include drivers which are actually supported and
operational on the server (as opposed to being included in Incus but not operational on the server).

## `server_supported_storage_drivers`

This adds supported storage driver info to server environment info.

## `event_lifecycle_requestor_address`

Adds a new address field to `lifecycle` requestor.

## `resources_gpu_usb`

Add a new `USBAddress` (`usb_address`) field to `ResourcesGPUCard` (GPU entries) in the resources API.

## `clustering_evacuation`

Adds `POST /1.0/cluster/members/<name>/state` endpoint for evacuating and restoring cluster members.
It also adds the configuration keys `cluster.evacuate` and `volatile.evacuate.origin` for setting the evacuation method (`auto`, `stop` or `migrate`) and the origin of any migrated instance respectively.

## `network_ovn_nat_address`

This introduces the `ipv4.nat.address` and `ipv6.nat.address` configuration keys for Incus `ovn` networks.
Those keys control the source address used for outbound traffic from the OVN virtual network.
These keys can only be specified when the OVN network's uplink network has `ovn.ingress_mode=routed`.

## `network_bgp`

This introduces support for Incus acting as a BGP router to advertise
routes to `bridge` and `ovn` networks.

This comes with the addition to global configuration of:

* `core.bgp_address`
* `core.bgp_asn`
* `core.bgp_routerid`

The following network configurations keys (`bridge` and `physical`):

* `bgp.peers.<name>.address`
* `bgp.peers.<name>.asn`
* `bgp.peers.<name>.password`

The `nexthop` configuration keys (`bridge`):

* `bgp.ipv4.nexthop`
* `bgp.ipv6.nexthop`

And the following NIC-specific configuration keys (`bridged` NIC type):

* `ipv4.routes.external`
* `ipv6.routes.external`

## `network_forward`

This introduces the networking address forward functionality. Allowing for `bridge` and `ovn` networks to define
external IP addresses that can be forwarded to internal IP(s) inside their respective networks.

## `custom_volume_refresh`

Adds support for refresh during volume migration.

## `network_counters_errors_dropped`

This adds the received and sent errors as well as inbound and outbound dropped packets to the network counters.

## `metrics`

This adds metrics to Incus. It returns metrics of running instances using the OpenMetrics format.

This includes the following endpoints:

* `GET /1.0/metrics`

## `image_source_project`

Adds a new `project` field to `POST /1.0/images` allowing for the source project
to be set at image copy time.

## `clustering_config`

Adds new `config` property to cluster members with configurable key/value pairs.

## `network_peer`

This adds network peering to allow traffic to flow between OVN networks without leaving the OVN subsystem.

## `linux_sysctl`

Adds new `linux.sysctl.*` configuration keys allowing users to modify certain kernel parameters
within containers.

## `network_dns`

Introduces a built-in DNS server and zones API to provide DNS records for Incus instances.

This introduces the following server configuration key:

* `core.dns_address`

The following network configuration key:

* `dns.zone.forward`
* `dns.zone.reverse.ipv4`
* `dns.zone.reverse.ipv6`

And the following project configuration key:

* `restricted.networks.zones`

A new REST API is also introduced to manage DNS zones:

* `/1.0/network-zones` (GET, POST)
* `/1.0/network-zones/<name>` (GET, PUT, PATCH, DELETE)

## `ovn_nic_acceleration`

Adds new `acceleration` configuration key to OVN NICs which can be used for enabling hardware offloading.
It takes the values `none` or `sriov`.

## `certificate_self_renewal`

This adds support for renewing a client's own trust certificate.

## `instance_project_move`

This adds a `project` field to the `POST /1.0/instances/NAME` API,
allowing for easy move of an instance between projects.

## `storage_volume_project_move`

This adds support for moving storage volume between projects.

## `cloud_init`

This adds a new `cloud-init` configuration key namespace which contains the following keys:

* `cloud-init.vendor-data`
* `cloud-init.user-data`
* `cloud-init.network-config`

 It also adds a new endpoint `/1.0/devices` to `/dev/incus` which shows an instance's devices.

## `network_dns_nat`

This introduces `network.nat` as a configuration option on network zones (DNS).

It defaults to the current behavior of generating records for all
instances NICs but if set to `false`, it will instruct Incus to only
generate records for externally reachable addresses.

## `database_leader`

Adds new `database-leader` role which is assigned to cluster leader.

## `instance_all_projects`

This adds support for displaying instances from all projects.

## `clustering_groups`

Add support for grouping cluster members.

This introduces the following new endpoints:

* `/1.0/cluster/groups` (GET, POST)
* `/1.0/cluster/groups/<name>` (GET, POST, PUT, PATCH, DELETE)

 The following project restriction is added:

* `restricted.cluster.groups`

## `ceph_rbd_du`

Adds a new `ceph.rbd.du` Boolean on Ceph storage pools which allows
disabling the use of the potentially slow `rbd du` calls.

## `instance_get_full`

This introduces a new `recursion=1` mode for `GET /1.0/instances/{name}` which allows for the retrieval of
all instance structs, including the state, snapshots and backup structs.

## `qemu_metrics`

This adds a new `security.agent.metrics` Boolean which defaults to `true`.
When set to `false`, it doesn't connect to the `incus-agent` for metrics and other state information, but relies on stats from QEMU.

## `gpu_mig_uuid`

Adds support for the new MIG UUID format used by NVIDIA `470+` drivers (for example, `MIG-74c6a31a-fde5-5c61-973b-70e12346c202`),
the `MIG-` prefix can be omitted

This extension supersedes old `mig.gi` and `mig.ci` parameters which are kept for compatibility with old drivers and
cannot be set together.

## `event_project`

Expose the project an API event belongs to.

## `clustering_evacuation_live`

This adds `live-migrate` as a configuration option to `cluster.evacuate`, which forces live-migration
of instances during cluster evacuation.

## `instance_allow_inconsistent_copy`

Adds `allow_inconsistent` field to instance source on `POST /1.0/instances`. If `true`, `rsync` will ignore the
`Partial transfer due to vanished source files` (code 24) error when creating an instance from a copy.

## `network_state_ovn`

This adds an `ovn` section to the `/1.0/networks/NAME/state` API which contains additional state information relevant to
OVN networks:

* chassis

## `storage_volume_api_filtering`

Adds support for filtering the result of a GET request for storage volumes.

## `image_restrictions`

This extension adds on to the image properties to include image restrictions/host requirements. These requirements
help determine the compatibility between an instance and the host system.

## `storage_zfs_export`

Introduces the ability to disable zpool export when unmounting pool by setting `zfs.export`.

## `network_dns_records`

This extends the network zones (DNS) API to add the ability to create and manage custom records.

This adds:

* `GET /1.0/network-zones/ZONE/records`
* `POST /1.0/network-zones/ZONE/records`
* `GET /1.0/network-zones/ZONE/records/RECORD`
* `PUT /1.0/network-zones/ZONE/records/RECORD`
* `PATCH /1.0/network-zones/ZONE/records/RECORD`
* `DELETE /1.0/network-zones/ZONE/records/RECORD`

## `storage_zfs_reserve_space`

Adds ability to set the `reservation`/`refreservation` ZFS property along with `quota`/`refquota`.

## `network_acl_log`

Adds a new `GET /1.0/networks-acls/NAME/log` API to retrieve ACL firewall logs.

## `storage_zfs_blocksize`

Introduces a new `zfs.blocksize` property for ZFS storage volumes which allows to set volume block size.

## `metrics_cpu_seconds`

This is used to detect whether Incus was fixed to output used CPU time in seconds rather than as milliseconds.

## `instance_snapshot_never`

Adds a `@never` option to `snapshots.schedule` which allows disabling inheritance.

## `certificate_token`

This adds token-based certificate addition to the trust store as a safer alternative to a trust password.

It adds the `token` field to `POST /1.0/certificates`.

## `instance_nic_routed_neighbor_probe`

This adds the ability to disable the `routed` NIC IP neighbor probing for availability on the parent network.

Adds the `ipv4.neighbor_probe` and `ipv6.neighbor_probe` NIC settings. Defaulting to `true` if not specified.

## `event_hub`

This adds support for `event-hub` cluster member role and the `ServerEventMode` environment field.

## `agent_nic_config`

If set to `true`, on VM start-up the `incus-agent` will apply NIC configuration to change the names and MTU of the instance NIC
devices.

## `projects_restricted_intercept`

Adds new `restricted.container.intercept` configuration key to allow usually safe system call interception options.

## `metrics_authentication`

Introduces a new `core.metrics_authentication` server configuration option to
allow for the `/1.0/metrics` endpoint to be generally available without
client authentication.

## `images_target_project`

Adds ability to copy image to a project different from the source.

## `cluster_migration_inconsistent_copy`

Adds `allow_inconsistent` field to `POST /1.0/instances/<name>`. Set to `true` to allow inconsistent copying between cluster
members.

## `cluster_ovn_chassis`

Introduces a new `ovn-chassis` cluster role which allows for specifying what cluster member should act as an OVN chassis.

## `container_syscall_intercept_sched_setscheduler`

Adds the `security.syscalls.intercept.sched_setscheduler` to allow advanced process priority management in containers.

## `storage_lvm_thinpool_metadata_size`

Introduces the ability to specify the thin pool metadata volume size via `storage.thinpool_metadata_size`.

If this is not specified then the default is to let LVM pick an appropriate thin pool metadata volume size.

## `storage_volume_state_total`

This adds `total` field to the `GET /1.0/storage-pools/{name}/volumes/{type}/{volume}/state` API.

## `instance_file_head`

Implements HEAD on `/1.0/instances/NAME/file`.

## `instances_nic_host_name`

This introduces the `instances.nic.host_name` server configuration key that can take a value of either `random` or
`mac`. The default value for the key if unspecified is `random`. If it is set to random then use the random host interface names.
If it's set to `mac`, then generate a name in the form `inc1122334455`.

## `image_copy_profile`

Adds ability to modify the set of profiles when image is copied.

## `container_syscall_intercept_sysinfo`

Adds the `security.syscalls.intercept.sysinfo` to allow the `sysinfo` syscall to be populated with cgroup-based resource usage information.

## `clustering_evacuation_mode`

This introduces a `mode` field to the evacuation request which allows
for overriding the evacuation mode traditionally set through
`cluster.evacuate`.

## `resources_pci_vpd`

Adds a new VPD struct to the PCI resource entries.
This struct extracts vendor provided data including the full product name and additional key/value configuration pairs.

## `qemu_raw_conf`

Introduces a `raw.qemu.conf` configuration key to override select sections of the generated `qemu.conf`.

## `storage_cephfs_fscache`

Add support for `fscache`/`cachefilesd` on CephFS pools through a new `cephfs.fscache` configuration option.

## `network_load_balancer`

This introduces the networking load balancer functionality. Allowing `ovn` networks to define port(s) on external
IP addresses that can be forwarded to one or more internal IP(s) inside their respective networks.

## `vsock_api`

This introduces a bidirectional `vsock` interface which allows the `incus-agent` and the Incus server to communicate better.

## `instance_ready_state`

This introduces a new `Ready` state for instances which can be set using `/dev/incus`.

## `network_bgp_holdtime`

This introduces a new `bgp.peers.<name>.holdtime` configuration key to control the BGP hold time for a particular peer.

## `storage_volumes_all_projects`

This introduces the ability to list storage volumes from all projects.

## `metrics_memory_oom_total`

This introduces a new `incus_memory_OOM_kills_total` metric to the `/1.0/metrics` API.
It reports the number of times the out of memory killer (`OOM`) has been triggered.

## `storage_buckets`

This introduces the storage bucket API. It allows the management of S3 object storage buckets for storage pools.

## `storage_buckets_create_credentials`

This updates the storage bucket API to return initial admin credentials at bucket creation time.

## `metrics_cpu_effective_total`
This introduces a new `incus_cpu_effective_total` metric to the `/1.0/metrics` API.
It reports the total number of effective CPUs.

## `projects_networks_restricted_access`

Adds the `restricted.networks.access` project configuration key to indicate (as a comma-delimited list) which networks can be accessed inside the project.
If not specified, all networks are accessible (assuming it is also allowed by the `restricted.devices.nic` setting, described below).

This also introduces a change whereby network access is controlled by the project's `restricted.devices.nic` setting:

* If `restricted.devices.nic` is set to `managed` (the default if not specified), only managed networks are accessible.
* If `restricted.devices.nic` is set to `allow`, all networks are accessible (dependent on the `restricted.networks.access` setting).
* If `restricted.devices.nic` is set to `block`, no networks are accessible.

## `storage_buckets_local`

This introduces the ability to use storage buckets on local storage pools by setting the new `core.storage_buckets_address` global configuration setting.

## `loki`

This adds support for sending life cycle and logging events to a Loki server.

It adds the following global configuration keys:

* `loki.api.ca_cert`: CA certificate which can be used when sending events to the Loki server
* `loki.api.url`: URL to the Loki server (protocol, name or IP and port)
* `loki.auth.username` and `loki.auth.password`: Used if Loki is behind a reverse proxy with basic authentication enabled
* `loki.labels`: Comma-separated list of values which are to be used as labels for Loki events.
* `loki.loglevel`: Minimum log level for events sent to the Loki server.
* `loki.types`: Types of events which are to be sent to the Loki server (`lifecycle` and/or `logging`).

## `acme`

This adds ACME support, which allows [Let's Encrypt](https://letsencrypt.org/) or other ACME services to issue certificates.

It adds the following global configuration keys:

* `acme.domain`: The domain for which the certificate should be issued.
* `acme.email`: The email address used for the account of the ACME service.
* `acme.ca_url`: The directory URL of the ACME service, defaults to `https://acme-v02.api.letsencrypt.org/directory`.

It also adds the following endpoint, which is required for the HTTP-01 challenge:

* `/.well-known/acme-challenge/<token>`

## `internal_metrics`

This adds internal metrics to the list of metrics.
These include:

* Total running operations
* Total active warnings
* Daemon uptime in seconds
* Go memory stats
* Number of goroutines

## `cluster_join_token_expiry`

This adds an expiry to cluster join tokens which defaults to 3 hours, but can be changed by setting the `cluster.join_token_expiry` configuration key.

## `remote_token_expiry`

This adds an expiry to remote add join tokens.
It can be set in the `core.remote_token_expiry` configuration key, and default to no expiry.

## `storage_volumes_created_at`

This change adds support for storing the creation date and time of storage volumes and their snapshots.

This adds the `CreatedAt` field to the `StorageVolume` and `StorageVolumeSnapshot` API types.

## `cpu_hotplug`
This adds CPU hotplugging for VMs.
Hotplugging is disabled when using CPU pinning, because this would require hotplugging NUMA devices as well, which is not possible.

## `projects_networks_zones`

This adds support for the `features.networks.zones` project feature, which changes which project network zones are
associated with when they are created. Previously network zones were tied to the value of `features.networks`,
meaning they were created in the same project as networks were.

Now this has been decoupled from `features.networks` to allow projects that share a network in the default project
(i.e those with `features.networks=false`) to have their own project level DNS zones that give a project oriented
"view" of the addresses on that shared network (which only includes addresses from instances in their project).

This also introduces a change to the network `dns.zone.forward` setting, which now accepts a comma-separated of
DNS zone names (a maximum of one per project) in order to associate a shared network with multiple zones.

No change to the `dns.zone.reverse.*` settings have been made, they still only allow a single DNS zone to be set.
However the resulting zone content that is generated now includes `PTR` records covering addresses from all
projects that are referencing that network via one of their forward zones.

Existing projects that have `features.networks=true` will have `features.networks.zones=true` set automatically,
but new projects will need to specify this explicitly.

## `instance_nic_txqueuelength`

Adds a `txqueuelen` key to control the `txqueuelen` parameter of the NIC device.

## `cluster_member_state`

Adds `GET /1.0/cluster/members/<member>/state` API endpoint and associated `ClusterMemberState` API response type.

## `instances_placement_scriptlet`

Adds support for a Starlark scriptlet to be provided to Incus to allow customized logic that controls placement of new instances in a cluster.

The Starlark scriptlet is provided to Incus via the new global configuration option `instances.placement.scriptlet`.

## `storage_pool_source_wipe`
Adds support for a `source.wipe` Boolean on the storage pool, indicating
that Incus should wipe partition headers off the requested disk rather
than potentially fail due to pre-existing file systems.

## `zfs_block_mode`

This adds support for using ZFS block {spellexception}`filesystem` volumes allowing the use of different file systems on top of ZFS.

This adds the following new configuration options for ZFS storage pools:

* `volume.zfs.block_mode`
* `volume.block.mount_options`
* `volume.block.filesystem`

## `instance_generation_id`

Adds support for instance generation ID. The VM or container generation ID will change whenever the instance's place in time moves backwards. As of now, the generation ID is only exposed through to VM type instances. This allows for the VM guest OS to reinitialize any state it needs to avoid duplicating potential state that has already occurred:

* `volatile.uuid.generation`

## `disk_io_cache`
This introduces a new `io.cache` property to disk devices which can be used to override the VM caching behavior.

## `amd_sev`
Adds support for AMD SEV (Secure Encrypted Virtualization) that can be used to encrypt the memory of a guest VM.

This adds the following new configuration options for SEV encryption:

* `security.sev` : (bool) is SEV enabled for this VM
* `security.sev.policy.es` : (bool) is SEV-ES enabled for this VM
* `security.sev.session.dh` : (string) guest owner's `base64`-encoded Diffie-Hellman key
* `security.sev.session.data` : (string) guest owner's `base64`-encoded session blob

## `storage_pool_loop_resize`
This allows growing loop file backed storage pools by changing the `size` setting of the pool.

## `migration_vm_live`
This adds support for performing VM QEMU to QEMU live migration for both shared storage (clustered Ceph) and
non-shared storage pools.

This also adds the `CRIUType_VM_QEMU` value of `3` for the migration `CRIUType` `protobuf` field.

## `ovn_nic_nesting`
This adds support for nesting an `ovn` NIC inside another `ovn` NIC on the same instance.
This allows for an OVN logical switch port to be tunneled inside another OVN NIC using VLAN tagging.

This feature is configured by specifying the parent NIC name using the `nested` property and the VLAN ID to use for tunneling with the `vlan` property.

## `oidc`

This adds support for OpenID Connect (OIDC) authentication.

This adds the following new configuration keys:

* `oidc.issuer`
* `oidc.client.id`
* `oidc.audience`

## `network_ovn_l3only`
This adds the ability to set an `ovn` network into "layer 3 only" mode.
This mode can be enabled at IPv4 or IPv6 level using `ipv4.l3only` and `ipv6.l3only` configuration options respectively.

With this mode enabled the following changes are made to the network:

* The virtual router's internal port address will be configured with a single host netmask (e.g. /32 for IPv4 or /128 for IPv6).
* Static routes for active instance NIC addresses will be added to the virtual router.
* A discard route for the entire internal subnet will be added to the virtual router to prevent packets destined for inactive addresses from escaping to the uplink network.
* The DHCPv4 server will be configured to indicate that a netmask of 255.255.255.255 be used for instance configuration.

## `ovn_nic_acceleration_vdpa`

This updates the `ovn_nic_acceleration` API extension. The `acceleration` configuration key for OVN NICs can now takes the value `vdpa` to support Virtual Data Path Acceleration (VDPA).

## `cluster_healing`
This adds cluster healing which automatically evacuates offline cluster members.

This adds the following new configuration key:

* `cluster.healing_threshold`

The configuration key takes an integer, and can be disabled by setting it to 0 (default). If set, the value represents the threshold after which an offline cluster member is to be evacuated. In case the value is lower than `cluster.offline_threshold`, that value will be used instead.

When the offline cluster member is evacuated, only remote-backed instances will be migrated. Local instances will be ignored as there is no way of migrating them once the cluster member is offline.

## `instances_state_total`
This extension adds a new `total` field to `InstanceStateDisk` and `InstanceStateMemory`, both part of the instance's state API.

## `auth_user`
Add current user details to the main API endpoint.

This introduces:

* `auth_user_name`
* `auth_user_method`

## `security_csm`
Introduce a new `security.csm` configuration key to control the use of
`CSM` (Compatibility Support Module) to allow legacy operating systems to
be run in Incus VMs.

## `instances_rebuild`
This extension adds the ability to rebuild an instance with the same origin image, alternate image or as empty. A new `POST /1.0/instances/<name>/rebuild?project=<project>` API endpoint has been added as well as a new CLI command [`incus rebuild`](incus_rebuild.md).

## `numa_cpu_placement`
This adds the possibility to place a set of CPUs in a desired set of NUMA nodes.

This adds the following new configuration key:

* `limits.cpu.nodes` : (string) comma-separated list of NUMA node IDs or NUMA node ID ranges to place the CPUs (chosen with a dynamic value of `limits.cpu`) in.

## `custom_volume_iso`
This adds the possibility to import ISO images as custom storage volumes.

This adds the `--type` flag to [`incus storage volume import`](incus_storage_volume_import.md).

## `network_allocations`
This adds the possibility to list a Incus deployment's network allocations.

Through the [`incus network list-allocations`](incus_network_list-allocations.md) command and the `--project <PROJECT> | --all-projects` flags,
you can list all the used IP addresses, hardware addresses (for instances), resource URIs and whether it uses NAT for
each `instance`, `network`, `network forward` and `network load-balancer`.

## `zfs_delegate`
This implements a new `zfs.delegate` volume Boolean for volumes on a ZFS storage driver.
When enabled and a suitable system is in use (requires ZFS 2.2 or higher), the ZFS dataset will be delegated to the container, allowing for its use through the `zfs` command line tool.

## `storage_api_remote_volume_snapshot_copy`

This allows copying storage volume snapshots to and from remotes.

## `operations_get_query_all_projects`

This introduces support for the `all-projects` query parameter for the GET API calls to both `/1.0/operations` and `/1.0/operations?recursion=1`.
This parameter allows bypassing the project name filter.

## `metadata_configuration`
Adds the `GET /1.0/metadata/configuration` API endpoint to retrieve the generated metadata configuration in a JSON format. The JSON structure adopts the structure ```"configs" > `ENTITY` > `ENTITY_SECTION` > "keys" > [<CONFIG_OPTION_0>, <CONFIG_OPTION_1>, ...]```.
Check the list of {doc}`configuration options </config-options>` to see which configuration options are included.

## `syslog_socket`

This introduces a syslog socket that can receive syslog formatted log messages. These can be viewed in the events API and `incus monitor`, and can be forwarded to Loki. To enable this feature, set `core.syslog_socket` to `true`.

## `event_lifecycle_name_and_project`

This adds the fields `Name` and `Project` to `lifecycle` events.

## `instances_nic_limits_priority`

This introduces a new per-NIC `limits.priority` option that works with both cgroup1 and cgroup2 unlike the deprecated `limits.network.priority` instance setting, which only worked with cgroup1.

## `disk_initial_volume_configuration`

This API extension provides the capability to set initial volume configurations for instance root devices.
Initial volume configurations are prefixed with `initial.` and can be specified either through profiles or directly
during instance initialization using the `--device` flag.

Note that these configuration are applied only at the time of instance creation and subsequent modifications have
no effect on existing devices.
