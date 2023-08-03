ensure_removed() {
  bad=0
  inc exec foo -- stat /dev/ttyS0 && bad=1
  if [ "${bad}" -eq 1 ]; then
    echo "device should have been removed; $*"
    false
  fi
}

dounixdevtest() {
    inc start foo
    inc config device add foo tty unix-char "$@"
    inc exec foo -- stat /dev/ttyS0
    inc restart foo --force
    inc exec foo -- stat /dev/ttyS0
    inc config device remove foo tty
    ensure_removed "was not hot-removed"
    inc restart foo --force
    ensure_removed "removed device re-appeared after container reboot"
    inc stop foo --force
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
  inc exec foo -- stat /mnt/hello && bad=1
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
  inc start foo
  inc config device add foo mnt disk source="${lpath}" path=/mnt
  inc exec foo stat /mnt/hello
  # Note - we need to add a set_running_config_item to lxc
  # or work around its absence somehow.  Once that's done, we
  # can run the following two lines:
  #inc exec foo reboot
  #inc exec foo stat /mnt/hello
  inc restart foo --force
  inc exec foo stat /mnt/hello
  inc config device remove foo mnt
  ensure_fs_unmounted "fs should have been hot-unmounted"
  inc restart foo --force
  ensure_fs_unmounted "removed fs re-appeared after restart"
  inc stop foo --force
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
  inc config device add foo order disk source="${TEST_DIR}/order" path=/mnt
  inc config device add foo orderFull disk source="${TEST_DIR}/order/full" path=/mnt/empty

  inc start foo
  inc exec foo -- cat /mnt/empty/filler
  inc stop foo --force
}

test_config_profiles() {
  # Unset INCUS_DEVMONITOR_DIR as this test uses devices in /dev instead of TEST_DIR.
  unset INCUS_DEVMONITOR_DIR
  shutdown_incus "${INCUS_DIR}"
  respawn_incus "${INCUS_DIR}" true

  ensure_import_testimage

  inc init testimage foo -s "incustest-$(basename "${INCUS_DIR}")"
  inc profile list | grep default

  # let's check that 'inc config profile' still works while it's deprecated
  inc config profile list | grep default

  # setting an invalid config item should error out when setting it, not get
  # into the database and never let the user edit the container again.
  ! inc config set foo raw.lxc lxc.notaconfigkey=invalid || false

  # validate unsets
  inc profile set default user.foo bar
  inc profile show default | grep -q user.foo
  inc profile unset default user.foo
  ! inc profile show default | grep -q user.foo || false

  inc profile device set default eth0 limits.egress 100Mbit
  inc profile show default | grep -q limits.egress
  inc profile device unset default eth0 limits.egress
  ! inc profile show default | grep -q limits.egress || false

  # check that various profile application mechanisms work
  inc profile create one
  inc profile create two
  inc profile assign foo one,two
  [ "$(inc list -f json foo | jq -r '.[0].profiles | join(" ")')" = "one two" ]
  inc profile assign foo ""
  [ "$(inc list -f json foo | jq -r '.[0].profiles | join(" ")')" = "" ]
  inc profile apply foo one # backwards compat check with `inc profile apply`
  [ "$(inc list -f json foo | jq -r '.[0].profiles | join(" ")')" = "one" ]
  inc profile assign foo ""
  inc profile add foo one
  [ "$(inc list -f json foo | jq -r '.[0].profiles | join(" ")')" = "one" ]
  inc profile remove foo one
  [ "$(inc list -f json foo | jq -r '.[0].profiles | join(" ")')" = "" ]

  inc profile create stdintest
  echo "BADCONF" | inc profile set stdintest user.user_data -
  inc profile show stdintest | grep BADCONF
  inc profile delete stdintest

  echo "BADCONF" | inc config set foo user.user_data -
  inc config show foo | grep BADCONF
  inc config unset foo user.user_data

  mkdir -p "${TEST_DIR}/mnt1"
  inc config device add foo mnt1 disk source="${TEST_DIR}/mnt1" path=/mnt1 readonly=true
  inc profile create onenic
  inc profile device add onenic eth0 nic nictype=p2p
  inc profile assign foo onenic
  inc profile create unconfined

  # Look at the LXC version to decide whether to use the new
  # or the new config key for apparmor.
  lxc_version=$(lxc info | awk '/driver_version:/ {print $NF}')
  lxc_major=$(echo "${lxc_version}" | cut -d. -f1)
  lxc_minor=$(echo "${lxc_version}" | cut -d. -f2)
  if [ "${lxc_major}" -lt 2 ] || { [ "${lxc_major}" = "2" ] && [ "${lxc_minor}" -lt "1" ]; }; then
      inc profile set unconfined raw.lxc "lxc.aa_profile=unconfined"
  else
      inc profile set unconfined raw.lxc "lxc.apparmor.profile=unconfined"
  fi

  inc profile assign foo onenic,unconfined

  # test profile rename
  inc profile create foo
  inc profile rename foo bar
  inc profile list | grep -qv foo  # the old name is gone
  inc profile delete bar

  inc config device list foo | grep mnt1
  inc config device show foo | grep "/mnt1"
  inc config show foo | grep "onenic" -A1 | grep "unconfined"
  inc profile list | grep onenic
  inc profile device list onenic | grep eth0
  inc profile device show onenic | grep p2p

  # test live-adding a nic
  veth_host_name="veth$$"
  inc start foo
  inc exec foo -- cat /proc/self/mountinfo | grep -q "/mnt1.*ro,"
  ! inc config show foo | grep -q "raw.lxc" || false
  inc config show foo --expanded | grep -q "raw.lxc"
  ! inc config show foo | grep -v "volatile.eth0" | grep -q "eth0" || false
  inc config show foo --expanded | grep -v "volatile.eth0" | grep -q "eth0"
  inc config device add foo eth2 nic nictype=p2p name=eth10 host_name="${veth_host_name}"
  inc exec foo -- /sbin/ifconfig -a | grep eth0
  inc exec foo -- /sbin/ifconfig -a | grep eth10
  inc config device list foo | grep eth2
  inc config device remove foo eth2

  # test live-adding a disk
  mkdir "${TEST_DIR}/mnt2"
  touch "${TEST_DIR}/mnt2/hosts"
  inc config device add foo mnt2 disk source="${TEST_DIR}/mnt2" path=/mnt2 readonly=true
  inc exec foo -- cat /proc/self/mountinfo | grep -q "/mnt2.*ro,"
  inc exec foo -- ls /mnt2/hosts
  inc stop foo --force
  inc start foo
  inc exec foo -- ls /mnt2/hosts
  inc config device remove foo mnt2
  ! inc exec foo -- ls /mnt2/hosts || false
  inc stop foo --force
  inc start foo
  ! inc exec foo -- ls /mnt2/hosts || false
  inc stop foo --force

  inc config set foo user.prop value
  inc list user.prop=value | grep foo
  inc config unset foo user.prop

  # Test for invalid raw.lxc
  ! inc config set foo raw.lxc a || false
  ! inc profile set default raw.lxc a || false

  bad=0
  inc list user.prop=value | grep foo && bad=1
  if [ "${bad}" -eq 1 ]; then
    echo "property unset failed"
    false
  fi

  bad=0
  inc config set foo user.prop 2>/dev/null && bad=1
  if [ "${bad}" -eq 1 ]; then
    echo "property set succeeded when it shouldn't have"
    false
  fi

  testunixdevs

  testloopmounts

  test_mount_order

  inc delete foo

  inc init testimage foo -s "incustest-$(basename "${INCUS_DIR}")"
  inc profile assign foo onenic,unconfined
  inc start foo

  if [ -e /sys/module/apparmor ]; then
    inc exec foo -- grep -xF unconfined /proc/self/attr/current
  fi
  inc exec foo -- ls /sys/class/net | grep eth0

  inc stop foo --force
  inc delete foo
}


