test_container_devices_nic_physical() {
    ensure_import_testimage
    ensure_has_localhost_remote "${INCUS_ADDR}"

    ctName="nt$$"
    dummyMAC="aa:3b:97:97:0f:d5"
    ctMAC="0a:92:a7:0d:b7:d9"

    networkName="testnet"

    # Create dummy interface for use as parent.
    ip link add "${ctName}" address "${dummyMAC}" type dummy

    # Record how many nics we started with.
    startNicCount=$(find /sys/class/net | wc -l)

    # Create test container from default profile.
    incus init testimage "${ctName}"

    # Add physical device to container/
    incus config device add "${ctName}" eth0 nic \
        nictype=physical \
        parent="${ctName}" \
        name=eth0 \
        mtu=1400 \
        hwaddr="${ctMAC}"

    # Launch container and check it has nic applied correctly.
    incus start "${ctName}"

    # Check custom MTU is applied if feature available in Incus.
    if incus info | grep 'network_phys_macvlan_mtu: "true"'; then
        if ! incus exec "${ctName}" -- grep "1400" /sys/class/net/eth0/mtu; then
            echo "mtu invalid"
            false
        fi
    fi

    # Check custom MAC is applied in container.
    if ! incus exec "${ctName}" -- grep -Fix "${ctMAC}" /sys/class/net/eth0/address; then
        echo "mac invalid"
        false
    fi

    # Check volatile cleanup on stop.
    incus stop -f "${ctName}"
    if incus config show "${ctName}" | grep volatile.eth0; then
        echo "unexpected volatile key remains"
        false
    fi

    # Check original MTU is restored on physical device.
    if incus info | grep 'network_phys_macvlan_mtu: "true"'; then
        if ! grep "1500" /sys/class/net/"${ctName}"/mtu; then
            echo "mtu invalid"
            false
        fi
    fi

    # Check original MAC is restored on physical device.
    if ! grep -Fix "${dummyMAC}" /sys/class/net/"${ctName}"/address; then
        echo "mac invalid"
        false
    fi

    # Remove boot time physical device and check MTU is restored.
    incus start "${ctName}"
    incus config device remove "${ctName}" eth0

    # Check original MTU is restored on physical device.
    if incus info | grep 'network_phys_macvlan_mtu: "true"'; then
        if ! grep "1500" /sys/class/net/"${ctName}"/mtu; then
            echo "mtu invalid"
            false
        fi
    fi

    # Check original MAC is restored on physical device.
    if ! grep -Fix "${dummyMAC}" /sys/class/net/"${ctName}"/address; then
        echo "mac invalid"
        false
    fi

    # Test hot-plugging physical device based on vlan parent.
    # Make the MTU higher than the original boot time 1400 MTU above to check that the
    # parent device's MTU is reset on removal to the pre-boot value on host (expect >=1500).
    ip link set "${ctName}" up #VLAN requires parent nic be up.
    incus config device add "${ctName}" eth0 nic \
        nictype=physical \
        parent="${ctName}" \
        name=eth0 \
        vlan=10 \
        hwaddr="${ctMAC}" \
        mtu=1401 #Higher than 1400 boot time value above

    # Check custom MTU is applied if feature available in Incus.
    if incus info | grep 'network_phys_macvlan_mtu: "true"'; then
        if ! incus exec "${ctName}" -- grep "1401" /sys/class/net/eth0/mtu; then
            echo "mtu invalid"
            false
        fi
    fi

    # Check custom MAC is applied in container.
    if ! incus exec "${ctName}" -- grep -Fix "${ctMAC}" /sys/class/net/eth0/address; then
        echo "mac invalid"
        false
    fi

    # Remove hot-plugged physical device and check MTU is restored.
    incus config device remove "${ctName}" eth0

    # Check original MTU is restored on physical device.
    if incus info | grep 'network_phys_macvlan_mtu: "true"'; then
        if ! grep "1500" /sys/class/net/"${ctName}"/mtu; then
            echo "mtu invalid"
            false
        fi
    fi

    # Check original MAC is restored on physical device.
    if ! grep -Fix "${dummyMAC}" /sys/class/net/"${ctName}"/address; then
        echo "mac invalid"
        false
    fi

    # Test hot-plugging physical device based on existing parent.
    # Make the MTU higher than the original boot time 1400 MTU above to check that the
    # parent device's MTU is reset on removal to the pre-boot value on host (expect >=1500).
    incus config device add "${ctName}" eth0 nic \
        nictype=physical \
        parent="${ctName}" \
        name=eth0 \
        hwaddr="${ctMAC}" \
        mtu=1402 #Higher than 1400 boot time value above

    # Check custom MTU is applied if feature available in Incus.
    if incus info | grep 'network_phys_macvlan_mtu: "true"'; then
        if ! incus exec "${ctName}" -- grep "1402" /sys/class/net/eth0/mtu; then
            echo "mtu invalid"
            false
        fi
    fi

    # Check custom MAC is applied in container.
    if ! incus exec "${ctName}" -- grep -Fix "${ctMAC}" /sys/class/net/eth0/address; then
        echo "mac invalid"
        false
    fi

    # Test removing a physical device an check its MTU gets restored to default 1500 mtu
    incus config device remove "${ctName}" eth0

    # Check original MTU is restored on physical device.
    if incus info | grep 'network_phys_macvlan_mtu: "true"'; then
        if ! grep "1500" /sys/class/net/"${ctName}"/mtu; then
            echo "mtu invalid"
            false
        fi
    fi

    # Check original MAC is restored on physical device.
    if ! grep -Fix "${dummyMAC}" /sys/class/net/"${ctName}"/address; then
        echo "mac invalid"
        false
    fi

    # Test hot-plugging physical device based on existing parent with new name that LXC doesn't know about.
    incus config device add "${ctName}" eth1 nic \
        nictype=physical \
        parent="${ctName}" \
        hwaddr="${ctMAC}" \
        mtu=1402 #Higher than 1400 boot time value above

    # Stop the container, LXC doesn't know about the nic, so we will rely on Incus to restore it.
    incus stop -f "${ctName}"

    # Check original MTU is restored on physical device.
    if incus info | grep 'network_phys_macvlan_mtu: "true"'; then
        if ! grep "1500" /sys/class/net/"${ctName}"/mtu; then
            echo "mtu invalid"
            false
        fi
    fi

    # Check original MAC is restored on physical device.
    if ! grep -Fix "${dummyMAC}" /sys/class/net/"${ctName}"/address; then
        echo "mac invalid"
        false
    fi

    # create a dummy test network of type physical
    incus network create "${networkName}" --type=physical parent="${ctName}" mtu=1400

    # remove existing device nic of the container
    incus config device remove "${ctName}" eth1

    # Test adding a physical network to container
    incus config device add "${ctName}" eth1 nic \
        network="${networkName}"

    # Check that network config has been applied
    if ! incus config show "${ctName}" | grep "network: ${networkName}"; then
        echo "no network configuration detected"
        false
    fi

    # Check container can start with the physical network configuration
    incus start "${ctName}"

    # Check custom MTU is applied if feature available in Incus.
    if incus info | grep 'network_phys_macvlan_mtu: "true"'; then
        if ! incus exec "${ctName}" -- grep "1400" /sys/class/net/eth1/mtu; then
            echo "mtu invalid"
            false
        fi
    fi

    incus delete "${ctName}" -f

    incus network delete "${networkName}"

    # Check we haven't left any NICS lying around.
    endNicCount=$(find /sys/class/net | wc -l)
    if [ "$startNicCount" != "$endNicCount" ]; then
        echo "leftover NICS detected"
        false
    fi

    # Remove dummy interface (should still exist).
    ip link delete "${ctName}"
}
