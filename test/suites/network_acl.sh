test_network_acl() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  # Check basic ACL creation, listing, deletion and project namespacing support.
  ! inc network acl create 192.168.1.1 || false # Don't allow non-hostname compatible names.
  inc network acl create testacl
  inc project create testproj -c features.networks=true
  inc project create testproj2 -c features.networks=false
  inc network acl create testacl --project testproj
  inc project show testproj | grep testacl # Check project sees testacl using it.
  ! inc network acl create testacl --project testproj2 || false
  inc network acl ls | grep testacl
  inc network acl ls --project testproj | grep testacl
  inc network acl delete testacl
  inc network acl delete testacl --project testproj
  ! inc network acl ls | grep testacl || false
  ! inc network acl ls --project testproj | grep testacl || false
  inc project delete testproj

  # ACL creation from stdin.
  cat <<EOF | inc network acl create testacl
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
 inc network acl show testacl | grep "description: Test ACL"
 inc network acl show testacl | grep "action: allow"
 inc network acl show testacl | grep "source: 192.168.1.1/32"
 inc network acl show testacl | grep "destination: 192.168.1.2/32"
 inc network acl show testacl | grep 'destination_port: "22"'
 inc network acl show testacl | grep "user.mykey: foo"

 # ACL Patch. Check for merged config and replaced description, ingress and egress fields.
 inc query -X PATCH -d "{\\\"config\\\": {\\\"user.myotherkey\\\": \\\"bah\\\"}}" /1.0/network-acls/testacl
 inc network acl show testacl | grep "user.mykey: foo"
 inc network acl show testacl | grep "user.myotherkey: bah"
 inc network acl show testacl | grep 'description: ""'
 inc network acl show testacl | grep 'ingress: \[\]'
 inc network acl show testacl | grep 'egress: \[\]'

 # ACL edit from stdin.
 cat <<EOF | inc network acl edit testacl
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
 inc network acl show testacl | grep "description: Test ACL updated"
 inc network acl show testacl | grep "description: a rule description"

 # ACL rule addition.
 ! inc network acl rule add testacl outbound || false # Invalid direction
 ! inc network acl rule add testacl ingress invalidfield=foo || false # Invalid field
 ! inc network acl rule add testacl ingress action=accept || false # Invalid action
 ! inc network acl rule add testacl ingress action=allow state=foo || false # Invalid state
 ! inc network acl rule add testacl ingress action=allow source=foo || false # Invalid source
 ! inc network acl rule add testacl ingress action=allow destination=foo || false # Invalid destination
 ! inc network acl rule add testacl ingress action=allow source_port=foo || false # Invalid source port
 ! inc network acl rule add testacl ingress action=allow destination_port=foo || false # Invalid destination port
 ! inc network acl rule add testacl ingress action=allow source_port=999999999 || false # Invalid source port
 ! inc network acl rule add testacl ingress action=allow destination_port=999999999 || false # Invalid destination port
 ! inc network acl rule add testacl ingress action=allow protocol=foo || false # Invalid protocol
 ! inc network acl rule add testacl ingress action=allow protocol=udp icmp_code=1 || false # Invalid icmp combination
 ! inc network acl rule add testacl ingress action=allow protocol=icmp4 icmp_code=256 || false # Invalid icmp combination
 ! inc network acl rule add testacl ingress action=allow protocol=icmp6 icmp_type=-1 || false # Invalid icmp combination

 inc network acl rule add testacl ingress action=allow source=192.168.1.2/32 protocol=tcp destination=192.168.1.1-192.168.1.3 destination_port="22, 2222-2223"
 ! inc network acl rule add testacl ingress action=allow source=192.168.1.2/32 protocol=tcp destination=192.168.1.1-192.168.1.3 destination_port=22,2222-2223 || false # Dupe rule detection
 inc network acl show testacl | grep "destination: 192.168.1.1-192.168.1.3"
 inc network acl show testacl | grep -c2 'state: enabled' # Default state enabled for new rules.

 # ACL rule removal.
 inc network acl rule add testacl ingress action=allow source=192.168.1.3/32 protocol=tcp destination=192.168.1.1-192.168.1.3 destination_port=22,2222-2223 description="removal rule test"
 ! inc network acl rule remove testacl ingress || false # Fail if match multiple rules with no filter and no --force.
 ! inc network acl rule remove testacl ingress destination_port=22,2222-2223 || false # Fail if match multiple rules with filter and no --force.
 inc network acl rule remove testacl ingress description="removal rule test" # Single matching rule removal.
 ! inc network acl rule remove testacl ingress description="removal rule test" || false # No match for removal fails.
 inc network acl rule remove testacl ingress --force # Remove all ingress rules.
 inc network acl show testacl | grep 'ingress: \[\]' # Check all ingress rules removed.

 # ACL rename.
 ! inc network acl rename testacl 192.168.1.1 || false # Don't allow non-hostname compatible names.
 inc network acl rename testacl testacl2
 inc network acl show testacl2

 # ACL custom config.
 inc network acl set testacl2 user.somekey foo
 inc network acl get testacl2 user.somekey | grep foo
 ! inc network acl set testacl2 non.userkey || false
 inc network acl unset testacl2 user.somekey
 ! inc network acl get testacl2 user.somekey | grep foo || false

 inc network acl delete testacl2
}
