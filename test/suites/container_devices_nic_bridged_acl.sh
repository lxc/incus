test_container_devices_nic_bridged_acl() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  firewallDriver=$(lxc info | awk -F ":" '/firewall:/{gsub(/ /, "", $0); print $2}')

  if [ "$firewallDriver" != "xtables" ] && [ "$firewallDriver" != "nftables" ]; then
    echo "Unrecognised firewall driver: ${firewallDriver}"
    false
  fi

  ctPrefix="nt$$"
  brName="inct$$"

  # Standard bridge.
  lxc network create "${brName}" \
        ipv6.dhcp.stateful=true \
        ipv4.address=192.0.2.1/24 \
        ipv6.address=2001:db8::1/64

  # Create empty ACL and apply to network.
  lxc network acl create "${brName}A"
  lxc network set "${brName}" security.acls="${brName}A"

  # Check ACL jump rules, and chain with default reject rules created.
  if [ "$firewallDriver" = "xtables" ]; then
      iptables -S | grep -c "\-j lxd_acl_${brName}" | grep 4
      iptables -S "lxd_acl_${brName}" | grep -c "\-j REJECT" | grep 2
  else
      nft -nn list chain inet lxd "aclin.${brName}" | grep -c "jump acl.${brName}" | grep 1
      nft -nn list chain inet lxd "aclout.${brName}" | grep -c "jump acl.${brName}" | grep 1
      nft -nn list chain inet lxd "aclfwd.${brName}" | grep -c "jump acl.${brName}" | grep 2
      nft -nn list chain inet lxd "acl.${brName}" | grep -c "reject" | grep 2
  fi

  # Unset ACLs and check the firewall config is cleaned up.
  lxc network unset "${brName}" security.acls
  if [ "$firewallDriver" = "xtables" ]; then
      ! iptables -S | grep "\-j lxd_acl_${brName}" || false
      ! iptables -S "lxd_acl_${brName}" || false
  else
      ! nft -nn list chain inet lxd "aclin.${brName}" || false
      ! nft -nn list chain inet lxd "aclout.${brName}" || false
      ! nft -nn list chain inet lxd "aclfwd.${brName}" || false
      ! nft -nn list chain inet lxd "acl.${brName}" || false
  fi

  # Set ACLs, then delete network and check the firewall config is cleaned up.
  lxc network set "${brName}" security.acls="${brName}A"

  # Check ACL jump rules, and chain with default reject rules created.
  if [ "$firewallDriver" = "xtables" ]; then
      iptables -S | grep -c "\-j lxd_acl_${brName}" | grep 4
      iptables -S "lxd_acl_${brName}" | grep -c "\-j REJECT" | grep 2
  else
      nft -nn list chain inet lxd "aclin.${brName}" | grep -c "jump acl.${brName}" | grep 1
      nft -nn list chain inet lxd "aclout.${brName}" | grep -c "jump acl.${brName}" | grep 1
      nft -nn list chain inet lxd "aclfwd.${brName}" | grep -c "jump acl.${brName}" | grep 2
      nft -nn list chain inet lxd "acl.${brName}" | grep -c "reject" | grep 2
  fi

  # Delete network and check the firewall config is cleaned up.
  lxc network delete "${brName}"
  if [ "$firewallDriver" = "xtables" ]; then
      ! iptables -S | grep "\-j lxd_acl_${brName}" || false
      ! iptables -S "lxd_acl_${brName}" || false
  else
      ! nft -nn list chain inet lxd "aclin.${brName}" || false
      ! nft -nn list chain inet lxd "aclout.${brName}" || false
      ! nft -nn list chain inet lxd "aclfwd.${brName}" || false
      ! nft -nn list chain inet lxd "acl.${brName}" || false
  fi

  # Create network and specify ACL at create time.
  lxc network create "${brName}" \
        ipv6.dhcp.stateful=true \
        ipv4.address=192.0.2.1/24 \
        ipv6.address=2001:db8::1/64 \
        security.acls="${brName}A" \
        raw.dnsmasq='host-record=testhost.test,192.0.2.1,2001:db8::1'

  # Change default actions to drop.
  lxc network set "${brName}" \
        security.acls.default.ingress.action=drop \
        security.acls.default.egress.action=drop

  # Check default reject rules changed to drop.
  if [ "$firewallDriver" = "xtables" ]; then
      iptables -S "lxd_acl_${brName}" | grep -c "\-j DROP" | grep 2
  else
      nft -nn list chain inet lxd "acl.${brName}" | grep -c "drop" | grep 2
  fi

  # Change default actions to reject.
  lxc network set "${brName}" \
        security.acls.default.ingress.action=reject \
        security.acls.default.egress.action=reject

  # Check default reject rules changed to reject.
  if [ "$firewallDriver" = "xtables" ]; then
      iptables -S "lxd_acl_${brName}" | grep -c "\-j REJECT" | grep 2
  else
      nft -nn list chain inet lxd "acl.${brName}" | grep -c "reject" | grep 2
  fi

  # Create profile for new containers.
  lxc profile copy default "${ctPrefix}"

  # Modify profile nictype and parent in atomic operation to ensure validation passes.
  lxc profile show "${ctPrefix}" | sed  "s/nictype: p2p/network: ${brName}/" | lxc profile edit "${ctPrefix}"

  lxc init testimage "${ctPrefix}A" -p "${ctPrefix}"
  lxc start "${ctPrefix}A"

  # Check DHCP works for baseline rules.
  lxc exec "${ctPrefix}A" -- udhcpc -f -i eth0 -n -q -t5 2>&1 | grep 'obtained'

  # Request DHCPv6 lease (if udhcpc6 is in busybox image).
  busyboxUdhcpc6=1
  if ! lxc exec "${ctPrefix}A" -- busybox --list | grep udhcpc6 ; then
    busyboxUdhcpc6=0
  fi

  if [ "$busyboxUdhcpc6" = "1" ]; then
    lxc exec "${ctPrefix}A" -- udhcpc6 -f -i eth0 -n -q -t5 2>&1 | grep 'IPv6 obtained'
  fi

  # Add static IPs to container.
  lxc exec "${ctPrefix}A" -- ip a add 192.0.2.2/24 dev eth0
  lxc exec "${ctPrefix}A" -- ip a add 2001:db8::2/64 dev eth0

  # Check ICMP to bridge is blocked.
  ! lxc exec "${ctPrefix}A" -- ping -c2 -4 -W5 192.0.2.1 || false
  ! lxc exec "${ctPrefix}A" -- ping -c2 -6 -W5 2001:db8::1 || false

  # Allow ICMP to bridge host.
  lxc network acl rule add "${brName}A" egress action=allow destination=192.0.2.1/32 protocol=icmp4 icmp_type=8
  lxc network acl rule add "${brName}A" egress action=allow destination=2001:db8::1/128 protocol=icmp6 icmp_type=128
  lxc exec "${ctPrefix}A" -- ping -c2 -4 -W5 192.0.2.1
  lxc exec "${ctPrefix}A" -- ping -c2 -6 -W5 2001:db8::1

  # Check DNS resolution (and connection tracking in the process).
  lxc exec "${ctPrefix}A" -- nslookup -type=a testhost.test 192.0.2.1
  lxc exec "${ctPrefix}A" -- nslookup -type=aaaa testhost.test 192.0.2.1
  lxc exec "${ctPrefix}A" -- nslookup -type=a testhost.test 2001:db8::1
  lxc exec "${ctPrefix}A" -- nslookup -type=aaaa testhost.test 2001:db8::1

  # Add new ACL to network with drop rule that prevents ICMP ping to check drop rules get higher priority.
  lxc network acl create "${brName}B"
  lxc network acl rule add "${brName}B" egress action=drop protocol=icmp4 icmp_type=8
  lxc network acl rule add "${brName}B" egress action=drop protocol=icmp6 icmp_type=128

  lxc network set "${brName}" security.acls="${brName}A,${brName}B"

  # Check egress ICMP ping to bridge is blocked.
  ! lxc exec "${ctPrefix}A" -- ping -c2 -4 -W5 192.0.2.1 || false
  ! lxc exec "${ctPrefix}A" -- ping -c2 -6 -W5 2001:db8::1 || false

  # Check ingress ICMPv4 ping is blocked.
  ! ping -c1 -4 192.0.2.2 || false

  # Allow ingress ICMPv4 ping.
  lxc network acl rule add "${brName}A" ingress action=allow destination=192.0.2.2/32 protocol=icmp4 icmp_type=8
  ping -c1 -4 192.0.2.2

  # Check egress ICMPv6 ping from host to bridge is allowed by default (for dnsmasq probing).
  ping -c1 -6 2001:db8::2

  # Check egress TCP.
  lxc exec "${ctPrefix}A" --disable-stdin -- nc -w2 192.0.2.1 53
  lxc exec "${ctPrefix}A" --disable-stdin -- nc -w2 2001:db8::1 53

  nc -l -p 8080 -q0 -s 192.0.2.1 </dev/null >/dev/null &
  nc -l -p 8080 -q0 -s 2001:db8::1 </dev/null >/dev/null &

  ! lxc exec "${ctPrefix}A" --disable-stdin -- nc -w2 192.0.2.1 8080 || false
  ! lxc exec "${ctPrefix}A" --disable-stdin -- nc -w2 2001:db8::1 8080 || false

  lxc network acl rule add "${brName}A" egress action=allow destination=192.0.2.1/32 protocol=tcp destination_port=8080
  lxc network acl rule add "${brName}A" egress action=allow destination=2001:db8::1/128 protocol=tcp destination_port=8080

  lxc exec "${ctPrefix}A" --disable-stdin -- nc -w2 192.0.2.1 8080
  lxc exec "${ctPrefix}A" --disable-stdin -- nc -w2 2001:db8::1 8080

  # Check can't delete ACL that is in use.
  ! lxc network acl delete "${brName}A" || false

  lxc delete -f "${ctPrefix}A"
  lxc profile delete "${ctPrefix}"
  lxc network delete "${brName}"
  lxc network acl delete "${brName}A"
  lxc network acl delete "${brName}B"
}
