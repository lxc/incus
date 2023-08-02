test_query() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  lxc init testimage querytest
  lxc query --wait -X POST -d "{\\\"name\\\": \\\"snap-test\\\"}" /1.0/containers/querytest/snapshots
  lxc info querytest | grep snap-test
  lxc delete querytest
}
