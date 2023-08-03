test_dev_incus() {
  ensure_import_testimage

  (
    cd dev_incus-client || return
    # Use -buildvcs=false here to prevent git complaining about untrusted directory when tests are run as root.
    go build -tags netgo -v -buildvcs=false ./...
  )

  inc launch testimage dev-incus -c security.guestapi=false

  ! inc exec dev-incus -- test -S /dev/incus/sock || false
  inc config unset dev-incus security.guestapi
  inc exec dev-incus -- test -S /dev/incus/sock
  inc file push "dev_incus-client/dev_incus-client" dev-incus/bin/

  inc exec dev-incus chmod +x /bin/dev_incus-client

  inc config set dev-incus user.foo bar
  inc exec dev-incus dev_incus-client user.foo | grep bar

  inc config set dev-incus user.foo "bar %s bar"
  inc exec dev-incus dev_incus-client user.foo | grep "bar %s bar"

  inc config set dev-incus security.nesting true
  ! inc exec dev-incus dev_incus-client security.nesting | grep true || false

  cmd=$(unset -f inc; command -v inc)
  ${cmd} exec dev-incus dev_incus-client monitor-websocket > "${TEST_DIR}/dev_incus-websocket.log" &
  client_websocket=$!

  ${cmd} exec dev-incus dev_incus-client monitor-stream > "${TEST_DIR}/dev_incus-stream.log" &
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
  ) > "${TEST_DIR}/dev_incus.expected"

  MATCH=0

  for _ in $(seq 10); do
    inc config set dev-incus user.foo bar
    inc config set dev-incus security.nesting true

    true > "${TEST_DIR}/dev_incus-websocket.log"
    true > "${TEST_DIR}/dev_incus-stream.log"

    inc config set dev-incus user.foo baz
    inc config set dev-incus security.nesting false
    inc config device add dev-incus mnt disk source="${TEST_DIR}" path=/mnt
    inc config device remove dev-incus mnt

    if [ "$(tr -d '\0' < "${TEST_DIR}/dev_incus-websocket.log" | md5sum | cut -d' ' -f1)" != "$(md5sum "${TEST_DIR}/dev_incus.expected" | cut -d' ' -f1)" ] || [ "$(tr -d '\0' < "${TEST_DIR}/dev_incus-stream.log" | md5sum | cut -d' ' -f1)" != "$(md5sum "${TEST_DIR}/dev_incus.expected" | cut -d' ' -f1)" ]; then
      sleep 0.5
      continue
    fi

    MATCH=1
    break
  done

  kill -9 "${client_websocket}"
  kill -9 "${client_stream}"

  inc monitor --type=lifecycle > "${TEST_DIR}/dev_incus.log" &
  monitorDevIncusPID=$!

  # Test instance Ready state
  inc info dev-incus | grep -q 'Status: RUNNING'
  inc exec dev-incus dev_incus-client ready-state true
  [ "$(inc config get dev-incus volatile.last_state.ready)" = "true" ]

  grep -Fc "instance-ready" "${TEST_DIR}/dev_incus.log" | grep -Fx 1

  inc info dev-incus | grep -q 'Status: READY'
  inc exec dev-incus dev_incus-client ready-state false
  [ "$(inc config get dev-incus volatile.last_state.ready)" = "false" ]

  grep -Fc "instance-ready" "${TEST_DIR}/dev_incus.log" | grep -Fx 1

  inc info dev-incus | grep -q 'Status: RUNNING'

  kill -9 ${monitorDevIncusPID} || true

  shutdown_incus "${INCUS_DIR}"
  respawn_incus "${INCUS_DIR}" true

  # volatile.last_state.ready should be unset during daemon init
  [ -z "$(inc config get dev-incus volatile.last_state.ready)" ]

  inc monitor --type=lifecycle > "${TEST_DIR}/dev_incus.log" &
  monitorDevIncusPID=$!

  inc exec dev-incus dev_incus-client ready-state true
  [ "$(inc config get dev-incus volatile.last_state.ready)" = "true" ]

  grep -Fc "instance-ready" "${TEST_DIR}/dev_incus.log" | grep -Fx 1

  inc stop -f dev-incus
  [ "$(inc config get dev-incus volatile.last_state.ready)" = "false" ]

  inc start dev-incus
  inc exec dev-incus dev_incus-client ready-state true
  [ "$(inc config get dev-incus volatile.last_state.ready)" = "true" ]

  grep -Fc "instance-ready" "${TEST_DIR}/dev_incus.log" | grep -Fx 2

  # Check device configs are available and that NIC hwaddr is available even if volatile.
  hwaddr=$(inc config get dev-incus volatile.eth0.hwaddr)
  inc exec dev-incus dev_incus-client devices | jq -r .eth0.hwaddr | grep -Fx "${hwaddr}"

  inc delete dev-incus --force
  kill -9 ${monitorDevIncusPID} || true

  [ "${MATCH}" = "1" ] || false
}
