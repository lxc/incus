test_security() {
    ensure_import_testimage

    # shellcheck disable=2153
    ensure_has_localhost_remote "${INCUS_ADDR}"

    # CVE-2016-1581
    if [ "$(storage_backend "$INCUS_DIR")" = "zfs" ]; then
        INCUS_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
        chmod +x "${INCUS_INIT_DIR}"
        spawn_incus "${INCUS_INIT_DIR}" false

        ZFS_POOL="incustest-$(basename "${INCUS_DIR}")-init"
        INCUS_DIR=${INCUS_INIT_DIR} incus admin init --storage-backend zfs --storage-create-loop 1 --storage-pool "${ZFS_POOL}" --auto

        PERM=$(stat -c %a "${INCUS_INIT_DIR}/disks/${ZFS_POOL}.img")
        if [ "${PERM}" != "600" ]; then
            echo "Bad zfs.img permissions: ${PERM}"
            false
        fi

        kill_incus "${INCUS_INIT_DIR}"
    fi

    # CVE-2016-1582
    incus launch testimage test-priv -c security.privileged=true

    PERM=$(stat -L -c %a "${INCUS_DIR}/containers/test-priv")
    UID=$(stat -L -c %u "${INCUS_DIR}/containers/test-priv")
    if [ "${PERM}" != "100" ]; then
        echo "Bad container permissions: ${PERM}"
        false
    fi

    if [ "${UID}" != "0" ]; then
        echo "Bad container owner: ${UID}"
        false
    fi

    incus config set test-priv security.privileged false
    incus restart test-priv --force
    incus config set test-priv security.privileged true
    incus restart test-priv --force

    PERM=$(stat -L -c %a "${INCUS_DIR}/containers/test-priv")
    UID=$(stat -L -c %u "${INCUS_DIR}/containers/test-priv")
    if [ "${PERM}" != "100" ]; then
        echo "Bad container permissions: ${PERM}"
        false
    fi

    if [ "${UID}" != "0" ]; then
        echo "Bad container owner: ${UID}"
        false
    fi

    incus delete test-priv --force

    incus launch testimage test-unpriv
    incus config set test-unpriv security.privileged true
    incus restart test-unpriv --force

    PERM=$(stat -L -c %a "${INCUS_DIR}/containers/test-unpriv")
    UID=$(stat -L -c %u "${INCUS_DIR}/containers/test-unpriv")
    if [ "${PERM}" != "100" ]; then
        echo "Bad container permissions: ${PERM}"
        false
    fi

    if [ "${UID}" != "0" ]; then
        echo "Bad container owner: ${UID}"
        false
    fi

    incus config set test-unpriv security.privileged false
    incus restart test-unpriv --force

    PERM=$(stat -L -c %a "${INCUS_DIR}/containers/test-unpriv")
    UID=$(stat -L -c %u "${INCUS_DIR}/containers/test-unpriv")
    if [ "${PERM}" != "100" ]; then
        echo "Bad container permissions: ${PERM}"
        false
    fi

    if [ "${UID}" = "0" ]; then
        echo "Bad container owner: ${UID}"
        false
    fi

    incus delete test-unpriv --force

    # Spawn a separate daemon

    # shellcheck disable=2039,3043
    local INCUS_STORAGE_DIR

    INCUS_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
    chmod +x "${INCUS_STORAGE_DIR}"
    spawn_incus "${INCUS_STORAGE_DIR}" true

    (
        set -e
        # shellcheck disable=2030
        INCUS_DIR="${INCUS_STORAGE_DIR}"

        # Enforce that only unprivileged containers can be created
        incus project set default restricted=true

        # Needed for the default profile in the test suite
        incus project set default restricted.devices.nic=allow

        # Import image into default storage pool.
        ensure_import_testimage

        # Verify that no privileged container can be created
        ! incus launch testimage c1 -c security.privileged=true || false

        # Verify that unprivileged container can be created
        incus launch testimage c1

        # Verify that we can't be tricked into using privileged containers
        ! incus config set c1 security.privileged true || false
        ! incus config set c1 raw.idmap "both 0 1000" || false
        ! incus config set c1 raw.lxc "lxc.idmap=" || false
        ! incus config set c1 raw.lxc "lxc.include=" || false

        # Verify that we can still unset and set to security.privileged to "false"
        incus config set c1 security.privileged false
        incus config unset c1 security.privileged

        # Verify that a profile can't be changed to trick us into using privileged
        # containers
        ! incus profile set default security.privileged true || false
        ! incus profile set default raw.idmap "both 0 1000" || false
        ! incus profile set default raw.lxc "lxc.idmap=" || false
        ! incus profile set default raw.lxc "lxc.include=" || false

        # Verify that we can still unset and set to security.privileged to "false"
        incus profile set default security.privileged false
        incus profile unset default security.privileged

        incus delete -f c1
    )

    # shellcheck disable=SC2031,2269
    INCUS_DIR="${INCUS_DIR}"
    kill_incus "${INCUS_STORAGE_DIR}"
}

test_security_protection() {
    ensure_import_testimage
    ensure_has_localhost_remote "${INCUS_ADDR}"

    # Test deletion protecton
    incus init testimage c1
    incus snapshot create c1
    incus delete c1

    incus profile set default security.protection.delete true

    incus init testimage c1
    incus snapshot create c1
    incus snapshot delete c1 snap0
    ! incus delete c1 || false

    incus config set c1 security.protection.delete false
    incus delete c1

    incus profile unset default security.protection.delete

    # Test shifting protection

    # Respawn Incus with kernel ID shifting support disabled to force manual shifting.
    shutdown_incus "${INCUS_DIR}"
    incusIdmappedMountsDisable=${INCUS_IDMAPPED_MOUNTS_DISABLE:-}

    export INCUS_IDMAPPED_MOUNTS_DISABLE=1
    respawn_incus "${INCUS_DIR}" true

    incus init testimage c1
    incus start c1
    incus stop c1 --force

    incus profile set default security.protection.shift true
    incus start c1
    incus stop c1 --force

    incus publish c1 --alias=protected
    incus image delete protected

    incus snapshot create c1
    incus publish c1/snap0 --alias=protected
    incus image delete protected

    incus config set c1 security.privileged true
    ! incus start c1 || false
    incus config set c1 security.protection.shift false
    incus start c1
    incus stop c1 --force

    incus delete c1
    incus profile unset default security.protection.shift

    # Respawn Incus to restore default kernel shifting support.
    shutdown_incus "${INCUS_DIR}"
    export INCUS_IDMAPPED_MOUNTS_DISABLE="${incusIdmappedMountsDisable}"

    respawn_incus "${INCUS_DIR}" true
}
