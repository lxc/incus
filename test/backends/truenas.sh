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

    incus storage create "incustest-$(basename "${INCUS_DIR}")" truenas "$(truenas_source)/$(uuidgen)" "$(truenas_config)" "$(truenas_allow_insecure)" "$(truenas_api_key)"
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
    if [ -n "${INCUS_TRUENAS_HOST:-}" ]; then
        echo "${INCUS_TRUENAS_HOST}:${INCUS_TRUENAS_DATASET}"
    else
        echo "${INCUS_TRUENAS_DATASET}"
    fi
}

# returns the base truenas source string
truenas_source() {
    echo "source=$(truenas_host_dataset)"
}

truenas_api_key() {
    echo "truenas.api_key=${INCUS_TRUENAS_API_KEY:-}"
}

truenas_config() {
    echo "truenas.config=${INCUS_TRUENAS_CONFIG:-}"
}

truenas_allow_insecure() {
    echo "truenas.allow_insecure=${INCUS_TRUENAS_ALLOW_INSECURE:-}"
}

call_truenas_tool() {
    # usage: call_truenas_tool dataset list -r --no-headers "${truenas_dataset}"
    truenas_incus_ctl "--config=${INCUS_TRUENAS_CONFIG:-}" "--allow-insecure=${INCUS_TRUENAS_ALLOW_INSECURE:-0}" "--host=${INCUS_TRUENAS_HOST:-}" "--api-key=${INCUS_TRUENAS_API_KEY:-}" "$@"
}
