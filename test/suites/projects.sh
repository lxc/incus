# Use the default project.
test_projects_default() {
  # The default project is used by the default profile
  lxc project show default | grep -q "/1.0/profiles/default$"

  # Containers and images are assigned to the default project
  ensure_import_testimage
  lxc init testimage c1
  lxc project show default | grep -q "/1.0/profiles/default$"
  lxc project show default | grep -q "/1.0/images/"
  lxc delete c1
}

# CRUD operations on project.
test_projects_crud() {
  # Create a project
  lxc project create foo

  # All features are enabled by default
  lxc project show foo | grep -q 'features.images: "true"'
  lxc project get foo "features.profiles" | grep -q 'true'

  # Trying to create a project with the same name fails
  ! lxc project create foo || false

  # Trying to create a project containing an underscore fails
  ! lxc project create foo_banned || false

  # Rename the project to a banned name fails
  ! lxc project rename foo bar_banned || false

  # Rename the project and check it occurs
  lxc project rename foo bar
  lxc project show bar

  # Edit the project
  lxc project show bar| sed 's/^description:.*/description: "Bar project"/' | lxc project edit bar
  lxc project show bar | grep -q "description: Bar project"

  # Create a second project
  lxc project create foo

  # Trying to rename a project using an existing name fails
  ! lxc project rename bar foo || false

  lxc project switch foo

  # Turning off the profiles feature makes the project see the default profile
  # from the default project.
  lxc project set foo features.profiles false
  lxc profile show default | grep -E -q '^description: Default LXD profile$'

  # Turning on the profiles feature creates a project-specific default
  # profile.
  lxc project set foo features.profiles true
  lxc profile show default | grep -E -q '^description: Default LXD profile for project foo$'

  # Invalid config values are rejected.
  ! lxc project set foo garbage xxx || false

  lxc project switch default

  # Delete the projects
  lxc project delete foo
  lxc project delete bar

  # We're back to the default project
  lxc project list | grep -q "default (current)"
}

# Use containers in a project.
test_projects_containers() {
  # Create a project and switch to it
  lxc project create foo
  lxc project switch foo

  deps/import-busybox --project foo --alias testimage
  fingerprint="$(lxc image list -c f --format json | jq -r .[0].fingerprint)"

  # Add a root device to the default profile of the project
  pool="incustest-$(basename "${INCUS_DIR}")"
  lxc profile device add default root disk path="/" pool="${pool}"

  # Create a container in the project
  lxc init testimage c1

  # The container is listed when using this project
  lxc list | grep -q c1
  lxc info c1 | grep -q "Name: c1"

  # The container's volume is listed too.
  lxc storage volume list "${pool}" | grep container | grep -q c1

  # For backends with optimized storage, we can see the image volume inside the
  # project.
  driver="$(storage_backend "$INCUS_DIR")"
  if [ "${driver}" != "dir" ]; then
      lxc storage volume list "${pool}" | grep image | grep -q "${fingerprint}"
  fi

  # Start the container
  lxc start c1
  lxc list | grep c1 | grep -q RUNNING
  echo "abc" | lxc exec c1 cat | grep -q abc

  # The container can't be managed when using the default project
  lxc project switch default
  ! lxc list | grep -q c1 || false
  ! lxc info c1 || false
  ! lxc delete c1 || false
  ! lxc storage volume list "${pool}" | grep container | grep -q c1 || false

  # Trying to delete a project which is in use fails
  ! lxc project delete foo || false

  # Trying to change features of a project which is in use fails
  ! lxc project show foo| sed 's/features.profiles:.*/features.profiles: "false"/' | lxc project edit foo || false
  ! lxc project set foo "features.profiles" "false" || false
  lxc project show foo | grep -q 'features.profiles: "true"'

  # Create a container with the same name in the default project
  ensure_import_testimage
  lxc init testimage c1
  lxc start c1
  lxc list | grep c1 | grep -q RUNNING
  lxc stop --force c1

  # Delete the container
  lxc project switch foo

  lxc stop --force c1
  lxc delete c1
  lxc image delete testimage

  # Delete the project
  lxc project delete foo

  # The container in the default project can still be used
  lxc start c1
  lxc list | grep c1 | grep -q RUNNING
  lxc stop --force c1
  lxc delete c1
}

