test_exec() {
  ensure_import_testimage

  name=x1
  lxc launch testimage x1
  lxc list ${name} | grep RUNNING

  exec_container_noninteractive() {
    echo "abc${1}" | lxc exec "${name}" --force-noninteractive -- cat | grep abc
  }

  exec_container_interactive() {
    echo "abc${1}" | lxc exec "${name}" -- cat | grep abc
  }

  for i in $(seq 1 25); do
    exec_container_interactive "${i}" > "${INCUS_DIR}/exec-${i}.out" 2>&1
  done

  for i in $(seq 1 25); do
    exec_container_noninteractive "${i}" > "${INCUS_DIR}/exec-${i}.out" 2>&1
  done

  # Check non-websocket based exec works.
  opID=$(lxc query -X POST -d '{\"command\":[\"touch\",\"/root/foo1\"],\"record-output\":false}' /1.0/instances/x1/exec | jq -r .id)
  sleep 1
  lxc query  /1.0/operations/"${opID}" | jq .metadata.return | grep -F "0"
  lxc exec x1 -- stat /root/foo1

  opID=$(lxc query -X POST -d '{\"command\":[\"missingcmd\"],\"record-output\":false}' /1.0/instances/x1/exec | jq -r .id)
  sleep 1
  lxc query  /1.0/operations/"${opID}" | jq .metadata.return | grep -F "127"

  echo "hello" | lxc exec x1 -- tee /root/foo1
  opID=$(lxc query -X POST -d '{\"command\":[\"cat\",\"/root/foo1\"],\"record-output\":true}' /1.0/instances/x1/exec | jq -r .id)
  sleep 1
  stdOutURL=$(lxc query  /1.0/operations/"${opID}" | jq '.metadata.output["1"]')
  lxc query "${stdOutURL}" | grep -F "hello"

  lxc stop "${name}" --force
  lxc delete "${name}"
}

test_concurrent_exec() {
  if [ -z "${INCUS_CONCURRENT:-}" ]; then
    echo "==> SKIP: INCUS_CONCURRENT isn't set"
    return
  fi

  ensure_import_testimage

  name=x1
  lxc launch testimage x1
  lxc list ${name} | grep RUNNING

  exec_container_noninteractive() {
    echo "abc${1}" | lxc exec "${name}" --force-noninteractive -- cat | grep abc
  }

  exec_container_interactive() {
    echo "abc${1}" | lxc exec "${name}" -- cat | grep abc
  }

  PIDS=""
  for i in $(seq 1 25); do
    exec_container_interactive "${i}" > "${INCUS_DIR}/exec-${i}.out" 2>&1 &
    PIDS="${PIDS} $!"
  done

  for i in $(seq 1 25); do
    exec_container_noninteractive "${i}" > "${INCUS_DIR}/exec-${i}.out" 2>&1 &
    PIDS="${PIDS} $!"
  done

  for pid in ${PIDS}; do
    wait "${pid}"
  done

  lxc stop "${name}" --force
  lxc delete "${name}"
}
