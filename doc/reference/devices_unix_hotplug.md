(devices-unix-hotplug)=
# Type: `unix-hotplug`

```{note}
The `unix-hotplug` device type is supported for containers.
It supports hotplugging.
```

Unix hotplug devices make the requested Unix device appear as a device in the instance (under `/dev`).
If the device exists on the host system, you can read from it and write to it.

The implementation depends on `systemd-udev` to be run on the host.

## Device options

`unix-hotplug` devices have the following device options:

% Include content from [../config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group devices-unix-hotplug start -->
    :end-before: <!-- config group devices-unix-hotplug end -->
```