# Copy/move between projects
test_projects_copy() {
  ensure_import_testimage

  # Create a couple of projects
  lxc project create foo -c features.profiles=false -c features.images=false
  lxc project create bar -c features.profiles=false -c features.images=false

  # Create a container in the project
  lxc --project foo init testimage c1
  lxc --project foo copy c1 c1 --target-project bar
  lxc --project bar start c1
  lxc --project bar delete c1 -f

  lxc --project foo snapshot c1
  lxc --project foo snapshot c1
  lxc --project foo snapshot c1

  lxc --project foo copy c1/snap0 c1 --target-project bar
  lxc --project bar start c1
  lxc --project bar delete c1 -f

  lxc --project foo copy c1 c1 --target-project bar
  lxc --project foo start c1
  lxc --project bar start c1

  lxc --project foo delete c1 -f
  lxc --project bar stop c1 -f
  lxc --project bar move c1 c1 --target-project foo
  lxc --project foo start c1
  lxc --project foo delete c1 -f

  # Move storage volume between projects
  pool="incustest-$(basename "${INCUS_DIR}")"

  lxc --project foo storage volume create "${pool}" vol1
  lxc --project foo --target-project bar storage volume move "${pool}"/vol1 "${pool}"/vol1

  # Clean things up
  lxc --project bar storage volume delete "${pool}" vol1
  lxc project delete foo
  lxc project delete bar
}

# Use snapshots in a project.
test_projects_snapshots() {
  # Create a project and switch to it
  lxc project create foo
  lxc project switch foo

  # Import an image into the project
  deps/import-busybox --project foo --alias testimage

  # Add a root device to the default profile of the project
  lxc profile device add default root disk path="/" pool="incustest-$(basename "${INCUS_DIR}")"

  # Create a container in the project
  lxc init testimage c1

  # Create, rename, restore and delete a snapshot
  lxc snapshot c1
  lxc info c1 | grep -q snap0
  lxc config show c1/snap0 | grep -q BusyBox
  lxc rename c1/snap0 c1/foo
  lxc restore c1 foo
  lxc delete c1/foo

  # Test copies
  lxc snapshot c1
  lxc snapshot c1
  lxc copy c1 c2
  lxc delete c2

  # Create a snapshot in this project and another one in the default project
  lxc snapshot c1

  lxc project switch default
  ensure_import_testimage
  lxc init testimage c1
  lxc snapshot c1
  lxc delete c1

  # Switch back to the project
  lxc project switch foo

  # Delete the container
  lxc delete c1

  # Delete the project
  lxc image delete testimage
  lxc project delete foo
}

# Use backups in a project.
test_projects_backups() {
  # Create a project and switch to it
  lxc project create foo
  lxc project switch foo

  # Import an image into the project
  deps/import-busybox --project foo --alias testimage

  # Add a root device to the default profile of the project
  lxc profile device add default root disk path="/" pool="incustest-$(basename "${INCUS_DIR}")"

  # Create a container in the project
  lxc init testimage c1

  mkdir "${INCUS_DIR}/non-optimized"

  # Create a backup.
  lxc export c1 "${INCUS_DIR}/c1.tar.gz"
  tar -xzf "${INCUS_DIR}/c1.tar.gz" -C "${INCUS_DIR}/non-optimized"

  # Check tarball content
  [ -f "${INCUS_DIR}/non-optimized/backup/index.yaml" ]
  [ -d "${INCUS_DIR}/non-optimized/backup/container" ]

  # Delete the container
  lxc delete c1

  # Import the backup.
  lxc import "${INCUS_DIR}/c1.tar.gz"
  lxc info c1
  lxc delete c1

  # Delete the project
  rm -rf "${INCUS_DIR}/non-optimized/"
  lxc image delete testimage
  lxc project delete foo
}

