# Use the default project.
test_projects_default() {
  # The default project is used by the default profile
  inc project show default | grep -q "/1.0/profiles/default$"

  # Containers and images are assigned to the default project
  ensure_import_testimage
  inc init testimage c1
  inc project show default | grep -q "/1.0/profiles/default$"
  inc project show default | grep -q "/1.0/images/"
  inc delete c1
}

# CRUD operations on project.
test_projects_crud() {
  # Create a project
  inc project create foo

  # All features are enabled by default
  inc project show foo | grep -q 'features.images: "true"'
  inc project get foo "features.profiles" | grep -q 'true'

  # Trying to create a project with the same name fails
  ! inc project create foo || false

  # Trying to create a project containing an underscore fails
  ! inc project create foo_banned || false

  # Rename the project to a banned name fails
  ! inc project rename foo bar_banned || false

  # Rename the project and check it occurs
  inc project rename foo bar
  inc project show bar

  # Edit the project
  inc project show bar| sed 's/^description:.*/description: "Bar project"/' | inc project edit bar
  inc project show bar | grep -q "description: Bar project"

  # Create a second project
  inc project create foo

  # Trying to rename a project using an existing name fails
  ! inc project rename bar foo || false

  inc project switch foo

  # Turning off the profiles feature makes the project see the default profile
  # from the default project.
  inc project set foo features.profiles false
  inc profile show default | grep -E -q '^description: Default LXD profile$'

  # Turning on the profiles feature creates a project-specific default
  # profile.
  inc project set foo features.profiles true
  inc profile show default | grep -E -q '^description: Default LXD profile for project foo$'

  # Invalid config values are rejected.
  ! inc project set foo garbage xxx || false

  inc project switch default

  # Delete the projects
  inc project delete foo
  inc project delete bar

  # We're back to the default project
  inc project list | grep -q "default (current)"
}

# Use containers in a project.
test_projects_containers() {
  # Create a project and switch to it
  inc project create foo
  inc project switch foo

  deps/import-busybox --project foo --alias testimage
  fingerprint="$(inc image list -c f --format json | jq -r .[0].fingerprint)"

  # Add a root device to the default profile of the project
  pool="incustest-$(basename "${INCUS_DIR}")"
  inc profile device add default root disk path="/" pool="${pool}"

  # Create a container in the project
  inc init testimage c1

  # The container is listed when using this project
  inc list | grep -q c1
  inc info c1 | grep -q "Name: c1"

  # The container's volume is listed too.
  inc storage volume list "${pool}" | grep container | grep -q c1

  # For backends with optimized storage, we can see the image volume inside the
  # project.
  driver="$(storage_backend "$INCUS_DIR")"
  if [ "${driver}" != "dir" ]; then
      inc storage volume list "${pool}" | grep image | grep -q "${fingerprint}"
  fi

  # Start the container
  inc start c1
  inc list | grep c1 | grep -q RUNNING
  echo "abc" | inc exec c1 cat | grep -q abc

  # The container can't be managed when using the default project
  inc project switch default
  ! inc list | grep -q c1 || false
  ! inc info c1 || false
  ! inc delete c1 || false
  ! inc storage volume list "${pool}" | grep container | grep -q c1 || false

  # Trying to delete a project which is in use fails
  ! inc project delete foo || false

  # Trying to change features of a project which is in use fails
  ! inc project show foo| sed 's/features.profiles:.*/features.profiles: "false"/' | inc project edit foo || false
  ! inc project set foo "features.profiles" "false" || false
  inc project show foo | grep -q 'features.profiles: "true"'

  # Create a container with the same name in the default project
  ensure_import_testimage
  inc init testimage c1
  inc start c1
  inc list | grep c1 | grep -q RUNNING
  inc stop --force c1

  # Delete the container
  inc project switch foo

  inc stop --force c1
  inc delete c1
  inc image delete testimage

  # Delete the project
  inc project delete foo

  # The container in the default project can still be used
  inc start c1
  inc list | grep c1 | grep -q RUNNING
  inc stop --force c1
  inc delete c1
}

