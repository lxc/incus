test_migration() {
  # setup a second LXD
  # shellcheck disable=2039,3043
  local INCUS2_DIR INCUS2_ADDR incus_backend
  # shellcheck disable=2153
  incus_backend=$(storage_backend "$INCUS_DIR")

  INCUS2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS2_DIR}"
  spawn_incus "${INCUS2_DIR}" true
  INCUS2_ADDR=$(cat "${INCUS2_DIR}/lxd.addr")

  # workaround for kernel/criu
  umount /sys/kernel/debug >/dev/null 2>&1 || true

  # shellcheck disable=2153
  inc_remote remote add l1 "${INCUS_ADDR}" --accept-certificate --password foo
  inc_remote remote add l2 "${INCUS2_ADDR}" --accept-certificate --password foo

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
    inc_remote storage create l1:"$storage_pool1" lvm lvm.use_thinpool=false size=1GiB volume.size=25MiB
    inc_remote profile device set l1:default root pool "$storage_pool1"

    inc_remote storage create l2:"$storage_pool2" lvm lvm.use_thinpool=false size=1GiB volume.size=25MiB
    inc_remote profile device set l2:default root pool "$storage_pool2"

    migration "$INCUS2_DIR"

    inc_remote profile device set l1:default root pool "incustest-$(basename "${INCUS_DIR}")"
    inc_remote profile device set l2:default root pool "incustest-$(basename "${INCUS2_DIR}")"

    inc_remote storage delete l1:"$storage_pool1"
    inc_remote storage delete l2:"$storage_pool2"
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
      inc_remote storage create l1:"$storage_pool1" zfs size=1GiB volume.zfs.block_mode=true volume.block.filesystem="${fs}"
      inc_remote profile device set l1:default root pool "$storage_pool1"

      inc_remote storage create l2:"$storage_pool2" zfs size=1GiB volume.zfs.block_mode=true volume.block.filesystem="${fs}"
      inc_remote profile device set l2:default root pool "$storage_pool2"

      migration "$INCUS2_DIR"

      inc_remote profile device set l1:default root pool "incustest-$(basename "${INCUS_DIR}")"
      inc_remote profile device set l2:default root pool "incustest-$(basename "${INCUS2_DIR}")"

      inc_remote storage delete l1:"$storage_pool1"
      inc_remote storage delete l2:"$storage_pool2"
    done
  fi

  inc_remote remote remove l1
  inc_remote remote remove l2
  kill_incus "$INCUS2_DIR"
}

