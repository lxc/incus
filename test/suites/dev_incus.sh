test_dev_incus() {
  ensure_import_testimage

  (
    cd dev_incus-client || return
    # Use -buildvcs=false here to prevent git complaining about untrusted directory when tests are run as root.
    go build -tags netgo -v -buildvcs=false ./...
  )

  incus launch testimage dev-incus -c security.guestapi=false

  ! incus exec dev-incus -- test -S /dev/incus/sock || false
  incus config unset dev-incus security.guestapi
  incus exec dev-incus -- test -S /dev/incus/sock
  incus file push --mode 0755 "dev_incus-client/dev_incus-client" dev-incus/bin/

  incus config set dev-incus user.foo bar
  incus exec dev-incus -- dev_incus-client user.foo | grep bar

  incus config set dev-incus user.foo "bar %s bar"
  incus exec dev-incus -- dev_incus-client user.foo | grep "bar %s bar"

  incus config set dev-incus security.nesting true
  ! incus exec dev-incus -- dev_incus-client security.nesting | grep true || false

  cmd=$(unset -f incus; command -v incus)
  ${cmd} exec dev-incus -- dev_incus-client monitor-websocket > "${TEST_DIR}/dev_incus-websocket.log" &
  client_websocket=$!

  ${cmd} exec dev-incus -- dev_incus-client monitor-stream > "${TEST_DIR}/dev_incus-stream.log" &
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
    incus config set dev-incus user.foo bar
    incus config set dev-incus security.nesting true

    true > "${TEST_DIR}/dev_incus-websocket.log"
    true > "${TEST_DIR}/dev_incus-stream.log"

    incus config set dev-incus user.foo baz
    incus config set dev-incus security.nesting false
    incus config device add dev-incus mnt disk source="${TEST_DIR}" path=/mnt
    incus config device remove dev-incus mnt

    if [ "$(tr -d '\0' < "${TEST_DIR}/dev_incus-websocket.log" | md5sum | cut -d' ' -f1)" != "$(md5sum "${TEST_DIR}/dev_incus.expected" | cut -d' ' -f1)" ] || [ "$(tr -d '\0' < "${TEST_DIR}/dev_incus-stream.log" | md5sum | cut -d' ' -f1)" != "$(md5sum "${TEST_DIR}/dev_incus.expected" | cut -d' ' -f1)" ]; then
      sleep 0.5
      continue
    fi

    MATCH=1
    break
  done

  kill -9 "${client_websocket}"
  kill -9 "${client_stream}"

  incus monitor --type=lifecycle > "${TEST_DIR}/dev_incus.log" &
  monitorDevIncusPID=$!

  # Test instance Ready state
  incus info dev-incus | grep -q 'Status: RUNNING'
  incus exec dev-incus -- dev_incus-client ready-state true
  [ "$(incus config get dev-incus volatile.last_state.ready)" = "true" ]

  grep -Fc "instance-ready" "${TEST_DIR}/dev_incus.log" | grep -Fx 1

  incus info dev-incus | grep -q 'Status: READY'
  incus exec dev-incus -- dev_incus-client ready-state false
  [ "$(incus config get dev-incus volatile.last_state.ready)" = "false" ]

  grep -Fc "instance-ready" "${TEST_DIR}/dev_incus.log" | grep -Fx 1

  incus info dev-incus | grep -q 'Status: RUNNING'

  kill -9 ${monitorDevIncusPID} || true

  shutdown_incus "${INCUS_DIR}"
  respawn_incus "${INCUS_DIR}" true

  # volatile.last_state.ready should be unset during daemon init
  [ -z "$(incus config get dev-incus volatile.last_state.ready)" ]

  incus monitor --type=lifecycle > "${TEST_DIR}/dev_incus.log" &
  monitorDevIncusPID=$!

  incus exec dev-incus -- dev_incus-client ready-state true
  [ "$(incus config get dev-incus volatile.last_state.ready)" = "true" ]

  grep -Fc "instance-ready" "${TEST_DIR}/dev_incus.log" | grep -Fx 1

  incus stop -f dev-incus
  [ "$(incus config get dev-incus volatile.last_state.ready)" = "false" ]

  incus start dev-incus
  incus exec dev-incus -- dev_incus-client ready-state true
  [ "$(incus config get dev-incus volatile.last_state.ready)" = "true" ]

  grep -Fc "instance-ready" "${TEST_DIR}/dev_incus.log" | grep -Fx 2

  # Check device configs are available and that NIC hwaddr is available even if volatile.
  hwaddr=$(incus config get dev-incus volatile.eth0.hwaddr)
  incus exec dev-incus -- dev_incus-client devices | jq -r .eth0.hwaddr | grep -Fx "${hwaddr}"

  incus delete dev-incus --force
  kill -9 ${monitorDevIncusPID} || true

  [ "${MATCH}" = "1" ] || false
}
