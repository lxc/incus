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
    inc storage create "incustest-$(basename "${INCUS_DIR}")-pool1" btrfs
    inc storage create "incustest-$(basename "${INCUS_DIR}")-pool2" btrfs

    # Set default storage pool for image import.
    inc profile device add default root disk path="/" pool="incustest-$(basename "${INCUS_DIR}")-pool1"

    # Import image into default storage pool.
    ensure_import_testimage

    # Create first container in pool1 with subvolumes.
    inc launch testimage c1pool1 -s "incustest-$(basename "${INCUS_DIR}")-pool1"

    # Snapshot without any subvolumes (to test missing subvolume parent origin is handled).
    inc snapshot c1pool1 snap0

    # Create some subvolumes and populate with test files. Mark the intermedia subvolume as read only.
    OWNER="$(stat -c %u:%g "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs")"
    btrfs subvolume create "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a"
    chown "${OWNER}" "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a"
    btrfs subvolume create "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b"
    chown "${OWNER}" "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b"
    btrfs subvolume create "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b/c"
    chown "${OWNER}" "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b/c"
    inc exec c1pool1 -- touch /a/a1.txt
    inc exec c1pool1 -- touch /a/b/b1.txt
    inc exec c1pool1 -- touch /a/b/c/c1.txt
    btrfs property set "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b" ro true

    # Snapshot again now subvolumes exist.
    inc snapshot c1pool1 snap1

    # Add some more files to subvolumes
    btrfs property set "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b" ro false
    inc exec c1pool1 -- touch /a/a2.txt
    inc exec c1pool1 -- touch /a/b/b2.txt
    inc exec c1pool1 -- touch /a/b/c/c2.txt
    btrfs property set "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b" ro true
    inc snapshot c1pool1 snap2

    # Copy container to other BTRFS storage pool (will use migration subsystem).
    inc copy c1pool1 c1pool2 -s "incustest-$(basename "${INCUS_DIR}")-pool2"
    inc start c1pool2
    inc exec c1pool2 -- stat /a/a2.txt
    inc exec c1pool2 -- stat /a/b/b2.txt
    inc exec c1pool2 -- stat /a/b/c/c2.txt

    # Test readonly property has been propagated.
    inc exec c1pool2 -- touch /a/w.txt
    ! inc exec c1pool2 -- touch /a/b/w.txt || false
    inc exec c1pool2 -- touch /a/b/c/w.txt

    # Restore copied snapshot and check it is correct.
    inc restore c1pool2 snap1
    inc exec c1pool2 -- stat /a/a1.txt
    inc exec c1pool2 -- stat /a/b/b1.txt
    inc exec c1pool2 -- stat /a/b/c/c1.txt
    ! inc exec c1pool2 -- stat /a/a2.txt || false
    ! inc exec c1pool2 -- stat /a/b/b2.txt || false
    ! inc exec c1pool2 -- stat /a/b/c/c2.txt || false

    # Test readonly property has been propagated in snapshot.
    inc exec c1pool2 -- touch /a/w.txt
    ! inc exec c1pool2 -- touch /a/b/w.txt || false
    inc exec c1pool2 -- touch /a/b/c/w.txt
    inc delete -f c1pool2

    # Copy snapshot to as a new instance on different pool.
    inc copy c1pool1/snap1 c1pool2 -s "incustest-$(basename "${INCUS_DIR}")-pool2"
    inc start c1pool2
    inc exec c1pool2 -- stat /a/a1.txt
    inc exec c1pool2 -- stat /a/b/b1.txt
    inc exec c1pool2 -- stat /a/b/c/c1.txt
    ! inc exec c1pool2 -- stat /a/a2.txt || false
    ! inc exec c1pool2 -- stat /a/b/b2.txt || false
    ! inc exec c1pool2 -- stat /a/b/c/c2.txt || false

    # Test readonly property has been propagated in snapshot.
    inc exec c1pool2 -- touch /a/w.txt
    ! inc exec c1pool2 -- touch /a/b/w.txt || false
    inc exec c1pool2 -- touch /a/b/c/w.txt
    inc delete -f c1pool2

    # Delete /a in c1pool1 and restore snap 1.
    btrfs property set "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b" ro false
    btrfs subvol delete "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b/c"
    btrfs subvol delete "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a/b"
    btrfs subvol delete "${INCUS_DIR}/storage-pools/incustest-$(basename "${INCUS_DIR}")-pool1/containers/c1pool1/rootfs/a"
    inc restore c1pool1 snap1
    inc exec c1pool1 -- stat /a/a1.txt
    inc exec c1pool1 -- stat /a/b/b1.txt
    inc exec c1pool1 -- stat /a/b/c/c1.txt
    inc exec c1pool1 -- touch /a/w.txt
    ! inc exec c1pool1 -- touch /a/b/w.txt || false
    inc exec c1pool1 -- touch /a/b/c/w.txt

    # Copy c1pool1 to same pool.
    inc copy c1pool1 c2pool1
    inc start c2pool1
    inc exec c2pool1 -- stat /a/a1.txt
    inc exec c2pool1 -- stat /a/b/b1.txt
    inc exec c2pool1 -- stat /a/b/c/c1.txt

    # Test readonly property has been propagated.
    inc exec c2pool1 -- touch /a/w.txt
    ! inc exec c2pool1 -- touch /a/b/w.txt || false
    inc exec c2pool1 -- touch /a/b/c/w.txt
    inc delete -f c2pool1

    # Copy snap2 of c1pool1 to same pool as separate instance.
    inc copy c1pool1/snap2 c2pool1
    inc start c2pool1
    inc exec c2pool1 -- stat /a/a2.txt
    inc exec c2pool1 -- stat /a/b/b2.txt
    inc exec c2pool1 -- stat /a/b/c/c2.txt

    # Test readonly property has been propagated.
    inc exec c2pool1 -- touch /a/w.txt
    ! inc exec c2pool1 -- touch /a/b/w.txt || false
    inc exec c2pool1 -- touch /a/b/c/w.txt
    inc delete -f c2pool1

    # Backup c1pool1 and test subvolumes can be restored.
    inc export c1pool1 "${INCUS_DIR}/c1pool1.tar.gz" --optimized-storage
    inc delete -f c1pool1
    inc import "${INCUS_DIR}/c1pool1.tar.gz"
    inc start c1pool1
    inc exec c1pool1 -- stat /a/a1.txt
    inc exec c1pool1 -- stat /a/b/b1.txt
    inc exec c1pool1 -- stat /a/b/c/c1.txt

    # Test readonly property has been propagated.
    inc exec c1pool1 -- touch /a/w.txt
    ! inc exec c1pool1 -- touch /a/b/w.txt || false
    inc exec c1pool1 -- touch /a/b/c/w.txt

    inc delete -f c1pool1
    inc profile device remove default root
    inc storage delete "incustest-$(basename "${INCUS_DIR}")-pool1"
    inc storage delete "incustest-$(basename "${INCUS_DIR}")-pool2"
  )

  # shellcheck disable=SC2031
  kill_incus "${INCUS_STORAGE_DIR}"
}
