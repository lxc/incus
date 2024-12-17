# Use the default project.
test_projects_default() {
  # The default project is used by the default profile
  incus project show default | grep -q "/1.0/profiles/default$"

  # Containers and images are assigned to the default project
  ensure_import_testimage
  incus init testimage c1
  incus project show default | grep -q "/1.0/profiles/default$"
  incus project show default | grep -q "/1.0/images/"
  incus delete c1
}

# CRUD operations on project.
test_projects_crud() {
  # Create a project
  incus project create foo

  # All features are enabled by default
  incus project show foo | grep -q 'features.images: "true"'
  incus project get foo "features.profiles" | grep -q 'true'

  # Trying to create a project with the same name fails
  ! incus project create foo || false

  # Trying to create a project containing an underscore fails
  ! incus project create foo_banned || false

  # Rename the project to a banned name fails
  ! incus project rename foo bar_banned || false

  # Rename the project and check it occurs
  incus project rename foo bar
  incus project show bar

  # Edit the project
  incus project show bar| sed 's/^description:.*/description: "Bar project"/' | incus project edit bar
  incus project show bar | grep -q "description: Bar project"

  # Create a second project
  incus project create foo

  # Trying to rename a project using an existing name fails
  ! incus project rename bar foo || false

  incus project switch foo

  # Turning off the profiles feature makes the project see the default profile
  # from the default project.
  incus project set foo features.profiles false
  incus profile show default | grep -E -q '^description: Default Incus profile$'

  # Turning on the profiles feature creates a project-specific default
  # profile.
  incus project set foo features.profiles true
  incus profile show default | grep -E -q '^description: Default Incus profile for project foo$'

  # Invalid config values are rejected.
  ! incus project set foo garbage xxx || false

  incus project switch default

  # Create a project with a description
  incus project create baz --description "Test description"
  incus project list | grep -q -F 'Test description'
  incus project show baz | grep -q -F 'description: Test description'

  # Delete the projects
  incus project delete foo
  incus project delete bar
  incus project delete baz

  # We're back to the default project
  [ "$(incus project get-current)" = "default" ]
}

# Use containers in a project.
test_projects_containers() {
  # Create a project and switch to it
  incus project create foo
  incus project switch foo

  deps/import-busybox --project foo --alias testimage
  fingerprint="$(incus image list -c f --format json | jq -r .[0].fingerprint)"

  # Add a root device to the default profile of the project
  pool="incustest-$(basename "${INCUS_DIR}")"
  incus profile device add default root disk path="/" pool="${pool}"

  # Create a container in the project
  incus init testimage c1

  # The container is listed when using this project
  incus list | grep -q c1
  incus info c1 | grep -q "Name: c1"

  # The container's volume is listed too.
  incus storage volume list "${pool}" | grep container | grep -q c1

  # For backends with optimized storage, we can see the image volume inside the
  # project.
  driver="$(storage_backend "$INCUS_DIR")"
  if [ "${driver}" != "dir" ]; then
      incus storage volume list "${pool}" | grep image | grep -q "${fingerprint}"
  fi

  # Start the container
  incus start c1
  incus list | grep c1 | grep -q RUNNING
  echo "abc" | incus exec c1 cat | grep -q abc

  # The container can't be managed when using the default project
  incus project switch default
  ! incus list | grep -q c1 || false
  ! incus info c1 || false
  ! incus delete c1 || false
  ! incus storage volume list "${pool}" | grep container | grep -q c1 || false

  # Trying to delete a project which is in use fails
  ! incus project delete foo || false

  # Trying to change features of a project which is in use fails
  ! incus project show foo| sed 's/features.profiles:.*/features.profiles: "false"/' | incus project edit foo || false
  ! incus project set foo "features.profiles" "false" || false
  incus project show foo | grep -q 'features.profiles: "true"'

  # Create a container with the same name in the default project
  ensure_import_testimage
  incus init testimage c1
  incus start c1
  incus list | grep c1 | grep -q RUNNING
  incus stop --force c1

  # Delete the container
  incus project switch foo

  incus stop --force c1
  incus delete c1
  incus image delete testimage

  # Delete the project
  incus project delete foo

  # The container in the default project can still be used
  incus start c1
  incus list | grep c1 | grep -q RUNNING
  incus stop --force c1
  incus delete c1
}