# Use private profiles in a project.
test_projects_profiles() {
  # Create a project and switch to it
  lxc project create foo
  lxc project switch foo

  # List profiles
  lxc profile list | grep -q 'default'
  lxc profile show default | grep -q 'description: Default LXD profile for project foo'

  # Create a profile in this project
  lxc profile create p1
  lxc profile list | grep -q 'p1'

  # Set a config key on this profile
  lxc profile set p1 user.x y
  lxc profile get p1 user.x | grep -q 'y'

  # The profile is not visible in the default project
  lxc project switch default
  ! lxc profile list | grep -q 'p1' || false

  # A profile with the same name can be created in the default project
  lxc profile create p1

  # The same key can have a different value
  lxc profile set p1 user.x z
  lxc profile get p1 user.x | grep -q 'z'

  # Switch back to the project
  lxc project switch foo

  # The profile has still the original config
  lxc profile get p1 user.x | grep -q 'y'

  # Delete the profile from the project
  lxc profile delete p1

  # Delete the project
  lxc project delete foo

  # Delete the profile from the default project
  lxc profile delete p1

  # Try project copy
  lxc project create foo
  lxc profile set --project default default user.x z
  lxc profile copy --project default --target-project foo default bar
  # copy to an existing profile without --refresh should fail
  ! lxc profile copy --project default --target-project foo default bar
  lxc profile copy --project default --target-project foo default bar --refresh
  lxc profile get --project foo bar user.x | grep -q 'z'
  lxc profile copy --project default --target-project foo default bar-non-existent --refresh
  lxc profile delete bar --project foo
  lxc profile delete bar-non-existent --project foo
  lxc project delete foo
}

# Use global profiles in a project.
test_projects_profiles_default() {
  # Create a new project, without the features.profiles config.
  lxc project create -c features.profiles=false foo
  lxc project switch foo

  # Import an image into the project and grab its fingerprint
  deps/import-busybox --project foo
  fingerprint="$(lxc image list -c f --format json | jq .[0].fingerprint)"

  # Create a container
  lxc init "${fingerprint}" c1

  # Switch back the default project
  lxc project switch default

  # Try updating the default profile
  lxc profile set default user.foo bar
  lxc profile unset default user.foo

  # Create a container in the default project as well.
  ensure_import_testimage
  lxc init testimage c1

  # If we look at the global profile we see that it's being used by both the
  # container in the above project and the one we just created.
  lxc profile show default | grep -E -q '^- /1.0/instances/c1$'
  lxc profile show default | grep -E -q '^- /1.0/instances/c1\?project=foo$'

  lxc delete c1

  lxc project switch foo

  # Delete the project
  lxc delete c1
  lxc image delete "${fingerprint}"
  lxc project delete foo
}

# Use private images in a project.
test_projects_images() {
  # Create a project and switch to it
  lxc project create foo
  lxc project switch foo

  # Import an image into the project and grab its fingerprint
  deps/import-busybox --project foo
  fingerprint="$(lxc image list -c f --format json | jq .[0].fingerprint)"

  # The imported image is not visible in the default project.
  lxc project switch default
  ! lxc image list | grep -q "${fingerprint}" || false

  # Switch back to the project and clean it up.
  lxc project switch foo
  lxc image delete "${fingerprint}"

  # Now Import an image into the project assigning it an alias
  deps/import-busybox --project foo --alias foo-image

  # The image alias shows up in the project
  lxc image list | grep -q foo-image

  # However the image alias is not visible in the default project.
  lxc project switch default
  ! lxc image list | grep -q foo-project || false

  # Let's import the same image in the default project
  ensure_import_testimage

  # Switch back to the project.
  lxc project switch foo

  # The image alias from the default project is not visible here
  ! lxc image list | grep -q testimage || false

  # Rename the image alias in the project using the same it has in the default
  # one.
  lxc image alias rename foo-image testimage

  # Create another alias for the image
  lxc image alias create egg-image "${fingerprint}"

  # Delete the old alias
  lxc image alias delete testimage

  # Delete the project and image altogether
  lxc image delete egg-image
  lxc project delete foo

  # We automatically switched to the default project, which still has the alias
  lxc image list | grep -q testimage
}

# Use global images in a project.
test_projects_images_default() {
  # Make sure that there's an image in the default project
  ensure_import_testimage

  # Create a new project, without the features.images config.
  lxc project create foo
  lxc project switch foo
  lxc project set foo "features.images" "false"

  # Create another project, without the features.images config.
  lxc project create bar
  lxc project set bar "features.images" "false"

  # The project can see images from the default project
  lxc image list | grep -q testimage

  # The image from the default project has correct profile assigned
  fingerprint="$(lxc image list --format json | jq -r .[0].fingerprint)"
  lxc query "/1.0/images/${fingerprint}?project=foo" | jq -r ".profiles[0]" | grep -xq default

  # The project can delete images in the default project
  lxc image delete testimage

  # Images imported into the project show up in the default project
  deps/import-busybox --project foo --alias foo-image
  lxc image list | grep -q foo-image
  lxc project switch default
  lxc image list | grep -q foo-image

  # Correct profile assigned to images from another project
  fingerprint="$(lxc image list --format json | jq -r '.[] | select(.aliases[0].name == "foo-image") | .fingerprint')"
  lxc query "/1.0/images/${fingerprint}?project=bar" | jq -r ".profiles[0]" | grep -xq default

  lxc image delete foo-image

  lxc project delete bar
  lxc project delete foo
}

