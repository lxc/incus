# LXD-related test helpers.

spawn_lxd() {
    set +x
    # LXD_DIR is local here because since $(lxc) is actually a function, it
    # overwrites the environment and we would lose LXD_DIR's value otherwise.

    # shellcheck disable=2039,3043
    local LXD_DIR lxddir lxd_backend

    lxddir=${1}
    shift

    storage=${1}
    shift

    # shellcheck disable=SC2153
    if [ "$LXD_BACKEND" = "random" ]; then
        lxd_backend="$(random_storage_backend)"
    else
        lxd_backend="$LXD_BACKEND"
    fi

    if [ "${LXD_BACKEND}" = "ceph" ] && [ -z "${LXD_CEPH_CLUSTER:-}" ]; then
        echo "A cluster name must be specified when using the CEPH driver." >&2
        exit 1
    fi

    # setup storage
    "$lxd_backend"_setup "${lxddir}"
    echo "$lxd_backend" > "${lxddir}/lxd.backend"

    echo "==> Spawning lxd in ${lxddir}"
    # shellcheck disable=SC2086

    if [ "${LXD_NETNS}" = "" ]; then
        LXD_DIR="${lxddir}" lxd --logfile "${lxddir}/lxd.log" "${DEBUG-}" "$@" 2>&1 &
    else
        # shellcheck disable=SC2153
        pid="$(cat "${TEST_DIR}/ns/${LXD_NETNS}/PID")"
        LXD_DIR="${lxddir}" nsenter -n -m -t "${pid}" lxd --logfile "${lxddir}/lxd.log" "${DEBUG-}" "$@" 2>&1 &
    fi
    LXD_PID=$!
    echo "${LXD_PID}" > "${lxddir}/lxd.pid"
    # shellcheck disable=SC2153
    echo "${lxddir}" >> "${TEST_DIR}/daemons"
    echo "==> Spawned LXD (PID is ${LXD_PID})"

    echo "==> Confirming lxd is responsive (PID is ${LXD_PID})"
    LXD_DIR="${lxddir}" lxd waitready --timeout=300 || (echo "Killing PID ${LXD_PID}" ; kill -9 "${LXD_PID}" ; false)

    if [ "${LXD_NETNS}" = "" ]; then
        echo "==> Binding to network"
        for _ in $(seq 10); do
            addr="127.0.0.1:$(local_tcp_port)"
            LXD_DIR="${lxddir}" lxc config set core.https_address "${addr}" || continue
            echo "${addr}" > "${lxddir}/lxd.addr"
            echo "==> Bound to ${addr}"
            break
        done
    fi

    echo "==> Setting trust password"
    LXD_DIR="${lxddir}" lxc config set core.trust_password foo
    if [ -n "${DEBUG:-}" ]; then
        set -x
    fi

    if [ "${LXD_NETNS}" = "" ]; then
        echo "==> Setting up networking"
        LXD_DIR="${lxddir}" lxc profile device add default eth0 nic nictype=p2p name=eth0
    fi

    if [ "${storage}" = true ]; then
        echo "==> Configuring storage backend"
        "$lxd_backend"_configure "${lxddir}"
    fi
}

respawn_lxd() {
    set +x
    # LXD_DIR is local here because since $(lxc) is actually a function, it
    # overwrites the environment and we would lose LXD_DIR's value otherwise.

    # shellcheck disable=2039,3043
    local LXD_DIR

    lxddir=${1}
    shift

    wait=${1}
    shift

    echo "==> Spawning lxd in ${lxddir}"
    # shellcheck disable=SC2086
    if [ "${LXD_NETNS}" = "" ]; then
        LXD_DIR="${lxddir}" lxd --logfile "${lxddir}/lxd.log" "${DEBUG-}" "$@" 2>&1 &
    else
        pid="$(cat "${TEST_DIR}/ns/${LXD_NETNS}/PID")"
        LXD_DIR="${lxddir}" nsenter -n -m -t "${pid}" lxd --logfile "${lxddir}/lxd.log" "${DEBUG-}" "$@" 2>&1 &
    fi
    LXD_PID=$!
    echo "${LXD_PID}" > "${lxddir}/lxd.pid"
    echo "==> Spawned LXD (PID is ${LXD_PID})"

    if [ "${wait}" = true ]; then
        echo "==> Confirming lxd is responsive (PID is ${LXD_PID})"
        LXD_DIR="${lxddir}" lxd waitready --timeout=300 || (echo "Killing PID ${LXD_PID}" ; kill -9 "${LXD_PID}" ; false)
    fi

    if [ -n "${DEBUG:-}" ]; then
        set -x
    fi
}

