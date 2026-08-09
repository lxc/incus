btrfs_setup() {
    # shellcheck disable=2039,3043
    local INCUS_DIR

    INCUS_DIR=$1

    echo "==> Setting up btrfs backend in ${INCUS_DIR}"
}

btrfs_configure() {
    # shellcheck disable=2039,3043
    local INCUS_DIR

    INCUS_DIR=$1

    # Use a larger pool than other backends as btrfs cannot reclaim its
    # allocated metadata chunks and runs out of space on 1GiB pools.
    incus storage create "incustest-$(basename "${INCUS_DIR}")" btrfs size=4GiB
    incus profile device add default root disk path="/" pool="incustest-$(basename "${INCUS_DIR}")"

    echo "==> Configuring btrfs backend in ${INCUS_DIR}"
}

btrfs_teardown() {
    # shellcheck disable=2039,3043
    local INCUS_DIR

    INCUS_DIR=$1

    echo "==> Tearing down btrfs backend in ${INCUS_DIR}"
}
