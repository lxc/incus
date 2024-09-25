ceph_setup() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  INCUS_DIR=$1

  echo "==> Setting up CEPH backend in ${INCUS_DIR}"
}

ceph_configure() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  INCUS_DIR=$1

  echo "==> Configuring CEPH backend in ${INCUS_DIR}"

  incus storage create "incustest-$(basename "${INCUS_DIR}")" ceph volume.size=25MiB ceph.osd.pg_num=8
  incus profile device add default root disk path="/" pool="incustest-$(basename "${INCUS_DIR}")"
}

ceph_teardown() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  INCUS_DIR=$1

  echo "==> Tearing down CEPH backend in ${INCUS_DIR}"
}
