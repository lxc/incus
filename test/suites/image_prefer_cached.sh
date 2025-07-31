# In case a cached image matching the desired alias is present, that
# one is preferred, even if the its remote has a more recent one.
test_image_prefer_cached() {

    if incus image alias list | grep -q "^| testimage\\s*|.*$"; then
        incus image delete testimage
    fi

    # shellcheck disable=2039,3043
    local INCUS2_DIR INCUS2_ADDR
    INCUS2_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${INCUS2_DIR}"
    spawn_incus "${INCUS2_DIR}" true
    INCUS2_ADDR=$(cat "${INCUS2_DIR}/incus.addr")

    (INCUS_DIR=${INCUS2_DIR} deps/import-busybox --alias testimage --public)
    fp1="$(INCUS_DIR=${INCUS2_DIR} incus image info testimage | awk '/^Fingerprint/ {print $2}')"

    token="$(INCUS_DIR=${INCUS2_DIR} incus config trust add foo -q)"
    incus remote add l2 "${INCUS2_ADDR}" --accept-certificate --token "${token}"
    incus init l2:testimage c1

    # Now the first image image is in the local store, since it was
    # downloaded to create c1.
    alias="$(incus image info "${fp1}" | awk '{if ($1 == "Alias:") {print $2}}')"
    [ "${alias}" = "testimage" ]

    # Delete the first image from the remote store and replace it with a
    # new one with a different fingerprint (passing "--template create"
    # will do that).
    (INCUS_DIR=${INCUS2_DIR} incus image delete testimage)
    (INCUS_DIR=${INCUS2_DIR} deps/import-busybox --alias testimage --public --template create)
    fp2="$(INCUS_DIR=${INCUS2_DIR} incus image info testimage | awk '/^Fingerprint/ {print $2}')"
    [ "${fp1}" != "${fp2}" ]

    # At this point starting a new container from "testimage" should not
    # result in the new image being downloaded.
    incus init l2:testimage c2
    if incus image info "${fp2}"; then
        echo "The second image ${fp2} was downloaded and the cached one not used"
        return 1
    fi

    incus delete c1
    incus delete c2
    incus remote remove l2
    incus image delete "${fp1}"

    kill_incus "$INCUS2_DIR"
}
