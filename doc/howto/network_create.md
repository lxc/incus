# How to create and configure a network

To create a managed network, use the [`incus network`](incus_network.md) command and its subcommands.
Append `--help` to any command to see more information about its usage and available flags.

(network-types)=
## Network types

The following network types are available:

```{list-table}
   :header-rows: 1

* - Network type
  - Documentation
  - Configuration options
* - `bridge`
  - {ref}`network-bridge`
  - {ref}`network-bridge-options`
* - `ovn`
  - {ref}`network-ovn`
  - {ref}`network-ovn-options`
* - `macvlan`
  - {ref}`network-macvlan`
  - {ref}`network-macvlan-options`
* - `sriov`
  - {ref}`network-sriov`
  - {ref}`network-sriov-options`
* - `physical`
  - {ref}`network-physical`
  - {ref}`network-physical-options`

```

## Create a network

Use the following command to create a network:

```bash
incus network create <name> --type=<network_type> [configuration_options...]
```

See {ref}`network-types` for a list of available network types and links to their configuration options.

If you do not specify a `--type` argument, the default type of `bridge` is used.

(network-create-cluster)=
### Create a network in a cluster

If you are running a Incus cluster and want to create a network, you must create the network for each cluster member separately.
The reason for this is that the network configuration, for example, the name of the parent network interface, might be different between cluster members.

Therefore, you must first create a pending network on each member with the `--target=<cluster_member>` flag and the appropriate configuration for the member.
Make sure to use the same network name for all members.
Then create the network without specifying the `--target` flag to actually set it up.

For example, the following series of commands sets up a physical network with the name `UPLINK` on three cluster members:

```{terminal}
:input: incus network create UPLINK --type=physical parent=br0 --target=vm01

Network UPLINK pending on member vm01
:input: incus network create UPLINK --type=physical parent=br0 --target=vm02
Network UPLINK pending on member vm02
:input: incus network create UPLINK --type=physical parent=br0 --target=vm03
Network UPLINK pending on member vm03
:input: incus network create UPLINK --type=physical
Network UPLINK created
```

Also see {ref}`cluster-config-networks`.

(network-attach)=
## Attach a network to an instance

After creating a managed network, you can attach it to an instance as a {ref}`NIC device <devices-nic>`.

To do so, use the following command:

    incus network attach <network_name> <instance_name> [<device_name>] [<interface_name>]

The device name and the interface name are optional, but we recommend specifying at least the device name.
If not specified, Incus uses the network name as the device name, which might be confusing and cause problems.
For example, Incus images perform IP auto-configuration on the `eth0` interface, which does not work if the interface is called differently.

For example, to attach the network `my-network` to the instance `my-instance` as `eth0` device, enter the following command:

    incus network attach my-network my-instance eth0

### Attach the network as a device

The [`incus network attach`](incus_network_attach.md) command is a shortcut for adding a NIC device to an instance.
Alternatively, you can add a NIC device based on the network configuration in the usual way:

    incus config device add <instance_name> <device_name> nic network=<network_name>

When using this way, you can add further configuration to the command to override the default settings for the network if needed.
See {ref}`NIC device <devices-nic>` for all available device options.

## Configure a network

To configure an existing network, use either the `incus network set` and `incus network unset` commands (to configure single settings) or the `incus network edit` command (to edit the full configuration).
To configure settings for specific cluster members, add the `--target` flag.

For example, the following command configures a DNS server for a physical network:

```bash
incus network set UPLINK dns.nameservers=8.8.8.8
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
