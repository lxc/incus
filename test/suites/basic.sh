test_basic_usage() {
  # shellcheck disable=2039,3043
  local incus_backend
  incus_backend=$(storage_backend "$INCUS_DIR")

  ensure_import_testimage

  # shellcheck disable=2153
  ensure_has_localhost_remote "${INCUS_ADDR}"

  # Test image export
  sum="$(incus image info testimage | awk '/^Fingerprint/ {print $2}')"
  incus image export testimage "${INCUS_DIR}/"
  [ "${sum}" = "$(sha256sum "${INCUS_DIR}/${sum}.tar.xz" | cut -d' ' -f1)" ]

  # Test an alias with slashes
  incus image show "${sum}"
  incus image alias create a/b/ "${sum}"
  incus image alias delete a/b/

  # Test alias list filtering
  incus image alias create foo "${sum}"
  incus image alias create bar "${sum}"
  incus image alias list local: | grep -q foo
  incus image alias list local: | grep -q bar
  incus image alias list local: foo | grep -q -v bar
  incus image alias list local: "${sum}" | grep -q foo
  incus image alias list local: non-existent | grep -q -v non-existent
  incus image alias delete foo
  incus image alias delete bar

  incus image alias create foo "${sum}"
  incus image alias rename foo bar
  incus image alias list | grep -qv foo  # the old name is gone
  incus image alias delete bar

  # Test image list output formats (table & json)
  incus image list --format table | grep -q testimage
  incus image list --format json \
    | jq '.[]|select(.alias[0].name="testimage")' \
    | grep -q '"name": "testimage"'

  # Test image delete
  incus image delete testimage

  # test GET /1.0, since the client always puts to /1.0/
  my_curl -f -X GET "https://${INCUS_ADDR}/1.0"
  my_curl -f -X GET "https://${INCUS_ADDR}/1.0/instances"

  # Re-import the image
  mv "${INCUS_DIR}/${sum}.tar.xz" "${INCUS_DIR}/testimage.tar.xz"
  incus image import "${INCUS_DIR}/testimage.tar.xz" --alias testimage user.foo=bar --public
  incus image show testimage | grep -qF "user.foo: bar"
  incus image show testimage | grep -qF "public: true"
  incus image delete testimage
  incus image import "${INCUS_DIR}/testimage.tar.xz" --alias testimage
  rm "${INCUS_DIR}/testimage.tar.xz"

  # Test filename for image export
  incus image export testimage "${INCUS_DIR}/"
  [ "${sum}" = "$(sha256sum "${INCUS_DIR}/${sum}.tar.xz" | cut -d' ' -f1)" ]
  rm "${INCUS_DIR}/${sum}.tar.xz"

  # Test custom filename for image export
  incus image export testimage "${INCUS_DIR}/foo"
  [ "${sum}" = "$(sha256sum "${INCUS_DIR}/foo.tar.xz" | cut -d' ' -f1)" ]
  rm "${INCUS_DIR}/foo.tar.xz"


  # Test image export with a split image.
  deps/import-busybox --split --alias splitimage

  sum="$(incus image info splitimage | awk '/^Fingerprint/ {print $2}')"

  incus image export splitimage "${INCUS_DIR}"
  [ "${sum}" = "$(cat "${INCUS_DIR}/meta-${sum}.tar.xz" "${INCUS_DIR}/${sum}.tar.xz" | sha256sum | cut -d' ' -f1)" ]

  # Delete the split image and exported files
  rm "${INCUS_DIR}/${sum}.tar.xz"
  rm "${INCUS_DIR}/meta-${sum}.tar.xz"
  incus image delete splitimage

  # Redo the split image export test, this time with the --filename flag
  # to tell import-busybox to set the 'busybox' filename in the upload.
  # The sum should remain the same as its the same image.
  deps/import-busybox --split --filename --alias splitimage

  incus image export splitimage "${INCUS_DIR}"
  [ "${sum}" = "$(cat "${INCUS_DIR}/meta-${sum}.tar.xz" "${INCUS_DIR}/${sum}.tar.xz" | sha256sum | cut -d' ' -f1)" ]

  # Delete the split image and exported files
  rm "${INCUS_DIR}/${sum}.tar.xz"
  rm "${INCUS_DIR}/meta-${sum}.tar.xz"
  incus image delete splitimage

  # Test --no-profiles flag
  poolName=$(incus profile device get default root pool)
  ! incus init testimage foo --no-profiles || false
  incus init testimage foo --no-profiles -s "${poolName}"
  incus delete -f foo

  # Test container creation
  incus init testimage foo
  incus list | grep foo | grep STOPPED
  incus list fo | grep foo | grep STOPPED

  # Test list json format
  incus list --format json | jq '.[]|select(.name="foo")' | grep '"name": "foo"'

  # Test list with --columns and --fast
  ! incus list --columns=nsp --fast || false

  # Check volatile.apply_template is correct.
  incus config get foo volatile.apply_template | grep create

  # Start the instance to clear apply_template.
  incus start foo
  incus stop foo -f

  # Test container rename
  incus move foo bar

  # Check volatile.apply_template is altered during rename.
  incus config get bar volatile.apply_template | grep rename

  incus list | grep -v foo
  incus list | grep bar

  incus rename bar foo
  incus list | grep -v bar
  incus list | grep foo
  incus rename foo bar

  # Test container copy
  incus copy bar foo
  incus delete foo

  # gen untrusted cert
  gen_cert client3

  # don't allow requests without a cert to get trusted data
  curl -k -s -X GET "https://${INCUS_ADDR}/1.0/instances/foo" | grep 403

  # Test unprivileged container publish
  incus publish bar --alias=foo-image prop1=val1
  incus image show foo-image | grep val1
  curl -k -s --cert "${INCUS_CONF}/client3.crt" --key "${INCUS_CONF}/client3.key" -X GET "https://${INCUS_ADDR}/1.0/images" | grep -F "/1.0/images/" && false
  incus image delete foo-image

  # Test container publish with existing alias
  incus publish bar --alias=foo-image --alias=foo-image2
  incus launch testimage baz
  # change the container filesystem so the resulting image is different
  incus exec baz touch /somefile
  incus stop baz --force
  # publishing another image with same alias should fail
  ! incus publish baz --alias=foo-image || false
  # publishing another image with same alias and '--reuse' flag should success
  incus publish baz --alias=foo-image --reuse
  fooImage=$(incus image list -cF -fcsv foo-image)
  fooImage2=$(incus image list -cF -fcsv foo-image2)
  incus delete baz
  incus image delete foo-image foo-image2

  # the first image should have foo-image2 alias and the second imgae foo-image alias
  if [ "$fooImage" = "$fooImage2" ]; then
    echo "foo-image and foo-image2 aliases should be assigned to two different images"
    false
  fi


  # Test container publish with existing alias
  incus publish bar --alias=foo-image --alias=foo-image2
  incus launch testimage baz
  # change the container filesystem so the resulting image is different
  incus exec baz touch /somefile
  incus stop baz --force
  # publishing another image with same aliases
  incus publish baz --alias=foo-image --alias=foo-image2 --reuse
  fooImage=$(incus image list -cF -fcsv foo-image)
  fooImage2=$(incus image list -cF -fcsv foo-image2)
  incus delete baz
  incus image delete foo-image

  # the second image should have foo-image and foo-image2 aliases and the first one should be removed
  if [ "$fooImage" != "$fooImage2" ]; then
    echo "foo-image and foo-image2 aliases should be assigned to the same image"
    false
  fi


  # Test image compression on publish
  incus publish bar --alias=foo-image-compressed --compression=bzip2 prop=val1
  incus image show foo-image-compressed | grep val1
  curl -k -s --cert "${INCUS_CONF}/client3.crt" --key "${INCUS_CONF}/client3.key" -X GET "https://${INCUS_ADDR}/1.0/images" | grep -F "/1.0/images/" && false
  incus image delete foo-image-compressed

  # Test compression options
  incus publish bar --alias=foo-image-compressed --compression="gzip --rsyncable" prop=val1
  incus image delete foo-image-compressed

  # Test privileged container publish
  incus profile create priv
  incus profile set priv security.privileged true
  incus init testimage barpriv -p default -p priv
  incus publish barpriv --alias=foo-image prop1=val1
  incus image show foo-image | grep val1
  curl -k -s --cert "${INCUS_CONF}/client3.crt" --key "${INCUS_CONF}/client3.key" -X GET "https://${INCUS_ADDR}/1.0/images" | grep -F "/1.0/images/" && false
  incus image delete foo-image
  incus delete barpriv
  incus profile delete priv

  # Test that containers without metadata.yaml are published successfully.
  # Note that this quick hack won't work for LVM, since it doesn't always mount
  # the container's filesystem. That's ok though: the logic we're trying to
  # test here is independent of storage backend, so running it for just one
  # backend (or all non-lvm backends) is enough.
  if [ "$incus_backend" = "lvm" ]; then
    incus init testimage nometadata
    rm -f "${INCUS_DIR}/containers/nometadata/metadata.yaml"
    incus publish nometadata --alias=nometadata-image
    incus image delete nometadata-image
    incus delete nometadata
  fi

  # Test public images
  incus publish --public bar --alias=foo-image2
  curl -k -s --cert "${INCUS_CONF}/client3.crt" --key "${INCUS_CONF}/client3.key" -X GET "https://${INCUS_ADDR}/1.0/images" | grep -F "/1.0/images/"
  incus image delete foo-image2

  # Test invalid instance names
  ! incus init testimage -abc || false
  ! incus init testimage abc- || false
  ! incus init testimage 1234 || false
  ! incus init testimage foo.bar || false
  ! incus init testimage a_b_c || false
  ! incus init testimage aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa || false

  # Test snapshot publish
  incus snapshot create bar
  incus publish bar/snap0 --alias foo
  incus init foo bar2
  incus list | grep bar2
  incus delete bar2
  incus image delete foo

  # Test alias support
  cp "${INCUS_CONF}/config.yml" "${INCUS_CONF}/config.yml.bak"

  #   1. Basic built-in alias functionality
  [ "$(incus ls)" = "$(incus list)" ]
  #   2. Basic user-defined alias functionality
  printf "aliases:\\n  l: list\\n" >> "${INCUS_CONF}/config.yml"
  [ "$(incus l)" = "$(incus list)" ]
  #   3. Built-in aliases and user-defined aliases can coexist
  [ "$(incus ls)" = "$(incus l)" ]
  #   4. Multi-argument alias keys and values
  echo "  i ls: image list" >> "${INCUS_CONF}/config.yml"
  [ "$(incus i ls)" = "$(incus image list)" ]
  #   5. Aliases where len(keys) != len(values) (expansion/contraction of number of arguments)
  printf "  ils: image list\\n  container ls: list\\n" >> "${INCUS_CONF}/config.yml"
  [ "$(incus ils)" = "$(incus image list)" ]
  [ "$(incus container ls)" = "$(incus list)" ]
  #   6. User-defined aliases override built-in aliases
  echo "  cp: list" >> "${INCUS_CONF}/config.yml"
  [ "$(incus ls)" = "$(incus cp)" ]
  #   7. User-defined aliases override commands and don't recurse
  incus init testimage foo
  INC_CONFIG_SHOW=$(incus config show foo --expanded)
  echo "  config show: config show --expanded" >> "${INCUS_CONF}/config.yml"
  [ "$(incus config show foo)" = "$INC_CONFIG_SHOW" ]
  incus delete foo

  # Restore the config to remove the aliases
  mv "${INCUS_CONF}/config.yml.bak" "${INCUS_CONF}/config.yml"

  # Delete the bar container we've used for several tests
  incus delete bar

  # incus delete should also delete all snapshots of bar
  [ ! -d "${INCUS_DIR}/containers-snapshots/bar" ]

  # Test randomly named container creation
  incus launch testimage
  RDNAME=$(incus list --format csv --columns n)
  incus delete -f "${RDNAME}"

  # Test "nonetype" container creation
  wait_for "${INCUS_ADDR}" my_curl -X POST "https://${INCUS_ADDR}/1.0/instances" \
        -d "{\"name\":\"nonetype\",\"source\":{\"type\":\"none\"}}"
  incus delete nonetype

  # Test "nonetype" container creation with an LXC config
  wait_for "${INCUS_ADDR}" my_curl -X POST "https://${INCUS_ADDR}/1.0/instances" \
        -d "{\"name\":\"configtest\",\"config\":{\"raw.lxc\":\"lxc.hook.clone=/bin/true\"},\"source\":{\"type\":\"none\"}}"
  # shellcheck disable=SC2102
  [ "$(my_curl "https://${INCUS_ADDR}/1.0/instances/configtest" | jq -r .metadata.config[\"raw.lxc\"])" = "lxc.hook.clone=/bin/true" ]
  incus delete configtest

  # Test activateifneeded/shutdown
  INCUS_ACTIVATION_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ACTIVATION_DIR}"
  spawn_incus "${INCUS_ACTIVATION_DIR}" true
  (
    set -e
    # shellcheck disable=SC2030
    INCUS_DIR=${INCUS_ACTIVATION_DIR}
    ensure_import_testimage
    incusd activateifneeded --debug 2>&1 | grep -qF "Daemon has core.https_address set, activating..."
    incus config unset core.https_address --force-local
    incusd activateifneeded --debug 2>&1 | grep -qF -v "activating..."
    incus init testimage autostart --force-local
    incusd activateifneeded --debug 2>&1 | grep -qF -v "activating..."
    incus config set autostart boot.autostart true --force-local

    # Restart the daemon, this forces the global database to be dumped to disk.
    shutdown_incus "${INCUS_DIR}"
    respawn_incus "${INCUS_DIR}" true
    incus stop --force autostart --force-local

    incusd activateifneeded --debug 2>&1 | grep -qF "Daemon has auto-started instances, activating..."

    incus config unset autostart boot.autostart --force-local
    incusd activateifneeded --debug 2>&1 | grep -qF -v "activating..."

    incus start autostart --force-local
    PID=$(incus info autostart --force-local | awk '/^PID:/ {print $2}')
    shutdown_incus "${INCUS_DIR}"
    [ -d "/proc/${PID}" ] && false

    incusd activateifneeded --debug 2>&1 | grep -qF "Daemon has auto-started instances, activating..."

    # shellcheck disable=SC2031
    respawn_incus "${INCUS_DIR}" true

    incus list --force-local autostart | grep -q RUNNING

    # Check for scheduled instance snapshots
    incus stop --force autostart --force-local
    incus config set autostart snapshots.schedule "* * * * *" --force-local
    shutdown_incus "${INCUS_DIR}"
    incusd activateifneeded --debug 2>&1 | grep -qF "Daemon has scheduled instance snapshots, activating..."

    # shellcheck disable=SC2031
    respawn_incus "${INCUS_DIR}" true

    incus config unset autostart snapshots.schedule --force-local

    # Check for scheduled volume snapshots
    storage_pool="incustest-$(basename "${INCUS_DIR}")"

    incus storage volume create "${storage_pool}" vol --force-local

    shutdown_incus "${INCUS_DIR}"
    incusd activateifneeded --debug 2>&1 | grep -qF -v "activating..."

    # shellcheck disable=SC2031
    respawn_incus "${INCUS_DIR}" true

    incus storage volume set "${storage_pool}" vol snapshots.schedule="* * * * *" --force-local

    shutdown_incus "${INCUS_DIR}"
    incusd activateifneeded --debug 2>&1 | grep -qF "Daemon has scheduled volume snapshots, activating..."

    # shellcheck disable=SC2031
    respawn_incus "${INCUS_DIR}" true

    incus delete autostart --force --force-local
    incus storage volume delete "${storage_pool}" vol --force-local
  )
  # shellcheck disable=SC2031,2269
  INCUS_DIR=${INCUS_DIR}
  kill_incus "${INCUS_ACTIVATION_DIR}"

  # Create and start a container
  incus launch testimage foo
  incus list | grep foo | grep RUNNING
  incus stop foo --force

  # cycle it a few times
  incus start foo
  mac1=$(incus exec foo cat /sys/class/net/eth0/address)
  incus stop foo --force
  incus start foo
  mac2=$(incus exec foo cat /sys/class/net/eth0/address)

  if [ -n "${mac1}" ] && [ -n "${mac2}" ] && [ "${mac1}" != "${mac2}" ]; then
    echo "==> MAC addresses didn't match across restarts (${mac1} vs ${mac2})"
    false
  fi

  # Test instance types
  incus launch testimage test-limits -t c0.5-m0.2
  [ "$(incus config get test-limits limits.cpu)" = "1" ]
  [ "$(incus config get test-limits limits.cpu.allowance)" = "50%" ]
  [ "$(incus config get test-limits limits.memory)" = "204MiB" ]
  incus delete -f test-limits

  # Test last_used_at field is working properly
  incus init testimage last-used-at-test
  incus list last-used-at-test  --format json | jq -r '.[].last_used_at' | grep '1970-01-01T00:00:00Z'
  incus start last-used-at-test
  incus list last-used-at-test  --format json | jq -r '.[].last_used_at' | grep -v '1970-01-01T00:00:00Z'
  incus delete last-used-at-test --force

  # Test user, group and cwd
  incus exec foo -- mkdir /blah
  [ "$(incus exec foo --user 1000 -- id -u)" = "1000" ] || false
  [ "$(incus exec foo --group 1000 -- id -g)" = "1000" ] || false
  [ "$(incus exec foo --cwd /blah -- pwd)" = "/blah" ] || false

  [ "$(incus exec foo --user 1234 --group 5678 --cwd /blah -- id -u)" = "1234" ] || false
  [ "$(incus exec foo --user 1234 --group 5678 --cwd /blah -- id -g)" = "5678" ] || false
  [ "$(incus exec foo --user 1234 --group 5678 --cwd /blah -- pwd)" = "/blah" ] || false

  # check that we can set the environment
  incus exec foo pwd | grep /root
  incus exec --env BEST_BAND=meshuggah foo env | grep meshuggah
  incus exec foo ip link show | grep eth0

  # check that we can get the return code for a non- wait-for-websocket exec
  op=$(my_curl -X POST "https://${INCUS_ADDR}/1.0/instances/foo/exec" -d '{"command": ["echo", "test"], "environment": {}, "wait-for-websocket": false, "interactive": false}' | jq -r .operation)
  [ "$(my_curl "https://${INCUS_ADDR}${op}/wait" | jq -r .metadata.metadata.return)" != "null" ]

  # test file transfer
  echo abc > "${INCUS_DIR}/in"

  incus file push "${INCUS_DIR}/in" foo/root/
  incus exec foo /bin/cat /root/in | grep abc
  incus exec foo -- /bin/rm -f root/in

  incus file push "${INCUS_DIR}/in" foo/root/in1
  incus exec foo /bin/cat /root/in1 | grep abc
  incus exec foo -- /bin/rm -f root/in1

  # test incus file edit doesn't change target file's owner and permissions
  echo "content" | incus file push - foo/tmp/edit_test
  incus exec foo -- chown 55.55 /tmp/edit_test
  incus exec foo -- chmod 555 /tmp/edit_test
  echo "new content" | incus file edit foo/tmp/edit_test
  [ "$(incus exec foo -- cat /tmp/edit_test)" = "new content" ]
  [ "$(incus exec foo -- stat -c \"%u %g %a\" /tmp/edit_test)" = "55 55 555" ]

  # make sure stdin is chowned to our container root uid (Issue #590)
  [ -t 0 ] && [ -t 1 ] && incus exec foo -- chown 1000:1000 /proc/self/fd/0

  echo foo | incus exec foo tee /tmp/foo

  # Detect regressions/hangs in exec
  sum=$(ps aux | tee "${INCUS_DIR}/out" | incus exec foo md5sum | cut -d' ' -f1)
  [ "${sum}" = "$(md5sum "${INCUS_DIR}/out" | cut -d' ' -f1)" ]
  rm "${INCUS_DIR}/out"

  # FIXME: make this backend agnostic
  if [ "$incus_backend" = "dir" ]; then
    content=$(cat "${INCUS_DIR}/containers/foo/rootfs/tmp/foo")
    [ "${content}" = "foo" ]
  fi

  incus launch testimage deleterunning
  my_curl -X DELETE "https://${INCUS_ADDR}/1.0/instances/deleterunning" | grep "Instance is running"
  incus delete deleterunning -f

  # cleanup
  incus delete foo -f

  if [ -e /sys/module/apparmor/ ]; then
    # check that an apparmor profile is created for this container, that it is
    # unloaded on stop, and that it is deleted when the container is deleted
    incus launch testimage inc-apparmor-test

    MAJOR=0
    MINOR=0
    if [ -f /sys/kernel/security/apparmor/features/domain/version ]; then
      MAJOR=$(awk -F. '{print $1}' < /sys/kernel/security/apparmor/features/domain/version)
      MINOR=$(awk -F. '{print $2}' < /sys/kernel/security/apparmor/features/domain/version)
    fi

    if [ "${MAJOR}" -gt "1" ] || { [ "${MAJOR}" = "1" ] && [ "${MINOR}" -ge "2" ]; }; then
      aa_namespace="incus-inc-apparmor-test_<$(echo "${INCUS_DIR}" | sed -e 's/\//-/g' -e 's/^.//')>"
      aa-status | grep -q ":${aa_namespace}:unconfined" || aa-status | grep -qF ":${aa_namespace}://unconfined"
      incus stop inc-apparmor-test --force
      ! aa-status | grep -qF ":${aa_namespace}:" || false
    else
      aa-status | grep "incus-inc-apparmor-test_<${INCUS_DIR}>"
      incus stop inc-apparmor-test --force
      ! aa-status | grep -qF "incus-inc-apparmor-test_<${INCUS_DIR}>" || false
    fi
    incus delete inc-apparmor-test
    [ ! -f "${INCUS_DIR}/security/apparmor/profiles/incus-inc-apparmor-test" ]
  else
    echo "==> SKIP: apparmor tests (missing kernel support)"
  fi

  if [ "$(awk '/^Seccomp:/ {print $2}' "/proc/self/status")" -eq "0" ]; then
    incus launch testimage inc-seccomp-test
    init=$(incus info inc-seccomp-test | awk '/^PID:/ {print $2}')
    [ "$(awk '/^Seccomp:/ {print $2}' "/proc/${init}/status")" -eq "2" ]
    incus stop --force inc-seccomp-test
    incus config set inc-seccomp-test security.syscalls.deny_default false
    incus start inc-seccomp-test
    init=$(incus info inc-seccomp-test | awk '/^PID:/ {print $2}')
    [ "$(awk '/^Seccomp:/ {print $2}' "/proc/${init}/status")" -eq "0" ]
    incus delete --force inc-seccomp-test
  else
    echo "==> SKIP: seccomp tests (seccomp filtering is externally enabled)"
  fi

  # make sure that privileged containers are not world-readable
  incus profile create unconfined
  incus profile set unconfined security.privileged true
  incus init testimage foo2 -p unconfined -s "incustest-$(basename "${INCUS_DIR}")"
  [ "$(stat -L -c "%a" "${INCUS_DIR}/containers/foo2")" = "100" ]
  incus delete foo2
  incus profile delete unconfined

  # Test boot.host_shutdown_timeout config setting
  incus init testimage configtest --config boot.host_shutdown_timeout=45
  [ "$(incus config get configtest boot.host_shutdown_timeout)" -eq 45 ]
  incus config set configtest boot.host_shutdown_timeout 15
  [ "$(incus config get configtest boot.host_shutdown_timeout)" -eq 15 ]
  incus delete configtest

  # Test deleting multiple images
  # Start 3 containers to create 3 different images
  incus launch testimage c1
  incus launch testimage c2
  incus launch testimage c3
  incus exec c1 -- touch /tmp/c1
  incus exec c2 -- touch /tmp/c2
  incus exec c3 -- touch /tmp/c3
  incus publish --force c1 --alias=image1
  incus publish --force c2 --alias=image2
  incus publish --force c3 --alias=image3
  # Delete multiple images with incus delete and confirm they're deleted
  incus image delete local:image1 local:image2 local:image3
  ! incus image list | grep -q image1 || false
  ! incus image list | grep -q image2 || false
  ! incus image list | grep -q image3 || false
  # Cleanup the containers
  incus delete --force c1 c2 c3

  # Test --all flag
  incus init testimage c1
  incus init testimage c2
  incus start --all
  incus list | grep c1 | grep RUNNING
  incus list | grep c2 | grep RUNNING
  ! incus stop --all c1 || false
  incus stop --all -f
  incus list | grep c1 | grep STOPPED
  incus list | grep c2 | grep STOPPED
  # Cleanup the containers
  incus delete --force c1 c2

  # Ephemeral
  incus launch testimage foo -e
  OLD_INIT=$(incus info foo | awk '/^PID:/ {print $2}')

  REBOOTED="false"

  for _ in $(seq 60); do
    NEW_INIT=$(incus info foo | awk '/^PID:/ {print $2}' || true)

    # If init process is running, check if is old or new process.
    if [ -n "${NEW_INIT}" ]; then
      if [ "${OLD_INIT}" != "${NEW_INIT}" ]; then
        REBOOTED="true"
        break
      else
        incus exec foo reboot || true  # Signal to running old init process to reboot if not rebooted yet.
      fi
    fi

    sleep 0.5
  done

  [ "${REBOOTED}" = "true" ]

  incus publish foo --alias foo --force
  incus image delete foo

  incus restart -f foo
  incus stop foo --force
  ! incus list | grep -q foo || false

  # Test renaming/deletion of the default profile
  ! incus profile rename default foobar || false
  ! incus profile delete default || false

  incus init testimage c1
  result="$(! incus config device override c1 root pool=bla 2>&1)"
  if ! echo "${result}" | grep "Error: Cannot update root disk device pool name"; then
    echo "Should fail device override because root disk device storage pool cannot be changed."
    false
  fi

  incus rm -f c1

  # Should fail to override root device storage pool when the new pool does not exist.
  ! incus init testimage c1 -d root,pool=bla || false

  # Should succeed in overriding root device storage pool when the pool does exist and the override occurs at create time.
  incus storage create bla dir
  incus init testimage c1 -d root,pool=bla
  incus config show c1 --expanded | grep -Pz '  root:\n    path: /\n    pool: bla\n    type: disk\n'

  incus storage volume create bla vol1
  incus storage volume create bla vol2
  incus config device add c1 dev disk source=vol1 pool=bla path=/vol

  # Should not be able to override a device that is not part of a profile (i.e. has been specifically added).
  result="$(! incus config device override c1 dev source=vol2 2>&1)"
  if ! echo "${result}" | grep "Error: The device already exists"; then
    echo "Should fail because device is defined against the instance not the profile."
    false
  fi

  incus rm -f c1
  incus storage volume delete bla vol1
  incus storage volume delete bla vol2
  incus storage delete bla

  # Test rebuilding an instance with its original image.
  incus init testimage c1
  incus start c1
  incus exec c1 -- touch /data.txt
  incus stop c1
  incus rebuild testimage c1
  incus start c1
  ! incus exec c1 -- stat /data.txt || false
  incus delete c1 -f

  # Test a forced rebuild
  incus launch testimage c1
  ! incus rebuild testimage c1 || false
  incus rebuild testimage c1 --force
  incus delete c1 -f

  # Test rebuilding an instance with a new image.
  incus init c1 --empty
  incus rebuild testimage c1
  incus start c1
  incus delete c1 -f

  # Test rebuilding an instance with an empty file system.
  incus init testimage c1
  incus rebuild c1 --empty
  ! incus config show c1 | grep -q 'image.*' || false
  incus delete c1 -f

  # Test assigning an empty profile (with no root disk device) to an instance.
  incus init testimage c1
  incus profile create foo
  ! incus profile assign c1 foo || false
  incus profile delete foo
  incus delete -f c1

  # Multiple ephemeral instances delete
  incus launch testimage c1
  incus launch testimage c2
  incus launch testimage c3

  incus delete -f c1 c2 c3
  remaining_instances="$(incus list --format csv)"
  [ -z "${remaining_instances}" ]
}
