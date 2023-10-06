(standard-devices)=
# Standard devices

Incus provides each instance with the basic devices that are required for a standard POSIX system to work.
These devices aren't visible in the instance or profile configuration, and they may not be overridden.

The standard devices are:

| Device         | Type of device    |
|:---------------|:------------------|
| `/dev/null`    | Character device  |
| `/dev/zero`    | Character device  |
| `/dev/full`    | Character device  |
| `/dev/console` | Character device  |
| `/dev/tty`     | Character device  |
| `/dev/random`  | Character device  |
| `/dev/urandom` | Character device  |
| `/dev/net/tun` | Character device  |
| `/dev/fuse`    | Character device  |
| `lo`           | Network interface |

Any other devices must be defined in the instance configuration or in one of the profiles used by the instance.
The default profile typically contains a network interface that becomes `eth0` in the instance.
