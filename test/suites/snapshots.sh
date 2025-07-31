test_snapshots() {
    snapshots

    if [ "$(storage_backend "$INCUS_DIR")" = "lvm" ]; then
        # Test that non-thinpool lvm backends work fine with snapshots.
        incus storage create "incustest-$(basename "${INCUS_DIR}")-non-thinpool-lvm-snapshots" lvm lvm.use_thinpool=false volume.size=25MiB
        incus profile device set default root pool "incustest-$(basename "${INCUS_DIR}")-non-thinpool-lvm-snapshots"

        snapshots

        incus profile device set default root pool "incustest-$(basename "${INCUS_DIR}")"

        incus storage delete "incustest-$(basename "${INCUS_DIR}")-non-thinpool-lvm-snapshots"
    fi
}

snapshots() {
    # shellcheck disable=2039,3043
    local incus_backend
    incus_backend=$(storage_backend "$INCUS_DIR")

    ensure_import_testimage
    ensure_has_localhost_remote "${INCUS_ADDR}"

    incus init testimage foo

    incus snapshot create foo
    # FIXME: make this backend agnostic
    if [ "$incus_backend" = "dir" ]; then
        [ -d "${INCUS_DIR}/containers-snapshots/foo/snap0" ]
    fi

    incus snapshot create foo
    # FIXME: make this backend agnostic
    if [ "$incus_backend" = "dir" ]; then
        [ -d "${INCUS_DIR}/containers-snapshots/foo/snap1" ]
    fi

    incus snapshot create foo tester
    # FIXME: make this backend agnostic
    if [ "$incus_backend" = "dir" ]; then
        [ -d "${INCUS_DIR}/containers-snapshots/foo/tester" ]
    fi

    incus copy foo/tester foosnap1
    # FIXME: make this backend agnostic
    if [ "$incus_backend" != "lvm" ] && [ "${incus_backend}" != "zfs" ] && [ "$incus_backend" != "ceph" ]; then
        [ -d "${INCUS_DIR}/containers/foosnap1/rootfs" ]
    fi

    incus snapshot delete foo snap0
    # FIXME: make this backend agnostic
    if [ "$incus_backend" = "dir" ]; then
        [ ! -d "${INCUS_DIR}/containers-snapshots/foo/snap0" ]
    fi

    # test deleting multiple snapshots
    incus snapshot create foo snap2
    incus snapshot create foo snap3
    incus snapshot delete foo snap2
    incus snapshot delete foo snap3
    ! incus info foo | grep -q snap2 || false
    ! incus info foo | grep -q snap3 || false

    # no CLI for this, so we use the API directly (rename a snapshot)
    wait_for "${INCUS_ADDR}" my_curl -X POST "https://${INCUS_ADDR}/1.0/instances/foo/snapshots/tester" -d "{\"name\":\"tester2\"}"
    # FIXME: make this backend agnostic
    if [ "$incus_backend" = "dir" ]; then
        [ ! -d "${INCUS_DIR}/containers-snapshots/foo/tester" ]
    fi

    incus snapshot rename foo tester2 tester-two
    incus snapshot delete foo tester-two
    # FIXME: make this backend agnostic
    if [ "$incus_backend" = "dir" ]; then
        [ ! -d "${INCUS_DIR}/containers-snapshots/foo/tester-two" ]
    fi

    incus snapshot create foo namechange
    # FIXME: make this backend agnostic
    if [ "$incus_backend" = "dir" ]; then
        [ -d "${INCUS_DIR}/containers-snapshots/foo/namechange" ]
    fi
    incus move foo foople
    [ ! -d "${INCUS_DIR}/containers/foo" ]
    [ -d "${INCUS_DIR}/containers/foople" ]
    # FIXME: make this backend agnostic
    if [ "$incus_backend" = "dir" ]; then
        [ -d "${INCUS_DIR}/containers-snapshots/foople/namechange" ]
        [ -d "${INCUS_DIR}/containers-snapshots/foople/namechange" ]
    fi

    incus delete foople
    incus delete foosnap1
    [ ! -d "${INCUS_DIR}/containers/foople" ]
    [ ! -d "${INCUS_DIR}/containers/foosnap1" ]
}

test_snap_restore() {
    snap_restore

    if [ "$(storage_backend "$INCUS_DIR")" = "lvm" ]; then
        # Test that non-thinpool lvm backends work fine with snapshots.
        incus storage create "incustest-$(basename "${INCUS_DIR}")-non-thinpool-lvm-snap-restore" lvm lvm.use_thinpool=false volume.size=25MiB
        incus profile device set default root pool "incustest-$(basename "${INCUS_DIR}")-non-thinpool-lvm-snap-restore"

        snap_restore

        incus profile device set default root pool "incustest-$(basename "${INCUS_DIR}")"

        incus storage delete "incustest-$(basename "${INCUS_DIR}")-non-thinpool-lvm-snap-restore"
    fi
}

