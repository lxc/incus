lvm_setup() {
    # shellcheck disable=2039,3043
    local INCUS_DIR

    INCUS_DIR=$1

    echo "==> Setting up lvm backend in ${INCUS_DIR}"
}

lvm_configure() {
    # shellcheck disable=2039,3043
    local INCUS_DIR

    INCUS_DIR=$1

    echo "==> Configuring lvm backend in ${INCUS_DIR}"

    incus storage create "incustest-$(basename "${INCUS_DIR}")" lvm volume.size=25MiB size=1GiB
    incus profile device add default root disk path="/" pool="incustest-$(basename "${INCUS_DIR}")"
}

lvm_teardown() {
    # shellcheck disable=2039,3043
    local INCUS_DIR

    INCUS_DIR=$1

    echo "==> Tearing down lvm backend in ${INCUS_DIR}"
}
