test_container_devices_nic_ipvlan() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  if ! incus info | grep 'network_ipvlan: "true"' ; then
    echo "==> SKIP: No IPVLAN support"
    return
  fi

  ctName="nt$$"
  ipRand=$(shuf -i 0-9 -n 1)

  # Test ipvlan support to offline container (hot plugging not supported).
  ip link add "${ctName}" type dummy

  # Record how many nics we started with.
  startNicCount=$(find /sys/class/net | wc -l)

  # Check that starting IPVLAN container.
  sysctl net.ipv6.conf."${ctName}".proxy_ndp=1
  sysctl net.ipv6.conf."${ctName}".forwarding=1
  sysctl net.ipv4.conf."${ctName}".forwarding=1
  incus init testimage "${ctName}"
  incus config device add "${ctName}" eth0 nic \
    nictype=ipvlan \
    parent=${ctName} \
    ipv4.address="192.0.2.1${ipRand}" \
    ipv6.address="2001:db8::1${ipRand}" \
    ipv4.gateway=auto \
    ipv6.gateway=auto \
    mtu=1400
  incus start "${ctName}"

  # Check custom MTU is applied.
  if ! incus exec "${ctName}" -- ip link show eth0 | grep "mtu 1400" ; then
    echo "mtu invalid"
    false
  fi

  incus stop "${ctName}" --force

  # Check that MTU is inherited from parent device when not specified on device.
  ip link set "${ctName}" mtu 1405
  incus config device unset "${ctName}" eth0 mtu
  incus start "${ctName}"
  if ! incus exec "${ctName}" -- grep "1405" /sys/class/net/eth0/mtu ; then
    echo "mtu not inherited from parent"
    false
  fi

  #Spin up another container with multiple IPs.
  incus init testimage "${ctName}2"
  incus config device add "${ctName}2" eth0 nic \
    nictype=ipvlan \
    parent=${ctName} \
    ipv4.address="192.0.2.2${ipRand}, 192.0.2.3${ipRand}" \
    ipv6.address="2001:db8::2${ipRand}, 2001:db8::3${ipRand}"
  incus start "${ctName}2"

  # Check comms between containers.
  incus exec "${ctName}" -- ping -c2 -W5 "192.0.2.2${ipRand}"
  incus exec "${ctName}" -- ping -c2 -W5 "192.0.2.3${ipRand}"
  incus exec "${ctName}" -- ping6 -c2 -W5 "2001:db8::2${ipRand}"
  incus exec "${ctName}" -- ping6 -c2 -W5 "2001:db8::3${ipRand}"
  incus exec "${ctName}2" -- ping -c2 -W5 "192.0.2.1${ipRand}"
  incus exec "${ctName}2" -- ping6 -c2 -W5 "2001:db8::1${ipRand}"
  incus stop -f "${ctName}2"

  # Check IPVLAN ontop of VLAN parent with custom routing tables.
  incus stop -f "${ctName}"
  incus config device set "${ctName}" eth0 vlan 1234
  incus config device set "${ctName}" eth0 ipv4.host_table=100
  incus config device set "${ctName}" eth0 ipv6.host_table=101

  # Check gateway settings don't accept IPs in default l3s mode.
  ! incus config device set "${ctName}" eth0 ipv4.gateway=192.0.2.254
  ! incus config device set "${ctName}" eth0 ipv6.gateway=2001:db8::FFFF

  incus start "${ctName}"

  # Check VLAN interface created
  if ! grep "1" "/sys/class/net/${ctName}.1234/carrier" ; then
    echo "vlan interface not created"
    false
  fi

  # Check static routes added to custom routing table
  ip -4 route show table 100 | grep "192.0.2.1${ipRand}"
  ip -6 route show table 101 | grep "2001:db8::1${ipRand}"

  # Check volatile cleanup on stop.
  incus stop -f "${ctName}"
  if incus config show "${ctName}" | grep volatile.eth0 | grep -v volatile.eth0.hwaddr | grep -v volatile.eth0.name ; then
    echo "unexpected volatile key remains"
    false
  fi

  # Check parent device is still up.
  if ! grep "1" "/sys/class/net/${ctName}/carrier" ; then
    echo "parent is down"
    false
  fi

  # Check static routes are removed from custom routing table
  ! ip -4 route show table 100 | grep "192.0.2.1${ipRand}"
  ! ip -6 route show table 101 | grep "2001:db8::1${ipRand}"

  # Check ipvlan l2 mode with mixture of singular and CIDR IPs, and gateway IPs.
  incus config device remove "${ctName}" eth0
  incus config device add "${ctName}" eth0 nic \
    nictype=ipvlan \
    mode=l2 \
    parent=${ctName} \
    ipv4.address="192.0.2.1${ipRand},192.0.2.2${ipRand}/32" \
    ipv6.address="2001:db8::1${ipRand},2001:db8::2${ipRand}/128" \
    ipv4.gateway=192.0.2.254 \
    ipv6.gateway=2001:db8::FFFF \
    mtu=1400
  incus start "${ctName}"

  incus config device remove "${ctName}2" eth0
  incus config device add "${ctName}2" eth0 nic \
    nictype=ipvlan \
    parent=${ctName} \
    ipv4.address="192.0.2.3${ipRand}" \
    ipv6.address="2001:db8::3${ipRand}" \
    mtu=1400
  incus start "${ctName}2"

  # Add an internally configured address (only possible in l2 mode).
  incus exec "${ctName}2" -- ip -4 addr add "192.0.2.4${ipRand}/32" dev eth0
  incus exec "${ctName}2" -- ip -6 addr add "2001:db8::4${ipRand}/128" dev eth0
  wait_for_dad "${ctName}2" eth0

  # Check comms between containers.
  incus exec "${ctName}" -- ping -c2 -W5 "192.0.2.3${ipRand}"
  incus exec "${ctName}" -- ping -c2 -W5 "192.0.2.4${ipRand}"
  incus exec "${ctName}" -- ping6 -c2 -W5 "2001:db8::3${ipRand}"
  incus exec "${ctName}" -- ping6 -c2 -W5 "2001:db8::4${ipRand}"
  incus exec "${ctName}2" -- ping -c2 -W5 "192.0.2.1${ipRand}"
  incus exec "${ctName}2" -- ping -c2 -W5 "192.0.2.2${ipRand}"
  incus exec "${ctName}2" -- ping6 -c2 -W5 "2001:db8::1${ipRand}"
  incus exec "${ctName}2" -- ping6 -c2 -W5 "2001:db8::2${ipRand}"

  incus stop -f "${ctName}"
  incus stop -f "${ctName}2"

  # Check we haven't left any NICS lying around.
  endNicCount=$(find /sys/class/net | wc -l)
  if [ "$startNicCount" != "$endNicCount" ]; then
    echo "leftover NICS detected"
    false
  fi

  # Cleanup ipvlan checks
  incus delete "${ctName}" -f
  incus delete "${ctName}2" -f
  ip link delete "${ctName}" type dummy
}
