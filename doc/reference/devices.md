(devices)=
# Devices

Devices are attached to an instance (see {ref}`instances-configure-devices`) or to a profile (see {ref}`profiles-edit`).

They include, for example, network interfaces, mount points, USB and GPU devices.
These devices can have instance device options, depending on the type of the instance device.

Incus supports the following device types:

| ID (database) | Name                                   | Condition | Description                     |
|:--------------|:---------------------------------------|:----------|:--------------------------------|
| 0             | [`none`](devices-none)                 | -         | Inheritance blocker             |
| 1             | [`nic`](devices-nic)                   | -         | Network interface               |
| 2             | [`disk`](devices-disk)                 | -         | Mount point inside the instance |
| 3             | [`unix-char`](devices-unix-char)       | container | Unix character device           |
| 4             | [`unix-block`](devices-unix-block)     | container | Unix block device               |
| 5             | [`usb`](devices-usb)                   | -         | USB device                      |
| 6             | [`gpu`](devices-gpu)                   | -         | GPU device                      |
| 7             | [`infiniband`](devices-infiniband)     | container | InfiniBand device               |
| 8             | [`proxy`](devices-proxy)               | container | Proxy device                    |
| 9             | [`unix-hotplug`](devices-unix-hotplug) | container | Unix hotplug device             |
| 10            | [`tpm`](devices-tpm)                   | -         | TPM device                      |
| 11            | [`pci`](devices-pci)                   | VM        | PCI device                      |

Each instance comes with a set of {ref}`standard-devices`.

```{toctree}
:maxdepth: 1
:hidden:

../reference/standard_devices.md
../reference/devices_none.md
../reference/devices_nic.md
../reference/devices_disk.md
../reference/devices_unix_char.md
../reference/devices_unix_block.md
../reference/devices_usb.md
../reference/devices_gpu.md
../reference/devices_infiniband.md
../reference/devices_proxy.md
../reference/devices_unix_hotplug.md
../reference/devices_tpm.md
../reference/devices_pci.md
```
