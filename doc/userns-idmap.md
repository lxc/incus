# Idmaps for user namespace

Incus runs safe containers. This is achieved mostly through the use of
user namespaces which make it possible to run containers unprivileged,
greatly limiting the attack surface.

User namespaces work by mapping a set of UIDs and GIDs on the host to a
set of UIDs and GIDs in the container.

For example, we can define that the host UIDs and GIDs from 100000 to
165535 may be used by Incus and should be mapped to UID/GID 0 through
65535 in the container.

As a result a process running as UID 0 in the container will actually be
running as UID 100000.

Allocations should always be of at least 65536 UIDs and GIDs to cover
the POSIX range including root (0) and nobody (65534).

## Kernel support

User namespaces require a kernel >= 3.12, Incus will start even on older
kernels but will refuse to start containers.

## Allowed ranges

On most hosts, Incus will check `/etc/subuid` and `/etc/subgid` for
allocations for the `incus` user and on first start, set the default
profile to use the first 65536 UIDs and GIDs from that range.

If the range is shorter than 65536 (which includes no range at all),
then Incus will fail to create or start any container until this is corrected.

If some but not all of `/etc/subuid`, `/etc/subgid`, `newuidmap` (path lookup)
and `newgidmap` (path lookup) can be found on the system, Incus will fail
the startup of any container until this is corrected as this shows a
broken shadow setup.

If none of those files can be found, then Incus will assume a 1000000000
UID/GID range starting at a base UID/GID of 1000000.

This is the most common case and is usually the recommended setup when
not running on a system which also hosts fully unprivileged containers
(where the container runtime itself runs as a user).

## Varying ranges between hosts

The source map is sent when moving containers between hosts so that they
can be remapped on the receiving host.

## Different idmaps per container

Incus supports using different idmaps per container, to further isolate
containers from each other. This is controlled with two per-container
configuration keys, `security.idmap.isolated` and `security.idmap.size`.

Containers with `security.idmap.isolated` will have a unique ID range computed
for them among the other containers with `security.idmap.isolated` set (if none
is available, setting this key will simply fail).

Containers with `security.idmap.size` set will have their ID range set to this
size. Isolated containers without this property set default to a ID range of
size 65536; this allows for POSIX compliance and a `nobody` user inside the
container.

To select a specific map, the `security.idmap.base` key will let you
override the auto-detection mechanism and tell Incus what host UID/GID you
want to use as the base for the container.

These properties require a container reboot to take effect.

## Custom idmaps

Incus also supports customizing bits of the idmap, e.g. to allow users to bind
mount parts of the host's file system into a container without the need for any
UID-shifting file system. The per-container configuration key for this is
`raw.idmap`, and looks like:

    both 1000 1000
    uid 50-60 500-510
    gid 100000-110000 10000-20000

The first line configures both the UID and GID 1000 on the host to map to UID
1000 inside the container (this can be used for example to bind mount a user's
home directory into a container).

The second and third lines map only the UID or GID ranges into the container,
respectively. The second entry per line is the source ID, i.e. the ID on the
host, and the third entry is the range inside the container. These ranges must
be the same size.

This property requires a container reboot to take effect.
