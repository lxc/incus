function test_address_set() {
  function get_container_ip() {
    local container="$1"
    for i in {1..10}; do
      ip=$(incus list "$container" --format csv | cut -d',' -f3 | head -n1 | cut -d' ' -f1)
      if [[ -n "$ip" ]]; then echo "$ip"; return 0; fi
      sleep 1
    done
    echo ""
  }

  function get_container_ip6() {
    local container="$1"
    for i in {1..10}; do
      ip6=$(incus list "$container" --format csv | cut -d',' -f4 | tr ' ' '\n' | head -n1)
      if [[ -n "$ip6" ]]; then echo "$ip6"; return 0; fi
      sleep 1
    done
    echo ""
  }
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"
  
  ! incus network address-set create 2432 || false 
  incus network address-set create testAS
  incus network address-set delete testAS

  # Test 2: Address set creation & deletion
  ! incus network address-set create 2432 || false
  incus network address-set create testAS
  incus network address-set delete testAS
  incus project create testproj -c features.networks=true
  incus network address-set create testAS --project testproj
  incus network address-set ls --project testproj | grep -q "testAS"
  incus network address-set delete testAS --project testproj
  incus project delete testproj

  cat <<EOF | incus network address-set create testAS
description: Test Address set from STDIN
addresses:
  - 192.168.0.1
  - 192.168.0.254
external_ids:
  user.mykey: foo
EOF
  incus network address-set show testAS | grep -q "description: Test Address set from STDIN"
  incus network address-set delete testAS
  incus network address-set create testAS --description "Listing test"
  incus network address-set ls | grep -q "testAS"
  incus network address-set delete testAS
  incus network address-set create testAS --description "Show test"
  incus network address-set delete testAS
  incus network address-set create testAS --description "Initial description"
  cat <<EOF | incus network address-set edit testAS
description: Updated address set
addresses:
  - 10.0.0.1
  - 10.0.0.2
external_ids:
  user.mykey: bar
EOF
  incus network address-set show testAS | grep -q "Updated address set"
  incus network address-set delete testAS
  incus network address-set create testAS --description "Patch test"
  incus query -X PATCH -d "{\"external_ids\": {\"user.myotherkey\": \"bah\"}}" /1.0/network-address-sets/testAS
  incus network address-set show testAS | grep -q "user.myotherkey: bah"
  incus network address-set delete testAS
  incus network address-set create testAS --description "Address add/remove test"
  incus network address-set add-addr testAS 192.168.1.100
  incus network address-set show testAS | grep -q "192.168.1.100"
  incus network address-set remove-addr testAS 192.168.1.100
  ! incus network address-set show testAS | grep -q "192.168.1.100" || false
  incus network address-set delete testAS
  incus network address-set create testAS --description "Rename test"
  incus network address-set rename testAS testAS-renamed
  incus network address-set ls | grep -q "testAS-renamed"
  incus network address-set delete testAS-renamed
  incus network address-set create testAS --description "Custom keys test"
  incus network address-set set testAS user.somekey foo
  incus network address-set show testAS | grep -q "foo"
  incus network address-set delete testAS
  ! incus network address-set ls | grep -q "testAS" || false
  # Testing if address sets are working correctly inside acls 
  incus launch images:debian/12 testct
  local ip=$(get_container_ip testct)
  incus network address-set create testAS
  incus network address-set add-addr testAS $ip
  incus network acl create blockping
  incus network acl rule add blockping ingress action=drop protocol=icmp4 destination="\$testAS"
  incus network set incusbr0 security.acls="blockping"
  sleep 1
  ! ping -c2 "$ip" > /dev/null || false
  incus network address-set remove-addr testAS $ip
  incus network set incusbr0 security.acls=""
  incus network acl delete blockping
  incus network address-set delete testAS
  # BLockin icmpv6 is an issue so we skip it for now
  incus network address-set create testAS
  incus network address-set add-addr testAS "$ip"
  incus launch images:debian/12 testct2
  sleep 2
  local ip2=$(get_container_ip testct2)
  incus network acl create mixedACL
  incus network acl rule add mixedACL ingress action=drop protocol=icmp4 destination="$ip2,\$testAS"
  incus network set incusbr0 security.acls="mixedACL"
  sleep 2
  ! ping -c2 "$ip" > /dev/null || false
  ! ping -c2 "$ip2" > /dev/null || false
  incus network set incusbr0 security.acls=""
  incus network acl delete mixedACL
  incus network address-set rm testAS
  incus delete testct2 --force
  local subnet=$(echo "$ip" | awk -F. '{print $1"."$2"."$3".0/24"}')
  incus network address-set create testAS
  incus network address-set add-addr testAS "$subnet"
  incus network acl create cidrACL
  incus network acl rule add cidrACL ingress action=drop protocol=icmp4 destination="\$testAS"
  incus network set incusbr0 security.acls="cidrACL"
  sleep 2
  ! ping -c2 "$ip" > /dev/null || false
  incus network set incusbr0 security.acls=""
  incus network acl delete cidrACL
  incus network address-set rm testAS
  local ip6=$(get_container_ip6 testct)
  nc -z -w 5 "$ip" 5355 # SHOULD WORK BY DEFAULT
  nc -6 -z -w 5 "$ip6" 5355 # SHOULD WORK BY DEFAULT
  incus network address-set create testAS
  incus network address-set add-addr testAS "$ip"
  incus network acl create blocktcp5355
  incus network acl rule add blocktcp5355 ingress action=drop protocol=tcp destination_port="5355" destination="\$testAS"
  incus network set incusbr0 security.acls="blocktcp5355"
  ! nc -z -w 5 "$ip" 5355 || false
  incus network address-set add-addr testAS "$ip6"
  ! nc -6 -z -w 5 "$ip6" 5355 || false
  incus network address-set remove-addr testAS "$ip6"
  nc -6 -z -w 5 "$ip6" 5355
  incus network set incusbr0 security.acls=""
  incus network acl delete blocktcp5355
  incus network address-set rm testAS
  # OVNTESTS
  PARENT_NETWORK="incusbr0"
  OVN_NETWORK="ovntest"
  parentNet=$(incus network ls | grep incusbr0 | cut -d"|" -f5)
  dhcpRangeLeft=$(echo $parentNet | awk -F. '{print $1"."$2"."$3".100"}')
  dhcpRangeRight=$(echo $parentNet | awk -F. '{print $1"."$2"."$3".110"}')
  ovnRangeLeft=$(echo $parentNet | awk -F. '{print $1"."$2"."$3".120"}')
  ovnRangeRight=$(echo $parentNet | awk -F. '{print $1"."$2"."$3".130"}')
  incus network set "$PARENT_NETWORK" ipv4.dhcp.ranges="$dhcpRangeLeft-$dhcpRangeRight" ipv4.ovn.ranges="$ovnRangeLeft-$ovnRangeRight"
  incus network create "$OVN_NETWORK" --type=ovn network="$PARENT_NETWORK" || true
  ovnnet4=$(incus network show ovntest | grep 'ipv4.address' | head -n 1 | cut -d ':' -f2)
  ovnnet6=$(incus network show ovntest | grep 'ipv6.address'| head -n1 | cut -d' ' -f4)
  ovnip4=$(incus network show ovntest | grep 'network.ipv4.address' | head -n 1 | cut -d ':' -f2)
  ovnip6=$(incus network show ovntest | grep 'network.ipv6.address' | head -n 1 | cut -d' ' -f4)
  # Add route to ovn network
  ip r a $(echo $ovnnet4 | awk -F. '{print $1"."$2"."$3".0/24"}') via $ovnip4
  ip r a $ovnnet6 via $ovnip6
  incus stop --force testct
  sleep 2
  incus config device override testct eth0 network=ovntest
  incus start testct
  sleep 2
  local ip=$(get_container_ip testct)
  ping -c2 "$ip" > /dev/null
  # When an ACL is applied all unwanted unmatched traffic is either dropped / rejected
  # So to asssess the behaviour we want to create allow rules
  incus network address-set create testAS
  incus network address-set add-addr testAS "$ip"
  incus network acl create allowping
  incus network acl rule add allowping ingress action=allow protocol=icmp4 destination="\$testAS"
  incus network set "$OVN_NETWORK" security.acls="allowping"
  sleep 1
  ping -c2 "$ip" > /dev/null
  incus network set "$OVN_NETWORK" security.acls=""
  incus network acl delete allowping
  incus network address-set delete testAS
  local ip6=$(get_container_ip6 testct)
  ping -6 -c2 "$ip6" > /dev/null
  incus network address-set create testAS
  incus network address-set add-addr testAS "$ip6"
  incus network acl create allowping
  incus network acl rule add allowping ingress action=allow protocol=icmp6 destination="\$testAS"
  incus network set "$OVN_NETWORK" security.acls="allowping"
  ping -6 -c2 "$ip6" > /dev/null
  incus network set "$OVN_NETWORK" security.acls=""
  incus network acl delete allowping
  incus network address-set delete testAS
  incus network address-set create testAS
  incus network address-set add-addr testAS "$ip"
  incus launch images:debian/12 testct2
  sleep 3
  local ip2=$(get_container_ip testct2)
  incus network acl create mixedACL
  incus network acl rule add mixedACL ingress action=allow protocol=icmp4 destination="$ip2,\$testAS"
  incus network set "$OVN_NETWORK" security.acls="mixedACL"
  sleep 1
  ping -c2 "$ip" > /dev/null
  ping -c2 "$ip2" > /dev/null
  incus network set "$OVN_NETWORK" security.acls=""
  incus network acl delete mixedACL
  incus network address-set delete testAS
  incus delete testct2 --force
  local subnet=$(echo "$ip" | awk -F. '{print $1"."$2"."$3".0/24"}')
  incus network address-set create testAS
  incus network address-set add-addr testAS "$subnet"
  incus network acl create cidrACL
  incus network acl rule add cidrACL ingress action=allow protocol=icmp4 destination="\$testAS"
  incus network set "$OVN_NETWORK" security.acls="cidrACL"
  sleep 1
  ping -c2 "$ip" > /dev/null
  incus network set "$OVN_NETWORK" security.acls=""
  incus network acl delete cidrACL
  incus network address-set delete testAS
  incus network address-set create testAS
  incus network address-set add-addr testAS "$ip"
  # systemd-resolved wont work with different subnet (I guess)
  # So we'll use a netcat dummy service
  incus exec testct -- apt install netcat-openbsd -y
  incus exec testct -- bash -c 'cat > /etc/systemd/system/nc-server.service <<EOF
[Unit]
Description=Netcat TCP Server on 7896
After=network.target

[Service]
ExecStart=/usr/bin/nc -l -p 7896
Restart=always

[Install]
WantedBy=multi-user.target
EOF'
  incus exec testct -- systemctl start nc-server
  incus network acl create allowtcp7896
  incus network acl rule add allowtcp7896 ingress action=allow protocol=tcp destination_port="7896" destination="\$testAS"
  incus network set "$OVN_NETWORK" security.acls="allowtcp7896"
  sleep 1
  nc -z -w 5 "$ip" 7896
  incus network address-set add-addr testAS "$ip6"
  incus exec testct -- bash -c 'cat > /etc/systemd/system/nc6-server.service <<EOF
[Unit]
Description=Netcat TCP Server on 7896
After=network.target

[Service]
ExecStart=/usr/bin/nc -6 -l -p 7896
Restart=always

[Install]
WantedBy=multi-user.target
EOF'
  incus exec testct -- systemctl stop nc-server
  incus exec testct -- systemctl start nc6-server
  nc -6 -z -w 5 "$ip6" 7896
  incus network address-set remove-addr testAS "$ip6"
  ! nc -6 -z -w 5 "$ip6" 7896 || false
  incus network set "$OVN_NETWORK" security.acls=""
  incus network acl delete allowtcp7896
  incus network address-set delete testAS
}
