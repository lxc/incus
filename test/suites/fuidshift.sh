test_common_fuidshift() {
    # test some bad arguments
    fail=0
    fuidshift /tmp -t b:0 > /dev/null 2>&1 && fail=1
    fuidshift /tmp -t x:0:0:0 > /dev/null 2>&1 && fail=1
    [ "${fail}" -ne 1 ]
}

test_nonroot_fuidshift() {
    test_common_fuidshift

    INCUS_FUIDMAP_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)

    u=$(id -u)
    g=$(id -g)
    u1=$((u + 1))
    g1=$((g + 1))

    touch "${INCUS_FUIDMAP_DIR}/x1"
    fuidshift "${INCUS_FUIDMAP_DIR}/x1" -t "u:${u}:100000:1" "g:${g}:100000:1" | tee /dev/stderr | grep "to 100000 100000" > /dev/null || fail=1
    if [ "${fail}" -eq 1 ]; then
        echo "==> Failed to shift own uid to container root"
        false
    fi
    fuidshift "${INCUS_FUIDMAP_DIR}/x1" -t "u:${u1}:10000:1" "g:${g1}:100000:1" | tee /dev/stderr | grep "to -1 -1" > /dev/null || fail=1
    if [ "${fail}" -eq 1 ]; then
        echo "==> Wrongly shifted invalid uid to container root"
        false
    fi

    # unshift it
    chown 100000:100000 "${INCUS_FUIDMAP_DIR}/x1"
    fuidshift "${INCUS_FUIDMAP_DIR}/x1" -r -t "u:${u}:100000:1" "g:${g}:100000:1" | tee /dev/stderr | grep "to 0 0" > /dev/null || fail=1
    if [ "${fail}" -eq 1 ]; then
        echo "==> Failed to unshift container root back to own uid"
        false
    fi
}

test_root_fuidshift() {
    test_nonroot_fuidshift

    # Todo - test ranges
}

test_fuidshift() {
    if ! command -v fuidshift > /dev/null 2>&1; then
        echo "==> SKIP: No fuidshift binary could be found"
        return
    fi

    if [ "$(id -u)" -ne 0 ]; then
        test_nonroot_fuidshift
    else
        test_root_fuidshift
    fi
}
