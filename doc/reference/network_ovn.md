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

(network-ovn-options)=
## Configuration options

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

The following applies to child networks:

- Instances on networks sharing a logical router can reach each other by default, as they are all routed by it.
  Use {ref}`network-acls` to restrict this.
- In ACL rules, the traffic of another network on the same router matches `@external` rather than `@internal`, because `@internal` only ever covers the addresses of the network the rule is applied to.
- The uplink, the external port, the chassis group and any network peers belong to the parent.
  A child cannot set `network`, `parent` (networks can only be nested one level deep), `bridge.hwaddr`, `bridge.external_interfaces`, `bridge.multicast_relay`, `ipv4.nat.address`, `ipv6.nat.address` or any `tunnel.*` option, and cannot take part in a peering.
- The subnets of a child must not overlap those of its parent or of the other children of that parent.
- `parent` can only be set when the network is created.
- A parent network cannot be renamed or deleted while it still has children.

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
