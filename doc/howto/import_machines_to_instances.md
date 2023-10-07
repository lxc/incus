(import-machines-to-instances)=
# How to import physical or virtual machines to Incus instances

Incus provides a tool (`incus-migrate`) to create a Incus instance based on an existing disk or image.

You can run the tool on any Linux machine.
It connects to a Incus server and creates a blank instance, which you can configure during or after the migration.
The tool then copies the data from the disk or image that you provide to the instance.

```{note}
If you want to configure your new instance during the migration process, set up the entities that you want your instance to use before starting the migration process.

By default, the new instance will use the entities specified in the `default` profile.
You can specify a different profile (or a profile list) to customize the configuration.
See {ref}`profiles` for more information.
You can also override {ref}`instance-options`, the {ref}`storage pool <storage-pools>` to be used and the size for the {ref}`storage volume <storage-volumes>`, and the {ref}`network <networking>` to be used.

Alternatively, you can update the instance configuration after the migration is complete.
```

The tool can create both containers and virtual machines:

* When creating a container, you must provide a disk or partition that contains the root file system for the container.
  For example, this could be the `/` root disk of the machine or container where you are running the tool.
* When creating a virtual machine, you must provide a bootable disk, partition or image.
  This means that just providing a file system is not sufficient, and you cannot create a virtual machine from a container that you are running.
  It is also not possible to create a virtual machine from the physical machine that you are using to do the migration, because the migration tool would be using the disk that it is copying.
  Instead, you could provide a bootable image, or a bootable partition or disk that is currently not in use.

   ````{tip}
   If you want to convert a Windows VM from a foreign hypervisor (not from QEMU/KVM with Q35/`virtio-scsi`),
   you must install the `virtio-win` drivers to your Windows. Otherwise, your VM won't boot.
   <details>
   <summary>Expand to see how to integrate the required drivers to your Windows VM</summary>
   Install the required tools on the host:

   1. Install `virt-v2v` version >= 2.3.4 (this is the minimal version that supports the `--block-driver` option).
   1. Install the `virtio-win` package, or download the [`virtio-win.iso`](https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/stable-virtio/virtio-win.iso) image and put it into the `/usr/share/virtio-win` folder.
   1. You might also need to install [`rhsrvany`](https://github.com/rwmjones/rhsrvany).

   Now you can use `virt-v2v` to convert images from a foreign hypervisor to `raw` images for Incus and include the required drivers:

   ```
   # Example 1. Convert a vmdk disk image to a raw image suitable for incus-migrate
   sudo virt-v2v --block-driver virtio-scsi -o local -of raw -os ./os -i vmx ./test-vm.vmx
   # Example 2. Convert a QEMU/KVM qcow2 image and integrate virtio-scsi driver
   sudo virt-v2v --block-driver virtio-scsi -o local -of raw -os ./os -if qcow2 -i disk test-vm-disk.qcow2
   ```

   You can find the resulting image in the `os` directory and use it with `incus-migrate` on the next steps.
   </details>
   ````

Complete the following steps to migrate an existing machine to a Incus instance:

1. Download the `bin.linux.incus-migrate` tool from the **Assets** section of the latest [Incus release](https://github.com/lxc/incus/releases).
1. Place the tool on the machine that you want to use to create the instance.
   Make it executable (usually by running `chmod u+x bin.linux.incus-migrate`).
1. Make sure that the machine has `rsync` installed.
   If it is missing, install it (for example, with `sudo apt install rsync`).
1. Run the tool:

       sudo ./bin.linux.incus-migrate

   The tool then asks you to provide the information required for the migration.

   ```{tip}
   As an alternative to running the tool interactively, you can provide the configuration as parameters to the command.
   See `./bin.linux.incus-migrate --help` for more information.
   ```

   1. Specify the Incus server URL, either as an IP address or as a DNS name.

      ```{note}
      The Incus server must be {ref}`exposed to the network <server-expose>`.
      If you want to import to a local Incus server, you must still expose it to the network.
      You can then specify `127.0.0.1` as the IP address to access the local server.
      ```

   1. Check and confirm the certificate fingerprint.
   1. Choose a method for authentication (see {ref}`authentication`).

      For example, if you choose using a certificate token, log on to the Incus server and create a token for the machine on which you are running the migration tool with [`incus config trust add`](incus_config_trust_add.md).
      Then use the generated token to authenticate the tool.
   1. Choose whether to create a container or a virtual machine.
      See {ref}`containers-and-vms`.
   1. Specify a name for the instance that you are creating.
   1. Provide the path to a root file system (for containers) or a bootable disk, partition or image file (for virtual machines).
   1. For containers, optionally add additional file system mounts.
   1. For virtual machines, specify whether secure boot is supported.
   1. Optionally, configure the new instance.
      You can do so by specifying {ref}`profiles <profiles>`, directly setting {ref}`configuration options <instance-options>` or changing {ref}`storage <storage>` or {ref}`network <networking>` settings.

      Alternatively, you can configure the new instance after the migration.
   1. When you are done with the configuration, start the migration process.

   <details>
   <summary>Expand to see an example output for importing to a container</summary>

   ```{terminal}
   :input: sudo ./bin.linux.incus-migrate

   Please provide Incus server URL: https://192.0.2.7:8443
   Certificate fingerprint: xxxxxxxxxxxxxxxxx
   ok (y/n)? y

   1) Use a certificate token
   2) Use an existing TLS authentication certificate
   3) Generate a temporary TLS authentication certificate
   Please pick an authentication mechanism above: 1
   Please provide the certificate token: xxxxxxxxxxxxxxxx

   Remote Incus server:
     Hostname: bar
     Version: 5.4

   Would you like to create a container (1) or virtual-machine (2)?: 1
   Name of the new instance: foo
   Please provide the path to a root filesystem: /
   Do you want to add additional filesystem mounts? [default=no]:

   Instance to be created:
     Name: foo
     Project: default
     Type: container
     Source: /

   Additional overrides can be applied at this stage:
   1) Begin the migration with the above configuration
   2) Override profile list
   3) Set additional configuration options
   4) Change instance storage pool or volume size
   5) Change instance network

   Please pick one of the options above [default=1]: 3
   Please specify config keys and values (key=value ...): limits.cpu=2

   Instance to be created:
     Name: foo
     Project: default
     Type: container
     Source: /
     Config:
       limits.cpu: "2"

   Additional overrides can be applied at this stage:
   1) Begin the migration with the above configuration
   2) Override profile list
   3) Set additional configuration options
   4) Change instance storage pool or volume size
   5) Change instance network

   Please pick one of the options above [default=1]: 4
   Please provide the storage pool to use: default
   Do you want to change the storage size? [default=no]: yes
   Please specify the storage size: 20GiB

   Instance to be created:
     Name: foo
     Project: default
     Type: container
     Source: /
     Storage pool: default
     Storage pool size: 20GiB
     Config:
       limits.cpu: "2"

   Additional overrides can be applied at this stage:
   1) Begin the migration with the above configuration
   2) Override profile list
   3) Set additional configuration options
   4) Change instance storage pool or volume size
   5) Change instance network

   Please pick one of the options above [default=1]: 5
   Please specify the network to use for the instance: incusbr0

   Instance to be created:
     Name: foo
     Project: default
     Type: container
     Source: /
     Storage pool: default
     Storage pool size: 20GiB
     Network name: incusbr0
     Config:
       limits.cpu: "2"

   Additional overrides can be applied at this stage:
   1) Begin the migration with the above configuration
   2) Override profile list
   3) Set additional configuration options
   4) Change instance storage pool or volume size
   5) Change instance network

   Please pick one of the options above [default=1]: 1
   Instance foo successfully created
   ```

   </details>
   <details>
   <summary>Expand to see an example output for importing to a VM</summary>

   ```{terminal}
   :input: sudo ./bin.linux.incus-migrate

   Please provide Incus server URL: https://192.0.2.7:8443
   Certificate fingerprint: xxxxxxxxxxxxxxxxx
   ok (y/n)? y

   1) Use a certificate token
   2) Use an existing TLS authentication certificate
   3) Generate a temporary TLS authentication certificate
   Please pick an authentication mechanism above: 1
   Please provide the certificate token: xxxxxxxxxxxxxxxx

   Remote Incus server:
     Hostname: bar
     Version: 5.4

   Would you like to create a container (1) or virtual-machine (2)?: 2
   Name of the new instance: foo
   Please provide the path to a root filesystem: ./virtual-machine.img
   Does the VM support UEFI Secure Boot? [default=no]: no

   Instance to be created:
     Name: foo
     Project: default
     Type: virtual-machine
     Source: ./virtual-machine.img
     Config:
       security.secureboot: "false"

   Additional overrides can be applied at this stage:
   1) Begin the migration with the above configuration
   2) Override profile list
   3) Set additional configuration options
   4) Change instance storage pool or volume size
   5) Change instance network

   Please pick one of the options above [default=1]: 3
   Please specify config keys and values (key=value ...): limits.cpu=2

   Instance to be created:
     Name: foo
     Project: default
     Type: virtual-machine
     Source: ./virtual-machine.img
     Config:
       limits.cpu: "2"
       security.secureboot: "false"

   Additional overrides can be applied at this stage:
   1) Begin the migration with the above configuration
   2) Override profile list
   3) Set additional configuration options
   4) Change instance storage pool or volume size
   5) Change instance network

   Please pick one of the options above [default=1]: 4
   Please provide the storage pool to use: default
   Do you want to change the storage size? [default=no]: yes
   Please specify the storage size: 20GiB

   Instance to be created:
     Name: foo
     Project: default
     Type: virtual-machine
     Source: ./virtual-machine.img
     Storage pool: default
     Storage pool size: 20GiB
     Config:
       limits.cpu: "2"
       security.secureboot: "false"

   Additional overrides can be applied at this stage:
   1) Begin the migration with the above configuration
   2) Override profile list
   3) Set additional configuration options
   4) Change instance storage pool or volume size
   5) Change instance network

   Please pick one of the options above [default=1]: 5
   Please specify the network to use for the instance: incusbr0

   Instance to be created:
     Name: foo
     Project: default
     Type: virtual-machine
     Source: ./virtual-machine.img
     Storage pool: default
     Storage pool size: 20GiB
     Network name: incusbr0
     Config:
       limits.cpu: "2"
       security.secureboot: "false"

   Additional overrides can be applied at this stage:
   1) Begin the migration with the above configuration
   2) Override profile list
   3) Set additional configuration options
   4) Change instance storage pool or volume size
   5) Change instance network

   Please pick one of the options above [default=1]: 1
   Instance foo successfully created
   ```

   </details>
1. When the migration is complete, check the new instance and update its configuration to the new environment.
   Typically, you must update at least the storage configuration (`/etc/fstab`) and the network configuration.
