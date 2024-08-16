test_init_interactive() {
  # - incus admin init
  INCUS_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_INIT_DIR}"
  spawn_incus "${INCUS_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034
    INCUS_DIR=${INCUS_INIT_DIR}

    # XXX We need to remove the eth0 device from the default profile, which
    #     is typically attached by spawn_incus.
    if incus profile show default | grep -q eth0; then
      incus profile device remove default eth0
    fi

    cat <<EOF | incus admin init
no
yes
my-storage-pool
dir

yes
inct$$
auto
none
no
no
yes
EOF

    incus info | grep -q 'images.auto_update_interval: "0"'
    incus network list | grep -q "inct$$"
    incus storage list | grep -q "my-storage-pool"
    incus profile show default | grep -q "pool: my-storage-pool"
    incus profile show default | grep -q "network: inct$$"
    printf 'config: {}\ndevices: {}' | incus profile edit default
    incus network delete inct$$
  )
  kill_incus "${INCUS_INIT_DIR}"

  return
}
