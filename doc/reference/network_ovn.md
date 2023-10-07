(network-ovn)=
# OVN network

<!-- Include start OVN intro -->
{abbr}`OVN (Open Virtual Network)` is a software-defined networking system that supports virtual network abstraction.
You can use it to build your own private cloud.
See [`www.ovn.org`](https://www.ovn.org/) for more information.
<!-- Include end OVN intro -->

The `ovn` network type allows to create logical networks using the OVN {abbr}`SDN (software-defined networking)`.
This kind of network can be useful for labs and multi-tenant environments where the same logical subnets are used in multiple discrete networks.

A Incus OVN network can be connected to an existing managed {ref}`network-bridge` or {ref}`network-physical` to gain access to the wider network.
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

Key                                  | Type      | Condition             | Default                   | Description
:--                                  | :--       | :--                   | :--                       | :--
`network`                            | string    | -                     | -                         | Uplink network to use for external network access
`bridge.hwaddr`                      | string    | -                     | -                         | MAC address for the bridge
`bridge.mtu`                         | integer   | -                     | `1442`                    | Bridge MTU (default allows host to host Geneve tunnels)
`dns.domain`                         | string    | -                     | `incus`                   | Domain to advertise to DHCP clients and use for DNS resolution
`dns.search`                         | string    | -                     | -                         | Full comma-separated domain search list, defaulting to `dns.domain` value
`dns.zone.forward`                   | string    | -                     | -                         | Comma-separated list of DNS zone names for forward DNS records
`dns.zone.reverse.ipv4`              | string    | -                     | -                         | DNS zone name for IPv4 reverse DNS records
`dns.zone.reverse.ipv6`              | string    | -                     | -                         | DNS zone name for IPv6 reverse DNS records
`ipv4.address`                       | string    | standard mode         | - (initial value on creation: `auto`) | IPv4 address for the bridge (use `none` to turn off IPv4 or `auto` to generate a new random unused subnet) (CIDR)
`ipv4.dhcp`                          | bool      | IPv4 address          | `true`                    | Whether to allocate addresses using DHCP
`ipv4.l3only`                        | bool      | IPv4 address          | `false`                   | Whether to enable layer 3 only mode.
`ipv4.nat`                           | bool      | IPv4 address          | `false` (initial value on creation if `ipv4.address` is set to `auto`: `true`) | Whether to NAT
`ipv4.nat.address`                   | string    | IPv4 address          | -                         | The source address used for outbound traffic from the network (requires uplink `ovn.ingress_mode=routed`)
`ipv6.address`                       | string    | standard mode         | - (initial value on creation: `auto`) | IPv6 address for the bridge (use `none` to turn off IPv6 or `auto` to generate a new random unused subnet) (CIDR)
`ipv6.dhcp`                          | bool      | IPv6 address          | `true`                    | Whether to provide additional network configuration over DHCP
`ipv6.dhcp.stateful`                 | bool      | IPv6 DHCP             | `false`                   | Whether to allocate addresses using DHCP
`ipv6.l3only`                        | bool      | IPv6 DHCP stateful    | `false`                   | Whether to enable layer 3 only mode.
`ipv6.nat`                           | bool      | IPv6 address          | `false` (initial value on creation if `ipv6.address` is set to `auto`: `true`) | Whether to NAT
`ipv6.nat.address`                   | string    | IPv6 address          | -                         | The source address used for outbound traffic from the network (requires uplink `ovn.ingress_mode=routed`)
`security.acls`                      | string    | -                     | -                         | Comma-separated list of Network ACLs to apply to NICs connected to this network
`security.acls.default.egress.action`| string    | `security.acls`       | `reject`                  | Action to use for egress traffic that doesn't match any ACL rule
`security.acls.default.egress.logged`| bool      | `security.acls`       | `false`                   | Whether to log egress traffic that doesn't match any ACL rule
`security.acls.default.ingress.action` | string  | `security.acls`       | `reject`                  | Action to use for ingress traffic that doesn't match any ACL rule
`security.acls.default.ingress.logged` | bool    | `security.acls`       | `false`                   | Whether to log ingress traffic that doesn't match any ACL rule
`user.*`                             | string    | -                     | -                         | User-provided free-form key/value pairs

(network-ovn-features)=
## Supported features

The following features are supported for the `ovn` network type:

- {ref}`network-acls`
- {ref}`network-forwards`
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
