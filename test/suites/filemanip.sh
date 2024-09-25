test_filemanip() {
  # Workaround for shellcheck getting confused by "cd"
  set -e
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  echo "test" > "${TEST_DIR}"/filemanip

  incus project create test -c features.profiles=false -c features.images=false -c features.storage.volumes=false
  incus project switch test
  incus launch testimage filemanip
  incus exec filemanip --project=test -- ln -s /tmp/ /tmp/outside
  incus file push "${TEST_DIR}"/filemanip filemanip/tmp/outside/

  [ ! -f /tmp/filemanip ]
  incus exec filemanip --project=test -- ls /tmp/filemanip

  # missing files should return 404
  err=$(my_curl -o /dev/null -w "%{http_code}" -X GET "https://${INCUS_ADDR}/1.0/instances/filemanip/files?path=/tmp/foo")
  [ "${err}" -eq "404" ]

  # incus {push|pull} -r
  mkdir "${TEST_DIR}"/source
  mkdir "${TEST_DIR}"/source/another_level
  chown 1000:1000 "${TEST_DIR}"/source/another_level
  echo "foo" > "${TEST_DIR}"/source/foo
  echo "bar" > "${TEST_DIR}"/source/bar
  ln -s bar "${TEST_DIR}"/source/baz

  incus file push -p -r "${TEST_DIR}"/source filemanip/tmp/ptest

  [ "$(incus exec filemanip --project=test -- stat -c "%u" /tmp/ptest/source)" = "$(id -u)" ]
  [ "$(incus exec filemanip --project=test -- stat -c "%g" /tmp/ptest/source)" = "$(id -g)" ]
  [ "$(incus exec filemanip --project=test -- stat -c "%u" /tmp/ptest/source/another_level)" = "1000" ]
  [ "$(incus exec filemanip --project=test -- stat -c "%g" /tmp/ptest/source/another_level)" = "1000" ]
  [ "$(incus exec filemanip --project=test -- stat -c "%a" /tmp/ptest/source)" = "755" ]
  [ "$(incus exec filemanip --project=test -- readlink /tmp/ptest/source/baz)" = "bar" ]

  incus exec filemanip --project=test -- rm -rf /tmp/ptest/source

  # Test pushing/pulling a file with spaces
  echo "foo" > "${TEST_DIR}/source/file with spaces"

  incus file push -p -r "${TEST_DIR}"/source filemanip/tmp/ptest
  incus exec filemanip --project=test -- find /tmp/ptest/source | grep -q "file with spaces"
  rm -rf "${TEST_DIR}/source/file with spaces"

  incus file pull -p -r filemanip/tmp/ptest "${TEST_DIR}/dest"
  find "${TEST_DIR}/dest/" | grep "file with spaces"
  rm -rf "${TEST_DIR}/dest"

  # Check that file permissions are not applied to intermediate directories
  incus file push -p --mode=400 "${TEST_DIR}"/source/foo \
      filemanip/tmp/ptest/d1/d2/foo

  [ "$(incus exec filemanip --project=test -- stat -c "%a" /tmp/ptest/d1)" = "750" ]
  [ "$(incus exec filemanip --project=test -- stat -c "%a" /tmp/ptest/d1/d2)" = "750" ]

  incus exec filemanip --project=test -- rm -rf /tmp/ptest/d1

  # Special case where we are in the same directory as the one we are currently
  # created.
  oldcwd=$(pwd)
  cd "${TEST_DIR}"

  incus file push -r source filemanip/tmp/ptest

  [ "$(incus exec filemanip --project=test -- stat -c "%u" /tmp/ptest/source)" = "$(id -u)" ]
  [ "$(incus exec filemanip --project=test -- stat -c "%g" /tmp/ptest/source)" = "$(id -g)" ]
  [ "$(incus exec filemanip --project=test -- stat -c "%a" /tmp/ptest/source)" = "755" ]

  incus exec filemanip --project=test -- rm -rf /tmp/ptest/source

  # Special case where we are in the same directory as the one we are currently
  # created.
  cd source

  incus file push -r ./ filemanip/tmp/ptest

  [ "$(incus exec filemanip --project=test -- stat -c "%u" /tmp/ptest/another_level)" = "1000" ]
  [ "$(incus exec filemanip --project=test -- stat -c "%g" /tmp/ptest/another_level)" = "1000" ]

  incus exec filemanip --project=test -- rm -rf /tmp/ptest/*

  incus file push -r ../source filemanip/tmp/ptest

  [ "$(incus exec filemanip --project=test -- stat -c "%u" /tmp/ptest/source)" = "$(id -u)" ]
  [ "$(incus exec filemanip --project=test -- stat -c "%g" /tmp/ptest/source)" = "$(id -g)" ]
  [ "$(incus exec filemanip --project=test -- stat -c "%a" /tmp/ptest/source)" = "755" ]

  # Switch back to old working directory.
  cd "${oldcwd}"

  mkdir "${TEST_DIR}"/dest
  incus file pull -r filemanip/tmp/ptest/source "${TEST_DIR}"/dest

  [ "$(cat "${TEST_DIR}"/dest/source/foo)" = "foo" ]
  [ "$(cat "${TEST_DIR}"/dest/source/bar)" = "bar" ]

  [ "$(stat -c "%u" "${TEST_DIR}"/dest/source)" = "$(id -u)" ]
  [ "$(stat -c "%g" "${TEST_DIR}"/dest/source)" = "$(id -g)" ]
  [ "$(stat -c "%a" "${TEST_DIR}"/dest/source)" = "755" ]

  incus file push -p "${TEST_DIR}"/source/foo local:filemanip/tmp/this/is/a/nonexistent/directory/
  incus file pull local:filemanip/tmp/this/is/a/nonexistent/directory/foo "${TEST_DIR}"
  [ "$(cat "${TEST_DIR}"/foo)" = "foo" ]

  incus file push -p "${TEST_DIR}"/source/foo filemanip/.
  [ "$(incus exec filemanip --project=test -- cat /foo)" = "foo" ]

  incus file push -p "${TEST_DIR}"/source/foo filemanip/A/B/C/D/
  [ "$(incus exec filemanip --project=test -- cat /A/B/C/D/foo)" = "foo" ]

  if [ "$(storage_backend "$INCUS_DIR")" != "lvm" ]; then
    incus launch testimage idmap -c "raw.idmap=both 0 0"
    [ "$(stat -c %u "${INCUS_DIR}/containers/test_idmap/rootfs")" = "0" ]
    incus delete idmap --force
  fi

  # Test incus file create.

  # Create a new empty file.
  incus file create filemanip/tmp/create-test
  [ -z "$(incus exec filemanip --project=test -- cat /tmp/create-test)" ]

  # This fails because the parent directory doesn't exist.
  ! incus file create filemanip/tmp/create-test-dir/foo || false

  # Create foo along with the parent directory.
  incus file create --create-dirs filemanip/tmp/create-test-dir/foo
  [ -z "$(incus exec filemanip --project=test -- cat /tmp/create-test-dir/foo)" ]

  # Create directory using --type flag.
  incus file create --type=directory filemanip/tmp/create-test-dir/sub-dir
  incus exec filemanip --project=test -- test -d /tmp/create-test-dir/sub-dir

  # Create directory using trailing "/".
  incus file create filemanip/tmp/create-test-dir/sub-dir-1/
  incus exec filemanip --project=test -- test -d /tmp/create-test-dir/sub-dir-1

  # Create symlink.
  incus file create --type=symlink filemanip/tmp/create-symlink foo
  [ "$(incus exec filemanip --project=test -- readlink /tmp/create-symlink)" = "foo" ]

  # Test SFTP functionality.
  cmd=$(unset -f incus; command -v incus)
  $cmd file mount filemanip --listen=127.0.0.1:2022 --no-auth &
  mountPID=$!
  sleep 1

  output=$(curl -s -S --insecure sftp://127.0.0.1:2022/foo || true)
  kill -9 ${mountPID}
  incus delete filemanip -f
  [ "$output" = "foo" ]

  rm "${TEST_DIR}"/source/baz
  rm -rf "${TEST_DIR}/dest"
  incus project switch default
  incus project delete test
}
