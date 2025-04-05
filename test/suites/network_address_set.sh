# shellcheck disable=2148
test_address_set() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

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
config:
  user.mykey: foo
EOF
  incus network address-set show testAS | grep -q "description: Test Address set from STDIN"
  incus network address-set delete testAS

  incus network address-set create testAS --description "Listing test"
  incus network address-set ls | grep -q "testAS"
  incus network address-set delete testAS

  incus network address-set create testAS --description "Initial description"
  cat <<EOF | incus network address-set edit testAS
description: Updated address set
addresses:
  - 10.0.0.1
  - 10.0.0.2
config:
  user.mykey: bar
EOF
  incus network address-set show testAS | grep -q "Updated address set"
  incus network address-set delete testAS

  incus network address-set create testAS --description "Patch test"
  incus query -X PATCH -d "{\\\"config\\\": {\\\"user.myotherkey\\\": \\\"bah\\\"}}" /1.0/network-address-sets/testAS
  incus network address-set show testAS | grep -q "user.myotherkey: bah"
  incus network address-set delete testAS

  incus network address-set create testAS --description "Address add/remove test"
  incus network address-set add testAS 192.168.1.100
  incus network address-set show testAS | grep -q "192.168.1.100"
  incus network address-set remove testAS 192.168.1.100
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

  brName="inct$$"
  incus network create "${brName}" \
        ipv6.dhcp.stateful=true \
        ipv4.address=192.0.2.1/24 \
        ipv6.address=2001:db8::1/64

  incus init testimage testct --network "${brName}"
  incus start testct
  incus exec testct -- ip a add 192.0.2.2/24 dev eth0
  incus exec testct -- ip a add 2001:db8::2/64 dev eth0

  incus network address-set create testAS
  incus network address-set add testAS 192.0.2.2

  incus network acl create allowping
  # shellcheck disable=2016
  incus network acl rule add allowping ingress action=allow protocol=icmp4 destination='\$testAS' # single quote to avoid expansion
  incus network set "${brName}" security.acls="allowping"
  ping -c2 192.0.2.2 > /dev/null
  incus network address-set remove testAS 192.0.2.2
  incus network set "${brName}" security.acls=""
  incus network acl delete allowping
  incus network address-set delete testAS

  incus network address-set create testAS
  incus network address-set add testAS 192.0.2.2
  incus launch testimage testct2
  incus exec testct -- ip a add 192.0.2.3/24 dev eth0
  incus exec testct -- ip a add 2001:db8::3/64 dev eth0
  incus network acl create mixedACL
  # shellcheck disable=2016
  incus network acl rule add mixedACL ingress action=allow protocol=icmp4 destination='192.0.2.3,\$testAS'
  incus network set "${brName}" security.acls="mixedACL"
  ping -c2 192.0.2.2 > /dev/null
  ping -c2 192.0.2.3 > /dev/null
  incus network set "${brName}" security.acls=""
  incus network acl delete mixedACL
  incus network address-set rm testAS
  incus delete testct2 --force

  subnet=$(echo 192.0.2.2 | awk -F. '{print $1"."$2"."$3".0/24"}')
  incus network address-set create testAS
  incus network address-set add testAS "$subnet"
  incus network acl create cidrACL
  # shellcheck disable=2016
  incus network acl rule add cidrACL ingress action=allow protocol=icmp4 destination='\$testAS'
  incus network set "${brName}" security.acls="cidrACL"
  ping -c2 192.0.2.2 > /dev/null
  incus network set "${brName}" security.acls=""
  incus network acl delete cidrACL
  incus network address-set rm testAS

  incus network address-set create testAS
  incus network address-set add testAS 192.0.2.1
  incus network acl create allowtcp8080
  # shellcheck disable=2016
  incus network acl rule add allowtcp8080 egress action=allow protocol=tcp destination_port="8080" destination='\$testAS'
  incus network set "${brName}" security.acls="allowtcp8080"
  nc -l -p 8080 -q0 -s 192.0.2.1 </dev/null >/dev/null &
  nc -l -p 8080 -q0 -s 2001:db8::1 </dev/null >/dev/null &
  incus exec testct --disable-stdin -- nc -w2 192.0.2.1 8080
  incus network address-set add testAS 2001:db8::1
  incus exec testct --disable-stdin -- nc -w2 2001:db8::1 8080
  incus network address-set remove testAS 2001:db8::1
  ! incus exec testct --disable-stdin -- nc -w2 2001:db8::1 8080 || false
  incus network set "${brName}" security.acls=""
  incus network acl delete allowtcp8080
  incus network address-set rm testAS
  incus rm --force testct

  incus network delete "${brName}"
}
