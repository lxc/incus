ensure_removed() {
  bad=0
  incus exec foo -- stat /dev/ttyS0 && bad=1
  if [ "${bad}" -eq 1 ]; then
    echo "device should have been removed; $*"
    false
  fi
}

dounixdevtest() {
    incus start foo
    incus config device add foo tty unix-char "$@"
    incus exec foo -- stat /dev/ttyS0
    incus restart foo --force
    incus exec foo -- stat /dev/ttyS0
    incus config device remove foo tty
    ensure_removed "was not hot-removed"
    incus restart foo --force
    ensure_removed "removed device re-appeared after container reboot"
    incus stop foo --force
}

testunixdevs() {
  if [ ! -e /dev/ttyS0 ] || [ ! -e /dev/ttyS1 ]; then
     echo "==> SKIP: /dev/ttyS0 or /dev/ttyS1 are missing"
     return
  fi

  echo "Testing passing char device /dev/ttyS0"
  dounixdevtest path=/dev/ttyS0

  echo "Testing passing char device 4 64"
  dounixdevtest path=/dev/ttyS0 major=4 minor=64

  echo "Testing passing char device source=/dev/ttyS0"
  dounixdevtest source=/dev/ttyS0

  echo "Testing passing char device path=/dev/ttyS0 source=/dev/ttyS0"
  dounixdevtest path=/dev/ttyS0 source=/dev/ttyS0

  echo "Testing passing char device path=/dev/ttyS0 source=/dev/ttyS1"
  dounixdevtest path=/dev/ttyS0 source=/dev/ttyS1
}

ensure_fs_unmounted() {
  bad=0
  incus exec foo -- stat /mnt/hello && bad=1
  if [ "${bad}" -eq 1 ]; then
    echo "device should have been removed; $*"
    false
  fi
}

testloopmounts() {
  loopfile=$(mktemp -p "${TEST_DIR}" loop_XXX)
  dd if=/dev/zero of="${loopfile}" bs=1M seek=200 count=1
  mkfs.ext4 -F "${loopfile}"

  lpath=$(losetup --show -f "${loopfile}")
  if [ ! -e "${lpath}" ]; then
    echo "failed to setup loop"
    false
  fi
  echo "${lpath}" >> "${TEST_DIR}/loops"

  mkdir -p "${TEST_DIR}/mnt"
  mount "${lpath}" "${TEST_DIR}/mnt" || { echo "loop mount failed"; return; }
  touch "${TEST_DIR}/mnt/hello"
  umount -l "${TEST_DIR}/mnt"
  incus start foo
  incus config device add foo mnt disk source="${lpath}" path=/mnt
  incus exec foo -- stat /mnt/hello
  # Note - we need to add a set_running_config_item to lxc
  # or work around its absence somehow.  Once that's done, we
  # can run the following two lines:
  #incus exec foo -- reboot
  #incus exec foo -- stat /mnt/hello
  incus restart foo --force
  incus exec foo -- stat /mnt/hello
  incus config device remove foo mnt
  ensure_fs_unmounted "fs should have been hot-unmounted"
  incus restart foo --force
  ensure_fs_unmounted "removed fs re-appeared after restart"
  incus stop foo --force
  losetup -d "${lpath}"
  sed -i "\\|^${lpath}|d" "${TEST_DIR}/loops"
}

test_mount_order() {
  mkdir -p "${TEST_DIR}/order/empty"
  mkdir -p "${TEST_DIR}/order/full"
  touch "${TEST_DIR}/order/full/filler"

  # The idea here is that sometimes (depending on how golang randomizes the
  # config) the empty dir will have the contents of full in it, but sometimes
  # it won't depending on whether the devices below are processed in order or
  # not. This should not be racy, and they should *always* be processed in path
  # order, so the filler file should always be there.
  incus config device add foo order disk source="${TEST_DIR}/order" path=/mnt
  incus config device add foo orderFull disk source="${TEST_DIR}/order/full" path=/mnt/empty

  incus start foo
  incus exec foo -- cat /mnt/empty/filler
  incus stop foo --force
}

