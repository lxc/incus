test_network_forward() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  firewallDriver=$(lxc info | awk -F ":" '/firewall:/{gsub(/ /, "", $0); print $2}')
  netName=inct$$

  lxc network create "${netName}" \
        ipv4.address=192.0.2.1/24 \
        ipv6.address=fd42:4242:4242:1010::1/64

  # Check creating a forward with an unspecified IPv4 address fails.
  ! lxc network forward create "${netName}" 0.0.0.0 || false

  # Check creating a forward with an unspecified IPv6 address fails.
  ! lxc network forward create "${netName}" :: || false

  # Check creating empty forward doesn't create any firewall rules.
  lxc network forward create "${netName}" 198.51.100.1
  if [ "$firewallDriver" = "xtables" ]; then
    ! iptables -w -t nat -S | grep -c "generated for LXD network-forward ${netName}" || false
  else
    ! nft -nn list chain inet lxd "fwdprert.${netName}" || false
    ! nft -nn list chain inet lxd "fwdout.${netName}" || false
    ! nft -nn list chain inet lxd "fwdpstrt.${netName}" || false
  fi

  # Check forward is exported via BGP prefixes.
  lxc query /internal/testing/bgp | grep "198.51.100.1/32"

  lxc network forward delete "${netName}" 198.51.100.1

  # Check deleting network forward removes forward BGP prefix.
  ! lxc query /internal/testing/bgp | grep "198.51.100.1/32" || false

  # Check creating forward with default target creates valid firewall rules.
  lxc network forward create "${netName}" 198.51.100.1 target_address=192.0.2.2
  if [ "$firewallDriver" = "xtables" ]; then
    iptables -w -t nat -S | grep -- "-A PREROUTING -d 198.51.100.1/32 -m comment --comment \"generated for LXD network-forward ${netName}\" -j DNAT --to-destination 192.0.2.2"
    iptables -w -t nat -S | grep -- "-A OUTPUT -d 198.51.100.1/32 -m comment --comment \"generated for LXD network-forward ${netName}\" -j DNAT --to-destination 192.0.2.2"
    iptables -w -t nat -S | grep -- "-A POSTROUTING -s 192.0.2.2/32 -d 192.0.2.2/32 -m comment --comment \"generated for LXD network-forward ${netName}\" -j MASQUERADE"
  else
    nft -nn list chain inet lxd "fwdprert.${netName}" | grep "ip daddr 198.51.100.1 dnat ip to 192.0.2.2"
    nft -nn list chain inet lxd "fwdout.${netName}" | grep "ip daddr 198.51.100.1 dnat ip to 192.0.2.2"
    nft -nn list chain inet lxd "fwdpstrt.${netName}"  | grep "ip saddr 192.0.2.2 ip daddr 192.0.2.2 masquerade"
  fi

  # Check unsetting default target clears firewall rules.
  lxc network forward unset "${netName}" 198.51.100.1 target_address
  if [ "$firewallDriver" = "xtables" ]; then
    ! iptables -w -t nat -S | grep -c "generated for LXD network-forward ${netName}" || false
  else
    ! nft -nn list chain inet lxd "fwdprert.${netName}" || false
    ! nft -nn list chain inet lxd "fwdout.${netName}" || false
    ! nft -nn list chain inet lxd "fwdpstrt.${netName}" || false
  fi

  # Check can't add a port based rule to the same target IP as the default target.
  lxc network forward set "${netName}" 198.51.100.1 target_address=192.0.2.2
  ! lxc network forward port add "${netName}" 198.51.100.1 tcp 80 192.0.2.2 || false

  # Check can't add a port based rule to multiple target ports if only one listener port.
  ! lxc network forward port add "${netName}" 198.51.100.1 tcp 80 192.0.2.3 80-81 || false

  # Check can add a port with a listener range and no target port (so it uses same range for target ports).
  lxc network forward port add "${netName}" 198.51.100.1 tcp 80-81 192.0.2.3
  if [ "$firewallDriver" = "xtables" ]; then
    iptables -w -t nat -S | grep -- "-A PREROUTING -d 198.51.100.1/32 -p tcp -m tcp --dport 80:81 -m comment --comment \"generated for LXD network-forward ${netName}\" -j DNAT --to-destination 192.0.2.3"
    iptables -w -t nat -S | grep -- "-A OUTPUT -d 198.51.100.1/32 -p tcp -m tcp --dport 80:81 -m comment --comment \"generated for LXD network-forward ${netName}\" -j DNAT --to-destination 192.0.2.3"
    iptables -w -t nat -S | grep -- "-A POSTROUTING -s 192.0.2.3/32 -d 192.0.2.3/32 -p tcp -m tcp --dport 80:81 -m comment --comment \"generated for LXD network-forward ${netName}\" -j MASQUERADE"
  else
    nft -nn list chain inet lxd "fwdprert.${netName}" | grep "ip daddr 198.51.100.1 tcp dport 80-81 dnat ip to 192.0.2.3"
    nft -nn list chain inet lxd "fwdout.${netName}" | grep "ip daddr 198.51.100.1 tcp dport 80-81 dnat ip to 192.0.2.3"
    nft -nn list chain inet lxd "fwdpstrt.${netName}" | grep "ip saddr 192.0.2.3 ip daddr 192.0.2.3 tcp dport 80-81 masquerade"
  fi

  # Check can't add port with duplicate listen port.
  ! lxc network forward port add "${netName}" 198.51.100.1 tcp 80 192.0.2.3 90 || false

  # Check adding port with single listen and target port.
  lxc network forward port add "${netName}" 198.51.100.1 udp 80 192.0.2.3 90
  if [ "$firewallDriver" = "xtables" ]; then
    iptables -w -t nat -S | grep -- "-A PREROUTING -d 198.51.100.1/32 -p udp -m udp --dport 80 -m comment --comment \"generated for LXD network-forward ${netName}\" -j DNAT --to-destination 192.0.2.3:90"
    iptables -w -t nat -S | grep -- "-A OUTPUT -d 198.51.100.1/32 -p udp -m udp --dport 80 -m comment --comment \"generated for LXD network-forward ${netName}\" -j DNAT --to-destination 192.0.2.3:90"
    iptables -w -t nat -S | grep -- "-A POSTROUTING -s 192.0.2.3/32 -d 192.0.2.3/32 -p udp -m udp --dport 90 -m comment --comment \"generated for LXD network-forward ${netName}\" -j MASQUERADE"
  else
    nft -nn list chain inet lxd "fwdprert.${netName}" | grep "ip daddr 198.51.100.1 udp dport 80 dnat ip to 192.0.2.3:90"
    nft -nn list chain inet lxd "fwdout.${netName}" | grep "ip daddr 198.51.100.1 udp dport 80 dnat ip to 192.0.2.3:90"
    nft -nn list chain inet lxd "fwdpstrt.${netName}" | grep "ip saddr 192.0.2.3 ip daddr 192.0.2.3 udp dport 90 masquerade"
  fi

  # Check can't add multi-port listener with mismatch target ports.
  ! lxc network forward port add "${netName}" 198.51.100.1 tcp 82,83,84 192.0.2.3 90,91 || false

  # Check adding port with listen port range and single target port (using mixture of commas and dashes).
  lxc network forward port add "${netName}" 198.51.100.1 tcp 82-83,84 192.0.2.3 90,91-92
  if [ "$firewallDriver" = "xtables" ]; then
    iptables -w -t nat -S | grep -- "-A PREROUTING -d 198.51.100.1/32 -p tcp -m tcp --dport 84 -m comment --comment \"generated for LXD network-forward ${netName}\" -j DNAT --to-destination 192.0.2.3:92"
    iptables -w -t nat -S | grep -- "-A PREROUTING -d 198.51.100.1/32 -p tcp -m tcp --dport 83 -m comment --comment \"generated for LXD network-forward ${netName}\" -j DNAT --to-destination 192.0.2.3:91"
    iptables -w -t nat -S | grep -- "-A PREROUTING -d 198.51.100.1/32 -p tcp -m tcp --dport 82 -m comment --comment \"generated for LXD network-forward ${netName}\" -j DNAT --to-destination 192.0.2.3:90"
    iptables -w -t nat -S | grep -- "-A OUTPUT -d 198.51.100.1/32 -p tcp -m tcp --dport 84 -m comment --comment \"generated for LXD network-forward ${netName}\" -j DNAT --to-destination 192.0.2.3:92"
    iptables -w -t nat -S | grep -- "-A OUTPUT -d 198.51.100.1/32 -p tcp -m tcp --dport 83 -m comment --comment \"generated for LXD network-forward ${netName}\" -j DNAT --to-destination 192.0.2.3:91"
    iptables -w -t nat -S | grep -- "-A OUTPUT -d 198.51.100.1/32 -p tcp -m tcp --dport 82 -m comment --comment \"generated for LXD network-forward ${netName}\" -j DNAT --to-destination 192.0.2.3:90"
    iptables -w -t nat -S | grep -- "-A POSTROUTING -s 192.0.2.3/32 -d 192.0.2.3/32 -p tcp -m tcp --dport 90:92 -m comment --comment \"generated for LXD network-forward ${netName}\" -j MASQUERADE"
  else
    nft -nn list chain inet lxd "fwdprert.${netName}" | grep "ip daddr 198.51.100.1 tcp dport 82 dnat ip to 192.0.2.3:90"
    nft -nn list chain inet lxd "fwdprert.${netName}" | grep "ip daddr 198.51.100.1 tcp dport 83 dnat ip to 192.0.2.3:91"
    nft -nn list chain inet lxd "fwdprert.${netName}" | grep "ip daddr 198.51.100.1 tcp dport 84 dnat ip to 192.0.2.3:92"
    nft -nn list chain inet lxd "fwdout.${netName}" | grep "ip daddr 198.51.100.1 tcp dport 82 dnat ip to 192.0.2.3:90"
    nft -nn list chain inet lxd "fwdout.${netName}" | grep "ip daddr 198.51.100.1 tcp dport 83 dnat ip to 192.0.2.3:91"
    nft -nn list chain inet lxd "fwdout.${netName}" | grep "ip daddr 198.51.100.1 tcp dport 84 dnat ip to 192.0.2.3:92"
    nft -nn list chain inet lxd "fwdpstrt.${netName}" | grep "ip saddr 192.0.2.3 ip daddr 192.0.2.3 tcp dport 90-92 masquerade"
  fi

  # Check deleting multiple rules is prevented without --force, and that it takes effect with --force.
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(iptables -w -t nat -S | grep -c "generated for LXD network-forward ${netName}")" -eq 16 ]
  else
    [ "$(nft -nn list chain inet lxd "fwdprert.${netName}" | wc -l)" -eq 11 ]
    [ "$(nft -nn list chain inet lxd "fwdout.${netName}"| wc -l)" -eq 11 ]
    [ "$(nft -nn list chain inet lxd "fwdpstrt.${netName}" | wc -l)" -eq 9 ]
  fi

  ! lxc network forward port remove "${netName}" 198.51.100.1 tcp || false
  lxc network forward port remove "${netName}" 198.51.100.1 tcp --force

  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(iptables -w -t nat -S | grep -c "generated for LXD network-forward ${netName}")" -eq 6 ]
  else
    [ "$(nft -nn list chain inet lxd "fwdprert.${netName}" | wc -l)" -eq 7 ]
    [ "$(nft -nn list chain inet lxd "fwdout.${netName}"| wc -l)" -eq 7 ]
    [ "$(nft -nn list chain inet lxd "fwdpstrt.${netName}" | wc -l)" -eq 7 ]
  fi

  # Check forward is exported via BGP prefixes before network delete.
  lxc query /internal/testing/bgp | grep "198.51.100.1/32"

  # Check deleting the network clears the forward firewall rules.
  lxc network delete "${netName}"

  # Check deleting network removes forward BGP prefix.
  ! lxc query /internal/testing/bgp | grep "198.51.100.1/32" || false

  if [ "$firewallDriver" = "xtables" ]; then
    ! iptables -w -t nat -S | grep -c "generated for LXD network-forward ${netName}" || false
  else
    ! nft -nn list chain inet lxd "fwdprert.${netName}" || false
    ! nft -nn list chain inet lxd "fwdout.${netName}" || false
    ! nft -nn list chain inet lxd "fwdpstrt.${netName}" || false
  fi
}
