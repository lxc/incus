test_storage_volume_recover() {
  INCUS_IMPORT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_IMPORT_DIR}"
  spawn_incus "${INCUS_IMPORT_DIR}" true

  poolName=$(incus profile device get default root pool)
  poolDriver=$(incus storage show "${poolName}" | awk '/^driver:/ {print $2}')

  # Create custom block volume.
  incus storage volume create "${poolName}" vol1 --type=block

  # Import ISO.
  truncate -s 8MiB foo.iso
  incus storage volume import "${poolName}" ./foo.iso vol2 --type=iso

  # Delete database entry of the created custom block volume.
  incus admin sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='vol1'"
  incus admin sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='vol2'"

  # Ensure the custom block volume is no longer listed.
  ! incus storage volume show "${poolName}" vol1 || false
  ! incus storage volume show "${poolName}" vol2 || false

  if [ "$poolDriver" = "zfs" ]; then
    # Create filesystem volume.
    incus storage volume create "${poolName}" vol3

    # Create block_mode enabled volume.
    incus storage volume create "${poolName}" vol4 zfs.block_mode=true size=200MiB

    # Delete database entries of the created custom volumes.
    incus admin sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='vol3'"
    incus admin sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='vol4'"

    # Ensure the custom volumes are no longer listed.
    ! incus storage volume show "${poolName}" vol3 || false
    ! incus storage volume show "${poolName}" vol4 || false
  fi

  # Recover custom block volume.
  cat <<EOF | incus admin recover
no
yes
yes
EOF

  # Ensure custom storage volume has been recovered.
  incus storage volume show "${poolName}" vol1 | grep -q 'content_type: block'
  incus storage volume show "${poolName}" vol2 | grep -q 'content_type: iso'

  if [ "$poolDriver" = "zfs" ]; then
    # Ensure custom storage volumes have been recovered.
    incus storage volume show "${poolName}" vol3 | grep -q 'content_type: filesystem'
    incus storage volume show "${poolName}" vol4 | grep -q 'content_type: filesystem'

    # Cleanup
    incus storage volume delete "${poolName}" vol3
    incus storage volume delete "${poolName}" vol4
  fi

  # Cleanup
  rm -f foo.iso
  incus storage volume delete "${poolName}" vol1
  incus storage volume delete "${poolName}" vol2
  shutdown_incus "${INCUS_IMPORT_DIR}"
}

