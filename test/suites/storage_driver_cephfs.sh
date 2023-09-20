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

  # Second create (confirm got cleaned up properly)
  incus storage create cephfs cephfs source="${INCUS_CEPH_CEPHFS}/$(basename "${INCUS_DIR}")"
  incus storage info cephfs

  # Creation, rename and deletion
  incus storage volume create cephfs vol1
  incus storage volume set cephfs vol1 size 100MiB
  incus storage volume rename cephfs vol1 vol2
  incus storage volume copy cephfs/vol2 cephfs/vol1
  incus storage volume delete cephfs vol1
  incus storage volume delete cephfs vol2

  # Snapshots
  incus storage volume create cephfs vol1
  incus storage volume snapshot create cephfs vol1
  incus storage volume snapshot create cephfs vol1
  incus storage volume snapshot create cephfs vol1 blah1
  incus storage volume snapshot rename cephfs vol1 blah1 blah2
  incus storage volume snapshot create cephfs vol1 blah1
  incus storage volume snapshot delete cephfs vol1 snap0
  incus storage volume snapshot delete cephfs vol1 snap1
  incus storage volume snapshot restore cephfs vol1 blah1
  incus storage volume copy cephfs/vol1 cephfs/vol2 --volume-only
  incus storage volume copy cephfs/vol1 cephfs/vol3 --volume-only
  incus storage volume delete cephfs vol1
  incus storage volume delete cephfs vol2
  incus storage volume delete cephfs vol3

  # Cleanup
  incus storage delete cephfs
}