# Copy/move between projects
test_projects_copy() {
  ensure_import_testimage

  # Create a couple of projects
  inc project create foo -c features.profiles=false -c features.images=false
  inc project create bar -c features.profiles=false -c features.images=false

  # Create a container in the project
  inc --project foo init testimage c1
  inc --project foo copy c1 c1 --target-project bar
  inc --project bar start c1
  inc --project bar delete c1 -f

  inc --project foo snapshot c1
  inc --project foo snapshot c1
  inc --project foo snapshot c1

  inc --project foo copy c1/snap0 c1 --target-project bar
  inc --project bar start c1
  inc --project bar delete c1 -f

  inc --project foo copy c1 c1 --target-project bar
  inc --project foo start c1
  inc --project bar start c1

  inc --project foo delete c1 -f
  inc --project bar stop c1 -f
  inc --project bar move c1 c1 --target-project foo
  inc --project foo start c1
  inc --project foo delete c1 -f

  # Move storage volume between projects
  pool="incustest-$(basename "${INCUS_DIR}")"

  inc --project foo storage volume create "${pool}" vol1
  inc --project foo --target-project bar storage volume move "${pool}"/vol1 "${pool}"/vol1

  # Clean things up
  inc --project bar storage volume delete "${pool}" vol1
  inc project delete foo
  inc project delete bar
}

# Use snapshots in a project.
test_projects_snapshots() {
  # Create a project and switch to it
  inc project create foo
  inc project switch foo

  # Import an image into the project
  deps/import-busybox --project foo --alias testimage

  # Add a root device to the default profile of the project
  inc profile device add default root disk path="/" pool="incustest-$(basename "${INCUS_DIR}")"

  # Create a container in the project
  inc init testimage c1

  # Create, rename, restore and delete a snapshot
  inc snapshot c1
  inc info c1 | grep -q snap0
  inc config show c1/snap0 | grep -q BusyBox
  inc rename c1/snap0 c1/foo
  inc restore c1 foo
  inc delete c1/foo

  # Test copies
  inc snapshot c1
  inc snapshot c1
  inc copy c1 c2
  inc delete c2

  # Create a snapshot in this project and another one in the default project
  inc snapshot c1

  inc project switch default
  ensure_import_testimage
  inc init testimage c1
  inc snapshot c1
  inc delete c1

  # Switch back to the project
  inc project switch foo

  # Delete the container
  inc delete c1

  # Delete the project
  inc image delete testimage
  inc project delete foo
}

# Use backups in a project.
test_projects_backups() {
  # Create a project and switch to it
  inc project create foo
  inc project switch foo

  # Import an image into the project
  deps/import-busybox --project foo --alias testimage

  # Add a root device to the default profile of the project
  inc profile device add default root disk path="/" pool="incustest-$(basename "${INCUS_DIR}")"

  # Create a container in the project
  inc init testimage c1

  mkdir "${INCUS_DIR}/non-optimized"

  # Create a backup.
  inc export c1 "${INCUS_DIR}/c1.tar.gz"
  tar -xzf "${INCUS_DIR}/c1.tar.gz" -C "${INCUS_DIR}/non-optimized"

  # Check tarball content
  [ -f "${INCUS_DIR}/non-optimized/backup/index.yaml" ]
  [ -d "${INCUS_DIR}/non-optimized/backup/container" ]

  # Delete the container
  inc delete c1

  # Import the backup.
  inc import "${INCUS_DIR}/c1.tar.gz"
  inc info c1
  inc delete c1

  # Delete the project
  rm -rf "${INCUS_DIR}/non-optimized/"
  inc image delete testimage
  inc project delete foo
}

