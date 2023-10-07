(instances-create)=
# How to create instances

To create an instance, you can use either the [`incus init`](incus_create.md) or the [`incus launch`](incus_launch.md) command.
The [`incus init`](incus_create.md) command only creates the instance, while the [`incus launch`](incus_launch.md) command creates and starts it.

## Usage

Enter the following command to create a container:

    incus launch|init <image_server>:<image_name> <instance_name> [flags]

Image
: Images contain a basic operating system (for example, a Linux distribution) and some Incus-related information.
  Images for various operating systems are available on the built-in remote image servers.
  See {ref}`images` for more information.

  Unless the image is available locally, you must specify the name of the image server and the name of the image (for example, `images:ubuntu/22.04` for the official 22.04 Ubuntu image).

Instance name
: Instance names must be unique within a Incus deployment (also within a cluster).
  See {ref}`instance-properties` for additional requirements.

Flags
: See [`incus launch --help`](incus_launch.md) or [`incus init --help`](incus_create.md) for a full list of flags.
  The most common flags are:

  - `--config` to specify a configuration option for the new instance
  - `--device` to override {ref}`device options <devices>` for a device provided through a profile
  - `--profile` to specify a {ref}`profile <profiles>` to use for the new instance
  - `--network` or `--storage` to make the new instance use a specific network or storage pool
  - `--target` to create the instance on a specific cluster member
  - `--vm` to create a virtual machine instead of a container

## Pass a configuration file

Instead of specifying the instance configuration as flags, you can pass it to the command as a YAML file.

For example, to launch a container with the configuration from `config.yaml`, enter the following command:

    incus launch images:ubuntu/22.04 ubuntu-config < config.yaml

```{tip}
Check the contents of an existing instance configuration ([`incus config show <instance_name> --expanded`](incus_config_show.md)) to see the required syntax of the YAML file.
```

## Examples

The following examples use [`incus launch`](incus_launch.md), but you can use [`incus init`](incus_create.md) in the same way.

### Launch a container

To launch a container with an Ubuntu 22.04 image from the `images` server using the instance name `ubuntu-container`, enter the following command:

    incus launch images:ubuntu/22.04 ubuntu-container

### Launch a virtual machine

To launch a virtual machine with an Ubuntu 22.04 image from the `images` server using the instance name `ubuntu-vm`, enter the following command:

    incus launch images:ubuntu/22.04 ubuntu-vm --vm

Or with a bigger disk:

    incus launch images:ubuntu/22.04 ubuntu-vm-big --vm --device root,size=30GiB

### Launch a container with specific configuration options

To launch a container and limit its resources to one vCPU and 192 MiB of RAM, enter the following command:

    incus launch images:ubuntu/22.04 ubuntu-limited --config limits.cpu=1 --config limits.memory=192MiB

### Launch a VM on a specific cluster member

To launch a virtual machine on the cluster member `server2`, enter the following command:

    incus launch images:ubuntu/22.04 ubuntu-container --vm --target server2

### Launch a container with a specific instance type

Incus supports simple instance types for clouds.
Those are represented as a string that can be passed at instance creation time.

The syntax allows the three following forms:

- `<instance type>`
- `<cloud>:<instance type>`
- `c<CPU>-m<RAM in GiB>`

For example, the following three instance types are equivalent:

- `t2.micro`
- `aws:t2.micro`
- `c1-m1`

To launch a container with this instance type, enter the following command:

    incus launch images:ubuntu/22.04 my-instance --type t2.micro

The list of supported clouds and instance types can be found at [`https://github.com/dustinkirkland/instance-type`](https://github.com/dustinkirkland/instance-type).

### Launch a VM that boots from an ISO

To launch a VM that boots from an ISO, you must first create a VM.
Let's assume that we want to create a VM and install it from the ISO image.
In this scenario, use the following command to create an empty VM:

    incus init iso-vm --empty --vm

The second step is to import an ISO image that can later be attached to the VM as a storage volume:

    incus storage volume import <path-to-image.iso> iso-volume --type=iso

Lastly, you need to attach the custom ISO volume to the VM using the following command:

    incus config device add iso-vm iso-volume disk pool=default source=iso-volume boot.priority=10

The `boot.priority` configuration key ensures that the VM will boot from the ISO first.
Start the VM and connect to the console as there might be a menu you need to interact with:

    incus start iso-vm --console

Once you're done in the serial console, you need to disconnect from the console using `ctrl+a-q`, and connect to the VGA console using the following command:

    incus console iso-vm --type=vga

You should now see the installer. After the installation is done, you need to detach the custom ISO volume:

    incus storage volume detach default iso-volume iso-vm

Now the VM can be rebooted, and it will boot from disk.
