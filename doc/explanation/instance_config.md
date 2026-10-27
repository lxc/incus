(instance-config)=
# Instance configuration

The instance configuration consists of different categories:

Instance properties
: Instance properties are specified when the instance is created.
  They include, for example, the instance name and architecture.
  Some of the properties are read-only and cannot be changed after creation, while others can be updated by {ref}`setting their property value <instances-configure-properties>` or {ref}`editing the full instance configuration <instances-configure-edit>`.

  In the YAML configuration, properties are on the top level.

  See {ref}`instance-properties` for a reference of available instance properties.

Instance options
: Instance options are configuration options that are related directly to the instance.
  They include, for example, startup options, security settings, hardware limits, kernel modules, snapshots and user keys.
  These options can be specified as key/value pairs during instance creation (through the `--config key=value` flag).
  After creation, they can be configured with the [`incus config set`](incus_config_set.md) and [`incus config unset`](incus_config_unset.md) commands.

  In the YAML configuration, options are located under the `config` entry.

  See {ref}`instance-options` for a reference of available instance options, and {ref}`instances-configure-options` for instructions on how to configure the options.

Instance devices
: Instance devices are attached to an instance.
  They include, for example, network interfaces, mount points, USB and GPU devices.
  Devices are usually added after an instance is created with the [`incus config device add`](incus_config_device_add.md) command, but they can also be added to a profile or a YAML configuration file that is used to create an instance.

  Each type of device has its own specific set of options, referred to as *instance device options*.

  In the YAML configuration, devices are located under the `devices` entry.

  See {ref}`devices` for a reference of available devices and the corresponding instance device options, and {ref}`instances-configure-devices` for instructions on how to add and configure instance devices.

```{toctree}
:maxdepth: 1
:hidden:

../reference/instance_properties.md
../reference/instance_options.md
../reference/devices.md
../reference/instance_units.md
```
