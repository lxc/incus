test_metrics() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  incus config set core.https_address "${INCUS_ADDR}"

  incus launch testimage c1
  incus init testimage c2

  # c1 metrics should show as the container is running
  incus query "/1.0/metrics" | grep "name=\"c1\""

  # c2 metrics should not exist as it's not running
  ! incus query "/1.0/metrics" | grep "name=\"c2\"" || false

  # create new certificate
  gen_cert_and_key "${TEST_DIR}/metrics.key" "${TEST_DIR}/metrics.crt" "metrics.local"

  # this should fail as the certificate is not trusted yet
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${INCUS_ADDR}/1.0/metrics" | grep "\"error_code\":403"

  # trust newly created certificate for metrics only
  incus config trust add-certificate "${TEST_DIR}/metrics.crt" --type=metrics

  # c1 metrics should show as the container is running
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${INCUS_ADDR}/1.0/metrics" | grep "name=\"c1\""

  # c2 metrics should not exist as it's not running
  ! curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${INCUS_ADDR}/1.0/metrics" | grep "name=\"c2\"" || false

  # make sure nothing else can be done with this certificate
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${INCUS_ADDR}/1.0/instances" | grep "\"error_code\":403"

  metrics_addr="127.0.0.1:$(local_tcp_port)"

  incus config set core.metrics_address "${metrics_addr}"

  # c1 metrics should show as the container is running
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${metrics_addr}/1.0/metrics" | grep "name=\"c1\""

  # c2 metrics should not exist as it's not running
  ! curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${metrics_addr}/1.0/metrics" | grep "name=\"c2\"" || false

  # make sure no other endpoint is available
  curl -k -s --cert "${TEST_DIR}/metrics.crt" --key "${TEST_DIR}/metrics.key" -X GET "https://${metrics_addr}/1.0/instances" | grep "\"error_code\":404"

  # test unauthenticated connections
  ! curl -k -s -X GET "https://${metrics_addr}/1.0/metrics" | grep "name=\"c1\"" || false
  incus config set core.metrics_authentication=false
  curl -k -s -X GET "https://${metrics_addr}/1.0/metrics" | grep "name=\"c1\""

  # Check that metrics contain instance type
  curl -k -s -X GET "https://${metrics_addr}/1.0/metrics" | grep "incus_cpu_effective_total" | grep "type=\"container\""

  incus delete -f c1 c2
}
