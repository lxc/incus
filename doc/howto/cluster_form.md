(cluster-form)=
# How to form a cluster

When forming a Incus cluster, you start with a bootstrap server.
This bootstrap server can be an existing Incus server or a newly installed one.

After initializing the bootstrap server, you can join additional servers to the cluster.
See {ref}`clustering-members` for more information.

You can form the Incus cluster interactively by providing configuration information during the initialization process or by using preseed files that contain the full configuration.

To quickly and automatically set up a basic Incus cluster, you can use MicroCloud.
Note, however, that this project is still in an early phase.

## Configure the cluster interactively

To form your cluster, you must first run `incus admin init` on the bootstrap server. After that, run it on the other servers that you want to join to the cluster.

When forming a cluster interactively, you answer the questions that `incus admin init` prompts you with to configure the cluster.

### Initialize the bootstrap server

To initialize the bootstrap server, run `incus admin init` and answer the questions according to your desired configuration.

You can accept the default values for most questions, but make sure to answer the following questions accordingly:

- `Would you like to use Incus clustering?`

  Select **yes**.
- `What IP address or DNS name should be used to reach this server?`

  Make sure to use an IP or DNS address that other servers can reach.
- `Are you joining an existing cluster?`

  Select **no**.

<details>
<summary>Expand to see a full example for <code>incus admin init</code> on the bootstrap server</summary>

```{terminal}
:input: incus admin init

Would you like to use Incus clustering? (yes/no) [default=no]: yes
What IP address or DNS name should be used to reach this server? [default=192.0.2.101]:
Are you joining an existing cluster? (yes/no) [default=no]: no
What member name should be used to identify this server in the cluster? [default=server1]:
Setup password authentication on the cluster? (yes/no) [default=no]: no
Do you want to configure a new local storage pool? (yes/no) [default=yes]:
Name of the storage backend to use (btrfs, dir, lvm, zfs) [default=zfs]:
Create a new ZFS pool? (yes/no) [default=yes]:
Would you like to use an existing empty block device (e.g. a disk or partition)? (yes/no) [default=no]:
Size in GiB of the new loop device (1GiB minimum) [default=9GiB]:
Do you want to configure a new remote storage pool? (yes/no) [default=no]:
Would you like to configure Incus to use an existing bridge or host interface? (yes/no) [default=no]:
Would you like stale cached images to be updated automatically? (yes/no) [default=yes]:
Would you like a YAML "incus admin init" preseed to be printed? (yes/no) [default=no]:
```

</details>

After the initialization process finishes, your first cluster member should be up and available on your network.
You can check this with [`incus cluster list`](incus_cluster_list.md).

### Join additional servers

You can now join further servers to the cluster.

```{note}
The servers that you add should be newly installed Incus servers.
If you are using existing servers, make sure to clear their contents before joining them, because any existing data on them will be lost.
```

To join a server to the cluster, run `incus admin init` on the cluster.
Joining an existing cluster requires root privileges, so make sure to run the command as root or with `sudo`.

Basically, the initialization process consists of the following steps:

1. Request to join an existing cluster.

   Answer the first questions that `incus admin init` asks accordingly:

   - `Would you like to use Incus clustering?`

     Select **yes**.
   - `What IP address or DNS name should be used to reach this server?`

     Make sure to use an IP or DNS address that other servers can reach.
   - `Are you joining an existing cluster?`

     Select **yes**.

1. Authenticate with the cluster.

   There are two alternative methods, depending on which authentication method you choose when configuring the bootstrap server.

   `````{tabs}

   ````{group-tab} Authentication tokens (recommended)
   If you configured your cluster to use {ref}`authentication tokens <authentication-token>`, you must generate a join token for each new member.
   To do so, run the following command on an existing cluster member (for example, the bootstrap server):

       incus cluster add <new_member_name>

   This command returns a single-use join token that is valid for a configurable time (see {config:option}`server-cluster:cluster.join_token_expiry`).
   Enter this token when `incus admin init` prompts you for the join token.

   The join token contains the addresses of the existing online members, as well as a single-use secret and the fingerprint of the cluster certificate.
   This reduces the amount of questions that you must answer during `incus admin init`, because the join token can be used to answer these questions automatically.
   ````

   `````

1. Confirm that all local data for the server is lost when joining a cluster.
1. Configure server-specific settings (see {ref}`clustering-member-config` for more information).

   You can accept the default values or specify custom values for each server.

<details>
<summary>Expand to see full examples for <code>incus admin init</code> on additional servers</summary>

`````{tabs}

````{group-tab} Authentication tokens (recommended)

```{terminal}
:input: sudo incus admin init

