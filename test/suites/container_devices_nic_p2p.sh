test_container_devices_nic_p2p() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  vethHostName="veth$$"
  ctName="nt$$"
  ctMAC="0a:92:a7:0d:b7:d9"
  ipRand=$(shuf -i 0-9 -n 1)

  # Record how many nics we started with.
  startNicCount=$(find /sys/class/net | wc -l)

  # Test pre-launch profile config is applied at launch.
  incus profile copy default ${ctName}
  incus profile device set ${ctName} eth0 ipv4.routes "192.0.2.1${ipRand}/32"
  incus profile device set ${ctName} eth0 ipv6.routes "2001:db8::1${ipRand}/128"
  incus profile device set ${ctName} eth0 limits.ingress 1Mbit
  incus profile device set ${ctName} eth0 limits.egress 2Mbit
  incus profile device set ${ctName} eth0 host_name "${vethHostName}"
  incus profile device set ${ctName} eth0 mtu "1400"
  incus profile device set ${ctName} eth0 hwaddr "${ctMAC}"
  incus profile device set ${ctName} eth0 nictype "p2p"
  incus launch testimage "${ctName}" -p ${ctName}

  # Check profile routes are applied on boot.
  if ! ip -4 r list dev "${vethHostName}" | grep "192.0.2.1${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${vethHostName}" | grep "2001:db8::1${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi

  # Check profile limits are applied on boot.
  if ! tc class show dev "${vethHostName}" | grep "1Mbit" ; then
    echo "limits.ingress invalid"
    false
  fi
  if ! tc filter show dev "${vethHostName}" egress | grep "2Mbit" ; then
    echo "limits.egress invalid"
    false
  fi

  # Check profile custom MTU is applied in container on boot.
  if ! incus exec "${ctName}" -- grep "1400" /sys/class/net/eth0/mtu ; then
    echo "container veth mtu invalid"
    false
  fi

  # Check profile custom MTU is applied on host-side on boot.
  if !  grep "1400" /sys/class/net/"${vethHostName}"/mtu ; then
    echo "host veth mtu invalid"
    false
  fi

  # Check profile custom MAC is applied in container on boot.
  if ! incus exec "${ctName}" -- grep -Fix "${ctMAC}" /sys/class/net/eth0/address ; then
    echo "mac invalid"
    false
  fi

  # Add IP alias to container and check routes actually work.
  ip -4 addr add 192.0.2.1/32 dev "${vethHostName}"
  incus exec "${ctName}" -- ip -4 addr add "192.0.2.1${ipRand}/32" dev eth0
  incus exec "${ctName}" -- ip -4 route add default dev eth0
  wait_for_dad "${ctName}" eth0
  ping -c2 -W5 "192.0.2.1${ipRand}"
  ip -6 addr add 2001:db8::1/128 dev "${vethHostName}"
  incus exec "${ctName}" -- ip -6 addr add "2001:db8::1${ipRand}/128" dev eth0
  incus exec "${ctName}" -- ip -6 route add default dev eth0
  wait_for_dad "${ctName}" eth0
  ping6 -c2 -W5 "2001:db8::1${ipRand}"

  # Test hot plugging a container nic with different settings to profile with the same name.
  incus config device add "${ctName}" eth0 nic \
    nictype=p2p \
    name=eth0 \
    ipv4.routes="192.0.2.3${ipRand}/32" \
    ipv6.routes="2001:db8::3${ipRand}/128" \
    limits.ingress=3Mbit \
    limits.egress=4Mbit \
    host_name="${vethHostName}p2p" \
    hwaddr="${ctMAC}" \
    mtu=1401

  # Check routes are applied on hot-plug.
  if ! ip -4 r list dev "${vethHostName}p2p" | grep "192.0.2.3${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${vethHostName}p2p" | grep "2001:db8::3${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi

  # Check limits are applied on hot-plug.
  if ! tc class show dev "${vethHostName}p2p" | grep "3Mbit" ; then
    echo "limits.ingress invalid"
    false
  fi
  if ! tc filter show dev "${vethHostName}p2p" egress | grep "4Mbit" ; then
    echo "limits.egress invalid"
    false
  fi

  # Check custom MTU is applied on hot-plug.
  if ! incus exec "${ctName}" -- grep "1401" /sys/class/net/eth0/mtu ; then
    echo "container veth mtu invalid"
    false
  fi

  # Check custom MTU is applied on host-side on hot-plug.
  if !  grep "1401" /sys/class/net/"${vethHostName}p2p"/mtu ; then
    echo "host veth mtu invalid"
    false
  fi

  # Check custom MAC is applied on hot-plug.
  if ! incus exec "${ctName}" -- grep -Fix "${ctMAC}" /sys/class/net/eth0/address ; then
    echo "mac invalid"
    false
  fi

  # Test removing hot plugged device and check profile nic is restored.
  incus config device remove "${ctName}" eth0

  # Check profile routes are applied on hot-removal.
  if ! ip -4 r list dev "${vethHostName}" | grep "192.0.2.1${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${vethHostName}" | grep "2001:db8::1${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! tc class show dev "${vethHostName}" | grep "1Mbit" ; then
    echo "limits.ingress invalid"
    false
  fi

  # Check profile limits are applied on hot-removal.
  if ! tc filter show dev "${vethHostName}" egress | grep "2Mbit" ; then
    echo "limits.egress invalid"
    false
  fi

  # Check profile custom MTU is applied on hot-removal.
  if ! incus exec "${ctName}" -- grep "1400" /sys/class/net/eth0/mtu ; then
    echo "container veth mtu invalid"
    false
  fi

  # Check profile custom MTU is applied on host-side on hot-removal.
  if ! grep "1400" /sys/class/net/"${vethHostName}"/mtu ; then
    echo "host veth mtu invalid"
    false
  fi

  # Test hot plugging a container nic then updating it.
  incus config device add "${ctName}" eth0 nic \
    nictype=p2p \
    name=eth0 \
    host_name="${vethHostName}"

  incus config device set "${ctName}" eth0 ipv4.routes "192.0.2.2${ipRand}/32"
  incus config device set "${ctName}" eth0 ipv6.routes "2001:db8::2${ipRand}/128"
  incus config device set "${ctName}" eth0 limits.ingress 3Mbit
  incus config device set "${ctName}" eth0 limits.egress 4Mbit
  incus config device set "${ctName}" eth0 mtu 1402
  incus config device set "${ctName}" eth0 hwaddr "${ctMAC}"

  # Check routes are applied on update.
  if ! ip -4 r list dev "${vethHostName}" | grep "192.0.2.2${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${vethHostName}" | grep "2001:db8::2${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi

  # Check limits are applied on update.
  if ! tc class show dev "${vethHostName}" | grep "3Mbit" ; then
    echo "limits.ingress invalid"
    false
  fi
  if ! tc filter show dev "${vethHostName}" egress | grep "4Mbit" ; then
    echo "limits.egress invalid"
    false
  fi

  # Check custom MTU is applied update.
  if ! incus exec "${ctName}" -- grep "1402" /sys/class/net/eth0/mtu ; then
    echo "mtu invalid"
    false
  fi

  # Check custom MAC is applied update.
  if ! incus exec "${ctName}" -- grep -Fix "${ctMAC}" /sys/class/net/eth0/address ; then
    echo "mac invalid"
    false
  fi

  # Cleanup.
  incus config device remove "${ctName}" eth0
  incus delete "${ctName}" -f
  incus profile delete "${ctName}"

  # Test adding a p2p device to a running container without host_name and no limits/routes.
  incus launch testimage "${ctName}"
  incus config device add "${ctName}" eth0 nic \
    nictype=p2p

  # Now add some routes
  incus config device set "${ctName}" eth0 ipv4.routes "192.0.2.2${ipRand}/32"
  incus config device set "${ctName}" eth0 ipv6.routes "2001:db8::2${ipRand}/128"

  # Check routes are applied on update. The host name is dynamic, so just check routes exist.
  if ! ip -4 r list | grep "192.0.2.2${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list | grep "2001:db8::2${ipRand}" ; then
    echo "ipv6.routes invalid"
    false
  fi

  # Now update routes, check old routes go and new routes added.
  incus config device set "${ctName}" eth0 ipv4.routes "192.0.2.3${ipRand}/32"
  incus config device set "${ctName}" eth0 ipv6.routes "2001:db8::3${ipRand}/128"

  # Check routes are applied on update. The host name is dynamic, so just check routes exist.
  if ! ip -4 r list | grep "192.0.2.3${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list | grep "2001:db8::3${ipRand}" ; then
    echo "ipv6.routes invalid"
    false
  fi

  # Check old routes removed
  if ip -4 r list | grep "192.0.2.2${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ip -6 r list | grep "2001:db8::2${ipRand}" ; then
    echo "ipv6.routes invalid"
    false
  fi

  # Now remove device, check routes go
  incus config device remove "${ctName}" eth0

  if ip -4 r list | grep "192.0.2.3${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ip -6 r list | grep "2001:db8::3${ipRand}" ; then
    echo "ipv6.routes invalid"
    false
  fi

  # Check volatile cleanup on stop.
  incus stop -f "${ctName}"
  if incus config show "${ctName}" | grep volatile.eth0 | grep -v volatile.eth0.hwaddr ; then
    echo "unexpected volatile key remains"
    false
  fi

  # Now add a nic to a stopped container with routes.
  incus config device add "${ctName}" eth0 nic \
    nictype=p2p \
    ipv4.routes="192.0.2.2${ipRand}/32" \
    ipv6.routes="2001:db8::2${ipRand}/128"

  incus start "${ctName}"

  # Check routes are applied on start. The host name is dynamic, so just check routes exist.
  if ! ip -4 r list | grep "192.0.2.2${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list | grep "2001:db8::2${ipRand}" ; then
    echo "ipv6.routes invalid"
    false
  fi

  # Now update routes on boot time nic, check old routes go and new routes added.
  incus config device set "${ctName}" eth0 ipv4.routes "192.0.2.3${ipRand}/32"
  incus config device set "${ctName}" eth0 ipv6.routes "2001:db8::3${ipRand}/128"

  # Check routes are applied on update. The host name is dynamic, so just check routes exist.
  if ! ip -4 r list | grep "192.0.2.3${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list | grep "2001:db8::3${ipRand}" ; then
    echo "ipv6.routes invalid"
    false
  fi

  # Check old routes removed
  if ip -4 r list | grep "192.0.2.2${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ip -6 r list | grep "2001:db8::2${ipRand}" ; then
    echo "ipv6.routes invalid"
    false
  fi

  # Now remove boot time device
  incus config device remove "${ctName}" eth0

  # Check old routes removed
  if ip -4 r list | grep "192.0.2.3${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ip -6 r list | grep "2001:db8::3${ipRand}" ; then
    echo "ipv6.routes invalid"
    false
  fi

  # Add hot plug device with routes.
  incus config device add "${ctName}" eth0 nic \
    nictype=p2p

  # Now update routes on hotplug nic
  incus config device set "${ctName}" eth0 ipv4.routes "192.0.2.2${ipRand}/32"
  incus config device set "${ctName}" eth0 ipv6.routes "2001:db8::2${ipRand}/128"

  # Check routes are applied. The host name is dynamic, so just check routes exist.
  if ! ip -4 r list | grep "192.0.2.2${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list | grep "2001:db8::2${ipRand}" ; then
    echo "ipv6.routes invalid"
    false
  fi

  # Now remove hotplug device
  incus config device remove "${ctName}" eth0

  # Check old routes removed
  if ip -4 r list | grep "192.0.2.2${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ip -6 r list | grep "2001:db8::2${ipRand}" ; then
    echo "ipv6.routes invalid"
    false
  fi

  # Test hotplugging nic with new name (rather than updating existing nic).
  incus config device add "${ctName}" eth1 nic nictype=p2p

  incus stop -f "${ctName}"

  # Check we haven't left any NICS lying around.
  endNicCount=$(find /sys/class/net | wc -l)
  if [ "$startNicCount" != "$endNicCount" ]; then
    echo "leftover NICS detected"
    false
  fi

  incus delete "${ctName}" -f
}
