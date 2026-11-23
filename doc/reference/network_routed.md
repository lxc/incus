(network-routed)=
# Routed network

<!-- Include start routed intro -->
A routed network connects instances to the host through a dedicated virtual device per NIC and routes their addresses, without a shared bridge or layer 2 segment.
<!-- Include end routed intro -->

The `routed` network type allows to specify presets to use when connecting instances with {ref}`routed NICs <nic-routed>`.
In this case, the instance NICs can simply set the `network` option to the network they connect to without knowing any of the underlying configuration details.

The NICs connected to the network can have one address per IP family, which must be within the network's `ipv4.subnets` and `ipv6.subnets`.
They can't set `ipv4.routes`, `ipv6.routes`, `ipv4.host_address` or `ipv6.host_address`, as those could take over addresses outside of the subnets.

(network-routed-addresses)=
## Address assignment

NICs connected to the network don't need to set `ipv4.address` or `ipv6.address`.
If a NIC has no address of a family that the network has subnets for, Incus assigns it the first free address of those subnets when the instance starts.
The addresses are stored in the instance's `volatile.<name>.ipv4.address` and `volatile.<name>.ipv6.address` keys, kept across restarts and migrations and released when the NIC or instance is removed.
Copies of the instance get new addresses.

Incus doesn't assign the network and broadcast addresses of IPv4 subnets larger than `/31`, the subnet-router anycast address (first address) of IPv6 subnets and addresses used by other routed NICs.
If the network's subnets are changed so that they no longer contain an assigned address, a new address is assigned on the next start.
Each address can only be used by one NIC: an instance doesn't start if one of its configured addresses is already used by another NIC, and an assigned address is replaced on the next start if another NIC got it configured since.

(network-routed-dhcp)=
## DHCP for virtual machines

Containers get their addresses and gateways configured directly by Incus.
Virtual machines instead need to configure them on their own.

To avoid having to do this manually or through `cloud-init`, a routed network runs a DHCP server for each virtual machine NIC connected to it:

- For IPv4, it hands out the NIC's IPv4 address with a `/32` subnet mask and the host-side gateway address (`169.254.0.1` by default) as an on-link default gateway.
- For IPv6, it sends router advertisements from the host-side gateway address (`fe80::1` by default) and hands out the NIC's IPv6 address as a `/128` over DHCPv6.
  The `ipv6.subnets` entry containing the address is advertised as off-link and without SLAAC, so that all traffic goes through the gateway.
  To advertise it, the host-side interface also gets the subnet-router anycast address (the first address of the subnet), which can't be used by NICs.

The DHCP server requires the NIC to use `ipv4.gateway=auto` or `ipv6.gateway=auto` (with a link-local `ipv6.host_address`) and can be disabled per family with `ipv4.dhcp=false` and `ipv6.dhcp=false`.

When using a `vrf`, the host should have `net.ipv6.conf.all.forwarding=1` set (as needed for routing IPv6 anyway), as the VRF interface otherwise learns a default route from the router advertisements.

(network-routed-options)=
## Configuration options

The following configuration key namespaces are currently supported for the `routed` network type:

- `user` (free-form key/value for user metadata)

```{note}
{{note_ip_addresses_CIDR}}
```

The following configuration options are available for the `routed` network type:

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group network_routed-common start -->
    :end-before: <!-- config group network_routed-common end -->
```

### IPv4 options

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group network_routed-ipv4 start -->
    :end-before: <!-- config group network_routed-ipv4 end -->
```

### IPv6 options

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group network_routed-ipv6 start -->
    :end-before: <!-- config group network_routed-ipv6 end -->
```
