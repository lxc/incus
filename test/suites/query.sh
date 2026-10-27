test_query() {
    ensure_import_testimage
    ensure_has_localhost_remote "${INCUS_ADDR}"

    incus init testimage querytest
    incus query --wait -X POST -d "{\\\"name\\\": \\\"snap-test\\\"}" /1.0/instances/querytest/snapshots
    incus info querytest | grep snap-test

    # Bad --header values are rejected.
    ! incus query -H "bogus" /1.0 || false
    ! incus query -H ": value" /1.0 || false

    # --header allows uploading a file through the raw API.
    incus start querytest

    echo "hello" > "${TEST_DIR}/query-upload.txt"
    incus query -X POST --data-file "${TEST_DIR}/query-upload.txt" \
        -H "X-Incus-type: file" -H "X-Incus-mode: 0600" -H "X-Incus-uid: 0" -H "X-Incus-gid: 0" \
        "/1.0/instances/querytest/files?path=/root/query-upload.txt"
    [ "$(incus exec querytest -- stat -c "%a %u %g" /root/query-upload.txt)" = "600 0 0" ]
    [ "$(incus file pull querytest/root/query-upload.txt -)" = "hello" ]
    rm "${TEST_DIR}/query-upload.txt"

    incus delete -f querytest
}
