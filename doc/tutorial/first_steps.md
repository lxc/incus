(first-steps)=
# First steps with Incus

This tutorial guides you through the first steps with Incus.
It covers installing and initializing Incus, creating and configuring some instances, interacting with the instances, and creating snapshots.

After going through these steps, you will have a general idea of how to use Incus, and you can start exploring more advanced use cases!

## Install and initialize Incus

Currently the easiest way to install Incus is to use the Debian or Ubuntu packages provided by [Zabbly](https://zabbly.com).
There are two repositories available, one for the current stable release and one for daily (untested) builds.

Installation instructions may be found here: [`https://github.com/zabbly/incus`](https://github.com/zabbly/incus)

If you prefer a different installation method, see {ref}`installing`.

1. Allow your user to control Incus

   Access to Incus in the packages above is controlled through two groups:

   - `incus` allows basic user access, no configuration and all actions restricted to a per-user project.
   - `incus-admin` allows full control over Incus.

   To control Incus without having to run all commands as root, you can add yourself to the `incus-admin` group:

       sudo adduser YOUR-USERNAME incus-admin
       newgrp incus-admin

   The `newgrp` step is needed in any terminal that interacts with Incus until you restart your user session.

1. Initialize Incus with:

       incus admin init --minimal

   This will create a minimal setup with default options.
   If you want to tune the initialization options, see {ref}`initialize` for more information.

## Launch and inspect instances

Incus is image based and can load images from different image servers.
In this tutorial, we will use the [official image server](https://images.linuxcontainers.org/).

You can list all images that are available on this server with:

    incus image list images:

See {ref}`images` for more information about the images that Incus uses.

Now, let's start by launching a few instances.
With *instance*, we mean either a container or a virtual machine.
See {ref}`containers-and-vms` for information about the difference between the two instance types.

For managing instances, we use the Incus command line client `incus`.

1. Launch a container called `first` using the Ubuntu 22.04 image:

       incus launch images:ubuntu/22.04 first

   ```{note}
   Launching this container takes a few seconds, because the image must be downloaded and unpacked first.
   ```

1. Launch a container called `second` using the same image:

       incus launch images:ubuntu/22.04 second

   ```{note}
   Launching this container is quicker than launching the first, because the image is already available.
   ```

1. Copy the first container into a container called `third`:

       incus copy first third

1. Launch a VM called `ubuntu-vm` using the Ubuntu 22.04 image:

       incus launch images:ubuntu/22.04 ubuntu-vm --vm

   ```{note}
   Even though you are using the same image name to launch the instance, Incus downloads a slightly different image that is compatible with VMs.
   ```

1. Check the list of instances that you launched:

       incus list

   You will see that all but the third container are running.
   This is because you created the third container by copying the first, but you didn't start it.

   You can start the third container with:

       incus start third

1. Query more information about each instance with:

       incus info first
       incus info second
       incus info third
       incus info ubuntu-vm

1. We don't need all of these instances for the remainder of the tutorial, so let's clean some of them up:

   1. Stop the second container:

          incus stop second

   1. Delete the second container:

          incus delete second

   1. Delete the third container:

          incus delete third

      Since this container is running, you get an error message that you must stop it first.
      Alternatively, you can force-delete it:

          incus delete third --force

See {ref}`instances-create` and {ref}`instances-manage` for more information.

## Configure instances

There are several limits and configuration options that you can set for your instances.
See {ref}`instance-options` for an overview.

Let's create another container with some resource limits:

1. Launch a container and limit it to one vCPU and 192 MiB of RAM:

       incus launch images:ubuntu/22.04 limited --config limits.cpu=1 --config limits.memory=192MiB

1. Check the current configuration and compare it to the configuration of the first (unlimited) container:

       incus config show limited
       incus config show first

1. Check the amount of free and used memory on the parent system and on the two containers:

       free -m
       incus exec first -- free -m
       incus exec limited -- free -m

   ```{note}
   The total amount of memory is identical for the parent system and the first container, because by default, the container inherits the resources from its parent environment.
   The limited container, on the other hand, has only 192 MiB available.
   ```

1. Check the number of CPUs available on the parent system and on the two containers:

       nproc
       incus exec first -- nproc
       incus exec limited -- nproc

   ```{note}
   Again, the number is identical for the parent system and the first container, but reduced for the limited container.
   ```

1. You can also update the configuration while your container is running:

   1. Configure a memory limit for your container:

          incus config set limited limits.memory=128MiB

   1. Check that the configuration has been applied:

          incus config show limited

   1. Check the amount of memory that is available to the container:

          incus exec limited -- free -m

      Note that the number has changed.

1. Depending on the instance type and the storage drivers that you use, there are more configuration options that you can specify.
   For example, you can configure the size of the root disk device for a VM:

   1. Check the current size of the root disk device of the Ubuntu VM:

      ```{terminal}
      :input: incus exec ubuntu-vm -- df -h

      Filesystem      Size  Used Avail Use% Mounted on
      /dev/root       9.6G  1.4G  8.2G  15% /
      tmpfs           483M     0  483M   0% /dev/shm
      tmpfs           193M  604K  193M   1% /run
      tmpfs           5.0M     0  5.0M   0% /run/lock
      tmpfs            50M   14M   37M  27% /run/incus_agent
      /dev/sda15      105M  6.1M   99M   6% /boot/efi
      ```

   1. Override the size of the root disk device:

          incus config device override ubuntu-vm root size=30GiB

   1. Restart the VM:

          incus restart ubuntu-vm

   1. Check the size of the root disk device again:

       ```{terminal}
       :input: incus exec ubuntu-vm -- df -h

       Filesystem      Size  Used Avail Use% Mounted on
       /dev/root        29G  1.4G   28G   5% /
       tmpfs           483M     0  483M   0% /dev/shm
       tmpfs           193M  588K  193M   1% /run
       tmpfs           5.0M     0  5.0M   0% /run/lock
       tmpfs            50M   14M   37M  27% /run/incus_agent
       /dev/sda15      105M  6.1M   99M   6% /boot/efi
       ```

See {ref}`instances-configure` and {ref}`instance-config` for more information.

## Interact with instances

You can interact with your instances by running commands in them (including an interactive shell) or accessing the files in the instance.

Start by launching an interactive shell in your instance:

1. Run the `bash` command in your container:

       incus exec first -- bash

1. Enter some commands, for example, display information about the operating system:

       cat /etc/*release

1. Exit the interactive shell:

       exit

Instead of logging on to the instance and running commands there, you can run commands directly from the host.

For example, you can install a command line tool on the instance and run it:

    incus exec first -- apt-get update
    incus exec first -- apt-get install sl -y
    incus exec first -- /usr/games/sl

See {ref}`run-commands` for more information.

You can also access the files from your instance and interact with them:

1. Pull a file from the container:

       incus file pull first/etc/hosts .

1. Add an entry to the file:

       echo "1.2.3.4 my-example" >> hosts

1. Push the file back to the container:

       incus file push hosts first/etc/hosts

1. Use the same mechanism to access log files:

       incus file pull first/var/log/syslog - | less

   ```{note}
   Press `q` to exit the `less` command.
   ```

See {ref}`instances-access-files` for more information.

## Manage snapshots

You can create a snapshot of your instance, which makes it easy to restore the instance to a previous state.

1. Create a snapshot called "clean":

       incus snapshot create first clean

1. Confirm that the snapshot has been created:

       incus list first
       incus info first

   ```{note}
   `incus list` shows the number of snapshots.
   `incus info` displays information about each snapshot.
   ```

1. Break the container:

       incus exec first -- rm /usr/bin/bash

1. Confirm the breakage:

       incus exec first -- bash

   ```{note}
   You do not get a shell, because you deleted the `bash` command.
   ```

1. Restore the container to the state of the snapshot:

       incus snapshot restore first clean

1. Confirm that everything is back to normal:

       incus exec first -- bash
       exit

1. Delete the snapshot:

       incus delete first/clean

See {ref}`instances-snapshots` for more information.