test_config_profiles() {
  # Unset INCUS_DEVMONITOR_DIR as this test uses devices in /dev instead of TEST_DIR.
  unset INCUS_DEVMONITOR_DIR
  shutdown_incus "${INCUS_DIR}"
  respawn_incus "${INCUS_DIR}" true

  ensure_import_testimage

  incus init testimage foo -s "incustest-$(basename "${INCUS_DIR}")"
  incus profile list | grep default

  # let's check that 'incus config profile' still works while it's deprecated
  incus config profile list | grep default

  # setting an invalid config item should error out when setting it, not get
  # into the database and never let the user edit the container again.
  ! incus config set foo raw.lxc lxc.notaconfigkey=invalid || false

  # validate unsets
  incus profile set default user.foo bar
  incus profile show default | grep -q user.foo
  incus profile unset default user.foo
  ! incus profile show default | grep -q user.foo || false

  incus profile device set default eth0 limits.egress 100Mbit
  incus profile show default | grep -q limits.egress
  incus profile device unset default eth0 limits.egress
  ! incus profile show default | grep -q limits.egress || false

  # check that various profile application mechanisms work
  incus profile create one
  incus profile create two
  incus profile assign foo one,two
  [ "$(incus list -f json foo | jq -r '.[0].profiles | join(" ")')" = "one two" ]
  incus profile assign foo ""
  [ "$(incus list -f json foo | jq -r '.[0].profiles | join(" ")')" = "" ]
  incus profile apply foo one # backwards compat check with `incus profile apply`
  [ "$(incus list -f json foo | jq -r '.[0].profiles | join(" ")')" = "one" ]
  incus profile assign foo ""
  incus profile add foo one
  [ "$(incus list -f json foo | jq -r '.[0].profiles | join(" ")')" = "one" ]
  incus profile remove foo one
  [ "$(incus list -f json foo | jq -r '.[0].profiles | join(" ")')" = "" ]

  incus profile create stdintest
  echo "BADCONF" | incus profile set stdintest user.user_data -
  incus profile show stdintest | grep BADCONF
  incus profile delete stdintest

  echo "BADCONF" | incus config set foo user.user_data -
  incus config show foo | grep BADCONF
  incus config unset foo user.user_data

  mkdir -p "${TEST_DIR}/mnt1"
  incus config device add foo mnt1 disk source="${TEST_DIR}/mnt1" path=/mnt1 readonly=true
  incus profile create onenic
  incus profile device add onenic eth0 nic nictype=p2p
  incus profile assign foo onenic
  incus profile create unconfined

  incus profile set unconfined raw.lxc "lxc.apparmor.profile=unconfined"

  incus profile assign foo onenic,unconfined

  # test profile rename
  incus profile create foo
  incus profile rename foo bar
  incus profile list | grep -qv foo  # the old name is gone
  incus profile delete bar

  incus config device list foo | grep mnt1
  incus config device show foo | grep "/mnt1"
  incus config show foo | grep "onenic" -A1 | grep "unconfined"
  incus profile list | grep onenic
  incus profile device list onenic | grep eth0
  incus profile device show onenic | grep p2p

  # test live-adding a nic
  veth_host_name="veth$$"
  incus start foo
  incus exec foo -- cat /proc/self/mountinfo | grep -q "/mnt1.*ro,"
  ! incus config show foo | grep -q "raw.lxc" || false
  incus config show foo --expanded | grep -q "raw.lxc"
  ! incus config show foo | grep -v "volatile.eth0" | grep -q "eth0" || false
  incus config show foo --expanded | grep -v "volatile.eth0" | grep -q "eth0"
  incus config device add foo eth2 nic nictype=p2p name=eth10 host_name="${veth_host_name}"
  incus exec foo -- /sbin/ifconfig -a | grep eth0
  incus exec foo -- /sbin/ifconfig -a | grep eth10
  incus config device list foo | grep eth2
  incus config device remove foo eth2

  # test live-adding a disk
  mkdir "${TEST_DIR}/mnt2"
  touch "${TEST_DIR}/mnt2/hosts"
  incus config device add foo mnt2 disk source="${TEST_DIR}/mnt2" path=/mnt2 readonly=true
  incus exec foo -- cat /proc/self/mountinfo | grep -q "/mnt2.*ro,"
  incus exec foo -- ls /mnt2/hosts
  incus stop foo --force
  incus start foo
  incus exec foo -- ls /mnt2/hosts
  incus config device remove foo mnt2
  ! incus exec foo -- ls /mnt2/hosts || false
  incus stop foo --force
  incus start foo
  ! incus exec foo -- ls /mnt2/hosts || false
  incus stop foo --force

  incus config set foo user.prop value
  incus list user.prop=value | grep foo
  incus config unset foo user.prop

  # Test for invalid raw.lxc
  ! incus config set foo raw.lxc a || false
  ! incus profile set default raw.lxc a || false

  bad=0
  incus list user.prop=value | grep foo && bad=1
  if [ "${bad}" -eq 1 ]; then
    echo "property unset failed"
    false
  fi

  bad=0
  incus config set foo user.prop 2>/dev/null && bad=1
  if [ "${bad}" -eq 1 ]; then
    echo "property set succeeded when it shouldn't have"
    false
  fi

  # Test unsetting config keys
  incus config set core.metrics_authentication false
  [ "$(incus config get core.metrics_authentication)" = "false" ]

  incus config unset core.metrics_authentication
  [ -z "$(incus config get core.metrics_authentication)" ]

  # Validate user.* keys
  ! incus config set user.⍾ foo || false
  incus config set user.foo bar
  incus config unset user.foo

  testunixdevs

  testloopmounts

  test_mount_order

  incus delete foo

  incus init testimage foo -s "incustest-$(basename "${INCUS_DIR}")"
  incus profile assign foo onenic,unconfined
  incus start foo

  if [ -e /sys/module/apparmor ]; then
    incus exec foo -- grep -xF unconfined /proc/self/attr/current
  fi
  incus exec foo -- ls /sys/class/net | grep eth0

  incus stop foo --force
  incus delete foo
}


