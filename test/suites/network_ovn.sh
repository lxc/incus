network_ovn_supported() {
    if [ -n "${INCUS_OFFLINE:-}" ]; then
        echo "==> SKIP: External connectivity needed to pull test image"
        export TEST_UNMET_REQUIREMENT="external connectivity needed to pull test image"
        return 1
    fi

    if ! command -v ovn-nbctl > /dev/null 2>&1 || ! command -v ovs-vsctl > /dev/null 2>&1; then
        echo "==> SKIP: OVN tools not available"
        export TEST_UNMET_REQUIREMENT="OVN tools not available"
        return 1
    fi

    if ! ovn-nbctl --timeout=10 show > /dev/null 2>&1; then
        echo "==> SKIP: OVN northbound database not available"
        export TEST_UNMET_REQUIREMENT="OVN northbound database not available"
        return 1
    fi

    return 0
}

network_ovn_wait_ping() {
    # OVN can take tens of seconds to program new ACL flows and the first blocked
    # packet of a flow is committed to conntrack as blocked, so retry with fresh flows.
    for _ in 1 2 3 4 5 6; do
        if incus exec "$@"; then
            return 0
        fi

        sleep 2
    done

    return 1
}

test_network_ovn_basic() {
    if ! network_ovn_supported; then
        return
    fi

    poolName=$(incus profile device get default root pool)
    instanceImage="images:debian/13"

    incus network create incusbr0 \
        ipv4.address=10.10.10.1/24 ipv4.nat=true \
        ipv4.dhcp.ranges=10.10.10.2-10.10.10.199 \
        ipv4.ovn.ranges=10.10.10.200-10.10.10.254 \
        ipv6.address=fd42:4242:4242:1010::1/64 ipv6.nat=true \
        ipv6.ovn.ranges=fd42:4242:4242:1010::200-fd42:4242:4242:1010::254

    # Create OVN network without specifying uplink parent network (check default selection works).
    incus network create ovn-virtual-network --type=ovn
    sleep 2
    incus network list | grep ovn-virtual-network

    echo "==> Check we can get the network state (MAC address and MTU are reported in network info)"
    incus network info ovn-virtual-network | grep -P 'MAC address: [0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}'
    incus network info ovn-virtual-network | grep -P 'MTU: \d+'
    incus network info ovn-virtual-network | grep -P "Chassis: $(hostname)"

    echo "==> Check host connectivity to linuxcontainers.org"
    hostHasIPv4Internet=0
    hostHasIPv6Internet=0
    if ping -c1 -4 -w5 linuxcontainers.org; then
        hostHasIPv4Internet=1
    fi

    if ping -c1 -6 -w5 linuxcontainers.org; then
        hostHasIPv6Internet=1
    fi

    echo "==> Launching a test container on incusbr0"
    incus init "${instanceImage}" u1 -s "${poolName}"
    incus config device add u1 eth0 nic network=incusbr0 name=eth0
    incus start u1

    echo "==> Launching a first test container on ovn-virtual-network"
    incus init "${instanceImage}" u2 -s "${poolName}"
    incus config device add u2 eth0 nic network=ovn-virtual-network name=eth0
    incus start u2

    echo "==> Launching a second test container on ovn-virtual-network"
    incus init "${instanceImage}" u3 -s "${poolName}"
    incus config device add u3 eth0 nic network=ovn-virtual-network name=eth0
    incus start u3

    echo "==> Wait for addresses"
    sleep 10
    incus list

    echo "==> Testing connectivity"
    U1_IPV4="$(incus list u1 -c4 --format=csv | cut -d' ' -f1)"
    U1_IPV6="$(incus list u1 -c6 --format=csv | cut -d' ' -f1)"
    U2_IPV4="$(incus list u2 -c4 --format=csv | cut -d' ' -f1)"
    U2_IPV6="$(incus list u2 -c6 --format=csv | cut -d' ' -f1)"
    U3_IPV4="$(incus list u3 -c4 --format=csv | cut -d' ' -f1)"
    U3_IPV6="$(incus list u3 -c6 --format=csv | cut -d' ' -f1)"

    echo "==> incusbr0 to internet"
    if [ "${hostHasIPv4Internet}" = "1" ]; then
        incus exec u1 -- ping -c1 -4 -w5 linuxcontainers.org
    fi

    if [ "${hostHasIPv6Internet}" = "1" ]; then
        incus exec u1 -- ping -c1 -6 -w5 linuxcontainers.org
    fi

    echo "==> incusbr0 to OVN gateway"
    incus exec u1 -- ping -c1 -4 -w5 10.10.10.200
    incus exec u1 -- ping -c1 -6 -w5 fd42:4242:4242:1010::200

    echo "==> OVN to OVN"
    incus exec u2 -- ping -c1 -4 -w5 "${U3_IPV4}"
    incus exec u2 -- ping -c1 -6 -w5 "${U3_IPV6}"

    echo "==> OVN to incusbr0 instance"
    incus exec u3 -- ping -c1 -4 -w5 "${U1_IPV4}"
    incus exec u3 -- ping -c1 -6 -w5 "${U1_IPV6}"

    echo "==> DNS resolution on OVN"
    incus exec u3 -- ping -c1 -4 -w5 u2.incus
    incus exec u3 -- ping -c1 -6 -w5 u2.incus

    echo "==> OVN to incusbr0 gateway"
    incus exec u2 -- ping -c1 -w5 10.10.10.1
    incus exec u2 -- ping -c1 -w5 fd42:4242:4242:1010::1

    echo "==> OVN to internet"
    if [ "${hostHasIPv4Internet}" = "1" ]; then
        incus exec u2 -- ping -c1 -4 -w5 linuxcontainers.org
    fi

    if [ "${hostHasIPv6Internet}" = "1" ]; then
        incus exec u2 -- ping -c1 -6 -w5 linuxcontainers.org
    fi

    # Check sticky DHCPv4 allocations, so that when deleting an instance with a lower dynamic IP that restarting a
    # later instance doesn't cause it to be allocated the (now available) lower IP and instead it should keep the
    # IP it was already allocated. Also check that the newly launched instance that replaces the deleted one claims
    # the lower available IP that the deleted instance was using.
    echo "===> Testing sticky DHCPv4"
    U2_IPV4_OLD=${U2_IPV4}
    U3_IPV4_OLD=${U3_IPV4}

    incus delete -f u2
    incus restart -f u3
    incus init "${instanceImage}" u2 -s "${poolName}"
    incus config device add u2 eth0 nic network=ovn-virtual-network name=eth0
    incus start u2

    echo "==> Wait for new addresses"
    sleep 10
    incus list

    U2_IPV4="$(incus list u2 -c4 --format=csv | cut -d' ' -f1)"
    U2_IPV6="$(incus list u2 -c6 --format=csv | cut -d' ' -f1)"
    U3_IPV4="$(incus list u3 -c4 --format=csv | cut -d' ' -f1)"
    U3_IPV6="$(incus list u3 -c6 --format=csv | cut -d' ' -f1)"

    [ "${U2_IPV4_OLD}" = "${U2_IPV4}" ]
    [ "${U3_IPV4_OLD}" = "${U3_IPV4}" ]

    echo "===> Testing project restrictions"
    incus project create testovn -c features.networks=true -c features.images=false -c restricted=true
    incus profile device add default root disk path=/ pool="${poolName}" --project testovn

    # Test we cannot create network in restricted project with no defined uplinks.
    ! incus network create ovn-virtual-network --project testovn || false

    # Test we can create network with a single restricted uplink network defined without specifying it (or type).
    incus project set testovn restricted.networks.uplinks=incusbr0
    incus network create ovn-virtual-network --project testovn
    incus network delete ovn-virtual-network --project testovn

    # Test we have to specify uplink network if multiple are allowed.
    incus network create incusbr1 --project default
    incus project set testovn restricted.networks.uplinks=incusbr0,incusbr1
    ! incus network create ovn-virtual-network --project testovn || false
    incus network create ovn-virtual-network network=incusbr0 --project testovn
    incus network delete ovn-virtual-network --project testovn
    incus network delete incusbr1 --project default

    # Test networks shared from the default project through restricted.networks.access.
    incus network create incusbr-shared --project default
    incus project set testovn restricted.networks.access=incusbr-shared

    # Shared network is visible in the project.
    incus network list --project testovn | grep -F incusbr-shared
    incus network show incusbr-shared --project testovn

    # Shared network name is reserved in the project.
    ! incus network create incusbr-shared --project testovn || false

    # Shared network can't be modified or deleted from the project.
    ! incus network set incusbr-shared user.foo=bar --project testovn || false
    ! incus network delete incusbr-shared --project testovn || false

    # Shared network can be used by instances in the project.
    incus init "${instanceImage}" u-shared --project testovn -s "${poolName}"
    incus config device add u-shared eth0 nic network=incusbr-shared name=eth0 --project testovn

    # Usage from the sharing project is visible and blocks deletion.
    incus network show incusbr-shared --project default | grep -F "/1.0/instances/u-shared?project=testovn"
    ! incus network delete incusbr-shared --project default || false

    incus delete -f u-shared --project testovn

    # Names of project networks can't be added to restricted.networks.access.
    incus network create ovn-virtual-network network=incusbr0 --project testovn
    ! incus project set testovn restricted.networks.access=incusbr-shared,ovn-virtual-network || false
    incus network delete ovn-virtual-network --project testovn

    incus project unset testovn restricted.networks.access
    incus network delete incusbr-shared --project default

    # Test physical uplink with external IPs.
    ip link add dummy0 type dummy
    incus network create dummy --type=physical --project default \
        parent=dummy0 \
        ipv4.gateway=192.0.2.1/24 \
        ipv6.gateway=2001:db8:1:1::1/64 \
        ipv4.ovn.ranges=192.0.2.10-192.0.2.19 \
        ipv4.routes=198.51.100.0/24 \
        ipv6.routes=2001:db8:1:2::/64 \
        dns.nameservers=192.0.2.53

    # Test using external subnets using physical uplink.
    incus project set testovn restricted.networks.uplinks=dummy
    incus network create ovn-virtual-network --type=ovn --project testovn network=dummy \
        ipv4.address=198.51.100.1/24 \
        ipv6.address=2001:db8:1:2::1/64 \
        ipv4.nat=false \
        ipv6.nat=false

    # Check network external subnet overlap is prevented.
    ! incus network create ovn-virtual-network2 --type=ovn --project default network=dummy \
        ipv4.address=198.51.100.1/26 \
        ipv4.nat=false || false

    # Check uplink dns.nameservers changes are applied to dependent OVN networks.
    ovn-nbctl --format=csv --bare --column=options find dhcp_option cidr=198.51.100.0/24 | grep -F "dns_server={192.0.2.53}"
    incus network set dummy dns.nameservers=192.0.2.54 --project default
    ovn-nbctl --format=csv --bare --column=options find dhcp_option cidr=198.51.100.0/24 | grep -F "dns_server={192.0.2.54}"
    incus network set dummy dns.nameservers=192.0.2.53 --project default

    # Check network external subnet overlap check relaxation when uplink has anycast routed ingress mode enabled.
    incus network set dummy ovn.ingress_mode=routed ipv4.routes.anycast=true ipv6.routes.anycast=true --project default

    incus network create ovn-virtual-network2 --type=ovn --project default network=dummy \
        ipv4.address=198.51.100.1/26 \
        ipv4.nat=false

    incus network delete ovn-virtual-network2 --project default
    incus network unset dummy ovn.ingress_mode --project default
    incus network unset dummy ipv4.routes.anycast --project default
    incus network unset dummy ipv6.routes.anycast --project default

    incus init "${instanceImage}" u1 --project testovn -s "${poolName}"
    incus config device add u1 eth0 nic network=ovn-virtual-network name=eth0 --project testovn

    incus start u1 --project testovn

    # Test external IPs allocated and published using ARP proxy.
    sleep 5
    U1_EXT_IPV4="$(incus list u1 --project testovn -c4 --format=csv | cut -d' ' -f1)"
    U1_EXT_IPV6="$(incus list u1 --project testovn -c6 --format=csv | cut -d' ' -f1)"
    ovn-nbctl --bare --format=csv --column=options find logical_switch_port | grep -F "${U1_EXT_IPV4}/32"
    ovn-nbctl --bare --format=csv --column=options find logical_switch_port | grep -F "${U1_EXT_IPV6}/128"
    incus stop -f u1 --project testovn

    # Check ARP proxy entries got cleaned up.
    ovn-nbctl --bare --format=csv --column=options find logical_switch_port | grep -cF arp_proxy | grep -xF 0

    # Test external IPs routed to OVN NIC.
    incus network set ovn-virtual-network --project testovn \
        ipv4.address=auto \
        ipv6.address=auto \
        ipv4.nat=true \
        ipv6.nat=true

    # Record NAT rules count before u1 started again.
    natRulesBefore=$(ovn-nbctl --bare --format=csv --column=external_ip,logical_ip,type find nat | wc -l)

    # Check external routes are not too big (when using l2proxy uplink ingress mode).
    ! incus config device set u1 eth0 ipv4.routes.external=198.51.100.0/24 --project testovn || false
    ! incus config device set u1 eth0 ipv6.routes.external=2001:db8:1:2::/64 --project testovn || false

    # Check external routes are ensured to be within uplink's external routes.
    ! incus config device set u1 eth0 ipv4.routes.external=203.0.113.0/26 --project testovn || false
    ! incus config device set u1 eth0 ipv6.routes.external=2001:db8:2:2::/122  --project testovn || false
    incus config device set u1 eth0 ipv4.routes.external=198.51.100.0/26 --project testovn
    incus config device set u1 eth0 ipv6.routes.external=2001:db8:1:2::/122 --project testovn

    # Check NIC external route overlap detection.
    incus init "${instanceImage}" u2 --project testovn -s "${poolName}"
    incus config device add u2 eth0 nic network=ovn-virtual-network name=eth0 --project testovn
    ! incus config device set u2 eth0 ipv4.routes.external=198.51.100.1/32 --project testovn || false
    ! incus config device set u2 eth0 ipv6.routes.external=2001:db8:1:2::1/128 --project testovn || false

    # Check NIC external route overlap check relaxation when uplink has anycast routed ingress mode enabled.
    incus network set dummy ovn.ingress_mode=routed ipv4.routes.anycast=true ipv6.routes.anycast=true --project default
    incus config device set u2 eth0 ipv4.routes.external=198.51.100.1/32 --project testovn
    incus config device set u2 eth0 ipv6.routes.external=2001:db8:1:2::1/128 --project testovn
    incus delete -f u2 --project testovn
    incus network unset dummy ovn.ingress_mode --project default
    incus network unset dummy ipv4.routes.anycast --project default
    incus network unset dummy ipv6.routes.anycast --project default

    # Check ARP proxy entries get added when starting instance port with external routes.
    incus start u1 --project testovn
    ovn-nbctl --bare --format=csv --column=options find logical_switch_port | grep -F "arp_proxy=198.51.100.0/26 2001:db8:1:2::/122"

    # Add internal static route to instance NIC.
    incus config device set u1 eth0 ipv4.routes=203.0.113.1/32 ipv6.routes=2001:db8:2:2::1/128 --project testovn

    # Check static routes get added when starting instance port with external and internal routes, and that they remain
    # when the network is modified while the instance NIC is running.
    ovn-nbctl --bare --format=csv --column=ip_prefix,nexthop find logical_router_static_route | grep -F "198.51.100.0/26,"
    ovn-nbctl --bare --format=csv --column=ip_prefix,nexthop find logical_router_static_route | grep -F "2001:db8:1:2::/122,"
    ovn-nbctl --bare --format=csv --column=ip_prefix,nexthop find logical_router_static_route | grep -F "203.0.113.1/32,"
    ovn-nbctl --bare --format=csv --column=ip_prefix,nexthop find logical_router_static_route | grep -F "2001:db8:2:2::1/128,"
    incus network set ovn-virtual-network --project testovn ipv4.dhcp=false
    ovn-nbctl --bare --format=csv --column=ip_prefix,nexthop find logical_router_static_route | grep -F "198.51.100.0/26,"
    ovn-nbctl --bare --format=csv --column=ip_prefix,nexthop find logical_router_static_route | grep -F "2001:db8:1:2::/122,"
    ovn-nbctl --bare --format=csv --column=ip_prefix,nexthop find logical_router_static_route | grep -F "203.0.113.1/32,"
    ovn-nbctl --bare --format=csv --column=ip_prefix,nexthop find logical_router_static_route | grep -F "2001:db8:2:2::1/128,"

    # Check instance can be started with DHCP disabled when using device routes.
    # We expect OVN to still allow dynamic IPs to the switch port so that device routes can work correctly.
    incus network set ovn-virtual-network --project testovn ipv4.dhcp=false ipv6.dhcp=false
    incus stop -f u1 --project testovn
    incus start u1 --project testovn
    incus stop -f u1 --project testovn
    incus network unset ovn-virtual-network --project testovn ipv4.dhcp
    incus network unset ovn-virtual-network --project testovn ipv6.dhcp
    incus start u1 --project testovn

    # Check ARP proxy entries get removed when switching to routed ingress mode.
    incus network set dummy ovn.ingress_mode=routed
    ovn-nbctl --bare --format=csv --column=options find logical_switch_port | grep -cF arp_proxy | grep -xF 0

    # Check ARP proxy entries are re-added when switching to l2proxy ingress mode.
    incus network unset dummy ovn.ingress_mode
    ovn-nbctl --bare --format=csv --column=options find logical_switch_port | grep -F "arp_proxy=198.51.100.0/26 2001:db8:1:2::/122"

    incus stop -f u1 --project testovn

    # Check ARP proxy entries got cleaned up.
    ovn-nbctl --bare --format=csv --column=options find logical_switch_port | grep -cF arp_proxy | grep -xF 0

    # Check routed ingress mode allows larger subnets and doesn't add ARP proxy entries.
    incus network set dummy ovn.ingress_mode=routed
    incus config device set u1 eth0 ipv4.routes.external=198.51.100.0/24 --project testovn
    incus config device set u1 eth0 ipv6.routes.external=2001:db8:1:2::/64 --project testovn
    incus start u1 --project testovn

    # Check no NAT rules or ARP proxy entries got added.
    natRulesAfter=$(ovn-nbctl --bare --format=csv --column=external_ip,logical_ip,type find nat | wc -l)
    if [ "$natRulesBefore" -ne "$natRulesAfter" ]; then
        echo "NAT rules got added in routed ingress mode. Started with ${natRulesBefore} now have ${natRulesAfter}"
        false
    fi

    ovn-nbctl --bare --format=csv --column=options find logical_switch_port | grep -cF arp_proxy | grep -xF 0

    incus delete -f u1 --project testovn
    incus network unset dummy ovn.ingress_mode

    # Set custom domain to allow identification of DHCP options.
    incus network set ovn-virtual-network dns.domain=testdhcp --project testovn

    # Look for DHCP options mentioning our testdhcp domain name, there should be two.
    ovn-nbctl --format=csv --no-headings --data=bare --colum=_uuid,options find dhcp_options | grep -cF testdhcp | grep -xF 2

    # Only enable IPv6 DHCP.
    incus init "${instanceImage}" u1 --project testovn -s "${poolName}"
    incus network set ovn-virtual-network ipv4.dhcp=false ipv6.dhcp=true --project testovn

    # Look for DHCP options mentioning our testdhcp domain name, there should be one.
    ovn-nbctl --format=csv --no-headings --data=bare --colum=_uuid,options find dhcp_options | grep -cF testdhcp | grep -xF 1

    # Check container can start with IPv4 DHCP disabled.
    incus start u1 --project testovn
    incus stop -f u1 --project testovn

    # Only enable IPv4 DHCP.
    incus network set ovn-virtual-network ipv4.dhcp=true ipv6.dhcp=false --project testovn

    # Look for DHCP options mentioning our testdhcp domain name, there should be one.
    ovn-nbctl --format=csv --no-headings --data=bare --colum=_uuid,options find dhcp_options | grep -cF testdhcp | grep -xF 1

    # Check container can start with IPv6 DHCP disabled.
    incus start u1 --project testovn
    incus delete -f u1 --project testovn

    # Disable both IPv4 and IPv6 DHCP.
    incus network set ovn-virtual-network ipv4.dhcp=false ipv6.dhcp=false --project testovn

    # Look for DHCP options mentioning our testdhcp domain name, there shouldn't be any.
    ovn-nbctl --format=csv --no-headings --data=bare --colum=_uuid,options find dhcp_options | grep -cF testdhcp | grep -xF 0

    incus network delete ovn-virtual-network --project testovn
    incus project delete testovn

    incus network delete dummy --project default
    ip link delete dummy0

    echo "===> Testing projects"
    incus project create testovn -c features.networks=true -c features.images=false -c limits.networks=1
    incus project switch testovn
    incus profile device add default root disk path=/ pool="${poolName}"

    # Create network inside project with same name and subnet as network in default project.
    incus network create ovn-virtual-network network=incusbr0 --type=ovn \
        ipv4.address="$(incus network get ovn-virtual-network ipv4.address --project default)" ipv4.nat=true \
        ipv6.address="$(incus network get ovn-virtual-network ipv6.address --project default)" ipv6.nat=true
    sleep 2

    # Test we cannot exceed specified project limits for networks.
    ! incus network create ovn-virtual-network-toomany network=incusbr0 --type=ovn || false

    echo "==> Launching a first test container on testovn project ovn-virtual-network"
    incus init "${instanceImage}" u2 -s "${poolName}"
    incus config device add u2 eth0 nic network=ovn-virtual-network name=eth0
    incus start u2

    echo "==> Launching a second test container on testovn project ovn-virtual-network"
    incus init "${instanceImage}" u3 -s "${poolName}"
    incus config device add u3 eth0 nic network=ovn-virtual-network name=eth0
    incus start u3

    echo "==> Wait for addresses"
    sleep 10
    incus list

    echo "==> Testing connectivity"

    U3_IPV4="$(incus list u3 -c4 --format=csv | cut -d' ' -f1)"
    U3_IPV6="$(incus list u3 -c6 --format=csv | cut -d' ' -f1)"

    echo "==> incusbr0 to OVN gateway in project testovn"
    incus exec u1 --project default -- ping -c1 -w5 -4 10.10.10.201
    incus exec u1 --project default -- ping -c1 -w5 -6 fd42:4242:4242:1010::201

    echo "==> OVN to OVN in project testovn"
    incus exec u2 -- ping -c1 -4 "${U3_IPV4}"
    incus exec u2 -- ping -c1 -6 "${U3_IPV6}"

    echo "==> OVN to incusbr0 instance in project testovn"
    incus exec u3 -- ping -c1 -4 "${U1_IPV4}"
    incus exec u3 -- ping -c1 -6 "${U1_IPV6}"

    echo "==> DNS resolution on OVN in project testovn"
    incus exec u3 -- ping -c1 -4 u2.incus
    incus exec u3 -- ping -c1 -6 u2.incus

    echo "==> OVN to incusbr0 gateway in project testovn"
    incus exec u2 -- ping -c1 10.10.10.1
    incus exec u2 -- ping -c1 fd42:4242:4242:1010::1

    echo "==> OVN to internet in project testovn"
    if [ "${hostHasIPv4Internet}" = "1" ]; then
        incus exec u2 -- ping -c1 -4 -w5 linuxcontainers.org
    fi

    if [ "${hostHasIPv6Internet}" = "1" ]; then
        incus exec u2 -- ping -c1 -6 -w5 linuxcontainers.org
    fi

    echo "===> Check network in use protection from deletion"
    # Delete instances in default project first.
    incus delete -f u1 u2 u3 --project default

    # Check we cannot delete incusbr0 (as it is parent of OVN networks).
    ! incus network delete incusbr0 --project default || false

    # Delete OVN network in default project.
    incus network delete ovn-virtual-network --project default

    # Check we cannot delete incusbr0 (as it is still parent of OVN network in project).
    ! incus network delete incusbr0 --project default || false

    # Check we cannot delete OVN network in project due to instances using it.
    ! incus network delete ovn-virtual-network || false

    # Remove instances using OVN network in project.
    incus delete -f u2 u3

    # Delete OVN network in project and parent in default project.
    incus network delete ovn-virtual-network
    incus network delete incusbr0 --project default

    # Test physical uplinks using native bridge.
    incus project switch default
    ip link add dummybr0 type bridge # Create dummy uplink bridge.
    ip address add 192.0.2.1/24 dev dummybr0
    ip address add 2001:db8:1:1::1/64 dev dummybr0
    ip link set dummybr0 up
    incus network create dummy --type=physical \
        parent=dummybr0 \
        ipv4.gateway=192.0.2.1/24 \
        ipv6.gateway=2001:db8:1:1::1/64 \
        ipv4.ovn.ranges=192.0.2.10-192.0.2.19
    incus network create ovn-virtual-network --type=ovn network=dummy
    sleep 2
    bridge link show | grep -cF dummybr0 | grep -xF 1 # Check we have one port connected to the uplink bridge.
    ovs-vsctl list-br | grep -cF ovn | grep -xF 1 # Check we have one OVS bridge.
    ovnIPv4="$(incus network get ovn-virtual-network volatile.network.ipv4.address)"
    ovnIPv6="$(incus network get ovn-virtual-network volatile.network.ipv6.address)"
    ping -c1 -4 "${ovnIPv4}" # Check IPv4 connectivity over dummy bridge to OVN router.
    ping -c1 -6 "${ovnIPv6}" # Check IPv6 connectivity over dummy bridge to OVN router.
    incus network delete ovn-virtual-network
    incus network delete dummy
    bridge link show | grep -cF dummybr0 | grep -xF 0 # Check the port is removed from the uplink bridge.
    ovs-vsctl list-br | grep -cF ovn | grep -xF 0 # Check the OVS bridge is removed.
    ip link delete dummybr0 # Remove dummy uplink bridge.

    # Test physical uplinks using OVS bridge.
    ovs-vsctl add-br dummybr0 # Create dummy uplink bridge.
    ip address add 192.0.2.1/24 dev dummybr0
    ip address add 2001:db8:1:1::1/64 dev dummybr0
    ip link set dummybr0 up
    incus network create dummy --type=physical \
        parent=dummybr0 \
        ipv4.gateway=192.0.2.1/24 \
        ipv6.gateway=2001:db8:1:1::1/64 \
        ipv4.ovn.ranges=192.0.2.10-192.0.2.19
    incus network create ovn-virtual-network --type=ovn network=dummy
    sleep 2
    ovs-vsctl list-ports dummybr0 | grep -cF patch-incus-net | grep -xF 1 # Check bridge has an OVN patch port connected.
    ovnIPv4="$(incus network get ovn-virtual-network volatile.network.ipv4.address)"
    ovnIPv6="$(incus network get ovn-virtual-network volatile.network.ipv6.address)"
    ping -c1 -4 "${ovnIPv4}" # Check IPv4 connectivity over dummy bridge to OVN router.
    ping -c1 -6 "${ovnIPv6}" # Check IPv6 connectivity over dummy bridge to OVN router.
    incus network delete ovn-virtual-network
    incus network delete dummy
    ovs-vsctl list-ports dummybr0 | grep -cF patch-incus-net | grep -xF 0 # Check bridge has no OVN patch port connected.
    ovs-vsctl del-br dummybr0 # Remove dummy uplink bridge.

    # Test external SNAT address for network.
    ip link add dummybr0 type bridge # Create dummy uplink bridge.
    ip address add 192.0.2.1/24 dev dummybr0
    ip address add 2001:db8:1:1::1/64 dev dummybr0
    ip link set dummybr0 up
    incus network create dummy --type=physical \
        parent=dummybr0 \
        ipv4.gateway=192.0.2.1/24 \
        ipv6.gateway=2001:db8:1:1::1/64 \
        ipv4.ovn.ranges=192.0.2.10-192.0.2.19 \
        ipv4.routes=198.51.100.0/24 \
        ipv6.routes=2001:db8:1:2::/64

    incus network create ovn-virtual-network --type=ovn network=dummy

    # Check connectivity back to uplink bridge without SNAT address specified.
    incus launch "${instanceImage}" u1 -n ovn-virtual-network -s "${poolName}"
    sleep 5
    incus exec u1 -- ping -c1 -w5 192.0.2.1
    incus exec u1 -- ping -c1 -w5 2001:db8:1:1::1

    # Set external SNAT address on OVN network (and check it only is allowed with uplink routed ingress mode).
    ! incus network set ovn-virtual-network ipv4.nat.address=198.51.100.1 || false
    ! incus network set ovn-virtual-network ipv6.nat.address=2001:db8:1:2::1 || false
    incus network set dummy ovn.ingress_mode=routed
    incus network set ovn-virtual-network ipv4.nat.address=198.51.100.1
    incus network set ovn-virtual-network ipv6.nat.address=2001:db8:1:2::1
    ovn-nbctl list nat | grep -F 198.51.100.1
    ovn-nbctl list nat | grep -F 2001:db8:1:2::1

    # Add a static route to SNAT address on uplink to OVN network's router (simulating BGP route advert to uplink).
    ovnIPv4="$(incus network get ovn-virtual-network volatile.network.ipv4.address)"
    ovnIPv6="$(incus network get ovn-virtual-network volatile.network.ipv6.address)"
    ip r add 198.51.100.1/32 via "${ovnIPv4}" dev dummybr0
    ip r add 2001:db8:1:2::1/128 via "${ovnIPv6}" dev dummybr0

    # Check connectivity back to uplink bridge with SNAT address specified.
    incus exec u1 -- ping -c1 -w5 192.0.2.1
    incus exec u1 -- ping -c1 -w5 2001:db8:1:1::1

    # Remove the routes and check that connectivity fails (indicating that SNAT is indeed rewriting packet source).
    ip r del 198.51.100.1/32 via "${ovnIPv4}" dev dummybr0
    ip r del 2001:db8:1:2::1/128 via "${ovnIPv6}" dev dummybr0
    ! incus exec u1 -- ping -c1 -w5 192.0.2.1 || false
    ! incus exec u1 -- ping -c1 -w5 2001:db8:1:1::1 || false

    # Check a NIC in the same OVN network can use a subnet containing the SNAT address in its external routes.
    incus config device set u1 eth0 ipv4.routes.external=198.51.100.0/24
    incus config device set u1 eth0 ipv6.routes.external=2001:db8:1:2::/64
    incus config device unset u1 eth0 ipv4.routes.external
    incus config device unset u1 eth0 ipv6.routes.external

    # Create another OVN network and check it cannot use an external subnet containing the SNAT address of 1st network.
    incus network create ovn-virtual-network2 --type=ovn network=dummy
    ! incus network set ovn-virtual-network2 ipv4.nat.address=198.51.100.1 || false
    ! incus network set ovn-virtual-network2 ipv6.nat.address=2001:db8:1:2::1 || false
    incus network delete ovn-virtual-network2

    # Remove external SNAT address and check normal SNAT operation returns connectivity.
    incus network unset ovn-virtual-network ipv4.nat.address
    incus network unset ovn-virtual-network ipv6.nat.address
    incus exec u1 -- networkctl up eth0
    sleep 5
    incus exec u1 -- ping -c1 -w5 192.0.2.1
    incus exec u1 -- ping -c1 -w5 2001:db8:1:1::1

    incus delete -f u1
    incus network delete ovn-virtual-network
    incus network delete dummy
    ip link delete dummybr0 # Remove dummy uplink bridge.

    incus project switch default
    incus project delete testovn
}

