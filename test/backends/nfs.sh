nfs_setup() {
    # shellcheck disable=2039,3043
    local INCUS_DIR

    INCUS_DIR=$1

    echo "==> Setting up nfs backend in ${INCUS_DIR}"
}

nfs_configure() {
    # shellcheck disable=2039,3043
    local INCUS_DIR

    INCUS_DIR=$1

    echo "==> Configuring nfs backend in ${INCUS_DIR}"

    incus storage create "incustest-$(basename "${INCUS_DIR}")" nfs
    incus profile device add default root disk path="/" pool="incustest-$(basename "${INCUS_DIR}")"
}

nfs_teardown() {
    # shellcheck disable=2039,3043
    local INCUS_DIR

    INCUS_DIR=$1

    echo "==> Tearing down nfs backend in ${INCUS_DIR}"
}