# Use private profiles in a project.
test_projects_profiles() {
  # Create a project and switch to it
  inc project create foo
  inc project switch foo

  # List profiles
  inc profile list | grep -q 'default'
  inc profile show default | grep -q 'description: Default LXD profile for project foo'

  # Create a profile in this project
  inc profile create p1
  inc profile list | grep -q 'p1'

  # Set a config key on this profile
  inc profile set p1 user.x y
  inc profile get p1 user.x | grep -q 'y'

  # The profile is not visible in the default project
  inc project switch default
  ! inc profile list | grep -q 'p1' || false

  # A profile with the same name can be created in the default project
  inc profile create p1

  # The same key can have a different value
  inc profile set p1 user.x z
  inc profile get p1 user.x | grep -q 'z'

  # Switch back to the project
  inc project switch foo

  # The profile has still the original config
  inc profile get p1 user.x | grep -q 'y'

  # Delete the profile from the project
  inc profile delete p1

  # Delete the project
  inc project delete foo

  # Delete the profile from the default project
  inc profile delete p1

  # Try project copy
  inc project create foo
  inc profile set --project default default user.x z
  inc profile copy --project default --target-project foo default bar
  # copy to an existing profile without --refresh should fail
  ! inc profile copy --project default --target-project foo default bar
  inc profile copy --project default --target-project foo default bar --refresh
  inc profile get --project foo bar user.x | grep -q 'z'
  inc profile copy --project default --target-project foo default bar-non-existent --refresh
  inc profile delete bar --project foo
  inc profile delete bar-non-existent --project foo
  inc project delete foo
}

# Use global profiles in a project.
test_projects_profiles_default() {
  # Create a new project, without the features.profiles config.
  inc project create -c features.profiles=false foo
  inc project switch foo

  # Import an image into the project and grab its fingerprint
  deps/import-busybox --project foo
  fingerprint="$(inc image list -c f --format json | jq .[0].fingerprint)"

  # Create a container
  inc init "${fingerprint}" c1

  # Switch back the default project
  inc project switch default

  # Try updating the default profile
  inc profile set default user.foo bar
  inc profile unset default user.foo

  # Create a container in the default project as well.
  ensure_import_testimage
  inc init testimage c1

  # If we look at the global profile we see that it's being used by both the
  # container in the above project and the one we just created.
  inc profile show default | grep -E -q '^- /1.0/instances/c1$'
  inc profile show default | grep -E -q '^- /1.0/instances/c1\?project=foo$'

  inc delete c1

  inc project switch foo

  # Delete the project
  inc delete c1
  inc image delete "${fingerprint}"
  inc project delete foo
}

# Use private images in a project.
test_projects_images() {
  # Create a project and switch to it
  inc project create foo
  inc project switch foo

  # Import an image into the project and grab its fingerprint
  deps/import-busybox --project foo
  fingerprint="$(inc image list -c f --format json | jq .[0].fingerprint)"

  # The imported image is not visible in the default project.
  inc project switch default
  ! inc image list | grep -q "${fingerprint}" || false

  # Switch back to the project and clean it up.
  inc project switch foo
  inc image delete "${fingerprint}"

  # Now Import an image into the project assigning it an alias
  deps/import-busybox --project foo --alias foo-image

  # The image alias shows up in the project
  inc image list | grep -q foo-image

  # However the image alias is not visible in the default project.
  inc project switch default
  ! inc image list | grep -q foo-project || false

  # Let's import the same image in the default project
  ensure_import_testimage

  # Switch back to the project.
  inc project switch foo

  # The image alias from the default project is not visible here
  ! inc image list | grep -q testimage || false

  # Rename the image alias in the project using the same it has in the default
  # one.
  inc image alias rename foo-image testimage

  # Create another alias for the image
  inc image alias create egg-image "${fingerprint}"

  # Delete the old alias
  inc image alias delete testimage

  # Delete the project and image altogether
  inc image delete egg-image
  inc project delete foo

  # We automatically switched to the default project, which still has the alias
  inc image list | grep -q testimage
}

# Use global images in a project.
test_projects_images_default() {
  # Make sure that there's an image in the default project
  ensure_import_testimage

  # Create a new project, without the features.images config.
  inc project create foo
  inc project switch foo
  inc project set foo "features.images" "false"

  # Create another project, without the features.images config.
  inc project create bar
  inc project set bar "features.images" "false"

  # The project can see images from the default project
  inc image list | grep -q testimage

  # The image from the default project has correct profile assigned
  fingerprint="$(inc image list --format json | jq -r .[0].fingerprint)"
  inc query "/1.0/images/${fingerprint}?project=foo" | jq -r ".profiles[0]" | grep -xq default

  # The project can delete images in the default project
  inc image delete testimage

  # Images imported into the project show up in the default project
  deps/import-busybox --project foo --alias foo-image
  inc image list | grep -q foo-image
  inc project switch default
  inc image list | grep -q foo-image

  # Correct profile assigned to images from another project
  fingerprint="$(inc image list --format json | jq -r '.[] | select(.aliases[0].name == "foo-image") | .fingerprint')"
  inc query "/1.0/images/${fingerprint}?project=bar" | jq -r ".profiles[0]" | grep -xq default

  inc image delete foo-image

  inc project delete bar
  inc project delete foo
}

