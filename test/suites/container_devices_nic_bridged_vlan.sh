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
  incus network create "${prefix}"
  incus network set "${prefix}" bridge.driver "${bridgeDriver}"

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
  incus profile copy default "${prefix}"

  # Modify profile nictype and parent in atomic operation to ensure validation passes.
  incus profile show "${prefix}" | sed  "s/nictype: p2p/nictype: bridged\\n    parent: ${prefix}/" | incus profile edit "${prefix}"

  # Test tagged VLAN traffic is allowed when VLAN filtering and IP filtering are disabled.
  incus launch testimage "${prefix}-ctA" -p "${prefix}"
  incus launch testimage "${prefix}-ctB" -p "${prefix}"
  incus exec "${prefix}-ctA" -- ip link add link eth0 name eth0.2 type vlan id 2
  incus exec "${prefix}-ctA" -- ip link set eth0.2 up
  incus exec "${prefix}-ctA" -- ip a add 192.0.2.1/24 dev eth0.2
  incus exec "${prefix}-ctB" -- ip link add link eth0 name eth0.2 type vlan id 2
  incus exec "${prefix}-ctB" -- ip link set eth0.2 up
  incus exec "${prefix}-ctB" -- ip a add 192.0.2.2/24 dev eth0.2
  incus exec "${prefix}-ctA" -- ping -c2 -W5 192.0.2.2
  incus exec "${prefix}-ctB" -- ping -c2 -W5 192.0.2.1
  incus stop -f "${prefix}-ctA"

  # Test tagged VLAN traffic is filtered when IP filtering is enabled.
  if [ "$bridgeDriver" = "native" ]; then
    incus config device override "${prefix}-ctA" eth0 security.ipv4_filtering=true
    incus start "${prefix}-ctA"
    incus exec "${prefix}-ctA" -- ip link add link eth0 name eth0.2 type vlan id 2
    incus exec "${prefix}-ctA" -- ip link set eth0.2 up
    incus exec "${prefix}-ctA" -- ip a add 192.0.2.1/24 dev eth0.2
    ! incus exec "${prefix}-ctA" -- ping -c2 -W5 192.0.2.2 || false
    ! incus exec "${prefix}-ctB" -- ping -c2 -W5 192.0.2.1 || false
    incus stop -f "${prefix}-ctA"
    incus config device remove "${prefix}-ctA" eth0
  fi

  # Test tagged VLAN traffic is filtered when using MAC filtering with spoofed MAC address.
  if [ "$bridgeDriver" = "native" ]; then
    incus config device override "${prefix}-ctA" eth0 security.mac_filtering=true
    incus start "${prefix}-ctA"
    incus exec "${prefix}-ctA" -- ip link add link eth0 name eth0.2 type vlan id 2
    incus exec "${prefix}-ctA" -- ip link set eth0.2 up
    incus exec "${prefix}-ctA" -- ip a add 192.0.2.1/24 dev eth0.2
    incus exec "${prefix}-ctA" -- ip link set eth0.2 address 10:66:6a:92:f3:c1
    ! incus exec "${prefix}-ctA" -- ping -c2 -W5 192.0.2.2 || false
    ! incus exec "${prefix}-ctB" -- ping -c2 -W5 192.0.2.1 || false
    incus stop -f "${prefix}-ctA"
    incus config device remove "${prefix}-ctA" eth0
  fi

  # Test VLAN validation.
  incus config device override "${prefix}-ctA" eth0 vlan=2 # Test valid untagged VLAN ID.
  incus config device set "${prefix}-ctA" eth0 vlan.tagged="3, 4,5" # Test valid tagged VLAN ID list.
  incus config device set "${prefix}-ctA" eth0 vlan.tagged="3,4-6" # Test valid tagged VLAN ID list with range.
  ! incus config device set "${prefix}-ctA" eth0 vlan.tagged=3,2,4 # Test same tagged VLAN ID as untagged VLAN ID.
  ! incus config device set "${prefix}-ctA" eth0 security.ipv4_filtering = true # Can't use IP filtering with VLANs.
  ! incus config device set "${prefix}-ctA" eth0 security.ipv6_filtering = true # Can't use IP filtering with VLANs.
  ! incus config device set "${prefix}-ctA" eth0 vlan = invalid # Check invalid VLAN ID.
  ! incus config device set "${prefix}-ctA" eth0 vlan = 4096 # Check out of range VLAN ID.
  ! incus config device set "${prefix}-ctA" eth0 vlan = 0 # Check out of range VLAN ID.
  ! incus config device set "${prefix}-ctA" eth0 vlan.tagged = 5,invalid, 6 # Check invalid VLAN ID list.
  ! incus config device set "${prefix}-ctA" eth0 vlan.tagged=-1 # Check out of range VLAN ID list.
  ! incus config device set "${prefix}-ctA" eth0 vlan.tagged=4096 # Check out of range VLAN ID list.
  ! incus config device set "${prefix}-ctA" eth0 vlan.tagged=1,2,-3-4 # Check invalid VLAN ID range input
  ! incus config device set "${prefix}-ctA" eth0 vlan.tagged=1,2,4-3 # Check invalid VLAN ID range boundary (declining range)
  incus config device remove "${prefix}-ctA" eth0

  # Test untagged VLANs (and that tagged VLANs are filtered).
  if [ "$bridgeDriver" = "native" ]; then
    echo 1 > "/sys/class/net/${prefix}/bridge/vlan_filtering"
  fi

  incus config device override "${prefix}-ctA" eth0 vlan=2
  incus start "${prefix}-ctA"
  incus exec "${prefix}-ctA" -- ip link set eth0 up
  incus exec "${prefix}-ctA" -- ip a add 192.0.2.1/24 dev eth0
  incus exec "${prefix}-ctA" -- ip link add link eth0 name eth0.3 type vlan id 3
  incus exec "${prefix}-ctA" -- ip link set eth0.3 up
  incus exec "${prefix}-ctA" -- ip a add 192.0.3.1/24 dev eth0.3
  incus stop -f "${prefix}-ctB"
  incus config device override "${prefix}-ctB" eth0 vlan=2
  incus start "${prefix}-ctB"
  incus exec "${prefix}-ctB" -- ip link set eth0 up
  incus exec "${prefix}-ctB" -- ip a add 192.0.2.2/24 dev eth0
  incus exec "${prefix}-ctB" -- ip link add link eth0 name eth0.3 type vlan id 3
  incus exec "${prefix}-ctB" -- ip link set eth0.3 up
  incus exec "${prefix}-ctB" -- ip a add 192.0.3.2/24 dev eth0.3
  incus exec "${prefix}-ctA" -- ping -c2 -W5 192.0.2.2
  incus exec "${prefix}-ctB" -- ping -c2 -W5 192.0.2.1
  ! incus exec "${prefix}-ctA" -- ping -c2 -W5 192.0.3.2 || false
  ! incus exec "${prefix}-ctB" -- ping -c2 -W5 192.0.3.1 || false
  incus stop -f "${prefix}-ctA"
  incus config device remove "${prefix}-ctA" eth0
  incus stop -f "${prefix}-ctB"
  incus config device remove "${prefix}-ctB" eth0

  # Test tagged VLANs (and that vlan=none filters untagged frames).
  if [ "$bridgeDriver" = "native" ]; then
    echo 1 > "/sys/class/net/${prefix}/bridge/vlan_filtering"
  fi

  incus config device override "${prefix}-ctA" eth0 vlan.tagged=2 vlan=none
  incus start "${prefix}-ctA"
  incus exec "${prefix}-ctA" -- ip link set eth0 up
  incus exec "${prefix}-ctA" -- ip a add 192.0.3.1/24 dev eth0
  incus exec "${prefix}-ctA" -- ip link add link eth0 name eth0.2 type vlan id 2
  incus exec "${prefix}-ctA" -- ip link set eth0.2 up
  incus exec "${prefix}-ctA" -- ip a add 192.0.2.1/24 dev eth0.2
  incus config device override "${prefix}-ctB" eth0 vlan.tagged=2 vlan=none
  incus start "${prefix}-ctB"
  incus exec "${prefix}-ctB" -- ip link set eth0 up
  incus exec "${prefix}-ctB" -- ip a add 192.0.3.2/24 dev eth0
  incus exec "${prefix}-ctB" -- ip link add link eth0 name eth0.2 type vlan id 2
  incus exec "${prefix}-ctB" -- ip link set eth0.2 up
  incus exec "${prefix}-ctB" -- ip a add 192.0.2.2/24 dev eth0.2
  incus exec "${prefix}-ctA" -- ping -c2 -W5 192.0.2.2
  incus exec "${prefix}-ctB" -- ping -c2 -W5 192.0.2.1
  ! incus exec "${prefix}-ctA" -- ping -c2 -W5 192.0.3.2 || false
  ! incus exec "${prefix}-ctB" -- ping -c2 -W5 192.0.3.1 || false
  incus stop -f "${prefix}-ctA"
  incus config device remove "${prefix}-ctA" eth0
  incus stop -f "${prefix}-ctB"
  incus config device remove "${prefix}-ctB" eth0

  # Test custom default VLAN PVID is respected on unmanaged native bridge.
  if [ "$bridgeDriver" = "native" ]; then
    ip link add "${prefix}B" type bridge
    ip link set "${prefix}B" up
    echo 0 > "/sys/class/net/${prefix}B/bridge/vlan_filtering"
    echo 2 > "/sys/class/net/${prefix}B/bridge/default_pvid"
    incus config device override "${prefix}-ctA" eth0 parent="${prefix}B" vlan.tagged=3
    ! incus start "${prefix}-ctA" # Check it fails to start with vlan_filtering disabled.
    echo 1 > "/sys/class/net/${prefix}B/bridge/vlan_filtering"
    incus start "${prefix}-ctA"
    incus exec "${prefix}-ctA" -- ip link set eth0 up
    incus exec "${prefix}-ctA" -- ip a add 192.0.2.1/24 dev eth0
    incus config device override "${prefix}-ctB" eth0 parent="${prefix}B" vlan=2 # Specify VLAN 2 explicitly (ctA is implicit).
    incus start "${prefix}-ctB"
    incus exec "${prefix}-ctB" -- ip link set eth0 up
    incus exec "${prefix}-ctB" -- ip a add 192.0.2.2/24 dev eth0
    incus exec "${prefix}-ctA" -- ping -c2 -W5 192.0.2.2
    incus exec "${prefix}-ctB" -- ping -c2 -W5 192.0.2.1
    incus stop -f "${prefix}-ctA"
    incus config device remove "${prefix}-ctA" eth0
    incus stop -f "${prefix}-ctB"
    incus config device remove "${prefix}-ctB" eth0
    ip link delete "${prefix}B"
  fi

  # Cleanup.
  incus delete -f "${prefix}-ctA"
  incus delete -f "${prefix}-ctB"
  incus profile delete "${prefix}"
  incus network delete "${prefix}"
}
