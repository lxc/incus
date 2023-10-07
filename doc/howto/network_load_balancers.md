(network-load-balancers)=
# How to configure network load balancers

```{note}
Network load balancers are currently available for the {ref}`network-ovn`.
```

Network load balancers are similar to forwards in that they allow specific ports on an external IP address to be forwarded to specific ports on internal IP addresses in the network that the load balancer belongs to. The difference between load balancers and forwards is that load balancers can be used to share ingress traffic between multiple internal backend addresses.

This feature can be useful if you have limited external IP addresses or want to share a single external address and ports over multiple instances.

A load balancer is made up of:

- A single external listen IP address.
- One or more named backends consisting of an internal IP and optional port ranges.
- One or more listen port ranges that are configured to forward to one or more named backends.

## Create a network load balancer

Use the following command to create a network load balancer:

```bash
incus network load-balancer create <network_name> <listen_address> [configuration_options...]
```

Each load balancer is assigned to a network.
It requires a single external listen address (see {ref}`network-load-balancers-listen-addresses` for more information about which addresses can be load-balanced).

### Load balancer properties

Network load balancers have the following properties:

Property         | Type         | Required | Description
:--              | :--          | :--      | :--
`listen_address` | string       | yes      | IP address to listen on
`description`    | string       | no       | Description of the network load balancer
`config`         | string set   | no       | Configuration options as key/value pairs (only `user.*` custom keys supported)
`backends`       | backend list | no       | List of {ref}`backend specifications <network-load-balancers-backend-specifications>`
`ports`          | port list    | no       | List of {ref}`port specifications <network-load-balancers-port-specifications>`

(network-load-balancers-listen-addresses)=
### Requirements for listen addresses

The following requirements must be met for valid listen addresses:

- Allowed listen addresses must be defined in the uplink network's `ipv{n}.routes` settings or the project's {config:option}`project-restricted:restricted.networks.subnets` setting (if set).
- The listen address must not overlap with a subnet that is in use with another network or entity in that network.

(network-load-balancers-backend-specifications)=
## Configure backends

You can add backend specifications to the network load balancer to define target addresses (and optionally ports).
The backend target address must be within the same subnet as the network that the load balancer is associated to.

Use the following command to add a backend specification:

```bash
incus network load-balancer backend add <network_name> <listen_address> <backend_name> <listen_ports> <target_address> [<target_ports>]
```

The target ports are optional.
If not specified, the load balancer will use the listen ports for the backend for the backend target ports.

If you want to forward the traffic to different ports, you have two options:

- Specify a single target port to forward traffic from all listen ports to this target port.
- Specify a set of target ports with the same number of ports as the listen ports to forward traffic from the first listen port to the first target port, the second listen port to the second target port, and so on.

### Backend properties

Network load balancer backends have the following properties:

Property          | Type       | Required | Description
:--               | :--        | :--      | :--
`name`            | string     | yes      | Name of the backend
`target_address`  | string     | yes      | IP address to forward to
`target_port`     | string     | no       | Target port(s) (e.g. `70,80-90` or `90`), same as the {ref}`port <network-load-balancers-port-specifications>`'s `listen_port` if empty
`description`     | string     | no       | Description of backend

(network-load-balancers-port-specifications)=
## Configure ports

You can add port specifications to the network load balancer to forward traffic from specific ports on the listen address to specific ports on one or more target backends.

Use the following command to add a port specification:

```bash
incus network load-balancer port add <network_name> <listen_address> <protocol> <listen_ports> <backend_name>[,<backend_name>...]
```

You can specify a single listen port or a set of ports.
The backend(s) specified must have target port(s) settings compatible with the port's listen port(s) setting.

### Port properties

Network load balancer ports have the following properties:

Property          | Type         | Required | Description
:--               | :--          | :--      | :--
`protocol`        | string       | yes      | Protocol for the port(s) (`tcp` or `udp`)
`listen_port`     | string       | yes      | Listen port(s) (e.g. `80,90-100`)
`target_backend`  | backend list | yes      | Backend name(s) to forward to
`description`     | string       | no       | Description of port(s)

## Edit a network load balancer

Use the following command to edit a network load balancer:

```bash
incus network load-balancer edit <network_name> <listen_address>
```

This command opens the network load balancer in YAML format for editing.
You can edit both the general configuration, backend and the port specifications.

## Delete a network load balancer

Use the following command to delete a network load balancer:

```bash
incus network load-balancer delete <network_name> <listen_address>
```
