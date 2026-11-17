test_init_dump() {
    # - incus admin init --dump
    INCUS_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${INCUS_INIT_DIR}"
    spawn_incus "${INCUS_INIT_DIR}" false

    INCUS_RESTORE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${INCUS_RESTORE_DIR}"
    spawn_incus "${INCUS_RESTORE_DIR}" false

    (
        set -e
        # shellcheck disable=SC2034
        INCUS_DIR=${INCUS_INIT_DIR}

        storage_pool="incustest-$(basename "${INCUS_DIR}")-data"

        incus storage create "${storage_pool}" dir
        incus profile device add default root disk path=/ pool="${storage_pool}"
        incus profile create test-profile --description "Test profile"
        incus profile set test-profile limits.memory=2GiB
        incus project create dump-test
        incus profile create example --project dump-test --description "Example profile"
        incus profile set example limits.memory=512MiB --project dump-test

        # Profiles from every project are dumped along with their project.
        incus admin init --dump > "${TEST_DIR}/dump.yaml"
        grep -q "project: dump-test" "${TEST_DIR}/dump.yaml"
        grep -q "name: example" "${TEST_DIR}/dump.yaml"

        # The dump recreates the same configuration on another server.
        init_address=$(incus config get core.https_address)
        restore_address=$(INCUS_DIR="${INCUS_RESTORE_DIR}" incus config get core.https_address)
        sed -e "s#${INCUS_INIT_DIR}#${INCUS_RESTORE_DIR}#g" -e "s#${init_address}#${restore_address}#g" "${TEST_DIR}/dump.yaml" | INCUS_DIR="${INCUS_RESTORE_DIR}" incus admin init --preseed
        INCUS_DIR="${INCUS_RESTORE_DIR}" incus profile show default | grep -q "pool: ${storage_pool}"
        INCUS_DIR="${INCUS_RESTORE_DIR}" incus profile show test-profile | grep -q "limits.memory: 2GiB"
        INCUS_DIR="${INCUS_RESTORE_DIR}" incus profile show example --project dump-test | grep -q "limits.memory: 512MiB"
        INCUS_DIR="${INCUS_RESTORE_DIR}" incus admin init --dump | sed -e "s#${INCUS_RESTORE_DIR}#${INCUS_INIT_DIR}#g" -e "s#${restore_address}#${init_address}#g" | diff -u "${TEST_DIR}/dump.yaml" -

        for dir in "${INCUS_INIT_DIR}" "${INCUS_RESTORE_DIR}"; do
            INCUS_DIR="${dir}" incus profile delete example --project dump-test
            INCUS_DIR="${dir}" incus project delete dump-test
        done
    )
    rm -f "${TEST_DIR}/dump.yaml"
    kill_incus "${INCUS_RESTORE_DIR}"
    kill_incus "${INCUS_INIT_DIR}"
}