snap_restore() {
    # shellcheck disable=2039,3043
    local incus_backend
    incus_backend=$(storage_backend "$INCUS_DIR")

    ensure_import_testimage
    ensure_has_localhost_remote "${INCUS_ADDR}"

    ##########################################################
    # PREPARATION
    ##########################################################

    ## create some state we will check for when snapshot is restored

    ## prepare snap0
    incus launch testimage bar
    echo snap0 > state
    incus file push state bar/root/state
    incus file push state bar/root/file_only_in_snap0
    incus exec bar -- mkdir /root/dir_only_in_snap0
    incus exec bar -- ln -s file_only_in_snap0 /root/statelink
    incus stop bar --force

    # Get container's pool.
    pool=$(incus config profile device get default root pool)

    incus storage volume set "${pool}" container/bar user.foo=snap0

    # Check parent volume.block.filesystem is copied to snapshot and not from pool.
    if [ "$incus_backend" = "lvm" ] || [ "$incus_backend" = "ceph" ]; then
        # Change pool volume.block.filesystem setting after creation of instance and before snapshot.
        incus storage set "${pool}" volume.block.filesystem=xfs
    fi

    incus snapshot create bar snap0

    ## prepare snap1
    incus start bar
    echo snap1 > state
    incus file push state bar/root/state
    incus file push state bar/root/file_only_in_snap1

    incus exec bar -- rmdir /root/dir_only_in_snap0
    incus exec bar -- rm /root/file_only_in_snap0
    incus exec bar -- rm /root/statelink
    incus exec bar -- ln -s file_only_in_snap1 /root/statelink
    incus exec bar -- mkdir /root/dir_only_in_snap1
    initialUUID=$(incus config get bar volatile.uuid)
    initialGenerationID=$(incus config get bar volatile.uuid.generation)
    incus stop bar --force
    incus storage volume set "${pool}" container/bar user.foo=snap1

    # Delete the state file we created to prevent leaking.
    rm state

    incus config set bar limits.cpu 1

    incus snapshot create bar snap1
    incus storage volume set "${pool}" container/bar user.foo=postsnaps

    # Check volume.block.filesystem on storage volume in parent and snapshot match.
    if [ "${incus_backend}" = "lvm" ] || [ "${incus_backend}" = "ceph" ]; then
        # Change pool volume.block.filesystem setting after creation of instance and before snapshot.
        pool=$(incus config profile device get default root pool)
        parentFS=$(incus storage volume get "${pool}" container/bar block.filesystem)
        snapFS=$(incus storage volume get "${pool}" container/bar/snap0 block.filesystem)

        if [ "${parentFS}" != "${snapFS}" ]; then
            echo "block.filesystem settings do not match in parent and snapshot"
            false
        fi

        incus storage unset "${pool}" volume.block.filesystem
    fi

    ##########################################################

    if [ "$incus_backend" != "zfs" ]; then
        # The problem here is that you can't `zfs rollback` to a snapshot with a
        # parent, which snap0 has (snap1).
        restore_and_compare_fs snap0

        # Check container config has been restored (limits.cpu is unset)
        cpus=$(incus config get bar limits.cpu)
        if [ -n "${cpus}" ]; then
            echo "==> config didn't match expected value after restore (${cpus})"
            false
        fi

        # Check storage volume has been restored (user.foo=snap0)
        incus storage volume get "${pool}" container/bar user.foo | grep -Fx "snap0"
    fi

    ##########################################################

    # test restore using full snapshot name
    restore_and_compare_fs snap1

    # Check that instances UUID remain the same before and after snapshotting
    newUUID=$(incus config get bar volatile.uuid)
    if [ "${initialUUID}" != "${newUUID}" ]; then
        echo "==> UUID of the instance should remain the same after restoring its snapshot"
        false
    fi

    # Check that the generation UUID from before changes compared to the one after snapshotting
    newGenerationID=$(incus config get bar volatile.uuid.generation)
    if [ "${initialGenerationID}" = "${newGenerationID}" ]; then
        echo "==> Generation UUID of the instance should change after restoring its snapshot"
        false
    fi

    # Check that instances UUIS remain the same before and after snapshotting  (stateful mode)
    if ! command -v criu > /dev/null 2>&1; then
        echo "==> SKIP: stateful snapshotting with CRIU (missing binary)"
    else
        initialUUID=$(incus config get bar volatile.uuid)
        initialGenerationID=$(incus config get bar volatile.uuid.generation)
        incus start bar
        incus snapshot create bar snap2 --stateful
        restore_and_compare_fs snap2

        newUUID=$(incus config get bar volatile.uuid)
        if [ "${initialUUID}" != "${newUUID}" ]; then
            echo "==> UUID of the instance should remain the same after restoring its stateful snapshot"
            false
        fi

        newGenerationID=$(incus config get bar volatile.uuid.generation)
        if [ "${initialGenerationID}" = "${newGenerationID}" ]; then
            echo "==> Generation UUID of the instance should change after restoring its stateful snapshot"
            false
        fi

        incus stop bar --force
    fi

    # Check that instances have two different UUID after a snapshot copy
    incus launch testimage bar2
    initialUUID=$(incus config get bar2 volatile.uuid)
    initialGenerationID=$(incus config get bar2 volatile.uuid.generation)
    incus copy bar2 bar3
    newUUID=$(incus config get bar3 volatile.uuid)
    newGenerationID=$(incus config get bar3 volatile.uuid.generation)

    if [ "${initialGenerationID}" = "${newGenerationID}" ] || [ "${initialUUID}" = "${newUUID}" ]; then
        echo "==> UUIDs of the instance should be different after copying snapshot into instance"
        false
    fi

    incus delete --force bar2
    incus delete --force bar3

    # Check config value in snapshot has been restored
    cpus=$(incus config get bar limits.cpu)
    if [ "${cpus}" != "1" ]; then
        echo "==> config didn't match expected value after restore (${cpus})"
        false
    fi

    # Check storage volume has been restored (user.foo=snap0)
    incus storage volume get "${pool}" container/bar user.foo | grep -Fx "snap1"

    ##########################################################

    # Start container and then restore snapshot to verify the running state after restore.
    incus start bar

    if [ "$incus_backend" != "zfs" ]; then
        # see comment above about snap0
        restore_and_compare_fs snap0

        # check container is running after restore
        incus list | grep bar | grep RUNNING
    fi

    incus stop --force bar

    incus delete bar

    # Test if container's with hyphen's in their names are treated correctly.
    incus launch testimage a-b
    incus snapshot create a-b base
    incus snapshot restore a-b base
    incus snapshot create a-b c-d
    incus snapshot restore a-b c-d
    incus delete -f a-b
}

