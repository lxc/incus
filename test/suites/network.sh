test_network() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  lxc init testimage nettest

  # Standard bridge with random subnet and a bunch of options
  lxc network create inct$$
  lxc network set inct$$ dns.mode dynamic
  lxc network set inct$$ dns.domain blah
  lxc network set inct$$ ipv4.routing false
  lxc network set inct$$ ipv6.routing false
  lxc network set inct$$ ipv6.dhcp.stateful true
  lxc network set inct$$ bridge.hwaddr 00:11:22:33:44:55
  [ "$(cat /sys/class/net/inct$$/address)" = "00:11:22:33:44:55" ]

  # validate unset and patch
  [ "$(lxc network get inct$$ ipv6.dhcp.stateful)" = "true" ]
  lxc network unset inct$$ ipv6.dhcp.stateful
  [ "$(lxc network get inct$$ ipv6.dhcp.stateful)" = "" ]
  lxc query -X PATCH -d "{\\\"config\\\": {\\\"ipv6.dhcp.stateful\\\": \\\"true\\\"}}" /1.0/networks/inct$$
  [ "$(lxc network get inct$$ ipv6.dhcp.stateful)" = "true" ]

  # check ipv4.address and ipv6.address can be unset without triggering random subnet generation.
  lxc network unset inct$$ ipv4.address
  ! lxc network show inct$$ | grep ipv4.address || false
  lxc network unset inct$$ ipv6.address
  ! lxc network show inct$$ | grep ipv6.address || false

  # check ipv4.address and ipv6.address can be regenerated on update using "auto" value.
  lxc network set inct$$ ipv4.address auto
  lxc network show inct$$ | grep ipv4.address
  lxc network set inct$$ ipv6.address auto
  lxc network show inct$$ | grep ipv6.address

  # delete the network
  lxc network delete inct$$

  # edit network description
  lxc network create inct$$
  lxc network show inct$$ | sed 's/^description:.*/description: foo/' | lxc network edit inct$$
  lxc network show inct$$ | grep -q 'description: foo'
  lxc network delete inct$$

  # rename network
  lxc network create inct$$
  lxc network rename inct$$ newnet$$
  lxc network list | grep -qv inct$$  # the old name is gone
  lxc network delete newnet$$

  # Unconfigured bridge
  lxc network create inct$$ ipv4.address=none ipv6.address=none
  lxc network delete inct$$

  # Configured bridge with static assignment
  lxc network create inct$$ dns.domain=test dns.mode=managed ipv6.dhcp.stateful=true
  lxc network attach inct$$ nettest eth0
  v4_addr="$(lxc network get inct$$ ipv4.address | cut -d/ -f1)0"
  v6_addr="$(lxc network get inct$$ ipv6.address | cut -d/ -f1)00"
  lxc config device set nettest eth0 ipv4.address "${v4_addr}"
  lxc config device set nettest eth0 ipv6.address "${v6_addr}"
  grep -q "${v4_addr}.*nettest" "${INCUS_DIR}/networks/inct$$/dnsmasq.hosts/nettest.eth0"
  grep -q "${v6_addr}.*nettest" "${INCUS_DIR}/networks/inct$$/dnsmasq.hosts/nettest.eth0"
  lxc start nettest

  lxc network list-leases inct$$ | grep STATIC | grep -q "${v4_addr}"
  lxc network list-leases inct$$ | grep STATIC | grep -q "${v6_addr}"

  # Request DHCPv6 lease (if udhcpc6 is in busybox image).
  busyboxUdhcpc6=1
  if ! lxc exec nettest -- busybox --list | grep udhcpc6 ; then
    busyboxUdhcpc6=0
  fi

  if [ "$busyboxUdhcpc6" = "1" ]; then
    lxc exec nettest -- udhcpc6 -f -i eth0 -n -q -t5 2>&1 | grep 'IPv6 obtained'
  fi

  # Check IPAM information
  net_ipv4="$(lxc network get inct$$ ipv4.address)"
  net_ipv6="$(lxc network get inct$$ ipv6.address)"

  lxc network list-allocations | grep -e "${net_ipv4}" -e "${net_ipv6}"
  lxc network list-allocations | grep -e "/1.0/networks/inct$$" -e "/1.0/instances/nettest"
  lxc network list-allocations | grep -e "${v4_addr}" -e "${v6_addr}"

  lxc delete nettest -f
  lxc network delete inct$$
}