test_network_ovn_forward() {
    if ! network_ovn_supported; then
        return
    fi

    poolName=$(incus profile device get default root pool)
    instanceImage="images:debian/13"

    # Create incusbr0 bridge to act as uplink network.
    incus network create incusbr0 \
        ipv4.address=10.10.10.1/24 ipv4.nat=true \
        ipv4.dhcp.ranges=10.10.10.2-10.10.10.199 \
        ipv4.ovn.ranges=10.10.10.200-10.10.10.254 \
        ipv6.address=fd42:4242:4242:1010::1/64 ipv6.nat=true \
        ipv6.ovn.ranges=fd42:4242:4242:1010::200-fd42:4242:4242:1010::254

    # Create OVN network using physical uplink network.
    incus project create testovn -c features.networks=true -c features.images=false
    incus project switch testovn
    incus network create ovn-virtual-network --type=ovn network=incusbr0
    sleep 2
    incus network show ovn-virtual-network
    ip4AddressPrefix=$(incus network get ovn-virtual-network ipv4.address | sed 's/\.1\/24$//g')
    ip6AddressPrefix=$(incus network get ovn-virtual-network ipv6.address | sed 's/::1\/64$//g')

    # Check no forward can be created with a listen address set that is not in the uplink's routes.
    ! incus network forward create ovn-virtual-network 192.0.2.1 target_address="${ip4AddressPrefix}.2" || false
    ! incus network forward create ovn-virtual-network 2001:db8:1:2::1 target_address="${ip6AddressPrefix}.2" || false

    # Add routes to the uplink network and check we can create forwards.
    incus network set incusbr0 ipv4.routes=192.0.2.0/24 ipv6.routes=2001:db8:1:2::/64 --project=default

    # Add a static route to SNAT address on uplink to OVN network's router (simulating BGP route advert to uplink).
    ovnIPv4="$(incus network get ovn-virtual-network volatile.network.ipv4.address)"
    ovnIPv6="$(incus network get ovn-virtual-network volatile.network.ipv6.address)"
    ip r add 192.0.2.1/32 via "${ovnIPv4}" dev incusbr0
    ip r add 2001:db8:1:2::1/128 via "${ovnIPv6}" dev incusbr0

    # Check forwards can be created with no target address.
    incus network forward create ovn-virtual-network 192.0.2.1
    incus network forward create ovn-virtual-network 2001:db8:1:2::1
    ovn-nbctl list load_balancer | grep -cF name | grep -xF 0
    ovn-nbctl --bare --columns=type find nat type=dnat | grep -cF dnat | grep -xF 0

    # Check load balancer cannot be created on same listen address as forward.
    ! incus network load-balancer create ovn-virtual-network 192.0.2.1 || false
    ! incus network load-balancer create ovn-virtual-network 2001:db8:1:2::1 || false

    # Check user config is allowed, but unknown keys are not.
    incus network forward set ovn-virtual-network 192.0.2.1 user.foo=bar
    incus network forward show ovn-virtual-network 192.0.2.1 | grep 'user.foo: bar'
    ! incus network forward set ovn-virtual-network 192.0.2.1 foo=bar || false

    # Check basic forward default target address can be set (to non-existent target address).
    incus network forward set ovn-virtual-network 192.0.2.1 target_address="${ip4AddressPrefix}.2"
    incus network forward set ovn-virtual-network 2001:db8:1:2::1  target_address="${ip6AddressPrefix}::2"
    ovn-nbctl list load_balancer | grep -cF name | grep -xF 0
    ovn-nbctl --bare --columns=type find nat type=dnat | grep -cF dnat | grep -xF 2
    incus network forward delete ovn-virtual-network 192.0.2.1
    incus network forward delete ovn-virtual-network 2001:db8:1:2::1
    ovn-nbctl list load_balancer | grep -cF name | grep -xF 0
    ovn-nbctl --bare --columns=type find nat type=dnat | grep -cF dnat | grep -xF 0

    # Check basic forward can be created with target address (to non-existent target address).
    incus network forward create ovn-virtual-network 192.0.2.1 target_address="${ip4AddressPrefix}.2"
    incus network forward create ovn-virtual-network 2001:db8:1:2::1 target_address="${ip6AddressPrefix}::2"
    ovn-nbctl list load_balancer | grep -cF name | grep -xF 0
    ovn-nbctl --bare --columns=type find nat type=dnat | grep -cF dnat | grep -xF 2

    # Check duplicate forwards cannot be created.
    ! incus network forward create ovn-virtual-network 192.0.2.1 target_address="${ip4AddressPrefix}.2" || false
    ! incus network forward create ovn-virtual-network 2001:db8:1:2::1 target_address="${ip6AddressPrefix}::2" || false

    # Check a forward on another ovn network connected to same uplink cannot conflict with an existing forward.
    incus network create ovn-virtual-network2 --type=ovn network=incusbr0
    ip4AddressPrefix2=$(incus network get ovn-virtual-network2 ipv4.address | sed 's/\.1\/24$//g')
    ip6AddressPrefix2=$(incus network get ovn-virtual-network2 ipv6.address | sed 's/::1\/64$//g')

    ! incus network forward create ovn-virtual-network2 192.0.2.1 target_address="${ip4AddressPrefix2}.2" || false
    ! incus network forward create ovn-virtual-network2 2001:db8:1:2::1 target_address="${ip6AddressPrefix2}::2" || false
    incus network forward create ovn-virtual-network2 192.0.2.2 target_address="${ip4AddressPrefix2}.2"
    incus network forward create ovn-virtual-network2 2001:db8:1:2::2 target_address="${ip6AddressPrefix2}::2"

    # Check a forward on the uplink bridge network cannot conflict with an existing OVN network forward.
    ! incus network forward create incusbr0 192.0.2.1 --project default || false
    ! incus network forward create incusbr0 2001:db8:1:2::1 --project default || false
    incus network forward create incusbr0 192.0.2.3 --project default || false
    incus network forward create incusbr0 2001:db8:1:2::3 --project default || false
    ! incus network forward create ovn-virtual-network2 192.0.2.3 target_address="${ip4AddressPrefix2}.2" || false
    ! incus network forward create ovn-virtual-network2 2001:db8:1:2::3 target_address="${ip6AddressPrefix2}::2" || false
    incus network forward delete incusbr0 192.0.2.3 --project default
    incus network forward delete incusbr0 2001:db8:1:2::3 --project default
    incus network forward create ovn-virtual-network2 192.0.2.3 target_address="${ip4AddressPrefix2}.2" || false
    incus network forward create ovn-virtual-network2 2001:db8:1:2::3 target_address="${ip6AddressPrefix2}::2" || false
    incus network forward delete ovn-virtual-network2 192.0.2.3
    incus network forward delete ovn-virtual-network2 2001:db8:1:2::3

    # Check an external route on another network's NIC prevents another network from setting up a forward listener.
    incus init "${instanceImage}" u1 -n ovn-virtual-network2 -s "${poolName}"
    incus config device set u1 eth0 \
        ipv4.routes.external=192.0.2.3/32,192.0.2.4/32 \
        ipv6.routes.external=2001:db8:1:2::3/128,2001:db8:1:2::4/128
    ! incus network forward create ovn-virtual-network 192.0.2.3 || false
    ! incus network forward create ovn-virtual-network 2001:db8:1:2::3 || false
    incus delete -f u1
    incus network forward create ovn-virtual-network 192.0.2.3
    incus network forward create ovn-virtual-network 2001:db8:1:2::3

    # Check changing network's subnet(s) are prevented if forwards exist that conflict.
    ! incus network set ovn-virtual-network2 ipv4.address=192.168.0.1/24 || false
    ! incus network set ovn-virtual-network2 ipv6.address=2001:db8:1:3::1/64 || false

    # Check network forwards are removed when network is removed.
    ovn-nbctl list load_balancer | grep -cF name | grep -xF 0
    ovn-nbctl --bare --columns=type find nat type=dnat | grep -cF dnat | grep -xF 4
    incus network delete ovn-virtual-network2
    ovn-nbctl list load_balancer | grep -cF name | grep -xF 0
    ovn-nbctl --bare --columns=type find nat type=dnat | grep -cF dnat | grep -xF 2

    # Create instance connected to ovn-virtual-network.
    incus launch "${instanceImage}" u1 -n ovn-virtual-network -s "${poolName}"
    sleep 5
    incus list
    U1_IPV4="$(incus list u1 -c4 --format=csv | cut -d' ' -f1)"
    U1_IPV6="$(incus list u1 -c6 --format=csv | cut -d' ' -f1)"

    incus network forward delete ovn-virtual-network 192.0.2.1
    incus network forward delete ovn-virtual-network 2001:db8:1:2::1

    # Check IP is unused and not working.
    ! ping -c1 -4 -w5 192.0.2.1 || false
    ! ping -c1 -6 -w5 2001:db8:1:2::1 || false

    # Add forward to u1 and test working.
    incus network forward create ovn-virtual-network 192.0.2.1 target_address="${U1_IPV4}"
    incus network forward create ovn-virtual-network 2001:db8:1:2::1 target_address="${U1_IPV6}"
    sleep 2

    # Check forward is working by pinging it.
    # Relies on static route (above) rather than neighbour adverts see https://github.com/ovn-org/ovn/issues/124
    ping -c1 -4 -w5 192.0.2.1
    ping -c1 -6 -w5 2001:db8:1:2::1

    # Check DNS TCP forwarding using default target rule.
    incus exec u1 -- systemctl mask dnsmasq
    incus exec u1 -- apt-get update
    incus exec u1 -- apt-get install --no-install-recommends --yes dnsmasq
cat <<EOF | incus exec u1 -- tee /etc/dnsmasq.d/incus_test.conf
interface=eth0
bind-interfaces
interface-name=u1.incus,eth0
EOF

    incus exec u1 -- systemctl unmask dnsmasq
    incus exec u1 -- systemctl start dnsmasq
    dig a +tcp @192.0.2.1 u1.incus
    dig aaaa +tcp @2001:db8:1:2::1 u1.incus

    # Check stopping the DNS service and checking we get no connection using default target rule.
    incus exec u1 -- systemctl stop dnsmasq
    ! dig a +tcp @192.0.2.1 u1.incus || false
    ! dig aaaa +tcp @2001:db8:1:2::1 u1.incus || false

    # Check TCP port forward of port 53 to a different host.
    incus launch "${instanceImage}" u2 -n ovn-virtual-network -s "${poolName}"
    sleep 5
    incus list
    U2_IPV4="$(incus list u2 -c4 --format=csv | cut -d' ' -f1)"
    U2_IPV6="$(incus list u2 -c6 --format=csv | cut -d' ' -f1)"

    incus exec u2 -- systemctl mask dnsmasq
    incus exec u2 -- apt-get update
    incus exec u2 -- apt-get install --no-install-recommends --yes dnsmasq
cat <<EOF | incus exec u2 -- tee /etc/dnsmasq.d/incus_test.conf
interface=eth0
bind-interfaces
interface-name=u2.incus,eth0
EOF

    incus exec u2 -- systemctl unmask dnsmasq
    incus exec u2 -- systemctl start dnsmasq

    incus network forward port add ovn-virtual-network 192.0.2.1 tcp 53 "${U2_IPV4}"
    incus network forward port add ovn-virtual-network 2001:db8:1:2::1 tcp 53 "${U2_IPV6}"

    dig a +tcp @192.0.2.1 u2.incus
    dig aaaa +tcp @2001:db8:1:2::1 u2.incus

    # Remove port forward and check it reverts to default forward rule (which shouldn't work as dnsmasq is off).
    incus network forward port remove ovn-virtual-network 192.0.2.1 tcp 53
    incus network forward port remove ovn-virtual-network 2001:db8:1:2::1 tcp 53
    ! dig a +tcp @192.0.2.1 u1.incus || false
    ! dig aaaa +tcp @2001:db8:1:2::1 u1.incus || false

    # Check starting dnsmasq allows the default forward rule to work to u1.
    incus exec u1 -- systemctl start dnsmasq
    dig a +tcp @192.0.2.1 u1.incus
    dig aaaa +tcp @2001:db8:1:2::1 u1.incus

    # Check UDP port forwarding rules, and mixing protocols on same listen port without default target address.
    incus network forward unset ovn-virtual-network 192.0.2.1 target_address
    incus network forward unset ovn-virtual-network 2001:db8:1:2::1 target_address
    incus network forward port add ovn-virtual-network 192.0.2.1 udp 53 "${U1_IPV4}"
    incus network forward port add ovn-virtual-network 2001:db8:1:2::1 udp 53 "${U1_IPV6}"
    incus network forward port add ovn-virtual-network 192.0.2.1 tcp 53 "${U2_IPV4}"
    incus network forward port add ovn-virtual-network 2001:db8:1:2::1 tcp 53 "${U2_IPV6}"

    # Check UDP port forwarding to u1 is working, and that TCP port forwarding to u2 is not (as dnsmasq is off).
    incus exec u2 -- systemctl stop dnsmasq
## NOTE: current OVN udp load-balancer bug in test environment
#    dig a @192.0.2.1 u1.incus
#    dig aaaa @2001:db8:1:2::1 u1.incus
    ! dig a +tcp @192.0.2.1 u2.incus || false
    ! dig aaaa +tcp @2001:db8:1:2::1 u2.incus || false

    # Check TCP port forwarding on same listen port as UDP port is working by starting dnsmasq on u2.
    incus exec u2 -- systemctl start dnsmasq
    dig a +tcp @192.0.2.1 u2.incus
    dig aaaa +tcp @2001:db8:1:2::1 u2.incus

    # Clean up.
    incus delete -f u1
    incus delete -f u2
    incus network delete ovn-virtual-network
    ovn-nbctl list load_balancer | grep -cF name | grep -xF 0
    incus project switch default
    incus project delete testovn
    incus network delete incusbr0
}