# Interaction between projects and storage pools.
test_projects_storage() {
  pool="incustest-$(basename "${INCUS_DIR}")"

  inc storage volume create "${pool}" vol

  inc project create foo -c features.storage.volumes=false
  inc project switch foo

  inc storage volume list "${pool}" | grep custom | grep -q vol

  inc storage volume delete "${pool}" vol

  inc project switch default

  ! inc storage volume list "${pool}" | grep custom | grep -q vol || false

  inc project set foo features.storage.volumes=true
  inc storage volume create "${pool}" vol
  inc project switch foo
  ! inc storage volume list "${pool}" | grep custom | grep -q vol

  inc storage volume create "${pool}" vol
  inc storage volume delete "${pool}" vol

  inc storage volume create "${pool}" vol2
  inc project switch default
  ! inc storage volume list "${pool}" | grep custom | grep -q vol2

  inc project switch foo
  inc storage volume delete "${pool}" vol2

  inc project switch default
  inc storage volume delete "${pool}" vol
  inc project delete foo
}

# Interaction between projects and networks.
test_projects_network() {
  # Standard bridge with random subnet and a bunch of options
  network="inct$$"
  inc network create "${network}"

  inc project create foo
  inc project switch foo

  # Import an image into the project
  deps/import-busybox --project foo --alias testimage

  # Add a root device to the default profile of the project
  inc profile device add default root disk path="/" pool="incustest-$(basename "${INCUS_DIR}")"

  # Create a container in the project
  inc init -n "${network}" testimage c1

  inc network show "${network}" |grep -q "/1.0/instances/c1?project=foo"

  # Delete the container
  inc delete c1

  # Delete the project
  inc image delete testimage
  inc project delete foo

  inc network delete "${network}"
}

