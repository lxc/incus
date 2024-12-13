test_network() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  incus init testimage nettest

  # Test DNS resolution of instance names
  incus network create inct$$
  incus launch testimage 0abc -n inct$$
  incus launch testimage def0 -n inct$$
  v4_addr="$(incus network get inct$$ ipv4.address | cut -d/ -f1)"
  sleep 2
  dig @"${v4_addr}" 0abc.incus
  dig @"${v4_addr}" def0.incus
  incus delete -f 0abc
  incus delete -f def0
  incus network delete inct$$

  # Standard bridge with random subnet and a bunch of options
  incus network create inct$$
  incus network set inct$$ dns.mode dynamic
  incus network set inct$$ dns.domain blah
  incus network set inct$$ ipv4.routing false
  incus network set inct$$ ipv6.routing false
  incus network set inct$$ ipv6.dhcp.stateful true
  incus network set inct$$ bridge.hwaddr 00:11:22:33:44:55
  [ "$(cat /sys/class/net/inct$$/address)" = "00:11:22:33:44:55" ]

  # validate unset and patch
  [ "$(incus network get inct$$ ipv6.dhcp.stateful)" = "true" ]
  incus network unset inct$$ ipv6.dhcp.stateful
  [ "$(incus network get inct$$ ipv6.dhcp.stateful)" = "" ]
  incus query -X PATCH -d "{\\\"config\\\": {\\\"ipv6.dhcp.stateful\\\": \\\"true\\\"}}" /1.0/networks/inct$$
  [ "$(incus network get inct$$ ipv6.dhcp.stateful)" = "true" ]

  # check ipv4.address and ipv6.address can be unset without triggering random subnet generation.
  incus network unset inct$$ ipv4.address
  ! incus network show inct$$ | grep ipv4.address || false
  incus network unset inct$$ ipv6.address
  ! incus network show inct$$ | grep ipv6.address || false

  # check ipv4.address and ipv6.address can be regenerated on update using "auto" value.
  incus network set inct$$ ipv4.address auto
  incus network show inct$$ | grep ipv4.address
  incus network set inct$$ ipv6.address auto
  incus network show inct$$ | grep ipv6.address

  # delete the network
  incus network delete inct$$

  # edit network description
  incus network create inct$$ --description "Test description"
  incus network list | grep -q 'Test description'
  incus network show inct$$ | grep -q 'description: Test description'
  incus network show inct$$ | sed 's/^description:.*/description: foo/' | incus network edit inct$$
  incus network list | grep -q 'foo'
  incus network show inct$$ | grep -q 'description: foo'
  incus network delete inct$$

  # rename network
  incus network create inct$$
  incus network rename inct$$ newnet$$
  incus network list | grep -qv inct$$  # the old name is gone
  incus network delete newnet$$

  # Unconfigured bridge
  incus network create inct$$ ipv4.address=none ipv6.address=none
  incus network delete inct$$

  # Configured bridge with static assignment
  incus network create inct$$ dns.domain=test dns.mode=managed ipv6.dhcp.stateful=true
  incus network attach inct$$ nettest eth0
  v4_addr="$(incus network get inct$$ ipv4.address | cut -d/ -f1)0"
  v6_addr="$(incus network get inct$$ ipv6.address | cut -d/ -f1)00"
  incus config device set nettest eth0 ipv4.address "${v4_addr}"
  incus config device set nettest eth0 ipv6.address "${v6_addr}"
  grep -q "${v4_addr}.*nettest" "${INCUS_DIR}/networks/inct$$/dnsmasq.hosts/nettest.eth0"
  grep -q "${v6_addr}.*nettest" "${INCUS_DIR}/networks/inct$$/dnsmasq.hosts/nettest.eth0"
  incus start nettest

  incus network list-leases inct$$ | grep STATIC | grep -q "${v4_addr}"
  incus network list-leases inct$$ | grep STATIC | grep -q "${v6_addr}"

  # Request DHCPv6 lease (if udhcpc6 is in busybox image).
  busyboxUdhcpc6=1
  if ! incus exec nettest -- busybox --list | grep udhcpc6 ; then
    busyboxUdhcpc6=0
  fi

  if [ "$busyboxUdhcpc6" = "1" ]; then
    incus exec nettest -- udhcpc6 -f -i eth0 -n -q -t5 2>&1 | grep 'IPv6 obtained'
  fi

  # Check IPAM information
  net_ipv4="$(incus network get inct$$ ipv4.address)"
  net_ipv6="$(incus network get inct$$ ipv6.address)"

  incus network list-allocations | grep -e "${net_ipv4}" -e "${net_ipv6}"
  incus network list-allocations | grep -e "/1.0/networks/inct$$" -e "/1.0/instances/nettest"
  incus network list-allocations | grep -e "${v4_addr}" -e "${v6_addr}"
  incus network list-allocations localhost: | grep -e "${net_ipv4}" -e "${net_ipv6}"
  incus network list-allocations localhost: | grep -e "/1.0/networks/inct$$" -e "/1.0/instances/nettest"
  incus network list-allocations localhost: | grep -e "${v4_addr}" -e "${v6_addr}"

  incus delete nettest -f
  incus network delete inct$$
}
