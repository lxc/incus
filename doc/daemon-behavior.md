(daemon-behavior)=
# Daemon behavior

This specification covers some of the Incus daemon's behavior.

## Startup

On every start, LXD checks that its directory structure exists. If it
doesn't, it creates the required directories, generates a key pair and
initializes the database.

Once the daemon is ready for work, LXD scans the instances table
for any instance for which the stored power state differs from the
current one. If an instance's power state was recorded as running and the
instance isn't running, LXD starts it.

## Signal handling

### `SIGINT`, `SIGQUIT`, `SIGTERM`

For those signals, LXD assumes that it's being temporarily stopped and
will be restarted at a later time to continue handling the instances.

The instances will keep running and LXD will close all connections and
exit cleanly.

### `SIGPWR`

Indicates to LXD that the host is going down.

LXD will attempt a clean shutdown of all the instances. After 30 seconds, it
kills any remaining instance.

The instance `power_state` in the instances table is kept as it was so
that LXD can restore the instances as they were after the host is done rebooting.

### `SIGUSR1`

Write a memory profile dump to the file specified with `--memprofile`.
