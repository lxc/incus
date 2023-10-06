(devices-usb)=
# Type: `usb`

```{note}
The `usb` device type is supported for both containers and VMs.
It supports hotplugging for both containers and VMs.
```

USB devices make the specified USB device appear in the instance.
For performance issues, avoid using devices that require high throughput or low latency.

For containers, only `libusb` devices (at `/dev/bus/usb`) are passed to the instance.
This method works for devices that have user-space drivers.
For devices that require dedicated kernel drivers, use a [`unix-char` device](devices-unix-char) or a [`unix-hotplug` device](devices-unix-hotplug) instead.

For virtual machines, the entire USB device is passed through, so any USB device is supported.
When a device is passed to the instance, it vanishes from the host.

## Device options

`usb` devices have the following device options:

Key         | Type      | Default           | Description
:--         | :--       | :--               | :--
`gid`       | int       | `0`               | Only for containers: GID of the device owner in the instance
`mode`      | int       | `0660`            | Only for containers: Mode of the device in the instance
`productid` | string    | -                 | The product ID of the USB device
`required`  | bool      | `false`           | Whether this device is required to start the instance (the default is `false`, and all devices can be hotplugged)
`uid`       | int       | `0`               | Only for containers: UID of the device owner in the instance
`vendorid`  | string    | -                 | The vendor ID of the USB device
