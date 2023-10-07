(devices-unix-block)=
# Type: `unix-block`

```{note}
The `unix-block` device type is supported for containers.
It supports hotplugging.
```

Unix block devices make the specified block device appear as a device in the instance (under `/dev`).
You can read from the device and write to it.

## Device options

`unix-block` devices have the following device options:

Key         | Type      | Default           | Description
:--         | :--       | :--               | :--
`gid`       | int       | `0`               | GID of the device owner in the instance
`major`     | int       | device on host    | Device major number
`minor`     | int       | device on host    | Device minor number
`mode`      | int       | `0660`            | Mode of the device in the instance
`path`      | string    | -                 | Path inside the instance (one of `source` and `path` must be set)
`required`  | bool      | `true`            | Whether this device is required to start the instance (see {ref}`devices-unix-block-hotplugging`)
`source`    | string    | -                 | Path on the host (one of `source` and `path` must be set)
`uid`       | int       | `0`               | UID of the device owner in the instance

(devices-unix-block-hotplugging)=
## Hotplugging

Hotplugging is enabled if you set `required=false` and specify the `source` option for the device.

In this case, the device is automatically passed into the container when it appears on the host, even after the container starts.
If the device disappears from the host system, it is removed from the container as well.
