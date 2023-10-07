---
orphan: true
nosearch: true
---

# Configuration options

```{important}
This page shows how to output configuration option documentation.
The content in this page is for demonstration purposes only.
```

Some instance options:

```{config:option} agent.nic_config instance
:shortdesc: Set the name and MTU to be the same as the instance devices
:default: "`false`"
:type: bool
:liveupdate: "`no`"
:condition: Virtual machine

Controls whether to set the name and MTU of the default network interfaces to be the same as the instance devices (this happens automatically for containers)
```

```{config:option} migration.incremental.memory.iterations instance
:shortdesc: Maximum number of transfer operations
:condition: container
:default: 10
:type: integer
:liveupdate: "yes"

Maximum number of transfer operations to go through before stopping the instance
```

```{config:option} cluster.evacuate instance
:shortdesc: What to do when evacuating the instance
:default: "`auto`"
:type: string
:liveupdate: "no"

Controls what to do when evacuating the instance (`auto`, `migrate`, `live-migrate`, or `stop`)
```

These need the `instance` scope to be specified as second argument.
The default scope is `server`, so this argument isn't required.

Some server options:

```{config:option} backups.compression_algorithm server
:shortdesc: Compression algorithm for images
:type: string
:scope: global
:default: "`gzip`"

Compression algorithm to use for new images (`bzip2`, `gzip`, `lzma`, `xz` or `none`)
```

```{config:option} instances.nic.host_name
:shortdesc: How to generate a host name
:type: string
:scope: global
:default: "`random`"

If set to `random`, use the random host interface name as the host name; if set to `mac`, generate a host name in the form `inc<mac_address>` (MAC without leading two digits)
```

```{config:option} instances.placement.scriptlet
:shortdesc: Custom automatic instance placement logic
:type: string
:scope: global

Stores the {ref}`clustering-instance-placement-scriptlet` for custom automatic instance placement logic
```

Any other scope is also possible.
This scope shows that you can use formatting, mainly in the short description and the description, and the available options.

```{config:option} test1 something
:shortdesc: testing

Testing.
```

```{config:option} test2 something
:shortdesc: Hello! **bold** and `code`

This is the real text.

With two paragraphs.

And a list:

- Item
- Item
- Item

And a table:

Key                                 | Type      | Scope     | Default                                          | Description
:--                                 | :---      | :----     | :------                                          | :----------
`acme.agree_tos`                    | bool      | global    | `false`                                          | Agree to ACME terms of service
`acme.ca_url`                       | string    | global    | `https://acme-v02.api.letsencrypt.org/directory` | URL to the directory resource of the ACME service
`acme.domain`                       | string    | global    | -                                                | Domain for which the certificate is issued
`acme.email`                        | string    | global    | -                                                | Email address used for the account registration
```

```{config:option} test3 something
:shortdesc: testing
:default: "`false`"
:type: Type
:liveupdate: Python parses the options, so "no" is converted to "False" - to prevent this, put quotes around the text ("no" or "`no`")
:condition: "yes"
:readonly: "`maybe` - also add quotes if the option starts with code"
:resource: Resource,
:managed: Managed
:required: Required
:scope: (this is something like "global" or "local", **not** the scope of the option (`server`, `instance`, ...)

Content
```

To reference an option, use `{config:option}`.
It is not possible to override the link text.
Except for server options (default), you must specify the scope.

{config:option}`instance:migration.incremental.memory.iterations`

{config:option}`something:test1`

The index is here:
{ref}`config-options`
