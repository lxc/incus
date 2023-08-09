test_snapshots() {
  snapshots

  if [ "$(storage_backend "$INCUS_DIR")" = "lvm" ]; then
    # Test that non-thinpool lvm backends work fine with snaphots.
    inc storage create "incustest-$(basename "${INCUS_DIR}")-non-thinpool-lvm-snapshots" lvm lvm.use_thinpool=false volume.size=25MiB
    inc profile device set default root pool "incustest-$(basename "${INCUS_DIR}")-non-thinpool-lvm-snapshots"

    snapshots

    inc profile device set default root pool "incustest-$(basename "${INCUS_DIR}")"

    inc storage delete "incustest-$(basename "${INCUS_DIR}")-non-thinpool-lvm-snapshots"
  fi
}

snapshots() {
  # shellcheck disable=2039,3043
  local incus_backend
  incus_backend=$(storage_backend "$INCUS_DIR")

  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  inc init testimage foo

  inc snapshot foo
  # FIXME: make this backend agnostic
  if [ "$incus_backend" = "dir" ]; then
    [ -d "${INCUS_DIR}/snapshots/foo/snap0" ]
  fi

  inc snapshot foo
  # FIXME: make this backend agnostic
  if [ "$incus_backend" = "dir" ]; then
    [ -d "${INCUS_DIR}/snapshots/foo/snap1" ]
  fi

  inc snapshot foo tester
  # FIXME: make this backend agnostic
  if [ "$incus_backend" = "dir" ]; then
    [ -d "${INCUS_DIR}/snapshots/foo/tester" ]
  fi

  inc copy foo/tester foosnap1
  # FIXME: make this backend agnostic
  if [ "$incus_backend" != "lvm" ] && [ "${incus_backend}" != "zfs" ] && [ "$incus_backend" != "ceph" ]; then
    [ -d "${INCUS_DIR}/containers/foosnap1/rootfs" ]
  fi

  inc delete foo/snap0
  # FIXME: make this backend agnostic
  if [ "$incus_backend" = "dir" ]; then
    [ ! -d "${INCUS_DIR}/snapshots/foo/snap0" ]
  fi

  # test deleting multiple snapshots
  inc snapshot foo snap2
  inc snapshot foo snap3
  inc delete foo/snap2 foo/snap3
  ! inc info foo | grep -q snap2 || false
  ! inc info foo | grep -q snap3 || false

  # no CLI for this, so we use the API directly (rename a snapshot)
  wait_for "${INCUS_ADDR}" my_curl -X POST "https://${INCUS_ADDR}/1.0/instances/foo/snapshots/tester" -d "{\"name\":\"tester2\"}"
  # FIXME: make this backend agnostic
  if [ "$incus_backend" = "dir" ]; then
    [ ! -d "${INCUS_DIR}/snapshots/foo/tester" ]
  fi

  inc move foo/tester2 foo/tester-two
  inc delete foo/tester-two
  # FIXME: make this backend agnostic
  if [ "$incus_backend" = "dir" ]; then
    [ ! -d "${INCUS_DIR}/snapshots/foo/tester-two" ]
  fi

  inc snapshot foo namechange
  # FIXME: make this backend agnostic
  if [ "$incus_backend" = "dir" ]; then
    [ -d "${INCUS_DIR}/snapshots/foo/namechange" ]
  fi
  inc move foo foople
  [ ! -d "${INCUS_DIR}/containers/foo" ]
  [ -d "${INCUS_DIR}/containers/foople" ]
  # FIXME: make this backend agnostic
  if [ "$incus_backend" = "dir" ]; then
    [ -d "${INCUS_DIR}/snapshots/foople/namechange" ]
    [ -d "${INCUS_DIR}/snapshots/foople/namechange" ]
  fi

  inc delete foople
  inc delete foosnap1
  [ ! -d "${INCUS_DIR}/containers/foople" ]
  [ ! -d "${INCUS_DIR}/containers/foosnap1" ]
}

