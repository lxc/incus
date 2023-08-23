(instances-configure)=
# How to configure instances

You can configure instances by setting {ref}`instance-options` or by adding and configuring {ref}`devices`.

See the following sections for instructions.

```{note}
To store and reuse different instance configurations, use {ref}`profiles <profiles>`.
```

(instances-configure-options)=
## Configure instance options

You can specify instance options when you {ref}`create an instance <instances-create>`.

To update instance options after the instance is created, use the [`lxc config set`](lxc_config_set.md) command.
Specify the instance name and the key and value of the instance option:

    lxc config set <instance_name> <option_key>=<option_value> <option_key>=<option_value> ...

See {ref}`instance-options` for a list of available options and information about which options are available for which instance type.

For example, to change the memory limit for your container, enter the following command:

    lxc config set my-container limits.memory=128MiB

```{note}
Some of the instance options are updated immediately while the instance is running.
Others are updated only when the instance is restarted.

See the "Live update" column in the {ref}`instance-options` tables for information about which options are applied immediately while the instance is running.
```

(instances-configure-properties)=
## Configure instance properties

To update instance properties after the instance is created, use the [`lxc config set`](lxc_config_set.md) command with the `--property` flag.
Specify the instance name and the key and value of the instance property:

    lxc config set <instance_name> <property_key>=<property_value> <property_key>=<property_value> ... --property

Using the same flag, you can also unset a property just like you would unset a configuration option:

    lxc config unset <instance_name> <property_key> --property

You can also retrieve a specific property value with:

    lxc config get <instance_name> <property_key> --property

(instances-configure-devices)=
## Configure devices

To add and configure an instance device for your instance, use the [`lxc config device add`](lxc_config_device_add.md) command.
Generally, devices can be added or removed for a container while it is running.
VMs support hotplugging for some device types, but not all.

Specify the instance name, a device name, the device type and maybe device options (depending on the {ref}`device type <devices>`):

    lxc config device add <instance_name> <device_name> <device_type> <device_option_key>=<device_option_value> <device_option_key>=<device_option_value> ...

See {ref}`devices` for a list of available device types and their options.

```{note}
Every device entry is identified by a name unique to the instance.

Devices from profiles are applied to the instance in the order in which the profiles are assigned to the instance.
Devices defined directly in the instance configuration are applied last.
At each stage, if a device with the same name already exists from an earlier stage, the whole device entry is overridden by the latest definition.

Device names are limited to a maximum of 64 characters.
```

For example, to add the storage at `/share/c1` on the host system to your instance at path `/opt`, enter the following command:

    lxc config device add my-container disk-storage-device disk source=/share/c1 path=/opt

To configure instance device options for a device that you have added earlier, use the [`lxc config device set`](lxc_config_device_set.md) command:

    lxc config device set <instance_name> <device_name> <device_option_key>=<device_option_value> <device_option_key>=<device_option_value> ...

```{note}
You can also specify device options by using the `--device` flag when {ref}`creating an instance <instances-create>`.
This is useful if you want to override device options for a device that is provided through a {ref}`profile <profiles>`.
```

To remove a device, use the [`lxc config device remove`](lxc_config_device_remove.md) command.
See [`lxc config device --help`](lxc_config_device.md) for a full list of available commands.

## Display instance configuration

To display the current configuration of your instance, including writable instance properties, instance options, devices and device options, enter the following command:

    lxc config show <instance_name> --expanded

(instances-configure-edit)=
## Edit the full instance configuration

To edit the full instance configuration, including writable instance properties, instance options, devices and device options, enter the following command:

    lxc config edit <instance_name>

```{note}
For convenience, the [`lxc config edit`](lxc_config_edit.md) command displays the full configuration including read-only instance properties.
However, you cannot edit those properties.
Any changes are ignored.
```
