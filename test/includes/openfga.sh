run_openfga() {
  mkdir "${TEST_DIR}/openfga"

  API_TOKEN="$(tr -dc A-Za-z0-9 </dev/urandom | head -c 16)"
  echo "${API_TOKEN}" > "${TEST_DIR}/openfga/token"

  # Use host IP so that the server is addressable from other network namespaces in cluster tests.
  HOST_IP="$(hostname -I | cut -d' ' -f1)"
  HTTP_PORT="$(local_tcp_port)"
  HTTP_ADDR="${HOST_IP}:${HTTP_PORT}"
  echo "${HTTP_ADDR}" > "${TEST_DIR}/openfga/addr.http"

  GRPC_PORT="$(local_tcp_port)"
  GRPC_ADDR="${HOST_IP}:${GRPC_PORT}"
  echo "${GRPC_ADDR}" > "${TEST_DIR}/openfga/addr.grpc"

  openfga run \
    --http-addr "${HTTP_ADDR}" \
    --grpc-addr "${GRPC_ADDR}" \
    --authn-method=preshared \
    --authn-preshared-keys="${API_TOKEN}" \
    --playground-enabled=false \
    --metrics-enabled=false >"${TEST_DIR}/openfga/openfga.log" 2>&1 &
  PID="$!"
  sleep 1

  echo "${PID}" > "${TEST_DIR}/openfga/pid"
}

shutdown_openfga() {
  if [ ! -d "${TEST_DIR}/openfga" ]; then
    return
  fi

  incus config unset openfga.api.url
  incus config unset openfga.api.token
  incus config unset openfga.store.id

  pid="$(cat "${TEST_DIR}/openfga/pid")"
  kill "${pid}"

  rm -rf "${TEST_DIR}/openfga"
}

fga_address() {
  echo "http://$(cat "${TEST_DIR}/openfga/addr.http")"
}

fga_token() {
  cat "${TEST_DIR}/openfga/token"
}

fga() {
  cmd=$(unset -f fga; command -v fga)
  cmd="${cmd} --api-token $(fga_token) --server-url $(fga_address) ${*}"
  eval "${cmd}"
}