# Interaction between projects and storage pools.
test_projects_storage() {
  pool="incustest-$(basename "${INCUS_DIR}")"

  lxc storage volume create "${pool}" vol

  lxc project create foo -c features.storage.volumes=false
  lxc project switch foo

  lxc storage volume list "${pool}" | grep custom | grep -q vol

  lxc storage volume delete "${pool}" vol

  lxc project switch default

  ! lxc storage volume list "${pool}" | grep custom | grep -q vol || false

  lxc project set foo features.storage.volumes=true
  lxc storage volume create "${pool}" vol
  lxc project switch foo
  ! lxc storage volume list "${pool}" | grep custom | grep -q vol

  lxc storage volume create "${pool}" vol
  lxc storage volume delete "${pool}" vol

  lxc storage volume create "${pool}" vol2
  lxc project switch default
  ! lxc storage volume list "${pool}" | grep custom | grep -q vol2

  lxc project switch foo
  lxc storage volume delete "${pool}" vol2

  lxc project switch default
  lxc storage volume delete "${pool}" vol
  lxc project delete foo
}

# Interaction between projects and networks.
test_projects_network() {
  # Standard bridge with random subnet and a bunch of options
  network="inct$$"
  lxc network create "${network}"

  lxc project create foo
  lxc project switch foo

  # Import an image into the project
  deps/import-busybox --project foo --alias testimage

  # Add a root device to the default profile of the project
  lxc profile device add default root disk path="/" pool="incustest-$(basename "${INCUS_DIR}")"

  # Create a container in the project
  lxc init -n "${network}" testimage c1

  lxc network show "${network}" |grep -q "/1.0/instances/c1?project=foo"

  # Delete the container
  lxc delete c1

  # Delete the project
  lxc image delete testimage
  lxc project delete foo

  lxc network delete "${network}"
}

