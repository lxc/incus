test_security() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  # CVE-2016-1581
  if [ "$(storage_backend "$INCUS_DIR")" = "zfs" ]; then
    INCUS_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${INCUS_INIT_DIR}"
    spawn_incus "${INCUS_INIT_DIR}" false

    ZFS_POOL="incustest-$(basename "${INCUS_DIR}")-init"
    INCUS_DIR=${INCUS_INIT_DIR} incus init --storage-backend zfs --storage-create-loop 1 --storage-pool "${ZFS_POOL}" --auto

    PERM=$(stat -c %a "${INCUS_INIT_DIR}/disks/${ZFS_POOL}.img")
    if [ "${PERM}" != "600" ]; then
      echo "Bad zfs.img permissions: ${PERM}"
      false
    fi

    kill_incus "${INCUS_INIT_DIR}"
  fi

  # CVE-2016-1582
  inc launch testimage test-priv -c security.privileged=true

  PERM=$(stat -L -c %a "${INCUS_DIR}/containers/test-priv")
  UID=$(stat -L -c %u "${INCUS_DIR}/containers/test-priv")
  if [ "${PERM}" != "100" ]; then
    echo "Bad container permissions: ${PERM}"
    false
  fi

  if [ "${UID}" != "0" ]; then
    echo "Bad container owner: ${UID}"
    false
  fi

  inc config set test-priv security.privileged false
  inc restart test-priv --force
  inc config set test-priv security.privileged true
  inc restart test-priv --force

  PERM=$(stat -L -c %a "${INCUS_DIR}/containers/test-priv")
  UID=$(stat -L -c %u "${INCUS_DIR}/containers/test-priv")
  if [ "${PERM}" != "100" ]; then
    echo "Bad container permissions: ${PERM}"
    false
  fi

  if [ "${UID}" != "0" ]; then
    echo "Bad container owner: ${UID}"
    false
  fi

  inc delete test-priv --force

  inc launch testimage test-unpriv
  inc config set test-unpriv security.privileged true
  inc restart test-unpriv --force

  PERM=$(stat -L -c %a "${INCUS_DIR}/containers/test-unpriv")
  UID=$(stat -L -c %u "${INCUS_DIR}/containers/test-unpriv")
  if [ "${PERM}" != "100" ]; then
    echo "Bad container permissions: ${PERM}"
    false
  fi

  if [ "${UID}" != "0" ]; then
    echo "Bad container owner: ${UID}"
    false
  fi

  inc config set test-unpriv security.privileged false
  inc restart test-unpriv --force

  PERM=$(stat -L -c %a "${INCUS_DIR}/containers/test-unpriv")
  UID=$(stat -L -c %u "${INCUS_DIR}/containers/test-unpriv")
  if [ "${PERM}" != "100" ]; then
    echo "Bad container permissions: ${PERM}"
    false
  fi

  if [ "${UID}" = "0" ]; then
    echo "Bad container owner: ${UID}"
    false
  fi

  inc delete test-unpriv --force

  # shellcheck disable=2039,3043
  local INCUS_STORAGE_DIR

  INCUS_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  chmod +x "${INCUS_STORAGE_DIR}"
  # Enforce that only unprivileged containers can be created
  INCUS_UNPRIVILEGED_ONLY=true
  export INCUS_UNPRIVILEGED_ONLY
  spawn_incus "${INCUS_STORAGE_DIR}" true
  unset INCUS_UNPRIVILEGED_ONLY

  (
    set -e
    # shellcheck disable=2030
    INCUS_DIR="${INCUS_STORAGE_DIR}"

    # Import image into default storage pool.
    ensure_import_testimage

    # Verify that no privileged container can be created
    ! inc launch testimage c1 -c security.privileged=true || false

    # Verify that unprivileged container can be created
    inc launch testimage c1

    # Verify that we can't be tricked into using privileged containers
    ! inc config set c1 security.privileged true || false
    ! inc config set c1 raw.idmap "both 0 1000" || false
    ! inc config set c1 raw.lxc "lxc.idmap=" || false
    ! inc config set c1 raw.lxc "lxc.include=" || false

    # Verify that we can still unset and set to security.privileged to "false"
    inc config set c1 security.privileged false
    inc config unset c1 security.privileged

    # Verify that a profile can't be changed to trick us into using privileged
    # containers
    ! inc profile set default security.privileged true || false
    ! inc profile set default raw.idmap "both 0 1000" || false
    ! inc profile set default raw.lxc "lxc.idmap=" || false
    ! inc profile set default raw.lxc "lxc.include=" || false

    # Verify that we can still unset and set to security.privileged to "false"
    inc profile set default security.privileged false
    inc profile unset default security.privileged

    inc delete -f c1
  )

  # shellcheck disable=SC2031,2269
  INCUS_DIR="${INCUS_DIR}"
  kill_incus "${INCUS_STORAGE_DIR}"
}

test_security_protection() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  # Test deletion protecton
  inc init testimage c1
  inc snapshot c1
  inc delete c1

  inc profile set default security.protection.delete true

  inc init testimage c1
  inc snapshot c1
  inc delete c1/snap0
  ! inc delete c1 || false

  inc config set c1 security.protection.delete false
  inc delete c1

  inc profile unset default security.protection.delete

  # Test shifting protection

  # Respawn Incus with kernel ID shifting support disabled to force manual shifting.
  shutdown_incus "${INCUS_DIR}"
  incusShiftfsDisable=${INCUS_SHIFTFS_DISABLE:-}
  incusIdmappedMountsDisable=${INCUS_IDMAPPED_MOUNTS_DISABLE:-}

  export INCUS_SHIFTFS_DISABLE=1
  export INCUS_IDMAPPED_MOUNTS_DISABLE=1
  respawn_incus "${INCUS_DIR}" true

  inc init testimage c1
  inc start c1
  inc stop c1 --force

  inc profile set default security.protection.shift true
  inc start c1
  inc stop c1 --force

  inc publish c1 --alias=protected
  inc image delete protected

  inc snapshot c1
  inc publish c1/snap0 --alias=protected
  inc image delete protected

  inc config set c1 security.privileged true
  ! inc start c1 || false
  inc config set c1 security.protection.shift false
  inc start c1
  inc stop c1 --force

  inc delete c1
  inc profile unset default security.protection.shift

  # Respawn Incus to restore default kernel shifting support.
  shutdown_incus "${INCUS_DIR}"
  export INCUS_SHIFTFS_DISABLE="${incusShiftfsDisable}"
  export INCUS_IDMAPPED_MOUNTS_DISABLE="${incusIdmappedMountsDisable}"

  respawn_incus "${INCUS_DIR}" true
}
