test_storage_volume_rebuild() {
    ensure_import_testimage
    ensure_has_localhost_remote "${INCUS_ADDR}"

    pool="incustest-$(basename "${INCUS_DIR}")"

    # Create a custom filesystem volume with some config and content.
    incus storage volume create "${pool}" vol1
    incus storage volume set "${pool}" vol1 user.foo=bar
    echo "data" | incus storage volume file push - "${pool}" vol1/testfile
    [ "$(incus storage volume file pull "${pool}" vol1/testfile -)" = "data" ]

    # Rebuilding wipes the content but keeps the volume and its config.
    incus storage volume rebuild "${pool}" vol1
    incus storage volume get "${pool}" vol1 user.foo | grep -Fx "bar"
    ! incus storage volume file pull "${pool}" vol1/testfile - || false

    # The explicit "custom/" type prefix is also accepted.
    echo "data" | incus storage volume file push - "${pool}" vol1/testfile
    incus storage volume rebuild "${pool}" custom/vol1
    ! incus storage volume file pull "${pool}" vol1/testfile - || false

    # Only custom volumes can be rebuilt.
    ! incus storage volume rebuild "${pool}" container/vol1 || false

    # A volume with snapshots cannot be rebuilt.
    incus storage volume snapshot create "${pool}" vol1
    ! incus storage volume rebuild "${pool}" vol1 || false
    incus storage volume snapshot delete "${pool}" vol1 snap0
    incus storage volume rebuild "${pool}" vol1

    # A volume used by a running instance cannot be rebuilt.
    incus launch testimage c1
    incus storage volume attach "${pool}" vol1 c1 vol1 /mnt/vol1
    ! incus storage volume rebuild "${pool}" vol1 || false

    # Stopping the instance allows the rebuild to proceed.
    incus stop -f c1
    incus storage volume rebuild "${pool}" vol1
    incus delete -f c1

    incus storage volume delete "${pool}" vol1
}