# Set resource limits on projects.
test_projects_limits() {
  # Create a project
  inc project create p1

  # Instance limits validation
  ! inc project set p1 limits.containers xxx || false
  ! inc project set p1 limits.virtual-machines -1 || false

  inc project switch p1

  # Add a root device to the default profile of the project and import an image.
  pool="incustest-$(basename "${INCUS_DIR}")"
  inc profile device add default root disk path="/" pool="${pool}"

  deps/import-busybox --project p1 --alias testimage

  # Create a couple of containers in the project.
  inc init testimage c1
  inc init testimage c2

  # Can't set the containers limit below the current count.
  ! inc project set p1 limits.containers 1 || false

  # Can't create containers anymore after the limit is reached.
  inc project set p1 limits.containers 2
  ! inc init testimage c3 || false

  # Can't set the project's memory limit to a percentage value.
  ! inc project set p1 limits.memory 10% || false

  # Can't set the project's memory limit because not all instances have
  # limits.memory defined.
  ! inc project set p1 limits.memory 10GiB || false

  # Set limits.memory on the default profile.
  inc profile set default limits.memory 1GiB

  # Can't set the memory limit below the current total usage.
  ! inc project set p1 limits.memory 1GiB || false

  # Configure a valid project memory limit.
  inc project set p1 limits.memory 3GiB

  # Validate that snapshots don't fail with limits.
  inc snapshot c2
  inc restore c2 snap0

  inc delete c2

  # Create a new profile which does not define "limits.memory".
  inc profile create unrestricted
  inc profile device add unrestricted root disk path="/" pool="${pool}"

  # Can't create a new container without defining "limits.memory"
  ! inc init testimage c2 -p unrestricted || false

  # Can't create a new container if "limits.memory" is too high
  ! inc init testimage c2 -p unrestricted -c limits.memory=4GiB || false

  # Can't create a new container if "limits.memory" is a percentage
  ! inc init testimage c2 -p unrestricted -c limits.memory=10% || false

  # No error occurs if we define "limits.memory" and stay within the limits.
  inc init testimage c2 -p unrestricted -c limits.memory=1GiB

  # Can't change the container's "limits.memory" if it would overflow the limit.
  ! inc config set c2 limits.memory=4GiB || false

  # Can't unset the instance's "limits.memory".
  ! inc config unset c2 limits.memory || false

  # Can't unset the default profile's "limits.memory", as it would leave c1
  # without an effective "limits.memory".
  ! inc profile unset default limits.memory || false

  # Can't check the default profile's "limits.memory" to a value that would
  # violate project's limits.
  ! inc profile set default limits.memory=4GiB || false

  # Can't change limits.memory to a percentage.
  ! inc profile set default limits.memory=10% || false
  ! inc config set c2 limits.memory=10% || false

  # It's possible to change both a profile and an instance memory limit, if they
  # don't break the project's aggregate allowance.
  inc profile set default limits.memory=2GiB
  inc config set c2 limits.memory=512MiB

  # Can't set the project's processes limit because no instance has
  # limits.processes defined.
  ! inc project set p1 limits.processes 100 || false

  # Set processes limits on the default profile and on c2.
  inc profile set default limits.processes=50
  inc config set c2 limits.processes=50

  # Can't set the project's processes limit if it's below the current total.
  ! inc project set p1 limits.processes 75 || false

  # Set the project's processes limit.
  inc project set p1 limits.processes 150

  # Changing profile and instance processes limits within the aggregate
  # project's limit is fine.
  inc profile set default limits.processes=75
  inc config set c2 limits.processes=75

  # Changing profile and instance processes limits above the aggregate project's
  # limit is not possible.
  ! inc profile set default limits.processes=80 || false
  ! inc config set c2 limits.processes=80 || false

  # Changing the project's processes limit below the current aggregate amount is
  # not possible.
  ! inc project set p1 limits.processes 125 || false

  # Set a cpu limit on the default profile and on the instance, with c2
  # using CPU pinning.
  inc profile set default limits.cpu=2
  inc config set c2 limits.cpu=0,1

  # It's not possible to set the project's cpu limit since c2 is using CPU
  # pinning.
  ! inc project set p1 limits.cpu 4 || false

  # Change c2's from cpu pinning to a regular cpu count limit.
  inc config set c2 limits.cpu=2

  # Can't set the project's cpu limit below the current aggregate count.
  ! inc project set p1 limits.cpu 3 || false

  # Set the project's cpu limit
  inc project set p1 limits.cpu 4

  # Can't update the project's cpu limit below the current aggregate count.
  ! inc project set p1 limits.cpu 3 || false

  # Changing profile and instance cpu limits above the aggregate project's
  # limit is not possible.
  ! inc profile set default limits.cpu=3 || false
  ! inc config set c2 limits.cpu=3 || false

  # CPU limits can be updated if they stay within limits.
  inc project set p1 limits.cpu 7
  inc profile set default limits.cpu=3
  inc config set c2 limits.cpu=3

  # Can't set the project's disk limit because not all instances have
  # the "size" config defined on the root device.
  ! inc project set p1 limits.disk 1GiB || false

  # Set a disk limit on the default profile and also on instance c2
  inc profile device set default root size=100MiB
  inc config device add c2 root disk path="/" pool="${pool}" size=50MiB

  # Can't set the project's disk limit because not all volumes have
  # the "size" config defined.
  pool1="inctest1-$(basename "${INCUS_DIR}")"
  inc storage create "${pool1}" lvm size=1GiB
  inc storage volume create "${pool1}" v1
  ! inc project set p1 limits.disk 1GiB || false
  inc storage volume delete "${pool1}" v1
  inc storage delete "${pool1}"

  # Create a custom volume without any size property defined.
  inc storage volume create "${pool}" v1

  # Set a size on the custom volume.
  inc storage volume set "${pool}" v1 size 50MiB

  # Can't set the project's disk limit below the current aggregate count.
  ! inc project set p1 limits.disk 190MiB || false

  # Set the project's disk limit
  inc project set p1 limits.disk 250MiB

  # Can't update the project's disk limit below the current aggregate count.
  ! inc project set p1 limits.disk 190MiB || false

  # Changing profile or instance root device size or volume size above the
  # aggregate project's limit is not possible.
  ! inc profile device set default root size=160MiB || false
  ! inc config device set c2 root size 110MiB || false
  ! inc storage volume set "${pool}" v1 size 110MiB || false

  # Can't create a custom volume without specifying a size.
  ! inc storage volume create "${pool}" v2 || false

  # Disk limits can be updated if they stay within limits.
  inc project set p1 limits.disk 204900KiB
  inc profile device set default root size=90MiB
  inc config device set c2 root size 60MiB

  # Can't upload an image if that would exceed the current quota.
  ! deps/import-busybox --project p1 --template start --alias otherimage || false

  # Can't export publish an instance as image if that would exceed the current
  # quota.
  ! inc publish c1 --alias=c1image || false

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
    inc project switch default
    spawn_incus "${INCUS_REMOTE_DIR}" true
    inc project switch p1

    INCUS_REMOTE_ADDR=$(cat "${INCUS_REMOTE_DIR}/incus.addr")
    (INCUS_DIR=${INCUS_REMOTE_DIR} deps/import-busybox --alias remoteimage --template start --public)

    inc remote add l2 "${INCUS_REMOTE_ADDR}" --accept-certificate --password foo

    # Relax all constraints except the disk limits, which won't be enough for the
    # image to be downloaded.
    inc profile device set default root size=500KiB
    inc project set p1 limits.disk 111MiB
    inc project unset p1 limits.containers
    inc project unset p1 limits.cpu
    inc project unset p1 limits.memory
    inc project unset p1 limits.processes

    # Can't download a remote image if that would exceed the current quota.
    ! inc init l2:remoteimage c3 || false
  fi

  inc storage volume delete "${pool}" v1
  inc delete c1
  inc delete c2
  inc image delete testimage
  inc profile delete unrestricted

  inc project switch default
  inc project delete p1

  # Start with clean project.
  inc project create p1
  inc project switch p1

  # Relaxing restricted.containers.lowlevel to 'allow' makes it possible set
  # low-level keys.
  inc project set p1 restricted.containers.lowlevel allow

  # Add a root device to the default profile of the project and import an image.
  pool="incustest-$(basename "${INCUS_DIR}")"
  inc profile device add default root disk path="/" pool="${pool}"

  deps/import-busybox --project p1 --alias testimage

  # Create a couple of containers in the project.
  inc init testimage c1 -c limits.memory=1GiB
  inc init testimage c2 -c limits.memory=1GiB

  inc export c1
  inc delete c1

  # Configure a valid project memory limit.
  inc project set p1 limits.memory 1GiB

  # Can't import the backup as it would exceed the 1GiB project memory limit.
  ! inc import c1.tar.gz || false

  rm c1.tar.gz
  inc delete c2
  inc image delete testimage
  inc project switch default
  inc project delete p1

  if [ "${INCUS_BACKEND}" = "dir" ] || [ "${INCUS_BACKEND}" = "zfs" ]; then
    inc remote remove l2
    kill_incus "$INCUS_REMOTE_DIR"
  fi
}