# Set resource limits on projects.
test_projects_limits() {
  # Create a project
  lxc project create p1

  # Instance limits validation
  ! lxc project set p1 limits.containers xxx || false
  ! lxc project set p1 limits.virtual-machines -1 || false

  lxc project switch p1

  # Add a root device to the default profile of the project and import an image.
  pool="incustest-$(basename "${INCUS_DIR}")"
  lxc profile device add default root disk path="/" pool="${pool}"

  deps/import-busybox --project p1 --alias testimage

  # Create a couple of containers in the project.
  lxc init testimage c1
  lxc init testimage c2

  # Can't set the containers limit below the current count.
  ! lxc project set p1 limits.containers 1 || false

  # Can't create containers anymore after the limit is reached.
  lxc project set p1 limits.containers 2
  ! lxc init testimage c3 || false

  # Can't set the project's memory limit to a percentage value.
  ! lxc project set p1 limits.memory 10% || false

  # Can't set the project's memory limit because not all instances have
  # limits.memory defined.
  ! lxc project set p1 limits.memory 10GiB || false

  # Set limits.memory on the default profile.
  lxc profile set default limits.memory 1GiB

  # Can't set the memory limit below the current total usage.
  ! lxc project set p1 limits.memory 1GiB || false

  # Configure a valid project memory limit.
  lxc project set p1 limits.memory 3GiB

  # Validate that snapshots don't fail with limits.
  lxc snapshot c2
  lxc restore c2 snap0

  lxc delete c2

  # Create a new profile which does not define "limits.memory".
  lxc profile create unrestricted
  lxc profile device add unrestricted root disk path="/" pool="${pool}"

  # Can't create a new container without defining "limits.memory"
  ! lxc init testimage c2 -p unrestricted || false

  # Can't create a new container if "limits.memory" is too high
  ! lxc init testimage c2 -p unrestricted -c limits.memory=4GiB || false

  # Can't create a new container if "limits.memory" is a percentage
  ! lxc init testimage c2 -p unrestricted -c limits.memory=10% || false

  # No error occurs if we define "limits.memory" and stay within the limits.
  lxc init testimage c2 -p unrestricted -c limits.memory=1GiB

  # Can't change the container's "limits.memory" if it would overflow the limit.
  ! lxc config set c2 limits.memory=4GiB || false

  # Can't unset the instance's "limits.memory".
  ! lxc config unset c2 limits.memory || false

  # Can't unset the default profile's "limits.memory", as it would leave c1
  # without an effective "limits.memory".
  ! lxc profile unset default limits.memory || false

  # Can't check the default profile's "limits.memory" to a value that would
  # violate project's limits.
  ! lxc profile set default limits.memory=4GiB || false

  # Can't change limits.memory to a percentage.
  ! lxc profile set default limits.memory=10% || false
  ! lxc config set c2 limits.memory=10% || false

  # It's possible to change both a profile and an instance memory limit, if they
  # don't break the project's aggregate allowance.
  lxc profile set default limits.memory=2GiB
  lxc config set c2 limits.memory=512MiB

  # Can't set the project's processes limit because no instance has
  # limits.processes defined.
  ! lxc project set p1 limits.processes 100 || false

  # Set processes limits on the default profile and on c2.
  lxc profile set default limits.processes=50
  lxc config set c2 limits.processes=50

  # Can't set the project's processes limit if it's below the current total.
  ! lxc project set p1 limits.processes 75 || false

  # Set the project's processes limit.
  lxc project set p1 limits.processes 150

  # Changing profile and instance processes limits within the aggregate
  # project's limit is fine.
  lxc profile set default limits.processes=75
  lxc config set c2 limits.processes=75

  # Changing profile and instance processes limits above the aggregate project's
  # limit is not possible.
  ! lxc profile set default limits.processes=80 || false
  ! lxc config set c2 limits.processes=80 || false

  # Changing the project's processes limit below the current aggregate amount is
  # not possible.
  ! lxc project set p1 limits.processes 125 || false

  # Set a cpu limit on the default profile and on the instance, with c2
  # using CPU pinning.
  lxc profile set default limits.cpu=2
  lxc config set c2 limits.cpu=0,1

  # It's not possible to set the project's cpu limit since c2 is using CPU
  # pinning.
  ! lxc project set p1 limits.cpu 4 || false

  # Change c2's from cpu pinning to a regular cpu count limit.
  lxc config set c2 limits.cpu=2

  # Can't set the project's cpu limit below the current aggregate count.
  ! lxc project set p1 limits.cpu 3 || false

  # Set the project's cpu limit
  lxc project set p1 limits.cpu 4

  # Can't update the project's cpu limit below the current aggregate count.
  ! lxc project set p1 limits.cpu 3 || false

  # Changing profile and instance cpu limits above the aggregate project's
  # limit is not possible.
  ! lxc profile set default limits.cpu=3 || false
  ! lxc config set c2 limits.cpu=3 || false

  # CPU limits can be updated if they stay within limits.
  lxc project set p1 limits.cpu 7
  lxc profile set default limits.cpu=3
  lxc config set c2 limits.cpu=3

  # Can't set the project's disk limit because not all instances have
  # the "size" config defined on the root device.
  ! lxc project set p1 limits.disk 1GiB || false

  # Set a disk limit on the default profile and also on instance c2
  lxc profile device set default root size=100MiB
  lxc config device add c2 root disk path="/" pool="${pool}" size=50MiB

  # Can't set the project's disk limit because not all volumes have
  # the "size" config defined.
  pool1="inctest1-$(basename "${INCUS_DIR}")"
  lxc storage create "${pool1}" lvm size=1GiB
  lxc storage volume create "${pool1}" v1
  ! lxc project set p1 limits.disk 1GiB || false
  lxc storage volume delete "${pool1}" v1
  lxc storage delete "${pool1}"

  # Create a custom volume without any size property defined.
  lxc storage volume create "${pool}" v1

  # Set a size on the custom volume.
  lxc storage volume set "${pool}" v1 size 50MiB

  # Can't set the project's disk limit below the current aggregate count.
  ! lxc project set p1 limits.disk 190MiB || false

  # Set the project's disk limit
  lxc project set p1 limits.disk 250MiB

  # Can't update the project's disk limit below the current aggregate count.
  ! lxc project set p1 limits.disk 190MiB || false

  # Changing profile or instance root device size or volume size above the
  # aggregate project's limit is not possible.
  ! lxc profile device set default root size=160MiB || false
  ! lxc config device set c2 root size 110MiB || false
  ! lxc storage volume set "${pool}" v1 size 110MiB || false

  # Can't create a custom volume without specifying a size.
  ! lxc storage volume create "${pool}" v2 || false

  # Disk limits can be updated if they stay within limits.
  lxc project set p1 limits.disk 204900KiB
  lxc profile device set default root size=90MiB
  lxc config device set c2 root size 60MiB

  # Can't upload an image if that would exceed the current quota.
  ! deps/import-busybox --project p1 --template start --alias otherimage || false

  # Can't export publish an instance as image if that would exceed the current
  # quota.
  ! lxc publish c1 --alias=c1image || false

  # Run the following part of the test only against the dir or zfs backend,
  # since it on other backends it requires resize the rootfs to a value which is
  # too small for resize2fs.
  if [ "${INCUS_BACKEND}" = "dir" ] || [ "${INCUS_BACKEND}" = "zfs" ]; then
    # Add a remote LXD to be used as image server.
    # shellcheck disable=2039,3043
    local INCUS_REMOTE_DIR
    INCUS_REMOTE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${INCUS_REMOTE_DIR}"

    # Switch to default project to spawn new LXD server, and then switch back to p1.
    lxc project switch default
    spawn_incus "${INCUS_REMOTE_DIR}" true
    lxc project switch p1

    INCUS_REMOTE_ADDR=$(cat "${INCUS_REMOTE_DIR}/lxd.addr")
    (INCUS_DIR=${INCUS_REMOTE_DIR} deps/import-busybox --alias remoteimage --template start --public)

    lxc remote add l2 "${INCUS_REMOTE_ADDR}" --accept-certificate --password foo

    # Relax all constraints except the disk limits, which won't be enough for the
    # image to be downloaded.
    lxc profile device set default root size=500KiB
    lxc project set p1 limits.disk 111MiB
    lxc project unset p1 limits.containers
    lxc project unset p1 limits.cpu
    lxc project unset p1 limits.memory
    lxc project unset p1 limits.processes

    # Can't download a remote image if that would exceed the current quota.
    ! lxc init l2:remoteimage c3 || false
  fi

  lxc storage volume delete "${pool}" v1
  lxc delete c1
  lxc delete c2
  lxc image delete testimage
  lxc profile delete unrestricted

  lxc project switch default
  lxc project delete p1

  # Start with clean project.
  lxc project create p1
  lxc project switch p1

  # Relaxing restricted.containers.lowlevel to 'allow' makes it possible set
  # low-level keys.
  lxc project set p1 restricted.containers.lowlevel allow

  # Add a root device to the default profile of the project and import an image.
  pool="incustest-$(basename "${INCUS_DIR}")"
  lxc profile device add default root disk path="/" pool="${pool}"

  deps/import-busybox --project p1 --alias testimage

  # Create a couple of containers in the project.
  lxc init testimage c1 -c limits.memory=1GiB
  lxc init testimage c2 -c limits.memory=1GiB

  lxc export c1
  lxc delete c1

  # Configure a valid project memory limit.
  lxc project set p1 limits.memory 1GiB

  # Can't import the backup as it would exceed the 1GiB project memory limit.
  ! lxc import c1.tar.gz || false

  rm c1.tar.gz
  lxc delete c2
  lxc image delete testimage
  lxc project switch default
  lxc project delete p1

  if [ "${INCUS_BACKEND}" = "dir" ] || [ "${INCUS_BACKEND}" = "zfs" ]; then
    lxc remote remove l2
    kill_incus "$INCUS_REMOTE_DIR"
  fi
}

