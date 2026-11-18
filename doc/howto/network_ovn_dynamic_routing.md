(network-ovn-dynamic-routing)=
# How to export OVN networks through OVN-native dynamic routing

```{note}
This feature requires OVN 26.03 or later.

Exporting multiple OVN networks that share an uplink into the same routing table requires OVN 27.03 or later.
Older versions don't export any routes into that table on a cluster member that hosts the active gateway of more than one of these networks.
```

OVN can export an OVN network's prefixes into the host's routing stack itself, as an alternative to announcing them through the {ref}`Incus BGP server <network-bgp>`.

Instead of announcing over BGP, OVN programs the network's prefixes into a Linux VRF table, but only on the cluster member that currently hosts the active OVN gateway chassis.
An external routing daemon on the host is then able to redistribute the routes from that VRF towards the fabric.
On failover, OVN moves the routes to the new active gateway member automatically.

To match what the Incus BGP server announces for OVN networks, this covers:

- the network's connected subnets,
- its network forward and load-balancer addresses,
- and the external addresses of its NAT rules (the SNAT address of NATed networks and the external addresses of NICs).

The next-hop is the OVN router's uplink address.

## Requirements

- OVN 26.03 or later (27.03 or later for multiple OVN networks on the same uplink).
- An OVN network that has an uplink.
- A host VRF using the same routing table on every cluster member.

## Configure dynamic routing

Dynamic routing is configured on the uplink network rather than on each OVN network, so all OVN networks sharing that uplink export into the same routing table.

To enable it, set `ovn.routes_table` on the uplink network to the routing table ID of the host VRF:

```bash
incus network set UPLINK ovn.routes_table=100
```

Once enabled, the downstream OVN networks export their prefixes into the host VRF on whichever member currently hosts each network's active gateway chassis.
The routes appear in the configured routing table (`proto ovn`) on that member only, and move automatically on failover.

To disable dynamic routing again, unset the key:

```bash
incus network unset UPLINK ovn.routes_table
```