# Copy/move between projects
test_projects_copy() {
  ensure_import_testimage

  # Create a couple of projects
  incus project create foo -c features.profiles=false -c features.images=false
  incus project create bar -c features.profiles=false -c features.images=false

  # Create a container in the project
  incus --project foo init testimage c1
  incus --project foo copy c1 c1 --target-project bar
  incus --project bar start c1
  incus --project bar delete c1 -f

  incus --project foo snapshot c1
  incus --project foo snapshot c1
  incus --project foo snapshot c1

  incus --project foo copy c1/snap0 c1 --target-project bar
  incus --project bar start c1
  incus --project bar delete c1 -f

  incus --project foo copy c1 c1 --target-project bar
  incus --project foo start c1
  incus --project bar start c1

  incus --project foo delete c1 -f
  incus --project bar stop c1 -f
  incus --project bar move c1 c1 --target-project foo
  incus --project foo start c1
  incus --project foo delete c1 -f

  # Move storage volume between projects
  pool="incustest-$(basename "${INCUS_DIR}")"

  incus --project foo storage volume create "${pool}" vol1
  incus --project foo --target-project bar storage volume move "${pool}"/vol1 "${pool}"/vol1

  # Clean things up
  incus --project bar storage volume delete "${pool}" vol1
  incus project delete foo
  incus project delete bar
}

# Use snapshots in a project.
test_projects_snapshots() {
  # Create a project and switch to it
  incus project create foo
  incus project switch foo

  # Import an image into the project
  deps/import-busybox --project foo --alias testimage

  # Add a root device to the default profile of the project
  incus profile device add default root disk path="/" pool="incustest-$(basename "${INCUS_DIR}")"

  # Create a container in the project
  incus init testimage c1

  # Create, rename, restore and delete a snapshot
  incus snapshot create c1
  incus info c1 | grep -q snap0
  incus config show c1/snap0 | grep -q BusyBox
  incus snapshot rename c1 snap0 foo
  incus snapshot restore c1 foo
  incus snapshot delete c1 foo

  # Test copies
  incus snapshot create c1
  incus snapshot create c1
  incus copy c1 c2
  incus delete c2

  # Create a snapshot in this project and another one in the default project
  incus snapshot create c1

  incus project switch default
  ensure_import_testimage
  incus init testimage c1
  incus snapshot create c1
  incus delete c1

  # Switch back to the project
  incus project switch foo

  # Delete the container
  incus delete c1

  # Delete the project
  incus image delete testimage
  incus project delete foo
}

# Use backups in a project.
test_projects_backups() {
  # Create a project and switch to it
  incus project create foo
  incus project switch foo

  # Import an image into the project
  deps/import-busybox --project foo --alias testimage

  # Add a root device to the default profile of the project
  incus profile device add default root disk path="/" pool="incustest-$(basename "${INCUS_DIR}")"

  # Create a container in the project
  incus init testimage c1

  mkdir "${INCUS_DIR}/non-optimized"

  # Create a backup.
  incus export c1 "${INCUS_DIR}/c1.tar.gz"
  tar -xzf "${INCUS_DIR}/c1.tar.gz" -C "${INCUS_DIR}/non-optimized"

  # Check tarball content
  [ -f "${INCUS_DIR}/non-optimized/backup/index.yaml" ]
  [ -d "${INCUS_DIR}/non-optimized/backup/container" ]

  # Delete the container
  incus delete c1

  # Import the backup.
  incus import "${INCUS_DIR}/c1.tar.gz"
  incus info c1
  incus delete c1

  # Delete the project
  rm -rf "${INCUS_DIR}/non-optimized/"
  incus image delete testimage
  incus project delete foo
}

# Use private profiles in a project.
test_projects_profiles() {
  # Create a project and switch to it
  incus project create foo
  incus project switch foo

  # List profiles
  incus profile list | grep -q 'default'
  incus profile show default | grep -q 'description: Default Incus profile for project foo'

  # Create a profile in this project
  incus profile create p1
  incus profile list | grep -q 'p1'

  # Set a config key on this profile
  incus profile set p1 user.x y
  incus profile get p1 user.x | grep -q 'y'

  # The profile is not visible in the default project
  incus project switch default
  ! incus profile list | grep -q 'p1' || false

  # A profile with the same name can be created in the default project
  incus profile create p1

  # The same key can have a different value
  incus profile set p1 user.x z
  incus profile get p1 user.x | grep -q 'z'

  # Switch back to the project
  incus project switch foo

  # The profile has still the original config
  incus profile get p1 user.x | grep -q 'y'

  # Delete the profile from the project
  incus profile delete p1

  # Delete the project
  incus project delete foo

  # Delete the profile from the default project
  incus profile delete p1

  # Try project copy
  incus project create foo
  incus profile set --project default default user.x z
  incus profile copy --project default --target-project foo default bar
  # copy to an existing profile without --refresh should fail
  ! incus profile copy --project default --target-project foo default bar
  incus profile copy --project default --target-project foo default bar --refresh
  incus profile get --project foo bar user.x | grep -q 'z'
  incus profile copy --project default --target-project foo default bar-non-existent --refresh
  incus profile delete bar --project foo
  incus profile delete bar-non-existent --project foo
  incus project delete foo
}

