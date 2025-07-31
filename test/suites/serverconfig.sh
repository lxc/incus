test_server_config() {
    INCUS_SERVERCONFIG_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    spawn_incus "${INCUS_SERVERCONFIG_DIR}" true
    ensure_has_localhost_remote "${INCUS_ADDR}"

    test_server_config_access
    test_server_config_storage

    kill_incus "${INCUS_SERVERCONFIG_DIR}"
}

test_server_config_access() {
    # test untrusted server GET
    my_curl -X GET "https://$(cat "${INCUS_SERVERCONFIG_DIR}/incus.addr")/1.0" | grep -v -q environment

    # test authentication type
    curl --unix-socket "$INCUS_DIR/unix.socket" "incus/1.0" | jq .metadata.auth_methods | grep tls

    # test fetch metadata validation.
    [ "$(curl --silent --unix-socket "$INCUS_DIR/unix.socket" -w "%{http_code}" -o /dev/null -H 'Sec-Fetch-Site: same-origin' "incus/1.0")" = "200" ]
    [ "$(curl --silent --unix-socket "$INCUS_DIR/unix.socket" -w "%{http_code}" -o /dev/null -H 'Sec-Fetch-Site: cross-site' "incus/1.0")" = "403" ]
    [ "$(curl --silent --unix-socket "$INCUS_DIR/unix.socket" -w "%{http_code}" -o /dev/null -H 'Sec-Fetch-Site: same-site' "incus/1.0")" = "403" ]
}

test_server_config_storage() {
    # shellcheck disable=2039,3043
    local incus_backend

    incus_backend=$(storage_backend "$INCUS_DIR")
    if [ "$incus_backend" = "ceph" ]; then
        return
    fi

    ensure_import_testimage
    pool=$(incus profile device get default root pool)

    incus init testimage foo
    incus query --wait /1.0/instances/foo/backups -X POST -d '{\"expires_at\": \"2100-01-01T10:00:00-05:00\"}'

    # Record before
    BACKUPS_BEFORE=$(find "${INCUS_DIR}/backups/" | sort)
    IMAGES_BEFORE=$(find "${INCUS_DIR}/images/" | sort)

    incus storage volume create "${pool}" backups
    incus storage volume create "${pool}" images

    # Validate errors
    ! incus config set storage.backups_volume foo/bar
    ! incus config set storage.images_volume foo/bar
    ! incus config set storage.backups_volume "${pool}/bar"
    ! incus config set storage.images_volume "${pool}/bar"

    incus storage volume snapshot create "${pool}" backups
    incus storage volume snapshot create "${pool}" images
    ! incus config set storage.backups_volume "${pool}/backups"
    ! incus config set storage.images_volume "${pool}/images"

    incus storage volume snapshot delete "${pool}" backups snap0
    incus storage volume snapshot delete "${pool}" images snap0

    # Set the configuration
    incus config set storage.backups_volume "${pool}/backups"
    incus config set storage.images_volume "${pool}/images"

    # Record after
    BACKUPS_AFTER=$(find "${INCUS_DIR}/backups/" | sort)
    IMAGES_AFTER=$(find "${INCUS_DIR}/images/" | sort)

    # Validate content
    if [ "${BACKUPS_BEFORE}" != "${BACKUPS_AFTER}" ]; then
        echo "Backups dir content mismatch"
        false
    fi

    if [ "${IMAGES_BEFORE}" != "${IMAGES_AFTER}" ]; then
        echo "Images dir content mismatch"
        false
    fi

    # Validate more errors
    ! incus storage volume delete "${pool}" backups
    ! incus storage volume delete "${pool}" images
    ! incus storage volume rename "${pool}" backups backups1
    ! incus storage volume rename "${pool}" images images1
    ! incus storage volume snapshot create "${pool}" backups
    ! incus storage volume snapshot create "${pool}" images

    # Modify container and publish to image on custom volume.
    incus start foo
    incus exec foo -- touch /root/foo
    incus stop -f foo
    incus publish foo --alias fooimage

    # Launch container from published image on custom volume.
    incus init fooimage foo2
    incus delete -f foo2
    incus image delete fooimage

    # Reset and cleanup
    incus config unset storage.backups_volume
    incus config unset storage.images_volume
    incus storage volume delete "${pool}" backups
    incus storage volume delete "${pool}" images
    incus delete -f foo
}
