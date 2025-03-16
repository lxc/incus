(server-migrate-lxd)=
# Migrating from LXD

Incus includes a tool named `lxd-to-incus` which can be used to convert an existing LXD installation into an Incus one.

For this to work properly, you should make sure to {doc}`install </installing>` the latest stable release of Incus but not initialize it.
Instead, make sure that both `incus info` and `lxc info` both work properly, then run `lxd-to-incus` to migrate your data.

This process transfers the entire database and all storage from LXD to Incus, resulting in an identical setup after the migration.

```{note}
Following the migration, you will need to add any user that was in the `lxd` group into the equivalent `incus-admin` group.
As group membership only updates on login, users may need to close their session and re-open it for it to take effect.
```

```{note}
Additionally, this process doesn't migrate the command line tool configuration.
For this you may want to transfer the content of `~/.config/lxc/` or `~/snap/lxd/common/config/` over to `~/.config/incus/`.

This is mostly useful to those who interact with other remote servers or have configured custom aliases.
```

```{terminal}
:input: lxd-to-incus
:user: root
=> Looking for source server
==> Detected: snap package
=> Looking for target server
=> Connecting to source server
=> Connecting to the target server
=> Checking server versions
==> Source version: 5.19
==> Target version: 0.1
=> Validating version compatibility
=> Checking that the source server isn't empty
=> Checking that the target server is empty
=> Validating source server configuration

The migration is now ready to proceed.
At this point, the source server and all its instances will be stopped.
Instances will come back online once the migration is complete.

Proceed with the migration? [default=no]: yes
=> Stopping the source server
=> Stopping the target server
=> Wiping the target server
=> Migrating the data
=> Migrating database
=> Cleaning up target paths
=> Starting the target server
=> Checking the target server
Uninstall the LXD package? [default=no]: yes
=> Uninstalling the source server
```

```{terminal}
:input: incus list
:user: root
To start your first container, try: incus launch images:ubuntu/22.04
Or for a virtual machine: incus launch images:ubuntu/22.04 --vm

+------+---------+-----------------------+------------------------------------------------+-----------+-----------+
| NAME |  STATE  |         IPV4          |                     IPV6                       |   TYPE    | SNAPSHOTS |
+------+---------+-----------------------+------------------------------------------------+-----------+-----------+
| u1   | RUNNING | 10.204.220.101 (eth0) | fd42:1eb6:f1d8:4e2a:1266:6aff:fe65:940d (eth0) | CONTAINER | 0         |
+------+---------+-----------------------+------------------------------------------------+-----------+-----------+
```

The tool will also look for any configuration that is incompatible with Incus and fail before any data is migrated.

```{warning}
All instances will be stopped during the migration.
Once the migration process is started, it cannot easily be reversed so make sure to plan adequate downtime.
```
