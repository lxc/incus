test_container_local_cross_pool_handling() {
  ensure_import_testimage

  # shellcheck disable=2039,3043
  local INCUS_STORAGE_DIR incus_backend
  incus_backend=$(storage_backend "$INCUS_DIR")
  INCUS_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  chmod +x "${INCUS_STORAGE_DIR}"
  spawn_incus "${INCUS_STORAGE_DIR}" true

  (
    set -e
    # shellcheck disable=2030
    INCUS_DIR="${INCUS_STORAGE_DIR}"
    ensure_import_testimage

    brName="inct$$"
    incus network create "${brName}"

    if storage_backend_available "btrfs"; then
      incus storage create "incustest-$(basename "${INCUS_DIR}")-btrfs" btrfs size=1GiB
    fi

    if storage_backend_available "ceph"; then
      incus storage create "incustest-$(basename "${INCUS_DIR}")-ceph" ceph volume.size=25MiB ceph.osd.pg_num=16
    fi

    incus storage create "incustest-$(basename "${INCUS_DIR}")-dir" dir

    if storage_backend_available "lvm"; then
      incus storage create "incustest-$(basename "${INCUS_DIR}")-lvm" lvm volume.size=25MiB
    fi

    if storage_backend_available "zfs"; then
      incus storage create "incustest-$(basename "${INCUS_DIR}")-zfs" zfs size=1GiB
    fi

    for driver in "btrfs" "ceph" "dir" "lvm" "zfs"; do
      if [ "$incus_backend" = "$driver" ]; then
        pool_opts=

        if [ "$driver" = "btrfs" ] || [ "$driver" = "zfs" ]; then
          pool_opts="size=1GiB"
        fi

        if [ "$driver" = "ceph" ]; then
          pool_opts="volume.size=25MiB ceph.osd.pg_num=16"
        fi

        if [ "$driver" = "lvm" ]; then
          pool_opts="volume.size=25MiB"
        fi

        if [ -n "${pool_opts}" ]; then
          # shellcheck disable=SC2086
          incus storage create "incustest-$(basename "${INCUS_DIR}")-${driver}1" "${driver}" $pool_opts
        else
          incus storage create "incustest-$(basename "${INCUS_DIR}")-${driver}1" "${driver}"
        fi

        incus init testimage c1
        incus config device add c1 eth0 nic network="${brName}"
        incus config show c1

        originalPool=$(incus profile device get default root pool)

        # Check volatile.apply_template is initialized during create.
        incus config get c1 volatile.apply_template | grep create
        incus copy c1 c2 -s "incustest-$(basename "${INCUS_DIR}")-${driver}1"

        # Check volatile.apply_template is altered during copy.
        incus config get c2 volatile.apply_template | grep copy
        incus storage volume show "incustest-$(basename "${INCUS_DIR}")-${driver}1" container/c2
        incus delete -f c2
        incus move c1 c2 -s "incustest-$(basename "${INCUS_DIR}")-${driver}1"

        # Check volatile.apply_template is not altered during move and rename.
        incus config get c2 volatile.apply_template | grep create
        ! incus info c1 || false
        incus storage volume show "incustest-$(basename "${INCUS_DIR}")-${driver}1" container/c2

        # Test moving back to original pool without renaming.
        incus move c2 -s "${originalPool}"
        incus config get c2 volatile.apply_template | grep create
        incus storage volume show "${originalPool}" container/c2
        incus delete -f c2

        incus init testimage c1
        incus snapshot create c1
        incus snapshot create c1
        incus copy c1 c2 -s "incustest-$(basename "${INCUS_DIR}")-${driver}1" --instance-only
        incus storage volume show "incustest-$(basename "${INCUS_DIR}")-${driver}1" container/c2
        ! incus storage volume snapshot show "incustest-$(basename "${INCUS_DIR}")-${driver}1" container/c2/snap0 || false
        ! incus storage volume snapshot show "incustest-$(basename "${INCUS_DIR}")-${driver}1" container/c2/snap1 || false
        incus delete -f c2
        incus move c1 c2 -s "incustest-$(basename "${INCUS_DIR}")-${driver}1" --instance-only
        ! incus info c1 || false
        incus storage volume show "incustest-$(basename "${INCUS_DIR}")-${driver}1" container/c2
        ! incus storage volume snapshot show "incustest-$(basename "${INCUS_DIR}")-${driver}1" container/c2/snap0 || false
        ! incus storage volume snapshot show "incustest-$(basename "${INCUS_DIR}")-${driver}1" container/c2/snap1 || false
        incus delete -f c2

        incus init testimage c1
        incus snapshot create c1
        incus snapshot create c1
        incus copy c1 c2 -s "incustest-$(basename "${INCUS_DIR}")-${driver}1"
        incus storage volume show "incustest-$(basename "${INCUS_DIR}")-${driver}1" container/c2
        incus storage volume snapshot show "incustest-$(basename "${INCUS_DIR}")-${driver}1" container/c2/snap0
        incus storage volume snapshot show "incustest-$(basename "${INCUS_DIR}")-${driver}1" container/c2/snap1
        incus delete -f c2
        incus move c1 c2 -s "incustest-$(basename "${INCUS_DIR}")-${driver}1"
        ! incus info c1 || false
        incus storage volume show "incustest-$(basename "${INCUS_DIR}")-${driver}1" container/c2
        incus storage volume snapshot show "incustest-$(basename "${INCUS_DIR}")-${driver}1" container/c2/snap0
        incus storage volume snapshot show "incustest-$(basename "${INCUS_DIR}")-${driver}1" container/c2/snap1
        incus delete -f c2
      fi
    done

    incus network delete "${brName}"
  )

  # shellcheck disable=SC2031,2269
  INCUS_DIR="${INCUS_DIR}"
  kill_incus "${INCUS_STORAGE_DIR}"
}
