test_storage_driver_cephfs() {
  # shellcheck disable=2039,3043
  local incus_backend

  incus_backend=$(storage_backend "$INCUS_DIR")
  if [ "$incus_backend" != "ceph" ] || [ -z "${INCUS_CEPH_CEPHFS:-}" ]; then
    return
  fi

  # Simple create/delete attempt
  incus storage create cephfs cephfs source="${INCUS_CEPH_CEPHFS}/$(basename "${INCUS_DIR}")"
  incus storage delete cephfs

  # Test invalid key combinations for auto-creation of cephfs entities.
  ! incus storage create cephfs cephfs source="${INCUS_CEPH_CEPHFS}/$(basename "${INCUS_DIR}")" cephfs.osd_pg_num=32 || true
  ! incus storage create cephfs cephfs source="${INCUS_CEPH_CEPHFS}/$(basename "${INCUS_DIR}")" cephfs.meta_pool=xyz || true
  ! incus storage create cephfs cephfs source="${INCUS_CEPH_CEPHFS}/$(basename "${INCUS_DIR}")" cephfs.data_pool=xyz || true
  ! incus storage create cephfs cephfs source="${INCUS_CEPH_CEPHFS}/$(basename "${INCUS_DIR}")" cephfs.create_missing=true cephfs.data_pool=xyz_data cephfs.meta_pool=xyz_meta || true


  # Test cephfs storage volumes.
  for fs in "cephfs" "cephfs2" ; do
    if [ "${fs}" = "cephfs" ]; then
      # Create one cephfs with pre-existing OSDs.
      incus storage create "${fs}" cephfs source="${INCUS_CEPH_CEPHFS}/$(basename "${INCUS_DIR}")"
    else
      # Create one cephfs by creating the OSDs and the cephfs itself.
      incus storage create "${fs}" cephfs source=cephfs2 cephfs.create_missing=true cephfs.data_pool=xyz_data cephfs.meta_pool=xyz_meta
    fi

    # Confirm got cleaned up properly
    incus storage info "${fs}"

    # Creation, rename and deletion
    incus storage volume create "${fs}" vol1
    incus storage volume set "${fs}" vol1 size 100MiB
    incus storage volume rename "${fs}" vol1 vol2
    incus storage volume copy "${fs}"/vol2 "${fs}"/vol1
    incus storage volume delete "${fs}" vol1
    incus storage volume delete "${fs}" vol2

    # Snapshots
    incus storage volume create "${fs}" vol1
    incus storage volume snapshot create "${fs}" vol1
    incus storage volume snapshot create "${fs}" vol1
    incus storage volume snapshot create "${fs}" vol1 blah1
    incus storage volume snapshot rename "${fs}" vol1 blah1 blah2
    incus storage volume snapshot create "${fs}" vol1 blah1
    incus storage volume snapshot delete "${fs}" vol1 snap0
    incus storage volume snapshot delete "${fs}" vol1 snap1
    incus storage volume snapshot restore "${fs}" vol1 blah1
    incus storage volume copy "${fs}"/vol1 "${fs}"/vol2 --volume-only
    incus storage volume copy "${fs}"/vol1 "${fs}"/vol3 --volume-only
    incus storage volume delete "${fs}" vol1
    incus storage volume delete "${fs}" vol2
    incus storage volume delete "${fs}" vol3

    # Cleanup
    incus storage delete "${fs}"

    # Remove the filesystem so we can create a new one.
    ceph fs fail "${fs}"
    ceph fs rm "${fs}" --yes-i-really-mean-it
  done

  # Recreate the fs for other tests.
  ceph fs new cephfs cephfs_meta cephfs_data --force
}