Would you like to use Incus clustering? (yes/no) [default=no]: yes
What IP address or DNS name should be used to reach this server? [default=192.0.2.102]:
Are you joining an existing cluster? (yes/no) [default=no]: yes
Do you have a join token? (yes/no/[token]) [default=no]: yes
Please provide join token: eyJzZXJ2ZXJfbmFtZSI6InJwaTAxIiwiZmluZ2VycHJpbnQiOiIyNjZjZmExZDk0ZDZiMjk2Nzk0YjU0YzJlYzdjOTMwNDA5ZjIzNjdmNmM1YjRhZWVjOGM0YjAxYTc2NjU0MjgxIiwiYWRkcmVzc2VzIjpbIjE3Mi4xNy4zMC4xODM6ODQ0MyJdLCJzZWNyZXQiOiJmZGI1OTgyNjgxNTQ2ZGQyNGE2ZGE0Mzg5MTUyOGM1ZGUxNWNmYmQ5M2M3OTU3ODNkNGI5OGU4MTQ4MWMzNmUwIn0=
All existing data is lost when joining a cluster, continue? (yes/no) [default=no] yes
Choose "size" property for storage pool "local":
Choose "source" property for storage pool "local":
Choose "zfs.pool_name" property for storage pool "local":
Would you like a YAML "incus admin init" preseed to be printed? (yes/no) [default=no]:
```

````
````{group-tab} Trust password

```{terminal}
:input: sudo incus admin init

Would you like to use Incus clustering? (yes/no) [default=no]: yes
What IP address or DNS name should be used to reach this server? [default=192.0.2.102]:
Are you joining an existing cluster? (yes/no) [default=no]: yes
Do you have a join token? (yes/no/[token]) [default=no]: no
What member name should be used to identify this server in the cluster? [default=server2]:
IP address or FQDN of an existing cluster member (may include port): 192.0.2.101:8443
Cluster fingerprint: 2915dafdf5c159681a9086f732644fb70680533b0fb9005b8c6e9bca51533113
You can validate this fingerprint by running "incus info" locally on an existing cluster member.
Is this the correct fingerprint? (yes/no/[fingerprint]) [default=no]: yes
Cluster trust password:
All existing data is lost when joining a cluster, continue? (yes/no) [default=no] yes
Choose "size" property for storage pool "local":
Choose "source" property for storage pool "local":
Choose "zfs.pool_name" property for storage pool "local":
Would you like a YAML "incus admin init" preseed to be printed? (yes/no) [default=no]:
```

````
`````

</details>

After the initialization process finishes, your server is added as a new cluster member.
You can check this with [`incus cluster list`](incus_cluster_list.md).

## Configure the cluster through preseed files

To form your cluster, you must first run `incus admin init` on the bootstrap server.
After that, run it on the other servers that you want to join to the cluster.

Instead of answering the `incus admin init` questions interactively, you can provide the required information through preseed files.
You can feed a file to `incus admin init` with the following command:

    cat <preseed-file> | incus admin init --preseed

You need a different preseed file for every server.

### Initialize the bootstrap server

`````{tabs}

````{group-tab} Authentication tokens (recommended)
To enable clustering, the preseed file for the bootstrap server must contain the following fields:

```yaml
config:
  core.https_address: <IP_address_and_port>
cluster:
  server_name: <server_name>
  enabled: true
```

Here is an example preseed file for the bootstrap server:

```yaml
config:
  core.https_address: 192.0.2.101:8443
  images.auto_update_interval: 15
storage_pools:
- name: default
  driver: dir
- name: my-pool
  driver: zfs
networks:
- name: incusbr0
  type: bridge
profiles:
- name: default
  devices:
    root:
      path: /
      pool: my-pool
      type: disk
    eth0:
      name: eth0
      nictype: bridged
      parent: incusbr0
      type: nic
cluster:
  server_name: server1
  enabled: true
```

````

`````

### Join additional servers

The preseed files for new cluster members require only a `cluster` section with data and configuration values that are specific to the joining server.

`````{tabs}

````{group-tab} Authentication tokens (recommended)
The preseed file for additional servers must include the following fields:

```yaml
cluster:
  enabled: true
  server_address: <IP_address_of_server>
  cluster_token: <join_token>
```

Here is an example preseed file for a new cluster member:

```yaml
cluster:
  enabled: true
  server_address: 192.0.2.102:8443
  cluster_token: eyJzZXJ2ZXJfbmFtZSI6Im5vZGUyIiwiZmluZ2VycHJpbnQiOiJjZjlmNmVhMWIzYjhiNjgxNzQ1YTY1NTY2YjM3ZGUwOTUzNjRmM2MxMDAwMGNjZWQyOTk5NDU5YzY2MGIxNWQ4IiwiYWRkcmVzc2VzIjpbIjE3Mi4xNy4zMC4xODM6ODQ0MyJdLCJzZWNyZXQiOiIxNGJmY2EzMDhkOTNhY2E3MGJmYThkMzE0NWM4NWY3YmE0ZmU1YmYyNmJiNDhmMmUwNzhhOGZhMDczZDc0YTFiIn0=
  member_config:
  - entity: storage-pool
    name: default
    key: source
    value: ""
  - entity: storage-pool
    name: my-pool
    key: source
    value: ""
  - entity: storage-pool
    name: my-pool
    key: driver
    value: "zfs"

```

````

`````
