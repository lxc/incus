test_network_acl() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  # Check basic ACL creation, listing, deletion and project namespacing support.
  ! lxc network acl create 192.168.1.1 || false # Don't allow non-hostname compatible names.
  lxc network acl create testacl
  lxc project create testproj -c features.networks=true
  lxc project create testproj2 -c features.networks=false
  lxc network acl create testacl --project testproj
  lxc project show testproj | grep testacl # Check project sees testacl using it.
  ! lxc network acl create testacl --project testproj2 || false
  lxc network acl ls | grep testacl
  lxc network acl ls --project testproj | grep testacl
  lxc network acl delete testacl
  lxc network acl delete testacl --project testproj
  ! lxc network acl ls | grep testacl || false
  ! lxc network acl ls --project testproj | grep testacl || false
  lxc project delete testproj

  # ACL creation from stdin.
  cat <<EOF | lxc network acl create testacl
description: Test ACL
egress: []
ingress:
- action: allow
  source: 192.168.1.1/32
  destination: 192.168.1.2/32
  protocol: tcp
  source_port: ""
  destination_port: "22"
  icmp_type: ""
  icmp_code: ""
  description: ""
  state: enabled
config:
  user.mykey: foo
EOF
 lxc network acl show testacl | grep "description: Test ACL"
 lxc network acl show testacl | grep "action: allow"
 lxc network acl show testacl | grep "source: 192.168.1.1/32"
 lxc network acl show testacl | grep "destination: 192.168.1.2/32"
 lxc network acl show testacl | grep 'destination_port: "22"'
 lxc network acl show testacl | grep "user.mykey: foo"

 # ACL Patch. Check for merged config and replaced description, ingress and egress fields.
 lxc query -X PATCH -d "{\\\"config\\\": {\\\"user.myotherkey\\\": \\\"bah\\\"}}" /1.0/network-acls/testacl
 lxc network acl show testacl | grep "user.mykey: foo"
 lxc network acl show testacl | grep "user.myotherkey: bah"
 lxc network acl show testacl | grep 'description: ""'
 lxc network acl show testacl | grep 'ingress: \[\]'
 lxc network acl show testacl | grep 'egress: \[\]'

 # ACL edit from stdin.
 cat <<EOF | lxc network acl edit testacl
description: Test ACL updated
egress: []
ingress:
- action: allow
  source: 192.168.1.1/32
  destination: 192.168.1.2/32
  protocol: tcp
  source_port: ""
  destination_port: "22"
  icmp_type: ""
  icmp_code: ""
  description: "a rule description"
  state: enabled
config:
  user.mykey: foo
EOF
 lxc network acl show testacl | grep "description: Test ACL updated"
 lxc network acl show testacl | grep "description: a rule description"

 # ACL rule addition.
 ! lxc network acl rule add testacl outbound || false # Invalid direction
 ! lxc network acl rule add testacl ingress invalidfield=foo || false # Invalid field
 ! lxc network acl rule add testacl ingress action=accept || false # Invalid action
 ! lxc network acl rule add testacl ingress action=allow state=foo || false # Invalid state
 ! lxc network acl rule add testacl ingress action=allow source=foo || false # Invalid source
 ! lxc network acl rule add testacl ingress action=allow destination=foo || false # Invalid destination
 ! lxc network acl rule add testacl ingress action=allow source_port=foo || false # Invalid source port
 ! lxc network acl rule add testacl ingress action=allow destination_port=foo || false # Invalid destination port
 ! lxc network acl rule add testacl ingress action=allow source_port=999999999 || false # Invalid source port
 ! lxc network acl rule add testacl ingress action=allow destination_port=999999999 || false # Invalid destination port
 ! lxc network acl rule add testacl ingress action=allow protocol=foo || false # Invalid protocol
 ! lxc network acl rule add testacl ingress action=allow protocol=udp icmp_code=1 || false # Invalid icmp combination
 ! lxc network acl rule add testacl ingress action=allow protocol=icmp4 icmp_code=256 || false # Invalid icmp combination
 ! lxc network acl rule add testacl ingress action=allow protocol=icmp6 icmp_type=-1 || false # Invalid icmp combination

 lxc network acl rule add testacl ingress action=allow source=192.168.1.2/32 protocol=tcp destination=192.168.1.1-192.168.1.3 destination_port="22, 2222-2223"
 ! lxc network acl rule add testacl ingress action=allow source=192.168.1.2/32 protocol=tcp destination=192.168.1.1-192.168.1.3 destination_port=22,2222-2223 || false # Dupe rule detection
 lxc network acl show testacl | grep "destination: 192.168.1.1-192.168.1.3"
 lxc network acl show testacl | grep -c2 'state: enabled' # Default state enabled for new rules.

 # ACL rule removal.
 lxc network acl rule add testacl ingress action=allow source=192.168.1.3/32 protocol=tcp destination=192.168.1.1-192.168.1.3 destination_port=22,2222-2223 description="removal rule test"
 ! lxc network acl rule remove testacl ingress || false # Fail if match multiple rules with no filter and no --force.
 ! lxc network acl rule remove testacl ingress destination_port=22,2222-2223 || false # Fail if match multiple rules with filter and no --force.
 lxc network acl rule remove testacl ingress description="removal rule test" # Single matching rule removal.
 ! lxc network acl rule remove testacl ingress description="removal rule test" || false # No match for removal fails.
 lxc network acl rule remove testacl ingress --force # Remove all ingress rules.
 lxc network acl show testacl | grep 'ingress: \[\]' # Check all ingress rules removed.

 # ACL rename.
 ! lxc network acl rename testacl 192.168.1.1 || false # Don't allow non-hostname compatible names.
 lxc network acl rename testacl testacl2
 lxc network acl show testacl2

 # ACL custom config.
 lxc network acl set testacl2 user.somekey foo
 lxc network acl get testacl2 user.somekey | grep foo
 ! lxc network acl set testacl2 non.userkey || false
 lxc network acl unset testacl2 user.somekey
 ! lxc network acl get testacl2 user.somekey | grep foo || false

 lxc network acl delete testacl2
}
