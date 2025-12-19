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

  Unless the image is available locally, you must specify the name of the image server and the name of the image (for example, `images:debian/12` for a Debian 12 image).

Instance name
: Instance names must be unique within an Incus deployment (also within a cluster).
  See {ref}`instance-properties` for additional requirements.

Flags
: See [`incus launch --help`](incus_launch.md) or [`incus init --help`](incus_create.md) for a full list of flags.
  The most common flags are:

  - `--config` to specify a configuration option for the new instance
  - `--device` to override {ref}`device options <devices>` for a device provided through a profile, or to specify an {ref}`initial configuration for the root disk device <devices-disk-initial-config>`
  - `--profile` to specify a {ref}`profile <profiles>` to use for the new instance
  - `--network` or `--storage` to make the new instance use a specific network or storage pool
  - `--target` to create the instance on a specific cluster member
  - `--vm` to create a virtual machine instead of a container

## Pass a configuration file

Instead of specifying the instance configuration as flags, you can pass it to the command as a YAML file.

For example, to launch a container with the configuration from `config.yaml`, enter the following command:

    incus launch images:debian/12 debian-config < config.yaml

```{tip}
Check the contents of an existing instance configuration ([`incus config show <instance_name> --expanded`](incus_config_show.md)) to see the required syntax of the YAML file.
```

## Examples

The following examples use [`incus launch`](incus_launch.md), but you can use [`incus init`](incus_create.md) in the same way.

### Launch a system container

To launch a system container with a Debian 12 image from the `images` server using the instance name `debian-container`, enter the following command:

    incus launch images:debian/12 debian-container

### Launch an application container

To launch an application (OCI) container, you first need to add an image registry:

    incus remote add oci-docker https://docker.io --protocol=oci

And then can launch a container from one of its images:

    incus launch oci-docker:hello-world --ephemeral --console

### Launch a virtual machine

To launch a virtual machine with a Debian 12 image from the `images` server using the instance name `debian-vm`, enter the following command:

    incus launch images:debian/12 debian-vm --vm

Or with a bigger disk:

    incus launch images:debian/12 debian-vm-big --vm --device root,size=30GiB

### Launch a container with specific configuration options

To launch a container and limit its resources to one vCPU and 192 MiB of RAM, enter the following command:

    incus launch images:debian/12 debian-limited --config limits.cpu=1 --config limits.memory=192MiB

### Launch a VM on a specific cluster member

To launch a virtual machine on the cluster member `server2`, enter the following command:

    incus launch images:debian/12 debian-container --vm --target server2

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

    incus launch images:debian/12 my-instance --type t2.micro

