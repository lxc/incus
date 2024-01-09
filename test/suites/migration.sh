test_migration() {
  # setup a second Incus
  # shellcheck disable=2039,3043
  local INCUS2_DIR INCUS2_ADDR incus_backend
  # shellcheck disable=2153
  incus_backend=$(storage_backend "$INCUS_DIR")

  INCUS2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS2_DIR}"
  spawn_incus "${INCUS2_DIR}" true
  INCUS2_ADDR=$(cat "${INCUS2_DIR}/incus.addr")

  # workaround for kernel/criu
  umount /sys/kernel/debug >/dev/null 2>&1 || true

  token="$(incus config trust add foo -q)"
  # shellcheck disable=2153
  incus_remote remote add l1 "${INCUS_ADDR}" --accept-certificate --token "${token}"

  token="$(INCUS_DIR=${INCUS2_DIR} incus config trust add foo -q)"
  incus_remote remote add l2 "${INCUS2_ADDR}" --accept-certificate --token "${token}"

  migration "$INCUS2_DIR"

  # This should only run on lvm and when the backend is not random. Otherwise
  # we might perform existence checks for files or dirs that won't be available
  # since the logical volume is not mounted when the container is not running.
  # shellcheck disable=2153
  if [ "${INCUS_BACKEND}" = "lvm" ]; then
    # Test that non-thinpool lvm backends work fine with migration.

    # shellcheck disable=2039,3043
    local storage_pool1 storage_pool2
    # shellcheck disable=2153
    storage_pool1="incustest-$(basename "${INCUS_DIR}")-non-thinpool-lvm-migration"
    storage_pool2="incustest-$(basename "${INCUS2_DIR}")-non-thinpool-lvm-migration"
    incus_remote storage create l1:"$storage_pool1" lvm lvm.use_thinpool=false size=1GiB volume.size=25MiB
    incus_remote profile device set l1:default root pool "$storage_pool1"

    incus_remote storage create l2:"$storage_pool2" lvm lvm.use_thinpool=false size=1GiB volume.size=25MiB
    incus_remote profile device set l2:default root pool "$storage_pool2"

    migration "$INCUS2_DIR"

    incus_remote profile device set l1:default root pool "incustest-$(basename "${INCUS_DIR}")"
    incus_remote profile device set l2:default root pool "incustest-$(basename "${INCUS2_DIR}")"

    incus_remote storage delete l1:"$storage_pool1"
    incus_remote storage delete l2:"$storage_pool2"
  fi

  if [ "${INCUS_BACKEND}" = "zfs" ]; then
    # Test that block mode zfs backends work fine with migration.
    for fs in "ext4" "btrfs" "xfs"; do
      if ! command -v "mkfs.${fs}" >/dev/null 2>&1; then
        echo "==> SKIP: Skipping block mode test on ${fs} due to missing tools."
        continue
      fi

      # shellcheck disable=2039,3043
      local storage_pool1 storage_pool2
      # shellcheck disable=2153
      storage_pool1="incustest-$(basename "${INCUS_DIR}")-block-mode"
      storage_pool2="incustest-$(basename "${INCUS2_DIR}")-block-mode"
      incus_remote storage create l1:"$storage_pool1" zfs size=1GiB volume.zfs.block_mode=true volume.block.filesystem="${fs}"
      incus_remote profile device set l1:default root pool "$storage_pool1"

      incus_remote storage create l2:"$storage_pool2" zfs size=1GiB volume.zfs.block_mode=true volume.block.filesystem="${fs}"
      incus_remote profile device set l2:default root pool "$storage_pool2"

      migration "$INCUS2_DIR"

      incus_remote profile device set l1:default root pool "incustest-$(basename "${INCUS_DIR}")"
      incus_remote profile device set l2:default root pool "incustest-$(basename "${INCUS2_DIR}")"

      incus_remote storage delete l1:"$storage_pool1"
      incus_remote storage delete l2:"$storage_pool2"
    done
  fi

  incus_remote remote remove l1
  incus_remote remote remove l2
  kill_incus "$INCUS2_DIR"
}

