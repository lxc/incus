(server)=
# Server configuration

The Incus server can be configured through a set of key/value configuration options.

The key/value configuration is namespaced.
The following options are available:

- {ref}`server-options-core`
- {ref}`server-options-acme`
- {ref}`server-options-cluster`
- {ref}`server-options-images`
- {ref}`server-options-logging`
- {ref}`server-options-misc`
- {ref}`server-options-oidc`
- {ref}`server-options-openfga`

See {ref}`server-configure` for instructions on how to set the configuration options.

```{note}
Options marked with a `global` scope are immediately applied to all cluster members.
Options with a `local` scope must be set on a per-member basis.
```

(server-options-core)=
## Core configuration

The following server options control the core daemon configuration:

% Include content from [config_options.txt](config_options.txt)
```{include} config_options.txt
    :start-after: <!-- config group server-core start -->
    :end-before: <!-- config group server-core end -->
```

(server-options-acme)=
## ACME configuration

The following server options control the {ref}`ACME <authentication-server-certificate>` configuration:

% Include content from [config_options.txt](config_options.txt)
```{include} config_options.txt
    :start-after: <!-- config group server-acme start -->
    :end-before: <!-- config group server-acme end -->
```

(server-options-oidc)=
## OpenID Connect configuration

The following server options configure external user authentication through {ref}`authentication-openid`:

% Include content from [config_options.txt](config_options.txt)
```{include} config_options.txt
    :start-after: <!-- config group server-oidc start -->
    :end-before: <!-- config group server-oidc end -->
```

(server-options-openfga)=
## OpenFGA configuration

The following server options configure external user authorization through {ref}`authorization-openfga`:

% Include content from [config_options.txt](config_options.txt)
```{include} config_options.txt
    :start-after: <!-- config group server-openfga start -->
    :end-before: <!-- config group server-openfga end -->
```

(server-options-cluster)=
## Cluster configuration

The following server options control {ref}`clustering`:

% Include content from [config_options.txt](config_options.txt)
```{include} config_options.txt
    :start-after: <!-- config group server-cluster start -->
    :end-before: <!-- config group server-cluster end -->
```

(server-options-images)=
## Images configuration

The following server options configure how to handle {ref}`images`:

% Include content from [config_options.txt](config_options.txt)
```{include} config_options.txt
    :start-after: <!-- config group server-images start -->
    :end-before: <!-- config group server-images end -->
```

(server-options-logging)=
## Logging configuration

The logging system now supports multiple configurable targets, each identified by a unique name (e.g., `loki01`, `syslog01`).
Each target can be independently configured and assigned specific log types.

### Supported Targets

- `loki` -  For sending logs to a Grafana Loki server
- `syslog` - For sending logs to remote syslog endpoint

### Example configuration

```
logging.loki01.target.type: loki
logging.loki01.target.address: https://loki01.int.example.net
logging.loki01.target.username: foo
logging.loki01.target.password: bar
logging.loki01.types: lifecycle,network-acl
logging.loki01.lifecycle.types: instance

logging.syslog01.target.type: syslog
logging.syslog01.target.address: syslog01.int.example.net
logging.syslog01.target.facility: security
logging.syslog01.types: logging
logging.syslog01.logging.level: warning
```

% Include content from [config_options.txt](config_options.txt)
```{include} config_options.txt
    :start-after: <!-- config group server-logging start -->
    :end-before: <!-- config group server-logging end -->
```

(server-options-misc)=
## Miscellaneous options

The following server options configure server-specific settings for {ref}`instances`, {ref}`OVN <network-ovn>` integration, {ref}`Backups <backups>` and {ref}`storage`:

% Include content from [config_options.txt](config_options.txt)
```{include} config_options.txt
    :start-after: <!-- config group server-miscellaneous start -->
    :end-before: <!-- config group server-miscellaneous end -->
```

(server-options-user)=
## User options

Additional user defined configuration keys are available within the `user.` namespace.
User defined configuration keys are always of type `string` and have `global` scope.
Note that keys starting with `user.ui.` are used for web UI configuration options and are visible even to unauthenticated users.