# Use global profiles in a project.
test_projects_profiles_default() {
  # Create a new project, without the features.profiles config.
  incus project create -c features.profiles=false foo
  incus project switch foo

  # Import an image into the project and grab its fingerprint
  deps/import-busybox --project foo
  fingerprint="$(incus image list -c f --format json | jq .[0].fingerprint)"

  # Create a container
  incus init "${fingerprint}" c1

  # Switch back the default project
  incus project switch default

  # Try updating the default profile
  incus profile set default user.foo bar
  incus profile unset default user.foo

  # Create a container in the default project as well.
  ensure_import_testimage
  incus init testimage c1

  # If we look at the global profile we see that it's being used by both the
  # container in the above project and the one we just created.
  incus profile show default | grep -E -q '^- /1.0/instances/c1$'
  incus profile show default | grep -E -q '^- /1.0/instances/c1\?project=foo$'

  incus delete c1

  incus project switch foo

  # Delete the project
  incus delete c1
  incus image delete "${fingerprint}"
  incus project delete foo
}

# Use private images in a project.
test_projects_images() {
  # Create a project and switch to it
  incus project create foo
  incus project switch foo

  # Import an image into the project and grab its fingerprint
  deps/import-busybox --project foo
  fingerprint="$(incus image list -c f --format json | jq .[0].fingerprint)"

  # The imported image is not visible in the default project.
  incus project switch default
  ! incus image list | grep -q "${fingerprint}" || false

  # Switch back to the project and clean it up.
  incus project switch foo
  incus image delete "${fingerprint}"

  # Now Import an image into the project assigning it an alias
  deps/import-busybox --project foo --alias foo-image

  # The image alias shows up in the project
  incus image list | grep -q foo-image

  # However the image alias is not visible in the default project.
  incus project switch default
  ! incus image list | grep -q foo-project || false

  # Let's import the same image in the default project
  ensure_import_testimage

  # Switch back to the project.
  incus project switch foo

  # The image alias from the default project is not visible here
  ! incus image list | grep -q testimage || false

  # Rename the image alias in the project using the same it has in the default
  # one.
  incus image alias rename foo-image testimage

  # Create another alias for the image
  incus image alias create egg-image "${fingerprint}"

  # Delete the old alias
  incus image alias delete testimage

  # Delete the project and image altogether
  incus image delete egg-image
  incus project delete foo

  # We automatically switched to the default project, which still has the alias
  incus image list | grep -q testimage
}

# Use global images in a project.
test_projects_images_default() {
  # Make sure that there's an image in the default project
  ensure_import_testimage

  # Create a new project, without the features.images config.
  incus project create foo
  incus project switch foo
  incus project set foo "features.images" "false"

  # Create another project, without the features.images config.
  incus project create bar
  incus project set bar "features.images" "false"

  # The project can see images from the default project
  incus image list | grep -q testimage

  # The image from the default project has correct profile assigned
  fingerprint="$(incus image list --format json | jq -r .[0].fingerprint)"
  incus query "/1.0/images/${fingerprint}?project=foo" | jq -r ".profiles[0]" | grep -xq default

  # The project can delete images in the default project
  incus image delete testimage

  # Images imported into the project show up in the default project
  deps/import-busybox --project foo --alias foo-image
  incus image list | grep -q foo-image
  incus project switch default
  incus image list | grep -q foo-image

  # Correct profile assigned to images from another project
  fingerprint="$(incus image list --format json | jq -r '.[] | select(.aliases[0].name == "foo-image") | .fingerprint')"
  incus query "/1.0/images/${fingerprint}?project=bar" | jq -r ".profiles[0]" | grep -xq default

  incus image delete foo-image

  incus project delete bar
  incus project delete foo
}