test_config_edit() {
    if ! tty -s; then
        echo "==> SKIP: test_config_edit requires a terminal"
        return
    fi

    ensure_import_testimage

    incus init testimage foo -s "incustest-$(basename "${INCUS_DIR}")"
    incus config show foo | sed 's/^description:.*/description: bar/' | incus config edit foo
    incus config show foo | grep -q 'description: bar'

    # Check instance name is included in edit screen.
    cmd=$(unset -f incus; command -v incus)
    output=$(EDITOR="cat" timeout --foreground 120 "${cmd}" config edit foo)
    echo "${output}" | grep "name: foo"

    # Check expanded config isn't included in edit screen.
    ! echo "${output}" | grep "expanded" || false

    incus delete foo
}

test_property() {
  ensure_import_testimage

  incus init testimage foo -s "incustest-$(basename "${INCUS_DIR}")"

  # Set a property of an instance
  incus config set foo description="a new description" --property
  # Check that the property is set
  incus config show foo | grep -q "description: a new description"

  # Unset a property of an instance
  incus config unset foo description --property
  # Check that the property is unset
  ! incus config show foo | grep -q "description: a new description" || false

  # Set a property of an instance (bool)
  incus config set foo ephemeral=true --property
  # Check that the property is set
  incus config show foo | grep -q "ephemeral: true"

  # Unset a property of an instance (bool)
  incus config unset foo ephemeral --property
  # Check that the property is unset (i.e false)
  incus config show foo | grep -q "ephemeral: false"

  # Create a snap of the instance to set its expiration timestamp
  incus snapshot create foo s1
  incus config set foo/s1 expires_at="2024-03-23T17:38:37.753398689-04:00" --property
  incus config get foo/s1 expires_at --property | grep -q "2024-03-23 17:38:37.753398689 -0400 -0400"
  incus config show foo/s1 | grep -q "expires_at: 2024-03-23T17:38:37.753398689-04:00"
  incus config unset foo/s1 expires_at --property
  incus config show foo/s1 | grep -q "expires_at: 0001-01-01T00:00:00Z"


  # Create a storage volume, create a volume snapshot and set its expiration timestamp
  # shellcheck disable=2039,3043
  local storage_pool
  storage_pool="incustest-$(basename "${INCUS_DIR}")"
  storage_volume="${storage_pool}-vol"

  incus storage volume create "${storage_pool}" "${storage_volume}"
  incus launch testimage c1 -s "${storage_pool}"

  # This will create a snapshot named 'snap0'
  incus storage volume snapshot create "${storage_pool}" "${storage_volume}"

  incus storage volume set "${storage_pool}" "${storage_volume}"/snap0 expires_at="2024-03-23T17:38:37.753398689-04:00" --property
  incus storage volume snapshot show "${storage_pool}" "${storage_volume}/snap0" | grep 'expires_at: 2024-03-23T17:38:37.753398689-04:00'
  incus storage volume unset "${storage_pool}" "${storage_volume}"/snap0 expires_at --property
  incus storage volume snapshot show "${storage_pool}" "${storage_volume}/snap0" | grep 'expires_at: 0001-01-01T00:00:00Z'

  incus delete -f c1
  incus storage volume delete "${storage_pool}" "${storage_volume}"
  incus delete -f foo
}