test_config_edit() {
    if ! tty -s; then
        echo "==> SKIP: Test requires a terminal"
        return
    fi

    ensure_import_testimage

    inc init testimage foo -s "incustest-$(basename "${INCUS_DIR}")"
    inc config show foo | sed 's/^description:.*/description: bar/' | inc config edit foo
    inc config show foo | grep -q 'description: bar'

    # Check instance name is included in edit screen.
    cmd=$(unset -f inc; command -v inc)
    output=$(EDITOR="cat" timeout --foreground 120 "${cmd}" config edit foo)
    echo "${output}" | grep "name: foo"

    # Check expanded config isn't included in edit screen.
    ! echo "${output}" | grep "expanded" || false

    inc delete foo
}

test_property() {
  ensure_import_testimage

  inc init testimage foo -s "incustest-$(basename "${INCUS_DIR}")"

  # Set a property of an instance
  inc config set foo description="a new description" --property
  # Check that the property is set
  inc config show foo | grep -q "description: a new description"

  # Unset a property of an instance
  inc config unset foo description --property
  # Check that the property is unset
  ! inc config show foo | grep -q "description: a new description" || false

  # Set a property of an instance (bool)
  inc config set foo ephemeral=true --property
  # Check that the property is set
  inc config show foo | grep -q "ephemeral: true"

  # Unset a property of an instance (bool)
  inc config unset foo ephemeral --property
  # Check that the property is unset (i.e false)
  inc config show foo | grep -q "ephemeral: false"

  # Create a snap of the instance to set its expiration timestamp
  inc snapshot foo s1
  inc config set foo/s1 expires_at="2024-03-23T17:38:37.753398689-04:00" --property
  inc config show foo/s1 | grep -q "expires_at: 2024-03-23T17:38:37.753398689-04:00"
  inc config unset foo/s1 expires_at --property
  inc config show foo/s1 | grep -q "expires_at: 0001-01-01T00:00:00Z"


  # Create a storage volume, create a volume snapshot and set its expiration timestamp
  # shellcheck disable=2039,3043
  local storage_pool
  storage_pool="incustest-$(basename "${INCUS_DIR}")"
  storage_volume="${storage_pool}-vol"

  inc storage volume create "${storage_pool}" "${storage_volume}"
  inc launch testimage c1 -s "${storage_pool}"

  # This will create a snapshot named 'snap0'
  inc storage volume snapshot "${storage_pool}" "${storage_volume}"

  inc storage volume set "${storage_pool}" "${storage_volume}"/snap0 expires_at="2024-03-23T17:38:37.753398689-04:00" --property
  inc storage volume show "${storage_pool}" "${storage_volume}/snap0" | grep 'expires_at: 2024-03-23T17:38:37.753398689-04:00'
  inc storage volume unset "${storage_pool}" "${storage_volume}"/snap0 expires_at --property
  inc storage volume show "${storage_pool}" "${storage_volume}/snap0" | grep 'expires_at: 0001-01-01T00:00:00Z'

  inc delete -f c1
  inc storage volume delete "${storage_pool}" "${storage_volume}"
  inc delete -f foo
}

