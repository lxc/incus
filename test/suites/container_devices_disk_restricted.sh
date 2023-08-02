test_container_devices_disk_restricted() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  # Create directory for use as basis for restricted disk source tests.
  testRoot="${TEST_DIR}/restricted"
  mkdir "${testRoot}"

  # Create directory for use as allowed disk source path prefix in project.
  mkdir "${testRoot}/allowed1"
  mkdir "${testRoot}/allowed2"
  touch "${testRoot}/allowed1/foo1"
  touch "${testRoot}/allowed1/foo2"
  ln "${INCUS_DIR}/unix.socket" "${testRoot}/allowed1/lxd.sock"
  chown 1000:1000 "${testRoot}/allowed1/foo1"
  chown 1001:1001 "${testRoot}/allowed1/foo2"
  mkdir "${testRoot}/not-allowed1"
  ln -s "${testRoot}/not-allowed1" "${testRoot}/allowed1/not-allowed1"
  ln -s "${testRoot}/allowed2" "${testRoot}/allowed1/not-allowed2"
  (cd "${testRoot}/allowed1" || false; ln -s foo1 foolink)

  # Create project with restricted disk source path.
  lxc project create restricted \
    -c features.images=false \
    -c restricted=true \
    -c restricted.devices.disk=allow \
    -c restricted.devices.disk.paths="${testRoot}/allowed1,${testRoot}/allowed2"
  lxc project switch restricted
  pool="incustest-$(basename "${INCUS_DIR}")"
  lxc profile device add default root disk path="/" pool="${pool}"
  lxc profile show default

  # Create instance and add check relative source paths are not allowed.
  lxc init testimage c1
  ! lxc config device add c1 d1 disk source=foo path=/mnt || false

  # Check adding a disk with a source path above the restricted parent source path isn't allowed.
  ! lxc config device add c1 d1 disk source="${testRoot}/not-allowed1" path=/mnt || false

  # Check adding a disk with a source path that is a symlink above the restricted parent source path isn't allowed
  # at start time (check that openat2 restrictions are doing their job).
  lxc config device add c1 d1 disk source="${testRoot}/allowed1/not-allowed1" path=/mnt
  ! lxc start c1 || false

  # Check some rudimentary work arounds to allowed path checks don't work.
  ! lxc config device set c1 d1 source="${testRoot}/../not-allowed1" || false

  # Check adding a disk from a restricted source path cannot use shifting at start time. This is not safe as we
  # cannot prevent creation of files with setuid, which would allow a root executable to be created.
  lxc config device set c1 d1 source="${testRoot}/allowed1" shift=true
  ! lxc start c1 || false

  # Check adding a disk with a source path that is allowed is allowed.
  lxc config device set c1 d1 source="${testRoot}/allowed1" shift=false
  lxc start c1
  lxc exec c1 --project restricted -- ls /mnt/foo1

  # Check adding a disk with a source path that is allowed that symlinks to another allowed source path isn't
  # allowed at start time.
  ! lxc config device set c1 d1 source="${testRoot}/allowed1/not-allowed2" || false

  # Check relative symlink inside allowed parent path is allowed.
  lxc stop -f c1
  lxc config device set c1 d1 source="${testRoot}/allowed1/foolink" path=/mnt/foolink
  lxc start c1
  [ "$(lxc exec c1 --project restricted  -- stat /mnt/foolink -c '%u:%g')" = "65534:65534" ] || false
  lxc stop -f c1

  # Check usage of raw.idmap is restricted.
  ! lxc config set c1 raw.idmap="both 1000 1000" || false

  # Allow specific raw.idmap host UID/GID.
  lxc project set restricted restricted.idmap.uid=1000
  ! lxc config set c1 raw.idmap="both 1000 1000" || false
  ! lxc config set c1 raw.idmap="gid 1000 1000" || false
  lxc config set c1 raw.idmap="uid 1000 1000"

  lxc project set restricted restricted.idmap.gid=1000
  lxc config set c1 raw.idmap="gid 1000 1000"
  lxc config set c1 raw.idmap="both 1000 1000"

  # Check conflict detection works.
  ! lxc project unset restricted restricted.idmap.uid || false
  ! lxc project unset restricted restricted.idmap.gid || false

  # Check single entry raw.idmap has taken effect on disk share.
  lxc config device set c1 d1 source="${testRoot}/allowed1" path=/mnt
  lxc start c1 || (lxc info --show-log c1 ; false)
  [ "$(lxc exec c1 --project restricted -- stat /mnt/foo1 -c '%u:%g')" = "1000:1000" ] || false
  [ "$(lxc exec c1 --project restricted -- stat /mnt/foo2 -c '%u:%g')" = "65534:65534" ] || false

  # Check adding unix socket is allowed.
  lxc config device add c1 unix-socket disk source="${testRoot}/allowed1/lxd.sock" path=/root/lxd.sock
  [ "$(lxc exec c1 --project restricted -- stat /root/lxd.sock -c '%F')" = "socket" ] || false

  lxc delete -f c1
  lxc project switch default
  lxc project delete restricted
  rm "${testRoot}/allowed1/not-allowed1"
  rm "${testRoot}/allowed1/not-allowed2"
  rm "${testRoot}/allowed1/foo1"
  rm "${testRoot}/allowed1/foo2"
  rm "${testRoot}/allowed1/foolink"
  rm "${testRoot}/allowed1/lxd.sock"
  rmdir "${testRoot}/allowed1"
  rmdir "${testRoot}/allowed2"
  rmdir "${testRoot}/not-allowed1"
  rmdir "${testRoot}"
}
