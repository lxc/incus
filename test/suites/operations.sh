# Helper function that waits until all operations across all projects have completed.
wait_no_operations() {
    retries=30

    while [ "${retries}" -gt 0 ]; do
        echo "Waiting operations to complete (${retries} retries left) ..."
        count=$(incus query "/1.0/operations?all-projects=true" | jq '.success | length')
        if [ -z "${count}" ] || [ "${count}" -eq 0 ]; then
            return 0
        fi

        retries=$((retries - 1))
        sleep 1
    done

    echo "Exceeded maximum retries waiting for operations to complete."
    return 1
}

# Helper function that asserts the number of operations in all projects equals
# the provided operation count.
assert_all_operations_count() {
    opCount="$1"

    result=$(incus query "/1.0/operations?all-projects=true" | jq '.success | length')
    test "${result}" -eq "${opCount}"

    result=$(incus query "/1.0/operations?all-projects=true&recursion=1" | jq '[.success[] | select(.status_code)] | length')
    test "${result}" -eq "${opCount}"
}

# Helper function that asserts the number of operations in a specific project
# equals the provided operation count.
assert_project_operations_count() {
    project="$1"
    opCount="$2"

    result=$(incus query "/1.0/operations?project=${project}" | jq '.success | length ')
    test "${result}" -eq "${opCount}"

    result=$(incus query "/1.0/operations?project=${project}&recursion=1" | jq '[.success[] | select(.status_code)] | length')
    test "${result}" -eq "${opCount}"
}

test_get_operations() {
    ensure_import_testimage

    proj1="op-proj1"
    proj2="op-proj2"

    (
        set -e

        incus project create "${proj1}" -c features.profiles=false -c features.images=false
        incus project create "${proj2}" -c features.profiles=false -c features.images=false

        incus launch testimage c1 --project "${proj1}"
        incus launch testimage c2 --project "${proj2}"

        wait_no_operations

        # For each project, generate a single operation.
        incus exec -T --project="${proj1}" c1 true
        incus exec -T --project="${proj2}" c2 true

        # Verify that both individual project operations and the collective set of
        # operations are queried correctly.
        assert_project_operations_count "${proj1}" 1
        assert_project_operations_count "${proj2}" 1
        assert_all_operations_count 2

        incus delete c1 --force --project "${proj1}"
        incus delete c2 --force --project "${proj2}"
    )
}

test_operation_wait() {
    ensure_import_testimage

    (
        cd operation-wait || return
        # Use -buildvcs=false here to prevent git complaining about untrusted directory when tests are run as root.
        go build -v -buildvcs=false ./...
    )

    incus launch testimage c1

    # Without events, the wait ends with the wait or the client context.
    [ "$(timeout 15 ./operation-wait/operation-wait "${INCUS_DIR}/unix.socket" c1 deadline 2)" = "context deadline exceeded" ]
    [ "$(timeout 15 ./operation-wait/operation-wait "${INCUS_DIR}/unix.socket" c1 deadline -1)" = "context deadline exceeded" ]
    [ "$(timeout 15 ./operation-wait/operation-wait "${INCUS_DIR}/unix.socket" c1 cancel 2)" = "context canceled" ]
    timeout 15 ./operation-wait/operation-wait "${INCUS_DIR}/unix.socket" c1 client-cancel 2 | grep -q "context canceled"

    # Without deadline or cancellation, the wait lasts until the operation ends.
    start="$(date +%s)"
    [ "$(timeout 15 ./operation-wait/operation-wait "${INCUS_DIR}/unix.socket" c1 none 0)" = "<nil>" ]
    [ "$(($(date +%s) - start))" -ge 9 ]

    incus delete c1 --force
}
