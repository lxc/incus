test_storage_volume_import() {
    truncate -s 25MiB foo.iso
    truncate -s 25MiB foo.img

    ensure_import_testimage

    # importing an ISO as storage volume requires a volume name
    ! incus storage volume import "incustest-$(basename "${INCUS_DIR}")" ./foo.iso || false
    ! incus storage volume import "incustest-$(basename "${INCUS_DIR}")" ./foo.img --type=iso || false

    # import ISO as storage volume
    incus storage volume import "incustest-$(basename "${INCUS_DIR}")" ./foo.iso foo
    incus storage volume import "incustest-$(basename "${INCUS_DIR}")" ./foo.img --type=iso foobar
    incus storage volume show "incustest-$(basename "${INCUS_DIR}")" foo | grep -q 'content_type: iso'
    incus storage volume show "incustest-$(basename "${INCUS_DIR}")" foobar | grep -q 'content_type: iso'

    # delete an ISO storage volume and re-import it
    incus storage volume delete "incustest-$(basename "${INCUS_DIR}")" foo
    incus storage volume delete "incustest-$(basename "${INCUS_DIR}")" foobar

    incus storage volume import "incustest-$(basename "${INCUS_DIR}")" ./foo.iso foo
    incus storage volume import "incustest-$(basename "${INCUS_DIR}")" ./foo.img --type=iso foobar
    incus storage volume show "incustest-$(basename "${INCUS_DIR}")" foo | grep -q 'content_type: iso'
    incus storage volume show "incustest-$(basename "${INCUS_DIR}")" foobar | grep -q 'content_type: iso'

    # snapshots are disabled for ISO storage volumes
    ! incus storage volume snapshot create "incustest-$(basename "${INCUS_DIR}")" foo || false

    # backups are disabled for ISO storage volumes
    ! incus storage volume export "incustest-$(basename "${INCUS_DIR}")" foo || false

    # cannot attach ISO storage volumes to containers
    incus init testimage c1
    ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")" c1 foo || false

    # cannot change storage volume config
    ! incus storage volume set "incustest-$(basename "${INCUS_DIR}")" foo size=1GiB || false

    # copy volume
    incus storage volume copy "incustest-$(basename "${INCUS_DIR}")"/foo "incustest-$(basename "${INCUS_DIR}")"/bar
    incus storage volume show "incustest-$(basename "${INCUS_DIR}")" bar | grep -q 'content_type: iso'

    # cannot refresh copy
    ! incus storage volume copy "incustest-$(basename "${INCUS_DIR}")"/foo "incustest-$(basename "${INCUS_DIR}")"/bar --refresh || false

    # can change description
    incus storage volume show "incustest-$(basename "${INCUS_DIR}")" foo | sed 's/^description:.*/description: foo/' | incus storage volume edit "incustest-$(basename "${INCUS_DIR}")" foo
    incus storage volume show "incustest-$(basename "${INCUS_DIR}")" foo | grep -q 'description: foo'

    # cleanup
    incus delete -f c1
    incus storage volume delete "incustest-$(basename "${INCUS_DIR}")" foo
    incus storage volume delete "incustest-$(basename "${INCUS_DIR}")" bar
    incus storage volume delete "incustest-$(basename "${INCUS_DIR}")" foobar

    rm -f foo.iso foo.img
}