The list of supported clouds and instance types can be found at [`https://github.com/dustinkirkland/instance-type`](https://github.com/dustinkirkland/instance-type).

### Launch a VM that boots from an ISO

```{note}
When creating a Windows or a macOS virtual machine, make sure to set the `image.os` property to something starting with `Windows` or `macOS` respectively.
Doing so will tell Incus to expect the correct OS to be running inside of the virtual machine and to tweak behavior accordingly.

This notably will cause on Windows:
 - Some unsupported virtual devices to be disabled
 - The {abbr}`RTC (Real Time Clock)` clock to be based on system local time rather than UTC
 - IOMMU handling to switch to an Intel IOMMU controller
```

To launch a VM that boots from an ISO, you must first create a VM.
Let's assume that we want to create a VM and install it from the ISO image.
In this scenario, use the following command to create an empty VM:

    incus init iso-vm --empty --vm

```{note}
Depending on the needs of the operating system being installed, you may want to allocate more CPU, memory or storage to the virtual machine.

For example, for 2 CPUs, 4 GiB of memory and 50 GiB of storage, you can do:

    incus init iso-vm --empty --vm -c limits.cpu=2 -c limits.memory=4GiB -d root,size=50GiB
```

The second step is to import an ISO image that can later be attached to the VM as a storage volume:

    incus storage volume import <pool> <path-to-image.iso> iso-volume --type=iso

Lastly, you need to attach the custom ISO volume to the VM using the following command:

    incus config device add iso-vm iso-volume disk pool=<pool> source=iso-volume boot.priority=10

The `boot.priority` configuration key ensures that the VM will boot from the ISO first.
Start the VM and connect to the console as there might be a menu you need to interact with:

    incus start iso-vm --console

Once you're done in the serial console, you need to disconnect from the console using `ctrl+a-q`, and connect to the VGA console using the following command:

    incus console iso-vm --type=vga

You should now see the installer. After the installation is done, you need to detach the custom ISO volume:

    incus storage volume detach <pool> iso-volume iso-vm

Now the VM can be rebooted, and it will boot from disk.

### Install the Incus Agent into virtual machine instances

```{warning}
The Incus agent relies on TLS certificates for communication between the host and guest.
For this to work correctly, the guest clock needs to be reasonably in sync with the host.
```

In order for features like direct command execution (`incus exec`), file transfers (`incus file`) and detailed usage metrics (`incus info`)
to work properly with virtual machines, an agent software is provided by Incus.

The virtual machine images from the [images](https://images.linuxcontainers.org) remote are pre-configured to load that agent on startup.

For other virtual machines, you may want to manually install the agent.

```{note}
The Incus Agent is currently available only on Linux, Windows and macOS virtual machines.
```

Incus provides the agent primarily through a remote `9p` file system with mount name `config`.
Alternatively, it is possible to get the agent files through a virtual CD-ROM drive by adding a `disk` device to the instance and using `agent:config` as the `source` property.

    incus config device add INSTANCE-NAME agent disk source=agent:config

#### On Linux

To install the agent on a Linux system with `9p`, you'll need to get access to the virtual machine and run the following commands:

    mount -t 9p config /mnt
    cd /mnt
    ./install.sh

When using the virtual CD-ROM drive, you can use the following instead:

    mount /dev/disk/by-label/incus-agent /mnt
    cd /mnt
    ./install.sh

```{note}
All installation commands showed above should be run from a `root` shell.
They require a Linux system using `systemd` as its init system.

The first line will mount the remote file system on the mount point `/mnt`.
The subsequent commands will run the installation script `install.sh` to install and run the Incus Agent.
```

#### On Windows

For Windows systems, the virtual CD-ROM drive must be used.
The agent can be installed as a service by running the `install.ps1` file from the CD-ROM drive (by either executing the file from the explorer or terminal).
To automatically let the agent update itself, leave the CD-ROM available to the virtual machine

```{note}
To install: The agent as a service, local administrator privileges are required.

To update: When rebooting the virtual machine, the CD-ROM will be mounted again with the newest version and the service will automatically update itself using the files in the CD-ROM drive.
```

Otherwise, the agent can manually be started by opening a terminal and running (assuming `d:\` is the CD-ROM):

    d:\
    .\incus-agent.exe

#### On macOS

For macOS systems, the agent can manually be installed using a `9p` mount by opening a terminal **as root** and running the following commands:

    mount_9p config
    cd /Volumes/config
    ./install.sh

```{warning}
Apple's Transparency, Consent, Control daemon requires you to allow full disk access to `sh` for the agent to be automatically started.
This reduces the overall security of the system, by relaxing some of Apple's additional security restrictions.
This does not in any way bypass UNIX permissions, however, if you are not comfortable with that, you will need to manually run `incus-agent` each time.
```

### Configure the Incus Agent
By default the Incus Agent will have all features enabled.

In some environments, VM owners may want to turn off specific features.

This can be done through a `incus-agent.yml` file which is located at:

- `/etc/incus-agent.yml` on Linux
- `/usr/local/etc/incus-agent.yml` on MacOS
- `C:\Program Files\Incus Agent\incus-agent.yml` on Windows

If the file is missing or is empty, all features will be enabled.
If the file contains a `features` map, then all features will be disabled unless specifically enabled.

The supported features are:

- `guestapi` controls whether the agent exposes the `/dev/incus` API within the guest
- `exec` controls whether commands can be executed through the agent
- `files` controls whether the files transfer API is available
- `mounts` controls whether to setup the file system mounts for shared disk devices
- `metrics` controls access to detailed OpenMetrics data
- `state` controls access to basic OS state information (OS version, network interface details, ...)

An example YAML file would be:

```
features:
  guestapi: true
  metrics: true
  state: true
```
