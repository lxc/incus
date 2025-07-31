# Test setup helper functions.

ensure_has_localhost_remote() {
    # shellcheck disable=SC2039,3043
    local addr="${1}"
    if ! incus remote list | grep -q "localhost"; then
        token="$(incus config trust add foo -q)"
        incus remote add localhost "https://${addr}" --accept-certificate --token "${token}"
    fi
}

ensure_import_testimage() {
    if ! incus image alias list | grep -q "^| testimage\\s*|.*$"; then
        if [ -e "${INCUS_TEST_IMAGE:-}" ]; then
            incus image import "${INCUS_TEST_IMAGE}" --alias testimage
        else
            if [ ! -e "/bin/busybox" ]; then
                echo "Please install busybox (busybox-static) or set INCUS_TEST_IMAGE"
                exit 1
            fi

            if ldd /bin/busybox > /dev/null 2>&1; then
                echo "The testsuite requires /bin/busybox to be a static binary"
                exit 1
            fi

            project="$(incus project list | awk '/(current)/ {print $2}')"
            deps/import-busybox --alias testimage --project "$project"
        fi
    fi
}
