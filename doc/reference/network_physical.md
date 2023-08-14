(network-physical)=
# Physical network

<!-- Include start physical intro -->
The `physical` network type connects to an existing physical network, which can be a network interface or a bridge, and serves as an uplink network for OVN.
<!-- Include end physical intro -->

This network type allows to specify presets to use when connecting OVN networks to a parent interface or to allow an instance to use a physical interface as a NIC.
In this case, the instance NICs can simply set the `network`option to the network they connect to without knowing any of the underlying configuration details.

(network-physical-options)=
## Configuration options

The following configuration key namespaces are currently supported for the `physical` network type:

- `bgp` (BGP peer configuration)
- `dns` (DNS server and resolution configuration)
- `ipv4` (L3 IPv4 configuration)
- `ipv6` (L3 IPv6 configuration)
- `ovn` (OVN configuration)
- `user` (free-form key/value for user metadata)

```{note}
{{note_ip_addresses_CIDR}}
```

The following configuration options are available for the `physical` network type:

Key                             | Type      | Condition             | Default                   | Description
:--                             | :--       | :--                   | :--                       | :--
`gvrp`                          | bool      | -                     | `false`                   | Register VLAN using GARP VLAN Registration Protocol
`mtu`                           | integer   | -                     | -                         | The MTU of the new interface
`parent`                        | string    | -                     | -                         | Existing interface to use for network
`vlan`                          | integer   | -                     | -                         | The VLAN ID to attach to
`bgp.peers.NAME.address`        | string    | BGP server            | -                         | Peer address (IPv4 or IPv6) for use by `ovn` downstream networks
`bgp.peers.NAME.asn`            | integer   | BGP server            | -                         | Peer AS number for use by `ovn` downstream networks
`bgp.peers.NAME.password`       | string    | BGP server            | - (no password)           | Peer session password (optional) for use by `ovn` downstream networks
`bgp.peers.NAME.holdtime`       | integer   | BGP server            | `180`                     | Peer session hold time (in seconds; optional)
`dns.nameservers`               | string    | standard mode         | -                         | List of DNS server IPs on `physical` network
`ipv4.gateway`                  | string    | standard mode         | -                         | IPv4 address for the gateway and network (CIDR)
`ipv4.ovn.ranges`               | string    | -                     | -                         | Comma-separated list of IPv4 ranges to use for child OVN network routers (FIRST-LAST format)
`ipv4.routes`                   | string    | IPv4 address          | -                         | Comma-separated list of additional IPv4 CIDR subnets that can be used with child OVN networks `ipv4.routes.external` setting
`ipv4.routes.anycast`           | bool      | IPv4 address          | `false`                   | Allow the overlapping routes to be used on multiple networks/NIC at the same time
`ipv6.gateway`                  | string    | standard mode         | -                         | IPv6 address for the gateway and network (CIDR)
`ipv6.ovn.ranges`               | string    | -                     | -                         | Comma-separated list of IPv6 ranges to use for child OVN network routers (FIRST-LAST format)
`ipv6.routes`                   | string    | IPv6 address          | -                         | Comma-separated list of additional IPv6 CIDR subnets that can be used with child OVN networks `ipv6.routes.external` setting
`ipv6.routes.anycast`           | bool      | IPv6 address          | `false`                   | Allow the overlapping routes to be used on multiple networks/NIC at the same time
`ovn.ingress_mode`              | string    | standard mode         | `l2proxy`                 | Sets the method how OVN NIC external IPs will be advertised on uplink network: `l2proxy` (proxy ARP/NDP) or `routed`
`user.*`                        | string    | -                     | -                         | User-provided free-form key/value pairs

(network-physical-features)=
## Supported features

The following features are supported for the `physical` network type:

- {ref}`network-bgp`