# Interaction between projects and storage pools.
test_projects_storage() {
  pool="incustest-$(basename "${INCUS_DIR}")"

  incus storage volume create "${pool}" vol

  incus project create foo -c features.storage.volumes=false
  incus project switch foo

  incus storage volume list "${pool}" | grep custom | grep -q vol

  incus storage volume delete "${pool}" vol

  incus project switch default

  ! incus storage volume list "${pool}" | grep custom | grep -q vol || false

  incus project set foo features.storage.volumes=true
  incus storage volume create "${pool}" vol
  incus project switch foo
  ! incus storage volume list "${pool}" | grep custom | grep -q vol

  incus storage volume create "${pool}" vol
  incus storage volume delete "${pool}" vol

  incus storage volume create "${pool}" vol2
  incus project switch default
  ! incus storage volume list "${pool}" | grep custom | grep -q vol2

  incus project switch foo
  incus storage volume delete "${pool}" vol2

  incus project switch default
  incus storage volume delete "${pool}" vol
  incus project delete foo
}

# Interaction between projects and networks.
test_projects_network() {
  # Standard bridge with random subnet and a bunch of options
  network="inct$$"
  incus network create "${network}"

  incus project create foo
  incus project switch foo

  # Import an image into the project
  deps/import-busybox --project foo --alias testimage

  # Add a root device to the default profile of the project
  incus profile device add default root disk path="/" pool="incustest-$(basename "${INCUS_DIR}")"

  # Create a container in the project
  incus init -n "${network}" testimage c1

  incus network show "${network}" | grep -q "/1.0/instances/c1?project=foo"

  # Delete the container
  incus delete c1

  # Delete the project
  incus image delete testimage
  incus project delete foo

  incus network delete "${network}"
}