# Set restrictions on projects.
test_projects_restrictions() {
  # Add a managed network.
  netManaged="lxd$$"
  lxc network create "${netManaged}"

  netUnmanaged="${netManaged}-unm"
  ip link add "${netUnmanaged}" type bridge

  # Create a project and switch to it
  lxc project create p1 -c features.storage.volumes=false
  lxc project switch p1

  # Check with restricted unset and restricted.devices.nic unset that managed & unmanaged networks are accessible.
  lxc network list | grep -F "${netManaged}"
  lxc network list | grep -F "${netUnmanaged}"
  lxc network show "${netManaged}"
  lxc network show "${netUnmanaged}"

  # Check with restricted unset and restricted.devices.nic=block that managed & unmanaged networks are accessible.
  lxc project set p1 restricted.devices.nic=block
  lxc network list | grep -F "${netManaged}"
  lxc network list | grep -F "${netUnmanaged}"
  lxc network show "${netManaged}"
  lxc network show "${netUnmanaged}"

  # Check with restricted=true and restricted.devices.nic=block that managed & unmanaged networks are inaccessible.
  lxc project set p1 restricted=true
  ! lxc network list | grep -F "${netManaged}"|| false
  ! lxc network show "${netManaged}" || false
  ! lxc network list | grep -F "${netUnmanaged}"|| false
  ! lxc network show "${netUnmanaged}" || false

  # Check with restricted=true and restricted.devices.nic=managed that managed networks are accessible and that
  # unmanaged networks are inaccessible.
  lxc project set p1 restricted.devices.nic=managed
  lxc network list | grep -F "${netManaged}"
  lxc network show "${netManaged}"
  ! lxc network list | grep -F "${netUnmanaged}"|| false
  ! lxc network show "${netUnmanaged}" || false

  # Check with restricted.devices.nic=allow and restricted.networks.access set to a network other than the existing
  # managed and unmanaged ones that they are inaccessible.
  lxc project set p1 restricted.devices.nic=allow
  lxc project set p1 restricted.networks.access=foo
  ! lxc network list | grep -F "${netManaged}"|| false
  ! lxc network show "${netManaged}" || false
  ! lxc network info "${netManaged}"|| false

  ! lxc network list | grep -F "${netUnmanaged}"|| false
  ! lxc network show "${netUnmanaged}" || false
  ! lxc network info "${netUnmanaged}"|| false

  ! lxc network set "${netManaged}" user.foo=bah || false
  ! lxc network get "${netManaged}" ipv4.address || false
  ! lxc network info "${netManaged}"|| false
  ! lxc network delete "${netManaged}" || false

  ! lxc profile device add default eth0 nic nictype=bridge parent=netManaged || false
  ! lxc profile device add default eth0 nic nictype=bridge parent=netUnmanaged || false

  ip link delete "${netUnmanaged}"

  # Disable restrictions to allow devices to be added to profile.
  lxc project unset p1 restricted.networks.access
  lxc project set p1 restricted.devices.nic=managed
  lxc project set p1 restricted=false

  # Add a root device to the default profile of the project and import an image.
  pool="incustest-$(basename "${INCUS_DIR}")"
  lxc profile device add default root disk path="/" pool="${pool}"

  deps/import-busybox --project p1 --alias testimage
  fingerprint="$(lxc image list -c f --format json | jq -r .[0].fingerprint)"

  # Add a volume.
  lxc storage volume create "${pool}" "v-proj$$"

  # Enable all restrictions.
  lxc project set p1 restricted=true

  # It's not possible to create nested containers.
  ! lxc profile set default security.nesting=true || false
  ! lxc init testimage c1 -c security.nesting=true || false

  # It's not possible to use forbidden low-level options
  ! lxc profile set default "raw.idmap=both 0 0" || false
  ! lxc init testimage c1 -c "raw.idmap=both 0 0" || false
  ! lxc init testimage c1 -c volatile.uuid="foo" || false

  # It's not possible to create privileged containers.
  ! lxc profile set default security.privileged=true || false
  ! lxc init testimage c1 -c security.privileged=true || false

  # It's possible to create non-isolated containers.
  lxc init testimage c1 -c security.idmap.isolated=false

  # It's not possible to change low-level options
  ! lxc config set c1 "raw.idmap=both 0 0" || false
  ! lxc config set c1 volatile.uuid="foo" || false

  # It's not possible to attach character devices.
  ! lxc profile device add default tty unix-char path=/dev/ttyS0 || false
  ! lxc config device add c1 tty unix-char path=/dev/ttyS0 || false

  # It's not possible to attach raw network devices.
  ! lxc profile device add default eth0 nic nictype=p2p || false

  # It's not possible to attach non-managed disk devices.
  ! lxc profile device add default testdir disk source="${TEST_DIR}" path=/mnt || false
  ! lxc config device add c1 testdir disk source="${TEST_DIR}" path=/mnt || false

  # It's possible to attach managed network devices.
  lxc profile device add default eth0 nic network="${netManaged}"

  # It's possible to attach disks backed by a pool.
  lxc config device add c1 data disk pool="${pool}" path=/mnt source="v-proj$$"

  # It's not possible to set restricted.containers.nic to 'block' because
  # there's an instance using the managed network.
  ! lxc project set p1 restricted.devices.nic=block || false

  # Relaxing restricted.containers.nic to 'allow' makes it possible to attach
  # raw network devices.
  lxc project set p1 restricted.devices.nic=allow
  lxc config device add c1 eth1 nic nictype=p2p

  # Relaxing restricted.containers.disk to 'allow' makes it possible to attach
  # non-managed disks.
  lxc project set p1 restricted.devices.disk=allow
  lxc config device add c1 testdir disk source="${TEST_DIR}" path=/foo

  # Relaxing restricted.containers.lowlevel to 'allow' makes it possible set
  # low-level keys.
  lxc project set p1 restricted.containers.lowlevel=allow
  lxc config set c1 "raw.idmap=both 0 0"

  lxc delete c1

  # Setting restricted.containers.disk to 'block' allows only the root disk
  # device.
  lxc project set p1 restricted.devices.disk=block
  ! lxc profile device add default data disk pool="${pool}" path=/mnt source="v-proj$$" || false

  # Setting restricted.containers.nesting to 'allow' makes it possible to create
  # nested containers.
  lxc project set p1 restricted.containers.nesting=allow
  lxc init testimage c1 -c security.nesting=true

  # It's not possible to set restricted.containers.nesting back to 'block',
  # because there's an instance with security.nesting=true.
  ! lxc project set p1 restricted.containers.nesting=block || false

  lxc delete c1

  # Setting restricted.containers.lowlevel to 'allow' makes it possible to set
  # low-level options.
  lxc project set p1 restricted.containers.lowlevel=allow
  lxc init testimage c1 -c "raw.idmap=both 0 0" || false

  # It's not possible to set restricted.containers.lowlevel back to 'block',
  # because there's an instance with raw.idmap set.
  ! lxc project set p1 restricted.containers.lowlevel=block || false

  lxc delete c1

  # Setting restricted.containers.privilege to 'allow' makes it possible to create
  # privileged containers.
  lxc project set p1 restricted.containers.privilege=allow
  lxc init testimage c1 -c security.privileged=true

  # It's not possible to set restricted.containers.privilege back to
  # 'unprivileged', because there's an instance with security.privileged=true.
  ! lxc project set p1 restricted.containers.privilege=unprivileged || false

  # Test expected syscall interception behavior.
  ! lxc config set c1 security.syscalls.intercept.mknod=true || false
  lxc config set c1 security.syscalls.intercept.mknod=false
  lxc project set p1 restricted.containers.interception=block
  ! lxc config set c1 security.syscalls.intercept.mknod=true || false
  lxc project set p1 restricted.containers.interception=allow
  lxc config set c1 security.syscalls.intercept.mknod=true
  lxc config set c1 security.syscalls.intercept.mount=true
  ! lxc config set c1 security.syscalls.intercept.mount.allow=ext4 || false

  lxc delete c1

  lxc image delete testimage

  lxc project switch default
  lxc project delete p1

  lxc network delete "${netManaged}"
  lxc storage volume delete "${pool}" "v-proj$$"
}

