(network-address-sets)=
# How to use network address sets

```{note}
Network address sets are working with {ref}`ACLs <network-acls>` and work only with {ref}`network-ovn` or with {ref}`bridged networks <network-bridge-firewall>` using `nftables` only.
```

Network address sets are a list of either IPv4, IPv6 addresses with or without CIDR suffix. They can be used in source or destination fields of {ref}`ACLs <network-acls-rules-properties>`.

## Address set properties

Address sets have the following properties:

Property         | Type         | Required | Description
:--              | :--          | :--      | :--
`name`           | string       | yes      | Name of the network address set
`description`    | string       | no       | Description of the network address set
`addresses`      | string list  | no       | Ingress traffic rules

## Address set configuration options

The following configuration options are available for all network address sets:

% Include content from [../config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group network_address_set-common start -->
    :end-before: <!-- config group network_address_set-common end -->
```

## Creating an address set

Use the following command to create an address set.

```bash
incus network address-set create <name> [configuration_options...]
```

This will create an address set without any addresses, after this you can {ref}`add addresses <manage-addresses-in-set>`.

(manage-addresses-in-set)=
## Add or remove addresses

Adding addresses is pretty straightforward:

```bash
incus network address-set add <name> <address1> <address2>
```

There is no restriction about the kind of address you are appending in your set, a mix of IPv4, IPv6 and CIDR can be used without disruption.

To remove addresses, the same `remove` command can be used instead.

```bash
incus network address-set remove <name> <address1> <address2>
```

## Use of address sets in ACL rules

In order to use an address set in an {ref}`ACL <network-acls-address-sets>`, we need to prepend `name` with `$` (you need to escape the dollar in command line). Then we can refer the address set in `source` or `destination` fields of an ACL rule.
