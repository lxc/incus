test_metrics() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  inc config set core.https_address "${INCUS_ADDR}"

  inc launch testimage c1
  inc init testimage c2

  # c1 metrics should show as the container is running
  inc query "/1.0/metrics" | grep "name=\"c1\""

  # c2 metrics should not exist as it's not running
  ! inc query "/1.0/metrics" | grep "name=\"c2\"" || false

  # create new certificate
  openssl req -x509 -newkey rsa:2048 -keyout "${TEST_DIR}/metrics.key" -nodes -out "${TEST_DIR}/metrics.crt" -subj "/CN=incus.local"

  # this should fail as the certificate is not trusted yet
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${INCUS_ADDR}/1.0/metrics" | grep "\"error_code\":403"

  # trust newly created certificate for metrics only
  inc config trust add "${TEST_DIR}/metrics.crt" --type=metrics

  # c1 metrics should show as the container is running
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${INCUS_ADDR}/1.0/metrics" | grep "name=\"c1\""

  # c2 metrics should not exist as it's not running
  ! curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${INCUS_ADDR}/1.0/metrics" | grep "name=\"c2\"" || false

  # make sure nothing else can be done with this certificate
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${INCUS_ADDR}/1.0/instances" | grep "\"error_code\":403"

  metrics_addr="127.0.0.1:$(local_tcp_port)"

  inc config set core.metrics_address "${metrics_addr}"

  # c1 metrics should show as the container is running
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${metrics_addr}/1.0/metrics" | grep "name=\"c1\""

  # c2 metrics should not exist as it's not running
  ! curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${metrics_addr}/1.0/metrics" | grep "name=\"c2\"" || false

  # make sure no other endpoint is available
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${metrics_addr}/1.0/instances" | grep "\"error_code\":404"

  # test unauthenticated connections
  ! curl -k -s -X GET "https://${metrics_addr}/1.0/metrics" | grep "name=\"c1\"" || false
  inc config set core.metrics_authentication=false
  curl -k -s -X GET "https://${metrics_addr}/1.0/metrics" | grep "name=\"c1\""

  inc delete -f c1 c2
}
