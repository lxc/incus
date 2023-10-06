(instances-configure)=
# How to configure instances

You can configure instances by setting {ref}`instance-properties`, {ref}`instance-options`, or by adding and configuring {ref}`devices`.

See the following sections for instructions.

```{note}
To store and reuse different instance configurations, use {ref}`profiles <profiles>`.
```

(instances-configure-options)=
## Configure instance options

You can specify instance options when you {ref}`create an instance <instances-create>`.
Alternatively, you can update the instance options after the instance is created.

````{tabs}
```{group-tab} CLI
Use the [`incus config set`](incus_config_set.md) command to update instance options.
Specify the instance name and the key and value of the instance option:

    incus config set <instance_name> <option_key>=<option_value> <option_key>=<option_value> ...
```

```{group-tab} API
Send a PATCH request to the instance to update instance options.
Specify the instance name and the key and value of the instance option:

    incus query --request PATCH /1.0/instances/<instance_name> --data '{"config": {"<option_key>":"<option_value>","<option_key>":"<option_value>"}}'

See [`PATCH /1.0/instances/{name}`](swagger:/instances/instance_patch) for more information.
```

```{group-tab} UI
To update instance options, go to the {guilabel}`Configuration` tab of the instance detail page and click {guilabel}`Edit instance`.

Find the configuration option that you want to update and change its value.
Click {guilabel}`Save changes` to save the updated configuration.

To configure instance options that are not displayed in the UI, follow the instructions in {ref}`instances-configure-edit`.
```
````

See {ref}`instance-options` for a list of available options and information about which options are available for which instance type.

For example, change the memory limit for your container:

````{tabs}
```{group-tab} CLI
To set the memory limit to 8 GiB, enter the following command:

    incus config set my-container limits.memory=8GiB
```

```{group-tab} API
To set the memory limit to 8 GiB, send the following request:

    incus query --request PATCH /1.0/instances/my-container --data '{"config": {"limits.memory":"8GiB"}}'
```

```{group-tab} UI
To set the memory limit to 8 GiB, go to the {guilabel}`Configuration` tab of the instance detail page and select {guilabel}`Advanced > Resource limits`.
Then click {guilabel}`Edit instance`.

Select {guilabel}`Override` for the **Memory limit** and enter 8 GiB as the absolute value.

![Setting the memory limit for an instance to 8 GiB](/images/UI/limits_memory_example.png)
```
````

```{note}
Some of the instance options are updated immediately while the instance is running.
Others are updated only when the instance is restarted.

See the "Live update" information in the {ref}`instance-options` reference for information about which options are applied immediately while the instance is running.
```

(instances-configure-properties)=
## Configure instance properties

````{tabs}
```{group-tab} CLI
To update instance properties after the instance is created, use the [`incus config set`](incus_config_set.md) command with the `--property` flag.
Specify the instance name and the key and value of the instance property:

    incus config set <instance_name> <property_key>=<property_value> <property_key>=<property_value> ... --property

Using the same flag, you can also unset a property just like you would unset a configuration option:

    incus config unset <instance_name> <property_key> --property

You can also retrieve a specific property value with:

    incus config get <instance_name> <property_key> --property
```

```{group-tab} API
To update instance properties through the API, use the same mechanism as for configuring instance options.
The only difference is that properties are on the root level of the configuration, while options are under the `config` field.

Therefore, to set an instance property, send a PATCH request to the instance:

    incus query --request PATCH /1.0/instances/<instance_name> --data '{"<property_key>":"<property_value>","<property_key>":"property_value>"}}'

To unset an instance property, send a PUT request that contains the full instance configuration that you want except for the property that you want to unset.

See [`PATCH /1.0/instances/{name}`](swagger:/instances/instance_patch) and [`PUT /1.0/instances/{name}`](swagger:/instances/instance_put) for more information.
```

```{group-tab} UI
The Incus UI does not distinguish between instance options and instance properties.
Therefore, you can configure instance properties in the same way as you {ref}`configure instance options <instances-configure-options>`.
```
````

(instances-configure-devices)=
## Configure devices

Generally, devices can be added or removed for a container while it is running.
VMs support hotplugging for some device types, but not all.

See {ref}`devices` for a list of available device types and their options.

```{note}
Every device entry is identified by a name unique to the instance.

Devices from profiles are applied to the instance in the order in which the profiles are assigned to the instance.
Devices defined directly in the instance configuration are applied last.
At each stage, if a device with the same name already exists from an earlier stage, the whole device entry is overridden by the latest definition.

Device names are limited to a maximum of 64 characters.
```

