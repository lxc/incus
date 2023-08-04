test_filemanip() {
  # Workaround for shellcheck getting confused by "cd"
  set -e
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  echo "test" > "${TEST_DIR}"/filemanip

  inc project create test -c features.profiles=false -c features.images=false -c features.storage.volumes=false
  inc project switch test
  inc launch testimage filemanip
  inc exec filemanip --project=test -- ln -s /tmp/ /tmp/outside
  inc file push "${TEST_DIR}"/filemanip filemanip/tmp/outside/

  [ ! -f /tmp/filemanip ]
  inc exec filemanip --project=test -- ls /tmp/filemanip

  # missing files should return 404
  err=$(my_curl -o /dev/null -w "%{http_code}" -X GET "https://${INCUS_ADDR}/1.0/instances/filemanip/files?path=/tmp/foo")
  [ "${err}" -eq "404" ]

  # inc {push|pull} -r
  mkdir "${TEST_DIR}"/source
  mkdir "${TEST_DIR}"/source/another_level
  chown 1000:1000 "${TEST_DIR}"/source/another_level
  echo "foo" > "${TEST_DIR}"/source/foo
  echo "bar" > "${TEST_DIR}"/source/bar
  ln -s bar "${TEST_DIR}"/source/baz

  inc file push -p -r "${TEST_DIR}"/source filemanip/tmp/ptest

  [ "$(inc exec filemanip --project=test -- stat -c "%u" /tmp/ptest/source)" = "$(id -u)" ]
  [ "$(inc exec filemanip --project=test -- stat -c "%g" /tmp/ptest/source)" = "$(id -g)" ]
  [ "$(inc exec filemanip --project=test -- stat -c "%u" /tmp/ptest/source/another_level)" = "1000" ]
  [ "$(inc exec filemanip --project=test -- stat -c "%g" /tmp/ptest/source/another_level)" = "1000" ]
  [ "$(inc exec filemanip --project=test -- stat -c "%a" /tmp/ptest/source)" = "755" ]
  [ "$(inc exec filemanip --project=test -- readlink /tmp/ptest/source/baz)" = "bar" ]

  inc exec filemanip --project=test -- rm -rf /tmp/ptest/source

  # Test pushing/pulling a file with spaces
  echo "foo" > "${TEST_DIR}/source/file with spaces"

  inc file push -p -r "${TEST_DIR}"/source filemanip/tmp/ptest
  inc exec filemanip --project=test -- find /tmp/ptest/source | grep -q "file with spaces"
  rm -rf "${TEST_DIR}/source/file with spaces"

  inc file pull -p -r filemanip/tmp/ptest "${TEST_DIR}/dest"
  find "${TEST_DIR}/dest/" | grep "file with spaces"
  rm -rf "${TEST_DIR}/dest"

  # Check that file permissions are not applied to intermediate directories
  inc file push -p --mode=400 "${TEST_DIR}"/source/foo \
      filemanip/tmp/ptest/d1/d2/foo

  [ "$(inc exec filemanip --project=test -- stat -c "%a" /tmp/ptest/d1)" = "750" ]
  [ "$(inc exec filemanip --project=test -- stat -c "%a" /tmp/ptest/d1/d2)" = "750" ]

  inc exec filemanip --project=test -- rm -rf /tmp/ptest/d1

  # Special case where we are in the same directory as the one we are currently
  # created.
  oldcwd=$(pwd)
  cd "${TEST_DIR}"

  inc file push -r source filemanip/tmp/ptest

  [ "$(inc exec filemanip --project=test -- stat -c "%u" /tmp/ptest/source)" = "$(id -u)" ]
  [ "$(inc exec filemanip --project=test -- stat -c "%g" /tmp/ptest/source)" = "$(id -g)" ]
  [ "$(inc exec filemanip --project=test -- stat -c "%a" /tmp/ptest/source)" = "755" ]

  inc exec filemanip --project=test -- rm -rf /tmp/ptest/source

  # Special case where we are in the same directory as the one we are currently
  # created.
  cd source

  inc file push -r ./ filemanip/tmp/ptest

  [ "$(inc exec filemanip --project=test -- stat -c "%u" /tmp/ptest/another_level)" = "1000" ]
  [ "$(inc exec filemanip --project=test -- stat -c "%g" /tmp/ptest/another_level)" = "1000" ]

  inc exec filemanip --project=test -- rm -rf /tmp/ptest/*

  inc file push -r ../source filemanip/tmp/ptest

  [ "$(inc exec filemanip --project=test -- stat -c "%u" /tmp/ptest/source)" = "$(id -u)" ]
  [ "$(inc exec filemanip --project=test -- stat -c "%g" /tmp/ptest/source)" = "$(id -g)" ]
  [ "$(inc exec filemanip --project=test -- stat -c "%a" /tmp/ptest/source)" = "755" ]

  # Switch back to old working directory.
  cd "${oldcwd}"

  mkdir "${TEST_DIR}"/dest
  inc file pull -r filemanip/tmp/ptest/source "${TEST_DIR}"/dest

  [ "$(cat "${TEST_DIR}"/dest/source/foo)" = "foo" ]
  [ "$(cat "${TEST_DIR}"/dest/source/bar)" = "bar" ]

  [ "$(stat -c "%u" "${TEST_DIR}"/dest/source)" = "$(id -u)" ]
  [ "$(stat -c "%g" "${TEST_DIR}"/dest/source)" = "$(id -g)" ]
  [ "$(stat -c "%a" "${TEST_DIR}"/dest/source)" = "755" ]

  inc file push -p "${TEST_DIR}"/source/foo local:filemanip/tmp/this/is/a/nonexistent/directory/
  inc file pull local:filemanip/tmp/this/is/a/nonexistent/directory/foo "${TEST_DIR}"
  [ "$(cat "${TEST_DIR}"/foo)" = "foo" ]

  inc file push -p "${TEST_DIR}"/source/foo filemanip/.
  [ "$(inc exec filemanip --project=test -- cat /foo)" = "foo" ]

  inc file push -p "${TEST_DIR}"/source/foo filemanip/A/B/C/D/
  [ "$(inc exec filemanip --project=test -- cat /A/B/C/D/foo)" = "foo" ]

  if [ "$(storage_backend "$INCUS_DIR")" != "lvm" ]; then
    inc launch testimage idmap -c "raw.idmap=both 0 0"
    [ "$(stat -c %u "${INCUS_DIR}/containers/test_idmap/rootfs")" = "0" ]
    inc delete idmap --force
  fi

  # Test SFTP functionality.
  cmd=$(unset -f inc; command -v inc)
  $cmd file mount filemanip --listen=127.0.0.1:2022 --no-auth &
  mountPID=$!
  sleep 1

  output=$(curl -s -S --insecure sftp://127.0.0.1:2022/foo || true)
  kill -9 ${mountPID}
  inc delete filemanip -f
  [ "$output" = "foo" ]

  rm "${TEST_DIR}"/source/baz
  rm -rf "${TEST_DIR}/dest"
  inc project switch default
  inc project delete test
}
