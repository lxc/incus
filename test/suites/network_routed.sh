test_network_routed() {
    if [ -n "${INCUS_OFFLINE:-}" ]; then
        echo "==> SKIP: External connectivity needed to pull test image"
        export TEST_UNMET_REQUIREMENT="external connectivity needed to pull test image"
        return
    fi

    if [ ! -e /dev/kvm ] || ! command -v "qemu-system-$(uname -m)" > /dev/null 2>&1; then
        echo "==> SKIP: QEMU and KVM needed to run virtual machines"
        export TEST_UNMET_REQUIREMENT="QEMU and KVM needed to run virtual machines"
        return
    fi

    # Set global sysctl.
    sysctl net.ipv6.conf.all.forwarding=1
    sysctl net.ipv6.conf.all.proxy_ndp=1

    # Setup dummy parent interface.
    ip link add dummy0 type dummy
    sysctl net.ipv6.conf.dummy0.proxy_ndp=1
    sysctl net.ipv6.conf.dummy0.forwarding=1
    sysctl net.ipv4.conf.dummy0.forwarding=1
    sysctl net.ipv6.conf.dummy0.accept_dad=0
    ip link set dummy0 up
    ip addr add 192.0.2.1/32 dev dummy0
    ip addr add 2001:db8::1/128 dev dummy0

    # Create VM and add routed NIC.
    incus init images:debian/13 v1 --vm -c security.nesting=true
    incus config device add v1 eth0 nic \
        nictype=routed \
        parent=dummy0 \
        name=eth0 \
        ipv4.address=192.0.2.2,192.0.2.3 \
        ipv6.address=2001:db8::2,2001:db8::3
    incus start v1
    incus wait v1 agent --timeout=90 --interval=1

    # Configure NIC in VM manually.
    incus exec v1 -- rm /etc/systemd/network/enp5s0.network
    incus exec v1 -- systemctl restart systemd-networkd
    incus exec v1 -- systemctl mask systemd-networkd
    incus exec v1 -- systemctl stop systemd-networkd
    incus exec v1 -- ip -4 a add 192.0.2.2/32 dev enp5s0
    incus exec v1 -- ip -6 a add 2001:db8::2/128 dev enp5s0
    incus exec v1 -- ip -4 r add 169.254.0.1/32 dev enp5s0
    incus exec v1 -- ip -4 r add default via 169.254.0.1 dev enp5s0
    incus exec v1 -- ip -6 r add fe80::1/128 dev enp5s0
    incus exec v1 -- ip -6 r add default via fe80::1 dev enp5s0

    # Test ping to/from VM NIC.
    ping -c1 -4 -W5 192.0.2.2
    ping -c1 -6 -W5 2001:db8::2
    incus exec v1 -- ping -c1 -4 -W5 192.0.2.1
    incus exec v1 -- ping -c1 -6 -W5 2001:db8::2
    incus exec v1 -- ping -c1 -4 -W5 169.254.0.1
    incus exec v1 -- ping -c1 -6 -W5 -I enp5s0 fe80::1

    incus delete -f v1

    # Create a managed routed network.
    incus network create rtest --type=routed parent=dummy0 ipv4.subnets=198.51.100.0/24,192.0.2.0/24 ipv6.subnets=2001:db8::/64

    # Test NIC validation against the network.
    incus init images:debian/13 v1 --vm -c security.nesting=true
    ! incus config device add v1 eth0 nic network=rtest ipv4.address=203.0.113.2 || false
    ! incus config device add v1 eth0 nic network=rtest ipv6.address=2001:db8:: || false
    ! incus config device add v1 eth0 nic network=rtest nictype=routed ipv4.address=192.0.2.2 || false
    incus config device add v1 eth0 nic network=rtest name=eth0 ipv4.address=192.0.2.2 ipv6.address=2001:db8::2

    # Test DHCP with the network in the default VRF, then in a separate one.
    for vrf in "" rvrf; do
        if [ -n "${vrf}" ]; then
            ip link add "${vrf}" type vrf table 100
            ip link set "${vrf}" up
            ip link set dummy0 master "${vrf}"
            ip addr add 2001:db8::1/128 dev dummy0 # Re-add the IPv6 address dropped when joining the VRF.
            incus network set rtest vrf="${vrf}"
        fi

        incus start v1
        incus wait v1 agent --timeout=90 --interval=1

        # Test the addresses are handed out over DHCP.
        for _ in $(seq 30); do
            incus exec v1 -- ip -6 a show dev enp5s0 | grep -qF "2001:db8::2/128" && break
            sleep 1
        done

        incus exec v1 -- ip -4 a show dev enp5s0 | grep -qF "192.0.2.2/32"
        incus exec v1 -- ip -6 a show dev enp5s0 | grep -qF "2001:db8::2/128"
        incus exec v1 -- ip -4 r show default | grep -qF "via 169.254.0.1"
        incus exec v1 -- ip -6 r show default | grep -qF "via fe80::1"
        ping -c1 -4 -W5 ${vrf:+-I "${vrf}"} 192.0.2.2
        ping -c1 -6 -W5 ${vrf:+-I "${vrf}"} 2001:db8::2
        incus exec v1 -- ping -c1 -4 -W5 192.0.2.1
        incus exec v1 -- ping -c1 -6 -W5 2001:db8::1

        # Test the DHCP server is stopped with the instance.
        [ -e "${INCUS_DIR}/devices/v1/dnsmasq.eth0.pid" ]
        incus stop -f v1
        [ ! -e "${INCUS_DIR}/devices/v1/dnsmasq.eth0.pid" ]
    done

    # Test the DHCP server options.
    incus network set rtest dns.nameservers=192.0.2.53,2001:db8::53 mtu=9000
    incus start v1
    dhcpArgs="$(pgrep -af "^dnsmasq .*--interface=$(incus config get v1 volatile.eth0.host_name) ")"
    echo "${dhcpArgs}" | grep -qF -- "--dhcp-option-force=6,192.0.2.53"
    echo "${dhcpArgs}" | grep -qF -- "--dhcp-option-force=option6:dns-server,[2001:db8::53]"
    echo "${dhcpArgs}" | grep -qF -- "--dhcp-option-force=26,9000"
    echo "${dhcpArgs}" | grep -qF -- ",mtu:9000,"
    incus stop -f v1

    # Test DHCP can be turned off per family.
    incus network set rtest ipv4.dhcp=false
    incus start v1
    dhcpArgs="$(pgrep -af "^dnsmasq .*--interface=$(incus config get v1 volatile.eth0.host_name) ")"
    ! echo "${dhcpArgs}" | grep -qF -- "--shared-network" || false
    echo "${dhcpArgs}" | grep -qF -- "--enable-ra"
    incus stop -f v1
    incus network set rtest ipv6.dhcp=false
    incus start v1
    [ ! -e "${INCUS_DIR}/devices/v1/dnsmasq.eth0.pid" ]
    incus stop -f v1
    incus network unset rtest ipv4.dhcp
    incus network unset rtest ipv6.dhcp

    # Test assigned addresses are handed out over DHCP.
    incus config device unset v1 eth0 ipv4.address
    incus config device unset v1 eth0 ipv6.address
    incus start v1
    incus wait v1 agent --timeout=90 --interval=1
    assignedV4="$(incus config get v1 volatile.eth0.ipv4.address)"
    assignedV6="$(incus config get v1 volatile.eth0.ipv6.address)"
    [ "${assignedV4}" = "198.51.100.1" ]
    for _ in $(seq 30); do
        incus exec v1 -- ip -6 a show dev enp5s0 | grep -qF "${assignedV6}/128" && break
        sleep 1
    done

    incus exec v1 -- ip -4 a show dev enp5s0 | grep -qF "${assignedV4}/32"
    incus exec v1 -- ip -6 a show dev enp5s0 | grep -qF "${assignedV6}/128"

    # Cleanup.
    incus delete -f v1
    incus network delete rtest
    ip link delete dummy0
    ip link delete rvrf
}