migration() {
  # shellcheck disable=2039,3043
  local incus2_dir incus_backend incus2_backend
  incus2_dir="$1"
  incus_backend=$(storage_backend "$INCUS_DIR")
  incus2_backend=$(storage_backend "$incus2_dir")
  ensure_import_testimage

  incus_remote init testimage nonlive
  # test moving snapshots
  incus_remote config set l1:nonlive user.tester foo
  incus_remote snapshot create l1:nonlive
  incus_remote config unset l1:nonlive user.tester
  incus_remote move l1:nonlive l2:
  incus_remote config show l2:nonlive/snap0 | grep user.tester | grep foo

  # This line exists so that the container's storage volume is mounted when we
  # perform existence check for various files.
  incus_remote start l2:nonlive
  # FIXME: make this backend agnostic
  if [ "$incus2_backend" != "lvm" ] && [ "$incus2_backend" != "zfs" ] && [ "$incus2_backend" != "ceph" ]; then
    [ -d "${incus2_dir}/containers/nonlive/rootfs" ]
  fi
  incus_remote stop l2:nonlive --force

  [ ! -d "${INCUS_DIR}/containers/nonlive" ]
  # FIXME: make this backend agnostic
  if [ "$incus2_backend" = "dir" ]; then
    [ -d "${incus2_dir}/containers-snapshots/nonlive/snap0/rootfs/bin" ]
  fi

  incus_remote copy l2:nonlive l1:nonlive2 --mode=push
  # This line exists so that the container's storage volume is mounted when we
  # perform existence check for various files.
  incus_remote start l2:nonlive
  [ -d "${INCUS_DIR}/containers/nonlive2" ]
  # FIXME: make this backend agnostic
  if [ "$incus2_backend" != "lvm" ] && [ "$incus2_backend" != "zfs" ] && [ "$incus2_backend" != "ceph" ]; then
    [ -d "${incus2_dir}/containers/nonlive/rootfs/bin" ]
  fi

  # FIXME: make this backend agnostic
  if [ "$incus_backend" = "dir" ]; then
    [ -d "${INCUS_DIR}/containers-snapshots/nonlive2/snap0/rootfs/bin" ]
  fi

  incus_remote copy l1:nonlive2/snap0 l2:nonlive3 --mode=relay
  # FIXME: make this backend agnostic
  if [ "$incus2_backend" != "lvm" ] && [ "$incus2_backend" != "zfs" ] && [ "$incus2_backend" != "ceph" ]; then
    [ -d "${incus2_dir}/containers/nonlive3/rootfs/bin" ]
  fi
  incus_remote delete l2:nonlive3 --force

  incus_remote stop l2:nonlive --force
  incus_remote copy l2:nonlive l2:nonlive2 --mode=push
  # should have the same base image tag
  [ "$(incus_remote config get l2:nonlive volatile.base_image)" = "$(incus_remote config get l2:nonlive2 volatile.base_image)" ]
  # check that nonlive2 has a new addr in volatile
  [ "$(incus_remote config get l2:nonlive volatile.eth0.hwaddr)" != "$(incus_remote config get l2:nonlive2 volatile.eth0.hwaddr)" ]

  incus_remote config unset l2:nonlive volatile.base_image
  incus_remote copy l2:nonlive l1:nobase
  incus_remote delete l1:nobase

  incus_remote start l1:nonlive2
  incus_remote list l1: | grep RUNNING | grep nonlive2
  incus_remote delete l1:nonlive2 l2:nonlive2 --force

  incus_remote launch testimage cccp
  incus_remote copy l1:cccp l2:udssr --stateless
  incus_remote delete l2:udssr --force
  incus_remote copy l1:cccp l2:udssr --stateless --mode=push
  incus_remote delete l2:udssr --force
  incus_remote copy l1:cccp l2:udssr --stateless --mode=relay
  incus_remote delete l2:udssr --force

  incus_remote move l1:cccp l2:udssr --stateless
  incus_remote delete l2:udssr --force
  incus_remote launch testimage cccp
  incus_remote move l1:cccp l2:udssr --stateless --mode=push
  incus_remote delete l2:udssr --force
  incus_remote launch testimage cccp
  incus_remote move l1:cccp l2:udssr --stateless --mode=relay
  incus_remote delete l2:udssr --force

  incus_remote start l2:nonlive
  incus_remote list l2: | grep RUNNING | grep nonlive
  incus_remote delete l2:nonlive --force

  # Get container's pool.
  pool=$(incus config profile device get default root pool)
  remote_pool=$(incus_remote config profile device get l2:default root pool)

  # Test container only copies
  incus init testimage cccp

  incus storage volume set "${pool}" container/cccp user.foo=snap0
  echo "before" | incus file push - cccp/blah
  incus snapshot create cccp
  incus storage volume set "${pool}" container/cccp user.foo=snap1
  incus snapshot create cccp
  echo "after" | incus file push - cccp/blah
  incus storage volume set "${pool}" container/cccp user.foo=postsnap1

  # Check storage volume creation times are set.
  incus query /1.0/storage-pools/"${pool}"/volumes/container/cccp | jq .created_at | grep -Fv '0001-01-01T00:00:00Z'
  incus query /1.0/storage-pools/"${pool}"/volumes/container/cccp/snapshots/snap0 | jq .created_at | grep -Fv '0001-01-01T00:00:00Z'

  # Local container only copy.
  incus copy cccp udssr --instance-only
  [ "$(incus info udssr | grep -c snap)" -eq 0 ]
  [ "$(incus file pull udssr/blah -)" = "after" ]
  incus delete udssr

  # Local container with snapshots copy.
  incus copy cccp udssr
  [ "$(incus info udssr | grep -c snap)" -eq 2 ]
  [ "$(incus file pull udssr/blah -)" = "after" ]
  incus storage volume show "${pool}" container/udssr
  incus storage volume get "${pool}" container/udssr user.foo | grep -Fx "postsnap1"
  incus storage volume get "${pool}" container/udssr/snap0 user.foo | grep -Fx "snap0"
  incus storage volume get "${pool}" container/udssr/snap1 user.foo | grep -Fx "snap1"
  incus delete udssr

  # Remote container only copy.
  incus_remote copy l1:cccp l2:udssr --instance-only
  [ "$(incus_remote info l2:udssr | grep -c snap)" -eq 0 ]
  [ "$(incus_remote file pull l2:udssr/blah -)" = "after" ]
  incus_remote delete l2:udssr

  # Remote container with snapshots copy.
  incus_remote copy l1:cccp l2:udssr
  [ "$(incus_remote info l2:udssr | grep -c snap)" -eq 2 ]
  [ "$(incus_remote file pull l2:udssr/blah -)" = "after" ]
  incus_remote storage volume show l2:"${remote_pool}" container/udssr
  incus_remote storage volume get l2:"${remote_pool}" container/udssr user.foo | grep -Fx "postsnap1"
  incus_remote storage volume get l2:"${remote_pool}" container/udssr/snap0 user.foo | grep -Fx "snap0"
  incus_remote storage volume get l2:"${remote_pool}" container/udssr/snap1 user.foo | grep -Fx "snap1"
  incus_remote delete l2:udssr

  # Remote container only move.
  incus_remote move l1:cccp l2:udssr --instance-only --mode=relay
  ! incus_remote info l1:cccp || false
  [ "$(incus_remote info l2:udssr | grep -c snap)" -eq 0 ]
  incus_remote delete l2:udssr

  incus_remote init testimage l1:cccp
  incus_remote snapshot create l1:cccp
  incus_remote snapshot create l1:cccp

  # Remote container with snapshots move.
  incus_remote move l1:cccp l2:udssr --mode=push
  ! incus_remote info l1:cccp || false
  [ "$(incus_remote info l2:udssr | grep -c snap)" -eq 2 ]
  incus_remote delete l2:udssr

  # Test container only copies
  incus init testimage cccp
  incus snapshot create cccp
  incus snapshot create cccp

  # Local container with snapshots move.
  incus move cccp udssr --mode=pull
  ! incus info cccp || false
  [ "$(incus info udssr | grep -c snap)" -eq 2 ]
  incus delete udssr

  if [ "$incus_backend" = "zfs" ]; then
    # Test container only copies when zfs.clone_copy is set to false.
    incus storage set "incustest-$(basename "${INCUS_DIR}")" zfs.clone_copy false
    incus init testimage cccp
    incus snapshot create cccp
    incus snapshot create cccp

    # Test container only copies when zfs.clone_copy is set to false.
    incus copy cccp udssr --instance-only
    [ "$(incus info udssr | grep -c snap)" -eq 0 ]
    incus delete udssr

    # Test container with snapshots copy when zfs.clone_copy is set to false.
    incus copy cccp udssr
    [ "$(incus info udssr | grep -c snap)" -eq 2 ]
    incus delete cccp
    incus delete udssr

    incus storage unset "incustest-$(basename "${INCUS_DIR}")" zfs.clone_copy
  fi

  incus_remote init testimage l1:c1
  incus_remote copy l1:c1 l2:c2
  incus_remote copy l1:c1 l2:c2 --refresh

  incus_remote start l1:c1 l2:c2

  # Make sure the testfile doesn't exist
  ! incus file pull l1:c1 -- /root/testfile1 || false
  ! incus file pull l2:c2 -- /root/testfile1 || false

  #incus_remote start l1:c1 l2:c2

  # Containers may not be running when refreshing
  ! incus_remote copy l1:c1 l2:c2 --refresh || false

  # Create test file in c1
  echo test | incus_remote file push - l1:c1/root/testfile1

  incus_remote stop -f l1:c1 l2:c2

  # Refresh the container and validate the contents
  incus_remote copy l1:c1 l2:c2 --refresh
  incus_remote start l2:c2
  incus_remote file pull l2:c2/root/testfile1 .
  [ "$(cat testfile1)" = "test" ]
  rm testfile1
  incus_remote stop -f l2:c2

  # Change the files modification time by adding one nanosecond.
  # Perform the change on the test runner since the busybox instances `touch` doesn't support setting nanoseconds.
  incus_remote start l1:c1
  c1_pid="$(incus_remote query l1:/1.0/instances/c1?recursion=1 | jq -r .state.pid)"
  mtime_old="$(stat -c %y "/proc/${c1_pid}/root/root/testfile1")"
  mtime_old_ns="$(date -d "$mtime_old" +%N | sed 's/^0*//')"

  # Ensure the final nanoseconds are padded with zeros to create a valid format.
  mtime_new_ns="$(printf "%09d\n" "$((mtime_old_ns+1))")"
  mtime_new="$(date -d "$mtime_old" "+%Y-%m-%d %H:%M:%S.${mtime_new_ns} %z")"
  incus_remote stop -f l1:c1

  # Before setting the new mtime create a local copy too.
  incus_remote copy l1:c1 l1:c2

  # Change the modification time.
  incus_remote start l1:c1
  c1_pid="$(incus_remote query l1:/1.0/instances/c1?recursion=1 | jq -r .state.pid)"
  touch -m -d "$mtime_new" "/proc/${c1_pid}/root/root/testfile1"
  incus_remote stop -f l1:c1

  # Starting from rsync 3.1.3 it should discover the change of +1 nanosecond.
  # Check if the file got refreshed to a different remote.
  incus_remote copy l1:c1 l2:c2 --refresh
  incus_remote start l1:c1 l2:c2
  c1_pid="$(incus_remote query l1:/1.0/instances/c1?recursion=1 | jq -r .state.pid)"
  c2_pid="$(incus_remote query l2:/1.0/instances/c2?recursion=1 | jq -r .state.pid)"
  [ "$(stat "/proc/${c1_pid}/root/root/testfile1" -c %y)" = "$(stat "/proc/${c2_pid}/root/root/testfile1" -c %y)" ]
  incus_remote stop -f l1:c1 l2:c2

  # Check if the file got refreshed locally.
  incus_remote copy l1:c1 l1:c2 --refresh
  incus_remote start l1:c1 l1:c2
  c1_pid="$(incus_remote query l1:/1.0/instances/c1?recursion=1 | jq -r .state.pid)"
  c2_pid="$(incus_remote query l1:/1.0/instances/c2?recursion=1 | jq -r .state.pid)"
  [ "$(stat "/proc/${c1_pid}/root/root/testfile1" -c %y)" = "$(stat "/proc/${c2_pid}/root/root/testfile1" -c %y)" ]
  incus_remote rm -f l1:c2
  incus_remote stop -f l1:c1

  # This will create snapshot c1/snap0 with test device and expiry date.
  incus_remote config device add l1:c1 testsnapdev none
  incus_remote config set l1:c1 snapshots.expiry '1d'
  incus_remote snapshot create l1:c1
  incus_remote config device remove l1:c1 testsnapdev
  incus_remote config device add l1:c1 testdev none

  # Remove the testfile from c1 and refresh again
  incus_remote file delete l1:c1/root/testfile1
  incus_remote copy l1:c1 l2:c2 --refresh --instance-only
  incus_remote start l2:c2
  ! incus_remote file pull l2:c2/root/testfile1 . || false
  incus_remote stop -f l2:c2

  # Check whether snapshot c2/snap0 has been created with its config intact.
  ! incus_remote config show l2:c2/snap0 || false
  incus_remote copy l1:c1 l2:c2 --refresh
  incus_remote ls l2:
  incus_remote config show l2:c2/snap0
  ! incus_remote config show l2:c2/snap0 | grep -q 'expires_at: 0001-01-01T00:00:00Z' || false
  incus_remote config device get l2:c2 testdev type | grep -q 'none'
  incus_remote snapshot restore l2:c2 snap0
  incus_remote config device get l2:c2 testsnapdev type | grep -q 'none'

  # This will create snapshot c2/snap1
  incus_remote snapshot create l2:c2
  incus_remote config show l2:c2/snap1

  # This should remove c2/snap1
  incus_remote copy l1:c1 l2:c2 --refresh
  ! incus_remote config show l2:c2/snap1 || false

  incus_remote rm -f l1:c1 l2:c2

  remote_pool1="incustest-$(basename "${INCUS_DIR}")"
  remote_pool2="incustest-$(basename "${incus2_dir}")"

  incus_remote storage volume create l1:"$remote_pool1" vol1
  incus_remote storage volume set l1:"$remote_pool1" vol1 user.foo=snap0vol1
  incus_remote storage volume snapshot create l1:"$remote_pool1" vol1
  incus_remote storage volume set l1:"$remote_pool1" vol1 user.foo=snap1vol1
  incus_remote storage volume snapshot create l1:"$remote_pool1" vol1
  incus_remote storage volume set l1:"$remote_pool1" vol1 user.foo=postsnap1vol1

  # remote storage volume and snapshots migration in "pull" mode
  incus_remote storage volume copy l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol2"
  incus_remote storage volume get l2:"$remote_pool2" vol2 user.foo | grep -Fx "postsnap1vol1"
  incus_remote storage volume get l2:"$remote_pool2" vol2/snap0 user.foo | grep -Fx "snap0vol1"
  incus_remote storage volume get l2:"$remote_pool2" vol2/snap1 user.foo | grep -Fx "snap1vol1"
  incus_remote storage volume delete l2:"$remote_pool2" vol2

  # check moving volume and snapshots.
  incus_remote storage volume copy l1:"$remote_pool1/vol1" l1:"$remote_pool1/vol2"
  incus_remote storage volume move l1:"$remote_pool1/vol2" l2:"$remote_pool2/vol3"
  ! incus_remote storage volume show l1:"$remote_pool1" vol2 || false
  incus_remote storage volume get l2:"$remote_pool2" vol3 user.foo | grep -Fx "postsnap1vol1"
  incus_remote storage volume get l2:"$remote_pool2" vol3/snap0 user.foo | grep -Fx "snap0vol1"
  incus_remote storage volume get l2:"$remote_pool2" vol3/snap1 user.foo | grep -Fx "snap1vol1"
  incus_remote storage volume delete l2:"$remote_pool2" vol3

  incus_remote storage volume copy l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol2" --volume-only
  incus_remote storage volume get l2:"$remote_pool2" vol2 user.foo | grep -Fx "postsnap1vol1"
  ! incus_remote storage volume show l2:"$remote_pool2" vol2/snap0 || false
  ! incus_remote storage volume show l2:"$remote_pool2" vol2/snap1 || false
  incus_remote storage volume delete l2:"$remote_pool2" vol2

  # remote storage volume and snapshots migration refresh in "pull" mode
  incus_remote storage volume set l1:"$remote_pool1" vol1 user.foo=snapremovevol1
  incus_remote storage volume snapshot create l1:"$remote_pool1" vol1 snapremove
  incus_remote storage volume set l1:"$remote_pool1" vol1 user.foo=postsnap1vol1
  incus_remote storage volume copy l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol2" --refresh
  incus_remote storage volume delete l1:"$remote_pool1" vol1

  incus_remote storage volume get l2:"$remote_pool2" vol2 user.foo | grep -Fx "postsnap1vol1"
  incus_remote storage volume get l2:"$remote_pool2" vol2/snap0 user.foo | grep -Fx "snap0vol1"
  incus_remote storage volume get l2:"$remote_pool2" vol2/snap1 user.foo | grep -Fx "snap1vol1"
  incus_remote storage volume get l2:"$remote_pool2" vol2/snapremove user.foo | grep -Fx "snapremovevol1"

  # check remote storage volume refresh from a different volume
  incus_remote storage volume create l1:"$remote_pool1" vol3
  incus_remote storage volume set l1:"$remote_pool1" vol3 user.foo=snap0vol3
  incus_remote storage volume snapshot create l1:"$remote_pool1" vol3
  incus_remote storage volume set l1:"$remote_pool1" vol3 user.foo=snap1vol3
  incus_remote storage volume snapshot create l1:"$remote_pool1" vol3
  incus_remote storage volume set l1:"$remote_pool1" vol3 user.foo=snap2vol3
  incus_remote storage volume snapshot create l1:"$remote_pool1" vol3
  incus_remote storage volume set l1:"$remote_pool1" vol3 user.foo=postsnap1vol3

  # check snapshot volumes and snapshots are refreshed
  incus_remote storage volume copy l1:"$remote_pool1/vol3" l2:"$remote_pool2/vol2" --refresh
  incus_remote storage volume ls l2:"$remote_pool2"
  incus_remote storage volume delete l1:"$remote_pool1" vol3
  incus_remote storage volume get l2:"$remote_pool2" vol2 user.foo | grep -Fx "postsnap1vol1" # FIXME Should be postsnap1vol3
  incus_remote storage volume get l2:"$remote_pool2" vol2/snap0 user.foo | grep -Fx "snap0vol3"
  incus_remote storage volume get l2:"$remote_pool2" vol2/snap1 user.foo | grep -Fx "snap1vol3"
  incus_remote storage volume get l2:"$remote_pool2" vol2/snap2 user.foo | grep -Fx "snap2vol3"
  ! incus_remote storage volume show l2:"$remote_pool2" vol2/snapremove || false

  incus_remote storage volume delete l2:"$remote_pool2" vol2

  # remote storage volume migration in "push" mode
  incus_remote storage volume create l1:"$remote_pool1" vol1
  incus_remote storage volume create l1:"$remote_pool1" vol2
  incus_remote storage volume snapshot create l1:"$remote_pool1" vol2

  incus_remote storage volume copy l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol2" --mode=push
  incus_remote storage volume move l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol3" --mode=push
  ! incus_remote storage volume list l1:"$remote_pool1/vol1" || false
  incus_remote storage volume copy l1:"$remote_pool1/vol2" l2:"$remote_pool2/vol4" --volume-only --mode=push
  incus_remote storage volume copy l1:"$remote_pool1/vol2" l2:"$remote_pool2/vol5" --mode=push
  incus_remote storage volume move l1:"$remote_pool1/vol2" l2:"$remote_pool2/vol6" --mode=push

  incus_remote storage volume delete l2:"$remote_pool2" vol2
  incus_remote storage volume delete l2:"$remote_pool2" vol3
  incus_remote storage volume delete l2:"$remote_pool2" vol4
  incus_remote storage volume delete l2:"$remote_pool2" vol5
  incus_remote storage volume delete l2:"$remote_pool2" vol6

  # remote storage volume migration in "relay" mode
  incus_remote storage volume create l1:"$remote_pool1" vol1
  incus_remote storage volume create l1:"$remote_pool1" vol2
  incus_remote storage volume snapshot create l1:"$remote_pool1" vol2

  incus_remote storage volume copy l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol2" --mode=relay
  incus_remote storage volume move l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol3" --mode=relay
  ! incus_remote storage volume list l1:"$remote_pool1/vol1" || false
  incus_remote storage volume copy l1:"$remote_pool1/vol2" l2:"$remote_pool2/vol4" --volume-only --mode=relay
  incus_remote storage volume copy l1:"$remote_pool1/vol2" l2:"$remote_pool2/vol5" --mode=relay
  incus_remote storage volume move l1:"$remote_pool1/vol2" l2:"$remote_pool2/vol6" --mode=relay

  incus_remote storage volume delete l2:"$remote_pool2" vol2
  incus_remote storage volume delete l2:"$remote_pool2" vol3
  incus_remote storage volume delete l2:"$remote_pool2" vol4
  incus_remote storage volume delete l2:"$remote_pool2" vol5
  incus_remote storage volume delete l2:"$remote_pool2" vol6

  # Test migration when rsync compression is disabled
  incus_remote storage set l1:"$remote_pool1" rsync.compression false
  incus_remote storage volume create l1:"$remote_pool1" foo
  incus_remote storage volume copy l1:"$remote_pool1"/foo l2:"$remote_pool2"/bar
  incus_remote storage volume delete l1:"$remote_pool1" foo
  incus_remote storage volume delete l2:"$remote_pool2" bar
  incus_remote storage unset l1:"$remote_pool1" rsync.compression

  # Test some migration between projects
  incus_remote project create l1:proj -c features.images=false -c features.profiles=false
  incus_remote project switch l1:proj

  incus_remote init testimage l1:c1
  incus_remote copy l1:c1 l2:
  incus_remote start l2:c1
  incus_remote delete l2:c1 -f

  incus_remote snapshot create l1:c1
  incus_remote snapshot create l1:c1
  incus_remote snapshot create l1:c1
  incus_remote copy l1:c1 l2:
  incus_remote start l2:c1
  incus_remote stop l2:c1 -f
  incus_remote delete l1:c1

  incus_remote copy l2:c1 l1:
  incus_remote start l1:c1
  incus_remote delete l1:c1 -f

  incus_remote snapshot delete l2:c1 snap0
  incus_remote snapshot delete l2:c1 snap1
  incus_remote snapshot delete l2:c1 snap2
  incus_remote copy l2:c1 l1:
  incus_remote start l1:c1
  incus_remote delete l1:c1 -f
  incus_remote delete l2:c1 -f

  incus_remote project switch l1:default
  incus_remote project delete l1:proj


  # Check migration with invalid snapshot config (disks attached with missing source pool and source path).
  incus_remote init testimage l1:c1
  incus_remote storage create l1:dir dir
  incus_remote storage volume create l1:dir vol1
  incus_remote storage volume attach l1:dir vol1 c1 /mnt
  mkdir "$INCUS_DIR/testvol2"
  incus_remote config device add l1:c1 vol2 disk source="$INCUS_DIR/testvol2" path=/vol2
  incus_remote snapshot create l1:c1 # Take snapshot with disk devices still attached.
  incus_remote config device remove c1 vol1
  incus_remote config device remove c1 vol2
  rmdir "$INCUS_DIR/testvol2"
  incus_remote copy l1:c1 l2:
  incus_remote info l2:c1 | grep snap0
  incus_remote delete l1:c1 -f
  incus_remote delete l2:c1 -f
  incus_remote storage volume delete l1:dir vol1
  incus_remote storage delete l1:dir

  # Test optimized refresh
  incus_remote init testimage l1:c1
  echo test | incus_remote file push - l1:c1/tmp/foo
  incus_remote copy l1:c1 l2:c1
  incus_remote file pull l2:c1/tmp/foo .
  incus_remote snapshot create l1:c1
  echo test | incus_remote file push - l1:c1/tmp/bar
  incus_remote copy l1:c1 l2:c1 --refresh
  incus_remote start l2:c1
  incus_remote file pull l2:c1/tmp/foo .
  incus_remote file pull l2:c1/tmp/bar .
  incus_remote stop l2:c1 -f

  incus_remote snapshot restore l2:c1 snap0
  incus_remote start l2:c1
  incus_remote file pull l2:c1/tmp/foo .
  ! incus_remote file pull l2:c1/tmp/bar . ||  false
  incus_remote stop l2:c1 -f

  rm foo bar

  incus_remote rm l1:c1
  incus_remote rm l2:c1

  incus_remote init testimage l1:c1
  # This creates snap0
  incus_remote snapshot create l1:c1
  # This creates snap1
  incus_remote snapshot create l1:c1
  incus_remote copy l1:c1 l2:c1
  # This creates snap2
  incus_remote snapshot create l1:c1

  # Delete first snapshot from target
  incus_remote snapshot rm l2:c1 snap0

  # Refresh
  incus_remote copy l1:c1 l2:c1 --refresh

  incus_remote rm -f l1:c1
  incus_remote rm -f l2:c1

  # In this scenario the source Incus server used to crash due to a missing slice check.
  # Let's test this to make sure it doesn't happen again.
  incus_remote init testimage l1:c1
  incus_remote copy l1:c1 l2:c1
  incus_remote snapshot create l1:c1
  incus_remote snapshot create l1:c1

  incus_remote copy l1:c1 l2:c1 --refresh
  incus_remote copy l1:c1 l2:c1 --refresh

  incus_remote rm -f l1:c1
  incus_remote rm -f l2:c1

  # On btrfs, this used to cause a failure because btrfs couldn't find the parent subvolume.
  incus_remote init testimage l1:c1
  incus_remote copy l1:c1 l2:c1
  incus_remote snapshot create l1:c1
  incus_remote copy l1:c1 l2:c1 --refresh
  incus_remote snapshot create l1:c1
  incus_remote copy l1:c1 l2:c1 --refresh

  incus_remote rm -f l1:c1
  incus_remote rm -f l2:c1

  # On zfs, this used to crash due to a websocket read issue.
  incus launch testimage c1
  incus snapshot create c1
  incus copy c1 l2:c1 --stateless
  incus copy c1 l2:c1 --stateless --refresh

  incus_remote rm -f l1:c1
  incus_remote rm -f l2:c1

  # migrate ISO custom volumes
  truncate -s 8MiB foo.iso
  incus storage volume import l1:"${pool}" ./foo.iso iso1
  incus storage volume copy l1:"${pool}"/iso1 l2:"${remote_pool}"/iso1

  incus storage volume show l2:"${remote_pool}" iso1 | grep -q 'content_type: iso'
  incus storage volume move l1:"${pool}"/iso1 l2:"${remote_pool}"/iso2
  incus storage volume show l2:"${remote_pool}" iso2 | grep -q 'content_type: iso'
  ! incus storage volume show l1:"${pool}" iso1 || false

  incus storage volume delete l2:"${remote_pool}" iso1
  incus storage volume delete l2:"${remote_pool}" iso2
  rm -f foo.iso

  if ! command -v criu >/dev/null 2>&1; then
    echo "==> SKIP: live migration with CRIU (missing binary)"
    return
  fi

  echo "==> CRIU: starting testing live-migration"
  incus_remote launch testimage l1:migratee -c raw.lxc=lxc.console.path=none

  # Wait for the container to be done booting
  sleep 1

  # Test stateful stop
  incus_remote stop --stateful l1:migratee
  incus_remote start l1:migratee

  # Test stateful snapshots
  # There is apparently a bug in CRIU that prevents checkpointing an instance that has been started from a
  # checkpoint. So stop instance first before taking stateful snapshot.
  incus_remote stop -f l1:migratee
  incus_remote start l1:migratee
  incus_remote snapshot create --stateful l1:migratee
  incus_remote snapshot restore l1:migratee snap0

  # Test live migration of container
  # There is apparently a bug in CRIU that prevents checkpointing an instance that has been started from a
  # checkpoint. So stop instance first before taking stateful snapshot.
  incus_remote stop -f l1:migratee
  incus_remote start l1:migratee
  incus_remote move l1:migratee l2:migratee

  # Test copy of stateful snapshot
  incus_remote copy l2:migratee/snap0 l1:migratee
  ! incus_remote copy l2:migratee/snap0 l1:migratee-new-name || false

  # Test stateless copies
  incus_remote copy --stateless l2:migratee/snap0 l1:migratee-new-name

  # Cleanup
  incus_remote delete --force l1:migratee
  incus_remote delete --force l2:migratee
  incus_remote delete --force l1:migratee-new-name
}