test_config_edit_container_snapshot_pool_config() {
    # shellcheck disable=2034,2039,2155,3043
    local storage_pool="incustest-$(basename "${INCUS_DIR}")"

    ensure_import_testimage

    incus init testimage c1 -s "$storage_pool"
    incus snapshot create c1 s1
    # edit the container volume name
    incus storage volume show "$storage_pool" container/c1 | \
        sed 's/^description:.*/description: bar/' | \
        incus storage volume edit "$storage_pool" container/c1
    incus storage volume show "$storage_pool" container/c1 | grep -q 'description: bar'
    # edit the container snapshot volume name
    incus storage volume snapshot show "$storage_pool" container/c1/s1 | \
        sed 's/^description:.*/description: baz/' | \
        incus storage volume edit "$storage_pool" container/c1/s1
    incus storage volume snapshot show "$storage_pool" container/c1/s1 | grep -q 'description: baz'
    incus delete c1
}

test_container_metadata() {
    ensure_import_testimage
    incus init testimage c

    # metadata for the container are printed
    incus config metadata show c | grep -q BusyBox

    # metadata can be edited
    incus config metadata show c | sed 's/BusyBox/BB/' | incus config metadata edit c
    incus config metadata show c | grep -q BB

    # templates can be listed
    incus config template list c | grep -q template.tpl

    # template content can be returned
    incus config template show c template.tpl | grep -q "name:"

    # templates can be added
    incus config template create c my.tpl
    incus config template list c | grep -q my.tpl

    # template content can be updated
    echo "some content" | incus config template edit c my.tpl
    incus config template show c my.tpl | grep -q "some content"

    # templates can be removed
    incus config template delete c my.tpl
    ! incus config template list c | grep -q my.tpl || false

    incus delete c
}

test_container_snapshot_config() {
    if ! tty -s; then
        echo "==> SKIP: test_container_snapshot_config requires a terminal"
        return
    fi

    ensure_import_testimage

    incus init testimage foo -s "incustest-$(basename "${INCUS_DIR}")"
    incus snapshot create foo
    incus config show foo/snap0 | grep -q 'expires_at: 0001-01-01T00:00:00Z'

    echo 'expires_at: 2100-01-01T00:00:00Z' | incus config edit foo/snap0
    incus config show foo/snap0 | grep -q 'expires_at: 2100-01-01T00:00:00Z'

    # Remove expiry date using zero time
    echo 'expires_at: 0001-01-01T00:00:00Z' | incus config edit foo/snap0
    incus config show foo/snap0 | grep -q 'expires_at: 0001-01-01T00:00:00Z'

    echo 'expires_at: 2100-01-01T00:00:00Z' | incus config edit foo/snap0
    incus config show foo/snap0 | grep -q 'expires_at: 2100-01-01T00:00:00Z'

    # Remove expiry date using empty value
    echo 'expires_at:' | incus config edit foo/snap0
    incus config show foo/snap0 | grep -q 'expires_at: 0001-01-01T00:00:00Z'

    # Check instance name is included in edit screen.
    cmd=$(unset -f incus; command -v incus)
    output=$(EDITOR="cat" timeout --foreground 120 "${cmd}" config edit foo/snap0)
    echo "${output}" | grep "name: snap0"

    # Check expanded config isn't included in edit screen.
    ! echo "${output}"  | grep "expanded" || false

    incus delete -f foo
}
