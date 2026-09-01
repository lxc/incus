(network-ovn-peer-groups)=
# How to create peer group routing relationships

{ref}`network-ovn-peers` let you connect two OVN networks directly.
When you need to connect more than two networks to each other, creating and maintaining a peering for every pair quickly becomes impractical.
Network peer groups solve this by letting you gather several OVN networks into a single group.

## Create a peer group

To create a network peer group, use the following command:

    incus network peer-group create <peer_group_name> [configuration_options]

For example:

    incus network peer-group create region1 --description "Peer group for region1"

### Peer group properties

Network peer groups have the following properties:

| Property      | Type   | Required | Description                                                           |
| :------------ | :----- | :------- | :-------------------------------------------------------------------- |
| `name`        | string | yes      | Name of the network peer group                                        |
| `description` | string | no       | Description of the network peer group                                 |
| `networks`    | list   | no       | List of OVN networks (name and project) that are members of the group |

## Join a network to a peer group

To join a network to a peer group, use the following command:

    incus network peer-group join <peer_group_name> <network>

You can also join a network from a different project:

    incus network peer-group join <peer_group_name> <project>/<network>

```{note}
The network must already exist, must be an OVN network, and must have at least one configured subnet.
That subnet must not overlap with the subnet of any other member already in the group.
```

## Remove a network from a peer group

To remove a network from a peer group, use the following command:

    incus network peer-group leave <peer_group_name> <network>

## List peer groups

To list all network peer groups, use the following command:

    incus network peer-group list

## Show a peer group

To show the configuration and member networks of a peer group, use the following command:

    incus network peer-group show <peer_group_name>

## Edit a peer group

Use the following command to edit a network peer group, including its list of member networks, as YAML:

    incus network peer-group edit <peer_group_name>

## Delete a peer group

To delete a network peer group, use the following command:

    incus network peer-group delete <peer_group_name>

```{note}
Deleting a peer group automatically detaches any remaining member networks.
```
