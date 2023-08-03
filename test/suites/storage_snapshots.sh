test_storage_volume_snapshots() {
  ensure_import_testimage

  # shellcheck disable=2039,3043
  local INCUS_STORAGE_DIR incus_backend

  incus_backend=$(storage_backend "$INCUS_DIR")
  INCUS_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  chmod +x "${INCUS_STORAGE_DIR}"
  spawn_incus "${INCUS_STORAGE_DIR}" false

  # shellcheck disable=2039,3043
  local storage_pool storage_volume
  storage_pool="incustest-$(basename "${INCUS_STORAGE_DIR}")-pool"
  storage_volume="${storage_pool}-vol"

  inc storage create "$storage_pool" "$incus_backend"
  inc storage volume create "${storage_pool}" "${storage_volume}"
  inc launch testimage c1 -s "${storage_pool}"
  inc storage volume attach "${storage_pool}" "${storage_volume}" c1 /mnt
  # Create file on volume
  echo foobar > "${TEST_DIR}/testfile"
  inc file push "${TEST_DIR}/testfile" c1/mnt/testfile

  # Validate file
  inc exec c1 -- test -f /mnt/testfile
  [ "$(inc exec c1 -- cat /mnt/testfile)" = 'foobar' ]

  inc storage volume detach "${storage_pool}" "${storage_volume}" c1
  # This will create a snapshot named 'snap0'
  inc storage volume snapshot "${storage_pool}" "${storage_volume}"
  inc storage volume list "${storage_pool}" |  grep "${storage_volume}/snap0"
  inc storage volume show "${storage_pool}" "${storage_volume}/snap0" | grep 'name: snap0'
  inc storage volume show "${storage_pool}" "${storage_volume}/snap0" | grep 'expires_at: 0001-01-01T00:00:00Z'

  # edit volume snapshot description
  inc storage volume show "${storage_pool}" "${storage_volume}/snap0" | sed 's/^description:.*/description: foo/' | inc storage volume edit "${storage_pool}" "${storage_volume}/snap0"
  inc storage volume show "${storage_pool}" "${storage_volume}/snap0" | grep -q 'description: foo'

  # edit volume snapshot expiry date
  inc storage volume show "${storage_pool}" "${storage_volume}/snap0" | sed 's/^expires_at:.*/expires_at: 2100-01-02T15:04:05Z/' | inc storage volume edit "${storage_pool}" "${storage_volume}/snap0"
  # Depending on the timezone of the runner, some values will be different.
  # Both the year (2100) and the month (01) will be constant though.
  inc storage volume show "${storage_pool}" "${storage_volume}/snap0" | grep -q '^expires_at: 2100-01'
  # Reset/remove expiry date
  inc storage volume show "${storage_pool}" "${storage_volume}/snap0" | sed '/^expires_at:/d' | inc storage volume edit "${storage_pool}" "${storage_volume}/snap0"
  inc storage volume show "${storage_pool}" "${storage_volume}/snap0" | grep -q '^expires_at: 0001-01-01T00:00:00Z'

  inc storage volume set "${storage_pool}" "${storage_volume}" snapshots.expiry '1d'
  inc storage volume snapshot "${storage_pool}" "${storage_volume}"
  ! inc storage volume show "${storage_pool}" "${storage_volume}/snap1" | grep -q 'expires_at: 0001-01-01T00:00:00Z' || false

  inc storage volume snapshot "${storage_pool}" "${storage_volume}" --no-expiry
  inc storage volume show "${storage_pool}" "${storage_volume}/snap2" | grep -q 'expires_at: 0001-01-01T00:00:00Z' || false

  inc storage volume rm "${storage_pool}" "${storage_volume}/snap2"
  inc storage volume rm "${storage_pool}" "${storage_volume}/snap1"

  # Test snapshot renaming
  inc storage volume snapshot "${storage_pool}" "${storage_volume}"
  inc storage volume list "${storage_pool}" |  grep "${storage_volume}/snap1"
  inc storage volume show "${storage_pool}" "${storage_volume}/snap1" | grep 'name: snap1'
  inc storage volume rename "${storage_pool}" "${storage_volume}/snap1" "${storage_volume}/foo"
  inc storage volume list "${storage_pool}" |  grep "${storage_volume}/foo"
  inc storage volume show "${storage_pool}" "${storage_volume}/foo" | grep 'name: foo'

  inc storage volume attach "${storage_pool}" "${storage_volume}" c1 /mnt
  # Delete file on volume
  inc file delete c1/mnt/testfile

  # Validate file
  ! inc exec c1 -- test -f /mnt/testfile || false

  # This should fail since you cannot restore a snapshot when the target volume
  # is attached to the container
  ! inc storage volume restore "${storage_pool}" "${storage_volume}" snap0 || false

  inc stop -f c1
  inc storage volume restore "${storage_pool}" "${storage_volume}" foo

  inc start c1
  inc storage volume detach "${storage_pool}" "${storage_volume}" c1
  inc storage volume restore "${storage_pool}" "${storage_volume}" foo
  inc storage volume attach "${storage_pool}" "${storage_volume}" c1 /mnt

  # Validate file
  inc exec c1 -- test -f /mnt/testfile
  [ "$(inc exec c1 -- cat /mnt/testfile)" = 'foobar' ]

  inc storage volume detach "${storage_pool}" "${storage_volume}" c1
  inc delete -f c1
  inc storage volume delete "${storage_pool}" "${storage_volume}"

  # Check snapshots naming conflicts.
  inc storage volume create "${storage_pool}" "vol1"
  inc storage volume create "${storage_pool}" "vol1-snap0"
  inc storage volume snapshot "${storage_pool}" "vol1" "snap0"
  inc storage volume delete "${storage_pool}" "vol1"
  inc storage volume delete "${storage_pool}" "vol1-snap0"


  inc storage delete "${storage_pool}"

  # shellcheck disable=SC2031,2269
  INCUS_DIR="${INCUS_DIR}"
  kill_incus "${INCUS_STORAGE_DIR}"
}
