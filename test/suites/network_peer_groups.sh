test_network_peer_groups() {
    if ! network_ovn_supported; then
        return
    fi

    poolName=$(incus profile device get default root pool)
    instanceImage="images:debian/13"

    # Create an uplink network for the OVN networks to use.
    incus network create inct$$-uplink \
        ipv4.address=10.10.10.1/24 ipv4.nat=true \
        ipv4.dhcp.ranges=10.10.10.2-10.10.10.199 \
        ipv4.ovn.ranges=10.10.10.200-10.10.10.254 \
        ipv6.address=fd42:4242:4242:1010::1/64 ipv6.nat=true \
        ipv6.ovn.ranges=fd42:4242:4242:1010::200-fd42:4242:4242:1010::254

    # Create two OVN networks with non-overlapping subnets.
    incus network create inct$$-ovn1 --type=ovn network=inct$$-uplink \
        ipv4.address=192.0.2.1/24 ipv6.address=2001:db8:1::1/64
    incus network create inct$$-ovn2 --type=ovn network=inct$$-uplink \
        ipv4.address=192.0.3.1/24 ipv6.address=2001:db8:2::1/64
    sleep 2

    # Create the peer group and join both networks to it.
    incus network peer-group create inct$$-pg1 --description "Test peer group"
    incus network peer-group join inct$$-pg1 inct$$-ovn1
    incus network peer-group join inct$$-pg1 inct$$-ovn2

    incus network peer-group list | grep inct$$-pg1
    incus network peer-group show inct$$-pg1 | grep inct$$-ovn1
    incus network peer-group show inct$$-pg1 | grep inct$$-ovn2

    # Joining a network that's already a member should fail.
    ! incus network peer-group join inct$$-pg1 inct$$-ovn1 || false

    # Joining a network whose subnet overlaps an existing member's should fail.
    incus network create inct$$-ovn3 --type=ovn network=inct$$-uplink \
        ipv4.address=192.0.2.5/24 ipv6.address=2001:db8:1::5/64
    ! incus network peer-group join inct$$-pg1 inct$$-ovn3 || false
    incus network delete inct$$-ovn3

    # Launch an instance on ovn1 and check it can reach ovn2's gateway through the peer group.
    incus launch "${instanceImage}" inct$$-c1 -n inct$$-ovn1 -s "${poolName}"
    sleep 5
    incus exec inct$$-c1 -- ping -c1 -4 -w5 192.0.3.1
    incus exec inct$$-c1 -- ping -c1 -6 -w5 2001:db8:2::1

    # Leaving the group should remove connectivity to that network.
    incus network peer-group leave inct$$-pg1 inct$$-ovn2
    ! incus network peer-group show inct$$-pg1 | grep inct$$-ovn2 || false
    ! incus exec inct$$-c1 -- ping -c1 -4 -w5 192.0.3.1 || false

    # Leaving a network that isn't a member should fail.
    ! incus network peer-group leave inct$$-pg1 inct$$-ovn2 || false

    # Deleting the peer group should detach any remaining member (ovn1) automatically.
    incus network peer-group delete inct$$-pg1
    ! incus network peer-group show inct$$-pg1 || false

    # Cleanup.
    incus delete -f inct$$-c1
    incus network delete inct$$-ovn1
    incus network delete inct$$-ovn2
    incus network delete inct$$-uplink
}