test_network_ovn_load_balancer() {
    if ! network_ovn_supported; then
        return
    fi

    poolName=$(incus profile device get default root pool)
    instanceImage="images:debian/13"

    # Create incusbr0 bridge to act as uplink network.
    incus network create incusbr0 \
        ipv4.address=10.10.10.1/24 ipv4.nat=true \
        ipv4.dhcp.ranges=10.10.10.2-10.10.10.199 \
        ipv4.ovn.ranges=10.10.10.200-10.10.10.254 \
        ipv6.address=fd42:4242:4242:1010::1/64 ipv6.nat=true \
        ipv6.ovn.ranges=fd42:4242:4242:1010::200-fd42:4242:4242:1010::254

    # Create OVN network using physical uplink network.
    incus project create testovn -c features.networks=true -c features.images=false
    incus project switch testovn
    incus network create ovn-virtual-network --type=ovn network=incusbr0
    sleep 2
    incus network show ovn-virtual-network
    ip4AddressPrefix=$(incus network get ovn-virtual-network ipv4.address | sed 's/\.1\/24$//g')
    ip6AddressPrefix=$(incus network get ovn-virtual-network ipv6.address | sed 's/::1\/64$//g')

    # Check no load balancer can be created with a listen address set that is not in the uplink's routes.
    ! incus network load-balancer create ovn-virtual-network 192.0.2.1 || false
    ! incus network load-balancer create ovn-virtual-network 2001:db8:1:2::1 || false

    # Add routes to the uplink network and check we can create load balancers.
    incus network set incusbr0 ipv4.routes=192.0.2.0/24 ipv6.routes=2001:db8:1:2::/64 --project=default

    # Add a static route to SNAT address on uplink to OVN network's router (simulating BGP route advert to uplink).
    ovnIPv4="$(incus network get ovn-virtual-network volatile.network.ipv4.address)"
    ovnIPv6="$(incus network get ovn-virtual-network volatile.network.ipv6.address)"
    ip r add 192.0.2.1/32 via "${ovnIPv4}" dev incusbr0
    ip r add 2001:db8:1:2::1/128 via "${ovnIPv6}" dev incusbr0

    # Check load balancers can be created.
    incus network load-balancer create ovn-virtual-network 192.0.2.1
    incus network load-balancer create ovn-virtual-network 2001:db8:1:2::1

    # Check no OVN config yet as not fully setup.
    ovn-nbctl list load_balancer | grep -cF name -c | grep -xF 0

    # Check forward cannot be created on same listen address as load balancer.
    ! incus network forward create ovn-virtual-network 192.0.2.1 || false
    ! incus network forward create ovn-virtual-network 2001:db8:1:2::1 || false

    # Check user config is allowed, but unknown keys are not.
    incus network load-balancer set ovn-virtual-network 192.0.2.1 user.foo=bar
    incus network load-balancer show ovn-virtual-network 192.0.2.1 | grep -F 'user.foo: bar'
    ! incus network load-balancer set ovn-virtual-network 192.0.2.1 foo=bar || false

    # Check duplicate load balancers cannot be created.
    ! incus network load-balancer create ovn-virtual-network 192.0.2.1 || false
    ! incus network load-balancer create ovn-virtual-network 2001:db8:1:2::1 || false

    # Check a load balancer on another ovn network connected to same uplink cannot conflict with an existing load balancer.
    incus network create ovn-virtual-network2 --type=ovn network=incusbr0
    ip4AddressPrefix2=$(incus network get ovn-virtual-network2 ipv4.address | sed 's/\.1\/24$//g')
    ip6AddressPrefix2=$(incus network get ovn-virtual-network2 ipv6.address | sed 's/::1\/64$//g')

    ! incus network load-balancer create ovn-virtual-network2 192.0.2.1 || false
    ! incus network load-balancer create ovn-virtual-network2 2001:db8:1:2::1|| false
    incus network load-balancer create ovn-virtual-network2 192.0.2.2
    incus network load-balancer create ovn-virtual-network2 2001:db8:1:2::2

    # Check an external route on another network's NIC prevents another network from setting up a load balancer listener.
    incus init "${instanceImage}" u1 -n ovn-virtual-network2 -s "${poolName}"
    incus config device set u1 eth0 \
        ipv4.routes.external=192.0.2.3/32,192.0.2.4/32 \
        ipv6.routes.external=2001:db8:1:2::3/128,2001:db8:1:2::4/128
    ! incus network load-balancer create ovn-virtual-network 192.0.2.3 || false
    ! incus network load-balancer create ovn-virtual-network 2001:db8:1:2::3 || false
    incus delete -f u1
    incus network load-balancer create ovn-virtual-network 192.0.2.3
    incus network load-balancer create ovn-virtual-network 2001:db8:1:2::3

    # Check basic load balancer with backend and port configuration produces config in OVN.
    incus network load-balancer backend add ovn-virtual-network2 192.0.2.2 test-backend "${ip4AddressPrefix2}.2"
    incus network load-balancer backend add ovn-virtual-network2 2001:db8:1:2::2 test-backend "${ip6AddressPrefix2}::2"
    incus network load-balancer port add ovn-virtual-network2 192.0.2.2 tcp 53 test-backend
    incus network load-balancer port add ovn-virtual-network2 2001:db8:1:2::2 tcp 53 test-backend

    # Check changing network's subnet(s) are prevented if load balancers backends exist that conflict.
    ! incus network set ovn-virtual-network2 ipv4.address=192.168.0.1/24 || false
    ! incus network set ovn-virtual-network2 ipv6.address=2001:db8:1:3::1/64 || false

    # Check network load balancers are removed when network is removed.
    ovn-nbctl list load_balancer | grep -cF name | grep -xF 2
    incus network delete ovn-virtual-network2
    ovn-nbctl list load_balancer | grep -cF name | grep -xF 0

    # Create instance connected to ovn-virtual-network.
    incus launch "${instanceImage}" u1 -n ovn-virtual-network -s "${poolName}"
    sleep 5
    incus list
    U1_IPV4="$(incus list u1 -c4 --format=csv | cut -d' ' -f1)"
    U1_IPV6="$(incus list u1 -c6 --format=csv | cut -d' ' -f1)"

    # Check IP is unused and not working.
    ! ping -c1 -4 -w5 192.0.2.1 || false
    ! ping -c1 -6 -w5 2001:db8:1:2::1 || false

    # Add incompatible load balancer backend for u1.
    incus network load-balancer backend add ovn-virtual-network 192.0.2.1 u1 "${U1_IPV4}" 53-63,80
    incus network load-balancer backend add ovn-virtual-network 2001:db8:1:2::1 u1 "${U1_IPV6}" 53-63,80
    ! incus network load-balancer port add ovn-virtual-network 192.0.2.1 tcp 53 u1 || false
    ! incus network load-balancer port add ovn-virtual-network 2001:db8:1:2::1 tcp 53 u1 || false

    # Add compatible explicit single port backend for u1.
    incus network load-balancer backend remove ovn-virtual-network 192.0.2.1 u1
    incus network load-balancer backend remove ovn-virtual-network 2001:db8:1:2::1 u1
    incus network load-balancer backend add ovn-virtual-network 192.0.2.1 u1 "${U1_IPV4}" 54
    incus network load-balancer backend add ovn-virtual-network 2001:db8:1:2::1 u1 "${U1_IPV6}" 54
    incus network load-balancer port add ovn-virtual-network 192.0.2.1 tcp 53 u1
    incus network load-balancer port add ovn-virtual-network 2001:db8:1:2::1 tcp 53 u1

    # Check can't remove backend in use.
    ! incus network load-balancer backend remove ovn-virtual-network 192.0.2.1 u1 || false
    ! incus network load-balancer backend remove ovn-virtual-network 2001:db8:1:2::1 u1 || false
    incus network load-balancer port remove ovn-virtual-network 192.0.2.1 tcp 53
    incus network load-balancer port remove ovn-virtual-network 2001:db8:1:2::1 tcp 53
    incus network load-balancer backend remove ovn-virtual-network 192.0.2.1 u1
    incus network load-balancer backend remove ovn-virtual-network 2001:db8:1:2::1 u1

    # Add load balancer ports towards u1 using no port target.
    incus network load-balancer backend add ovn-virtual-network 192.0.2.1 u1 "${U1_IPV4}"
    incus network load-balancer backend add ovn-virtual-network 2001:db8:1:2::1 u1 "${U1_IPV6}"
    incus network load-balancer port add ovn-virtual-network 192.0.2.1 tcp 53 u1
    incus network load-balancer port add ovn-virtual-network 2001:db8:1:2::1 tcp 53 u1

    # Check DNS TCP forwarding towards u1.
    # Relies on static route (above) rather than neighbour adverts see https://github.com/ovn-org/ovn/issues/124
    incus exec u1 -- systemctl mask dnsmasq
    incus exec u1 -- apt-get update
    incus exec u1 -- apt-get install --no-install-recommends --yes dnsmasq
cat <<EOF | incus exec u1 -- tee /etc/dnsmasq.d/incus_test.conf
interface=eth0
bind-interfaces
interface-name=u1.incus,eth0
host-record=incus.localdomain,127.0.0.1
EOF

    incus exec u1 -- systemctl unmask dnsmasq
    incus exec u1 -- systemctl start dnsmasq
    dig a +tcp @192.0.2.1 u1.incus
    dig aaaa +tcp @2001:db8:1:2::1 u1.incus

    # Check stopping the DNS service and checking we get no connection.
    incus exec u1 -- systemctl stop dnsmasq
    ! dig a +tcp @192.0.2.1 u1.incus || false
    ! dig aaaa +tcp @2001:db8:1:2::1 u1.incus || false
    incus exec u1 -- systemctl stop dnsmasq

    # Check changing DNS TCP forwarding towards u2.
    incus launch "${instanceImage}" u2 -n ovn-virtual-network -s "${poolName}"
    sleep 5
    incus list
    U2_IPV4="$(incus list u2 -c4 --format=csv | cut -d' ' -f1)"
    U2_IPV6="$(incus list u2 -c6 --format=csv | cut -d' ' -f1)"

    # Add load balancer backends for u2.
    incus network load-balancer backend add ovn-virtual-network 192.0.2.1 u2 "${U2_IPV4}"
    incus network load-balancer backend add ovn-virtual-network 2001:db8:1:2::1 u2 "${U2_IPV6}"

    # Change load balancer ports towards u2.
    incus network load-balancer port remove ovn-virtual-network 192.0.2.1 tcp 53
    incus network load-balancer port remove ovn-virtual-network 2001:db8:1:2::1 tcp 53
    incus network load-balancer port add ovn-virtual-network 192.0.2.1 tcp 53 u2
    incus network load-balancer port add ovn-virtual-network 2001:db8:1:2::1 tcp 53 u2

    # Check DNS TCP forwarding towards u2.
    incus exec u2 -- systemctl mask dnsmasq
    incus exec u2 -- apt-get update
    incus exec u2 -- apt-get install --no-install-recommends --yes dnsmasq
cat <<EOF | incus exec u2 -- tee /etc/dnsmasq.d/incus_test.conf
interface=eth0
bind-interfaces
interface-name=u2.incus,eth0
host-record=incus.localdomain,127.0.0.2
EOF

    incus exec u2 -- systemctl unmask dnsmasq
    incus exec u2 -- systemctl start dnsmasq

    dig a +tcp @192.0.2.1 u2.incus
    dig aaaa +tcp @2001:db8:1:2::1 u2.incus

    # Change load balancer ports towards u1 and u2.
    incus network load-balancer port remove ovn-virtual-network 192.0.2.1 tcp 53
    incus network load-balancer port remove ovn-virtual-network 2001:db8:1:2::1 tcp 53
    incus network load-balancer port add ovn-virtual-network 192.0.2.1 tcp 53 u1,u2
    incus network load-balancer port add ovn-virtual-network 2001:db8:1:2::1 tcp 53 u1,u2

    # Check OVN config mentions both backend target IPs.
    ovn-nbctl list load_balancer | grep -F "{\"192.0.2.1:53\"=\"${U1_IPV4}:53,${U2_IPV4}:53\"}"
    ovn-nbctl list load_balancer | grep -F "{\"[2001:db8:1:2::1]:53\"=\"[${U1_IPV6}]:53,[${U2_IPV6}]:53\"}"

    # Check DNS load balancing working.
    incus exec u1 -- systemctl start dnsmasq

    for _ in $(seq 5); do
        dig a +tcp @192.0.2.1 incus.localdomain
        dig aaaa +tcp @2001:db8:1:2::1 incus.localdomain
    done

    # Check stopping dnsmasq on both instances stop forward from working.
    incus exec u1 -- systemctl stop dnsmasq
    incus exec u2 -- systemctl stop dnsmasq

    for _ in $(seq 5); do
        ! dig a +tcp @192.0.2.1 incus.localdomain || false
        ! dig aaaa +tcp @2001:db8:1:2::1 incus.localdomain || false
    done

    # Clean up.
    incus delete -f u1
    incus delete -f u2
    incus network delete ovn-virtual-network
    ovn-nbctl list load_balancer | grep -cF name | grep -xF 0
    incus project switch default
    incus project delete testovn
    incus network delete incusbr0
}

