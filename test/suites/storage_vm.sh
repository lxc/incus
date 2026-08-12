test_storage_vm() {
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

    poolDriverList="${INCUS_VM_STORAGE_DRIVERS:-dir btrfs lvm lvm-thin zfs ceph linstor}"

    incus network create incusbr0
    incus profile device remove default eth0
    incus profile device add default eth0 nic network=incusbr0 name=eth0

    poolName="vmpool$$"

    GiB=1073741823

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

        if [ "${poolDriver}" = "dir" ] || [ "${poolDriver}" = "ceph" ]; then
            incus storage create "${poolName}" "${poolDriver}"
        elif [ "${poolDriver}" = "linstor" ]; then
            incus storage create "${poolName}" "${poolDriver}" linstor.resource_group.place_count=1
        elif [ "${poolDriver}" = "lvm" ]; then
            incus storage create "${poolName}" "${poolDriver}" size=60GiB lvm.use_thinpool=false
        elif [ "${poolDriver}" = "lvm-thin" ]; then
            incus storage create "${poolName}" lvm size=20GiB
        else
            incus storage create "${poolName}" "${poolDriver}" size=20GiB
        fi

        echo "==> Create VM and boot"
        incus init images:debian/13 v1 --vm -s "${poolName}"
        incus start v1
        incus wait v1 agent --timeout=90 --interval=1
        incus info v1

        echo "==> Check /dev/disk/by-id"
        incus exec v1 -- test -e /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_root
        incus exec v1 -- test -e /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_root-part1
        incus exec v1 -- test -e /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_root-part2

        echo "==> Check config drive is readonly"
        # Check 9p config drive share is exported readonly.
        incus exec v1 -- mount -t 9p config /srv
        ! incus exec v1 -- touch /srv/incus-test || false
        incus exec v1 -- umount /srv

        echo "==> Checking VM root disk size is 10GiB"
        [ $(($(incus exec v1 -- blockdev --getsize64 /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_root) / GiB)) -eq "10" ]

        echo "foo" | incus exec v1 -- tee /root/foo.txt
        incus exec v1 -- sync
        incus snapshot create v1

        echo "==> Checking restore VM snapshot"
        incus snapshot restore v1 snap0
        incus wait v1 agent --timeout=90 --interval=1
        incus exec v1 -- cat /root/foo.txt | grep -Fx "foo"

        echo "==> Checking running copied VM snapshot"
        incus copy v1/snap0 v2
        incus start v2
        incus wait v2 agent --timeout=90 --interval=1
        incus exec v2 -- cat /root/foo.txt | grep -Fx "foo"

        echo "==> Checking VM snapshot copy root disk size is 10GiB"
        [ $(($(incus exec v2 -- blockdev --getsize64 /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_root) / GiB)) -eq "10" ]
        incus delete -f v2
        incus snapshot delete v1 snap0

        echo "==> Check QEMU crash behavior and recovery"
        incus exec v1 -- fsfreeze --freeze /
        uuid=$(incus config get v1 volatile.uuid)
        pgrep -af "${uuid}"
        rm "${INCUS_DIR}/run/v1/qemu.monitor"
        daemon_pid="$(cat "${INCUS_DIR}/incus.pid")"
        kill "${daemon_pid}"
        wait "${daemon_pid}" || true
        respawn_incus "${INCUS_DIR}" true
        sleep 5
        incus ls v1 | grep ERROR
        ! incus stop v1 || false
        ! incus start v1 || false
        pgrep -af "${uuid}"
        incus stop v1 -f
        ! pgrep -af "${uuid}" || false
        incus start v1
        incus wait v1 agent --timeout=90 --interval=1

        echo "==> Testing VM non-optimized export/import (while running to check config.mount is excluded)"
        incus exec v1 -- fsfreeze --freeze /
        incus export v1 "${TEST_DIR}/incus-test-${poolName}.tar.gz"
        incus delete -f v1
        incus import "${TEST_DIR}/incus-test-${poolName}.tar.gz"
        rm "${TEST_DIR}/incus-test-${poolName}.tar.gz"
        incus start v1
        incus wait v1 agent --timeout=90 --interval=1

        echo "==> Testing VM optimized export/import (while running to check config.mount is excluded)"
        incus exec v1 -- fsfreeze --freeze /
        incus export v1 "${TEST_DIR}/incus-test-${poolName}-optimized.tar.gz" --optimized-storage
        incus delete -f v1
        incus import "${TEST_DIR}/incus-test-${poolName}-optimized.tar.gz"
        rm "${TEST_DIR}/incus-test-${poolName}-optimized.tar.gz"
        incus start v1
        incus wait v1 agent --timeout=90 --interval=1

        incus config device set v1 root size=11GiB
        if [ "$(incus config get v1 volatile.root.apply_quota)" = "true" ]; then
            # Drivers that can't resize the volume of a running VM defer it to the next start.
            echo "==> Increasing VM root disk size for next boot"
            incus stop -f v1
            incus start v1
            incus wait v1 agent --timeout=90 --interval=1
        else
            echo "==> Increasing VM root disk size"
        fi

        echo "==> Checking VM root disk size is 11GiB"
        [ $(($(incus exec v1 -- blockdev --getsize64 /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_root) / GiB)) -eq "11" ]

        echo "==> Check VM shrink is blocked"
        ! incus config device set v1 root size=10GiB || false

        echo "==> Checking additional disk device support"
        incus stop -f v1

        # Create directory with a file for directory disk tests.
        mkdir "${TEST_DIR}/incus-test-${poolName}"
        touch "${TEST_DIR}/incus-test-${poolName}/incus-test"

        # Create empty block file for block disk tests.
        truncate -s 5m "${TEST_DIR}/incus-test-${poolName}/incus-test-block"

        # Add disks
        incus config device add v1 dir1rw disk source="${TEST_DIR}/incus-test-${poolName}" path="/srv/rw"
        incus config device add v1 dir1ro disk source="${TEST_DIR}/incus-test-${poolName}" path="/srv/ro" readonly=true
        incus config device add v1 block1ro disk source="${TEST_DIR}/incus-test-${poolName}/incus-test-block" readonly=true
        incus config device add v1 block1rw disk source="${TEST_DIR}/incus-test-${poolName}/incus-test-block"
        incus start v1
        incus wait v1 agent --timeout=90 --interval=1

        echo "==> Testing VM incus-agent drive mounts"
        # Check there is only 1 mount for each directory disk and that it is mounted with the appropriate options.
        incus exec v1 -- mount | grep '/srv/rw type' -c | grep 1
        incus exec v1 -- mount | grep '/srv/ro type' -c | grep 1

        # RW disks should use virtio-fs.
        incus exec v1 -- mount | grep 'incus_dir1rw on /srv/rw type virtiofs (rw,relatime)'

        # RO disks should use virtio-fs but be mounted readonly.
        incus exec v1 -- mount | grep 'incus_dir1ro on /srv/ro type virtiofs (ro,relatime)'

        # Check UID/GID are correct.
        incus exec v1 -- stat -c '%u:%g' /srv/rw | grep '0:0'
        incus exec v1 -- stat -c '%u:%g' /srv/ro | grep '0:0'

        # Remount the readonly disk as rw inside VM and check that the disk is still readonly at the Incus layer.
        incus exec v1 -- mount -oremount,rw /srv/ro
        incus exec v1 -- mount | grep 'incus_dir1ro on /srv/ro type virtiofs (rw,relatime)'
        ! incus exec v1 -- touch /srv/ro/incus-test-ro || false
        ! incus exec v1 -- mkdir /srv/ro/incus-test-ro || false
        ! incus exec v1 -- rm /srv/ro/incus-test.txt || false
        ! incus exec v1 -- chmod 777 /srv/ro || false

        ## Mount the readonly disk as rw inside VM using 9p and check the disk is still readonly at the Incus layer.
        incus stop v1
        incus config device set v1 dir1ro io.bus=9p
        incus start v1
        incus wait v1 agent --timeout=90 --interval=1

        incus exec v1 -- umount /srv/ro
        incus exec v1 -- mkdir /srv/ro9p
        incus exec v1 -- mount -t 9p incus_dir1ro /srv/ro9p
        incus exec v1 -- mount | grep 'incus_dir1ro on /srv/ro9p type 9p (rw'
        ! incus exec v1 -- touch /srv/ro9p/incus-test-ro || false
        ! incus exec v1 -- mkdir /srv/ro9p/incus-test-ro || false
        ! incus exec v1 -- rm /srv/ro9p/incus-test.txt || false
        ! incus exec v1 -- chmod 777 /srv/ro9p || false

        # Check writable disk is writable.
        incus exec v1 -- touch /srv/rw/incus-test-rw
        stat -c '%u:%g' "${TEST_DIR}/incus-test-${poolName}/incus-test-rw" | grep "0:0"
        incus exec v1 -- rm /srv/rw/incus-test-rw
        incus exec v1 -- rm /srv/rw/incus-test

        # Check block disks are available.
        incus exec v1 -- stat -L -c "%F" /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_block1ro | grep "block special file"
        incus exec v1 -- stat -L -c "%F" /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_block1rw | grep "block special file"

        # Check the rw disk accepts writes and the ro does not.
        ! incus exec v1 -- dd if=/dev/urandom of=/dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_block1ro bs=512 count=2 || false
        incus exec v1 -- dd if=/dev/urandom of=/dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_block1rw bs=512 count=2

        # Remove temporary directory (should now be empty aside from block file).
        echo "==> Stopping VM"
        incus stop -f v1
        rm "${TEST_DIR}/incus-test-${poolName}/incus-test-block"
        rmdir "${TEST_DIR}/incus-test-${poolName}"

        echo "==> Deleting VM"
        incus delete -f v1

        # Create directory with a file for directory disk tests.
        mkdir "${TEST_DIR}/incus-test-${poolName}"

        # Create empty block file for block disk tests.
        truncate -s 5m "${TEST_DIR}/incus-test-${poolName}/incus-test-block"

        echo "==> Checking disk device hotplug support"
        incus launch images:debian/13 v1 --vm -s "${poolName}"
        incus wait v1 agent --timeout=90 --interval=1

        # Hotplug disks (hotplugged devices and their by-id links show up asynchronously)
        incus storage volume create "${poolName}" vol1 --type=block size=10MB
        incus storage volume attach "${poolName}" vol1 v1
        # shellcheck disable=2016
        incus exec v1 -- /bin/sh -c 'for i in \$(seq 30); do stat -L -c %F /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_vol1 2> /dev/null | grep -q ^block && exit 0; sleep 1; done; ls -l /dev/disk/by-id/; exit 1'
        incus storage volume detach "${poolName}" vol1 v1
        # shellcheck disable=2016
        incus exec v1 -- /bin/sh -c 'for i in \$(seq 30); do stat /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_vol1 > /dev/null 2>&1 || exit 0; sleep 1; done; ls -l /dev/disk/by-id/; exit 1'
        incus storage volume delete "${poolName}" vol1

        incus config device add v1 block1 disk source="${TEST_DIR}/incus-test-${poolName}/incus-test-block" readonly=true
        # shellcheck disable=2016
        incus exec v1 -- /bin/sh -c 'for i in \$(seq 30); do blockdev --getro /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_block1 2> /dev/null | grep -qx 1 && exit 0; sleep 1; done; ls -l /dev/disk/by-id/; exit 1'
        incus config device set v1 block1 readonly=false
        # shellcheck disable=2016
        incus exec v1 -- /bin/sh -c 'for i in \$(seq 30); do blockdev --getro /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_block1 2> /dev/null | grep -qx 0 && exit 0; sleep 1; done; ls -l /dev/disk/by-id/; exit 1'

        # Hotplugging directories is not allowed and will fail
        ! incus config device add v1 dir1 disk source="${TEST_DIR}/incus-test-${poolName}" || false

        # Hot plug cloud-init:config ISO.
        incus config device add v1 cloudinit disk source=cloud-init:config
        # shellcheck disable=2016
        incus exec v1 -- /bin/sh -c 'for i in \$(seq 30); do mount -t iso9660 -o ro /dev/disk/by-id/scsi-0QEMU_QEMU_CD-ROM_incus_cloudinit /mnt && exit 0; sleep 1; done; ls -l /dev/disk/by-id/; exit 1'
        incus exec v1 -- umount /dev/disk/by-id/scsi-0QEMU_QEMU_CD-ROM_incus_cloudinit
        incus config device remove v1 cloudinit
        # shellcheck disable=2016
        incus exec v1 -- /bin/sh -c 'for i in \$(seq 30); do stat /dev/disk/by-id/scsi-0QEMU_QEMU_CD-ROM_incus_cloudinit > /dev/null 2>&1 || exit 0; sleep 1; done; ls -l /dev/disk/by-id/; exit 1'

        # Remove temporary directory.
        echo "==> Stopping VM"
        incus stop -f v1
        rm "${TEST_DIR}/incus-test-${poolName}/incus-test-block"
        rmdir "${TEST_DIR}/incus-test-${poolName}"

        echo "==> Deleting VM"
        incus delete -f v1

        echo "==> Change volume.size on pool and create VM"
        incus storage set "${poolName}" volume.size 6GiB
        incus init images:debian/13 v1 --vm -s "${poolName}"
        incus start v1
        incus wait v1 agent --timeout=90 --interval=1
        incus info v1

        echo "==> Checking VM root disk size is 6GiB"
        [ $(($(incus exec v1 -- blockdev --getsize64 /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_root) / GiB)) -eq "6" ]

        echo "==> Deleting VM and reset pool volume.size"
        incus delete -f v1
        incus storage unset "${poolName}" volume.size

        if [ "${poolDriver}" = "lvm" ]; then
            echo "==> Change volume.block.filesystem on pool and create VM"
            incus storage set "${poolName}" volume.block.filesystem xfs
            incus init images:debian/13 v1 --vm -s "${poolName}"
            incus start v1
            incus wait v1 agent --timeout=90 --interval=1
            incus info v1

            echo "==> Checking VM config disk filesystem is XFS"
            stat -f -c %T "${INCUS_DIR}/virtual-machines/v1" | grep xfs

            echo "==> Deleting VM"
            incus delete -f v1
            incus storage unset "${poolName}" volume.block.filesystem
        fi

        echo "==> Create VM from profile with small disk size"
        incus profile copy default vmsmall
        incus profile device remove vmsmall root
        incus profile device add vmsmall root disk pool="${poolName}" path=/ size=7GiB
        incus init images:debian/13 v1 --vm -p vmsmall
        incus start v1
        incus wait v1 agent --timeout=90 --interval=1
        incus info v1

        echo "==> Checking VM root disk size is 7GiB"
        [ $(($(incus exec v1 -- blockdev --getsize64 /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_root) / GiB)) -eq "7" ]
        incus stop -f v1

        echo "==> Copy to different storage pool with same driver and check size"
        if [ "${poolDriver}" = "dir" ] || [ "${poolDriver}" = "ceph" ]; then
            incus storage create "${poolName}2" "${poolDriver}"
        elif [ "${poolDriver}" = "linstor" ]; then
            incus storage create "${poolName}2" "${poolDriver}" linstor.resource_group.place_count=1
        elif [ "${poolDriver}" = "lvm" ]; then
            incus storage create "${poolName}2" "${poolDriver}" size=40GiB lvm.use_thinpool=false
        elif [ "${poolDriver}" = "lvm-thin" ]; then
            incus storage create "${poolName}2" lvm size=20GiB
        else
            incus storage create "${poolName}2" "${poolDriver}" size=20GiB
        fi

        incus copy v1 v2 -s "${poolName}2"
        incus start v2
        incus wait v2 agent --timeout=90 --interval=1
        incus info v2

        echo "==> Checking copied VM root disk size is 7GiB"
        [ $(($(incus exec v2 -- blockdev --getsize64 /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_root) / GiB)) -eq "7" ]
        incus delete -f v2
        incus storage delete "${poolName}2"

        echo "==> Copy to different storage pool with different driver and check size"
        dstPoolDriver=zfs # Use ZFS storage pool as that has fixed volumes not files.
        if [ "${poolDriver}" = "zfs" ]; then
            dstPoolDriver=lvm # Use something different when testing ZFS.
        fi

        incus storage create "${poolName}2" "${dstPoolDriver}" size=20GiB
        incus copy v1 v2 -s "${poolName}2"
        incus start v2
        incus wait v2 agent --timeout=90 --interval=1
        incus info v2

        echo "==> Checking copied VM root disk size is 7GiB"
        [ $(($(incus exec v2 -- blockdev --getsize64 /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_root) / GiB)) -eq "7" ]
        incus delete -f v2

        echo "==> Grow above default volume size and copy to different storage pool"
        incus config device override v1 root size=11GiB
        incus copy v1 v2 -s "${poolName}2"
        incus start v2
        incus wait v2 agent --timeout=90 --interval=1
        incus info v2

        echo "==> Checking copied VM root disk size is 11GiB"
        [ $(($(incus exec v2 -- blockdev --getsize64 /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_root) / GiB)) -eq "11" ]
        incus delete -f v2
        incus storage delete "${poolName}2"

        echo "==> Publishing larger VM"
        incus start v1 # Start to ensure cloud-init grows filesystem before publish.
        incus wait v1 agent --timeout=90 --interval=1
        incus info v1
        incus stop -f v1
        incus publish v1 --alias vmbig
        incus delete -f v1
        incus storage set "${poolName}" volume.size 9GiB

        echo "==> Check VM create fails when image larger than volume.size"
        ! incus init vmbig v1 --vm -s "${poolName}" || false

        echo "==> Check VM create succeeds when no volume.size set"
        incus storage unset "${poolName}" volume.size
        incus init vmbig v1 --vm -s "${poolName}"
        incus start v1
        incus wait v1 agent --timeout=90 --interval=1
        incus info v1

        echo "==> Checking new VM root disk size is 11GiB"
        [ $(($(incus exec v1 -- blockdev --getsize64 /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_root) / GiB)) -eq "11" ]

        echo "===> Renaming VM"
        incus stop -f v1
        incus rename v1 v1renamed

        echo "==> Deleting VM, vmbig image and vmsmall profile"
        incus delete -f v1renamed
        incus image delete vmbig
        incus profile delete vmsmall

        echo "==> Checking VM Generation UUID with QEMU"
        incus init images:debian/13 v1 --vm -s "${poolName}"
        incus start v1
        incus wait v1 agent --timeout=90 --interval=1
        incus info v1

        # Check that the volatile.uuid.generation setting is applied to the QEMU process.
        vmGenID=$(incus config get v1 volatile.uuid.generation)
        qemuGenID=$(awk '/driver = "vmgenid"/,/guid = / {print $3}' "${INCUS_DIR}/run/v1/qemu.conf" | sed -n 's/"\([0-9a-fA-F]\{8\}-[0-9a-fA-F]\{4\}-[0-9a-fA-F]\{4\}-[0-9a-fA-F]\{4\}-[0-9a-fA-F]\{12\}\)"/\1/p')
        if [ "${vmGenID}" != "${qemuGenID}" ]; then
            echo "==> VM Generation ID in Incus config does not match VM Generation ID in QEMU process"
            false
        fi

        incus delete -f v1

        echo "==> Deleting storage pool"
        incus storage delete "${poolName}"
    done

    echo "==> Restoring profile and deleting network"
    incus profile device remove default eth0
    incus profile device add default eth0 nic nictype=p2p name=eth0
    incus network delete incusbr0
}
