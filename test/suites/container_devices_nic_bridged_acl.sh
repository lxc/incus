test_container_devices_nic_bridged_acl() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  firewallDriver=$(inc info | awk -F ":" '/firewall:/{gsub(/ /, "", $0); print $2}')

  if [ "$firewallDriver" != "xtables" ] && [ "$firewallDriver" != "nftables" ]; then
    echo "Unrecognised firewall driver: ${firewallDriver}"
    false
  fi

  ctPrefix="nt$$"
  brName="inct$$"

  # Standard bridge.
  inc network create "${brName}" \
        ipv6.dhcp.stateful=true \
        ipv4.address=192.0.2.1/24 \
        ipv6.address=2001:db8::1/64

  # Create empty ACL and apply to network.
  inc network acl create "${brName}A"
  inc network set "${brName}" security.acls="${brName}A"

  # Check ACL jump rules, and chain with default reject rules created.
  if [ "$firewallDriver" = "xtables" ]; then
      iptables -S | grep -c "\-j incus_acl_${brName}" | grep 4
      iptables -S "incus_acl_${brName}" | grep -c "\-j REJECT" | grep 2
  else
      nft -nn list chain inet incus "aclin.${brName}" | grep -c "jump acl.${brName}" | grep 1
      nft -nn list chain inet incus "aclout.${brName}" | grep -c "jump acl.${brName}" | grep 1
      nft -nn list chain inet incus "aclfwd.${brName}" | grep -c "jump acl.${brName}" | grep 2
      nft -nn list chain inet incus "acl.${brName}" | grep -c "reject" | grep 2
  fi

  # Unset ACLs and check the firewall config is cleaned up.
  inc network unset "${brName}" security.acls
  if [ "$firewallDriver" = "xtables" ]; then
      ! iptables -S | grep "\-j incus_acl_${brName}" || false
      ! iptables -S "incus_acl_${brName}" || false
  else
      ! nft -nn list chain inet incus "aclin.${brName}" || false
      ! nft -nn list chain inet incus "aclout.${brName}" || false
      ! nft -nn list chain inet incus "aclfwd.${brName}" || false
      ! nft -nn list chain inet incus "acl.${brName}" || false
  fi

  # Set ACLs, then delete network and check the firewall config is cleaned up.
  inc network set "${brName}" security.acls="${brName}A"

  # Check ACL jump rules, and chain with default reject rules created.
  if [ "$firewallDriver" = "xtables" ]; then
      iptables -S | grep -c "\-j incus_acl_${brName}" | grep 4
      iptables -S "incus_acl_${brName}" | grep -c "\-j REJECT" | grep 2
  else
      nft -nn list chain inet incus "aclin.${brName}" | grep -c "jump acl.${brName}" | grep 1
      nft -nn list chain inet incus "aclout.${brName}" | grep -c "jump acl.${brName}" | grep 1
      nft -nn list chain inet incus "aclfwd.${brName}" | grep -c "jump acl.${brName}" | grep 2
      nft -nn list chain inet incus "acl.${brName}" | grep -c "reject" | grep 2
  fi

  # Delete network and check the firewall config is cleaned up.
  inc network delete "${brName}"
  if [ "$firewallDriver" = "xtables" ]; then
      ! iptables -S | grep "\-j incus_acl_${brName}" || false
      ! iptables -S "incus_acl_${brName}" || false
  else
      ! nft -nn list chain inet incus "aclin.${brName}" || false
      ! nft -nn list chain inet incus "aclout.${brName}" || false
      ! nft -nn list chain inet incus "aclfwd.${brName}" || false
      ! nft -nn list chain inet incus "acl.${brName}" || false
  fi

  # Create network and specify ACL at create time.
  inc network create "${brName}" \
        ipv6.dhcp.stateful=true \
        ipv4.address=192.0.2.1/24 \
        ipv6.address=2001:db8::1/64 \
        security.acls="${brName}A" \
        raw.dnsmasq='host-record=testhost.test,192.0.2.1,2001:db8::1'

  # Change default actions to drop.
  inc network set "${brName}" \
        security.acls.default.ingress.action=drop \
        security.acls.default.egress.action=drop

  # Check default reject rules changed to drop.
  if [ "$firewallDriver" = "xtables" ]; then
      iptables -S "incus_acl_${brName}" | grep -c "\-j DROP" | grep 2
  else
      nft -nn list chain inet incus "acl.${brName}" | grep -c "drop" | grep 2
  fi

  # Change default actions to reject.
  inc network set "${brName}" \
        security.acls.default.ingress.action=reject \
        security.acls.default.egress.action=reject

  # Check default reject rules changed to reject.
  if [ "$firewallDriver" = "xtables" ]; then
      iptables -S "incus_acl_${brName}" | grep -c "\-j REJECT" | grep 2
  else
      nft -nn list chain inet incus "acl.${brName}" | grep -c "reject" | grep 2
  fi

  # Create profile for new containers.
  inc profile copy default "${ctPrefix}"

  # Modify profile nictype and parent in atomic operation to ensure validation passes.
  inc profile show "${ctPrefix}" | sed  "s/nictype: p2p/network: ${brName}/" | inc profile edit "${ctPrefix}"

  inc init testimage "${ctPrefix}A" -p "${ctPrefix}"
  inc start "${ctPrefix}A"

  # Check DHCP works for baseline rules.
  inc exec "${ctPrefix}A" -- udhcpc -f -i eth0 -n -q -t5 2>&1 | grep 'obtained'

  # Request DHCPv6 lease (if udhcpc6 is in busybox image).
  busyboxUdhcpc6=1
  if ! inc exec "${ctPrefix}A" -- busybox --list | grep udhcpc6 ; then
    busyboxUdhcpc6=0
  fi

  if [ "$busyboxUdhcpc6" = "1" ]; then
    inc exec "${ctPrefix}A" -- udhcpc6 -f -i eth0 -n -q -t5 2>&1 | grep 'IPv6 obtained'
  fi

  # Add static IPs to container.
  inc exec "${ctPrefix}A" -- ip a add 192.0.2.2/24 dev eth0
  inc exec "${ctPrefix}A" -- ip a add 2001:db8::2/64 dev eth0

  # Check ICMP to bridge is blocked.
  ! inc exec "${ctPrefix}A" -- ping -c2 -4 -W5 192.0.2.1 || false
  ! inc exec "${ctPrefix}A" -- ping -c2 -6 -W5 2001:db8::1 || false

  # Allow ICMP to bridge host.
  inc network acl rule add "${brName}A" egress action=allow destination=192.0.2.1/32 protocol=icmp4 icmp_type=8
  inc network acl rule add "${brName}A" egress action=allow destination=2001:db8::1/128 protocol=icmp6 icmp_type=128
  inc exec "${ctPrefix}A" -- ping -c2 -4 -W5 192.0.2.1
  inc exec "${ctPrefix}A" -- ping -c2 -6 -W5 2001:db8::1

  # Check DNS resolution (and connection tracking in the process).
  inc exec "${ctPrefix}A" -- nslookup -type=a testhost.test 192.0.2.1
  inc exec "${ctPrefix}A" -- nslookup -type=aaaa testhost.test 192.0.2.1
  inc exec "${ctPrefix}A" -- nslookup -type=a testhost.test 2001:db8::1
  inc exec "${ctPrefix}A" -- nslookup -type=aaaa testhost.test 2001:db8::1

  # Add new ACL to network with drop rule that prevents ICMP ping to check drop rules get higher priority.
  inc network acl create "${brName}B"
  inc network acl rule add "${brName}B" egress action=drop protocol=icmp4 icmp_type=8
  inc network acl rule add "${brName}B" egress action=drop protocol=icmp6 icmp_type=128

  inc network set "${brName}" security.acls="${brName}A,${brName}B"

  # Check egress ICMP ping to bridge is blocked.
  ! inc exec "${ctPrefix}A" -- ping -c2 -4 -W5 192.0.2.1 || false
  ! inc exec "${ctPrefix}A" -- ping -c2 -6 -W5 2001:db8::1 || false

  # Check ingress ICMPv4 ping is blocked.
  ! ping -c1 -4 192.0.2.2 || false

  # Allow ingress ICMPv4 ping.
  inc network acl rule add "${brName}A" ingress action=allow destination=192.0.2.2/32 protocol=icmp4 icmp_type=8
  ping -c1 -4 192.0.2.2

  # Check egress ICMPv6 ping from host to bridge is allowed by default (for dnsmasq probing).
  ping -c1 -6 2001:db8::2

  # Check egress TCP.
  inc exec "${ctPrefix}A" --disable-stdin -- nc -w2 192.0.2.1 53
  inc exec "${ctPrefix}A" --disable-stdin -- nc -w2 2001:db8::1 53

  nc -l -p 8080 -q0 -s 192.0.2.1 </dev/null >/dev/null &
  nc -l -p 8080 -q0 -s 2001:db8::1 </dev/null >/dev/null &

  ! inc exec "${ctPrefix}A" --disable-stdin -- nc -w2 192.0.2.1 8080 || false
  ! inc exec "${ctPrefix}A" --disable-stdin -- nc -w2 2001:db8::1 8080 || false

  inc network acl rule add "${brName}A" egress action=allow destination=192.0.2.1/32 protocol=tcp destination_port=8080
  inc network acl rule add "${brName}A" egress action=allow destination=2001:db8::1/128 protocol=tcp destination_port=8080

  inc exec "${ctPrefix}A" --disable-stdin -- nc -w2 192.0.2.1 8080
  inc exec "${ctPrefix}A" --disable-stdin -- nc -w2 2001:db8::1 8080

  # Check can't delete ACL that is in use.
  ! inc network acl delete "${brName}A" || false

  inc delete -f "${ctPrefix}A"
  inc profile delete "${ctPrefix}"
  inc network delete "${brName}"
  inc network acl delete "${brName}A"
  inc network acl delete "${brName}B"
}
