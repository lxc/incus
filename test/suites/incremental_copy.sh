test_incremental_copy() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  do_copy "" ""

  # cross-pool copy
  # shellcheck disable=2039,3043
  local source_pool
  source_pool="incustest-$(basename "${INCUS_DIR}")-dir-pool"
  incus storage create "${source_pool}" dir
  do_copy "${source_pool}" "incustest-$(basename "${INCUS_DIR}")"
  incus storage rm "${source_pool}"
}

do_copy() {
  # shellcheck disable=2039,3043
  local source_pool="${1}"
  # shellcheck disable=2039,3043
  local target_pool="${2}"

  # Make sure the containers don't exist
  incus rm -f c1 c2 || true

  if [ -z "${source_pool}" ]; then
    source_pool=$(incus profile device get default root pool)
  fi

  incus init testimage c1 -s "${source_pool}"
  incus storage volume set "${source_pool}" container/c1 user.foo=main

  # Set size to check this is supported during copy.
  incus config device set c1 root size=200MiB

  targetPoolFlag=
  if [ -n "${target_pool}" ]; then
    targetPoolFlag="-s ${target_pool}"
  else
    target_pool="${source_pool}"
  fi

  # Initial copy
  # shellcheck disable=2086
  incus copy c1 c2 ${targetPoolFlag}
  incus storage volume get "${target_pool}" container/c2 user.foo | grep -Fx "main"

  incus start c1 c2

  # Target container may not be running when refreshing
  # shellcheck disable=2086
  ! incus copy c1 c2 --refresh ${targetPoolFlag} || false

  # Create test file in c1
  incus exec c1 -- touch /root/testfile1

  incus stop -f c2

  # Refresh the container and validate the contents
  # shellcheck disable=2086
  incus copy c1 c2 --refresh ${targetPoolFlag}
  incus start c2
  incus exec c2 -- test -f /root/testfile1
  incus stop -f c2

  # This will create snapshot c1/snap0
  incus storage volume set "${source_pool}" container/c1 user.foo=snap0
  incus snapshot create c1
  incus storage volume set "${source_pool}" container/c1 user.foo=snap1
  incus snapshot create c1
  incus storage volume set "${source_pool}" container/c1 user.foo=main

  # Remove the testfile from c1 and refresh again
  incus exec c1 -- rm /root/testfile1
  # shellcheck disable=2086
  incus copy c1 c2 --refresh --instance-only ${targetPoolFlag}
  incus start c2
  ! incus exec c2 -- test -f /root/testfile1 || false
  incus stop -f c2

  # Check whether snapshot c2/snap0 has been created
  ! incus config show c2/snap0 || false
  # shellcheck disable=2086
  incus copy c1 c2 --refresh ${targetPoolFlag}
  incus config show c2/snap0
  incus config show c2/snap1
  incus storage volume get "${target_pool}" container/c2 user.foo | grep -Fx "main"
  incus storage volume get "${target_pool}" container/c2/snap0 user.foo | grep -Fx "snap0"
  incus storage volume get "${target_pool}" container/c2/snap1 user.foo | grep -Fx "snap1"

  # This will create snapshot c2/snap2
  incus snapshot create c2
  incus config show c2/snap2
  incus storage volume snapshot show "${target_pool}" container/c2/snap2

  # This should remove c2/snap2
  # shellcheck disable=2086
  incus copy c1 c2 --refresh ${targetPoolFlag}
  ! incus config show c2/snap2 || false
  ! incus storage volume snapshot show "${target_pool}" container/c2/snap2 || false

  incus rm -f c1 c2
}