`````{tabs}
````{group-tab} CLI
To add and configure an instance device for your instance, use the [`incus config device add`](incus_config_device_add.md) command.

Specify the instance name, a device name, the device type and maybe device options (depending on the {ref}`device type <devices>`):

    incus config device add <instance_name> <device_name> <device_type> <device_option_key>=<device_option_value> <device_option_key>=<device_option_value> ...

For example, to add the storage at `/share/c1` on the host system to your instance at path `/opt`, enter the following command:

    incus config device add my-container disk-storage-device disk source=/share/c1 path=/opt

To configure instance device options for a device that you have added earlier, use the [`incus config device set`](incus_config_device_set.md) command:

    incus config device set <instance_name> <device_name> <device_option_key>=<device_option_value> <device_option_key>=<device_option_value> ...

```{note}
You can also specify device options by using the `--device` flag when {ref}`creating an instance <instances-create>`.
This is useful if you want to override device options for a device that is provided through a {ref}`profile <profiles>`.
```

To remove a device, use the [`incus config device remove`](incus_config_device_remove.md) command.
See [`incus config device --help`](incus_config_device.md) for a full list of available commands.
````

````{group-tab} API
To add and configure an instance device for your instance, use the same mechanism of patching the instance configuration.
The device configuration is located under the `devices` field of the configuration.

Specify the instance name, a device name, the device type and maybe device options (depending on the {ref}`device type <devices>`):

    incus query --request PATCH /1.0/instances/<instance_name> --data '{"devices": {"<device_name>": {"type":"<device_type>","<device_option_key>":"<device_option_value>","<device_option_key>":"device_option_value>"}}}'

For example, to add the storage at `/share/c1` on the host system to your instance at path `/opt`, enter the following command:

    incus query --request PATCH /1.0/instances/my-container --data '{"devices": {"disk-storage-device": {"type":"disk","source":"/share/c1","path":"/opt"}}}'

See [`PATCH /1.0/instances/{name}`](swagger:/instances/instance_patch) for more information.
````

````{group-tab} UI
The UI currently has limited support for devices.

To attach a device to your instance, you must first create it.
Then you can update your instance configuration (in the same way as you {ref}`configure instance options <instances-configure-options>`) to attach the device to the instance.

```{note}
Some of the devices that are displayed in the instance configuration are inherited from a {ref}`profile <profiles>` or defined through a {ref}`project <projects>`.
These devices cannot be edited for an instance.
```

To add and configure devices that are not currently supported in the UI, follow the instructions in {ref}`instances-configure-edit`.
````

`````

## Display instance configuration

````{tabs}
```{group-tab} CLI
To display the current configuration of your instance, including writable instance properties, instance options, devices and device options, enter the following command:

    incus config show <instance_name> --expanded
```

```{group-tab} API
To retrieve the current configuration of your instance, including writable instance properties, instance options, devices and device options, send a GET request to the instance:

    incus query /1.0/instances/<instance_name>

See [`GET /1.0/instances/{name}`](swagger:/instances/instance_get) for more information.
```

```{group-tab} UI
To view the current configuration of your instance, go to {guilabel}`Instances`, select your instance, and then switch to the {guilabel}`Configuration` tab.

To see the full configuration including instance properties, instance options, devices and device options (also the ones that aren't yet supported by the UI), select {guilabel}`YAML configuration`.
This view shows the full YAML of the instance configuration.
```
````

(instances-configure-edit)=
## Edit the full instance configuration

`````{tabs}
````{group-tab} CLI
To edit the full instance configuration, including writable instance properties, instance options, devices and device options, enter the following command:

    incus config edit <instance_name>

```{note}
For convenience, the [`incus config edit`](incus_config_edit.md) command displays the full configuration including read-only instance properties.
However, you cannot edit those properties.
Any changes are ignored.
```
````

````{group-tab} API
To update the full instance configuration, including writable instance properties, instance options, devices and device options, send a PUT request to the instance:

    incus query --request PUT /1.0/instances/<instance_name> --data '<instance_configuration>'

See [`PUT /1.0/instances/{name}`](swagger:/instances/instance_put) for more information.

```{note}
If you include changes to any read-only instance properties in the configuration you provide, they are ignored.
```
````

````{group-tab} UI
Instead of using the UI forms to configure your instance, you can choose to edit the YAML configuration of the instance.
You must use this method if you need to update any configurations that are not available in the UI.

```{important}
When doing updates, do not navigate away from the YAML configuration without saving your changes.
If you do, your updates are lost.
```

To edit the YAML configuration of your instance, go to the instance detail page, switch to the {guilabel}`Configuration` tab and select {guilabel}`YAML configuration`.
Then click {guilabel}`Edit instance`.

Edit the YAML configuration as required.
Then click {guilabel}`Save changes` to save the updated configuration.

```{note}
For convenience, the YAML contains the full configuration including read-only instance properties.
However, you cannot edit those properties.
Any changes are ignored.
```
````
`````
