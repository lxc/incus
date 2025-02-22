test_network_address_set() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  ! incus network address-set create 2432 || false # Don't allow non-hostname compatible names.
  incus network address-set create testAS
  incus network address-set delete testAS
  incus project create testproj -c features.networks=true
  incus project create testproj2 -c features.networks=false
  incus network address-set create testAS --project testproj  # NOK because still dependent on uniquenesseven if in separate project
  incus project show testproj | grep testAS # Check project sees testAS using it. NOK
  ! incus network address-set create testAS --project testproj2 || false # NOK
  incus network address-set ls | grep testAS
  incus network address-set ls --project testproj | grep testAS
  incus network address-set delete testAS
  incus network address-set delete testAS --project testproj
  ! incus network address-set ls | grep testAS || false
  ! incus network address-set ls --project testproj | grep testAS || false
  incus project delete testproj
  incus project delete testproj2
  incus network address-set create testAS --description "Test description"
  incus network address-set list | grep -q -F 'Test description'
  incus network address-set show testAS | grep -q -F 'description: Test description'
  incus network address-set delete testAS


    # address set creation from stdin
      cat <<EOF | incus network address-set create testAS
  description: Test Address set
  addresses: 192.168..0.1, 192.68.0.254
  external_ids:
    user.mykey: foo
  }
  EOF

  incus network address-set show testAS | grep "description:Test Address set"
  incus network address-set show testAS | grep "addresses: 192.168..0.1, 192.68.0.254"
  incus network address-set show testAS | grep "user.mykey: foo"
  
  incus query -X PATCH -d "{\\\"external_ids\\\": {\\\"user.myotherkey\\\": \\\"bah\\\"}}" /1.0/network-address-sets/testAS
  incus network address-set show testAS | grep "user.mykey: foo"
  incus network address-set show testAS | grep "user.myotherkey: bah"
  incus network address-set show testAS | grep 'description: ""'
  incus network address-set show testAS | grep 'addresses: \[\]'

  # Address set edit from stdin

  cat << EOF | incus network address-set edit testAS
description: Test address set update
addresses: 10.0.4.5, 10.0.0.2
external_ids:
    user.mykey: bar
EOF

  incus netwotk address-set show testAS | grep "description: Test address set update"
  incus netwotk address-set show testAS | grep "addresses: 10.0.4.5, 10.0.0.2"
  incus network address-set show testAS | grep "user.mykey: bar"


}
