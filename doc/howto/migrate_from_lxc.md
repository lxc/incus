(migrate-from-lxc)=
# How to migrate containers from LXC to Incus

Incus provides a tool (`lxc-to-incus`) that you can use to import LXC containers into your Incus server.
The LXC containers must exist on the same machine as the Incus server.

The tool analyzes the LXC containers and migrates both their data and their configuration into new Incus containers.

```{note}
Alternatively, you can use the `incus-migrate` tool within a LXC container to migrate it to Incus (see {ref}`import-machines-to-instances`).
However, this tool does not migrate any of the LXC container configuration.
```

## Get the tool

If the tool isn't provided alongside your Incus installation, you can build it yourself.
Make sure that you have `go` (version 1.18 or later) installed and get the tool with the following command:

    go install github.com/lxc/incus/cmd/lxc-to-incus@latest

## Prepare your LXC containers

You can migrate one container at a time or all of your LXC containers at the same time.

```{note}
Migrated containers use the same name as the original containers.
You cannot migrate containers with a name that already exists as an instance name in Incus.

Therefore, rename any LXC containers that might cause name conflicts before you start the migration process.
```

Before you start the migration process, stop the LXC containers that you want to migrate.

## Start the migration process

Run `sudo lxc-to-incus [flags]` to migrate the containers.

For example, to migrate all containers:

    sudo lxc-to-incus --all

To migrate only the `lxc1` container:

    sudo lxc-to-incus --containers lxc1

To migrate two containers (`lxc1` and `lxc2`) and use the `my-storage` storage pool in Incus:

    sudo lxc-to-incus --containers lxc1,lxc2 --storage my-storage

To test the migration of all containers without actually running it:

    sudo lxc-to-incus --all --dry-run

To migrate all containers but limit the `rsync` bandwidth to 5000 KB/s:

    sudo lxc-to-incus --all --rsync-args --bwlimit=5000

Run `sudo lxc-to-incus --help` to check all available flags.

```{note}
If you get an error that the `linux64` architecture isn't supported, either update the tool to the latest version or change the architecture in the LXC container configuration from `linux64` to either `amd64` or `x86_64`.
```

## Check the configuration

The tool analyzes the LXC configuration and the configuration of the container (or containers) and migrates as much of the configuration as possible.
You will see output similar to the following:

```{terminal}
:input: sudo lxc-to-incus --containers lxc1

Parsing LXC configuration
Checking for unsupported LXC configuration keys
Checking for existing containers
Checking whether container has already been migrated
Validating whether incomplete AppArmor support is enabled
Validating whether mounting a minimal /dev is enabled
Validating container rootfs
Processing network configuration
Processing storage configuration
Processing environment configuration
Processing container boot configuration
Processing container apparmor configuration
Processing container seccomp configuration
Processing container SELinux configuration
Processing container capabilities configuration
Processing container architecture configuration
Creating container
Transferring container: lxc1: ...
Container 'lxc1' successfully created
```

After the migration process is complete, you can check and, if necessary, update the configuration in Incus before you start the migrated Incus container.