test_container_recover() {
  INCUS_IMPORT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_IMPORT_DIR}"
  spawn_incus "${INCUS_IMPORT_DIR}" true

  (
    set -e

    # shellcheck disable=SC2030
    INCUS_DIR=${INCUS_IMPORT_DIR}
    incus_backend=$(storage_backend "$INCUS_DIR")

    ensure_import_testimage

    poolName=$(incus profile device get default root pool)
    poolDriver=$(incus storage show "${poolName}" | awk '/^driver:/ {print $2}')

    incus storage set "${poolName}" user.foo=bah
    incus project create test -c features.images=false -c features.profiles=true -c features.storage.volumes=true
    incus profile device add default root disk path=/ pool="${poolName}" --project test
    incus profile device add default eth0 nic nictype=p2p --project test
    incus project switch test

    # Basic no-op check.
    cat <<EOF | incus admin recover | grep "No unknown storage pools or volumes found. Nothing to do."
no
yes
EOF

    # Recover container and custom volume that isn't mounted.
    incus init testimage c1
    incus storage volume create "${poolName}" vol1_test
    incus storage volume attach "${poolName}" vol1_test c1 /mnt
    incus start c1
    incus exec c1 --project test -- mount | grep /mnt
    echo "hello world" | incus exec c1 --project test -- tee /mnt/test.txt
    incus exec c1 --project test -- grep -xF "hello world" /mnt/test.txt
    incus stop -f c1
    incus snapshot create c1
    incus info c1

    incus storage volume snapshot create "${poolName}" vol1_test snap0
    incus storage volume show "${poolName}" vol1_test
    incus storage volume snapshot show "${poolName}" vol1_test/snap0

    # Remove container DB records and symlink.
    incus admin sql global "PRAGMA foreign_keys=ON; DELETE FROM instances WHERE name='c1'"
    incus admin sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='c1'"
    rm "${INCUS_DIR}/containers/test_c1"

    # Remove mount directories if block backed storage.
    if [ "$poolDriver" != "dir" ] && [ "$poolDriver" != "btrfs" ] && [ "$poolDriver" != "cephfs" ]; then
      rmdir "${INCUS_DIR}/storage-pools/${poolName}/containers/test_c1"
      rmdir "${INCUS_DIR}/storage-pools/${poolName}/containers-snapshots/test_c1/snap0"
      rmdir "${INCUS_DIR}/storage-pools/${poolName}/containers-snapshots/test_c1"
    fi

    # Remove custom volume DB record.
    incus admin sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='vol1_test'"

    # Remove mount directories if block backed storage.
    if [ "$poolDriver" != "dir" ] && [ "$poolDriver" != "btrfs" ] && [ "$poolDriver" != "cephfs" ]; then
      rmdir "${INCUS_DIR}/storage-pools/${poolName}/custom/test_vol1_test"
      rmdir "${INCUS_DIR}/storage-pools/${poolName}/custom-snapshots/test_vol1_test/snap0"
      rmdir "${INCUS_DIR}/storage-pools/${poolName}/custom-snapshots/test_vol1_test"
    fi

    # Check container appears removed.
    ! ls "${INCUS_DIR}/containers/test_c1" || false
    ! incus info c1 || false
    ! incus storage volume show "${poolName}" container/c1 || false
    ! incus storage volume snapshot show "${poolName}" container/c1/snap0 || false

    if [ "$poolDriver" != "dir" ] && [ "$poolDriver" != "btrfs" ] && [ "$poolDriver" != "cephfs" ]; then
      ! ls "${INCUS_DIR}/storage-pools/${poolName}/containers/test_c1" || false
      ! ls "${INCUS_DIR}/storage-pools/${poolName}/containers-snapshots/test_c1" || false
    fi

    # Check custom volume appears removed.
    ! incus storage volume show "${poolName}" vol1_test || false
    ! incus storage volume snapshot show "${poolName}" vol1_test/snap0 || false

    # Shutdown Incus so pools are unmounted.
    shutdown_incus "${INCUS_DIR}"

    # Remove empty directory structures for pool drivers that don't have a mounted root.
    # This is so we can test the restoration of the storage pool directory structure.
    if [ "$poolDriver" != "dir" ] && [ "$poolDriver" != "btrfs" ] && [ "$poolDriver" != "cephfs" ]; then
      rm -rvf "${INCUS_DIR}/storage-pools/${poolName}"
    fi

    respawn_incus "${INCUS_DIR}" true

    cat <<EOF | incus admin recover
no
yes
yes
EOF

    # Check container mount directories have been restored.
    ls "${INCUS_DIR}/containers/test_c1"
    ls "${INCUS_DIR}/storage-pools/${poolName}/containers/test_c1"
    ls "${INCUS_DIR}/storage-pools/${poolName}/containers-snapshots/test_c1/snap0"

    # Check custom volume mount directories have been restored.
    ls "${INCUS_DIR}/storage-pools/${poolName}/custom/test_vol1_test"
    ls "${INCUS_DIR}/storage-pools/${poolName}/custom-snapshots/test_vol1_test/snap0"

    # Check custom volume record exists with snapshot.
    incus storage volume show "${poolName}" vol1_test
    incus storage volume snapshot show "${poolName}" vol1_test/snap0

    # Check snapshot exists and container can be started.
    incus info c1 | grep snap0
    incus storage volume ls "${poolName}"
    incus storage volume show "${poolName}" container/c1
    incus storage volume snapshot show "${poolName}" container/c1/snap0
    incus start c1
    incus exec c1 --project test -- hostname

    # Check custom volume accessible.
    incus exec c1 --project test -- mount | grep /mnt
    incus exec c1 --project test -- grep -xF "hello world" /mnt/test.txt

    # Check snashot can be restored.
    incus snapshot restore c1 snap0
    incus info c1
    incus exec c1 --project test -- hostname

    # Recover container that is running.
    incus admin sql global "PRAGMA foreign_keys=ON; DELETE FROM instances WHERE name='c1'"
    incus admin sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='c1'"

    # Restart Incus so internal mount counters are cleared for deleted (but running) container.
    shutdown_incus "${INCUS_DIR}"
    respawn_incus "${INCUS_DIR}" true

    cat <<EOF | incus admin recover
no
yes
yes
EOF

    incus info c1 | grep snap0
    incus exec c1 --project test -- hostname
    incus snapshot restore c1 snap0
    incus info c1
    incus exec c1 --project test -- hostname

    # Test recover after pool DB config deletion too.
    poolConfigBefore=$(incus admin sql global "SELECT key,value FROM storage_pools_config JOIN storage_pools ON storage_pools.id = storage_pools_config.storage_pool_id WHERE storage_pools.name = '${poolName}' ORDER BY key")
    poolSource=$(incus storage get "${poolName}" source)
    poolExtraConfig=""

    case $poolDriver in
      lvm)
        poolExtraConfig="lvm.vg_name=$(incus storage get "${poolName}" lvm.vg_name)
"
      ;;
      zfs)
        poolExtraConfig="zfs.pool_name=$(incus storage get "${poolName}" zfs.pool_name)
"
      ;;
      ceph)
        poolExtraConfig="ceph.cluster_name=$(incus storage get "${poolName}" ceph.cluster_name)
ceph.osd.pool_name=$(incus storage get "${poolName}" ceph.osd.pool_name)
ceph.user.name=$(incus storage get "${poolName}" ceph.user.name)
"
      ;;
    esac

    incus admin sql global "PRAGMA foreign_keys=ON; DELETE FROM instances WHERE name='c1'"
    incus admin sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_volumes WHERE name='c1'"
    incus admin sql global "PRAGMA foreign_keys=ON; DELETE FROM storage_pools WHERE name='${poolName}'"

    cat <<EOF |incus admin recover
yes
${poolName}
${poolDriver}
${poolSource}
${poolExtraConfig}
no
yes
yes
EOF

    # Check recovered pool config (from instance backup file) matches what originally was there.
    incus storage show "${poolName}"
    poolConfigAfter=$(incus admin sql global "SELECT key,value FROM storage_pools_config JOIN storage_pools ON storage_pools.id = storage_pools_config.storage_pool_id WHERE storage_pools.name = '${poolName}' ORDER BY key")
    echo "Before:"
    echo "${poolConfigBefore}"

    echo "After:"
    echo "${poolConfigAfter}"

    [ "${poolConfigBefore}" =  "${poolConfigAfter}" ] || false
    incus storage show "${poolName}"

    incus info c1 | grep snap0
    incus exec c1 --project test -- ls
    incus snapshot restore c1 snap0
    incus info c1
    incus exec c1 --project test -- ls
    incus delete -f c1
    incus storage volume delete "${poolName}" vol1_test
    incus project switch default
    incus project delete test
  )

  # shellcheck disable=SC2031,2269
  INCUS_DIR=${INCUS_DIR}
  kill_incus "${INCUS_IMPORT_DIR}"
}

