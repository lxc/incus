test_image_auto_update() {
  if incus image alias list | grep -q "^| testimage\\s*|.*$"; then
      incus image delete testimage
  fi

  # shellcheck disable=2039,3043
  local INCUS2_DIR INCUS2_ADDR
  INCUS2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS2_DIR}"
  spawn_incus "${INCUS2_DIR}" true
  INCUS2_ADDR=$(cat "${INCUS2_DIR}/incus.addr")

  (INCUS_DIR=${INCUS2_DIR} deps/import-busybox --alias testimage --public)
  fp1="$(INCUS_DIR=${INCUS2_DIR} incus image info testimage | awk '/^Fingerprint/ {print $2}')"

  token="$(INCUS_DIR=${INCUS2_DIR} incus config trust add foo -q)"
  incus remote add l2 "${INCUS2_ADDR}" --accept-certificate --token "${token}"
  incus init l2:testimage c1

  # Now the first image image is in the local store, since it was
  # downloaded to create c1.
  alias="$(incus image info "${fp1}" | awk '{if ($1 == "Alias:") {print $2}}')"
  [ "${alias}" = "testimage" ]

  # Delete the first image from the remote store and replace it with a
  # new one with a different fingerprint (passing "--template create"
  # will do that).
  (INCUS_DIR=${INCUS2_DIR} incus image delete testimage)
  (INCUS_DIR=${INCUS2_DIR} deps/import-busybox --alias testimage --public --template create)
  fp2="$(INCUS_DIR=${INCUS2_DIR} incus image info testimage | awk '/^Fingerprint/ {print $2}')"
  [ "${fp1}" != "${fp2}" ]

  # Restart the server to force an image refresh immediately
  # shellcheck disable=2153
  shutdown_incus "${INCUS_DIR}"
  respawn_incus "${INCUS_DIR}" true

  # Check that the first image got deleted from the local storage
  #
  # XXX: Since the auto-update logic runs asynchronously we need to wait
  #      a little bit before it actually completes.
  retries=600
  while [ "${retries}" != "0" ]; do
    if incus image info "${fp1}" > /dev/null 2>&1; then
        sleep 2
        retries=$((retries-1))
        continue
    fi
    break
  done

  if [ "${retries}" -eq 0 ]; then
      echo "First image ${fp1} not deleted from local store"
      return 1
  fi

  # The second image replaced the first one in the local storage.
  alias="$(incus image info "${fp2}" | awk '{if ($1 == "Alias:") {print $2}}')"
  [ "${alias}" = "testimage" ]

  incus delete c1
  incus remote remove l2
  incus image delete "${fp2}"
  kill_incus "$INCUS2_DIR"
}
