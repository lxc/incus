(network-acls)=
# How to configure network ACLs

```{note}
Network ACLs are available for the {ref}`OVN NIC type <nic-ovn>`, the {ref}`network-ovn` and the {ref}`network-bridge` (with some exceptions, see {ref}`network-acls-bridge-limitations`).
```

Network {abbr}`ACLs (Access Control Lists)` define traffic rules that allow controlling network access between different instances connected to the same network, and access to and from other networks.

Network ACLs can be assigned directly to the {abbr}`NIC (Network Interface Controller)` of an instance or to a network.
When assigned to a network, the ACL applies to all NICs connected to the network.

The instance NICs that have a particular ACL applied (either explicitly or implicitly through a network) make up a logical group, which can be referenced from other rules as a source or destination.
See {ref}`network-acls-groups` for more information.

## Create an ACL

Use the following command to create an ACL:

```bash
incus network acl create <ACL_name> [configuration_options...]
```

This command creates an ACL without rules.
As a next step, {ref}`add rules <network-acls-rules>` to the ACL.

Valid network ACL names must adhere to the following rules:

- Names must be between 1 and 63 characters long.
- Names must be made up exclusively of letters, numbers and dashes from the ASCII table.
- Names must not start with a digit or a dash.
- Names must not end with a dash.

### ACL properties

ACLs have the following properties:

Property         | Type       | Required | Description
:--              | :--        | :--      | :--
`name`           | string     | yes      | Unique name of the network ACL in the project
`description`    | string     | no       | Description of the network ACL
`ingress`        | rule list  | no       | Ingress traffic rules
`egress`         | rule list  | no       | Egress traffic rules
`config`         | string set | no       | Configuration options as key/value pairs (only `user.*` custom keys supported)

(network-acls-rules)=
## Add or remove rules

Each ACL contains two lists of rules:

- *Ingress* rules apply to inbound traffic going towards the NIC.
- *Egress* rules apply to outbound traffic leaving the NIC.

To add a rule to an ACL, use the following command, where `<direction>` can be either `ingress` or `egress`:

```bash
incus network acl rule add <ACL_name> <direction> [properties...]
```

This command adds a rule to the list for the specified direction.

You cannot edit a rule (except if you {ref}`edit the full ACL <network-acls-edit>`), but you can delete rules with the following command:

```bash
incus network acl rule remove <ACL_name> <direction> [properties...]
```

You must either specify all properties needed to uniquely identify a rule or add `--force` to the command to delete all matching rules.

### Rule ordering and priorities

Rules are provided as lists.
However, the order of the rules in the list is not important and does not affect
filtering.

Incus automatically orders the rules based on the `action` property as follows:

- `drop`
- `reject`
- `allow`
- Automatic default action for any unmatched traffic (defaults to `reject`, see {ref}`network-acls-defaults`).

This means that when you apply multiple ACLs to a NIC, there is no need to specify a combined rule ordering.
If one of the rules in the ACLs matches, the action for that rule is taken and no other rules are considered.

### Rule properties

ACL rules have the following properties:

Property          | Type       | Required | Description
:--               | :--        | :--      | :--
`action`          | string     | yes      | Action to take for matching traffic (`allow`, `reject` or `drop`)
`state`           | string     | yes      | State of the rule (`enabled`, `disabled` or `logged`), defaulting to `enabled` if not specified
`description`     | string     | no       | Description of the rule
`source`          | string     | no       | Comma-separated list of CIDR or IP ranges, source subject name selectors (for ingress rules), or empty for any
`destination`     | string     | no       | Comma-separated list of CIDR or IP ranges, destination subject name selectors (for egress rules), or empty for any
`protocol`        | string     | no       | Protocol to match (`icmp4`, `icmp6`, `tcp`, `udp`) or empty for any
`source_port`     | string     | no       | If protocol is `udp` or `tcp`, then a comma-separated list of ports or port ranges (start-end inclusive), or empty for any
`destination_port`| string     | no       | If protocol is `udp` or `tcp`, then a comma-separated list of ports or port ranges (start-end inclusive), or empty for any
`icmp_type`       | string     | no       | If protocol is `icmp4` or `icmp6`, then ICMP type number, or empty for any
`icmp_code`       | string     | no       | If protocol is `icmp4` or `icmp6`, then ICMP code number, or empty for any

