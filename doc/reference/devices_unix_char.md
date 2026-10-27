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

% Include content from [../config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group devices-unix-char-block start -->
    :end-before: <!-- config group devices-unix-char-block end -->
```

(devices-unix-char-hotplugging)=
## Hotplugging

% Include content from [devices_unix_block.md](device_unix_block.md)
```{include} devices_unix_block.md
    :start-after: Hotplugging
```
