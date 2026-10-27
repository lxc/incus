test_container_devices_nic_bridged_acl() {
    ensure_import_testimage
    ensure_has_localhost_remote "${INCUS_ADDR}"

    ctPrefix="nt$$"
    brName="inct$$"

    # Standard bridge.
    incus network create "${brName}" \
        ipv6.dhcp.stateful=true \
        ipv4.address=192.0.2.1/24 \
        ipv6.address=2001:db8::1/64

    # Create empty ACL and apply to network.
    incus network acl create "${brName}A"
    incus network set "${brName}" security.acls="${brName}A"

    # Check ACL jump rules, and chain with default reject rules created.
    nft -nn list chain inet incus "aclin.${brName}" | grep -c "jump acl.${brName}" | grep 1
    nft -nn list chain inet incus "aclout.${brName}" | grep -c "jump acl.${brName}" | grep 1
    nft -nn list chain inet incus "aclfwd.${brName}" | grep -c "jump acl.${brName}" | grep 2
    nft -nn list chain inet incus "acl.${brName}" | grep -c "reject" | grep 2

    # Check a rule mixing an IPv4 literal with an address set isn't widened for IPv6.
    incus network address-set create "${brName}set"
    incus network address-set add "${brName}set" 192.0.2.9 2001:db8::9
    incus network acl rule add "${brName}A" ingress action=allow protocol=tcp source=192.0.2.1 "destination=\\\$${brName}set" destination_port=443
    nft -nn list chain inet incus "acl.${brName}" | grep -q "@${brName}set_ipv4"
    ! nft -nn list chain inet incus "acl.${brName}" | grep -q "@${brName}set_ipv6" || false
    incus network acl rule remove "${brName}A" ingress protocol=tcp source=192.0.2.1 "destination=\\\$${brName}set"
    incus network address-set delete "${brName}set"

    # Unset ACLs and check the firewall config is cleaned up.
    incus network unset "${brName}" security.acls
    ! nft -nn list chain inet incus "aclin.${brName}" || false
    ! nft -nn list chain inet incus "aclout.${brName}" || false
    ! nft -nn list chain inet incus "aclfwd.${brName}" || false
    ! nft -nn list chain inet incus "acl.${brName}" || false

    # Set ACLs, then delete network and check the firewall config is cleaned up.
    incus network set "${brName}" security.acls="${brName}A"

    # Check ACL jump rules, and chain with default reject rules created.
    nft -nn list chain inet incus "aclin.${brName}" | grep -c "jump acl.${brName}" | grep 1
    nft -nn list chain inet incus "aclout.${brName}" | grep -c "jump acl.${brName}" | grep 1
    nft -nn list chain inet incus "aclfwd.${brName}" | grep -c "jump acl.${brName}" | grep 2
    nft -nn list chain inet incus "acl.${brName}" | grep -c "reject" | grep 2

    # Delete network and check the firewall config is cleaned up.
    incus network delete "${brName}"
    ! nft -nn list chain inet incus "aclin.${brName}" || false
    ! nft -nn list chain inet incus "aclout.${brName}" || false
    ! nft -nn list chain inet incus "aclfwd.${brName}" || false
    ! nft -nn list chain inet incus "acl.${brName}" || false

    # Create network and specify ACL at create time.
    incus network create "${brName}" \
        ipv6.dhcp.stateful=true \
        ipv4.address=192.0.2.1/24 \
        ipv6.address=2001:db8::1/64 \
        security.acls="${brName}A" \
        raw.dnsmasq='host-record=testhost.test,192.0.2.1,2001:db8::1'

    # Change default actions to drop.
    incus network set "${brName}" \
        security.acls.default.ingress.action=drop \
        security.acls.default.egress.action=drop

    # Check default reject rules changed to drop.
    nft -nn list chain inet incus "acl.${brName}" | grep -c "drop" | grep 2

    # Change default actions to reject.
    incus network set "${brName}" \
        security.acls.default.ingress.action=reject \
        security.acls.default.egress.action=reject

    # Check default reject rules changed to reject.
    nft -nn list chain inet incus "acl.${brName}" | grep -c "reject" | grep 2

    # Create profile for new containers.
    incus profile copy default "${ctPrefix}"

    # Modify profile nictype and parent in atomic operation to ensure validation passes.
    incus profile show "${ctPrefix}" | sed "s/nictype: p2p/network: ${brName}/" | incus profile edit "${ctPrefix}"

    incus init testimage "${ctPrefix}A" -p "${ctPrefix}"
    incus start "${ctPrefix}A"

    # Check DHCP works for baseline rules.
    incus exec "${ctPrefix}A" -- udhcpc -f -i eth0 -n -q -t5 2>&1 | grep 'obtained'

    # Request DHCPv6 lease (if udhcpc6 is in busybox image).
    busyboxUdhcpc6=1
    if ! incus exec "${ctPrefix}A" -- busybox --list | grep udhcpc6; then
        busyboxUdhcpc6=0
    fi

    if [ "$busyboxUdhcpc6" = "1" ]; then
        incus exec "${ctPrefix}A" -- udhcpc6 -f -i eth0 -n -q -t5 2>&1 | grep 'IPv6 obtained'
    fi

    # Add static IPs to container.
    incus exec "${ctPrefix}A" -- ip a add 192.0.2.2/24 dev eth0
    incus exec "${ctPrefix}A" -- ip a add 2001:db8::2/64 dev eth0

    # Check ICMP to bridge is blocked.
    ! incus exec "${ctPrefix}A" -- ping -c2 -4 -W5 192.0.2.1 || false
    ! incus exec "${ctPrefix}A" -- ping -c2 -6 -W5 2001:db8::1 || false

    # Allow ICMP to bridge host.
    incus network acl rule add "${brName}A" egress action=allow destination=192.0.2.1/32 protocol=icmp4 icmp_type=8
    incus network acl rule add "${brName}A" egress action=allow destination=2001:db8::1/128 protocol=icmp6 icmp_type=128

    # ICMPv6 must be matched behind extension headers, not on the fixed header.
    ! nft -nn list chain inet incus "acl.${brName}" | grep -q "nexthdr" || false
    incus exec "${ctPrefix}A" -- ping -c2 -4 -W5 192.0.2.1
    incus exec "${ctPrefix}A" -- ping -c2 -6 -W5 2001:db8::1

    # Check DNS resolution (and connection tracking in the process).
    incus exec "${ctPrefix}A" -- nslookup -type=a testhost.test 192.0.2.1
    incus exec "${ctPrefix}A" -- nslookup -type=aaaa testhost.test 192.0.2.1
    incus exec "${ctPrefix}A" -- nslookup -type=a testhost.test 2001:db8::1
    incus exec "${ctPrefix}A" -- nslookup -type=aaaa testhost.test 2001:db8::1

    # Add new ACL to network with drop rule that prevents ICMP ping to check drop rules get higher priority.
    incus network acl create "${brName}B"
    incus network acl rule add "${brName}B" egress action=drop protocol=icmp4 icmp_type=8
    incus network acl rule add "${brName}B" egress action=drop protocol=icmp6 icmp_type=128

    incus network set "${brName}" security.acls="${brName}A,${brName}B"

    # Check egress ICMP ping to bridge is blocked.
    ! incus exec "${ctPrefix}A" -- ping -c2 -4 -W5 192.0.2.1 || false
    ! incus exec "${ctPrefix}A" -- ping -c2 -6 -W5 2001:db8::1 || false

    # Check ingress ICMPv4 ping is blocked.
    ! ping -c1 -4 192.0.2.2 || false

    # Allow ingress ICMPv4 ping.
    incus network acl rule add "${brName}A" ingress action=allow destination=192.0.2.2/32 protocol=icmp4 icmp_type=8
    ping -c1 -4 192.0.2.2

    # Check egress ICMPv6 ping from host to bridge is allowed by default (for dnsmasq probing).
    ping -c1 -6 2001:db8::2

    # Check egress TCP.
    incus exec "${ctPrefix}A" --disable-stdin -- nc -w2 192.0.2.1 53
    incus exec "${ctPrefix}A" --disable-stdin -- nc -w2 2001:db8::1 53

    nc -l -p 8080 -q0 -s 192.0.2.1 < /dev/null > /dev/null &
    nc -l -p 8080 -q0 -s 2001:db8::1 < /dev/null > /dev/null &

    ! incus exec "${ctPrefix}A" --disable-stdin -- nc -w2 192.0.2.1 8080 || false
    ! incus exec "${ctPrefix}A" --disable-stdin -- nc -w2 2001:db8::1 8080 || false

    incus network acl rule add "${brName}A" egress action=allow destination=192.0.2.1/32 protocol=tcp destination_port=8080
    incus network acl rule add "${brName}A" egress action=allow destination=2001:db8::1/128 protocol=tcp destination_port=8080

    incus exec "${ctPrefix}A" --disable-stdin -- nc -w2 192.0.2.1 8080
    incus exec "${ctPrefix}A" --disable-stdin -- nc -w2 2001:db8::1 8080

    # Check can't delete ACL that is in use.
    ! incus network acl delete "${brName}A" || false

    # Check a failed ACL update doesn't leave a bridged NIC unfiltered.
    incus network acl create "${brName}C"
    incus config device override "${ctPrefix}A" eth0 security.acls="${brName}C" security.acls.default.ingress.action=drop security.acls.default.egress.action=drop
    nft -nn list chain bridge incus "in.${ctPrefix}A.eth0" | grep -c "drop" | grep -v "^0$"
    ! incus network acl rule add "${brName}C" egress action=allow-stateless protocol=tcp destination_port=2222 || false
    ! incus network acl show "${brName}C" | grep -F "allow-stateless" || false
    nft -nn list chain bridge incus "in.${ctPrefix}A.eth0" | grep -c "drop" | grep -v "^0$"

    # Check stateless rules and default actions are rejected on bridge networks and NICs.
    incus network acl create "${brName}D"
    incus network acl rule add "${brName}D" egress action=allow-stateless protocol=tcp destination_port=2222
    ! incus config device set "${ctPrefix}A" eth0 security.acls="${brName}D" || false
    ! incus config device set "${ctPrefix}A" eth0 security.acls.default.egress.action=allow-stateless || false
    ! incus network set "${brName}" security.acls="${brName}A,${brName}D" || false
    ! incus network set "${brName}" security.acls.default.egress.action=allow-stateless || false

    # Check the NIC filters are removed when its ACL is unset.
    incus config device set "${ctPrefix}A" eth0 security.acls=
    ! nft -nn list chain bridge incus "in.${ctPrefix}A.eth0" || false
    incus network acl delete "${brName}C"
    incus network acl delete "${brName}D"

    incus delete -f "${ctPrefix}A"
    incus profile delete "${ctPrefix}"
    incus network delete "${brName}"
    incus network acl delete "${brName}A"
    incus network acl delete "${brName}B"
}
