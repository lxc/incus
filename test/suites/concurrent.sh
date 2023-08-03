test_concurrent() {
  if [ -z "${INCUS_CONCURRENT:-}" ]; then
    echo "==> SKIP: INCUS_CONCURRENT isn't set"
    return
  fi

  ensure_import_testimage

  spawn_container() {
    set -e

    name=concurrent-${1}

    inc launch testimage "${name}"
    inc info "${name}" | grep RUNNING
    echo abc | inc exec "${name}" -- cat | grep abc
    inc stop "${name}" --force
    inc delete "${name}"
  }

  PIDS=""

  for id in $(seq $(($(find /sys/bus/cpu/devices/ -type l | wc -l)*8))); do
    spawn_container "${id}" 2>&1 | tee "${INCUS_DIR}/incus-${id}.out" &
    PIDS="${PIDS} $!"
  done

  for pid in ${PIDS}; do
    wait "${pid}"
  done

  ! inc list | grep -q concurrent || false
}