kill_lxd() {
    # LXD_DIR is local here because since $(lxc) is actually a function, it
    # overwrites the environment and we would lose LXD_DIR's value otherwise.

    # shellcheck disable=2039,3043
    local LXD_DIR daemon_dir daemon_pid check_leftovers lxd_backend

    daemon_dir=${1}
    LXD_DIR=${daemon_dir}

    # Check if already killed
    if [ ! -f "${daemon_dir}/lxd.pid" ]; then
      return
    fi

    daemon_pid=$(cat "${daemon_dir}/lxd.pid")
    check_leftovers="false"
    lxd_backend=$(storage_backend "$daemon_dir")
    echo "==> Killing LXD at ${daemon_dir} (${daemon_pid})"

    if [ -e "${daemon_dir}/unix.socket" ]; then
        # Delete all containers
        echo "==> Deleting all containers"
        for container in $(timeout -k 2 2 lxc list --force-local --format csv --columns n); do
            timeout -k 10 10 lxc delete "${container}" --force-local -f || true
        done

        # Delete all images
        echo "==> Deleting all images"
        for image in $(timeout -k 2 2 lxc image list --force-local --format csv --columns f); do
            timeout -k 10 10 lxc image delete "${image}" --force-local || true
        done

        # Delete all profiles
        echo "==> Deleting all profiles"
        for profile in $(timeout -k 2 2 lxc profile list --force-local --format csv | cut -d, -f1); do
            timeout -k 10 10 lxc profile delete "${profile}" --force-local || true
        done

        # Delete all networks
        echo "==> Deleting all managed networks"
        for network in $(timeout -k 2 2 lxc network list --force-local --format csv | awk -F, '{if ($3 == "YES") {print $1}}'); do
            timeout -k 10 10 lxc network delete "${network}" --force-local || true
        done

        # Clear config of the default profile since the profile itself cannot
        # be deleted.
        echo "==> Clearing config of default profile"
        printf 'config: {}\ndevices: {}' | timeout -k 5 5 lxc profile edit default

        echo "==> Deleting all storage pools"
        for storage_pool in $(lxc query "/1.0/storage-pools?recursion=1" | jq .[].name -r); do
            # Delete the storage volumes.
            for volume in $(lxc query "/1.0/storage-pools/${storage_pool}/volumes/custom?recursion=1" | jq .[].name -r); do
                echo "==> Deleting storage volume ${volume} on ${storage_pool}"
                timeout -k 20 20 lxc storage volume delete "${storage_pool}" "${volume}" --force-local || true
            done

            # Delete the storage buckets.
            for bucket in $(lxc query "/1.0/storage-pools/${storage_pool}/buckets?recursion=1" | jq .[].name -r); do
                echo "==> Deleting storage bucket ${bucket} on ${storage_pool}"
                timeout -k 20 20 lxc storage bucket delete "${storage_pool}" "${bucket}" --force-local || true
            done

            ## Delete the storage pool.
            timeout -k 20 20 lxc storage delete "${storage_pool}" --force-local || true
        done

        echo "==> Checking for locked DB tables"
        for table in $(echo .tables | sqlite3 "${daemon_dir}/local.db"); do
            echo "SELECT * FROM ${table};" | sqlite3 "${daemon_dir}/local.db" >/dev/null
        done

        # Kill the daemon
        timeout -k 30 30 lxd shutdown || kill -9 "${daemon_pid}" 2>/dev/null || true

        sleep 2

        # Cleanup shmounts (needed due to the forceful kill)
        find "${daemon_dir}" -name shmounts -exec "umount" "-l" "{}" \; >/dev/null 2>&1 || true
        find "${daemon_dir}" -name devlxd -exec "umount" "-l" "{}" \; >/dev/null 2>&1 || true

        check_leftovers="true"
    fi

    if [ -n "${LXD_LOGS:-}" ]; then
        echo "==> Copying the logs"
        mkdir -p "${LXD_LOGS}/${daemon_pid}"
        cp -R "${daemon_dir}/logs/" "${LXD_LOGS}/${daemon_pid}/"
        cp "${daemon_dir}/lxd.log" "${LXD_LOGS}/${daemon_pid}/"
    fi

    if [ "${check_leftovers}" = "true" ]; then
        echo "==> Checking for leftover files"
        rm -f "${daemon_dir}/containers/lxc-monitord.log"

        # Support AppArmor policy cache directory
        if apparmor_parser --help | grep -q -- '--print-cache.dir'; then
          apparmor_cache_dir="$(apparmor_parser -L "${daemon_dir}"/security/apparmor/cache --print-cache-dir)"
        else
          apparmor_cache_dir="${daemon_dir}/security/apparmor/cache"
        fi
        rm -f "${apparmor_cache_dir}/.features"
        check_empty "${daemon_dir}/containers/"
        check_empty "${daemon_dir}/devices/"
        check_empty "${daemon_dir}/images/"
        # FIXME: Once container logging rework is done, uncomment
        # check_empty "${daemon_dir}/logs/"
        check_empty "${apparmor_cache_dir}"
        check_empty "${daemon_dir}/security/apparmor/profiles/"
        check_empty "${daemon_dir}/security/seccomp/"
        check_empty "${daemon_dir}/shmounts/"
        check_empty "${daemon_dir}/snapshots/"

        echo "==> Checking for leftover DB entries"
        check_empty_table "${daemon_dir}/database/global/db.bin" "instances"
        check_empty_table "${daemon_dir}/database/global/db.bin" "instances_config"
        check_empty_table "${daemon_dir}/database/global/db.bin" "instances_devices"
        check_empty_table "${daemon_dir}/database/global/db.bin" "instances_devices_config"
        check_empty_table "${daemon_dir}/database/global/db.bin" "instances_profiles"
        check_empty_table "${daemon_dir}/database/global/db.bin" "images"
        check_empty_table "${daemon_dir}/database/global/db.bin" "images_aliases"
        check_empty_table "${daemon_dir}/database/global/db.bin" "images_properties"
        check_empty_table "${daemon_dir}/database/global/db.bin" "images_source"
        check_empty_table "${daemon_dir}/database/global/db.bin" "images_nodes"
        check_empty_table "${daemon_dir}/database/global/db.bin" "networks"
        check_empty_table "${daemon_dir}/database/global/db.bin" "networks_config"
        check_empty_table "${daemon_dir}/database/global/db.bin" "profiles"
        check_empty_table "${daemon_dir}/database/global/db.bin" "profiles_config"
        check_empty_table "${daemon_dir}/database/global/db.bin" "profiles_devices"
        check_empty_table "${daemon_dir}/database/global/db.bin" "profiles_devices_config"
        check_empty_table "${daemon_dir}/database/global/db.bin" "storage_pools"
        check_empty_table "${daemon_dir}/database/global/db.bin" "storage_pools_nodes"
        check_empty_table "${daemon_dir}/database/global/db.bin" "storage_pools_config"
        check_empty_table "${daemon_dir}/database/global/db.bin" "storage_volumes"
        check_empty_table "${daemon_dir}/database/global/db.bin" "storage_volumes_config"
    fi

    # teardown storage
    "$lxd_backend"_teardown "${daemon_dir}"

    # Wipe the daemon directory
    wipe "${daemon_dir}"

    # Remove the daemon from the list
    sed "\\|^${daemon_dir}|d" -i "${TEST_DIR}/daemons"
}