test_bucket_recover() {
  if ! command -v "minio" >/dev/null 2>&1; then
    echo "==> SKIP: Skip bucket recovery test due to missing minio"
    return
  fi

  (
    set -e

    poolName=$(incus profile device get default root pool)
    poolDriver=$(incus storage show "${poolName}" | awk '/^driver:/ {print $2}')
    bucketName="bucket123"

    # Skip ceph driver - ceph does not support storage buckets
    if [ "${poolDriver}" = "ceph" ]; then
      return 0
    fi

    # Create storage bucket
    incus storage bucket create "${poolName}" "${bucketName}"

    # Create storage bucket keys
    key1=$(incus storage bucket key create "${poolName}" "${bucketName}" key1 --role admin)
    key2=$(incus storage bucket key create "${poolName}" "${bucketName}" key2 --role read-only)
    key1_accessKey=$(echo "$key1" | awk '/^Access key/ { print $3 }')
    key1_secretKey=$(echo "$key1" | awk '/^Secret key/ { print $3 }')
    key2_accessKey=$(echo "$key2" | awk '/^Access key/ { print $3 }')
    key2_secretKey=$(echo "$key2" | awk '/^Secret key/ { print $3 }')

    # Remove bucket from global DB
    incus admin sql global "delete from storage_buckets where name = '${bucketName}'"

    # Recover bucket
    cat <<EOF | incus admin recover
no
yes
yes
EOF

    # Verify bucket is recovered
    incus storage bucket ls "${poolName}" --format compact | grep "${bucketName}"

    # Verify bucket key with role admin is recovered
    recoveredKey1=$(incus storage bucket key show "${poolName}" "${bucketName}" "${key1_accessKey}")
    echo "${recoveredKey1}" | grep "role: admin"
    echo "${recoveredKey1}" | grep "access-key: ${key1_accessKey}"
    echo "${recoveredKey1}" | grep "secret-key: ${key1_secretKey}"

    # Verify bucket key with role read-only is recovered
    recoveredKey2=$(incus storage bucket key show "${poolName}" "${bucketName}" "${key2_accessKey}")
    echo "${recoveredKey2}" | grep "role: read-only"
    echo "${recoveredKey2}" | grep "access-key: ${key2_accessKey}"
    echo "${recoveredKey2}" | grep "secret-key: ${key2_secretKey}"
  )
}

test_backup_import() {
  test_backup_import_with_project
  test_backup_import_with_project fooproject
}

