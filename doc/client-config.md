(client-config)=
# CLI configuration file
The Incus CLI client uses a configuration file to store environment data and modify its behavior. This documentation describes the file structure and its options.

```{note}
Changing this configuration file manually may leave the CLI in an inconsistent state, so you should only edit it via CLI commands such as [`incus remote`](incus_remote.md) and [`incus alias`](incus_alias.md).
```

## The configuration file
By default, the Incus CLI will use the `$HOME/.config/incus/config.yml`, but it can also use an arbitrary file provided by the `INCUS_CONF` environment variable.

If the file or its path don't exist, it will use the default:

```yaml
default-remote: local
remotes:
  images:
    protocol: simplestreams
    public: true
    addr: https://images.linuxcontainers.org
  local:
    protocol: incus
    public: false
    addr: unix://
aliases: {}
defaults:
  list_format: ""
  console_type: ""
  console_spice_command: ""
  no_color: false
```

## `remotes`
Incus remote information managed by [`incus remote`](incus_remote.md) commands is stored in the `remotes` section. Remotes may have the following configuration keys.

### `addr`
Address of the remote server. Accepts IPs, FQDNs and URLs. Multiple addresses can be provided, separated with commas `addr1,addr2,...`.

### `last_working_address`
Used by the CLI to register the last working address of a remote, if the remote has multiple addresses.

### `auth_type`
Method that the client will use to authenticate to the server API. Accepts `tls` and `oidc`.

### `keepalive`
Timeout in seconds that the `keepalive` feature will maintain the connection opened.

### `project`
Default project used by the CLI when using that remote. It's defined by the [`incus project switch`](incus_project_switch.md) command. It's overwritten by the `--project` flag and the `INCUS_PROJECT` environment variable.

### `protocol`
The protocol the CLI will use to communicate with the remote. This defines if the remote will be used as an Incus or image server. Accepts the following options:

- `incus`: private Incus server accessible over the network
- `oci`: [Open Container Initiative (OCI)](https://opencontainers.org/) server that provides application container images
- `public`: Incus server accessible over the network to provide images only
- `simplestream`: [Simple Stream](https://git.launchpad.net/simplestreams/tree/) server that provide images

### `credentials_helper`
Defines a helper command that will handle OCI service authentication.

### `public`
Defines if the remote is an image-only server (public).

## `default_remotes`
Defines the default Incus remote used by the CLI to execute commands. It's defined by the [`incus remote switch`](incus_remote_switch.md) command. This configuration is overwritten by the environment variable `INCUS_REMOTE` or by the `<remote>:` prefix.

## `aliases`
Stores the alias managed by the [`incus alias`](incus_alias.md) command. Each alias is a key/value pair that consists of the alias name and the alias command (enclosed by quotes).

## `defaults`
Determines the default CLI behavior for certain CLI commands.

### `list_format`
Defines how list outputs will be displayed. Equivalent to the `--format` (`-f`) flag. Accepts `csv`, `json`, `table`, `yaml`, `compact` and `markdown` formats.

### `console_type`
Defines the default console type used by the [`incus console`](incus_console.md) command. Available options are:

- `console`: text based console
- `vga`: graphic UI console

### `console_spice_command`
Defines an alternative SPICE command to provide the VGA console used by [`incus console`](incus_console.md).

### `no_color`
Disables CLI colors.
