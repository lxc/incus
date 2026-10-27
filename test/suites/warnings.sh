test_warnings() {
    # Delete previous warnings
    incus query --wait /1.0/warnings\?recursion=1 | jq -r '.[].uuid' | xargs -n1 incus warning delete

    # Create a global warning (no node and no project)
    incus query --wait -X POST -d '{\"type_code\": 0, \"message\": \"global warning\"}' /internal/debug/warnings

    # More valid queries
    incus query --wait -X POST -d '{\"type_code\": 0, \"message\": \"global warning\", \"project\": \"default\"}' /internal/debug/warnings

    # Update the last warning. This will not create a new warning.
    incus query --wait -X POST -d '{\"type_code\": 0, \"message\": \"global warning 2\", \"project\": \"default\"}' /internal/debug/warnings

    # There should be two warnings now.
    count=$(incus query --wait /1.0/warnings | jq 'length')
    [ "${count}" -eq 2 ] || false

    count=$(incus query --wait /1.0/warnings\?recursion=1 | jq 'length')
    [ "${count}" -eq 2 ] || false

    # Invalid query (unknown project)
    ! incus query --wait -X POST -d '{\"type_code\": 0, \"message\": \"global warning\", \"project\": \"foo\"}' /internal/debug/warnings || false

    # Invalid query (unknown type code)
    ! incus query --wait -X POST -d '{\"type_code\": 999, \"message\": \"global warning\"}' /internal/debug/warnings || false

    # Both entity type code as entity ID need to be valid otherwise no warning will be created. Note that empty/null values are valid as well.
    ! incus query --wait -X POST -d '{\"type_code\": 0, \"message\": \"global warning\", \"entity_type_code\": 0, \"entity_id\": 0}' /internal/debug/warnings || false

    ensure_import_testimage

    # Get image ID from database instead of assuming it
    image_id=$(echo 'select image_id from images_aliases where name="testimage"' | incus admin sql global - | grep -Eo '[[:digit:]]+')

    # Create a warning with entity type "image" and entity ID ${image_id} (the imported testimage)
    incus query --wait -X POST -d "{\\\"type_code\\\": 0, \\\"message\\\": \\\"global warning\\\", \\\"entity_type_code\\\": 1, \\\"entity_id\\\": ${image_id}}" /internal/debug/warnings

    # There should be three warnings now.
    count=$(incus warning list --format json | jq 'length')
    [ "${count}" -eq 3 ] || false

    # Test filtering
    count=$(curl -G --unix-socket "$INCUS_DIR/unix.socket" "incus/1.0/warnings" --data-urlencode "recursion=0" --data-urlencode "filter=status eq new" | jq ".metadata | length")
    [ "${count}" -eq 3 ] || false

    count=$(curl -G --unix-socket "$INCUS_DIR/unix.socket" "incus/1.0/warnings" --data-urlencode "recursion=0" --data-urlencode "filter=status eq resolved" | jq ".metadata | length")
    [ "${count}" -eq 0 ] || false

    # Acknowledge a warning
    uuid=$(incus warning list --format json | jq -r '.[] | select(.last_message=="global warning 2") | .uuid')
    incus warning ack "${uuid}"

    # This should hide the acknowledged
    count=$(incus warning list --format json | jq 'length')
    [ "${count}" -eq 2 ] || false

    # ... unless one uses --all.
    count=$(incus warning list --all --format json | jq 'length')
    [ "${count}" -eq 3 ] || false

    incus warning show "${uuid}" | grep "global warning 2"

    # Delete warning
    incus warning rm "${uuid}"
    ! incus warning list | grep -q "${uuid}" || false
    ! incus warning show "${uuid}" || false
}

test_warnings_forced_stop() {
    ensure_import_testimage

    # A halt signal the busybox init ignores, so the clean shutdown always times out.
    incus launch testimage wfs -c boot.host_shutdown_timeout=1 -c raw.lxc=lxc.signal.halt=SIGXCPU

    shutdown_incus "${INCUS_DIR}"
    respawn_incus "${INCUS_DIR}" true

    # The forced stop is recorded and survives the restart.
    incus warning list --format json | jq -e '.[] | select(.type == "Instance forcefully stopped" and .entity_url == "/1.0/instances/wfs" and .status == "new") | .last_message | contains("server shutdown")'

    # The warning follows a rename.
    incus stop -f wfs
    incus rename wfs wfs2
    incus warning list --format json | jq -e '.[] | select(.type == "Instance forcefully stopped" and .entity_url == "/1.0/instances/wfs2")'

    # A clean shutdown resolves it.
    incus config unset wfs2 raw.lxc
    incus config set wfs2 boot.host_shutdown_timeout=10
    incus start wfs2

    shutdown_incus "${INCUS_DIR}"
    respawn_incus "${INCUS_DIR}" true

    incus warning list --all --format json | jq -e '.[] | select(.type == "Instance forcefully stopped" and .entity_url == "/1.0/instances/wfs2" and .status == "resolved")'

    # Deleting the instance removes it.
    incus delete -f wfs2
    [ "$(incus warning list --all --format json | jq '[.[] | select(.type == "Instance forcefully stopped")] | length')" -eq 0 ]
}
