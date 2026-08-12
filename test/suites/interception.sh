test_interception() {
    if [ -n "${INCUS_OFFLINE:-}" ]; then
        echo "==> SKIP: External connectivity needed to pull test image"
        export TEST_UNMET_REQUIREMENT="external connectivity needed to pull test image"
        return
    fi

    poolName=$(incus profile device get default root pool)

    incus network create incusbr0
    incus init images:debian/13 c1 -s "${poolName}"
    incus config device add c1 eth0 nic network=incusbr0 name=eth0
    incus start c1
    sleep 10
    incus exec c1 -- apt-get install --no-install-recommends --yes attr e2fsprogs fuse2fs fuse

    echo "==> Testing setxattr interception"
    incus exec c1 -- touch xattr-test
    ! incus exec c1 -- setfattr -n trusted.overlay.opaque -v y xattr-test || false
    incus config set c1 security.syscalls.intercept.setxattr true
    incus restart c1 -f
    incus exec c1 -- setfattr -n trusted.overlay.opaque -v y xattr-test
    [ "$(getfattr --only-values --absolute-names -n trusted.overlay.opaque "${INCUS_DIR}/containers/c1/rootfs/root/xattr-test")" = "y" ]

    echo "==> Testing mknod interception"
    ! incus exec c1 -- mknod mknod-test c 1 3 || false
    incus config set c1 security.syscalls.intercept.mknod true
    incus restart c1 -f

    ## Relative path
    incus exec c1 -- mknod mknod-test c 1 3

    ## Absolute path on tmpfs
    incus exec c1 -- mknod /dev/mknod-test c 1 3

    ## Absolute path on rootfs
    incus exec c1 -- mknod /root/mknod-test1 c 1 3

    echo "==> Testing bpf interception"
    incus config set c1 security.syscalls.intercept.bpf=true security.syscalls.intercept.bpf.devices=true
    incus restart c1 -f

    echo "==> Testing mount interception"
    configure_loop_device loop_file_1 loop_device_1

    # Create a device node in the allowed device source path.
    # shellcheck disable=SC2154
    mknod "${TEST_DIR}/dev/loopdisk" b "$(stat -c '%Hr' "${loop_device_1}")" "$(stat -c '%Lr' "${loop_device_1}")"

    incus config device add c1 loop unix-block source="${TEST_DIR}/dev/loopdisk" path=/dev/sda
    incus exec c1 -- mkfs.ext4 /dev/sda
    ! incus exec c1 -- mount /dev/sda /mnt || false
    incus config set c1 security.syscalls.intercept.mount=true

    incus config set c1 security.syscalls.intercept.mount.allowed=ext4
    incus restart c1 -f
    incus exec c1 -- mount /dev/sda /mnt
    [ "$(incus exec c1 -- stat --format=%u:%g /mnt)" = "65534:65534" ]
    incus exec c1 -- umount /mnt

    incus config set c1 security.syscalls.intercept.mount.shift=true
    incus exec c1 -- mount /dev/sda /mnt
    [ "$(incus exec c1 -- stat --format=%u:%g /mnt)" = "0:0" ]
    incus exec c1 -- umount /mnt

    incus config unset c1 security.syscalls.intercept.mount.allowed
    incus config set c1 security.syscalls.intercept.mount.fuse=ext4=fuse2fs
    incus restart c1 -f

    incus exec c1 -- mount /dev/sda /mnt
    [ "$(incus exec c1 -- stat --format=%u:%g /mnt)" = "0:0" ]
    incus exec c1 -- umount /mnt

    incus delete -f c1
    rm "${TEST_DIR}/dev/loopdisk"
    # shellcheck disable=SC2154
    deconfigure_loop_device "${loop_file_1}" "${loop_device_1}"
    incus network delete incusbr0
}
