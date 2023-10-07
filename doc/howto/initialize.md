(initialize)=
# How to initialize Incus

Before you can create a Incus instance, you must configure and initialize Incus.

## Interactive configuration

Run the following command to start the interactive configuration process:

    incus admin init

```{note}
For simple configurations, you can run this command as a normal user.
However, some more advanced operations during the initialization process (for example, joining an existing cluster) require root privileges.
In this case, run the command with `sudo` or as root.
```

The tool asks a series of questions to determine the required configuration.
The questions are dynamically adapted to the answers that you give.
They cover the following areas:

Clustering (see {ref}`exp-clustering` and {ref}`cluster-form`)
: A cluster combines several Incus servers.
  The cluster members share the same distributed database and can be managed uniformly using the Incus client ([`incus`](incus.md)) or the REST API.

  The default answer is `no`, which means clustering is not enabled.
  If you answer `yes`, you can either connect to an existing cluster or create one.

Networking (see {ref}`networks` and {ref}`Network devices <devices-nic>`)
: Provides network access for the instances.

  You can let Incus create a new bridge (recommended) or use an existing network bridge or interface.

  You can create additional bridges and assign them to instances later.

Storage pools (see {ref}`exp-storage` and  {ref}`storage-drivers`)
: Instances (and other data) are stored in storage pools.

  For testing purposes, you can create a loop-backed storage pool.
  For production use, however, you should use an empty partition (or full disk) instead of loop-backed storage (because loop-backed pools are slower and their size can't be reduced).

  The recommended backends are `zfs` and `btrfs`.

  You can create additional storage pools later.

Remote access (see {ref}`security_remote_access` and {ref}`authentication`)
: Allows remote access to the server over the network.

  The default answer is `no`, which means remote access is not allowed.
  If you answer `yes`, you can connect to the server over the network.

  You can choose to add client certificates to the server (manually or through tokens, the recommended way) or set a trust password.

Automatic image update (see {ref}`about-images`)
: You can download images from image servers.
  In this case, images can be updated automatically.

  The default answer is `yes`, which means that Incus will update the downloaded images regularly.

YAML `incus admin init` preseed (see {ref}`initialize-preseed`)
: If you answer `yes`, the command displays a summary of your chosen configuration options in the terminal.

### Minimal setup

To create a minimal setup with default options, you can skip the configuration steps by adding the `--minimal` flag to the `incus admin init` command:

    incus admin init --minimal

```{note}
The minimal setup provides a basic configuration, but the configuration is not optimized for speed or functionality.
Especially the [`dir` storage driver](storage-dir), which is used by default, is slower than other drivers and doesn't provide fast snapshots, fast copy/launch, quotas and optimized backups.

If you want to use an optimized setup, go through the interactive configuration process instead.
```

(initialize-preseed)=
## Non-interactive configuration

The `incus admin init` command supports a `--preseed` command line flag that makes it possible to fully configure the Incus daemon settings, storage pools, network devices and profiles, in a non-interactive way through a preseed YAML file.

For example, starting from a brand new Incus installation, you could configure Incus with the following command:

```bash
    cat <<EOF | incus admin init --preseed
config:
  core.https_address: 192.0.2.1:9999
  images.auto_update_interval: 15
networks:
- name: incusbr0
  type: bridge
  config:
    ipv4.address: auto
    ipv6.address: none
EOF
```

This preseed configuration initializes the Incus daemon to listen for HTTPS connections on port 9999 of the 192.0.2.1 address, to automatically update images every 15 hours and to create a network bridge device named `incusbr0`, which gets assigned an IPv4 address automatically.

### Re-configuring an existing Incus installation

If you are configuring a new Incus installation, the preseed command applies the configuration as specified (as long as the given YAML contains valid keys and values).
There is no existing state that might conflict with the specified configuration.

However, if you are re-configuring an existing Incus installation using the preseed command, the provided YAML configuration might conflict with the existing configuration.
To avoid such conflicts, the following rules are in place:

- The provided YAML configuration overwrites existing entities.
  This means that if you are re-configuring an existing entity, you must provide the full configuration for the entity and not just the different keys.
- If the provided YAML configuration contains entities that do not exist, they are created.

This is the same behavior as for a `PUT` request in the {doc}`../rest-api`.

#### Rollback

If some parts of the new configuration conflict with the existing state (for example, they try to change the driver of a storage pool from `dir` to `zfs`), the preseed command fails and automatically attempts to roll back any changes that were applied so far.

For example, it deletes entities that were created by the new configuration and reverts overwritten entities back to their original state.

Failure modes when overwriting entities are the same as for the `PUT` requests in the {doc}`../rest-api`.

```{note}
The rollback process might potentially fail, although rarely (typically due to backend bugs or limitations).
You should therefore be careful when trying to reconfigure a Incus daemon via preseed.
```

### Default profile

Unlike the interactive initialization mode, the `incus admin init --preseed` command does not modify the default profile, unless you explicitly express that in the provided YAML payload.

For instance, you will typically want to attach a root disk device and a network interface to your default profile.
See the following section for an example.

### Configuration format

The supported keys and values of the various entities are the same as the ones documented in the {doc}`../rest-api`, but converted to YAML for convenience.
However, you can also use JSON, since YAML is a superset of JSON.

The following snippet gives an example of a preseed payload that contains most of the possible configurations.
You can use it as a template for your own preseed file and add, change or remove what you need:

```yaml

# Daemon settings
config:
  core.https_address: 192.0.2.1:9999
  images.auto_update_interval: 6

# Storage pools
storage_pools:
- name: data
  driver: zfs
  config:
    source: my-zfs-pool/my-zfs-dataset

# Network devices
networks:
- name: incus-my-bridge
  type: bridge
  config:
    ipv4.address: auto
    ipv6.address: none

# Profiles
profiles:
- name: default
  devices:
    root:
      path: /
      pool: data
      type: disk
- name: test-profile
  description: "Test profile"
  config:
    limits.memory: 2GiB
  devices:
    test0:
      name: test0
      nictype: bridged
      parent: incus-my-bridge
      type: nic
```