# Set restrictions on projects.
test_projects_restrictions() {
  # Add a managed network.
  netManaged="inc$$"
  inc network create "${netManaged}"

  netUnmanaged="${netManaged}-unm"
  ip link add "${netUnmanaged}" type bridge

  # Create a project and switch to it
  inc project create p1 -c features.storage.volumes=false
  inc project switch p1

  # Check with restricted unset and restricted.devices.nic unset that managed & unmanaged networks are accessible.
  inc network list | grep -F "${netManaged}"
  inc network list | grep -F "${netUnmanaged}"
  inc network show "${netManaged}"
  inc network show "${netUnmanaged}"

  # Check with restricted unset and restricted.devices.nic=block that managed & unmanaged networks are accessible.
  inc project set p1 restricted.devices.nic=block
  inc network list | grep -F "${netManaged}"
  inc network list | grep -F "${netUnmanaged}"
  inc network show "${netManaged}"
  inc network show "${netUnmanaged}"

  # Check with restricted=true and restricted.devices.nic=block that managed & unmanaged networks are inaccessible.
  inc project set p1 restricted=true
  ! inc network list | grep -F "${netManaged}"|| false
  ! inc network show "${netManaged}" || false
  ! inc network list | grep -F "${netUnmanaged}"|| false
  ! inc network show "${netUnmanaged}" || false

  # Check with restricted=true and restricted.devices.nic=managed that managed networks are accessible and that
  # unmanaged networks are inaccessible.
  inc project set p1 restricted.devices.nic=managed
  inc network list | grep -F "${netManaged}"
  inc network show "${netManaged}"
  ! inc network list | grep -F "${netUnmanaged}"|| false
  ! inc network show "${netUnmanaged}" || false

  # Check with restricted.devices.nic=allow and restricted.networks.access set to a network other than the existing
  # managed and unmanaged ones that they are inaccessible.
  inc project set p1 restricted.devices.nic=allow
  inc project set p1 restricted.networks.access=foo
  ! inc network list | grep -F "${netManaged}"|| false
  ! inc network show "${netManaged}" || false
  ! inc network info "${netManaged}"|| false

  ! inc network list | grep -F "${netUnmanaged}"|| false
  ! inc network show "${netUnmanaged}" || false
  ! inc network info "${netUnmanaged}"|| false

  ! inc network set "${netManaged}" user.foo=bah || false
  ! inc network get "${netManaged}" ipv4.address || false
  ! inc network info "${netManaged}"|| false
  ! inc network delete "${netManaged}" || false

  ! inc profile device add default eth0 nic nictype=bridge parent=netManaged || false
  ! inc profile device add default eth0 nic nictype=bridge parent=netUnmanaged || false

  ip link delete "${netUnmanaged}"

  # Disable restrictions to allow devices to be added to profile.
  inc project unset p1 restricted.networks.access
  inc project set p1 restricted.devices.nic=managed
  inc project set p1 restricted=false

  # Add a root device to the default profile of the project and import an image.
  pool="incustest-$(basename "${INCUS_DIR}")"
  inc profile device add default root disk path="/" pool="${pool}"

  deps/import-busybox --project p1 --alias testimage
  fingerprint="$(inc image list -c f --format json | jq -r .[0].fingerprint)"

  # Add a volume.
  inc storage volume create "${pool}" "v-proj$$"

  # Enable all restrictions.
  inc project set p1 restricted=true

  # It's not possible to create nested containers.
  ! inc profile set default security.nesting=true || false
  ! inc init testimage c1 -c security.nesting=true || false

  # It's not possible to use forbidden low-level options
  ! inc profile set default "raw.idmap=both 0 0" || false
  ! inc init testimage c1 -c "raw.idmap=both 0 0" || false
  ! inc init testimage c1 -c volatile.uuid="foo" || false

  # It's not possible to create privileged containers.
  ! inc profile set default security.privileged=true || false
  ! inc init testimage c1 -c security.privileged=true || false

  # It's possible to create non-isolated containers.
  inc init testimage c1 -c security.idmap.isolated=false

  # It's not possible to change low-level options
  ! inc config set c1 "raw.idmap=both 0 0" || false
  ! inc config set c1 volatile.uuid="foo" || false

  # It's not possible to attach character devices.
  ! inc profile device add default tty unix-char path=/dev/ttyS0 || false
  ! inc config device add c1 tty unix-char path=/dev/ttyS0 || false

  # It's not possible to attach raw network devices.
  ! inc profile device add default eth0 nic nictype=p2p || false

  # It's not possible to attach non-managed disk devices.
  ! inc profile device add default testdir disk source="${TEST_DIR}" path=/mnt || false
  ! inc config device add c1 testdir disk source="${TEST_DIR}" path=/mnt || false

  # It's possible to attach managed network devices.
  inc profile device add default eth0 nic network="${netManaged}"

  # It's possible to attach disks backed by a pool.
  inc config device add c1 data disk pool="${pool}" path=/mnt source="v-proj$$"

  # It's not possible to set restricted.containers.nic to 'block' because
  # there's an instance using the managed network.
  ! inc project set p1 restricted.devices.nic=block || false

  # Relaxing restricted.containers.nic to 'allow' makes it possible to attach
  # raw network devices.
  inc project set p1 restricted.devices.nic=allow
  inc config device add c1 eth1 nic nictype=p2p

  # Relaxing restricted.containers.disk to 'allow' makes it possible to attach
  # non-managed disks.
  inc project set p1 restricted.devices.disk=allow
  inc config device add c1 testdir disk source="${TEST_DIR}" path=/foo

  # Relaxing restricted.containers.lowlevel to 'allow' makes it possible set
  # low-level keys.
  inc project set p1 restricted.containers.lowlevel=allow
  inc config set c1 "raw.idmap=both 0 0"

  inc delete c1

  # Setting restricted.containers.disk to 'block' allows only the root disk
  # device.
  inc project set p1 restricted.devices.disk=block
  ! inc profile device add default data disk pool="${pool}" path=/mnt source="v-proj$$" || false

  # Setting restricted.containers.nesting to 'allow' makes it possible to create
  # nested containers.
  inc project set p1 restricted.containers.nesting=allow
  inc init testimage c1 -c security.nesting=true

  # It's not possible to set restricted.containers.nesting back to 'block',
  # because there's an instance with security.nesting=true.
  ! inc project set p1 restricted.containers.nesting=block || false

  inc delete c1

  # Setting restricted.containers.lowlevel to 'allow' makes it possible to set
  # low-level options.
  inc project set p1 restricted.containers.lowlevel=allow
  inc init testimage c1 -c "raw.idmap=both 0 0" || false

  # It's not possible to set restricted.containers.lowlevel back to 'block',
  # because there's an instance with raw.idmap set.
  ! inc project set p1 restricted.containers.lowlevel=block || false

  inc delete c1

  # Setting restricted.containers.privilege to 'allow' makes it possible to create
  # privileged containers.
  inc project set p1 restricted.containers.privilege=allow
  inc init testimage c1 -c security.privileged=true

  # It's not possible to set restricted.containers.privilege back to
  # 'unprivileged', because there's an instance with security.privileged=true.
  ! inc project set p1 restricted.containers.privilege=unprivileged || false

  # Test expected syscall interception behavior.
  ! inc config set c1 security.syscalls.intercept.mknod=true || false
  inc config set c1 security.syscalls.intercept.mknod=false
  inc project set p1 restricted.containers.interception=block
  ! inc config set c1 security.syscalls.intercept.mknod=true || false
  inc project set p1 restricted.containers.interception=allow
  inc config set c1 security.syscalls.intercept.mknod=true
  inc config set c1 security.syscalls.intercept.mount=true
  ! inc config set c1 security.syscalls.intercept.mount.allow=ext4 || false

  inc delete c1

  inc image delete testimage

  inc project switch default
  inc project delete p1

  inc network delete "${netManaged}"
  inc storage volume delete "${pool}" "v-proj$$"
}

