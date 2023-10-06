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

Key         | Type      | Default           | Description
:--         | :--       | :--               | :--
`gid`       | int       | `0`               | GID of the device owner in the instance
`mode`      | int       | `0660`            | Mode of the device in the instance
`productid` | string    | -                 | The product ID of the Unix device
`required`  | bool      | `false`           | Whether this device is required to start the instance (the default is `false`, and all devices can be hotplugged)
`uid`       | int       | `0`               | UID of the device owner in the instance
`vendorid`  | string    | -                 | The vendor ID of the Unix device
