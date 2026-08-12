test_storage_volumes_vm() {
    if [ -n "${INCUS_OFFLINE:-}" ]; then
        echo "==> SKIP: External connectivity needed to pull test image"
        export TEST_UNMET_REQUIREMENT="external connectivity needed to pull test image"
        return
    fi

    if [ ! -e /dev/kvm ] || ! command -v "qemu-system-$(uname -m)" > /dev/null 2>&1; then
        echo "==> SKIP: QEMU and KVM needed to run virtual machines"
        export TEST_UNMET_REQUIREMENT="QEMU and KVM needed to run virtual machines"
        return
    fi

    if ! command -v genisoimage > /dev/null 2>&1; then
        echo "==> SKIP: genisoimage needed to create ISO volumes"
        export TEST_UNMET_REQUIREMENT="genisoimage needed to create ISO volumes"
        return
    fi

    poolDriverList="${INCUS_VM_STORAGE_DRIVERS:-dir btrfs lvm lvm-thin zfs ceph linstor}"

    incus network create incusbr0
    incus project create test-volumes -c features.images=false
    incus project switch test-volumes
    incus profile device add default eth0 nic network=incusbr0 name=eth0

    poolName="vmpool$$"

    for poolDriver in ${poolDriverList}; do
        if ! storage_backend_available "${poolDriver%-thin}"; then
            # Fail when an explicitly requested driver isn't available.
            if [ -n "${INCUS_VM_STORAGE_DRIVERS:-}" ]; then
                echo "==> FAIL: Storage driver ${poolDriver} not available"
                false
            fi

            echo "==> Skipping driver ${poolDriver} (not available)"
            continue
        fi

        echo "==> Create storage pool using driver ${poolDriver}"
        if [ "${poolDriver}" = "linstor" ]; then
            linstor_preconfigure "${INCUS_DIR}"
        fi

        if [ "${poolDriver}" = "dir" ]; then
            incus storage create "${poolName}" "${poolDriver}" volume.size=5GB
        elif [ "${poolDriver}" = "ceph" ]; then
            incus storage create "${poolName}" "${poolDriver}" source="${poolName}" volume.size=5GB
        elif [ "${poolDriver}" = "linstor" ]; then
            incus storage create "${poolName}" "${poolDriver}" linstor.resource_group.place_count=1 volume.size=5GB
        elif [ "${poolDriver}" = "lvm" ]; then
            incus storage create "${poolName}" "${poolDriver}" size=40GiB lvm.use_thinpool=false volume.size=5GB
        elif [ "${poolDriver}" = "lvm-thin" ]; then
            incus storage create "${poolName}" lvm size=20GiB volume.size=5GB
        else
            incus storage create "${poolName}" "${poolDriver}" size=20GB volume.size=5GB
        fi

        echo "==> Create VM"
        incus init images:debian/13 v1 --vm -s "${poolName}"
        incus init images:debian/13 v2 --vm -s "${poolName}"

        echo "==> Create custom block volume and attach it to VM"
        incus storage volume create "${poolName}" vol1 --type=block size=10MB
        incus storage volume attach "${poolName}" vol1 v1

        echo "==> Create custom volume and attach it to VM"
        incus storage volume create "${poolName}" vol4 size=10MB
        incus storage volume attach "${poolName}" vol4 v1 foo /foo

        echo "==> Start VM and add content to custom block volume"
        incus start v1
        incus wait v1 agent --timeout=90 --interval=1

        # Retry the mkfs as the by-id device links are created asynchronously at boot.
        # shellcheck disable=2016
        incus exec v1 -- /bin/sh -c 'for i in \$(seq 30); do mkfs.ext4 /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_vol1 && exit 0; sleep 1; done; ls -l /dev/disk/by-id/; cat /proc/mounts; exit 1'
        incus exec v1 -- /bin/sh -c "mount /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_vol1 /mnt && echo foo > /mnt/bar && umount /mnt"

        echo "==> Stop VM and detach custom volumes"
        incus stop -f v1
        incus storage volume detach "${poolName}" vol1 v1
        incus storage volume detach "${poolName}" vol4 v1

        echo "==> Backup custom block volume"
        incus storage volume export "${poolName}" vol1 "${TEST_DIR}/vol1.tar.gz"
        incus storage volume export "${poolName}" vol1 "${TEST_DIR}/vol1-optimized.tar.gz" --optimized-storage

        echo "==> Import custom block volume"
        incus storage volume import "${poolName}" "${TEST_DIR}/vol1.tar.gz" vol2
        incus storage volume import "${poolName}" "${TEST_DIR}/vol1-optimized.tar.gz" vol3
        rm "${TEST_DIR}/vol1.tar.gz"
        rm "${TEST_DIR}/vol1-optimized.tar.gz"

        echo "==> Import custom ISO volume"
        tmp_iso_dir="$(mktemp -d -p "${TEST_DIR}" XXX)"
        echo foo > "${tmp_iso_dir}/foo"
        genisoimage -o "${TEST_DIR}/vol5.iso" "${tmp_iso_dir}"
        rm -f "${tmp_iso_dir}/foo"
        echo bar > "${tmp_iso_dir}/bar"
        genisoimage -o "${TEST_DIR}/vol6.iso" "${tmp_iso_dir}"
        rm -rf "${tmp_iso_dir}"
        incus storage volume import "${poolName}" "${TEST_DIR}/vol5.iso" vol5
        incus storage volume import "${poolName}" "${TEST_DIR}/vol6.iso" vol6
        rm -f "${TEST_DIR}/vol5.iso" "${TEST_DIR}/vol6.iso"

        echo "==> Attach custom block volumes to VM"
        # Both volumes can be attached at the same time.
        incus storage volume attach "${poolName}" vol2 v1
        incus storage volume attach "${poolName}" vol3 v1

        echo "==> Attach custom ISO volumes to VM"
        incus storage volume attach "${poolName}" vol5 v1
        incus storage volume attach "${poolName}" vol6 v1
        incus storage volume attach "${poolName}" vol5 v2
        incus storage volume attach "${poolName}" vol6 v2

        echo "==> Start VM and check content"
        incus start v1
        incus start v2
        incus wait v1 agent --timeout=90 --interval=1
        incus wait v2 agent --timeout=90 --interval=1

        # Wait for the devices to be mountable as the by-id device links are created asynchronously at boot.
        # shellcheck disable=2016
        incus exec v1 -- /bin/sh -c 'for i in \$(seq 30); do mount /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_vol2 /mnt && umount /mnt && mount /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_vol3 /mnt && umount /mnt && exit 0; sleep 1; done; ls -l /dev/disk/by-id/; cat /proc/mounts; exit 1'
        # shellcheck disable=2016
        incus exec v2 -- /bin/sh -c 'for i in \$(seq 30); do mount /dev/disk/by-id/scsi-0QEMU_QEMU_CD-ROM_incus_vol5 /mnt && umount /mnt && exit 0; sleep 1; done; ls -l /dev/disk/by-id/; cat /proc/mounts; exit 1'

        # shellcheck disable=2016
        incus exec v1 -- /bin/sh -c 'mount /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_vol2 /mnt && [ \$(cat /mnt/bar) = foo ] && umount /mnt'
        # shellcheck disable=2016
        incus exec v1 -- /bin/sh -c 'mount /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_vol3 /mnt && [ \$(cat /mnt/bar) = foo ] && umount /mnt'

        # mount ISOs and check content
        # shellcheck disable=2016
        incus exec v1 -- /bin/sh -c 'mount /dev/disk/by-id/scsi-0QEMU_QEMU_CD-ROM_incus_vol5 /mnt && [ \$(cat /mnt/foo) = foo ] && ! touch /mnt/foo && umount /mnt'
        # shellcheck disable=2016
        incus exec v1 -- /bin/sh -c 'mount /dev/disk/by-id/scsi-0QEMU_QEMU_CD-ROM_incus_vol6 /mnt && [ \$(cat /mnt/bar) = bar ] && ! touch /mnt/bar && umount /mnt'

        # concurrent readonly ISO mounts
        # shellcheck disable=2016
        incus exec v1 -- /bin/sh -c 'mount /dev/disk/by-id/scsi-0QEMU_QEMU_CD-ROM_incus_vol5 /mnt && [ \$(cat /mnt/foo) = foo ] && ! touch /mnt/foo'
        # shellcheck disable=2016
        incus exec v2 -- /bin/sh -c 'mount /dev/disk/by-id/scsi-0QEMU_QEMU_CD-ROM_incus_vol5 /mnt && [ \$(cat /mnt/foo) = foo ] && ! touch /mnt/foo'
        incus exec v1 -- umount /mnt
        incus exec v2 -- umount /mnt

        echo "==> Detaching volumes"
        incus stop -f v1
        incus delete -f v2
        incus storage volume detach "${poolName}" vol2 v1
        incus storage volume detach "${poolName}" vol3 v1
        incus storage volume detach "${poolName}" vol6 v1

        echo "==> Publishing VM to image"
        # Daemon storage volumes aren't allowed on clustered storage pools.
        if [ "${poolDriver}" != "ceph" ] && [ "${poolDriver}" != "linstor" ]; then
            incus storage volume create "${poolName}" images --project=default
            incus config set storage.images_volume "${poolName}"/images
        fi

        incus publish v1 --alias v1image
        incus launch v1image v2 -s "${poolName}"
        incus wait v2 agent --timeout=90 --interval=1
        incus delete v2 -f
        incus image delete v1image

        if [ "${poolDriver}" != "ceph" ] && [ "${poolDriver}" != "linstor" ]; then
            incus config unset storage.images_volume
            incus storage volume delete "${poolName}" images --project=default
        fi

        echo "==> Deleting VM"
        incus rm v1

        echo "==> Deleting storage pool and volumes"
        incus storage volume rm "${poolName}" vol1
        incus storage volume rm "${poolName}" vol2
        incus storage volume rm "${poolName}" vol3
        incus storage volume rm "${poolName}" vol4
        incus storage volume rm "${poolName}" vol5
        incus storage volume rm "${poolName}" vol6
        incus storage rm "${poolName}"
    done

    incus profile device remove default eth0
    incus project switch default
    incus project delete test-volumes
    incus network delete incusbr0
}
