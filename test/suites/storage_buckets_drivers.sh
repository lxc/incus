test_storage_buckets_drivers() {
    poolDriverList="dir btrfs lvm lvm-thin zfs"

    incus project create test-buckets -c features.images=false
    incus project switch test-buckets

    buckets_addr="127.0.0.1:$(local_tcp_port)"
    incus config set core.storage_buckets_address "${buckets_addr}"

    poolName="bucketpool$$"

    for poolDriver in ${poolDriverList}; do
        if ! storage_backend_available "${poolDriver%-thin}"; then
            echo "==> Skipping driver ${poolDriver} (not available)"
            continue
        fi

        echo "==> Create storage pool using driver ${poolDriver}"
        if [ "${poolDriver}" = "dir" ]; then
            incus storage create "${poolName}" "${poolDriver}" volume.size=5GB
        elif [ "${poolDriver}" = "lvm" ]; then
            incus storage create "${poolName}" "${poolDriver}" size=40GiB lvm.use_thinpool=false volume.size=5GB
        elif [ "${poolDriver}" = "lvm-thin" ]; then
            incus storage create "${poolName}" lvm size=20GiB volume.size=5GB
        else
            incus storage create "${poolName}" "${poolDriver}" size=20GB volume.size=5GB
        fi

        incus storage show "${poolName}"

        echo "==> Creating buckets"
        incus storage bucket create "${poolName}" bucket1

        echo "==> Deleting buckets and storage pool"
        incus storage bucket delete "${poolName}" bucket1
        incus storage rm "${poolName}"
    done

    incus config unset core.storage_buckets_address

    incus project switch default
    incus project delete test-buckets
}
