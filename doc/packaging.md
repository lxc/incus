# Packaging recommendations
Below are a few recommendations for packagers of Incus.

Following those recommendations should provide a more predictable experience across Linux distributions.

## Packages

It's usually a good idea to at least split things into an `incus` and `incus-client` package.
The latter allows for installing just the `incus` command line tool without bringing the daemon and its dependencies.

Additionally, it may be useful to have an `incus-tools` package with some of the less commonly used tools like `fuidshift`, `lxc-to-incus`, `incus-benchmark` and `incus-migrate`.

## Groups

Two groups should be provided:

- `incus-admin` which grants access to the `unix.socket` socket and effectively grants full control over Incus.
- `incus` which grants access to the `user.socket` socket which provides users with a restricted Incus project.

## Init scripts

The following assumes the use of `systemd`. Distributions not using
`systemd` should try to stick to a similar naming scheme but will likely
see some differences on things like socket activation.

- `incus.service` is the main unit that starts and stops the `incusd` daemon.
- `incus.socket` is the socket-activation unit for the `incus.service` unit. If present, `incus.service` should not be made to start on its own.
- `incus-user.service` is the unit responsible for starting and stopping the `incus-user` daemon.
- `incus-user.socket` is the socket-activation unit for the `incus-user.service` unit. If present, `incus-user.service` should not be made to start on its own.
- `incus-startup.service` uses the `incusd activateifneeded` command to trigger daemon startup if it is required. It also calls `incusd shutdown` to handle orderly shutdown of instances on host shutdown.

## Binaries

The `incusd` and `incus-user` daemons should be kept outside of the user's `PATH`.
The same is true of `incus-agent` which needs to be available in the daemon's `PATH` but not be visible to users.

The main binary that should be made visible to users is `incus`.

On top of those, the following optional binaries may also be made available:

- `fuidshift` (should be kept to root only)
- `incus-benchmark`
- `incus-migrate`
- `lxc-to-incus`
- `lxd-to-incus` (should be kept to root only)

## Incus agent binaries

There are two ways to provide the `incus-agent` binary.

### Single agent setup

The simplest way is to have `incus-agent` be available in the `PATH` of `incusd`.

In this scenario the agent should be a static build of `incus-agent` for the primary architecture of the system.

### Multiple agent setup

Alternatively, it's possible to provide multiple builds of the `incus-agent` binary, offering support for multiple architectures or operating systems.

To do that, the `INCUS_AGENT_PATH` environment variable should be set for the `incusd` process and point to a path that includes the `incus-agent` builds.

Those builds should be named after the operating system name and architecture.
For example `incus-agent.linux.x86_64`, `incus-agent.linux.i686` or `incus-agent.linux.aarch64`.

## Documentation
### Web documentation
Incus can serve its own documentation when the network listener is enabled (`core.https_address`).

For that to work, the documentation provided in the release tarball
should be shipped as part of the package and its path be passed to Incus
through the `INCUS_DOCUMENTATION` environment variable.

### Manual pages
While we don't specifically write full `manpage` entries for Incus, it is possible to generate those from the CLI.

Running `incus manpage --all --format=man /target/path` will generate a separate page for each command/sub-command.

This is effectively the same as what's otherwise made available through `--help`,
so unless a distribution packaging policy requires all binaries have `manpages`,
it's usually best to rely on `--help` and `help` sub-commands.