# Set resource limits on projects.
test_projects_limits() {
  # Create a project
  incus project create p1

  # Instance limits validation
  ! incus project set p1 limits.containers xxx || false
  ! incus project set p1 limits.virtual-machines -1 || false

  incus project switch p1

  # Add a root device to the default profile of the project and import an image.
  pool="incustest-$(basename "${INCUS_DIR}")"
  incus profile device add default root disk path="/" pool="${pool}"

  deps/import-busybox --project p1 --alias testimage

  # Test per-pool limits.
  incus storage create limit1 dir
  incus storage create limit2 dir

  incus project set p1 limits.disk=50MiB
  incus project set p1 limits.disk.pool.limit1=0
  incus project set p1 limits.disk.pool.limit2=0

  ! incus storage list | grep -q limit1 || false
  ! incus storage list | grep -q limit2 || false

  incus storage volume create "${pool}" foo size=10MiB
  ! incus storage volume create "${pool}" bar size=50MiB || false
  incus storage volume delete "${pool}" foo

  ! incus storage volume create limit1 foo size=10GiB || false
  ! incus storage volume create limit2 foo size=10GiB || false

  incus project set p1 limits.disk.pool.limit1=10MiB
  incus project set p1 limits.disk.pool.limit2=10MiB
  incus storage volume create limit1 foo size=10MiB
  ! incus storage volume create limit1 bar size=10MiB || false
  incus storage volume create limit2 foo size=10MiB
  ! incus storage volume create limit2 bar size=10MiB || false

  ! incus storage volume create "${pool}" foo size=40MiB || false
  incus storage volume delete limit1 foo
  incus storage volume delete limit2 foo
  incus storage volume create "${pool}" foo size=40MiB

  incus storage volume delete "${pool}" foo
  incus project unset p1 limits.disk.pool.limit1
  incus project unset p1 limits.disk.pool.limit2
  incus project unset p1 limits.disk
  incus storage delete limit1
  incus storage delete limit2

  # Create a couple of containers in the project.
  incus init testimage c1
  incus init testimage c2

  # Can't set the containers limit below the current count.
  ! incus project set p1 limits.containers 1 || false

  # Can't create containers anymore after the limit is reached.
  incus project set p1 limits.containers 2
  ! incus init testimage c3 || false

  # Can't set the project's memory limit to a percentage value.
  ! incus project set p1 limits.memory 10% || false

  # Can't set the project's memory limit because not all instances have
  # limits.memory defined.
  ! incus project set p1 limits.memory 10GiB || false

  # Set limits.memory on the default profile.
  incus profile set default limits.memory 1GiB

  # Can't set the memory limit below the current total usage.
  ! incus project set p1 limits.memory 1GiB || false

  # Configure a valid project memory limit.
  incus project set p1 limits.memory 3GiB

  # Validate that snapshots don't fail with limits.
  incus snapshot create c2
  incus snapshot restore c2 snap0

  incus delete c2

  # Create a new profile which does not define "limits.memory".
  incus profile create unrestricted
  incus profile device add unrestricted root disk path="/" pool="${pool}"

  # Can't create a new container without defining "limits.memory"
  ! incus init testimage c2 -p unrestricted || false

  # Can't create a new container if "limits.memory" is too high
  ! incus init testimage c2 -p unrestricted -c limits.memory=4GiB || false

  # Can't create a new container if "limits.memory" is a percentage
  ! incus init testimage c2 -p unrestricted -c limits.memory=10% || false

  # No error occurs if we define "limits.memory" and stay within the limits.
  incus init testimage c2 -p unrestricted -c limits.memory=1GiB

  # Can't change the container's "limits.memory" if it would overflow the limit.
  ! incus config set c2 limits.memory=4GiB || false

  # Can't unset the instance's "limits.memory".
  ! incus config unset c2 limits.memory || false

  # Can't unset the default profile's "limits.memory", as it would leave c1
  # without an effective "limits.memory".
  ! incus profile unset default limits.memory || false

  # Can't check the default profile's "limits.memory" to a value that would
  # violate project's limits.
  ! incus profile set default limits.memory=4GiB || false

  # Can't change limits.memory to a percentage.
  ! incus profile set default limits.memory=10% || false
  ! incus config set c2 limits.memory=10% || false

  # It's possible to change both a profile and an instance memory limit, if they
  # don't break the project's aggregate allowance.
  incus profile set default limits.memory=2GiB
  incus config set c2 limits.memory=512MiB

  # Can't set the project's processes limit because no instance has
  # limits.processes defined.
  ! incus project set p1 limits.processes 100 || false

  # Set processes limits on the default profile and on c2.
  incus profile set default limits.processes=50
  incus config set c2 limits.processes=50

  # Can't set the project's processes limit if it's below the current total.
  ! incus project set p1 limits.processes 75 || false

  # Set the project's processes limit.
  incus project set p1 limits.processes 150

  # Changing profile and instance processes limits within the aggregate
  # project's limit is fine.
  incus profile set default limits.processes=75
  incus config set c2 limits.processes=75

  # Changing profile and instance processes limits above the aggregate project's
  # limit is not possible.
  ! incus profile set default limits.processes=80 || false
  ! incus config set c2 limits.processes=80 || false

  # Changing the project's processes limit below the current aggregate amount is
  # not possible.
  ! incus project set p1 limits.processes 125 || false

  # Set a cpu limit on the default profile and on the instance, with c2
  # using CPU pinning.
  incus profile set default limits.cpu=2
  incus config set c2 limits.cpu=0,1

  # It's not possible to set the project's cpu limit since c2 is using CPU
  # pinning.
  ! incus project set p1 limits.cpu 4 || false

  # Change c2's from cpu pinning to a regular cpu count limit.
  incus config set c2 limits.cpu=2

  # Can't set the project's cpu limit below the current aggregate count.
  ! incus project set p1 limits.cpu 3 || false

  # Set the project's cpu limit
  incus project set p1 limits.cpu 4

  # Can't update the project's cpu limit below the current aggregate count.
  ! incus project set p1 limits.cpu 3 || false

  # Changing profile and instance cpu limits above the aggregate project's
  # limit is not possible.
  ! incus profile set default limits.cpu=3 || false
  ! incus config set c2 limits.cpu=3 || false

  # CPU limits can be updated if they stay within limits.
  incus project set p1 limits.cpu 7
  incus profile set default limits.cpu=3
  incus config set c2 limits.cpu=3

  # Can't set the project's disk limit because not all instances have
  # the "size" config defined on the root device.
  ! incus project set p1 limits.disk 1GiB || false

  # Set a disk limit on the default profile and also on instance c2
  incus profile device set default root size=100MiB
  incus config device add c2 root disk path="/" pool="${pool}" size=50MiB

  if [ "${INCUS_BACKEND}" = "lvm" ]; then
    # Can't set the project's disk limit because not all volumes have
    # the "size" config defined.
    pool1="inctest1-$(basename "${INCUS_DIR}")"
    incus storage create "${pool1}" lvm size=1GiB
    incus storage volume create "${pool1}" v1
    ! incus project set p1 limits.disk 1GiB || false
    incus storage volume delete "${pool1}" v1
    incus storage delete "${pool1}"
  fi

  # Create a custom volume without any size property defined.
  incus storage volume create "${pool}" v1

  # Set a size on the custom volume.
  incus storage volume set "${pool}" v1 size 50MiB

  # Can't set the project's disk limit below the current aggregate count.
  ! incus project set p1 limits.disk 190MiB || false

  # Set the project's disk limit
  incus project set p1 limits.disk 250MiB

  # Can't update the project's disk limit below the current aggregate count.
  ! incus project set p1 limits.disk 190MiB || false

  # Changing profile or instance root device size or volume size above the
  # aggregate project's limit is not possible.
  ! incus profile device set default root size=160MiB || false
  ! incus config device set c2 root size 110MiB || false
  ! incus storage volume set "${pool}" v1 size 110MiB || false

  # Can't create a custom volume without specifying a size.
  ! incus storage volume create "${pool}" v2 || false

  # Disk limits can be updated if they stay within limits.
  incus project set p1 limits.disk 204900KiB
  incus profile device set default root size=90MiB
  incus config device set c2 root size 60MiB

  # Can't upload an image if that would exceed the current quota.
  ! deps/import-busybox --project p1 --template start --alias otherimage || false

  # Can't export publish an instance as image if that would exceed the current
  # quota.
  ! incus publish c1 --alias=c1image || false

  # Run the following part of the test only against the dir or zfs backend,
  # since it on other backends it requires resize the rootfs to a value which is
  # too small for resize2fs.
  if [ "${INCUS_BACKEND}" = "dir" ] || [ "${INCUS_BACKEND}" = "zfs" ]; then
    # Add a remote Incus to be used as image server.
    # shellcheck disable=2039,3043
    local INCUS_REMOTE_DIR
    INCUS_REMOTE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${INCUS_REMOTE_DIR}"

    # Switch to default project to spawn new Incus server, and then switch back to p1.
    incus project switch default
    spawn_incus "${INCUS_REMOTE_DIR}" true
    incus project switch p1

    INCUS_REMOTE_ADDR=$(cat "${INCUS_REMOTE_DIR}/incus.addr")
    (INCUS_DIR=${INCUS_REMOTE_DIR} deps/import-busybox --alias remoteimage --template start --public)

    token="$(INCUS_DIR=${INCUS_REMOTE_DIR} incus config trust add foo -q)"
    incus remote add l2 "${INCUS_REMOTE_ADDR}" --accept-certificate --token "${token}"

    # Relax all constraints except the disk limits, which won't be enough for the
    # image to be downloaded.
    incus profile device set default root size=500KiB
    incus project set p1 limits.disk 111MiB
    incus project unset p1 limits.containers
    incus project unset p1 limits.cpu
    incus project unset p1 limits.memory
    incus project unset p1 limits.processes

    # Can't download a remote image if that would exceed the current quota.
    ! incus init l2:remoteimage c3 || false
  fi

  incus storage volume delete "${pool}" v1
  incus delete c1
  incus delete c2
  incus image delete testimage
  incus profile delete unrestricted

  incus project switch default
  incus project delete p1

  # Start with clean project.
  incus project create p1
  incus project switch p1

  # Relaxing restricted.containers.lowlevel to 'allow' makes it possible set
  # low-level keys.
  incus project set p1 restricted.containers.lowlevel allow

  # Add a root device to the default profile of the project and import an image.
  pool="incustest-$(basename "${INCUS_DIR}")"
  incus profile device add default root disk path="/" pool="${pool}"

  deps/import-busybox --project p1 --alias testimage

  # Create a couple of containers in the project.
  incus init testimage c1 -c limits.memory=1GiB
  incus init testimage c2 -c limits.memory=1GiB

  incus export c1
  incus delete c1

  # Configure a valid project memory limit.
  incus project set p1 limits.memory 1GiB

  # Can't import the backup as it would exceed the 1GiB project memory limit.
  ! incus import c1.tar.gz || false

  rm c1.tar.gz
  incus delete c2
  incus image delete testimage
  incus project switch default
  incus project delete p1

  if [ "${INCUS_BACKEND}" = "dir" ] || [ "${INCUS_BACKEND}" = "zfs" ]; then
    incus remote remove l2
    kill_incus "$INCUS_REMOTE_DIR"
  fi
}

