(network-macvlan)=
# Macvlan network

<!-- Include start macvlan intro -->
Macvlan is a virtual {abbr}`LAN (Local Area Network)` that you can use if you want to assign several IP addresses to the same network interface, basically splitting up the network interface into several sub-interfaces with their own IP addresses.
You can then assign IP addresses based on the randomly generated MAC addresses.
<!-- Include end macvlan intro -->

The `macvlan` network type allows to specify presets to use when connecting instances to a parent interface.
In this case, the instance NICs can simply set the `network` option to the network they connect to without knowing any of the underlying configuration details.

```{note}
If you are using a `macvlan` network, communication between the Incus host and the instances is not possible.
Both the host and the instances can talk to the gateway, but they cannot communicate directly.
```

(network-macvlan-options)=
## Configuration options

The following configuration key namespaces are currently supported for the `macvlan` network type:

- `user` (free-form key/value for user metadata)

```{note}
{{note_ip_addresses_CIDR}}
```

The following configuration options are available for the `macvlan` network type:

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group network_macvlan-common start -->
    :end-before: <!-- config group network_macvlan-common end -->
```
