(devices-nic)=
# Type: `nic`

```{note}
The `nic` device type is supported for both containers and VMs.

NICs support hotplugging for both containers and VMs (with the exception of the `ipvlan` NIC type).
```

Network devices, also referred to as *Network Interface Controllers* or *NICs*, supply a connection to a network.
Incus supports several different types of network devices (*NIC types*).

## `nictype` vs. `network`

When adding a network device to an instance, there are two methods to specify the type of device that you want to add: through the `nictype` device option or the `network` device option.

These two device options are mutually exclusive, and you can specify only one of them when you create a device.
However, note that when you specify the `network` option, the `nictype` option is derived automatically from the network type.

`nictype`
: When using the `nictype` device option, you can specify a network interface that is not controlled by Incus.
  Therefore, you must specify all information that Incus needs to use the network interface.

  When using this method, the `nictype` option must be specified when creating the device, and it cannot be changed later.

`network`
: When using the `network` device option, the NIC is linked to an existing {ref}`managed network <managed-networks>`.
  In this case, Incus has all required information about the network, and you need to specify only the network name when adding the device.

  When using this method, Incus derives the `nictype` option automatically.
  The value is read-only and cannot be changed.

  Other device options that are inherited from the network are marked with a "yes" in the "Managed" column of the NIC-specific tables of device options.
  You cannot customize these options directly for the NIC if you're using the `network` method.

See {ref}`networks` for more information.

## Available NIC types

The following NICs can be added using the `nictype` or `network` options:

- [`bridged`](nic-bridged): Uses an existing bridge on the host and creates a virtual device pair to connect the host bridge to the instance.
- [`macvlan`](nic-macvlan): Sets up a new network device based on an existing one, but using a different MAC address.
- [`sriov`](nic-sriov): Passes a virtual function of an SR-IOV-enabled physical network device into the instance.
- [`physical`](nic-physical): Passes a physical device from the host through to the instance.
  The targeted device will vanish from the host and appear in the instance.

The following NICs can be added using only the `network` option:

- [`ovn`](nic-ovn): Uses an existing OVN network and creates a virtual device pair to connect the instance to it.

The following NICs can be added using only the `nictype` option:

- [`ipvlan`](nic-ipvlan): Sets up a new network device based on an existing one, using the same MAC address but a different IP.
- [`p2p`](nic-p2p): Creates a virtual device pair, putting one side in the instance and leaving the other side on the host.
- [`routed`](nic-routed): Creates a virtual device pair to connect the host to the instance and sets up static routes and proxy ARP/NDP entries to allow the instance to join the network of a designated parent interface.

The available device options depend on the NIC type and are listed in the tables in the following sections.

(nic-bridged)=
### `nictype`: `bridged`

```{note}
You can select this NIC type through the `nictype` option or the `network` option (see {ref}`network-bridge` for information about the managed `bridge` network).
```

A `bridged` NIC uses an existing bridge on the host and creates a virtual device pair to connect the host bridge to the instance.

#### Device options

NIC devices of type `bridged` have the following device options:

