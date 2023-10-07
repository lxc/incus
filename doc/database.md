(database)=
# About the Incus database

Incus uses a distributed database to store the server configuration and state, which allows for quicker queries than if the configuration was stored inside each instance's directory (as it is done by LXC, for example).

To understand the advantages, consider a query against the configuration of all instances, like "what instances are using `br0`?".
To answer that question without a database, you would have to iterate through every single instance, load and parse its configuration, and then check which network devices are defined in there.
With a database, you can run a simple query on the database to retrieve this information.

## Cowsql

In a Incus cluster, all members of the cluster must share the same database state.
Therefore, Incus uses [Cowsql](https://github.com/cowsql/cowsql), a distributed version of SQLite.
Cowsql provides replication, fault-tolerance, and automatic failover without the need of external database processes.

When using Incus as a single machine and not as a cluster, the Cowsql database effectively behaves like a regular SQLite database.

## File location

The database files are stored in the `database` sub-directory of your Incus data directory (`/var/lib/incus/database/`).

Upgrading Incus to a newer version might require updating the database schema.
In this case, Incus automatically stores a backup of the database and then runs the update.
See {ref}`installing-upgrade` for more information.

## Backup

See {ref}`backup-database` for instructions on how to back up the contents of the Incus database.
