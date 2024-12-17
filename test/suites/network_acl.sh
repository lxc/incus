test_network_acl() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  # Check basic ACL creation, listing, deletion and project namespacing support.
  ! incus network acl create 192.168.1.1 || false # Don't allow non-hostname compatible names.
  incus network acl create testacl
  incus project create testproj -c features.networks=true
  incus project create testproj2 -c features.networks=false
  incus network acl create testacl --project testproj
  incus project show testproj | grep testacl # Check project sees testacl using it.
  ! incus network acl create testacl --project testproj2 || false
  incus network acl ls | grep testacl
  incus network acl ls --project testproj | grep testacl
  incus network acl delete testacl
  incus network acl delete testacl --project testproj
  ! incus network acl ls | grep testacl || false
  ! incus network acl ls --project testproj | grep testacl || false
  incus project delete testproj
  incus network acl create testacl --description "Test description"
  incus network acl list | grep -q -F 'Test description'
  incus network acl show testacl | grep -q -F 'description: Test description'
  incus network acl delete testacl

  # ACL creation from stdin.
  cat <<EOF | incus network acl create testacl
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
 incus network acl show testacl | grep "description: Test ACL"
 incus network acl show testacl | grep "action: allow"
 incus network acl show testacl | grep "source: 192.168.1.1/32"
 incus network acl show testacl | grep "destination: 192.168.1.2/32"
 incus network acl show testacl | grep 'destination_port: "22"'
 incus network acl show testacl | grep "user.mykey: foo"

 # ACL Patch. Check for merged config and replaced description, ingress and egress fields.
 incus query -X PATCH -d "{\\\"config\\\": {\\\"user.myotherkey\\\": \\\"bah\\\"}}" /1.0/network-acls/testacl
 incus network acl show testacl | grep "user.mykey: foo"
 incus network acl show testacl | grep "user.myotherkey: bah"
 incus network acl show testacl | grep 'description: ""'
 incus network acl show testacl | grep 'ingress: \[\]'
 incus network acl show testacl | grep 'egress: \[\]'

 # ACL edit from stdin.
 cat <<EOF | incus network acl edit testacl
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
 incus network acl show testacl | grep "description: Test ACL updated"
 incus network acl show testacl | grep "description: a rule description"

 # ACL rule addition.
 ! incus network acl rule add testacl outbound || false # Invalid direction
 ! incus network acl rule add testacl ingress invalidfield=foo || false # Invalid field
 ! incus network acl rule add testacl ingress action=accept || false # Invalid action
 ! incus network acl rule add testacl ingress action=allow state=foo || false # Invalid state
 ! incus network acl rule add testacl ingress action=allow source=foo || false # Invalid source
 ! incus network acl rule add testacl ingress action=allow destination=foo || false # Invalid destination
 ! incus network acl rule add testacl ingress action=allow source_port=foo || false # Invalid source port
 ! incus network acl rule add testacl ingress action=allow destination_port=foo || false # Invalid destination port
 ! incus network acl rule add testacl ingress action=allow source_port=999999999 || false # Invalid source port
 ! incus network acl rule add testacl ingress action=allow destination_port=999999999 || false # Invalid destination port
 ! incus network acl rule add testacl ingress action=allow protocol=foo || false # Invalid protocol
 ! incus network acl rule add testacl ingress action=allow protocol=udp icmp_code=1 || false # Invalid icmp combination
 ! incus network acl rule add testacl ingress action=allow protocol=icmp4 icmp_code=256 || false # Invalid icmp combination
 ! incus network acl rule add testacl ingress action=allow protocol=icmp6 icmp_type=-1 || false # Invalid icmp combination

 incus network acl rule add testacl ingress action=allow source=192.168.1.2/32 protocol=tcp destination=192.168.1.1-192.168.1.3 destination_port="22, 2222-2223" --description "Test ACL rule description"
 ! incus network acl rule add testacl ingress action=allow source=192.168.1.2/32 protocol=tcp destination=192.168.1.1-192.168.1.3 destination_port=22,2222-2223 --description "Test ACL rule description" || false # Dupe rule detection
 incus network acl show testacl | grep "destination: 192.168.1.1-192.168.1.3"
 incus network acl show testacl | grep -c2 'state: enabled' # Default state enabled for new rules.
 incus network acl show testacl | grep "description: Test ACL rule description"

 # ACL rule removal.
 incus network acl rule add testacl ingress action=allow source=192.168.1.3/32 protocol=tcp destination=192.168.1.1-192.168.1.3 destination_port=22,2222-2223 description="removal rule test"
 ! incus network acl rule remove testacl ingress || false # Fail if match multiple rules with no filter and no --force.
 ! incus network acl rule remove testacl ingress destination_port=22,2222-2223 || false # Fail if match multiple rules with filter and no --force.
 incus network acl rule remove testacl ingress description="removal rule test" # Single matching rule removal.
 ! incus network acl rule remove testacl ingress description="removal rule test" || false # No match for removal fails.
 incus network acl rule remove testacl ingress --force # Remove all ingress rules.
 incus network acl show testacl | grep 'ingress: \[\]' # Check all ingress rules removed.

 # ACL rename.
 ! incus network acl rename testacl 192.168.1.1 || false # Don't allow non-hostname compatible names.
 incus network acl rename testacl testacl2
 incus network acl show testacl2

 # ACL custom config.
 incus network acl set testacl2 user.somekey foo
 incus network acl get testacl2 user.somekey | grep foo
 ! incus network acl set testacl2 non.userkey || false
 incus network acl unset testacl2 user.somekey
 ! incus network acl get testacl2 user.somekey | grep foo || false

 incus network acl delete testacl2
}
