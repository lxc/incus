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
