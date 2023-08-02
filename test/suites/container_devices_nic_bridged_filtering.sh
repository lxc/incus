test_container_devices_nic_bridged_filtering() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  firewallDriver=$(lxc info | awk -F ":" '/firewall:/{gsub(/ /, "", $0); print $2}')

  if [ "$firewallDriver" != "xtables" ] && [ "$firewallDriver" != "nftables" ]; then
    echo "Unrecognised firewall driver: ${firewallDriver}"
    false
  fi

  if [ "$firewallDriver" = "xtables" ]; then
    if readlink -f "$(command -v ebtables)" | grep -q nft; then
      echo "==> SKIP: ebtables must be legacy version (try update-alternatives --set ebtables /usr/sbin/ebtables-legacy)"
      return
    fi
  fi

  # Record how many nics we started with.
  startNicCount=$(find /sys/class/net | wc -l)

  ctPrefix="nt$$"
  brName="inct$$"

  # Standard bridge with random subnet and a bunch of options.
  lxc network create "${brName}"
  lxc network set "${brName}" dns.mode managed
  lxc network set "${brName}" dns.domain blah
  lxc network set "${brName}" ipv4.nat true

  # Routing is required for container to container traffic as filtering requires br_netfilter module.
  # This then causes bridged traffic to go through the FORWARD chain in iptables.
  lxc network set "${brName}" ipv4.routing true
  lxc network set "${brName}" ipv6.routing true

  lxc network set "${brName}" ipv6.dhcp.stateful true
  lxc network set "${brName}" bridge.hwaddr 00:11:22:33:44:55
  lxc network set "${brName}" ipv4.address 192.0.2.1/24
  lxc network set "${brName}" ipv6.address 2001:db8:1::1/64
  [ "$(cat /sys/class/net/${brName}/address)" = "00:11:22:33:44:55" ]

  # Create profile for new containers.
  lxc profile copy default "${ctPrefix}"

  # Modify profile nictype and parent in atomic operation to ensure validation passes.
  lxc profile show "${ctPrefix}" | sed  "s/nictype: p2p/nictype: bridged\\n    parent: ${brName}/" | lxc profile edit "${ctPrefix}"

  # Launch first container.
  lxc init testimage "${ctPrefix}A" -p "${ctPrefix}"
  lxc config device add "${ctPrefix}A" eth0 nic nictype=nic name=eth0 nictype=bridged parent="${brName}"
  lxc start "${ctPrefix}A"
  lxc exec "${ctPrefix}A" -- ip a add 192.0.2.2/24 dev eth0

  # Launch second container.
  lxc init testimage "${ctPrefix}B" -p "${ctPrefix}"
  lxc config device add "${ctPrefix}B" eth0 nic nictype=nic name=eth0 nictype=bridged parent="${brName}"
  lxc start "${ctPrefix}B"
  lxc exec "${ctPrefix}B" -- ip a add 192.0.2.3/24 dev eth0

  # Check basic connectivity without any filtering.
  lxc exec "${ctPrefix}A" -- ping -c2 -W5 192.0.2.1
  lxc exec "${ctPrefix}A" -- ping -c2 -W5 192.0.2.3

  # Enable MAC filtering on CT A and test.
  lxc config device set "${ctPrefix}A" eth0 security.mac_filtering true
  ctAMAC=$(lxc config get "${ctPrefix}A" volatile.eth0.hwaddr)

  # Check MAC filter is present in firewall.
  ctAHost=$(lxc config get "${ctPrefix}A" volatile.eth0.host_name)
  if [ "$firewallDriver" = "xtables" ]; then
    if ! ebtables --concurrent -L --Lmac2 --Lx | grep -e "-s ! ${ctAMAC} -i ${ctAHost} -j DROP" ; then
      echo "MAC filter not applied as part of mac_filtering in ebtables"
      false
    fi
  else
    macHex=$(echo "${ctAMAC}" |sed "s/://g")
    macDec=$(printf "%d" 0x"${macHex}")
    macHex=$(printf "0x%x" "${macDec}")

    for table in "in" "fwd"
    do
      rules=$(nft -nn list chain bridge lxd "${table}.${ctPrefix}A.eth0")

      if ! echo "${rules}" | grep -e "iifname \"${ctAHost}\" ether saddr != ${ctAMAC} drop"; then
        echo "MAC filter not applied as part of mac_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
      if ! echo "${rules}" | grep -e "iifname \"${ctAHost}\" arp saddr ether != ${ctAMAC} drop"; then
        echo "MAC ARP filter not applied as part of mac_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
      if ! echo "${rules}" | grep -P "iifname \"${ctAHost}\" icmpv6 type 136 @nh,528,48 != (${macHex}|${macDec}) drop"; then
        echo "MAC NDP filter not applied as part of mac_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
    done
  fi

  # Setup fake MAC inside container.
  lxc exec "${ctPrefix}A" -- ip link set dev eth0 address 00:11:22:33:44:56 up

  # Check that ping is no longer working (i.e its filtered after fake MAC setup).
  if lxc exec "${ctPrefix}A" -- ping -c2 -W5 192.0.2.1; then
      echo "MAC filter not working to host"
      false
  fi

  # Check that ping is no longer working (i.e its filtered after fake MAC setup).
  if lxc exec "${ctPrefix}A" -- ping -c2 -W5 192.0.2.3; then
      echo "MAC filter not working to other container"
      false
  fi

  # Restore real MAC
  lxc exec "${ctPrefix}A" -- ip link set dev eth0 address "${ctAMAC}" up

  # Check basic connectivity with MAC filtering but real MAC configured.
  lxc exec "${ctPrefix}A" -- ping -c2 -W5 192.0.2.1
  lxc exec "${ctPrefix}A" -- ping -c2 -W5 192.0.2.3

  # Stop CT A and check filters are cleaned up.
  lxc stop -f "${ctPrefix}A"
  if [ "$firewallDriver" = "xtables" ]; then
    if ebtables --concurrent -L --Lmac2 --Lx | grep -e "-s ! ${ctAMAC} -i ${ctAHost} -j DROP" ; then
        echo "MAC filter still applied as part of mac_filtering in ebtables"
        false
    fi
  else
    for table in "in" "fwd"
    do
      if nft -nn list chain bridge lxd "${table}.${ctPrefix}A.eth0" | grep -e "${ctAHost}"; then
        echo "MAC filter still applied as part of mac_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
    done
  fi

  # Add a fake IPv4 and check connectivity
  lxc start "${ctPrefix}A"
  lxc exec "${ctPrefix}A" -- ip link set dev eth0 address "${ctAMAC}" up
  lxc exec "${ctPrefix}A" -- ip a add 192.0.2.254/24 dev eth0
  lxc exec "${ctPrefix}A" -- ping -c2 -W5 192.0.2.1
  lxc exec "${ctPrefix}A" -- ping -c2 -W5 192.0.2.3

  # Enable IPv4 filtering on CT A and test (disable security.mac_filtering to check its applied too).
  lxc config device set "${ctPrefix}A" eth0 ipv4.address 192.0.2.2
  lxc config device set "${ctPrefix}A" eth0 security.mac_filtering false
  lxc config device set "${ctPrefix}A" eth0 security.ipv4_filtering true
  lxc config device set "${ctPrefix}A" eth0 ipv4.routes 198.51.100.0/24
  lxc config device set "${ctPrefix}A" eth0 ipv4.routes.external 203.0.113.0/24

  # Check MAC and IPv4 filter is present in firewall.
  ctAHost=$(lxc config get "${ctPrefix}A" volatile.eth0.host_name)
  if [ "$firewallDriver" = "xtables" ]; then
    if ! ebtables --concurrent -L --Lmac2 --Lx | grep -e "-s ! ${ctAMAC} -i ${ctAHost} -j DROP" ; then
      echo "MAC filter not applied as part of ipv4_filtering in ebtables"
      false
    fi
    if ! ebtables --concurrent -L --Lmac2 --Lx | grep -e "192.0.2.2" ; then
        echo "IPv4 filter not applied as part of ipv4_filtering in ebtables"
        false
    fi
    if ! ebtables --concurrent -L --Lmac2 --Lx | grep -e "198.51.100.0/24" ; then
        echo "IPv4 filter for ipv4.routes not applied as part of ipv4_filtering in ebtables"
        false
    fi
    if ! ebtables --concurrent -L --Lmac2 --Lx | grep -e "203.0.113.0/24" ; then
        echo "IPv4 filter for ipv4.routes.external not applied as part of ipv4_filtering in ebtables"
        false
    fi
  else
    for table in "in" "fwd"
    do
      if ! nft -nn list chain bridge lxd "${table}.${ctPrefix}A.eth0" | grep -e "iifname \"${ctAHost}\" ether saddr != ${ctAMAC} drop"; then
        echo "MAC filter not applied as part of ipv4_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
      if ! nft -nn list chain bridge lxd "${table}.${ctPrefix}A.eth0" | grep -e "iifname \"${ctAHost}\" arp saddr ether != ${ctAMAC} drop"; then
        echo "MAC ARP filter not applied as part of ipv4_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
      if ! nft -nn list chain bridge lxd "${table}.${ctPrefix}A.eth0" | grep -e "iifname \"${ctAHost}\" ip saddr 192.0.2.2 accept"; then
        echo "IPv4 filter not applied as part of ipv4_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
      if ! nft -nn list chain bridge lxd "${table}.${ctPrefix}A.eth0" | grep -e "iifname \"${ctAHost}\" arp saddr ip 192.0.2.2 accept"; then
        echo "IPv4 ARP filter not applied as part of ipv4_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
      if ! nft -nn list chain bridge lxd "${table}.${ctPrefix}A.eth0" | grep -e "iifname \"${ctAHost}\" ip saddr 198.51.100.0/24 accept"; then
        echo "IPv4 filter not applied as part of ipv4_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
      if ! nft -nn list chain bridge lxd "${table}.${ctPrefix}A.eth0" | grep -e "iifname \"${ctAHost}\" arp saddr ip 198.51.100.0/24 accept"; then
        echo "IPv4 ARP filter not applied as part of ipv4_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
      if ! nft -nn list chain bridge lxd "${table}.${ctPrefix}A.eth0" | grep -e "iifname \"${ctAHost}\" ip saddr 203.0.113.0/24 accept"; then
        echo "IPv4 filter not applied as part of ipv4_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
      if ! nft -nn list chain bridge lxd "${table}.${ctPrefix}A.eth0" | grep -e "iifname \"${ctAHost}\" arp saddr ip 203.0.113.0/24 accept"; then
        echo "IPv4 ARP filter not applied as part of ipv4_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
    done
  fi

  # Check DHCPv4 allocation still works.
  lxc exec "${ctPrefix}A" -- ip link set dev eth0 address "${ctAMAC}" up
  lxc exec "${ctPrefix}A" -- udhcpc -f -i eth0 -n -q -t5
  lxc exec "${ctPrefix}A" -- ip a flush dev eth0
  lxc exec "${ctPrefix}A" -- ip a add 192.0.2.2/24 dev eth0

  # Check basic connectivity with IPv4 filtering and real IPs configured.
  lxc exec "${ctPrefix}A" -- ping -c2 -W5 192.0.2.1
  lxc exec "${ctPrefix}A" -- ping -c2 -W5 192.0.2.3

  # Add a fake IP
  lxc exec "${ctPrefix}A" -- ip a flush dev eth0
  lxc exec "${ctPrefix}A" -- ip a add 192.0.2.254/24 dev eth0

  # Check that ping is no longer working (i.e its filtered after fake IP setup).
  if lxc exec "${ctPrefix}A" -- ping -c2 -W5 192.0.2.1; then
      echo "IPv4 filter not working to host"
      false
  fi

  # Check that ping is no longer working (i.e its filtered after fake IP setup).
  if lxc exec "${ctPrefix}A" -- ping -c2 -W5 192.0.2.3; then
      echo "IPv4 filter not working to other container"
      false
  fi

  # Add a fake IP within ipv4.routes range (198.51.100.0/24)
  lxc exec "${ctPrefix}A" -- ip a flush dev eth0
  lxc exec "${ctPrefix}A" -- ip a add 198.51.100.1/32 dev eth0
  lxc exec "${ctPrefix}A" -- ip r add 192.0.2.0/24 dev eth0
  lxc exec "${ctPrefix}B" -- ip r add 198.51.100.0/24 dev eth0

  # Check that ping is still working (i.e the filter did not apply to the ipv4.routes subnet).
  if ! lxc exec "${ctPrefix}A" -- ping -c2 -W5 192.0.2.1; then
      echo "IPv4 filter is preventing traffic from within ipv4.routes"
      false
  fi

  # Check that ping is still working (i.e the filter did not apply to the ipv4.routes subnet).
  if ! lxc exec "${ctPrefix}A" -- ping -c2 -W5 192.0.2.3; then
      echo "IPv4 filter is preventing traffic from within ipv4.routes"
      false
  fi

  # Add a fake IP within ipv4.routes.external range (203.0.113.0/24)
  lxc exec "${ctPrefix}A" -- ip a flush dev eth0
  lxc exec "${ctPrefix}A" -- ip a add 203.0.113.1/32 dev eth0
  lxc exec "${ctPrefix}A" -- ip r add 192.0.2.0/24 dev eth0
  lxc exec "${ctPrefix}B" -- ip r add 203.0.113.0/24 dev eth0

  # Check that ping is still working (i.e the filter did not apply to the ipv4.routes.external subnet).
  if ! lxc exec "${ctPrefix}A" -- ping -c2 -W5 192.0.2.1; then
      echo "IPv4 filter is preventing traffic from within ipv4.routes.external"
      false
  fi

  # Check that ping is still working (i.e the filter did not apply to the ipv4.routes.external subnet).
  if ! lxc exec "${ctPrefix}A" -- ping -c2 -W5 192.0.2.3; then
      echo "IPv4 filter is preventing traffic from within ipv4.routes.external"
      false
  fi

  # Stop CT A and check filters are cleaned up in firewall.
  lxc stop -f "${ctPrefix}A"
  if [ "$firewallDriver" = "xtables" ]; then
    if ebtables --concurrent -L --Lmac2 --Lx | grep -e "${ctAHost}" ; then
        echo "IPv4 filter still applied as part of ipv4_filtering in ebtables"
        false
    fi
  else
    for table in "in" "fwd"
    do
      if nft -nn list chain bridge lxd "${table}.${ctPrefix}A.eth0" | grep -e "${ctAHost}"; then
        echo "IPv4 filter still applied as part of ipv4_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
    done
  fi

  # Remove static IP and check IP filter works with previous DHCP lease.
  rm "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/${ctPrefix}A.eth0"
  lxc config device unset "${ctPrefix}A" eth0 ipv4.address
  lxc start "${ctPrefix}A"
  if ! grep "192.0.2.2" "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/${ctPrefix}A.eth0" ; then
    echo "dnsmasq host config doesnt contain previous lease as static IPv4 config"
    false
  fi

  lxc stop -f "${ctPrefix}A"
  lxc config device set "${ctPrefix}A" eth0 security.ipv4_filtering false
  rm "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/${ctPrefix}A.eth0"

  # Simulate 192.0.2.2 being used by another container, next free IP is 192.0.2.3
  kill "$(awk '/^pid/ {print $2}' "${INCUS_DIR}"/networks/"${brName}"/dnsmasq.pid)"
  echo "$(date --date="1hour" +%s) 00:16:3e:55:4c:fd 192.0.2.2 c1 ff:6f:c3:ab:c5:00:02:00:00:ab:11:f8:5c:3d:73:db:b2:6a:06" > "${INCUS_DIR}/networks/${brName}/dnsmasq.leases"
  shutdown_incus "${INCUS_DIR}"
  respawn_incus "${INCUS_DIR}" true
  lxc config device set "${ctPrefix}A" eth0 security.ipv4_filtering true
  lxc start "${ctPrefix}A"

  if ! grep "192.0.2.3" "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/${ctPrefix}A.eth0" ; then
    echo "dnsmasq host config doesnt contain sequentially allocated static IPv4 config"
    false
  fi

  # Simulate changing DHCPv4 ranges.
  lxc stop -f "${ctPrefix}A"
  lxc network set "${brName}" ipv4.dhcp.ranges "192.0.2.100-192.0.2.110"
  lxc start "${ctPrefix}A"

  if ! grep "192.0.2.100" "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/${ctPrefix}A.eth0" ; then
    echo "dnsmasq host config doesnt contain sequentially range allocated static IPv4 config"
    false
  fi

  # Make sure br_netfilter is loaded, needed for IPv6 filtering.
  modprobe br_netfilter || true
  if ! grep 1 /proc/sys/net/bridge/bridge-nf-call-ip6tables ; then
    echo "br_netfilter didn't load, skipping IPv6 filter checks"
    lxc delete -f "${ctPrefix}A"
    lxc delete -f "${ctPrefix}B"
    lxc profile delete "${ctPrefix}"
    lxc network delete "${brName}"
    return
  fi

  # Add a fake IPv6 and check connectivity
  lxc exec "${ctPrefix}B" -- ip -6 a add 2001:db8:1::3/64 dev eth0
  wait_for_dad "${ctPrefix}B" eth0

  lxc exec "${ctPrefix}A" -- ip link set dev eth0 address "${ctAMAC}" up
  lxc exec "${ctPrefix}A" -- ip -6 a add 2001:db8:1::254 dev eth0
  wait_for_dad "${ctPrefix}A" eth0
  lxc exec "${ctPrefix}A" -- ping6 -c2 -W5 2001:db8:1::1
  lxc exec "${ctPrefix}A" -- ping6 -c2 -W5 2001:db8:1::3

  # Enable IPv6 filtering on CT A and test (disable security.mac_filtering to check its applied too).
  lxc config device set "${ctPrefix}A" eth0 ipv6.address 2001:db8:1::2
  lxc config device set "${ctPrefix}A" eth0 security.mac_filtering false
  lxc config device set "${ctPrefix}A" eth0 security.ipv6_filtering true

  # Set the ipv6.routes with mask ::ffff (i.e. subnet is last four hex digits)
  lxc config device set "${ctPrefix}A" eth0 ipv6.routes 2001:db8:2::/64
  lxc config device set "${ctPrefix}A" eth0 ipv6.routes.external 2001:db8:3::/64

  # Check MAC filter is present in firewall.
  ctAHost=$(lxc config get "${ctPrefix}A" volatile.eth0.host_name)
  macHex=$(echo "${ctAMAC}" |sed "s/://g")

  if [ "$firewallDriver" = "xtables" ]; then
    if ! ebtables --concurrent -L --Lmac2 --Lx | grep -e "-s ! ${ctAMAC} -i ${ctAHost} -j DROP" ; then
        echo "MAC filter not applied as part of ipv6_filtering in ebtables"
        false
    fi

    # Check NDP MAC filter is present in ip6tables.
    if ! ip6tables -S -w -t filter | grep -e "${macHex}" ; then
        echo "MAC NDP filter not applied as part of ipv6_filtering in ip6tables"
        false
    fi

    # Check NDP IPv6 filter is present in ip6tables.
    if ! ip6tables -S -w -t filter | grep -e "20010db8000100000000000000000002" ; then
        echo "IPv6 NDP filter not applied as part of ipv6_filtering in ip6tables"
        false
    fi

    # Check NDP IPv6 filter for ipv6.routes is present in ip6tables.
    if ! ip6tables -S -w -t filter | grep -e "20010db800020000" ; then
        echo "IPv6 NDP filter for ipv6.routes not applied as part of ipv6_filtering in ip6tables"
        false
    fi

    # Check NDP IPv6 filter for ipv6.routes.external is present in ip6tables.
    if ! ip6tables -S -w -t filter | grep -e "20010db800030000" ; then
        echo "IPv6 NDP filter for ipv6.routes.external not applied as part of ipv6_filtering in ip6tables"
        false
    fi

    # Check IPv6 filter is present in ebtables.
    if ! ebtables --concurrent -L --Lmac2 --Lx | grep -e "2001:db8:1::2" ; then
        echo "IPv6 filter not applied as part of ipv6_filtering in ebtables"
        false
    fi

    # Check IPv6 RA filter is present in ebtables.
    if ! ebtables --concurrent -L --Lmac2 --Lx | grep -e "-i ${ctAHost} --ip6-proto ipv6-icmp --ip6-icmp-type router-advertisement -j DROP" ; then
        echo "IPv6 RA filter not applied as part of ipv6_filtering in ebtables"
        false
    fi
  else
    macDec=$(printf "%d" 0x"${macHex}")
    macHex=$(printf "0x%x" "${macDec}")
    ipv6Hex="0x20010db8000100000000000000000002"
    ipv6Dec="42540766411283801782723599580828532738"
    ipv6RoutesHex="0x20010db800020000"
    ipv6RoutesDec="2306139568115679232"
    ipv6RoutesExternalHex="0x20010db800030000"
    ipv6RoutesExternalDec="2306139568115744768"

    rules=$(nft -nn list chain bridge lxd "${table}.${ctPrefix}A.eth0")

    for table in "in" "fwd"
    do
      if ! echo "${rules}" | grep "iifname \"${ctAHost}\" ether saddr != ${ctAMAC} drop"; then
        echo "MAC filter not applied as part of ipv6_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
      if ! echo "${rules}" | grep "iifname \"${ctAHost}\" arp saddr ether != ${ctAMAC} drop"; then
        echo "MAC ARP filter not applied as part of ipv6_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
      if ! echo "${rules}" | grep -P "iifname \"${ctAHost}\" icmpv6 type 136 @nh,528,48 != (${macHex}|${macDec}) drop"; then
        echo "MAC NDP filter not applied as part of ipv6_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
      if ! echo "${rules}" | grep -P "iifname \"${ctAHost}\" icmpv6 type 136 @nh,384,128 (${ipv6Hex}|${ipv6Dec}) accept"; then
        echo "IPv6 NDP filter not applied as part of ipv6_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
      if ! echo "${rules}" | grep "iifname \"${ctAHost}\" ip6 saddr 2001:db8:1::2 accept"; then
        echo "IPv6 filter not applied as part of ipv6_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
      if ! echo "${rules}" | grep -P "iifname \"${ctAHost}\" icmpv6 type 136 @nh,384,64 (${ipv6RoutesHex}|${ipv6RoutesDec}) accept"; then
        echo "IPv6 NDP filter not applied as part of ipv6_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
      if ! echo "${rules}" | grep "iifname \"${ctAHost}\" ip6 saddr 2001:db8:2::/64 accept"; then
        echo "IPv6 filter not applied as part of ipv6_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
      if ! echo "${rules}" | grep -P "iifname \"${ctAHost}\" icmpv6 type 136 @nh,384,64 (${ipv6RoutesExternalHex}|${ipv6RoutesExternalDec}) accept"; then
        echo "IPv6 NDP filter not applied as part of ipv6_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
      if ! echo "${rules}" | grep "iifname \"${ctAHost}\" ip6 saddr 2001:db8:3::/64 accept"; then
        echo "IPv6 filter not applied as part of ipv6_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
      if ! echo "${rules}" | grep -e "iifname \"${ctAHost}\" icmpv6 type 134 drop"; then
        echo "IPv6 RA filter not applied as part of ipv6_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
    done
  fi

  # Check DHCPv6 allocation still works (if udhcpc6 is in busybox image).
  lxc exec "${ctPrefix}A" -- ip link set dev eth0 address "${ctAMAC}" up

  busyboxUdhcpc6=1
  if ! lxc exec "${ctPrefix}A" -- busybox --list | grep udhcpc6 ; then
    busyboxUdhcpc6=0
  fi

  if [ "$busyboxUdhcpc6" = "1" ]; then
      lxc exec "${ctPrefix}A" -- udhcpc6 -f -i eth0 -n -q -t5 2>&1 | grep 'IPv6 obtained'
  fi

  lxc exec "${ctPrefix}A" -- ip -6 a flush dev eth0
  lxc exec "${ctPrefix}A" -- ip -6 a add 2001:db8:1::2/64 dev eth0
  wait_for_dad "${ctPrefix}A" eth0

  # Check basic connectivity with IPv6 filtering and real IPs configured.
  lxc exec "${ctPrefix}A" -- ping6 -c2 -W5 2001:db8:1::1
  lxc exec "${ctPrefix}A" -- ping6 -c2 -W5 2001:db8:1::3

  # Add a fake IP
  lxc exec "${ctPrefix}A" -- ip -6 a flush dev eth0
  lxc exec "${ctPrefix}A" -- ip -6 a add 2001:db8:1::254/64 dev eth0
  wait_for_dad "${ctPrefix}A" eth0

  # Check that ping is no longer working (i.e its filtered after fake IP setup).
  if lxc exec "${ctPrefix}A" -- ping6 -c2 -W5 2001:db8:1::1; then
      echo "IPv6 filter not working to host"
      false
  fi

  # Check that ping is no longer working (i.e its filtered after fake IP setup).
  if lxc exec "${ctPrefix}A" -- ping6 -c2 -W5 2001:db8:1::3; then
      echo "IPv6 filter not working to other container"
      false
  fi

  # Add a fake IP within ipv6.routes range (2001:db8:2::/64)
  lxc exec "${ctPrefix}A" -- ip -6 a flush dev eth0
  lxc exec "${ctPrefix}A" -- ip -6 a add 2001:db8:2::1/128 dev eth0
  lxc exec "${ctPrefix}A" -- ip -6 r add 2001:db8:1::/64 dev eth0
  lxc exec "${ctPrefix}B" -- ip -6 r add 2001:db8:2::/64 dev eth0
  wait_for_dad "${ctPrefix}A" eth0

  # Check that ping is still working (i.e the filter did not apply to the ipv6.routes subnet).
  if ! lxc exec "${ctPrefix}A" -- ping6 -c2 -W5 2001:db8:1::1; then
      echo "IPv6 filter is preventing traffic from from within ipv6.routes"
      false
  fi

  # Check that ping is still working (i.e the filter did not apply to the ipv6.routes subnet).
  if ! lxc exec "${ctPrefix}A" -- ping6 -c2 -W5 2001:db8:1::3; then
      echo "IPv6 filter is preventing traffic from within ipv6.routes"
      false
  fi

  # Add a fake IP within ipv6.routes.external range (2001:db8:0:0:2::/96)
  lxc exec "${ctPrefix}A" -- ip -6 a flush dev eth0
  lxc exec "${ctPrefix}A" -- ip -6 a add 2001:db8:3::1/128 dev eth0
  lxc exec "${ctPrefix}B" -- ip -6 r add 2001:db8:3::/64 dev eth0
  wait_for_dad "${ctPrefix}A" eth0

  # Check that ping is still working (i.e the filter did not apply to the ipv6.routes.external subnet).
  if ! lxc exec "${ctPrefix}A" -- ping6 -c2 -W5 2001:db8:1::1; then
      echo "IPv6 filter is preventing traffic from within ipv6.routes.external"
      false
  fi

  # Check that ping is still working (i.e the filter did not apply to the ipv6.routes subnet).
  if ! lxc exec "${ctPrefix}A" -- ping6 -c2 -W5 2001:db8:1::3; then
      echo "IPv6 filter is preventing traffic from within ipv6.routes.external"
      false
  fi

  # Stop CT A and check filters are cleaned up.
  lxc stop -f "${ctPrefix}A"
  if [ "$firewallDriver" = "xtables" ]; then
    if ebtables --concurrent -L --Lmac2 --Lx | grep -e "${ctAHost}" ; then
        echo "IPv6 filter still applied as part of ipv6_filtering in ebtables"
        false
    fi
  else
    for table in "in" "fwd"
    do
      if nft -nn list chain bridge lxd "${table}.${ctPrefix}A.eth0" | grep -e "${ctAHost}"; then
        echo "IPv6 filter still applied as part of ipv4_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
    done
  fi

  # Check volatile cleanup on stop.
  if lxc config show "${ctPrefix}A" | grep volatile.eth0 | grep -v volatile.eth0.hwaddr ; then
    echo "unexpected volatile key remains"
    false
  fi

  # Set static MAC so that SLAAC address is derived predictably and check it is applied to static config.
  lxc config device unset "${ctPrefix}A" eth0 ipv6.address
  lxc config device set "${ctPrefix}A" eth0 hwaddr 00:16:3e:92:f3:c1
  lxc config device set "${ctPrefix}A" eth0 security.ipv6_filtering false
  rm "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/${ctPrefix}A.eth0"
  lxc config device set "${ctPrefix}A" eth0 security.ipv6_filtering true
  lxc start "${ctPrefix}A"
  if ! grep "\\[2001:db8:1:0:216:3eff:fe92:f3c1\\]" "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/${ctPrefix}A.eth0" ; then
    echo "dnsmasq host config doesnt contain dynamically allocated static IPv6 config"
    false
  fi

  lxc stop -f "${ctPrefix}A"
  lxc config device set "${ctPrefix}A" eth0 security.ipv6_filtering false
  rm "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/${ctPrefix}A.eth0"

  # Simulate SLAAC 2001:db8:1::216:3eff:fe92:f3c1 being used by another container, next free IP is 2001:db8:1::2
  kill "$(awk '/^pid/ {print $2}' "${INCUS_DIR}"/networks/"${brName}"/dnsmasq.pid)"
  echo "$(date --date="1hour" +%s) 1875094469 2001:db8:1::216:3eff:fe92:f3c1 c1 00:02:00:00:ab:11:f8:5c:3d:73:db:b2:6a:06" > "${INCUS_DIR}/networks/${brName}/dnsmasq.leases"
  shutdown_incus "${INCUS_DIR}"
  respawn_incus "${INCUS_DIR}" true
  lxc config device set "${ctPrefix}A" eth0 security.ipv6_filtering true
  lxc start "${ctPrefix}A"
  if ! grep "\\[2001:db8:1::2\\]" "${INCUS_DIR}/networks/${brName}/dnsmasq.hosts/${ctPrefix}A.eth0" ; then
    echo "dnsmasq host config doesnt contain sequentially allocated static IPv6 config"
    false
  fi

  lxc stop -f "${ctPrefix}A"
  lxc stop -f "${ctPrefix}B"

  lxc delete -f "${ctPrefix}A"
  lxc delete -f "${ctPrefix}B"

  # Check filtering works when ipv4 and ipv6 addresses are set to none on the nic device and the parent network is managed.
  lxc init testimage "${ctPrefix}A" -p "${ctPrefix}"
  lxc config device add "${ctPrefix}A" eth0 nic \
    name=eth0 \
    nictype=bridged \
    parent="${brName}" \
    security.ipv4_filtering=true \
    security.ipv6_filtering=true \
    ipv4.address=none \
    ipv6.address=none
  lxc start "${ctPrefix}A"
  ctAHost=$(lxc config get "${ctPrefix}A" volatile.eth0.host_name)

  # When IPv{n} addresses are "none", every packet should be dropped.
  if [ "$firewallDriver" = "xtables" ]; then
    ebtables --concurrent -L --Lmac2 --Lx | grep -e "-A INPUT -p ARP -i ${ctAHost} -j DROP"
    ebtables --concurrent -L --Lmac2 --Lx | grep -e "-A FORWARD -p ARP -i ${ctAHost} -j DROP"
    ebtables --concurrent -L --Lmac2 --Lx | grep -e "-A INPUT -p IPv4 -i ${ctAHost} -j DROP"
    ebtables --concurrent -L --Lmac2 --Lx | grep -e "-A FORWARD -p IPv4 -i ${ctAHost} -j DROP"
    ebtables --concurrent -L --Lmac2 --Lx | grep -e "-A INPUT -p IPv6 -i ${ctAHost} -j DROP"
    ebtables --concurrent -L --Lmac2 --Lx | grep -e "-A FORWARD -p IPv6 -i ${ctAHost} -j DROP"
  else
    for table in "in" "fwd"
    do
      nft -nn list chain bridge lxd "${table}.${ctPrefix}A.eth0" | grep -e "iifname \"${ctAHost}\" ether type 0x0806 drop" # ARP
      nft -nn list chain bridge lxd "${table}.${ctPrefix}A.eth0" | grep -e "iifname \"${ctAHost}\" ether type 0x0800 drop" # IPv4
      nft -nn list chain bridge lxd "${table}.${ctPrefix}A.eth0" | grep -e "iifname \"${ctAHost}\" ether type 0x86dd drop" # IPv6
    done
  fi

  lxc delete -f "${ctPrefix}A"

  # Check filtering works with non-DHCP statically defined IPs and a bridge with no IP address and DHCP disabled.
  lxc network set "${brName}" ipv4.dhcp false
  lxc network set "${brName}" ipv4.address none

  lxc network set "${brName}" ipv6.dhcp false
  lxc network set "${brName}" ipv6.address none

  lxc network set "${brName}" ipv6.dhcp.stateful false
  lxc init testimage "${ctPrefix}A" -p "${ctPrefix}"
  lxc config device add "${ctPrefix}A" eth0 nic \
    nictype=nic \
    name=eth0 \
    nictype=bridged \
    parent="${brName}" \
    ipv4.address=192.0.2.2 \
    ipv6.address=2001:db8::2 \
    security.ipv4_filtering=true \
    security.ipv6_filtering=true
  lxc start "${ctPrefix}A"

  # Check MAC filter is present in ebtables.
  ctAHost=$(lxc config get "${ctPrefix}A" volatile.eth0.host_name)
  ctAMAC=$(lxc config get "${ctPrefix}A" volatile.eth0.hwaddr)
  macHex=$(echo "${ctAMAC}" |sed "s/://g")

  if [ "$firewallDriver" = "xtables" ]; then
    # Check MAC filter is present in ebtables.
    if ! ebtables --concurrent -L --Lmac2 --Lx | grep -e "-s ! ${ctAMAC} -i ${ctAHost} -j DROP" ; then
        echo "MAC filter not applied as part of ipv4_filtering in ebtables"
        false
    fi

    # Check MAC NDP filter is present in ip6tables.
    if ! ip6tables -S -w -t filter | grep -e "${macHex}" ; then
        echo "MAC NDP ip6tables filter not applied as part of ipv6_filtering in ip6tables"
        false
    fi

    # Check IPv4 filter is present in ebtables.
    if ! ebtables --concurrent -L --Lmac2 --Lx | grep -e "192.0.2.2" ; then
        echo "IPv4 filter not applied as part of ipv4_filtering in ebtables"
        false
    fi

    # Check IPv6 filter is present in ebtables.
    if ! ebtables --concurrent -L --Lmac2 --Lx | grep -e "2001:db8::2" ; then
        echo "IPv6 filter not applied as part of ipv6_filtering in ebtables"
        false
    fi

    # Check IPv6 filter is present in ip6tables.
    if ! ip6tables -S -w -t filter | grep -e "20010db8000000000000000000000002" ; then
        echo "IPv6 filter not applied as part of ipv6_filtering in ip6tables"
        false
    fi
  else
    macHex=$(echo "${ctAMAC}" |sed "s/://g")
    macDec=$(printf "%d" 0x"${macHex}")
    macHex=$(printf "0x%x" "${macDec}")
    ipv6Hex="0x20010db8000000000000000000000002"
    ipv6Dec="42540766411282592856903984951653826562"

    rules=$(nft -nn list chain bridge lxd "${table}.${ctPrefix}A.eth0")

    for table in "in" "fwd"
    do
      if ! echo "${rules}" | grep "iifname \"${ctAHost}\" ether saddr != ${ctAMAC} drop"; then
        echo "MAC filter not applied as part of ipv4_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
      if ! echo "${rules}" | grep "iifname \"${ctAHost}\" arp saddr ether != ${ctAMAC} drop"; then
        echo "MAC ARP filter not applied as part of ipv4_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
      if ! echo "${rules}" | grep -P "iifname \"${ctAHost}\" icmpv6 type 136 @nh,528,48 != (${macHex}|${macDec}) drop"; then
        echo "MAC NDP filter not applied as part of ipv6_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
      if ! echo "${rules}" | grep -P "iifname \"${ctAHost}\" icmpv6 type 136 @nh,384,128 (${ipv6Hex}|${ipv6Dec}) accept"; then
        echo "IPv6 NDP filter not applied as part of ipv6_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
      if ! echo "${rules}" | grep "iifname \"${ctAHost}\" ip6 saddr 2001:db8::2 accept"; then
        echo "IPv6 filter not applied as part of ipv6_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
    done
  fi

  # Check that you cannot remove static IPs with filtering enabled and DHCP disabled.
  if lxc config device unset "${ctPrefix}A" eth0 ipv4.address ; then
    echo "Shouldn't be able to unset IPv4 address with ipv4_filtering enabled and DHCPv4 disabled"
  fi

  if lxc config device unset "${ctPrefix}A" eth0 ipv6.address ; then
    echo "Shouldn't be able to unset IPv6 address with ipv4_filtering enabled and DHCPv6 disabled"
  fi

  # Delete container and check filters are cleaned up.
  lxc delete -f "${ctPrefix}A"
  if [ "$firewallDriver" = "xtables" ]; then
    if ebtables --concurrent -L --Lmac2 --Lx | grep -e "${ctAHost}" ; then
        echo "ebtables filter still applied after delete"
        false
    fi
  else
    for table in "in" "fwd"
    do
      if nft -nn list chain bridge lxd "${table}.${ctPrefix}A.eth0" | grep -e "${ctAHost}"; then
        echo "nftables filter still applied after delete (${table}.${ctPrefix}A.eth0)"
        false
      fi
    done
  fi

  # Test MAC filtering on unmanaged bridge.
  ip link add "${brName}2" type bridge
  ip a add 192.0.2.1/24 dev "${brName}2"
  ip a add 2001:db8::1/64 dev "${brName}2"
  ip link set "${brName}2" up

  lxc init testimage "${ctPrefix}A" -p "${ctPrefix}"
  lxc config device add "${ctPrefix}A" eth0 nic \
    nictype=nic \
    name=eth0 \
    nictype=bridged \
    parent="${brName}2" \
    security.mac_filtering=true
  lxc start "${ctPrefix}A"

  # Check MAC filter is present in firewall.
  ctAHost=$(lxc config get "${ctPrefix}A" volatile.eth0.host_name)
  ctAMAC=$(lxc config get "${ctPrefix}A" volatile.eth0.hwaddr)

  if [ "$firewallDriver" = "xtables" ]; then
    if ! ebtables --concurrent -L --Lmac2 --Lx | grep -e "-s ! ${ctAMAC} -i ${ctAHost} -j DROP" ; then
        echo "MAC ebtables filter not applied as part of mac_filtering in ebtables"
        false
    fi
  else
    macHex=$(echo "${ctAMAC}" |sed "s/://g")
    macDec=$(printf "%d" 0x"${macHex}")
    macHex=$(printf "0x%x" "${macDec}")

    rules=$(nft -nn list chain bridge lxd "${table}.${ctPrefix}A.eth0")

    for table in "in" "fwd"
    do
      if ! echo "${rules}" | grep "iifname \"${ctAHost}\" ether saddr != ${ctAMAC} drop"; then
        echo "MAC filter not applied as part of mac_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
      if ! echo "${rules}" | grep "iifname \"${ctAHost}\" arp saddr ether != ${ctAMAC} drop"; then
        echo "MAC ARP filter not applied as part of mac_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi

      if ! echo "${rules}" | grep -P "iifname \"${ctAHost}\" icmpv6 type 136 @nh,528,48 != (${macHex}|${macDec}) drop"; then
        echo "MAC NDP filter not applied as part of mac_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
    done
  fi

  # Stop container and check filters are cleaned up.
  lxc stop -f "${ctPrefix}A"
  if [ "$firewallDriver" = "xtables" ]; then
    if ebtables --concurrent -L --Lmac2 --Lx | grep -e "${ctAHost}" ; then
        echo "MAC filter still applied as part of mac_filtering in ebtables"
        false
    fi
  else
    for table in "in" "fwd"
    do
      if nft -nn list chain bridge lxd "${table}.${ctPrefix}A.eth0" | grep -e "${ctAHost}"; then
        echo "MAC filter still applied as part of mac_filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
    done
  fi

  # Check manual IPs cannot be specified on an unmanaged bridged without using IP filtering.
  ! lxc config device set "${ctPrefix}A" eth0 ipv4.address=192.0.2.2 || false
  ! lxc config device set "${ctPrefix}A" eth0 ipv6.address=2001:db8::2 || false

  # Check IP filtering cannot be enabled without manual IP assigned in LXD config.
  ! lxc config device set "${ctPrefix}A" eth0 security.ipv4_filtering=true || false
  lxc config device set "${ctPrefix}A" eth0 ipv4.address=192.0.2.2 security.ipv4_filtering=true
  ! lxc config device set "${ctPrefix}A" eth0 security.ipv6_filtering=true || false
    lxc config device set "${ctPrefix}A" eth0 ipv6.address=2001:db8::2 security.ipv6_filtering=true

  lxc start "${ctPrefix}A"
  lxc exec "${ctPrefix}A" -- ip a add 192.0.2.2/24 dev eth0
  lxc exec "${ctPrefix}A" -- ip a add 2001:db8::2/64 dev eth0

  # Check basic connectivity without any filtering.
  lxc exec "${ctPrefix}A" -- ping -c2 -W5 192.0.2.1
  lxc exec "${ctPrefix}A" -- ping -c2 -W5 2001:db8::1

  # Check fraudulent IPs are blocked.
  lxc exec "${ctPrefix}A" -- ip a flush dev eth0
  lxc exec "${ctPrefix}A" -- ip a add 192.0.2.3/24 dev eth0
  lxc exec "${ctPrefix}A" -- ip a add 2001:db8::3/64 dev eth0

  ! lxc exec "${ctPrefix}A" -- ping -c2 -W5 192.0.2.1 || false
  ! lxc exec "${ctPrefix}A" -- ping -c2 -W5 2001:db8::1 || false

  # Check IP filtering can be enabled with IP assigned as none in LXD config.
  lxc config device set "${ctPrefix}A" eth0 ipv4.address=none security.ipv4_filtering=true
  lxc config device set "${ctPrefix}A" eth0 ipv6.address=none security.ipv6_filtering=true
  lxc exec "${ctPrefix}A" -- ip a flush dev eth0
  lxc exec "${ctPrefix}A" -- ip a add 192.0.2.2/24 dev eth0
  lxc exec "${ctPrefix}A" -- ip a add 2001:db8::2/64 dev eth0
  ! lxc exec "${ctPrefix}A" -- ping -c2 -W5 192.0.2.1 || false
  ! lxc exec "${ctPrefix}A" -- ping -c2 -W5 2001:db8::1 || false

  lxc delete -f "${ctPrefix}A"
  ip link delete "${brName}2"

  # Check filtering works with no IP addresses (total protocol blocking).
  lxc network set "${brName}" ipv4.dhcp false
  lxc network set "${brName}" ipv4.address none
  lxc network set "${brName}" ipv6.dhcp false
  lxc network set "${brName}" ipv6.address none
  lxc network set "${brName}" ipv6.dhcp.stateful false

  lxc init testimage "${ctPrefix}A" -p "${ctPrefix}"
  lxc config device add "${ctPrefix}A" eth0 nic \
    nictype=nic \
    name=eth0 \
    nictype=bridged \
    parent="${brName}" \
    security.ipv4_filtering=true \
    security.ipv6_filtering=true
  lxc start "${ctPrefix}A"
  ctAHost=$(lxc config get "${ctPrefix}A" volatile.eth0.host_name)

  if [ "$firewallDriver" = "xtables" ]; then
    ebtables --concurrent -L --Lmac2 --Lx | grep -e "-A INPUT -p ARP -i ${ctAHost} -j DROP"
    ebtables --concurrent -L --Lmac2 --Lx | grep -e "-A FORWARD -p ARP -i ${ctAHost} -j DROP"
    ebtables --concurrent -L --Lmac2 --Lx | grep -e "-A INPUT -p IPv4 -i ${ctAHost} -j DROP"
    ebtables --concurrent -L --Lmac2 --Lx | grep -e "-A FORWARD -p IPv4 -i ${ctAHost} -j DROP"
    ebtables --concurrent -L --Lmac2 --Lx | grep -e "-A INPUT -p IPv6 -i ${ctAHost} -j DROP"
    ebtables --concurrent -L --Lmac2 --Lx | grep -e "-A FORWARD -p IPv6 -i ${ctAHost} -j DROP"
  else
    for table in "in" "fwd"
    do
      nft -nn list chain bridge lxd "${table}.${ctPrefix}A.eth0" | grep -e "iifname \"${ctAHost}\" ether type 0x0806 drop" # ARP
      nft -nn list chain bridge lxd "${table}.${ctPrefix}A.eth0" | grep -e "iifname \"${ctAHost}\" ether type 0x0800 drop" # IPv4
      nft -nn list chain bridge lxd "${table}.${ctPrefix}A.eth0" | grep -e "iifname \"${ctAHost}\" ether type 0x86dd drop" # IPv6
    done
  fi

  # Delete container and check filters are cleaned up.
  lxc delete -f "${ctPrefix}A"
  if [ "$firewallDriver" = "xtables" ]; then
    if ebtables --concurrent -L --Lmac2 --Lx | grep -e "${ctAHost}" ; then
        echo "Filters still applied as part of IP filter in ebtables"
        false
    fi
  else
    for table in "in" "fwd"
    do
      if nft -nn list chain bridge lxd "${table}.${ctPrefix}A.eth0" | grep -e "${ctAHost}"; then
        echo "Filters still applied as part of IP filtering in nftables (${table}.${ctPrefix}A.eth0)"
        false
      fi
    done
  fi

  # Cleanup.
  lxc profile delete "${ctPrefix}"
  lxc network delete "${brName}"

  # Check we haven't left any NICS lying around.
  endNicCount=$(find /sys/class/net | wc -l)
  if [ "$startNicCount" != "$endNicCount" ]; then
    echo "leftover NICS detected"
    false
  fi
}
