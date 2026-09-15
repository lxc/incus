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
    if [ "${incus_backend}" = "linstor" ]; then
        incus storage create "${pool2}" "${incus_backend}" linstor.resource_group.place_count=1
    elif [ "$incus_backend" = "truenas" ]; then
        incus storage create "${pool2}" "${incus_backend}" "$(truenas_source)/$(uuidgen)" "$(truenas_config)" "$(truenas_allow_insecure)" "$(truenas_api_key)"
    else
        incus storage create "${pool2}" "${incus_backend}"
    fi

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
    [ "$(incus ls --project ${project} --format csv --columns n)" = "c2" ]                   # Verify new project.
    [ "$(incus config device get c2 root pool --project ${project})" = "${pool}" ]           # Verify same pool (new local device).
    [ "$(incus ls --project "${project}" -c nP -f csv | awk -F, '/c2/ { print $2 }')" = "" ] # Verify no profiles are applied.
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

    # An attached custom volume can't follow the instance into another volume project.
    incus storage volume create "${pool}" vol1
    incus init "${image}" c20
    incus config device add c20 d1 disk pool="${pool}" source=vol1 path=/mnt
    ! incus move c20 --target-project "${project}" || false # Err: volume isn't available in the target project
    incus config device remove c20 d1
    incus move c20 --target-project "${project}"
    [ "$(incus ls --project ${project} --format csv --columns n)" = "c20" ] # Verify new project.
    incus delete -f c20 --project "${project}"

    # It can when both projects share the default volume project.
    incus project create novolproject -c features.storage.volumes=false
    incus init "${image}" c21
    incus config device add c21 d1 disk pool="${pool}" source=vol1 path=/mnt
    incus move c21 --target-project novolproject
    incus storage volume show "${pool}" vol1 --project novolproject > /dev/null # Verify same volume.
    incus start c21 --project novolproject
    incus delete -f c21 --project novolproject
    incus project delete novolproject
    incus storage volume delete "${pool}" vol1

    # An instance with backups can't change project.
    incus init "${image}" c22
    incus query -X POST --wait -d '{\"name\":\"bak0\"}' /1.0/instances/c22/backups
    ! incus move c22 --target-project "${project}" || false # Err: Instances with backups cannot be moved
    incus delete -f c22

    # A dependent volume follows the instance into a project with its own volumes.
    incus storage volume create "${pool}" dvol
    incus init "${image}" c24
    incus config device add c24 dsk disk pool="${pool}" source=dvol path=/mnt dependent=true
    incus move c24 --target-project "${project}"
    incus storage volume show "${pool}" dvol --project "${project}" > /dev/null # Verify the volume moved.
    ! incus storage volume show "${pool}" dvol > /dev/null 2>&1 || false        # Verify it left the source project.
    incus start c24 --project "${project}"
    incus stop -f c24 --project "${project}"
    incus delete -f c24 --project "${project}"

    # It can't when both projects share their storage volumes.
    incus project create sharedvolumes -c features.storage.volumes=false
    incus storage volume create "${pool}" dvol2
    incus init "${image}" c25
    incus config device add c25 dsk disk pool="${pool}" source=dvol2 path=/mnt dependent=true
    incus move c25 --target-project sharedvolumes 2>&1 | grep -q "as both projects share their storage volumes"
    incus delete -f c25
    incus project delete sharedvolumes

    # A live project change needs a cluster and a target member.
    incus launch "${image}" c23
    incus move c23 --target-project "${project}" 2>&1 | grep -q "Live project changes aren't supported on standalone systems"
    incus move c23 --target-project "${project}" --stateless 2>&1 | grep -q "Instance must be stopped for a stateless move across projects"
    incus delete -f c23

    # Near-live migration.
    incus launch "${image}" c9
    incus config set c9 boot.host_shutdown_timeout=1

    # --refresh can be called only with --stateless.
    ! incus move c9 --target-project "${project}" --refresh || false
    incus query -X POST -d '{\"migration\": true, \"live\": true, \"refresh\": true}' /1.0/instances/c9 2>&1 | grep -q "Refresh migration can't be used with a stateful migration"

    # A local rename can't be performed as near-live migration.
    ! incus move c9 c10 --stateless --refresh || false

    # A running container can't change project, name or storage pool.
    ! incus move c9 --target-project "${project}" --stateless --refresh || false
    ! incus move c9 --storage "${pool2}" --stateless --refresh || false

    # The evacuation mode is a valid instance configuration value.
    incus config set c9 cluster.evacuate=refresh-migrate

    incus delete -f c9

    incus profile delete "${profile}" --project "${project}"
    incus storage delete "${pool2}"
    incus project delete "${project}"
}
