test_storage_disks_vm() {
    if [ -n "${INCUS_OFFLINE:-}" ]; then
        echo "==> SKIP: External connectivity needed to pull test image"
        export TEST_UNMET_REQUIREMENT="external connectivity needed to pull test image"
        return
    fi

    if [ ! -e /dev/kvm ] || ! command -v "qemu-system-$(uname -m)" > /dev/null 2>&1; then
        echo "==> SKIP: QEMU and KVM needed to run virtual machines"
        export TEST_UNMET_REQUIREMENT="QEMU and KVM needed to run virtual machines"
        return
    fi

    if ! storage_backend_available zfs; then
        echo "==> SKIP: ZFS not available"
        export TEST_UNMET_REQUIREMENT="ZFS not available"
        return
    fi

    echo "==> Setup share directory"
    # Create directory for use as basis for restricted disk source tests.
    testRoot="${TEST_DIR}/restricted"
    mkdir "${testRoot}"

    # Create directory for use as allowed disk source path prefix in project.
    mkdir "${testRoot}/allowed1"
    mkdir "${testRoot}/allowed2"
    mkdir "${testRoot}/allowed1/foo1"
    mkdir "${testRoot}/allowed1/foo2"
    chown 1000:1000 "${testRoot}/allowed1/foo1"
    chown 1001:1001 "${testRoot}/allowed1/foo2"
    mkdir "${testRoot}/not-allowed1"
    ln -s "${testRoot}/not-allowed1" "${testRoot}/allowed1/not-allowed1"
    ln -s "${testRoot}/allowed2" "${testRoot}/allowed1/not-allowed2"
    (cd "${testRoot}/allowed1" || false; ln -s foo1 foolink)

    poolName="vmpool$$"

    incus network create incusbr0
    incus storage create "${poolName}" zfs size=20GB volume.size=5GB

    # Create project with restricted disk source path.
    incus project create restricted \
        -c features.images=false \
        -c restricted=true \
        -c restricted.devices.disk=allow \
        -c restricted.devices.disk.paths="${testRoot}/allowed1,${testRoot}/allowed2"
    incus project switch restricted
    incus profile device add default root disk path="/" pool="${poolName}"
    incus profile device add default eth0 nic network=incusbr0
    incus profile show default

    # Create instance and add check relative source paths are not allowed.
    incus init images:debian/13 v1 --vm
    ! incus config device add v1 d1 disk source=foo path=/mnt || false

    # Check adding a disk with a source path above the restricted parent source path isn't allowed.
    ! incus config device add v1 d1 disk source="${testRoot}/not-allowed1" path=/mnt || false

    # Check adding a disk with a source path that is a symlink above the restricted parent source path isn't allowed
    # at start time (check that openat2 restrictions are doing their job).
    incus config device add v1 d1 disk source="${testRoot}/allowed1/not-allowed1" path=/mnt
    ! incus start v1 || false

    # Check some rudimentary work arounds to allowed path checks don't work.
    ! incus config device set v1 d1 source="${testRoot}/../not-allowed1" || false

    # Check adding a disk from a restricted source path cannot use shifting at start time. This is not safe as we
    # cannot prevent creation of files with setuid, which would allow a root executable to be created.
    incus config device set v1 d1 source="${testRoot}/allowed1" shift=true
    ! incus start v1 || false

    # Check adding a disk with a source path that is allowed is allowed.
    incus config device set v1 d1 source="${testRoot}/allowed1" shift=false
    incus start v1
    incus wait v1 agent --timeout=90 --interval=1
    incus exec v1 --project restricted -- ls /mnt/foo1
    incus stop -f v1

    # Check adding a disk with a source path that is allowed that symlinks to another allowed source path isn't
    # allowed at start time.
    incus config device set v1 d1 source="${testRoot}/allowed1/not-allowed2"
    ! incus start v1 || false

    # Check relative symlink inside allowed parent path is allowed.
    incus config device set v1 d1 source="${testRoot}/allowed1/foolink" path=/mnt/foolink
    incus start v1
    incus wait v1 agent --timeout=90 --interval=1
    [ "$(incus exec v1 --project restricted -- stat /mnt/foolink -c '%u:%g')" = "1000:1000" ] || false
    incus stop -f v1

    # Check usage of raw.idmap is restricted.
    ! incus config set v1 raw.idmap="both 1000 1000" || false

    # Allow specific raw.idmap host UID/GID.
    incus project set restricted restricted.idmap.uid=1000
    ! incus config set v1 raw.idmap="both 1000 1000" || false
    ! incus config set v1 raw.idmap="gid 1000 1000" || false
    incus config set v1 raw.idmap="uid 1000 1000"

    incus project set restricted restricted.idmap.gid=1000
    incus config set v1 raw.idmap="gid 1000 1000"
    incus config set v1 raw.idmap="both 1000 1000"

    # Check conflict detection works.
    ! incus project unset restricted restricted.idmap.uid || false
    ! incus project unset restricted restricted.idmap.gid || false

    # Check single entry raw.idmap has taken effect on disk share.
    incus config device set v1 d1 source="${testRoot}/allowed1" path=/mnt
    incus start v1 || (incus info --show-log v1 ; false)
    incus wait v1 agent --timeout=90 --interval=1
    [ "$(incus exec v1 --project restricted -- stat /mnt/foo1 -c '%u:%g')" = "1000:1000" ] || false
    [ "$(incus exec v1 --project restricted -- stat /mnt/foo2 -c '%u:%g')" = "1001:1001" ] || false
    incus exec v1 --project restricted -- chown 1000:1000 /mnt/foo2
    ! incus exec v1 --project restricted -- chown 1001:1001 /mnt/foo2 || false

    # Check burst I/O limits can be applied to and cleared from a running VM.
    incus config device override v1 root limits.read=10MiB limits.read.burst=20MiB limits.read.burst.length=10s
    incus config device set v1 root limits.read= limits.read.burst= limits.read.burst.length=

    # Check security.secureboot setting is applied to running VM at next start up.
    incus exec v1 -- mokutil --sb-state | grep -Fx "SecureBoot enabled"
    incus profile set default security.secureboot=false
    incus restart v1
    incus wait v1 agent --timeout=90 --interval=1
    incus exec v1 -- mokutil --sb-state | grep -Fx "SecureBoot disabled"

    echo "==> Cleanup"
    incus delete -f v1
    incus project switch default
    incus project delete restricted
    incus storage delete "${poolName}"
    incus network delete incusbr0

    rm "${testRoot}/allowed1/not-allowed1"
    rm "${testRoot}/allowed1/not-allowed2"
    rmdir "${testRoot}/allowed1/foo1"
    rmdir "${testRoot}/allowed1/foo2"
    rm "${testRoot}/allowed1/foolink"
    rmdir "${testRoot}/allowed1"
    rmdir "${testRoot}/allowed2"
    rmdir "${testRoot}/not-allowed1"
    rmdir "${testRoot}"
}
