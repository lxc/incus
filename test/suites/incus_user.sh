test_incus_user() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  incus-user --group nogroup &
  USER_PID="$!"
  while :; do
    [ -S "${INCUS_DIR}/unix.socket.user" ] && break
    sleep 0.5
  done

  USER_TEMPDIR="${TEST_DIR}/user"
  mkdir "${USER_TEMPDIR}"
  chown nobody:nogroup "${USER_TEMPDIR}"

  cmd=$(unset -f incus; command -v incus)
  sudo -u nobody -Es -- env INCUS_CONF="${USER_TEMPDIR}" "${cmd}" project list

  kill -9 "${USER_PID}"
}