test_backup_import_with_project() {
  project="default"

  if [ "$#" -ne 0 ]; then
    # Create a projects
    project="$1"
    incus project create "$project"
    incus project create "$project-b"
    incus project switch "$project"

    deps/import-busybox --project "$project" --alias testimage
    deps/import-busybox --project "$project-b" --alias testimage

    # Add a root device to the default profile of the project
    pool="incustest-$(basename "${INCUS_DIR}")"
    incus profile device add default root disk path="/" pool="${pool}"
    incus profile device add default root disk path="/" pool="${pool}" --project "$project-b"
  fi

  ensure_import_testimage

  # shellcheck disable=2153
  ensure_has_localhost_remote "${INCUS_ADDR}"

  incus launch testimage c1
  incus launch testimage c2
  incus snapshot create c2

  incus_backend=$(storage_backend "$INCUS_DIR")

  # container only

  # create backup
  if [ "$incus_backend" = "btrfs" ] || [ "$incus_backend" = "zfs" ]; then
    incus export c1 "${INCUS_DIR}/c1-optimized.tar.gz" --optimized-storage --instance-only
  fi

  incus export c1 "${INCUS_DIR}/c1.tar.gz" --instance-only
  incus delete --force c1

  # import backup, and ensure it's valid and runnable
  incus import "${INCUS_DIR}/c1.tar.gz"
  incus info c1
  incus start c1
  incus delete --force c1

  if [ "$incus_backend" = "btrfs" ] || [ "$incus_backend" = "zfs" ]; then
    incus import "${INCUS_DIR}/c1-optimized.tar.gz"
    incus info c1
    incus start c1
    incus delete --force c1
  fi

  # with snapshots

  if [ "$incus_backend" = "btrfs" ] || [ "$incus_backend" = "zfs" ]; then
    incus export c2 "${INCUS_DIR}/c2-optimized.tar.gz" --optimized-storage
  fi

  incus export c2 "${INCUS_DIR}/c2.tar.gz"
  incus delete --force c2

  incus import "${INCUS_DIR}/c2.tar.gz"
  incus import "${INCUS_DIR}/c2.tar.gz" c3
  incus info c2 | grep snap0
  incus info c3 | grep snap0
  incus start c2
  incus start c3
  incus stop c2 --force
  incus stop c3 --force

  if [ "$#" -ne 0 ]; then
    # Import into different project (before deleting earlier import).
    incus import "${INCUS_DIR}/c2.tar.gz" --project "$project-b"
    incus import "${INCUS_DIR}/c2.tar.gz" --project "$project-b" c3
    incus info c2 --project "$project-b" | grep snap0
    incus info c3 --project "$project-b" | grep snap0
    incus start c2 --project "$project-b"
    incus start c3 --project "$project-b"
    incus stop c2 --project "$project-b" --force
    incus stop c3 --project "$project-b" --force
    incus snapshot restore c2 snap0 --project "$project-b"
    incus snapshot restore c3 snap0 --project "$project-b"
    incus delete --force c2 --project "$project-b"
    incus delete --force c3 --project "$project-b"
  fi

  incus snapshot restore c2 snap0
  incus snapshot restore c3 snap0
  incus start c2
  incus start c3
  incus delete --force c2
  incus delete --force c3


  if [ "$incus_backend" = "btrfs" ] || [ "$incus_backend" = "zfs" ]; then
    incus import "${INCUS_DIR}/c2-optimized.tar.gz"
    incus import "${INCUS_DIR}/c2-optimized.tar.gz" c3
    incus info c2 | grep snap0
    incus info c3 | grep snap0
    incus start c2
    incus start c3
    incus stop c2 --force
    incus stop c3 --force
    incus snapshot restore c2 snap0
    incus snapshot restore c3 snap0
    incus start c2
    incus start c3
    incus delete --force c2
    incus delete --force c3
  fi

  # Test exporting container and snapshot names that container hyphens.
  # Also check that the container storage volume config is correctly captured and restored.
  default_pool="$(incus profile device get default root pool)"

  incus launch testimage c1-foo
  incus storage volume set "${default_pool}" container/c1-foo user.foo=c1-foo-snap0
  incus snapshot create c1-foo c1-foo-snap0
  incus storage volume set "${default_pool}" container/c1-foo user.foo=c1-foo-snap1
  incus snapshot create c1-foo c1-foo-snap1
  incus storage volume set "${default_pool}" container/c1-foo user.foo=post-c1-foo-snap1

  incus export c1-foo "${INCUS_DIR}/c1-foo.tar.gz"
  incus delete --force c1-foo

  incus import "${INCUS_DIR}/c1-foo.tar.gz"
  incus storage volume ls "${default_pool}"
  incus storage volume get "${default_pool}" container/c1-foo user.foo | grep -Fx "post-c1-foo-snap1"
  incus storage volume get "${default_pool}" container/c1-foo/c1-foo-snap0 user.foo | grep -Fx "c1-foo-snap0"
  incus storage volume get "${default_pool}" container/c1-foo/c1-foo-snap1 user.foo | grep -Fx "c1-foo-snap1"
  incus delete --force c1-foo

  # Create new storage pools
  incus storage create pool_1 dir
  incus storage create pool_2 dir

  # Export created container
  incus init testimage c3 -s pool_1
  incus export c3 "${INCUS_DIR}/c3.tar.gz"

  # Remove container and storage pool
  incus rm -f c3
  incus storage delete pool_1

  # This should succeed as it will fall back on the default pool
  incus import "${INCUS_DIR}/c3.tar.gz"

  incus rm -f c3

  # Remove root device
  incus profile device remove default root

  # This should fail as the expected storage is not available, and there is no default
  ! incus import "${INCUS_DIR}/c3.tar.gz" || false

  # Specify pool explicitly; this should fails as the pool doesn't exist
  ! incus import "${INCUS_DIR}/c3.tar.gz" -s pool_1 || false

  # Specify pool explicitly
  incus import "${INCUS_DIR}/c3.tar.gz" -s pool_2

  incus rm -f c3

  # Reset default storage pool
  incus profile device add default root disk path=/ pool="${default_pool}"

  incus storage delete pool_2

  if [ "$#" -ne 0 ]; then
    incus image rm testimage
    incus image rm testimage --project "$project-b"
    incus project switch default
    incus project delete "$project"
    incus project delete "$project-b"
  fi
}

test_backup_export() {
  test_backup_export_with_project
  test_backup_export_with_project fooproject
}

