volfile_check_noderef() {
    incus storage volume file pull "$1" "$2" "vol1$3" "${TEST_DIR}/tmpfile" --project=test
    [ -h "${TEST_DIR}/tmpfile" ]
    [ "$(readlink "${TEST_DIR}/tmpfile")" = "$4" ]
    rm "${TEST_DIR}/tmpfile"
}

volfile_check_deref() {
    incus storage volume file pull "$1" "$2" "vol1$3" "${TEST_DIR}/tmpfile" --project=test
    [ ! -h "${TEST_DIR}/tmpfile" ]
    [ "$(cat "${TEST_DIR}/tmpfile")" = "$4" ]
    rm "${TEST_DIR}/tmpfile"
}

volfile_check_noderef_dir() {
    incus storage volume file pull "$1" "$2" "vol1$3" "${TEST_DIR}/tmpdir" --project=test
    [ -h "${TEST_DIR}/tmpdir/$4" ]
    [ "$(readlink "${TEST_DIR}/tmpdir/$4")" = "$5" ]
    rm -rf "${TEST_DIR}/tmpdir"
}

volfile_check_deref_dir() {
    incus storage volume file pull "$1" "$2" "vol1$3" "${TEST_DIR}/tmpdir" --project=test
    [ ! -h "${TEST_DIR}/tmpdir/$4" ]
    [ "$(cat "${TEST_DIR}/tmpdir/$4")" = "$5" ]
    rm -rf "${TEST_DIR}/tmpdir"
}

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
    incus storage volume attach "${pool}" vol1 filemanip vol1 /v1

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


    # Test all sorts of option combinations for `incus file pull`.

    echo barqux | incus storage volume file push - "${pool}" vol1/tmp/foo --project=test
    [ "$(incus storage volume file pull "${pool}" vol1/tmp/foo - --project=test)" = "barqux" ]

    # Create a directory and play with our options.
    incus storage volume file create --type=directory "${pool}" vol1/tmp/bar --project=test
    incus storage volume file create --type=symlink "${pool}" vol1/tmp/bar/baz /tmp/foo --project=test
    # -r doesn’t dereference.
    volfile_check_noderef_dir -r "${pool}" /tmp/bar baz /tmp/foo
    # -rP doesn’t dereference.
    volfile_check_noderef_dir -rP "${pool}" /tmp/bar baz /tmp/foo
    # -rH doesn’t dereference.
    volfile_check_noderef_dir -rH "${pool}" /tmp/bar baz /tmp/foo
    # -rL does dereference.
    volfile_check_deref_dir -rL "${pool}" /tmp/bar baz barqux
    incus storage volume file delete -f "${pool}" vol1/tmp/bar --project=test

    # Create a symlink and play with our options.
    incus storage volume file create --type=symlink "${pool}" vol1/tmp/bar /tmp/foo --project=test
    # Pulling to stdout must dereference the symlink...
    [ "$(incus storage volume file pull "${pool}" vol1/tmp/bar - --project=test)" = "barqux" ]
    # ... even if we passed -r.
    [ "$(incus storage volume file pull -r "${pool}" vol1/tmp/bar - --project=test)" = "barqux" ]
    # -r doesn’t dereference.
    volfile_check_noderef -r "${pool}" /tmp/bar /tmp/foo
    # -P doesn’t dereference.
    volfile_check_noderef -P "${pool}" /tmp/bar /tmp/foo
    # -H does dereference.
    volfile_check_deref -H "${pool}" /tmp/bar barqux
    # -L does dereference.
    volfile_check_deref -L "${pool}" /tmp/bar barqux
    incus storage volume file delete "${pool}" vol1/tmp/bar --project=test

    # Create a symlink to a directory and play with our options.
    incus storage volume file create --type=directory "${pool}" vol1/tmp/bar --project=test
    incus storage volume file create --type=symlink "${pool}" vol1/tmp/bar/baz /tmp/foo --project=test
    incus storage volume file create --type=symlink "${pool}" vol1/tmp/qux /tmp/bar --project=test
    # -r doesn’t dereference...
    volfile_check_noderef -r "${pool}" /tmp/qux /tmp/bar
    # ... except if we add a trailing `/`, in which case the first level is followed.
    volfile_check_noderef_dir -r "${pool}" /tmp/qux/ baz /tmp/foo
    # -P doesn’t dereference.
    volfile_check_noderef -P "${pool}" /tmp/qux /tmp/bar
    # -rP doesn’t dereference...
    volfile_check_noderef -rP "${pool}" /tmp/qux /tmp/bar
    # ... except if we add a trailing `/`, in which case the first level is followed.
    volfile_check_noderef_dir -rP "${pool}" /tmp/qux/ baz /tmp/foo
    # -rH does dereference the first level...
    volfile_check_noderef_dir -rH "${pool}" /tmp/qux baz /tmp/foo
    # ... and so does adding a trailing `/`.
    volfile_check_noderef_dir -rH "${pool}" /tmp/qux/ baz /tmp/foo
    # -rL does dereference all levels...
    volfile_check_deref_dir -rL "${pool}" /tmp/qux baz barqux
    # ... and so does adding a trailing `/`.
    volfile_check_deref_dir -rL "${pool}" /tmp/qux/ baz barqux


    # Test SFTP functionality.

    ! incus storage volume file mount "${pool}" doesnotexist || false
    ! incus storage volume file mount doesnotexist vol1 || false

    incus storage volume file mount "${pool}" vol1 --listen=127.0.0.1:2022 --no-auth &
    mountPID=$!
    sleep 1

    output=$(curl -s -S --insecure sftp://127.0.0.1:2022/pull_file || true)
    kill -9 ${mountPID}
    incus storage volume detach "${pool}" vol1 filemanip
    incus delete filemanip -f
    [ "$output" = "pull" ]

    rm -rf "${TEST_DIR}"/source
    rm -rf "${TEST_DIR}/dest"
    incus storage volume delete "${pool}" vol1
    incus project switch default
    incus project delete test
}
