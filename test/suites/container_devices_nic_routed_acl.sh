test_container_devices_nic_routed_acl() {
    ensure_import_testimage
    ensure_has_localhost_remote "${INCUS_ADDR}"

    ctPrefix="nt$$"
    dummyName="incrt$$"

    # No need to enable IPv4 forwarding, as Incus will do this on the veth host_name interface automatically.
    sysctl net.ipv6.conf.all.forwarding=1

    # Give the host addresses of its own to talk to the instances from.
    ip link add "${dummyName}" type dummy
    ip link set "${dummyName}" up
    ip addr add 198.51.100.1/32 dev "${dummyName}"
    ip addr add 2001:db8:1::1/128 dev "${dummyName}"

    incus network acl create "${ctPrefix}A"

    # Check an unknown ACL is refused.
    incus init testimage "${ctPrefix}A"
    ! incus config device add "${ctPrefix}A" eth0 nic nictype=routed name=eth0 \
        ipv4.address=192.0.2.2 ipv6.address=2001:db8::2 security.acls=unknown || false

    # Two routed containers, A with an empty ACL and B without.
    incus config device add "${ctPrefix}A" eth0 nic nictype=routed name=eth0 \
        ipv4.address=192.0.2.2 ipv6.address=2001:db8::2 security.acls="${ctPrefix}A"
    incus start "${ctPrefix}A"

    incus init testimage "${ctPrefix}B"
    incus config device add "${ctPrefix}B" eth0 nic nictype=routed name=eth0 \
        ipv4.address=192.0.2.3 ipv6.address=2001:db8::3
    incus start "${ctPrefix}B"

    wait_for_dad "${ctPrefix}A" eth0
    wait_for_dad "${ctPrefix}B" eth0

    # Check the filter chains with their default rules: reject egress, drop ingress (reject is converted).
    nft -nn list chain inet incus "routedin.${ctPrefix}A.eth0" | grep -c "reject" | grep 1
    nft -nn list chain inet incus "routedout.${ctPrefix}A.eth0" | grep -c "drop" | grep 1
    nft -nn list chain inet incus "routedfwd.${ctPrefix}A.eth0" | grep -c "reject" | grep 1
    nft -nn list chain inet incus "routedfwd.${ctPrefix}A.eth0" | grep -c "drop" | grep 1
    ! nft -nn list chain inet incus "routedin.${ctPrefix}B.eth0" || false

    # Check the ACL lists the instance as a user, and can't be deleted.
    incus network acl show "${ctPrefix}A" | grep -F "/1.0/instances/${ctPrefix}A"
    ! incus network acl delete "${ctPrefix}A" || false

    # B, without ACLs, is reachable from the host.
    ping -c2 -W5 192.0.2.3
    ping -6 -c2 -W5 2001:db8::3

    # A is not: host to A (output), A to host (input), A to B and B to A (forward).
    ! ping -c2 -W2 192.0.2.2 || false
    ! ping -6 -c2 -W2 2001:db8::2 || false
    ! incus exec "${ctPrefix}A" -- ping -c2 -W2 198.51.100.1 || false
    ! incus exec "${ctPrefix}A" -- ping -6 -c2 -W2 2001:db8:1::1 || false
    ! incus exec "${ctPrefix}A" -- ping -c2 -W2 192.0.2.3 || false
    ! incus exec "${ctPrefix}B" -- ping -c2 -W2 192.0.2.2 || false

    # Allow ICMP ping into A. The change is applied to the running NIC.
    incus network acl rule add "${ctPrefix}A" ingress action=allow protocol=icmp4 icmp_type=8
    incus network acl rule add "${ctPrefix}A" ingress action=allow protocol=icmp6 icmp_type=128
    ping -c2 -W5 192.0.2.2
    ping -6 -c2 -W5 2001:db8::2
    incus exec "${ctPrefix}B" -- ping -c2 -W5 192.0.2.2
    incus exec "${ctPrefix}B" -- ping -6 -c2 -W5 2001:db8::2

    # Egress is still rejected.
    ! incus exec "${ctPrefix}A" -- ping -c2 -W2 198.51.100.1 || false
    ! incus exec "${ctPrefix}A" -- ping -c2 -W2 192.0.2.3 || false

    # Allow egress ping, to the host and to B.
    incus network acl rule add "${ctPrefix}A" egress action=allow protocol=icmp4 icmp_type=8
    incus network acl rule add "${ctPrefix}A" egress action=allow protocol=icmp6 icmp_type=128
    incus exec "${ctPrefix}A" -- ping -c2 -W5 198.51.100.1
    incus exec "${ctPrefix}A" -- ping -6 -c2 -W5 2001:db8:1::1
    incus exec "${ctPrefix}A" -- ping -c2 -W5 192.0.2.3
    incus exec "${ctPrefix}A" -- ping -6 -c2 -W5 2001:db8::3

    # Drop rules take priority over allow rules.
    incus network acl create "${ctPrefix}B"
    incus network acl rule add "${ctPrefix}B" egress action=drop protocol=icmp4 icmp_type=8 destination=192.0.2.3
    incus config device set "${ctPrefix}A" eth0 security.acls="${ctPrefix}A,${ctPrefix}B"
    ! incus exec "${ctPrefix}A" -- ping -c2 -W2 192.0.2.3 || false
    incus exec "${ctPrefix}A" -- ping -c2 -W5 198.51.100.1

    # TCP to the host, through an address set.
    nc -l -p 8080 -q0 -s 198.51.100.1 < /dev/null > /dev/null &
    ! incus exec "${ctPrefix}A" --disable-stdin -- nc -w2 198.51.100.1 8080 || false

    incus network address-set create "${ctPrefix}set"
    incus network address-set add "${ctPrefix}set" 198.51.100.1 2001:db8:1::1
    incus network acl rule add "${ctPrefix}A" egress action=allow protocol=tcp "destination=\\\$${ctPrefix}set" destination_port=8080
    nft -nn list chain inet incus "routedin.${ctPrefix}A.eth0" | grep -q "@${ctPrefix}set_ipv4"
    nft list set inet incus "${ctPrefix}set_ipv4" | grep -F 198.51.100.1

    incus exec "${ctPrefix}A" --disable-stdin -- nc -w2 198.51.100.1 8080

    # Changing the address set updates the set in the inet table.
    incus network address-set add "${ctPrefix}set" 198.51.100.9
    nft list set inet incus "${ctPrefix}set_ipv4" | grep -F 198.51.100.9

    # Check stateless rules and default actions are refused on routed NICs.
    incus network acl create "${ctPrefix}C"
    incus network acl rule add "${ctPrefix}C" egress action=allow-stateless protocol=tcp destination_port=2222
    ! incus config device set "${ctPrefix}A" eth0 security.acls="${ctPrefix}C" || false
    ! incus config device set "${ctPrefix}A" eth0 security.acls.default.egress.action=allow-stateless || false
    ! incus network acl rule add "${ctPrefix}A" egress action=allow-stateless protocol=tcp destination_port=2222 || false

    # Change the default actions to allow.
    incus config device set "${ctPrefix}A" eth0 security.acls.default.ingress.action=allow security.acls.default.egress.action=allow
    ! nft -nn list chain inet incus "routedin.${ctPrefix}A.eth0" | grep -q "reject" || false
    ! nft -nn list chain inet incus "routedout.${ctPrefix}A.eth0" | grep -q "drop" || false
    incus exec "${ctPrefix}A" -- ping -c2 -W5 -s 100 198.51.100.1

    # Unsetting the ACLs removes the filter, and the address set no rule uses.
    incus config device set "${ctPrefix}A" eth0 security.acls=
    ! nft -nn list chain inet incus "routedin.${ctPrefix}A.eth0" || false
    ! nft -nn list chain inet incus "routedout.${ctPrefix}A.eth0" || false
    ! nft -nn list chain inet incus "routedfwd.${ctPrefix}A.eth0" || false
    ! nft list set inet incus "${ctPrefix}set_ipv4" || false
    incus exec "${ctPrefix}A" -- ping -c2 -W5 192.0.2.3

    # ACLs set while running are applied, and removed when the instance stops.
    incus config device set "${ctPrefix}A" eth0 security.acls="${ctPrefix}A"
    nft -nn list chain inet incus "routedin.${ctPrefix}A.eth0"
    incus stop -f "${ctPrefix}A"
    ! nft -nn list chain inet incus "routedin.${ctPrefix}A.eth0" || false

    # And applied again at start.
    incus start "${ctPrefix}A"
    nft -nn list chain inet incus "routedin.${ctPrefix}A.eth0"

    # In a VRF, the input interface is the VRF device, so egress rules must match the enslaved device.
    vrfName="incvrf$$"
    ip link add "${vrfName}" type vrf table 1100
    ip link set "${vrfName}" up
    ip addr add 198.51.100.2/32 dev "${vrfName}"

    incus network acl create "${ctPrefix}D"
    incus network acl rule add "${ctPrefix}D" ingress action=allow protocol=icmp4 icmp_type=8
    incus network acl rule add "${ctPrefix}D" egress action=allow protocol=icmp4 icmp_type=8 destination=198.51.100.2

    incus init testimage "${ctPrefix}C"
    incus config device add "${ctPrefix}C" eth0 nic nictype=routed name=eth0 vrf="${vrfName}" \
        ipv4.address=192.0.2.4 security.acls="${ctPrefix}D"
    incus init testimage "${ctPrefix}D"
    incus config device add "${ctPrefix}D" eth0 nic nictype=routed name=eth0 vrf="${vrfName}" ipv4.address=192.0.2.5
    incus start "${ctPrefix}C" "${ctPrefix}D"

    nft -nn list chain inet incus "routedin.${ctPrefix}C.eth0" | grep -q "sdifname"
    nft -nn list chain inet incus "routedfwd.${ctPrefix}C.eth0" | grep -q "sdifname"

    # C to the VRF address (input) is allowed, C to D (forward) is rejected, D to C (forward) is allowed.
    incus exec "${ctPrefix}C" -- ping -c2 -W5 198.51.100.2
    ! incus exec "${ctPrefix}C" -- ping -c2 -W2 192.0.2.5 || false
    incus exec "${ctPrefix}D" -- ping -c2 -W5 192.0.2.4

    # Host to C (output) is allowed for ping only.
    ip vrf exec "${vrfName}" ping -c2 -W5 192.0.2.4
    incus network acl rule remove "${ctPrefix}D" ingress action=allow protocol=icmp4 icmp_type=8
    ! ip vrf exec "${vrfName}" ping -c2 -W2 192.0.2.4 || false
    ! incus exec "${ctPrefix}D" -- ping -c2 -W2 192.0.2.4 || false

    incus delete -f "${ctPrefix}C" "${ctPrefix}D"
    incus network acl delete "${ctPrefix}D"
    ip link delete "${vrfName}"

    # A routed NIC in a profile uses the ACL through its instances.
    incus profile create "${ctPrefix}"
    ! incus profile device add "${ctPrefix}" eth0 nic nictype=routed security.acls="${ctPrefix}C" || false
    incus profile device add "${ctPrefix}" eth0 nic nictype=routed security.acls="${ctPrefix}B"
    incus network acl show "${ctPrefix}B" | grep -F "/1.0/profiles/${ctPrefix}"
    incus profile delete "${ctPrefix}"

    incus delete -f "${ctPrefix}A" "${ctPrefix}B"
    ! nft -nn list chain inet incus "routedin.${ctPrefix}A.eth0" || false
    incus network acl delete "${ctPrefix}A"
    incus network acl delete "${ctPrefix}B"
    incus network acl delete "${ctPrefix}C"
    incus network address-set delete "${ctPrefix}set"
    ip link delete "${dummyName}"
}
