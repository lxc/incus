test_dependent_volumes() {
    ensure_import_testimage

    # shellcheck disable=2039,3043
    local storage_pool storage_volume
    storage_pool="incustest-$(basename "${INCUS_DIR}")"
    storage_volume="${storage_pool}-vol1"

    incus init testimage c1
    incus storage volume create "${storage_pool}" "${storage_volume}"

    # Verify that setting a disk as dependent also marks the volume as dependent
    incus storage volume attach "${storage_pool}" "${storage_volume}" c1 vol1 path=/mnt/disk
    incus config device set c1 vol1 dependent=true
    incus storage volume get "${storage_pool}" "${storage_volume}" dependent | grep -Fx 'true'

    # Verify that removing the dependent flag from a disk also unmarks the volume as dependent
    incus config device unset c1 vol1 dependent
    ! incus storage volume get "${storage_pool}" "${storage_volume}" dependent | grep . || false
    incus storage volume detach "${storage_pool}" "${storage_volume}" c1

    # Attaching a volume with snapshots as dependent is not allowed
    incus storage volume snapshot create "${storage_pool}" "${storage_volume}" snap0
    incus storage volume attach "${storage_pool}" "${storage_volume}" c1 vol1 path=/mnt/disk
    ! incus config device set c1 vol1 dependent=true || false
    incus storage volume detach "${storage_pool}" "${storage_volume}" c1
    incus storage volume snapshot rm "${storage_pool}" "${storage_volume}" snap0

    # Create a blank snapshot on the volume if the instance already has a snapshot
    incus snapshot create c1 snap-test
    incus storage volume attach "${storage_pool}" "${storage_volume}" c1 vol1 path=/mnt/disk
    incus config device set c1 vol1 dependent=true
    [ "$(incus storage volume snapshot ls "${storage_pool}" "${storage_volume}" --format json | jq 'length == 1')" = "true" ]
    snap_name=$(incus storage volume snapshot ls "${storage_pool}" "${storage_volume}" --format json | jq -r '.[0].name')
    [ "${snap_name}" = "${storage_volume}/snap-test" ]

    # Creating snapshots on an instance creates snapshots on dependent volumes
    incus snapshot create c1 snap-test2
    [ "$(incus storage volume snapshot ls "${storage_pool}" "${storage_volume}" --format json | jq 'length == 2')" = "true" ]
    snap_name=$(incus storage volume snapshot ls "${storage_pool}" "${storage_volume}" --format json | jq -r '.[1].name')
    [ "${snap_name}" = "${storage_volume}/snap-test2" ]

    # Creating snapshots on a dependent volume is not allowed
    ! incus storage volume snapshot create "${storage_pool}" "${storage_volume}" || false

    # Deleting snapshots on a dependent volume is not allowed
    ! incus storage volume snapshot delete "${storage_pool}" "${storage_volume}" snap-test2 || false

    # Deleting an instance snapshot deletes the volume snapshot
    incus snapshot delete c1 snap-test2
    incus snapshot delete c1 snap-test
    [ "$(incus storage volume snapshot ls "${storage_pool}" "${storage_volume}" --format json | jq 'length == 0')" = "true" ]

    # Attaching a dependent volume to another instance is not allowed
    incus init testimage c2
    ! incus storage volume attach "${storage_pool}" "${storage_volume}" c2 vol1 path=/mnt/disk || false

    # Deleting an instance deletes the volume
    incus delete --force c1
    [ "$(incus storage volume ls "${storage_pool}" "${storage_volume}" --format json | jq 'length == 0')" = "true" ]

    # Cleanup
    incus delete --force c2
}
