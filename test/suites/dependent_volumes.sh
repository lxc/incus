test_dependent_volumes() {
    ensure_import_testimage

    # shellcheck disable=2039,3043
    local storage_pool storage_volume
    storage_pool="incustest-$(basename "${INCUS_DIR}")"
    storage_volume="${storage_pool}-vol1"
    storage_volume2="${storage_pool}-vol2"

    incus init testimage c1
    incus storage volume create "${storage_pool}" "${storage_volume}"

    # Verify that setting a disk as dependent also marks the volume as dependent
    incus storage volume attach "${storage_pool}" "${storage_volume}" c1 vol1 /mnt/disk
    incus config device set c1 vol1 dependent=true
    incus storage volume get "${storage_pool}" "${storage_volume}" dependent | grep -Fx 'true'

    # Verify that removing the dependent flag from a disk also unmarks the volume as dependent
    incus config device unset c1 vol1 dependent
    ! incus storage volume get "${storage_pool}" "${storage_volume}" dependent | grep . || false
    incus storage volume detach "${storage_pool}" "${storage_volume}" c1

    # Attaching a volume with snapshots as dependent is not allowed
    incus storage volume snapshot create "${storage_pool}" "${storage_volume}" snap0
    incus storage volume attach "${storage_pool}" "${storage_volume}" c1 vol1 /mnt/disk
    ! incus config device set c1 vol1 dependent=true || false
    incus storage volume detach "${storage_pool}" "${storage_volume}" c1
    incus storage volume snapshot rm "${storage_pool}" "${storage_volume}" snap0

    # Create a blank snapshot on the volume if the instance already has a snapshot
    incus snapshot create c1 snap-test
    incus storage volume attach "${storage_pool}" "${storage_volume}" c1 vol1 /mnt/disk
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
    ! incus storage volume attach "${storage_pool}" "${storage_volume}" c2 vol1 /mnt/disk || false

    # Disallow creating volumes with the 'dependent' flag
    ! incus storage volume create "${storage_pool}" "${storage_volume2}" dependent=true || false

    # Adding a volume as dependent using 'incus config device add' should also mark the volume as dependent
    incus storage volume create "${storage_pool}" "${storage_volume2}"
    incus config device add c1 vol2 disk pool="${storage_pool}" source="${storage_volume2}" dependent=true path=/extra
    incus storage volume get "${storage_pool}" "${storage_volume2}" dependent | grep -Fx 'true'

    # Snapshots on the instance and dependent volumes must survive export and import.
    incus snapshot create c1 snap-export
    [ "$(incus storage volume snapshot ls "${storage_pool}" "${storage_volume}" --format json | jq 'length == 1')" = "true" ]
    [ "$(incus storage volume snapshot ls "${storage_pool}" "${storage_volume2}" --format json | jq 'length == 1')" = "true" ]

    # Export the instance and dependent volumes.
    incus export c1 "${INCUS_DIR}/c1.tar.gz"
    incus delete -f c1

    # Import the instance from tarball.
    incus import "${INCUS_DIR}/c1.tar.gz"
    [ "$(incus query /1.0/instances/c1/snapshots | jq 'length == 1')" = "true" ]
    [ "$(incus storage volume snapshot ls "${storage_pool}" "${storage_volume}" --format json | jq 'length == 1')" = "true" ]
    [ "$(incus storage volume snapshot ls "${storage_pool}" "${storage_volume2}" --format json | jq 'length == 1')" = "true" ]
    incus storage volume get "${storage_pool}" "${storage_volume}" dependent | grep -Fx 'true'
    incus storage volume get "${storage_pool}" "${storage_volume2}" dependent | grep -Fx 'true'
    incus snapshot delete c1 snap-export

    # Detaching the volume removes the 'dependent' flag
    incus storage volume detach "${storage_pool}" "${storage_volume2}" c1
    ! incus storage volume get "${storage_pool}" "${storage_volume2}" dependent | grep . || false

    # Deleting an instance deletes the volume
    incus delete --force c1
    [ "$(incus storage volume ls "${storage_pool}" "${storage_volume}" --format json | jq 'length == 0')" = "true" ]

    # Copying from an instance snapshot takes the dependent volumes from that snapshot
    storage_volume4="${storage_pool}-vol4"
    storage_volume5="${storage_pool}-vol5"
    incus launch testimage c4
    incus storage volume create "${storage_pool}" "${storage_volume4}"
    incus config device add c4 vol4 disk pool="${storage_pool}" source="${storage_volume4}" path=/mnt dependent=true
    incus exec c4 -- touch /mnt/before
    incus snapshot create c4 snap0
    incus exec c4 -- touch /mnt/after
    incus copy c4/snap0 c5 --device "vol4,source=${storage_volume5}"
    incus start c5
    incus exec c5 -- test -e /mnt/before
    ! incus exec c5 -- test -e /mnt/after || false
    incus delete --force c4 c5

    # Dependent volumes work in a project using the default project's storage volumes
    storage_volume3="${storage_pool}-vol3"
    incus project create depvols -c features.storage.volumes=false -c features.images=false -c features.profiles=false
    incus init testimage c3 --project depvols
    incus storage volume create "${storage_pool}" "${storage_volume3}" --project depvols
    incus config device add c3 vol3 disk pool="${storage_pool}" source="${storage_volume3}" path=/mnt dependent=true --project depvols
    incus snapshot create c3 snap0 --project depvols
    [ "$(incus storage volume snapshot ls "${storage_pool}" "${storage_volume3}" --format json | jq 'length == 1')" = "true" ]
    incus delete --force c3 --project depvols
    [ "$(incus storage volume ls "${storage_pool}" "${storage_volume3}" --format json | jq 'length == 0')" = "true" ]
    incus project delete depvols

    # Copying an instance from a snapshot takes the dependent volume from the matching snapshot
    storage_volume4="${storage_pool}-vol4"
    incus init testimage c4
    incus storage volume create "${storage_pool}" "${storage_volume4}"
    incus config device add c4 vol4 disk pool="${storage_pool}" source="${storage_volume4}" path=/mnt/vol4 dependent=true
    incus start c4
    incus exec c4 -- sh -c 'echo before > /root/f; echo before > /mnt/vol4/f'
    incus snapshot create c4 snap-copy
    incus exec c4 -- sh -c 'echo after > /root/f; echo after > /mnt/vol4/f'
    incus copy c4/snap-copy c5 -d vol4,source="${storage_volume4}-copy"
    incus start c5
    [ "$(incus exec c5 -- cat /root/f)" = "before" ]
    [ "$(incus exec c5 -- cat /mnt/vol4/f)" = "before" ]

    # Refreshing from the instance keeps the copy's own volume and brings in the snapshot
    incus stop --force c5
    incus copy c4 c5 --refresh
    incus start c5
    [ "$(incus exec c5 -- cat /root/f)" = "after" ]
    [ "$(incus exec c5 -- cat /mnt/vol4/f)" = "after" ]
    incus config device get c5 vol4 source | grep -Fx "${storage_volume4}-copy"

    # The copied snapshot refers to the copied volume and restores it
    incus stop --force c5
    incus snapshot restore c5 snap-copy
    incus start c5
    [ "$(incus exec c5 -- cat /root/f)" = "before" ]
    [ "$(incus exec c5 -- cat /mnt/vol4/f)" = "before" ]
    incus delete --force c5
    [ "$(incus storage volume ls "${storage_pool}" "${storage_volume4}-copy" --format json | jq 'length == 0')" = "true" ]

    # Same when copying across pools
    incus storage create "${storage_pool}-dir" dir
    incus copy c4/snap-copy c6 -s "${storage_pool}-dir" -d vol4,pool="${storage_pool}-dir" -d vol4,source="${storage_volume4}-copy"
    incus start c6
    [ "$(incus exec c6 -- cat /root/f)" = "before" ]
    [ "$(incus exec c6 -- cat /mnt/vol4/f)" = "before" ]
    incus stop --force c6
    incus copy c4 c6 --refresh
    incus start c6
    [ "$(incus exec c6 -- cat /root/f)" = "after" ]
    [ "$(incus exec c6 -- cat /mnt/vol4/f)" = "after" ]
    incus config device get c6 vol4 pool | grep -Fx "${storage_pool}-dir"
    incus delete --force c6
    [ "$(incus storage volume ls "${storage_pool}-dir" "${storage_volume4}-copy" --format json | jq 'length == 0')" = "true" ]
    incus storage delete "${storage_pool}-dir"
    incus delete --force c4

    # Cleanup
    rm "${INCUS_DIR}/c1.tar.gz"
    incus storage volume delete "${storage_pool}" "${storage_volume2}"
    incus delete --force c2
}