test_network_ovn_peering() {
    if ! network_ovn_supported; then
        return
    fi

    poolName=$(incus profile device get default root pool)
    instanceImage="images:debian/13"

    # Create two projects to test cross-project peering.
    incus project create prj-ovn1 \
        -c features.images=false \
        -c features.profiles=false \
        -c features.storage.volumes=false \
        -c features.networks=true

    incus project create prj-ovn2 \
        -c features.images=false \
        -c features.profiles=false \
        -c features.storage.volumes=false \
        -c features.networks=true

    # Create incusbr0 bridge to act as uplink network with some uplink routes that can be used.
    incus network create incusbr0 \
        ipv4.address=10.10.10.1/24 ipv4.nat=true \
        ipv4.dhcp.ranges=10.10.10.2-10.10.10.199 \
        ipv4.ovn.ranges=10.10.10.200-10.10.10.254 \
        ipv6.address=fd42:4242:4242:1010::1/64 ipv6.nat=true \
        ipv6.ovn.ranges=fd42:4242:4242:1010::200-fd42:4242:4242:1010::254 \
        ipv4.routes=198.51.100.0/24 \
        ipv6.routes=2001:db8:1:2::/64

    # Create OVN networks in each project.
    incus network create ovn1 --type=ovn network=incusbr0 --project=prj-ovn1
    incus network create ovn2 --type=ovn network=incusbr0 --project=prj-ovn2
    sleep 2

    incus network show ovn1 --project=prj-ovn1
    incus network show ovn2 --project=prj-ovn2

    ovn1NetIPv4=$(incus network get ovn1 ipv4.address --project=prj-ovn1 | sed 's/\/24$//g')
    ovn1NetIPv6=$(incus network get ovn1 ipv6.address --project=prj-ovn1 | sed 's/\/64$//g')
    ovn2NetIPv4=$(incus network get ovn2 ipv4.address --project=prj-ovn2 | sed 's/\/24$//g')
    ovn2NetIPv6=$(incus network get ovn2 ipv6.address --project=prj-ovn2 | sed 's/\/64$//g')

    # Now setup peering.
    incus network peer create ovn1 ovn1foo prj-ovn2/ovn2 --project=prj-ovn1
    incus network peer ls ovn1 --project=prj-ovn1 | grep ovn1foo | grep PENDING
    incus network peer create ovn2 ovn2foo prj-ovn1/ovn1 --project=prj-ovn2
    incus network peer ls ovn1 --project=prj-ovn1 | grep ovn1foo | grep CREATED
    incus network peer ls ovn2 --project=prj-ovn2 | grep ovn2foo | grep CREATED

    # Check networks show as in use by peered network.
    incus network show ovn1 --project=prj-ovn1 | grep ovn2
    incus network show ovn2 --project=prj-ovn2 | grep ovn1

    # Check network delete is protected by peer usage.
    ! incus network delete ovn1 --project=prj-ovn1 || false
    ! incus network delete ovn2 --project=prj-ovn2 || false

    # Delete peering.
    incus network peer delete ovn1 ovn1foo --project=prj-ovn1
    incus network peer ls ovn2 --project=prj-ovn2 | grep ovn2foo | grep ERRORED
    ! incus network show ovn1 --project=prj-ovn1 | grep ovn2 || false
    ! incus network show ovn2 --project=prj-ovn2 | grep ovn1 || false

    # Launch instance in each network (with one having NIC routes from uplink).
    incus init "${instanceImage}" ovn1 -n ovn1 -s "${poolName}" --project=prj-ovn1
    incus config device set ovn1 eth0 --project=prj-ovn1 \
        ipv4.routes=198.51.100.1/32 \
        ipv6.routes=2001:db8:1:2::1/128 \
        ipv4.routes.external=198.51.100.2/32 \
        ipv6.routes.external=2001:db8:1:2::2/128
    incus start ovn1 --project=prj-ovn1
    incus launch "${instanceImage}" ovn2 -n ovn2 -s "${poolName}" --project=prj-ovn2

    # Add IPs from routes to ovn1 instance.
    sleep 2
    incus exec ovn1 --project=prj-ovn1 -- ip a add 198.51.100.1/32 dev eth0
    incus exec ovn1 --project=prj-ovn1 -- ip a add 198.51.100.2/32 dev eth0
    incus exec ovn1 --project=prj-ovn1 -- ip a add 2001:db8:1:2::1/128 dev eth0
    incus exec ovn1 --project=prj-ovn1 -- ip a add 2001:db8:1:2::2/128 dev eth0

    # Test that pinging internal addresses between networks doesn't work without peering.
    sleep 5
    ! incus exec ovn1 --project=prj-ovn1 -- ping -c1 -4 -w5 "${ovn2NetIPv4}" || false
    ! incus exec ovn1 --project=prj-ovn1 -- ping -c1 -6 -w5 "${ovn2NetIPv6}" || false
    ! incus exec ovn2 --project=prj-ovn2 -- ping -c1 -4 -w5 198.51.100.1 || false
    ! incus exec ovn2 --project=prj-ovn2 -- ping -c1 -6 -w5 2001:db8:1:2::1 || false

    # Test that pinging external addresses between networks does worth without peering (goes via uplink).
    incus exec ovn2 --project=prj-ovn2 -- ping -c1 -4 -w5 198.51.100.2
    incus exec ovn2 --project=prj-ovn2 -- ping -c1 -6 -w5 2001:db8:1:2::2

    # Remove routes on uplink manually to purposefully break uplink routing of external routes.
    ip -4 r del 198.51.100.0/24 dev incusbr0
    ip -6 r del 2001:db8:1:2::/64 dev incusbr0

    # Test that pinging external addresses between networks doesn't worth via uplink with removed routes.
    ! incus exec ovn2 --project=prj-ovn2 -- ping -c1 -4 -w5 198.51.100.2 || false
    ! incus exec ovn2 --project=prj-ovn2 -- ping -c1 -6 -w5 2001:db8:1:2::2 || false

    # Now setup peering again.
    incus network peer create ovn1 ovn1foo prj-ovn2/ovn2 --project=prj-ovn1
    ! incus network peer create ovn2 ovn2foo prj-ovn1/ovn1 --project=prj-ovn2 || false # Detect old peering entry exists.
    incus network peer delete ovn2 ovn2foo --project=prj-ovn2 # Delete old peering entry.
    incus network peer create ovn2 ovn2foo prj-ovn1/ovn1 --project=prj-ovn2
    incus network peer ls ovn2 --project=prj-ovn2 | grep ovn2foo | grep CREATED

    # Test that pinging now works between networks.
    sleep 5

    # Ping gateways on each peer endpoint.
    incus exec ovn1 --project=prj-ovn1 -- ping -c1 -4 -w5 "${ovn2NetIPv4}"
    incus exec ovn1 --project=prj-ovn1 -- ping -c1 -6 -w5 "${ovn2NetIPv6}"
    incus exec ovn2 --project=prj-ovn2 -- ping -c1 -4 -w5 "${ovn1NetIPv4}"
    incus exec ovn2 --project=prj-ovn2 -- ping -c1 -6 -w5 "${ovn1NetIPv6}"

    # Ping internal and external NIC route addresses over peer connection.
    incus exec ovn2 --project=prj-ovn2 -- ping -c1 -4 -w5 198.51.100.1
    incus exec ovn2 --project=prj-ovn2 -- ping -c1 -6 -w5 2001:db8:1:2::1
    incus exec ovn2 --project=prj-ovn2 -- ping -c1 -4 -w5 198.51.100.2
    incus exec ovn2 --project=prj-ovn2 -- ping -c1 -6 -w5 2001:db8:1:2::2

    # Check address set entries added for running instances.
    ovn-nbctl list address_set | grep -F 198.51.100.1/32
    ovn-nbctl list address_set | grep -F 2001:db8:1:2::1/128
    ovn-nbctl list address_set | grep -F 198.51.100.2/32
    ovn-nbctl list address_set | grep -F 2001:db8:1:2::2/128

    # Check address set entries deleted for instance NIC when stopped and added when started again.
    incus stop -f ovn1 --project=prj-ovn1
    ! ovn-nbctl list address_set | grep -F 198.51.100.1/32 || false
    ! ovn-nbctl list address_set | grep -F 2001:db8:1:2::1/128 || false
    ! ovn-nbctl list address_set | grep -F 198.51.100.2/32 || false
    ! ovn-nbctl list address_set | grep -F 2001:db8:1:2::2/128 || false
    incus start ovn1 --project=prj-ovn1
    ovn-nbctl list address_set | grep -F 198.51.100.1/32
    ovn-nbctl list address_set | grep -F 2001:db8:1:2::1/128
    ovn-nbctl list address_set | grep -F 198.51.100.2/32
    ovn-nbctl list address_set | grep -F 2001:db8:1:2::2/128

    # Check security policies prevent spoofed packets using peer connection.
    sleep 5
    ovn1NICIPv4="$(incus list ovn1 -c4 --format=csv --project=prj-ovn1 | cut -d' ' -f1)"
    ovn1NICIPv6="$(incus list ovn1 -c6 --format=csv --project=prj-ovn1 | cut -d' ' -f1)"
    ovn2NICIPv4="$(incus list ovn2 -c4 --format=csv --project=prj-ovn2 | cut -d' ' -f1)"
    ovn2NICIPv6="$(incus list ovn2 -c6 --format=csv --project=prj-ovn2 | cut -d' ' -f1)"

    # Install tcpdump and check we can detect ICMP packets coming to the ovn2 instance from ovn1 instance when
    # pinging the allowed addresses.
    incus exec ovn2 --project=prj-ovn2 -- apt-get update
    incus exec ovn2 --project=prj-ovn2 -- apt-get install --no-install-recommends --yes tcpdump
    incus exec ovn1 -T -n --project=prj-ovn1 -- ping -4 -w5 "${ovn2NICIPv4}" || true &
    pingPID=$!
    incus exec ovn2 -T -n --project=prj-ovn2 -- timeout 5s tcpdump -i eth0 -nn icmp and src "${ovn1NICIPv4}" -q -c 1 > /dev/null
    wait "${pingPID}"

    incus exec ovn1 -T -n --project=prj-ovn1 -- ping -6 -w5 "${ovn2NICIPv6}" || true &
    pingPID=$!
    incus exec ovn2 -T -n --project=prj-ovn2 -- timeout 5s tcpdump -i eth0 -nn icmp6 and src "${ovn1NICIPv6}" -q -c 1 > /dev/null
    wait "${pingPID}"

    # Now reconfigure ovn1's IP address inside the instance to an unknown IP and try pinging ovn2.
    # Check that the security policies prevent those ICMP packets from reaching ovn2 instance.
    # Disable DHCP client and SLAAC acceptance so we don't get automatic IPs added.
    incus exec ovn1 --project=prj-ovn1 -- rm /etc/systemd/network/eth0.network
    incus exec ovn1 --project=prj-ovn1 -- systemctl stop systemd-networkd
    incus exec ovn1 --project=prj-ovn1 -- systemctl mask systemd-networkd
    incus exec ovn1 --project=prj-ovn1 -- sysctl net.ipv6.conf.eth0.accept_ra=0
    incus exec ovn1 --project=prj-ovn1 -- sysctl net.ipv6.conf.eth0.autoconf=0
    incus exec ovn1 --project=prj-ovn1 -- ip a flush dev eth0
    incus exec ovn1 --project=prj-ovn1 -- ip a add 198.51.100.3/32 dev eth0
    incus exec ovn1 --project=prj-ovn1 -- ip a add 2001:db8:1:2::3/128 dev eth0
    incus exec ovn1 --project=prj-ovn1 -- ip a
    sleep 2
    incus exec ovn1 --project=prj-ovn1 -- ip -4 r add "${ovn1NetIPv4}" dev eth0
    incus exec ovn1 --project=prj-ovn1 -- ip -4 r del default || true
    incus exec ovn1 --project=prj-ovn1 -- ip -4 r add default via "${ovn1NetIPv4}" dev eth0 src 198.51.100.3
    incus exec ovn1 --project=prj-ovn1 -- ip -6 r add "${ovn1NetIPv6}" dev eth0
    incus exec ovn1 --project=prj-ovn1 -- ip -6 r del default || true
    incus exec ovn1 --project=prj-ovn1 -- ip -6 r add default via "${ovn1NetIPv6}" dev eth0 src 2001:db8:1:2::3
    incus exec ovn1 --project=prj-ovn1 -- ip -4 r
    incus exec ovn1 --project=prj-ovn1 -- ip -6 r

    incus exec ovn1 -T -n --project=prj-ovn1 -- ping -4 -w5 "${ovn2NICIPv4}" || true &
    pingPID=$!
    ! incus exec ovn2 -T -n --project=prj-ovn2 -- timeout 5s tcpdump -i eth0 -nn icmp and src 198.51.100.3 -q -c 1 > /dev/null || false
    wait "${pingPID}"

    incus exec ovn1 -T -n --project=prj-ovn1 -- ping -6 -w5 "${ovn2NICIPv6}" || true &
    pingPID=$!
    ! incus exec ovn2 -T -n --project=prj-ovn2 -- timeout 5s tcpdump -i eth0 -nn icmp6 and src 2001:db8:1:2::3 -q -c 1 > /dev/null || false
    wait "${pingPID}"

    # Clear all security rules and check spoofed packets are received to prove the security rules were working.
    ovn-nbctl lr-list
    ovn1NetID=$(incus admin sql global "select id from networks where name = 'ovn1'" | grep -Po '\d+')
    ovn2NetID=$(incus admin sql global "select id from networks where name = 'ovn2'" | grep -Po '\d+')

    ovn-nbctl lr-policy-del "incus-net${ovn1NetID}-lr"
    ovn-nbctl lr-policy-del "incus-net${ovn2NetID}-lr"

    incus exec ovn1 -T -n --project=prj-ovn1 -- ping -4 -w5 "${ovn2NICIPv4}" || true &
    pingPID=$!
    incus exec ovn2 -T -n --project=prj-ovn2 -- timeout 5s tcpdump -i eth0 -nn icmp and src 198.51.100.3 -q -c 1 > /dev/null
    wait "${pingPID}"

    incus exec ovn1 -T -n --project=prj-ovn1 -- ping -6 -w5 "${ovn2NICIPv6}" || true &
    pingPID=$!
    incus exec ovn2 -T -n --project=prj-ovn2 -- timeout 5s tcpdump -i eth0 -nn icmp6 and src 2001:db8:1:2::3 -q -c 1 > /dev/null
    wait "${pingPID}"

    # Check changing the network's subnet is reflected in the address sets and peering routes and the old subnets
    # are removed.
    incus delete -f ovn1 --project=prj-ovn1 # Remove as we cleared its DHCP config.
    incus network set ovn1 ipv4.address=192.0.2.1/24 ipv6.address=2001:db8:1:1::1/64 --project=prj-ovn1
    incus launch "${instanceImage}" ovn1 -n ovn1 -s "${poolName}" --project=prj-ovn1
    sleep 5
    ovn1NICIPv4="$(incus list ovn1 -c4 --format=csv --project=prj-ovn1 | cut -d' ' -f1)"
    ovn1NICIPv6="$(incus list ovn1 -c6 --format=csv --project=prj-ovn1 | cut -d' ' -f1)"
    incus exec ovn2 -T -n --project=prj-ovn2 -- ping -c1 -4 -w5 "${ovn1NICIPv4}"
    incus exec ovn2 -T -n --project=prj-ovn2 -- ping -c1 -6 -w5 "${ovn1NICIPv6}"

    # Check external ingress spoof protection.
    # Setup ovn1's IP on the external bridge, with a spoofed route source address and confirm no spoofed packets
    # reach ovn2's NIC.
    ovn2IPv4="$(incus network get ovn2 volatile.network.ipv4.address --project=prj-ovn2)"
    ovn2IPv6="$(incus network get ovn2 volatile.network.ipv6.address --project=prj-ovn2)"

    ip a add "${ovn1NICIPv4}/32" dev incusbr0
    ip a add "${ovn1NICIPv6}/128" dev incusbr0
    ip -4 r add "${ovn2NICIPv4}/32" via "${ovn2IPv4}" dev incusbr0 src "${ovn1NICIPv4}"
    ip -6 r add "${ovn2NICIPv6}/128" via "${ovn2IPv6}" dev incusbr0 src "${ovn1NICIPv6}"

    ping -4 -w5 "${ovn2NICIPv4}" || true &
    pingPID=$!
    ! incus exec ovn2 -T -n --project=prj-ovn2 -- timeout 5s tcpdump -i eth0 -nn icmp and src "${ovn1NICIPv4}" -q -c 1 > /dev/null || false
    wait "${pingPID}"

    ping -6 -w5 "${ovn2NICIPv6}" || true &
    pingPID=$!
    ! incus exec ovn2 -T -n --project=prj-ovn2 -- timeout 5s tcpdump -i eth0 -nn icmp6 and src "${ovn1NICIPv6}" -q -c 1 > /dev/null || false
    wait "${pingPID}"

    # Remove spoofed IPs and routes, and check can ping the instance NIC externally using non-spoofed IP.
    ip a del "${ovn1NICIPv4}/32" dev incusbr0
    ip a del "${ovn1NICIPv6}/128" dev incusbr0
    ip -4 r replace "${ovn2NICIPv4}/32" via "${ovn2IPv4}" dev incusbr0
    ip -6 r replace "${ovn2NICIPv6}/128" via "${ovn2IPv6}" dev incusbr0
    ping -c1 -4 -w5 "${ovn2NICIPv4}"
    ping -c1 -6 -w5 "${ovn2NICIPv6}"
    ip -4 r del "${ovn2NICIPv4}/32" via "${ovn2IPv4}" dev incusbr0
    ip -6 r del "${ovn2NICIPv6}/128" via "${ovn2IPv6}" dev incusbr0

    # ACLs referencing peers.
    incus network acl create ovn1 --project=prj-ovn1
    incus config device set ovn1 eth0 security.acls=ovn1 --project=prj-ovn1
    ! incus exec ovn1 -T -n --project=prj-ovn1 -- ping -c1 -4 -w5 "${ovn2NICIPv4}" || false
    ! incus exec ovn1 -T -n --project=prj-ovn1 -- ping -c1 -6 -w5 "${ovn2NICIPv6}" || false
    ! incus exec ovn2 -T -n --project=prj-ovn2 -- ping -c1 -4 -w5 "${ovn1NICIPv4}" || false
    ! incus exec ovn2 -T -n --project=prj-ovn2 -- ping -c1 -6 -w5 "${ovn1NICIPv6}" || false

    incus network acl rule add ovn1 egress destination=@ovn1/ovn1foo action=allow --project=prj-ovn1
    incus exec ovn1 -T -n --project=prj-ovn1 -- ping -c1 -4 -w5 "${ovn2NICIPv4}"
    incus exec ovn1 -T -n --project=prj-ovn1 -- ping -c1 -6 -w5 "${ovn2NICIPv6}"
    ! incus exec ovn2 -T -n --project=prj-ovn2 -- ping -c1 -4 -w5 "${ovn1NICIPv4}" || false
    ! incus exec ovn2 -T -n --project=prj-ovn2 -- ping -c1 -6 -w5 "${ovn1NICIPv6}" || false

    incus network acl rule add ovn1 ingress source=@ovn1/ovn1foo action=allow state=logged --project=prj-ovn1
    incus exec ovn1 -T -n --project=prj-ovn1 -- ping -c1 -4 -w5 "${ovn2NICIPv4}"
    incus exec ovn1 -T -n --project=prj-ovn1 -- ping -c1 -6 -w5 "${ovn2NICIPv6}"
    incus exec ovn2 -T -n --project=prj-ovn2 -- ping -c1 -4 -w5 "${ovn1NICIPv4}"
    incus exec ovn2 -T -n --project=prj-ovn2 -- ping -c1 -6 -w5 "${ovn1NICIPv6}"

    # Check that acl rule for ovn ingress has all the expected values
    incus network acl show-log ovn1 --project=prj-ovn1 | while IFS= read -r logline; do
      proto=$(echo "$logline" | jq -r '.proto')
      src=$(echo "$logline" | jq -r '.src')
      dst=$(echo "$logline" | jq -r '.dst')
      icmp_type=$(echo "$logline" | jq -r '.icmp_type')
      icmp_code=$(echo "$logline" | jq -r '.icmp_code')
      action=$(echo "$logline" | jq -r '.action')

      [ "$action" = "allow" ] || false

      if [ "$proto" = "icmp" ]; then
        # IPv4 ping from ovn2 to ovn1 allowed
        [ "$src" = "${ovn2NICIPv4}" ] || false
        [ "$dst" = "${ovn1NICIPv4}" ] || false
        [ "$icmp_type" = "8" ] || false
        [ "$icmp_code" = "0" ] || false
      elif [ "$proto" = "icmp6" ]; then
        # IPv6 ping from ovn2 to ovn1 allowed
        [ "$src" = "${ovn2NICIPv6}" ] || false
        [ "$dst" = "${ovn1NICIPv6}" ] || false
        [ "$icmp_type" = "128" ] || false
        [ "$icmp_code" = "0" ] || false
      fi
    done

    # Check cannot add an ACL to a network NIC that references a peer connection from another network.
    incus network create ovn1b --type=ovn network=incusbr0 --project=prj-ovn1
    ! incus network set ovn1b security.acls=ovn1 --project=prj-ovn1 || false
    incus network delete ovn1b --project=prj-ovn1

    # Cleanup instances.
    incus delete -f ovn1 --project=prj-ovn1
    incus delete -f ovn2 --project=prj-ovn2

    # Check cannot delete peer used by ACL.
    ! incus network peer delete ovn1 ovn1foo --project=prj-ovn1 || false

    # Cleanup.
    incus network acl delete ovn1 --project=prj-ovn1
    incus network peer delete ovn1 ovn1foo --project=prj-ovn1
    incus network delete ovn1 --project=prj-ovn1
    incus network delete ovn2 --project=prj-ovn2
    incus network delete incusbr0

    incus project delete prj-ovn1
    incus project delete prj-ovn2
}