Key                      | Type    | Default           | Managed | Description
:--                      | :--     | :--               | :--     | :--
`boot.priority`          | integer | -                 | no      | Boot priority for VMs (higher value boots first)
`host_name`              | string  | randomly assigned | no      | The name of the interface inside the host
`hwaddr`                 | string  | randomly assigned | no      | The MAC address of the new interface
`ipv4.address`           | string  | -                 | no      | An IPv4 address to assign to the instance through DHCP (can be `none` to restrict all IPv4 traffic when `security.ipv4_filtering` is set)
`ipv4.routes`            | string  | -                 | no      | Comma-delimited list of IPv4 static routes to add on host to NIC
`ipv4.routes.external`   | string  | -                 | no      | Comma-delimited list of IPv4 static routes to route to the NIC and publish on uplink network (BGP)
`ipv6.address`           | string  | -                 | no      | An IPv6 address to assign to the instance through DHCP (can be `none` to restrict all IPv6 traffic when `security.ipv6_filtering` is set)
`ipv6.routes`            | string  | -                 | no      | Comma-delimited list of IPv6 static routes to add on host to NIC
`ipv6.routes.external`   | string  | -                 | no      | Comma-delimited list of IPv6 static routes to route to the NIC and publish on uplink network (BGP)
`limits.egress`          | string  | -                 | no      | I/O limit in bit/s for outgoing traffic (various suffixes supported, see {ref}`instances-limit-units`)
`limits.ingress`         | string  | -                 | no      | I/O limit in bit/s for incoming traffic (various suffixes supported, see {ref}`instances-limit-units`)
`limits.max`             | string  | -                 | no      | I/O limit in bit/s for both incoming and outgoing traffic (same as setting both `limits.ingress` and `limits.egress`)
`limits.priority`        | integer | -                 | no      | The `skb->priority` value (32-bit unsigned integer) for outgoing traffic, to be used by the kernel queuing discipline (qdisc) to prioritize network packets (The effect of this value depends on the particular qdisc implementation, for example, `SKBPRIO` or `QFQ`. Consult the kernel qdisc documentation before setting this value.)
`mtu`                    | integer | parent MTU        | yes     | The MTU of the new interface
`name`                   | string  | kernel assigned   | no      | The name of the interface inside the instance
`network`                | string  | -                 | no      | The managed network to link the device to (instead of specifying the `nictype` directly)
`parent`                 | string  | -                 | yes     | The name of the host device (required if specifying the `nictype` directly)
`queue.tx.length`        | integer | -                 | no      | The transmit queue length for the NIC
`security.ipv4_filtering`| bool    | `false`           | no      | Prevent the instance from spoofing another instance's IPv4 address (enables `security.mac_filtering`)
`security.ipv6_filtering`| bool    | `false`           | no      | Prevent the instance from spoofing another instance's IPv6 address (enables `security.mac_filtering`)
`security.mac_filtering` | bool    | `false`           | no      | Prevent the instance from spoofing another instance's MAC address
`security.port_isolation`| bool    | `false`           | no      | Prevent the NIC from communicating with other NICs in the network that have port isolation enabled
`vlan`                   | integer | -                 | no      | The VLAN ID to use for non-tagged traffic (can be `none` to remove port from default VLAN)
`vlan.tagged`            | integer | -                 | no      | Comma-delimited list of VLAN IDs or VLAN ranges to join for tagged traffic

(nic-macvlan)=
### `nictype`: `macvlan`

```{note}
You can select this NIC type through the `nictype` option or the `network` option (see {ref}`network-macvlan` for information about the managed `macvlan` network).
```

A `macvlan` NIC sets up a new network device based on an existing one, but using a different MAC address.

If you are using a `macvlan` NIC, communication between the Incus host and the instances is not possible.
Both the host and the instances can talk to the gateway, but they cannot communicate directly.

#### Device options

NIC devices of type `macvlan` have the following device options:

Key                     | Type    | Default           | Managed | Description
:--                     | :--     | :--               | :--     | :--
`boot.priority`         | integer | -                 | no      | Boot priority for VMs (higher value boots first)
`gvrp`                  | bool    | `false`           | no      | Register VLAN using GARP VLAN Registration Protocol
`hwaddr`                | string  | randomly assigned | no      | The MAC address of the new interface
`mtu`                   | integer | parent MTU        | yes     | The MTU of the new interface
`name`                  | string  | kernel assigned   | no      | The name of the interface inside the instance
`network`               | string  | -                 | no      | The managed network to link the device to (instead of specifying the `nictype` directly)
`parent`                | string  | -                 | yes     | The name of the host device (required if specifying the `nictype` directly)
`vlan`                  | integer | -                 | no      | The VLAN ID to attach to

