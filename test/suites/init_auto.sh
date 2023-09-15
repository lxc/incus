test_init_auto() {
  # - incus admin init --auto --storage-backend zfs
  # and
  # - incus admin init --auto
  # can't be easily tested on jenkins since it hard-codes "default" as pool
  # naming. This can cause naming conflicts when multiple test-suites are run on
  # a single runner.

  if [ "$(storage_backend "$INCUS_DIR")" = "zfs" ]; then
    # incus admin init --auto --storage-backend zfs --storage-pool <name>
    INCUS_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${INCUS_INIT_DIR}"
    spawn_incus "${INCUS_INIT_DIR}" false

    configure_loop_device loop_file_1 loop_device_1
    # shellcheck disable=SC2154
    zpool create -m none -O compression=on "incustest-$(basename "${INCUS_DIR}")-pool1-existing-pool" "${loop_device_1}"
    INCUS_DIR=${INCUS_INIT_DIR} incus admin init --auto --storage-backend zfs --storage-pool "incustest-$(basename "${INCUS_DIR}")-pool1-existing-pool"
    INCUS_DIR=${INCUS_INIT_DIR} incus profile show default | grep -q "pool: default"

    kill_incus "${INCUS_INIT_DIR}"
    sed -i "\\|^${loop_device_1}|d" "${TEST_DIR}/loops"
    losetup -d "${loop_device_1}"

    # incus admin init --auto --storage-backend zfs --storage-pool <name>/<non-existing-dataset>
    INCUS_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${INCUS_INIT_DIR}"
    spawn_incus "${INCUS_INIT_DIR}" false

    # shellcheck disable=SC2154
    configure_loop_device loop_file_1 loop_device_1
    zpool create -m none -O compression=on "incustest-$(basename "${INCUS_DIR}")-pool1-existing-pool" "${loop_device_1}"
    INCUS_DIR=${INCUS_INIT_DIR} incus admin init --auto --storage-backend zfs --storage-pool "incustest-$(basename "${INCUS_DIR}")-pool1-existing-pool/non-existing-dataset"
    kill_incus "${INCUS_INIT_DIR}"

    # incus admin init --auto --storage-backend zfs --storage-pool <name>/<existing-dataset>
    INCUS_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${INCUS_INIT_DIR}"
    spawn_incus "${INCUS_INIT_DIR}" false

    zfs create -p -o mountpoint=none "incustest-$(basename "${INCUS_DIR}")-pool1-existing-pool/existing-dataset"
    INCUS_DIR=${INCUS_INIT_DIR} incus admin init --auto --storage-backend zfs --storage-pool "incustest-$(basename "${INCUS_DIR}")-pool1-existing-pool/existing-dataset"

    kill_incus "${INCUS_INIT_DIR}"
    zpool destroy "incustest-$(basename "${INCUS_DIR}")-pool1-existing-pool"
    sed -i "\\|^${loop_device_1}|d" "${TEST_DIR}/loops"
    losetup -d "${loop_device_1}"

    # incus admin init --storage-backend zfs --storage-create-loop 1 --storage-pool <name> --auto
    INCUS_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${INCUS_INIT_DIR}"
    spawn_incus "${INCUS_INIT_DIR}" false

    ZFS_POOL="incustest-$(basename "${INCUS_DIR}")-init"
    INCUS_DIR=${INCUS_INIT_DIR} incus admin init --storage-backend zfs --storage-create-loop 1 --storage-pool "${ZFS_POOL}" --auto

    kill_incus "${INCUS_INIT_DIR}"
  fi

  # incus admin init --network-address 127.0.0.1 --network-port LOCAL --auto
  INCUS_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_INIT_DIR}"
  spawn_incus "${INCUS_INIT_DIR}" false

  INCUS_DIR=${INCUS_INIT_DIR} incus admin init --network-address 127.0.0.1 --network-port "$(local_tcp_port)" --auto

  kill_incus "${INCUS_INIT_DIR}"
}
