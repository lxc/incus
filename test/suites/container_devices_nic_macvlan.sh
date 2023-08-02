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
  inc profile copy default "${ctName}"

  # Modifiy profile nictype and parent in atomic operation to ensure validation passes.
  inc profile show "${ctName}" | sed  "s/nictype: p2p/nictype: macvlan\\n    parent: ${ctName}/" | inc profile edit "${ctName}"
  inc profile device set "${ctName}" eth0 mtu "1400"

  inc launch testimage "${ctName}" -p "${ctName}"
  inc exec "${ctName}" -- ip addr add "192.0.2.1${ipRand}/24" dev eth0
  inc exec "${ctName}" -- ip addr add "2001:db8::1${ipRand}/64" dev eth0

  # Check custom MTU is applied if feature available in Incus.
  if inc info | grep 'network_phys_macvlan_mtu: "true"' ; then
    if ! inc exec "${ctName}" -- ip link show eth0 | grep "mtu 1400" ; then
      echo "mtu invalid"
      false
    fi
  fi

  #Spin up another container with multiple IPs.
  inc launch testimage "${ctName}2" -p "${ctName}"
  inc exec "${ctName}2" -- ip addr add "192.0.2.2${ipRand}/24" dev eth0
  inc exec "${ctName}2" -- ip addr add "2001:db8::2${ipRand}/64" dev eth0

  # Check comms between containers.
  inc exec "${ctName}" -- ping -c2 -W5 "192.0.2.2${ipRand}"
  inc exec "${ctName}" -- ping6 -c2 -W5 "2001:db8::2${ipRand}"
  inc exec "${ctName}2" -- ping -c2 -W5 "192.0.2.1${ipRand}"
  inc exec "${ctName}2" -- ping6 -c2 -W5 "2001:db8::1${ipRand}"

  # Test hot plugging a container nic with different settings to profile with the same name.
  inc config device add "${ctName}" eth0 nic \
    nictype=macvlan \
    name=eth0 \
    parent="${ctName}" \
    mtu=1401

  # Check custom MTU is applied on hot-plug.
  if ! inc exec "${ctName}" -- ip link show eth0 | grep "mtu 1401" ; then
    echo "mtu invalid"
    false
  fi

  # Check that MTU is inherited from parent device when not specified on device.
  ip link set "${ctName}" mtu 1405
  inc config device unset "${ctName}" eth0 mtu
  if ! inc exec "${ctName}" -- grep "1405" /sys/class/net/eth0/mtu ; then
    echo "mtu not inherited from parent"
    false
  fi

  # Check volatile cleanup on stop.
  inc stop -f "${ctName}"
  if inc config show "${ctName}" | grep volatile.eth0 | grep -v volatile.eth0.hwaddr ; then
    echo "unexpected volatile key remains"
    false
  fi

  inc start "${ctName}"
  inc config device remove "${ctName}" eth0

  # Test hot plugging macvlan device based on vlan parent.
  inc config device add "${ctName}" eth0 nic \
    nictype=macvlan \
    parent="${ctName}" \
    name=eth0 \
    vlan=10 \
    mtu=1402

  # Check custom MTU is applied.
  if ! inc exec "${ctName}" -- ip link show eth0 | grep "mtu 1402" ; then
    echo "mtu invalid"
    false
  fi

  # Check VLAN interface created
  if ! grep "1" "/sys/class/net/${ctName}.10/carrier" ; then
    echo "vlan interface not created"
    false
  fi

  # Remove device from container, this should also remove created VLAN parent device.
  inc config device remove "${ctName}" eth0

  # Check parent device is still up.
  if ! grep "1" "/sys/class/net/${ctName}/carrier" ; then
    echo "parent is down"
    false
  fi

  # Test using macvlan network.

  # Create macvlan network and add NIC device using that network.
  inc network create "${ctName}net" --type=macvlan parent="${ctName}"
  inc config device add "${ctName}" eth0 nic \
    network="${ctName}net" \
    name=eth0
  inc exec "${ctName}" -- ip addr add "192.0.2.1${ipRand}/24" dev eth0
  inc exec "${ctName}" -- ip addr add "2001:db8::1${ipRand}/64" dev eth0
  inc exec "${ctName}" -- ip link set eth0 up
  inc exec "${ctName}" -- ping -c2 -W5 "192.0.2.2${ipRand}"
  inc exec "${ctName}" -- ping6 -c2 -W5 "2001:db8::2${ipRand}"
  inc config device remove "${ctName}" eth0
  inc network delete "${ctName}net"

  # Check we haven't left any NICS lying around.
  endNicCount=$(find /sys/class/net | wc -l)
  if [ "$startNicCount" != "$endNicCount" ]; then
    echo "leftover NICS detected"
    false
  fi

  # Cleanup.
  inc delete "${ctName}" -f
  inc delete "${ctName}2" -f
  inc profile delete "${ctName}"
  ip link delete "${ctName}" type dummy
}