test_backup_export_with_project() {
  project="default"

  if [ "$#" -ne 0 ]; then
    # Create a project
    project="$1"
    incus project create "$project"
    incus project switch "$project"

    deps/import-busybox --project "$project" --alias testimage

    # Add a root device to the default profile of the project
    pool="incustest-$(basename "${INCUS_DIR}")"
    incus profile device add default root disk path="/" pool="${pool}"
  fi

  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  incus launch testimage c1
  incus snapshot create c1

  mkdir "${INCUS_DIR}/optimized" "${INCUS_DIR}/non-optimized"
  incus_backend=$(storage_backend "$INCUS_DIR")

  # container only

  if [ "$incus_backend" = "btrfs" ] || [ "$incus_backend" = "zfs" ]; then
    incus export c1 "${INCUS_DIR}/c1-optimized.tar.gz" --optimized-storage --instance-only
    tar -xzf "${INCUS_DIR}/c1-optimized.tar.gz" -C "${INCUS_DIR}/optimized"

    [ -f "${INCUS_DIR}/optimized/backup/index.yaml" ]
    [ -f "${INCUS_DIR}/optimized/backup/container.bin" ]
    [ ! -d "${INCUS_DIR}/optimized/backup/snapshots" ]
  fi

  incus export c1 "${INCUS_DIR}/c1.tar.gz" --instance-only
  tar -xzf "${INCUS_DIR}/c1.tar.gz" -C "${INCUS_DIR}/non-optimized"

  # check tarball content
  [ -f "${INCUS_DIR}/non-optimized/backup/index.yaml" ]
  [ -d "${INCUS_DIR}/non-optimized/backup/container" ]
  [ ! -d "${INCUS_DIR}/non-optimized/backup/snapshots" ]

  rm -rf "${INCUS_DIR}/non-optimized/"* "${INCUS_DIR}/optimized/"*

  # with snapshots

  if [ "$incus_backend" = "btrfs" ] || [ "$incus_backend" = "zfs" ]; then
    incus export c1 "${INCUS_DIR}/c1-optimized.tar.gz" --optimized-storage
    tar -xzf "${INCUS_DIR}/c1-optimized.tar.gz" -C "${INCUS_DIR}/optimized"

    [ -f "${INCUS_DIR}/optimized/backup/index.yaml" ]
    [ -f "${INCUS_DIR}/optimized/backup/container.bin" ]
    [ -f "${INCUS_DIR}/optimized/backup/snapshots/snap0.bin" ]
  fi

  incus export c1 "${INCUS_DIR}/c1.tar.gz"
  tar -xzf "${INCUS_DIR}/c1.tar.gz" -C "${INCUS_DIR}/non-optimized"

  # check tarball content
  [ -f "${INCUS_DIR}/non-optimized/backup/index.yaml" ]
  [ -d "${INCUS_DIR}/non-optimized/backup/container" ]
  [ -d "${INCUS_DIR}/non-optimized/backup/snapshots/snap0" ]

  incus delete --force c1
  rm -rf "${INCUS_DIR}/optimized" "${INCUS_DIR}/non-optimized"

  # Check if hyphens cause issues when creating backups
  incus launch testimage c1-foo
  incus snapshot create c1-foo

  incus export c1-foo "${INCUS_DIR}/c1-foo.tar.gz"

  incus delete --force c1-foo

  if [ "$#" -ne 0 ]; then
    incus image rm testimage
    incus project switch default
    incus project delete "$project"
  fi
}

test_backup_rename() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  if ! incus query -X POST /1.0/instances/c1/backups/backupmissing -d '{\"name\": \"backupnewname\"}' --wait 2>&1 | grep -q "Error: Instance not found" ; then
    echo "invalid rename response for missing container"
    false
  fi

  incus init testimage c1

  if ! incus query -X POST /1.0/instances/c1/backups/backupmissing -d '{\"name\": \"backupnewname\"}' --wait 2>&1 | grep -q "Error: Load backup from database: Instance backup not found" ; then
    echo "invalid rename response for missing backup"
    false
  fi

  # Create backup
  incus query -X POST --wait -d '{\"name\":\"foo\"}' /1.0/instances/c1/backups

  # All backups should be listed
  incus query /1.0/instances/c1/backups | jq .'[0]' | grep instances/c1/backups/foo

  # The specific backup should exist
  incus query /1.0/instances/c1/backups/foo

  # Rename the container which should rename the backup(s) as well
  incus mv c1 c2

  # All backups should be listed
  incus query /1.0/instances/c2/backups | jq .'[0]' | grep instances/c2/backups/foo

  # The specific backup should exist
  incus query /1.0/instances/c2/backups/foo

  # The old backup should not exist
  ! incus query /1.0/instances/c1/backups/foo || false

  incus delete --force c2
}

test_backup_volume_export() {
  test_backup_volume_export_with_project default "incustest-$(basename "${INCUS_DIR}")"
  test_backup_volume_export_with_project fooproject "incustest-$(basename "${INCUS_DIR}")"

  if [ "$incus_backend" = "ceph" ] && [ -n "${INCUS_CEPH_CEPHFS:-}" ]; then
    custom_vol_pool="incustest-$(basename "${INCUS_DIR}")-cephfs"
    incus storage create "${custom_vol_pool}" cephfs source="${INCUS_CEPH_CEPHFS}/$(basename "${INCUS_DIR}")-cephfs"

    test_backup_volume_export_with_project default "${custom_vol_pool}"
    test_backup_volume_export_with_project fooproject "${custom_vol_pool}"

    incus storage rm "${custom_vol_pool}"
  fi
}

