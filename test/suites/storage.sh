test_storage() {
  ensure_import_testimage

  # shellcheck disable=2039,3043
  local INCUS_STORAGE_DIR incus_backend

  incus_backend=$(storage_backend "$INCUS_DIR")
  INCUS_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  chmod +x "${INCUS_STORAGE_DIR}"
  spawn_incus "${INCUS_STORAGE_DIR}" false

  # edit storage and pool description
  # shellcheck disable=2039,3043
  local storage_pool storage_volume
  storage_pool="incustest-$(basename "${INCUS_DIR}")-pool"
  storage_volume="${storage_pool}-vol"
  incus storage create "$storage_pool" "$incus_backend" --description foo
  incus storage show "$storage_pool" | grep -q 'description: foo'
  incus storage show "$storage_pool" | sed 's/^description:.*/description: bar/' | incus storage edit "$storage_pool"
  incus storage show "$storage_pool" | grep -q 'description: bar'

  # Create a storage volume with a description
  incus storage volume create "$storage_pool" "$storage_volume" --description foo
  incus storage volume show "$storage_pool" "$storage_volume" | grep -q 'description: foo'

  # Test setting description on a storage volume
  incus storage volume show "$storage_pool" "$storage_volume" | sed 's/^description:.*/description: bar/' | incus storage volume edit "$storage_pool" "$storage_volume"
  incus storage volume show "$storage_pool" "$storage_volume" | grep -q 'description: bar'

  # Validate get/set
  incus storage set "$storage_pool" user.abc def
  [ "$(incus storage get "$storage_pool" user.abc)" = "def" ]

  incus storage volume set "$storage_pool" "$storage_volume" user.abc def
  [ "$(incus storage volume get "$storage_pool" "$storage_volume" user.abc)" = "def" ]

  incus storage volume delete "$storage_pool" "$storage_volume"

  # Test copying pool volume.* key to the volume with prefix stripped at volume creation time
  incus storage set "$storage_pool" volume.snapshots.expiry 3d
  incus storage volume create "$storage_pool" "$storage_volume"
  [ "$(incus storage volume get "$storage_pool" "$storage_volume" snapshots.expiry)" = "3d" ]
  incus storage volume delete "$storage_pool" "$storage_volume"

  incus storage delete "$storage_pool"

  # Test btrfs resize
  if [ "$incus_backend" = "lvm" ] || [ "$incus_backend" = "ceph" ]; then
      # shellcheck disable=2039,3043
      local btrfs_storage_pool btrfs_storage_volume
      btrfs_storage_pool="incustest-$(basename "${INCUS_DIR}")-pool-btrfs"
      btrfs_storage_volume="${storage_pool}-vol"
      incus storage create "$btrfs_storage_pool" "$incus_backend" volume.block.filesystem=btrfs volume.size=200MiB
      incus storage volume create "$btrfs_storage_pool" "$btrfs_storage_volume"
      incus storage volume show "$btrfs_storage_pool" "$btrfs_storage_volume"
      incus storage volume set "$btrfs_storage_pool" "$btrfs_storage_volume" size 256MiB
      incus storage volume delete "$btrfs_storage_pool" "$btrfs_storage_volume"

      # Test generation of unique UUID.
      incus init testimage uuid1 -s "incustest-$(basename "${INCUS_DIR}")-pool-btrfs"
      POOL="incustest-$(basename "${INCUS_DIR}")-pool-btrfs"
      incus copy uuid1 uuid2
      incus start uuid1
      incus start uuid2
      if [ "$incus_backend" = "lvm" ]; then
        [ "$(blkid -s UUID -o value -p /dev/"${POOL}"/containers_uuid1)" != "$(blkid -s UUID -o value -p /dev/"${POOL}"/containers_uuid2)" ]
      elif [ "$incus_backend" = "ceph" ]; then
        [ "$(blkid -s UUID -o value -p /dev/rbd/"${POOL}"/container_uuid1)" != "$(blkid -s UUID -o value -p /dev/rbd/"${POOL}"/container_uuid2)" ]
      fi
      incus delete --force uuid1
      incus delete --force uuid2
      incus image delete testimage

      incus storage delete "$btrfs_storage_pool"
  fi
  ensure_import_testimage

  (
    set -e
    # shellcheck disable=2030
    INCUS_DIR="${INCUS_STORAGE_DIR}"

    # shellcheck disable=SC1009
    if [ "$incus_backend" = "zfs" ]; then
    # Create loop file zfs pool.
      incus storage create "incustest-$(basename "${INCUS_DIR}")-pool1" zfs

      # Check that we can't create a loop file in a non-Incus owned location.
      INVALID_LOOP_FILE="$(mktemp -p "${INCUS_DIR}" XXXXXXXXX)-invalid-loop-file"
      ! incus storage create "incustest-$(basename "${INCUS_DIR}")-pool1" zfs source="${INVALID_LOOP_FILE}" || false

      # Let Incus use an already existing dataset.
      zfs create -p -o mountpoint=none "incustest-$(basename "${INCUS_DIR}")-pool1/existing-dataset-as-pool"
      incus storage create "incustest-$(basename "${INCUS_DIR}")-pool7" zfs source="incustest-$(basename "${INCUS_DIR}")-pool1/existing-dataset-as-pool"

      # Let Incus use an already existing storage pool.
      configure_loop_device loop_file_4 loop_device_4
      # shellcheck disable=SC2154
      zpool create -f -m none -O compression=on "incustest-$(basename "${INCUS_DIR}")-pool9-existing-pool" "${loop_device_4}"
      incus storage create "incustest-$(basename "${INCUS_DIR}")-pool9" zfs source="incustest-$(basename "${INCUS_DIR}")-pool9-existing-pool"

      # Let Incus create a new dataset and use as pool.
      incus storage create "incustest-$(basename "${INCUS_DIR}")-pool8" zfs source="incustest-$(basename "${INCUS_DIR}")-pool1/non-existing-dataset-as-pool"

      # Create device backed zfs pool
      configure_loop_device loop_file_1 loop_device_1
      # shellcheck disable=SC2154
      incus storage create "incustest-$(basename "${INCUS_DIR}")-pool2" zfs source="${loop_device_1}"

      # Test that no invalid zfs storage pool configuration keys can be set.
      ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-zfs-pool-config" zfs lvm.thinpool_name=bla || false
      ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-zfs-pool-config" zfs lvm.use_thinpool=false || false
      ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-zfs-pool-config" zfs lvm.vg_name=bla || false

      # Test that all valid zfs storage pool configuration keys can be set.
      incus storage create "incustest-$(basename "${INCUS_DIR}")-valid-zfs-pool-config" zfs volume.zfs.remove_snapshots=true
      incus storage delete "incustest-$(basename "${INCUS_DIR}")-valid-zfs-pool-config"

      incus storage create "incustest-$(basename "${INCUS_DIR}")-valid-zfs-pool-config" zfs volume.zfs.use_refquota=true
      incus storage delete "incustest-$(basename "${INCUS_DIR}")-valid-zfs-pool-config"

      incus storage create "incustest-$(basename "${INCUS_DIR}")-valid-zfs-pool-config" zfs zfs.clone_copy=true
      incus storage delete "incustest-$(basename "${INCUS_DIR}")-valid-zfs-pool-config"

      incus storage create "incustest-$(basename "${INCUS_DIR}")-valid-zfs-pool-config" zfs zfs.pool_name="incustest-$(basename "${INCUS_DIR}")-valid-zfs-pool-config"
      incus storage delete "incustest-$(basename "${INCUS_DIR}")-valid-zfs-pool-config"

      incus storage create "incustest-$(basename "${INCUS_DIR}")-valid-zfs-pool-config" zfs rsync.bwlimit=1024
      incus storage delete "incustest-$(basename "${INCUS_DIR}")-valid-zfs-pool-config"
    fi

    if [ "$incus_backend" = "btrfs" ]; then
      # Create loop file btrfs pool.
      incus storage create "incustest-$(basename "${INCUS_DIR}")-pool3" btrfs

      # Create device backed btrfs pool.
      configure_loop_device loop_file_2 loop_device_2
      # shellcheck disable=SC2154
      incus storage create "incustest-$(basename "${INCUS_DIR}")-pool4" btrfs source="${loop_device_2}"

      # Check that we cannot create storage pools inside of ${INCUS_DIR} other than ${INCUS_DIR}/storage-pools/{pool_name}.
      ! incus storage create "incustest-$(basename "${INCUS_DIR}")-pool5_under_incus_dir" btrfs source="${INCUS_DIR}" || false

      # Test that no invalid btrfs storage pool configuration keys can be set.
      ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-btrfs-pool-config" btrfs lvm.thinpool_name=bla || false
      ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-btrfs-pool-config" btrfs lvm.use_thinpool=false || false
      ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-btrfs-pool-config" btrfs lvm.vg_name=bla || false
      ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-btrfs-pool-config" btrfs volume.block.filesystem=ext4 || false
      ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-btrfs-pool-config" btrfs volume.block.mount_options=discard || false
      ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-btrfs-pool-config" btrfs volume.zfs.remove_snapshots=true || false
      ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-btrfs-pool-config" btrfs volume.zfs.use_refquota=true || false
      ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-btrfs-pool-config" btrfs zfs.clone_copy=true || false
      ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-btrfs-pool-config" btrfs zfs.pool_name=bla || false

      incus storage create "incustest-$(basename "${INCUS_DIR}")-valid-btrfs-pool-config" btrfs rsync.bwlimit=1024
      incus storage delete "incustest-$(basename "${INCUS_DIR}")-valid-btrfs-pool-config"

      incus storage create "incustest-$(basename "${INCUS_DIR}")-valid-btrfs-pool-config" btrfs btrfs.mount_options="rw,strictatime,user_subvol_rm_allowed"
      incus storage set "incustest-$(basename "${INCUS_DIR}")-valid-btrfs-pool-config" btrfs.mount_options "rw,relatime,user_subvol_rm_allowed"
      incus storage delete "incustest-$(basename "${INCUS_DIR}")-valid-btrfs-pool-config"
    fi

    # Create dir pool.
    incus storage create "incustest-$(basename "${INCUS_DIR}")-pool5" dir

    # Check that we cannot create storage pools inside of ${INCUS_DIR} other than ${INCUS_DIR}/storage-pools/{pool_name}.
    ! incus storage create "incustest-$(basename "${INCUS_DIR}")-pool5_under_incus_dir" dir source="${INCUS_DIR}" || false

    # Check that we can create storage pools inside of ${INCUS_DIR}/storage-pools/{pool_name}.
    incus storage create "incustest-$(basename "${INCUS_DIR}")-pool5_under_incus_dir" dir source="${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool5_under_incus_dir"

    incus storage delete "incustest-$(basename "${INCUS_DIR}")-pool5_under_incus_dir"

    # Test that no invalid dir storage pool configuration keys can be set.
    ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-dir-pool-config" dir lvm.thinpool_name=bla || false
    ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-dir-pool-config" dir lvm.use_thinpool=false || false
    ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-dir-pool-config" dir lvm.vg_name=bla || false
    ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-dir-pool-config" dir size=1GiB || false
    ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-dir-pool-config" dir volume.block.filesystem=ext4 || false
    ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-dir-pool-config" dir volume.block.mount_options=discard || false
    ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-dir-pool-config" dir volume.zfs.remove_snapshots=true || false
    ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-dir-pool-config" dir volume.zfs.use_refquota=true || false
    ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-dir-pool-config" dir zfs.clone_copy=true || false
    ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-dir-pool-config" dir zfs.pool_name=bla || false

    incus storage create "incustest-$(basename "${INCUS_DIR}")-valid-dir-pool-config" dir rsync.bwlimit=1024
    incus storage delete "incustest-$(basename "${INCUS_DIR}")-valid-dir-pool-config"

    if [ "$incus_backend" = "lvm" ]; then
      # Create lvm pool.
      configure_loop_device loop_file_3 loop_device_3
      # shellcheck disable=SC2154
      incus storage create "incustest-$(basename "${INCUS_DIR}")-pool6" lvm source="${loop_device_3}" volume.size=25MiB

      configure_loop_device loop_file_5 loop_device_5
      # shellcheck disable=SC2154
      # Should fail if vg does not exist, since we have no way of knowing where
      # to create the vg without a block device path set.
      ! incus storage create "incustest-$(basename "${INCUS_DIR}")-pool10" lvm source=test_vg_1 volume.size=25MiB || false
      # shellcheck disable=SC2154
      deconfigure_loop_device "${loop_file_5}" "${loop_device_5}"

      configure_loop_device loop_file_6 loop_device_6
      # shellcheck disable=SC2154
      pvcreate "${loop_device_6}"
      vgcreate "incustest-$(basename "${INCUS_DIR}")-pool11-test_vg_2" "${loop_device_6}"
      # Reuse existing volume group "test_vg_2" on existing physical volume.
      incus storage create "incustest-$(basename "${INCUS_DIR}")-pool11" lvm source="incustest-$(basename "${INCUS_DIR}")-pool11-test_vg_2" volume.size=25MiB

      configure_loop_device loop_file_7 loop_device_7
      # shellcheck disable=SC2154
      pvcreate "${loop_device_7}"
      vgcreate "incustest-$(basename "${INCUS_DIR}")-pool12-test_vg_3" "${loop_device_7}"
      # Reuse existing volume group "test_vg_3" on existing physical volume.
      incus storage create "incustest-$(basename "${INCUS_DIR}")-pool12" lvm source="incustest-$(basename "${INCUS_DIR}")-pool12-test_vg_3" volume.size=25MiB

      configure_loop_device loop_file_8 loop_device_8
      # shellcheck disable=SC2154
      # Create new volume group "test_vg_4".
      incus storage create "incustest-$(basename "${INCUS_DIR}")-pool13" lvm source="${loop_device_8}" lvm.vg_name="incustest-$(basename "${INCUS_DIR}")-pool13-test_vg_4" volume.size=25MiB

      incus storage create "incustest-$(basename "${INCUS_DIR}")-pool14" lvm volume.size=25MiB

      incus storage create "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" lvm lvm.use_thinpool=false volume.size=25MiB

      # Test that no invalid lvm storage pool configuration keys can be set.
      ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-lvm-pool-config" lvm volume.zfs.remove_snapshots=true || false
      ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-lvm-pool-config" lvm volume.zfs_use_refquota=true || false
      ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-lvm-pool-config" lvm zfs.clone_copy=true || false
      ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-lvm-pool-config" lvm zfs.pool_name=bla || false
      ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-lvm-pool-config" lvm lvm.use_thinpool=false lvm.thinpool_name="incustest-$(basename "${INCUS_DIR}")-invalid-lvm-pool-config" || false

      # Test that all valid lvm storage pool configuration keys can be set.
      incus storage create "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool16" lvm lvm.thinpool_name="incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config"
      incus storage create "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool17" lvm lvm.vg_name="incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config"
      incus storage create "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool18" lvm size=1GiB
      incus storage create "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool19" lvm volume.block.filesystem=ext4
      incus storage create "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool20" lvm volume.block.mount_options=discard
      incus storage create "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool21" lvm volume.size=25MiB
      incus storage create "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool22" lvm lvm.use_thinpool=true
      incus storage create "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool23" lvm lvm.use_thinpool=true lvm.thinpool_name="incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config"
      incus storage create "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool24" lvm rsync.bwlimit=1024
      incus storage create "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool25" lvm volume.block.mount_options="rw,strictatime,discard"
      incus storage set "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool25" volume.block.mount_options "rw,lazytime"
      incus storage create "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool26" lvm volume.block.filesystem=btrfs
    fi

    # Set default storage pool for image import.
    incus profile device add default root disk path="/" pool="incustest-$(basename "${INCUS_DIR}")-pool5"

    # Import image into default storage pool.
    ensure_import_testimage

    # Muck around with some containers on various pools.
    if [ "$incus_backend" = "zfs" ]; then
      incus init testimage c1pool1 -s "incustest-$(basename "${INCUS_DIR}")-pool1"
      incus list -c b c1pool1 | grep "incustest-$(basename "${INCUS_DIR}")-pool1"

      incus init testimage c2pool2 -s "incustest-$(basename "${INCUS_DIR}")-pool2"
      incus list -c b c2pool2 | grep "incustest-$(basename "${INCUS_DIR}")-pool2"

      incus launch testimage c3pool1 -s "incustest-$(basename "${INCUS_DIR}")-pool1"
      incus list -c b c3pool1 | grep "incustest-$(basename "${INCUS_DIR}")-pool1"

      incus launch testimage c4pool2 -s "incustest-$(basename "${INCUS_DIR}")-pool2"
      incus list -c b c4pool2 | grep "incustest-$(basename "${INCUS_DIR}")-pool2"

      incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool1" c1pool1
      incus storage volume set "incustest-$(basename "${INCUS_DIR}")-pool1" c1pool1 zfs.use_refquota true
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool1" c1pool1 c1pool1 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool1" c1pool1 c1pool1 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool1" c1pool1 c1pool1
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool1" custom/c1pool1 c1pool1 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool1" custom/c1pool1 c1pool1 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool1" c1pool1 c1pool1

      incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool1" c2pool2
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool1" c2pool2 c2pool2 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool1" c2pool2 c2pool2 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool1" c2pool2 c2pool2
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool1" custom/c2pool2 c2pool2 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool1" custom/c2pool2 c2pool2 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool1" c2pool2 c2pool2

      incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool2" c3pool1
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool2" c3pool1 c3pool1 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool2" c3pool1 c3pool1 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool2" c3pool1 c3pool1
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool2" c3pool1 c3pool1 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool2" c3pool1 c3pool1 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool2" c3pool1 c3pool1

      incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool2" c4pool2
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool2" c4pool2 c4pool2 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool2" c4pool2 c4pool2 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool2" c4pool2 c4pool2
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool2" custom/c4pool2 c4pool2 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool2" custom/c4pool2 c4pool2 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool2" c4pool2 c4pool2
      incus storage volume rename "incustest-$(basename "${INCUS_DIR}")-pool2" c4pool2 c4pool2-renamed
      incus storage volume rename "incustest-$(basename "${INCUS_DIR}")-pool2" c4pool2-renamed c4pool2
    fi

    if [ "$incus_backend" = "btrfs" ]; then
      incus init testimage c5pool3 -s "incustest-$(basename "${INCUS_DIR}")-pool3"
      incus list -c b c5pool3 | grep "incustest-$(basename "${INCUS_DIR}")-pool3"
      incus init testimage c6pool4 -s "incustest-$(basename "${INCUS_DIR}")-pool4"
      incus list -c b c6pool4 | grep "incustest-$(basename "${INCUS_DIR}")-pool4"

      incus launch testimage c7pool3 -s "incustest-$(basename "${INCUS_DIR}")-pool3"
      incus list -c b c7pool3 | grep "incustest-$(basename "${INCUS_DIR}")-pool3"
      incus launch testimage c8pool4 -s "incustest-$(basename "${INCUS_DIR}")-pool4"
      incus list -c b c8pool4 | grep "incustest-$(basename "${INCUS_DIR}")-pool4"

      incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool3" c5pool3
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool3" c5pool3 c5pool3 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool3" c5pool3 c5pool3 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool3" c5pool3 c5pool3 testDevice
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool3" custom/c5pool3 c5pool3 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool3" custom/c5pool3 c5pool3 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool3" c5pool3 c5pool3 testDevice

      incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool4" c6pool4
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool4" c6pool4 c5pool3 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool4" c6pool4 c5pool3 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool4" c6pool4 c5pool3 testDevice
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool4" custom/c6pool4 c5pool3 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool4" custom/c6pool4 c5pool3 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool4" c6pool4 c5pool3 testDevice

      incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool3" c7pool3
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool3" c7pool3 c7pool3 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool3" c7pool3 c7pool3 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool3" c7pool3 c7pool3 testDevice
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool3" custom/c7pool3 c7pool3 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool3" custom/c7pool3 c7pool3 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool3" c7pool3 c7pool3 testDevice

      incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool4" c8pool4
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool4" c8pool4 c8pool4 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool4" c8pool4 c8pool4 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool4" c8pool4 c8pool4 testDevice
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool4" custom/c8pool4 c8pool4 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool4" custom/c8pool4 c8pool4 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool4" c8pool4 c8pool4 testDevice
      incus storage volume rename "incustest-$(basename "${INCUS_DIR}")-pool4" c8pool4 c8pool4-renamed
      incus storage volume rename "incustest-$(basename "${INCUS_DIR}")-pool4" c8pool4-renamed c8pool4
    fi

    incus init testimage c9pool5 -s "incustest-$(basename "${INCUS_DIR}")-pool5"
    incus list -c b c9pool5 | grep "incustest-$(basename "${INCUS_DIR}")-pool5"

    incus launch testimage c11pool5 -s "incustest-$(basename "${INCUS_DIR}")-pool5"
    incus list -c b c11pool5 | grep "incustest-$(basename "${INCUS_DIR}")-pool5"

    incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool5" c9pool5
    incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool5" c9pool5 c9pool5 testDevice /opt
    ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool5" c9pool5 c9pool5 testDevice2 /opt || false
    incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool5" c9pool5 c9pool5 testDevice
    incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool5" custom/c9pool5 c9pool5 testDevice /opt
    ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool5" custom/c9pool5 c9pool5 testDevice2 /opt || false
    incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool5" c9pool5 c9pool5 testDevice

    incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool5" c11pool5
    incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool5" c11pool5 c11pool5 testDevice /opt
    ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool5" c11pool5 c11pool5 testDevice2 /opt || false
    incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool5" c11pool5 c11pool5 testDevice
    incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool5" custom/c11pool5 c11pool5 testDevice /opt
    ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool5" custom/c11pool5 c11pool5 testDevice2 /opt || false
    incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool5" c11pool5 c11pool5 testDevice
    incus storage volume rename "incustest-$(basename "${INCUS_DIR}")-pool5" c11pool5 c11pool5-renamed
    incus storage volume rename "incustest-$(basename "${INCUS_DIR}")-pool5" c11pool5-renamed c11pool5

    incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool5" c12pool5
    # should create snap0
    incus storage volume snapshot create "incustest-$(basename "${INCUS_DIR}")-pool5" c12pool5
    # should create snap1
    incus storage volume snapshot create "incustest-$(basename "${INCUS_DIR}")-pool5" c12pool5

    if [ "$incus_backend" = "lvm" ]; then
      incus init testimage c10pool6 -s "incustest-$(basename "${INCUS_DIR}")-pool6"
      incus list -c b c10pool6 | grep "incustest-$(basename "${INCUS_DIR}")-pool6"

      # Test if volume group renaming works by setting lvm.vg_name.
      incus storage set "incustest-$(basename "${INCUS_DIR}")-pool6" lvm.vg_name "incustest-$(basename "${INCUS_DIR}")-pool6-newName"

      incus storage set "incustest-$(basename "${INCUS_DIR}")-pool6" lvm.thinpool_name "incustest-$(basename "${INCUS_DIR}")-pool6-newThinpoolName"

      incus launch testimage c12pool6 -s "incustest-$(basename "${INCUS_DIR}")-pool6"
      incus list -c b c12pool6 | grep "incustest-$(basename "${INCUS_DIR}")-pool6"
      # grow lv
      incus config device set c12pool6 root size 30MiB
      incus restart c12pool6 --force
      # shrink lv
      incus config device set c12pool6 root size 25MiB
      incus restart c12pool6 --force

      incus init testimage c10pool11 -s "incustest-$(basename "${INCUS_DIR}")-pool11"
      incus list -c b c10pool11 | grep "incustest-$(basename "${INCUS_DIR}")-pool11"

      incus launch testimage c12pool11 -s "incustest-$(basename "${INCUS_DIR}")-pool11"
      incus list -c b c12pool11 | grep "incustest-$(basename "${INCUS_DIR}")-pool11"

      incus init testimage c10pool12 -s "incustest-$(basename "${INCUS_DIR}")-pool12"
      incus list -c b c10pool12 | grep "incustest-$(basename "${INCUS_DIR}")-pool12"

      incus launch testimage c12pool12 -s "incustest-$(basename "${INCUS_DIR}")-pool12"
      incus list -c b c12pool12 | grep "incustest-$(basename "${INCUS_DIR}")-pool12"

      incus init testimage c10pool13 -s "incustest-$(basename "${INCUS_DIR}")-pool13"
      incus list -c b c10pool13 | grep "incustest-$(basename "${INCUS_DIR}")-pool13"

      incus launch testimage c12pool13 -s "incustest-$(basename "${INCUS_DIR}")-pool13"
      incus list -c b c12pool13 | grep "incustest-$(basename "${INCUS_DIR}")-pool13"

      incus init testimage c10pool14 -s "incustest-$(basename "${INCUS_DIR}")-pool14"
      incus list -c b c10pool14 | grep "incustest-$(basename "${INCUS_DIR}")-pool14"

      incus launch testimage c12pool14 -s "incustest-$(basename "${INCUS_DIR}")-pool14"
      incus list -c b c12pool14 | grep "incustest-$(basename "${INCUS_DIR}")-pool14"

      incus init testimage c10pool15 -s "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15"
      incus list -c b c10pool15 | grep "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15"

      incus launch testimage c12pool15 -s "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15"
      incus list -c b c12pool15 | grep "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15"

      # Test that changing block filesystem works
      incus storage set "incustest-$(basename "${INCUS_DIR}")-pool6" volume.block.filesystem xfs
      incus init testimage c1pool6 -s "incustest-$(basename "${INCUS_DIR}")-pool6"
      incus storage set "incustest-$(basename "${INCUS_DIR}")-pool6" volume.block.filesystem btrfs
      incus storage set "incustest-$(basename "${INCUS_DIR}")-pool6" volume.size 120MiB
      incus init testimage c2pool6 -s "incustest-$(basename "${INCUS_DIR}")-pool6"

      incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool6" c10pool6
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool6" c10pool6 c10pool6 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool6" c10pool6 c10pool6 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool6" c10pool6 c10pool6 testDevice
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool6" custom/c10pool6 c10pool6 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool6" custom/c10pool6 c10pool6 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool6" c10pool6 c10pool6 testDevice

      incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool6" c12pool6
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool6" c12pool6 c12pool6 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool6" c12pool6 c12pool6 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool6" c12pool6 c12pool6 testDevice
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool6" custom/c12pool6 c12pool6 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool6" custom/c12pool6 c12pool6 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool6" c12pool6 c12pool6 testDevice

      incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool11" c10pool11
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool11" c10pool11 c10pool11 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool11" c10pool11 c10pool11 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool11" c10pool11 c10pool11 testDevice
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool11" custom/c10pool11 c10pool11 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool11" custom/c10pool11 c10pool11 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool11" c10pool11 c10pool11 testDevice

      incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool11" c12pool11
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool11" c12pool11 c10pool11 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool11" c12pool11 c10pool11 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool11" c12pool11 c10pool11 testDevice
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool11" custom/c12pool11 c10pool11 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool11" custom/c12pool11 c10pool11 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool11" c12pool11 c10pool11 testDevice

      incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool12" c10pool12
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool12" c10pool12 c10pool12 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool12" c10pool12 c10pool12 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool12" c10pool12 c10pool12 testDevice
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool12" custom/c10pool12 c10pool12 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool12" custom/c10pool12 c10pool12 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool12" c10pool12 c10pool12 testDevice

      incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool12" c12pool12
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool12" c12pool12 c12pool12 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool12" c12pool12 c12pool12 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool12" c12pool12 c12pool12 testDevice
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool12" custom/c12pool12 c12pool12 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool12" custom/c12pool12 c12pool12 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool12" c12pool12 c12pool12 testDevice

      incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool13" c10pool13
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool13" c10pool13 c10pool13 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool13" c10pool13 c10pool13 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool13" c10pool13 c10pool13 testDevice
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool13" custom/c10pool13 c10pool13 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool13" custom/c10pool13 c10pool13 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool13" c10pool13 c10pool13 testDevice

      incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool13" c12pool13
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool13" c12pool13 c12pool13 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool13" c12pool13 c12pool13 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool13" c12pool13 c12pool13 testDevice
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool13" custom/c12pool13 c12pool13 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool13" custom/c12pool13 c12pool13 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool13" c12pool13 c12pool13 testDevice

      incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool14" c10pool14
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool14" c10pool14 c10pool14 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool14" c10pool14 c10pool14 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool14" c10pool14 c10pool14 testDevice
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool14" custom/c10pool14 c10pool14 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool14" custom/c10pool14 c10pool14 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool14" c10pool14 c10pool14 testDevice

      incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool14" c12pool14
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool14" c12pool14 c12pool14 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool14" c12pool14 c12pool14 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool14" c12pool14 c12pool14 testDevice
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool14" custom/c12pool14 c12pool14 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool14" custom/c12pool14 c12pool14 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool14" c12pool14 c12pool14 testDevice

      incus storage volume create "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" c10pool15
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" c10pool15 c10pool15 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" c10pool15 c10pool15 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" c10pool15 c10pool15 testDevice
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" custom/c10pool15 c10pool15 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" custom/c10pool15 c10pool15 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" c10pool15 c10pool15 testDevice

      incus storage volume create "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" c12pool15
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" c12pool15 c12pool15 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" c12pool15 c12pool15 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" c12pool15 c12pool15 testDevice
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" custom/c12pool15 c12pool15 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" custom/c12pool15 c12pool15 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" c12pool15 c12pool15 testDevice
    fi

    if [ "$incus_backend" = "zfs" ]; then
      incus launch testimage c13pool7 -s "incustest-$(basename "${INCUS_DIR}")-pool7"
      incus launch testimage c14pool7 -s "incustest-$(basename "${INCUS_DIR}")-pool7"

      incus launch testimage c15pool8 -s "incustest-$(basename "${INCUS_DIR}")-pool8"
      incus launch testimage c16pool8 -s "incustest-$(basename "${INCUS_DIR}")-pool8"

      incus launch testimage c17pool9 -s "incustest-$(basename "${INCUS_DIR}")-pool9"
      incus launch testimage c18pool9 -s "incustest-$(basename "${INCUS_DIR}")-pool9"

      incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool7" c13pool7
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool7" c13pool7 c13pool7 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool7" c13pool7 c13pool7 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool7" c13pool7 c13pool7 testDevice
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool7" custom/c13pool7 c13pool7 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool7" custom/c13pool7 c13pool7 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool7" c13pool7 c13pool7 testDevice

      incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool7" c14pool7
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool7" c14pool7 c14pool7 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool7" c14pool7 c14pool7 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool7" c14pool7 c14pool7 testDevice
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool7" custom/c14pool7 c14pool7 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool7" custom/c14pool7 c14pool7 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool7" c14pool7 c14pool7 testDevice

      incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool8" c15pool8
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool8" c15pool8 c15pool8 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool8" c15pool8 c15pool8 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool8" c15pool8 c15pool8 testDevice
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool8" custom/c15pool8 c15pool8 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool8" custom/c15pool8 c15pool8 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool8" c15pool8 c15pool8 testDevice

      incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool8" c16pool8
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool8" c16pool8 c16pool8 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool8" c16pool8 c16pool8 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool8" c16pool8 c16pool8 testDevice
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool8" custom/c16pool8 c16pool8 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool8" custom/c16pool8 c16pool8 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool8" c16pool8 c16pool8 testDevice

      incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool9" c17pool9
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool9" c17pool9 c17pool9 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool9" c17pool9 c17pool9 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool9" c17pool9 c17pool9 testDevice
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool9" custom/c17pool9 c17pool9 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool9" custom/c17pool9 c17pool9 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool9" c17pool9 c17pool9 testDevice

      incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool9" c18pool9
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool9" c18pool9 c18pool9 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool9" c18pool9 c18pool9 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool9" c18pool9 c18pool9 testDevice
      incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool9" custom/c18pool9 c18pool9 testDevice /opt
      ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool9" custom/c18pool9 c18pool9 testDevice2 /opt || false
      incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool9" c18pool9 c18pool9 testDevice
      incus storage volume rename "incustest-$(basename "${INCUS_DIR}")-pool9" c18pool9 c18pool9-renamed
      incus storage volume rename "incustest-$(basename "${INCUS_DIR}")-pool9" c18pool9-renamed c18pool9
    fi

    if [ "$incus_backend" = "zfs" ]; then
      incus delete -f c1pool1
      incus delete -f c3pool1

      incus delete -f c4pool2
      incus delete -f c2pool2

      incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool1" c1pool1
      incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool1" c2pool2
      incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool2" c3pool1
      incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool2" c4pool2
    fi

    if [ "$incus_backend" = "btrfs" ]; then
      incus delete -f c5pool3
      incus delete -f c7pool3

      incus delete -f c8pool4
      incus delete -f c6pool4

      incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool3" c5pool3
      incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool4" c6pool4
      incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool3" c7pool3
      incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool4" c8pool4
    fi

    incus delete -f c9pool5
    incus delete -f c11pool5

    incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool5" c9pool5
    incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool5" c11pool5
    incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool5" c12pool5

    if [ "$incus_backend" = "lvm" ]; then
      incus delete -f c1pool6
      incus delete -f c2pool6
      incus delete -f c10pool6
      incus delete -f c12pool6

      incus delete -f c10pool11
      incus delete -f c12pool11

      incus delete -f c10pool12
      incus delete -f c12pool12

      incus delete -f c10pool13
      incus delete -f c12pool13

      incus delete -f c10pool14
      incus delete -f c12pool14

      incus delete -f c10pool15
      incus delete -f c12pool15

      incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool6" c10pool6
      incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool6"  c12pool6
      incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool11" c10pool11
      incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool11" c12pool11
      incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool12" c10pool12
      incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool12" c12pool12
      incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool13" c10pool13
      incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool13" c12pool13
      incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool14" c10pool14
      incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool14" c12pool14
      incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" c10pool15
      incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" c12pool15
    fi

    if [ "$incus_backend" = "zfs" ]; then
      incus delete -f c13pool7
      incus delete -f c14pool7

      incus delete -f c15pool8
      incus delete -f c16pool8

      incus delete -f c17pool9
      incus delete -f c18pool9

      incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool7" c13pool7
      incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool7" c14pool7
      incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool8" c15pool8
      incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool8" c16pool8
      incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool9" c17pool9
      incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool9" c18pool9
    fi

    incus image delete testimage

    if [ "$incus_backend" = "zfs" ]; then
      incus storage delete "incustest-$(basename "${INCUS_DIR}")-pool7"
      incus storage delete "incustest-$(basename "${INCUS_DIR}")-pool8"
      incus storage delete "incustest-$(basename "${INCUS_DIR}")-pool9"
      # shellcheck disable=SC2154
      deconfigure_loop_device "${loop_file_4}" "${loop_device_4}"

      incus storage delete "incustest-$(basename "${INCUS_DIR}")-pool1"

      incus storage delete "incustest-$(basename "${INCUS_DIR}")-pool2"
      # shellcheck disable=SC2154
      deconfigure_loop_device "${loop_file_1}" "${loop_device_1}"
    fi

    if [ "$incus_backend" = "btrfs" ]; then
      incus storage delete "incustest-$(basename "${INCUS_DIR}")-pool4"
      # shellcheck disable=SC2154
      deconfigure_loop_device "${loop_file_2}" "${loop_device_2}"
    fi

    if [ "$incus_backend" = "lvm" ]; then
      incus storage delete "incustest-$(basename "${INCUS_DIR}")-pool6"
      # shellcheck disable=SC2154
      pvremove -ff "${loop_device_3}" || true
      # shellcheck disable=SC2154
      deconfigure_loop_device "${loop_file_3}" "${loop_device_3}"

      incus storage delete "incustest-$(basename "${INCUS_DIR}")-pool11"
      # shellcheck disable=SC2154
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-pool11-test_vg_2" || true
      pvremove -ff "${loop_device_6}" || true
      # shellcheck disable=SC2154
      deconfigure_loop_device "${loop_file_6}" "${loop_device_6}"

      incus storage delete "incustest-$(basename "${INCUS_DIR}")-pool12"
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-pool12-test_vg_3" || true
      pvremove -ff "${loop_device_7}" || true
      # shellcheck disable=SC2154
      deconfigure_loop_device "${loop_file_7}" "${loop_device_7}"

      incus storage delete "incustest-$(basename "${INCUS_DIR}")-pool13"
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-pool13-test_vg_4" || true
      pvremove -ff "${loop_device_8}" || true
      # shellcheck disable=SC2154
      deconfigure_loop_device "${loop_file_8}" "${loop_device_8}"

      incus storage delete "incustest-$(basename "${INCUS_DIR}")-pool14"
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-pool14" || true

      incus storage delete "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15"
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" || true

      incus storage delete "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool16"
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool16" || true

      incus storage delete "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool17"
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool17" || true

      incus storage delete "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool18"
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool18" || true

      incus storage delete "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool19"
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool19" || true

      incus storage delete "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool20"
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool20" || true

      incus storage delete "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool21"
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool21" || true

      incus storage delete "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool22"
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool22" || true

      incus storage delete "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool23"
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool23" || true

      incus storage delete "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool24"
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool24" || true

      incus storage delete "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool25"
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool25" || true
    fi
  )

  # Test applying quota (expected size ranges are in KiB and have an allowable range to account for allocation variations).
  QUOTA1="20MiB"
  rootMinKiB1="13800"
  rootMaxKiB1="23000"

  QUOTA2="25MiB"
  rootMinKiB2="18900"
  rootMaxKiB2="28000"

  if [ "$incus_backend" != "dir" ]; then
    incus launch testimage quota1
    rootOrigSizeKiB=$(incus exec quota1 -- df -P / | tail -n1 | awk '{print $2}')
    rootOrigMinSizeKiB=$((rootOrigSizeKiB-2000))
    rootOrigMaxSizeKiB=$((rootOrigSizeKiB+2000))

    incus profile device set default root size "${QUOTA1}"
    incus stop -f quota1
    incus start quota1

    # BTRFS quota isn't accessible with the df tool.
    if [ "$incus_backend" != "btrfs" ]; then
    rootSizeKiB=$(incus exec quota1 -- df -P / | tail -n1 | awk '{print $2}')
      if [ "$rootSizeKiB" -gt "$rootMaxKiB1" ] || [ "$rootSizeKiB" -lt "$rootMinKiB1" ] ; then
        echo "root size not within quota range"
        false
      fi
    fi

    incus launch testimage quota2
    incus stop -f quota2
    incus start quota2

    incus init testimage quota3
    incus start quota3

    incus profile device set default root size "${QUOTA2}"

    incus stop -f quota1
    incus start quota1

    incus stop -f quota2
    incus start quota2
    if [ "$incus_backend" != "btrfs" ]; then
      rootSizeKiB=$(incus exec quota2 -- df -P / | tail -n1 | awk '{print $2}')
      if [ "$rootSizeKiB" -gt "$rootMaxKiB2" ] || [ "$rootSizeKiB" -lt "$rootMinKiB2" ] ; then
        echo "root size not within quota range"
        false
      fi
    fi

    incus stop -f quota3
    incus start quota3

    incus profile device unset default root size

    # Only ZFS supports hot quota changes (LVM requires a reboot).
    if [ "$incus_backend" = "zfs" ]; then
      rootSizeKiB=$(incus exec quota1 -- df -P / | tail -n1 | awk '{print $2}')
      if [ "$rootSizeKiB" -gt "$rootOrigMaxSizeKiB" ] || [ "$rootSizeKiB" -lt "$rootOrigMinSizeKiB" ] ; then
        echo "original root size not restored"
        false
      fi
    fi

    incus stop -f quota1
    incus start quota1
    if [ "$incus_backend" = "zfs" ]; then
      rootSizeKiB=$(incus exec quota1 -- df -P / | tail -n1 | awk '{print $2}')
      if [ "$rootSizeKiB" -gt "$rootOrigMaxSizeKiB" ] || [ "$rootSizeKiB" -lt "$rootOrigMinSizeKiB" ] ; then
        echo "original root size not restored"
        false
      fi
    fi

    incus stop -f quota2
    incus start quota2

    incus stop -f quota3
    incus start quota3

    incus delete -f quota1
    incus delete -f quota2
    incus delete -f quota3
  fi

  if [ "${incus_backend}" = "btrfs" ]; then
    # shellcheck disable=SC2031
    pool_name="incustest-$(basename "${INCUS_DIR}")-quota"

    # shellcheck disable=SC1009
    incus storage create "${pool_name}" btrfs

    # Import image into default storage pool.
    ensure_import_testimage

    # Launch container.
    incus launch -s "${pool_name}" testimage c1

    # Disable quotas. The usage should be 0.
    # shellcheck disable=SC2031
    btrfs quota disable "${INCUS_DIR}/storage-pools/${pool_name}"
    usage=$(incus query /1.0/instances/c1/state | jq '.disk.root')
    [ "${usage}" = "null" ]

    # Enable quotas. The usage should then be > 0.
    # shellcheck disable=SC2031
    btrfs quota enable "${INCUS_DIR}/storage-pools/${pool_name}"
    usage=$(incus query /1.0/instances/c1/state | jq '.disk.root.usage')
    [ "${usage}" -gt 0 ]

    # Clean up everything.
    incus rm -f c1
    incus storage delete "${pool_name}"
  fi

  # Test removing storage pools only containing image volumes
  # shellcheck disable=SC2031,2269
  INCUS_DIR="${INCUS_DIR}"
  storage_pool="incustest-$(basename "${INCUS_DIR}")-pool26"
  incus storage create "$storage_pool" "$incus_backend"
  incus init -s "${storage_pool}" testimage c1
  # The storage pool will not be removed since it has c1 attached to it
  ! incus storage delete "${storage_pool}" || false
  incus delete c1
  # The storage pool will be deleted since the testimage is also attached to
  # the default pool
  incus storage delete "${storage_pool}"
  incus image show testimage

  # Test storage pool resize
  if [ "${incus_backend}" = "btrfs" ] || [ "${incus_backend}" = "lvm" ] || [ "${incus_backend}" = "zfs" ]; then
    # shellcheck disable=SC1009
    pool_name="incustest-$(basename "${INCUS_DIR}")-pool1"

    incus storage create "${pool_name}" "${incus_backend}" size=1GiB

    incus launch testimage c1 -s "${pool_name}"

    expected_size=1073741824
    # +/- 5% of the expected size
    expected_size_min=1020054732
    expected_size_max=1127428916

    # Check pool file size
    [ "$(stat --format="%s" "${INCUS_DIR}/disks/${pool_name}.img")" = "${expected_size}" ]

    if [ "${incus_backend}" = "btrfs" ]; then
      actual_size="$(btrfs filesystem show --raw "${INCUS_DIR}/disks/${pool_name}.img" | awk '/devid/{print $4}')"
    elif [ "${incus_backend}" = "lvm" ]; then
      actual_size="$(lvs --noheadings --nosuffix --units b --options='lv_size' "incustest-$(basename "${INCUS_DIR}")/IncusThinPool")"
    elif [ "${incus_backend}" = "zfs" ]; then
      actual_size="$(zpool list -Hp "${pool_name}" | awk '{print $2}')"
    fi

    # Check that pool size is within the expected range
    [ "${actual_size}" -ge "${expected_size_min}" ] || [ "${actual_size}" -le "${expected_size_max}" ]

    # Grow pool
    incus storage set "${pool_name}" size=2GiB

    expected_size=2147483648
    # +/- 5% of the expected size
    expected_size_min=2040109465
    expected_size_max=2254857831

    # Check pool file size
    [ "$(stat --format="%s" "${INCUS_DIR}/disks/${pool_name}.img")" = "${expected_size}" ]

    if [ "${incus_backend}" = "btrfs" ]; then
      actual_size="$(btrfs filesystem show --raw "${INCUS_DIR}/disks/${pool_name}.img" | awk '/devid/{print $4}')"
    elif [ "${incus_backend}" = "lvm" ]; then
      actual_size="$(lvs --noheadings --nosuffix --units b --options='lv_size' "incustest-$(basename "${INCUS_DIR}")/IncusThinPool")"
    elif [ "${incus_backend}" = "zfs" ]; then
      actual_size="$(zpool list -Hp "${pool_name}" | awk '{print $2}')"
    fi

    # Check that pool size is within the expected range
    [ "${actual_size}" -ge "${expected_size_min}" ] || [ "${actual_size}" -le "${expected_size_max}" ]

    # Shrinking the pool should fail
    ! incus storage set "${pool_name}" size=1GiB || false

    # Ensure the pool is still usable after resizing by launching an instance
    incus launch testimage c2 -s "${pool_name}"

    incus rm -f c1 c2
    incus storage rm "${pool_name}"
  fi

  # shellcheck disable=SC2031,2269
  INCUS_DIR="${INCUS_DIR}"
  kill_incus "${INCUS_STORAGE_DIR}"
}
