test_image_expiry() {
  # shellcheck disable=2039,3043
  local INCUS2_DIR INCUS2_ADDR
  INCUS2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS2_DIR}"
  spawn_incus "${INCUS2_DIR}" true
  INCUS2_ADDR=$(cat "${INCUS2_DIR}/incus.addr")

  ensure_import_testimage

  # shellcheck disable=2153
  inc_remote remote add l1 "${INCUS_ADDR}" --accept-certificate --password foo
  inc_remote remote add l2 "${INCUS2_ADDR}" --accept-certificate --password foo

  # Create containers from a remote image in two projects.
  inc_remote project create l2:p1 -c features.images=true -c features.profiles=false
  inc_remote init l1:testimage l2:c1 --project default
  inc_remote project switch l2:p1
  inc_remote init l1:testimage l2:c2
  inc_remote project switch l2:default

  fp="$(inc_remote image info testimage | awk '/^Fingerprint/ {print $2}')"

  # Confirm the image is cached
  [ -n "${fp}" ]
  fpbrief=$(echo "${fp}" | cut -c 1-12)
  inc_remote image list l2: | grep -q "${fpbrief}"

  # Test modification of image expiry date
  inc_remote image info "l2:${fp}" | grep -q "Expires.*never"
  inc_remote image show "l2:${fp}" | sed "s/expires_at.*/expires_at: 3000-01-01T00:00:00-00:00/" | inc_remote image edit "l2:${fp}"
  inc_remote image info "l2:${fp}" | grep -q "Expires.*3000"

  # Override the upload date for the image record in the default project.
  INCUS_DIR="$INCUS2_DIR" incusd sql global "UPDATE images SET last_use_date='$(date --rfc-3339=seconds -u -d "2 days ago")' WHERE fingerprint='${fp}' AND project_id = 1" | grep -q "Rows affected: 1"

  # Trigger the expiry
  inc_remote config set l2: images.remote_cache_expiry 1

  for _ in $(seq 20); do
    sleep 1
    ! inc_remote image list l2: | grep -q "${fpbrief}" && break
  done

  ! inc_remote image list l2: | grep -q "${fpbrief}" || false

  # Check image is still in p1 project and has not been expired.
  inc_remote image list l2: --project p1 | grep -q "${fpbrief}"

  # Test instance can still be created in p1 project.
  inc_remote project switch l2:p1
  inc_remote init l1:testimage l2:c3
  inc_remote project switch l2:default

  # Override the upload date for the image record in the p1 project.
  INCUS_DIR="$INCUS2_DIR" incusd sql global "UPDATE images SET last_use_date='$(date --rfc-3339=seconds -u -d "2 days ago")' WHERE fingerprint='${fp}' AND project_id > 1" | grep -q "Rows affected: 1"
  inc_remote project set l2:p1 images.remote_cache_expiry=1

  # Trigger the expiry in p1 project by changing global images.remote_cache_expiry.
  inc_remote config unset l2: images.remote_cache_expiry

  for _ in $(seq 20); do
    sleep 1
    ! inc_remote image list l2: --project p1 | grep -q "${fpbrief}" && break
  done

  ! inc_remote image list l2: --project p1 | grep -q "${fpbrief}" || false

  # Cleanup and reset
  inc_remote delete -f l2:c1
  inc_remote delete -f l2:c2 --project p1
  inc_remote delete -f l2:c3 --project p1
  inc_remote project delete l2:p1
  inc_remote remote remove l1
  inc_remote remote remove l2
  kill_incus "$INCUS2_DIR"
}

test_image_list_all_aliases() {
    ensure_import_testimage
    # shellcheck disable=2039,2034,2155,3043
    local sum="$(inc image info testimage | awk '/^Fingerprint/ {print $2}')"
    inc image alias create zzz "$sum"
    inc image list | grep -vq zzz
    # both aliases are listed if the "aliases" column is included in output
    inc image list -c L | grep -q testimage
    inc image list -c L | grep -q zzz

}

test_image_import_dir() {
    ensure_import_testimage
    inc image export testimage
    # shellcheck disable=2039,2034,2155,3043
    local image="$(ls -1 -- *.tar.xz)"
    mkdir -p unpacked
    tar -C unpacked -xf "$image"
    # shellcheck disable=2039,2034,2155,3043
    local fingerprint="$(inc image import unpacked | awk '{print $NF;}')"
    rm -rf "$image" unpacked

    inc image export "$fingerprint"
    # shellcheck disable=2039,2034,2155,3043
    local exported="${fingerprint}.tar.xz"

    tar tvf "$exported" | grep -Fq metadata.yaml
    rm "$exported"
}

test_image_import_existing_alias() {
    ensure_import_testimage
    inc init testimage c
    inc publish c --alias newimage --alias image2
    inc delete c
    inc image export testimage testimage.file
    inc image delete testimage
    # the image can be imported with an existing alias
    inc image import testimage.file --alias newimage
    inc image delete newimage image2
}

test_image_refresh() {
  # shellcheck disable=2039,3043
  local INCUS2_DIR INCUS2_ADDR
  INCUS2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS2_DIR}"
  spawn_incus "${INCUS2_DIR}" true
  INCUS2_ADDR=$(cat "${INCUS2_DIR}/incus.addr")

  ensure_import_testimage

  inc_remote remote add l2 "${INCUS2_ADDR}" --accept-certificate --password foo

  poolDriver="$(inc storage show "$(inc profile device get default root pool)" | awk '/^driver:/ {print $2}')"

  # Publish image
  inc image copy testimage l2: --alias testimage --public
  fp="$(inc image info l2:testimage | awk '/Fingerprint: / {print $2}')"
  inc image rm testimage

  # Create container from published image
  inc init l2:testimage c1

  # Create an alias for the received image
  inc image alias create testimage "${fp}"

  # Change image and publish it
  inc init l2:testimage l2:c1
  echo test | inc file push - l2:c1/tmp/testfile
  inc publish l2:c1 l2: --alias testimage --reuse --public
  new_fp="$(inc image info l2:testimage | awk '/Fingerprint: / {print $2}')"

  # Ensure the images differ
  [ "${fp}" != "${new_fp}" ]

  # Check original image exists before refresh.
  inc image info "${fp}"

  if [ "${poolDriver}" != "dir" ]; then
    # Check old storage volume record exists and new one doesn't.
    incusd sql global 'select name from storage_volumes' | grep "${fp}"
    ! incusd sql global 'select name from storage_volumes' | grep "${new_fp}" || false
  fi

  # Refresh image
  inc image refresh testimage

  # Ensure the old image is gone.
  ! inc image info "${fp}" || false

  if [ "${poolDriver}" != "dir" ]; then
    # Check old storage volume record has been replaced with new one.
    ! incusd sql global 'select name from storage_volumes' | grep "${fp}" || false
    incusd sql global 'select name from storage_volumes' | grep "${new_fp}"
  fi

  # Cleanup
  inc rm l2:c1
  inc rm c1
  inc remote rm l2
  kill_incus "${INCUS2_DIR}"
}
