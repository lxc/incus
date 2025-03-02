(network-address-sets)=
# How to use network address sets

```{note}
Network address sets are working with {ref}`ACLs <network-acls> and works only with {ref}`network-ovn` or with {ref}`bridged networks using nftables only <network-bridge-firewall>`.
```
Network `Address Sets` are a list of either IPv4, IPv6 addresses with or without CIDR suffix. They can be used in Source of Destination field of {ref}`ACLs <rule-properties>`.