test_backup_volume_export_with_project() {
  pool="incustest-$(basename "${INCUS_DIR}")"
  project="$1"
  custom_vol_pool="$2"

  if [ "${project}" != "default" ]; then
    # Create a project.
    incus project create "$project"
    incus project create "$project-b"
    incus project switch "$project"

    deps/import-busybox --project "$project" --alias testimage
    deps/import-busybox --project "$project-b" --alias testimage

    # Add a root device to the default profile of the project.
    incus profile device add default root disk path="/" pool="${pool}"
  fi

  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  mkdir "${INCUS_DIR}/optimized" "${INCUS_DIR}/non-optimized"
  incus_backend=$(storage_backend "$INCUS_DIR")

  # Create test container.
  incus init testimage c1

  # Create custom storage volume.
  incus storage volume create "${custom_vol_pool}" testvol

  # Attach storage volume to the test container and start.
  incus storage volume attach "${custom_vol_pool}" testvol c1 /mnt
  incus start c1

  # Create file on the custom volume.
  echo foo | incus file push - c1/mnt/test

  # Snapshot the custom volume.
  incus storage volume set "${custom_vol_pool}" testvol user.foo=test-snap0
  incus storage volume snapshot create "${custom_vol_pool}" testvol test-snap0

  # Change the content (the snapshot will contain the old value).
  echo bar | incus file push - c1/mnt/test

  incus storage volume set "${custom_vol_pool}" testvol user.foo=test-snap1
  incus storage volume snapshot create "${custom_vol_pool}" testvol test-snap1
  incus storage volume set "${custom_vol_pool}" testvol user.foo=post-test-snap1

  if [ "$incus_backend" = "btrfs" ] || [ "$incus_backend" = "zfs" ]; then
    # Create optimized backup without snapshots.
    incus storage volume export "${custom_vol_pool}" testvol "${INCUS_DIR}/testvol-optimized.tar.gz" --volume-only --optimized-storage

    [ -f "${INCUS_DIR}/testvol-optimized.tar.gz" ]

    # Extract backup tarball.
    tar -xzf "${INCUS_DIR}/testvol-optimized.tar.gz" -C "${INCUS_DIR}/optimized"

    [ -f "${INCUS_DIR}/optimized/backup/index.yaml" ]
    [ -f "${INCUS_DIR}/optimized/backup/volume.bin" ]
    [ ! -d "${INCUS_DIR}/optimized/backup/volume-snapshots" ]
  fi

  # Create non-optimized backup without snapshots.
  incus storage volume export "${custom_vol_pool}" testvol "${INCUS_DIR}/testvol.tar.gz" --volume-only

  [ -f "${INCUS_DIR}/testvol.tar.gz" ]

  # Extract non-optimized backup tarball.
  tar -xzf "${INCUS_DIR}/testvol.tar.gz" -C "${INCUS_DIR}/non-optimized"

  # Check tarball content.
  [ -f "${INCUS_DIR}/non-optimized/backup/index.yaml" ]
  [ -d "${INCUS_DIR}/non-optimized/backup/volume" ]
  [ "$(cat "${INCUS_DIR}/non-optimized/backup/volume/test")" = "bar" ]
  [ ! -d "${INCUS_DIR}/non-optimized/backup/volume-snapshots" ]

  ! grep -q -- '- test-snap0' "${INCUS_DIR}/non-optimized/backup/index.yaml" || false

  rm -rf "${INCUS_DIR}/non-optimized/"*
  rm "${INCUS_DIR}/testvol.tar.gz"

  if [ "$incus_backend" = "btrfs" ] || [ "$incus_backend" = "zfs" ]; then
    # Create optimized backup with snapshots.
    incus storage volume export "${custom_vol_pool}" testvol "${INCUS_DIR}/testvol-optimized.tar.gz" --optimized-storage

    [ -f "${INCUS_DIR}/testvol-optimized.tar.gz" ]

    # Extract backup tarball.
    tar -xzf "${INCUS_DIR}/testvol-optimized.tar.gz" -C "${INCUS_DIR}/optimized"

    [ -f "${INCUS_DIR}/optimized/backup/index.yaml" ]
    [ -f "${INCUS_DIR}/optimized/backup/volume.bin" ]
    [ -f "${INCUS_DIR}/optimized/backup/volume-snapshots/test-snap0.bin" ]
  fi

  # Create non-optimized backup with snapshots.
  incus storage volume export "${custom_vol_pool}" testvol "${INCUS_DIR}/testvol.tar.gz"

  [ -f "${INCUS_DIR}/testvol.tar.gz" ]

  # Extract backup tarball.
  tar -xzf "${INCUS_DIR}/testvol.tar.gz" -C "${INCUS_DIR}/non-optimized"

  # Check tarball content.
  [ -f "${INCUS_DIR}/non-optimized/backup/index.yaml" ]
  [ -d "${INCUS_DIR}/non-optimized/backup/volume" ]
  [ "$(cat "${INCUS_DIR}/non-optimized/backup/volume/test")" = "bar" ]
  [ -d "${INCUS_DIR}/non-optimized/backup/volume-snapshots/test-snap0" ]
  [  "$(cat "${INCUS_DIR}/non-optimized/backup/volume-snapshots/test-snap0/test")" = "foo" ]

  grep -q -- '- test-snap0' "${INCUS_DIR}/non-optimized/backup/index.yaml"

  rm -rf "${INCUS_DIR}/non-optimized/"*

  # Test non-optimized import.
  incus stop -f c1
  incus storage volume detach "${custom_vol_pool}" testvol c1
  incus storage volume delete "${custom_vol_pool}" testvol
  incus storage volume import "${custom_vol_pool}" "${INCUS_DIR}/testvol.tar.gz"
  incus storage volume ls "${custom_vol_pool}"
  incus storage volume get "${custom_vol_pool}" testvol user.foo | grep -Fx "post-test-snap1"
  incus storage volume snapshot show "${custom_vol_pool}" testvol/test-snap0
  incus storage volume get "${custom_vol_pool}" testvol/test-snap0 user.foo | grep -Fx "test-snap0"
  incus storage volume get "${custom_vol_pool}" testvol/test-snap1 user.foo | grep -Fx "test-snap1"

  incus storage volume import "${custom_vol_pool}" "${INCUS_DIR}/testvol.tar.gz" testvol2
  incus storage volume attach "${custom_vol_pool}" testvol c1 /mnt
  incus storage volume attach "${custom_vol_pool}" testvol2 c1 /mnt2
  incus start c1
  incus exec c1 --project "$project" -- stat /mnt/test
  incus exec c1 --project "$project" -- stat /mnt2/test
  incus stop -f c1

  if [ "${project}" != "default" ]; then
    # Import into different project (before deleting earlier import).
    incus storage volume import "${custom_vol_pool}" "${INCUS_DIR}/testvol.tar.gz" --project "$project-b"
    incus storage volume import "${custom_vol_pool}" "${INCUS_DIR}/testvol.tar.gz" --project "$project-b" testvol2
    incus storage volume delete "${custom_vol_pool}" testvol --project "$project-b"
    incus storage volume delete "${custom_vol_pool}" testvol2 --project "$project-b"
  fi

  # Test optimized import.
  if [ "$incus_backend" = "btrfs" ] || [ "$incus_backend" = "zfs" ]; then
    incus storage volume detach "${custom_vol_pool}" testvol c1
    incus storage volume detach "${custom_vol_pool}" testvol2 c1
    incus storage volume delete "${custom_vol_pool}" testvol
    incus storage volume delete "${custom_vol_pool}" testvol2
    incus storage volume import "${custom_vol_pool}" "${INCUS_DIR}/testvol-optimized.tar.gz"
    incus storage volume ls "${custom_vol_pool}"
    incus storage volume get "${custom_vol_pool}" testvol user.foo | grep -Fx "post-test-snap1"
    incus storage volume get "${custom_vol_pool}" testvol/test-snap0 user.foo | grep -Fx "test-snap0"
    incus storage volume get "${custom_vol_pool}" testvol/test-snap1 user.foo | grep -Fx "test-snap1"

    incus storage volume import "${custom_vol_pool}" "${INCUS_DIR}/testvol-optimized.tar.gz" testvol2
    incus storage volume attach "${custom_vol_pool}" testvol c1 /mnt
    incus storage volume attach "${custom_vol_pool}" testvol2 c1 /mnt2
    incus start c1
    incus exec c1 --project "$project" -- stat /mnt/test
    incus exec c1 --project "$project" -- stat /mnt2/test
    incus stop -f c1

    if [ "${project}" != "default" ]; then
      # Import into different project (before deleting earlier import).
      incus storage volume import "${custom_vol_pool}" "${INCUS_DIR}/testvol-optimized.tar.gz" --project "$project-b"
      incus storage volume import "${custom_vol_pool}" "${INCUS_DIR}/testvol-optimized.tar.gz" --project "$project-b" testvol2
      incus storage volume delete "${custom_vol_pool}" testvol --project "$project-b"
      incus storage volume delete "${custom_vol_pool}" testvol2 --project "$project-b"
    fi
  fi

  # Clean up.
  rm -rf "${INCUS_DIR}/non-optimized/"* "${INCUS_DIR}/optimized/"*
  incus storage volume detach "${custom_vol_pool}" testvol c1
  incus storage volume detach "${custom_vol_pool}" testvol2 c1
  incus storage volume rm "${custom_vol_pool}" testvol
  incus storage volume rm "${custom_vol_pool}" testvol2
  incus rm -f c1
  rmdir "${INCUS_DIR}/optimized"
  rmdir "${INCUS_DIR}/non-optimized"

  if [ "${project}" != "default" ]; then
    incus project switch default
    incus image rm testimage --project "$project"
    incus image rm testimage --project "$project-b"
    incus project delete "$project"
    incus project delete "$project-b"
  fi
}

