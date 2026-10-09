(network-ovn)=
# OVN network

<!-- Include start OVN intro -->
{abbr}`OVN (Open Virtual Network)` is a software-defined networking system that supports virtual network abstraction.
You can use it to build your own private cloud.
See [`www.ovn.org`](https://www.ovn.org/) for more information.
<!-- Include end OVN intro -->

The `ovn` network type allows to create logical networks using the OVN {abbr}`SDN (software-defined networking)`.
This kind of network can be useful for labs and multi-tenant environments where the same logical subnets are used in multiple discrete networks.

An Incus OVN network can be connected to an existing managed {ref}`network-bridge` or {ref}`network-physical` to gain access to the wider network.
By default, all connections from the OVN logical networks are NATed to an IP allocated from the uplink network.

See {ref}`network-ovn-setup` for basic instructions for setting up an OVN network.

% Include content from [network_bridge.md](network_bridge.md)
```{include} network_bridge.md
    :start-after: <!-- Include start MAC identifier note -->
    :end-before: <!-- Include end MAC identifier note -->
```

(network-ovn-offline)=
## Cluster members that are offline

The `network_ovn_offline` API extension allows creation of an OVN network while some cluster members are unreachable, provided the Incus database has quorum and the OVN services are available.
Creation initializes reachable, active members. It does not confirm local setup on unreachable members.
A missed member remains `Pending`, claims initialization as `Starting`, and becomes `Created` only after successful local setup using the current configuration.
Initialization runs on daemon startup and live reconnection, and before operations that require the local network, including starting an instance NIC.
Local setup failures are retried and do not turn a pending member into a successful one. Failures during startup are reported as warnings; failures during live reconnection are only logged.
In a cluster, an untargeted query reports the global network status:

    incus network show NAME

Global `Created` means global creation completed; an unreachable member can still be locally `Pending`.
Use a targeted single-network query to inspect a reachable member's local status:

    incus network show NAME --target MEMBER

A targeted single-network query forwards to the member and reports an availability error while that member is unreachable; it does not return the stored member state as a substitute.
Network listings in a cluster report global status, including when a target is specified.
The local lifecycle states `Pending`, `Starting`, `Preparing`, `Prepared` and `Stopped` are durable and appear directly in targeted queries. They are not replaced with `Unavailable` merely because the current daemon has not established readiness.
A stored local `Created` state retains local ownership across daemon restarts, but the targeted status is `Unavailable` until the current daemon establishes local readiness and the network is available.
`Unavailable` does not by itself mean that initialization failed, and global `Created` does not establish current readiness on every member.
Global `Deleting` takes precedence over local status.

If a failed creation retained its global configuration and the global status is neither `Created` nor `Deleting`, repair the reported failure and retry the global create request in the same project, without `--target`:

    incus network create NAME --type=ovn

Keep `--type=ovn` so the request matches the stored network type; omitting it can select the default `bridge` type.
Do not repeat the original global configuration arguments or supply them through standard input: a partial creation reuses the saved configuration and rejects any configuration in the retry, even if the values are unchanged.
If a retry fails again, retry it from another member: whether it completes can depend on the member that runs it.
A network in `Deleting` requires deletion repair and retry instead.
If the global status is already `Created` and a member is locally `Pending`, another create request is rejected. That member instead initializes on startup, live reconnection or an operation requiring local readiness; members in maintenance require explicit restore as described below.

Deleting a network can skip an unreachable member only if it is proven never to have begun initialization under this contract, or if it completed acknowledged maintenance preparation.
An old `Pending` database row alone is insufficient proof.
An initialized, unprepared member must be reachable for deletion. Missing heartbeats do not fence a member that can still access OVN.

To prepare an initialized member for temporary maintenance, evacuate it while it is reachable:

    incus cluster evacuate MEMBER --action=stop

After instance evacuation, Incus drains network callbacks, removes local tunnels and OVS ports, withdraws owned BGP routes and releases uplink ownership.
The member's networks become `Prepared` only after local cleanup succeeds and the member becomes evacuated only after all preparation succeeds.
Other users of a shared uplink keep their resources.
BGP withdrawals are asynchronous: acknowledged local cleanup does not acknowledge convergence at a remote peer. Check the configured peers before relying on remote route withdrawal.
These steps prepare a reachable member. Automatic healing of an offline member skips network cleanup and does not certify its networks as `Prepared`.

An evacuated member stays gated across daemon or host restarts.
While it is offline, other members may delete prepared networks or apply the supported shared updates below.
Network creation and configuration updates originating on the prepared member are rejected; deleting a network from it is allowed, because it holds no local resources.
Restore the member explicitly to initialize surviving networks from current configuration before restoring workloads:

    incus cluster restore MEMBER

`Preparing` means maintenance cleanup is incomplete. `Stopped` means local ownership was released but the operation did not reach its final acknowledgment.
A failed evacuation remains gated once a nonempty workload batch has been dispatched and may have effects, even if network preparation has not begun. This also applies to healing. An earlier failure before workload dispatch or network preparation can return a previously active member to `Created`. A failed workload batch does not acknowledge network cleanup or make networks `Prepared`. Readiness-dependent OVN updates may remain blocked while evacuation is incomplete. Repair the reported failure and retry evacuation or explicitly restore the member.
A failed restore remains `Restoring`; some networks or workloads may already be active. This can block cluster network updates until the member completes restore or is evacuated again. Repair and retry restore, or evacuate again to acknowledge cleanup before powering off. A retry preserves already running networks when their configuration matches, instead of restarting networks used by restored workloads.
After a daemon restart during partial restore, those networks are not automatically started; BGP advertisements and local callbacks remain absent until explicit restore.
An instance that fails to start during restore keeps its restore intent; the next restore attempt starts it again.
Evacuation first retries pending OVN NIC cleanup of instances that are already stopped on the member, so a NIC left unacknowledged by an earlier failed start does not prevent network preparation.
Restoring a member is rejected while a network deletion that skipped it as prepared is still in progress; retry the restore after that deletion finishes.
Restore starts only OVN networks whose global status is `Created`. A network whose creation has not completed returns to `Stopped` on the restored member, as after a failed creation, so its create retry completes it there; a network in `Deleting` is completed by its deletion retry.
A network that is deleted while evacuation or restore is running is skipped by that operation. A network created or replaced while the member is in maintenance stays `Pending` on that member and initializes after the member becomes active again.

On a clustered member that is evacuating, evacuated or restoring, an external snapshot restore operation fails if it requests stateful restoration or its target is already running. Restoring a stopped instance statelessly, including with `disk_only`, retains its existing behavior. Requests originating from internal cluster control retain their exception. Explicit member restore retains its existing recovery path. This check occurs within the snapshot operation after snapshot and project validation, before calling the restore driver; it checks maintenance state at that point rather than excluding concurrent maintenance transitions.

A reachable member whose initialization fails under the current configuration can block updates requiring all members. Evacuate that member, repair the shared configuration, and restore it explicitly.
`Deleting` records deletion intent once cleanup may have begun. Other operations reject that network until deletion is repaired and retried.
Deletion rechecks the OVN references of the network in each backend transaction. If unrelated concurrent OVN changes invalidate that check before any effect, deletion repeats its checks a bounded number of times; a new live reference still causes it to fail.
A timeout does not cancel work already accepted by another member. Reservations and accepted receipts remain held until that work finishes or eligible startup recovery releases them as described below.
Incus permits only one daemon process per state directory, holding the process lock until exit.
Before startup releases eligible work from a previous daemon that used backend fencing, the local OVS and OVN Northbound databases must acknowledge a new generation for that member, and OVN Southbound must match its retained database identity. Any recorded interconnect recovery requirements must also be satisfied. This also applies to members in maintenance, even though their networks are not started.
The member's private backend identity persists across restarts and cluster joins; a missing or inconsistent identity prevents automatic recovery.
The identities of the OVS, OVN Northbound and OVN Southbound databases are also retained. A different database cannot acknowledge outstanding work from the previous one; restore access to the original database before retrying recovery.
Changing an OVN or OVS backend connection setting is serialized with OVN lifecycle operations; other server configuration changes are not. Moving a connection through another server, relay or proxy is allowed when it reaches the same database.
A member accepts a different OVN Southbound or OVS database, for example one that was rebuilt, only while it has no unresolved OVN work of its own: operations, receipts, incomplete NIC cleanup or open migrations. That work can only be completed through the original database, so a member that has such work cannot use OVN with the replacement; restore the original database. A refused OVS database also stops that member from managing `openvswitch` bridges.
A different OVN Northbound database is only accepted while the cluster has no OVN networks, operations or notification receipts. While OVN networks exist, restore the original Northbound database.
After replacing a database, restart the daemon on every member: a running daemon keeps the identity of the database it connected to, and its OVN operations wait for that identity until the daemon restarts. A Northbound replacement is then bound by the first OVN network creation, network ACL or address set change, or OVN connection change. A member that was offline during that switch, or that joins the cluster, follows the database the cluster already uses if it has no unresolved OVN work of its own.
Database cloning, rollback and simultaneous independent copies of a database are outside this recovery contract.
OVS and OVN Northbound setup and automatic recovery require permission to read and update the member's entries in `Open_vSwitch.external_ids` and `NB_Global.external_ids`. Remote peering and its recovery also require permission to read and update the member's entries in `IC_NB_Global.external_ids`. Incus does not update generation metadata in `SB_Global.external_ids`; it needs access to read the Southbound root and perform conditional MAC binding invalidations. It does not modify roles or bypass access controls.
Southbound invalidation checks the retained database root and the captured row UUID, datapath, logical port, IP address and learned MAC address, not the server-local row version. A delayed invalidation can still remove an unchanged captured cache entry, including one relearned with the same MAC address; a replacement row or one relearned with another MAC address is retained.
If eligible work from the previous daemon remains and a required backend cannot satisfy its recovery checks, that work stays reserved. Unrelated daemon services can start, while OVN client setup and background recovery retry those checks. Recovery releases only the old operations captured at startup; it does not release work admitted by the new daemon. Maintenance networks remain stopped until explicit restore.
If OVN is unreachable when a member starts, its OVN networks are initialized in the background once OVN is reachable again, and instances that could not start are retried then. Startup does not wait for each OVN network to time out, but registering the OVN NICs of running instances can still delay readiness.
Interconnect work must also satisfy the recorded recovery conditions described below.
Editing the private backend identity or saved database roots is not a supported recovery procedure.
Reservations and accepted receipts from older daemons without backend fencing are retained. An upgrade does not make that earlier work safe to release automatically.
Missing heartbeats, request timeouts and daemon restarts alone do not establish backend completion.
A waiting request retains its timeout error even if accepted work later finishes or is fenced. After the reservation is released, retry the operation to complete any partial cleanup.
Reservations owned by a member that cannot restart are retained, including after forced member removal.
There is no supported recovery for a permanently lost origin in this extension.
A cluster-wide membership reservation also serializes OVN creation with graceful member removal. Concurrent creations of different OVN networks wait for each other. A lost owner of that reservation blocks every OVN network creation and graceful member removal in the cluster.
For the limits on accepted deletion receipts whose recipient cannot restart, see below.

Peering changes are serialized with OVN network lifecycle operations, including changes to child networks.
The originating member must be active and its local network ready. Creating a local peering or deleting an established local peering also requires the existing target network to be created and locally ready.
A pending peer definition can still refer to a target network that does not yet exist.

An interrupted remote peering operation can retain its cluster-wide peering reservation and recorded peer state. A remote peering request whose interconnect write has an uncertain outcome waits until the interconnect database is reachable again and fences that write before it fails and releases its reservation. If the reachable interconnect database has a different identity, the request keeps waiting, holding the cluster-wide peering reservation, until the daemon restarts; restart recovery then applies.
If the update of a member's own backend generation has an uncertain outcome, that member refuses further writes to that database until its daemon restarts.
If the database commit that accepts a deletion receipt reports an error although it was applied, the origin waits until the recipient's daemon restarts.
On restart, Incus can release eligible operations captured from that member's previous daemon after the required local backend generation acknowledgments and root checks. For a recorded interconnect recovery requirement, it must also acknowledge a new generation at the same OVN interconnect Northbound database, with the same member backend identity, reservation token, integration ID and database root.
If an integration or root no longer matches, or a required acknowledgment fails, the work remains reserved. Recovery releases the reservation; it does not complete the peering operation.
While retained, that reservation blocks OVN lifecycle operations and changes to network integrations, including endpoint changes and renames.
This includes NIC creation, start, stop and removal on every OVN network, and OVN network startup after a member restarts. Cleanup attempts can wait for the reservation and then fail, so retained remote peering work can also delay shutdown.
The accepted deletion receipt discussion below does not provide a way to release peering reservations.
Successful, acknowledged remote peering operations release their reservation normally.

A profile change that would remove an OVN network or network ACL from the effective configuration of an instance using that profile is rejected, including when the instance's member is unavailable. Renaming a NIC device in the profile counts as removing it.
Override or remove that NIC through a successful instance update first. The profile can then be changed, and the old network or ACL deleted once nothing uses it.

If stopping an instance cannot complete its OVN NIC cleanup, Incus keeps the original NIC allocation reserved and refuses to start that NIC again.
Stopping the already stopped instance retries the cleanup. If the host interface and its OVS port no longer exist, the retry acknowledges the cleanup only after confirming that the original interface is absent and that no OVS interface is still bound to the OVN port.
Starting a stopped instance first retries such pending cleanup, so instances running when their host stopped uncleanly start again once the cleanup is confirmed. Host interfaces from an earlier kernel boot count as absent. For NICs using `acceleration`, the earlier boot has already reset the virtual function, its representor and any vDPA device, as Incus assumes without this extension; if the representor no longer exists or now serves another NIC, the retry only confirms that no OVS interface is still bound to the NIC's OVN port.
Instances restored with `incus admin recover` or imported from a backup into an unrestricted project keep NIC claims bound to their original instance and cannot start their OVN NICs; this is not supported. A copied claim can also prevent deleting the imported instance, and retaining the original UUID can block the original instance's NIC Stop or Start. Import into a restricted project removes those volatile keys as part of the project's import restrictions; removing the keys does not acknowledge cleanup on the original member.
A NIC start that fails before making any OVN change releases its host allocation immediately.

When an instance moves to another member while stopped, including evacuation that migrates instances, its OVN port moves with it.
Instances with an OVN NIC must be stopped before moving across projects. Live OVN NIC handover supports moves between members within the same project; a live project change is refused before migration effects.
The new member takes over the port only after the previous member acknowledged its NIC cleanup; until then, starting the NIC on the new member is refused.
A stopped instance that moved keeps its port's original record until its NIC starts on the new member; shared network updates and NIC removal still apply to it.
Automatic healing and moves away from an offline member do not acknowledge that member's NIC cleanup, and the moved instances cannot start on their new member. When the original member returns, its restore moves healed instances back and starting them there completes the cleanup; an instance moved manually must be moved back to that member and started or stopped there first. Until then that member cannot prepare its networks for maintenance, and the new member cannot be evacuated. There is no supported recovery while the original member stays offline.

VM live migration hands the OVN port over when the target member starts the VM's NICs, before the memory transfer. The VM's OVN network connectivity is interrupted from then until the switchover completes.
If the migration fails after the target started receiving the VM state, the port stays bound to the target member and the migration stays open: the network cannot be deleted, the instance cannot be moved while stopped, and stopping the source copy leaves its NIC cleanup pending. There is no supported recovery for that NIC.
If the target finished receiving the VM state but the hand-over could not be recorded, the guest continues on the target while the instance is still recorded on the source member. The paused source copy refuses to resume, because both copies use the same storage; stopping it leaves its NIC cleanup pending as above.
If the source's NIC cleanup fails after a successful hand-over, the move reports the retained cleanup. The source member keeps that cleanup pending, which prevents evacuating it and deleting the network; for a local storage pool the source copy and its volume also remain. If the failure was outside the NIC itself, there is no supported recovery for that cleanup.
If a live migration is refused after the VM's NICs were prepared for it, those NICs keep a captured cleanup record that refuses live NIC updates until the VM is stopped.

Deleting a network ACL first removes the network-specific OVN port groups that it no longer needs, which can retain a network's router port after the last NIC stops using the ACL.
Each network is updated under its own lifecycle reservation, so this can wait for, or be refused by, another operation on that network.
After an instance device update commits, Incus also collects unused ACL port groups on the affected networks. If that cleanup fails, the error says that the device configuration was committed; it is not rolled back. Resolve the reported cleanup problem, then retry with an unchanged network update: use `incus network unset <network> security.acls` if the network currently has no ACLs, or `incus network set <network> security.acls=<current-value>` to preserve its current ACL list.
In a cluster that uses OVN, network ACL and address set changes check OVN for references to them, so they need OVN to be reachable. They wait up to two minutes for a moment without other OVN network and NIC operations, so steady OVN activity can make them fail; a retained OVN reservation blocks them. While such a change runs, OVN network start and stop, including at daemon startup and shutdown, fail instead of waiting.
These changes also fail if other writers, such as interconnect route learning, keep changing the OVN tables they check.
Once a cluster has used an OVN backend, network ACL and address set changes keep requiring OVN even after its OVN networks are deleted.
NIC creation, start, stop and removal on an OVN network are serialized across the cluster; an operation that waits more than two minutes for the network fails. A NIC stop on a child or peered network waits at most 10 seconds for the parent's or peer's reservation, so concurrent stops on mutually peered networks can fail each other; retry them.

If a network that shares a logical router with another network is created or changes its subnets while a NIC with `ipv4.routes` or `ipv6.routes` on that other network is starting, the NIC's routes can remain in its network's route address set after the NIC stops, until the network is deleted.

If an instance is deleted while the removal of one of its OVN NICs fails, the stopped port can remain on the network.
Until then such a port also blocks updates and deletion of the network ACLs and address sets it uses. The next update of that network's shared configuration removes such ports, and their static DHCP reservations, once no instance with that identity exists and no member has pending cleanup for it.
A failed NIC device update can leave its rollback incomplete. Incus then keeps the affected network reservations until the member's daemon restarts; the instance NIC may need to be repaired before it can be started or stopped again.
If a failed creation's rollback also fails on a member, that member refuses to initialize the network until its daemon restarts.

Instance NIC ports created by an Incus version without this extension carry no record of the NIC that created them.
After an upgrade, such a port is taken over by the instance NIC whose name, instance and network it matches. That NIC's current configuration is taken as the one that created the port, as earlier versions assumed.
A network update reapplies it and removing the NIC deletes it. Stopping it, or a live update before it is stopped, takes over the internal routes and proxy ARP/NDP entries it added, so stopping it releases them except for entries also held by the network or by another NIC.
The next start records the port normally.
A port that is still active on a member other than the starting instance's is not taken over.
Such a port left behind by an instance that no longer exists is not removed automatically and blocks updates of the network's shared configuration until it is deleted from OVN.

These guarantees apply to OVN networks. They do not change the lifecycle requirements of other network drivers, except that the OVS client is shared: managing `openvswitch` bridges also requires updating the member's entries in `Open_vSwitch.external_ids`, and after the OVS database is recreated the daemon must be restarted. A cluster that has never bound an OVN backend and has no OVN network, other than definitions whose creation has not started, manages network ACLs and address sets without contacting OVN.

(network-ovn-delete-receipt-recovery)=
## Retained accepted deletion receipts

An accepted deletion receipt holds its origin's operation until the recipient acknowledges its driver work or eligible startup recovery clears the receipt. That work can include local cleanup and shared OVN writes.
The origin's wait checks whether the receipt still exists; removing it does not independently verify local cleanup or completion of accepted backend writes. Removing one receipt also does not guarantee reservation release while other receipts remain.
Physically fencing a recipient, identifying the waiting origin or matching database rows does not establish that earlier accepted shared backend effects finished or satisfied the recovery checks.
Do not delete a receipt or reservation on the strength of those checks. This extension does not provide a supported manual receipt-release procedure for a recipient that cannot restart.
A request that already timed out retains its error; receipt removal is not a guarantee that the origin follows an error path or that the network remains `Deleting`.
Initialized, unprepared members are not eligible to be skipped on deletion retries. A permanently lost origin retains its reservations as described above.

(network-ovn-options)=
## Configuration options

On an existing, fully created OVN network, the following settings can be updated while cluster members are offline:

- DNS settings: `dns.domain`, `dns.search`, `dns.nameservers` and `dns.mode`.
- DHCP settings: `ipv4.dhcp`, `ipv4.dhcp.gateway`, `ipv4.dhcp.expiry`, `ipv4.dhcp.ranges`, `ipv4.dhcp.routes`, `ipv6.dhcp` and `ipv6.dhcp.stateful`.
- ACL settings: `security.acls` and the `security.acls.default.*` actions and logging settings.
- The network description and `user.*` metadata.

These changes update the shared configuration and OVN database without changing the offline members' local network configuration.
The Incus database and OVN services must remain available.
As with other OVN policy changes, an isolated OVN chassis applies the new policy after reconnecting to OVN; success does not confirm enforcement on an isolated chassis.
Updates that also change other settings, or updates to networks with custom tunnels, still require all unprepared members to be reachable.
Prepared members are excluded from notifications; explicit restore applies the current configuration.
Changes to forwards or load balancers require all unprepared members to be reachable.
The saved configuration is desired state, not confirmation that every member or backend finished applying it. A failed update can leave partial changes or retained work; repair the reported failure before retrying.

The following configuration key namespaces are currently supported for the `ovn` network type:

- `bridge` (L2 interface configuration)
- `dns` (DNS server and resolution configuration)
- `ipv4` (L3 IPv4 configuration)
- `ipv6` (L3 IPv6 configuration)
- `security` (network ACL configuration)
- `user` (free-form key/value for user metadata)

```{note}
{{note_ip_addresses_CIDR}}
```

The following configuration options are available for the `ovn` network type:

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group network_ovn-common start -->
    :end-before: <!-- config group network_ovn-common end -->
```

```{note}
The `bridge.external_interfaces` option supports an extended format allowing the creation of missing VLAN interfaces.
The extended format is `<interfaceName>/<parentInterfaceName>/<vlanId>`.
When the external interface is added to the list with the extended format, the system will automatically create the interface upon the network's creation and subsequently delete it when the network is terminated. The system verifies that the `<interfaceName>` does not already exist. If the interface name is in use with a different parent or VLAN ID, or if the creation of the interface is unsuccessful, the system will revert with an error message.
```

(network-ovn-child)=
## Child networks

An OVN network can be created with a `parent` pointing at another OVN network in the same project.
Instead of creating a logical router of its own, the child attaches its own logical switch and subnet to the logical router of its parent:

    incus network create net1 --type=ovn network=UPLINK ipv4.address=192.0.2.1/24
    incus network create net2 --type=ovn parent=net1 ipv4.address=198.51.100.1/24
    incus launch images:debian/13 c1 --network net2

This allows several internal subnets to be routed by a single logical router and to share its uplink.

A child network keeps its own switch, subnet, DHCP, DNS records, ACLs and instance ports.
It has no uplink of its own, reaching the outside through the external port of its parent's router, and it can enable NAT independently of its parent so that one subnet can be translated while another is routed natively on the same router.
A child can also set `ipv4.nat.address` and `ipv6.nat.address` to translate to an address of its own rather than to the external address of the shared router.

With `ovn.ingress_mode` set to `l2proxy` (the default) on the uplink network, a NAT address is advertised on that network with proxy ARP/NDP.
With `routed`, the uplink network must instead route the NAT address to the external address of the logical router, for example through {ref}`network-bgp`.

The following applies to child networks:

- Instances on networks sharing a logical router can reach each other by default, as they are all routed by it.
  Use {ref}`network-acls` to restrict this.
- In ACL rules, the traffic of another network on the same router matches `@external` rather than `@internal`, because `@internal` only ever covers the addresses of the network the rule is applied to.
- The uplink, the external port, the chassis group and any network peers belong to the parent.
  A child cannot set `network`, `parent` (networks can only be nested one level deep), `bridge.hwaddr`, `bridge.external_interfaces`, `bridge.multicast_relay` or any `tunnel.*` option, and cannot take part in a peering.
  A peering of the parent routes the subnets of its children as well, and ACL rules referring to that peering match their traffic.
- The subnets of a child must not overlap those of its parent or of the other children of that parent.
- A network's `parent` can be changed, detaching it from its current parent's logical router and attaching it to the new one, but only while the network has no address forwards, load balancers, peerings, or a running instance with `ipv4.routes`/`ipv6.routes` (such routes would be orphaned on the old logical router).
  Removing the `parent` turns the network into a standalone network, which then needs a `network` to be set if external access is required.
- A parent network cannot be renamed, deleted or given a `parent` of its own while it still has children.
- A child network cannot be deleted while its parent has peerings, because the peering policies refer to the child's subnets. Remove the parent's peerings, delete the child, then create the peerings again. A child deleted otherwise can leave its routing address sets behind.
- A parent network that has a child in any state, including one being created, cannot be deleted, and a child can only be created while its parent is fully created. Changing an existing network's `parent` to a network that is being deleted can leave that deletion incomplete; change the `parent` back and retry the deletion.

(network-ovn-features)=
## Supported features

The following features are supported for the `ovn` network type:

- {ref}`network-acls`
- {ref}`network-forwards`
- {ref}`network-integrations`
- {ref}`network-zones`
- {ref}`network-ovn-peers`
- {ref}`network-load-balancers`

```{toctree}
:maxdepth: 1
:hidden:

Set up OVN </howto/network_ovn_setup>
Create routing relationships </howto/network_ovn_peers>
Configure network load balancers </howto/network_load_balancers>
```
