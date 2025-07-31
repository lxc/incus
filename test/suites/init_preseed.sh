test_init_preseed() {
    # - incus admin init --preseed
    incus_backend=$(storage_backend "$INCUS_DIR")
    INCUS_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${INCUS_INIT_DIR}"
    spawn_incus "${INCUS_INIT_DIR}" false

    (
        set -e
        # shellcheck disable=SC2034
        INCUS_DIR=${INCUS_INIT_DIR}

        storage_pool="incustest-$(basename "${INCUS_DIR}")-data"
        storage_volume="${storage_pool}-volume"
        # In case we're running against the ZFS backend, let's test
        # creating a zfs storage pool, otherwise just use dir.
        if [ "$incus_backend" = "zfs" ]; then
            configure_loop_device loop_file_4 loop_device_4
            # shellcheck disable=SC2154
            zpool create -f -m none -O compression=on "incustest-$(basename "${INCUS_DIR}")-preseed-pool" "${loop_device_4}"
            driver="zfs"
            source="incustest-$(basename "${INCUS_DIR}")-preseed-pool"
        elif [ "$incus_backend" = "ceph" ]; then
            driver="ceph"
            source=""
        else
            driver="dir"
            source=""
        fi

        cat << EOF | incus admin init --preseed
config:
  core.https_address: 127.0.0.1:9999
  images.auto_update_interval: 15
storage_pools:
- name: ${storage_pool}
  driver: $driver
  config:
    source: $source
storage_volumes:
- name: ${storage_volume}
  pool: ${storage_pool}
networks:
- name: inct$$
  type: bridge
  config:
    ipv4.address: none
    ipv6.address: none
profiles:
- name: default
  devices:
    root:
      path: /
      pool: ${storage_pool}
      type: disk
- name: test-profile
  description: "Test profile"
  config:
    limits.memory: 2GiB
  devices:
    test0:
      name: test0
      nictype: bridged
      parent: inct$$
      type: nic
EOF

        incus info | grep -q 'core.https_address: 127.0.0.1:9999'
        incus info | grep -q 'images.auto_update_interval: "15"'
        incus network list | grep -q "inct$$"
        incus storage list | grep -q "${storage_pool}"
        incus storage show "${storage_pool}" | grep -q "$source"
        incus storage volume list "${storage_pool}" | grep -q "${storage_volume}"
        incus profile list | grep -q "test-profile"
        incus profile show default | grep -q "pool: ${storage_pool}"
        incus profile show test-profile | grep -q "limits.memory: 2GiB"
        incus profile show test-profile | grep -q "nictype: bridged"
        incus profile show test-profile | grep -q "parent: inct$$"
        printf 'config: {}\ndevices: {}' | incus profile edit default
        incus profile delete test-profile
        incus network delete inct$$
        incus storage volume delete "${storage_pool}" "${storage_volume}"
        incus storage delete "${storage_pool}"

        if [ "$incus_backend" = "zfs" ]; then
            # shellcheck disable=SC2154
            deconfigure_loop_device "${loop_file_4}" "${loop_device_4}"
        fi
    )
    kill_incus "${INCUS_INIT_DIR}"
}
