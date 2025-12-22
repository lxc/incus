(network-wireguard)=
# WireGuard network

<!-- Include start wireguard intro -->
{abbr}`WireGuard` is a modern, fast, and secure VPN tunnel that uses state-of-the-art cryptography.
It is designed to be faster, simpler, and more secure than IPsec and OpenVPN.
See [`www.wireguard.com`](https://www.wireguard.com/) for more information.
<!-- Include end wireguard intro -->

The `wireguard` network type allows you to create a WireGuard VPN interface that instances can connect to using the `wireguard` NIC type.
This enables secure point-to-point and site-to-site VPN connections.

WireGuard networks operate at layer 3 (network layer), making them suitable for routing traffic between instances and remote peers.

```{note}
WireGuard requires the `wireguard-tools` package to be installed on the host system.
```

(network-wireguard-options)=
## Configuration options

The following configuration key namespaces are currently supported for the `wireguard` network type:

- `user` (free-form key/value for user metadata)

```{note}
{{note_ip_addresses_CIDR}}
```

The following configuration options are available for the `wireguard` network type:

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group network_wireguard-common start -->
    :end-before: <!-- config group network_wireguard-common end -->
```

You can also configure peers for the `wireguard` network type. Each peer can have the following configuration options:

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group network_wireguard-peers start -->
    :end-before: <!-- config group network_wireguard-peers end -->
```

(network-wireguard-features)=
## Supported features

The following features are supported for the `wireguard` network type:

- **Node-specific configuration**: Each cluster member can have different WireGuard interface configurations
- **Network peering**: WireGuard networks support peering with remote WireGuard peers

(network-wireguard-examples)=
## Examples

### Create a basic WireGuard network

```bash
incus network create wg0 --type=wireguard ipv4.address=10.0.0.1/24
```

### Create a WireGuard network with IPv6

```bash
incus network create wg0 --type=wireguard ipv4.address=10.0.0.1/24 ipv6.address=2001:db8::1/64
```

### Create a WireGuard network with a peer

```bash
incus network create wg0 --type=wireguard \
  ipv4.address=10.0.0.1/24 \
  private_key="<base64_private_key>" \
  peers.remote.public_key="<base64_public_key>" \
  peers.remote.allowed_ips="10.0.0.0/24" \
  peers.remote.endpoint="192.168.1.100:51820" \
  peers.remote.persistent_keepalive=25
```

### Connect an instance to a WireGuard network

```bash
incus launch images:ubuntu/jammy/cloud myinstance --network=wg0
```

The instance will automatically receive an IP address from the WireGuard network's address range.
