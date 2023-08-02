test_init_interactive() {
  # - lxd init
  INCUS_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_INIT_DIR}"
  spawn_incus "${INCUS_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034
    INCUS_DIR=${INCUS_INIT_DIR}

    # XXX We need to remove the eth0 device from the default profile, which
    #     is typically attached by spawn_incus.
    if lxc profile show default | grep -q eth0; then
      lxc profile device remove default eth0
    fi

    cat <<EOF | lxd init
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

    lxc info | grep -q 'images.auto_update_interval: "0"'
    lxc network list | grep -q "inct$$"
    lxc storage list | grep -q "my-storage-pool"
    lxc profile show default | grep -q "pool: my-storage-pool"
    lxc profile show default | grep -q "network: inct$$"
    printf 'config: {}\ndevices: {}' | lxc profile edit default
    lxc network delete inct$$
  )
  kill_incus "${INCUS_INIT_DIR}"

  return
}
