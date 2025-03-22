(network-integrations)=
# How to configure network integrations

```{note}
Network integrations are currently only available for the {ref}`network-ovn`.
```

Network integrations can be used to connect networks on the local Incus
deployment to remote networks hosted on Incus or other platforms.

## OVN interconnection

At this time the only type of network integrations supported is OVN
which makes use of OVN interconnection gateways to peer OVN networks
together across multiple deployments.

For this to work one needs a working OVN interconnection setup with:

- OVN interconnection `NorthBound` and `SouthBound` databases
- Two or more OVN clusters with their availability-zone names set properly (`name` property)
- All OVN clusters need to have the `ovn-ic` daemon running
- OVN clusters configured to advertise and learn routes from interconnection
- At least one server marked as an OVN interconnection gateway

More details can be found in the [upstream documentation](https://docs.ovn.org/en/latest/tutorials/ovn-interconnection.html).

## Creating a network integration

A network integration can be created with `incus network integration create`.
Integrations are global to the Incus deployment, they are not tied to a network or project.

An example for an OVN integration would be:

```
incus network integration create ovn-region ovn
incus network integration set ovn-region ovn.northbound_connection tcp:[192.0.2.12]:6645,tcp:[192.0.3.13]:6645,tcp:[192.0.3.14]:6645
incus network integration set ovn-region ovn.southbound_connection tcp:[192.0.2.12]:6646,tcp:[192.0.3.13]:6646,tcp:[192.0.3.14]:6646
```

## Using a network integration

To make use of a network integration, one needs to peer with it.

This is done through `incus network peer create`, for example:

```
incus network peer create default region ovn-region --type=remote
```

## Integration properties

Address sets have the following properties:

Property         | Type     | Required | Description
:--              | :--      | :--      | :--
`name`           | string   | yes      | Name of the network integration
`description`    | string   | no       | Description of the network integration
`type`           | string   | yes      | Type of network integration (currently only `ovn`)

## Integration configuration options

The following configuration options are available for all network integrations:

% Include content from [../config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group network_integration-common start -->
    :end-before: <!-- config group network_integration-common end -->
```

### OVN configuration options

Those options are specific to the OVN network integrations:

% Include content from [../config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group network_integration-ovn start -->
    :end-before: <!-- config group network_integration-ovn end -->
```
