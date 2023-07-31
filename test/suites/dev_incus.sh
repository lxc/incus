test_devlxd() {
  ensure_import_testimage

  (
    cd devlxd-client || return
    # Use -buildvcs=false here to prevent git complaining about untrusted directory when tests are run as root.
    go build -tags netgo -v -buildvcs=false ./...
  )

  lxc launch testimage devlxd -c security.devlxd=false

  ! lxc exec devlxd -- test -S /dev/lxd/sock || false
  lxc config unset devlxd security.devlxd
  lxc exec devlxd -- test -S /dev/lxd/sock
  lxc file push "devlxd-client/devlxd-client" devlxd/bin/

  lxc exec devlxd chmod +x /bin/devlxd-client

  lxc config set devlxd user.foo bar
  lxc exec devlxd devlxd-client user.foo | grep bar

  lxc config set devlxd user.foo "bar %s bar"
  lxc exec devlxd devlxd-client user.foo | grep "bar %s bar"

  lxc config set devlxd security.nesting true
  ! lxc exec devlxd devlxd-client security.nesting | grep true || false

  cmd=$(unset -f lxc; command -v lxc)
  ${cmd} exec devlxd devlxd-client monitor-websocket > "${TEST_DIR}/devlxd-websocket.log" &
  client_websocket=$!

  ${cmd} exec devlxd devlxd-client monitor-stream > "${TEST_DIR}/devlxd-stream.log" &
  client_stream=$!

  (
    cat << EOF
metadata:
  key: user.foo
  old_value: bar
  value: baz
timestamp: null
type: config

metadata:
  action: added
  config:
    path: /mnt
    source: ${TEST_DIR}
    type: disk
  name: mnt
timestamp: null
type: device

metadata:
  action: removed
  config:
    path: /mnt
    source: ${TEST_DIR}
    type: disk
  name: mnt
timestamp: null
type: device

EOF
  ) > "${TEST_DIR}/devlxd.expected"

  MATCH=0

  for _ in $(seq 10); do
    lxc config set devlxd user.foo bar
    lxc config set devlxd security.nesting true

    true > "${TEST_DIR}/devlxd-websocket.log"
    true > "${TEST_DIR}/devlxd-stream.log"

    lxc config set devlxd user.foo baz
    lxc config set devlxd security.nesting false
    lxc config device add devlxd mnt disk source="${TEST_DIR}" path=/mnt
    lxc config device remove devlxd mnt

    if [ "$(tr -d '\0' < "${TEST_DIR}/devlxd-websocket.log" | md5sum | cut -d' ' -f1)" != "$(md5sum "${TEST_DIR}/devlxd.expected" | cut -d' ' -f1)" ] || [ "$(tr -d '\0' < "${TEST_DIR}/devlxd-stream.log" | md5sum | cut -d' ' -f1)" != "$(md5sum "${TEST_DIR}/devlxd.expected" | cut -d' ' -f1)" ]; then
      sleep 0.5
      continue
    fi

    MATCH=1
    break
  done

  kill -9 "${client_websocket}"
  kill -9 "${client_stream}"

  lxc monitor --type=lifecycle > "${TEST_DIR}/devlxd.log" &
  monitorDevlxdPID=$!

  # Test instance Ready state
  lxc info devlxd | grep -q 'Status: RUNNING'
  lxc exec devlxd devlxd-client ready-state true
  [ "$(lxc config get devlxd volatile.last_state.ready)" = "true" ]

  grep -Fc "instance-ready" "${TEST_DIR}/devlxd.log" | grep -Fx 1

  lxc info devlxd | grep -q 'Status: READY'
  lxc exec devlxd devlxd-client ready-state false
  [ "$(lxc config get devlxd volatile.last_state.ready)" = "false" ]

  grep -Fc "instance-ready" "${TEST_DIR}/devlxd.log" | grep -Fx 1

  lxc info devlxd | grep -q 'Status: RUNNING'

  kill -9 ${monitorDevlxdPID} || true

  shutdown_lxd "${LXD_DIR}"
  respawn_lxd "${LXD_DIR}" true

  # volatile.last_state.ready should be unset during daemon init
  [ -z "$(lxc config get devlxd volatile.last_state.ready)" ]

  lxc monitor --type=lifecycle > "${TEST_DIR}/devlxd.log" &
  monitorDevlxdPID=$!

  lxc exec devlxd devlxd-client ready-state true
  [ "$(lxc config get devlxd volatile.last_state.ready)" = "true" ]

  grep -Fc "instance-ready" "${TEST_DIR}/devlxd.log" | grep -Fx 1

  lxc stop -f devlxd
  [ "$(lxc config get devlxd volatile.last_state.ready)" = "false" ]

  lxc start devlxd
  lxc exec devlxd devlxd-client ready-state true
  [ "$(lxc config get devlxd volatile.last_state.ready)" = "true" ]

  grep -Fc "instance-ready" "${TEST_DIR}/devlxd.log" | grep -Fx 2

  # Check device configs are available and that NIC hwaddr is available even if volatile.
  hwaddr=$(lxc config get devlxd volatile.eth0.hwaddr)
  lxc exec devlxd devlxd-client devices | jq -r .eth0.hwaddr | grep -Fx "${hwaddr}"

  lxc delete devlxd --force
  kill -9 ${monitorDevlxdPID} || true

  [ "${MATCH}" = "1" ] || false
}
