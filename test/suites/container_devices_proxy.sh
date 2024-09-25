test_container_devices_proxy() {
  container_devices_proxy_validation
  container_devices_proxy_tcp
  container_devices_proxy_tcp_unix
  container_devices_proxy_tcp_udp
  container_devices_proxy_udp
  container_devices_proxy_unix
  container_devices_proxy_unix_udp
  container_devices_proxy_unix_tcp
  container_devices_proxy_with_overlapping_forward_net
}

container_devices_proxy_validation() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"
  HOST_TCP_PORT=$(local_tcp_port)
  incus launch testimage proxyTester

  # Check that connecting to a DNS name is not allowed (security risk).
  if incus config device add proxyTester proxyDev proxy "listen=tcp:127.0.0.1:$HOST_TCP_PORT" connect=tcp:localhost:4321 bind=host ; then
    echo "Proxy device shouldn't allow connect hostnames, only IPs"
    false
  fi

  # Check using wildcard addresses isn't allowed in NAT mode.
  if incus config device add proxyTester proxyDev proxy "listen=tcp:0.0.0.0:$HOST_TCP_PORT" connect=tcp:0.0.0.0:4321 nat=true ; then
    echo "Proxy device shouldn't allow wildcard IPv4 listen addresses in NAT mode"
    false
  fi
  if incus config device add proxyTester proxyDev proxy "listen=tcp:[::]:$HOST_TCP_PORT" connect=tcp:0.0.0.0:4321 nat=true ; then
    echo "Proxy device shouldn't allow wildcard IPv6 listen addresses in NAT mode"
    false
  fi

  # Check using mixing IP versions in listen/connect addresses isn't allowed in NAT mode.
  if incus config device add proxyTester proxyDev proxy "listen=tcp:127.0.0.1:$HOST_TCP_PORT" "connect=tcp:[::]:4321" nat=true ; then
    echo "Proxy device shouldn't allow mixing IP address versions in NAT mode"
    false
  fi
  if incus config device add proxyTester proxyDev proxy "listen=tcp:[::1]:$HOST_TCP_PORT" connect=tcp:0.0.0.0:4321 nat=true ; then
    echo "Proxy device shouldn't allow mixing IP address versions in NAT mode"
    false
  fi

  # Check user proxy_protocol isn't allowed in NAT mode.
  if incus config device add proxyTester proxyDev proxy "listen=tcp:[::1]:$HOST_TCP_PORT" "connect=tcp:[::]:4321" nat=true proxy_protocol=true ; then
    echo "Proxy device shouldn't allow proxy_protocol in NAT mode"
    false
  fi

  # Check that old invalid config doesn't prevent device being stopped and removed cleanly.
  incus config device add proxyTester proxyDev proxy "listen=tcp:127.0.0.1:$HOST_TCP_PORT" connect=tcp:127.0.0.1:4321 bind=host
  incus admin sql global "UPDATE instances_devices_config SET value='tcp:localhost:4321' WHERE value='tcp:127.0.0.1:4321';"
  incus config device remove proxyTester proxyDev

  # Add the device again with the same listen param so if the old process hasn't been stopped it will fail to start.
  incus config device add proxyTester proxyDev proxy "listen=tcp:127.0.0.1:$HOST_TCP_PORT" connect=tcp:127.0.0.1:4321 bind=host

  incus delete -f proxyTester
}

