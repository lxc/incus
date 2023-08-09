test_warnings() {
    # Delete previous warnings
    inc query --wait /1.0/warnings\?recursion=1 | jq -r '.[].uuid' | xargs -n1 inc warning delete

    # Create a global warning (no node and no project)
    inc query --wait -X POST -d '{\"type_code\": 0, \"message\": \"global warning\"}' /internal/testing/warnings

    # More valid queries
    inc query --wait -X POST -d '{\"type_code\": 0, \"message\": \"global warning\", \"project\": \"default\"}' /internal/testing/warnings

    # Update the last warning. This will not create a new warning.
    inc query --wait -X POST -d '{\"type_code\": 0, \"message\": \"global warning 2\", \"project\": \"default\"}' /internal/testing/warnings

    # There should be two warnings now.
    count=$(inc query --wait /1.0/warnings | jq 'length')
    [ "${count}" -eq 2 ] || false

    count=$(inc query --wait /1.0/warnings\?recursion=1 | jq 'length')
    [ "${count}" -eq 2 ] || false

    # Invalid query (unknown project)
    ! inc query --wait -X POST -d '{\"type_code\": 0, \"message\": \"global warning\", \"project\": \"foo\"}' /internal/testing/warnings || false

    # Invalid query (unknown type code)
    ! inc query --wait -X POST -d '{\"type_code\": 999, \"message\": \"global warning\"}' /internal/testing/warnings || false

    # Both entity type code as entity ID need to be valid otherwise no warning will be created. Note that empty/null values are valid as well.
    ! inc query --wait -X POST -d '{\"type_code\": 0, \"message\": \"global warning\", \"entity_type_code\": 0, \"entity_id\": 0}' /internal/testing/warnings || false

    ensure_import_testimage

    # Get image ID from database instead of assuming it
    image_id=$(incusd sql global 'select image_id from images_aliases where name="testimage"' | grep -Eo '[[:digit:]]+')

    # Create a warning with entity type "image" and entity ID ${image_id} (the imported testimage)
    inc query --wait -X POST -d "{\\\"type_code\\\": 0, \\\"message\\\": \\\"global warning\\\", \\\"entity_type_code\\\": 1, \\\"entity_id\\\": ${image_id}}" /internal/testing/warnings

    # There should be three warnings now.
    count=$(inc warning list --format json | jq 'length')
    [ "${count}" -eq 3 ] || false

    # Test filtering
    count=$(curl -G --unix-socket "$INCUS_DIR/unix.socket" "incus/1.0/warnings" --data-urlencode "recursion=0" --data-urlencode "filter=status eq new" | jq ".metadata | length")
    [ "${count}" -eq 3 ] || false

    count=$(curl -G --unix-socket "$INCUS_DIR/unix.socket" "incus/1.0/warnings" --data-urlencode "recursion=0" --data-urlencode "filter=status eq resolved" | jq ".metadata | length")
    [ "${count}" -eq 0 ] || false

    # Acknowledge a warning
    uuid=$(inc warning list --format json | jq -r '.[] | select(.last_message=="global warning 2") | .uuid')
    inc warning ack "${uuid}"

    # This should hide the acknowledged
    count=$(inc warning list --format json | jq 'length')
    [ "${count}" -eq 2 ] || false

    # ... unless one uses --all.
    count=$(inc warning list --all --format json | jq 'length')
    [ "${count}" -eq 3 ] || false

    inc warning show "${uuid}" | grep "global warning 2"

    # Delete warning
    inc warning rm "${uuid}"
    ! inc warning list | grep -q "${uuid}" || false
    ! inc warning show "${uuid}" || false
}
