# Test API filtering.
test_filtering() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  INCUS_FILTERING_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)

  spawn_incus "${INCUS_FILTERING_DIR}" true

  (
    set -e
    # shellcheck disable=SC2034,SC2030
    INCUS_DIR="${INCUS_FILTERING_DIR}"

    ensure_import_testimage

    incus init testimage c1
    incus init testimage c2

    count=$(curl -G --unix-socket "$INCUS_DIR/unix.socket" "incus/1.0/instances" --data-urlencode "recursion=0" --data-urlencode "filter=name eq c1" | jq ".metadata | length")
    [ "${count}" = "1" ] || false

    count=$(curl -G --unix-socket "$INCUS_DIR/unix.socket" "incus/1.0/instances" --data-urlencode "recursion=1" --data-urlencode "filter=name eq c1" | jq ".metadata | length")
    [ "${count}" = "1" ] || false

    count=$(curl -G --unix-socket "$INCUS_DIR/unix.socket" "incus/1.0/instances" --data-urlencode "recursion=2" --data-urlencode "filter=name eq c1" | jq ".metadata | length")
    [ "${count}" = "1" ] || false

    count=$(curl -G --unix-socket "$INCUS_DIR/unix.socket" "incus/1.0/images" --data-urlencode "recursion=0" --data-urlencode "filter=properties.os eq BusyBox" | jq ".metadata | length")
    [ "${count}" = "1" ] || false

    count=$(curl -G --unix-socket "$INCUS_DIR/unix.socket" "incus/1.0/images" --data-urlencode "recursion=1" --data-urlencode "filter=properties.os eq Ubuntu" | jq ".metadata | length")
    [ "${count}" = "0" ] || false

    incus delete c1
    incus delete c2
  )

  kill_incus "${INCUS_FILTERING_DIR}"
}