(nic-sriov)=
### `nictype`: `sriov`

```{note}
You can select this NIC type through the `nictype` option or the `network` option (see {ref}`network-sriov` for information about the managed `sriov` network).
```

An `sriov` NIC passes a virtual function of an SR-IOV-enabled physical network device into the instance.

An SR-IOV-enabled network device associates a set of virtual functions (VFs) with the single physical function (PF) of the network device.
PFs are standard PCIe functions.
VFs, on the other hand, are very lightweight PCIe functions that are optimized for data movement.
They come with a limited set of configuration capabilities to prevent changing properties of the PF.

Given that VFs appear as regular PCIe devices to the system, they can be passed to instances just like a regular physical device.

VF allocation
: The `sriov` interface type expects to be passed the name of an SR-IOV enabled network device on the system via the `parent` property.
  Incus then checks for any available VFs on the system.

  By default, Incus allocates the first free VF it finds.
  If it detects that either none are enabled or all currently enabled VFs are in use, it bumps the number of supported VFs to the maximum value and uses the first free VF.
  If all possible VFs are in use or the kernel or card doesn't support incrementing the number of VFs, Incus returns an error.

  ```{note}
  If you need Incus to use a specific VF, use a `physical` NIC instead of a `sriov` NIC and set its `parent` option to the VF name.
  ```

#### Device options

NIC devices of type `sriov` have the following device options:

Key                     | Type    | Default           | Managed | Description
:--                     | :--     | :--               | :--     | :--
`boot.priority`         | integer | -                 | no      | Boot priority for VMs (higher value boots first)
`hwaddr`                | string  | randomly assigned | no      | The MAC address of the new interface
`mtu`                   | integer | kernel assigned   | yes     | The MTU of the new interface
`name`                  | string  | kernel assigned   | no      | The name of the interface inside the instance
`network`               | string  | -                 | no      | The managed network to link the device to (instead of specifying the `nictype` directly)
`parent`                | string  | -                 | yes     | The name of the host device (required if specifying the `nictype` directly)
`security.mac_filtering`| bool    | `false`           | no      | Prevent the instance from spoofing another instance's MAC address
`vlan`                  | integer | -                 | no      | The VLAN ID to attach to

(nic-ovn)=
### `nictype`: `ovn`

```{note}
You can select this NIC type only through the `network` option (see {ref}`network-ovn` for information about the managed `ovn` network).
```

An `ovn` NIC uses an existing OVN network and creates a virtual device pair to connect the instance to it.

(devices-nic-hw-acceleration)=
SR-IOV hardware acceleration
: To use `acceleration=sriov`, you must have a compatible SR-IOV physical NIC that supports the Ethernet switch device driver model (`switchdev`) in your Incus host.
  Incus assumes that the physical NIC (PF) is configured in `switchdev` mode and connected to the OVN integration OVS bridge, and that it has one or more virtual functions (VFs) active.

  To achieve this, follow these basic prerequisite setup steps:

   1. Set up PF and VF:

      1. Activate some VFs on PF (called `enp9s0f0np0` in the following example, with a PCI address of `0000:09:00.0`) and unbind them.
      1. Enable `switchdev` mode and `hw-tc-offload` on the PF.
      1. Rebind the VFs.

      ```
      echo 4 > /sys/bus/pci/devices/0000:09:00.0/sriov_numvfs
      for i in $(lspci -nnn | grep "Virtual Function" | cut -d' ' -f1); do echo 0000:$i > /sys/bus/pci/drivers/mlx5_core/unbind; done
      devlink dev eswitch set pci/0000:09:00.0 mode switchdev
      ethtool -K enp9s0f0np0 hw-tc-offload on
      for i in $(lspci -nnn | grep "Virtual Function" | cut -d' ' -f1); do echo 0000:$i > /sys/bus/pci/drivers/mlx5_core/bind; done
      ```

   1. Set up OVS by enabling hardware offload and adding the PF NIC to the integration bridge (normally called `br-int`):

      ```
      ovs-vsctl set open_vswitch . other_config:hw-offload=true
      systemctl restart openvswitch-switch
      ovs-vsctl add-port br-int enp9s0f0np0
      ip link set enp9s0f0np0 up
      ```

