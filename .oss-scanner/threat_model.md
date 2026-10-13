# Incus threat model

Anthropic's OSS Scanner reads this file before it scans Incus. The project's own
security documentation lives in `doc/explanation/security.md` and `SECURITY.md`.

## What Incus does and where untrusted input enters

Incus is a system container and virtual machine manager. The `incusd` daemon runs as
root on the host, exposes a REST API over a local Unix socket and optionally over
HTTPS, and drives LXC containers and QEMU virtual machines. Clusters of hosts share
a distributed database (cowsql). The `incus` command line tool is a client of that
API, and `incus-agent` runs inside virtual machines to serve exec, file transfer and
metrics requests from the host.

Attacker-controlled:

- **Everything inside an unprivileged container or a virtual machine, including root.**
  Guest data reaches the host through the `/dev/incus` guest API
  (`cmd/incusd/dev_incus.go`, served over a Unix socket in containers and vsock in VMs),
  the agent protocol between `incusd` and `incus-agent` (`cmd/incus-agent`, the QEMU
  driver in `internal/server/instance/drivers`), exec and console sessions, file push
  and pull (SFTP), DHCP and DNS traffic to `dnsmasq` on managed bridges, and the
  network traffic that Incus filters with `nftables`, OVS or OVN.
- **Clients holding a restricted TLS certificate or an OIDC identity.** The API lets
  such clients manage instances inside the projects they are confined to. Project
  restrictions (`restricted.*` keys in `internal/server/project`) are the boundary that
  keeps them from touching the host or other projects: device types and sources they
  may attach, `security.*` instance keys they may change, networks they may use, and
  so on. Authorization is in `internal/server/auth` (TLS, OpenFGA and scriptlets).
- **Images, backups and migration streams.** Image servers (`images:`, OCI registries),
  backup tarballs imported through the API and migration data from another Incus server
  are untrusted archives. Unpacking happens in `shared/archive` and
  `internal/server/instance`, `internal/server/storage` and `cmd/incusd/backup*.go`.
- **Peers on the network.** Routed and bridged NICs, `dnsmasq`, the built-in DNS
  server, BGP sessions, OVN and the HTTPS API itself all face the network.

Trusted: the host root user and members of the `incus-admin` group (anyone who can
reach the local Unix socket has full control, by design), the other members of a
cluster, the storage backends (ZFS, Btrfs, LVM, Ceph, LINSTOR), the kernel, QEMU and
liblxc, and server configuration set by an administrator.

## Components that matter most / least

Most:

- The API server in `cmd/incusd` and the authorization and project confinement code it
  calls. A restricted client must not escape its project, read another project's data,
  attach a host path or device that `restricted.*` forbids, or reach host root.
- Code that consumes guest input: `cmd/incusd/dev_incus.go`, the agent client in the QEMU
  driver, the SFTP and file transfer paths, `internal/server/device` (device hotplug and
  guest-facing proxy devices) and the instance drivers.
- Archive and image handling: `shared/archive`, image import and unpacking, backup import
  and the migration receivers. Path traversal, symlink following and writes outside the
  target are the issues to look for.
- Network security features: MAC and IP filtering, ACLs for bridged and OVN networks
  (`internal/server/network`, `internal/server/firewall`). A filtered instance must not
  spoof or bypass a configured ACL.
- UID/GID shifting and idmap handling (`shared/idmap`), which keep unprivileged
  containers unprivileged.
- `cmd/incus-user`, a daemon that gives members of the `incus` group their own restricted
  project. It must not let such users reach anything beyond that project.

Least: `doc/`, `test/`, `cmd/generate-*`, `cmd/lxc-to-incus`, `grafana/`, the
documentation tooling and the `incus` CLI itself, which runs with the privileges of the
user invoking it.

## How to exercise it

- `/src` is the checkout, built with `make`. The binaries (`incusd`, `incus`,
  `incus-agent`, `incus-migrate`, ...) are installed in `/go/bin`, which is on `PATH`.
  Go's module and build caches are warm, so `go build ./...` and `go test ./...` work
  offline.
- `go test ./...` is the baseline. The image build runs it and logs failures without
  aborting, so check that output rather than assuming the suite passed. The
  `cmd/lxc-to-incus` tests create a real LXC container and fail where that is not
  possible; that is an environment limitation, not a finding.
- `incusd` can be started for API-level reproducers with
  `INCUS_DIR=/tmp/incus incusd --group root` and queried with `incus` (the `local:`
  remote talks to `$INCUS_DIR/unix.socket`). Use the `dir` storage driver. The container
  has no KVM and no permission to create user namespaces or cgroups, so do not expect
  instances to start; reproducers that need a running instance should say so.
- The shell test suite in `test/` needs a full host with root, kernel features and
  storage tooling and is not provisioned in this image.

## How you rate severity

Severity follows the attacker's starting position:

- **Critical**: escape from an unprivileged container or a virtual machine to the host;
  unauthenticated access to the remote API; a restricted client gaining host root or
  full control of other projects.
- **High**: a restricted client bypassing a `restricted.*` project limit or an
  authorization check; guest-triggered code execution in `incusd` or `incus-agent`
  running as root; path traversal or symlink attacks through file transfer, image, backup
  or migration handling that write or read outside the intended location; TLS or OIDC
  authentication bypass.
- **Medium**: a guest or a restricted client crashing or hanging `incusd`, exhausting
  host resources beyond the instance's limits, or reading metadata that belongs to
  another project; network filtering or ACL bypass that only affects the instance's own
  segment.
- **Low**: issues requiring an administrator's cooperation, non-default configuration
  that the documentation already calls unsafe, or a purely theoretical path.

Not vulnerabilities:

- Anything available to host root, `incus-admin` members or anyone else with access to
  the local Unix socket. They already have full control.
- Escapes from privileged containers (`security.privileged=true`). Incus does not
  consider privileged containers root safe; see `SECURITY.md`.
- Consequences of an administrator disabling a security feature, such as
  `security.nesting`, `security.syscalls.intercept.*`, `raw.lxc`, `raw.qemu` or
  disk devices with `shift=true` or host paths, unless a restricted project is supposed
  to prevent that setting and does not.
- Bugs in the kernel, QEMU, liblxc, OVN or other dependencies without an Incus-specific
  path. Report dependency-caused findings separately and explain how Incus reaches them.

## Reports and patches

Name the scanned commit, the attacker's position, the configuration needed, the entry
point and the full path to the impact. Include a deterministic reproducer, expected
versus actual behavior, and a focused patch with a regression test where the Go unit
tests can hold one. Follow `AGENTS.md` for code style: short one-line comments, no
single-line define-and-test `if`, `gofumpt` formatting. Deduplicate reports that share
a root cause.
