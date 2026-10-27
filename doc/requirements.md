# Requirements

(requirements-go)=
## Go

Incus requires Go 1.26 or higher and is only tested with the Golang compiler.

We recommend having at least 2GiB of RAM to allow the build to complete.

## Kernel requirements

The minimum supported kernel version is 6.12.

Incus requires a kernel with support for:

* Control Groups (`blkio`, `cpuset`, `devices`, `freezer`, `memory` and `pids`)
* Namespaces (`cgroup`, `ipc`, `pid`, `mount`, `net`, `user` and `uts`)
* Seccomp
* Native Linux AIO
  ([`io_setup(2)`](https://man7.org/linux/man-pages/man2/io_setup.2.html), etc.)

The following optional features also require extra kernel options:

* AppArmor
* CRIU (exact details to be found with CRIU upstream)
* SELinux

As well as any other kernel feature required by the LXC version in use.

## LXC

Incus requires LXC 6.0.0 or higher with the following build options:

* `apparmor` (if using Incus' AppArmor support) (minimum version: 3.0.0)
* `seccomp`
* `selinux` (if using Incus' SELinux support)

LXCFS is strongly recommended to properly report resource consumption inside the container.

## OCI

To run OCI containers, Incus currently relies on `skopeo` for registry interactions.

## QEMU

For virtual machines, QEMU 8.2 or higher is required.

When using `virtiofsd`, only the [Rust rewrite](https://gitlab.com/virtio-fs/virtiofsd) of `virtiofsd` is supported.

## Network

Minimum versions for network related tooling:

* `nftables`: 1.0.0
* `dnsmasq`: 2.90

When using Incus with OVN networks, the minimum versions of OVS and OVN are:

* `openvswitch`: 3.3.0
* `ovn`: 24.03.0

## Storage

Minimum versions for storage drivers:

* `zfs`: 2.1.0
* `lvm`: 2.03.11
* `linstor-drbd`: 9.0
* `truenas-incus-ctl`: 0.7.7

## Additional libraries (and development headers)

Incus uses `cowsql` for its database, to build and set it up, you can
run `make deps`.

Incus itself also uses a number of (usually packaged) C libraries:

* `libacl1`
* `libcap2`
* `libuv1` (for `cowsql`)
* `libsqlite3` >= 3.25.0 (for `cowsql`)

Make sure you have all these libraries themselves and their development
headers (`-dev` packages) installed.
