test_incremental_copy() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  do_copy "" ""

  # cross-pool copy
  # shellcheck disable=2039,3043
  local source_pool
  source_pool="incustest-$(basename "${INCUS_DIR}")-dir-pool"
  lxc storage create "${source_pool}" dir
  do_copy "${source_pool}" "incustest-$(basename "${INCUS_DIR}")"
  lxc storage rm "${source_pool}"
}

do_copy() {
  # shellcheck disable=2039,3043
  local source_pool="${1}"
  # shellcheck disable=2039,3043
  local target_pool="${2}"

  # Make sure the containers don't exist
  lxc rm -f c1 c2 || true

  if [ -z "${source_pool}" ]; then
    source_pool=$(lxc profile device get default root pool)
  fi

  lxc init testimage c1 -s "${source_pool}"
  lxc storage volume set "${source_pool}" container/c1 user.foo=main

  # Set size to check this is supported during copy.
  lxc config device set c1 root size=50MiB

  targetPoolFlag=
  if [ -n "${target_pool}" ]; then
    targetPoolFlag="-s ${target_pool}"
  else
    target_pool="${source_pool}"
  fi

  # Initial copy
  # shellcheck disable=2086
  lxc copy c1 c2 ${targetPoolFlag}
  lxc storage volume get "${target_pool}" container/c2 user.foo | grep -Fx "main"

  lxc start c1 c2

  # Target container may not be running when refreshing
  # shellcheck disable=2086
  ! lxc copy c1 c2 --refresh ${targetPoolFlag} || false

  # Create test file in c1
  lxc exec c1 -- touch /root/testfile1

  lxc stop -f c2

  # Refresh the container and validate the contents
  # shellcheck disable=2086
  lxc copy c1 c2 --refresh ${targetPoolFlag}
  lxc start c2
  lxc exec c2 -- test -f /root/testfile1
  lxc stop -f c2

  # This will create snapshot c1/snap0
  lxc storage volume set "${source_pool}" container/c1 user.foo=snap0
  lxc snapshot c1
  lxc storage volume set "${source_pool}" container/c1 user.foo=snap1
  lxc snapshot c1
  lxc storage volume set "${source_pool}" container/c1 user.foo=main

  # Remove the testfile from c1 and refresh again
  lxc exec c1 -- rm /root/testfile1
  # shellcheck disable=2086
  lxc copy c1 c2 --refresh --instance-only ${targetPoolFlag}
  lxc start c2
  ! lxc exec c2 -- test -f /root/testfile1 || false
  lxc stop -f c2

  # Check whether snapshot c2/snap0 has been created
  ! lxc config show c2/snap0 || false
  # shellcheck disable=2086
  lxc copy c1 c2 --refresh ${targetPoolFlag}
  lxc config show c2/snap0
  lxc config show c2/snap1
  lxc storage volume get "${target_pool}" container/c2 user.foo | grep -Fx "main"
  lxc storage volume get "${target_pool}" container/c2/snap0 user.foo | grep -Fx "snap0"
  lxc storage volume get "${target_pool}" container/c2/snap1 user.foo | grep -Fx "snap1"

  # This will create snapshot c2/snap2
  lxc snapshot c2
  lxc config show c2/snap2
  lxc storage volume show "${target_pool}" container/c2/snap2

  # This should remove c2/snap2
  # shellcheck disable=2086
  lxc copy c1 c2 --refresh ${targetPoolFlag}
  ! lxc config show c2/snap2 || false
  ! lxc storage volume show "${target_pool}" container/c2/snap2 || false

  lxc rm -f c1 c2
}
