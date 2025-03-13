(storage-linstor-setup)=
# How to set up LINSTOR with Incus

Follow this guide to setup a LINSTOR cluster and configure Incus to use it as a storage provider.

In this guide, we'll demonstrate the setup across three nodes (`server01`, `server02`, `server03`) running Ubuntu 24.04, all of which will run Incus instances and contribute storage to the LINSTOR cluster. Other configurations are also supported, such as having dedicated storage nodes and only consuming that storage via the network. Regardless of the underlying storage setup, all Incus nodes should run the LINSTOR satellite service to be able to mount volumes on the node.

It's also worth noting that we'll be using LVM Thin as the LINSTOR storage backend, but regular LVM and ZFS are also supported.

1. Complete the following steps on the three machines to install the required LINSTOR components:

   1. Add the LINBIT PPA:

          sudo add-apt-repository ppa:linbit/linbit-drbd9-stack
          sudo apt update

   1. Install the required packages:

          sudo apt install lvm2 drbd-dkms drbd-utils linstor-satellite

   1. Enable the LINSTOR satellite service to ensure it is always started with the machine:

          sudo systemctl enable --now linstor-satellite

1. Complete the following steps on the first machine (`server01`) to setup the LINSTOR controller and bootstrap the LINSTOR cluster:

   1. Install the LINSTOR controller and client packages:

          sudo apt install linstor-controller linstor-client python3-setuptools

   1. Enable the LINSTOR controller service to ensure it is always started with the machine:

          sudo systemctl enable --now linstor-controller

   1. Add the nodes to the LINSTOR cluster (replace `<server_1>`, `<server_2>` and `<server_3>` with the IP addresses of the respective machines). In this case `server01` is a combined node (controller + satellite), while the other two nodes are just satellites:

          linstor node create server01 <server_1> --node-type combined
          linstor node create server02 <server_2> --node-type satellite
          linstor node create server03 <server_3> --node-type satellite

   1. Verify that all nodes are online and that their node names match the node names in the Incus cluster:

      ```{terminal}
      :input: linstor node list
      :scroll:

      ╭─────────────────────────────────────────────────────────────╮
      ┊ Node     ┊ NodeType  ┊ Addresses                   ┊ State  ┊
      ╞═════════════════════════════════════════════════════════════╡
      ┊ server01 ┊ COMBINED  ┊ 10.172.117.211:3366 (PLAIN) ┊ Online ┊
      ┊ server02 ┊ SATELLITE ┊ 10.172.117.35:3366 (PLAIN)  ┊ Online ┊
      ┊ server03 ┊ SATELLITE ┊ 10.172.117.232:3366 (PLAIN) ┊ Online ┊
      ╰─────────────────────────────────────────────────────────────╯
      ```

   1. Verify that all nodes have the desired features available. In this case we're interested in `LVMThin` and `DRBD`, which are available:

      ```{terminal}
      :input: linstor node info
      :scroll:

      ╭───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
      ┊ Node     ┊ Diskless ┊ LVM ┊ LVMThin ┊ ZFS/Thin ┊ File/Thin ┊ SPDK ┊ EXOS ┊ Remote SPDK ┊ Storage Spaces ┊ Storage Spaces/Thin ┊
      ╞═══════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════╡
      ┊ server01 ┊ +        ┊ +   ┊ +       ┊ +        ┊ +         ┊ -    ┊ -    ┊ +           ┊ -              ┊ -                   ┊
      ┊ server02 ┊ +        ┊ +   ┊ +       ┊ +        ┊ +         ┊ -    ┊ -    ┊ +           ┊ -              ┊ -                   ┊
      ┊ server03 ┊ +        ┊ +   ┊ +       ┊ +        ┊ +         ┊ -    ┊ -    ┊ +           ┊ -              ┊ -                   ┊
      ╰───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯

      ╭───────────────────────────────────────────────────────────────────────╮
      ┊ Node     ┊ DRBD ┊ LUKS ┊ NVMe ┊ Cache ┊ BCache ┊ WriteCache ┊ Storage ┊
      ╞═══════════════════════════════════════════════════════════════════════╡
      ┊ server01 ┊ +    ┊ -    ┊ -    ┊ +     ┊ -      ┊ +          ┊ +       ┊
      ┊ server02 ┊ +    ┊ -    ┊ -    ┊ +     ┊ -      ┊ +          ┊ +       ┊
      ┊ server03 ┊ +    ┊ -    ┊ -    ┊ +     ┊ -      ┊ +          ┊ +       ┊
      ╰───────────────────────────────────────────────────────────────────────╯
      ```

   1. Create the storage pools in each satellite node that will contribute storage to the cluster. In this case all satellite nodes will contribute storage, but in a setup with dedicated storage nodes we'd only create the storage pools on those nodes. We could setup the LVM volume group manually using `vgcreate` and `pvcreate` and tell LINSTOR to use this volume group to setup its storage pool, but the `linstor physical-storage create-device-pool` automates this setup in a convenient way. We could also specify multiple devices to compose the pool, but in this case we have a single `/dev/nvme1n1` device available on each node:

          linstor physical-storage create-device-pool --storage-pool nvme_pool --pool-name nvme_pool lvmthin server01 /dev/nvme1n1
          linstor physical-storage create-device-pool --storage-pool nvme_pool --pool-name nvme_pool lvmthin server02 /dev/nvme1n1
          linstor physical-storage create-device-pool --storage-pool nvme_pool --pool-name nvme_pool lvmthin server03 /dev/nvme1n1

   1. Verify that all storage pools are created and report the expected size:

      ```{terminal}
      :input: linstor storage-pool list
      :scroll:

      ╭────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
      ┊ StoragePool          ┊ Node     ┊ Driver   ┊ PoolName                    ┊ FreeCapacity ┊ TotalCapacity ┊ CanSnapshots ┊ State ┊ SharedName                    ┊
      ╞════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════╡
      ┊ DfltDisklessStorPool ┊ server01 ┊ DISKLESS ┊                             ┊              ┊               ┊ False        ┊ Ok    ┊ server01;DfltDisklessStorPool ┊
      ┊ DfltDisklessStorPool ┊ server02 ┊ DISKLESS ┊                             ┊              ┊               ┊ False        ┊ Ok    ┊ server02;DfltDisklessStorPool ┊
      ┊ DfltDisklessStorPool ┊ server03 ┊ DISKLESS ┊                             ┊              ┊               ┊ False        ┊ Ok    ┊ server03;DfltDisklessStorPool ┊
      ┊ nvme_pool            ┊ server01 ┊ LVM_THIN ┊ linstor_nvme_pool/nvme_pool ┊    49.89 GiB ┊     49.89 GiB ┊ True         ┊ Ok    ┊ server01;nvme_pool            ┊
      ┊ nvme_pool            ┊ server02 ┊ LVM_THIN ┊ linstor_nvme_pool/nvme_pool ┊    49.89 GiB ┊     49.89 GiB ┊ True         ┊ Ok    ┊ server02;nvme_pool            ┊
      ┊ nvme_pool            ┊ server03 ┊ LVM_THIN ┊ linstor_nvme_pool/nvme_pool ┊    49.89 GiB ┊     49.89 GiB ┊ True         ┊ Ok    ┊ server03;nvme_pool            ┊
      ╰────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
      ```

   1. Configure Incus to be able to communicate with the LINSTOR controller (replace `<server_1>` with the IP address of the controller machine):

          incus config set storage.linstor.controller_connection=http://<server_1>:3370

   1. Create the storage pool on Incus. We'll specify the `linstor.resource_group.storage_pool` option to ensure that LINSTOR uses the `nvme_pool` storage pool for the volumes on this Incus storage pool. This is specially useful if you have multiple LINSTOR storage pools (e.g. one for NVMe drives and another for SATA HDDs):

          incus storage create remote linstor --target server01
          incus storage create remote linstor --target server02
          incus storage create remote linstor --target server03
          incus storage create remote linstor linstor.resource_group.storage_pool=nvme_pool

   1. Verify the LINSTOR resource group created by Incus:

      ```{terminal}
      :input: linstor resource-group list
      :scroll:
      ╭──────────────────────────────────────────────────────────────────────────────────────╮
      ┊ ResourceGroup ┊ SelectFilter              ┊ VlmNrs ┊ Description                     ┊
      ╞══════════════════════════════════════════════════════════════════════════════════════╡
      ┊ DfltRscGrp    ┊ PlaceCount: 2             ┊        ┊                                 ┊
      ╞┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄╡
      ┊ remote        ┊ PlaceCount: 2             ┊        ┊ Resource group managed by Incus ┊
      ┊               ┊ StoragePool(s): nvme_pool ┊        ┊                                 ┊
      ╰──────────────────────────────────────────────────────────────────────────────────────╯
      ```

   1. To test the storage, create some volumes and instances:

          incus launch images:ubuntu/24.04 c1 --storage remote
          incus storage volume create remote fsvol
          incus storage volume attach remote fsvol c1 /mnt

          incus launch images:ubuntu/24.04 v1 --storage remote --vm -c migration.stateful=true
          incus storage volume create remote vol --type block size=42GiB
          incus storage volume attach remote vol v1

   1. Verify the LINSTOR view of the resources created by Incus:

      ```{terminal}
      :input: linstor resource-definition list --show-props Aux/Incus/name Aux/Incus/type Aux/Incus/content-type
      :scroll:
      ╭─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╮
      ┊ ResourceName                                  ┊ Port ┊ ResourceGroup ┊ Layers       ┊ State ┊ Aux/Incus/name                                                                ┊ Aux/Incus/type   ┊ Aux/Incus/content-type ┊
      ╞═════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════════╡
      ┊ incus-volume-1cb987892f6748299a7f894a483e4e7e ┊ 7004 ┊ remote        ┊ DRBD,STORAGE ┊ ok    ┊ incus-volume-v1                                                               ┊ virtual-machines ┊ block                  ┊
      ┊ incus-volume-5b680bf0dd6f4b39b784c1c151dd510c ┊ 7002 ┊ remote        ┊ DRBD,STORAGE ┊ ok    ┊ incus-volume-default_fsvol                                                    ┊ custom           ┊ filesystem             ┊
      ┊ incus-volume-5d7ee1b9c5224f73b3dd3c3a4ff46fed ┊ 7000 ┊ remote        ┊ DRBD,STORAGE ┊ ok    ┊ incus-volume-198e0b3f6b3685418d9c21b58445686f939596b1fccd8e295191fe515d1ab32c ┊ images           ┊ filesystem             ┊
      ┊ incus-volume-9f7ed7091da346e2b7c764348ffada54 ┊ 7001 ┊ remote        ┊ DRBD,STORAGE ┊ ok    ┊ incus-volume-c1                                                               ┊ containers       ┊ filesystem             ┊
      ┊ incus-volume-10991980d449418b9b8714b769f030d7 ┊ 7005 ┊ remote        ┊ DRBD,STORAGE ┊ ok    ┊ incus-volume-default_vol                                                      ┊ custom           ┊ block                  ┊
      ┊ incus-volume-af0e3529ad514b7b89c7a3a9b8b718ff ┊ 7003 ┊ remote        ┊ DRBD,STORAGE ┊ ok    ┊ incus-volume-dfc28af5f731668509b897ce7eb30d07c5bfe50502da4b2f19421a8a0b05137a ┊ images           ┊ block                  ┊
      ╰─────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
      ```
