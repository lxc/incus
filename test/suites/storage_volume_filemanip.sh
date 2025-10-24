test_storage_volume_filemanip() {
    # Workaround for shellcheck getting confused by "cd"
    set -e
    ensure_import_testimage
    ensure_has_localhost_remote "${INCUS_ADDR}"

    echo "test" > "${TEST_DIR}"/filemanip

    incus project create test -c features.profiles=false -c features.images=false -c features.storage.volumes=false
    incus project switch test
    incus launch testimage filemanip
    pool="incustest-$(basename "${INCUS_DIR}")"
    incus storage volume create "${pool}" vol1
    incus storage volume attach "${pool}" vol1 filemanip /v1

    # missing files should return 404
    err=$(my_curl -o /dev/null -w "%{http_code}" -X GET "https://${INCUS_ADDR}/1.0/instances/filemanip/files?path=/tmp/foo")
    [ "${err}" -eq "404" ]

    # Test storage volume {push|pull} -r
    mkdir "${TEST_DIR}"/source
    mkdir "${TEST_DIR}"/source/another_level
    chown 1000:1000 "${TEST_DIR}"/source/another_level
    echo "foo" > "${TEST_DIR}"/source/foo
    echo "bar" > "${TEST_DIR}"/source/bar
    ln -s bar "${TEST_DIR}"/source/baz
    echo "pull" > "${TEST_DIR}"/pull_file

    incus storage volume file push "${TEST_DIR}"/pull_file "${pool}" vol1/pull_file
    incus storage volume file pull "${pool}" vol1/pull_file "${TEST_DIR}"/pull_test
    [ "$(cat "${TEST_DIR}"/pull_test)" = "pull" ]

    incus storage volume file push -p -r "${TEST_DIR}"/source "${pool}" vol1/tmp/ptest

    [ "$(incus exec filemanip --project=test -- stat -c "%u" /v1/tmp/ptest/source)" = "$(id -u)" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%g" /v1/tmp/ptest/source)" = "$(id -g)" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%u" /v1/tmp/ptest/source/another_level)" = "1000" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%g" /v1/tmp/ptest/source/another_level)" = "1000" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%a" /v1/tmp/ptest/source)" = "755" ]
    [ "$(incus exec filemanip --project=test -- readlink /v1/tmp/ptest/source/baz)" = "bar" ]

    incus storage volume file delete -f "${pool}" vol1/tmp/ptest/source

    # # This fails because the last command should have removed that directory.
    ! incus exec filemanip --project=test -- test -d /v1/tmp/ptest/source || false

    # Test storage volume file create.

    # Create a new empty file.
    incus storage volume file create "${pool}" vol1/tmp/create-test
    [ -z "$(incus exec filemanip --project=test -- cat /v1/tmp/create-test)" ]

    # This fails because the parent directory doesn't exist.
    ! incus storage volume file create "${pool}" vol1/tmp/create-test-dir/foo || false

    # Create foo along with the parent directory.
    incus storage volume file create --create-dirs "${pool}" vol1/tmp/create-test-dir/foo
    [ -z "$(incus exec filemanip --project=test -- cat /v1/tmp/create-test-dir/foo)" ]

    # Create directory using --type flag.
    incus storage volume file create --type=directory "${pool}" vol1/tmp/create-test-dir/sub-dir
    incus exec filemanip --project=test -- test -d /v1/tmp/create-test-dir/sub-dir

    # Create directory using trailing "/".
    incus storage volume file create "${pool}" vol1/tmp/create-test-dir/sub-dir-1/
    incus exec filemanip --project=test -- test -d /v1/tmp/create-test-dir/sub-dir-1

    # Create symlink.
    incus storage volume file create --type=symlink "${pool}" vol1/tmp/create-symlink foo
    [ "$(incus exec filemanip --project=test -- readlink /v1/tmp/create-symlink)" = "foo" ]

    incus storage volume detach "${pool}" vol1 filemanip
    incus delete filemanip -f

    rm -rf "${TEST_DIR}"/source
    rm -rf "${TEST_DIR}/dest"
    incus storage volume delete "${pool}" vol1
    incus project switch default
    incus project delete test
}