test_snap_restore() {
  snap_restore

  if [ "$(storage_backend "$INCUS_DIR")" = "lvm" ]; then
    # Test that non-thinpool lvm backends work fine with snaphots.
    inc storage create "incustest-$(basename "${INCUS_DIR}")-non-thinpool-lvm-snap-restore" lvm lvm.use_thinpool=false volume.size=25MiB
    inc profile device set default root pool "incustest-$(basename "${INCUS_DIR}")-non-thinpool-lvm-snap-restore"

    snap_restore

    inc profile device set default root pool "incustest-$(basename "${INCUS_DIR}")"

    inc storage delete "incustest-$(basename "${INCUS_DIR}")-non-thinpool-lvm-snap-restore"
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
  inc launch testimage bar
  echo snap0 > state
  inc file push state bar/root/state
  inc file push state bar/root/file_only_in_snap0
  inc exec bar -- mkdir /root/dir_only_in_snap0
  inc exec bar -- ln -s file_only_in_snap0 /root/statelink
  inc stop bar --force

  # Get container's pool.
  pool=$(inc config profile device get default root pool)

  inc storage volume set "${pool}" container/bar user.foo=snap0

  # Check parent volume.block.filesystem is copied to snapshot and not from pool.
  if [ "$incus_backend" = "lvm" ] || [ "$incus_backend" = "ceph" ]; then
    # Change pool volume.block.filesystem setting after creation of instance and before snapshot.
    inc storage set "${pool}" volume.block.filesystem=xfs
  fi

  inc snapshot bar snap0

  ## prepare snap1
  inc start bar
  echo snap1 > state
  inc file push state bar/root/state
  inc file push state bar/root/file_only_in_snap1

  inc exec bar -- rmdir /root/dir_only_in_snap0
  inc exec bar -- rm /root/file_only_in_snap0
  inc exec bar -- rm /root/statelink
  inc exec bar -- ln -s file_only_in_snap1 /root/statelink
  inc exec bar -- mkdir /root/dir_only_in_snap1
  initialUUID=$(inc config get bar volatile.uuid)
  initialGenerationID=$(inc config get bar volatile.uuid.generation)
  inc stop bar --force
  inc storage volume set "${pool}" container/bar user.foo=snap1

  # Delete the state file we created to prevent leaking.
  rm state

  inc config set bar limits.cpu 1

  inc snapshot bar snap1
  inc storage volume set "${pool}" container/bar user.foo=postsnaps

  # Check volume.block.filesystem on storage volume in parent and snapshot match.
  if [ "${incus_backend}" = "lvm" ] || [ "${incus_backend}" = "ceph" ]; then
    # Change pool volume.block.filesystem setting after creation of instance and before snapshot.
    pool=$(inc config profile device get default root pool)
    parentFS=$(inc storage volume get "${pool}" container/bar block.filesystem)
    snapFS=$(inc storage volume get "${pool}" container/bar/snap0 block.filesystem)

    if [ "${parentFS}" != "${snapFS}" ]; then
      echo "block.filesystem settings do not match in parent and snapshot"
      false
    fi

    inc storage unset "${pool}" volume.block.filesystem
  fi

  ##########################################################

  if [ "$incus_backend" != "zfs" ]; then
    # The problem here is that you can't `zfs rollback` to a snapshot with a
    # parent, which snap0 has (snap1).
    restore_and_compare_fs snap0

    # Check container config has been restored (limits.cpu is unset)
    cpus=$(inc config get bar limits.cpu)
    if [ -n "${cpus}" ]; then
      echo "==> config didn't match expected value after restore (${cpus})"
      false
    fi

    # Check storage volume has been restored (user.foo=snap0)
    inc storage volume get "${pool}" container/bar user.foo | grep -Fx "snap0"
  fi

  ##########################################################

  # test restore using full snapshot name
  restore_and_compare_fs snap1

  # Check that instances UUID remain the same before and after snapshoting
  newUUID=$(inc config get bar volatile.uuid)
  if [ "${initialUUID}" != "${newUUID}" ]; then
    echo "==> UUID of the instance should remain the same after restoring its snapshot"
    false
  fi

  # Check that the generation UUID from before changes compared to the one after snapshoting
  newGenerationID=$(inc config get bar volatile.uuid.generation)
  if [ "${initialGenerationID}" = "${newGenerationID}" ]; then
    echo "==> Generation UUID of the instance should change after restoring its snapshot"
    false
  fi

  # Check that instances UUIS remain the same before and after snapshoting  (stateful mode)
  if ! command -v criu >/dev/null 2>&1; then
    echo "==> SKIP: stateful snapshotting with CRIU (missing binary)"
  else
    initialUUID=$(inc config get bar volatile.uuid)
    initialGenerationID=$(inc config get bar volatile.uuid.generation)
    inc start bar
    inc snapshot bar snap2 --stateful
    restore_and_compare_fs snap2

    newUUID=$(inc config get bar volatile.uuid)
    if [ "${initialUUID}" != "${newUUID}" ]; then
      echo "==> UUID of the instance should remain the same after restoring its stateful snapshot"
      false
    fi

    newGenerationID=$(inc config get bar volatile.uuid.generation)
    if [ "${initialGenerationID}" = "${newGenerationID}" ]; then
      echo "==> Generation UUID of the instance should change after restoring its stateful snapshot"
      false
    fi

    inc stop bar --force
  fi

  # Check that instances have two different UUID after a snapshot copy
  inc launch testimage bar2
  initialUUID=$(inc config get bar2 volatile.uuid)
  initialGenerationID=$(inc config get bar2 volatile.uuid.generation)
  inc copy bar2 bar3
  newUUID=$(inc config get bar3 volatile.uuid)
  newGenerationID=$(inc config get bar3 volatile.uuid.generation)

  if [ "${initialGenerationID}" = "${newGenerationID}" ] || [ "${initialUUID}" = "${newUUID}" ]; then
    echo "==> UUIDs of the instance should be different after copying snapshot into instance"
    false
  fi

  inc delete --force bar2
  inc delete --force bar3

  # Check config value in snapshot has been restored
  cpus=$(inc config get bar limits.cpu)
  if [ "${cpus}" != "1" ]; then
   echo "==> config didn't match expected value after restore (${cpus})"
   false
  fi

  # Check storage volume has been restored (user.foo=snap0)
  inc storage volume get "${pool}" container/bar user.foo | grep -Fx "snap1"

  ##########################################################

  # Start container and then restore snapshot to verify the running state after restore.
  inc start bar

  if [ "$incus_backend" != "zfs" ]; then
    # see comment above about snap0
    restore_and_compare_fs snap0

    # check container is running after restore
    inc list | grep bar | grep RUNNING
  fi

  inc stop --force bar

  inc delete bar

  # Test if container's with hyphen's in their names are treated correctly.
  inc launch testimage a-b
  inc snapshot a-b base
  inc restore a-b base
  inc snapshot a-b c-d
  inc restore a-b c-d
  inc delete -f a-b
}

