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

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group network_bridge-common start -->
    :end-before: <!-- config group network_bridge-common end -->
```

## BGP options

These options configure BGP peering for OVN downstream networks:

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group network_bridge-bgp start -->
    :end-before: <!-- config group network_bridge-bgp end -->
```

```{note}
The `bridge.external_interfaces` option supports an extended format allowing the creation of missing VLAN interfaces.
The extended format is `<interfaceName>/<parentInterfaceName>/<vlanId>`.
When the external interface is added to the list with the extended format, the system will automatically create the interface upon the network's creation and subsequently delete it when the network is terminated. The system verifies that the `<interfaceName>` does not already exist. If the interface name is in use with a different parent or VLAN ID, or if the creation of the interface is unsuccessful, the system will revert with an error message.
```

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
