(network-zones)=
# How to configure network zones

```{note}
Network zones are available for the {ref}`network-ovn` and the {ref}`network-bridge`.
```

Network zones can be used to serve DNS records for Incus networks.

You can use network zones to automatically maintain valid forward and reverse records for all your instances.
This can be useful if you are operating a Incus cluster with multiple instances across many networks.

Having DNS records for each instance makes it easier to access network services running on an instance.
It is also important when hosting, for example, an outbound SMTP service.
Without correct forward and reverse DNS entries for the instance, sent mail might be flagged as potential spam.

Each network can be associated to different zones:

- Forward DNS records - multiple comma-separated zones (no more than one per project)
- IPv4 reverse DNS records - single zone
- IPv6 reverse DNS records - single zone

Incus will then automatically manage forward and reverse records for all instances, network gateways and downstream network ports and serve those zones for zone transfer to the operator’s production DNS servers.

## Project views

Projects have a {config:option}`project-features:features.networks.zones` feature, which is disabled by default.
This controls which project new networks zones are created in.
When this feature is enabled new zones are created in the project, otherwise they are created in the default project.

This allows projects that share a network in the default project (i.e those with `features.networks=false`) to have their own project level DNS zones that give a project oriented
"view" of the addresses on that shared network (which only includes addresses from instances in their project).

## Generated records

### Forward records

If you configure a zone with forward DNS records for `incus.example.net` for your network, it generates records that resolve the following DNS names:

- For all instances in the network: `<instance_name>.incus.example.net`
- For the network gateway: `<network_name>.gw.incus.example.net`
- For downstream network ports (for network zones set on an uplink network with a downstream OVN network): `<project_name>-<downstream_network_name>.uplink.incus.example.net`
- Manual records added to the zone.

You can check the records that are generated with your zone setup with the `dig` command.

This assumes that {config:option}`server-core:core.dns_address` was set to `<DNS_server_IP>:<DNS_server_PORT>`. (Setting that configuration
option causes the backend to immediately start serving on that address.)

In order for the `dig` request to be allowed for a given zone, you must set the
`peers.NAME.address` configuration option for that zone. `NAME` can be anything random. The value must match the
IP address where your `dig` is calling from. You must leave `peers.NAME.key` for that same random `NAME` unset.

For example: `incus network zone set incus.example.net peers.whatever.address=192.0.2.1`.

```{note}
It is not enough for the address to be of the same machine that `dig` is calling from; it needs to
match as a string with what the DNS server in `incus` thinks is the exact remote address. `dig` binds to
`0.0.0.0`, therefore the address you need is most likely the same that you provided to `core.dns_address`.
```

For example, running `dig @<DNS_server_IP> -p <DNS_server_PORT> axfr incus.example.net` might give the following output:

```{terminal}
:input: dig @192.0.2.200 -p 1053 axfr incus.example.net

incus.example.net.                        3600 IN SOA  incus.example.net. ns1.incus.example.net. 1669736788 120 60 86400 30
incus.example.net.                        300  IN NS   ns1.incus.example.net.
inctest.gw.incus.example.net.             300  IN A    192.0.2.1
inctest.gw.incus.example.net.             300  IN AAAA fd42:4131:a53c:7211::1
default-ovntest.uplink.incus.example.net. 300  IN A    192.0.2.20
default-ovntest.uplink.incus.example.net. 300  IN AAAA fd42:4131:a53c:7211:216:3eff:fe4e:b794
c1.incus.example.net.                     300  IN AAAA fd42:4131:a53c:7211:216:3eff:fe19:6ede
c1.incus.example.net.                     300  IN A    192.0.2.125
manualtest.incus.example.net.             300  IN A    8.8.8.8
incus.example.net.                        3600 IN SOA  incus.example.net. ns1.incus.example.net. 1669736788 120 60 86400 30
```

### Reverse records

If you configure a zone for IPv4 reverse DNS records for `2.0.192.in-addr.arpa` for a network using `192.0.2.0/24`, it generates reverse `PTR` DNS records for addresses from all projects that are referencing that network via one of their forward zones.

For example, running `dig @<DNS_server_IP> -p <DNS_server_PORT> axfr 2.0.192.in-addr.arpa` might give the following output:

```{terminal}
:input: dig @192.0.2.200 -p 1053 axfr 2.0.192.in-addr.arpa

2.0.192.in-addr.arpa.                  3600 IN SOA  2.0.192.in-addr.arpa. ns1.2.0.192.in-addr.arpa. 1669736828 120 60 86400 30
2.0.192.in-addr.arpa.                  300  IN NS   ns1.2.0.192.in-addr.arpa.
1.2.0.192.in-addr.arpa.                300  IN PTR  inctest.gw.incus.example.net.
20.2.0.192.in-addr.arpa.               300  IN PTR  default-ovntest.uplink.incus.example.net.
125.2.0.192.in-addr.arpa.              300  IN PTR  c1.incus.example.net.
2.0.192.in-addr.arpa.                  3600 IN SOA  2.0.192.in-addr.arpa. ns1.2.0.192.in-addr.arpa. 1669736828 120 60 86400 30
```