VDPA hardware acceleration
: To use `acceleration=vdpa`, you must have a compatible VDPA physical NIC.
  The setup is the same as for SR-IOV hardware acceleration, except that you must also enable the `vhost_vdpa` module and check that you have some available VDPA management devices :

  ```
  modprobe vhost_vdpa && vdpa mgmtdev show
  ```

#### Device options

NIC devices of type `ovn` have the following device options:

Key                                   | Type    | Default           | Managed | Description
:--                                   | :--     | :--               | :--     | :--
`acceleration`                        | string  | `none`            | no      | Enable hardware offloading (either `none`, `sriov` or `vdpa`, see {ref}`devices-nic-hw-acceleration`)
`boot.priority`                       | integer | -                 | no      | Boot priority for VMs (higher value boots first)
`host_name`                           | string  | randomly assigned | no      | The name of the interface inside the host
`hwaddr`                              | string  | randomly assigned | no      | The MAC address of the new interface
`ipv4.address`                        | string  | -                 | no      | An IPv4 address to assign to the instance through DHCP
`ipv4.routes`                         | string  | -                 | no      | Comma-delimited list of IPv4 static routes to route to the NIC
`ipv4.routes.external`                | string  | -                 | no      | Comma-delimited list of IPv4 static routes to route to the NIC and publish on uplink network
`ipv6.address`                        | string  | -                 | no      | An IPv6 address to assign to the instance through DHCP
`ipv6.routes`                         | string  | -                 | no      | Comma-delimited list of IPv6 static routes to route to the NIC
`ipv6.routes.external`                | string  | -                 | no      | Comma-delimited list of IPv6 static routes to route to the NIC and publish on uplink network
`name`                                | string  | kernel assigned   | no      | The name of the interface inside the instance
`nested`                              | string  | -                 | no      | The parent NIC name to nest this NIC under (see also `vlan`)
`network`                             | string  | -                 | yes     | The managed network to link the device to (required)
`security.acls`                       | string  | -                 | no      | Comma-separated list of network ACLs to apply
`security.acls.default.egress.action` | string  | `reject`          | no      | Action to use for egress traffic that doesn't match any ACL rule
`security.acls.default.egress.logged` | bool    | `false`           | no      | Whether to log egress traffic that doesn't match any ACL rule
`security.acls.default.ingress.action`| string  | `reject`          | no      | Action to use for ingress traffic that doesn't match any ACL rule
`security.acls.default.ingress.logged`| bool    | `false`           | no      | Whether to log ingress traffic that doesn't match any ACL rule
`vlan`                                | integer | -                 | no      | The VLAN ID to use when nesting (see also `nested`)

(nic-physical)=
### `nictype`: `physical`

```{note}
- You can select this NIC type through the `nictype` option or the `network` option (see {ref}`network-physical` for information about the managed `physical` network).
- You can have only one `physical` NIC for each parent device.
```

A `physical` NIC provides straight physical device pass-through from the host.
The targeted device will vanish from the host and appear in the instance (which means that you can have only one `physical` NIC for each targeted device).

#### Device options

NIC devices of type `physical` have the following device options:

Key                     | Type    | Default           | Managed | Description
:--                     | :--     | :--               | :--     | :--
`boot.priority`         | integer | -                 | no      | Boot priority for VMs (higher value boots first)
`gvrp`                  | bool    | `false`           | no      | Register VLAN using GARP VLAN Registration Protocol
`hwaddr`                | string  | randomly assigned | no      | The MAC address of the new interface
`mtu`                   | integer | parent MTU        | no      | The MTU of the new interface
`name`                  | string  | kernel assigned   | no      | The name of the interface inside the instance
`network`               | string  | -                 | no      | The managed network to link the device to (instead of specifying the `nictype` directly)
`parent`                | string  | -                 | yes     | The name of the host device (required if specifying the `nictype` directly)
`vlan`                  | integer | -                 | no      | The VLAN ID to attach to

