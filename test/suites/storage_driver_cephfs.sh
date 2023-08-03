test_storage_driver_cephfs() {
  # shellcheck disable=2039,3043
  local incus_backend

  incus_backend=$(storage_backend "$INCUS_DIR")
  if [ "$incus_backend" != "ceph" ] || [ -z "${INCUS_CEPH_CEPHFS:-}" ]; then
    return
  fi

  # Simple create/delete attempt
  inc storage create cephfs cephfs source="${INCUS_CEPH_CEPHFS}/$(basename "${INCUS_DIR}")"
  inc storage delete cephfs

  # Second create (confirm got cleaned up properly)
  inc storage create cephfs cephfs source="${INCUS_CEPH_CEPHFS}/$(basename "${INCUS_DIR}")"
  inc storage info cephfs

  # Creation, rename and deletion
  inc storage volume create cephfs vol1
  inc storage volume set cephfs vol1 size 100MiB
  inc storage volume rename cephfs vol1 vol2
  inc storage volume copy cephfs/vol2 cephfs/vol1
  inc storage volume delete cephfs vol1
  inc storage volume delete cephfs vol2

  # Snapshots
  inc storage volume create cephfs vol1
  inc storage volume snapshot cephfs vol1
  inc storage volume snapshot cephfs vol1
  inc storage volume snapshot cephfs vol1 blah1
  inc storage volume rename cephfs vol1/blah1 vol1/blah2
  inc storage volume snapshot cephfs vol1 blah1
  inc storage volume delete cephfs vol1/snap0
  inc storage volume delete cephfs vol1/snap1
  inc storage volume restore cephfs vol1 blah1
  inc storage volume copy cephfs/vol1 cephfs/vol2 --volume-only
  inc storage volume copy cephfs/vol1 cephfs/vol3 --volume-only
  inc storage volume delete cephfs vol1
  inc storage volume delete cephfs vol2
  inc storage volume delete cephfs vol3

  # Cleanup
  inc storage delete cephfs
}
