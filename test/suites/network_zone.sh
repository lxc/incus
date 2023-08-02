test_network_zone() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  poolName=$(lxc profile device get default root pool)

  # Enable the DNS server
  lxc config unset core.https_address
  lxc config set core.dns_address "${INCUS_ADDR}"

  # Create a network
  netName=inct$$
  lxc network create "${netName}" \
        ipv4.address=192.0.2.1/24 \
        ipv6.address=fd42:4242:4242:1010::1/64

  # Create the zones
  ! lxc network zone create /lxd.example.net || false
  lxc network zone create lxd.example.net/withslash
  lxc network zone delete lxd.example.net/withslash
  lxc network zone create lxd.example.net
  lxc network zone create 2.0.192.in-addr.arpa
  lxc network zone create 0.1.0.1.2.4.2.4.2.4.2.4.2.4.d.f.ip6.arpa

  # Create project and forward zone in project.
  lxc project create foo \
    -c features.images=false \
    -c restricted=true \
    -c restricted.networks.zones=example.net

  # Put an instance on the network in each project.
  lxc init testimage c1 --network "${netName}" -d eth0,ipv4.address=192.0.2.42
  lxc init testimage c2 --network "${netName}" --storage "${poolName}" -d eth0,ipv4.address=192.0.2.43 --project foo

  # Check features.networks.zones can be enabled if false in a non-empty project, but cannot be disabled again.
  lxc project set foo features.networks.zones=true
  ! lxc project set foo features.networks.zones=false || false

  # Check restricted.networks.zones is working.
  ! lxc network zone create lxdfoo.restricted.net --project foo || false

  # Create zone in project.
  lxc network zone create lxdfoo.example.net --project foo

  # Check associating a network to a missing zone isn't allowed.
  ! lxc network set "${netName}" dns.zone.forward missing || false
  ! lxc network set "${netName}" dns.zone.reverse.ipv4 missing || false
  ! lxc network set "${netName}" dns.zone.reverse.ipv6 missing || false

  # Link the zones to the network
  lxc network set "${netName}" \
    dns.zone.forward="lxd.example.net, lxdfoo.example.net" \
    dns.zone.reverse.ipv4=2.0.192.in-addr.arpa \
    dns.zone.reverse.ipv6=0.1.0.1.2.4.2.4.2.4.2.4.2.4.d.f.ip6.arpa

  # Check that associating a network to multiple forward zones from the same project isn't allowed.
  lxc network zone create lxd2.example.net
  ! lxc network set "${netName}" dns.zone.forward "lxd.example.net, lxd2.example.net" || false
  lxc network zone delete lxd2.example.net

  # Check associating a network to multiple reverse zones isn't allowed.
  ! lxc network set "${netName}" dns.zone.reverse.ipv4 "2.0.192.in-addr.arpa, lxd.example.net" || false
  ! lxc network set "${netName}" dns.zone.reverse.ipv6 "0.1.0.1.2.4.2.4.2.4.2.4.2.4.d.f.ip6.arpa, lxd.example.net" || false

  lxc start c1
  lxc start c2 --project foo

  # Wait for IPv4 and IPv6 addresses
  while :; do
    sleep 1
    [ -n "$(lxc list -c6 --format=csv c1)" ] || continue
    break
  done

  # Setup DNS peers
  lxc network zone set lxd.example.net peers.test.address=127.0.0.1
  lxc network zone set lxdfoo.example.net peers.test.address=127.0.0.1 --project=foo
  lxc network zone set 2.0.192.in-addr.arpa peers.test.address=127.0.0.1
  lxc network zone set 0.1.0.1.2.4.2.4.2.4.2.4.2.4.d.f.ip6.arpa peers.test.address=127.0.0.1

  # Check the zones
  DNS_ADDR="$(echo "${INCUS_ADDR}" | cut -d: -f1)"
  DNS_PORT="$(echo "${INCUS_ADDR}" | cut -d: -f2)"
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr lxd.example.net
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr lxd.example.net | grep "${netName}.gw.lxd.example.net.\s\+300\s\+IN\s\+A\s\+"
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr lxd.example.net | grep "c1.lxd.example.net.\s\+300\s\+IN\s\+A\s\+"
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr lxd.example.net | grep "${netName}.gw.lxd.example.net.\s\+300\s\+IN\s\+AAAA\s\+"
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr lxd.example.net | grep "c1.lxd.example.net.\s\+300\s\+IN\s\+AAAA\s\+"

  # Check the c2 instance from project foo isn't in the forward view of lxd.example.net
  ! dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr lxd.example.net | grep "c2.lxd.example.net" || false

  # Check the c2 instance is the lxdfoo.example.net zone view, but not the network's gateways.
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr lxdfoo.example.net
  ! dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr lxdfoo.example.net | grep "${netName}.gw.lxdfoo.example.net.\s\+300\s\+IN\s\+A\s\+" || false
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr lxdfoo.example.net | grep "c2.lxdfoo.example.net.\s\+300\s\+IN\s\+A\s\+"
  ! dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr lxdfoo.example.net | grep "${netName}.gw.lxdfoo.example.net.\s\+300\s\+IN\s\+AAAA\s\+" || false
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr lxdfoo.example.net | grep "c2.lxdfoo.example.net.\s\+300\s\+IN\s\+AAAA\s\+"

  # Check the c1 instance from project default isn't in the forward view of lxdfoo.example.net
  ! dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr lxdfoo.example.net | grep "c1.lxd.example.net" || false

  # Check reverse zones include records from both projects associated to the relevant forward zone name.
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr 2.0.192.in-addr.arpa | grep -Fc "PTR" | grep -Fx 3
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr 2.0.192.in-addr.arpa | grep "300\s\+IN\s\+PTR\s\+${netName}.gw.lxd.example.net."
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr 2.0.192.in-addr.arpa | grep "300\s\+IN\s\+PTR\s\+c1.lxd.example.net."
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr 2.0.192.in-addr.arpa | grep "300\s\+IN\s\+PTR\s\+c2.lxdfoo.example.net."

  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr 0.1.0.1.2.4.2.4.2.4.2.4.2.4.d.f.ip6.arpa
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr 0.1.0.1.2.4.2.4.2.4.2.4.2.4.d.f.ip6.arpa | grep -Fc "PTR" | grep -Fx 3
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr 0.1.0.1.2.4.2.4.2.4.2.4.2.4.d.f.ip6.arpa | grep "300\s\+IN\s\+PTR\s\+${netName}.gw.lxd.example.net."
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr 0.1.0.1.2.4.2.4.2.4.2.4.2.4.d.f.ip6.arpa | grep "300\s\+IN\s\+PTR\s\+c1.lxd.example.net."
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr 0.1.0.1.2.4.2.4.2.4.2.4.2.4.d.f.ip6.arpa | grep "300\s\+IN\s\+PTR\s\+c2.lxdfoo.example.net."

  # Test extra records
  lxc network zone record create lxd.example.net demo user.foo=bar
  ! lxc network zone record create lxd.example.net demo user.foo=bar || false
  lxc network zone record entry add lxd.example.net demo A 1.1.1.1 --ttl 900
  lxc network zone record entry add lxd.example.net demo A 2.2.2.2
  lxc network zone record entry add lxd.example.net demo AAAA 1111::1111 --ttl 1800
  lxc network zone record entry add lxd.example.net demo AAAA 2222::2222
  lxc network zone record entry add lxd.example.net demo MX "1 mx1.example.net." --ttl 900
  lxc network zone record entry add lxd.example.net demo MX "10 mx2.example.net." --ttl 900
  lxc network zone record list lxd.example.net
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr lxd.example.net | grep -Fc demo.lxd.example.net | grep -Fx 6
  lxc network zone record entry remove lxd.example.net demo A 1.1.1.1

  lxd sql global 'select * from networks_zones_records'
  lxc network zone record create lxdfoo.example.net demo user.foo=bar --project foo
  ! lxc network zone record create lxdfoo.example.net demo user.foo=bar --project foo || false
  lxc network zone record entry add lxdfoo.example.net demo A 1.1.1.1 --ttl 900 --project foo
  lxc network zone record entry add lxdfoo.example.net demo A 2.2.2.2 --project foo
  lxc network zone record entry add lxdfoo.example.net demo AAAA 1111::1111 --ttl 1800 --project foo
  lxc network zone record entry add lxdfoo.example.net demo AAAA 2222::2222 --project foo
  lxc network zone record entry add lxdfoo.example.net demo MX "1 mx1.example.net." --ttl 900 --project foo
  lxc network zone record entry add lxdfoo.example.net demo MX "10 mx2.example.net." --ttl 900 --project foo
  lxc network zone record list lxdfoo.example.net --project foo
  dig "@${DNS_ADDR}" -p "${DNS_PORT}" axfr lxdfoo.example.net | grep -Fc demo.lxdfoo.example.net | grep -Fx 6
  lxc network zone record entry remove lxdfoo.example.net demo A 1.1.1.1 --project foo

  # Cleanup
  lxc delete -f c1
  lxc delete -f c2 --project foo
  lxc network delete "${netName}"
  lxc network zone delete lxd.example.net
  lxc network zone delete lxdfoo.example.net --project foo
  lxc network zone delete 2.0.192.in-addr.arpa
  lxc network zone delete 0.1.0.1.2.4.2.4.2.4.2.4.2.4.d.f.ip6.arpa
  lxc project delete foo

  lxc config unset core.dns_address
  lxc config set core.https_address "${INCUS_ADDR}"
}
