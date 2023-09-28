test_image_expiry() {
  # shellcheck disable=2039,3043
  local INCUS2_DIR INCUS2_ADDR
  INCUS2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS2_DIR}"
  spawn_incus "${INCUS2_DIR}" true
  INCUS2_ADDR=$(cat "${INCUS2_DIR}/incus.addr")

  ensure_import_testimage

  token="$(incus config trust add foo -q)"
  # shellcheck disable=2153
  incus_remote remote add l1 "${INCUS_ADDR}" --accept-certificate --token "${token}"

  token="$(INCUS_DIR=${INCUS2_DIR} incus config trust add foo -q)"
  incus_remote remote add l2 "${INCUS2_ADDR}" --accept-certificate --token "${token}"

  # Create containers from a remote image in two projects.
  incus_remote project create l2:p1 -c features.images=true -c features.profiles=false
  incus_remote init l1:testimage l2:c1 --project default
  incus_remote project switch l2:p1
  incus_remote init l1:testimage l2:c2
  incus_remote project switch l2:default

  fp="$(incus_remote image info testimage | awk '/^Fingerprint/ {print $2}')"

  # Confirm the image is cached
  [ -n "${fp}" ]
  fpbrief=$(echo "${fp}" | cut -c 1-12)
  incus_remote image list l2: | grep -q "${fpbrief}"

  # Test modification of image expiry date
  incus_remote image info "l2:${fp}" | grep -q "Expires.*never"
  incus_remote image show "l2:${fp}" | sed "s/expires_at.*/expires_at: 3000-01-01T00:00:00-00:00/" | incus_remote image edit "l2:${fp}"
  incus_remote image info "l2:${fp}" | grep -q "Expires.*3000"

  # Override the upload date for the image record in the default project.
  INCUS_DIR="$INCUS2_DIR" incus admin sql global "UPDATE images SET last_use_date='$(date --rfc-3339=seconds -u -d "2 days ago")' WHERE fingerprint='${fp}' AND project_id = 1" | grep -q "Rows affected: 1"

  # Trigger the expiry
  incus_remote config set l2: images.remote_cache_expiry 1

  for _ in $(seq 20); do
    sleep 1
    ! incus_remote image list l2: | grep -q "${fpbrief}" && break
  done

  ! incus_remote image list l2: | grep -q "${fpbrief}" || false

  # Check image is still in p1 project and has not been expired.
  incus_remote image list l2: --project p1 | grep -q "${fpbrief}"

  # Test instance can still be created in p1 project.
  incus_remote project switch l2:p1
  incus_remote init l1:testimage l2:c3
  incus_remote project switch l2:default

  # Override the upload date for the image record in the p1 project.
  INCUS_DIR="$INCUS2_DIR" incus admin sql global "UPDATE images SET last_use_date='$(date --rfc-3339=seconds -u -d "2 days ago")' WHERE fingerprint='${fp}' AND project_id > 1" | grep -q "Rows affected: 1"
  incus_remote project set l2:p1 images.remote_cache_expiry=1

  # Trigger the expiry in p1 project by changing global images.remote_cache_expiry.
  incus_remote config unset l2: images.remote_cache_expiry

  for _ in $(seq 20); do
    sleep 1
    ! incus_remote image list l2: --project p1 | grep -q "${fpbrief}" && break
  done

  ! incus_remote image list l2: --project p1 | grep -q "${fpbrief}" || false

  # Cleanup and reset
  incus_remote delete -f l2:c1
  incus_remote delete -f l2:c2 --project p1
  incus_remote delete -f l2:c3 --project p1
  incus_remote project delete l2:p1
  incus_remote remote remove l1
  incus_remote remote remove l2
  kill_incus "$INCUS2_DIR"
}

test_image_list_all_aliases() {
    ensure_import_testimage
    # shellcheck disable=2039,2034,2155,3043
    local sum="$(incus image info testimage | awk '/^Fingerprint/ {print $2}')"
    incus image alias create zzz "$sum"
    incus image list | grep -vq zzz
    # both aliases are listed if the "aliases" column is included in output
    incus image list -c L | grep -q testimage
    incus image list -c L | grep -q zzz

}

test_image_import_dir() {
    ensure_import_testimage
    incus image export testimage
    # shellcheck disable=2039,2034,2155,3043
    local image="$(ls -1 -- *.tar.xz)"
    mkdir -p unpacked
    tar -C unpacked -xf "$image"
    # shellcheck disable=2039,2034,2155,3043
    local fingerprint="$(incus image import unpacked | awk '{print $NF;}')"
    rm -rf "$image" unpacked

    incus image export "$fingerprint"
    # shellcheck disable=2039,2034,2155,3043
    local exported="${fingerprint}.tar.xz"

    tar tvf "$exported" | grep -Fq metadata.yaml
    rm "$exported"
}

test_image_import_existing_alias() {
    ensure_import_testimage
    incus init testimage c
    incus publish c --alias newimage --alias image2
    incus delete c
    incus image export testimage testimage.file
    incus image delete testimage
    # the image can be imported with an existing alias
    incus image import testimage.file --alias newimage
    incus image delete newimage image2
}

test_image_refresh() {
  # shellcheck disable=2039,3043
  local INCUS2_DIR INCUS2_ADDR
  INCUS2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS2_DIR}"
  spawn_incus "${INCUS2_DIR}" true
  INCUS2_ADDR=$(cat "${INCUS2_DIR}/incus.addr")

  ensure_import_testimage

  token="$(INCUS_DIR=${INCUS2_DIR} incus config trust add foo -q)"
  incus_remote remote add l2 "${INCUS2_ADDR}" --accept-certificate --token "${token}"

  poolDriver="$(incus storage show "$(incus profile device get default root pool)" | awk '/^driver:/ {print $2}')"

  # Publish image
  incus image copy testimage l2: --alias testimage --public
  fp="$(incus image info l2:testimage | awk '/Fingerprint: / {print $2}')"
  incus image rm testimage

  # Create container from published image
  incus init l2:testimage c1

  # Create an alias for the received image
  incus image alias create testimage "${fp}"

  # Change image and publish it
  incus init l2:testimage l2:c1
  echo test | incus file push - l2:c1/tmp/testfile
  incus publish l2:c1 l2: --alias testimage --reuse --public
  new_fp="$(incus image info l2:testimage | awk '/Fingerprint: / {print $2}')"

  # Ensure the images differ
  [ "${fp}" != "${new_fp}" ]

  # Check original image exists before refresh.
  incus image info "${fp}"

  if [ "${poolDriver}" != "dir" ]; then
    # Check old storage volume record exists and new one doesn't.
    incus admin sql global 'select name from storage_volumes' | grep "${fp}"
    ! incus admin sql global 'select name from storage_volumes' | grep "${new_fp}" || false
  fi

  # Refresh image
  incus image refresh testimage

  # Ensure the old image is gone.
  ! incus image info "${fp}" || false

  if [ "${poolDriver}" != "dir" ]; then
    # Check old storage volume record has been replaced with new one.
    ! incus admin sql global 'select name from storage_volumes' | grep "${fp}" || false
    incus admin sql global 'select name from storage_volumes' | grep "${new_fp}"
  fi

  # Cleanup
  incus rm l2:c1
  incus rm c1
  incus remote rm l2
  kill_incus "${INCUS2_DIR}"
}
