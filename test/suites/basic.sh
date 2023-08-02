test_basic_usage() {
  # shellcheck disable=2039,3043
  local incus_backend
  incus_backend=$(storage_backend "$INCUS_DIR")

  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  # Test image export
  sum="$(lxc image info testimage | awk '/^Fingerprint/ {print $2}')"
  lxc image export testimage "${INCUS_DIR}/"
  [ "${sum}" = "$(sha256sum "${INCUS_DIR}/${sum}.tar.xz" | cut -d' ' -f1)" ]

  # Test an alias with slashes
  lxc image show "${sum}"
  lxc image alias create a/b/ "${sum}"
  lxc image alias delete a/b/

  # Test alias list filtering
  lxc image alias create foo "${sum}"
  lxc image alias create bar "${sum}"
  lxc image alias list local: | grep -q foo
  lxc image alias list local: | grep -q bar
  lxc image alias list local: foo | grep -q -v bar
  lxc image alias list local: "${sum}" | grep -q foo
  lxc image alias list local: non-existent | grep -q -v non-existent
  lxc image alias delete foo
  lxc image alias delete bar

  lxc image alias create foo "${sum}"
  lxc image alias rename foo bar
  lxc image alias list | grep -qv foo  # the old name is gone
  lxc image alias delete bar

  # Test image list output formats (table & json)
  lxc image list --format table | grep -q testimage
  lxc image list --format json \
    | jq '.[]|select(.alias[0].name="testimage")' \
    | grep -q '"name": "testimage"'

  # Test image delete
  lxc image delete testimage

  # test GET /1.0, since the client always puts to /1.0/
  my_curl -f -X GET "https://${INCUS_ADDR}/1.0"
  my_curl -f -X GET "https://${INCUS_ADDR}/1.0/containers"

  # Re-import the image
  mv "${INCUS_DIR}/${sum}.tar.xz" "${INCUS_DIR}/testimage.tar.xz"
  lxc image import "${INCUS_DIR}/testimage.tar.xz" --alias testimage user.foo=bar --public
  lxc image show testimage | grep -qF "user.foo: bar"
  lxc image show testimage | grep -qF "public: true"
  lxc image delete testimage
  lxc image import "${INCUS_DIR}/testimage.tar.xz" --alias testimage
  rm "${INCUS_DIR}/testimage.tar.xz"

  # Test filename for image export
  lxc image export testimage "${INCUS_DIR}/"
  [ "${sum}" = "$(sha256sum "${INCUS_DIR}/${sum}.tar.xz" | cut -d' ' -f1)" ]
  rm "${INCUS_DIR}/${sum}.tar.xz"

  # Test custom filename for image export
  lxc image export testimage "${INCUS_DIR}/foo"
  [ "${sum}" = "$(sha256sum "${INCUS_DIR}/foo.tar.xz" | cut -d' ' -f1)" ]
  rm "${INCUS_DIR}/foo.tar.xz"


  # Test image export with a split image.
  deps/import-busybox --split --alias splitimage

  sum="$(lxc image info splitimage | awk '/^Fingerprint/ {print $2}')"

  lxc image export splitimage "${INCUS_DIR}"
  [ "${sum}" = "$(cat "${INCUS_DIR}/meta-${sum}.tar.xz" "${INCUS_DIR}/${sum}.tar.xz" | sha256sum | cut -d' ' -f1)" ]

  # Delete the split image and exported files
  rm "${INCUS_DIR}/${sum}.tar.xz"
  rm "${INCUS_DIR}/meta-${sum}.tar.xz"
  lxc image delete splitimage

  # Redo the split image export test, this time with the --filename flag
  # to tell import-busybox to set the 'busybox' filename in the upload.
  # The sum should remain the same as its the same image.
  deps/import-busybox --split --filename --alias splitimage

  lxc image export splitimage "${INCUS_DIR}"
  [ "${sum}" = "$(cat "${INCUS_DIR}/meta-${sum}.tar.xz" "${INCUS_DIR}/${sum}.tar.xz" | sha256sum | cut -d' ' -f1)" ]

  # Delete the split image and exported files
  rm "${INCUS_DIR}/${sum}.tar.xz"
  rm "${INCUS_DIR}/meta-${sum}.tar.xz"
  lxc image delete splitimage

  # Test --no-profiles flag
  poolName=$(lxc profile device get default root pool)
  ! lxc init testimage foo --no-profiles || false
  lxc init testimage foo --no-profiles -s "${poolName}"
  lxc delete -f foo

  # Test container creation
  lxc init testimage foo
  lxc list | grep foo | grep STOPPED
  lxc list fo | grep foo | grep STOPPED

  # Test list json format
  lxc list --format json | jq '.[]|select(.name="foo")' | grep '"name": "foo"'

  # Test list with --columns and --fast
  ! lxc list --columns=nsp --fast || false

  # Check volatile.apply_template is correct.
  lxc config get foo volatile.apply_template | grep create

  # Start the instance to clear apply_template.
  lxc start foo
  lxc stop foo -f

  # Test container rename
  lxc move foo bar

  # Check volatile.apply_template is altered during rename.
  lxc config get bar volatile.apply_template | grep rename

  lxc list | grep -v foo
  lxc list | grep bar

  lxc rename bar foo
  lxc list | grep -v bar
  lxc list | grep foo
  lxc rename foo bar

  # Test container copy
  lxc copy bar foo
  lxc delete foo

  # gen untrusted cert
  gen_cert client3

  # don't allow requests without a cert to get trusted data
  curl -k -s -X GET "https://${INCUS_ADDR}/1.0/containers/foo" | grep 403

  # Test unprivileged container publish
  lxc publish bar --alias=foo-image prop1=val1
  lxc image show foo-image | grep val1
  curl -k -s --cert "${INCUS_CONF}/client3.crt" --key "${INCUS_CONF}/client3.key" -X GET "https://${INCUS_ADDR}/1.0/images" | grep -F "/1.0/images/" && false
  lxc image delete foo-image

  # Test container publish with existing alias
  lxc publish bar --alias=foo-image --alias=foo-image2
  lxc launch testimage baz
  # change the container filesystem so the resulting image is different
  lxc exec baz touch /somefile
  lxc stop baz --force
  # publishing another image with same alias should fail
  ! lxc publish baz --alias=foo-image || false
  # publishing another image with same alias and '--reuse' flag should success
  lxc publish baz --alias=foo-image --reuse
  fooImage=$(lxc image list -cF -fcsv foo-image)
  fooImage2=$(lxc image list -cF -fcsv foo-image2)
  lxc delete baz
  lxc image delete foo-image foo-image2

  # the first image should have foo-image2 alias and the second imgae foo-image alias
  if [ "$fooImage" = "$fooImage2" ]; then
    echo "foo-image and foo-image2 aliases should be assigned to two different images"
    false
  fi


  # Test container publish with existing alias
  lxc publish bar --alias=foo-image --alias=foo-image2
  lxc launch testimage baz
  # change the container filesystem so the resulting image is different
  lxc exec baz touch /somefile
  lxc stop baz --force
  # publishing another image with same aliases
  lxc publish baz --alias=foo-image --alias=foo-image2 --reuse
  fooImage=$(lxc image list -cF -fcsv foo-image)
  fooImage2=$(lxc image list -cF -fcsv foo-image2)
  lxc delete baz
  lxc image delete foo-image

  # the second image should have foo-image and foo-image2 aliases and the first one should be removed
  if [ "$fooImage" != "$fooImage2" ]; then
    echo "foo-image and foo-image2 aliases should be assigned to the same image"
    false
  fi


  # Test image compression on publish
  lxc publish bar --alias=foo-image-compressed --compression=bzip2 prop=val1
  lxc image show foo-image-compressed | grep val1
  curl -k -s --cert "${INCUS_CONF}/client3.crt" --key "${INCUS_CONF}/client3.key" -X GET "https://${INCUS_ADDR}/1.0/images" | grep -F "/1.0/images/" && false
  lxc image delete foo-image-compressed

  # Test compression options
  lxc publish bar --alias=foo-image-compressed --compression="gzip --rsyncable" prop=val1
  lxc image delete foo-image-compressed

  # Test privileged container publish
  lxc profile create priv
  lxc profile set priv security.privileged true
  lxc init testimage barpriv -p default -p priv
  lxc publish barpriv --alias=foo-image prop1=val1
  lxc image show foo-image | grep val1
  curl -k -s --cert "${INCUS_CONF}/client3.crt" --key "${INCUS_CONF}/client3.key" -X GET "https://${INCUS_ADDR}/1.0/images" | grep -F "/1.0/images/" && false
  lxc image delete foo-image
  lxc delete barpriv
  lxc profile delete priv

  # Test that containers without metadata.yaml are published successfully.
  # Note that this quick hack won't work for LVM, since it doesn't always mount
  # the container's filesystem. That's ok though: the logic we're trying to
  # test here is independent of storage backend, so running it for just one
  # backend (or all non-lvm backends) is enough.
  if [ "$incus_backend" = "lvm" ]; then
    lxc init testimage nometadata
    rm -f "${INCUS_DIR}/containers/nometadata/metadata.yaml"
    lxc publish nometadata --alias=nometadata-image
    lxc image delete nometadata-image
    lxc delete nometadata
  fi

  # Test public images
  lxc publish --public bar --alias=foo-image2
  curl -k -s --cert "${INCUS_CONF}/client3.crt" --key "${INCUS_CONF}/client3.key" -X GET "https://${INCUS_ADDR}/1.0/images" | grep -F "/1.0/images/"
  lxc image delete foo-image2

  # Test invalid container names
  ! lxc init testimage -abc || false
  ! lxc init testimage abc- || false
  ! lxc init testimage 1234 || false
  ! lxc init testimage 12test || false
  ! lxc init testimage a_b_c || false
  ! lxc init testimage aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa || false

  # Test snapshot publish
  lxc snapshot bar
  lxc publish bar/snap0 --alias foo
  lxc init foo bar2
  lxc list | grep bar2
  lxc delete bar2
  lxc image delete foo

  # Test alias support
  cp "${INCUS_CONF}/config.yml" "${INCUS_CONF}/config.yml.bak"

  #   1. Basic built-in alias functionality
  [ "$(lxc ls)" = "$(lxc list)" ]
  #   2. Basic user-defined alias functionality
  printf "aliases:\\n  l: list\\n" >> "${INCUS_CONF}/config.yml"
  [ "$(lxc l)" = "$(lxc list)" ]
  #   3. Built-in aliases and user-defined aliases can coexist
  [ "$(lxc ls)" = "$(lxc l)" ]
  #   4. Multi-argument alias keys and values
  echo "  i ls: image list" >> "${INCUS_CONF}/config.yml"
  [ "$(lxc i ls)" = "$(lxc image list)" ]
  #   5. Aliases where len(keys) != len(values) (expansion/contraction of number of arguments)
  printf "  ils: image list\\n  container ls: list\\n" >> "${INCUS_CONF}/config.yml"
  [ "$(lxc ils)" = "$(lxc image list)" ]
  [ "$(lxc container ls)" = "$(lxc list)" ]
  #   6. User-defined aliases override built-in aliases
  echo "  cp: list" >> "${INCUS_CONF}/config.yml"
  [ "$(lxc ls)" = "$(lxc cp)" ]
  #   7. User-defined aliases override commands and don't recurse
  lxc init testimage foo
  INC_CONFIG_SHOW=$(lxc config show foo --expanded)
  echo "  config show: config show --expanded" >> "${INCUS_CONF}/config.yml"
  [ "$(lxc config show foo)" = "$INC_CONFIG_SHOW" ]
  lxc delete foo

  # Restore the config to remove the aliases
  mv "${INCUS_CONF}/config.yml.bak" "${INCUS_CONF}/config.yml"

  # Delete the bar container we've used for several tests
  lxc delete bar

  # lxc delete should also delete all snapshots of bar
  [ ! -d "${INCUS_DIR}/snapshots/bar" ]

  # Test randomly named container creation
  lxc launch testimage
  RDNAME=$(lxc list --format csv --columns n)
  lxc delete -f "${RDNAME}"

  # Test "nonetype" container creation
  wait_for "${INCUS_ADDR}" my_curl -X POST "https://${INCUS_ADDR}/1.0/containers" \
        -d "{\"name\":\"nonetype\",\"source\":{\"type\":\"none\"}}"
  lxc delete nonetype

  # Test "nonetype" container creation with an LXC config
  wait_for "${INCUS_ADDR}" my_curl -X POST "https://${INCUS_ADDR}/1.0/containers" \
        -d "{\"name\":\"configtest\",\"config\":{\"raw.lxc\":\"lxc.hook.clone=/bin/true\"},\"source\":{\"type\":\"none\"}}"
  # shellcheck disable=SC2102
  [ "$(my_curl "https://${INCUS_ADDR}/1.0/containers/configtest" | jq -r .metadata.config[\"raw.lxc\"])" = "lxc.hook.clone=/bin/true" ]
  lxc delete configtest

  # Test activateifneeded/shutdown
  INCUS_ACTIVATION_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ACTIVATION_DIR}"
  spawn_incus "${INCUS_ACTIVATION_DIR}" true
  (
    set -e
    # shellcheck disable=SC2030
    INCUS_DIR=${INCUS_ACTIVATION_DIR}
    ensure_import_testimage
    lxd activateifneeded --debug 2>&1 | grep -qF "Daemon has core.https_address set, activating..."
    lxc config unset core.https_address --force-local
    lxd activateifneeded --debug 2>&1 | grep -qF -v "activating..."
    lxc init testimage autostart --force-local
    lxd activateifneeded --debug 2>&1 | grep -qF -v "activating..."
    lxc config set autostart boot.autostart true --force-local

    # Restart the daemon, this forces the global database to be dumped to disk.
    shutdown_incus "${INCUS_DIR}"
    respawn_incus "${INCUS_DIR}" true
    lxc stop --force autostart --force-local

    lxd activateifneeded --debug 2>&1 | grep -qF "Daemon has auto-started instances, activating..."

    lxc config unset autostart boot.autostart --force-local
    lxd activateifneeded --debug 2>&1 | grep -qF -v "activating..."

    lxc start autostart --force-local
    PID=$(lxc info autostart --force-local | awk '/^PID:/ {print $2}')
    shutdown_incus "${INCUS_DIR}"
    [ -d "/proc/${PID}" ] && false

    lxd activateifneeded --debug 2>&1 | grep -qF "Daemon has auto-started instances, activating..."

    # shellcheck disable=SC2031
    respawn_incus "${INCUS_DIR}" true

    lxc list --force-local autostart | grep -q RUNNING

    # Check for scheduled instance snapshots
    lxc stop --force autostart --force-local
    lxc config set autostart snapshots.schedule "* * * * *" --force-local
    shutdown_incus "${INCUS_DIR}"
    lxd activateifneeded --debug 2>&1 | grep -qF "Daemon has scheduled instance snapshots, activating..."

    # shellcheck disable=SC2031
    respawn_incus "${INCUS_DIR}" true

    lxc config unset autostart snapshots.schedule --force-local

    # Check for scheduled volume snapshots
    storage_pool="incustest-$(basename "${INCUS_DIR}")"

    lxc storage volume create "${storage_pool}" vol --force-local

    shutdown_incus "${INCUS_DIR}"
    lxd activateifneeded --debug 2>&1 | grep -qF -v "activating..."

    # shellcheck disable=SC2031
    respawn_incus "${INCUS_DIR}" true

    lxc storage volume set "${storage_pool}" vol snapshots.schedule="* * * * *" --force-local

    shutdown_incus "${INCUS_DIR}"
    lxd activateifneeded --debug 2>&1 | grep -qF "Daemon has scheduled volume snapshots, activating..."

    # shellcheck disable=SC2031
    respawn_incus "${INCUS_DIR}" true

    lxc delete autostart --force --force-local
    lxc storage volume delete "${storage_pool}" vol --force-local
  )
  # shellcheck disable=SC2031,2269
  INCUS_DIR=${INCUS_DIR}
  kill_incus "${INCUS_ACTIVATION_DIR}"

  # Create and start a container
  lxc launch testimage foo
  lxc list | grep foo | grep RUNNING
  lxc stop foo --force

  # cycle it a few times
  lxc start foo
  mac1=$(lxc exec foo cat /sys/class/net/eth0/address)
  lxc stop foo --force
  lxc start foo
  mac2=$(lxc exec foo cat /sys/class/net/eth0/address)

  if [ -n "${mac1}" ] && [ -n "${mac2}" ] && [ "${mac1}" != "${mac2}" ]; then
    echo "==> MAC addresses didn't match across restarts (${mac1} vs ${mac2})"
    false
  fi

  # Test instance types
  lxc launch testimage test-limits -t c0.5-m0.2
  [ "$(lxc config get test-limits limits.cpu)" = "1" ]
  [ "$(lxc config get test-limits limits.cpu.allowance)" = "50%" ]
  [ "$(lxc config get test-limits limits.memory)" = "204MiB" ]
  lxc delete -f test-limits

  # Test last_used_at field is working properly
  lxc init testimage last-used-at-test
  lxc list last-used-at-test  --format json | jq -r '.[].last_used_at' | grep '1970-01-01T00:00:00Z'
  lxc start last-used-at-test
  lxc list last-used-at-test  --format json | jq -r '.[].last_used_at' | grep -v '1970-01-01T00:00:00Z'
  lxc delete last-used-at-test --force

  # Test user, group and cwd
  lxc exec foo -- mkdir /blah
  [ "$(lxc exec foo --user 1000 -- id -u)" = "1000" ] || false
  [ "$(lxc exec foo --group 1000 -- id -g)" = "1000" ] || false
  [ "$(lxc exec foo --cwd /blah -- pwd)" = "/blah" ] || false

  [ "$(lxc exec foo --user 1234 --group 5678 --cwd /blah -- id -u)" = "1234" ] || false
  [ "$(lxc exec foo --user 1234 --group 5678 --cwd /blah -- id -g)" = "5678" ] || false
  [ "$(lxc exec foo --user 1234 --group 5678 --cwd /blah -- pwd)" = "/blah" ] || false

  # check that we can set the environment
  lxc exec foo pwd | grep /root
  lxc exec --env BEST_BAND=meshuggah foo env | grep meshuggah
  lxc exec foo ip link show | grep eth0

  # check that we can get the return code for a non- wait-for-websocket exec
  op=$(my_curl -X POST "https://${INCUS_ADDR}/1.0/containers/foo/exec" -d '{"command": ["echo", "test"], "environment": {}, "wait-for-websocket": false, "interactive": false}' | jq -r .operation)
  [ "$(my_curl "https://${INCUS_ADDR}${op}/wait" | jq -r .metadata.metadata.return)" != "null" ]

  # test file transfer
  echo abc > "${INCUS_DIR}/in"

  lxc file push "${INCUS_DIR}/in" foo/root/
  lxc exec foo /bin/cat /root/in | grep abc
  lxc exec foo -- /bin/rm -f root/in

  lxc file push "${INCUS_DIR}/in" foo/root/in1
  lxc exec foo /bin/cat /root/in1 | grep abc
  lxc exec foo -- /bin/rm -f root/in1

  # test lxc file edit doesn't change target file's owner and permissions
  echo "content" | lxc file push - foo/tmp/edit_test
  lxc exec foo -- chown 55.55 /tmp/edit_test
  lxc exec foo -- chmod 555 /tmp/edit_test
  echo "new content" | lxc file edit foo/tmp/edit_test
  [ "$(lxc exec foo -- cat /tmp/edit_test)" = "new content" ]
  [ "$(lxc exec foo -- stat -c \"%u %g %a\" /tmp/edit_test)" = "55 55 555" ]

  # make sure stdin is chowned to our container root uid (Issue #590)
  [ -t 0 ] && [ -t 1 ] && lxc exec foo -- chown 1000:1000 /proc/self/fd/0

  echo foo | lxc exec foo tee /tmp/foo

  # Detect regressions/hangs in exec
  sum=$(ps aux | tee "${INCUS_DIR}/out" | lxc exec foo md5sum | cut -d' ' -f1)
  [ "${sum}" = "$(md5sum "${INCUS_DIR}/out" | cut -d' ' -f1)" ]
  rm "${INCUS_DIR}/out"

  # FIXME: make this backend agnostic
  if [ "$incus_backend" = "dir" ]; then
    content=$(cat "${INCUS_DIR}/containers/foo/rootfs/tmp/foo")
    [ "${content}" = "foo" ]
  fi

  lxc launch testimage deleterunning
  my_curl -X DELETE "https://${INCUS_ADDR}/1.0/containers/deleterunning" | grep "Instance is running"
  lxc delete deleterunning -f

  # cleanup
  lxc delete foo -f

  if [ -e /sys/module/apparmor/ ]; then
    # check that an apparmor profile is created for this container, that it is
    # unloaded on stop, and that it is deleted when the container is deleted
    lxc launch testimage lxd-apparmor-test

    MAJOR=0
    MINOR=0
    if [ -f /sys/kernel/security/apparmor/features/domain/version ]; then
      MAJOR=$(awk -F. '{print $1}' < /sys/kernel/security/apparmor/features/domain/version)
      MINOR=$(awk -F. '{print $2}' < /sys/kernel/security/apparmor/features/domain/version)
    fi

    if [ "${MAJOR}" -gt "1" ] || { [ "${MAJOR}" = "1" ] && [ "${MINOR}" -ge "2" ]; }; then
      aa_namespace="lxd-lxd-apparmor-test_<$(echo "${INCUS_DIR}" | sed -e 's/\//-/g' -e 's/^.//')>"
      aa-status | grep -q ":${aa_namespace}:unconfined" || aa-status | grep -qF ":${aa_namespace}://unconfined"
      lxc stop lxd-apparmor-test --force
      ! aa-status | grep -qF ":${aa_namespace}:" || false
    else
      aa-status | grep "lxd-lxd-apparmor-test_<${INCUS_DIR}>"
      lxc stop lxd-apparmor-test --force
      ! aa-status | grep -qF "lxd-lxd-apparmor-test_<${INCUS_DIR}>" || false
    fi
    lxc delete lxd-apparmor-test
    [ ! -f "${INCUS_DIR}/security/apparmor/profiles/lxd-lxd-apparmor-test" ]
  else
    echo "==> SKIP: apparmor tests (missing kernel support)"
  fi

  if [ "$(awk '/^Seccomp:/ {print $2}' "/proc/self/status")" -eq "0" ]; then
    lxc launch testimage lxd-seccomp-test
    init=$(lxc info lxd-seccomp-test | awk '/^PID:/ {print $2}')
    [ "$(awk '/^Seccomp:/ {print $2}' "/proc/${init}/status")" -eq "2" ]
    lxc stop --force lxd-seccomp-test
    lxc config set lxd-seccomp-test security.syscalls.deny_default false
    lxc start lxd-seccomp-test
    init=$(lxc info lxd-seccomp-test | awk '/^PID:/ {print $2}')
    [ "$(awk '/^Seccomp:/ {print $2}' "/proc/${init}/status")" -eq "0" ]
    lxc delete --force lxd-seccomp-test
  else
    echo "==> SKIP: seccomp tests (seccomp filtering is externally enabled)"
  fi

  # make sure that privileged containers are not world-readable
  lxc profile create unconfined
  lxc profile set unconfined security.privileged true
  lxc init testimage foo2 -p unconfined -s "incustest-$(basename "${INCUS_DIR}")"
  [ "$(stat -L -c "%a" "${INCUS_DIR}/containers/foo2")" = "100" ]
  lxc delete foo2
  lxc profile delete unconfined

  # Test boot.host_shutdown_timeout config setting
  lxc init testimage configtest --config boot.host_shutdown_timeout=45
  [ "$(lxc config get configtest boot.host_shutdown_timeout)" -eq 45 ]
  lxc config set configtest boot.host_shutdown_timeout 15
  [ "$(lxc config get configtest boot.host_shutdown_timeout)" -eq 15 ]
  lxc delete configtest

  # Test deleting multiple images
  # Start 3 containers to create 3 different images
  lxc launch testimage c1
  lxc launch testimage c2
  lxc launch testimage c3
  lxc exec c1 -- touch /tmp/c1
  lxc exec c2 -- touch /tmp/c2
  lxc exec c3 -- touch /tmp/c3
  lxc publish --force c1 --alias=image1
  lxc publish --force c2 --alias=image2
  lxc publish --force c3 --alias=image3
  # Delete multiple images with lxc delete and confirm they're deleted
  lxc image delete local:image1 local:image2 local:image3
  ! lxc image list | grep -q image1 || false
  ! lxc image list | grep -q image2 || false
  ! lxc image list | grep -q image3 || false
  # Cleanup the containers
  lxc delete --force c1 c2 c3

  # Test --all flag
  lxc init testimage c1
  lxc init testimage c2
  lxc start --all
  lxc list | grep c1 | grep RUNNING
  lxc list | grep c2 | grep RUNNING
  ! lxc stop --all c1 || false
  lxc stop --all -f
  lxc list | grep c1 | grep STOPPED
  lxc list | grep c2 | grep STOPPED
  # Cleanup the containers
  lxc delete --force c1 c2

  # Ephemeral
  lxc launch testimage foo -e
  OLD_INIT=$(lxc info foo | awk '/^PID:/ {print $2}')

  REBOOTED="false"

  for _ in $(seq 60); do
    NEW_INIT=$(lxc info foo | awk '/^PID:/ {print $2}' || true)

    # If init process is running, check if is old or new process.
    if [ -n "${NEW_INIT}" ]; then
      if [ "${OLD_INIT}" != "${NEW_INIT}" ]; then
        REBOOTED="true"
        break
      else
        lxc exec foo reboot || true  # Signal to running old init processs to reboot if not rebooted yet.
      fi
    fi

    sleep 0.5
  done

  [ "${REBOOTED}" = "true" ]

  lxc publish foo --alias foo --force
  lxc image delete foo

  lxc restart -f foo
  lxc stop foo --force
  ! lxc list | grep -q foo || false

  # Test renaming/deletion of the default profile
  ! lxc profile rename default foobar || false
  ! lxc profile delete default || false

  lxc init testimage c1
  result="$(! lxc config device override c1 root pool=bla 2>&1)"
  if ! echo "${result}" | grep "Error: Cannot update root disk device pool name"; then
    echo "Should fail device override because root disk device storage pool cannot be changed."
    false
  fi

  lxc rm -f c1

  # Should fail to override root device storage pool when the new pool does not exist.
  ! lxc init testimage c1 -d root,pool=bla || false

  # Should succeed in overriding root device storage pool when the pool does exist and the override occurs at create time.
  lxc storage create bla dir
  lxc init testimage c1 -d root,pool=bla
  lxc config show c1 --expanded | grep -Pz '  root:\n    path: /\n    pool: bla\n    type: disk\n'

  lxc storage volume create bla vol1
  lxc storage volume create bla vol2
  lxc config device add c1 dev disk source=vol1 pool=bla path=/vol

  # Should not be able to override a device that is not part of a profile (i.e. has been specifically added).
  result="$(! lxc config device override c1 dev source=vol2 2>&1)"
  if ! echo "${result}" | grep "Error: The device already exists"; then
    echo "Should fail because device is defined against the instance not the profile."
    false
  fi

  lxc rm -f c1
  lxc storage volume delete bla vol1
  lxc storage volume delete bla vol2
  lxc storage delete bla

  # Test rebuilding an instance with its original image.
  lxc init testimage c1
  lxc start c1
  lxc exec c1 -- touch /data.txt
  lxc stop c1
  lxc rebuild testimage c1
  lxc start c1
  ! lxc exec c1 -- stat /data.txt || false
  lxc delete c1 -f

  # Test a forced rebuild
  lxc launch testimage c1
  ! lxc rebuild testimage c1 || false
  lxc rebuild testimage c1 --force
  lxc delete c1 -f

  # Test rebuilding an instance with a new image.
  lxc init c1 --empty
  lxc remote add l1 "${INCUS_ADDR}" --accept-certificate --password foo
  lxc rebuild l1:testimage c1
  lxc start c1
  lxc delete c1 -f
  lxc remote remove l1

  # Test rebuilding an instance with an empty file system.
  lxc init testimage c1
  lxc rebuild c1 --empty
  lxc delete c1 -f

  # Test assigning an empty profile (with no root disk device) to an instance.
  lxc init testimage c1
  lxc profile create foo
  ! lxc profile assign c1 foo || false
  lxc profile delete foo
  lxc delete -f c1
}
