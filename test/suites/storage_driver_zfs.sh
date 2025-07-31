test_storage_driver_zfs() {
    do_storage_driver_zfs ext4
    do_storage_driver_zfs xfs
    do_storage_driver_zfs btrfs

    do_zfs_cross_pool_copy
    do_zfs_delegate

    do_zfs_encryption
}

do_zfs_delegate() {
    # shellcheck disable=2039,3043
    local incus_backend

    incus_backend=$(storage_backend "$INCUS_DIR")
    if [ "$incus_backend" != "zfs" ]; then
        return
    fi

    if ! zfs --help | grep -q '^\s\+zone\b'; then
        echo "==> SKIP: Skipping ZFS delegation tests due as installed version doesn't support it"
        return
    fi

    # Import image into default storage pool.
    ensure_import_testimage

    # Test enabling delegation.
    storage_pool="incustest-$(basename "${INCUS_DIR}")"

    incus init testimage c1
    incus storage volume set "${storage_pool}" container/c1 zfs.delegate=true
    incus start c1

    PID=$(incus info c1 | awk '/^PID:/ {print $2}')
    nsenter -t "${PID}" -U -- zfs list | grep -q containers/c1

    # Confirm that ZFS dataset is empty when off.
    incus stop -f c1
    incus storage volume unset "${storage_pool}" container/c1 zfs.delegate
    incus start c1

    PID=$(incus info c1 | awk '/^PID:/ {print $2}')
    ! nsenter -t "${PID}" -U -- zfs list | grep -q containers/c1

    incus delete -f c1
}

do_zfs_encryption() {
    # shellcheck disable=2039,3043
    local INCUS_STORAGE_DIR incus_backend

    incus_backend=$(storage_backend "$INCUS_DIR")
    if [ "$incus_backend" != "zfs" ]; then
        return
    fi

    if ! zfs --help | grep -q '^\s\+load-key\b'; then
        echo "==> SKIP: Skipping ZFS encryption tests as installed version doesn't support it"
        return
    fi

    INCUS_STORAGE_DIR="$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)"
    chmod +x "${INCUS_STORAGE_DIR}"
    spawn_incus "${INCUS_STORAGE_DIR}" false

    # shellcheck disable=3043
    local zpool_name zpool_keyfile zpool_vdev
    # Create a new pool. incus storage create doesn't support setting up
    # encrypted datasets, so we need to create the pool ourselves.
    configure_loop_device zpool_file zpool_vdev
    zpool_name="$(mktemp -u incus-zpool-XXXXXXXXX)"
    zpool_keyfile="$(mktemp -p "${TEST_DIR}" incus-zpool-keyfile.XXXXXXXXX)"
    echo "dummy-passphrase" > "$zpool_keyfile"

    zpool create \
        -O encryption=on \
        -O keyformat=passphrase \
        -O keylocation="file://$zpool_keyfile" \
        "$zpool_name" "$zpool_vdev"

    INCUS_DIR="${INCUS_STORAGE_DIR}" incus storage create zpool_encrypted zfs source="$zpool_name"

    # Make sure that incus sees that the pool is imported.
    zfs get -H -o name,property,value keystatus "$zpool_name" | grep -Eq "$zpool_name\s+keystatus\s+available"
    INCUS_DIR="${INCUS_STORAGE_DIR}" incus storage list -f csv -c nDs | grep -q "zpool_encrypted,zfs,CREATED"

    # Shut down Incus to force the pool to get exported.
    shutdown_incus "${INCUS_STORAGE_DIR}"

    # The pool should be exported now.
    ! zpool status "$zpool_name" 2> /dev/null || false

    # Restart Incus.
    respawn_incus "${INCUS_STORAGE_DIR}" true

    # The keys should've been re-imported automatically by Incus.
    zfs get -H -o name,property,value keystatus "$zpool_name" | grep -Eq "$zpool_name\s+keystatus\s+available"
    INCUS_DIR="${INCUS_STORAGE_DIR}" incus storage list -f csv -c nDs | grep -q "zpool_encrypted,zfs,CREATED"

    # Now we reconfigure the dataset so that the encryption key is provided via
    # an interactive prompt. Incus cannot handle this, so we should expect the
    # pool to remain unimported and the storage pool should be reported as
    # UNAVAILABLE.
    zfs set keylocation=prompt "$zpool_name"

    # Shut down Incus to force the pool to get exported.
    shutdown_incus "${INCUS_STORAGE_DIR}"

    # The pool should be exported now.
    ! zpool status "$zpool_name" 2> /dev/null || false

    # Restart Incus.
    respawn_incus "${INCUS_STORAGE_DIR}" true

    # The pool should still not be imported, and Incus should report the storage
    # pool as UNAVAILABLE.
    ! zpool status "$zpool_name" 2> /dev/null || false
    INCUS_DIR="${INCUS_STORAGE_DIR}" incus storage list -f csv -c nDs | grep -q "zpool_encrypted,zfs,UNAVAILABLE"

    INCUS_DIR="${INCUS_STORAGE_DIR}" incus storage delete zpool_encrypted

    # shellcheck disable=SC2031
    kill_incus "${INCUS_STORAGE_DIR}"
}

