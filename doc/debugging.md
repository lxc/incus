# How to debug LXD

For information on debugging instance issues, see {ref}`instances-troubleshoot`.

## Debugging `lxc` and `lxd`

Here are different ways to help troubleshooting `lxc` and `lxd` code.

### `lxc --debug`

Adding `--debug` flag to any client command will give extra information
about internals. If there is no useful info, it can be added with the
logging call:

    logger.Debugf("Hello: %s", "Debug")

### `lxc monitor`

This command will monitor messages as they appear on remote server.

## REST API through local socket

On server side the most easy way is to communicate with LXD through
local socket. This command accesses `GET /1.0` and formats JSON into
human readable form using [jq](https://stedolan.github.io/jq/tutorial/)
utility:

```bash
curl --unix-socket /var/lib/lxd/unix.socket lxd/1.0 | jq .
```

or for snap users:

```bash
curl --unix-socket /var/snap/lxd/common/lxd/unix.socket lxd/1.0 | jq .
```

See the [RESTful API](rest-api.md) for available API.

## REST API through HTTPS

{ref}`HTTPS connection to LXD <security>` requires valid
client certificate that is generated on first [`lxc remote add`](lxc_remote_add.md). This
certificate should be passed to connection tools for authentication
and encryption.

If desired, `openssl` can be used to examine the certificate (`~/.config/lxc/client.crt`
or `~/snap/lxd/common/config/client.crt` for snap users):

```bash
openssl x509 -text -noout -in client.crt
```

Among the lines you should see:

    Certificate purposes:
    SSL client : Yes

### With command line tools

```bash
wget --no-check-certificate --certificate=$HOME/.config/lxc/client.crt --private-key=$HOME/.config/lxc/client.key -qO - https://127.0.0.1:8443/1.0

# or for snap users
wget --no-check-certificate --certificate=$HOME/snap/lxd/common/config/client.crt --private-key=$HOME/snap/lxd/common/config/client.key -qO - https://127.0.0.1:8443/1.0
```

### With browser

Some browser plugins provide convenient interface to create, modify
and replay web requests. To authenticate against LXD server, convert
`lxc` client certificate into importable format and import it into
browser.

For example this produces `client.pfx` in Windows-compatible format:

```bash
openssl pkcs12 -clcerts -inkey client.key -in client.crt -export -out client.pfx
```

After that, opening [`https://127.0.0.1:8443/1.0`](https://127.0.0.1:8443/1.0) should work as expected.

## Debug the LXD database

The files of the global {ref}`database <database>` are stored under the `./database/global`
sub-directory of your LXD data directory (e.g. `/var/lib/lxd/database/global` or
`/var/snap/lxd/common/lxd/database/global` for snap users).

Since each member of the cluster also needs to keep some data which is specific
to that member, LXD also uses a plain SQLite database (the "local" database),
which you can find in `./database/local.db`.

Backups of the global database directory and of the local database file are made
before upgrades, and are tagged with the `.bak` suffix. You can use those if
you need to revert the state as it was before the upgrade.

### Dumping the database content or schema

If you want to get a SQL text dump of the content or the schema of the databases,
use the `lxd sql <local|global> [.dump|.schema]` command, which produces the
equivalent output of the `.dump` or `.schema` directives of the `sqlite3`
command line tool.

### Running custom queries from the console

If you need to perform SQL queries (e.g. `SELECT`, `INSERT`, `UPDATE`)
against the local or global database, you can use the `lxd sql` command (run
`lxd sql --help` for details).

You should only need to do that in order to recover from broken updates or bugs.
Please consult the LXD team first (creating a [GitHub
issue](https://github.com/canonical/lxd/issues/new) or
[forum](https://discourse.ubuntu.com/c/lxd/) post).

### Running custom queries at LXD daemon startup

In case the LXD daemon fails to start after an upgrade because of SQL data
migration bugs or similar problems, it's possible to recover the situation by
creating `.sql` files containing queries that repair the broken update.

To perform repairs against the local database, write a
`./database/patch.local.sql` file containing the relevant queries, and
similarly a `./database/patch.global.sql` for global database repairs.

Those files will be loaded very early in the daemon startup sequence and deleted
if the queries were successful (if they fail, no state will change as they are
run in a SQL transaction).

As above, please consult the LXD team first.

### Syncing the cluster database to disk

If you want to flush the content of the cluster database to disk, use the `lxd
sql global .sync` command, that will write a plain SQLite database file into
`./database/global/db.bin`, which you can then inspect with the `sqlite3`
command line tool.