# Set restrictions on projects.
test_projects_restrictions() {
  # Add a managed network.
  netManaged="inc$$"
  incus network create "${netManaged}"

  netUnmanaged="${netManaged}-unm"
  ip link add "${netUnmanaged}" type bridge

  # Create a project and switch to it
  incus project create p1 -c features.storage.volumes=false
  incus project switch p1

  # Check with restricted unset and restricted.devices.nic unset that managed & unmanaged networks are accessible.
  incus network list | grep -F "${netManaged}"
  incus network list | grep -F "${netUnmanaged}"
  incus network show "${netManaged}"
  incus network show "${netUnmanaged}"

  # Check with restricted unset and restricted.devices.nic=block that managed & unmanaged networks are accessible.
  incus project set p1 restricted.devices.nic=block
  incus network list | grep -F "${netManaged}"
  incus network list | grep -F "${netUnmanaged}"
  incus network show "${netManaged}"
  incus network show "${netUnmanaged}"

  # Check with restricted=true and restricted.devices.nic=block that managed & unmanaged networks are inaccessible.
  incus project set p1 restricted=true
  ! incus network list | grep -F "${netManaged}"|| false
  ! incus network show "${netManaged}" || false
  ! incus network list | grep -F "${netUnmanaged}"|| false
  ! incus network show "${netUnmanaged}" || false

  # Check with restricted=true and restricted.devices.nic=managed that managed networks are accessible and that
  # unmanaged networks are inaccessible.
  incus project set p1 restricted.devices.nic=managed
  incus network list | grep -F "${netManaged}"
  incus network show "${netManaged}"
  ! incus network list | grep -F "${netUnmanaged}"|| false
  ! incus network show "${netUnmanaged}" || false

  # Check with restricted.devices.nic=allow and restricted.networks.access set to a network other than the existing
  # managed and unmanaged ones that they are inaccessible.
  incus project set p1 restricted.devices.nic=allow
  incus project set p1 restricted.networks.access=foo
  ! incus network list | grep -F "${netManaged}"|| false
  ! incus network show "${netManaged}" || false
  ! incus network info "${netManaged}"|| false

  ! incus network list | grep -F "${netUnmanaged}"|| false
  ! incus network show "${netUnmanaged}" || false
  ! incus network info "${netUnmanaged}"|| false

  ! incus network set "${netManaged}" user.foo=bah || false
  ! incus network get "${netManaged}" ipv4.address || false
  ! incus network info "${netManaged}"|| false
  ! incus network delete "${netManaged}" || false

  ! incus profile device add default eth0 nic nictype=bridge parent=netManaged || false
  ! incus profile device add default eth0 nic nictype=bridge parent=netUnmanaged || false

  ip link delete "${netUnmanaged}"

  # Disable restrictions to allow devices to be added to profile.
  incus project unset p1 restricted.networks.access
  incus project set p1 restricted.devices.nic=managed
  incus project set p1 restricted=false

  # Add a root device to the default profile of the project and import an image.
  pool="incustest-$(basename "${INCUS_DIR}")"
  incus profile device add default root disk path="/" pool="${pool}"

  deps/import-busybox --project p1 --alias testimage
  fingerprint="$(incus image list -c f --format json | jq -r .[0].fingerprint)"

  # Add a volume.
  incus storage volume create "${pool}" "v-proj$$"

  # Enable all restrictions.
  incus project set p1 restricted=true

  # It's not possible to create nested containers.
  ! incus profile set default security.nesting=true || false
  ! incus init testimage c1 -c security.nesting=true || false

  # It's not possible to use forbidden low-level options
  ! incus profile set default "raw.idmap=both 0 0" || false
  ! incus init testimage c1 -c "raw.idmap=both 0 0" || false
  ! incus init testimage c1 -c volatile.uuid="foo" || false

  # It's not possible to create privileged containers.
  ! incus profile set default security.privileged=true || false
  ! incus init testimage c1 -c security.privileged=true || false

  # It's possible to create non-isolated containers.
  incus init testimage c1 -c security.idmap.isolated=false

  # It's not possible to change low-level options
  ! incus config set c1 "raw.idmap=both 0 0" || false
  ! incus config set c1 volatile.uuid="foo" || false

  # It's not possible to attach character devices.
  ! incus profile device add default tty unix-char path=/dev/ttyS0 || false
  ! incus config device add c1 tty unix-char path=/dev/ttyS0 || false

  # It's not possible to attach raw network devices.
  ! incus profile device add default eth0 nic nictype=p2p || false

  # It's not possible to attach non-managed disk devices.
  ! incus profile device add default testdir disk source="${TEST_DIR}" path=/mnt || false
  ! incus config device add c1 testdir disk source="${TEST_DIR}" path=/mnt || false

  # It's possible to attach managed network devices.
  incus profile device add default eth0 nic network="${netManaged}"

  # It's possible to attach disks backed by a pool.
  incus config device add c1 data disk pool="${pool}" path=/mnt source="v-proj$$"

  # It's not possible to set restricted.containers.nic to 'block' because
  # there's an instance using the managed network.
  ! incus project set p1 restricted.devices.nic=block || false

  # Relaxing restricted.containers.nic to 'allow' makes it possible to attach
  # raw network devices.
  incus project set p1 restricted.devices.nic=allow
  incus config device add c1 eth1 nic nictype=p2p

  # Relaxing restricted.containers.disk to 'allow' makes it possible to attach
  # non-managed disks.
  incus project set p1 restricted.devices.disk=allow
  incus config device add c1 testdir disk source="${TEST_DIR}" path=/foo

  # Relaxing restricted.containers.lowlevel to 'allow' makes it possible set
  # low-level keys.
  incus project set p1 restricted.containers.lowlevel=allow
  incus config set c1 "raw.idmap=both 0 0"

  incus delete c1

  # Setting restricted.containers.disk to 'block' allows only the root disk
  # device.
  incus project set p1 restricted.devices.disk=block
  ! incus profile device add default data disk pool="${pool}" path=/mnt source="v-proj$$" || false

  # Setting restricted.containers.nesting to 'allow' makes it possible to create
  # nested containers.
  incus project set p1 restricted.containers.nesting=allow
  incus init testimage c1 -c security.nesting=true

  # It's not possible to set restricted.containers.nesting back to 'block',
  # because there's an instance with security.nesting=true.
  ! incus project set p1 restricted.containers.nesting=block || false

  incus delete c1

  # Setting restricted.containers.lowlevel to 'allow' makes it possible to set
  # low-level options.
  incus project set p1 restricted.containers.lowlevel=allow
  incus init testimage c1 -c "raw.idmap=both 0 0" || false

  # It's not possible to set restricted.containers.lowlevel back to 'block',
  # because there's an instance with raw.idmap set.
  ! incus project set p1 restricted.containers.lowlevel=block || false

  incus delete c1

  # Setting restricted.containers.privilege to 'allow' makes it possible to create
  # privileged containers.
  incus project set p1 restricted.containers.privilege=allow
  incus init testimage c1 -c security.privileged=true

  # It's not possible to set restricted.containers.privilege back to
  # 'unprivileged', because there's an instance with security.privileged=true.
  ! incus project set p1 restricted.containers.privilege=unprivileged || false

  # Test expected syscall interception behavior.
  ! incus config set c1 security.syscalls.intercept.mknod=true || false
  incus config set c1 security.syscalls.intercept.mknod=false
  incus project set p1 restricted.containers.interception=block
  ! incus config set c1 security.syscalls.intercept.mknod=true || false
  incus project set p1 restricted.containers.interception=allow
  incus config set c1 security.syscalls.intercept.mknod=true
  incus config set c1 security.syscalls.intercept.mount=true
  ! incus config set c1 security.syscalls.intercept.mount.allow=ext4 || false

  incus delete c1

  incus image delete testimage

  incus project switch default
  incus project delete p1

  incus network delete "${netManaged}"
  incus storage volume delete "${pool}" "v-proj$$"
}