test_network_ovn_dhcp_reservation() {
    if ! network_ovn_supported; then
        return
    fi

    poolName=$(incus profile device get default root pool)
    instanceImage="images:debian/13"

    incus network create incusbr0 \
        ipv4.address=10.10.10.1/24 ipv4.nat=true \
        ipv4.dhcp.ranges=10.10.10.2-10.10.10.199 \
        ipv4.ovn.ranges=10.10.10.200-10.10.10.254 \
        ipv6.address=fd42:4242:4242:1010::1/64 ipv6.nat=true \
        ipv6.ovn.ranges=fd42:4242:4242:1010::200-fd42:4242:4242:1010::254

    # Create OVN network.
    incus network create ovn1 --type=ovn \
        network=incusbr0 \
        ipv4.address=10.10.11.1/24 ipv4.nat=true \
        ipv6.address=fd42:4242:4242:1011::1/64 ipv6.nat=true

    sleep 2
    incus network list | grep ovn1

    # Check there are no dns records entries and only the gateway IP is reserved.
    [ "$(ovn-nbctl list dns | grep -Fc 'records')" = "0" ] || false
    [ "$(ovn-nbctl list logical_switch | grep -Fc 'exclude_ips="10.10.11.1 10.10.11.254"')" = "1" ] || false

    # Create instance and check empty DNS record entry is created (showing successfully adding a NIC).
    incus init "${instanceImage}" u1 -s "${poolName}" -n ovn1
    [ "$(ovn-nbctl list dns | grep -Fc 'records')" = "1" ] || false

    # Check specifying a static IP is added to DHCP reservations.
    incus config device set u1 eth0 ipv4.address=10.10.11.200
    [ "$(ovn-nbctl list logical_switch | grep -Fc 'exclude_ips="10.10.11.1 10.10.11.254 10.10.11.200"')" = "1" ] || false

    # Check changing static IP is reflected in DHCP reservations.
    incus config device set u1 eth0 ipv4.address=10.10.11.2
    [ "$(ovn-nbctl list logical_switch | grep -Fc 'exclude_ips="10.10.11.1 10.10.11.254 10.10.11.2"')" = "1" ] || false

    # Launch new dynamic IP instance and check its not allocated the reserved IP.
    incus launch "${instanceImage}" u2 -s "${poolName}" -n ovn1
    sleep 10
    incus list
    U2_IPV4="$(incus list u2 -c4 --format=csv | cut -d' ' -f1)"
    [ "${U2_IPV4}" != "10.10.11.2" ] || false
    incus delete -f u2

    # Copy instance with static IP so it creates an IP conflict.
    incus copy u1 u2

    # Check neither instances are allowed to start.
    ! incus start u1 || false
    ! incus start u2 || false

    # Check there is only 1 DNS record entry and only 1 reserved instance IP.
    [ "$(ovn-nbctl list dns | grep -Fc 'records')" = "1" ] || false
    [ "$(ovn-nbctl list logical_switch | grep -Fc 'exclude_ips="10.10.11.1 10.10.11.254 10.10.11.2"')" = "1" ] || false

    # Delete the new copy which caused the conflict and check the DNS record entry and reserved IP for the original
    # instance is left alone.
    incus delete -f u2
    [ "$(ovn-nbctl list dns | grep -Fc 'records')" = "1" ] || false
    [ "$(ovn-nbctl list logical_switch | grep -Fc 'exclude_ips="10.10.11.1 10.10.11.254 10.10.11.2"')" = "1" ] || false

    # Create conflict again, but this time delete the original instance.
    incus copy u1 u2
    incus delete -f u1
    [ "$(ovn-nbctl list dns | grep -Fc 'records')" = "0" ] || false
    [ "$(ovn-nbctl list logical_switch | grep -Fc 'exclude_ips="10.10.11.1 10.10.11.254 10.10.11.2"')" = "1" ] || false

    # Check that starting the instance copy creates the missing DNS record entry.
    incus start u2
    [ "$(ovn-nbctl list dns | grep -Fc 'records')" = "1" ] || false
    [ "$(ovn-nbctl list logical_switch | grep -Fc 'exclude_ips="10.10.11.1 10.10.11.254 10.10.11.2"')" = "1" ] || false

    # Check that deleting the instance copy after successful start removes the DNS record entry and IP allocation.
    incus delete -f u2
    [ "$(ovn-nbctl list dns | grep -Fc 'records')" = "0" ] || false
    [ "$(ovn-nbctl list logical_switch | grep -Fc 'exclude_ips="10.10.11.1 10.10.11.254"')" = "1" ] || false

    incus network delete ovn1
    incus network delete incusbr0
}

