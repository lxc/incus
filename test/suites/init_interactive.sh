test_init_interactive() {
  # - incusd init
  INCUS_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_INIT_DIR}"
  spawn_incus "${INCUS_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034
    INCUS_DIR=${INCUS_INIT_DIR}

    # XXX We need to remove the eth0 device from the default profile, which
    #     is typically attached by spawn_incus.
    if inc profile show default | grep -q eth0; then
      inc profile device remove default eth0
    fi

    cat <<EOF | incusd init
no
yes
my-storage-pool
dir
no
yes
inct$$
auto
none
no
no
yes
EOF

    inc info | grep -q 'images.auto_update_interval: "0"'
    inc network list | grep -q "inct$$"
    inc storage list | grep -q "my-storage-pool"
    inc profile show default | grep -q "pool: my-storage-pool"
    inc profile show default | grep -q "network: inct$$"
    printf 'config: {}\ndevices: {}' | inc profile edit default
    inc network delete inct$$
  )
  kill_incus "${INCUS_INIT_DIR}"

  return
}
