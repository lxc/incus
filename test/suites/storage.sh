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
  inc storage create "$storage_pool" "$incus_backend"
  inc storage show "$storage_pool" | sed 's/^description:.*/description: foo/' | inc storage edit "$storage_pool"
  inc storage show "$storage_pool" | grep -q 'description: foo'

  inc storage volume create "$storage_pool" "$storage_volume"

  # Test setting description on a storage volume
  inc storage volume show "$storage_pool" "$storage_volume" | sed 's/^description:.*/description: bar/' | inc storage volume edit "$storage_pool" "$storage_volume"
  inc storage volume show "$storage_pool" "$storage_volume" | grep -q 'description: bar'

  # Validate get/set
  inc storage set "$storage_pool" user.abc def
  [ "$(inc storage get "$storage_pool" user.abc)" = "def" ]

  inc storage volume set "$storage_pool" "$storage_volume" user.abc def
  [ "$(inc storage volume get "$storage_pool" "$storage_volume" user.abc)" = "def" ]

  inc storage volume delete "$storage_pool" "$storage_volume"

  # Test copying pool volume.* key to the volume with prefix stripped at volume creation time
  inc storage set "$storage_pool" volume.snapshots.expiry 3d
  inc storage volume create "$storage_pool" "$storage_volume"
  [ "$(inc storage volume get "$storage_pool" "$storage_volume" snapshots.expiry)" = "3d" ]
  inc storage volume delete "$storage_pool" "$storage_volume"

  inc storage delete "$storage_pool"

  # Test btrfs resize
  if [ "$incus_backend" = "lvm" ] || [ "$incus_backend" = "ceph" ]; then
      # shellcheck disable=2039,3043
      local btrfs_storage_pool btrfs_storage_volume
      btrfs_storage_pool="incustest-$(basename "${INCUS_DIR}")-pool-btrfs"
      btrfs_storage_volume="${storage_pool}-vol"
      inc storage create "$btrfs_storage_pool" "$incus_backend" volume.block.filesystem=btrfs volume.size=200MiB
      inc storage volume create "$btrfs_storage_pool" "$btrfs_storage_volume"
      inc storage volume show "$btrfs_storage_pool" "$btrfs_storage_volume"
      inc storage volume set "$btrfs_storage_pool" "$btrfs_storage_volume" size 256MiB
      inc storage volume delete "$btrfs_storage_pool" "$btrfs_storage_volume"

      # Test generation of unique UUID.
      inc init testimage uuid1 -s "incustest-$(basename "${INCUS_DIR}")-pool-btrfs"
      POOL="incustest-$(basename "${INCUS_DIR}")-pool-btrfs"
      inc copy uuid1 uuid2
      inc start uuid1
      inc start uuid2
      if [ "$incus_backend" = "lvm" ]; then
        [ "$(blkid -s UUID -o value -p /dev/"${POOL}"/containers_uuid1)" != "$(blkid -s UUID -o value -p /dev/"${POOL}"/containers_uuid2)" ]
      elif [ "$incus_backend" = "ceph" ]; then
        [ "$(blkid -s UUID -o value -p /dev/rbd/"${POOL}"/container_uuid1)" != "$(blkid -s UUID -o value -p /dev/rbd/"${POOL}"/container_uuid2)" ]
      fi
      inc delete --force uuid1
      inc delete --force uuid2
      inc image delete testimage

      inc storage delete "$btrfs_storage_pool"
  fi
  ensure_import_testimage

  (
    set -e
    # shellcheck disable=2030
    INCUS_DIR="${INCUS_STORAGE_DIR}"

    # shellcheck disable=SC1009
    if [ "$incus_backend" = "zfs" ]; then
    # Create loop file zfs pool.
      inc storage create "incustest-$(basename "${INCUS_DIR}")-pool1" zfs

      # Check that we can't create a loop file in a non-Incus owned location.
      INVALID_LOOP_FILE="$(mktemp -p "${INCUS_DIR}" XXXXXXXXX)-invalid-loop-file"
      ! inc storage create "incustest-$(basename "${INCUS_DIR}")-pool1" zfs source="${INVALID_LOOP_FILE}" || false

      # Let Incus use an already existing dataset.
      zfs create -p -o mountpoint=none "incustest-$(basename "${INCUS_DIR}")-pool1/existing-dataset-as-pool"
      inc storage create "incustest-$(basename "${INCUS_DIR}")-pool7" zfs source="incustest-$(basename "${INCUS_DIR}")-pool1/existing-dataset-as-pool"

      # Let Incus use an already existing storage pool.
      configure_loop_device loop_file_4 loop_device_4
      # shellcheck disable=SC2154
      zpool create -f -m none -O compression=on "incustest-$(basename "${INCUS_DIR}")-pool9-existing-pool" "${loop_device_4}"
      inc storage create "incustest-$(basename "${INCUS_DIR}")-pool9" zfs source="incustest-$(basename "${INCUS_DIR}")-pool9-existing-pool"

      # Let Incus create a new dataset and use as pool.
      inc storage create "incustest-$(basename "${INCUS_DIR}")-pool8" zfs source="incustest-$(basename "${INCUS_DIR}")-pool1/non-existing-dataset-as-pool"

      # Create device backed zfs pool
      configure_loop_device loop_file_1 loop_device_1
      # shellcheck disable=SC2154
      inc storage create "incustest-$(basename "${INCUS_DIR}")-pool2" zfs source="${loop_device_1}"

      # Test that no invalid zfs storage pool configuration keys can be set.
      ! inc storage create "incustest-$(basename "${INCUS_DIR}")-invalid-zfs-pool-config" zfs lvm.thinpool_name=bla || false
      ! inc storage create "incustest-$(basename "${INCUS_DIR}")-invalid-zfs-pool-config" zfs lvm.use_thinpool=false || false
      ! inc storage create "incustest-$(basename "${INCUS_DIR}")-invalid-zfs-pool-config" zfs lvm.vg_name=bla || false

      # Test that all valid zfs storage pool configuration keys can be set.
      inc storage create "incustest-$(basename "${INCUS_DIR}")-valid-zfs-pool-config" zfs volume.zfs.remove_snapshots=true
      inc storage delete "incustest-$(basename "${INCUS_DIR}")-valid-zfs-pool-config"

      inc storage create "incustest-$(basename "${INCUS_DIR}")-valid-zfs-pool-config" zfs volume.zfs.use_refquota=true
      inc storage delete "incustest-$(basename "${INCUS_DIR}")-valid-zfs-pool-config"

      inc storage create "incustest-$(basename "${INCUS_DIR}")-valid-zfs-pool-config" zfs zfs.clone_copy=true
      inc storage delete "incustest-$(basename "${INCUS_DIR}")-valid-zfs-pool-config"

      inc storage create "incustest-$(basename "${INCUS_DIR}")-valid-zfs-pool-config" zfs zfs.pool_name="incustest-$(basename "${INCUS_DIR}")-valid-zfs-pool-config"
      inc storage delete "incustest-$(basename "${INCUS_DIR}")-valid-zfs-pool-config"

      inc storage create "incustest-$(basename "${INCUS_DIR}")-valid-zfs-pool-config" zfs rsync.bwlimit=1024
      inc storage delete "incustest-$(basename "${INCUS_DIR}")-valid-zfs-pool-config"
    fi

    if [ "$incus_backend" = "btrfs" ]; then
      # Create loop file btrfs pool.
      inc storage create "incustest-$(basename "${INCUS_DIR}")-pool3" btrfs

      # Create device backed btrfs pool.
      configure_loop_device loop_file_2 loop_device_2
      # shellcheck disable=SC2154
      inc storage create "incustest-$(basename "${INCUS_DIR}")-pool4" btrfs source="${loop_device_2}"

      # Check that we cannot create storage pools inside of ${INCUS_DIR} other than ${INCUS_DIR}/storage-pools/{pool_name}.
      ! inc storage create "incustest-$(basename "${INCUS_DIR}")-pool5_under_incus_dir" btrfs source="${INCUS_DIR}" || false

      # Test that no invalid btrfs storage pool configuration keys can be set.
      ! inc storage create "incustest-$(basename "${INCUS_DIR}")-invalid-btrfs-pool-config" btrfs lvm.thinpool_name=bla || false
      ! inc storage create "incustest-$(basename "${INCUS_DIR}")-invalid-btrfs-pool-config" btrfs lvm.use_thinpool=false || false
      ! inc storage create "incustest-$(basename "${INCUS_DIR}")-invalid-btrfs-pool-config" btrfs lvm.vg_name=bla || false
      ! inc storage create "incustest-$(basename "${INCUS_DIR}")-invalid-btrfs-pool-config" btrfs volume.block.filesystem=ext4 || false
      ! inc storage create "incustest-$(basename "${INCUS_DIR}")-invalid-btrfs-pool-config" btrfs volume.block.mount_options=discard || false
      ! inc storage create "incustest-$(basename "${INCUS_DIR}")-invalid-btrfs-pool-config" btrfs volume.zfs.remove_snapshots=true || false
      ! inc storage create "incustest-$(basename "${INCUS_DIR}")-invalid-btrfs-pool-config" btrfs volume.zfs.use_refquota=true || false
      ! inc storage create "incustest-$(basename "${INCUS_DIR}")-invalid-btrfs-pool-config" btrfs zfs.clone_copy=true || false
      ! inc storage create "incustest-$(basename "${INCUS_DIR}")-invalid-btrfs-pool-config" btrfs zfs.pool_name=bla || false

      inc storage create "incustest-$(basename "${INCUS_DIR}")-valid-btrfs-pool-config" btrfs rsync.bwlimit=1024
      inc storage delete "incustest-$(basename "${INCUS_DIR}")-valid-btrfs-pool-config"

      inc storage create "incustest-$(basename "${INCUS_DIR}")-valid-btrfs-pool-config" btrfs btrfs.mount_options="rw,strictatime,user_subvol_rm_allowed"
      inc storage set "incustest-$(basename "${INCUS_DIR}")-valid-btrfs-pool-config" btrfs.mount_options "rw,relatime,user_subvol_rm_allowed"
      inc storage delete "incustest-$(basename "${INCUS_DIR}")-valid-btrfs-pool-config"
    fi

    # Create dir pool.
    inc storage create "incustest-$(basename "${INCUS_DIR}")-pool5" dir

    # Check that we cannot create storage pools inside of ${INCUS_DIR} other than ${INCUS_DIR}/storage-pools/{pool_name}.
    ! inc storage create "incustest-$(basename "${INCUS_DIR}")-pool5_under_incus_dir" dir source="${INCUS_DIR}" || false

    # Check that we can create storage pools inside of ${INCUS_DIR}/storage-pools/{pool_name}.
    inc storage create "incustest-$(basename "${INCUS_DIR}")-pool5_under_incus_dir" dir source="${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool5_under_incus_dir"

    inc storage delete "incustest-$(basename "${INCUS_DIR}")-pool5_under_incus_dir"

    # Test that no invalid dir storage pool configuration keys can be set.
    ! inc storage create "incustest-$(basename "${INCUS_DIR}")-invalid-dir-pool-config" dir lvm.thinpool_name=bla || false
    ! inc storage create "incustest-$(basename "${INCUS_DIR}")-invalid-dir-pool-config" dir lvm.use_thinpool=false || false
    ! inc storage create "incustest-$(basename "${INCUS_DIR}")-invalid-dir-pool-config" dir lvm.vg_name=bla || false
    ! inc storage create "incustest-$(basename "${INCUS_DIR}")-invalid-dir-pool-config" dir size=1GiB || false
    ! inc storage create "incustest-$(basename "${INCUS_DIR}")-invalid-dir-pool-config" dir volume.block.filesystem=ext4 || false
    ! inc storage create "incustest-$(basename "${INCUS_DIR}")-invalid-dir-pool-config" dir volume.block.mount_options=discard || false
    ! inc storage create "incustest-$(basename "${INCUS_DIR}")-invalid-dir-pool-config" dir volume.zfs.remove_snapshots=true || false
    ! inc storage create "incustest-$(basename "${INCUS_DIR}")-invalid-dir-pool-config" dir volume.zfs.use_refquota=true || false
    ! inc storage create "incustest-$(basename "${INCUS_DIR}")-invalid-dir-pool-config" dir zfs.clone_copy=true || false
    ! inc storage create "incustest-$(basename "${INCUS_DIR}")-invalid-dir-pool-config" dir zfs.pool_name=bla || false

    inc storage create "incustest-$(basename "${INCUS_DIR}")-valid-dir-pool-config" dir rsync.bwlimit=1024
    inc storage delete "incustest-$(basename "${INCUS_DIR}")-valid-dir-pool-config"

    if [ "$incus_backend" = "lvm" ]; then
      # Create lvm pool.
      configure_loop_device loop_file_3 loop_device_3
      # shellcheck disable=SC2154
      inc storage create "incustest-$(basename "${INCUS_DIR}")-pool6" lvm source="${loop_device_3}" volume.size=25MiB

      configure_loop_device loop_file_5 loop_device_5
      # shellcheck disable=SC2154
      # Should fail if vg does not exist, since we have no way of knowing where
      # to create the vg without a block device path set.
      ! inc storage create "incustest-$(basename "${INCUS_DIR}")-pool10" lvm source=test_vg_1 volume.size=25MiB || false
      # shellcheck disable=SC2154
      deconfigure_loop_device "${loop_file_5}" "${loop_device_5}"

      configure_loop_device loop_file_6 loop_device_6
      # shellcheck disable=SC2154
      pvcreate "${loop_device_6}"
      vgcreate "incustest-$(basename "${INCUS_DIR}")-pool11-test_vg_2" "${loop_device_6}"
      # Reuse existing volume group "test_vg_2" on existing physical volume.
      inc storage create "incustest-$(basename "${INCUS_DIR}")-pool11" lvm source="incustest-$(basename "${INCUS_DIR}")-pool11-test_vg_2" volume.size=25MiB

      configure_loop_device loop_file_7 loop_device_7
      # shellcheck disable=SC2154
      pvcreate "${loop_device_7}"
      vgcreate "incustest-$(basename "${INCUS_DIR}")-pool12-test_vg_3" "${loop_device_7}"
      # Reuse existing volume group "test_vg_3" on existing physical volume.
      inc storage create "incustest-$(basename "${INCUS_DIR}")-pool12" lvm source="incustest-$(basename "${INCUS_DIR}")-pool12-test_vg_3" volume.size=25MiB

      configure_loop_device loop_file_8 loop_device_8
      # shellcheck disable=SC2154
      # Create new volume group "test_vg_4".
      inc storage create "incustest-$(basename "${INCUS_DIR}")-pool13" lvm source="${loop_device_8}" lvm.vg_name="incustest-$(basename "${INCUS_DIR}")-pool13-test_vg_4" volume.size=25MiB

      inc storage create "incustest-$(basename "${INCUS_DIR}")-pool14" lvm volume.size=25MiB

      inc storage create "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" lvm lvm.use_thinpool=false volume.size=25MiB

      # Test that no invalid lvm storage pool configuration keys can be set.
      ! inc storage create "incustest-$(basename "${INCUS_DIR}")-invalid-lvm-pool-config" lvm volume.zfs.remove_snapshots=true || false
      ! inc storage create "incustest-$(basename "${INCUS_DIR}")-invalid-lvm-pool-config" lvm volume.zfs_use_refquota=true || false
      ! inc storage create "incustest-$(basename "${INCUS_DIR}")-invalid-lvm-pool-config" lvm zfs.clone_copy=true || false
      ! inc storage create "incustest-$(basename "${INCUS_DIR}")-invalid-lvm-pool-config" lvm zfs.pool_name=bla || false
      ! inc storage create "incustest-$(basename "${INCUS_DIR}")-invalid-lvm-pool-config" lvm lvm.use_thinpool=false lvm.thinpool_name="incustest-$(basename "${INCUS_DIR}")-invalid-lvm-pool-config" || false

      # Test that all valid lvm storage pool configuration keys can be set.
      inc storage create "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool16" lvm lvm.thinpool_name="incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config"
      inc storage create "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool17" lvm lvm.vg_name="incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config"
      inc storage create "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool18" lvm size=1GiB
      inc storage create "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool19" lvm volume.block.filesystem=ext4
      inc storage create "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool20" lvm volume.block.mount_options=discard
      inc storage create "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool21" lvm volume.size=25MiB
      inc storage create "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool22" lvm lvm.use_thinpool=true
      inc storage create "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool23" lvm lvm.use_thinpool=true lvm.thinpool_name="incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config"
      inc storage create "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool24" lvm rsync.bwlimit=1024
      inc storage create "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool25" lvm volume.block.mount_options="rw,strictatime,discard"
      inc storage set "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool25" volume.block.mount_options "rw,lazytime"
      inc storage create "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool26" lvm volume.block.filesystem=btrfs
    fi

    # Set default storage pool for image import.
    inc profile device add default root disk path="/" pool="incustest-$(basename "${INCUS_DIR}")-pool5"

    # Import image into default storage pool.
    ensure_import_testimage

    # Muck around with some containers on various pools.
    if [ "$incus_backend" = "zfs" ]; then
      inc init testimage c1pool1 -s "incustest-$(basename "${INCUS_DIR}")-pool1"
      inc list -c b c1pool1 | grep "incustest-$(basename "${INCUS_DIR}")-pool1"

      inc init testimage c2pool2 -s "incustest-$(basename "${INCUS_DIR}")-pool2"
      inc list -c b c2pool2 | grep "incustest-$(basename "${INCUS_DIR}")-pool2"

      inc launch testimage c3pool1 -s "incustest-$(basename "${INCUS_DIR}")-pool1"
      inc list -c b c3pool1 | grep "incustest-$(basename "${INCUS_DIR}")-pool1"

      inc launch testimage c4pool2 -s "incustest-$(basename "${INCUS_DIR}")-pool2"
      inc list -c b c4pool2 | grep "incustest-$(basename "${INCUS_DIR}")-pool2"

      inc storage volume create "incustest-$(basename "${INCUS_DIR}")-pool1" c1pool1
      inc storage volume set "incustest-$(basename "${INCUS_DIR}")-pool1" c1pool1 zfs.use_refquota true
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool1" c1pool1 c1pool1 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool1" c1pool1 c1pool1 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool1" c1pool1 c1pool1
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool1" custom/c1pool1 c1pool1 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool1" custom/c1pool1 c1pool1 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool1" c1pool1 c1pool1

      inc storage volume create "incustest-$(basename "${INCUS_DIR}")-pool1" c2pool2
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool1" c2pool2 c2pool2 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool1" c2pool2 c2pool2 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool1" c2pool2 c2pool2
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool1" custom/c2pool2 c2pool2 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool1" custom/c2pool2 c2pool2 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool1" c2pool2 c2pool2

      inc storage volume create "incustest-$(basename "${INCUS_DIR}")-pool2" c3pool1
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool2" c3pool1 c3pool1 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool2" c3pool1 c3pool1 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool2" c3pool1 c3pool1
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool2" c3pool1 c3pool1 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool2" c3pool1 c3pool1 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool2" c3pool1 c3pool1

      inc storage volume create "incustest-$(basename "${INCUS_DIR}")-pool2" c4pool2
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool2" c4pool2 c4pool2 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool2" c4pool2 c4pool2 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool2" c4pool2 c4pool2
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool2" custom/c4pool2 c4pool2 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool2" custom/c4pool2 c4pool2 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool2" c4pool2 c4pool2
      inc storage volume rename "incustest-$(basename "${INCUS_DIR}")-pool2" c4pool2 c4pool2-renamed
      inc storage volume rename "incustest-$(basename "${INCUS_DIR}")-pool2" c4pool2-renamed c4pool2
    fi

    if [ "$incus_backend" = "btrfs" ]; then
      inc init testimage c5pool3 -s "incustest-$(basename "${INCUS_DIR}")-pool3"
      inc list -c b c5pool3 | grep "incustest-$(basename "${INCUS_DIR}")-pool3"
      inc init testimage c6pool4 -s "incustest-$(basename "${INCUS_DIR}")-pool4"
      inc list -c b c6pool4 | grep "incustest-$(basename "${INCUS_DIR}")-pool4"

      inc launch testimage c7pool3 -s "incustest-$(basename "${INCUS_DIR}")-pool3"
      inc list -c b c7pool3 | grep "incustest-$(basename "${INCUS_DIR}")-pool3"
      inc launch testimage c8pool4 -s "incustest-$(basename "${INCUS_DIR}")-pool4"
      inc list -c b c8pool4 | grep "incustest-$(basename "${INCUS_DIR}")-pool4"

      inc storage volume create "incustest-$(basename "${INCUS_DIR}")-pool3" c5pool3
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool3" c5pool3 c5pool3 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool3" c5pool3 c5pool3 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool3" c5pool3 c5pool3 testDevice
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool3" custom/c5pool3 c5pool3 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool3" custom/c5pool3 c5pool3 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool3" c5pool3 c5pool3 testDevice

      inc storage volume create "incustest-$(basename "${INCUS_DIR}")-pool4" c6pool4
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool4" c6pool4 c5pool3 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool4" c6pool4 c5pool3 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool4" c6pool4 c5pool3 testDevice
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool4" custom/c6pool4 c5pool3 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool4" custom/c6pool4 c5pool3 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool4" c6pool4 c5pool3 testDevice

      inc storage volume create "incustest-$(basename "${INCUS_DIR}")-pool3" c7pool3
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool3" c7pool3 c7pool3 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool3" c7pool3 c7pool3 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool3" c7pool3 c7pool3 testDevice
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool3" custom/c7pool3 c7pool3 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool3" custom/c7pool3 c7pool3 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool3" c7pool3 c7pool3 testDevice

      inc storage volume create "incustest-$(basename "${INCUS_DIR}")-pool4" c8pool4
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool4" c8pool4 c8pool4 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool4" c8pool4 c8pool4 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool4" c8pool4 c8pool4 testDevice
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool4" custom/c8pool4 c8pool4 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool4" custom/c8pool4 c8pool4 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool4" c8pool4 c8pool4 testDevice
      inc storage volume rename "incustest-$(basename "${INCUS_DIR}")-pool4" c8pool4 c8pool4-renamed
      inc storage volume rename "incustest-$(basename "${INCUS_DIR}")-pool4" c8pool4-renamed c8pool4
    fi

    inc init testimage c9pool5 -s "incustest-$(basename "${INCUS_DIR}")-pool5"
    inc list -c b c9pool5 | grep "incustest-$(basename "${INCUS_DIR}")-pool5"

    inc launch testimage c11pool5 -s "incustest-$(basename "${INCUS_DIR}")-pool5"
    inc list -c b c11pool5 | grep "incustest-$(basename "${INCUS_DIR}")-pool5"

    inc storage volume create "incustest-$(basename "${INCUS_DIR}")-pool5" c9pool5
    inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool5" c9pool5 c9pool5 testDevice /opt
    ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool5" c9pool5 c9pool5 testDevice2 /opt || false
    inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool5" c9pool5 c9pool5 testDevice
    inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool5" custom/c9pool5 c9pool5 testDevice /opt
    ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool5" custom/c9pool5 c9pool5 testDevice2 /opt || false
    inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool5" c9pool5 c9pool5 testDevice

    inc storage volume create "incustest-$(basename "${INCUS_DIR}")-pool5" c11pool5
    inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool5" c11pool5 c11pool5 testDevice /opt
    ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool5" c11pool5 c11pool5 testDevice2 /opt || false
    inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool5" c11pool5 c11pool5 testDevice
    inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool5" custom/c11pool5 c11pool5 testDevice /opt
    ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool5" custom/c11pool5 c11pool5 testDevice2 /opt || false
    inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool5" c11pool5 c11pool5 testDevice
    inc storage volume rename "incustest-$(basename "${INCUS_DIR}")-pool5" c11pool5 c11pool5-renamed
    inc storage volume rename "incustest-$(basename "${INCUS_DIR}")-pool5" c11pool5-renamed c11pool5

    inc storage volume create "incustest-$(basename "${INCUS_DIR}")-pool5" c12pool5
    # should create snap0
    inc storage volume snapshot "incustest-$(basename "${INCUS_DIR}")-pool5" c12pool5
    # should create snap1
    inc storage volume snapshot "incustest-$(basename "${INCUS_DIR}")-pool5" c12pool5

    if [ "$incus_backend" = "lvm" ]; then
      inc init testimage c10pool6 -s "incustest-$(basename "${INCUS_DIR}")-pool6"
      inc list -c b c10pool6 | grep "incustest-$(basename "${INCUS_DIR}")-pool6"

      # Test if volume group renaming works by setting lvm.vg_name.
      inc storage set "incustest-$(basename "${INCUS_DIR}")-pool6" lvm.vg_name "incustest-$(basename "${INCUS_DIR}")-pool6-newName"

      inc storage set "incustest-$(basename "${INCUS_DIR}")-pool6" lvm.thinpool_name "incustest-$(basename "${INCUS_DIR}")-pool6-newThinpoolName"

      inc launch testimage c12pool6 -s "incustest-$(basename "${INCUS_DIR}")-pool6"
      inc list -c b c12pool6 | grep "incustest-$(basename "${INCUS_DIR}")-pool6"
      # grow lv
      inc config device set c12pool6 root size 30MiB
      inc restart c12pool6 --force
      # shrink lv
      inc config device set c12pool6 root size 25MiB
      inc restart c12pool6 --force

      inc init testimage c10pool11 -s "incustest-$(basename "${INCUS_DIR}")-pool11"
      inc list -c b c10pool11 | grep "incustest-$(basename "${INCUS_DIR}")-pool11"

      inc launch testimage c12pool11 -s "incustest-$(basename "${INCUS_DIR}")-pool11"
      inc list -c b c12pool11 | grep "incustest-$(basename "${INCUS_DIR}")-pool11"

      inc init testimage c10pool12 -s "incustest-$(basename "${INCUS_DIR}")-pool12"
      inc list -c b c10pool12 | grep "incustest-$(basename "${INCUS_DIR}")-pool12"

      inc launch testimage c12pool12 -s "incustest-$(basename "${INCUS_DIR}")-pool12"
      inc list -c b c12pool12 | grep "incustest-$(basename "${INCUS_DIR}")-pool12"

      inc init testimage c10pool13 -s "incustest-$(basename "${INCUS_DIR}")-pool13"
      inc list -c b c10pool13 | grep "incustest-$(basename "${INCUS_DIR}")-pool13"

      inc launch testimage c12pool13 -s "incustest-$(basename "${INCUS_DIR}")-pool13"
      inc list -c b c12pool13 | grep "incustest-$(basename "${INCUS_DIR}")-pool13"

      inc init testimage c10pool14 -s "incustest-$(basename "${INCUS_DIR}")-pool14"
      inc list -c b c10pool14 | grep "incustest-$(basename "${INCUS_DIR}")-pool14"

      inc launch testimage c12pool14 -s "incustest-$(basename "${INCUS_DIR}")-pool14"
      inc list -c b c12pool14 | grep "incustest-$(basename "${INCUS_DIR}")-pool14"

      inc init testimage c10pool15 -s "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15"
      inc list -c b c10pool15 | grep "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15"

      inc launch testimage c12pool15 -s "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15"
      inc list -c b c12pool15 | grep "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15"

      # Test that changing block filesystem works
      inc storage set "incustest-$(basename "${INCUS_DIR}")-pool6" volume.block.filesystem xfs
      inc init testimage c1pool6 -s "incustest-$(basename "${INCUS_DIR}")-pool6"
      inc storage set "incustest-$(basename "${INCUS_DIR}")-pool6" volume.block.filesystem btrfs
      inc storage set "incustest-$(basename "${INCUS_DIR}")-pool6" volume.size 120MiB
      inc init testimage c2pool6 -s "incustest-$(basename "${INCUS_DIR}")-pool6"

      inc storage volume create "incustest-$(basename "${INCUS_DIR}")-pool6" c10pool6
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool6" c10pool6 c10pool6 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool6" c10pool6 c10pool6 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool6" c10pool6 c10pool6 testDevice
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool6" custom/c10pool6 c10pool6 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool6" custom/c10pool6 c10pool6 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool6" c10pool6 c10pool6 testDevice

      inc storage volume create "incustest-$(basename "${INCUS_DIR}")-pool6" c12pool6
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool6" c12pool6 c12pool6 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool6" c12pool6 c12pool6 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool6" c12pool6 c12pool6 testDevice
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool6" custom/c12pool6 c12pool6 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool6" custom/c12pool6 c12pool6 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool6" c12pool6 c12pool6 testDevice

      inc storage volume create "incustest-$(basename "${INCUS_DIR}")-pool11" c10pool11
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool11" c10pool11 c10pool11 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool11" c10pool11 c10pool11 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool11" c10pool11 c10pool11 testDevice
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool11" custom/c10pool11 c10pool11 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool11" custom/c10pool11 c10pool11 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool11" c10pool11 c10pool11 testDevice

      inc storage volume create "incustest-$(basename "${INCUS_DIR}")-pool11" c12pool11
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool11" c12pool11 c10pool11 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool11" c12pool11 c10pool11 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool11" c12pool11 c10pool11 testDevice
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool11" custom/c12pool11 c10pool11 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool11" custom/c12pool11 c10pool11 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool11" c12pool11 c10pool11 testDevice

      inc storage volume create "incustest-$(basename "${INCUS_DIR}")-pool12" c10pool12
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool12" c10pool12 c10pool12 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool12" c10pool12 c10pool12 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool12" c10pool12 c10pool12 testDevice
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool12" custom/c10pool12 c10pool12 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool12" custom/c10pool12 c10pool12 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool12" c10pool12 c10pool12 testDevice

      inc storage volume create "incustest-$(basename "${INCUS_DIR}")-pool12" c12pool12
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool12" c12pool12 c12pool12 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool12" c12pool12 c12pool12 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool12" c12pool12 c12pool12 testDevice
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool12" custom/c12pool12 c12pool12 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool12" custom/c12pool12 c12pool12 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool12" c12pool12 c12pool12 testDevice

      inc storage volume create "incustest-$(basename "${INCUS_DIR}")-pool13" c10pool13
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool13" c10pool13 c10pool13 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool13" c10pool13 c10pool13 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool13" c10pool13 c10pool13 testDevice
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool13" custom/c10pool13 c10pool13 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool13" custom/c10pool13 c10pool13 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool13" c10pool13 c10pool13 testDevice

      inc storage volume create "incustest-$(basename "${INCUS_DIR}")-pool13" c12pool13
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool13" c12pool13 c12pool13 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool13" c12pool13 c12pool13 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool13" c12pool13 c12pool13 testDevice
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool13" custom/c12pool13 c12pool13 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool13" custom/c12pool13 c12pool13 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool13" c12pool13 c12pool13 testDevice

      inc storage volume create "incustest-$(basename "${INCUS_DIR}")-pool14" c10pool14
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool14" c10pool14 c10pool14 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool14" c10pool14 c10pool14 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool14" c10pool14 c10pool14 testDevice
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool14" custom/c10pool14 c10pool14 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool14" custom/c10pool14 c10pool14 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool14" c10pool14 c10pool14 testDevice

      inc storage volume create "incustest-$(basename "${INCUS_DIR}")-pool14" c12pool14
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool14" c12pool14 c12pool14 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool14" c12pool14 c12pool14 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool14" c12pool14 c12pool14 testDevice
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool14" custom/c12pool14 c12pool14 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool14" custom/c12pool14 c12pool14 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool14" c12pool14 c12pool14 testDevice

      inc storage volume create "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" c10pool15
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" c10pool15 c10pool15 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" c10pool15 c10pool15 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" c10pool15 c10pool15 testDevice
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" custom/c10pool15 c10pool15 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" custom/c10pool15 c10pool15 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" c10pool15 c10pool15 testDevice

      inc storage volume create "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" c12pool15
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" c12pool15 c12pool15 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" c12pool15 c12pool15 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" c12pool15 c12pool15 testDevice
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" custom/c12pool15 c12pool15 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" custom/c12pool15 c12pool15 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" c12pool15 c12pool15 testDevice
    fi

    if [ "$incus_backend" = "zfs" ]; then
      inc launch testimage c13pool7 -s "incustest-$(basename "${INCUS_DIR}")-pool7"
      inc launch testimage c14pool7 -s "incustest-$(basename "${INCUS_DIR}")-pool7"

      inc launch testimage c15pool8 -s "incustest-$(basename "${INCUS_DIR}")-pool8"
      inc launch testimage c16pool8 -s "incustest-$(basename "${INCUS_DIR}")-pool8"

      inc launch testimage c17pool9 -s "incustest-$(basename "${INCUS_DIR}")-pool9"
      inc launch testimage c18pool9 -s "incustest-$(basename "${INCUS_DIR}")-pool9"

      inc storage volume create "incustest-$(basename "${INCUS_DIR}")-pool7" c13pool7
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool7" c13pool7 c13pool7 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool7" c13pool7 c13pool7 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool7" c13pool7 c13pool7 testDevice
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool7" custom/c13pool7 c13pool7 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool7" custom/c13pool7 c13pool7 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool7" c13pool7 c13pool7 testDevice

      inc storage volume create "incustest-$(basename "${INCUS_DIR}")-pool7" c14pool7
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool7" c14pool7 c14pool7 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool7" c14pool7 c14pool7 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool7" c14pool7 c14pool7 testDevice
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool7" custom/c14pool7 c14pool7 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool7" custom/c14pool7 c14pool7 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool7" c14pool7 c14pool7 testDevice

      inc storage volume create "incustest-$(basename "${INCUS_DIR}")-pool8" c15pool8
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool8" c15pool8 c15pool8 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool8" c15pool8 c15pool8 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool8" c15pool8 c15pool8 testDevice
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool8" custom/c15pool8 c15pool8 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool8" custom/c15pool8 c15pool8 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool8" c15pool8 c15pool8 testDevice

      inc storage volume create "incustest-$(basename "${INCUS_DIR}")-pool8" c16pool8
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool8" c16pool8 c16pool8 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool8" c16pool8 c16pool8 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool8" c16pool8 c16pool8 testDevice
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool8" custom/c16pool8 c16pool8 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool8" custom/c16pool8 c16pool8 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool8" c16pool8 c16pool8 testDevice

      inc storage volume create "incustest-$(basename "${INCUS_DIR}")-pool9" c17pool9
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool9" c17pool9 c17pool9 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool9" c17pool9 c17pool9 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool9" c17pool9 c17pool9 testDevice
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool9" custom/c17pool9 c17pool9 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool9" custom/c17pool9 c17pool9 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool9" c17pool9 c17pool9 testDevice

      inc storage volume create "incustest-$(basename "${INCUS_DIR}")-pool9" c18pool9
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool9" c18pool9 c18pool9 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool9" c18pool9 c18pool9 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool9" c18pool9 c18pool9 testDevice
      inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool9" custom/c18pool9 c18pool9 testDevice /opt
      ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool9" custom/c18pool9 c18pool9 testDevice2 /opt || false
      inc storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool9" c18pool9 c18pool9 testDevice
      inc storage volume rename "incustest-$(basename "${INCUS_DIR}")-pool9" c18pool9 c18pool9-renamed
      inc storage volume rename "incustest-$(basename "${INCUS_DIR}")-pool9" c18pool9-renamed c18pool9
    fi

    if [ "$incus_backend" = "zfs" ]; then
      inc delete -f c1pool1
      inc delete -f c3pool1

      inc delete -f c4pool2
      inc delete -f c2pool2

      inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool1" c1pool1
      inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool1" c2pool2
      inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool2" c3pool1
      inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool2" c4pool2
    fi

    if [ "$incus_backend" = "btrfs" ]; then
      inc delete -f c5pool3
      inc delete -f c7pool3

      inc delete -f c8pool4
      inc delete -f c6pool4

      inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool3" c5pool3
      inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool4" c6pool4
      inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool3" c7pool3
      inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool4" c8pool4
    fi

    inc delete -f c9pool5
    inc delete -f c11pool5

    inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool5" c9pool5
    inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool5" c11pool5
    inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool5" c12pool5

    if [ "$incus_backend" = "lvm" ]; then
      inc delete -f c1pool6
      inc delete -f c2pool6
      inc delete -f c10pool6
      inc delete -f c12pool6

      inc delete -f c10pool11
      inc delete -f c12pool11

      inc delete -f c10pool12
      inc delete -f c12pool12

      inc delete -f c10pool13
      inc delete -f c12pool13

      inc delete -f c10pool14
      inc delete -f c12pool14

      inc delete -f c10pool15
      inc delete -f c12pool15

      inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool6" c10pool6
      inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool6"  c12pool6
      inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool11" c10pool11
      inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool11" c12pool11
      inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool12" c10pool12
      inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool12" c12pool12
      inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool13" c10pool13
      inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool13" c12pool13
      inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool14" c10pool14
      inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool14" c12pool14
      inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" c10pool15
      inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" c12pool15
    fi

    if [ "$incus_backend" = "zfs" ]; then
      inc delete -f c13pool7
      inc delete -f c14pool7

      inc delete -f c15pool8
      inc delete -f c16pool8

      inc delete -f c17pool9
      inc delete -f c18pool9

      inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool7" c13pool7
      inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool7" c14pool7
      inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool8" c15pool8
      inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool8" c16pool8
      inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool9" c17pool9
      inc storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool9" c18pool9
    fi

    inc image delete testimage

    if [ "$incus_backend" = "zfs" ]; then
      inc storage delete "incustest-$(basename "${INCUS_DIR}")-pool7"
      inc storage delete "incustest-$(basename "${INCUS_DIR}")-pool8"
      inc storage delete "incustest-$(basename "${INCUS_DIR}")-pool9"
      # shellcheck disable=SC2154
      deconfigure_loop_device "${loop_file_4}" "${loop_device_4}"

      inc storage delete "incustest-$(basename "${INCUS_DIR}")-pool1"

      inc storage delete "incustest-$(basename "${INCUS_DIR}")-pool2"
      # shellcheck disable=SC2154
      deconfigure_loop_device "${loop_file_1}" "${loop_device_1}"
    fi

    if [ "$incus_backend" = "btrfs" ]; then
      inc storage delete "incustest-$(basename "${INCUS_DIR}")-pool4"
      # shellcheck disable=SC2154
      deconfigure_loop_device "${loop_file_2}" "${loop_device_2}"
    fi

    if [ "$incus_backend" = "lvm" ]; then
      inc storage delete "incustest-$(basename "${INCUS_DIR}")-pool6"
      # shellcheck disable=SC2154
      pvremove -ff "${loop_device_3}" || true
      # shellcheck disable=SC2154
      deconfigure_loop_device "${loop_file_3}" "${loop_device_3}"

      inc storage delete "incustest-$(basename "${INCUS_DIR}")-pool11"
      # shellcheck disable=SC2154
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-pool11-test_vg_2" || true
      pvremove -ff "${loop_device_6}" || true
      # shellcheck disable=SC2154
      deconfigure_loop_device "${loop_file_6}" "${loop_device_6}"

      inc storage delete "incustest-$(basename "${INCUS_DIR}")-pool12"
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-pool12-test_vg_3" || true
      pvremove -ff "${loop_device_7}" || true
      # shellcheck disable=SC2154
      deconfigure_loop_device "${loop_file_7}" "${loop_device_7}"

      inc storage delete "incustest-$(basename "${INCUS_DIR}")-pool13"
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-pool13-test_vg_4" || true
      pvremove -ff "${loop_device_8}" || true
      # shellcheck disable=SC2154
      deconfigure_loop_device "${loop_file_8}" "${loop_device_8}"

      inc storage delete "incustest-$(basename "${INCUS_DIR}")-pool14"
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-pool14" || true

      inc storage delete "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15"
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool15" || true

      inc storage delete "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool16"
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool16" || true

      inc storage delete "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool17"
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool17" || true

      inc storage delete "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool18"
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool18" || true

      inc storage delete "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool19"
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool19" || true

      inc storage delete "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool20"
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool20" || true

      inc storage delete "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool21"
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool21" || true

      inc storage delete "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool22"
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool22" || true

      inc storage delete "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool23"
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool23" || true

      inc storage delete "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool24"
      vgremove -ff "incustest-$(basename "${INCUS_DIR}")-non-thinpool-pool24" || true

      inc storage delete "incustest-$(basename "${INCUS_DIR}")-valid-lvm-pool-config-pool25"
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
    inc launch testimage quota1
    rootOrigSizeKiB=$(inc exec quota1 -- df -P / | tail -n1 | awk '{print $2}')
    rootOrigMinSizeKiB=$((rootOrigSizeKiB-2000))
    rootOrigMaxSizeKiB=$((rootOrigSizeKiB+2000))

    inc profile device set default root size "${QUOTA1}"
    inc stop -f quota1
    inc start quota1

    # BTRFS quota isn't accessible with the df tool.
    if [ "$incus_backend" != "btrfs" ]; then
    rootSizeKiB=$(inc exec quota1 -- df -P / | tail -n1 | awk '{print $2}')
      if [ "$rootSizeKiB" -gt "$rootMaxKiB1" ] || [ "$rootSizeKiB" -lt "$rootMinKiB1" ] ; then
        echo "root size not within quota range"
        false
      fi
    fi

    inc launch testimage quota2
    inc stop -f quota2
    inc start quota2

    inc init testimage quota3
    inc start quota3

    inc profile device set default root size "${QUOTA2}"

    inc stop -f quota1
    inc start quota1

    inc stop -f quota2
    inc start quota2
    if [ "$incus_backend" != "btrfs" ]; then
      rootSizeKiB=$(inc exec quota2 -- df -P / | tail -n1 | awk '{print $2}')
      if [ "$rootSizeKiB" -gt "$rootMaxKiB2" ] || [ "$rootSizeKiB" -lt "$rootMinKiB2" ] ; then
        echo "root size not within quota range"
        false
      fi
    fi

    inc stop -f quota3
    inc start quota3

    inc profile device unset default root size

    # Only ZFS supports hot quota changes (LVM requires a reboot).
    if [ "$incus_backend" = "zfs" ]; then
      rootSizeKiB=$(inc exec quota1 -- df -P / | tail -n1 | awk '{print $2}')
      if [ "$rootSizeKiB" -gt "$rootOrigMaxSizeKiB" ] || [ "$rootSizeKiB" -lt "$rootOrigMinSizeKiB" ] ; then
        echo "original root size not restored"
        false
      fi
    fi

    inc stop -f quota1
    inc start quota1
    if [ "$incus_backend" = "zfs" ]; then
      rootSizeKiB=$(inc exec quota1 -- df -P / | tail -n1 | awk '{print $2}')
      if [ "$rootSizeKiB" -gt "$rootOrigMaxSizeKiB" ] || [ "$rootSizeKiB" -lt "$rootOrigMinSizeKiB" ] ; then
        echo "original root size not restored"
        false
      fi
    fi

    inc stop -f quota2
    inc start quota2

    inc stop -f quota3
    inc start quota3

    inc delete -f quota1
    inc delete -f quota2
    inc delete -f quota3
  fi

  if [ "${incus_backend}" = "btrfs" ]; then
    # shellcheck disable=SC2031
    pool_name="incustest-$(basename "${INCUS_DIR}")-quota"

    # shellcheck disable=SC1009
    inc storage create "${pool_name}" btrfs

    # Import image into default storage pool.
    ensure_import_testimage

    # Launch container.
    inc launch -s "${pool_name}" testimage c1

    # Disable quotas. The usage should be 0.
    # shellcheck disable=SC2031
    btrfs quota disable "${INCUS_DIR}/storage-pools/${pool_name}"
    usage=$(inc query /1.0/instances/c1/state | jq '.disk.root')
    [ "${usage}" = "null" ]

    # Enable quotas. The usage should then be > 0.
    # shellcheck disable=SC2031
    btrfs quota enable "${INCUS_DIR}/storage-pools/${pool_name}"
    usage=$(inc query /1.0/instances/c1/state | jq '.disk.root.usage')
    [ "${usage}" -gt 0 ]

    # Clean up everything.
    inc rm -f c1
    inc storage delete "${pool_name}"
  fi

  # Test removing storage pools only containing image volumes
  # shellcheck disable=SC2031,2269
  INCUS_DIR="${INCUS_DIR}"
  storage_pool="incustest-$(basename "${INCUS_DIR}")-pool26"
  inc storage create "$storage_pool" "$incus_backend"
  inc init -s "${storage_pool}" testimage c1
  # The storage pool will not be removed since it has c1 attached to it
  ! inc storage delete "${storage_pool}" || false
  inc delete c1
  # The storage pool will be deleted since the testimage is also attached to
  # the default pool
  inc storage delete "${storage_pool}"
  inc image show testimage

  # Test storage pool resize
  if [ "${incus_backend}" = "btrfs" ] || [ "${incus_backend}" = "lvm" ] || [ "${incus_backend}" = "zfs" ]; then
    # shellcheck disable=SC1009
    pool_name="incustest-$(basename "${INCUS_DIR}")-pool1"

    inc storage create "${pool_name}" "${incus_backend}" size=1GiB

    inc launch testimage c1 -s "${pool_name}"

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
    inc storage set "${pool_name}" size=2GiB

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
    ! inc storage set "${pool_name}" size=1GiB || false

    # Ensure the pool is still usable after resizing by launching an instance
    inc launch testimage c2 -s "${pool_name}"

    inc rm -f c1 c2
    inc storage rm "${pool_name}"
  fi

  # shellcheck disable=SC2031,2269
  INCUS_DIR="${INCUS_DIR}"
  kill_incus "${INCUS_STORAGE_DIR}"
}
