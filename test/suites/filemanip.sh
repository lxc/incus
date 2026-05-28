file_check_pull_noderef() {
    incus file pull "$1" "filemanip$2" "${TEST_DIR}/tmpfile" --project=test
    [ -h "${TEST_DIR}/tmpfile" ]
    [ "$(readlink "${TEST_DIR}/tmpfile")" = "$3" ]
    rm "${TEST_DIR}/tmpfile"
}

file_check_push_noderef() {
    incus file push "$1" "${TEST_DIR}$2" filemanip/tmpfile --project=test
    incus exec filemanip --project=test -- [ -h /tmpfile ]
    [ "$(incus exec filemanip readlink /tmpfile --project=test)" = "${TEST_DIR}$3" ]
    incus file delete filemanip/tmpfile --project=test
}

file_check_pull_deref() {
    incus file pull "$1" "filemanip$2" "${TEST_DIR}/tmpfile" --project=test
    [ ! -h "${TEST_DIR}/tmpfile" ]
    [ "$(cat "${TEST_DIR}/tmpfile")" = "$3" ]
    rm "${TEST_DIR}/tmpfile"
}

file_check_push_deref() {
    incus file push "$1" "${TEST_DIR}$2" filemanip/tmpfile --project=test
    incus exec filemanip --project=test -- [ ! -h /tmpfile ]
    [ "$(incus file pull filemanip/tmpfile - --project=test)" = "$3" ]
    incus file delete filemanip/tmpfile --project=test
}

file_check_pull_noderef_dir() {
    incus file pull "$1" "filemanip$2" "${TEST_DIR}/tmpdir" --project=test
    [ -h "${TEST_DIR}/tmpdir/$3" ]
    [ "$(readlink "${TEST_DIR}/tmpdir/$3")" = "$4" ]
    rm -rf "${TEST_DIR}/tmpdir"
}

file_check_push_noderef_dir() {
    incus file push "$1" "${TEST_DIR}$2" filemanip/tmpdir --project=test
    incus exec filemanip --project=test -- [ -h "/tmpdir/$3" ]
    [ "$(incus exec filemanip readlink "/tmpdir/$3" --project=test)" = "${TEST_DIR}$4" ]
    incus file delete -f filemanip/tmpdir --project=test
}

file_check_pull_deref_dir() {
    incus file pull "$1" "filemanip$2" "${TEST_DIR}/tmpdir" --project=test
    [ ! -h "${TEST_DIR}/tmpdir/$3" ]
    [ "$(cat "${TEST_DIR}/tmpdir/$3")" = "$4" ]
    rm -rf "${TEST_DIR}/tmpdir"
}

