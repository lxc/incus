test_storage_driver_truenas() {
    do_storage_driver_truenas ext4
    do_storage_driver_truenas xfs
    do_storage_driver_truenas btrfs
}

maybe_list_datasets() {
    # full list of datasets and snaps
    call_truenas_tool list -r --no-headers "$1"
}

do_storage_driver_truenas() {
    filesystem="$1"

    if ! command -v "mkfs.${filesystem}" > /dev/null 2>&1; then
        echo "==> SKIP: Skipping TrueNAS block mode test on ${filesystem} due to missing tools."
        return
    fi

    # shellcheck disable=2039,3043
    local INCUS_STORAGE_DIR incus_backend truenas_dataset truenas_storage_pool

    incus_backend=$(storage_backend "$INCUS_DIR")
    if [ "$incus_backend" != "truenas" ]; then
        return
    fi

    INCUS_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
    chmod +x "${INCUS_STORAGE_DIR}"
    spawn_incus "${INCUS_STORAGE_DIR}" false

    truenas_storage_pool="incustest-$(basename "${INCUS_DIR}")"
    truenas_dataset=$(incus storage get "${truenas_storage_pool}" truenas.dataset)

    # block.filesystem defaults to ext4, but other tests can set this.
    incus storage unset incustest-"$(basename "${INCUS_DIR}")" volume.block.filesystem

    # Import image into default storage pool.
    ensure_import_testimage

    fingerprint=$(incus image info testimage | awk '/^Fingerprint/ {print $2}')

    # Create non-block container
    incus launch testimage c1

    maybe_list_datasets "${truenas_dataset}"

    # Check created container and image volumes
    [ "$(call_truenas_tool dataset list --no-headers -o name "${truenas_dataset}/containers/c1")" = "${truenas_dataset}/containers/c1" ]
    [ "$(call_truenas_tool dataset list --no-headers -o type "${truenas_dataset}/containers/c1")" = "volume" ]

    [ "$(call_truenas_tool dataset list --no-headers -o name "${truenas_dataset}/images/${fingerprint}_ext4")" = "${truenas_dataset}/images/${fingerprint}_ext4" ]
    [ "$(call_truenas_tool snapshot list --no-headers -o name "${truenas_dataset}/images/${fingerprint}_ext4@readonly")" = "${truenas_dataset}/images/${fingerprint}_ext4@readonly" ]

    # Set block filesystem
    incus storage set incustest-"$(basename "${INCUS_DIR}")" volume.block.filesystem "${filesystem}"

    incus launch testimage c2
    incus config device override c2 root size=11GiB

    maybe_list_datasets "${truenas_dataset}"

    # Check created truenas volumes
    [ "$(call_truenas_tool dataset list --no-headers -o name "${truenas_dataset}/containers/c2")" = "${truenas_dataset}/containers/c2" ]
    [ "$(call_truenas_tool dataset list --no-headers -o type "${truenas_dataset}/containers/c2")" = "volume" ]
    [ "$(call_truenas_tool dataset list --no-headers -o name "${truenas_dataset}/images/${fingerprint}_${filesystem}")" = "${truenas_dataset}/images/${fingerprint}_${filesystem}" ]
    [ "$(call_truenas_tool snapshot list --no-headers -o name "${truenas_dataset}/images/${fingerprint}_${filesystem}@readonly")" = "${truenas_dataset}/images/${fingerprint}_${filesystem}@readonly" ]

    # Create container in block mode with smaller size override.
    incus init testimage c3 -d root,size=5GiB
    incus delete -f c3

    # Delete image volume
    incus storage volume rm incustest-"$(basename "${INCUS_DIR}")" image/"${fingerprint}"

    maybe_list_datasets "${truenas_dataset}"

    [ "$(call_truenas_tool dataset list --no-headers -o name "${truenas_dataset}/deleted/images/${fingerprint}_${filesystem}")" = "${truenas_dataset}/deleted/images/${fingerprint}_${filesystem}" ]
    [ "$(call_truenas_tool snapshot list --no-headers -o name "${truenas_dataset}/deleted/images/${fingerprint}_${filesystem}@readonly")" = "${truenas_dataset}/deleted/images/${fingerprint}_${filesystem}@readonly" ]

    incus stop -f c1 c2

    # Try renaming instance
    incus rename c2 c3

    # Create snapshot
    incus snapshot create c3 snap0

    maybe_list_datasets "${truenas_dataset}"

    [ "$(call_truenas_tool list --no-headers -o type "${truenas_dataset}/containers/c3@snapshot-snap0")" = "snapshot" ]

    # This should create c11 and c21 as zvols, but c11 is cloned, and c21 is replicated
    incus copy c1 c11
    [ "$(call_truenas_tool dataset list --no-headers -o type "${truenas_dataset}/containers/c11")" = "volume" ]

    maybe_list_datasets "${truenas_dataset}"

    incus copy c3 c21
    [ "$(call_truenas_tool dataset list --no-headers -o type "${truenas_dataset}/containers/c21")" = "volume" ]
    [ "$(call_truenas_tool list --no-headers -o type "${truenas_dataset}/containers/c21@snapshot-snap0")" = "snapshot" ]

    maybe_list_datasets "${truenas_dataset}"

    # Create storage volumes
    incus storage volume create incustest-"$(basename "${INCUS_DIR}")" vol1
    [ "$(call_truenas_tool dataset list --no-headers -o type "${truenas_dataset}/custom/default_vol1")" = "volume" ]
    [ "$(call_truenas_tool dataset list --no-headers -o incus:content_type "${truenas_dataset}/custom/default_vol1")" = "filesystem" ]

    maybe_list_datasets "${truenas_dataset}"

    incus storage volume copy incustest-"$(basename "${INCUS_DIR}")"/vol1 incustest-"$(basename "${INCUS_DIR}")"/vol1-clone
    [ "$(call_truenas_tool dataset list --no-headers -o incus:content_type "${truenas_dataset}/custom/default_vol1")" = "filesystem" ]

    maybe_list_datasets "${truenas_dataset}"

    incus storage volume create incustest-"$(basename "${INCUS_DIR}")" vol2 --type=block
    [ "$(call_truenas_tool dataset list --no-headers -o type "${truenas_dataset}/custom/default_vol2")" = "volume" ]
    [ "$(call_truenas_tool dataset list --no-headers -o incus:content_type "${truenas_dataset}/custom/default_vol2")" = "block" ]

    maybe_list_datasets "${truenas_dataset}"

    incus storage volume copy incustest-"$(basename "${INCUS_DIR}")"/vol2 incustest-"$(basename "${INCUS_DIR}")"/vol2-clone

    maybe_list_datasets "${truenas_dataset}"

    [ "$(call_truenas_tool dataset list --no-headers -o incus:content_type "${truenas_dataset}/custom/default_vol2")" = "block" ]

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
    [ "$(call_truenas_tool dataset list --no-headers -o type "${truenas_dataset}/containers/c4")" = "volume" ]

    incus exec c4 -- touch /root/foo
    incus stop -f c4
    incus snapshot create c4 snap0
    incus export c4 "${INCUS_DIR}/c4.tar.gz"
    incus rm -f c4

    incus import "${INCUS_DIR}/c4.tar.gz" c4
    [ "$(call_truenas_tool dataset list --no-headers -o type "${truenas_dataset}/containers/c4")" = "volume" ]

    [ "$(call_truenas_tool list --no-headers -o type "${truenas_dataset}/containers/c4@snapshot-snap0")" = "snapshot" ]

    incus start c4
    incus exec c4 -- test -f /root/foo
    rm "${INCUS_DIR}/c4.tar.gz"

    # Snapshot and restore
    incus snapshot create c4 snap1
    incus exec c4 -- touch /root/bar
    incus stop -f c4
    incus snapshot restore c4 snap1
    incus start c4
    incus exec c4 -- test -f /root/foo
    ! incus exec c4 -- test -f /root/bar || false

    [ "$(call_truenas_tool list --no-headers -o type "${truenas_dataset}/containers/c4@snapshot-snap1")" = "snapshot" ]

    incus snapshot rename c4 snap1 snap-rename

    [ "$(call_truenas_tool list --no-headers -o type "${truenas_dataset}/containers/c4@snapshot-snap-rename")" = "snapshot" ]

    incus storage set incustest-"$(basename "${INCUS_DIR}")" volume.size=5GiB
    incus launch testimage c5
    incus storage unset incustest-"$(basename "${INCUS_DIR}")" volume.size

    # restore default back to ext4, otherwise it can affect future tests.
    incus storage unset incustest-"$(basename "${INCUS_DIR}")" volume.block.filesystem

    # Clean up. TrueNAS create/delete can be very slow, this can cause the default 120s command timeout to be exceeded when doing mass instance deletes.
    # FIXME: when deletes aren't so slow, just use `incus rm -f c1 c3 c11 c21 c4 c5`
    instances="c1 c3 c11 c21 c4 c5"
    for i in $instances; do
        incus rm -f "$i"
    done

    incus storage volume rm incustest-"$(basename "${INCUS_DIR}")" vol1
    incus storage volume rm incustest-"$(basename "${INCUS_DIR}")" vol1-clone
    incus storage volume rm incustest-"$(basename "${INCUS_DIR}")" vol2
    incus storage volume rm incustest-"$(basename "${INCUS_DIR}")" vol2-clone

    maybe_list_datasets "${truenas_dataset}"

    # shellcheck disable=SC2031
    kill_incus "${INCUS_STORAGE_DIR}"
}
