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
    incus project create foo
    incus profile create foo
    incus network zone create foo
    incus network zone record create foo foo
    incus network zone record create foo bar
    incus network zone create bar
    incus network integration create foo ovn
    incus network integration create bar ovn

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

    count=$(curl -G --unix-socket "$INCUS_DIR/unix.socket" "incus/1.0/images" --data-urlencode "recursion=0" --data-urlencode "filter=properties.os eq BusyBox" | jq ".metadata | length")
    [ "${count}" = "1" ] || false

    count=$(curl -G --unix-socket "$INCUS_DIR/unix.socket" "incus/1.0/projects" --data-urlencode "recursion=0" --data-urlencode "filter=name eq foo" | jq ".metadata | length")
    [ "${count}" = "1" ] || false

    count=$(curl -G --unix-socket "$INCUS_DIR/unix.socket" "incus/1.0/profiles" --data-urlencode "recursion=0" --data-urlencode "filter=name eq foo" | jq ".metadata | length")
    [ "${count}" = "1" ] || false

    count=$(curl -G --unix-socket "$INCUS_DIR/unix.socket" "incus/1.0/network-zones" --data-urlencode "recursion=0" --data-urlencode "filter=name eq foo" | jq ".metadata | length")
    [ "${count}" = "1" ] || false

    count=$(curl -G --unix-socket "$INCUS_DIR/unix.socket" "incus/1.0/network-zones/foo/records" --data-urlencode "recursion=0" --data-urlencode "filter=name eq foo" | jq ".metadata | length")
    [ "${count}" = "1" ] || false

    count=$(curl -G --unix-socket "$INCUS_DIR/unix.socket" "incus/1.0/network-integrations" --data-urlencode "recursion=0" --data-urlencode "filter=name eq foo" | jq ".metadata | length")
    [ "${count}" = "1" ] || false

    incus delete c1
    incus delete c2
    incus project delete foo
    incus profile delete foo
    incus network zone delete foo
    incus network zone delete bar
    incus network integration delete foo
    incus network integration delete bar
  )

  kill_incus "${INCUS_FILTERING_DIR}"
}
