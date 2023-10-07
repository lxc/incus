(network-bridge-resolved)=
# How to integrate with `systemd-resolved`

If the system that runs Incus uses `systemd-resolved` to perform DNS lookups, you should notify `resolved` of the domains that Incus can resolve.
To do so, add the DNS servers and domains provided by a Incus network bridge to the `resolved` configuration.

```{note}
The `dns.mode` option (see {ref}`network-bridge-options`) must be set to `managed` or `dynamic` if you want to use this feature.

Depending on the configured `dns.domain`, you might need to disable DNSSEC in `resolved` to allow for DNS resolution.
This can be done through the `DNSSEC` option in `resolved.conf`.
```

(network-bridge-resolved-configure)=
## Configure resolved

To add a network bridge to the `resolved` configuration, specify the DNS addresses and domains for the respective bridge.

DNS address
: You can use the IPv4 address, the IPv6 address or both.
  The address must be specified without the subnet netmask.

  To retrieve the IPv4 address for the bridge, use the following command:

      incus network get <network_bridge> ipv4.address

  To retrieve the IPv6 address for the bridge, use the following command:

      incus network get <network_bridge> ipv6.address

DNS domain
: To retrieve the DNS domain name for the bridge, use the following command:

      incus network get <network_bridge> dns.domain

  If this option is not set, the default domain name is `incus`.

Use the following commands to configure `resolved`:

    resolvectl dns <network_bridge> <dns_address>
    resolvectl domain <network_bridge> ~<dns_domain>

```{note}
When configuring `resolved` with the DNS domain name, you should prefix the name with `~`.
The `~` tells `resolved` to use the respective name server to look up only this domain.

Depending on which shell you use, you might need to include the DNS domain in quotes to prevent the `~` from being expanded.
```

For example:

    resolvectl dns incusbr0 192.0.2.10
    resolvectl domain incusbr0 '~incus'

```{note}
Alternatively, you can use the `systemd-resolve` command.
This command has been deprecated in newer releases of `systemd`, but it is still provided for backwards compatibility.

    systemd-resolve --interface <network_bridge> --set-domain ~<dns_domain> --set-dns <dns_address>
```

The `resolved` configuration persists as long as the bridge exists.
You must repeat the commands after each reboot and after Incus is restarted, or make it persistent as described below.

## Make the `resolved` configuration persistent

You can automate the `systemd-resolved` DNS configuration, so that it is applied on system start and takes effect when Incus creates the network interface.

To do so, create a `systemd` unit file named `/etc/systemd/system/incus-dns-<network_bridge>.service` with the following content:

```
[Unit]
Description=Incus per-link DNS configuration for <network_bridge>
BindsTo=sys-subsystem-net-devices-<network_bridge>.device
After=sys-subsystem-net-devices-<network_bridge>.device

[Service]
Type=oneshot
ExecStart=/usr/bin/resolvectl dns <network_bridge> <dns_address>
ExecStart=/usr/bin/resolvectl domain <network_bridge> <dns_domain>
ExecStopPost=/usr/bin/resolvectl revert <network_bridge>
RemainAfterExit=yes

[Install]
WantedBy=sys-subsystem-net-devices-<network_bridge>.device
```

Replace `<network_bridge>` in the file name and content with the name of your bridge (for example, `incusbr0`).
Also replace `<dns_address>` and `<dns_domain>` as described in {ref}`network-bridge-resolved-configure`.

Then enable and start the service with the following commands:

    sudo systemctl daemon-reload
    sudo systemctl enable --now incus-dns-<network_bridge>

If the respective bridge already exists (because Incus is already running), you can use the following command to check that the new service has started:

    sudo systemctl status incus-dns-<network_bridge>.service

You should see output similar to the following:

```{terminal}
:input: sudo systemctl status incus-dns-incusbr0.service

● incus-dns-incusbr0.service - Incus per-link DNS configuration for incusbr0
     Loaded: loaded (/etc/systemd/system/incus-dns-incusbr0.service; enabled; vendor preset: enabled)
     Active: inactive (dead) since Mon 2021-06-14 17:03:12 BST; 1min 2s ago
    Process: 9433 ExecStart=/usr/bin/resolvectl dns incusbr0 n.n.n.n (code=exited, status=0/SUCCESS)
    Process: 9434 ExecStart=/usr/bin/resolvectl domain incusbr0 ~incus (code=exited, status=0/SUCCESS)
   Main PID: 9434 (code=exited, status=0/SUCCESS)
```

To check that `resolved` has applied the settings, use `resolvectl status <network_bridge>`:

```{terminal}
:input: resolvectl status incusbr0

Link 6 (incusbr0)
      Current Scopes: DNS
DefaultRoute setting: no
       LLMNR setting: yes
MulticastDNS setting: no
  DNSOverTLS setting: no
      DNSSEC setting: no
    DNSSEC supported: no
  Current DNS Server: n.n.n.n
         DNS Servers: n.n.n.n
          DNS Domain: ~incus
```
