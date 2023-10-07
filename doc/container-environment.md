(container-runtime-environment)=
# Container runtime environment

Incus attempts to present a consistent environment to all containers it runs.

The exact environment will differ slightly based on kernel features and user configuration, but otherwise, it is identical for all containers.

## File system

Incus assumes that any image it uses to create a new container comes with at least the following root-level directories:

- `/dev` (empty)
- `/proc` (empty)
- `/sbin/init` (executable)
- `/sys` (empty)

## Devices

Incus containers have a minimal and ephemeral `/dev` based on a `tmpfs` file system.
Since this is a `tmpfs` and not a `devtmpfs` file system, device nodes appear only if manually created.

The following standard set of device nodes is set up automatically:

- `/dev/console`
- `/dev/fd`
- `/dev/full`
- `/dev/log`
- `/dev/null`
- `/dev/ptmx`
- `/dev/random`
- `/dev/stdin`
- `/dev/stderr`
- `/dev/stdout`
- `/dev/tty`
- `/dev/urandom`
- `/dev/zero`

In addition to the standard set of devices, the following devices are also set up for convenience:

- `/dev/fuse`
- `/dev/net/tun`
- `/dev/mqueue`

### Network

Incus containers may have any number of network devices attached to them.
The naming for those (unless overridden by the user) is `ethX`, where `X` is an incrementing number.

### Container-to-host communication

Incus sets up a socket at `/dev/incus/sock` that the root user in the container can use to communicate with Incus on the host.

See {doc}`dev-incus` for the API documentation.

## Mounts

The following mounts are set up by default:

- `/proc` ({spellexception}`proc`)
- `/sys` (`sysfs`)
- `/sys/fs/cgroup/*` (`cgroupfs`) (only on kernels that lack cgroup namespace support)

If they are present on the host, the following paths will also automatically be mounted:

- `/proc/sys/fs/binfmt_misc`
- `/sys/firmware/efi/efivars`
- `/sys/fs/fuse/connections`
- `/sys/fs/pstore`
- `/sys/kernel/debug`
- `/sys/kernel/security`

The reason for passing all of those paths is that legacy init systems require them to be mounted, or be mountable, inside the container.

The majority of those paths will not be writable (or even readable) from inside an unprivileged container.
In privileged containers, they will be blocked by the AppArmor policy.

### LXCFS

If LXCFS is present on the host, it is automatically set up for the container.

This normally results in a number of `/proc` files being overridden through bind-mounts.
On older kernels, a virtual version of `/sys/fs/cgroup` might also be set up by LXCFS.

## PID1

Incus spawns whatever is located at `/sbin/init` as the initial process of the container (PID 1).
This binary should act as a proper init system, including handling re-parented processes.

Incus' communication with PID1 in the container is limited to two signals:

- `SIGINT` to trigger a reboot of the container
- `SIGPWR` (or alternatively `SIGRTMIN`+3) to trigger a clean shutdown of the container

The initial environment of PID1 is blank except for `container=lxc`, which can be used by the init system to detect the runtime.

All file descriptors above the default three are closed prior to PID1 being spawned.
