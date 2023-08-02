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
    lxc storage create "incustest-$(basename "${INCUS_DIR}")-pool1" btrfs
    lxc storage create "incustest-$(basename "${INCUS_DIR}")-pool2" btrfs

    # Set default storage pool for image import.
    lxc profile device add default root disk path="/" pool="incustest-$(basename "${INCUS_DIR}")-pool1"

    # Import image into default storage pool.
    ensure_import_testimage

    # Create first container in pool1 with subvolumes.
    lxc launch testimage c1pool1 -s "incustest-$(basename "${INCUS_DIR}")-pool1"

    # Snapshot without any subvolumes (to test missing subvolume parent origin is handled).
    lxc snapshot c1pool1 snap0

    # Create some subvolumes and populate with test files. Mark the intermedia subvolume as read only.
    OWNER="$(stat -c %u:%g "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs")"
    btrfs subvolume create "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a"
    chown "${OWNER}" "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a"
    btrfs subvolume create "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b"
    chown "${OWNER}" "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b"
    btrfs subvolume create "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b/c"
    chown "${OWNER}" "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b/c"
    lxc exec c1pool1 -- touch /a/a1.txt
    lxc exec c1pool1 -- touch /a/b/b1.txt
    lxc exec c1pool1 -- touch /a/b/c/c1.txt
    btrfs property set "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b" ro true

    # Snapshot again now subvolumes exist.
    lxc snapshot c1pool1 snap1

    # Add some more files to subvolumes
    btrfs property set "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b" ro false
    lxc exec c1pool1 -- touch /a/a2.txt
    lxc exec c1pool1 -- touch /a/b/b2.txt
    lxc exec c1pool1 -- touch /a/b/c/c2.txt
    btrfs property set "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b" ro true
    lxc snapshot c1pool1 snap2

    # Copy container to other BTRFS storage pool (will use migration subsystem).
    lxc copy c1pool1 c1pool2 -s "incustest-$(basename "${INCUS_DIR}")-pool2"
    lxc start c1pool2
    lxc exec c1pool2 -- stat /a/a2.txt
    lxc exec c1pool2 -- stat /a/b/b2.txt
    lxc exec c1pool2 -- stat /a/b/c/c2.txt

    # Test readonly property has been propagated.
    lxc exec c1pool2 -- touch /a/w.txt
    ! lxc exec c1pool2 -- touch /a/b/w.txt || false
    lxc exec c1pool2 -- touch /a/b/c/w.txt

    # Restore copied snapshot and check it is correct.
    lxc restore c1pool2 snap1
    lxc exec c1pool2 -- stat /a/a1.txt
    lxc exec c1pool2 -- stat /a/b/b1.txt
    lxc exec c1pool2 -- stat /a/b/c/c1.txt
    ! lxc exec c1pool2 -- stat /a/a2.txt || false
    ! lxc exec c1pool2 -- stat /a/b/b2.txt || false
    ! lxc exec c1pool2 -- stat /a/b/c/c2.txt || false

    # Test readonly property has been propagated in snapshot.
    lxc exec c1pool2 -- touch /a/w.txt
    ! lxc exec c1pool2 -- touch /a/b/w.txt || false
    lxc exec c1pool2 -- touch /a/b/c/w.txt
    lxc delete -f c1pool2

    # Copy snapshot to as a new instance on different pool.
    lxc copy c1pool1/snap1 c1pool2 -s "incustest-$(basename "${INCUS_DIR}")-pool2"
    lxc start c1pool2
    lxc exec c1pool2 -- stat /a/a1.txt
    lxc exec c1pool2 -- stat /a/b/b1.txt
    lxc exec c1pool2 -- stat /a/b/c/c1.txt
    ! lxc exec c1pool2 -- stat /a/a2.txt || false
    ! lxc exec c1pool2 -- stat /a/b/b2.txt || false
    ! lxc exec c1pool2 -- stat /a/b/c/c2.txt || false

    # Test readonly property has been propagated in snapshot.
    lxc exec c1pool2 -- touch /a/w.txt
    ! lxc exec c1pool2 -- touch /a/b/w.txt || false
    lxc exec c1pool2 -- touch /a/b/c/w.txt
    lxc delete -f c1pool2

    # Delete /a in c1pool1 and restore snap 1.
    btrfs property set "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b" ro false
    btrfs subvol delete "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b/c"
    btrfs subvol delete "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b"
    btrfs subvol delete "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a"
    lxc restore c1pool1 snap1
    lxc exec c1pool1 -- stat /a/a1.txt
    lxc exec c1pool1 -- stat /a/b/b1.txt
    lxc exec c1pool1 -- stat /a/b/c/c1.txt
    lxc exec c1pool1 -- touch /a/w.txt
    ! lxc exec c1pool1 -- touch /a/b/w.txt || false
    lxc exec c1pool1 -- touch /a/b/c/w.txt

    # Copy c1pool1 to same pool.
    lxc copy c1pool1 c2pool1
    lxc start c2pool1
    lxc exec c2pool1 -- stat /a/a1.txt
    lxc exec c2pool1 -- stat /a/b/b1.txt
    lxc exec c2pool1 -- stat /a/b/c/c1.txt

    # Test readonly property has been propagated.
    lxc exec c2pool1 -- touch /a/w.txt
    ! lxc exec c2pool1 -- touch /a/b/w.txt || false
    lxc exec c2pool1 -- touch /a/b/c/w.txt
    lxc delete -f c2pool1

    # Copy snap2 of c1pool1 to same pool as separate instance.
    lxc copy c1pool1/snap2 c2pool1
    lxc start c2pool1
    lxc exec c2pool1 -- stat /a/a2.txt
    lxc exec c2pool1 -- stat /a/b/b2.txt
    lxc exec c2pool1 -- stat /a/b/c/c2.txt

    # Test readonly property has been propagated.
    lxc exec c2pool1 -- touch /a/w.txt
    ! lxc exec c2pool1 -- touch /a/b/w.txt || false
    lxc exec c2pool1 -- touch /a/b/c/w.txt
    lxc delete -f c2pool1

    # Backup c1pool1 and test subvolumes can be restored.
    lxc export c1pool1 "${INCUS_DIR}/c1pool1.tar.gz" --optimized-storage
    lxc delete -f c1pool1
    lxc import "${INCUS_DIR}/c1pool1.tar.gz"
    lxc start c1pool1
    lxc exec c1pool1 -- stat /a/a1.txt
    lxc exec c1pool1 -- stat /a/b/b1.txt
    lxc exec c1pool1 -- stat /a/b/c/c1.txt

    # Test readonly property has been propagated.
    lxc exec c1pool1 -- touch /a/w.txt
    ! lxc exec c1pool1 -- touch /a/b/w.txt || false
    lxc exec c1pool1 -- touch /a/b/c/w.txt

    lxc delete -f c1pool1
    lxc profile device remove default root
    lxc storage delete "incustest-$(basename "${INCUS_DIR}")-pool1"
    lxc storage delete "incustest-$(basename "${INCUS_DIR}")-pool2"
  )

  # shellcheck disable=SC2031
  kill_incus "${INCUS_STORAGE_DIR}"
}