(network-dns-server)=
## Enable the built-in DNS server

To make use of network zones, you must enable the built-in DNS server.

To do so, set the {config:option}`server-core:core.dns_address` configuration option to a local address on the Incus server.
To avoid conflicts with an existing DNS we suggest not using the port 53.
This is the address on which the DNS server will listen.
Note that in a Incus cluster, the address may be different on each cluster member.

```{note}
The built-in DNS server supports only zone transfers through AXFR.
It cannot be directly queried for DNS records.
Therefore, the built-in DNS server must be used in combination with an external DNS server (`bind9`, `nsd`, ...), which will transfer the entire zone from Incus, refresh it upon expiry and provide authoritative answers to DNS requests.

Authentication for zone transfers is configured on a per-zone basis, with peers defined in the zone configuration and a combination of IP address matching and TSIG-key based authentication.
```

## Create and configure a network zone

Use the following command to create a network zone:

```bash
incus network zone create <network_zone> [configuration_options...]
```

The following examples show how to configure a zone for forward DNS records, one for IPv4 reverse DNS records and one for IPv6 reverse DNS records, respectively:

```bash
incus network zone create incus.example.net
incus network zone create 2.0.192.in-addr.arpa
incus network zone create 1.0.0.0.1.0.0.0.8.b.d.0.1.0.0.2.ip6.arpa
```

```{note}
Zones must be globally unique, even across projects.
If you get a creation error, it might be due to the zone already existing in another project.
```

You can either specify the configuration options when you create the network or configure them afterwards with the following command:

```bash
incus network zone set <network_zone> <key>=<value>
```

Use the following command to edit a network zone in YAML format:

```bash
incus network zone edit <network_zone>
```

### Configuration options

The following configuration options are available for network zones:

Key                 | Type       | Required | Default | Description
:--                 | :--        | :--      | -       | :--
`peers.NAME.address`| string     | no       | -       | IP address of a DNS server
`peers.NAME.key`    | string     | no       | -       | TSIG key for the server
`dns.nameservers`   | string set | no       | -       | Comma-separated list of DNS server FQDNs (for NS records)
`network.nat`       | bool       | no       | `true`  | Whether to generate records for NAT-ed subnets
`user.*`            | *          | no       | -       | User-provided free-form key/value pairs

```{note}
When generating the TSIG key using `tsig-keygen`, the key name must follow the format `<zone_name>_<peer_name>.`.
For example, if your zone name is `incus.example.net` and the peer name is `bind9`, then the key name must be `incus.example.net_bind9.`.
If this format is not followed, zone transfer might fail.
```

## Add a network zone to a network

To add a zone to a network, set the corresponding configuration option in the network configuration:

- For forward DNS records: `dns.zone.forward`
- For IPv4 reverse DNS records: `dns.zone.reverse.ipv4`
- For IPv6 reverse DNS records: `dns.zone.reverse.ipv6`

For example:

```bash
incus network set <network_name> dns.zone.forward="incus.example.net"
```

Zones belong to projects and are tied to the `networks` features of projects.
You can restrict projects to specific domains and sub-domains through the {config:option}`project-restricted:restricted.networks.zones` project configuration key.

## Add custom records

A network zone automatically generates forward and reverse records for all instances, network gateways and downstream network ports.
If required, you can manually add custom records to a zone.

To do so, use the [`incus network zone record`](incus_network_zone_record.md) command.

### Create a record

Use the following command to create a record:

```bash
incus network zone record create <network_zone> <record_name>
```

This command creates an empty record without entries and adds it to a network zone.

#### Record properties

Records have the following properties:

Property          | Type       | Required | Description
:--               | :--        | :--      | :--
`name`            | string     | yes      | Unique name of the record
`description`     | string     | no       | Description of the record
`entries`         | entry list | no       | A list of DNS entries
`config`          | string set | no       | Configuration options as key/value pairs (only `user.*` custom keys supported)

### Add or remove entries

To add an entry to the record, use the following command:

```bash
incus network zone record entry add <network_zone> <record_name> <type> <value> [--ttl <TTL>]
```

This command adds a DNS entry with the specified type and value to the record.

For example, to create a dual-stack web server, add a record with two entries similar to the following:

```bash
incus network zone record entry add <network_zone> <record_name> A 1.2.3.4
incus network zone record entry add <network_zone> <record_name> AAAA 1234::1234
```

You can use the `--ttl` flag to set a custom time-to-live (in seconds) for the entry.
Otherwise, the default of 300 seconds is used.

You cannot edit an entry (except if you edit the full record with [`incus network zone record edit`](incus_network_zone_record_edit.md)), but you can delete entries with the following command:

```bash
incus network zone record entry remove <network_zone> <record_name> <type> <value>
```
