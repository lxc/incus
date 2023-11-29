test_container_move() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  incus_backend=$(storage_backend "$INCUS_DIR")
  pool=$(incus profile device get default root pool)
  pool2="test-pool"
  image="testimage"
  project="test-project"
  profile="test-profile"

  # Setup.
  incus project create "${project}" --
  incus storage create "${pool2}" "${incus_backend}"
  incus profile create "${profile}"
  incus profile device add default root disk pool="${pool2}" path=/ --project "${project}"

  # Move project, verify root disk device is retained.
  incus init "${image}" c1
  incus move c1 --target-project "${project}"
  [ "$(incus ls --project ${project} --format csv --columns n)" = "c1" ]         # Verify project.
  [ "$(incus config device get c1 root pool --project ${project})" = "${pool}" ] # Verify pool is retained.
  incus delete -f c1 --project "${project}"

  # Move to different storage pool.
  incus init "${image}" c2
  incus move c2 --storage "${pool2}"
  [ "$(incus ls --format csv --columns n)" = "c2" ]          # Verify project.
  [ "$(incus config device get c2 root pool)" = "${pool2}" ] # Verify pool.
  incus delete -f c2

  # Move to different storage pool and project.
  incus init "${image}" c3
  incus move c3 --target-project "${project}" --storage "${pool2}"
  [ "$(incus ls --project ${project} --format csv --columns n)" = "c3" ]          # Verify project.
  [ "$(incus config device get c3 root pool --project ${project})" = "${pool2}" ] # Verify pool.
  incus delete -f c3 --project "${project}"

  # Ensure profile is not retained.
  incus init "${image}" c4 --profile default --profile "${profile}"
  ! incus move c4 --target-project "${project}" # Err: Profile not found in target project
  incus delete -f c4

  # Create matching profile in target project and ensure it is applied on move.
  incus profile create "${profile}" --project "${project}"
  incus profile set "${profile}" user.foo="test" --project "${project}"
  incus init "${image}" c5 --profile default --profile "${profile}"
  incus move c5 --target-project "${project}"
  [ "$(incus ls --project ${project} --format csv --columns n)" = "c5" ] # Verify project.
  [ "$(incus config get c5 user.foo -e --project ${project})" = "test" ] # Verify pool.
  incus delete -f c5 --project "${project}"

  # Cleanup.
  incus profile device remove default root --project "${project}"
  incus profile delete "${profile}" --project "${project}"
  incus profile delete "${profile}"
  incus storage delete "${pool2}"
  incus project delete "${project}"
}