do_zfs_cross_pool_copy() {
    # shellcheck disable=2039,3043
    local INCUS_STORAGE_DIR incus_backend

    incus_backend=$(storage_backend "$INCUS_DIR")
    if [ "$incus_backend" != "zfs" ]; then
        return
    fi

    INCUS_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
    chmod +x "${INCUS_STORAGE_DIR}"
    spawn_incus "${INCUS_STORAGE_DIR}" false

    # Import image into default storage pool.
    ensure_import_testimage

    incus storage create incustest-"$(basename "${INCUS_DIR}")"-dir dir

    incus init testimage c1 -s incustest-"$(basename "${INCUS_DIR}")"-dir
    incus copy c1 c2 -s incustest-"$(basename "${INCUS_DIR}")"

    # Check created zfs volume
    [ "$(zfs get -H -o value type incustest-"$(basename "${INCUS_DIR}")/containers/c2")" = "filesystem" ]

    # Turn on block mode
    incus storage set incustest-"$(basename "${INCUS_DIR}")" volume.zfs.block_mode true

    incus copy c1 c3 -s incustest-"$(basename "${INCUS_DIR}")"

    # Check created zfs volume
    [ "$(zfs get -H -o value type incustest-"$(basename "${INCUS_DIR}")/containers/c3")" = "volume" ]

    # Turn off block mode
    incus storage unset incustest-"$(basename "${INCUS_DIR}")" volume.zfs.block_mode

    incus storage create incustest-"$(basename "${INCUS_DIR}")"-zfs zfs

    incus init testimage c4 -s incustest-"$(basename "${INCUS_DIR}")"-zfs
    incus copy c4 c5 -s incustest-"$(basename "${INCUS_DIR}")"

    # Check created zfs volume
    [ "$(zfs get -H -o value type incustest-"$(basename "${INCUS_DIR}")/containers/c5")" = "filesystem" ]

    # Turn on block mode
    incus storage set incustest-"$(basename "${INCUS_DIR}")" volume.zfs.block_mode true

    # Although block mode is turned on on the target storage pool, c6 will be created as a dataset.
    # That is because of optimized transfer which doesn't change the volume type.
    incus copy c4 c6 -s incustest-"$(basename "${INCUS_DIR}")"

    # Check created zfs volume
    [ "$(zfs get -H -o value type incustest-"$(basename "${INCUS_DIR}")/containers/c6")" = "filesystem" ]

    # Turn off block mode
    incus storage unset incustest-"$(basename "${INCUS_DIR}")" volume.zfs.block_mode

    # Clean up
    incus rm -f c1 c2 c3 c4 c5 c6
    incus storage rm incustest-"$(basename "${INCUS_DIR}")"-dir
    incus storage rm incustest-"$(basename "${INCUS_DIR}")"-zfs

    # shellcheck disable=SC2031
    kill_incus "${INCUS_STORAGE_DIR}"
}

