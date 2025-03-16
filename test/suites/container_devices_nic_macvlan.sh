test_container_devices_nic_macvlan() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  ctName="nt$$"
  ipRand=$(shuf -i 0-9 -n 1)

  # Create dummy interface for use as parent.
  ip link add "${ctName}" type dummy
  ip link set "${ctName}" up

  # Record how many nics we started with.
  startNicCount=$(find /sys/class/net | wc -l)

  # Test pre-launch profile config is applied at launch.
  incus profile copy default "${ctName}"

  # Modify profile nictype and parent in atomic operation to ensure validation passes.
  incus profile show "${ctName}" | sed  "s/nictype: p2p/nictype: macvlan\\n    parent: ${ctName}/" | incus profile edit "${ctName}"
  incus profile device set "${ctName}" eth0 mtu "1400"

  incus launch testimage "${ctName}" -p "${ctName}"
  incus exec "${ctName}" -- ip addr add "192.0.2.1${ipRand}/24" dev eth0
  incus exec "${ctName}" -- ip addr add "2001:db8::1${ipRand}/64" dev eth0

  # Check custom MTU is applied if feature available in Incus.
  if incus info | grep 'network_phys_macvlan_mtu: "true"' ; then
    if ! incus exec "${ctName}" -- ip link show eth0 | grep "mtu 1400" ; then
      echo "mtu invalid"
      false
    fi
  fi

  #Spin up another container with multiple IPs.
  incus launch testimage "${ctName}2" -p "${ctName}"
  incus exec "${ctName}2" -- ip addr add "192.0.2.2${ipRand}/24" dev eth0
  incus exec "${ctName}2" -- ip addr add "2001:db8::2${ipRand}/64" dev eth0

  # Check comms between containers.
  incus exec "${ctName}" -- ping -c2 -W5 "192.0.2.2${ipRand}"
  incus exec "${ctName}" -- ping6 -c2 -W5 "2001:db8::2${ipRand}"
  incus exec "${ctName}2" -- ping -c2 -W5 "192.0.2.1${ipRand}"
  incus exec "${ctName}2" -- ping6 -c2 -W5 "2001:db8::1${ipRand}"

  # Test hot plugging a container nic with different settings to profile with the same name.
  incus config device add "${ctName}" eth0 nic \
    nictype=macvlan \
    name=eth0 \
    parent="${ctName}" \
    mtu=1401

  # Check custom MTU is applied on hot-plug.
  if ! incus exec "${ctName}" -- ip link show eth0 | grep "mtu 1401" ; then
    echo "mtu invalid"
    false
  fi

  # Check that MTU is inherited from parent device when not specified on device.
  ip link set "${ctName}" mtu 1405
  incus config device unset "${ctName}" eth0 mtu
  if ! incus exec "${ctName}" -- grep "1405" /sys/class/net/eth0/mtu ; then
    echo "mtu not inherited from parent"
    false
  fi

  # Check volatile cleanup on stop.
  incus stop -f "${ctName}"
  if incus config show "${ctName}" | grep volatile.eth0 | grep -v volatile.eth0.hwaddr ; then
    echo "unexpected volatile key remains"
    false
  fi

  incus start "${ctName}"
  incus config device remove "${ctName}" eth0

  # Test hot plugging macvlan device based on vlan parent.
  incus config device add "${ctName}" eth0 nic \
    nictype=macvlan \
    parent="${ctName}" \
    name=eth0 \
    vlan=10 \
    mtu=1402

  # Check custom MTU is applied.
  if ! incus exec "${ctName}" -- ip link show eth0 | grep "mtu 1402" ; then
    echo "mtu invalid"
    false
  fi

  # Check VLAN interface created
  if ! grep "1" "/sys/class/net/${ctName}.10/carrier" ; then
    echo "vlan interface not created"
    false
  fi

  # Remove device from container, this should also remove created VLAN parent device.
  incus config device remove "${ctName}" eth0

  # Check parent device is still up.
  if ! grep "1" "/sys/class/net/${ctName}/carrier" ; then
    echo "parent is down"
    false
  fi

  # Test using macvlan network.

  # Create macvlan network and add NIC device using that network.
  incus network create "${ctName}net" --type=macvlan parent="${ctName}"
  incus config device add "${ctName}" eth0 nic \
    network="${ctName}net" \
    name=eth0
  incus exec "${ctName}" -- ip addr add "192.0.2.1${ipRand}/24" dev eth0
  incus exec "${ctName}" -- ip addr add "2001:db8::1${ipRand}/64" dev eth0
  incus exec "${ctName}" -- ip link set eth0 up
  incus exec "${ctName}" -- ping -c2 -W5 "192.0.2.2${ipRand}"
  incus exec "${ctName}" -- ping6 -c2 -W5 "2001:db8::2${ipRand}"
  incus config device remove "${ctName}" eth0
  incus network delete "${ctName}net"

  # Check we haven't left any NICS lying around.
  endNicCount=$(find /sys/class/net | wc -l)
  if [ "$startNicCount" != "$endNicCount" ]; then
    echo "leftover NICS detected"
    false
  fi

  # Cleanup.
  incus delete "${ctName}" -f
  incus delete "${ctName}2" -f
  incus profile delete "${ctName}"
  ip link delete "${ctName}" type dummy
}