(nic-ipvlan)=
### `nictype`: `ipvlan`

```{note}
- This NIC type is available only for containers, not for virtual machines.
- You can select this NIC type only through the `nictype` option.
- This NIC type does not support hotplugging.
```

An `ipvlan` NIC sets up a new network device based on an existing one, using the same MAC address but a different IP.

If you are using an `ipvlan` NIC, communication between the Incus host and the instances is not possible.
Both the host and the instances can talk to the gateway, but they cannot communicate directly.

Incus currently supports IPVLAN in L2 and L3S mode.
In this mode, the gateway is automatically set by Incus, but the IP addresses must be manually specified using the `ipv4.address` and/or `ipv6.address` options before the container is started.

DNS
: The name servers must be configured inside the container, because they are not set automatically.
  To do this, set the following `sysctls`:

   - When using IPv4 addresses:

     ```
     net.ipv4.conf.<parent>.forwarding=1
     ```

   - When using IPv6 addresses:

     ```
     net.ipv6.conf.<parent>.forwarding=1
     net.ipv6.conf.<parent>.proxy_ndp=1
     ```

#### Device options

NIC devices of type `ipvlan` have the following device options:

Key                     | Type    | Default            | Description
:--                     | :--     | :--                | :--
`gvrp`                  | bool    | `false`            | Register VLAN using GARP VLAN Registration Protocol
`hwaddr`                | string  | randomly assigned  | The MAC address of the new interface
`ipv4.address`          | string  | -                  | Comma-delimited list of IPv4 static addresses to add to the instance (in `l2` mode, these can be specified as CIDR values or singular addresses using a subnet of `/24`)
`ipv4.gateway`          | string  | `auto` (`l3s`), - (`l2`) | In `l3s` mode, whether to add an automatic default IPv4 gateway (can be `auto` or `none`); in `l2` mode, the IPv4 address of the gateway
`ipv4.host_table`       | integer | -                  | The custom policy routing table ID to add IPv4 static routes to (in addition to the main routing table)
`ipv6.address`          | string  | -                  | Comma-delimited list of IPv6 static addresses to add to the instance (in `l2` mode, these can be specified as CIDR values or singular addresses using a subnet of `/64`)
`ipv6.gateway`          | string  | `auto` (`l3s`), - (`l2`) | In `l3s` mode, whether to add an automatic default IPv6 gateway (can be `auto` or `none`); in `l2` mode, the IPv6 address of the gateway
`ipv6.host_table`       | integer | -                  | The custom policy routing table ID to add IPv6 static routes to (in addition to the main routing table)
`mode`                  | string  | `l3s`              | The IPVLAN mode (either `l2` or `l3s`)
`mtu`                   | integer | parent MTU         | The MTU of the new interface
`name`                  | string  | kernel assigned    | The name of the interface inside the instance
`parent`                | string  | -                  | The name of the host device (required)
`vlan`                  | integer | -                  | The VLAN ID to attach to

(nic-p2p)=
### `nictype`: `p2p`

```{note}
You can select this NIC type only through the `nictype` option.
```

A `p2p` NIC creates a virtual device pair, putting one side in the instance and leaving the other side on the host.

#### Device options

NIC devices of type `p2p` have the following device options:

