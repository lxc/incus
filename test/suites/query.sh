test_query() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  inc init testimage querytest
  inc query --wait -X POST -d "{\\\"name\\\": \\\"snap-test\\\"}" /1.0/instances/querytest/snapshots
  inc info querytest | grep snap-test
  inc delete querytest
}