test_backup_volume_rename_delete() {
  ensure_has_localhost_remote "${INCUS_ADDR}"

  pool="incustest-$(basename "${INCUS_DIR}")"

  # Create test volume.
  incus storage volume create "${pool}" vol1

  if ! incus query -X POST /1.0/storage-pools/"${pool}"/volumes/custom/vol1/backups/backupmissing -d '{\"name\": \"backupnewname\"}' --wait 2>&1 | grep -q "Error: Storage volume backup not found" ; then
    echo "invalid rename response for missing storage volume"
    false
  fi

  # Create backup.
  incus query -X POST --wait -d '{\"name\":\"foo\"}' /1.0/storage-pools/"${pool}"/volumes/custom/vol1/backups

  # All backups should be listed.
  incus query /1.0/storage-pools/"${pool}"/volumes/custom/vol1/backups
  incus query /1.0/storage-pools/"${pool}"/volumes/custom/vol1/backups | jq .'[0]' | grep storage-pools/"${pool}"/volumes/custom/vol1/backups/foo

  # The specific backup should exist.
  incus query /1.0/storage-pools/"${pool}"/volumes/custom/vol1/backups/foo
  stat "${INCUS_DIR}"/backups/custom/"${pool}"/default_vol1/foo

  # Delete backup and check it is removed from DB and disk.
  incus query -X DELETE --wait /1.0/storage-pools/"${pool}"/volumes/custom/vol1/backups/foo
  ! incus query /1.0/storage-pools/"${pool}"/volumes/custom/vol1/backups/foo || false
  ! stat "${INCUS_DIR}"/backups/custom/"${pool}"/default_vol1/foo || false
  ! stat "${INCUS_DIR}"/backups/custom/"${pool}"/default_vol1 || false

  # Create backup again to test rename.
  incus query -X POST --wait -d '{\"name\":\"foo\"}' /1.0/storage-pools/"${pool}"/volumes/custom/vol1/backups

  # Rename the container which should rename the backup(s) as well.
  incus storage volume rename "${pool}" vol1 vol2

  # All backups should be listed.
  incus query /1.0/storage-pools/"${pool}"/volumes/custom/vol2/backups | jq .'[0]' | grep storage-pools/"${pool}"/volumes/custom/vol2/backups/foo

  # The specific backup should exist.
  incus query /1.0/storage-pools/"${pool}"/volumes/custom/vol2/backups/foo
  stat "${INCUS_DIR}"/backups/custom/"${pool}"/default_vol2/foo

  # The old backup should not exist.
  ! incus query /1.0/storage-pools/"${pool}"/volumes/custom/vol1/backups/foo || false
  ! stat "${INCUS_DIR}"/backups/custom/"${pool}"/default_vol1/foo || false
  ! stat "${INCUS_DIR}"/backups/custom/"${pool}"/default_vol1 || false

  # Rename backup itself and check its renamed in DB and on disk.
  incus query -X POST --wait -d '{\"name\":\"foo2\"}' /1.0/storage-pools/"${pool}"/volumes/custom/vol2/backups/foo
  incus query /1.0/storage-pools/"${pool}"/volumes/custom/vol2/backups | jq .'[0]' | grep storage-pools/"${pool}"/volumes/custom/vol2/backups/foo2
  stat "${INCUS_DIR}"/backups/custom/"${pool}"/default_vol2/foo2
  ! stat "${INCUS_DIR}"/backups/custom/"${pool}"/default_vol2/foo || false

  # Remove volume and check the backups are removed too.
  incus storage volume rm "${pool}" vol2
  ! stat "${INCUS_DIR}"/backups/custom/"${pool}"/default_vol2 || false
}