test_config_edit_container_snapshot_pool_config() {
    # shellcheck disable=2034,2039,2155,3043
    local storage_pool="incustest-$(basename "${INCUS_DIR}")"

    ensure_import_testimage

    inc init testimage c1 -s "$storage_pool"
    inc snapshot c1 s1
    # edit the container volume name
    inc storage volume show "$storage_pool" container/c1 | \
        sed 's/^description:.*/description: bar/' | \
        inc storage volume edit "$storage_pool" container/c1
    inc storage volume show "$storage_pool" container/c1 | grep -q 'description: bar'
    # edit the container snapshot volume name
    inc storage volume show "$storage_pool" container/c1/s1 | \
        sed 's/^description:.*/description: baz/' | \
        inc storage volume edit "$storage_pool" container/c1/s1
    inc storage volume show "$storage_pool" container/c1/s1 | grep -q 'description: baz'
    inc delete c1
}

test_container_metadata() {
    ensure_import_testimage
    inc init testimage c

    # metadata for the container are printed
    inc config metadata show c | grep -q BusyBox

    # metadata can be edited
    inc config metadata show c | sed 's/BusyBox/BB/' | inc config metadata edit c
    inc config metadata show c | grep -q BB

    # templates can be listed
    inc config template list c | grep -q template.tpl

    # template content can be returned
    inc config template show c template.tpl | grep -q "name:"

    # templates can be added
    inc config template create c my.tpl
    inc config template list c | grep -q my.tpl

    # template content can be updated
    echo "some content" | inc config template edit c my.tpl
    inc config template show c my.tpl | grep -q "some content"

    # templates can be removed
    inc config template delete c my.tpl
    ! inc config template list c | grep -q my.tpl || false

    inc delete c
}

test_container_snapshot_config() {
    if ! tty -s; then
        echo "==> SKIP: Test requires a terminal"
        return
    fi

    ensure_import_testimage

    inc init testimage foo -s "incustest-$(basename "${INCUS_DIR}")"
    inc snapshot foo
    inc config show foo/snap0 | grep -q 'expires_at: 0001-01-01T00:00:00Z'

    echo 'expires_at: 2100-01-01T00:00:00Z' | inc config edit foo/snap0
    inc config show foo/snap0 | grep -q 'expires_at: 2100-01-01T00:00:00Z'

    # Remove expiry date using zero time
    echo 'expires_at: 0001-01-01T00:00:00Z' | inc config edit foo/snap0
    inc config show foo/snap0 | grep -q 'expires_at: 0001-01-01T00:00:00Z'

    echo 'expires_at: 2100-01-01T00:00:00Z' | inc config edit foo/snap0
    inc config show foo/snap0 | grep -q 'expires_at: 2100-01-01T00:00:00Z'

    # Remove expiry date using empty value
    echo 'expires_at:' | inc config edit foo/snap0
    inc config show foo/snap0 | grep -q 'expires_at: 0001-01-01T00:00:00Z'

    # Check instance name is included in edit screen.
    cmd=$(unset -f inc; command -v inc)
    output=$(EDITOR="cat" timeout --foreground 120 "${cmd}" config edit foo/snap0)
    echo "${output}" | grep "name: snap0"

    # Check expanded config isn't included in edit screen.
    ! echo "${output}"  | grep "expanded" || false

    inc delete -f foo
}
