test_container_devices_disk() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  incus init testimage foo

  test_container_devices_disk_shift
  test_container_devices_raw_mount_options
  test_container_devices_disk_ceph
  test_container_devices_disk_cephfs
  test_container_devices_disk_socket
  test_container_devices_disk_char

  incus delete -f foo
}

test_container_devices_disk_shift() {
  # shellcheck disable=2039,3043
  local incus_backend
  incus_backend=$(storage_backend "$INCUS_DIR")

  if [ -n "${INCUS_IDMAPPED_MOUNTS_DISABLE:-}" ]; then
    return
  fi

  if [ "${incus_backend}" = "zfs" ]; then
    # ZFS 2.2 is required for idmapped mounts support.
    zfs_version=$(zfs --version | grep -m 1 '^zfs-' | cut -d '-' -f 2)
    if [ "$(printf '%s\n' "$zfs_version" "2.2" | sort -V | head -n1)" = "$zfs_version" ]; then
      if [ "$zfs_version" != "2.2" ]; then
        echo "ZFS version is less than 2.2. Skipping idmapped mounts tests."
        return
      else
        echo "ZFS version is 2.2. Idmapped mounts are supported with ZFS."
      fi
    else
      echo "ZFS version is greater than 2.2. Idmapped mounts are supported with ZFS."
    fi
  fi

  # Test basic shifting
  mkdir -p "${TEST_DIR}/shift-source"
  touch "${TEST_DIR}/shift-source/a"
  chown 123:456 "${TEST_DIR}/shift-source/a"

  incus start foo
  incus config device add foo idmapped_mount disk source="${TEST_DIR}/shift-source" path=/mnt
  [ "$(incus exec foo -- stat /mnt/a -c '%u:%g')" = "65534:65534" ] || false
  incus config device remove foo idmapped_mount

  incus config device add foo idmapped_mount disk source="${TEST_DIR}/shift-source" path=/mnt shift=true
  [ "$(incus exec foo -- stat /mnt/a -c '%u:%g')" = "123:456" ] || false

  incus stop foo -f
  incus start foo
  [ "$(incus exec foo -- stat /mnt/a -c '%u:%g')" = "123:456" ] || false
  incus config device remove foo idmapped_mount
  incus stop foo -f

  # Test shifted custom volumes
  POOL=$(incus profile device get default root pool)

  # Cannot set both security.shifted and security.unmapped.
  ! incus storage volume create "${POOL}" foo-shift security.shifted=true security.unmapped=true || false

  incus storage volume create "${POOL}" foo-shift security.shifted=true

  # Cannot set both security.shifted and security.unmapped.
  ! incus storage volume set "${POOL}" foo-shift security.unmapped=true || false

  incus start foo
  incus launch testimage foo-priv -c security.privileged=true
  incus launch testimage foo-isol1 -c security.idmap.isolated=true
  incus launch testimage foo-isol2 -c security.idmap.isolated=true

  incus config device add foo shifted disk pool="${POOL}" source=foo-shift path=/mnt
  incus config device add foo-priv shifted disk pool="${POOL}" source=foo-shift path=/mnt
  incus config device add foo-isol1 shifted disk pool="${POOL}" source=foo-shift path=/mnt
  incus config device add foo-isol2 shifted disk pool="${POOL}" source=foo-shift path=/mnt

  incus exec foo -- touch /mnt/a
  incus exec foo -- chown 123:456 /mnt/a

  [ "$(incus exec foo -- stat /mnt/a -c '%u:%g')" = "123:456" ] || false
  [ "$(incus exec foo-priv -- stat /mnt/a -c '%u:%g')" = "123:456" ] || false
  [ "$(incus exec foo-isol1 -- stat /mnt/a -c '%u:%g')" = "123:456" ] || false
  [ "$(incus exec foo-isol2 -- stat /mnt/a -c '%u:%g')" = "123:456" ] || false

  incus delete -f foo-priv foo-isol1 foo-isol2
  incus config device remove foo shifted
  incus storage volume delete "${POOL}" foo-shift
  incus stop foo -f
}

