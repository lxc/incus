zfs_setup() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  INCUS_DIR=$1

  echo "==> Setting up ZFS backend in ${INCUS_DIR}"
}

zfs_configure() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  INCUS_DIR=$1

  echo "==> Configuring ZFS backend in ${INCUS_DIR}"

  incus storage create "incustest-$(basename "${INCUS_DIR}")" zfs size=1GiB
  incus profile device add default root disk path="/" pool="incustest-$(basename "${INCUS_DIR}")"
}

zfs_teardown() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  INCUS_DIR=$1

  echo "==> Tearing down ZFS backend in ${INCUS_DIR}"
}
