test_container_devices_unix_block() {
  test_container_devices_unix "unix-block"
}

test_container_devices_unix_char() {
  test_container_devices_unix "unix-char"
}

test_container_devices_unix() {
  deviceType=$1
  deviceTypeCode=""
  deviceTypeDesc=""

  if [ "$deviceType" = "unix-block" ]; then
    deviceTypeCode="b"
    deviceTypeDesc="block special file"
  fi

  if [ "$deviceType" = "unix-char" ]; then
    deviceTypeCode="c"
    deviceTypeDesc="character special file"
  fi

  if [ "$deviceTypeCode" = "" ]; then
    echo "invalid device type specified in test"
    false
  fi

  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"
  ctName="ct$$"
  incus launch testimage "${ctName}"

  # Create a test unix device.
  testDev="${TEST_DIR}"/dev/testdev-"${ctName}"
  mknod "${testDev}" "${deviceTypeCode}" 0 0

  # Check adding a device without source or path fails.
  ! incus config device add "${ctName}" test-dev-invalid "${deviceType}"
  ! incus config device add "${ctName}" test-dev-invalid "${deviceType}" required=false

  # Check adding a device with missing source and no major/minor numbers fails.
  ! incus config device add "${ctName}" test-dev-invalid "${deviceType}" path=/tmp/testdevmissing

  # Check adding a required (default) missing device fails.
  ! incus config device add "${ctName}" test-dev-invalid "${deviceType}" path=/tmp/testdevmissing
  ! incus config device add "${ctName}" test-dev-invalid "${deviceType}" path=/tmp/testdevmissing required=true

  # Add device based on existing device, check its host-side name, default mode, major/minor inherited, and mounted in container.
  incus config device add "${ctName}" test-dev1 "${deviceType}" source="${testDev}" path=/tmp/testdev
  incus exec "${ctName}" -- mount | grep "/tmp/testdev"
  incus exec "${ctName}" -- stat -c '%F %a %t %T' /tmp/testdev | grep "${deviceTypeDesc} 660 0 0"
  stat -c '%F %a %t %T' "${INCUS_DIR}"/devices/"${ctName}"/unix.test--dev1.tmp-testdev | grep "${deviceTypeDesc} 660 0 0"

  # Add device with same dest path as existing device, but with different mode and major/minor and check original isn't replaced inside instance.
  incus config device add "${ctName}" test-dev2 "${deviceType}" source="${testDev}" path=/tmp/testdev major=1 minor=1 mode=600
  incus exec "${ctName}" -- mount | grep "/tmp/testdev"
  incus exec "${ctName}" -- stat -c '%F %a %t %T' /tmp/testdev | grep "${deviceTypeDesc} 660 0 0"

  # Check a new host side file was created with correct attributes.
  stat -c '%F %a %t %T' "${INCUS_DIR}"/devices/"${ctName}"/unix.test--dev2.tmp-testdev | grep "${deviceTypeDesc} 600 1 1"

  # Remove dupe device and check the original is still mounted.
  incus config device remove "${ctName}" test-dev2
  incus exec "${ctName}" -- mount | grep "/tmp/testdev"
  incus exec "${ctName}" -- stat -c '%F %a %t %T' /tmp/testdev | grep "${deviceTypeDesc} 660 0 0"

  # Check dupe device host side file is removed though.
  if ls "${INCUS_DIR}"/devices/"${ctName}"/unix.test--dev2.tmp-testdev; then
    echo "test-dev2 host side file not removed"
    false
  fi

  # Add new device with custom mode and check it creates correctly on boot.
  incus stop -f "${ctName}"
  incus config device add "${ctName}" test-dev3 "${deviceType}" source="${testDev}" path=/tmp/testdev3 major=1 minor=1 mode=600
  incus start "${ctName}"
  incus exec "${ctName}" -- mount | grep "/tmp/testdev3"
  incus exec "${ctName}" -- stat -c '%F %a %t %T' /tmp/testdev3 | grep "${deviceTypeDesc} 600 1 1"
  stat -c '%F %a %t %T' "${INCUS_DIR}"/devices/"${ctName}"/unix.test--dev3.tmp-testdev3 | grep "${deviceTypeDesc} 600 1 1"
  incus config device remove "${ctName}" test-dev3

  # Add new device without a source, but with a path and major and minor numbers.
  incus config device add "${ctName}" test-dev4 "${deviceType}" path=/tmp/testdev4 major=0 minor=2 mode=777
  incus exec "${ctName}" -- mount | grep "/tmp/testdev4"
  incus exec "${ctName}" -- stat -c '%F %a %t %T' /tmp/testdev4 | grep "${deviceTypeDesc} 777 0 2"
  stat -c '%F %a %t %T' "${INCUS_DIR}"/devices/"${ctName}"/unix.test--dev4.tmp-testdev4 | grep "${deviceTypeDesc} 777 0 2"
  incus config device remove "${ctName}" test-dev4

  incus stop -f "${ctName}"
  incus config device remove "${ctName}" test-dev1
  rm "${testDev}"

  # Add a device that is missing, but not required, start instance and then add it.
  incus config device add "${ctName}" test-dev-dynamic "${deviceType}" required=false source="${testDev}" path=/tmp/testdev
  incus start "${ctName}"
  ! ls "${INCUS_DIR}"/devices/"${ctName}"/unix.test--dev--dynamic.tmp-testdev
  mknod "${testDev}" "${deviceTypeCode}" 0 0
  sleep 2
  incus exec "${ctName}" -- mount | grep "/tmp/testdev"
  incus exec "${ctName}" -- stat -c '%F %a %t %T' /tmp/testdev | grep "${deviceTypeDesc} 660 0 0"
  stat -c '%F %a %t %T' "${INCUS_DIR}"/devices/"${ctName}"/unix.test--dev--dynamic.tmp-testdev | grep "${deviceTypeDesc} 660 0 0"

  # Remove host side device and check it is dynamically removed from instance.
  rm "${testDev}"
  sleep 1
  ! incus exec "${ctName}" -- mount | grep "/tmp/testdev"
  ! incus exec "${ctName}" -- ls /tmp/testdev
  ! ls "${INCUS_DIR}"/devices/"${ctName}"/unix.test--dev--dynamic.tmp-testdev

  # Leave instance running, restart Incus, then add device back to check Incus start time inotify works.
  shutdown_incus "${INCUS_DIR}"
  respawn_incus "${INCUS_DIR}" true
  mknod "${testDev}" "${deviceTypeCode}" 0 0
  sleep 2
  incus exec "${ctName}" -- mount | grep "/tmp/testdev"
  incus exec "${ctName}" -- stat -c '%F %a %t %T' /tmp/testdev | grep "${deviceTypeDesc} 660 0 0"
  stat -c '%F %a %t %T' "${INCUS_DIR}"/devices/"${ctName}"/unix.test--dev--dynamic.tmp-testdev | grep "${deviceTypeDesc} 660 0 0"

  # Update device's source, check old instance device is removed and new watchers set up.
  rm "${testDev}"
  testDevSubDir="${testDev}"/subdev
  ls -la "${TEST_DIR}"
  incus config device set "${ctName}" test-dev-dynamic source="${testDevSubDir}"
  ! incus exec "${ctName}" -- mount | grep "/tmp/testdev"
  ! incus exec "${ctName}" -- ls /tmp/testdev
  ! ls "${INCUS_DIR}"/devices/"${ctName}"/unix.test--dev--dynamic.tmp-testdev

  mkdir "${testDev}"
  mknod "${testDevSubDir}" "${deviceTypeCode}" 0 0
  sleep 2
  incus exec "${ctName}" -- mount | grep "/tmp/testdev"
  incus exec "${ctName}" -- stat -c '%F %a %t %T' /tmp/testdev | grep "${deviceTypeDesc} 660 0 0"
  stat -c '%F %a %t %T' "${INCUS_DIR}"/devices/"${ctName}"/unix.test--dev--dynamic.tmp-testdev | grep "${deviceTypeDesc} 660 0 0"

  # Cleanup.
  rm -rvf "${testDev}"
  sleep 1
  ! incus exec "${ctName}" -- mount | grep "/tmp/testdev"
  ! incus exec "${ctName}" -- ls /tmp/testdev
  ! ls "${INCUS_DIR}"/devices/"${ctName}"/unix.test--dev--dynamic.tmp-testdev
  incus delete -f "${ctName}"

  # Check multiple instances sharing same watcher.
  incus launch testimage "${ctName}1"
  incus config device add "${ctName}1" test-dev-dynamic "${deviceType}" required=false source="${testDev}" path=/tmp/testdev1
  incus launch testimage "${ctName}2"
  incus config device add "${ctName}2" test-dev-dynamic "${deviceType}" required=false source="${testDev}" path=/tmp/testdev2
  mknod "${testDev}" "${deviceTypeCode}" 0 0
  sleep 2
  incus exec "${ctName}1" -- mount | grep "/tmp/testdev1"
  incus exec "${ctName}1" -- stat -c '%F %a %t %T' /tmp/testdev1 | grep "${deviceTypeDesc} 660 0 0"
  stat -c '%F %a %t %T' "${INCUS_DIR}"/devices/"${ctName}"1/unix.test--dev--dynamic.tmp-testdev1 | grep "${deviceTypeDesc} 660 0 0"
  incus exec "${ctName}2" -- mount | grep "/tmp/testdev2"
  incus exec "${ctName}2" -- stat -c '%F %a %t %T' /tmp/testdev2 | grep "${deviceTypeDesc} 660 0 0"
  stat -c '%F %a %t %T' "${INCUS_DIR}"/devices/"${ctName}"2/unix.test--dev--dynamic.tmp-testdev2 | grep "${deviceTypeDesc} 660 0 0"

  # Stop one instance, then remove the host device to check the watcher still works after first
  # instance was stopped. This checks the removal logic when multiple containers share watch path.
  incus stop -f "${ctName}1"
  rm "${testDev}"
  sleep 1
  ! incus exec "${ctName}2" -- mount | grep "/tmp/testdev2"
  ! incus exec "${ctName}2" -- ls /tmp/testdev2
  ! ls "${INCUS_DIR}"/devices/"${ctName}"2/unix.test--dev--dynamic.tmp-testdev2
  incus delete -f "${ctName}1"
  incus delete -f "${ctName}2"
}

