test_cloud_init() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  lxc init testimage c1
  ID1=$(lxc config get c1 volatile.cloud-init.instance-id)
  [ -n "${ID1}" ]

  lxc rename c1 c2
  ID2=$(lxc config get c2 volatile.cloud-init.instance-id)
  [ -n "${ID2}" ] && [ "${ID2}" != "${ID1}" ]

  lxc copy c2 c1
  ID3=$(lxc config get c1 volatile.cloud-init.instance-id)
  [ -n "${ID3}" ] && [ "${ID3}" != "${ID2}" ]

  lxc config set c1 cloud-init.user-data blah
  ID4=$(lxc config get c1 volatile.cloud-init.instance-id)
  [ -n "${ID4}" ] && [ "${ID4}" != "${ID3}" ]

  lxc config device override c1 eth0 user.foo=bar
  ID5=$(lxc config get c1 volatile.cloud-init.instance-id)
  [ "${ID5}" = "${ID4}" ]

  lxc config device set c1 eth0 name=foo
  ID6=$(lxc config get c1 volatile.cloud-init.instance-id)
  [ -n "${ID6}" ] && [ "${ID6}" != "${ID5}" ]

  lxc delete -f c1 c2
}