shutdown_lxd() {
    # LXD_DIR is local here because since $(lxc) is actually a function, it
    # overwrites the environment and we would lose LXD_DIR's value otherwise.

    # shellcheck disable=2039,3043
    local LXD_DIR

    daemon_dir=${1}
    # shellcheck disable=2034
    LXD_DIR=${daemon_dir}
    daemon_pid=$(cat "${daemon_dir}/lxd.pid")
    echo "==> Shutting down LXD at ${daemon_dir} (${daemon_pid})"

    # Shutting down the daemon
    lxd shutdown || kill -9 "${daemon_pid}" 2>/dev/null || true

    # Wait for any cleanup activity that might be happening right
    # after the websocket is closed.
    sleep 0.5
}

wait_for() {
    # shellcheck disable=SC2039,3043
    local addr op

    addr=${1}
    shift
    op=$("$@" | jq -r .operation)
    my_curl "https://${addr}${op}/wait"
}

wipe() {
    if command -v btrfs >/dev/null 2>&1; then
        rm -Rf "${1}" 2>/dev/null || true
        if [ -d "${1}" ]; then
            find "${1}" | tac | xargs btrfs subvolume delete >/dev/null 2>&1 || true
        fi
    fi

    # shellcheck disable=SC2039,3043
    local pid
    # shellcheck disable=SC2009
    ps aux | grep lxc-monitord | grep "${1}" | awk '{print $2}' | while read -r pid; do
        kill -9 "${pid}" || true
    done

    if mountpoint -q "${1}"; then
        umount -l "${1}"
    fi

    rm -Rf "${1}"
}

# Kill and cleanup LXD instances and related resources
cleanup_lxds() {
    # shellcheck disable=SC2039,3043
    local test_dir daemon_dir
    test_dir="$1"

    # Kill all LXD instances
    while read -r daemon_dir; do
        kill_lxd "${daemon_dir}"
    done < "${test_dir}/daemons"

    # Cleanup leftover networks
    # shellcheck disable=SC2009
    ps aux | grep "interface=lxdt$$ " | grep -v grep | awk '{print $2}' | while read -r line; do
        kill -9 "${line}"
    done
    if [ -e "/sys/class/net/lxdt$$" ]; then
        ip link del lxdt$$
    fi

    # Cleanup clustering networking, if any
    teardown_clustering_netns
    teardown_clustering_bridge

    # Wipe the test environment
    wipe "$test_dir"

    umount_loops "$test_dir"
}