migration() {
  # shellcheck disable=2039,3043
  local lxd2_dir incus_backend lxd2_backend
  lxd2_dir="$1"
  incus_backend=$(storage_backend "$INCUS_DIR")
  lxd2_backend=$(storage_backend "$lxd2_dir")
  ensure_import_testimage

  inc_remote init testimage nonlive
  # test moving snapshots
  inc_remote config set l1:nonlive user.tester foo
  inc_remote snapshot l1:nonlive
  inc_remote config unset l1:nonlive user.tester
  inc_remote move l1:nonlive l2:
  inc_remote config show l2:nonlive/snap0 | grep user.tester | grep foo

  # This line exists so that the container's storage volume is mounted when we
  # perform existence check for various files.
  inc_remote start l2:nonlive
  # FIXME: make this backend agnostic
  if [ "$lxd2_backend" != "lvm" ] && [ "$lxd2_backend" != "zfs" ] && [ "$lxd2_backend" != "ceph" ]; then
    [ -d "${lxd2_dir}/containers/nonlive/rootfs" ]
  fi
  inc_remote stop l2:nonlive --force

  [ ! -d "${INCUS_DIR}/containers/nonlive" ]
  # FIXME: make this backend agnostic
  if [ "$lxd2_backend" = "dir" ]; then
    [ -d "${lxd2_dir}/snapshots/nonlive/snap0/rootfs/bin" ]
  fi

  inc_remote copy l2:nonlive l1:nonlive2 --mode=push
  # This line exists so that the container's storage volume is mounted when we
  # perform existence check for various files.
  inc_remote start l2:nonlive
  [ -d "${INCUS_DIR}/containers/nonlive2" ]
  # FIXME: make this backend agnostic
  if [ "$lxd2_backend" != "lvm" ] && [ "$lxd2_backend" != "zfs" ] && [ "$lxd2_backend" != "ceph" ]; then
    [ -d "${lxd2_dir}/containers/nonlive/rootfs/bin" ]
  fi

  # FIXME: make this backend agnostic
  if [ "$incus_backend" = "dir" ]; then
    [ -d "${INCUS_DIR}/snapshots/nonlive2/snap0/rootfs/bin" ]
  fi

  inc_remote copy l1:nonlive2/snap0 l2:nonlive3 --mode=relay
  # FIXME: make this backend agnostic
  if [ "$lxd2_backend" != "lvm" ] && [ "$lxd2_backend" != "zfs" ] && [ "$lxd2_backend" != "ceph" ]; then
    [ -d "${lxd2_dir}/containers/nonlive3/rootfs/bin" ]
  fi
  inc_remote delete l2:nonlive3 --force

  inc_remote stop l2:nonlive --force
  inc_remote copy l2:nonlive l2:nonlive2 --mode=push
  # should have the same base image tag
  [ "$(inc_remote config get l2:nonlive volatile.base_image)" = "$(inc_remote config get l2:nonlive2 volatile.base_image)" ]
  # check that nonlive2 has a new addr in volatile
  [ "$(inc_remote config get l2:nonlive volatile.eth0.hwaddr)" != "$(inc_remote config get l2:nonlive2 volatile.eth0.hwaddr)" ]

  inc_remote config unset l2:nonlive volatile.base_image
  inc_remote copy l2:nonlive l1:nobase
  inc_remote delete l1:nobase

  inc_remote start l1:nonlive2
  inc_remote list l1: | grep RUNNING | grep nonlive2
  inc_remote delete l1:nonlive2 l2:nonlive2 --force

  inc_remote launch testimage cccp
  inc_remote copy l1:cccp l2:udssr --stateless
  inc_remote delete l2:udssr --force
  inc_remote copy l1:cccp l2:udssr --stateless --mode=push
  inc_remote delete l2:udssr --force
  inc_remote copy l1:cccp l2:udssr --stateless --mode=relay
  inc_remote delete l2:udssr --force

  inc_remote move l1:cccp l2:udssr --stateless
  inc_remote delete l2:udssr --force
  inc_remote launch testimage cccp
  inc_remote move l1:cccp l2:udssr --stateless --mode=push
  inc_remote delete l2:udssr --force
  inc_remote launch testimage cccp
  inc_remote move l1:cccp l2:udssr --stateless --mode=relay
  inc_remote delete l2:udssr --force

  inc_remote start l2:nonlive
  inc_remote list l2: | grep RUNNING | grep nonlive
  inc_remote delete l2:nonlive --force

  # Get container's pool.
  pool=$(lxc config profile device get default root pool)
  remote_pool=$(inc_remote config profile device get l2:default root pool)

  # Test container only copies
  lxc init testimage cccp

  lxc storage volume set "${pool}" container/cccp user.foo=snap0
  echo "before" | lxc file push - cccp/blah
  lxc snapshot cccp
  lxc storage volume set "${pool}" container/cccp user.foo=snap1
  lxc snapshot cccp
  echo "after" | lxc file push - cccp/blah
  lxc storage volume set "${pool}" container/cccp user.foo=postsnap1

  # Check storage volume creation times are set.
  lxc query /1.0/storage-pools/"${pool}"/volumes/container/cccp | jq .created_at | grep -Fv '0001-01-01T00:00:00Z'
  lxc query /1.0/storage-pools/"${pool}"/volumes/container/cccp/snapshots/snap0 | jq .created_at | grep -Fv '0001-01-01T00:00:00Z'

  # Local container only copy.
  lxc copy cccp udssr --instance-only
  [ "$(lxc info udssr | grep -c snap)" -eq 0 ]
  [ "$(lxc file pull udssr/blah -)" = "after" ]
  lxc delete udssr

  # Local container with snapshots copy.
  lxc copy cccp udssr
  [ "$(lxc info udssr | grep -c snap)" -eq 2 ]
  [ "$(lxc file pull udssr/blah -)" = "after" ]
  lxc storage volume show "${pool}" container/udssr
  lxc storage volume get "${pool}" container/udssr user.foo | grep -Fx "postsnap1"
  lxc storage volume get "${pool}" container/udssr/snap0 user.foo | grep -Fx "snap0"
  lxc storage volume get "${pool}" container/udssr/snap1 user.foo | grep -Fx "snap1"
  lxc delete udssr

  # Remote container only copy.
  inc_remote copy l1:cccp l2:udssr --instance-only
  [ "$(inc_remote info l2:udssr | grep -c snap)" -eq 0 ]
  [ "$(inc_remote file pull l2:udssr/blah -)" = "after" ]
  inc_remote delete l2:udssr

  # Remote container with snapshots copy.
  inc_remote copy l1:cccp l2:udssr
  [ "$(inc_remote info l2:udssr | grep -c snap)" -eq 2 ]
  [ "$(inc_remote file pull l2:udssr/blah -)" = "after" ]
  inc_remote storage volume show l2:"${remote_pool}" container/udssr
  inc_remote storage volume get l2:"${remote_pool}" container/udssr user.foo | grep -Fx "postsnap1"
  inc_remote storage volume get l2:"${remote_pool}" container/udssr/snap0 user.foo | grep -Fx "snap0"
  inc_remote storage volume get l2:"${remote_pool}" container/udssr/snap1 user.foo | grep -Fx "snap1"
  inc_remote delete l2:udssr

  # Remote container only move.
  inc_remote move l1:cccp l2:udssr --instance-only --mode=relay
  ! inc_remote info l1:cccp || false
  [ "$(inc_remote info l2:udssr | grep -c snap)" -eq 0 ]
  inc_remote delete l2:udssr

  inc_remote init testimage l1:cccp
  inc_remote snapshot l1:cccp
  inc_remote snapshot l1:cccp

  # Remote container with snapshots move.
  inc_remote move l1:cccp l2:udssr --mode=push
  ! inc_remote info l1:cccp || false
  [ "$(inc_remote info l2:udssr | grep -c snap)" -eq 2 ]
  inc_remote delete l2:udssr

  # Test container only copies
  lxc init testimage cccp
  lxc snapshot cccp
  lxc snapshot cccp

  # Local container with snapshots move.
  lxc move cccp udssr --mode=pull
  ! lxc info cccp || false
  [ "$(lxc info udssr | grep -c snap)" -eq 2 ]
  lxc delete udssr

  if [ "$incus_backend" = "zfs" ]; then
    # Test container only copies when zfs.clone_copy is set to false.
    lxc storage set "incustest-$(basename "${INCUS_DIR}")" zfs.clone_copy false
    lxc init testimage cccp
    lxc snapshot cccp
    lxc snapshot cccp

    # Test container only copies when zfs.clone_copy is set to false.
    lxc copy cccp udssr --instance-only
    [ "$(lxc info udssr | grep -c snap)" -eq 0 ]
    lxc delete udssr

    # Test container with snapshots copy when zfs.clone_copy is set to false.
    lxc copy cccp udssr
    [ "$(lxc info udssr | grep -c snap)" -eq 2 ]
    lxc delete cccp
    lxc delete udssr

    lxc storage unset "incustest-$(basename "${INCUS_DIR}")" zfs.clone_copy
  fi

  inc_remote init testimage l1:c1
  inc_remote copy l1:c1 l2:c2
  inc_remote copy l1:c1 l2:c2 --refresh

  inc_remote start l1:c1 l2:c2

  # Make sure the testfile doesn't exist
  ! lxc file pull l1:c1 -- /root/testfile1 || false
  ! lxc file pull l2:c2 -- /root/testfile1 || false

  #inc_remote start l1:c1 l2:c2

  # Containers may not be running when refreshing
  ! inc_remote copy l1:c1 l2:c2 --refresh || false

  # Create test file in c1
  echo test | inc_remote file push - l1:c1/root/testfile1

  inc_remote stop -f l1:c1 l2:c2

  # Refresh the container and validate the contents
  inc_remote copy l1:c1 l2:c2 --refresh
  inc_remote start l2:c2
  inc_remote file pull l2:c2/root/testfile1 .
  rm testfile1
  inc_remote stop -f l2:c2

  # This will create snapshot c1/snap0 with test device and expiry date.
  inc_remote config device add l1:c1 testsnapdev none
  inc_remote config set l1:c1 snapshots.expiry '1d'
  inc_remote snapshot l1:c1
  inc_remote config device remove l1:c1 testsnapdev
  inc_remote config device add l1:c1 testdev none

  # Remove the testfile from c1 and refresh again
  inc_remote file delete l1:c1/root/testfile1
  inc_remote copy l1:c1 l2:c2 --refresh --instance-only
  inc_remote start l2:c2
  ! inc_remote file pull l2:c2/root/testfile1 . || false
  inc_remote stop -f l2:c2

  # Check whether snapshot c2/snap0 has been created with its config intact.
  ! inc_remote config show l2:c2/snap0 || false
  inc_remote copy l1:c1 l2:c2 --refresh
  inc_remote ls l2:
  inc_remote config show l2:c2/snap0
  ! inc_remote config show l2:c2/snap0 | grep -q 'expires_at: 0001-01-01T00:00:00Z' || false
  inc_remote config device get l2:c2 testdev type | grep -q 'none'
  inc_remote restore l2:c2 snap0
  inc_remote config device get l2:c2 testsnapdev type | grep -q 'none'

  # This will create snapshot c2/snap1
  inc_remote snapshot l2:c2
  inc_remote config show l2:c2/snap1

  # This should remove c2/snap1
  inc_remote copy l1:c1 l2:c2 --refresh
  ! inc_remote config show l2:c2/snap1 || false

  inc_remote rm -f l1:c1 l2:c2

  remote_pool1="incustest-$(basename "${INCUS_DIR}")"
  remote_pool2="incustest-$(basename "${lxd2_dir}")"

  inc_remote storage volume create l1:"$remote_pool1" vol1
  inc_remote storage volume set l1:"$remote_pool1" vol1 user.foo=snap0vol1
  inc_remote storage volume snapshot l1:"$remote_pool1" vol1
  inc_remote storage volume set l1:"$remote_pool1" vol1 user.foo=snap1vol1
  inc_remote storage volume snapshot l1:"$remote_pool1" vol1
  inc_remote storage volume set l1:"$remote_pool1" vol1 user.foo=postsnap1vol1

  # remote storage volume and snapshots migration in "pull" mode
  inc_remote storage volume copy l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol2"
  inc_remote storage volume get l2:"$remote_pool2" vol2 user.foo | grep -Fx "postsnap1vol1"
  inc_remote storage volume get l2:"$remote_pool2" vol2/snap0 user.foo | grep -Fx "snap0vol1"
  inc_remote storage volume get l2:"$remote_pool2" vol2/snap1 user.foo | grep -Fx "snap1vol1"
  inc_remote storage volume delete l2:"$remote_pool2" vol2

  # check moving volume and snapshots.
  inc_remote storage volume copy l1:"$remote_pool1/vol1" l1:"$remote_pool1/vol2"
  inc_remote storage volume move l1:"$remote_pool1/vol2" l2:"$remote_pool2/vol3"
  ! inc_remote storage volume show l1:"$remote_pool1" vol2 || false
  inc_remote storage volume get l2:"$remote_pool2" vol3 user.foo | grep -Fx "postsnap1vol1"
  inc_remote storage volume get l2:"$remote_pool2" vol3/snap0 user.foo | grep -Fx "snap0vol1"
  inc_remote storage volume get l2:"$remote_pool2" vol3/snap1 user.foo | grep -Fx "snap1vol1"
  inc_remote storage volume delete l2:"$remote_pool2" vol3

  inc_remote storage volume copy l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol2" --volume-only
  inc_remote storage volume get l2:"$remote_pool2" vol2 user.foo | grep -Fx "postsnap1vol1"
  ! inc_remote storage volume show l2:"$remote_pool2" vol2/snap0 || false
  ! inc_remote storage volume show l2:"$remote_pool2" vol2/snap1 || false
  inc_remote storage volume delete l2:"$remote_pool2" vol2

  # remote storage volume and snapshots migration refresh in "pull" mode
  inc_remote storage volume set l1:"$remote_pool1" vol1 user.foo=snapremovevol1
  inc_remote storage volume snapshot l1:"$remote_pool1" vol1 snapremove
  inc_remote storage volume set l1:"$remote_pool1" vol1 user.foo=postsnap1vol1
  inc_remote storage volume copy l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol2" --refresh
  inc_remote storage volume delete l1:"$remote_pool1" vol1

  inc_remote storage volume get l2:"$remote_pool2" vol2 user.foo | grep -Fx "postsnap1vol1"
  inc_remote storage volume get l2:"$remote_pool2" vol2/snap0 user.foo | grep -Fx "snap0vol1"
  inc_remote storage volume get l2:"$remote_pool2" vol2/snap1 user.foo | grep -Fx "snap1vol1"
  inc_remote storage volume get l2:"$remote_pool2" vol2/snapremove user.foo | grep -Fx "snapremovevol1"

  # check remote storage volume refresh from a different volume
  inc_remote storage volume create l1:"$remote_pool1" vol3
  inc_remote storage volume set l1:"$remote_pool1" vol3 user.foo=snap0vol3
  inc_remote storage volume snapshot l1:"$remote_pool1" vol3
  inc_remote storage volume set l1:"$remote_pool1" vol3 user.foo=snap1vol3
  inc_remote storage volume snapshot l1:"$remote_pool1" vol3
  inc_remote storage volume set l1:"$remote_pool1" vol3 user.foo=snap2vol3
  inc_remote storage volume snapshot l1:"$remote_pool1" vol3
  inc_remote storage volume set l1:"$remote_pool1" vol3 user.foo=postsnap1vol3

  # check snapshot volumes and snapshots are refreshed
  inc_remote storage volume copy l1:"$remote_pool1/vol3" l2:"$remote_pool2/vol2" --refresh
  inc_remote storage volume ls l2:"$remote_pool2"
  inc_remote storage volume delete l1:"$remote_pool1" vol3
  inc_remote storage volume get l2:"$remote_pool2" vol2 user.foo | grep -Fx "postsnap1vol1" # FIXME Should be postsnap1vol3
  inc_remote storage volume get l2:"$remote_pool2" vol2/snap0 user.foo | grep -Fx "snap0vol3"
  inc_remote storage volume get l2:"$remote_pool2" vol2/snap1 user.foo | grep -Fx "snap1vol3"
  inc_remote storage volume get l2:"$remote_pool2" vol2/snap2 user.foo | grep -Fx "snap2vol3"
  ! inc_remote storage volume show l2:"$remote_pool2" vol2/snapremove || false

  inc_remote storage volume delete l2:"$remote_pool2" vol2

  # remote storage volume migration in "push" mode
  inc_remote storage volume create l1:"$remote_pool1" vol1
  inc_remote storage volume create l1:"$remote_pool1" vol2
  inc_remote storage volume snapshot l1:"$remote_pool1" vol2

  inc_remote storage volume copy l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol2" --mode=push
  inc_remote storage volume move l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol3" --mode=push
  ! inc_remote storage volume list l1:"$remote_pool1/vol1" || false
  inc_remote storage volume copy l1:"$remote_pool1/vol2" l2:"$remote_pool2/vol4" --volume-only --mode=push
  inc_remote storage volume copy l1:"$remote_pool1/vol2" l2:"$remote_pool2/vol5" --mode=push
  inc_remote storage volume move l1:"$remote_pool1/vol2" l2:"$remote_pool2/vol6" --mode=push

  inc_remote storage volume delete l2:"$remote_pool2" vol2
  inc_remote storage volume delete l2:"$remote_pool2" vol3
  inc_remote storage volume delete l2:"$remote_pool2" vol4
  inc_remote storage volume delete l2:"$remote_pool2" vol5
  inc_remote storage volume delete l2:"$remote_pool2" vol6

  # remote storage volume migration in "relay" mode
  inc_remote storage volume create l1:"$remote_pool1" vol1
  inc_remote storage volume create l1:"$remote_pool1" vol2
  inc_remote storage volume snapshot l1:"$remote_pool1" vol2

  inc_remote storage volume copy l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol2" --mode=relay
  inc_remote storage volume move l1:"$remote_pool1/vol1" l2:"$remote_pool2/vol3" --mode=relay
  ! inc_remote storage volume list l1:"$remote_pool1/vol1" || false
  inc_remote storage volume copy l1:"$remote_pool1/vol2" l2:"$remote_pool2/vol4" --volume-only --mode=relay
  inc_remote storage volume copy l1:"$remote_pool1/vol2" l2:"$remote_pool2/vol5" --mode=relay
  inc_remote storage volume move l1:"$remote_pool1/vol2" l2:"$remote_pool2/vol6" --mode=relay

  inc_remote storage volume delete l2:"$remote_pool2" vol2
  inc_remote storage volume delete l2:"$remote_pool2" vol3
  inc_remote storage volume delete l2:"$remote_pool2" vol4
  inc_remote storage volume delete l2:"$remote_pool2" vol5
  inc_remote storage volume delete l2:"$remote_pool2" vol6

  # Test migration when rsync compression is disabled
  inc_remote storage set l1:"$remote_pool1" rsync.compression false
  inc_remote storage volume create l1:"$remote_pool1" foo
  inc_remote storage volume copy l1:"$remote_pool1"/foo l2:"$remote_pool2"/bar
  inc_remote storage volume delete l1:"$remote_pool1" foo
  inc_remote storage volume delete l2:"$remote_pool2" bar
  inc_remote storage unset l1:"$remote_pool1" rsync.compression

  # Test some migration between projects
  inc_remote project create l1:proj -c features.images=false -c features.profiles=false
  inc_remote project switch l1:proj

  inc_remote init testimage l1:c1
  inc_remote copy l1:c1 l2:
  inc_remote start l2:c1
  inc_remote delete l2:c1 -f

  inc_remote snapshot l1:c1
  inc_remote snapshot l1:c1
  inc_remote snapshot l1:c1
  inc_remote copy l1:c1 l2:
  inc_remote start l2:c1
  inc_remote stop l2:c1 -f
  inc_remote delete l1:c1

  inc_remote copy l2:c1 l1:
  inc_remote start l1:c1
  inc_remote delete l1:c1 -f

  inc_remote delete l2:c1/snap0
  inc_remote delete l2:c1/snap1
  inc_remote delete l2:c1/snap2
  inc_remote copy l2:c1 l1:
  inc_remote start l1:c1
  inc_remote delete l1:c1 -f
  inc_remote delete l2:c1 -f

  inc_remote project switch l1:default
  inc_remote project delete l1:proj


  # Check migration with invalid snapshot config (disks attached with missing source pool and source path).
  inc_remote init testimage l1:c1
  inc_remote storage create l1:dir dir
  inc_remote storage volume create l1:dir vol1
  inc_remote storage volume attach l1:dir vol1 c1 /mnt
  mkdir "$INCUS_DIR/testvol2"
  inc_remote config device add l1:c1 vol2 disk source="$INCUS_DIR/testvol2" path=/vol2
  inc_remote snapshot l1:c1 # Take snapshot with disk devices still attached.
  inc_remote config device remove c1 vol1
  inc_remote config device remove c1 vol2
  rmdir "$INCUS_DIR/testvol2"
  inc_remote copy l1:c1 l2:
  inc_remote info l2:c1 | grep snap0
  inc_remote delete l1:c1 -f
  inc_remote delete l2:c1 -f
  inc_remote storage volume delete l1:dir vol1
  inc_remote storage delete l1:dir

  # Test optimized refresh
  inc_remote init testimage l1:c1
  echo test | inc_remote file push - l1:c1/tmp/foo
  inc_remote copy l1:c1 l2:c1
  inc_remote file pull l2:c1/tmp/foo .
  inc_remote snapshot l1:c1
  echo test | inc_remote file push - l1:c1/tmp/bar
  inc_remote copy l1:c1 l2:c1 --refresh
  inc_remote start l2:c1
  inc_remote file pull l2:c1/tmp/foo .
  inc_remote file pull l2:c1/tmp/bar .
  inc_remote stop l2:c1 -f

  inc_remote restore l2:c1 snap0
  inc_remote start l2:c1
  inc_remote file pull l2:c1/tmp/foo .
  ! inc_remote file pull l2:c1/tmp/bar . ||  false
  inc_remote stop l2:c1 -f

  rm foo bar

  inc_remote rm l1:c1
  inc_remote rm l2:c1

  inc_remote init testimage l1:c1
  # This creates snap0
  inc_remote snapshot l1:c1
  # This creates snap1
  inc_remote snapshot l1:c1
  inc_remote copy l1:c1 l2:c1
  # This creates snap2
  inc_remote snapshot l1:c1

  # Delete first snapshot from target
  inc_remote rm l2:c1/snap0

  # Refresh
  inc_remote copy l1:c1 l2:c1 --refresh

  inc_remote rm -f l1:c1
  inc_remote rm -f l2:c1

  # In this scenario the source LXD server used to crash due to a missing slice check.
  # Let's test this to make sure it doesn't happen again.
  inc_remote init testimage l1:c1
  inc_remote copy l1:c1 l2:c1
  inc_remote snapshot l1:c1
  inc_remote snapshot l1:c1

  inc_remote copy l1:c1 l2:c1 --refresh
  inc_remote copy l1:c1 l2:c1 --refresh

  inc_remote rm -f l1:c1
  inc_remote rm -f l2:c1

  # On btrfs, this used to cause a failure because btrfs couldn't find the parent subvolume.
  inc_remote init testimage l1:c1
  inc_remote copy l1:c1 l2:c1
  inc_remote snapshot l1:c1
  inc_remote copy l1:c1 l2:c1 --refresh
  inc_remote snapshot l1:c1
  inc_remote copy l1:c1 l2:c1 --refresh

  inc_remote rm -f l1:c1
  inc_remote rm -f l2:c1

  # On zfs, this used to crash due to a websocket read issue.
  lxc launch testimage c1
  lxc snapshot c1
  lxc copy c1 l2:c1 --stateless
  lxc copy c1 l2:c1 --stateless --refresh

  inc_remote rm -f l1:c1
  inc_remote rm -f l2:c1

  # migrate ISO custom volumes
  truncate -s 25MiB foo.iso
  lxc storage volume import l1:"${pool}" ./foo.iso iso1
  lxc storage volume copy l1:"${pool}"/iso1 l2:"${remote_pool}"/iso1

  lxc storage volume show l2:"${remote_pool}" iso1 | grep -q 'content_type: iso'
  lxc storage volume move l1:"${pool}"/iso1 l2:"${remote_pool}"/iso2
  lxc storage volume show l2:"${remote_pool}" iso2 | grep -q 'content_type: iso'
  ! lxc storage volume show l1:"${pool}" iso1 || false

  lxc storage volume delete l2:"${remote_pool}" iso1
  lxc storage volume delete l2:"${remote_pool}" iso2
  rm -f foo.iso

  if ! command -v criu >/dev/null 2>&1; then
    echo "==> SKIP: live migration with CRIU (missing binary)"
    return
  fi

  echo "==> CRIU: starting testing live-migration"
  inc_remote launch testimage l1:migratee -c raw.lxc=lxc.console.path=none

  # Wait for the container to be done booting
  sleep 1

  # Test stateful stop
  inc_remote stop --stateful l1:migratee
  inc_remote start l1:migratee

  # Test stateful snapshots
  # There is apparently a bug in CRIU that prevents checkpointing an instance that has been started from a
  # checkpoint. So stop instance first before taking stateful snapshot.
  inc_remote stop -f l1:migratee
  inc_remote start l1:migratee
  inc_remote snapshot --stateful l1:migratee
  inc_remote restore l1:migratee snap0

  # Test live migration of container
  # There is apparently a bug in CRIU that prevents checkpointing an instance that has been started from a
  # checkpoint. So stop instance first before taking stateful snapshot.
  inc_remote stop -f l1:migratee
  inc_remote start l1:migratee
  inc_remote move l1:migratee l2:migratee

  # Test copy of stateful snapshot
  inc_remote copy l2:migratee/snap0 l1:migratee
  ! inc_remote copy l2:migratee/snap0 l1:migratee-new-name || false

  # Test stateless copies
  inc_remote copy --stateless l2:migratee/snap0 l1:migratee-new-name

  # Cleanup
  inc_remote delete --force l1:migratee
  inc_remote delete --force l2:migratee
  inc_remote delete --force l1:migratee-new-name
}