test_container_devices_raw_mount_options() {
  configure_loop_device loop_file_1 loop_device_1
  # shellcheck disable=SC2154
  mkfs.vfat "${loop_device_1}"

  incus launch testimage foo-priv -c security.privileged=true

  incus config device add foo-priv loop_raw_mount_options disk source="${loop_device_1}" path=/mnt
  [ "$(incus exec foo-priv -- stat /mnt -c '%u:%g')" = "0:0" ] || false
  incus exec foo-priv -- touch /mnt/foo
  incus config device remove foo-priv loop_raw_mount_options

  incus config device add foo-priv loop_raw_mount_options disk source="${loop_device_1}" path=/mnt raw.mount.options=uid=123,gid=456,ro
  [ "$(incus exec foo-priv -- stat /mnt -c '%u:%g')" = "123:456" ] || false
  ! incus exec foo-priv -- touch /mnt/foo || false
  incus config device remove foo-priv loop_raw_mount_options

  incus stop foo-priv -f
  incus config device add foo-priv loop_raw_mount_options disk source="${loop_device_1}" path=/mnt raw.mount.options=uid=123,gid=456,ro
  incus start foo-priv
  [ "$(incus exec foo-priv -- stat /mnt -c '%u:%g')" = "123:456" ] || false
  ! incus exec foo-priv -- touch /mnt/foo || false
  incus config device remove foo-priv loop_raw_mount_options

  incus delete -f foo-priv
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

  incus launch testimage ceph-disk -c security.privileged=true
  incus config device add ceph-disk rbd disk source=ceph:"${RBD_POOL_NAME}"/my-volume ceph.user_name=admin ceph.cluster_name=ceph path=/ceph
  incus exec ceph-disk -- stat /ceph/lost+found
  incus restart ceph-disk --force
  incus exec ceph-disk -- stat /ceph/lost+found
  incus delete -f ceph-disk
  ceph osd pool rm "${RBD_POOL_NAME}" "${RBD_POOL_NAME}" --yes-i-really-really-mean-it
}

test_container_devices_disk_cephfs() {
  # shellcheck disable=SC2039,3043
  local INCUS_BACKEND

  INCUS_BACKEND=$(storage_backend "$INCUS_DIR")
  if [ "${INCUS_BACKEND}" != "ceph" ] || [ -z "${INCUS_CEPH_CEPHFS:-}" ]; then
    return
  fi

  incus launch testimage ceph-fs -c security.privileged=true
  incus config device add ceph-fs fs disk source=cephfs:"${INCUS_CEPH_CEPHFS}"/ ceph.user_name=admin ceph.cluster_name=ceph path=/cephfs
  incus exec ceph-fs -- stat /cephfs
  incus restart ceph-fs --force
  incus exec ceph-fs -- stat /cephfs
  incus delete -f ceph-fs
}

test_container_devices_disk_socket() {
  incus start foo
  incus config device add foo unix-socket disk source="${INCUS_DIR}/unix.socket" path=/root/incus.sock
  [ "$(incus exec foo -- stat /root/incus.sock -c '%F')" = "socket" ] || false
  incus restart -f foo
  [ "$(incus exec foo -- stat /root/incus.sock -c '%F')" = "socket" ] || false
  incus config device remove foo unix-socket
  incus stop foo -f
}

test_container_devices_disk_char() {
  incus start foo
  incus config device add foo char disk source=/dev/zero path=/root/zero
  [ "$(incus exec foo -- stat /root/zero -c '%F')" = "character special file" ] || false
  incus restart -f foo
  [ "$(incus exec foo -- stat /root/zero -c '%F')" = "character special file" ] || false
  incus config device remove foo char
  incus stop foo -f
}
