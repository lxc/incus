(instances-routed-nic-vm)=
# How to add a routed NIC device to a virtual machine

When adding a {ref}`routed NIC device <nic-routed>` to an instance, you must configure the instance to use the link-local gateway IPs as default routes.
For containers, this is configured for you automatically.
For virtual machines, the gateways can be handed out over DHCP by a {ref}`managed routed network <network-routed>`, or they must be configured manually or via a mechanism like `cloud-init`.

(instances-routed-nic-vm-network)=
## Use a managed routed network

A managed routed network provides the settings for the routed NICs connected to it and runs a DHCP server for each virtual machine NIC, so the virtual machine doesn't need any network configuration.

First create the network with the subnets that the instance addresses are taken from:

    incus network create routed0 --type=routed parent=my-parent-network ipv4.subnets=192.0.2.0/24 ipv6.subnets=2001:db8::/64 dns.nameservers=192.0.2.53

In this command, `my-parent-network` is your parent network, and the subnets are part of the parent's network.
As with a routed NIC on its own, the parent network must be {ref}`configured <nic-routed>` to enable proxy ARP/NDP.
If the instance addresses are routed to the host in another way, for example through {ref}`BGP <network-bgp>`, you can leave out `parent`.

```{note}
In a cluster, you must first create a pending network on each member with the `--target=<cluster_member>` flag, and set `parent` there as it's specific to each member, see {ref}`network-create-cluster`.
```

Then initialize an instance and connect it to the network:

    incus init images:debian/12 bookworm --vm
    incus config device add bookworm eth0 nic network=routed0

When the instance starts, Incus assigns it the first free address of each subnet and records it in `volatile.eth0.ipv4.address` and `volatile.eth0.ipv6.address`.
To use specific addresses instead, set them on the NIC, for example `ipv4.address=192.0.2.2 ipv6.address=2001:db8::2`.

You can then start your instance with:

    incus start bookworm

The instance gets its addresses as `/32` and `/128` together with the gateways and name servers over DHCP.
See {ref}`network-routed-dhcp` for details.

(instances-routed-nic-vm-cloud-init)=
## Configure the gateways with `cloud-init`

To configure the gateways with `cloud-init`, firstly initialize an instance:

    incus init images:debian/12 bookworm --vm

Then add the routed NIC device:

    incus config device add bookworm eth0 nic nictype=routed parent=my-parent-network ipv4.address=192.0.2.2 ipv6.address=2001:db8::2

In this command, `my-parent-network` is your parent network, and the IPv4 and IPv6 addresses are within the subnet of the parent.

Next we will add some `netplan` configuration to the instance using the `cloud-init.network-config` configuration key:

    cat <<EOF | incus config set bookworm cloud-init.network-config=-
    network:
      version: 2
      ethernets:
        enp5s0:
          routes:
          - to: default
            via: 169.254.0.1
            on-link: true
          - to: default
            via: fe80::1
            on-link: true
          addresses:
          - 192.0.2.2/32
          - 2001:db8::2/128
    EOF

This `netplan` configuration adds the {ref}`static link-local next-hop addresses <nic-routed>` (`169.254.0.1` and `fe80::1`) that are required.
For each of these routes we set `on-link` to `true`, which specifies that the route is directly connected to the interface.
We also add the addresses that we configured in our routed NIC device.
For more information on `netplan`, see [their documentation](https://netplan.readthedocs.io/en/latest/).

```{note}
This `netplan` configuration does not include a name server.
To enable DNS within the instance, you must set a valid DNS IP address.
If there is a `incusbr0` network on the host, the name server can be set to that IP instead.
```

You can then start your instance with:

    incus start bookworm

```{note}
Before you start your instance, make sure that you have {ref}`configured the parent network <nic-routed>` to enable proxy ARP/NDP.
```