restore_and_compare_fs() {
    snap=${1}
    echo "==> Restoring ${snap}"

    incus snapshot restore bar "${snap}"

    # FIXME: make this backend agnostic
    if [ "$(storage_backend "$INCUS_DIR")" = "dir" ]; then
        # Recursive diff of container FS
        diff -r "${INCUS_DIR}/containers/bar/rootfs" "${INCUS_DIR}/containers-snapshots/bar/${snap}/rootfs"
    fi
}

test_snap_expiry() {
    # shellcheck disable=2039,3043
    local incus_backend
    incus_backend=$(storage_backend "$INCUS_DIR")

    ensure_import_testimage
    ensure_has_localhost_remote "${INCUS_ADDR}"

    incus launch testimage c1
    incus snapshot create c1
    incus config show c1/snap0 | grep -q 'expires_at: 0001-01-01T00:00:00Z'

    incus config set c1 snapshots.expiry '1d'
    incus snapshot create c1
    ! incus config show c1/snap1 | grep -q 'expires_at: 0001-01-01T00:00:00Z' || false

    incus copy c1 c2
    ! incus config show c2/snap1 | grep -q 'expires_at: 0001-01-01T00:00:00Z' || false

    incus snapshot create c1 --no-expiry
    incus config show c1/snap2 | grep -q 'expires_at: 0001-01-01T00:00:00Z' || false

    incus rm -f c1
    incus rm -f c2
}

test_snap_schedule() {
    # shellcheck disable=2039,3043
    local incus_backend
    incus_backend=$(storage_backend "$INCUS_DIR")

    ensure_import_testimage
    ensure_has_localhost_remote "${INCUS_ADDR}"

    # Check we get a snapshot on first start
    incus launch testimage c1 -c snapshots.schedule='@startup'
    incus launch testimage c2 -c snapshots.schedule='@startup, @daily'
    incus launch testimage c3 -c snapshots.schedule='@startup, 10 5,6 * * *'
    incus launch testimage c4 -c snapshots.schedule='@startup, 10 5-8 * * *'
    incus launch testimage c5 -c snapshots.schedule='@startup, 10 2,5-8/2 * * *'
    incus info c1 | grep -q snap0
    incus info c2 | grep -q snap0
    incus info c3 | grep -q snap0
    incus info c4 | grep -q snap0
    incus info c5 | grep -q snap0

    # Check we get a new snapshot on restart
    incus restart c1 -f
    incus info c1 | grep -q snap1

    incus rm -f c1 c2 c3 c4 c5
}

test_snap_volume_db_recovery() {
    # shellcheck disable=2039,3043
    local incus_backend
    incus_backend=$(storage_backend "$INCUS_DIR")

    ensure_import_testimage
    ensure_has_localhost_remote "${INCUS_ADDR}"

    poolName=$(incus profile device get default root pool)

    incus init testimage c1
    incus snapshot create c1
    incus snapshot create c1
    incus start c1
    incus stop -f c1
    echo 'DELETE FROM storage_volumes_snapshots' | incus admin sql global -                              # Remove volume snapshot DB records.
    echo 'DELETE FROM patches WHERE name = "storage_missing_snapshot_records"' | incus admin sql local - # Clear patch indicator.
    ! incus start c1 || false                                                                            # Shouldn't be able to start as backup.yaml generation checks for DB consistency.
    incus admin shutdown
    respawn_incus "${INCUS_DIR}" true
    incus storage volume snapshot show "${poolName}" container/c1/snap0 | grep "Auto repaired"
    incus storage volume snapshot show "${poolName}" container/c1/snap1 | grep "Auto repaired"
    incus start c1
    incus delete -f c1
}
