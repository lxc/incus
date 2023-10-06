(network-bgp)=
# How to configure Incus as a BGP server

```{note}
The BGP server feature is available for the {ref}`network-bridge` and the {ref}`network-physical`.
```

{abbr}`BGP (Border Gateway Protocol)` is a protocol that allows exchanging routing information between autonomous systems.

If you want to directly route external addresses to specific Incus servers or instances, you can configure Incus as a BGP server.
Incus will then act as a BGP peer and advertise relevant routes and next hops to external routers, for example, your network router.
It automatically establishes sessions with upstream BGP routers and announces the addresses and subnets that it's using.

The BGP server feature can be used to allow a Incus server or cluster to directly use internal/external address space by getting the specific subnets or addresses routed to the correct host.
This way, traffic can be forwarded to the target instance.

For bridge networks, the following addresses and networks are being advertised:

- Network `ipv4.address` or `ipv6.address` subnets (if the matching `nat` property isn't set to `true`)
- Network `ipv4.nat.address` or `ipv6.nat.address` subnets (if the matching `nat` property is set to `true`)
- Network forward addresses
- Addresses or subnets specified in `ipv4.routes.external` or `ipv6.routes.external` on an instance NIC that is connected to the bridge network

Make sure to add your subnets to the respective configuration options.
Otherwise, they won't be advertised.

For physical networks, no addresses are advertised directly at the level of the physical network.
Instead, the networks, forwards and routes of all downstream networks (the networks that specify the physical network as their uplink network through the `network` option) are advertised in the same way as for bridge networks.

```{note}
At this time, it is not possible to announce only some specific routes/addresses to particular peers.
If you need this, filter prefixes on the upstream routers.
```

## Configure the BGP server

To configure Incus as a BGP server, set the following server configuration options on all cluster members:

- {config:option}`server-core:core.bgp_address` - the IP address for the BGP server
- {config:option}`server-core:core.bgp_asn` - the {abbr}`ASN (Autonomous System Number)` for the local server
- {config:option}`server-core:core.bgp_routerid` - the unique identifier for the BGP server

For example, set the following values:

```bash
incus config set core.bgp_address=192.0.2.50:179
incus config set core.bgp_asn=65536
incus config set core.bgp_routerid=192.0.2.50
```

Once these configuration options are set, Incus starts listening for BGP sessions.

### Configure next-hop (`bridge` only)

For bridge networks, you can override the next-hop configuration.
By default, the next-hop is set to the address used for the BGP session.

To configure a different address, set `bgp.ipv4.nexthop` or `bgp.ipv6.nexthop`.

### Configure BGP peers for OVN networks

If you run an OVN network with an uplink network (`physical` or `bridge`), the uplink network is the one that holds the list of allowed subnets and the BGP configuration.
Therefore, you must configure BGP peers on the uplink network that contain the information that is required to connect to the BGP server.

Set the following configuration options on the uplink network:

- `bgp.peers.<name>.address` - the peer address to be used by the downstream networks
- `bgp.peers.<name>.asn` - the {abbr}`ASN (Autonomous System Number)` for the local server
- `bgp.peers.<name>.password` - an optional password for the peer session
- `bgp.peers.<name>.holdtime` - an optional hold time for the peer session (in seconds)

Once the uplink network is configured, downstream OVN networks will get their external subnets and addresses announced over BGP.
The next-hop is set to the address of the OVN router on the uplink network.
