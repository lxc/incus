(exp-security)=
# About security

% Include content from [../../README.md](../../README.md)
```{include} ../../README.md
    :start-after: <!-- Include start security -->
    :end-before: <!-- Include end security -->
```

See the following sections for detailed information.

If you discover a security issue, see the [Incus security policy](https://github.com/lxc/incus/blob/main/SECURITY.md) for information on how to report the issue.

## Supported versions

Never use unsupported Incus versions in a production environment.

% Include content from [../../SECURITY.md](../../SECURITY.md)
```{include} ../../SECURITY.md
    :start-after: <!-- Include start supported versions -->
    :end-before: <!-- Include end supported versions -->
```

(security-daemon-access)=
## Access to the Incus daemon

Incus is a daemon that can be accessed locally over a Unix socket or, if configured, remotely over a {abbr}`TLS (Transport Layer Security)` socket.
Anyone with access to the socket can fully control Incus, which includes the ability to attach host devices and file systems or to tweak the security features for all instances.

Therefore, make sure to restrict the access to the daemon to trusted users.

### Local access to the Incus daemon

The Incus daemon runs as root and provides a Unix socket for local communication.
Access control for Incus is based on group membership.
The root user and all members of the `incus-admin` group can interact with the local daemon.

````{important}
% Include content from [../../README.md](../../README.md)
```{include} ../../README.md
    :start-after: <!-- Include start security note -->
    :end-before: <!-- Include end security note -->
```
````

(security_remote_access)=
### Access to the remote API

By default, access to the daemon is only possible locally.
By setting the `core.https_address` configuration option, you can expose the same API over the network on a {abbr}`TLS (Transport Layer Security)` socket.
See {ref}`server-expose` for instructions.
Remote clients can then connect to Incus and access any image that is marked for public use.

There are several ways to authenticate remote clients as trusted clients to allow them to access the API.
See {ref}`authentication` for details.

In a production setup, you should set `core.https_address` to the single address where the server should be available (rather than any address on the host).
In addition, you should set firewall rules to allow access to the Incus port only from authorized hosts/subnets.

(container-security)=
## Container security

Incus containers can use a wide range of features for security.

By default, containers are *unprivileged*, meaning that they operate inside a user namespace, restricting the abilities of users in the container to that of regular users on the host with limited privileges on the devices that the container owns.

If data sharing between containers isn't needed, you can enable {config:option}`instance-security:security.idmap.isolated`, which will use non-overlapping UID/GID maps for each container, preventing potential {abbr}`DoS (Denial of Service)` attacks on other containers.

Incus can also run *privileged* containers.
Note, however, that those aren't root safe, and a user with root access in such a container will be able to DoS the host as well as find ways to escape confinement.

More details on container security and the kernel features we use can be found on the
[LXC security page](https://linuxcontainers.org/lxc/security/).

### Container name leakage

The default server configuration makes it easy to list all cgroups on a system and, by extension, all running containers.

You can prevent this name leakage by blocking access to `/sys/kernel/slab` and `/proc/sched_debug` before you start any containers.
To do so, run the following commands:

    chmod 400 /proc/sched_debug
    chmod 700 /sys/kernel/slab/

## Network security

Make sure to configure your network interfaces to be secure.
Which aspects you should consider depends on the networking mode you decide to use.

### Bridged NIC security

The default networking mode in Incus is to provide a "managed" private network bridge that each instance connects to.
In this mode, there is an interface on the host called `incusbr0` that acts as the bridge for the instances.

The host runs an instance of `dnsmasq` for each managed bridge, which is responsible for allocating IP addresses and providing both authoritative and recursive DNS services.

Instances using DHCPv4 will be allocated an IPv4 address, and a DNS record will be created for their instance name.
This prevents instances from being able to spoof DNS records by providing false host name information in the DHCP request.

The `dnsmasq` service also provides IPv6 router advertisement capabilities.
This means that instances will auto-configure their own IPv6 address using SLAAC, so no allocation is made by `dnsmasq`.
However, instances that are also using DHCPv4 will also get an AAAA DNS record created for the equivalent SLAAC IPv6 address.
This assumes that the instances are not using any IPv6 privacy extensions when generating IPv6 addresses.

In this default configuration, whilst DNS names cannot not be spoofed, the instance is connected to an Ethernet bridge and can transmit any layer 2 traffic that it wishes, which means an instance that is not trusted can effectively do MAC or IP spoofing on the bridge.

In the default configuration, it is also possible for instances connected to the bridge to modify the Incus host's IPv6 routing table by sending (potentially malicious) IPv6 router advertisements to the bridge.
This is because the `incusbr0` interface is created with `/proc/sys/net/ipv6/conf/incusbr0/accept_ra` set to `2`, meaning that the Incus host will accept router advertisements even though `forwarding` is enabled (see [`/proc/sys/net/ipv4/*` Variables](https://www.kernel.org/doc/Documentation/networking/ip-sysctl.txt) for more information).

However, Incus offers several bridged {abbr}`NIC (Network interface controller)` security features that can be used to control the type of traffic that an instance is allowed to send onto the network.
These NIC settings should be added to the profile that the instance is using, or they can be added to individual instances, as shown below.

The following security features are available for bridged NICs:

Key                      | Type      | Default           | Required  | Description
:--                      | :--       | :--               | :--       | :--
`security.mac_filtering` | bool      | `false`           | no        | Prevent the instance from spoofing another instance's MAC address
`security.ipv4_filtering`| bool      | `false`           | no        | Prevent the instance from spoofing another instance's IPv4 address (enables `mac_filtering`)
`security.ipv6_filtering`| bool      | `false`           | no        | Prevent the instance from spoofing another instance's IPv6 address (enables `mac_filtering`)

One can override the default bridged NIC settings from the profile on a per-instance basis using:

```
incus config device override <instance> <NIC> security.mac_filtering=true
```

Used together, these features can prevent an instance connected to a bridge from spoofing MAC and IP addresses.
These options are implemented using either `xtables` (`iptables`, `ip6tables` and `ebtables`) or `nftables`, depending on what is available on the host.

It's worth noting that those options effectively prevent nested containers from using the parent network with a different MAC address (i.e using bridged or `macvlan` NICs).

The IP filtering features block ARP and NDP advertisements that contain a spoofed IP, as well as blocking any packets that contain a spoofed source address.

If `security.ipv4_filtering` or `security.ipv6_filtering` is enabled and the instance cannot be allocated an IP address (because `ipvX.address=none` or there is no DHCP service enabled on the bridge), then all IP traffic for that protocol is blocked from the instance.

When `security.ipv6_filtering` is enabled, IPv6 router advertisements are blocked from the instance.

When `security.ipv4_filtering` or `security.ipv6_filtering` is enabled, any Ethernet frames that are not ARP, IPv4 or IPv6 are dropped.
This prevents stacked VLAN Q-in-Q (802.1ad) frames from bypassing the IP filtering.

### Routed NIC security

An alternative networking mode is available called "routed".
It provides a virtual Ethernet device pair between container and host.
In this networking mode, the Incus host functions as a router, and static routes are added to the host directing traffic for the container's IPs towards the container's `veth` interface.

By default, the `veth` interface created on the host has its `accept_ra` setting disabled to prevent router advertisements from the container modifying the IPv6 routing table on the Incus host.
In addition to that, the `rp_filter` on the host is set to `1` to prevent source address spoofing for IPs that the host does not know the container has.
