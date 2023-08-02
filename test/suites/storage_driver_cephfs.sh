test_storage_driver_cephfs() {
  # shellcheck disable=2039,3043
  local incus_backend

  incus_backend=$(storage_backend "$INCUS_DIR")
  if [ "$incus_backend" != "ceph" ] || [ -z "${INCUS_CEPH_CEPHFS:-}" ]; then
    return
  fi

  # Simple create/delete attempt
  lxc storage create cephfs cephfs source="${INCUS_CEPH_CEPHFS}/$(basename "${INCUS_DIR}")"
  lxc storage delete cephfs

  # Second create (confirm got cleaned up properly)
  lxc storage create cephfs cephfs source="${INCUS_CEPH_CEPHFS}/$(basename "${INCUS_DIR}")"
  lxc storage info cephfs

  # Creation, rename and deletion
  lxc storage volume create cephfs vol1
  lxc storage volume set cephfs vol1 size 100MiB
  lxc storage volume rename cephfs vol1 vol2
  lxc storage volume copy cephfs/vol2 cephfs/vol1
  lxc storage volume delete cephfs vol1
  lxc storage volume delete cephfs vol2

  # Snapshots
  lxc storage volume create cephfs vol1
  lxc storage volume snapshot cephfs vol1
  lxc storage volume snapshot cephfs vol1
  lxc storage volume snapshot cephfs vol1 blah1
  lxc storage volume rename cephfs vol1/blah1 vol1/blah2
  lxc storage volume snapshot cephfs vol1 blah1
  lxc storage volume delete cephfs vol1/snap0
  lxc storage volume delete cephfs vol1/snap1
  lxc storage volume restore cephfs vol1 blah1
  lxc storage volume copy cephfs/vol1 cephfs/vol2 --volume-only
  lxc storage volume copy cephfs/vol1 cephfs/vol3 --volume-only
  lxc storage volume delete cephfs vol1
  lxc storage volume delete cephfs vol2
  lxc storage volume delete cephfs vol3

  # Cleanup
  lxc storage delete cephfs
}
