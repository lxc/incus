network_bridge_firewall_tests() {
    # Add a local NIC device as the testsuite default profile already has a p2p eth0.
    incus init images:debian/13 c1
    incus config device add c1 eth0 nic name=eth0 "$@"
    incus start c1
    sleep 10

    managed=0

    if incus config show c1 --expanded | grep -qF "network: incusbr0"; then
        echo "==> Performing basic DHCP/SLAAC ping tests"
        incus exec c1 -- ping -c1 192.0.2.1
        incus exec c1 -- ping -c1 2001:db8::1
        managed=1
    fi

    # Disable DHCP client and SLAAC acceptance so we don't get automatic IPs added.
    incus exec c1 -- rm /etc/systemd/network/eth0.network
    incus exec c1 -- systemctl restart systemd-networkd
    incus exec c1 -- sysctl net.ipv6.conf.eth0.accept_ra=0

    echo "==> Performing faked source IP ping tests without filtering"
    incus exec c1 -- ip a flush dev eth0
    incus exec c1 -- ip a add 192.0.2.254/24 dev eth0
    incus exec c1 -- ip a add 2001:db8::254/64 dev eth0 nodad
    incus exec c1 -- ip a
    incus exec c1 -- ping -c1 192.0.2.1
    incus exec c1 -- ping -c1 2001:db8::1

    echo "==> Performing faked source IP ping tests with filtering"
    if [ ${managed} -eq 1 ]; then
        incus config device set c1 eth0 \
            security.mac_filtering=true \
            security.ipv4_filtering=true \
            security.ipv6_filtering=true
    else
        incus config device set c1 eth0 \
            security.mac_filtering=true \
            security.ipv4_filtering=true \
            security.ipv6_filtering=true \
            ipv4.address=192.0.2.2 \
            ipv6.address=2001:db8::2
    fi

    incus exec c1 -- ip a flush dev eth0
    incus exec c1 -- ip a add 192.0.2.254/24 dev eth0
    incus exec c1 -- ip a add 2001:db8::254/64 dev eth0 nodad
    incus exec c1 -- ip a
    ! incus exec c1 -- ping -c1 192.0.2.1 || false
    ! incus exec c1 -- ping -c1 2001:db8::1 || false

    echo "==> Performing faked source MAC ping tests without filtering"
    incus stop -f c1

    if [ ${managed} -eq 1 ]; then
        incus config device set c1 eth0 \
            security.mac_filtering=false \
            security.ipv4_filtering=false \
            security.ipv6_filtering=false
    else
        incus config device set c1 eth0 \
            security.mac_filtering=false \
            security.ipv4_filtering=false \
            security.ipv6_filtering=false \
            ipv4.address= \
            ipv6.address=
    fi

    incus start c1
    sleep 10
    incus exec c1 -- sysctl net.ipv6.conf.eth0.accept_ra=0
    incus exec c1 -- ip a flush dev eth0
    incus exec c1 -- ip link set dev eth0 address 00:11:22:33:44:56 up
    incus exec c1 -- ip a add 192.0.2.254/24 dev eth0
    incus exec c1 -- ip a add 2001:db8::254/64 dev eth0 nodad
    incus exec c1 -- ip a
    incus exec c1 -- ping -c1 192.0.2.1
    incus exec c1 -- ping -c1 2001:db8::1

    echo "==> Performing faked source MAC ping tests with filtering"
    incus config device set c1 eth0 security.mac_filtering=true
    incus exec c1 -- ip a
    ! incus exec c1 -- ping -c1 192.0.2.1 || false
    ! incus exec c1 -- ping -c1 2001:db8::1 || false

    incus delete -f c1
}

test_network_bridge_firewall() {
    if [ -n "${INCUS_OFFLINE:-}" ]; then
        echo "==> SKIP: External connectivity needed to pull test image"
        export TEST_UNMET_REQUIREMENT="external connectivity needed to pull test image"
        return
    fi

    if ! incus info | grep -qF "firewall: nftables"; then
        echo "==> SKIP: nftables firewall driver not in use"
        export TEST_UNMET_REQUIREMENT="nftables firewall driver not in use"
        return
    fi

    incus network create incusbr0 \
        ipv4.address=192.0.2.1/24 \
        ipv6.address=2001:db8::1/64 \
        ipv4.dhcp.ranges=192.0.2.2-192.0.2.199

    # Setup bridge filter and unmanaged bridge.
    modprobe br_netfilter
    ip link add incbr0unmanaged type bridge

    echo "==> Performing nftables managed bridge tests"
    network_bridge_firewall_tests network=incusbr0

    echo "==> Performing nftables unmanaged bridge tests"
    ip a flush dev incusbr0 # Clear duplicate address from incusbr0.
    ip link set incusbr0 down
    ip a add 192.0.2.1/24 dev incbr0unmanaged
    ip a add 2001:db8::1/64 dev incbr0unmanaged
    ip link set incbr0unmanaged up
    network_bridge_firewall_tests nictype=bridged parent=incbr0unmanaged

    # Cleanup.
    ip link delete incbr0unmanaged
    incus network delete incusbr0
}