Key                     | Type    | Default           | Description
:--                     | :--     | :--               | :--
`boot.priority`         | integer | -                 | Boot priority for VMs (higher value boots first)
`host_name`             | string  | randomly assigned | The name of the interface inside the host
`hwaddr`                | string  | randomly assigned | The MAC address of the new interface
`ipv4.routes`           | string  | -                 | Comma-delimited list of IPv4 static routes to add on host to NIC
`ipv6.routes`           | string  | -                 | Comma-delimited list of IPv6 static routes to add on host to NIC
`limits.egress`         | string  | -                 | I/O limit in bit/s for outgoing traffic (various suffixes supported, see {ref}`instances-limit-units`)
`limits.ingress`        | string  | -                 | I/O limit in bit/s for incoming traffic (various suffixes supported, see {ref}`instances-limit-units`)
`limits.max`            | string  | -                 | I/O limit in bit/s for both incoming and outgoing traffic (same as setting both `limits.ingress` and `limits.egress`)
`limits.priority`       | integer | -                 | The `skb->priority` value (32-bit unsigned integer) for outgoing traffic, to be used by the kernel queuing discipline (qdisc) to prioritize network packets (The effect of this value depends on the particular qdisc implementation, for example, `SKBPRIO` or `QFQ`. Consult the kernel qdisc documentation before setting this value.)
`mtu`                   | integer | kernel assigned   | The MTU of the new interface
`name`                  | string  | kernel assigned   | The name of the interface inside the instance
`queue.tx.length`       | integer | -                 | The transmit queue length for the NIC

(nic-routed)=
### `nictype`: `routed`

```{note}
You can select this NIC type only through the `nictype` option.
```

A `routed` NIC creates a virtual device pair to connect the host to the instance and sets up static routes and proxy ARP/NDP entries to allow the instance to join the network of a designated parent interface.
For containers it uses a virtual Ethernet device pair, and for VMs it uses a TAP device.

This NIC type is similar in operation to `ipvlan`, in that it allows an instance to join an external network without needing to configure a bridge and shares the host's MAC address.
However, it differs from `ipvlan` because it does not need IPVLAN support in the kernel, and the host and the instance can communicate with each other.

This NIC type respects `netfilter` rules on the host and uses the host's routing table to route packets, which can be useful if the host is connected to multiple networks.

IP addresses, gateways and routes
: You must manually specify the IP addresses (using `ipv4.address` and/or `ipv6.address`) before the instance is started.

  For containers, the NIC configures the following link-local gateway IPs on the host end and sets them as the default gateways in the container's NIC interface:

      169.254.0.1
      fe80::1

  For VMs, the gateways must be configured manually or via a mechanism like `cloud-init` (see the {ref}`how to guide <instances-routed-nic-vm>`).

  ```{note}
  If your container image is configured to perform DHCP on the interface, it will likely remove the automatically added configuration.
  In this case, you must configure the IP addresses and gateways manually or via a mechanism like `cloud-init`.
  ```

  The NIC type configures static routes on the host pointing to the instance's `veth` interface for all of the instance's IPs.

Multiple IP addresses
: Each NIC device can have multiple IP addresses added to it.

  However, it might be preferable to use multiple `routed` NIC interfaces instead.
  In this case, set the `ipv4.gateway` and `ipv6.gateway` values to `none` on any subsequent interfaces to avoid default gateway conflicts.
  Also consider specifying a different host-side address for these subsequent interfaces using `ipv4.host_address` and/or `ipv6.host_address`.

Parent interface
: This NIC can operate with and without a `parent` network interface set.

: With the `parent` network interface set, proxy ARP/NDP entries of the instance's IPs are added to the parent interface, which allows the instance to join the parent interface's network at layer 2.
: To enable this, the following network configuration must be applied on the host via `sysctl`:

   - When using IPv4 addresses:

     ```
     net.ipv4.conf.<parent>.forwarding=1
     ```

   - When using IPv6 addresses:

     ```
     net.ipv6.conf.all.forwarding=1
     net.ipv6.conf.<parent>.forwarding=1
     net.ipv6.conf.all.proxy_ndp=1
     net.ipv6.conf.<parent>.proxy_ndp=1
     ```

