# System call interception

Incus supports intercepting some specific system calls from unprivileged
containers. If they're considered to be safe, it executes them with
elevated privileges on the host.

Doing so comes with a performance impact for the syscall in question and
will cause some work for Incus to evaluate the request and if allowed,
process it with elevated privileges.

Enabling of specific system call interception options is done on a
per-container basis through container configuration options.

## Available system calls

### `mknod` / `mknodat`

The `mknod` and `mknodat` system calls can be used to create a variety of special files.

Most commonly inside containers, they may be called to create block or character devices.
Creating such devices isn't allowed in unprivileged containers as this
is a very easy way to escalate privileges by allowing direct write
access to resources like disks or memory.

But there are files which are safe to create. For those, intercepting
this syscall may unblock some specific workloads and allow them to run
inside an unprivileged containers.

The devices which are currently allowed are:

- overlayfs whiteout (char 0:0)
- `/dev/console` (char 5:1)
- `/dev/full` (char 1:7)
- `/dev/null` (char 1:3)
- `/dev/random` (char 1:8)
- `/dev/tty` (char 5:0)
- `/dev/urandom` (char 1:9)
- `/dev/zero` (char 1:5)

All file types other than character devices are currently sent to the
kernel as usual, so enabling this feature doesn't change their behavior
at all.

This can be enabled by setting `security.syscalls.intercept.mknod` to `true`.

### `bpf`

The `bpf` system call is used to manage eBPF programs in the kernel.
Those can be attached to a variety of kernel subsystems.

In general, loading of eBPF programs that are not trusted can be problematic as it
can facilitate timing based attacks.

Incus' eBPF support is currently restricted to programs managing devices
cgroup entries. To enable it, you need to set both
`security.syscalls.intercept.bpf` and
`security.syscalls.intercept.bpf.devices` to true.

### `mount`

The `mount` system call allows for mounting both physical and virtual file systems.
By default, unprivileged containers are restricted by the kernel to just
a handful of virtual and network file systems.

To allow mounting physical file systems, system call interception can be used.
Incus offers a variety of options to handle this.

`security.syscalls.intercept.mount` is used to control the entire
feature and needs to be turned on for any of the other options to work.

`security.syscalls.intercept.mount.allowed` allows specifying a list of
file systems which can be directly mounted in the container. This is the
most dangerous option as it allows the user to feed data that is not trusted at
the kernel. This can easily be used to crash the host system or to
attack it. It should only ever be used in trusted environments.

`security.syscalls.intercept.mount.shift` can be set on top of that so
the resulting mount is shifted to the UID/GID map used by the container.
This is needed to avoid everything showing up as `nobody`/`nogroup` inside
of unprivileged containers.

The much safer alternative to those is
`security.syscalls.intercept.mount.fuse` which can be set to pairs of
file-system name and FUSE handler. When this is set, an attempt at
mounting one of the configured file systems will be transparently
redirected to instead calling the FUSE equivalent of that file system.

As this is all running as the caller, it avoids the entire issue around
the kernel attack surface and so is generally considered to be safe,
though you should keep in mind that any kind of system call interception
makes for an easy way to overload the host system.

### `sched_setscheduler`

The `sched_setscheduler` system call is used to manage process priority.

Granting this may allow a user to significantly increase the priority of
their processes, potentially taking a lot of system resources.

It also allows access to schedulers like `SCHED_FIFO` which are generally
considered to be flawed and can significantly impact overall system
stability. This is why under normal conditions, only the real root user
(or global `CAP_SYS_NICE`) would allow its use.

### `setxattr`

The `setxattr` system call is used to set extended attributes on files.

The attributes which are handled by this currently are:

- `trusted.overlay.opaque` (overlayfs directory whiteout)

Note that because the mediation must happen on a number of character
strings, there is no easy way at present to only intercept the few
attributes we care about. As we only allow the attributes above, this
may result in breakage for other attributes that would have been
previously allowed by the kernel.

This can be enabled by setting `security.syscalls.intercept.setxattr` to `true`.

### `sysinfo`

The `sysinfo` system call is used by some distributions instead of `/proc/` entries to report on resource usage.

In order to provide resource usage information specific to the container, rather than the whole system, this
syscall interception mode uses cgroup-based resource usage information to fill in the system call response.
