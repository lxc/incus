(devices-unix-char)=
# Type: `unix-char`

```{note}
The `unix-char` device type is supported for containers.
It supports hotplugging.
```

Unix character devices make the specified character device appear as a device in the instance (under `/dev`).
You can read from the device and write to it.

## Device options

`unix-char` devices have the following device options:

Key         | Type      | Default           | Description
:--         | :--       | :--               | :--
`gid`       | int       | `0`               | GID of the device owner in the instance
`major`     | int       | device on host    | Device major number
`minor`     | int       | device on host    | Device minor number
`mode`      | int       | `0660`            | Mode of the device in the instance
`path`      | string    | -                 | Path inside the instance (one of `source` and `path` must be set)
`required`  | bool      | `true`            | Whether this device is required to start the instance (see {ref}`devices-unix-char-hotplugging`)
`source`    | string    | -                 | Path on the host (one of `source` and `path` must be set)
`uid`       | int       | `0`               | UID of the device owner in the instance

(devices-unix-char-hotplugging)=
## Hotplugging

% Include content from [devices_unix_block.md](device_unix_block.md)
```{include} devices_unix_block.md
    :start-after: Hotplugging
```