#### Device options

NIC devices of type `routed` have the following device options:

Key                     | Type    | Default           | Description
:--                     | :--     | :--               | :--
`gvrp`                  | bool    | `false`           | Register VLAN using GARP VLAN Registration Protocol
`host_name`             | string  | randomly assigned | The name of the interface inside the host
`hwaddr`                | string  | randomly assigned | The MAC address of the new interface
`ipv4.address`          | string  | -                 | Comma-delimited list of IPv4 static addresses to add to the instance
`ipv4.gateway`          | string  | `auto`            | Whether to add an automatic default IPv4 gateway (can be `auto` or `none`)
`ipv4.host_address`     | string  | `169.254.0.1`     | The IPv4 address to add to the host-side `veth` interface
`ipv4.host_table`       | integer | -                 | The custom policy routing table ID to add IPv4 static routes to (in addition to the main routing table)
`ipv4.neighbor_probe`   | bool    | `true`            | Whether to probe the parent network for IP address availability
`ipv4.routes`           | string  | -                 | Comma-delimited list of IPv4 static routes to add on host to NIC (without L2 ARP/NDP proxy)
`ipv6.address`          | string  | -                 | Comma-delimited list of IPv6 static addresses to add to the instance
`ipv6.gateway`          | string  | `auto`            | Whether to add an automatic default IPv6 gateway (can be `auto` or `none`)
`ipv6.host_address`     | string  | `fe80::1`         | The IPv6 address to add to the host-side `veth` interface
`ipv6.host_table`       | integer | -                 | The custom policy routing table ID to add IPv6 static routes to (in addition to the main routing table)
`ipv6.neighbor_probe`   | bool    | `true`            | Whether to probe the parent network for IP address availability
`ipv6.routes`           | string  | -                 | Comma-delimited list of IPv6 static routes to add on host to NIC (without L2 ARP/NDP proxy)
`limits.egress`         | string  | -                 | I/O limit in bit/s for outgoing traffic (various suffixes supported, see {ref}`instances-limit-units`)
`limits.ingress`        | string  | -                 | I/O limit in bit/s for incoming traffic (various suffixes supported, see {ref}`instances-limit-units`)
`limits.max`            | string  | -                 | I/O limit in bit/s for both incoming and outgoing traffic (same as setting both `limits.ingress` and `limits.egress`)
`limits.priority`       | integer | -                 | The `skb->priority` value (32-bit unsigned integer) for outgoing traffic, to be used by the kernel queuing discipline (qdisc) to prioritize network packets (The effect of this value depends on the particular qdisc implementation, for example, `SKBPRIO` or `QFQ`. Consult the kernel qdisc documentation before setting this value.)
`mtu`                   | integer | parent MTU        | The MTU of the new interface
`name`                  | string  | kernel assigned   | The name of the interface inside the instance
`parent`                | string  | -                 | The name of the host device to join the instance to
`queue.tx.length`       | integer | -                 | The transmit queue length for the NIC
`vlan`                  | integer | -                 | The VLAN ID to attach to

## `bridged`, `macvlan` or `ipvlan` for connection to physical network

The `bridged`, `macvlan` and `ipvlan` interface types can be used to connect to an existing physical network.

`macvlan` effectively lets you fork your physical NIC, getting a second interface that is then used by the instance.
This method saves you from creating a bridge device and virtual Ethernet device pairs and usually offers better performance than a bridge.

The downside to this method is that `macvlan` devices, while able to communicate between themselves and to the outside, cannot talk to their parent device.
This means that you can't use `macvlan` if you ever need your instances to talk to the host itself.

In such case, a `bridge` device is preferable.
A bridge also lets you use MAC filtering and I/O limits, which cannot be applied to a `macvlan` device.

`ipvlan` is similar to `macvlan`, with the difference being that the forked device has IPs statically assigned to it and inherits the parent's MAC address on the network.