restore_and_compare_fs() {
  snap=${1}
  echo "==> Restoring ${snap}"

  inc restore bar "${snap}"

  # FIXME: make this backend agnostic
  if [ "$(storage_backend "$INCUS_DIR")" = "dir" ]; then
    # Recursive diff of container FS
    diff -r "${INCUS_DIR}/containers/bar/rootfs" "${INCUS_DIR}/snapshots/bar/${snap}/rootfs"
  fi
}

test_snap_expiry() {
  # shellcheck disable=2039,3043
  local incus_backend
  incus_backend=$(storage_backend "$INCUS_DIR")

  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  inc launch testimage c1
  inc snapshot c1
  inc config show c1/snap0 | grep -q 'expires_at: 0001-01-01T00:00:00Z'

  inc config set c1 snapshots.expiry '1d'
  inc snapshot c1
  ! inc config show c1/snap1 | grep -q 'expires_at: 0001-01-01T00:00:00Z' || false

  inc copy c1 c2
  ! inc config show c2/snap1 | grep -q 'expires_at: 0001-01-01T00:00:00Z' || false

  inc snapshot c1 --no-expiry
  inc config show c1/snap2 | grep -q 'expires_at: 0001-01-01T00:00:00Z' || false

  inc rm -f c1
  inc rm -f c2
}

test_snap_schedule() {
  # shellcheck disable=2039,3043
  local incus_backend
  incus_backend=$(storage_backend "$INCUS_DIR")

  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  # Check we get a snapshot on first start
  inc launch testimage c1 -c snapshots.schedule='@startup'
  inc launch testimage c2 -c snapshots.schedule='@startup, @daily'
  inc launch testimage c3 -c snapshots.schedule='@startup, 10 5,6 * * *'
  inc launch testimage c4 -c snapshots.schedule='@startup, 10 5-8 * * *'
  inc launch testimage c5 -c snapshots.schedule='@startup, 10 2,5-8/2 * * *'
  inc info c1 | grep -q snap0
  inc info c2 | grep -q snap0
  inc info c3 | grep -q snap0
  inc info c4 | grep -q snap0
  inc info c5 | grep -q snap0

  # Check we get a new snapshot on restart
  inc restart c1 -f
  inc info c1 | grep -q snap1

  inc rm -f c1 c2 c3 c4 c5
}

test_snap_volume_db_recovery() {
  # shellcheck disable=2039,3043
  local incus_backend
  incus_backend=$(storage_backend "$INCUS_DIR")

  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  poolName=$(inc profile device get default root pool)

  inc init testimage c1
  inc snapshot c1
  inc snapshot c1
  inc start c1
  inc stop -f c1
  incusd sql global 'DELETE FROM storage_volumes_snapshots' # Remove volume snapshot DB records.
  incusd sql local 'DELETE FROM  patches WHERE name = "storage_missing_snapshot_records"' # Clear patch indicator.
  ! inc start c1 || false # Shouldn't be able to start as backup.yaml generation checks for DB consistency.
  incusd shutdown
  respawn_incus "${INCUS_DIR}" true
  inc storage volume show "${poolName}" container/c1/snap0 | grep "Auto repaired"
  inc storage volume show "${poolName}" container/c1/snap1 | grep "Auto repaired"
  inc start c1
  inc delete -f c1
}
