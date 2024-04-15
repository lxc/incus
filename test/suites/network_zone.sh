test_network_zone() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  poolName=$(incus profile device get default root pool)

  # Enable the DNS server
  incus config unset core.https_address
  incus config set core.dns_address "${INCUS_ADDR}"

  # Create a network
  netName=inct$$
  incus network create "${netName}" \
        ipv4.address=192.0.2.1/24 \
        ipv6.address=fd42:4242:4242:1010::1/64

  # Create the zones
  ! incus network zone create /incus.example.net || false
  incus network zone create incus.example.net/withslash
  incus network zone delete incus.example.net/withslash
  incus network zone create incus.example.net
  incus network zone create 2.0.192.in-addr.arpa
  incus network zone create 0.1.0.1.2.4.2.4.2.4.2.4.2.4.d.f.ip6.arpa

  # Create project and forward zone in project.
  incus project create foo \
    -c features.images=false \
    -c restricted=true \
    -c restricted.networks.zones=example.net

  # Put an instance on the network in each project.
  incus init testimage c1 --network "${netName}" -d eth0,ipv4.address=192.0.2.42
  incus init testimage c2 --network "${netName}" --storage "${poolName}" -d eth0,ipv4.address=192.0.2.43 --project foo

  # Check features.networks.zones can be enabled if false in a non-empty project, but cannot be disabled again.
  incus project set foo features.networks.zones=true
  ! incus project set foo features.networks.zones=false || false

  # Check restricted.networks.zones is working.
  ! incus network zone create incus-foo.restricted.net --project foo || false

  # Create zone in project.
  incus network zone create incus-foo.example.net --project foo

  # Check all-projects column
  incus network zone list --all-projects -fcsv | grep -q default,incus.example.net || false
  incus network zone list --all-projects -fcsv | grep -q foo,incus-foo.example.net || false

  # Check associating a network to a missing zone isn't allowed.
  ! incus network set "${netName}" dns.zone.forward missing || false
  ! incus network set "${netName}" dns.zone.reverse.ipv4 missing || false
  ! incus network set "${netName}" dns.zone.reverse.ipv6 missing || false

  # Link the zones to the network
  incus network set "${netName}" \
    dns.zone.forward="incus.example.net, incus-foo.example.net" \
    dns.zone.reverse.ipv4=2.0.192.in-addr.arpa \
    dns.zone.reverse.ipv6=0.1.0.1.2.4.2.4.2.4.2.4.2.4.d.f.ip6.arpa

  # Check that associating a network to multiple forward zones from the same project isn't allowed.
  incus network zone create incus2.example.net
  ! incus network set "${netName}" dns.zone.forward "incus.example.net, incus2.example.net" || false
  incus network zone delete incus2.example.net

  # Check associating a network to multiple reverse zones isn't allowed.
  ! incus network set "${netName}" dns.zone.reverse.ipv4 "2.0.192.in-addr.arpa, incus.example.net" || false
  ! incus network set "${netName}" dns.zone.reverse.ipv6 "0.1.0.1.2.4.2.4.2.4.2.4.2.4.d.f.ip6.arpa, incus.example.net" || false

  incus start c1
  incus start c2 --project foo

  # Wait for IPv4 and IPv6 addresses
  while :; do
    sleep 1
    [ -n "$(incus list -c6 --format=csv c1)" ] || continue
    break
  done

  # Setup DNS peers
  incus network zone set incus.example.net peers.test.address=127.0.0.1
  incus network zone set incus-foo.example.net peers.test.address=127.0.0.1 --project=foo
  incus network zone set 2.0.192.in-addr.arpa peers.test.address=127.0.0.1
  incus network zone set 0.1.0.1.2.4.2.4.2.4.2.4.2.4.d.f.ip6.arpa peers.test.address=127.0.0.1

  # Check the zones
  DNS_ADDR="$(echo "${INCUS_ADDR}" | cut -d: -f1)"
  DNS_PORT="$(echo "${INCUS_ADDR}" | cut -d: -f2)"
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr incus.example.net
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr incus.example.net | grep "${netName}.gw.incus.example.net.\s\+300\s\+IN\s\+A\s\+"
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr incus.example.net | grep "c1.incus.example.net.\s\+300\s\+IN\s\+A\s\+"
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr incus.example.net | grep "${netName}.gw.incus.example.net.\s\+300\s\+IN\s\+AAAA\s\+"
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr incus.example.net | grep "c1.incus.example.net.\s\+300\s\+IN\s\+AAAA\s\+"

  # Check the c2 instance from project foo isn't in the forward view of incus.example.net
  ! dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr incus.example.net | grep "c2.incus.example.net" || false

  # Check the c2 instance is the incus-foo.example.net zone view, but not the network's gateways.
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr incus-foo.example.net
  ! dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr incus-foo.example.net | grep "${netName}.gw.incus-foo.example.net.\s\+300\s\+IN\s\+A\s\+" || false
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr incus-foo.example.net | grep "c2.incus-foo.example.net.\s\+300\s\+IN\s\+A\s\+"
  ! dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr incus-foo.example.net | grep "${netName}.gw.incus-foo.example.net.\s\+300\s\+IN\s\+AAAA\s\+" || false
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr incus-foo.example.net | grep "c2.incus-foo.example.net.\s\+300\s\+IN\s\+AAAA\s\+"

  # Check the c1 instance from project default isn't in the forward view of incus-foo.example.net
  ! dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr incus-foo.example.net | grep "c1.incus.example.net" || false

  # Check reverse zones include records from both projects associated to the relevant forward zone name.
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr 2.0.192.in-addr.arpa | grep -Fc "PTR" | grep -Fx 3
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr 2.0.192.in-addr.arpa | grep "300\s\+IN\s\+PTR\s\+${netName}.gw.incus.example.net."
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr 2.0.192.in-addr.arpa | grep "300\s\+IN\s\+PTR\s\+c1.incus.example.net."
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr 2.0.192.in-addr.arpa | grep "300\s\+IN\s\+PTR\s\+c2.incus-foo.example.net."

  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr 0.1.0.1.2.4.2.4.2.4.2.4.2.4.d.f.ip6.arpa
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr 0.1.0.1.2.4.2.4.2.4.2.4.2.4.d.f.ip6.arpa | grep -Fc "PTR" | grep -Fx 3
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr 0.1.0.1.2.4.2.4.2.4.2.4.2.4.d.f.ip6.arpa | grep "300\s\+IN\s\+PTR\s\+${netName}.gw.incus.example.net."
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr 0.1.0.1.2.4.2.4.2.4.2.4.2.4.d.f.ip6.arpa | grep "300\s\+IN\s\+PTR\s\+c1.incus.example.net."
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr 0.1.0.1.2.4.2.4.2.4.2.4.2.4.d.f.ip6.arpa | grep "300\s\+IN\s\+PTR\s\+c2.incus-foo.example.net."

  # Test extra records
  incus network zone record create incus.example.net demo user.foo=bar
  ! incus network zone record create incus.example.net demo user.foo=bar || false
  incus network zone record entry add incus.example.net demo A 1.1.1.1 --ttl 900
  incus network zone record entry add incus.example.net demo A 2.2.2.2
  incus network zone record entry add incus.example.net demo AAAA 1111::1111 --ttl 1800
  incus network zone record entry add incus.example.net demo AAAA 2222::2222
  incus network zone record entry add incus.example.net demo MX "1 mx1.example.net." --ttl 900
  incus network zone record entry add incus.example.net demo MX "10 mx2.example.net." --ttl 900
  incus network zone record list incus.example.net
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr incus.example.net | grep -Fc demo.incus.example.net | grep -Fx 6
  incus network zone record entry remove incus.example.net demo A 1.1.1.1

  incus admin sql global 'select * from networks_zones_records'
  incus network zone record create incus-foo.example.net demo user.foo=bar --project foo
  ! incus network zone record create incus-foo.example.net demo user.foo=bar --project foo || false
  incus network zone record entry add incus-foo.example.net demo A 1.1.1.1 --ttl 900 --project foo
  incus network zone record entry add incus-foo.example.net demo A 2.2.2.2 --project foo
  incus network zone record entry add incus-foo.example.net demo AAAA 1111::1111 --ttl 1800 --project foo
  incus network zone record entry add incus-foo.example.net demo AAAA 2222::2222 --project foo
  incus network zone record entry add incus-foo.example.net demo MX "1 mx1.example.net." --ttl 900 --project foo
  incus network zone record entry add incus-foo.example.net demo MX "10 mx2.example.net." --ttl 900 --project foo
  incus network zone record list incus-foo.example.net --project foo
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr incus-foo.example.net | grep -Fc demo.incus-foo.example.net | grep -Fx 6
  incus network zone record entry remove incus-foo.example.net demo A 1.1.1.1 --project foo

  # Cleanup
  incus delete -f c1
  incus delete -f c2 --project foo
  incus network delete "${netName}"
  incus network zone delete incus.example.net
  incus network zone delete incus-foo.example.net --project foo
  incus network zone delete 2.0.192.in-addr.arpa
  incus network zone delete 0.1.0.1.2.4.2.4.2.4.2.4.2.4.d.f.ip6.arpa
  incus project delete foo

  incus config unset core.dns_address
  incus config set core.https_address "${INCUS_ADDR}"
}