test_network_ovn_nested_vlan() {
    if ! network_ovn_supported; then
        return
    fi

    poolName=$(incus profile device get default root pool)
    instanceImage="images:debian/13"

    incus network create incusbr0 \
        ipv4.address=10.10.10.1/24 ipv4.nat=true \
        ipv4.dhcp.ranges=10.10.10.2-10.10.10.199 \
        ipv4.ovn.ranges=10.10.10.200-10.10.10.254 \
        ipv6.address=fd42:4242:4242:1010::1/64 ipv6.nat=true \
        ipv6.ovn.ranges=fd42:4242:4242:1010::200-fd42:4242:4242:1010::254

    # Create OVN network.
    incus network create ovn1 --type=ovn \
        network=incusbr0 \
        ipv4.address=10.10.11.1/24 ipv4.nat=true \
        ipv6.address=fd42:4242:4242:1011::1/64 ipv6.nat=true \
        ipv6.dhcp.stateful=true

    sleep 2
    incus network list | grep ovn1
    incus launch "${instanceImage}" u1 -s "${poolName}" -n ovn1 -c security.nesting=true

    # Add nested NICs to u1.
    incus config device add u1 eth1 nic \
        network=ovn1 \
        nested=eth0 \
        vlan=1 \
        ipv4.address=10.10.11.10 \
        ipv6.address=fd42:4242:4242:1011::10

    eth1MAC="$(incus config get u1 volatile.eth1.hwaddr)"

    incus config device add u1 eth2 nic \
        network=ovn1 \
        nested=eth0 \
        vlan=2 \
        ipv4.address=10.10.11.11 \
        ipv6.address=fd42:4242:4242:1011::11

    eth2MAC="$(incus config get u1 volatile.eth2.hwaddr)"

    incus launch "${instanceImage}" u2 -s "${poolName}" -n ovn1

    # Configure nested VLAN 1 inside u1 in a network namespace.
    incus exec u1 -- ip link add link eth0 name eth0.1 address "${eth1MAC}" type vlan id 1
    incus exec u1 -- ip netns add vlan_nesting1
    incus exec u1 -- ip link set eth0.1 netns vlan_nesting1
    incus exec u1 -- ip netns exec vlan_nesting1 ip link set lo up
    incus exec u1 -- ip netns exec vlan_nesting1 ip address add 10.10.11.10/24 dev eth0.1
    incus exec u1 -- ip netns exec vlan_nesting1 ip address add fd42:4242:4242:1011::10/64 dev eth0.1 nodad
    incus exec u1 -- ip netns exec vlan_nesting1 ip link set eth0.1 up

    # Configure nested VLAN 2 inside u1 in a network namespace.
    incus exec u1 -- ip link add link eth0 name eth0.2 address "${eth2MAC}" type vlan id 2
    incus exec u1 -- ip netns add vlan_nesting2
    incus exec u1 -- ip link set eth0.2 netns vlan_nesting2
    incus exec u1 -- ip netns exec vlan_nesting2 ip link set lo up
    incus exec u1 -- ip netns exec vlan_nesting2 ip address add 10.10.11.11/24 dev eth0.2
    incus exec u1 -- ip netns exec vlan_nesting2 ip address add fd42:4242:4242:1011::11/64 dev eth0.2 nodad
    incus exec u1 -- ip netns exec vlan_nesting2 ip link set eth0.2 up

    sleep 5
    incus ls
    incus exec u1 -- ip netns exec vlan_nesting1 ip address
    incus exec u1 -- ip netns exec vlan_nesting2 ip address

    # Check nested VLAN NICs are pingable from parent instance.
    incus exec u1 -- ping -c1 -4 -w5 10.10.11.10
    incus exec u1 -- ping -c1 -6 -w5 fd42:4242:4242:1011::10
    incus exec u1 -- ping -c1 -4 -w5 10.10.11.11
    incus exec u1 -- ping -c1 -6 -w5 fd42:4242:4242:1011::11

    # Check nested VLAN 2 NIC is pingable from VLAN 1 namespace.
    incus exec u1 -- ip netns exec vlan_nesting1 ping -c1 -4 -w5 10.10.11.11
    incus exec u1 -- ip netns exec vlan_nesting1 ping -c1 -6 -w5 fd42:4242:4242:1011::11

    # Check nested VLAN NICs are pingable from a different instance on the same network.
    incus exec u2 -- ping -c1 -4 -w5 10.10.11.10
    incus exec u2 -- ping -c1 -6 -w5 fd42:4242:4242:1011::10
    incus exec u2 -- ping -c1 -4 -w5 10.10.11.11
    incus exec u2 -- ping -c1 -6 -w5 fd42:4242:4242:1011::11

    incus delete -f u1 u2
    incus network delete ovn1
    incus network delete incusbr0
}