file_check_push_deref_dir() {
    incus file push "$1" "${TEST_DIR}$2" filemanip/tmpdir --project=test
    incus exec filemanip --project=test -- [ ! -h "/tmpdir/$3" ]
    [ "$(incus file pull "filemanip/tmpdir/$3" - --project=test)" = "$4" ]
    incus file delete -f filemanip/tmpdir --project=test
}

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

    incus file push -p -r "${TEST_DIR}"/source filemanip/tmp/ptest/source

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

    [ "$(incus exec filemanip --project=test -- stat -c "%a" /tmp/ptest/d1)" = "755" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%a" /tmp/ptest/d1/d2)" = "755" ]

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

    # Special case where we are overriding combinations of UID/GID and mode, for both recursive and
    # non-recursive operations.

    incus file push -r --uid=1234 --gid=5678 source filemanip/tmp/ptest
    [ "$(incus exec filemanip --project=test -- stat -c "%u" /tmp/ptest/source)" = "1234" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%g" /tmp/ptest/source)" = "5678" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%a" /tmp/ptest/source)" = "755" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%u" /tmp/ptest/source/foo)" = "1234" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%g" /tmp/ptest/source/foo)" = "5678" ]
    # The mode is NOT applied recursively.
    [ "$(incus exec filemanip --project=test -- stat -c "%a" /tmp/ptest/source/foo)" = "644" ]
    incus exec filemanip --project=test -- rm -rf /tmp/ptest/source

    incus file push -r --mode=751 source filemanip/tmp/ptest
    [ "$(incus exec filemanip --project=test -- stat -c "%u" /tmp/ptest/source)" = "$(id -u)" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%g" /tmp/ptest/source)" = "$(id -g)" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%a" /tmp/ptest/source)" = "751" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%u" /tmp/ptest/source/foo)" = "$(id -u)" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%g" /tmp/ptest/source/foo)" = "$(id -g)" ]
    # The mode is NOT applied recursively.
    [ "$(incus exec filemanip --project=test -- stat -c "%a" /tmp/ptest/source/foo)" = "644" ]
    incus exec filemanip --project=test -- rm -rf /tmp/ptest/source

    incus file push -r --uid=1234 --gid=5678 --mode=751 source filemanip/tmp/ptest
    [ "$(incus exec filemanip --project=test -- stat -c "%u" /tmp/ptest/source)" = "1234" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%g" /tmp/ptest/source)" = "5678" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%a" /tmp/ptest/source)" = "751" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%u" /tmp/ptest/source/foo)" = "1234" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%g" /tmp/ptest/source/foo)" = "5678" ]
    # The mode is NOT applied recursively.
    [ "$(incus exec filemanip --project=test -- stat -c "%a" /tmp/ptest/source/foo)" = "644" ]
    incus exec filemanip --project=test -- rm -rf /tmp/ptest/source

    incus file push -pr --uid=1234 --gid=5678 source/foo filemanip/tmp/ptest/source/
    [ "$(incus exec filemanip --project=test -- stat -c "%u" /tmp/ptest/source)" = "1234" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%g" /tmp/ptest/source)" = "5678" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%a" /tmp/ptest/source)" = "755" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%u" /tmp/ptest/source/foo)" = "1234" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%g" /tmp/ptest/source/foo)" = "5678" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%a" /tmp/ptest/source/foo)" = "644" ]
    incus exec filemanip --project=test -- rm -rf /tmp/ptest/source

    incus file push -pr --mode=751 source/foo filemanip/tmp/ptest/source/
    [ "$(incus exec filemanip --project=test -- stat -c "%u" /tmp/ptest/source)" = "0" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%g" /tmp/ptest/source)" = "0" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%a" /tmp/ptest/source)" = "755" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%u" /tmp/ptest/source/foo)" = "$(id -u)" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%g" /tmp/ptest/source/foo)" = "$(id -g)" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%a" /tmp/ptest/source/foo)" = "751" ]
    incus exec filemanip --project=test -- rm -rf /tmp/ptest/source

    incus file push -pr --uid=1234 --gid=5678 --mode=751 source/foo filemanip/tmp/ptest/source/
    [ "$(incus exec filemanip --project=test -- stat -c "%u" /tmp/ptest/source)" = "1234" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%g" /tmp/ptest/source)" = "5678" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%a" /tmp/ptest/source)" = "755" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%u" /tmp/ptest/source/foo)" = "1234" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%g" /tmp/ptest/source/foo)" = "5678" ]
    [ "$(incus exec filemanip --project=test -- stat -c "%a" /tmp/ptest/source/foo)" = "751" ]
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


    # Test all sorts of option combinations for `incus file pull`.

    echo barqux | incus file push - filemanip/tmp/foo --project=test
    [ "$(incus file pull filemanip/tmp/foo - --project=test)" = "barqux" ]

    # Create a directory and play with our options.
    incus file create --type=directory filemanip/tmp/bar --project=test
    incus file create --type=symlink filemanip/tmp/bar/baz /tmp/foo --project=test
    # -r doesn’t dereference.
    file_check_pull_noderef_dir -r /tmp/bar baz /tmp/foo
    # -rP doesn’t dereference.
    file_check_pull_noderef_dir -rP /tmp/bar baz /tmp/foo
    # -rH doesn’t dereference.
    file_check_pull_noderef_dir -rH /tmp/bar baz /tmp/foo
    # -rL does dereference.
    file_check_pull_deref_dir -rL /tmp/bar baz barqux
    incus file delete -f filemanip/tmp/bar --project=test

    # Create a symlink and play with our options.
    incus file create --type=symlink filemanip/tmp/bar /tmp/foo --project=test
    # Pulling to stdout must dereference the symlink...
    [ "$(incus file pull filemanip/tmp/bar - --project=test)" = "barqux" ]
    # ... even if we passed -r.
    [ "$(incus file pull -r filemanip/tmp/bar - --project=test)" = "barqux" ]
    # -r doesn’t dereference.
    file_check_pull_noderef -r /tmp/bar /tmp/foo
    # -P doesn’t dereference.
    file_check_pull_noderef -P /tmp/bar /tmp/foo
    # -H does dereference.
    file_check_pull_deref -H /tmp/bar barqux
    # -L does dereference.
    file_check_pull_deref -L /tmp/bar barqux
    incus file delete filemanip/tmp/bar --project=test

    # Create a symlink to a directory and play with our options.
    incus file create --type=directory filemanip/tmp/bar --project=test
    incus file create --type=symlink filemanip/tmp/bar/baz /tmp/foo --project=test
    incus file create --type=symlink filemanip/tmp/qux /tmp/bar --project=test
    # -r doesn’t dereference...
    file_check_pull_noderef -r /tmp/qux /tmp/bar
    # ... except if we add a trailing `/`, in which case the first level is followed.
    file_check_pull_noderef_dir -r /tmp/qux/ baz /tmp/foo
    # -P doesn’t dereference.
    file_check_pull_noderef -P /tmp/qux /tmp/bar
    # -rP doesn’t dereference...
    file_check_pull_noderef -rP /tmp/qux /tmp/bar
    # ... except if we add a trailing `/`, in which case the first level is followed.
    file_check_pull_noderef_dir -rP /tmp/qux/ baz /tmp/foo
    # -rH does dereference the first level...
    file_check_pull_noderef_dir -rH /tmp/qux baz /tmp/foo
    # ... and so does adding a trailing `/`.
    file_check_pull_noderef_dir -rH /tmp/qux/ baz /tmp/foo
    # -rL does dereference all levels...
    file_check_pull_deref_dir -rL /tmp/qux baz barqux
    # ... and so does adding a trailing `/`.
    file_check_pull_deref_dir -rL /tmp/qux/ baz barqux

    # Asking to pull something ending with `/` which it not a directory leads to an error.
    ! incus file pull filemanip/tmp/foo/ - --project=test || false


    # Test all sorts of option combinations for `incus file push`.

    mkdir "${TEST_DIR}/tmp"
    echo barqux > "${TEST_DIR}/tmp/foo"

    # Create a directory and play with our options.
    mkdir "${TEST_DIR}/tmp/bar"
    ln -s "${TEST_DIR}/tmp/foo" "${TEST_DIR}/tmp/bar/baz"
    # -r doesn’t dereference.
    file_check_push_noderef_dir -r /tmp/bar baz /tmp/foo
    # -rP doesn’t dereference.
    file_check_push_noderef_dir -rP /tmp/bar baz /tmp/foo
    # -rH doesn’t dereference.
    file_check_push_noderef_dir -rH /tmp/bar baz /tmp/foo
    # -rL does dereference.
    file_check_push_deref_dir -rL /tmp/bar baz barqux
    rm -rf "${TEST_DIR}/tmp/bar"

    # Create a symlink and play with our options.
    ln -s "${TEST_DIR}/tmp/foo" "${TEST_DIR}/tmp/bar"
    # -r doesn’t dereference.
    file_check_push_noderef -r /tmp/bar /tmp/foo
    # -P doesn’t dereference.
    file_check_push_noderef -P /tmp/bar /tmp/foo
    # -H does dereference.
    file_check_push_deref -H /tmp/bar barqux
    # -L does dereference.
    file_check_push_deref -L /tmp/bar barqux
    rm -rf "${TEST_DIR}/tmp/bar"

    # Create a symlink to a directory and play with our options.
    mkdir "${TEST_DIR}/tmp/bar"
    ln -s "${TEST_DIR}/tmp/foo" "${TEST_DIR}/tmp/bar/baz"
    ln -s "${TEST_DIR}/tmp/bar" "${TEST_DIR}/tmp/qux"
    # -r doesn’t dereference...
    file_check_push_noderef -r /tmp/qux /tmp/bar
    # ... except if we add a trailing `/`, in which case the first level is followed.
    file_check_push_noderef_dir -r /tmp/qux/ baz /tmp/foo
    # -P doesn’t dereference.
    file_check_push_noderef -P /tmp/qux /tmp/bar
    # -rP doesn’t dereference...
    file_check_push_noderef -rP /tmp/qux /tmp/bar
    # ... except if we add a trailing `/`, in which case the first level is followed.
    file_check_push_noderef_dir -rP /tmp/qux/ baz /tmp/foo
    # -rH does dereference the first level...
    file_check_push_noderef_dir -rH /tmp/qux baz /tmp/foo
    # ... and so does adding a trailing `/`.
    file_check_push_noderef_dir -rH /tmp/qux/ baz /tmp/foo
    # -rL does dereference all levels...
    file_check_push_deref_dir -rL /tmp/qux baz barqux
    # ... and so does adding a trailing `/`.
    file_check_push_deref_dir -rL /tmp/qux/ baz barqux


    # Test consecutive pulls.

    rm -rf "${TEST_DIR}/tmp/foo"
    mkdir "${TEST_DIR}/tmp/foo"
    incus file delete -f filemanip/tmp/bar --project=test
    incus file create --type=directory filemanip/tmp/bar --project=test
    incus file create --type=directory filemanip/tmp/baz --project=test
    incus file create filemanip/tmp/bar/one --project=test
    incus file create filemanip/tmp/bar/two --project=test
    echo xxx | incus file push - filemanip/tmp/baz/one --project=test

    # One dotted, one non-dotted, no overlap.
    incus file pull -r filemanip/tmp/bar/. filemanip/tmp/baz/ "${TEST_DIR}/tmp/foo" --project=test
    [ -f "${TEST_DIR}/tmp/foo/one" ]
    [ -f "${TEST_DIR}/tmp/foo/two" ]
    [ -f "${TEST_DIR}/tmp/foo/baz/one" ]

    # Two dotted, overlapping.
    rm -rf "${TEST_DIR}/tmp/foo"
    mkdir "${TEST_DIR}/tmp/foo"
    incus file pull -r filemanip/tmp/bar/. filemanip/tmp/baz/. "${TEST_DIR}/tmp/foo" --project=test
    [ -f "${TEST_DIR}/tmp/foo/one" ]
    [ -f "${TEST_DIR}/tmp/foo/two" ]
    [ "$(cat "${TEST_DIR}/tmp/foo/one")" = xxx ]

    # File/directory overlap.
    rm -rf "${TEST_DIR}/tmp/foo"
    mkdir "${TEST_DIR}/tmp/foo"
    incus file delete -f filemanip/tmp/bar --project=test
    incus file create -p --type=directory filemanip/tmp/bar/one --project=test
    ! incus file pull -r filemanip/tmp/baz/. filemanip/tmp/bar/. "${TEST_DIR}/tmp/foo" --project=test || false
    # To mimic cp, the operation must have a leftover file.
    [ -f "${TEST_DIR}/tmp/foo/one" ]

    # Directory merging.
    rm -rf "${TEST_DIR}/tmp/foo"
    mkdir "${TEST_DIR}/tmp/foo"
    incus file delete -f filemanip/tmp/baz --project=test
    incus file create -p --type=directory filemanip/tmp/baz/one --project=test
    incus file create filemanip/tmp/bar/one/foo --project=test
    incus file create filemanip/tmp/bar/one/bar --project=test
    echo xxx | incus file push - filemanip/tmp/baz/one/foo --project=test
    incus file pull -r filemanip/tmp/bar/. filemanip/tmp/baz/. "${TEST_DIR}/tmp/foo" --project=test
    [ -f "${TEST_DIR}/tmp/foo/one/foo" ]
    [ -f "${TEST_DIR}/tmp/foo/one/bar" ]
    [ "$(cat "${TEST_DIR}/tmp/foo/one/foo")" = xxx ]

    # Directory stacking.
    rm -rf "${TEST_DIR}/tmp/foo"
    mkdir "${TEST_DIR}/tmp/foo"
    incus file delete -f filemanip/tmp/baz --project=test
    incus file pull -r filemanip/tmp/bar/one "${TEST_DIR}/tmp/foo/one" --project=test
    incus file pull -r filemanip/tmp/bar/one "${TEST_DIR}/tmp/foo/one" --project=test
    [ -f "${TEST_DIR}/tmp/foo/one/foo" ]
    [ -f "${TEST_DIR}/tmp/foo/one/bar" ]
    [ -f "${TEST_DIR}/tmp/foo/one/one/foo" ]
    [ -f "${TEST_DIR}/tmp/foo/one/one/bar" ]


    # Test consecutive pushes.

    incus file delete filemanip/tmp/foo --project=test
    rm -rf "${TEST_DIR}/tmp/bar"
    incus file create --type=directory filemanip/tmp/foo --project=test
    mkdir "${TEST_DIR}/tmp/bar" "${TEST_DIR}/tmp/baz"
    touch "${TEST_DIR}/tmp/bar/one" "${TEST_DIR}/tmp/bar/two"
    echo xxx > "${TEST_DIR}/tmp/baz/one"

    # One dotted, one non-dotted, no overlap.
    incus file push -r "${TEST_DIR}/tmp/bar/." "${TEST_DIR}/tmp/baz/" filemanip/tmp/foo --project=test
    incus exec filemanip --project=test -- [ -f /tmp/foo/one ]
    incus exec filemanip --project=test -- [ -f /tmp/foo/two ]
    incus exec filemanip --project=test -- [ -f /tmp/foo/baz/one ]

    # Two dotted, overlapping.
    incus file delete -f filemanip/tmp/foo --project=test
    incus file create --type=directory filemanip/tmp/foo --project=test
    incus file push -r "${TEST_DIR}/tmp/bar/." "${TEST_DIR}/tmp/baz/." filemanip/tmp/foo --project=test
    incus exec filemanip --project=test -- [ -f /tmp/foo/one ]
    incus exec filemanip --project=test -- [ -f /tmp/foo/two ]
    [ "$(incus file pull filemanip/tmp/foo/one - --project=test)" = xxx ]

    # File/directory overlap.
    incus file delete -f filemanip/tmp/foo --project=test
    incus file create --type=directory filemanip/tmp/foo --project=test
    rm -rf "${TEST_DIR}/tmp/bar"
    mkdir -p "${TEST_DIR}/tmp/bar/one"
    ! incus file push -r "${TEST_DIR}/tmp/baz/." "${TEST_DIR}/tmp/bar/." filemanip/tmp/foo --project=test || false
    # To mimic cp, the operation must have a leftover file.
    incus exec filemanip --project=test -- [ -f /tmp/foo/one ]

    # Directory merging.
    incus file delete -f filemanip/tmp/foo --project=test
    incus file create --type=directory filemanip/tmp/foo --project=test
    rm -rf "${TEST_DIR}/tmp/baz"
    mkdir -p "${TEST_DIR}/tmp/baz/one"
    touch "${TEST_DIR}/tmp/bar/one/foo" "${TEST_DIR}/tmp/bar/one/bar"
    echo xxx > "${TEST_DIR}/tmp/baz/one/foo"
    incus file push -r "${TEST_DIR}/tmp/bar/." "${TEST_DIR}/tmp/baz/." filemanip/tmp/foo --project=test
    incus exec filemanip --project=test -- [ -f /tmp/foo/one/foo ]
    incus exec filemanip --project=test -- [ -f /tmp/foo/one/bar ]
    [ "$(incus file pull filemanip/tmp/foo/one/foo - --project=test)" = xxx ]

    # Directory stacking.
    incus file delete -f filemanip/tmp/foo --project=test
    incus file create --type=directory filemanip/tmp/foo --project=test
    rm -rf "${TEST_DIR}/tmp/baz"
    incus file push -r "${TEST_DIR}/tmp/bar/one" filemanip/tmp/foo/one --project=test
    incus file push -r "${TEST_DIR}/tmp/bar/one" filemanip/tmp/foo/one --project=test
    incus exec filemanip --project=test -- [ -f /tmp/foo/one/foo ]
    incus exec filemanip --project=test -- [ -f /tmp/foo/one/bar ]
    incus exec filemanip --project=test -- [ -f /tmp/foo/one/one/foo ]
    incus exec filemanip --project=test -- [ -f /tmp/foo/one/one/bar ]

    # Test SFTP functionality.
    cmd=$(
        unset -f incus
        command -v incus
    )

    ! incus file mount doesnotexist || false

    $cmd file mount filemanip --listen=127.0.0.1:2022 --no-auth &
    mountPID=$!
    sleep 1

    output=$(curl -s -S --insecure sftp://127.0.0.1:2022/foo || true)
    kill -9 ${mountPID}
    incus delete filemanip -f
    [ "$output" = "foo" ]

    rm -rf "${TEST_DIR}/source"
    rm -rf "${TEST_DIR}/dest"
    rm -rf "${TEST_DIR}/tmp"
    incus project switch default
    incus project delete test
}