# Test project state api
test_projects_usage() {
  # Set configuration on the default project
  incus project create test-usage \
    -c limits.cpu=5 \
    -c limits.memory=1GiB \
    -c limits.disk=10GiB \
    -c limits.networks=3 \
    -c limits.processes=40

  # Create a profile defining resource allocations
  incus profile show default --project default | incus profile edit default --project test-usage
  incus profile set default --project test-usage \
    limits.cpu=1 \
    limits.memory=512MiB \
    limits.processes=20
  incus profile device set default root size=300MiB --project test-usage

  # Spin up a container
  deps/import-busybox --project test-usage --alias testimage
  incus init testimage c1 --project test-usage
  incus project info test-usage

  incus project info test-usage --format csv | grep -q "CONTAINERS,UNLIMITED,1"
  incus project info test-usage --format csv | grep -q "CPU,5,1"
  incus project info test-usage --format csv | grep -q "DISK,10.00GiB,300.00MiB"
  incus project info test-usage --format csv | grep -q "INSTANCES,UNLIMITED,1"
  incus project info test-usage --format csv | grep -q "MEMORY,1.00GiB,512.00MiB"
  incus project info test-usage --format csv | grep -q "NETWORKS,3,0"
  incus project info test-usage --format csv | grep -q "PROCESSES,40,20"
  incus project info test-usage --format csv | grep -q "VIRTUAL-MACHINES,UNLIMITED,0"

  incus delete c1 --project test-usage
  incus image delete testimage --project test-usage
  incus project delete test-usage
}
