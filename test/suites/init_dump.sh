test_init_dump() {
    # - incus admin init --dump
    INCUS_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${INCUS_INIT_DIR}"
    spawn_incus "${INCUS_INIT_DIR}" false

    (
        set -e
        # shellcheck disable=SC2034
        INCUS_DIR=${INCUS_INIT_DIR}

        storage_pool="incustest-$(basename "${INCUS_DIR}")-data"
        driver="dir"

        cat << EOF | incus admin init --preseed
config:
  core.https_address: 127.0.0.1:9999
  images.auto_update_interval: 15
storage_pools:
- name: ${storage_pool}
  driver: $driver
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
        incus admin init --dump > config.yaml
        cat << EOF > expected.yaml
config:
  core.https_address: 127.0.0.1:9999
  images.auto_update_interval: "15"
networks:
- config:
    ipv4.address: none
    ipv6.address: none
  description: ""
  managed: true
  name: inct$$
  type: bridge
storage_pools:
- config:
    source: ${INCUS_DIR}/storage-pools/${storage_pool}
  description: ""
  name: ${storage_pool}
  driver: ${driver}
profiles:
- config: {}
  description: Default Incus profile
  devices:
    eth0:
      name: eth0
      nictype: p2p
      type: nic
    root:
      path: /
      pool: ${storage_pool}
      type: disk
  name: default
- config:
    limits.memory: 2GiB
  description: Test profile
  devices:
    test0:
      name: test0
      nictype: bridged
      parent: inct$$
      type: nic
  name: test-profile

EOF

        diff -u config.yaml expected.yaml
    )
    rm -f config.yaml expected.yaml
    kill_incus "${INCUS_INIT_DIR}"
}
