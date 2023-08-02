# Test the incus sql command.
test_sql() {
  # Invalid arguments
  ! incus sql foo "SELECT * FROM CONFIG" || false
  ! incus sql global "" || false

  # Local database query
  incus sql local "SELECT * FROM config" | grep -qF "core.https_address"

  # Global database query
  incus sql global "SELECT * FROM config" | grep -qF "core.trust_password"

  # Global database insert
  incus sql global "INSERT INTO config(key,value) VALUES('core.https_allowed_credentials','true')" | grep -qxF "Rows affected: 1"
  incus sql global "DELETE FROM config WHERE key='core.https_allowed_credentials'" | grep -qxF "Rows affected: 1"

  # Standard input
  echo "SELECT * FROM config" | incus sql global - | grep -qF "core.trust_password"

  # Multiple queries
  incus sql global "SELECT * FROM config; SELECT * FROM instances" | grep -qxF "=> Query 0:"

  # Local database dump
  SQLITE_DUMP="${TEST_DIR}/dump.db"
  incus sql local .dump | sqlite3 "${SQLITE_DUMP}"
  sqlite3 "${SQLITE_DUMP}" "SELECT * FROM patches" | grep -qF "|dnsmasq_entries_include_device_name|"
  rm -f "${SQLITE_DUMP}"

  # Local database schema dump
  SQLITE_DUMP="${TEST_DIR}/dump.db"
  incus sql local .schema | sqlite3 "${SQLITE_DUMP}"
  [ "$(sqlite3 "${SQLITE_DUMP}" 'SELECT * FROM schema' | wc -l)" = "0" ]
  [ "$(sqlite3 "${SQLITE_DUMP}" 'SELECT * FROM patches' | wc -l)" = "0" ]
  rm -f "${SQLITE_DUMP}"

  # Global database dump
  SQLITE_DUMP="${TEST_DIR}/dump.db"
  GLOBAL_DUMP="$(incus sql global .dump)"
  echo "$GLOBAL_DUMP" | grep -F "CREATE TRIGGER" # ensure triggers are captured.
  echo "$GLOBAL_DUMP" | grep -F "CREATE INDEX"   # ensure indices are captured.
  echo "$GLOBAL_DUMP" | grep -F "CREATE VIEW"    # ensure views are captured.
  echo "$GLOBAL_DUMP" | sqlite3 "${SQLITE_DUMP}"
  sqlite3 "${SQLITE_DUMP}" "SELECT * FROM profiles" | grep -qF "|Default LXD profile|"
  rm -f "${SQLITE_DUMP}"

  # Global database schema dump
  SQLITE_DUMP="${TEST_DIR}/dump.db"
  incus sql global .schema | sqlite3 "${SQLITE_DUMP}"
  [ "$(sqlite3 "${SQLITE_DUMP}" 'SELECT * FROM schema' | wc -l)" = "0" ]
  [ "$(sqlite3 "${SQLITE_DUMP}" 'SELECT * FROM profiles' | wc -l)" = "0" ]
  rm -f "${SQLITE_DUMP}"

  # Sync the global database to disk
  SQLITE_SYNC="${INCUS_DIR}/database/global/db.bin"
  echo "SYNC ${SQLITE_SYNC}"
  incus sql global .sync
  sqlite3 "${SQLITE_SYNC}" "SELECT * FROM schema" | grep -q "^1|"
  sqlite3 "${SQLITE_SYNC}" "SELECT * FROM profiles" | grep -qF "|Default LXD profile|"
}
