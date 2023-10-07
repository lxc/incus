(network-ovn-peers)=
# How to create peer routing relationships

By default, traffic between two OVN networks goes through the uplink network.
This path is inefficient, however, because packets must leave the OVN subsystem and transit through the host's networking stack (and, potentially, an external network) and back into the OVN subsystem of the target network.
Depending on how the host's networking is configured, this might limit the available bandwidth (if the OVN overlay network is on a higher bandwidth network than the host's external network).

Therefore, Incus allows creating peer routing relationships between two OVN networks.
Using this method, traffic between the two networks can go directly from one OVN network to the other and thus stays within the OVN subsystem, rather than transiting through the uplink network.

## Create a routing relationship between networks

To add a peer routing relationship between two networks, you must create a network peering for both networks.
The relationship must be mutual.
If you set it up on only one network, the routing relationship will be in pending state, but not active.

When creating the peer routing relationship, specify a peering name that identifies the relationship for the respective network.
The name can be chosen freely, and you can use it later to edit or delete the relationship.

Use the following commands to create a peer routing relationship between networks in the same project:

    incus network peer create <network1> <peering_name> <network2> [configuration_options]
    incus network peer create <network2> <peering_name> <network1> [configuration_options]

You can also create peer routing relationships between OVN networks in different projects:

    incus network peer create <network1> <peering_name> <project2/network2> [configuration_options] --project=<project1>
    incus network peer create <network2> <peering_name> <project1/network1> [configuration_options] --project=<project2>

```{important}
If the project or the network name is incorrect, the command will not return any error indicating that the respective project/network does not exist, and the routing relationship will remain in pending state.
This behavior prevents users in a different project from discovering whether a project and network exists.
```

### Peering properties

Peer routing relationships have the following properties:

Property         | Type       | Required | Description
:--              | :--        | :--      | :--
`name`           | string     | yes      | Name of the network peering on the local network
`description`    | string     | no       | Description of the network peering
`config`         | string set | no       | Configuration options as key/value pairs (only `user.*` custom keys supported)
`target_project` | string     | yes      | Which project the target network exists in (required at create time)
`target_network` | string     | yes      | Which network to create a peering with (required at create time)
`status`         | string     | --       | Status indicating if pending or created (mutual peering exists with the target network)

## List routing relationships

To list all network peerings for a network, use the following command:

    incus network peer list <network>

## Edit a routing relationship

Use the following command to edit a network peering:

    incus network peer edit <network> <peering_name>

This command opens the network peering in YAML format for editing.