test_network_ovn_acl() {
    if ! network_ovn_supported; then
        return
    fi

    poolName=$(incus profile device get default root pool)
    instanceImage="images:debian/13"

    # Create uplink network with a special DNS record incusbr0.test pointing to the bridge addresses.
    incus network create incusbr0 \
        ipv4.address=10.10.10.1/24 ipv4.nat=true \
        ipv4.dhcp.ranges=10.10.10.2-10.10.10.199 \
        ipv4.ovn.ranges=10.10.10.200-10.10.10.254 \
        ipv6.address=fd42:4242:4242:1010::1/64 ipv6.nat=true \
        ipv6.ovn.ranges=fd42:4242:4242:1010::200-fd42:4242:4242:1010::254 \
        raw.dnsmasq='host-record=incusbr0.test,10.10.10.1,fd42:4242:4242:1010::1'

    # Create an ACL that allows ICMPv4 and ICMPv6 ping to incusbr0 IP.
    incus network acl create incusbr0-ping
    incus network acl rule add incusbr0-ping egress action=allow protocol=icmp4 destination=10.10.10.1/32
    incus network acl rule add incusbr0-ping egress action=allow protocol=icmp6 destination=fd42:4242:4242:1010::1/128

    # Don't expect any OVN port groups to be created yet.
    ovn-nbctl --bare --column=name --format=csv find port_group | grep -c incus | grep 0

    # Create OVN network with ACL, but without specifying uplink parent network (check default selection works).
    incus network create ovn0 --type=ovn \
        ipv4.address=10.10.11.1/24 ipv4.nat=true \
        ipv6.address=fd42:4242:4242:1011::1/64 ipv6.nat=true \
        security.acls=incusbr0-ping

    ! incus network acl delete incusbr0-ping || false # Can't delete ACL while in use.

    # Expect 7 Incus related port groups to exist (network port group, ACL port group, and per-ACL-per-network port groups).
    ovn-nbctl --bare --column=name --format=csv find port_group | grep -c incus | grep 7

    # Delete network to clean up OVN ACL port group.
    incus network delete ovn0

    # Expect the unused OVN port groups to be cleaned up.
    ovn-nbctl --bare --column=name --format=csv find port_group | grep -c incus | grep 0

    # Re-create network with no ACLS.
    incus network create ovn0 --type=ovn \
        ipv4.address=10.10.11.1/24 ipv4.nat=true \
        ipv6.address=fd42:4242:4242:1011::1/64 ipv6.nat=true

    # Expect 1 port group (the network port group) to exist.
    ovn-nbctl --bare --column=name --format=csv find port_group | grep -c incus | grep 1

    # Assign ACL to OVN network.
    incus network set ovn0 security.acls=incusbr0-ping

    # Expect 7 Incus related port groups to exist now its assigned (network port group, ACL port group, and per-ACL-per-network port groups).
    ovn-nbctl --bare --column=name --format=csv find port_group | grep -c incus | grep 7

    # Launch containers and check baseline services (DHCP, SLAAC, DNS) and incusbr0-ping ACL.
    incus launch "${instanceImage}" c1 -n ovn0 -s "${poolName}"
    incus launch "${instanceImage}" c2 -n ovn0 -s "${poolName}"

    echo "==> Wait for addresses"
    sleep 10
    incus list

    # Check per-NIC ACL rules added.
    ovn-nbctl list acl | grep -c 'match.*instance.*eth0' | grep 4

    # Test ping to OVN router (baseline rules).
    incus exec c1 -- ping -c1 -w5 -4 10.10.11.1
    incus exec c1 -- ping -c1 -w5 -6 fd42:4242:4242:1011::1

    # Test ping to incusbr0 IPs via DNS (baseline rules for DNS) and incusbr0-ping ACL tests.
    incus exec c1 -- ping -c1 -w5 -4 incusbr0.test
    incus exec c1 -- ping -c1 -w5 -6 incusbr0.test

    # Add additional IPs to incusbr0.
    ip a add 10.10.11.3/32 dev incusbr0
    ip a add fd42:4242:4242:1010::2/128 dev incusbr0 nodad
    ping -c1 -w5 -4 10.10.11.3
    ping -c1 -w5 -6 fd42:4242:4242:1010::2

    # Ping to additional IPs should be blocked.
    ! incus exec c1 -- ping -c1 -w5 -4 10.10.11.3 || false
    ! incus exec c1 -- ping -c1 -w5 -6 fd42:4242:4242:1010::2 || false

    # Ping to other instance should be blocked.
    ! incus exec c1 -- ping -c1 -w5 -4 c2.incus || false
    ! incus exec c1 -- ping -c1 -w5 -6 c2.incus || false

    # Check default rule action is reject (disable acl, install dig in c1, then re-enable acl).
    incus network unset ovn0 security.acls
    incus exec c1 -- apt-get update
    incus exec c1 -- apt-get install --no-install-recommends --yes dnsutils
    incus network set ovn0 security.acls=incusbr0-ping
    sleep 2

    incus exec c1 -- dig @10.10.11.3 +tcp +timeout=1 incusbr0.test | grep "refused"
    incus exec c1 -- dig @fd42:4242:4242:1010::2 +tcp +timeout=1 incusbr0.test | grep "refused"
    incus exec c1 -- ping -c1 -w5 -4 10.10.11.3 | grep "Host Unreachable"
    incus exec c1 -- ping -c1 -w5 -6 fd42:4242:4242:1010::2 | grep "Administratively prohibited"

    # Check setting default rule action to drop takes effect.
    incus network set ovn0 security.acls.default.egress.action=drop
    incus exec c1 -- dig @10.10.11.3 +tcp +timeout=1 incusbr0.test | grep "timed out"
    incus exec c1 -- dig @fd42:4242:4242:1010::2 +tcp +timeout=1 incusbr0.test | grep "timed out"
    ! incus exec c1 -- ping -c1 -w5 -4 10.10.11.3 || false
    ! incus exec c1 -- ping -c1 -w5 -6 fd42:4242:4242:1010::2 || false

    # Check setting default rule action to reject takes effect. Same as if unspecified.
    incus network set ovn0 security.acls.default.egress.action=reject
    sleep 2
    incus exec c1 -- dig @10.10.11.3 +tcp +timeout=1 -p 5053 incusbr0.test | grep "refused"
    incus exec c1 -- dig @fd42:4242:4242:1010::2 +tcp +timeout=1 -p 5053 incusbr0.test | grep "refused"
    incus exec c1 -- ping -c1 -w5 -4 c2.incus | grep "Host Unreachable"
    incus exec c1 -- ping -c1 -w5 -6 c2.incus | grep "Administratively prohibited"

    # Test assigning same ACL to NIC directly and unassigning from network.
    incus config device set c1 eth0 security.acls=incusbr0-ping
    incus network unset ovn0 security.acls

    # Check ACL port groups still exists.
    ovn-nbctl --bare --column=name --format=csv find port_group | grep -c incus | grep 7

    # Check only c1 has per-NIC ACL rules.
    ovn-nbctl list acl | grep -c 'match.*instance.*eth0' | grep 2
    incus config device unset c1 eth0 security.acls
    sleep 2

    # Check removing ACLs removes default per-NIC ACL rules.
    ovn-nbctl list acl | grep -c 'match.*instance.*eth0' | grep 0

    # Check assigning first ACL to a network adds per-NIC ACL rules to c1 and c2.
    incus network set ovn0 security.acls=incusbr0-ping
    ovn-nbctl list acl | grep -c 'match.*instance.*eth0' | grep 4
    incus network set ovn0 security.acls.default.ingress.logged=true
    ovn-nbctl list acl | grep -c 'name.*eth0-ingress' | grep 2
    incus network set ovn0 security.acls.default.egress.logged=true
    ovn-nbctl list acl | grep -c 'name.*eth0-egress' | grep 2
    incus network unset ovn0 security.acls
    ovn-nbctl list acl | grep -c 'match.*instance.*eth0' | grep 0
    ovn-nbctl list acl | grep -c 'name.*eth0-ingress' | grep 0
    ovn-nbctl list acl | grep -c 'name.*eth0-egress' | grep 0
    incus config device set c1 eth0 security.acls=incusbr0-ping
    ovn-nbctl list acl | grep -c 'match.*instance.*eth0' | grep 2
    ovn-nbctl list acl | grep -c 'name.*eth0-ingress' | grep 1
    ovn-nbctl list acl | grep -c 'name.*eth0-egress' | grep 1
    incus network set ovn0 security.acls.default.ingress.logged=false
    incus network set ovn0 security.acls.default.egress.logged=false
    ovn-nbctl list acl | grep -c 'match.*instance.*eth0' | grep 2
    ovn-nbctl list acl | grep -c 'name.*eth0-ingress' | grep 0
    ovn-nbctl list acl | grep -c 'name.*eth0-egress' | grep 0
    incus config device set c1 eth0 security.acls.default.ingress.logged=true
    incus config device set c1 eth0 security.acls.default.egress.logged=true
    ovn-nbctl list acl | grep -c 'name.*eth0-ingress' | grep 1
    ovn-nbctl list acl | grep -c 'name.*eth0-egress' | grep 1
    incus network unset ovn0 security.acls.default.ingress.logged
    incus network unset ovn0 security.acls.default.egress.logged
    ovn-nbctl list acl | grep -c 'name.*eth0-ingress' | grep 1
    ovn-nbctl list acl | grep -c 'name.*eth0-egress' | grep 1
    incus config device unset c1 eth0 security.acls.default.ingress.logged
    incus config device unset c1 eth0 security.acls.default.egress.logged
    ovn-nbctl list acl | grep -c 'name.*eth0-ingress' | grep 0
    ovn-nbctl list acl | grep -c 'name.*eth0-egress' | grep 0

    # Test c1's default ingress rule defaults to reject by querying from c2 (which now has no ACLs applied).
    incus exec c2 -- apt-get update
    incus exec c2 -- apt-get install --no-install-recommends --yes dnsutils
    incus exec c2 -- dig @c1.incus -4 +tcp +timeout=1 -p 5053 incusbr0.test | grep "refused"
    incus exec c2 -- dig @c1.incus -6 +tcp +timeout=1 -p 5053 incusbr0.test | grep "refused"
    incus exec c2 -- ping -c1 -w5 -4 c1.incus | grep "Host Unreachable"
    incus exec c2 -- ping -c1 -w5 -6 c1.incus | grep "Administratively prohibited"

    # Test c1's default ingress rule drop by querying from c2 (which now has no ACLs applied).
    incus config device set c1 eth0 security.acls.default.ingress.action=drop
    incus exec c2 -- dig @c1.incus -4 +tcp +timeout=1 -p 5053 incusbr0.test | grep "timed out"
    incus exec c2 -- dig @c1.incus -6 +tcp +timeout=1 -p 5053 incusbr0.test | grep "timed out"
    ! incus exec c2 -- ping -c1 -w5 -4 c1.incus || false
    ! incus exec c2 -- ping -c1 -w5 -6 c1.incus || false

    # Test unassigning ACL from NIC and check OVN port group is cleaned up.
    incus config device unset c1 eth0 security.acls

    # Expect the unused OVN port group to be cleaned up after unassignment from NIC (leaving just network port group).
    ovn-nbctl --bare --column=name --format=csv find port_group | grep -c incus | grep 1

    # Test assigning ACL to stopped instance NIC and check OVN port group isn't created until start time.
    incus stop -f c1
    incus config device set c1 eth0 security.acls=incusbr0-ping

    # Don't expect any more OVN port groups to be created yet (just network port group).
    ovn-nbctl --bare --column=name --format=csv find port_group | grep -c incus | grep 1

    incus start c1

    # Expect 7 Incus related port group to exist now its started (network port group, ACL port group, and per-ACL-per-network port groups).
    ovn-nbctl --bare --column=name --format=csv find port_group | grep -c incus | grep 7

    # Test delete instance and check unused OVN port group is cleaned up.
    incus delete -f c1

    # Expect the unused OVN port groups to be cleaned up (leaving just network port group)..
    ovn-nbctl --bare --column=name --format=csv find port_group | grep -c incus | grep 1

    # Create container with network assigned ACL and NIC specific ACL too. Check both sets of rules are applied.
    incus network set ovn0 ipv6.dhcp.stateful=true security.acls=incusbr0-ping

    # Expect 7 Incus related port group to exist now its assigned (network port group, ACL port group, and per-ACL-per-network port groups).
    ovn-nbctl --bare --column=name --format=csv find port_group | grep -c incus | grep 7

    incus init "${instanceImage}" c1 -s "${poolName}" -n ovn0
    incus config device set c1 eth0 ipv4.address=10.10.11.2 ipv6.address=fd42:4242:4242:1011::2
    incus start c1

    echo "==> Wait for addresses"
    sleep 10
    incus list

    # Ping to c1 instance from c2 should be blocked.
    incus exec c2 -- resolvectl flush-caches # c1 has changed its address since last test.
    ! incus exec c2 -- ping -c1 -w5 -4 c1.incus || false
    ! incus exec c2 -- ping -c1 -w5 -6 c1.incus || false

    # Create new ACL to allow ingress ping, assign to NIC and test pinging static assigned IPs.
    incus network acl create ingress-ping
    incus network acl rule add ingress-ping ingress action=allow protocol=icmp4
    incus network acl rule add ingress-ping ingress action=allow protocol=icmp6

    # Expect 7 Incus related port group to exist as new ACL not assigned to anything yet.
    ovn-nbctl --bare --column=name --format=csv find port_group | grep -c incus | grep 7

    incus config device set c1 eth0 security.acls=ingress-ping

    # Expect 11 Incus related port group to exist now new ACL is assigned (will add new ACL port group and new per-ACL-per-network port groups).
    ovn-nbctl --bare --column=name --format=csv find port_group | grep -c incus | grep 13

    network_ovn_wait_ping c2 -- ping -c1 -w5 -4 10.10.11.2
    network_ovn_wait_ping c2 -- ping -c1 -w5 -6 fd42:4242:4242:1011::2

    # Test IP range rule.
    incus network acl rule remove ingress-ping ingress action=allow protocol=icmp4
    ! incus exec c2 -- ping -c1 -w5 -4 10.10.11.2 || false
    incus network acl rule add ingress-ping ingress action=allow protocol=icmp4 destination=10.10.10.1-10.10.11.2
    incus exec c2 -- ping -c1 -w5 -4 10.10.11.2
    incus network acl rule remove ingress-ping ingress action=allow protocol=icmp4 destination=10.10.10.1-10.10.11.2
    incus network acl rule add ingress-ping ingress action=allow protocol=icmp4 destination=10.10.10.1-10.10.11.1
    ! incus exec c2 -- ping -c1 -w5 -4 10.10.11.2 || false

    # Test ACL rule referencing our own ACL name as source in rule.
    incus network acl rule remove ingress-ping ingress action=allow protocol=icmp4 destination=10.10.10.1-10.10.11.1
    incus network acl rule add ingress-ping ingress action=allow protocol=icmp4 source=ingress-ping
    incus network acl rule remove ingress-ping ingress action=allow protocol=icmp6
    incus network acl rule add ingress-ping ingress action=allow protocol=icmp6 source=ingress-ping

    ! incus exec c2 -- ping -c1 -w5 -4 c1.incus || false # Expect to fail as c2 isn't part of ingress-ping ACL group yet.
    ! incus exec c2 -- ping -c1 -w5 -6 c1.incus || false # Expect to fail as c2 isn't part of ingress-ping ACL group yet.

    incus config device set c2 eth0 security.acls=ingress-ping # Add c2 to ACL group.
    incus exec c2 -- ping -c1 -w5 -4 c1.incus
    incus exec c2 -- ping -c1 -w5 -6 c1.incus
    incus exec c1 -- ping -c1 -w5 -4 c2.incus
    incus exec c1 -- ping -c1 -w5 -6 c2.incus

    # Create empty ACL group and then update existing rule to reference that as source to check ping stops working.
    incus network acl create test-empty-group
    incus network acl rule remove ingress-ping ingress action=allow protocol=icmp4 source=ingress-ping
    incus network acl rule remove ingress-ping ingress action=allow protocol=icmp6 source=ingress-ping

    # Expect 11 Incus related port groups to exist as new one not assigned.
    ovn-nbctl --bare --column=name --format=csv find port_group | grep -c incus | grep 13

    incus network acl rule add ingress-ping ingress action=allow protocol=icmp4 source=test-empty-group
    incus network acl rule add ingress-ping ingress action=allow protocol=icmp6 source=test-empty-group

    # Expect 18 Incus related port groups to exist as new one now used by ACL that is assigned.
    ovn-nbctl --bare --column=name --format=csv find port_group | grep -c incus | grep 18

    ! incus exec c2 -- ping -c1 -w5 -4 c1.incus || false
    ! incus exec c2 -- ping -c1 -w5 -6 c1.incus || false
    ! incus exec c1 -- ping -c1 -w5 -4 c2.incus || false
    ! incus exec c1 -- ping -c1 -w5 -6 c2.incus || false

    # Clean up existing ACLs.
    incus network unset ovn0 security.acls
    incus network acl delete incusbr0-ping
    incus config device unset c1 eth0 security.acls
    incus config device unset c2 eth0 security.acls
    incus network acl delete ingress-ping
    incus network acl delete test-empty-group

    # Check only network level port group exists.
    ovn-nbctl --bare --column=name --format=csv find port_group | grep -c incus | grep 1

    # Test using external and internal reserved classifiers.
    incus network acl create icmp
    incus network set ovn0 security.acls=icmp
    ovn-nbctl --bare --column=name --format=csv find port_group | grep -c incus | grep 7

    # Test can't ping external uplink router IP or internal IPs.
    ! incus exec c1 -- ping -c1 -w5 -4 10.10.10.1 || false
    ! incus exec c1 -- ping -c1 -w5 -4 c2.incus || false

    # Allow external pings and test works and that internal ping still blocked (using deprecated subject).
    incus network acl rule add icmp egress destination='#external' action=allow protocol=icmp4
    incus exec c1 -- ping -c1 -w5 -4 10.10.10.1
    ! incus exec c1 -- ping -c1 -w5 -4 c2.incus || false
    incus network acl rule remove icmp egress destination='#external' action=allow protocol=icmp4

    # Allow external pings and test works and that internal ping still blocked (using current subject).
    incus network acl rule add icmp egress destination='@external' action=allow protocol=icmp4
    incus exec c1 -- ping -c1 -w5 -4 10.10.10.1
    ! incus exec c1 -- ping -c1 -w5 -4 c2.incus || false

    # Allow egress internal pings and test works (using deprecated subject).
    incus network acl rule add icmp egress destination='#internal' action=allow protocol=icmp4
    incus exec c1 -- ping -c1 -w5 -4 c2.incus
    incus network acl rule remove icmp egress destination='#internal' action=allow protocol=icmp4

    # Allow egress internal pings and test works (using current subject).
    incus network acl rule add icmp egress destination='@internal' action=allow protocol=icmp4
    incus exec c1 -- ping -c1 -w5 -4 c2.incus

    incus network acl rule remove icmp egress destination='@internal' action=allow protocol=icmp4
    ! incus exec c1 -- ping -c1 -w5 -4 c2.incus || false

    # Allow ingress internal pings and test works.
    incus network acl rule add icmp ingress source='@internal' action=allow protocol=icmp4
    incus exec c1 -- ping -c1 -w5 -4 c2.incus

    # Create new network with ingress ACL and check network port grpup and per-ACL-per-network are created.
    incus network create ovn1 --type=ovn \
        network=incusbr0 \
        ipv4.address=10.10.12.1/24 ipv4.nat=true \
        ipv6.address=fd42:4242:4242:1012::1/64 ipv6.nat=true \
        security.acls=icmp
    ovn-nbctl --bare --column=name --format=csv find port_group | grep -c incus | grep 9

    # Check per-ACL-per-network is removed when unassigning ACL.
    incus network unset ovn1 security.acls
    ovn-nbctl --bare --column=name --format=csv find port_group | grep -c incus | grep 8

    # Connect c2 to ovn1 with icmp ACL and check its created.
    incus stop -f c2
    incus config device set c2 eth0 network=ovn1 security.acls=icmp
    incus start c2
    ovn-nbctl --bare --column=name --format=csv find port_group | grep -c incus | grep 9

    # Connect c1 to ovn1 without any explicit ACLs.
    incus stop -f c1
    incus config device set c1 eth0 network=ovn1 ipv4.address= ipv6.address=
    incus start c1

    echo "==> Wait for addresses"
    sleep 10
    incus list

    # Check c2 can ping external.
    incus exec c2 -- ping -w5 -c1 -4 10.10.10.1

    # Check c1 can ping c2 even though its not part of the ACL (should be classified as internal anyway).
    incus exec c1 -- ping -w5 -c1 -4 c2.incus

    # Cleanup.
    incus delete -f c1
    incus delete -f c2
    incus network delete ovn0 --project default
    incus network delete ovn1 --project default

    # Expect all OVN port groups to be cleaned up.
    ovn-nbctl --bare --column=name --format=csv find port_group | grep -c incus | grep 0

    # Expect all OVN ACLs to be cleaned up.
    ovn-nbctl list acl | wc -l | grep 0

    incus network delete incusbr0 --project default
    incus network acl delete icmp --project default
}

