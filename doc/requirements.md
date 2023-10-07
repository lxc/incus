# Requirements

## Go

Incus requires Go 1.18 or higher and is only tested with the Golang compiler.

We recommend having at least 2GiB of RAM to allow the build to complete.

## Kernel requirements

The minimum supported kernel version is 5.4.

Incus requires a kernel with support for:

* Namespaces (`pid`, `net`, `uts`, `ipc` and `mount`)
* Seccomp
* Native Linux AIO
  ([`io_setup(2)`](https://man7.org/linux/man-pages/man2/io_setup.2.html), etc.)

The following optional features also require extra kernel options:

* Namespaces (`user` and `cgroup`)
* AppArmor (including Ubuntu patch for mount mediation)
* Control Groups (`blkio`, `cpuset`, `devices`, `memory`, `pids` and `net_prio`)
* CRIU (exact details to be found with CRIU upstream)

As well as any other kernel feature required by the LXC version in use.

## LXC

Incus requires LXC 4.0.0 or higher with the following build options:

* `apparmor` (if using Incus' AppArmor support)
* `seccomp`

To run recent version of various distributions, including Ubuntu, LXCFS
should also be installed.

## QEMU

For virtual machines, QEMU 6.0 or higher is required.

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
