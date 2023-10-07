(network-bridge)=
# Bridge network

As one of the possible network configuration types under Incus, Incus supports creating and managing network bridges.
<!-- Include start bridge intro -->
A network bridge creates a virtual L2 Ethernet switch that instance NICs can connect to, making it possible for them to communicate with each other and the host.
Incus bridges can leverage underlying native Linux bridges and Open vSwitch.
<!-- Include end bridge intro -->

The `bridge` network type allows to create an L2 bridge that connects the instances that use it together into a single network L2 segment.
Bridges created by Incus are managed, which means that in addition to creating the bridge interface itself, Incus also sets up a local `dnsmasq` process to provide DHCP, IPv6 route announcements and DNS services to the network.
By default, it also performs NAT for the bridge.

See {ref}`network-bridge-firewall` for instructions on how to configure your firewall to work with Incus bridge networks.

<!-- Include start MAC identifier note -->

```{note}
Static DHCP assignments depend on the client using its MAC address as the DHCP identifier.
This method prevents conflicting leases when copying an instance, and thus makes statically assigned leases work properly.
```

<!-- Include end MAC identifier note -->

## IPv6 prefix size

If you're using IPv6 for your bridge network, you should use a prefix size of 64.

Larger subnets (i.e., using a prefix smaller than 64) should work properly too, but they aren't typically that useful for {abbr}`SLAAC (Stateless Address Auto-configuration)`.

Smaller subnets are in theory possible (when using stateful DHCPv6 for IPv6 allocation), but they aren't properly supported by `dnsmasq` and might cause problems.
If you must create a smaller subnet, use static allocation or another standalone router advertisement daemon.

(network-bridge-options)=
## Configuration options

The following configuration key namespaces are currently supported for the `bridge` network type:

- `bgp` (BGP peer configuration)
- `bridge` (L2 interface configuration)
- `dns` (DNS server and resolution configuration)
- `ipv4` (L3 IPv4 configuration)
- `ipv6` (L3 IPv6 configuration)
- `security` (network ACL configuration)
- `raw` (raw configuration file content)
- `tunnel` (cross-host tunneling configuration)
- `user` (free-form key/value for user metadata)

```{note}
{{note_ip_addresses_CIDR}}
```

The following configuration options are available for the `bridge` network type:

