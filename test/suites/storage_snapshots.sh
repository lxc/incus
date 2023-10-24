test_storage_volume_snapshots() {
  ensure_import_testimage

  # shellcheck disable=2039,3043
  local INCUS_STORAGE_DIR incus_backend

  incus_backend=$(storage_backend "$INCUS_DIR")
  INCUS_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
  chmod +x "${INCUS_STORAGE_DIR}"
  spawn_incus "${INCUS_STORAGE_DIR}" false
  token="$(INCUS_DIR=${INCUS_DIR} incus config trust add foo -q)"
  # shellcheck disable=2153
  incus remote add test "${INCUS_ADDR}" --accept-certificate --token "${token}"

  # shellcheck disable=2039,3043
  local storage_pool storage_volume
  storage_pool="incustest-$(basename "${INCUS_STORAGE_DIR}")-pool"
  storage_pool2="${storage_pool}2"
  storage_volume="${storage_pool}-vol"

  incus storage create "$storage_pool" "$incus_backend"
  incus storage volume create "${storage_pool}" "${storage_volume}"
  incus launch testimage c1 -s "${storage_pool}"
  incus storage volume attach "${storage_pool}" "${storage_volume}" c1 /mnt
  # Create file on volume
  echo foobar > "${TEST_DIR}/testfile"
  incus file push "${TEST_DIR}/testfile" c1/mnt/testfile

  # Validate file
  incus exec c1 -- test -f /mnt/testfile
  [ "$(incus exec c1 -- cat /mnt/testfile)" = 'foobar' ]

  incus storage volume detach "${storage_pool}" "${storage_volume}" c1
  # This will create a snapshot named 'snap0'
  incus storage volume snapshot create "${storage_pool}" "${storage_volume}"
  incus storage volume list "${storage_pool}" |  grep "${storage_volume}/snap0"
  incus storage volume snapshot show "${storage_pool}" "${storage_volume}/snap0" | grep 'name: snap0'
  incus storage volume snapshot show "${storage_pool}" "${storage_volume}/snap0" | grep 'expires_at: 0001-01-01T00:00:00Z'

  # edit volume snapshot description
  incus storage volume snapshot show "${storage_pool}" "${storage_volume}/snap0" | sed 's/^description:.*/description: foo/' | incus storage volume edit "${storage_pool}" "${storage_volume}/snap0"
  incus storage volume snapshot show "${storage_pool}" "${storage_volume}/snap0" | grep -q 'description: foo'

  # edit volume snapshot expiry date
  incus storage volume snapshot show "${storage_pool}" "${storage_volume}/snap0" | sed 's/^expires_at:.*/expires_at: 2100-01-02T15:04:05Z/' | incus storage volume edit "${storage_pool}" "${storage_volume}/snap0"
  # Depending on the timezone of the runner, some values will be different.
  # Both the year (2100) and the month (01) will be constant though.
  incus storage volume snapshot show "${storage_pool}" "${storage_volume}/snap0" | grep -q '^expires_at: 2100-01'
  # Reset/remove expiry date
  incus storage volume snapshot show "${storage_pool}" "${storage_volume}/snap0" | sed '/^expires_at:/d' | incus storage volume edit "${storage_pool}" "${storage_volume}/snap0"
  incus storage volume snapshot show "${storage_pool}" "${storage_volume}/snap0" | grep -q '^expires_at: 0001-01-01T00:00:00Z'

  incus storage volume set "${storage_pool}" "${storage_volume}" snapshots.expiry '1d'
  incus storage volume snapshot create "${storage_pool}" "${storage_volume}"
  ! incus storage volume snapshot show "${storage_pool}" "${storage_volume}/snap1" | grep -q 'expires_at: 0001-01-01T00:00:00Z' || false

  incus storage volume snapshot create "${storage_pool}" "${storage_volume}" --no-expiry
  incus storage volume snapshot show "${storage_pool}" "${storage_volume}/snap2" | grep -q 'expires_at: 0001-01-01T00:00:00Z' || false

  incus storage volume snapshot rm "${storage_pool}" "${storage_volume}" "snap2"
  incus storage volume snapshot rm "${storage_pool}" "${storage_volume}" "snap1"

  # Test snapshot renaming
  incus storage volume snapshot create "${storage_pool}" "${storage_volume}"
  incus storage volume list "${storage_pool}" |  grep "${storage_volume}/snap1"
  incus storage volume snapshot show "${storage_pool}" "${storage_volume}/snap1" | grep 'name: snap1'
  incus storage volume snapshot rename "${storage_pool}" "${storage_volume}" snap1 foo
  incus storage volume list "${storage_pool}" |  grep "${storage_volume}/foo"
  incus storage volume snapshot show "${storage_pool}" "${storage_volume}/foo" | grep 'name: foo'

  incus storage volume attach "${storage_pool}" "${storage_volume}" c1 /mnt
  # Delete file on volume
  incus file delete c1/mnt/testfile

  # Validate file
  ! incus exec c1 -- test -f /mnt/testfile || false

  # This should fail since you cannot restore a snapshot when the target volume
  # is attached to the container
  ! incus storage volume snapshot restore "${storage_pool}" "${storage_volume}" snap0 || false

  incus stop -f c1
  incus storage volume snapshot restore "${storage_pool}" "${storage_volume}" foo

  incus start c1
  incus storage volume detach "${storage_pool}" "${storage_volume}" c1
  incus storage volume snapshot restore "${storage_pool}" "${storage_volume}" foo
  incus storage volume attach "${storage_pool}" "${storage_volume}" c1 /mnt

  # Validate file
  incus exec c1 -- test -f /mnt/testfile
  [ "$(incus exec c1 -- cat /mnt/testfile)" = 'foobar' ]

  incus storage volume detach "${storage_pool}" "${storage_volume}" c1
  incus delete -f c1
  incus storage volume delete "${storage_pool}" "${storage_volume}"

  # Check snapshots naming conflicts.
  incus storage volume create "${storage_pool}" "vol1"
  incus storage volume create "${storage_pool}" "vol1-snap0"
  incus storage volume snapshot create "${storage_pool}" "vol1" "snap0"
  incus storage volume delete "${storage_pool}" "vol1"
  incus storage volume delete "${storage_pool}" "vol1-snap0"

  # Check snapshot copy (mode pull).
  incus launch testimage "c1"
  incus storage volume create "${storage_pool}" "vol1"
  incus storage volume attach "${storage_pool}" "vol1" "c1" /mnt
  incus exec "c1" -- touch /mnt/foo
  incus delete -f "c1"
  incus storage volume snapshot create "${storage_pool}" "vol1" "snap0"
  incus storage volume copy "${storage_pool}/vol1/snap0" "${storage_pool}/vol2" --mode pull
  incus launch testimage "c1"
  incus storage volume attach "${storage_pool}" "vol2" "c1" /mnt
  incus exec "c1" -- test -f /mnt/foo
  incus delete -f "c1"
  incus storage volume delete "${storage_pool}" "vol2"

  # Check snapshot copy (mode pull, remote).
  incus storage volume copy "${storage_pool}/vol1/snap0" "test:${storage_pool}/vol2" --mode pull
  incus launch testimage "c1"
  incus storage volume attach "${storage_pool}" "vol2" "c1" /mnt
  incus exec "c1" -- test -f /mnt/foo
  incus delete -f "c1"
  incus storage volume delete "${storage_pool}" "vol2"

  # Check snapshot copy (mode push).
  incus storage volume copy "${storage_pool}/vol1/snap0" "${storage_pool}/vol2" --mode push
  incus launch testimage "c1"
  incus storage volume attach "${storage_pool}" "vol2" "c1" /mnt
  incus exec "c1" -- test -f /mnt/foo
  incus delete -f "c1"
  incus storage volume delete "${storage_pool}" "vol2"

  # Check snapshot copy (mode push, remote).
  incus storage volume copy "${storage_pool}/vol1/snap0" "test:${storage_pool}/vol2" --mode push
  incus launch testimage "c1"
  incus storage volume attach "${storage_pool}" "vol2" "c1" /mnt
  incus exec "c1" -- test -f /mnt/foo
  incus delete -f "c1"
  incus storage volume delete "${storage_pool}" "vol2"

  # Check snapshot copy (mode relay).
  incus storage volume copy "${storage_pool}/vol1/snap0" "${storage_pool}/vol2" --mode relay
  incus launch testimage "c1"
  incus storage volume attach "${storage_pool}" "vol2" "c1" /mnt
  incus exec "c1" -- test -f /mnt/foo
  incus delete -f "c1"
  incus storage volume delete "${storage_pool}" "vol2"

  # Check snapshot copy (mode relay, remote).
  incus storage volume copy "${storage_pool}/vol1/snap0" "test:${storage_pool}/vol2" --mode relay
  incus launch testimage "c1"
  incus storage volume attach "${storage_pool}" "vol2" "c1" /mnt
  incus exec "c1" -- test -f /mnt/foo
  incus delete -f "c1"
  incus storage volume delete "${storage_pool}" "vol2"

  # Check snapshot copy between pools.
  incus storage create "${storage_pool2}" dir
  incus storage volume copy "${storage_pool}/vol1/snap0" "${storage_pool2}/vol2"
  incus launch testimage "c1"
  incus storage volume attach "${storage_pool2}" "vol2" "c1" /mnt
  incus exec "c1" -- test -f /mnt/foo
  incus delete -f "c1"
  incus storage volume delete "${storage_pool2}" "vol2"
  incus storage delete "${storage_pool2}"

  # Check snapshot copy between pools (remote).
  incus storage create "${storage_pool2}" dir
  incus storage volume copy "${storage_pool}/vol1/snap0" "test:${storage_pool2}/vol2"
  incus launch testimage "c1"
  incus storage volume attach "${storage_pool2}" "vol2" "c1" /mnt
  incus exec "c1" -- test -f /mnt/foo
  incus delete -f "c1"
  incus storage volume delete "${storage_pool2}" "vol2"
  incus storage volume copy "test:${storage_pool}/vol1/snap0" "${storage_pool2}/vol2"
  incus launch testimage "c1"
  incus storage volume attach "${storage_pool2}" "vol2" "c1" /mnt
  incus exec "c1" -- test -f /mnt/foo
  incus delete -f "c1"
  incus storage volume delete "${storage_pool2}" "vol2"
  incus storage delete "${storage_pool2}"

  # Check snapshot volume only copy.
  ! incus storage volume copy "${storage_pool}/vol1/snap0" "${storage_pool}/vol2" --volume-only || false
  incus storage volume copy "${storage_pool}/vol1" "${storage_pool}/vol2" --volume-only
  [ "$(incus query "/1.0/storage-pools/${storage_pool}/volumes/custom/vol2/snapshots" | jq "length == 0")" = "true" ]
  incus storage volume delete "${storage_pool}" "vol2"

  # Check snapshot volume only copy (remote).
  ! incus storage volume copy "${storage_pool}/vol1/snap0" "test:${storage_pool}/vol2" --volume-only || false
  incus storage volume copy "${storage_pool}/vol1" "test:${storage_pool}/vol2" --volume-only
  [ "$(incus query "/1.0/storage-pools/${storage_pool}/volumes/custom/vol2/snapshots" | jq "length == 0")" = "true" ]
  incus storage volume delete "${storage_pool}" "vol2"

  # Check snapshot refresh.
  incus storage volume copy "${storage_pool}/vol1/snap0" "${storage_pool}/vol2"
  incus storage volume copy "${storage_pool}/vol1/snap0" "${storage_pool}/vol2" --refresh
  incus storage volume delete "${storage_pool}" "vol2"

  # Check snapshot refresh (remote).
  incus storage volume copy "${storage_pool}/vol1/snap0" "test:${storage_pool}/vol2"
  incus storage volume copy "${storage_pool}/vol1/snap0" "test:${storage_pool}/vol2" --refresh
  incus storage volume delete "${storage_pool}" "vol2"

  # Check snapshot copy between projects.
  incus project create project1
  incus storage volume copy "${storage_pool}/vol1/snap0" "${storage_pool}/vol1" --target-project project1
  [ "$(incus query "/1.0/storage-pools/${storage_pool}/volumes?project=project1" | jq "length == 1")" = "true" ]
  incus storage volume delete "${storage_pool}" "vol1" --project project1

  # Check snapshot copy between projects (remote).
  incus storage volume copy "${storage_pool}/vol1/snap0" "test:${storage_pool}/vol1" --target-project project1
  [ "$(incus query "/1.0/storage-pools/${storage_pool}/volumes?project=project1" | jq "length == 1")" = "true" ]
  incus storage volume delete "${storage_pool}" "vol1" --project project1

  incus storage volume delete "${storage_pool}" "vol1"
  incus project delete "project1"
  incus storage delete "${storage_pool}"
  incus remote remove "test"

  # shellcheck disable=SC2031,2269
  INCUS_DIR="${INCUS_DIR}"
  kill_incus "${INCUS_STORAGE_DIR}"
}
