(network-physical)=
# Physical network

<!-- Include start physical intro -->
The `physical` network type connects to an existing physical network, which can be a network interface or a bridge, and serves as an uplink network for OVN.
<!-- Include end physical intro -->

This network type allows to specify presets to use when connecting OVN networks to a parent interface or to allow an instance to use a physical interface as a NIC.
In this case, the instance NICs can simply set the `network`option to the network they connect to without knowing any of the underlying configuration details.

(network-physical-options)=
## Configuration options

The following configuration key namespaces are currently supported for the `physical` network type:

- `bgp` (BGP peer configuration)
- `dns` (DNS server and resolution configuration)
- `ipv4` (L3 IPv4 configuration)
- `ipv6` (L3 IPv6 configuration)
- `ovn` (OVN configuration)
- `user` (free-form key/value for user metadata)

```{note}
{{note_ip_addresses_CIDR}}
```

The following configuration options are available for the `physical` network type:

## BGP options

These options configure BGP peering for OVN downstream networks:

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group network_physical-bgp start -->
    :end-before: <!-- config group network_physical-bgp end -->
```

## DNS options

These keys control the DNS servers and search domains used by the physical network:

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group network_physical-dns start -->
    :end-before: <!-- config group network_physical-dns end -->
```

## IPV4 options

These options define the IPv4 configuration for the physical network:

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group network_physical-ipv4 start -->
    :end-before: <!-- config group network_physical-ipv4 end -->
```

## IPV6 options

These options define the IPv6 configuration for the physical network:

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group network_physical-ipv6 start -->
    :end-before: <!-- config group network_physical-ipv6 end -->
```

## OVN options

These options apply when using a physical network as an OVN uplink:

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group network_physical-ovn start -->
    :end-before: <!-- config group network_physical-ovn end -->
```

## Common options

These apply to all physical networks regardless of other features:

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group network_physical-common start -->
    :end-before: <!-- config group network_physical-common end -->
```

(network-physical-features)=
## Supported features

The following features are supported for the `physical` network type:

- {ref}`network-bgp`
