test_storage_driver_btrfs() {
  # shellcheck disable=2039,3043
  local INCUS_STORAGE_DIR incus_backend

  incus_backend=$(storage_backend "$INCUS_DIR")
  if [ "$incus_backend" != "btrfs" ]; then
    return
  fi

  INCUS_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  chmod +x "${INCUS_STORAGE_DIR}"
  spawn_incus "${INCUS_STORAGE_DIR}" false

  (
    set -e
    # shellcheck disable=2030
    INCUS_DIR="${INCUS_STORAGE_DIR}"

    # shellcheck disable=SC1009
    incus storage create "incustest-$(basename "${INCUS_DIR}")-pool1" btrfs
    incus storage create "incustest-$(basename "${INCUS_DIR}")-pool2" btrfs

    # Set default storage pool for image import.
    incus profile device add default root disk path="/" pool="incustest-$(basename "${INCUS_DIR}")-pool1"

    # Import image into default storage pool.
    ensure_import_testimage

    # Create first container in pool1 with subvolumes.
    incus launch testimage c1pool1 -s "incustest-$(basename "${INCUS_DIR}")-pool1"

    # Snapshot without any subvolumes (to test missing subvolume parent origin is handled).
    incus snapshot create c1pool1 snap0

    # Create some subvolumes and populate with test files. Mark the intermedia subvolume as read only.
    OWNER="$(stat -c %u:%g "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs")"
    btrfs subvolume create "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a"
    chown "${OWNER}" "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a"
    btrfs subvolume create "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b"
    chown "${OWNER}" "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b"
    btrfs subvolume create "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b/c"
    chown "${OWNER}" "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b/c"
    incus exec c1pool1 -- touch /a/a1.txt
    incus exec c1pool1 -- touch /a/b/b1.txt
    incus exec c1pool1 -- touch /a/b/c/c1.txt
    btrfs property set "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b" ro true

    # Snapshot again now subvolumes exist.
    incus snapshot create c1pool1 snap1

    # Add some more files to subvolumes
    btrfs property set "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b" ro false
    incus exec c1pool1 -- touch /a/a2.txt
    incus exec c1pool1 -- touch /a/b/b2.txt
    incus exec c1pool1 -- touch /a/b/c/c2.txt
    btrfs property set "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b" ro true
    incus snapshot create c1pool1 snap2

    # Copy container to other BTRFS storage pool (will use migration subsystem).
    incus copy c1pool1 c1pool2 -s "incustest-$(basename "${INCUS_DIR}")-pool2"
    incus start c1pool2
    incus exec c1pool2 -- stat /a/a2.txt
    incus exec c1pool2 -- stat /a/b/b2.txt
    incus exec c1pool2 -- stat /a/b/c/c2.txt

    # Test readonly property has been propagated.
    incus exec c1pool2 -- touch /a/w.txt
    ! incus exec c1pool2 -- touch /a/b/w.txt || false
    incus exec c1pool2 -- touch /a/b/c/w.txt

    # Restore copied snapshot and check it is correct.
    incus snapshot restore c1pool2 snap1
    incus exec c1pool2 -- stat /a/a1.txt
    incus exec c1pool2 -- stat /a/b/b1.txt
    incus exec c1pool2 -- stat /a/b/c/c1.txt
    ! incus exec c1pool2 -- stat /a/a2.txt || false
    ! incus exec c1pool2 -- stat /a/b/b2.txt || false
    ! incus exec c1pool2 -- stat /a/b/c/c2.txt || false

    # Test readonly property has been propagated in snapshot.
    incus exec c1pool2 -- touch /a/w.txt
    ! incus exec c1pool2 -- touch /a/b/w.txt || false
    incus exec c1pool2 -- touch /a/b/c/w.txt
    incus delete -f c1pool2

    # Copy snapshot to as a new instance on different pool.
    incus copy c1pool1/snap1 c1pool2 -s "incustest-$(basename "${INCUS_DIR}")-pool2"
    incus start c1pool2
    incus exec c1pool2 -- stat /a/a1.txt
    incus exec c1pool2 -- stat /a/b/b1.txt
    incus exec c1pool2 -- stat /a/b/c/c1.txt
    ! incus exec c1pool2 -- stat /a/a2.txt || false
    ! incus exec c1pool2 -- stat /a/b/b2.txt || false
    ! incus exec c1pool2 -- stat /a/b/c/c2.txt || false

    # Test readonly property has been propagated in snapshot.
    incus exec c1pool2 -- touch /a/w.txt
    ! incus exec c1pool2 -- touch /a/b/w.txt || false
    incus exec c1pool2 -- touch /a/b/c/w.txt
    incus delete -f c1pool2

    # Delete /a in c1pool1 and restore snap 1.
    btrfs property set "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b" ro false
    btrfs subvol delete "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b/c"
    btrfs subvol delete "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b"
    btrfs subvol delete "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a"
    incus snapshot restore c1pool1 snap1
    incus exec c1pool1 -- stat /a/a1.txt
    incus exec c1pool1 -- stat /a/b/b1.txt
    incus exec c1pool1 -- stat /a/b/c/c1.txt
    incus exec c1pool1 -- touch /a/w.txt
    ! incus exec c1pool1 -- touch /a/b/w.txt || false
    incus exec c1pool1 -- touch /a/b/c/w.txt

    # Copy c1pool1 to same pool.
    incus copy c1pool1 c2pool1
    incus start c2pool1
    incus exec c2pool1 -- stat /a/a1.txt
    incus exec c2pool1 -- stat /a/b/b1.txt
    incus exec c2pool1 -- stat /a/b/c/c1.txt

    # Test readonly property has been propagated.
    incus exec c2pool1 -- touch /a/w.txt
    ! incus exec c2pool1 -- touch /a/b/w.txt || false
    incus exec c2pool1 -- touch /a/b/c/w.txt
    incus delete -f c2pool1

    # Copy snap2 of c1pool1 to same pool as separate instance.
    incus copy c1pool1/snap2 c2pool1
    incus start c2pool1
    incus exec c2pool1 -- stat /a/a2.txt
    incus exec c2pool1 -- stat /a/b/b2.txt
    incus exec c2pool1 -- stat /a/b/c/c2.txt

    # Test readonly property has been propagated.
    incus exec c2pool1 -- touch /a/w.txt
    ! incus exec c2pool1 -- touch /a/b/w.txt || false
    incus exec c2pool1 -- touch /a/b/c/w.txt
    incus delete -f c2pool1

    # Backup c1pool1 and test subvolumes can be restored.
    incus export c1pool1 "${INCUS_DIR}/c1pool1.tar.gz" --optimized-storage
    incus delete -f c1pool1
    incus import "${INCUS_DIR}/c1pool1.tar.gz"
    incus start c1pool1
    incus exec c1pool1 -- stat /a/a1.txt
    incus exec c1pool1 -- stat /a/b/b1.txt
    incus exec c1pool1 -- stat /a/b/c/c1.txt

    # Test readonly property has been propagated.
    incus exec c1pool1 -- touch /a/w.txt
    ! incus exec c1pool1 -- touch /a/b/w.txt || false
    incus exec c1pool1 -- touch /a/b/c/w.txt

    incus delete -f c1pool1
    incus profile device remove default root
    incus storage delete "incustest-$(basename "${INCUS_DIR}")-pool1"
    incus storage delete "incustest-$(basename "${INCUS_DIR}")-pool2"

    # Test creating storage pool from exiting btrfs subvolume
    truncate -s 200M testpool.img
    mkfs.btrfs -f testpool.img
    basepath="$(pwd)/mnt"
    mkdir -p "${basepath}"
    mount testpool.img "${basepath}"
    btrfs subvolume create "${basepath}/foo"
    btrfs subvolume create "${basepath}/foo/bar"

    # This should fail as the source itself has subvolumes.
    ! incus storage create "incustest-$(basename "${INCUS_DIR}")-pool1" btrfs source="${basepath}/foo" || false

    # This should work as the provided subvolume is empty.
    btrfs subvolume delete "${basepath}/foo/bar"
    incus storage create "incustest-$(basename "${INCUS_DIR}")-pool1" btrfs source="${basepath}/foo"
    incus storage delete "incustest-$(basename "${INCUS_DIR}")-pool1"

    sleep 1

    umount "${basepath}"
    rmdir "${basepath}"
    rm -f testpool.img
  )

  # shellcheck disable=SC2031
  kill_incus "${INCUS_STORAGE_DIR}"
}
