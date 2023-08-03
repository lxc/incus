test_cloud_init() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  inc init testimage c1
  ID1=$(inc config get c1 volatile.cloud-init.instance-id)
  [ -n "${ID1}" ]

  inc rename c1 c2
  ID2=$(inc config get c2 volatile.cloud-init.instance-id)
  [ -n "${ID2}" ] && [ "${ID2}" != "${ID1}" ]

  inc copy c2 c1
  ID3=$(inc config get c1 volatile.cloud-init.instance-id)
  [ -n "${ID3}" ] && [ "${ID3}" != "${ID2}" ]

  inc config set c1 cloud-init.user-data blah
  ID4=$(inc config get c1 volatile.cloud-init.instance-id)
  [ -n "${ID4}" ] && [ "${ID4}" != "${ID3}" ]

  inc config device override c1 eth0 user.foo=bar
  ID5=$(inc config get c1 volatile.cloud-init.instance-id)
  [ "${ID5}" = "${ID4}" ]

  inc config device set c1 eth0 name=foo
  ID6=$(inc config get c1 volatile.cloud-init.instance-id)
  [ -n "${ID6}" ] && [ "${ID6}" != "${ID5}" ]

  inc delete -f c1 c2
}
