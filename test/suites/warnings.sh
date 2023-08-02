test_warnings() {
    # Delete previous warnings
    lxc query --wait /1.0/warnings\?recursion=1 | jq -r '.[].uuid' | xargs -n1 lxc warning delete

    # Create a global warning (no node and no project)
    lxc query --wait -X POST -d '{\"type_code\": 0, \"message\": \"global warning\"}' /internal/testing/warnings

    # More valid queries
    lxc query --wait -X POST -d '{\"type_code\": 0, \"message\": \"global warning\", \"project\": \"default\"}' /internal/testing/warnings

    # Update the last warning. This will not create a new warning.
    lxc query --wait -X POST -d '{\"type_code\": 0, \"message\": \"global warning 2\", \"project\": \"default\"}' /internal/testing/warnings

    # There should be two warnings now.
    count=$(lxc query --wait /1.0/warnings | jq 'length')
    [ "${count}" -eq 2 ] || false

    count=$(lxc query --wait /1.0/warnings\?recursion=1 | jq 'length')
    [ "${count}" -eq 2 ] || false

    # Invalid query (unknown project)
    ! lxc query --wait -X POST -d '{\"type_code\": 0, \"message\": \"global warning\", \"project\": \"foo\"}' /internal/testing/warnings || false

    # Invalid query (unknown type code)
    ! lxc query --wait -X POST -d '{\"type_code\": 999, \"message\": \"global warning\"}' /internal/testing/warnings || false

    # Both entity type code as entity ID need to be valid otherwise no warning will be created. Note that empty/null values are valid as well.
    ! lxc query --wait -X POST -d '{\"type_code\": 0, \"message\": \"global warning\", \"entity_type_code\": 0, \"entity_id\": 0}' /internal/testing/warnings || false

    ensure_import_testimage

    # Get image ID from database instead of assuming it
    image_id=$(lxd sql global 'select image_id from images_aliases where name="testimage"' | grep -Eo '[[:digit:]]+')

    # Create a warning with entity type "image" and entity ID ${image_id} (the imported testimage)
    lxc query --wait -X POST -d "{\\\"type_code\\\": 0, \\\"message\\\": \\\"global warning\\\", \\\"entity_type_code\\\": 1, \\\"entity_id\\\": ${image_id}}" /internal/testing/warnings

    # There should be three warnings now.
    count=$(lxc warning list --format json | jq 'length')
    [ "${count}" -eq 3 ] || false

    # Test filtering
    count=$(curl -G --unix-socket "$INCUS_DIR/unix.socket" "lxd/1.0/warnings" --data-urlencode "recursion=0" --data-urlencode "filter=status eq new" | jq ".metadata | length")
    [ "${count}" -eq 3 ] || false

    count=$(curl -G --unix-socket "$INCUS_DIR/unix.socket" "lxd/1.0/warnings" --data-urlencode "recursion=0" --data-urlencode "filter=status eq resolved" | jq ".metadata | length")
    [ "${count}" -eq 0 ] || false

    # Acknowledge a warning
    uuid=$(lxc warning list --format json | jq -r '.[] | select(.last_message=="global warning 2") | .uuid')
    lxc warning ack "${uuid}"

    # This should hide the acknowledged
    count=$(lxc warning list --format json | jq 'length')
    [ "${count}" -eq 2 ] || false

    # ... unless one uses --all.
    count=$(lxc warning list --all --format json | jq 'length')
    [ "${count}" -eq 3 ] || false

    lxc warning show "${uuid}" | grep "global warning 2"

    # Delete warning
    lxc warning rm "${uuid}"
    ! lxc warning list | grep -q "${uuid}" || false
    ! lxc warning show "${uuid}" || false
}
