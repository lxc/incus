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
  ln "${INCUS_DIR}/unix.socket" "${testRoot}/allowed1/incus.sock"
  chown 1000:1000 "${testRoot}/allowed1/foo1"
  chown 1001:1001 "${testRoot}/allowed1/foo2"
  mkdir "${testRoot}/not-allowed1"
  ln -s "${testRoot}/not-allowed1" "${testRoot}/allowed1/not-allowed1"
  ln -s "${testRoot}/allowed2" "${testRoot}/allowed1/not-allowed2"
  (cd "${testRoot}/allowed1" || false; ln -s foo1 foolink)

  # Create project with restricted disk source path.
  incus project create restricted \
    -c features.images=false \
    -c restricted=true \
    -c restricted.devices.disk=allow \
    -c restricted.devices.disk.paths="${testRoot}/allowed1,${testRoot}/allowed2"
  incus project switch restricted
  pool="incustest-$(basename "${INCUS_DIR}")"
  incus profile device add default root disk path="/" pool="${pool}"
  incus profile show default

  # Create instance and add check relative source paths are not allowed.
  incus init testimage c1
  ! incus config device add c1 d1 disk source=foo path=/mnt || false

  # Check adding a disk with a source path above the restricted parent source path isn't allowed.
  ! incus config device add c1 d1 disk source="${testRoot}/not-allowed1" path=/mnt || false

  # Check adding a disk with a source path that is a symlink above the restricted parent source path isn't allowed
  # at start time (check that openat2 restrictions are doing their job).
  incus config device add c1 d1 disk source="${testRoot}/allowed1/not-allowed1" path=/mnt
  ! incus start c1 || false

  # Check some rudimentary work arounds to allowed path checks don't work.
  ! incus config device set c1 d1 source="${testRoot}/../not-allowed1" || false

  # Check adding a disk from a restricted source path cannot use shifting at start time. This is not safe as we
  # cannot prevent creation of files with setuid, which would allow a root executable to be created.
  incus config device set c1 d1 source="${testRoot}/allowed1" shift=true
  ! incus start c1 || false

  # Check adding a disk with a source path that is allowed is allowed.
  incus config device set c1 d1 source="${testRoot}/allowed1" shift=false
  incus start c1
  incus exec c1 --project restricted -- ls /mnt/foo1

  # Check adding a disk with a source path that is allowed that symlinks to another allowed source path isn't
  # allowed at start time.
  ! incus config device set c1 d1 source="${testRoot}/allowed1/not-allowed2" || false

  # Check relative symlink inside allowed parent path is allowed.
  incus stop -f c1
  incus config device set c1 d1 source="${testRoot}/allowed1/foolink" path=/mnt/foolink
  incus start c1
  [ "$(incus exec c1 --project restricted  -- stat /mnt/foolink -c '%u:%g')" = "65534:65534" ] || false
  incus stop -f c1

  # Check usage of raw.idmap is restricted.
  ! incus config set c1 raw.idmap="both 1000 1000" || false

  # Allow specific raw.idmap host UID/GID.
  incus project set restricted restricted.idmap.uid=1000
  ! incus config set c1 raw.idmap="both 1000 1000" || false
  ! incus config set c1 raw.idmap="gid 1000 1000" || false
  incus config set c1 raw.idmap="uid 1000 1000"

  incus project set restricted restricted.idmap.gid=1000
  incus config set c1 raw.idmap="gid 1000 1000"
  incus config set c1 raw.idmap="both 1000 1000"

  # Check conflict detection works.
  ! incus project unset restricted restricted.idmap.uid || false
  ! incus project unset restricted restricted.idmap.gid || false

  # Check single entry raw.idmap has taken effect on disk share.
  incus config device set c1 d1 source="${testRoot}/allowed1" path=/mnt
  incus start c1 || (incus info --show-log c1 ; false)
  [ "$(incus exec c1 --project restricted -- stat /mnt/foo1 -c '%u:%g')" = "1000:1000" ] || false
  [ "$(incus exec c1 --project restricted -- stat /mnt/foo2 -c '%u:%g')" = "65534:65534" ] || false

  # Check adding unix socket is allowed.
  incus config device add c1 unix-socket disk source="${testRoot}/allowed1/incus.sock" path=/root/incus.sock
  [ "$(incus exec c1 --project restricted -- stat /root/incus.sock -c '%F')" = "socket" ] || false

  incus delete -f c1
  incus project switch default
  incus project delete restricted
  rm "${testRoot}/allowed1/not-allowed1"
  rm "${testRoot}/allowed1/not-allowed2"
  rm "${testRoot}/allowed1/foo1"
  rm "${testRoot}/allowed1/foo2"
  rm "${testRoot}/allowed1/foolink"
  rm "${testRoot}/allowed1/incus.sock"
  rmdir "${testRoot}/allowed1"
  rmdir "${testRoot}/allowed2"
  rmdir "${testRoot}/not-allowed1"
  rmdir "${testRoot}"
}