Key                                  | Type      | Condition             | Default                   | Description
:--                                  | :--       | :--                   | :--                       | :--
`bgp.peers.NAME.address`             | string    | BGP server            | -                         | Peer address (IPv4 or IPv6)
`bgp.peers.NAME.asn`                 | integer   | BGP server            | -                         | Peer AS number
`bgp.peers.NAME.password`            | string    | BGP server            | - (no password)           | Peer session password (optional)
`bgp.peers.NAME.holdtime`            | integer   | BGP server            | `180`                     | Peer session hold time (in seconds; optional)
`bgp.ipv4.nexthop`                   | string    | BGP server            | local address             | Override the next-hop for advertised prefixes
`bgp.ipv6.nexthop`                   | string    | BGP server            | local address             | Override the next-hop for advertised prefixes
`bridge.driver`                      | string    | -                     | `native`                  | Bridge driver: `native` or `openvswitch`
`bridge.external_interfaces`         | string    | -                     | -                         | Comma-separated list of unconfigured network interfaces to include in the bridge
`bridge.hwaddr`                      | string    | -                     | -                         | MAC address for the bridge
`bridge.mtu`                         | integer   | -                     | `1500`                    | Bridge MTU (default varies if tunnel in use)
`dns.domain`                         | string    | -                     | `incus`                   | Domain to advertise to DHCP clients and use for DNS resolution
`dns.mode`                           | string    | -                     | `managed`                 | DNS registration mode: `none` for no DNS record, `managed` for Incus-generated static records or `dynamic` for client-generated records
`dns.search`                         | string    | -                     | -                         | Full comma-separated domain search list, defaulting to `dns.domain` value
`dns.zone.forward`                   | string    | -                     | `managed`                 | Comma-separated list of DNS zone names for forward DNS records
`dns.zone.reverse.ipv4`              | string    | -                     | `managed`                 | DNS zone name for IPv4 reverse DNS records
`dns.zone.reverse.ipv6`              | string    | -                     | `managed`                 | DNS zone name for IPv6 reverse DNS records
`ipv4.address`                       | string    | standard mode         | - (initial value on creation: `auto`) | IPv4 address for the bridge (use `none` to turn off IPv4 or `auto` to generate a new random unused subnet) (CIDR)
`ipv4.dhcp`                          | bool      | IPv4 address          | `true`                    | Whether to allocate addresses using DHCP
`ipv4.dhcp.expiry`                   | string    | IPv4 DHCP             | `1h`                      | When to expire DHCP leases
`ipv4.dhcp.gateway`                  | string    | IPv4 DHCP             | IPv4 address              | Address of the gateway for the subnet
`ipv4.dhcp.ranges`                   | string    | IPv4 DHCP             | all addresses             | Comma-separated list of IP ranges to use for DHCP (FIRST-LAST format)
`ipv4.firewall`                      | bool      | IPv4 address          | `true`                    | Whether to generate filtering firewall rules for this network
`ipv4.nat`                           | bool      | IPv4 address          | `false` (initial value on creation if `ipv4.address` is set to `auto`: `true`) | Whether to NAT
`ipv4.nat.address`                   | string    | IPv4 address          | -                         | The source address used for outbound traffic from the bridge
`ipv4.nat.order`                     | string    | IPv4 address          | `before`                  | Whether to add the required NAT rules before or after any pre-existing rules
`ipv4.ovn.ranges`                    | string    | -                     | -                         | Comma-separated list of IPv4 ranges to use for child OVN network routers (FIRST-LAST format)
`ipv4.routes`                        | string    | IPv4 address          | -                         | Comma-separated list of additional IPv4 CIDR subnets to route to the bridge
`ipv4.routing`                       | bool      | IPv4 address          | `true`                    | Whether to route traffic in and out of the bridge
`ipv6.address`                       | string    | standard mode         | - (initial value on creation: `auto`) | IPv6 address for the bridge (use `none` to turn off IPv6 or `auto` to generate a new random unused subnet) (CIDR)
`ipv6.dhcp`                          | bool      | IPv6 address          | `true`                    | Whether to provide additional network configuration over DHCP
`ipv6.dhcp.expiry`                   | string    | IPv6 DHCP             | `1h`                      | When to expire DHCP leases
`ipv6.dhcp.ranges`                   | string    | IPv6 stateful DHCP    | all addresses             | Comma-separated list of IPv6 ranges to use for DHCP (FIRST-LAST format)
`ipv6.dhcp.stateful`                 | bool      | IPv6 DHCP             | `false`                   | Whether to allocate addresses using DHCP
`ipv6.firewall`                      | bool      | IPv6 address          | `true`                    | Whether to generate filtering firewall rules for this network
`ipv6.nat`                           | bool      | IPv6 address          | `false` (initial value on creation if `ipv6.address` is set to `auto`: `true`) | Whether to NAT
`ipv6.nat.address`                   | string    | IPv6 address          | -                         | The source address used for outbound traffic from the bridge
`ipv6.nat.order`                     | string    | IPv6 address          | `before`                  | Whether to add the required NAT rules before or after any pre-existing rules
`ipv6.ovn.ranges`                    | string    | -                     | -                         | Comma-separated list of IPv6 ranges to use for child OVN network routers (FIRST-LAST format)
`ipv6.routes`                        | string    | IPv6 address          | -                         | Comma-separated list of additional IPv6 CIDR subnets to route to the bridge
`ipv6.routing`                       | bool      | IPv6 address          | `true`                    | Whether to route traffic in and out of the bridge
`raw.dnsmasq`                        | string    | -                     | -                         | Additional `dnsmasq` configuration to append to the configuration file
`security.acls`                      | string    | -                     | -                         | Comma-separated list of Network ACLs to apply to NICs connected to this network (see {ref}`network-acls-bridge-limitations`)
`security.acls.default.egress.action`| string    | `security.acls`       | `reject`                  | Action to use for egress traffic that doesn't match any ACL rule
`security.acls.default.egress.logged`| bool      | `security.acls`       | `false`                   | Whether to log egress traffic that doesn't match any ACL rule
`security.acls.default.ingress.action`| string    | `security.acls`      | `reject`                  | Action to use for ingress traffic that doesn't match any ACL rule
`security.acls.default.ingress.logged`| bool      | `security.acls`      | `false`                   | Whether to log ingress traffic that doesn't match any ACL rule
`tunnel.NAME.group`                  | string    | `vxlan`               | `239.0.0.1`               | Multicast address for `vxlan` (used if local and remote aren't set)
`tunnel.NAME.id`                     | integer   | `vxlan`               | `0`                       | Specific tunnel ID to use for the `vxlan` tunnel
`tunnel.NAME.interface`              | string    | `vxlan`               | -                         | Specific host interface to use for the tunnel
`tunnel.NAME.local`                  | string    | `gre` or `vxlan`      | -                         | Local address for the tunnel (not necessary for multicast `vxlan`)
`tunnel.NAME.port`                   | integer   | `vxlan`               | `0`                       | Specific port to use for the `vxlan` tunnel
`tunnel.NAME.protocol`               | string    | standard mode         | -                         | Tunneling protocol: `vxlan` or `gre`
`tunnel.NAME.remote`                 | string    | `gre` or `vxlan`      | -                         | Remote address for the tunnel (not necessary for multicast `vxlan`)
`tunnel.NAME.ttl`                    | integer   | `vxlan`               | `1`                       | Specific TTL to use for multicast routing topologies
`user.*`                             | string    | -                     | -                         | User-provided free-form key/value pairs

(network-bridge-features)=
## Supported features

The following features are supported for the `bridge` network type:

- {ref}`network-acls`
- {ref}`network-forwards`
- {ref}`network-zones`
- {ref}`network-bgp`
- [How to integrate with `systemd-resolved`](network-bridge-resolved)

```{toctree}
:maxdepth: 1
:hidden:

Integrate with resolved </howto/network_bridge_resolved>
Configure your firewall </howto/network_bridge_firewalld>
```