do_storage_driver_zfs() {
    filesystem="$1"

    if ! command -v "mkfs.${filesystem}" > /dev/null 2>&1; then
        echo "==> SKIP: Skipping block mode test on ${filesystem} due to missing tools."
        return
    fi

    # shellcheck disable=2039,3043
    local INCUS_STORAGE_DIR incus_backend

    incus_backend=$(storage_backend "$INCUS_DIR")
    if [ "$incus_backend" != "zfs" ]; then
        return
    fi

    INCUS_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
    chmod +x "${INCUS_STORAGE_DIR}"
    spawn_incus "${INCUS_STORAGE_DIR}" false

    # Import image into default storage pool.
    ensure_import_testimage

    fingerprint=$(incus image info testimage | awk '/^Fingerprint/ {print $2}')

    # Create non-block container
    incus launch testimage c1

    # Check created container and image volumes
    zfs list incustest-"$(basename "${INCUS_DIR}")/containers/c1"
    zfs list incustest-"$(basename "${INCUS_DIR}")/images/${fingerprint}"
    zfs list incustest-"$(basename "${INCUS_DIR}")/images/${fingerprint}@readonly"
    [ "$(zfs get -H -o value type incustest-"$(basename "${INCUS_DIR}")/containers/c1")" = "filesystem" ]
    [ "$(zfs get -H -o value type incustest-"$(basename "${INCUS_DIR}")/images/${fingerprint}")" = "filesystem" ]

    # Turn on block mode
    incus storage set incustest-"$(basename "${INCUS_DIR}")" volume.zfs.block_mode true

    # Set block filesystem
    incus storage set incustest-"$(basename "${INCUS_DIR}")" volume.block.filesystem "${filesystem}"

    # Create container in block mode and check online grow.
    incus launch testimage c2
    incus config device override c2 root size=11GiB

    # Check created zfs volumes
    zfs list incustest-"$(basename "${INCUS_DIR}")/containers/c2"
    zfs list incustest-"$(basename "${INCUS_DIR}")/images/${fingerprint}_${filesystem}"
    zfs list incustest-"$(basename "${INCUS_DIR}")/images/${fingerprint}_${filesystem}@readonly"
    [ "$(zfs get -H -o value type incustest-"$(basename "${INCUS_DIR}")/containers/c2")" = "volume" ]

    # Create container in block mode with smaller size override.
    incus init testimage c3 -d root,size=5GiB
    incus delete -f c3

    # Delete image volume
    incus storage volume rm incustest-"$(basename "${INCUS_DIR}")" image/"${fingerprint}"

    zfs list incustest-"$(basename "${INCUS_DIR}")/deleted/images/${fingerprint}_${filesystem}"
    zfs list incustest-"$(basename "${INCUS_DIR}")/deleted/images/${fingerprint}_${filesystem}@readonly"

    incus storage unset incustest-"$(basename "${INCUS_DIR}")" volume.zfs.block_mode

    # Create non-block mode instance
    incus launch testimage c6
    zfs list incustest-"$(basename "${INCUS_DIR}")/images/${fingerprint}"
    [ "$(zfs get -H -o value type incustest-"$(basename "${INCUS_DIR}")/containers/c6")" = "filesystem" ]

    incus storage set incustest-"$(basename "${INCUS_DIR}")" volume.zfs.block_mode true

    # Create block mode instance
    incus launch testimage c7

    # Check created zfs volumes
    zfs list incustest-"$(basename "${INCUS_DIR}")/containers/c7"
    zfs list incustest-"$(basename "${INCUS_DIR}")/images/${fingerprint}_${filesystem}"
    zfs list incustest-"$(basename "${INCUS_DIR}")/images/${fingerprint}_${filesystem}@readonly"
    [ "$(zfs get -H -o value type incustest-"$(basename "${INCUS_DIR}")/containers/c7")" = "volume" ]

    incus stop -f c1 c2

    # Try renaming instance
    incus rename c2 c3

    # Create snapshot
    incus snapshot create c3 snap0
    [ "$(zfs get -H -o value type incustest-"$(basename "${INCUS_DIR}")/containers/c3@snapshot-snap0")" = "snapshot" ]

    # This should create c11 as a dataset, and c21 as a zvol
    incus copy c1 c11
    [ "$(zfs get -H -o value type incustest-"$(basename "${INCUS_DIR}")/containers/c11")" = "filesystem" ]

    incus copy c3 c21
    [ "$(zfs get -H -o value type incustest-"$(basename "${INCUS_DIR}")/containers/c21")" = "volume" ]
    [ "$(zfs get -H -o value type incustest-"$(basename "${INCUS_DIR}")/containers/c21@snapshot-snap0")" = "snapshot" ]

    # Create storage volumes
    incus storage volume create incustest-"$(basename "${INCUS_DIR}")" vol1
    [ "$(zfs get -H -o value type incustest-"$(basename "${INCUS_DIR}")/custom/default_vol1")" = "volume" ]
    [ "$(zfs get -H -o value incus:content_type incustest-"$(basename "${INCUS_DIR}")/custom/default_vol1")" = "filesystem" ]

    incus storage volume create incustest-"$(basename "${INCUS_DIR}")" --type=block vol2
    [ "$(zfs get -H -o value type incustest-"$(basename "${INCUS_DIR}")/custom/default_vol2")" = "volume" ]
    [ "$(zfs get -H -o value incus:content_type incustest-"$(basename "${INCUS_DIR}")/custom/default_vol2")" = "block" ]

    # verify incus:content_type is not lost when cloning
    incus storage volume copy incustest-"$(basename "${INCUS_DIR}")"/vol1 incustest-"$(basename "${INCUS_DIR}")"/vol1-clone
    incus storage volume copy incustest-"$(basename "${INCUS_DIR}")"/vol2 incustest-"$(basename "${INCUS_DIR}")"/vol2-clone
    [ "$(zfs get -H -o value incus:content_type incustest-"$(basename "${INCUS_DIR}")/custom/default_vol1-clone")" = "filesystem" ]
    [ "$(zfs get -H -o value incus:content_type incustest-"$(basename "${INCUS_DIR}")/custom/default_vol2-clone")" = "block" ]

    incus storage volume create incustest-"$(basename "${INCUS_DIR}")" vol3 zfs.block_mode=false
    [ "$(zfs get -H -o value type incustest-"$(basename "${INCUS_DIR}")/custom/default_vol3")" = "filesystem" ]

    incus storage volume attach incustest-"$(basename "${INCUS_DIR}")" vol1 c1 /mnt
    incus storage volume attach incustest-"$(basename "${INCUS_DIR}")" vol1 c3 /mnt
    incus storage volume attach incustest-"$(basename "${INCUS_DIR}")" vol1 c21 /mnt

    incus start c1
    incus start c3
    incus start c21

    incus exec c3 -- touch /mnt/foo
    incus exec c21 -- ls /mnt/foo
    incus exec c1 -- ls /mnt/foo

    incus storage volume detach incustest-"$(basename "${INCUS_DIR}")" vol1 c1
    incus storage volume detach incustest-"$(basename "${INCUS_DIR}")" vol1 c3
    incus storage volume detach incustest-"$(basename "${INCUS_DIR}")" vol1 c21

    ! incus exec c3 -- ls /mnt/foo || false
    ! incus exec c21 -- ls /mnt/foo || false

    # Backup and import
    incus launch testimage c4
    [ "$(zfs get -H -o value type incustest-"$(basename "${INCUS_DIR}")/containers/c4")" = "volume" ]
    incus exec c4 -- touch /root/foo
    incus stop -f c4
    incus snapshot create c4 snap0
    incus export c4 "${INCUS_DIR}/c4.tar.gz"
    incus rm -f c4

    incus import "${INCUS_DIR}/c4.tar.gz" c4
    [ "$(zfs get -H -o value type incustest-"$(basename "${INCUS_DIR}")/containers/c4")" = "volume" ]
    [ "$(zfs get -H -o value type incustest-"$(basename "${INCUS_DIR}")/containers/c4@snapshot-snap0")" = "snapshot" ]
    incus start c4
    incus exec c4 -- test -f /root/foo

    # Snapshot and restore
    incus snapshot create c4 snap1
    incus exec c4 -- touch /root/bar
    incus stop -f c4
    incus snapshot restore c4 snap1
    incus start c4
    incus exec c4 -- test -f /root/foo
    ! incus exec c4 -- test -f /root/bar || false

    incus storage set incustest-"$(basename "${INCUS_DIR}")" volume.size=5GiB
    incus launch testimage c5
    incus storage unset incustest-"$(basename "${INCUS_DIR}")" volume.size

    # Clean up
    incus rm -f c1 c3 c11 c21 c4 c5 c6 c7
    incus storage volume rm incustest-"$(basename "${INCUS_DIR}")" vol1
    incus storage volume rm incustest-"$(basename "${INCUS_DIR}")" vol1-clone
    incus storage volume rm incustest-"$(basename "${INCUS_DIR}")" vol2
    incus storage volume rm incustest-"$(basename "${INCUS_DIR}")" vol2-clone
    incus storage volume rm incustest-"$(basename "${INCUS_DIR}")" vol3

    # Turn off block mode
    incus storage unset incustest-"$(basename "${INCUS_DIR}")" volume.zfs.block_mode

    # Regular (no block mode) custom storage block volumes shouldn't be allowed to set block.*.
    ! incus storage create incustest-"$(basename "${INCUS_DIR}")" block.filesystem=ext4 || false
    ! incus storage create incustest-"$(basename "${INCUS_DIR}")" block.mount_options=rw || false

    # shellcheck disable=SC2031
    kill_incus "${INCUS_STORAGE_DIR}"
}