test_backup_different_instance_uuid() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  echo "==> Checking instances UUID during backup operation"
  incus launch testimage c1
  initialUUID=$(incus config get c1 volatile.uuid)
  initialGenerationID=$(incus config get c1 volatile.uuid.generation)

  # export and import to trigger new UUID generation
  incus export c1 "${INCUS_DIR}/c1.tar.gz"
  incus delete -f c1
  incus import "${INCUS_DIR}/c1.tar.gz"

  newUUID=$(incus config get c1 volatile.uuid)
  newGenerationID=$(incus config get c1 volatile.uuid.generation)

  if [ "${initialGenerationID}" != "${newGenerationID}" ] || [ "${initialUUID}" != "${newUUID}" ]; then
    echo "==> UUID of the instance should remain the same after importing the backup file"
    false
  fi

  incus delete -f c1
}

test_backup_volume_expiry() {
  poolName=$(incus profile device get default root pool)

  # Create custom volume.
  incus storage volume create "${poolName}" vol1

  # Create storage volume backups using the API directly.
  # The first one is created with an expiry date, the second one never expires.
  incus query -X POST -d '{\"expires_at\":\"2023-07-17T00:00:00Z\"}' /1.0/storage-pools/"${poolName}"/volumes/custom/vol1/backups
  incus query -X POST -d '{}' /1.0/storage-pools/"${poolName}"/volumes/custom/vol1/backups

  # Check that both backups are listed.
  [ "$(incus query /1.0/storage-pools/"${poolName}"/volumes/custom/vol1/backups | jq '.[]' | wc -l)" -eq 2 ]

  # Restart Incus which will trigger the task which removes expired volume backups.
  shutdown_incus "${INCUS_DIR}"
  respawn_incus "${INCUS_DIR}" true

  # Check that there's only one backup remaining.
  [ "$(incus query /1.0/storage-pools/"${poolName}"/volumes/custom/vol1/backups | jq '.[]' | wc -l)" -eq 1 ]

  # Cleanup.
  incus storage volume delete "${poolName}" vol1
}

test_backup_export_import_recover() {
  (
    set -e

    poolName=$(incus profile device get default root pool)

    ensure_import_testimage
    ensure_has_localhost_remote "${INCUS_ADDR}"

    # Create and export an instance.
    incus launch testimage c1
    incus export c1 "${INCUS_DIR}/c1.tar.gz"
    incus delete -f c1

    # Import instance and remove no longer required tarball.
    incus import "${INCUS_DIR}/c1.tar.gz" c2
    rm "${INCUS_DIR}/c1.tar.gz"

    # Remove imported instance enteries from database.
    incus admin sql global "delete from instances where name = 'c2'"
    incus admin sql global "delete from storage_volumes where name = 'c2'"

    # Recover removed instance.
    cat <<EOF | incus admin recover
no
yes
yes
EOF

    # Remove recovered instance.
    incus rm -f c2
  )
}

test_backup_export_import_instance_only() {
  poolName=$(incus profile device get default root pool)

  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  # Create an instance with snapshot.
  incus init testimage c1
  incus snapshot create c1

  # Export the instance and remove it.
  incus export c1 "${INCUS_DIR}/c1.tar.gz" --instance-only
  incus delete -f c1

  # Import the instance from tarball.
  incus import "${INCUS_DIR}/c1.tar.gz"

  # Verify imported instance has no snapshots.
  [ "$(incus query "/1.0/storage-pools/${poolName}/volumes/container/c1/snapshots" | jq "length == 0")" = "true" ]

  rm "${INCUS_DIR}/c1.tar.gz"
  incus delete -f c1
}
