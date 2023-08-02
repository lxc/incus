test_fdleak() {
  INCUS_FDLEAK_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_FDLEAK_DIR}"
  spawn_incus "${INCUS_FDLEAK_DIR}" true
  pid=$(cat "${INCUS_FDLEAK_DIR}/lxd.pid")

  beforefds=$(/bin/ls "/proc/${pid}/fd" | wc -l)
  (
    set -e
    # shellcheck disable=SC2034
    INCUS_DIR=${INCUS_FDLEAK_DIR}

    ensure_import_testimage

    for i in $(seq 5); do
      lxc init testimage "leaktest${i}"
      lxc info "leaktest${i}"
      lxc start "leaktest${i}"
      lxc exec "leaktest${i}" -- ps -ef
      lxc stop "leaktest${i}" --force
      lxc delete "leaktest${i}"
    done

    lxc list
    lxc query /internal/gc

    exit 0
  )

  # Check for open handles to liblxc lxc.log files.
  ! find "/proc/${pid}/fd" -ls | grep lxc.log || false

  for i in $(seq 20); do
    afterfds=$(/bin/ls "/proc/${pid}/fd" | wc -l)
    leakedfds=$((afterfds - beforefds))

    [ "${leakedfds}" -gt 5 ] || break
    sleep 0.5
  done

  bad=0
  # shellcheck disable=SC2015
  [ ${leakedfds} -gt 5 ] && bad=1 || true
  if [ ${bad} -eq 1 ]; then
    echo "${leakedfds} FDS leaked"
    ls "/proc/${pid}/fd" -al
    netstat -anp 2>&1 | grep "${pid}/"
    false
  fi

  kill_incus "${INCUS_FDLEAK_DIR}"
}
