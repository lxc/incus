test_syslog_socket() {
  INCUS_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  export INCUS_DIR
  chmod +x "${INCUS_DIR}"
  spawn_incus "${INCUS_DIR}" true

  incus config set core.syslog_socket=true
  incus monitor --type=network-acl > "${TEST_DIR}/ovn.log" &
  monitorOVNPID=$!

  sleep 1
  echo "<29> ovs|ovn-controller|00017|acl_log(foo)|INFO|name=\"some-acl\"" | socat - unix-sendto:"${INCUS_DIR}/syslog.socket"

  sleep 1

  kill -9 ${monitorOVNPID} || true
  grep -qF "type: network-acl" "${TEST_DIR}/ovn.log"
  grep -qF "name=\"some-acl\"" "${TEST_DIR}/ovn.log"

  shutdown_incus "${INCUS_DIR}"
}
