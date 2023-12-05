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
  incus project create "${project}"
  incus storage create "${pool2}" "${incus_backend}"
  incus profile create "${profile}" --project "${project}"
  incus profile device add "${profile}" root disk pool="${pool2}" path=/ --project "${project}"

  # Move to different project with same profile (root disk device and profile are retained).
  incus init "${image}" c1
  incus move c1 --target-project "${project}"
  [ "$(incus ls --project ${project} --format csv --columns n)" = "c1" ]                          # Verify new project.
  [ "$(incus config device get c1 root pool --project ${project})" = "${pool}" ]                  # Verify same pool (new local device).
  [ "$(incus ls --project "${project}" -c nP -f csv | awk -F, '/c1/ { print $2 }')" = "default" ] # Verify profile is retained.
  incus delete -f c1 --project "${project}"

  # Move to different project with no profiles (root disk device is retained).
  incus init "${image}" c2
  incus move c2 --target-project "${project}" --no-profiles
  [ "$(incus ls --project ${project} --format csv --columns n)" = "c2" ]                    # Verify new project.
  [ "$(incus config device get c2 root pool --project ${project})" = "${pool}" ]            # Verify same pool (new local device).
  [ "$(incus ls --project "${project}" -c nP -f csv | awk -F, '/c2/ { print $2 }')" = "" ]  # Verify no profiles are applied.
  incus delete -f c2 --project "${project}"

  # Move to different project with new profiles (root disk device is retained).
  incus init "${image}" c3
  incus move c3 --target-project "${project}" --profile "${profile}"
  [ "$(incus ls --project ${project} --format csv --columns n)" = "c3" ]         # Verify new project.
  [ "$(incus config device get c3 root pool --project ${project})" = "${pool}" ] # Verify same pool (new local device).
  incus config show c3 -e --project "${project}" | grep -- "- ${profile}"        # Verify new profile.
  incus delete -f c3 --project "${project}"

  # Move to different project with non-existing profile.
  incus init "${image}" c4
  ! incus move c4 --target-project "${project}" --profile invalid # Err: Profile not found in target project
  incus delete -f c4

  # Move to different storage pool.
  incus init "${image}" c5
  incus move c5 --storage "${pool2}"
  [ "$(incus ls --format csv --columns n)" = "c5" ]          # Verify same project.
  [ "$(incus config device get c5 root pool)" = "${pool2}" ] # Verify new pool.
  incus delete -f c5

  # Move to different project and storage pool.
  incus init "${image}" c6
  incus move c6 --target-project "${project}" --storage "${pool2}"
  [ "$(incus ls --project ${project} --format csv --columns n)" = "c6" ]          # Verify new project.
  [ "$(incus config device get c6 root pool --project ${project})" = "${pool2}" ] # Verify new pool.
  incus delete -f c6 --project "${project}"

  # Move to different project and overwrite storage pool using device entry.
  incus init "${image}" c7 --storage "${pool}" --no-profiles
  incus move c7 --target-project "${project}" --device "root,pool=${pool2}"
  [ "$(incus ls --project ${project} --format csv --columns n)" = "c7" ]          # Verify new project.
  [ "$(incus config device get c7 root pool --project ${project})" = "${pool2}" ] # Verify new pool.
  incus delete -f c7 --project "${project}"

  # Move to different project and apply config entry.
  incus init "${image}" c8
  incus move c8 --target-project "${project}" --config user.test=success
  [ "$(incus ls --project ${project} --format csv --columns n)" = "c8" ]  # Verify new project.
  [ "$(incus config get c8 user.test --project ${project})" = "success" ] # Verify new local config entry.
  incus delete -f c8 --project "${project}"

  incus profile delete "${profile}" --project "${project}"
  incus storage delete "${pool2}"
  incus project delete "${project}"
}
