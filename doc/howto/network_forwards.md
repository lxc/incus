(network-forwards)=
# How to configure network forwards

```{note}
Network forwards are available for the {ref}`network-ovn` and the {ref}`network-bridge`.
```

Network forwards allow an external IP address (or specific ports on it) to be forwarded to an internal IP address (or specific ports on it) in the network that the forward belongs to.

This feature can be useful if you have limited external IP addresses and want to share a single external address between multiple instances.
There are two different ways how you can use network forwards in this case:

- Forward all traffic from the external address to the internal address of one instance.
  This method makes it easy to move the traffic destined for the external address to another instance by simply reconfiguring the network forward.
- Forward traffic from different port numbers of the external address to different instances (and optionally different ports on those instances).
  This method allows to "share" your external IP address and expose more than one instance at a time.

## Create a network forward

Use the following command to create a network forward:

```bash
incus network forward create <network_name> <listen_address> [configuration_options...]
```

Each forward is assigned to a network.
It requires a single external listen address (see {ref}`network-forwards-listen-addresses` for more information about which addresses can be forwarded, depending on the network that you are using).

You can specify an optional default target address by adding the `target_address=<IP_address>` configuration option.
If you do, any traffic that does not match a port specification is forwarded to this address.
Note that this target address must be within the same subnet as the network that the forward is associated to.

### Forward properties

Network forwards have the following properties:

Property         | Type       | Required | Description
:--              | :--        | :--      | :--
`listen_address` | string     | yes      | IP address to listen on
`description`    | string     | no       | Description of the network forward
`config`         | string set | no       | Configuration options as key/value pairs (only `target_address` and `user.*` custom keys supported)
`ports`          | port list  | no       | List of {ref}`port specifications <network-forwards-port-specifications>`

(network-forwards-listen-addresses)=
### Requirements for listen addresses

The requirements for valid listen addresses vary depending on which network type the forward is associated to.

Bridge network
: - Any non-conflicting listen address is allowed.
  - The listen address must not overlap with a subnet that is in use with another network.

OVN network
: - Allowed listen addresses must be defined in the uplink network's `ipv{n}.routes` settings or the project's {config:option}`project-restricted:restricted.networks.subnets` setting (if set).
  - The listen address must not overlap with a subnet that is in use with another network.

(network-forwards-port-specifications)=
## Configure ports

You can add port specifications to the network forward to forward traffic from specific ports on the listen address to specific ports on the target address.
This target address must be different from the default target address.
It must be within the same subnet as the network that the forward is associated to.

Use the following command to add a port specification:

```bash
incus network forward port add <network_name> <listen_address> <protocol> <listen_ports> <target_address> [<target_ports>]
```

You can specify a single listen port or a set of ports.
If you want to forward the traffic to different ports, you have two options:

- Specify a single target port to forward traffic from all listen ports to this target port.
- Specify a set of target ports with the same number of ports as the listen ports to forward traffic from the first listen port to the first target port, the second listen port to the second target port, and so on.

### Port properties

Network forward ports have the following properties:

Property          | Type       | Required | Description
:--               | :--        | :--      | :--
`protocol`        | string     | yes      | Protocol for the port(s) (`tcp` or `udp`)
`listen_port`     | string     | yes      | Listen port(s) (e.g. `80,90-100`)
`target_address`  | string     | yes      | IP address to forward to
`target_port`     | string     | no       | Target port(s) (e.g. `70,80-90` or `90`), same as `listen_port` if empty
`description`     | string     | no       | Description of port(s)

## Edit a network forward

Use the following command to edit a network forward:

```bash
incus network forward edit <network_name> <listen_address>
```

This command opens the network forward in YAML format for editing.
You can edit both the general configuration and the port specifications.

## Delete a network forward

Use the following command to delete a network forward:

```bash
incus network forward delete <network_name> <listen_address>
```
