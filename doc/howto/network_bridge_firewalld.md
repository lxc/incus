(network-bridge-firewall)=
# How to configure your firewall

Linux firewalls are based on `netfilter`.
Incus uses the same subsystem, which can lead to connectivity issues.

If you run a firewall on your system, you might need to configure it to allow network traffic between the managed Incus bridge and the host.
Otherwise, some network functionality (DHCP, DNS and external network access) might not work as expected.

You might also see conflicts between the rules defined by your firewall (or another application) and the firewall rules that Incus adds.
For example, your firewall might erase Incus rules if it is started after the Incus daemon, which might interrupt network connectivity to the instance.

## `xtables` vs. `nftables`

There are different userspace commands to add rules to `netfilter`: `xtables` (`iptables` for IPv4 and `ip6tables` for IPv6) and `nftables`.

`xtables` provides an ordered list of rules, which might cause issues if multiple systems add and remove entries from the list.
`nftables` adds the ability to separate rules into namespaces, which helps to separate rules from different applications.
However, if a packet is blocked in one namespace, it is not possible for another namespace to allow it.
Therefore, rules in one namespace can still affect rules in another namespace, and firewall applications can still impact Incus network functionality.

If your system supports and uses `nftables`, Incus detects this and switches to `nftables` mode.
In this mode, Incus adds its rules into the `nftables`, using its own `nftables` namespace.

## Use Incus' firewall

By default, managed Incus bridges add firewall rules to ensure full functionality.
If you do not run another firewall on your system, you can let Incus manage its firewall rules.

To enable or disable this behavior, use the `ipv4.firewall` or `ipv6.firewall` {ref}`configuration options <network-bridge-options>`.

## Use another firewall

Firewall rules added by other applications might interfere with the firewall rules that Incus adds.
Therefore, if you use another firewall, you should disable Incus' firewall rules.
You must also configure your firewall to allow network traffic between the instances and the Incus bridge, so that the Incus instances can access the DHCP and DNS server that Incus runs on the host.

See the following sections for instructions on how to disable Incus' firewall rules and how to properly configure `firewalld` and UFW, respectively.

### Disable Incus' firewall rules

Run the following commands to prevent Incus from setting firewall rules for a specific network bridge (for example, `incusbr0`):

    incus network set <network_bridge> ipv6.firewall false
    incus network set <network_bridge> ipv4.firewall false

### `firewalld`: Add the bridge to the trusted zone

To allow traffic to and from the Incus bridge in `firewalld`, add the bridge interface to the `trusted` zone.
To do this permanently (so that it persists after a reboot), run the following commands:

    sudo firewall-cmd --zone=trusted --change-interface=<network_bridge> --permanent
    sudo firewall-cmd --reload

For example:

    sudo firewall-cmd --zone=trusted --change-interface=incusbr0 --permanent
    sudo firewall-cmd --reload

<!-- Include start warning -->

```{warning}
The commands given above show a simple example configuration.
Depending on your use case, you might need more advanced rules and the example configuration might inadvertently introduce a security risk.
```

<!-- Include end warning -->

### UFW: Add rules for the bridge

If UFW has a rule to drop all unrecognized traffic, it blocks the traffic to and from the Incus bridge.
In this case, you must add rules to allow traffic to and from the bridge, as well as allowing traffic forwarded to it.

To do so, run the following commands:

    sudo ufw allow in on <network_bridge>
    sudo ufw route allow in on <network_bridge>
    sudo ufw route allow out on <network_bridge>

For example:

    sudo ufw allow in on incusbr0
    sudo ufw route allow in on incusbr0
    sudo ufw route allow out on incusbr0

% Repeat warning from above
```{include} network_bridge_firewalld.md
    :start-after: <!-- Include start warning -->
    :end-before: <!-- Include end warning -->
```

(network-incus-docker)=
## Prevent connectivity issues with Incus and Docker

Running Incus and Docker on the same host can cause connectivity issues.
A common reason for these issues is that Docker sets the global FORWARD policy to `drop`, which prevents Incus from forwarding traffic and thus causes the instances to lose network connectivity.
See [Docker on a router](https://docs.docker.com/network/iptables/#docker-on-a-router) for detailed information.

There are different ways of working around this problem:

Uninstall Docker
: The easiest way to prevent such issues is to uninstall Docker from the system that runs Incus and restart the system.
  You can run Docker inside a Incus container or virtual machine instead.

Enable IPv4 forwarding
: If uninstalling Docker is not an option, enabling IPv4 forwarding before the Docker service starts will prevent Docker from modifying the global FORWARD policy.
  Incus bridge networks enable this setting normally.
  However, if Incus starts after Docker, then Docker will already have modified the global FORWARD policy.

  ```{warning}
  Enabling IPv4 forwarding can cause your Docker container ports to be reachable from any machine on your local network.
  Depending on your environment, this might be undesirable.
  See [local network container access issue](https://github.com/moby/moby/issues/14041) for more information.
  ```

  To enable IPv4 forwarding before Docker starts, ensure that the following `sysctl` setting is enabled:

      net.ipv4.conf.all.forwarding=1

  ```{important}
  You must make this setting persistent across host reboots.

  One way of doing this is to add a file to the `/etc/sysctl.d/` directory using the following commands:

      echo "net.ipv4.conf.all.forwarding=1" > /etc/sysctl.d/99-forwarding.conf
      systemctl restart systemd-sysctl

  ```

Allow egress network traffic flows
: If you do not want the Docker container ports to be potentially reachable from any machine on your local network, you can apply a more complex solution provided by Docker.

  Use the following commands to explicitly allow egress network traffic flows from your Incus managed bridge interface:

      iptables -I DOCKER-USER -i <network_bridge> -j ACCEPT
      iptables -I DOCKER-USER -o <network_bridge> -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT

  For example, if your Incus managed bridge is called `incusbr0`, you can allow egress traffic to flow using the following commands:

      iptables -I DOCKER-USER -i incusbr0 -j ACCEPT
      iptables -I DOCKER-USER -o incusbr0 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT

  ```{important}
  You  must make these firewall rules persistent across host reboots.
  How to do this depends on your Linux distribution.
  ```
