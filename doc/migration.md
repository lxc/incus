(migration)=
# Migration

Incus provides tools and functionality to migrate instances in different contexts.

Migrate existing Incus instances between servers
: The most basic kind of migration is if you have a Incus instance on one server and want to move it to a different Incus server.
  For virtual machines, you can do that as a live migration, which means that you can migrate your VM while it is running and there will be no downtime.

  See {ref}`move-instances` for more information.

Migrate physical or virtual machines to Incus instances
: If you have an existing machine, either physical or virtual (VM or container), you can use the `incus-migrate` tool to create a Incus instance based on your existing machine.
  The tool copies the provided partition, disk or image to the Incus storage pool of the provided Incus server, sets up an instance using that storage and allows you to configure additional settings for the new instance.

  See {ref}`import-machines-to-instances` for more information.

Migrate instances from LXC to Incus
: If you are using LXC and want to migrate all or some of your LXC containers to a Incus installation on the same machine, you can use the `lxc-to-incus` tool.
  The tool analyzes the LXC configuration and copies the data and configuration of your existing LXC containers into new Incus containers.

  See {ref}`migrate-from-lxc` for more information.

```{toctree}
:maxdepth: 1
:hidden:

Move instances <howto/move_instances>
Import existing machines <howto/import_machines_to_instances>
Migrate from LXC <howto/migrate_from_lxc>
```
