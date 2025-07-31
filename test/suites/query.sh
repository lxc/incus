test_query() {
    ensure_import_testimage
    ensure_has_localhost_remote "${INCUS_ADDR}"

    incus init testimage querytest
    incus query --wait -X POST -d "{\\\"name\\\": \\\"snap-test\\\"}" /1.0/instances/querytest/snapshots
    incus info querytest | grep snap-test
    incus delete querytest
}
