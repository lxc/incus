test_storage_local_volume_handling() {
    ensure_import_testimage

    # shellcheck disable=2039,3043
    local INCUS_STORAGE_DIR incus_backend
    incus_backend=$(storage_backend "$INCUS_DIR")
    INCUS_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
    chmod +x "${INCUS_STORAGE_DIR}"
    spawn_incus "${INCUS_STORAGE_DIR}" false

    ensure_import_testimage

    (
        set -e
        # shellcheck disable=2030
        INCUS_DIR="${INCUS_STORAGE_DIR}"
        pool_base="incustest-$(basename "${INCUS_DIR}")"

        if storage_backend_available "btrfs"; then
            incus storage create "${pool_base}-btrfs" btrfs size=1GiB
        fi

        if storage_backend_available "ceph"; then
            incus storage create "${pool_base}-ceph" ceph volume.size=25MiB ceph.osd.pg_num=16
            if [ -n "${INCUS_CEPH_CEPHFS:-}" ]; then
                incus storage create "${pool_base}-cephfs" cephfs source="${INCUS_CEPH_CEPHFS}/$(basename "${INCUS_DIR}")-cephfs"
            fi
        fi

        if storage_backend_available "truenas"; then
            incus storage create "${pool_base}-truenas" truenas "$(truenas_source)/$(uuidgen)" "$(truenas_config)" "$(truenas_allow_insecure)" "$(truenas_api_key)"
        fi

        incus storage create "${pool_base}-dir" dir

        if storage_backend_available "lvm"; then
            incus storage create "${pool_base}-lvm" lvm volume.size=25MiB
        fi

        if storage_backend_available "zfs"; then
            incus storage create "${pool_base}-zfs" zfs size=1GiB
        fi

        if storage_backend_available "linstor"; then
            if [ -n "${INCUS_LINSTOR_LOCAL_SATELLITE:-}" ]; then
                incus config set storage.linstor.satellite.name "${INCUS_LINSTOR_LOCAL_SATELLITE}"
            fi

            incus storage create "${pool_base}-linstor" linstor volume.size=1GiB linstor.resource_group.place_count=1
        fi

        # Test all combinations of our storage drivers

        driver="${incus_backend}"
        pool="${pool_base}-${driver}"
        project="${pool_base}-project"
        pool_opts=

        if [ "$driver" = "btrfs" ] || [ "$driver" = "zfs" ]; then
            pool_opts="size=1GiB"
        fi

        if [ "$driver" = "ceph" ]; then
            pool_opts="volume.size=25MiB ceph.osd.pg_num=16"
        fi

        if [ "$driver" = "truenas" ]; then
            pool_opts="$(truenas_source)/$(uuidgen) $(truenas_config) $(truenas_allow_insecure) $(truenas_api_key)"
        fi

        if [ "$driver" = "lvm" ]; then
            pool_opts="volume.size=25MiB"
        fi

        if [ "$driver" = "linstor" ]; then
            pool_opts="volume.size=1GiB linstor.resource_group.place_count=1 linstor.volume.prefix=incus-volume2-"
        fi

        if [ -n "${pool_opts}" ]; then
            # shellcheck disable=SC2086
            incus storage create "${pool}1" "${driver}" $pool_opts
        else
            incus storage create "${pool}1" "${driver}"
        fi

        incus storage volume create "${pool}" vol1
        incus storage volume set "${pool}" vol1 user.foo=snap0
        incus storage volume set "${pool}" vol1 snapshots.expiry=1H

        # This will create the snapshot vol1/snap0
        incus storage volume snapshot create "${pool}" vol1

        # This will create the snapshot vol1/snap1
        incus storage volume set "${pool}" vol1 user.foo=snap1
        incus storage volume snapshot create "${pool}" vol1
        incus storage volume set "${pool}" vol1 user.foo=postsnap1

        # Copy volume with snapshots in same pool
        incus storage volume copy "${pool}/vol1" "${pool}/vol1copy"

        # Ensure the target snapshots are there
        incus storage volume snapshot show "${pool}" vol1copy/snap0
        incus storage volume snapshot show "${pool}" vol1copy/snap1

        # Check snapshot volume config was copied
        incus storage volume get "${pool}" vol1copy user.foo | grep -Fx "postsnap1"
        incus storage volume get "${pool}" vol1copy/snap0 user.foo | grep -Fx "snap0"
        incus storage volume get "${pool}" vol1copy/snap1 user.foo | grep -Fx "snap1"
        incus storage volume delete "${pool}" vol1copy

        # Copy volume with snapshots in different pool
        incus storage volume copy "${pool}/vol1" "${pool}1/vol1"

        # Ensure the target snapshots are there
        incus storage volume snapshot show "${pool}1" vol1/snap0
        incus storage volume snapshot show "${pool}1" vol1/snap1

        # Check snapshot volume config was copied
        incus storage volume get "${pool}1" vol1 user.foo | grep -Fx "postsnap1"
        incus storage volume get "${pool}1" vol1/snap0 user.foo | grep -Fx "snap0"
        incus storage volume get "${pool}1" vol1/snap1 user.foo | grep -Fx "snap1"

        # Copy volume only
        incus storage volume copy --volume-only "${pool}/vol1" "${pool}1/vol2"

        # Ensure the target snapshots are not there
        ! incus storage volume snapshot show "${pool}1" vol2/snap0 || false
        ! incus storage volume snapshot show "${pool}1" vol2/snap1 || false

        # Check snapshot volume config was copied
        incus storage volume get "${pool}1" vol2 user.foo | grep -Fx "postsnap1"

        # Copy snapshot to volume
        incus storage volume copy "${pool}/vol1/snap0" "${pool}1/vol3"

        # Check snapshot volume config was copied from snapshot
        incus storage volume show "${pool}1" vol3
        incus storage volume get "${pool}1" vol3 user.foo | grep -Fx "snap0"

        # Rename custom volume using `incus storage volume move`
        incus storage volume move "${pool}1/vol1" "${pool}1/vol4"
        incus storage volume move "${pool}1/vol4" "${pool}1/vol1"

        # Move volume between projects
        incus project create "${project}"
        incus storage volume move "${pool}1/vol1" "${pool}1/vol1" --project default --target-project "${project}"
        incus storage volume show "${pool}1" vol1 --project "${project}"
        incus storage volume move "${pool}1/vol1" "${pool}1/vol1" --project "${project}" --target-project default
        incus storage volume show "${pool}1" vol1 --project default

        incus project delete "${project}"
        incus storage volume delete "${pool}1" vol1
        incus storage volume delete "${pool}1" vol2
        incus storage volume delete "${pool}1" vol3
        incus storage volume move "${pool}/vol1" "${pool}1/vol1"
        ! incus storage volume show "${pool}" vol1 || false
        incus storage volume show "${pool}1" vol1
        incus storage volume delete "${pool}1" vol1
        incus storage delete "${pool}1"

        for source_driver in "btrfs" "ceph" "cephfs" "dir" "lvm" "zfs" "linstor"; do
            for target_driver in "btrfs" "ceph" "cephfs" "dir" "lvm" "zfs" "linstor"; do
                # shellcheck disable=SC2235
                if [ "$source_driver" != "$target_driver" ] &&
                    ([ "$incus_backend" = "$source_driver" ] || ([ "$incus_backend" = "ceph" ] && [ "$source_driver" = "cephfs" ] && [ -n "${INCUS_CEPH_CEPHFS:-}" ])) &&
                    storage_backend_available "$source_driver" && storage_backend_available "$target_driver"; then
                    source_pool="${pool_base}-${source_driver}"
                    target_pool="${pool_base}-${target_driver}"

                    # source_driver -> target_driver
                    incus storage volume create "${source_pool}" vol1
                    # This will create the snapshot vol1/snap0
                    incus storage volume snapshot create "${source_pool}" vol1
                    # Copy volume with snapshots
                    incus storage volume copy "${source_pool}/vol1" "${target_pool}/vol1"
                    # Ensure the target snapshot is there
                    incus storage volume snapshot show "${target_pool}" vol1/snap0
                    # Copy volume only
                    incus storage volume copy --volume-only "${source_pool}/vol1" "${target_pool}/vol2"
                    # Copy snapshot to volume
                    incus storage volume copy "${source_pool}/vol1/snap0" "${target_pool}/vol3"
                    incus storage volume delete "${target_pool}" vol1
                    incus storage volume delete "${target_pool}" vol2
                    incus storage volume delete "${target_pool}" vol3
                    incus storage volume move "${source_pool}/vol1" "${target_pool}/vol1"
                    ! incus storage volume show "${source_pool}" vol1 || false
                    incus storage volume show "${target_pool}" vol1
                    incus storage volume delete "${target_pool}" vol1

                    # target_driver -> source_driver
                    incus storage volume create "${target_pool}" vol1
                    incus storage volume copy "${target_pool}/vol1" "${source_pool}/vol1"
                    incus storage volume delete "${source_pool}" vol1

                    incus storage volume move "${target_pool}/vol1" "${source_pool}/vol1"
                    ! incus storage volume show "${target_pool}" vol1 || false
                    incus storage volume show "${source_pool}" vol1
                    incus storage volume delete "${source_pool}" vol1

                    if [ "${source_driver}" = "cephfs" ] || [ "${target_driver}" = "cephfs" ]; then
                        continue
                    fi

                    # create custom block volume without snapshots
                    incus storage volume create "${source_pool}" vol1 --type=block size=4194304
                    incus storage volume copy "${source_pool}/vol1" "${target_pool}/vol1"
                    incus storage volume show "${target_pool}" vol1 | grep -q 'content_type: block'

                    # create custom block volume with a snapshot
                    incus storage volume create "${source_pool}" vol2 --type=block size=4194304
                    incus storage volume snapshot create "${source_pool}" vol2
                    incus storage volume snapshot show "${source_pool}" vol2/snap0 | grep -q 'content_type: block'

                    # restore snapshot
                    incus storage volume snapshot restore "${source_pool}" vol2 snap0
                    incus storage volume show "${source_pool}" vol2 | grep -q 'content_type: block'

                    # copy with snapshots
                    incus storage volume copy "${source_pool}/vol2" "${target_pool}/vol2"
                    incus storage volume show "${target_pool}" vol2 | grep -q 'content_type: block'
                    incus storage volume snapshot show "${target_pool}" vol2/snap0 | grep -q 'content_type: block'

                    # copy without snapshots
                    incus storage volume copy "${source_pool}/vol2" "${target_pool}/vol3" --volume-only
                    incus storage volume show "${target_pool}" vol3 | grep -q 'content_type: block'
                    ! incus storage volume snapshot show "${target_pool}" vol3/snap0 | grep -q 'content_type: block' || false

                    # move images
                    incus storage volume move "${source_pool}/vol2" "${target_pool}/vol4"
                    ! incus storage volume show "${source_pool}" vol2 | grep -q 'content_type: block' || false
                    incus storage volume show "${target_pool}" vol4 | grep -q 'content_type: block'
                    incus storage volume snapshot show "${target_pool}" vol4/snap0 | grep -q 'content_type: block'

                    # check refreshing volumes

                    # create storage volume with user config differing over snapshots
                    incus storage volume create "${source_pool}" vol5 --type=block size=4194304
                    incus storage volume set "${source_pool}" vol5 user.foo=snap0vol5
                    incus storage volume snapshot create "${source_pool}" vol5
                    incus storage volume set "${source_pool}" vol5 user.foo=snap1vol5
                    incus storage volume snapshot create "${source_pool}" vol5
                    incus storage volume set "${source_pool}" vol5 user.foo=postsnap1vol5

                    # copy to new volume destination with refresh flag
                    incus storage volume copy --refresh "${source_pool}/vol5" "${target_pool}/vol5"

                    # check snapshot volumes (including config) were copied
                    incus storage volume get "${target_pool}" vol5 user.foo | grep -Fx "postsnap1vol5"
                    incus storage volume get "${target_pool}" vol5/snap0 user.foo | grep -Fx "snap0vol5"
                    incus storage volume get "${target_pool}" vol5/snap1 user.foo | grep -Fx "snap1vol5"

                    # create additional snapshot on source_pool and one on target_pool
                    incus storage volume snapshot create "${source_pool}" vol5 postsnap1vol5
                    incus storage volume set "${target_pool}" vol5 user.foo=snapremovevol5
                    incus storage volume snapshot create "${target_pool}" vol5 snapremove

                    # incremental copy to existing volume destination with refresh flag
                    incus storage volume copy --refresh "${source_pool}/vol5" "${target_pool}/vol5"

                    # check snapshot volumes (including config) was overridden from new source and that missing snapshot is
                    # present and that the missing snapshot has been removed.
                    # Note: We are currently diffing the snapshots by name and creation date, so in fact existing
                    # snapshots of the same name and cretion date won't be overwritten even if their config or contents is different.
                    incus storage volume get "${target_pool}" vol5 user.foo | grep -Fx "snapremovevol5"
                    incus storage volume get "${target_pool}" vol5/snap0 user.foo | grep -Fx "snap0vol5"
                    incus storage volume get "${target_pool}" vol5/snap1 user.foo | grep -Fx "snap1vol5"
                    incus storage volume get "${target_pool}" vol5/postsnap1vol5 user.foo | grep -Fx "postsnap1vol5"
                    ! incus storage volume get "${target_pool}" vol5/snapremove user.foo || false

                    # copy ISO custom volumes
                    truncate -s 25MiB foo.iso
                    incus storage volume import "${source_pool}" ./foo.iso iso1
                    incus storage volume copy "${source_pool}/iso1" "${target_pool}/iso1"
                    incus storage volume show "${target_pool}" iso1 | grep -q 'content_type: iso'
                    incus storage volume move "${source_pool}/iso1" "${target_pool}/iso2"
                    incus storage volume show "${target_pool}" iso2 | grep -q 'content_type: iso'
                    ! incus storage volume show "${source_pool}" iso1 || false

                    # clean up
                    incus storage volume delete "${source_pool}" vol1
                    incus storage volume delete "${target_pool}" vol1
                    incus storage volume delete "${target_pool}" vol2
                    incus storage volume delete "${target_pool}" vol3
                    incus storage volume delete "${target_pool}" vol4
                    incus storage volume delete "${source_pool}" vol5
                    incus storage volume delete "${target_pool}" vol5
                    incus storage volume delete "${target_pool}" iso1
                    incus storage volume delete "${target_pool}" iso2
                    rm -f foo.iso
                fi
            done
        done
    )

    # shellcheck disable=SC2031,2269
    INCUS_DIR="${INCUS_DIR}"
    kill_incus "${INCUS_STORAGE_DIR}"
}