# Test project state api
test_projects_usage() {
  # Set configuration on the default project
  lxc project create test-usage \
    -c limits.cpu=5 \
    -c limits.memory=1GiB \
    -c limits.disk=10GiB \
    -c limits.networks=3 \
    -c limits.processes=40

  # Create a profile defining resource allocations
  lxc profile show default --project default | lxc profile edit default --project test-usage
  lxc profile set default --project test-usage \
    limits.cpu=1 \
    limits.memory=512MiB \
    limits.processes=20
  lxc profile device set default root size=3GiB --project test-usage

  # Spin up a container
  deps/import-busybox --project test-usage --alias testimage
  lxc init testimage c1 --project test-usage
  lxc project info test-usage

  lxc project info test-usage --format csv | grep -q "CONTAINERS,UNLIMITED,1"
  lxc project info test-usage --format csv | grep -q "CPU,5,1"
  lxc project info test-usage --format csv | grep -q "DISK,10.00GiB,3.00GiB"
  lxc project info test-usage --format csv | grep -q "INSTANCES,UNLIMITED,1"
  lxc project info test-usage --format csv | grep -q "MEMORY,1.00GiB,512.00MiB"
  lxc project info test-usage --format csv | grep -q "NETWORKS,3,0"
  lxc project info test-usage --format csv | grep -q "PROCESSES,40,20"
  lxc project info test-usage --format csv | grep -q "VIRTUAL-MACHINES,UNLIMITED,0"

  lxc delete c1 --project test-usage
  lxc image delete testimage --project test-usage
  lxc project delete test-usage
}
