(network-configure)=
# How to configure a network

To configure an existing network, use either the [`lxc network set`](lxc_network_set.md) and [`lxc network unset`](lxc_network_unset.md) commands (to configure single settings) or the `lxc network edit` command (to edit the full configuration).
To configure settings for specific cluster members, add the `--target` flag.

For example, the following command configures a DNS server for a physical network:

```bash
lxc network set UPLINK dns.nameservers=8.8.8.8
```

The available configuration options differ depending on the network type.
See {ref}`network-types` for links to the configuration options for each network type.

There are separate commands to configure advanced networking features.
See the following documentation:

- {doc}`/howto/network_acls`
- {doc}`/howto/network_forwards`
- {doc}`/howto/network_load_balancers`
- {doc}`/howto/network_zones`
- {doc}`/howto/network_ovn_peers` (OVN only)