# Test project state api
test_projects_usage() {
  # Set configuration on the default project
  inc project create test-usage \
    -c limits.cpu=5 \
    -c limits.memory=1GiB \
    -c limits.disk=10GiB \
    -c limits.networks=3 \
    -c limits.processes=40

  # Create a profile defining resource allocations
  inc profile show default --project default | inc profile edit default --project test-usage
  inc profile set default --project test-usage \
    limits.cpu=1 \
    limits.memory=512MiB \
    limits.processes=20
  inc profile device set default root size=3GiB --project test-usage

  # Spin up a container
  deps/import-busybox --project test-usage --alias testimage
  inc init testimage c1 --project test-usage
  inc project info test-usage

  inc project info test-usage --format csv | grep -q "CONTAINERS,UNLIMITED,1"
  inc project info test-usage --format csv | grep -q "CPU,5,1"
  inc project info test-usage --format csv | grep -q "DISK,10.00GiB,3.00GiB"
  inc project info test-usage --format csv | grep -q "INSTANCES,UNLIMITED,1"
  inc project info test-usage --format csv | grep -q "MEMORY,1.00GiB,512.00MiB"
  inc project info test-usage --format csv | grep -q "NETWORKS,3,0"
  inc project info test-usage --format csv | grep -q "PROCESSES,40,20"
  inc project info test-usage --format csv | grep -q "VIRTUAL-MACHINES,UNLIMITED,0"

  inc delete c1 --project test-usage
  inc image delete testimage --project test-usage
  inc project delete test-usage
}
