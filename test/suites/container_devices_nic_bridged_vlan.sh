test_container_devices_nic_bridged_vlan() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"
  prefix="incvlan$$"
  bridgeDriver=${INCUS_NIC_BRIDGED_DRIVER:-"native"}

  if [ "$bridgeDriver" != "native" ] && [ "$bridgeDriver" != "openvswitch" ]; then
    echo "Unrecognised bridge driver: ${bridgeDriver}"
    false
  fi

  # Standard bridge with random subnet.
  inc network create "${prefix}"
  inc network set "${prefix}" bridge.driver "${bridgeDriver}"

  if [ "$bridgeDriver" = "native" ]; then
    if ! grep "1" "/sys/class/net/${prefix}/bridge/vlan_filtering"; then
      echo "vlan filtering not enabled on managed bridge interface"
      false
    fi

    if ! grep "1" "/sys/class/net/${prefix}/bridge/default_pvid"; then
      echo "vlan default PVID not 1 on managed bridge interface"
      false
    fi

    # Make sure VLAN filtering on bridge is disabled initially (for IP filtering tests).
    echo 0 > "/sys/class/net/${prefix}/bridge/vlan_filtering"
  fi

  # Create profile for new containers.
  inc profile copy default "${prefix}"

  # Modify profile nictype and parent in atomic operation to ensure validation passes.
  inc profile show "${prefix}" | sed  "s/nictype: p2p/nictype: bridged\\n    parent: ${prefix}/" | inc profile edit "${prefix}"

  # Test tagged VLAN traffic is allowed when VLAN filtering and IP filtering are disabled.
  inc launch testimage "${prefix}-ctA" -p "${prefix}"
  inc launch testimage "${prefix}-ctB" -p "${prefix}"
  inc exec "${prefix}-ctA" -- ip link add link eth0 name eth0.2 type vlan id 2
  inc exec "${prefix}-ctA" -- ip link set eth0.2 up
  inc exec "${prefix}-ctA" -- ip a add 192.0.2.1/24 dev eth0.2
  inc exec "${prefix}-ctB" -- ip link add link eth0 name eth0.2 type vlan id 2
  inc exec "${prefix}-ctB" -- ip link set eth0.2 up
  inc exec "${prefix}-ctB" -- ip a add 192.0.2.2/24 dev eth0.2
  inc exec "${prefix}-ctA" -- ping -c2 -W5 192.0.2.2
  inc exec "${prefix}-ctB" -- ping -c2 -W5 192.0.2.1
  inc stop -f "${prefix}-ctA"

  # Test tagged VLAN traffic is filtered when IP filtering is enabled.
  if [ "$bridgeDriver" = "native" ]; then
    inc config device override "${prefix}-ctA" eth0 security.ipv4_filtering=true
    inc start "${prefix}-ctA"
    inc exec "${prefix}-ctA" -- ip link add link eth0 name eth0.2 type vlan id 2
    inc exec "${prefix}-ctA" -- ip link set eth0.2 up
    inc exec "${prefix}-ctA" -- ip a add 192.0.2.1/24 dev eth0.2
    ! inc exec "${prefix}-ctA" -- ping -c2 -W5 192.0.2.2 || false
    ! inc exec "${prefix}-ctB" -- ping -c2 -W5 192.0.2.1 || false
    inc stop -f "${prefix}-ctA"
    inc config device remove "${prefix}-ctA" eth0
  fi

  # Test tagged VLAN traffic is filtered when using MAC filtering with spoofed MAC address.
  if [ "$bridgeDriver" = "native" ]; then
    inc config device override "${prefix}-ctA" eth0 security.mac_filtering=true
    inc start "${prefix}-ctA"
    inc exec "${prefix}-ctA" -- ip link add link eth0 name eth0.2 type vlan id 2
    inc exec "${prefix}-ctA" -- ip link set eth0.2 up
    inc exec "${prefix}-ctA" -- ip a add 192.0.2.1/24 dev eth0.2
    inc exec "${prefix}-ctA" -- ip link set eth0.2 address 00:16:3e:92:f3:c1
    ! inc exec "${prefix}-ctA" -- ping -c2 -W5 192.0.2.2 || false
    ! inc exec "${prefix}-ctB" -- ping -c2 -W5 192.0.2.1 || false
    inc stop -f "${prefix}-ctA"
    inc config device remove "${prefix}-ctA" eth0
  fi

  # Test VLAN validation.
  inc config device override "${prefix}-ctA" eth0 vlan=2 # Test valid untagged VLAN ID.
  inc config device set "${prefix}-ctA" eth0 vlan.tagged="3, 4,5" # Test valid tagged VLAN ID list.
  inc config device set "${prefix}-ctA" eth0 vlan.tagged="3,4-6" # Test valid tagged VLAN ID list with range.
  ! inc config device set "${prefix}-ctA" eth0 vlan.tagged=3,2,4 # Test same tagged VLAN ID as untagged VLAN ID.
  ! inc config device set "${prefix}-ctA" eth0 security.ipv4_filtering = true # Can't use IP filtering with VLANs.
  ! inc config device set "${prefix}-ctA" eth0 security.ipv6_filtering = true # Can't use IP filtering with VLANs.
  ! inc config device set "${prefix}-ctA" eth0 vlan = invalid # Check invalid VLAN ID.
  ! inc config device set "${prefix}-ctA" eth0 vlan = 4096 # Check out of range VLAN ID.
  ! inc config device set "${prefix}-ctA" eth0 vlan = 0 # Check out of range VLAN ID.
  ! inc config device set "${prefix}-ctA" eth0 vlan.tagged = 5,invalid, 6 # Check invalid VLAN ID list.
  ! inc config device set "${prefix}-ctA" eth0 vlan.tagged=-1 # Check out of range VLAN ID list.
  ! inc config device set "${prefix}-ctA" eth0 vlan.tagged=4096 # Check out of range VLAN ID list.
  ! inc config device set "${prefix}-ctA" eth0 vlan.tagged=1,2,-3-4 # Check invalid VLAN ID range input
  ! inc config device set "${prefix}-ctA" eth0 vlan.tagged=1,2,4-3 # Check invalid VLAN ID range boundary (declining range)
  inc config device remove "${prefix}-ctA" eth0

  # Test untagged VLANs (and that tagged VLANs are filtered).
  if [ "$bridgeDriver" = "native" ]; then
    echo 1 > "/sys/class/net/${prefix}/bridge/vlan_filtering"
  fi

  inc config device override "${prefix}-ctA" eth0 vlan=2
  inc start "${prefix}-ctA"
  inc exec "${prefix}-ctA" -- ip link set eth0 up
  inc exec "${prefix}-ctA" -- ip a add 192.0.2.1/24 dev eth0
  inc exec "${prefix}-ctA" -- ip link add link eth0 name eth0.3 type vlan id 3
  inc exec "${prefix}-ctA" -- ip link set eth0.3 up
  inc exec "${prefix}-ctA" -- ip a add 192.0.3.1/24 dev eth0.3
  inc stop -f "${prefix}-ctB"
  inc config device override "${prefix}-ctB" eth0 vlan=2
  inc start "${prefix}-ctB"
  inc exec "${prefix}-ctB" -- ip link set eth0 up
  inc exec "${prefix}-ctB" -- ip a add 192.0.2.2/24 dev eth0
  inc exec "${prefix}-ctB" -- ip link add link eth0 name eth0.3 type vlan id 3
  inc exec "${prefix}-ctB" -- ip link set eth0.3 up
  inc exec "${prefix}-ctB" -- ip a add 192.0.3.2/24 dev eth0.3
  inc exec "${prefix}-ctA" -- ping -c2 -W5 192.0.2.2
  inc exec "${prefix}-ctB" -- ping -c2 -W5 192.0.2.1
  ! inc exec "${prefix}-ctA" -- ping -c2 -W5 192.0.3.2 || false
  ! inc exec "${prefix}-ctB" -- ping -c2 -W5 192.0.3.1 || false
  inc stop -f "${prefix}-ctA"
  inc config device remove "${prefix}-ctA" eth0
  inc stop -f "${prefix}-ctB"
  inc config device remove "${prefix}-ctB" eth0

  # Test tagged VLANs (and that vlan=none filters untagged frames).
  if [ "$bridgeDriver" = "native" ]; then
    echo 1 > "/sys/class/net/${prefix}/bridge/vlan_filtering"
  fi

  inc config device override "${prefix}-ctA" eth0 vlan.tagged=2 vlan=none
  inc start "${prefix}-ctA"
  inc exec "${prefix}-ctA" -- ip link set eth0 up
  inc exec "${prefix}-ctA" -- ip a add 192.0.3.1/24 dev eth0
  inc exec "${prefix}-ctA" -- ip link add link eth0 name eth0.2 type vlan id 2
  inc exec "${prefix}-ctA" -- ip link set eth0.2 up
  inc exec "${prefix}-ctA" -- ip a add 192.0.2.1/24 dev eth0.2
  inc config device override "${prefix}-ctB" eth0 vlan.tagged=2 vlan=none
  inc start "${prefix}-ctB"
  inc exec "${prefix}-ctB" -- ip link set eth0 up
  inc exec "${prefix}-ctB" -- ip a add 192.0.3.2/24 dev eth0
  inc exec "${prefix}-ctB" -- ip link add link eth0 name eth0.2 type vlan id 2
  inc exec "${prefix}-ctB" -- ip link set eth0.2 up
  inc exec "${prefix}-ctB" -- ip a add 192.0.2.2/24 dev eth0.2
  inc exec "${prefix}-ctA" -- ping -c2 -W5 192.0.2.2
  inc exec "${prefix}-ctB" -- ping -c2 -W5 192.0.2.1
  ! inc exec "${prefix}-ctA" -- ping -c2 -W5 192.0.3.2 || false
  ! inc exec "${prefix}-ctB" -- ping -c2 -W5 192.0.3.1 || false
  inc stop -f "${prefix}-ctA"
  inc config device remove "${prefix}-ctA" eth0
  inc stop -f "${prefix}-ctB"
  inc config device remove "${prefix}-ctB" eth0

  # Test custom default VLAN PVID is respected on unmanaged native bridge.
  if [ "$bridgeDriver" = "native" ]; then
    ip link add "${prefix}B" type bridge
    ip link set "${prefix}B" up
    echo 0 > "/sys/class/net/${prefix}B/bridge/vlan_filtering"
    echo 2 > "/sys/class/net/${prefix}B/bridge/default_pvid"
    inc config device override "${prefix}-ctA" eth0 parent="${prefix}B" vlan.tagged=3
    ! inc start "${prefix}-ctA" # Check it fails to start with vlan_filtering disabled.
    echo 1 > "/sys/class/net/${prefix}B/bridge/vlan_filtering"
    inc start "${prefix}-ctA"
    inc exec "${prefix}-ctA" -- ip link set eth0 up
    inc exec "${prefix}-ctA" -- ip a add 192.0.2.1/24 dev eth0
    inc config device override "${prefix}-ctB" eth0 parent="${prefix}B" vlan=2 # Specify VLAN 2 explicitly (ctA is implicit).
    inc start "${prefix}-ctB"
    inc exec "${prefix}-ctB" -- ip link set eth0 up
    inc exec "${prefix}-ctB" -- ip a add 192.0.2.2/24 dev eth0
    inc exec "${prefix}-ctA" -- ping -c2 -W5 192.0.2.2
    inc exec "${prefix}-ctB" -- ping -c2 -W5 192.0.2.1
    inc stop -f "${prefix}-ctA"
    inc config device remove "${prefix}-ctA" eth0
    inc stop -f "${prefix}-ctB"
    inc config device remove "${prefix}-ctB" eth0
    ip link delete "${prefix}B"
  fi

  # Cleanup.
  inc delete -f "${prefix}-ctA"
  inc delete -f "${prefix}-ctB"
  inc profile delete "${prefix}"
  inc network delete "${prefix}"
}
