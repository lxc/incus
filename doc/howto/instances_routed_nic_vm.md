(instances-routed-nic-vm)=
# How to add a routed NIC device to a virtual machine

When adding a {ref}`routed NIC device <nic-routed>` to an instance, you must configure the instance to use the link-local gateway IPs as default routes.
For containers, this is configured for you automatically.
For virtual machines, the gateways must be configured manually or via a mechanism like `cloud-init`.

To configure the gateways with `cloud-init`, firstly initialize an instance:

    incus init ubuntu:22.04 jammy --vm

Then add the routed NIC device:

    incus config device add jammy eth0 nic nictype=routed parent=my-parent-network ipv4.address=192.0.2.2 ipv6.address=2001:db8::2

In this command, `my-parent-network` is your parent network, and the IPv4 and IPv6 addresses are within the subnet of the parent.

Next we will create a `netplan` configuration file `netplan.yaml` to use with the instance:

    cat <<EOF > netplan.yaml
    network:
      version: 2
      ethernets:
        enp5s0:
          routes:
          - to: default
            via: 169.254.0.1
          - to: default
            via: fe80::1
          link-local:
          - ipv4
          - ipv6
          addresses:
          - 192.0.2.2/32
          - 2001:db8::2/128
    EOF

This `netplan` configuration adds the {ref}`static link-local next-hop addresses <nic-routed>` (`169.254.0.1` and `fe80::1`) that are required.
Additionally, we enable link-local addressing for IPv4 and IPv6, and add the addresses we configured in our routed NIC device.
For more information on `netplan`, see [their documentation](https://netplan.readthedocs.io/en/latest/).

```{note}
This `netplan` configuration does not include a name server.
To enable DNS within the instance, you must set a valid DNS IP address.
If there is a `lxdbr0` network on the host, the name server can be set to that IP instead.
```

Finally, we add this `netplan` configuration to the instance using the `cloud-init.network-config` configuration key:

    incus config set jammy cloud-init.network-config "$(cat netplan.yaml)"

```{note}
Before you start your instance, make sure that you have {ref}`configured the parent network <nic-routed>` to enable proxy ARP/NDP.
```

You can then start your instance with:

    incus start jammy
