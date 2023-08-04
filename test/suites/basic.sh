test_basic_usage() {
  # shellcheck disable=2039,3043
  local incus_backend
  incus_backend=$(storage_backend "$INCUS_DIR")

  ensure_import_testimage

  # shellcheck disable=2153
  ensure_has_localhost_remote "${INCUS_ADDR}"

  # Test image export
  sum="$(inc image info testimage | awk '/^Fingerprint/ {print $2}')"
  inc image export testimage "${INCUS_DIR}/"
  [ "${sum}" = "$(sha256sum "${INCUS_DIR}/${sum}.tar.xz" | cut -d' ' -f1)" ]

  # Test an alias with slashes
  inc image show "${sum}"
  inc image alias create a/b/ "${sum}"
  inc image alias delete a/b/

  # Test alias list filtering
  inc image alias create foo "${sum}"
  inc image alias create bar "${sum}"
  inc image alias list local: | grep -q foo
  inc image alias list local: | grep -q bar
  inc image alias list local: foo | grep -q -v bar
  inc image alias list local: "${sum}" | grep -q foo
  inc image alias list local: non-existent | grep -q -v non-existent
  inc image alias delete foo
  inc image alias delete bar

  inc image alias create foo "${sum}"
  inc image alias rename foo bar
  inc image alias list | grep -qv foo  # the old name is gone
  inc image alias delete bar

  # Test image list output formats (table & json)
  inc image list --format table | grep -q testimage
  inc image list --format json \
    | jq '.[]|select(.alias[0].name="testimage")' \
    | grep -q '"name": "testimage"'

  # Test image delete
  inc image delete testimage

  # test GET /1.0, since the client always puts to /1.0/
  my_curl -f -X GET "https://${INCUS_ADDR}/1.0"
  my_curl -f -X GET "https://${INCUS_ADDR}/1.0/instances"

  # Re-import the image
  mv "${INCUS_DIR}/${sum}.tar.xz" "${INCUS_DIR}/testimage.tar.xz"
  inc image import "${INCUS_DIR}/testimage.tar.xz" --alias testimage user.foo=bar --public
  inc image show testimage | grep -qF "user.foo: bar"
  inc image show testimage | grep -qF "public: true"
  inc image delete testimage
  inc image import "${INCUS_DIR}/testimage.tar.xz" --alias testimage
  rm "${INCUS_DIR}/testimage.tar.xz"

  # Test filename for image export
  inc image export testimage "${INCUS_DIR}/"
  [ "${sum}" = "$(sha256sum "${INCUS_DIR}/${sum}.tar.xz" | cut -d' ' -f1)" ]
  rm "${INCUS_DIR}/${sum}.tar.xz"

  # Test custom filename for image export
  inc image export testimage "${INCUS_DIR}/foo"
  [ "${sum}" = "$(sha256sum "${INCUS_DIR}/foo.tar.xz" | cut -d' ' -f1)" ]
  rm "${INCUS_DIR}/foo.tar.xz"


  # Test image export with a split image.
  deps/import-busybox --split --alias splitimage

  sum="$(inc image info splitimage | awk '/^Fingerprint/ {print $2}')"

  inc image export splitimage "${INCUS_DIR}"
  [ "${sum}" = "$(cat "${INCUS_DIR}/meta-${sum}.tar.xz" "${INCUS_DIR}/${sum}.tar.xz" | sha256sum | cut -d' ' -f1)" ]

  # Delete the split image and exported files
  rm "${INCUS_DIR}/${sum}.tar.xz"
  rm "${INCUS_DIR}/meta-${sum}.tar.xz"
  inc image delete splitimage

  # Redo the split image export test, this time with the --filename flag
  # to tell import-busybox to set the 'busybox' filename in the upload.
  # The sum should remain the same as its the same image.
  deps/import-busybox --split --filename --alias splitimage

  inc image export splitimage "${INCUS_DIR}"
  [ "${sum}" = "$(cat "${INCUS_DIR}/meta-${sum}.tar.xz" "${INCUS_DIR}/${sum}.tar.xz" | sha256sum | cut -d' ' -f1)" ]

  # Delete the split image and exported files
  rm "${INCUS_DIR}/${sum}.tar.xz"
  rm "${INCUS_DIR}/meta-${sum}.tar.xz"
  inc image delete splitimage

  # Test --no-profiles flag
  poolName=$(inc profile device get default root pool)
  ! inc init testimage foo --no-profiles || false
  inc init testimage foo --no-profiles -s "${poolName}"
  inc delete -f foo

  # Test container creation
  inc init testimage foo
  inc list | grep foo | grep STOPPED
  inc list fo | grep foo | grep STOPPED

  # Test list json format
  inc list --format json | jq '.[]|select(.name="foo")' | grep '"name": "foo"'

  # Test list with --columns and --fast
  ! inc list --columns=nsp --fast || false

  # Check volatile.apply_template is correct.
  inc config get foo volatile.apply_template | grep create

  # Start the instance to clear apply_template.
  inc start foo
  inc stop foo -f

  # Test container rename
  inc move foo bar

  # Check volatile.apply_template is altered during rename.
  inc config get bar volatile.apply_template | grep rename

  inc list | grep -v foo
  inc list | grep bar

  inc rename bar foo
  inc list | grep -v bar
  inc list | grep foo
  inc rename foo bar

  # Test container copy
  inc copy bar foo
  inc delete foo

  # gen untrusted cert
  gen_cert client3

  # don't allow requests without a cert to get trusted data
  curl -k -s -X GET "https://${INCUS_ADDR}/1.0/instances/foo" | grep 403

  # Test unprivileged container publish
  inc publish bar --alias=foo-image prop1=val1
  inc image show foo-image | grep val1
  curl -k -s --cert "${INCUS_CONF}/client3.crt" --key "${INCUS_CONF}/client3.key" -X GET "https://${INCUS_ADDR}/1.0/images" | grep -F "/1.0/images/" && false
  inc image delete foo-image

  # Test container publish with existing alias
  inc publish bar --alias=foo-image --alias=foo-image2
  inc launch testimage baz
  # change the container filesystem so the resulting image is different
  inc exec baz touch /somefile
  inc stop baz --force
  # publishing another image with same alias should fail
  ! inc publish baz --alias=foo-image || false
  # publishing another image with same alias and '--reuse' flag should success
  inc publish baz --alias=foo-image --reuse
  fooImage=$(inc image list -cF -fcsv foo-image)
  fooImage2=$(inc image list -cF -fcsv foo-image2)
  inc delete baz
  inc image delete foo-image foo-image2

  # the first image should have foo-image2 alias and the second imgae foo-image alias
  if [ "$fooImage" = "$fooImage2" ]; then
    echo "foo-image and foo-image2 aliases should be assigned to two different images"
    false
  fi


  # Test container publish with existing alias
  inc publish bar --alias=foo-image --alias=foo-image2
  inc launch testimage baz
  # change the container filesystem so the resulting image is different
  inc exec baz touch /somefile
  inc stop baz --force
  # publishing another image with same aliases
  inc publish baz --alias=foo-image --alias=foo-image2 --reuse
  fooImage=$(inc image list -cF -fcsv foo-image)
  fooImage2=$(inc image list -cF -fcsv foo-image2)
  inc delete baz
  inc image delete foo-image

  # the second image should have foo-image and foo-image2 aliases and the first one should be removed
  if [ "$fooImage" != "$fooImage2" ]; then
    echo "foo-image and foo-image2 aliases should be assigned to the same image"
    false
  fi


  # Test image compression on publish
  inc publish bar --alias=foo-image-compressed --compression=bzip2 prop=val1
  inc image show foo-image-compressed | grep val1
  curl -k -s --cert "${INCUS_CONF}/client3.crt" --key "${INCUS_CONF}/client3.key" -X GET "https://${INCUS_ADDR}/1.0/images" | grep -F "/1.0/images/" && false
  inc image delete foo-image-compressed

  # Test compression options
  inc publish bar --alias=foo-image-compressed --compression="gzip --rsyncable" prop=val1
  inc image delete foo-image-compressed

  # Test privileged container publish
  inc profile create priv
  inc profile set priv security.privileged true
  inc init testimage barpriv -p default -p priv
  inc publish barpriv --alias=foo-image prop1=val1
  inc image show foo-image | grep val1
  curl -k -s --cert "${INCUS_CONF}/client3.crt" --key "${INCUS_CONF}/client3.key" -X GET "https://${INCUS_ADDR}/1.0/images" | grep -F "/1.0/images/" && false
  inc image delete foo-image
  inc delete barpriv
  inc profile delete priv

  # Test that containers without metadata.yaml are published successfully.
  # Note that this quick hack won't work for LVM, since it doesn't always mount
  # the container's filesystem. That's ok though: the logic we're trying to
  # test here is independent of storage backend, so running it for just one
  # backend (or all non-lvm backends) is enough.
  if [ "$incus_backend" = "lvm" ]; then
    inc init testimage nometadata
    rm -f "${INCUS_DIR}/containers/nometadata/metadata.yaml"
    inc publish nometadata --alias=nometadata-image
    inc image delete nometadata-image
    inc delete nometadata
  fi

  # Test public images
  inc publish --public bar --alias=foo-image2
  curl -k -s --cert "${INCUS_CONF}/client3.crt" --key "${INCUS_CONF}/client3.key" -X GET "https://${INCUS_ADDR}/1.0/images" | grep -F "/1.0/images/"
  inc image delete foo-image2

  # Test invalid container names
  ! inc init testimage -abc || false
  ! inc init testimage abc- || false
  ! inc init testimage 1234 || false
  ! inc init testimage 12test || false
  ! inc init testimage a_b_c || false
  ! inc init testimage aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa || false

  # Test snapshot publish
  inc snapshot bar
  inc publish bar/snap0 --alias foo
  inc init foo bar2
  inc list | grep bar2
  inc delete bar2
  inc image delete foo

  # Test alias support
  cp "${INCUS_CONF}/config.yml" "${INCUS_CONF}/config.yml.bak"

  #   1. Basic built-in alias functionality
  [ "$(inc ls)" = "$(inc list)" ]
  #   2. Basic user-defined alias functionality
  printf "aliases:\\n  l: list\\n" >> "${INCUS_CONF}/config.yml"
  [ "$(inc l)" = "$(inc list)" ]
  #   3. Built-in aliases and user-defined aliases can coexist
  [ "$(inc ls)" = "$(inc l)" ]
  #   4. Multi-argument alias keys and values
  echo "  i ls: image list" >> "${INCUS_CONF}/config.yml"
  [ "$(inc i ls)" = "$(inc image list)" ]
  #   5. Aliases where len(keys) != len(values) (expansion/contraction of number of arguments)
  printf "  ils: image list\\n  container ls: list\\n" >> "${INCUS_CONF}/config.yml"
  [ "$(inc ils)" = "$(inc image list)" ]
  [ "$(inc container ls)" = "$(inc list)" ]
  #   6. User-defined aliases override built-in aliases
  echo "  cp: list" >> "${INCUS_CONF}/config.yml"
  [ "$(inc ls)" = "$(inc cp)" ]
  #   7. User-defined aliases override commands and don't recurse
  inc init testimage foo
  INC_CONFIG_SHOW=$(inc config show foo --expanded)
  echo "  config show: config show --expanded" >> "${INCUS_CONF}/config.yml"
  [ "$(inc config show foo)" = "$INC_CONFIG_SHOW" ]
  inc delete foo

  # Restore the config to remove the aliases
  mv "${INCUS_CONF}/config.yml.bak" "${INCUS_CONF}/config.yml"

  # Delete the bar container we've used for several tests
  inc delete bar

  # inc delete should also delete all snapshots of bar
  [ ! -d "${INCUS_DIR}/snapshots/bar" ]

  # Test randomly named container creation
  inc launch testimage
  RDNAME=$(inc list --format csv --columns n)
  inc delete -f "${RDNAME}"

  # Test "nonetype" container creation
  wait_for "${INCUS_ADDR}" my_curl -X POST "https://${INCUS_ADDR}/1.0/instances" \
        -d "{\"name\":\"nonetype\",\"source\":{\"type\":\"none\"}}"
  inc delete nonetype

  # Test "nonetype" container creation with an LXC config
  wait_for "${INCUS_ADDR}" my_curl -X POST "https://${INCUS_ADDR}/1.0/instances" \
        -d "{\"name\":\"configtest\",\"config\":{\"raw.lxc\":\"lxc.hook.clone=/bin/true\"},\"source\":{\"type\":\"none\"}}"
  # shellcheck disable=SC2102
  [ "$(my_curl "https://${INCUS_ADDR}/1.0/instances/configtest" | jq -r .metadata.config[\"raw.lxc\"])" = "lxc.hook.clone=/bin/true" ]
  inc delete configtest

  # Test activateifneeded/shutdown
  INCUS_ACTIVATION_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ACTIVATION_DIR}"
  spawn_incus "${INCUS_ACTIVATION_DIR}" true
  (
    set -e
    # shellcheck disable=SC2030
    INCUS_DIR=${INCUS_ACTIVATION_DIR}
    ensure_import_testimage
    incus activateifneeded --debug 2>&1 | grep -qF "Daemon has core.https_address set, activating..."
    inc config unset core.https_address --force-local
    incus activateifneeded --debug 2>&1 | grep -qF -v "activating..."
    inc init testimage autostart --force-local
    incus activateifneeded --debug 2>&1 | grep -qF -v "activating..."
    inc config set autostart boot.autostart true --force-local

    # Restart the daemon, this forces the global database to be dumped to disk.
    shutdown_incus "${INCUS_DIR}"
    respawn_incus "${INCUS_DIR}" true
    inc stop --force autostart --force-local

    incus activateifneeded --debug 2>&1 | grep -qF "Daemon has auto-started instances, activating..."

    inc config unset autostart boot.autostart --force-local
    incus activateifneeded --debug 2>&1 | grep -qF -v "activating..."

    inc start autostart --force-local
    PID=$(inc info autostart --force-local | awk '/^PID:/ {print $2}')
    shutdown_incus "${INCUS_DIR}"
    [ -d "/proc/${PID}" ] && false

    incus activateifneeded --debug 2>&1 | grep -qF "Daemon has auto-started instances, activating..."

    # shellcheck disable=SC2031
    respawn_incus "${INCUS_DIR}" true

    inc list --force-local autostart | grep -q RUNNING

    # Check for scheduled instance snapshots
    inc stop --force autostart --force-local
    inc config set autostart snapshots.schedule "* * * * *" --force-local
    shutdown_incus "${INCUS_DIR}"
    incus activateifneeded --debug 2>&1 | grep -qF "Daemon has scheduled instance snapshots, activating..."

    # shellcheck disable=SC2031
    respawn_incus "${INCUS_DIR}" true

    inc config unset autostart snapshots.schedule --force-local

    # Check for scheduled volume snapshots
    storage_pool="incustest-$(basename "${INCUS_DIR}")"

    inc storage volume create "${storage_pool}" vol --force-local

    shutdown_incus "${INCUS_DIR}"
    incus activateifneeded --debug 2>&1 | grep -qF -v "activating..."

    # shellcheck disable=SC2031
    respawn_incus "${INCUS_DIR}" true

    inc storage volume set "${storage_pool}" vol snapshots.schedule="* * * * *" --force-local

    shutdown_incus "${INCUS_DIR}"
    incus activateifneeded --debug 2>&1 | grep -qF "Daemon has scheduled volume snapshots, activating..."

    # shellcheck disable=SC2031
    respawn_incus "${INCUS_DIR}" true

    inc delete autostart --force --force-local
    inc storage volume delete "${storage_pool}" vol --force-local
  )
  # shellcheck disable=SC2031,2269
  INCUS_DIR=${INCUS_DIR}
  kill_incus "${INCUS_ACTIVATION_DIR}"

  # Create and start a container
  inc launch testimage foo
  inc list | grep foo | grep RUNNING
  inc stop foo --force

  # cycle it a few times
  inc start foo
  mac1=$(inc exec foo cat /sys/class/net/eth0/address)
  inc stop foo --force
  inc start foo
  mac2=$(inc exec foo cat /sys/class/net/eth0/address)

  if [ -n "${mac1}" ] && [ -n "${mac2}" ] && [ "${mac1}" != "${mac2}" ]; then
    echo "==> MAC addresses didn't match across restarts (${mac1} vs ${mac2})"
    false
  fi

  # Test instance types
  inc launch testimage test-limits -t c0.5-m0.2
  [ "$(inc config get test-limits limits.cpu)" = "1" ]
  [ "$(inc config get test-limits limits.cpu.allowance)" = "50%" ]
  [ "$(inc config get test-limits limits.memory)" = "204MiB" ]
  inc delete -f test-limits

  # Test last_used_at field is working properly
  inc init testimage last-used-at-test
  inc list last-used-at-test  --format json | jq -r '.[].last_used_at' | grep '1970-01-01T00:00:00Z'
  inc start last-used-at-test
  inc list last-used-at-test  --format json | jq -r '.[].last_used_at' | grep -v '1970-01-01T00:00:00Z'
  inc delete last-used-at-test --force

  # Test user, group and cwd
  inc exec foo -- mkdir /blah
  [ "$(inc exec foo --user 1000 -- id -u)" = "1000" ] || false
  [ "$(inc exec foo --group 1000 -- id -g)" = "1000" ] || false
  [ "$(inc exec foo --cwd /blah -- pwd)" = "/blah" ] || false

  [ "$(inc exec foo --user 1234 --group 5678 --cwd /blah -- id -u)" = "1234" ] || false
  [ "$(inc exec foo --user 1234 --group 5678 --cwd /blah -- id -g)" = "5678" ] || false
  [ "$(inc exec foo --user 1234 --group 5678 --cwd /blah -- pwd)" = "/blah" ] || false

  # check that we can set the environment
  inc exec foo pwd | grep /root
  inc exec --env BEST_BAND=meshuggah foo env | grep meshuggah
  inc exec foo ip link show | grep eth0

  # check that we can get the return code for a non- wait-for-websocket exec
  op=$(my_curl -X POST "https://${INCUS_ADDR}/1.0/instances/foo/exec" -d '{"command": ["echo", "test"], "environment": {}, "wait-for-websocket": false, "interactive": false}' | jq -r .operation)
  [ "$(my_curl "https://${INCUS_ADDR}${op}/wait" | jq -r .metadata.metadata.return)" != "null" ]

  # test file transfer
  echo abc > "${INCUS_DIR}/in"

  inc file push "${INCUS_DIR}/in" foo/root/
  inc exec foo /bin/cat /root/in | grep abc
  inc exec foo -- /bin/rm -f root/in

  inc file push "${INCUS_DIR}/in" foo/root/in1
  inc exec foo /bin/cat /root/in1 | grep abc
  inc exec foo -- /bin/rm -f root/in1

  # test inc file edit doesn't change target file's owner and permissions
  echo "content" | inc file push - foo/tmp/edit_test
  inc exec foo -- chown 55.55 /tmp/edit_test
  inc exec foo -- chmod 555 /tmp/edit_test
  echo "new content" | inc file edit foo/tmp/edit_test
  [ "$(inc exec foo -- cat /tmp/edit_test)" = "new content" ]
  [ "$(inc exec foo -- stat -c \"%u %g %a\" /tmp/edit_test)" = "55 55 555" ]

  # make sure stdin is chowned to our container root uid (Issue #590)
  [ -t 0 ] && [ -t 1 ] && inc exec foo -- chown 1000:1000 /proc/self/fd/0

  echo foo | inc exec foo tee /tmp/foo

  # Detect regressions/hangs in exec
  sum=$(ps aux | tee "${INCUS_DIR}/out" | inc exec foo md5sum | cut -d' ' -f1)
  [ "${sum}" = "$(md5sum "${INCUS_DIR}/out" | cut -d' ' -f1)" ]
  rm "${INCUS_DIR}/out"

  # FIXME: make this backend agnostic
  if [ "$incus_backend" = "dir" ]; then
    content=$(cat "${INCUS_DIR}/containers/foo/rootfs/tmp/foo")
    [ "${content}" = "foo" ]
  fi

  inc launch testimage deleterunning
  my_curl -X DELETE "https://${INCUS_ADDR}/1.0/instances/deleterunning" | grep "Instance is running"
  inc delete deleterunning -f

  # cleanup
  inc delete foo -f

  if [ -e /sys/module/apparmor/ ]; then
    # check that an apparmor profile is created for this container, that it is
    # unloaded on stop, and that it is deleted when the container is deleted
    inc launch testimage inc-apparmor-test

    MAJOR=0
    MINOR=0
    if [ -f /sys/kernel/security/apparmor/features/domain/version ]; then
      MAJOR=$(awk -F. '{print $1}' < /sys/kernel/security/apparmor/features/domain/version)
      MINOR=$(awk -F. '{print $2}' < /sys/kernel/security/apparmor/features/domain/version)
    fi

    if [ "${MAJOR}" -gt "1" ] || { [ "${MAJOR}" = "1" ] && [ "${MINOR}" -ge "2" ]; }; then
      aa_namespace="incus-inc-apparmor-test_<$(echo "${INCUS_DIR}" | sed -e 's/\//-/g' -e 's/^.//')>"
      aa-status | grep -q ":${aa_namespace}:unconfined" || aa-status | grep -qF ":${aa_namespace}://unconfined"
      inc stop inc-apparmor-test --force
      ! aa-status | grep -qF ":${aa_namespace}:" || false
    else
      aa-status | grep "incus-inc-apparmor-test_<${INCUS_DIR}>"
      inc stop inc-apparmor-test --force
      ! aa-status | grep -qF "incus-inc-apparmor-test_<${INCUS_DIR}>" || false
    fi
    inc delete inc-apparmor-test
    [ ! -f "${INCUS_DIR}/security/apparmor/profiles/incus-inc-apparmor-test" ]
  else
    echo "==> SKIP: apparmor tests (missing kernel support)"
  fi

  if [ "$(awk '/^Seccomp:/ {print $2}' "/proc/self/status")" -eq "0" ]; then
    inc launch testimage inc-seccomp-test
    init=$(inc info inc-seccomp-test | awk '/^PID:/ {print $2}')
    [ "$(awk '/^Seccomp:/ {print $2}' "/proc/${init}/status")" -eq "2" ]
    inc stop --force inc-seccomp-test
    inc config set inc-seccomp-test security.syscalls.deny_default false
    inc start inc-seccomp-test
    init=$(inc info inc-seccomp-test | awk '/^PID:/ {print $2}')
    [ "$(awk '/^Seccomp:/ {print $2}' "/proc/${init}/status")" -eq "0" ]
    inc delete --force inc-seccomp-test
  else
    echo "==> SKIP: seccomp tests (seccomp filtering is externally enabled)"
  fi

  # make sure that privileged containers are not world-readable
  inc profile create unconfined
  inc profile set unconfined security.privileged true
  inc init testimage foo2 -p unconfined -s "incustest-$(basename "${INCUS_DIR}")"
  [ "$(stat -L -c "%a" "${INCUS_DIR}/containers/foo2")" = "100" ]
  inc delete foo2
  inc profile delete unconfined

  # Test boot.host_shutdown_timeout config setting
  inc init testimage configtest --config boot.host_shutdown_timeout=45
  [ "$(inc config get configtest boot.host_shutdown_timeout)" -eq 45 ]
  inc config set configtest boot.host_shutdown_timeout 15
  [ "$(inc config get configtest boot.host_shutdown_timeout)" -eq 15 ]
  inc delete configtest

  # Test deleting multiple images
  # Start 3 containers to create 3 different images
  inc launch testimage c1
  inc launch testimage c2
  inc launch testimage c3
  inc exec c1 -- touch /tmp/c1
  inc exec c2 -- touch /tmp/c2
  inc exec c3 -- touch /tmp/c3
  inc publish --force c1 --alias=image1
  inc publish --force c2 --alias=image2
  inc publish --force c3 --alias=image3
  # Delete multiple images with inc delete and confirm they're deleted
  inc image delete local:image1 local:image2 local:image3
  ! inc image list | grep -q image1 || false
  ! inc image list | grep -q image2 || false
  ! inc image list | grep -q image3 || false
  # Cleanup the containers
  inc delete --force c1 c2 c3

  # Test --all flag
  inc init testimage c1
  inc init testimage c2
  inc start --all
  inc list | grep c1 | grep RUNNING
  inc list | grep c2 | grep RUNNING
  ! inc stop --all c1 || false
  inc stop --all -f
  inc list | grep c1 | grep STOPPED
  inc list | grep c2 | grep STOPPED
  # Cleanup the containers
  inc delete --force c1 c2

  # Ephemeral
  inc launch testimage foo -e
  OLD_INIT=$(inc info foo | awk '/^PID:/ {print $2}')

  REBOOTED="false"

  for _ in $(seq 60); do
    NEW_INIT=$(inc info foo | awk '/^PID:/ {print $2}' || true)

    # If init process is running, check if is old or new process.
    if [ -n "${NEW_INIT}" ]; then
      if [ "${OLD_INIT}" != "${NEW_INIT}" ]; then
        REBOOTED="true"
        break
      else
        inc exec foo reboot || true  # Signal to running old init processs to reboot if not rebooted yet.
      fi
    fi

    sleep 0.5
  done

  [ "${REBOOTED}" = "true" ]

  inc publish foo --alias foo --force
  inc image delete foo

  inc restart -f foo
  inc stop foo --force
  ! inc list | grep -q foo || false

  # Test renaming/deletion of the default profile
  ! inc profile rename default foobar || false
  ! inc profile delete default || false

  inc init testimage c1
  result="$(! inc config device override c1 root pool=bla 2>&1)"
  if ! echo "${result}" | grep "Error: Cannot update root disk device pool name"; then
    echo "Should fail device override because root disk device storage pool cannot be changed."
    false
  fi

  inc rm -f c1

  # Should fail to override root device storage pool when the new pool does not exist.
  ! inc init testimage c1 -d root,pool=bla || false

  # Should succeed in overriding root device storage pool when the pool does exist and the override occurs at create time.
  inc storage create bla dir
  inc init testimage c1 -d root,pool=bla
  inc config show c1 --expanded | grep -Pz '  root:\n    path: /\n    pool: bla\n    type: disk\n'

  inc storage volume create bla vol1
  inc storage volume create bla vol2
  inc config device add c1 dev disk source=vol1 pool=bla path=/vol

  # Should not be able to override a device that is not part of a profile (i.e. has been specifically added).
  result="$(! inc config device override c1 dev source=vol2 2>&1)"
  if ! echo "${result}" | grep "Error: The device already exists"; then
    echo "Should fail because device is defined against the instance not the profile."
    false
  fi

  inc rm -f c1
  inc storage volume delete bla vol1
  inc storage volume delete bla vol2
  inc storage delete bla

  # Test rebuilding an instance with its original image.
  inc init testimage c1
  inc start c1
  inc exec c1 -- touch /data.txt
  inc stop c1
  inc rebuild testimage c1
  inc start c1
  ! inc exec c1 -- stat /data.txt || false
  inc delete c1 -f

  # Test a forced rebuild
  inc launch testimage c1
  ! inc rebuild testimage c1 || false
  inc rebuild testimage c1 --force
  inc delete c1 -f

  # Test rebuilding an instance with a new image.
  inc init c1 --empty
  inc remote add l1 "${INCUS_ADDR}" --accept-certificate --password foo
  inc rebuild l1:testimage c1
  inc start c1
  inc delete c1 -f
  inc remote remove l1

  # Test rebuilding an instance with an empty file system.
  inc init testimage c1
  inc rebuild c1 --empty
  inc delete c1 -f

  # Test assigning an empty profile (with no root disk device) to an instance.
  inc init testimage c1
  inc profile create foo
  ! inc profile assign c1 foo || false
  inc profile delete foo
  inc delete -f c1
}
