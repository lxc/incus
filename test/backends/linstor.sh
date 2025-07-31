# shellcheck shell=sh

linstor_setup() {
    # shellcheck disable=2039,3043
    local INCUS_DIR

    INCUS_DIR=$1

    echo "==> Setting up LINSTOR backend in ${INCUS_DIR}"
}

linstor_preconfigure() {
    # shellcheck disable=2039,3043
    local INCUS_DIR

    INCUS_DIR=$1

    incus config set storage.linstor.controller_connection "${INCUS_LINSTOR_CLUSTER}"
    if [ -n "${INCUS_LINSTOR_LOCAL_SATELLITE:-}" ]; then
        incus config set storage.linstor.satellite.name "${INCUS_LINSTOR_LOCAL_SATELLITE}"
    fi
}

linstor_configure() {
    # shellcheck disable=2039,3043
    local INCUS_DIR

    INCUS_DIR=$1

    echo "==> Configuring LINSTOR backend in ${INCUS_DIR}"

    linstor_preconfigure "${INCUS_DIR}"
    driver_config="linstor.resource_group.place_count=1"

    if [ -n "${LINSTOR_PREFIX_OVERRIDE:-}" ]; then
        driver_config="${driver_config} linstor.volume.prefix=${LINSTOR_PREFIX_OVERRIDE}"
    fi

    # shellcheck disable=SC2086
    incus storage create "incustest-$(basename "${INCUS_DIR}")" linstor ${driver_config}
    incus profile device add default root disk path="/" pool="incustest-$(basename "${INCUS_DIR}")"
}

linstor_teardown() {
    # shellcheck disable=2039,3043
    local INCUS_DIR

    INCUS_DIR=$1

    echo "==> Tearing down LINSTOR backend in ${INCUS_DIR}"
}
