test_container_devices_nic_routed() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  if ! inc info | grep 'network_veth_router: "true"' ; then
    echo "==> SKIP: No veth router support"
    return
  fi

  ctName="nt$$"
  ipRand=$(shuf -i 0-9 -n 1)

  # These special values are needed to be enabled in kernel.
  # No need to enable IPv4 forwarding, as Incus will do this on the veth host_name interface automatically.
  sysctl net.ipv6.conf.all.forwarding=1
  sysctl net.ipv6.conf.all.proxy_ndp=1

  # Standard bridge.
  inc network create "${ctName}" \
        ipv4.address=192.0.2.1/24 \
        ipv6.address=2001:db8::1/64
  sysctl net.ipv6.conf."${ctName}".proxy_ndp=1
  sysctl net.ipv6.conf."${ctName}".forwarding=1
  sysctl net.ipv4.conf."${ctName}".forwarding=1

  # Wait for IPv6 DAD to complete.
  wait_for_dad "${ctName}"

  # Create container connected to bridge (which will be used for neighbor probe testing).
  inc init testimage "${ctName}neigh"
  inc config device add "${ctName}neigh" eth0 nic network="${ctName}"
  inc start "${ctName}neigh"
  inc exec "${ctName}neigh" -- ip -4 addr add 192.0.2.254/24 dev eth0
  inc exec "${ctName}neigh" -- ip -4 route replace default via 192.0.2.1 dev eth0
  inc exec "${ctName}neigh" -- ip -6 addr add 2001:db8::FFFF/64 dev eth0
  inc exec "${ctName}neigh" -- ip -6 route replace default via 2001:db8::1 dev eth0

  # Wait for IPv6 DAD to complete.
  wait_for_dad "${ctName}neigh" eth0

  ping -c2 -W5 192.0.2.254
  ping6 -c2 -W5 "2001:db8::FFFF"

  # Create dummy vlan parent.
  # Use slash notation when setting sysctls on vlan interface (that has period in interface name).
  ip link add link "${ctName}" name "${ctName}.1234" type vlan id 1234
  sysctl net/ipv6/conf/"${ctName}.1234"/proxy_ndp=1
  sysctl net/ipv6/conf/"${ctName}.1234"/forwarding=1
  sysctl net/ipv4/conf/"${ctName}.1234"/forwarding=1

  # Add IP addresses to parent vlan interface (this is needed for automatic gateway detection in container).
  ip link set "${ctName}.1234" up
  ip addr add 192.0.3.254/32 dev "${ctName}.1234"
  ip addr add 2001:db8:2::1/128 dev "${ctName}.1234"

  # Record how many nics we started with.
  startNicCount=$(find /sys/class/net | wc -l)

  inc init testimage "${ctName}"

  # Check vlan option not allowed without parent option.
  ! inc config device add "${ctName}" eth0 nic \
    name=eth0 \
    nictype=routed \
    vlan=1234 || false

  # Check VLAN parent interface creation and teardown.
  inc config device add "${ctName}" eth0 nic \
    name=eth0 \
    nictype=routed \
    parent=${ctName} \
    vlan=1235
  inc start "${ctName}"
  stat "/sys/class/net/${ctName}.1235"
  inc stop -f "${ctName}"
  ! stat "/sys/class/net/${ctName}.1235" || false
  inc config device remove "${ctName}" eth0

  # Add routed NIC to instance.
  inc config device add "${ctName}" eth0 nic \
    name=eth0 \
    nictype=routed \
    parent=${ctName}

  # Check starting routed NIC with IPs in use on parent network is prevented.
  inc config device set "${ctName}" eth0 ipv4.address="192.0.2.254"
  ! inc start "${ctName}" || false
  inc config device set "${ctName}" eth0 ipv4.neighbor_probe=false
  inc start "${ctName}"
  inc stop "${ctName}" -f

  inc config device set "${ctName}" eth0 ipv4.address="" ipv6.address="2001:db8::FFFF"

  ! inc start "${ctName}" || false
  inc config device set "${ctName}" eth0 ipv6.neighbor_probe=false
  inc start "${ctName}"
  inc stop "${ctName}" -f
  inc config device unset "${ctName}" eth0 ipv4.neighbor_probe
  inc config device unset "${ctName}" eth0 ipv6.neighbor_probe

  # Check starting routed NIC with unused IPs.
  inc config device set "${ctName}" eth0 \
    ipv4.address="192.0.2.1${ipRand}" \
    ipv6.address="2001:db8::1${ipRand}" \
    ipv4.routes="192.0.3.0/24" \
    ipv6.routes="2001:db7::/64" \
    mtu=1600
  inc start "${ctName}"

  ctHost=$(inc config get "${ctName}" volatile.eth0.host_name)
  # Check profile routes are applied
  if ! ip -4 r list dev "${ctHost}"| grep "192.0.3.0/24" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${ctHost}" | grep "2001:db7::/64" ; then
    echo "ipv6.routes invalid"
    false
  fi

  # Check IP is assigned and doesn't have a broadcast address set.
  inc exec "${ctName}" -- ip a | grep "inet 192.0.2.1${ipRand}/32 scope global eth0"

  # Check neighbour proxy entries added to parent interface.
  ip neigh show proxy dev "${ctName}" | grep "192.0.2.1${ipRand}"
  ip neigh show proxy dev "${ctName}" | grep "2001:db8::1${ipRand}"

  # Check custom MTU is applied.
  if ! inc exec "${ctName}" -- ip link show eth0 | grep "mtu 1600" ; then
    echo "mtu invalid"
    false
  fi

  # Check MAC address is applied.
  ctMAC=$(inc config get "${ctName}" volatile.eth0.hwaddr)
  if ! inc exec "${ctName}" -- grep -Fix "${ctMAC}" /sys/class/net/eth0/address ; then
    echo "mac invalid"
    false
  fi

  inc stop "${ctName}" --force

  # Check neighbour proxy entries removed from parent interface.
  ! ip neigh show proxy dev "${ctName}" | grep "192.0.2.1${ipRand}" || false
  ! ip neigh show proxy dev "${ctName}" | grep "2001:db8::1${ipRand}" || false

  # Check that MTU is inherited from parent device when not specified on device.
  ip link set "${ctName}" mtu 1605
  inc config device unset "${ctName}" eth0 mtu
  inc start "${ctName}"

  if ! inc exec "${ctName}" -- grep "1605" /sys/class/net/eth0/mtu ; then
    echo "mtu not inherited from parent"
    false
  fi

  #Spin up another container with multiple IPv4 addresses (no IPv6 to check single family operation).
  inc init testimage "${ctName}2"
  inc config device add "${ctName}2" eth0 nic \
    name=eth0 \
    nictype=routed \
    parent=${ctName} \
    ipv4.address="192.0.2.2${ipRand}, 192.0.2.3${ipRand}"
  inc start "${ctName}2"
  inc exec "${ctName}2" -- ip -4 r | grep "169.254.0.1"
  ! inc exec "${ctName}2" -- ip -6 r | grep "fe80::1" || false
  inc stop -f "${ctName}2"

  # Check single IPv6 family auto default gateway works.
  inc config device unset "${ctName}2" eth0 ipv4.address
  inc config device set "${ctName}2" eth0 ipv6.address="2001:db8::2${ipRand}, 2001:db8::3${ipRand}"
  inc start "${ctName}2"
  ! inc exec "${ctName}2" -- ip r | grep "169.254.0.1" || false
  inc exec "${ctName}2" -- ip -6 r | grep "fe80::1"
  inc stop -f "${ctName}2"

  # Enable both IP families.
  inc config device set "${ctName}2" eth0 ipv4.address="192.0.2.2${ipRand}, 192.0.2.3${ipRand}"
  inc start "${ctName}2"

  # Wait for IPv6 DAD to complete.
  wait_for_dad "${ctName}" eth0
  wait_for_dad "${ctName}2" eth0

  # Check comms between containers.
  inc exec "${ctName}" -- ping -c2 -W5 "192.0.2.1"
  inc exec "${ctName}" -- ping6 -c2 -W5 "2001:db8::1"

  inc exec "${ctName}2" -- ping -c2 -W5 "192.0.2.1"
  inc exec "${ctName}2" -- ping6 -c2 -W5 "2001:db8::1"

  inc exec "${ctName}" -- ping -c2 -W5 "192.0.2.2${ipRand}"
  inc exec "${ctName}" -- ping -c2 -W5 "192.0.2.3${ipRand}"

  inc exec "${ctName}" -- ping6 -c3 -W5 "2001:db8::3${ipRand}"
  inc exec "${ctName}" -- ping6 -c2 -W5 "2001:db8::2${ipRand}"

  inc exec "${ctName}2" -- ping -c2 -W5 "192.0.2.1${ipRand}"
  inc exec "${ctName}2" -- ping6 -c2 -W5 "2001:db8::1${ipRand}"

  inc stop -f "${ctName}2"
  inc stop -f "${ctName}"

  # Check routed ontop of VLAN parent with custom routing tables.
  inc config device set "${ctName}" eth0 vlan 1234
  inc config device set "${ctName}" eth0 ipv4.host_table=100
  inc config device set "${ctName}" eth0 ipv6.host_table=101
  inc start "${ctName}"

  # Check VLAN interface created
  if ! grep "1" "/sys/class/net/${ctName}.1234/carrier" ; then
    echo "vlan interface not created"
    false
  fi

  # Check static routes added to custom routing table
  ip -4 route show table 100 | grep "192.0.2.1${ipRand}"
  ip -6 route show table 101 | grep "2001:db8::1${ipRand}"

  # Check volatile cleanup on stop.
  inc stop -f "${ctName}"
  if inc config show "${ctName}" | grep volatile.eth0 | grep -v volatile.eth0.hwaddr | grep -v volatile.eth0.name ; then
    echo "unexpected volatile key remains"
    false
  fi

  # Check parent device is still up.
  if ! grep "1" "/sys/class/net/${ctName}/carrier" ; then
    echo "parent is down"
    false
  fi

  # Check we haven't left any NICS lying around.
  endNicCount=$(find /sys/class/net | wc -l)
  if [ "$startNicCount" != "$endNicCount" ]; then
    echo "leftover NICS detected"
    false
  fi

  # Cleanup routed checks
  inc delete "${ctName}" -f
  inc delete "${ctName}2" -f
  inc delete "${ctName}neigh" -f
  ip link delete "${ctName}.1234"
  inc network show "${ctName}"
  inc network delete "${ctName}"
}