test_network_ovn_l3only() {
    if ! network_ovn_supported; then
        return
    fi

    poolName=$(incus profile device get default root pool)
    instanceImage="images:debian/13"

    incus network create incusbr0 \
        ipv4.address=10.10.10.1/24 ipv4.nat=true \
        ipv4.dhcp.ranges=10.10.10.2-10.10.10.199 \
        ipv4.ovn.ranges=10.10.10.200-10.10.10.254 \
        ipv6.address=fd42:4242:4242:1010::1/64 ipv6.nat=true \
        ipv6.ovn.ranges=fd42:4242:4242:1010::200-fd42:4242:4242:1010::254

    # Create OVN network.
    incus network create ovn1 --type=ovn \
        network=incusbr0 \
        ipv4.address=10.10.11.1/24 ipv4.nat=true \
        ipv6.address=fd42:4242:4242:1011::1/64 ipv6.nat=true

    # Check that ipv3.l3only mode cannot be enabled if ipv6.dhcp is enabled and ipv6.dhcp.stateful is disabled.
    ! incus network set ovn1 ipv6.l3only=true || false

    # Enable ipv6.l3only mode with DHCPv6 disabled.
    incus network set ovn1 ipv6.dhcp=false ipv6.l3only=true

    # Enable l3only mode with stateful DHCPv6 enabled.
    incus network set ovn1 ipv6.dhcp=true ipv6.dhcp.stateful=true ipv4.l3only=true ipv6.l3only=true

    sleep 2
    incus network list | grep ovn1

    # Check internal router port is configured with single host subnet mask.
    [ "$(ovn-nbctl --format=csv --bare --columns=networks find logical_router_port | grep -cFx "10.10.11.1/32 fd42:4242:4242:1011::1/128" )" = "1" ]

    incus launch "${instanceImage}" u1 -s "${poolName}" -n ovn1
    incus launch "${instanceImage}" u2 -s "${poolName}" -n ovn1

    sleep 5
    incus ls

    U1_IPV4="$(incus list u1 -c4 --format=csv | cut -d' ' -f1)"
    U1_IPV6="$(incus list u1 -c6 --format=csv | cut -d' ' -f1)"
    U2_IPV4="$(incus list u2 -c4 --format=csv | cut -d' ' -f1)"
    U2_IPV6="$(incus list u2 -c6 --format=csv | cut -d' ' -f1)"

    # Check IPs configured have single host subnet mask.
    incus exec u1 -- ip -4 address show dev eth0 | grep -F "${U1_IPV4}/32"
    incus exec u1 -- ip -6 address show dev eth0 | grep -F "${U1_IPV6}/128"

    # Check connectivity to other instance.
    incus exec u1 -- ping -c1 -w5 -4 "${U2_IPV4}"
    incus exec u1 -- ping -c1 -w5 -6 "${U2_IPV6}"

    # Check connectivity to router gateway.
    incus exec u1 -- ping -c1 -w5 -4 10.10.11.1
    incus exec u1 -- ping -c1 -w5 -6 fd42:4242:4242:1011::1

    # Check connectivity to uplink gateway.
    incus exec u1 -- ping -c1 -4 -w5 10.10.10.1
    incus exec u1 -- ping -c1 -6 -w5 fd42:4242:4242:1010::1

    incus delete -f u1 u2
    incus network delete ovn1
    incus network delete incusbr0
}
