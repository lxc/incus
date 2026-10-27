test_lxc_to_incus() {
    if ! command -v "lxc-create" > /dev/null 2>&1; then
        echo "==> SKIP: Skipping lxc-to-incus as system is missing LXC"
        return
    fi

    ensure_has_localhost_remote "${INCUS_ADDR}"

    LXC_DIR="${TEST_DIR}/lxc"

    mkdir -p "${LXC_DIR}"

    incus network create lxcbr0

    # Create LXC containers
    lxc-create -P "${LXC_DIR}" -n c1 -B dir -t busybox
    lxc-start -P "${LXC_DIR}" -n c1
    lxc-attach -P "${LXC_DIR}" -n c1 -- touch /root/foo
    lxc-stop -P "${LXC_DIR}" -n c1 --kill

    lxc-create -P "${LXC_DIR}" -n c2 -B dir -t busybox
    lxc-create -P "${LXC_DIR}" -n c3 -B dir -t busybox

    # Convert single LXC container (dry run)
    lxc-to-incus --lxcpath "${LXC_DIR}" --dry-run --delete --containers c1

    # Ensure the LXC containers have not been deleted
    [ "$(lxc-ls -P "${LXC_DIR}" -1 | wc -l)" -eq "3" ]

    # Ensure no containers have been converted
    ! incus info c1 || false
    ! incus info c2 || false
    ! incus info c3 || false

    # Convert single LXC container
    lxc-to-incus --lxcpath "${LXC_DIR}" --containers c1

    # Ensure the LXC containers have not been deleted
    [ "$(lxc-ls -P "${LXC_DIR}" -1 | wc -l)" -eq 3 ]

    # Ensure only c1 has been converted
    incus info c1
    ! incus info c2 || false
    ! incus info c3 || false

    # Ensure the converted container is startable
    incus start c1
    incus exec c1 -- stat /root/foo
    incus delete -f c1

    # Convert some LXC containers
    lxc-to-incus --lxcpath "${LXC_DIR}" --delete --containers c1,c2

    # Ensure the LXC containers c1 and c2 have been deleted
    [ "$(lxc-ls -P "${LXC_DIR}" -1 | wc -l)" -eq 1 ]

    # Ensure all containers have been converted
    incus info c1
    incus info c2
    ! incus info c3 || false

    # Convert all LXC containers
    lxc-to-incus --lxcpath "${LXC_DIR}" --delete --all

    # Ensure the remaining LXC containers have been deleted
    [ "$(lxc-ls -P "${LXC_DIR}" -1 | wc -l)" -eq 0 ]

    # Ensure all containers have been converted
    incus info c1
    incus info c2
    incus info c3

    incus delete -f c1 c2 c3
    incus network delete lxcbr0
}
