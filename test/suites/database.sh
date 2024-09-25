# Test restore database backups after a failed upgrade.
test_database_restore(){
  INCUS_RESTORE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)

  spawn_incus "${INCUS_RESTORE_DIR}" true

  # Set a config value before the broken upgrade.
  (
    set -e
    # shellcheck disable=SC2034
    INCUS_DIR=${INCUS_RESTORE_DIR}
    incus config set "core.https_allowed_credentials" "true"
  )

  shutdown_incus "${INCUS_RESTORE_DIR}"

  # Simulate a broken update by dropping in a buggy patch.global.sql
  cat << EOF > "${INCUS_RESTORE_DIR}/database/patch.global.sql"
UPDATE config SET value='false' WHERE key='core.https_allowed_credentials';
INSERT INTO broken(n) VALUES(1);
EOF

  # Starting Incus fails.
  ! INCUS_DIR="${INCUS_RESTORE_DIR}" incusd --logfile "${INCUS_RESTORE_DIR}/incus.log" "${DEBUG-}" 2>&1 || false

  # Remove the broken patch
  rm -f "${INCUS_RESTORE_DIR}/database/patch.global.sql"

  # Restore the backup
  rm -rf "${INCUS_RESTORE_DIR}/database/global"
  cp -a "${INCUS_RESTORE_DIR}/database/global.bak" "${INCUS_RESTORE_DIR}/database/global"

  # Restart the daemon and check that our previous settings are still there
  respawn_incus "${INCUS_RESTORE_DIR}" true
  (
    set -e
    # shellcheck disable=SC2034
    INCUS_DIR=${INCUS_RESTORE_DIR}
    incus config get "core.https_allowed_credentials" | grep -q "true"
  )

  kill_incus "${INCUS_RESTORE_DIR}"
}

test_database_no_disk_space(){
  # shellcheck disable=2039,3043
  local INCUS_DIR

  INCUS_NOSPACE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)

  # Mount a tmpfs with limited space in the global database directory and create
  # a very big file in it, which will eventually cause database transactions to
  # fail.
  GLOBAL_DB_DIR="${INCUS_NOSPACE_DIR}/database/global"
  BIG_FILE="${GLOBAL_DB_DIR}/bigfile"
  mkdir -p "${GLOBAL_DB_DIR}"
  mount -t tmpfs -o size=67108864 tmpfs "${GLOBAL_DB_DIR}"
  dd bs=1024 count=51200 if=/dev/zero of="${BIG_FILE}"

  spawn_incus "${INCUS_NOSPACE_DIR}" true

  (
    set -e
    # shellcheck disable=SC2034,SC2030
    INCUS_DIR="${INCUS_NOSPACE_DIR}"

    ensure_import_testimage
    incus init testimage c

    # Set a custom user property with a big value, so we eventually eat up all
    # available disk space in the database directory.
    DATA="${INCUS_NOSPACE_DIR}/data"
    head -c 262144 < /dev/zero | tr '\0' '\141' > "${DATA}"
    for i in $(seq 20); do
        if ! incus config set c "user.prop${i}" - < "${DATA}"; then
            break
        fi
    done

    # Commands that involve writing to the database keep failing.
    ! incus config set c "user.propX" - < "${DATA}" || false
    ! incus config set c "user.propY" - < "${DATA}" || false

    # Removing the big file eventually makes the database happy again.
    rm "${BIG_FILE}"

    succeeded=no
    for i in $(seq 10); do
        if incus config set c "user.propZ" - < "${DATA}"; then
            succeeded=yes
            break
        fi
        sleep 1
    done
    [ "${succeeded}" = "yes" ] || false

    incus delete -f c
  )

  shutdown_incus "${INCUS_NOSPACE_DIR}"
  umount "${GLOBAL_DB_DIR}"
  kill_incus "${INCUS_NOSPACE_DIR}"
}
