test_container_devices_disk() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  inc init testimage foo

  test_container_devices_disk_shift
  test_container_devices_raw_mount_options
  test_container_devices_disk_ceph
  test_container_devices_disk_cephfs
  test_container_devices_disk_socket
  test_container_devices_disk_char

  inc delete -f foo
}

test_container_devices_disk_shift() {
  if ! grep -q shiftfs /proc/filesystems || [ -n "${INCUS_SHIFTFS_DISABLE:-}" ]; then
    return
  fi

  # Test basic shiftfs
  mkdir -p "${TEST_DIR}/shift-source"
  touch "${TEST_DIR}/shift-source/a"
  chown 123:456 "${TEST_DIR}/shift-source/a"

  inc start foo
  inc config device add foo shiftfs disk source="${TEST_DIR}/shift-source" path=/mnt
  [ "$(inc exec foo -- stat /mnt/a -c '%u:%g')" = "65534:65534" ] || false
  inc config device remove foo shiftfs

  inc config device add foo shiftfs disk source="${TEST_DIR}/shift-source" path=/mnt shift=true
  [ "$(inc exec foo -- stat /mnt/a -c '%u:%g')" = "123:456" ] || false

  inc stop foo -f
  inc start foo
  [ "$(inc exec foo -- stat /mnt/a -c '%u:%g')" = "123:456" ] || false
  inc config device remove foo shiftfs
  inc stop foo -f

  # Test shifted custom volumes
  POOL=$(inc profile device get default root pool)
  inc storage volume create "${POOL}" foo-shift security.shifted=true

  inc start foo
  inc launch testimage foo-priv -c security.privileged=true
  inc launch testimage foo-isol1 -c security.idmap.isolated=true
  inc launch testimage foo-isol2 -c security.idmap.isolated=true

  inc config device add foo shifted disk pool="${POOL}" source=foo-shift path=/mnt
  inc config device add foo-priv shifted disk pool="${POOL}" source=foo-shift path=/mnt
  inc config device add foo-isol1 shifted disk pool="${POOL}" source=foo-shift path=/mnt
  inc config device add foo-isol2 shifted disk pool="${POOL}" source=foo-shift path=/mnt

  inc exec foo -- touch /mnt/a
  inc exec foo -- chown 123:456 /mnt/a

  [ "$(inc exec foo -- stat /mnt/a -c '%u:%g')" = "123:456" ] || false
  [ "$(inc exec foo-priv -- stat /mnt/a -c '%u:%g')" = "123:456" ] || false
  [ "$(inc exec foo-isol1 -- stat /mnt/a -c '%u:%g')" = "123:456" ] || false
  [ "$(inc exec foo-isol2 -- stat /mnt/a -c '%u:%g')" = "123:456" ] || false

  inc delete -f foo-priv foo-isol1 foo-isol2
  inc config device remove foo shifted
  inc storage volume delete "${POOL}" foo-shift
  inc stop foo -f
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