(network-acls-selectors)=
### Use selectors in rules

```{note}
This feature is supported only for the {ref}`OVN NIC type <nic-ovn>` and the {ref}`network-ovn`.
```

The `source` field (for ingress rules) and the `destination` field (for egress rules) support using selectors instead of CIDR or IP ranges.

With this method, you can use ACL groups or network selectors to define rules for groups of instances without needing to maintain IP lists or create additional subnets.

(network-acls-groups)=
#### ACL groups

Instance NICs that are assigned a particular ACL (either explicitly or implicitly through a network) make up a logical port group.

Such ACL groups are called *subject name selectors*, and they can be referenced with the name of the ACL in other ACL groups.

For example, if you have an ACL with the name `foo`, you can specify the group of instance NICs that are assigned this ACL as source with `source=foo`.

#### Network selectors

You can use *network subject selectors* to define rules based on the network that the traffic is coming from or going to.

There are two special network subject selectors called `@internal` and `@external`.
They represent network local and external traffic, respectively.
For example:

```bash
source=@internal
```

If your network supports [network peers](network_ovn_peers.md), you can reference traffic to or from the peer connection by using a network subject selector in the format `@<network_name>/<peer_name>`.
For example:

```bash
source=@ovn1/mypeer
```

When using a network subject selector, the network that has the ACL applied to it must have the specified peer connection.
Otherwise, the ACL cannot be applied to it.

### Log traffic

Generally, ACL rules are meant to control the network traffic between instances and networks.
However, you can also use them to log specific network traffic, which can be useful for monitoring, or to test rules before actually enabling them.

To add a rule for logging, create it with the `state=logged` property.
You can then display the log output for all logging rules in the ACL with the following command:

```bash
incus network acl show-log <ACL_name>
```

(network-acls-edit)=
## Edit an ACL

Use the following command to edit an ACL:

```bash
incus network acl edit <ACL_name>
```

This command opens the ACL in YAML format for editing.
You can edit both the ACL configuration and the rules.

## Assign an ACL

After configuring an ACL, you must assign it to a network or an instance NIC.

To do so, add it to the `security.acls` list of the network or NIC configuration.
For networks, use the following command:

```bash
incus network set <network_name> security.acls="<ACL_name>"
```

For instance NICs, use the following command:

```bash
incus config device set <instance_name> <device_name> security.acls="<ACL_name>"
```

(network-acls-defaults)=
## Configure default actions

When one or more ACLs are applied to a NIC (either explicitly or implicitly through a network), a default reject rule is added to the NIC.
This rule rejects all traffic that doesn't match any of the rules in the applied ACLs.

You can change this behavior with the network and NIC level `security.acls.default.ingress.action` and `security.acls.default.egress.action` settings.
The NIC level settings override the network level settings.

For example, to set the default action for inbound traffic to `allow` for all instances connected to a network, use the following command:

```bash
incus network set <network_name> security.acls.default.ingress.action=allow
```

To configure the same default action for an instance NIC, use the following command:

```bash
incus config device set <instance_name> <device_name> security.acls.default.ingress.action=allow
```

(network-acls-bridge-limitations)=
## Bridge limitations

When using network ACLs with a bridge network, be aware of the following limitations:

- Unlike OVN ACLs, bridge ACLs are applied only on the boundary between the bridge and the Incus host.
  This means they can only be used to apply network policies for traffic going to or from external networks.
  They cannot be used for to create {spellexception}`intra-bridge` firewalls, thus firewalls that control traffic between instances connected to the same bridge.
- {ref}`ACL groups and network selectors <network-acls-selectors>` are not supported.
- When using the `iptables` firewall driver, you cannot use IP range subjects (for example, `192.0.2.1-192.0.2.10`).
- Baseline network service rules are added before ACL rules (in their respective INPUT/OUTPUT chains), because we cannot differentiate between INPUT/OUTPUT and FORWARD traffic once we have jumped into the ACL chain.
  Because of this, ACL rules cannot be used to block baseline service rules.
