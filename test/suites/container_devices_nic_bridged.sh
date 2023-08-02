test_container_devices_nic_bridged() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  vethHostName="veth$$"
  ctName="nt$$"
  ctMAC="0a:92:a7:0d:b7:d9"
  ipRand=$(shuf -i 0-9 -n 1)
  brName="inct$$"

  # Standard bridge with random subnet and a bunch of options
  lxc network create "${brName}"
  lxc network set "${brName}" dns.mode managed
  lxc network set "${brName}" dns.domain blah
  lxc network set "${brName}" ipv4.nat true
  lxc network set "${brName}" ipv4.routing false
  lxc network set "${brName}" ipv6.routing false
  lxc network set "${brName}" ipv6.dhcp.stateful true
  lxc network set "${brName}" bridge.hwaddr 00:11:22:33:44:55
  lxc network set "${brName}" ipv4.address 192.0.2.1/24
  lxc network set "${brName}" ipv6.address 2001:db8::1/64
  lxc network set "${brName}" ipv4.routes 192.0.3.0/24
  lxc network set "${brName}" ipv6.routes 2001:db7::/64
  [ "$(cat /sys/class/net/${brName}/address)" = "00:11:22:33:44:55" ]

  # Record how many nics we started with.
  startNicCount=$(find /sys/class/net | wc -l)

  # Test pre-launch profile config is applied at launch
  lxc profile copy default "${ctName}"

  # Modify profile nictype and parent in atomic operation to ensure validation passes.
  lxc profile show "${ctName}" | sed  "s/nictype: p2p/nictype: bridged\\n    parent: ${brName}/" | lxc profile edit "${ctName}"

  lxc profile device set "${ctName}" eth0 ipv4.routes "192.0.2.1${ipRand}/32"
  lxc profile device set "${ctName}" eth0 ipv6.routes "2001:db8::1${ipRand}/128"
  lxc profile device set "${ctName}" eth0 limits.ingress 1Mbit
  lxc profile device set "${ctName}" eth0 limits.egress 2Mbit
  lxc profile device set "${ctName}" eth0 host_name "${vethHostName}"
  lxc profile device set "${ctName}" eth0 mtu "1400"
  lxc profile device set "${ctName}" eth0 queue.tx.length "1200"
  lxc profile device set "${ctName}" eth0 hwaddr "${ctMAC}"

  lxc init testimage "${ctName}" -p "${ctName}"

  # Check that adding another NIC to the same network fails because it triggers duplicate instance DNS name checks.
  # Because this would effectively cause 2 NICs with the same instance name to be connected to the same network.
  ! lxc config device add "${ctName}" eth1 nic network=${brName} || false

  # Test device name validation (use vlan=1 to avoid trigger instance DNS name conflict detection).
  lxc config device add "${ctName}" 127.0.0.1 nic network=${brName} vlan=1
  lxc config device remove "${ctName}" 127.0.0.1
  lxc config device add "${ctName}" ::1 nic network=${brName} vlan=1
  lxc config device remove "${ctName}" ::1
  lxc config device add "${ctName}" _valid-name nic network=${brName} vlan=1
  lxc config device remove "${ctName}" _valid-name
  lxc config device add "${ctName}" /foo nic network=${brName} vlan=1
  lxc config device remove "${ctName}" /foo
  ! lxc config device add "${ctName}" .invalid nic network=${brName} vlan=1 || false
  ! lxc config device add "${ctName}" ./invalid nic network=${brName} vlan=1 || false
  ! lxc config device add "${ctName}" ../invalid nic network=${brName} vlan=1 || false

  # Start instance.
  lxc start "${ctName}"

  # Check profile routes are applied on boot.
  if ! ip -4 r list dev "${brName}" | grep "192.0.2.1${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${brName}" | grep "2001:db8::1${ipRand}" ; then
    echo "ipv6.routes invalid"
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
  if ! lxc exec "${ctName}" -- grep "1400" /sys/class/net/eth0/mtu ; then
    echo "container veth mtu invalid"
    false
  fi

  # Check profile custom MTU doesn't affect the host.
  if ! grep "1500" /sys/class/net/"${vethHostName}"/mtu ; then
    echo "host veth mtu invalid"
    false
  fi

  # Check profile custom txqueuelen is applied in container on boot.
  if ! lxc exec "${ctName}" -- grep "1200" /sys/class/net/eth0/tx_queue_len ; then
    echo "container veth txqueuelen invalid"
    false
  fi

  # Check profile custom txqueuelen is applied on host side of veth.
  if ! grep "1200" /sys/class/net/"${vethHostName}"/tx_queue_len ; then
    echo "host veth txqueuelen invalid"
    false
  fi

  # Check profile custom MAC is applied in container on boot.
  if ! lxc exec "${ctName}" -- grep -Fix "${ctMAC}" /sys/class/net/eth0/address ; then
    echo "mac invalid"
    false
  fi

  # Add IP alias to container and check routes actually work.
  lxc exec "${ctName}" -- ip -4 addr add "192.0.2.1${ipRand}/32" dev eth0
  lxc exec "${ctName}" -- ip -4 route add default dev eth0
  ping -c2 -W5 "192.0.2.1${ipRand}"
  lxc exec "${ctName}" -- ip -6 addr add "2001:db8::1${ipRand}/128" dev eth0
  wait_for_dad "${ctName}" eth0
  ping6 -c2 -W5 "2001:db8::1${ipRand}"

  # Test hot plugging a container nic with different settings to profile with the same name.
  lxc config device add "${ctName}" eth0 nic \
    nictype=bridged \
    name=eth0 \
    parent=${brName} \
    ipv4.routes="192.0.2.2${ipRand}/32" \
    ipv6.routes="2001:db8::2${ipRand}/128" \
    limits.ingress=3Mbit \
    limits.egress=4Mbit \
    host_name="${vethHostName}" \
    hwaddr="${ctMAC}" \
    mtu=1401

  # Check profile routes are removed on hot-plug.
  if ip -4 r list dev "${brName}" | grep "192.0.2.1${ipRand}" ; then
    echo "ipv4.routes remain"
    false
  fi
  if ip -6 r list dev "${brName}" | grep "2001:db8::1${ipRand}" ; then
    echo "ipv6.routes remain"
    false
  fi

  # Check routes are applied on hot-plug.
  if ! ip -4 r list dev "${brName}" | grep "192.0.2.2${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${brName}" | grep "2001:db8::2${ipRand}" ; then
    echo "ipv6.routes invalid"
    false
  fi

  # Check limits are applied on hot-plug.
  if ! tc class show dev "${vethHostName}" | grep "3Mbit" ; then
    echo "limits.ingress invalid"
    false
  fi
  if ! tc filter show dev "${vethHostName}" egress | grep "4Mbit" ; then
    echo "limits.egress invalid"
    false
  fi

  # Check custom MTU is applied on hot-plug.
  if ! lxc exec "${ctName}" -- grep "1401" /sys/class/net/eth0/mtu ; then
    echo "container veth mtu invalid"
    false
  fi

  # Check custom MTU doesn't affect the host.
  if ! grep "1500" /sys/class/net/"${vethHostName}"/mtu ; then
    echo "host veth mtu invalid"
    false
  fi

  # Check custom MAC is applied on hot-plug.
  if ! lxc exec "${ctName}" -- grep -Fix "${ctMAC}" /sys/class/net/eth0/address ; then
    echo "mac invalid"
    false
  fi

  # Test removing hot plugged device and check profile nic is restored.
  lxc config device remove "${ctName}" eth0

  # Check routes are removed on hot-plug.
  if ip -4 r list dev "${brName}" | grep "192.0.2.2${ipRand}" ; then
    echo "ipv4.routes remain"
    false
  fi
  if ip -6 r list dev "${brName}" | grep "2001:db8::2${ipRand}" ; then
    echo "ipv6.routes remain"
    false
  fi

  # Check profile routes are applied on hot-removal.
  if ! ip -4 r list dev "${brName}" | grep "192.0.2.1${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${brName}" | grep "2001:db8::1${ipRand}" ; then
    echo "ipv6.routes invalid"
    false
  fi

  # Check profile limits are applie on hot-removal.
  if ! tc class show dev "${vethHostName}" | grep "1Mbit" ; then
    echo "limits.ingress invalid"
    false
  fi
  if ! tc filter show dev "${vethHostName}" egress | grep "2Mbit" ; then
    echo "limits.egress invalid"
    false
  fi

  # Check profile custom MTU is applied on hot-removal.
  if ! lxc exec "${ctName}" -- grep "1400" /sys/class/net/eth0/mtu ; then
    echo "container veth mtu invalid"
    false
  fi

  # Check custom MTU doesn't affect the host.
  if ! grep "1500" /sys/class/net/"${vethHostName}"/mtu ; then
    echo "host veth mtu invalid"
    false
  fi

  # Test hot plugging a container nic then updating it.
  lxc config device add "${ctName}" eth0 nic \
    nictype=bridged \
    name=eth0 \
    parent=${brName} \
    host_name="${vethHostName}" \
    ipv4.routes="192.0.2.1${ipRand}/32" \
    ipv6.routes="2001:db8::1${ipRand}/128"

  # Check removing a required option fails.
  if lxc config device unset "${ctName}" eth0 parent ; then
    echo "shouldnt be able to unset invalrequiredid option"
    false
  fi

  # Check updating an invalid option fails.
  if lxc config device set "${ctName}" eth0 invalid.option "invalid value" ; then
    echo "shouldnt be able to set invalid option"
    false
  fi

  # Check setting invalid IPv4 route.
  if lxc config device set "${ctName}" eth0 ipv4.routes "192.0.2.1/33" ; then
      echo "shouldnt be able to set invalid ipv4.routes value"
    false
  fi

  # Check setting invalid IPv6 route.
  if lxc config device set "${ctName}" eth0 ipv6.routes "2001:db8::1/129" ; then
      echo "shouldnt be able to set invalid ipv6.routes value"
    false
  fi

  lxc config device set "${ctName}" eth0 ipv4.routes "192.0.2.2${ipRand}/32"
  lxc config device set "${ctName}" eth0 ipv6.routes "2001:db8::2${ipRand}/128"
  lxc config device set "${ctName}" eth0 limits.ingress 3Mbit
  lxc config device set "${ctName}" eth0 limits.egress 4Mbit
  lxc config device set "${ctName}" eth0 mtu 1402
  lxc config device set "${ctName}" eth0 hwaddr "${ctMAC}"

  # Check original routes are removed on hot-plug.
  if ip -4 r list dev "${brName}" | grep "192.0.2.1${ipRand}" ; then
    echo "ipv4.routes remain"
    false
  fi
  if ip -6 r list dev "${brName}" | grep "2001:db8::1${ipRand}" ; then
    echo "ipv6.routes remain"
    false
  fi

  # Check routes are applied on update.
  if ! ip -4 r list dev "${brName}" | grep "192.0.2.2${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${brName}" | grep "2001:db8::2${ipRand}" ; then
    echo "ipv6.routes invalid"
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
  if ! lxc exec "${ctName}" -- grep "1402" /sys/class/net/eth0/mtu ; then
    echo "mtu invalid"
    false
  fi

  # Check custom MAC is applied update.
  if ! lxc exec "${ctName}" -- grep -Fix "${ctMAC}" /sys/class/net/eth0/address ; then
    echo "mac invalid"
    false
  fi

  # Check that MTU is inherited from parent device when not specified on device.
  lxc stop "${ctName}" --force
  lxc config device unset "${ctName}" eth0 mtu
  lxc network set "${brName}" bridge.mtu "1405"
  lxc start "${ctName}"
  if ! lxc exec "${ctName}" -- grep "1405" /sys/class/net/eth0/mtu ; then
    echo "mtu not inherited from parent"
    false
  fi
  lxc stop "${ctName}" --force
  lxc network unset "${brName}" bridge.mtu
  lxc start "${ctName}"

  # Add an external 3rd party route to the bridge interface and check that it and the container
  # routes remain when the network is reconfigured.
  ip -4 route add 192.0.2"${ipRand}".0/24 via 192.0.2.1"${ipRand}" dev "${brName}"

  # Now change something that will trigger a network restart
  lxc network set "${brName}" ipv4.nat false

  # Check external routes are applied on update.
  if ! ip -4 r list dev "${brName}" | grep "192.0.2${ipRand}.0/24 via 192.0.2.1${ipRand}" ; then
    echo "external ipv4 routes invalid after network update"
    false
  fi

  # Check container routes are applied on update.
  if ! ip -4 r list dev "${brName}" | grep "192.0.2.2${ipRand}" ; then
    echo "container ipv4 routes invalid after network update"
    false
  fi

  # Check network routes are applied on update.
  if ! ip -4 r list dev "${brName}" | grep "192.0.3.0/24" ; then
    echo "network ipv4 routes invalid after network update"
    false
  fi

  # Check volatile cleanup on stop.
  lxc stop -f "${ctName}"
  if lxc config show "${ctName}" | grep volatile.eth0 ; then
    echo "unexpected volatile key remains"
    false
  fi

  # Test DHCP lease clearance.
  lxc delete "${ctName}" -f
  lxc launch testimage "${ctName}" -p "${ctName}"

  # Request DHCPv4 lease with custom name (to check managed name is allocated instead).
  lxc exec "${ctName}" -- udhcpc -f -i eth0 -n -q -t5 -F "${ctName}custom"

  # Check DHCPv4 lease is allocated.
  if ! grep -i "${ctMAC}" "${INCUS_DIR}/networks/${brName}/dnsmasq.leases" ; then
    echo "DHCPv4 lease not allocated"
    false
  fi

  # Check DHCPv4 lease has DNS record assigned.
  if ! dig @192.0.2.1 "${ctName}.blah" | grep "${ctName}.blah.\\+0.\\+IN.\\+A.\\+192.0.2." ; then
    echo "DNS resolution of DHCP name failed"
    false
  fi

  # Request DHCPv6 lease (if udhcpc6 is in busybox image).
  busyboxUdhcpc6=1
  if ! lxc exec "${ctName}" -- busybox --list | grep udhcpc6 ; then
    busyboxUdhcpc6=0
  fi

  if [ "$busyboxUdhcpc6" = "1" ]; then
        lxc exec "${ctName}" -- udhcpc6 -f -i eth0 -n -q -t5 2>&1 | grep 'IPv6 obtained'
  fi

  # Delete container, check LXD releases lease.
  lxc delete "${ctName}" -f

  # Wait for DHCP release to be processed.
  sleep 2

  # Check DHCPv4 lease is released (space before the MAC important to avoid mismatching IPv6 lease).
  if grep -i " ${ctMAC}" "${INCUS_DIR}/networks/${brName}/dnsmasq.leases" ; then
    echo "DHCPv4 lease not released"
    false
  fi

  # Check DHCPv6 lease is released.
  if grep -i " ${ctName}" "${INCUS_DIR}/networks/${brName}/dnsmasq.leases" ; then
    echo "DHCPv6 lease not released"
    false
  fi

  # Check dnsmasq host config file is removed.
  if [ -f "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/${ctName}.eth0" ] ; then
    echo "dnsmasq host config file not removed"
    false
  fi

  # Check dnsmasq host file is updated on new device.
  lxc init testimage "${ctName}" -p "${ctName}"
  lxc config device add "${ctName}" eth0 nic nictype=bridged parent="${brName}" name=eth0 ipv4.address=192.0.2.200 ipv6.address=2001:db8::200

  ls -lR "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/"

  if ! grep "192.0.2.200" "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/${ctName}.eth0" ; then
    echo "dnsmasq host config not updated with IPv4 address"
    false
  fi

  if ! grep "2001:db8::200" "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/${ctName}.eth0" ; then
    echo "dnsmasq host config not updated with IPv6 address"
    false
  fi

  lxc config device remove "${ctName}" eth0

  if grep "192.0.2.200" "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/${ctName}.eth0" ; then
    echo "dnsmasq host config still has old IPv4 address"
    false
  fi

  if grep "2001:db8::200" "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/${ctName}.eth0" ; then
    echo "dnsmasq host config still has old IPv6 address"
    false
  fi

  # Check dnsmasq leases file removed if DHCP disabled and that device can be removed.
  lxc config device add "${ctName}" eth0 nic nictype=bridged parent="${brName}" name=eth0
  lxc start "${ctName}"
  lxc exec "${ctName}" -- udhcpc -f -i eth0 -n -q -t5
  lxc network set "${brName}" ipv4.address none
  lxc network set "${brName}" ipv6.address none

  # Confirm IPv6 is disabled.
  [ "$(cat /proc/sys/net/ipv6/conf/${brName}/disable_ipv6)" = "1" ]

  if [ -f "${INCUS_DIR}/networks/${brName}/dnsmasq.leases" ] ; then
    echo "dnsmasq.leases file still present after disabling DHCP"
    false
  fi

  if [ -f "${INCUS_DIR}/networks/${brName}/dnsmasq.pid" ] ; then
    echo "dnsmasq.pid file still present after disabling DHCP"
    false
  fi

  lxc profile device unset "${ctName}" eth0 ipv6.routes
  lxc config device remove "${ctName}" eth0
  lxc stop -f "${ctName}"
  if [ -f "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/${ctName}.eth0" ] ; then
    echo "dnsmasq host config file not removed from network"
    false
  fi

  # Re-enable DHCP on network.
  lxc network set "${brName}" ipv4.address 192.0.2.1/24
  lxc network set "${brName}" ipv6.address 2001:db8::1/64
  lxc profile device set "${ctName}" eth0 ipv6.routes "2001:db8::1${ipRand}/128"

  # Confirm IPv6 is re-enabled.
  [ "$(cat /proc/sys/net/ipv6/conf/${brName}/disable_ipv6)" = "0" ]

  # Check dnsmasq host file is created on add.
  lxc config device add "${ctName}" eth0 nic nictype=bridged parent="${brName}" name=eth0
  if [ ! -f "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/${ctName}.eth0" ] ; then
    echo "dnsmasq host config file not created"
    false
  fi

  # Check connecting device to non-managed bridged.
  ip link add "${ctName}" type dummy
  lxc config device set "${ctName}" eth0 parent "${ctName}"
  if [ -f "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/${ctName}.eth0" ] ; then
    echo "dnsmasq host config file not removed from old network"
    false
  fi

  ip link delete "${ctName}"

  # Linked network tests.
  # Can't use network property when parent is set.
  ! lxc profile device set "${ctName}" eth0 network="${brName}"

  # Remove mtu, nictype and parent settings and assign network in one command.
  lxc profile device set "${ctName}" eth0 mtu="" parent="" nictype="" network="${brName}"

  # Can't remove network if parent not specified.
  ! lxc profile device unset "${ctName}" eth0 network

  # Can't use some settings when network is set.
  ! lxc profile device set "${ctName}" eth0 nictype="bridged"
  ! lxc profile device set "${ctName}" eth0 mtu="1400"
  ! lxc profile device set "${ctName}" eth0 maas.subnet.ipv4="test"
  ! lxc profile device set "${ctName}" eth0 maas.subnet.ipv6="test"

  # Can't set static IP that isn't part of network's subnet.
  ! lxc profile device set "${ctName}" eth0 ipv4.address="192.0.4.2"
  ! lxc profile device set "${ctName}" eth0 ipv6.address="2001:db8:2::2"

  # Test bridge MTU is inherited.
  lxc network set "${brName}" bridge.mtu 1400
  lxc config device remove "${ctName}" eth0
  lxc start "${ctName}"
  if ! lxc exec "${ctName}" -- grep "1400" /sys/class/net/eth0/mtu ; then
    echo "container mtu has not been inherited from network"
    false
  fi

  # Check profile routes are applied on boot (as these use derived nictype).
  if ! ip -4 r list dev "${brName}" | grep "192.0.2.1${ipRand}" ; then
    echo "ipv4.routes invalid"
    false
  fi
  if ! ip -6 r list dev "${brName}" | grep "2001:db8::1${ipRand}" ; then
    echo "ipv6.routes invalid"
    false
  fi

  lxc stop -f "${ctName}"
  lxc network unset "${brName}" bridge.mtu

  # Test stateful DHCP static IP checks.
  ! lxc config device override "${ctName}" eth0 ipv4.address="192.0.4.2"

  lxc network set "${brName}" ipv4.dhcp false
  ! lxc config device override "${ctName}" eth0 ipv4.address="192.0.2.2"
  lxc network unset "${brName}" ipv4.dhcp
  lxc config device override "${ctName}" eth0 ipv4.address="192.0.2.2"

  ! lxc config device set "${ctName}" eth0 ipv6.address="2001:db8:2::2"

  lxc network set "${brName}" ipv6.dhcp=false ipv6.dhcp.stateful=false
  ! lxc config device set "${ctName}" eth0 ipv6.address="2001:db8::2"
  lxc network set "${brName}" ipv6.dhcp=true ipv6.dhcp.stateful=false
  ! lxc config device set "${ctName}" eth0 ipv6.address="2001:db8::2"
  lxc network set "${brName}" ipv6.dhcp=false ipv6.dhcp.stateful=true
  ! lxc config device set "${ctName}" eth0 ipv6.address="2001:db8::2"

  lxc network unset "${brName}" ipv6.dhcp
  lxc config device set "${ctName}" eth0 ipv6.address="2001:db8::2"

  # Test port isolation.
  if bridge link set help 2>&1 | grep isolated ; then
    lxc config device set "${ctName}" eth0 security.port_isolation true
    lxc start "${ctName}"
    bridge -d link show dev "${vethHostName}" | grep "isolated on"
    lxc stop -f "${ctName}"
  else
    echo "bridge command doesn't support port isolation, skipping port isolation checks"
  fi

  # Test interface naming scheme.
  lxc init testimage test-naming
  lxc start test-naming
  lxc query "/1.0/instances/test-naming/state" | jq -r .network.eth0.host_name | grep ^veth
  lxc stop -f test-naming

  lxc config set instances.nic.host_name random
  lxc start test-naming
  lxc query "/1.0/instances/test-naming/state" | jq -r .network.eth0.host_name | grep ^veth
  lxc stop -f test-naming

  lxc config set instances.nic.host_name mac
  lxc start test-naming
  lxc query "/1.0/instances/test-naming/state" | jq -r .network.eth0.host_name | grep ^lxd
  lxc stop -f test-naming

  lxc config unset instances.nic.host_name
  lxc delete -f test-naming

  # Test new container with conflicting addresses can be created as a copy.
  lxc config device set "${ctName}" eth0 \
    ipv4.address=192.0.2.232 \
    hwaddr="" # Remove static MAC so that copies use new MAC (as changing MAC triggers device remove/add on snapshot restore).
  grep -F "192.0.2.232" "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/${ctName}.eth0"
  lxc copy "${ctName}" foo # Gets new MAC address but IPs still conflict.
  ! stat "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/foo.eth0" || false
  lxc snapshot foo
  lxc export foo foo.tar.gz
  ! lxc start foo || false
  lxc config device set foo eth0 \
    ipv4.address=192.0.2.233 \
    ipv6.address=2001:db8::3
  grep -F "192.0.2.233" "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/foo.eth0"
  lxc start foo
  lxc stop -f foo

  # Test container snapshot with conflicting addresses can be restored.
  lxc restore foo snap0 # Test restore, IPs conflict on config device update (due to only IPs changing).
  ! stat "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/foo.eth0" || false # Check lease file removed (due to non-user requested update failing).
  lxc config device get foo eth0 ipv4.address | grep -Fx '192.0.2.232'
  ! lxc start foo || false
  lxc config device set foo eth0 \
    hwaddr="0a:92:a7:0d:b7:c9" \
    ipv4.address=192.0.2.233 \
    ipv6.address=2001:db8::3
  lxc start foo
  lxc stop -f foo

  lxc restore foo snap0 # Test restore, IPs conflict on config device remove/add (due to MAC change).
  ! stat "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/foo.eth0" || false # Check lease file removed (due to MAC change).
  lxc config device get foo eth0 ipv4.address | grep -Fx '192.0.2.232'
  ! lxc start foo || false
  lxc config device set foo eth0 \
    hwaddr="0a:92:a7:0d:b7:c9" \
    ipv4.address=192.0.2.233 \
    ipv6.address=2001:db8::3
  grep -F "192.0.2.233" "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/foo.eth0"
  lxc start foo
  lxc delete -f foo

  # Test container with conflicting addresses can be restored from backup.
  lxc import foo.tar.gz
  ! stat "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/foo.eth0" || false
  ! lxc start foo || false
  lxc config device get foo eth0 ipv4.address | grep -Fx '192.0.2.232'
  lxc config show foo/snap0 | grep -F 'ipv4.address: 192.0.2.232'
  lxc config device set foo eth0 \
    hwaddr="0a:92:a7:0d:b7:c9" \
    ipv4.address=192.0.2.233 \
    ipv6.address=2001:db8::3
  grep -F "192.0.2.233" "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/foo.eth0"
  lxc config device get foo eth0 ipv4.address | grep -Fx '192.0.2.233'
  lxc start foo

  # Check MAC conflict detection:
  ! lxc config device set "${ctName}" eth0 hwaddr="0a:92:a7:0d:b7:c9" || false

  # Test container with conflicting addresses rebuilds DHCP lease if original conflicting instance is removed.
  lxc delete -f foo
  ! stat "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/foo.eth0" || false
  lxc import foo.tar.gz
  rm foo.tar.gz
  ! lxc start foo || false
  lxc delete "${ctName}" -f
  lxc start foo
  grep -F "192.0.2.232" "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/foo.eth0"
  lxc delete -f foo

  # Test container without extra network configuration can be restored from backup.
  lxc init testimage foo -p "${ctName}"
  lxc export foo foo.tar.gz
  lxc import foo.tar.gz foo2
  rm foo.tar.gz
  lxc profile assign foo2 "${ctName}"

  # Test container start will fail due to volatile MAC conflict.
  lxc config get foo volatile.eth0.hwaddr | grep -Fx "$(lxc config get foo2 volatile.eth0.hwaddr)"
  ! lxc start foo2 || false
  lxc delete -f foo foo2

  # Check we haven't left any NICS lying around.
  endNicCount=$(find /sys/class/net | wc -l)
  if [ "$startNicCount" != "$endNicCount" ]; then
    echo "leftover NICS detected"
    false
  fi

  # Cleanup.
  lxc profile delete "${ctName}"
  lxc network delete "${brName}"
}