container_devices_proxy_tcp() {
  echo "====> Testing tcp proxying"
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  # Setup
  MESSAGE="Proxy device test string: tcp"
  HOST_TCP_PORT=$(local_tcp_port)
  incus launch testimage proxyTester

  # Initial test
  nsenter -n -U -t "$(incus query /1.0/instances/proxyTester/state | jq .pid)" -- socat tcp-listen:4321 exec:/bin/cat &
  NSENTER_PID=$!
  incus config device add proxyTester proxyDev proxy "listen=tcp:127.0.0.1:$HOST_TCP_PORT" connect=tcp:127.0.0.1:4321 bind=host
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${INCUS_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  # Restart the container
  incus restart -f proxyTester
  nsenter -n -U -t "$(incus query /1.0/instances/proxyTester/state | jq .pid)" -- socat tcp-listen:4321 exec:/bin/cat &
  NSENTER_PID=$!
  sleep 1

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${INCUS_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart on container restart"
    false
  fi

  # Change the port
  nsenter -n -U -t "$(incus query /1.0/instances/proxyTester/state | jq .pid)" -- socat tcp-listen:1337 exec:/bin/cat &
  NSENTER_PID=$!
  incus config device set proxyTester proxyDev connect tcp:127.0.0.1:1337
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${INCUS_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  # Initial test
  incus config device remove proxyTester proxyDev
  HOST_TCP_PORT2=$(local_tcp_port)
  nsenter -n -U -t "$(incus query /1.0/instances/proxyTester/state | jq .pid)" -- socat tcp-listen:4321 exec:/bin/cat &
  NSENTER_PID=$!
  nsenter -n -U -t "$(incus query /1.0/instances/proxyTester/state | jq .pid)" -- socat tcp-listen:4322 exec:/bin/cat &
  NSENTER_PID1=$!
  incus config device add proxyTester proxyDev proxy "listen=tcp:127.0.0.1:$HOST_TCP_PORT,$HOST_TCP_PORT2" connect=tcp:127.0.0.1:4321-4322 bind=host
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true
  ECHO1=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT2}")
  kill "${NSENTER_PID1}" 2>/dev/null || true
  wait "${NSENTER_PID1}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${INCUS_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  if [ "${ECHO1}" != "${MESSAGE}" ]; then
    cat "${INCUS_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  # Cleanup
  incus delete -f proxyTester

  # Try NAT
  incus init testimage nattest

  incus network create inct$$ dns.domain=test dns.mode=managed ipv6.dhcp.stateful=true
  incus network attach inct$$ nattest eth0
  v4_addr="$(incus network get inct$$ ipv4.address | cut -d/ -f1)0"
  v6_addr="$(incus network get inct$$ ipv6.address | cut -d/ -f1)00"
  incus config device set nattest eth0 ipv4.address "${v4_addr}"
  incus config device set nattest eth0 ipv6.address "${v6_addr}"

  firewallDriver=$(incus info | awk -F ":" '/firewall:/{gsub(/ /, "", $0); print $2}')

  incus start nattest
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(iptables -w -t nat -S | grep -c "generated for Incus container nattest (validNAT)")" -eq 0 ]
  else
    ! nft -nn list chain inet incus prert.nattest.validNAT || false
    ! nft -nn list chain inet incus out.nattest.validNAT || false
  fi

  incus config device add nattest validNAT proxy listen="tcp:127.0.0.1:1234" connect="tcp:${v4_addr}:1234" bind=host
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(iptables -w -t nat -S | grep -c "generated for Incus container nattest (validNAT)")" -eq 0 ]
  else
    ! nft -nn list chain inet incus prert.nattest.validNAT || false
    ! nft -nn list chain inet incus out.nattest.validNAT || false
  fi

  # enable NAT
  incus config device set nattest validNAT nat true
  if [ "$firewallDriver" = "xtables" ]; then
    iptables -w -t nat -S | grep -- "-A PREROUTING -d 127.0.0.1/32 -p tcp -m tcp --dport 1234 -m comment --comment \"generated for Incus container nattest (validNAT)\" -j DNAT --to-destination ${v4_addr}:1234"
    iptables -w -t nat -S | grep -- "-A OUTPUT -d 127.0.0.1/32 -p tcp -m tcp --dport 1234 -m comment --comment \"generated for Incus container nattest (validNAT)\" -j DNAT --to-destination ${v4_addr}:1234"
    iptables -w -t nat -S | grep -- "-A POSTROUTING -s ${v4_addr}/32 -d ${v4_addr}/32 -p tcp -m tcp --dport 1234 -m comment --comment \"generated for Incus container nattest (validNAT)\" -j MASQUERADE"
  else
    [ "$(nft -nn list chain inet incus prert.nattest.validNAT | grep -c "ip daddr 127.0.0.1 tcp dport 1234 dnat ip to ${v4_addr}:1234")" -eq 1 ]
    [ "$(nft -nn list chain inet incus out.nattest.validNAT | grep -c "ip daddr 127.0.0.1 tcp dport 1234 dnat ip to ${v4_addr}:1234")" -eq 1 ]
  fi

  incus config device remove nattest validNAT
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(iptables -w -t nat -S | grep -c "generated for Incus container nattest (validNAT)")" -eq 0 ]
  else
    ! nft -nn list chain inet incus prert.nattest.validNAT
    ! nft -nn list chain inet incus out.nattest.validNAT
  fi

  incus config device add nattest validNAT proxy listen="tcp:127.0.0.1:1234-1235" connect="tcp:${v4_addr}:1234" bind=host nat=true
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(iptables -w -t nat -S | grep -c "generated for Incus container nattest (validNAT)")" -eq 3 ]
  else
    [ "$(nft -nn list chain inet incus prert.nattest.validNAT | grep -c "ip daddr 127.0.0.1 tcp dport 1234-1235 dnat ip to ${v4_addr}:1234")" -eq 1 ]
    [ "$(nft -nn list chain inet incus out.nattest.validNAT | grep -c "ip daddr 127.0.0.1 tcp dport 1234-1235 dnat ip to ${v4_addr}:1234")" -eq 1 ]
  fi

  incus config device remove nattest validNAT
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(iptables -w -t nat -S | grep -c "generated for Incus container nattest (validNAT)")" -eq 0 ]
  else
    ! nft -nn list chain inet incus prert.nattest.validNAT || false
    ! nft -nn list chain inet incus out.nattest.validNAT || false
  fi

  incus config device add nattest validNAT proxy listen="tcp:127.0.0.1:1234-1235" connect="tcp:${v4_addr}:1234-1235" bind=host nat=true
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(iptables -w -t nat -S | grep -c "generated for Incus container nattest (validNAT)")" -eq 3 ]
  else
    [ "$(nft -nn list chain inet incus prert.nattest.validNAT | grep -c "ip daddr 127.0.0.1 tcp dport 1234-1235 dnat ip to ${v4_addr}")" -eq 1 ]
    [ "$(nft -nn list chain inet incus out.nattest.validNAT | grep -c "ip daddr 127.0.0.1 tcp dport 1234-1235 dnat ip to ${v4_addr}")" -eq 1 ]
  fi

  incus config device remove nattest validNAT
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(iptables -w -t nat -S | grep -c "generated for Incus container nattest (validNAT)")" -eq 0 ]
  else
    ! nft -nn list chain inet incus prert.nattest.validNAT || false
    ! nft -nn list chain inet incus out.nattest.validNAT || false
  fi

  # IPv6 test
  incus config device add nattest validNAT proxy listen="tcp:[::1]:1234" connect="tcp:[::]:1234" bind=host nat=true
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(ip6tables -w -t nat -S | grep -c "generated for Incus container nattest (validNAT)")" -eq 3 ]
  else
    [ "$(nft -nn list chain inet incus prert.nattest.validNAT | grep -c "ip6 daddr ::1 tcp dport 1234 dnat ip6 to \[${v6_addr}\]:1234")" -eq 1 ]
    [ "$(nft -nn list chain inet incus out.nattest.validNAT | grep -c "ip6 daddr ::1 tcp dport 1234 dnat ip6 to \[${v6_addr}\]:1234")" -eq 1 ]
  fi

  incus config device unset nattest validNAT nat
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(ip6tables -w -t nat -S | grep -c "generated for Incus container nattest (validNAT)")" -eq 0 ]
  else
    ! nft -nn list chain inet incus prert.nattest.validNAT || false
    ! nft -nn list chain inet incus out.nattest.validNAT || false
  fi

  incus config device remove nattest validNAT

  # This won't enable NAT
  incus config device add nattest invalidNAT proxy listen="tcp:127.0.0.1:1234" connect="udp:${v4_addr}:1234" bind=host
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(iptables -w -t nat -S | grep -c "generated for Incus container nattest (invalidNAT)")" -eq 0 ]
  else
    ! nft -nn list chain inet incus prert.nattest.invalidNAT || false
    ! nft -nn list chain inet incus out.nattest.invalidNAT || false
  fi

  incus delete -f nattest
  if [ "$firewallDriver" = "xtables" ]; then
    [ "$(iptables -w -t nat -S | grep -c "generated for Incus container nattest (validNAT)")" -eq 0 ]
  else
    ! nft -nn list chain inet incus prert.nattest.validNAT || false
    ! nft -nn list chain inet incus out.nattest.validNAT || false
  fi

  incus network delete inct$$
}

container_devices_proxy_unix() {
  echo "====> Testing unix proxying"
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  # Setup
  MESSAGE="Proxy device test string: unix"
  HOST_SOCK="${TEST_DIR}/incustest-$(basename "${INCUS_DIR}")-host.sock"
  incus launch testimage proxyTester

  # Some busybox images don't have /tmp globally accessible.
  incus exec proxyTester -- chmod 1777 /tmp

  # Initial test
  (
    PID="$(incus query /1.0/instances/proxyTester/state | jq .pid)"
    cd "/proc/${PID}/root/tmp/" || exit
    umask 0000
    exec nsenter -n -U -t "${PID}" -- socat unix-listen:"incustest-$(basename "${INCUS_DIR}").sock",unlink-early exec:/bin/cat
  ) &
  NSENTER_PID=$!
  sleep 0.5

  incus config device add proxyTester proxyDev proxy "listen=unix:${HOST_SOCK}" uid=1234 gid=1234 security.uid=1234 security.gid=1234 connect=unix:/tmp/"incustest-$(basename "${INCUS_DIR}").sock" bind=host

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - unix:"${HOST_SOCK#"$(pwd)"/}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${INCUS_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  rm -f "${HOST_SOCK}"

  # Restart the container
  incus restart -f proxyTester
  (
    PID="$(incus query /1.0/instances/proxyTester/state | jq .pid)"
    cd "/proc/${PID}/root/tmp/" || exit
    umask 0000
    exec nsenter -n -U -t "${PID}" -- socat unix-listen:"incustest-$(basename "${INCUS_DIR}").sock",unlink-early exec:/bin/cat
  ) &
  NSENTER_PID=$!
  sleep 1

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - unix:"${HOST_SOCK#"$(pwd)"/}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${INCUS_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart on container restart"
    false
  fi

  rm -f "${HOST_SOCK}"

  # Change the socket
  (
    PID="$(incus query /1.0/instances/proxyTester/state | jq .pid)"
    cd "/proc/${PID}/root/tmp/" || exit
    umask 0000
    exec nsenter -n -U -t "${PID}" -- socat unix-listen:"incustest-$(basename "${INCUS_DIR}")-2.sock",unlink-early exec:/bin/cat
  ) &
  NSENTER_PID=$!

  incus config device set proxyTester proxyDev connect unix:/tmp/"incustest-$(basename "${INCUS_DIR}")-2.sock"
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - unix:"${HOST_SOCK#"$(pwd)"/}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${INCUS_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  rm -f "${HOST_SOCK}"

  # Cleanup
  incus delete -f proxyTester
}

container_devices_proxy_tcp_unix() {
  echo "====> Testing tcp to unix proxying"
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  # Setup
  MESSAGE="Proxy device test string: tcp -> unix"
  HOST_TCP_PORT=$(local_tcp_port)
  incus launch testimage proxyTester

  # Initial test
  (
    PID="$(incus query /1.0/instances/proxyTester/state | jq .pid)"
    cd "/proc/${PID}/root/tmp/" || exit
    umask 0000
    exec nsenter -n -U -t "${PID}" -- socat unix-listen:"incustest-$(basename "${INCUS_DIR}").sock",unlink-early exec:/bin/cat
  ) &
  NSENTER_PID=$!

  incus config device add proxyTester proxyDev proxy "listen=tcp:127.0.0.1:${HOST_TCP_PORT}" connect=unix:/tmp/"incustest-$(basename "${INCUS_DIR}").sock" bind=host
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${INCUS_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  # Restart the container
  incus restart -f proxyTester
  (
    PID="$(incus query /1.0/instances/proxyTester/state | jq .pid)"
    cd "/proc/${PID}/root/tmp/" || exit
    umask 0000
    exec nsenter -n -U -t "${PID}" -- socat unix-listen:"incustest-$(basename "${INCUS_DIR}").sock",unlink-early exec:/bin/cat
  ) &
  NSENTER_PID=$!
  sleep 1

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${INCUS_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart on container restart"
    false
  fi

  # Change the socket
  (
    PID="$(incus query /1.0/instances/proxyTester/state | jq .pid)"
    cd "/proc/${PID}/root/tmp/" || exit
    umask 0000
    exec nsenter -n -U -t "${PID}" -- socat unix-listen:"incustest-$(basename "${INCUS_DIR}")-2.sock",unlink-early exec:/bin/cat
  ) &
  NSENTER_PID=$!

  incus config device set proxyTester proxyDev connect unix:/tmp/"incustest-$(basename "${INCUS_DIR}")-2.sock"
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${INCUS_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  # Cleanup
  incus delete -f proxyTester
}

container_devices_proxy_unix_tcp() {
  echo "====> Testing unix to tcp proxying"
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  # Setup
  MESSAGE="Proxy device test string: unix -> tcp"
  HOST_SOCK="${TEST_DIR}/incustest-$(basename "${INCUS_DIR}")-host.sock"
  incus launch testimage proxyTester

  # Initial test
  nsenter -n -U -t "$(incus query /1.0/instances/proxyTester/state | jq .pid)" -- socat tcp-listen:4321 exec:/bin/cat &
  NSENTER_PID=$!
  incus config device add proxyTester proxyDev proxy "listen=unix:${HOST_SOCK}" connect=tcp:127.0.0.1:4321 bind=host
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - unix:"${HOST_SOCK#"$(pwd)"/}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${INCUS_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  rm -f "${HOST_SOCK}"

  # Restart the container
  incus restart -f proxyTester
  nsenter -n -U -t "$(incus query /1.0/instances/proxyTester/state | jq .pid)" -- socat tcp-listen:4321 exec:/bin/cat &
  NSENTER_PID=$!
  sleep 1

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - unix:"${HOST_SOCK#"$(pwd)"/}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${INCUS_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart on container restart"
    false
  fi

  rm -f "${HOST_SOCK}"

  # Change the port
  nsenter -n -U -t "$(incus query /1.0/instances/proxyTester/state | jq .pid)" -- socat tcp-listen:1337 exec:/bin/cat &
  NSENTER_PID=$!
  incus config device set proxyTester proxyDev connect tcp:127.0.0.1:1337
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - unix:"${HOST_SOCK#"$(pwd)"/}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${INCUS_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  rm -f "${HOST_SOCK}"

  # Cleanup
  incus delete -f proxyTester
}

container_devices_proxy_udp() {
  echo "====> Testing udp proxying"
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  # Setup
  MESSAGE="Proxy device test string: udp"
  HOST_UDP_PORT=$(local_tcp_port)
  incus launch testimage proxyTester

  # Initial test
  nsenter -n -U -t "$(incus query /1.0/instances/proxyTester/state | jq .pid)" -- socat udp-listen:4321 exec:/bin/cat &
  NSENTER_PID=$!
  incus config device add proxyTester proxyDev proxy "listen=udp:127.0.0.1:$HOST_UDP_PORT" connect=udp:127.0.0.1:4321 bind=host
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - udp:127.0.0.1:"${HOST_UDP_PORT}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${INCUS_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  # Restart the container
  incus restart -f proxyTester
  nsenter -n -U -t "$(incus query /1.0/instances/proxyTester/state | jq .pid)" -- socat udp-listen:4321 exec:/bin/cat &
  NSENTER_PID=$!
  sleep 1

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - udp:127.0.0.1:"${HOST_UDP_PORT}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${INCUS_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart on container restart"
    false
  fi

  # Change the port
  nsenter -n -U -t "$(incus query /1.0/instances/proxyTester/state | jq .pid)" -- socat udp-listen:1337 exec:/bin/cat &
  NSENTER_PID=$!
  incus config device set proxyTester proxyDev connect udp:127.0.0.1:1337
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - udp:127.0.0.1:"${HOST_UDP_PORT}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${INCUS_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  # Cleanup
  incus delete -f proxyTester
}

container_devices_proxy_unix_udp() {
  echo "====> Testing unix to udp proxying"
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  # Setup
  MESSAGE="Proxy device test string: unix -> udp"
  HOST_SOCK="${TEST_DIR}/incustest-$(basename "${INCUS_DIR}")-host.sock"
  incus launch testimage proxyTester

  # Initial test
  nsenter -n -U -t "$(incus query /1.0/instances/proxyTester/state | jq .pid)" -- socat udp-listen:4321 exec:/bin/cat &
  NSENTER_PID=$!
  incus config device add proxyTester proxyDev proxy "listen=unix:${HOST_SOCK}" connect=udp:127.0.0.1:4321 bind=host
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - unix:"${HOST_SOCK#"$(pwd)"/}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${INCUS_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  rm -f "${HOST_SOCK}"

  # Restart the container
  incus restart -f proxyTester
  nsenter -n -U -t "$(incus query /1.0/instances/proxyTester/state | jq .pid)" -- socat udp-listen:4321 exec:/bin/cat &
  NSENTER_PID=$!
  sleep 1

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - unix:"${HOST_SOCK#"$(pwd)"/}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${INCUS_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart on container restart"
    false
  fi

  rm -f "${HOST_SOCK}"

  # Change the port
  nsenter -n -U -t "$(incus query /1.0/instances/proxyTester/state | jq .pid)" -- socat udp-listen:1337 exec:/bin/cat &
  NSENTER_PID=$!
  incus config device set proxyTester proxyDev connect udp:127.0.0.1:1337
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - unix:"${HOST_SOCK#"$(pwd)"/}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${INCUS_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  rm -f "${HOST_SOCK}"

  # Cleanup
  incus delete -f proxyTester
}

container_devices_proxy_tcp_udp() {
  echo "====> Testing tcp to udp proxying"
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  # Setup
  MESSAGE="Proxy device test string: tcp -> udp"
  HOST_TCP_PORT=$(local_tcp_port)
  incus launch testimage proxyTester

  # Initial test
  nsenter -n -U -t "$(incus query /1.0/instances/proxyTester/state | jq .pid)" -- socat udp-listen:4321 exec:/bin/cat &
  NSENTER_PID=$!
  incus config device add proxyTester proxyDev proxy "listen=tcp:127.0.0.1:$HOST_TCP_PORT" connect=udp:127.0.0.1:4321 bind=host
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${INCUS_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly send data from host to container"
    false
  fi

  # Restart the container
  incus restart -f proxyTester
  nsenter -n -U -t "$(incus query /1.0/instances/proxyTester/state | jq .pid)" -- socat udp-listen:4321 exec:/bin/cat &
  NSENTER_PID=$!
  sleep 1

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${INCUS_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart on container restart"
    false
  fi

  # Change the port
  nsenter -n -U -t "$(incus query /1.0/instances/proxyTester/state | jq .pid)" -- socat udp-listen:1337 exec:/bin/cat &
  NSENTER_PID=$!
  incus config device set proxyTester proxyDev connect udp:127.0.0.1:1337
  sleep 0.5

  ECHO=$( (echo "${MESSAGE}" ; sleep 0.5) | socat - tcp:127.0.0.1:"${HOST_TCP_PORT}")
  kill "${NSENTER_PID}" 2>/dev/null || true
  wait "${NSENTER_PID}" 2>/dev/null || true

  if [ "${ECHO}" != "${MESSAGE}" ]; then
    cat "${INCUS_DIR}/logs/proxyTester/proxy.proxyDev.log"
    echo "Proxy device did not properly restart when config was updated"
    false
  fi

  # Cleanup
  incus delete -f proxyTester
}

container_devices_proxy_with_overlapping_forward_net() {
  echo "====> Testing proxy creation with overlapping network forward"
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  netName="testnet"

  incus network create "${netName}" \
        ipv4.address=192.0.2.1/24 \
        ipv6.address=fd42:4242:4242:1010::1/64

  overlappingAddr="192.0.2.2"
  proxyTesterStaticIP="192.0.2.3"
  HOST_TCP_PORT=$(local_tcp_port)

  # First, launch container with a static IP
  incus launch testimage proxyTester
  incus config device add proxyTester eth0 nic \
    nictype=bridged \
    name=eth0 \
    parent=${netName} \
    ipv4.address=${proxyTesterStaticIP}

  # Check creating empty forward doesn't create any firewall rules.
  incus network forward create "${netName}" "${overlappingAddr}"

  # Test overlapping issue (network forward exists --> proxy creation should fail)
  ! incus config device add proxyTester proxyDev proxy "listen=tcp:${overlappingAddr}:$HOST_TCP_PORT" "connect=tcp:${proxyTesterStaticIP}:4321" nat=true || false

  # Intermediary cleanup
  incus delete -f proxyTester
  incus network forward delete "${netName}" "${overlappingAddr}"

  # Same operations as before but in the reverse order
  incus launch testimage proxyTester
  incus config device add proxyTester eth0 nic \
    nictype=bridged \
    name=eth0 \
    parent=${netName} \
    ipv4.address=${proxyTesterStaticIP}

  incus config device add proxyTester proxyDev proxy "listen=tcp:${overlappingAddr}:$HOST_TCP_PORT" "connect=tcp:${proxyTesterStaticIP}:4321" nat=true

  # Test overlapping issue (proxy exists --> network forward creation should fail)
  ! incus network forward create "${netName}" "${overlappingAddr}" || false

  # Final cleanup
  incus delete -f proxyTester
  incus network delete "${netName}"
}
