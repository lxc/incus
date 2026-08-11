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
    incus init images:debian/13 v1 --vm
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

    # Cleanup.
    incus delete -f v1
    ip link delete dummy0
}
