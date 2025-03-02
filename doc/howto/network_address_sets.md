(network-address-sets)=
# How to use network address sets

```{note}
Network address sets are working with {ref}`ACLs <network-acls> and works only with {ref}`network-ovn` or with {ref}`bridged networks using nftables only <network-bridge-firewall>`.
```


Network `Address Sets` are a list of either IPv4, IPv6 addresses with or without CIDR suffix. They can be used in Source or Destination field of {ref}`ACLs <rule-properties>`.


## Address Set Creation


Use the following command to create an address set.

```bash
incus network address-set create <name> [configuration_options...]
```

This will create an address set without any addresses, after this you can {ref}`add addresses <manage-addresses-in-set>`.

Address sets follows the same rules than ACLs for naming:

- Names must be between 1 and 63 characters long.
- Names must be made up exclusively of letters, numbers and dashes from the ASCII table.
- Names must not start with a digit or a dash.
- Names must not end with a dash.

### Address Set properties

ACLs have the following properties:

Property         | Type       | Required | Description
:--              | :--        | :--      | :--
`name`           | string     | yes      | Unique name of the network address set in the project
`description`    | string     | no       | Description of the network address set
`addresses`      | string list| no       | Ingress traffic rules
`external_ids`   | string set | no       | Configuration options as key/value pairs (only `user.*` custom keys supported)


(manage-addresses-in-set=)
## Add or remove addresses

Adding addresses is pretty straightforward:

```bash
incus network address-set add-addr <name> <address1> <address2>
```

There is no restriction about the kind of address you are appending in your set, a mix of IPv4, IPv6 and CIDR can be used without disruption.

In order to remove addresses we apply the same logic, however if you want to delete multiple addresses at the same time `--force` flag must be used:


```bash
incus network address-set del-addr <name> <address1>
incus network address-set del-addr --force <name> <address1> <address2>
```

## Use of address sets in ACL rules

In order to use an address set in an {ref}`ACL <network-acls-address-sets>`, we need to prepend `name` with `$` (you need to escape the dollar in command line). Then we can refer the address set in `source` or `destination` fields of an ACL rule.