# Nothing need be done for the dir backed, but we still need some functions.
# This file can also serve as a skel file for what needs to be done to
# implement a new backend.

# Any necessary backend-specific setup
dir_setup() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  INCUS_DIR=$1

  echo "==> Setting up directory backend in ${INCUS_DIR}"
}

# Do the API voodoo necessary to configure Incus to use this backend
dir_configure() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  INCUS_DIR=$1

  echo "==> Configuring directory backend in ${INCUS_DIR}"

  incus storage create "incustest-$(basename "${INCUS_DIR}")" dir
  incus profile device add default root disk path="/" pool="incustest-$(basename "${INCUS_DIR}")"
}

dir_teardown() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  INCUS_DIR=$1

  echo "==> Tearing down directory backend in ${INCUS_DIR}"
}
