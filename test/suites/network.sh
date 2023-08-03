test_network() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  inc init testimage nettest

  # Standard bridge with random subnet and a bunch of options
  inc network create inct$$
  inc network set inct$$ dns.mode dynamic
  inc network set inct$$ dns.domain blah
  inc network set inct$$ ipv4.routing false
  inc network set inct$$ ipv6.routing false
  inc network set inct$$ ipv6.dhcp.stateful true
  inc network set inct$$ bridge.hwaddr 00:11:22:33:44:55
  [ "$(cat /sys/class/net/inct$$/address)" = "00:11:22:33:44:55" ]

  # validate unset and patch
  [ "$(inc network get inct$$ ipv6.dhcp.stateful)" = "true" ]
  inc network unset inct$$ ipv6.dhcp.stateful
  [ "$(inc network get inct$$ ipv6.dhcp.stateful)" = "" ]
  inc query -X PATCH -d "{\\\"config\\\": {\\\"ipv6.dhcp.stateful\\\": \\\"true\\\"}}" /1.0/networks/inct$$
  [ "$(inc network get inct$$ ipv6.dhcp.stateful)" = "true" ]

  # check ipv4.address and ipv6.address can be unset without triggering random subnet generation.
  inc network unset inct$$ ipv4.address
  ! inc network show inct$$ | grep ipv4.address || false
  inc network unset inct$$ ipv6.address
  ! inc network show inct$$ | grep ipv6.address || false

  # check ipv4.address and ipv6.address can be regenerated on update using "auto" value.
  inc network set inct$$ ipv4.address auto
  inc network show inct$$ | grep ipv4.address
  inc network set inct$$ ipv6.address auto
  inc network show inct$$ | grep ipv6.address

  # delete the network
  inc network delete inct$$

  # edit network description
  inc network create inct$$
  inc network show inct$$ | sed 's/^description:.*/description: foo/' | inc network edit inct$$
  inc network show inct$$ | grep -q 'description: foo'
  inc network delete inct$$

  # rename network
  inc network create inct$$
  inc network rename inct$$ newnet$$
  inc network list | grep -qv inct$$  # the old name is gone
  inc network delete newnet$$

  # Unconfigured bridge
  inc network create inct$$ ipv4.address=none ipv6.address=none
  inc network delete inct$$

  # Configured bridge with static assignment
  inc network create inct$$ dns.domain=test dns.mode=managed ipv6.dhcp.stateful=true
  inc network attach inct$$ nettest eth0
  v4_addr="$(inc network get inct$$ ipv4.address | cut -d/ -f1)0"
  v6_addr="$(inc network get inct$$ ipv6.address | cut -d/ -f1)00"
  inc config device set nettest eth0 ipv4.address "${v4_addr}"
  inc config device set nettest eth0 ipv6.address "${v6_addr}"
  grep -q "${v4_addr}.*nettest" "${INCUS_DIR}/networks/inct$$/dnsmasq.hosts/nettest.eth0"
  grep -q "${v6_addr}.*nettest" "${INCUS_DIR}/networks/inct$$/dnsmasq.hosts/nettest.eth0"
  inc start nettest

  inc network list-leases inct$$ | grep STATIC | grep -q "${v4_addr}"
  inc network list-leases inct$$ | grep STATIC | grep -q "${v6_addr}"

  # Request DHCPv6 lease (if udhcpc6 is in busybox image).
  busyboxUdhcpc6=1
  if ! inc exec nettest -- busybox --list | grep udhcpc6 ; then
    busyboxUdhcpc6=0
  fi

  if [ "$busyboxUdhcpc6" = "1" ]; then
    inc exec nettest -- udhcpc6 -f -i eth0 -n -q -t5 2>&1 | grep 'IPv6 obtained'
  fi

  # Check IPAM information
  net_ipv4="$(inc network get inct$$ ipv4.address)"
  net_ipv6="$(inc network get inct$$ ipv6.address)"

  inc network list-allocations | grep -e "${net_ipv4}" -e "${net_ipv6}"
  inc network list-allocations | grep -e "/1.0/networks/inct$$" -e "/1.0/instances/nettest"
  inc network list-allocations | grep -e "${v4_addr}" -e "${v6_addr}"

  inc delete nettest -f
  inc network delete inct$$
}
