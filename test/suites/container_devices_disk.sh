test_container_devices_disk() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  inc init testimage foo

  test_container_devices_raw_mount_options
  test_container_devices_disk_ceph
  test_container_devices_disk_cephfs
  test_container_devices_disk_socket
  test_container_devices_disk_char

  inc delete -f foo
}

test_container_devices_raw_mount_options() {
  configure_loop_device loop_file_1 loop_device_1
  # shellcheck disable=SC2154
  mkfs.vfat "${loop_device_1}"

  inc launch testimage foo-priv -c security.privileged=true

  inc config device add foo-priv loop_raw_mount_options disk source="${loop_device_1}" path=/mnt
  [ "$(inc exec foo-priv -- stat /mnt -c '%u:%g')" = "0:0" ] || false
  inc exec foo-priv -- touch /mnt/foo
  inc config device remove foo-priv loop_raw_mount_options

  inc config device add foo-priv loop_raw_mount_options disk source="${loop_device_1}" path=/mnt raw.mount.options=uid=123,gid=456,ro
  [ "$(inc exec foo-priv -- stat /mnt -c '%u:%g')" = "123:456" ] || false
  ! inc exec foo-priv -- touch /mnt/foo || false
  inc config device remove foo-priv loop_raw_mount_options

  inc stop foo-priv -f
  inc config device add foo-priv loop_raw_mount_options disk source="${loop_device_1}" path=/mnt raw.mount.options=uid=123,gid=456,ro
  inc start foo-priv
  [ "$(inc exec foo-priv -- stat /mnt -c '%u:%g')" = "123:456" ] || false
  ! inc exec foo-priv -- touch /mnt/foo || false
  inc config device remove foo-priv loop_raw_mount_options

  inc delete -f foo-priv
  # shellcheck disable=SC2154
  deconfigure_loop_device "${loop_file_1}" "${loop_device_1}"
}

test_container_devices_disk_ceph() {
  # shellcheck disable=SC2039,3043
  local INCUS_BACKEND

  INCUS_BACKEND=$(storage_backend "$INCUS_DIR")
  if ! [ "${INCUS_BACKEND}" = "ceph" ]; then
    return
  fi

  RBD_POOL_NAME=incustest-$(basename "${INCUS_DIR}")-disk
  ceph osd pool create "${RBD_POOL_NAME}" 1
  rbd create --pool "${RBD_POOL_NAME}" --size 50M my-volume
  RBD_DEVICE=$(rbd map --pool "${RBD_POOL_NAME}" my-volume)
  mkfs.ext4 -m0 "${RBD_DEVICE}"
  rbd unmap "${RBD_DEVICE}"

  inc launch testimage ceph-disk -c security.privileged=true
  inc config device add ceph-disk rbd disk source=ceph:"${RBD_POOL_NAME}"/my-volume ceph.user_name=admin ceph.cluster_name=ceph path=/ceph
  inc exec ceph-disk -- stat /ceph/lost+found
  inc restart ceph-disk --force
  inc exec ceph-disk -- stat /ceph/lost+found
  inc delete -f ceph-disk
  ceph osd pool rm "${RBD_POOL_NAME}" "${RBD_POOL_NAME}" --yes-i-really-really-mean-it
}

test_container_devices_disk_cephfs() {
  # shellcheck disable=SC2039,3043
  local INCUS_BACKEND

  INCUS_BACKEND=$(storage_backend "$INCUS_DIR")
  if [ "${INCUS_BACKEND}" != "ceph" ] || [ -z "${INCUS_CEPH_CEPHFS:-}" ]; then
    return
  fi

  inc launch testimage ceph-fs -c security.privileged=true
  inc config device add ceph-fs fs disk source=cephfs:"${INCUS_CEPH_CEPHFS}"/ ceph.user_name=admin ceph.cluster_name=ceph path=/cephfs
  inc exec ceph-fs -- stat /cephfs
  inc restart ceph-fs --force
  inc exec ceph-fs -- stat /cephfs
  inc delete -f ceph-fs
}

test_container_devices_disk_socket() {
  inc start foo
  inc config device add foo unix-socket disk source="${INCUS_DIR}/unix.socket" path=/root/incus.sock
  [ "$(inc exec foo -- stat /root/incus.sock -c '%F')" = "socket" ] || false
  inc restart -f foo
  [ "$(inc exec foo -- stat /root/incus.sock -c '%F')" = "socket" ] || false
  inc config device remove foo unix-socket
  inc stop foo -f
}

test_container_devices_disk_char() {
  inc start foo
  inc config device add foo char disk source=/dev/zero path=/root/zero
  [ "$(inc exec foo -- stat /root/zero -c '%F')" = "character special file" ] || false
  inc restart -f foo
  [ "$(inc exec foo -- stat /root/zero -c '%F')" = "character special file" ] || false
  inc config device remove foo char
  inc stop foo -f
}
