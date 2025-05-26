truenas_setup() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  INCUS_DIR=$1

  echo "==> Setting up TrueNAS backend in ${INCUS_DIR}"
}

truenas_configure() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  INCUS_DIR=$1


  echo "==> Configuring TrueNAS backend in ${INCUS_DIR}"

  incus storage create "incustest-$(basename "${INCUS_DIR}")" truenas "$(truenas_source)/$(basename "${INCUS_DIR}")" $(truenas_api_key)
  incus profile device add default root disk path="/" pool="incustest-$(basename "${INCUS_DIR}")"
}

truenas_teardown() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  INCUS_DIR=$1

  echo "==> Tearing down TrueNAS backend in ${INCUS_DIR}"
}

# returns the base truenas source string
truenas_host_dataset() {
  echo "${INCUS_TN_HOST}:${INCUS_TN_DATASET}"
}

# returns the base truenas source string
truenas_source() {
  echo "source=$(truenas_host_dataset)"
}

# returns the base truenas source string
truenas_source_uuid() {
  echo "$(truenas_source)/$(uuidgen)"
}

# returns the base truenas source string
truenas_api_key() {
  echo "truenas.api_key=${INCUS_TN_APIKEY}"
}
