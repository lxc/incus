truenas_setup() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  INCUS_DIR=$1

  echo "==> Setting up TrueBAS backend in ${INCUS_DIR}"
}

truenas_configure() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  INCUS_DIR=$1

  echo "==> Configuring TrueNAS backend in ${INCUS_DIR}"

  incus storage create "incustest-$(basename "${INCUS_DIR}")" truenas "source=${INCUS_TN_HOST}:${INCUS_TN_DATASET}/$(uuidgen)" "truenas.api_key=${INCUS_TN_APIKEY}"
  incus profile device add default root disk path="/" pool="incustest-$(basename "${INCUS_DIR}")"
}

truenas_teardown() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  INCUS_DIR=$1

  echo "==> Tearing down TrueNAS backend in ${INCUS_DIR}"
}
