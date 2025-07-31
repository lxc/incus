test_storage_volume_initial_config() {

    incus_backend=$(storage_backend "$INCUS_DIR")
    if [ "${incus_backend}" != "zfs" ] && [ "${incus_backend}" != "lvm" ] && [ "${incus_backend}" != "ceph" ]; then
        return
    fi

    ensure_import_testimage

    image="testimage"
    profile="profile-initial-values"
    pool=$(incus profile device get default root pool)

    if [ "$incus_backend" = "zfs" ] || [ "$incus_backend" = "lvm" ]; then
        pool="storage-initial-values"
        incus storage create "${pool}" "${incus_backend}" size=512MiB
    fi

    if [ "$incus_backend" = "zfs" ]; then
        incus storage set "${pool}" volume.zfs.block_mode=true
    fi

    incus profile create "${profile}"
    incus profile device add "${profile}" root disk path=/ pool="${pool}"

    incus storage set "${pool}" volume.size=128MiB
    incus storage set "${pool}" volume.block.filesystem=ext4

    # Test default configuration (without initial configuration).
    incus init "${image}" c --profile "${profile}"
    [ "$(incus storage volume get "${pool}" container/c block.filesystem)" = "ext4" ]
    incus rm c

    incus init c --empty --profile "${profile}"
    [ "$(incus storage volume get "${pool}" container/c block.filesystem)" = "ext4" ]
    incus rm c

    # Test profile initial configuration.
    incus profile device set "${profile}" root initial.block.filesystem=btrfs

    incus init "${image}" c --profile "${profile}"
    [ "$(incus storage volume get "${pool}" container/c block.filesystem)" = "btrfs" ]
    incus rm c

    incus init c --empty --profile "${profile}"
    [ "$(incus storage volume get "${pool}" container/c block.filesystem)" = "btrfs" ]
    incus rm c

    # Test instance initial configuration.
    incus init "${image}" c -s "${pool}" --no-profiles --device root,initial.block.filesystem=btrfs
    [ "$(incus storage volume get "${pool}" container/c block.filesystem)" = "btrfs" ]
    incus rm c

    incus init c --empty -s "${pool}" --no-profiles --device root,initial.block.filesystem=btrfs
    [ "$(incus storage volume get "${pool}" container/c block.filesystem)" = "btrfs" ]

    # Verify instance initial.* configuration modification.
    ! incus config device set c root initial.block.mount_options=noatime || false # NOK: Add new configuration.
    ! incus config device set c root initial.block.filesystem=xfs || false        # NOK: Modify existing configuration.
    incus config device set c root initial.block.filesystem=btrfs                 # OK:  No change.
    incus config device unset c root initial.block.filesystem                     # OK:  Remove existing configuration.
    incus rm c

    if [ "$incus_backend" = "zfs" ]; then
        # Clear profile and storage options.
        incus storage unset "${pool}" volume.block.filesystem
        incus storage unset "${pool}" volume.zfs.block_mode
        incus profile device unset "${profile}" root initial.block.filesystem

        # > Verify zfs.block_mode without initial configuration.

        # Verify "zfs.block_mode=true" is applied from pool configuration.
        incus storage set "${pool}" volume.zfs.block_mode=true

        incus init c --empty --profile "${profile}"
        [ "$(incus storage volume get "${pool}" container/c zfs.block_mode)" = "true" ]
        incus delete c --force

        # Verify "zfs.block_mode=false" is applied from pool configuration.
        incus storage set "${pool}" volume.zfs.block_mode=false

        incus init c --empty --profile "${profile}"
        [ "$(incus storage volume get "${pool}" container/c zfs.block_mode)" = "false" ]
        incus delete c --force

        # > Overwrite zfs.block_mode with initial configuration in profile.

        # Verify instance "initial.zfs.block_mode=true" configuration is applied.
        incus storage set "${pool}" volume.zfs.block_mode=false
        incus profile device set "${profile}" root initial.zfs.block_mode=true

        incus init c --empty --profile "${profile}"
        [ "$(incus storage volume get "${pool}" container/c zfs.block_mode)" = "true" ]
        incus delete c --force

        # Verify profile "initial.zfs.block_mode=false" configuration is applied.
        incus storage set "${pool}" volume.zfs.block_mode=true
        incus profile device set "${profile}" root initial.zfs.block_mode=false

        incus init c --empty --profile "${profile}"
        [ "$(incus storage volume get "${pool}" container/c zfs.block_mode)" = "false" ]
        incus delete c --force

        # > Verify instance overwrite of initial.* configuration.

        # Verify instance "initial.zfs.block_mode=true" configuration is applied.
        incus storage set "${pool}" volume.zfs.block_mode=false
        incus profile device set "${profile}" root initial.zfs.block_mode=false

        incus init c --empty --profile "${profile}" --device root,initial.zfs.block_mode=true
        [ "$(incus storage volume get "${pool}" container/c zfs.block_mode)" = "true" ]
        incus delete c --force

        # Verify instance "initial.zfs.block_mode=false" configuration is applied.
        incus storage set "${pool}" volume.zfs.block_mode=true
        incus profile device set "${profile}" root initial.zfs.block_mode=true

        incus init c --empty --profile "${profile}" --device root,initial.zfs.block_mode=false
        [ "$(incus storage volume get "${pool}" container/c zfs.block_mode)" = "false" ]
        incus delete c --force

        # > Verify initial.zfs.blocksize configuration.

        # Custom blocksize.
        incus init "${image}" c --no-profiles --storage "${pool}" --device root,initial.zfs.blocksize=64KiB
        [ "$(incus storage volume get "${pool}" container/c zfs.blocksize)" = "64KiB" ]
        [ "$(zfs get volblocksize "${pool}/containers/c" -H -o value)" = "64K" ]
        incus delete c --force

        # Custom blocksize that exceeds maximum allowed blocksize.
        incus init "${image}" c --no-profiles --storage "${pool}" --device root,initial.zfs.blocksize=512KiB
        [ "$(incus storage volume get "${pool}" container/c zfs.blocksize)" = "512KiB" ]
        [ "$(zfs get volblocksize "${pool}/containers/c" -H -o value)" = "128K" ]
        incus delete c --force
    fi

    # Test initial owner of a custom volume configuration options.
    incus storage volume create "${pool}" testvolume1
    incus storage volume create "${pool}" testvolume2 initial.uid=101 initial.gid=101 initial.mode=0700

    incus launch testimage c

    incus storage volume attach "${pool}" testvolume1 c /testvolume1
    incus storage volume attach "${pool}" testvolume2 c /testvolume2

    [ "$(incus exec c -- stat -c %u:%g /testvolume1)" = "0:0" ]
    [ "$(incus exec c -- stat -c %a /testvolume1)" = "711" ]
    [ "$(incus exec c -- stat -c %u:%g /testvolume2)" = "101:101" ]
    [ "$(incus exec c -- stat -c %a /testvolume2)" = "700" ]

    incus storage volume detach "${pool}" testvolume1 c
    incus storage volume detach "${pool}" testvolume2 c

    incus delete c --force

    incus storage volume delete "${pool}" testvolume1
    incus storage volume delete "${pool}" testvolume2

    # Cleanup
    incus profile delete "${profile}"

    if [ "$incus_backend" = "zfs" ] || [ "$incus_backend" = "lvm" ]; then
        incus storage delete "${pool}"
    fi
}
