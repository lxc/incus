test_storage_volume_import() {
  truncate -s 25MiB foo.iso
  truncate -s 25MiB foo.img

  ensure_import_testimage

  # importing an ISO as storage volume requires a volume name
  ! inc storage volume import "incustest-$(basename "${INCUS_DIR}")" ./foo.iso || false
  ! inc storage volume import "incustest-$(basename "${INCUS_DIR}")" ./foo.img --type=iso || false

  # import ISO as storage volume
  inc storage volume import "incustest-$(basename "${INCUS_DIR}")" ./foo.iso foo
  inc storage volume import "incustest-$(basename "${INCUS_DIR}")" ./foo.img --type=iso foobar
  inc storage volume show "incustest-$(basename "${INCUS_DIR}")" foo | grep -q 'content_type: iso'
  inc storage volume show "incustest-$(basename "${INCUS_DIR}")" foobar | grep -q 'content_type: iso'

  # delete an ISO storage volume and re-import it
  inc storage volume delete "incustest-$(basename "${INCUS_DIR}")" foo
  inc storage volume delete "incustest-$(basename "${INCUS_DIR}")" foobar

  inc storage volume import "incustest-$(basename "${INCUS_DIR}")" ./foo.iso foo
  inc storage volume import "incustest-$(basename "${INCUS_DIR}")" ./foo.img --type=iso foobar
  inc storage volume show "incustest-$(basename "${INCUS_DIR}")" foo | grep -q 'content_type: iso'
  inc storage volume show "incustest-$(basename "${INCUS_DIR}")" foobar | grep -q 'content_type: iso'

  # snapshots are disabled for ISO storage volumes
  ! inc storage volume snapshot "incustest-$(basename "${INCUS_DIR}")" foo || false

  # backups are disabled for ISO storage volumes
  ! inc storage volume export "incustest-$(basename "${INCUS_DIR}")" foo || false

  # cannot attach ISO storage volumes to containers
  inc init testimage c1
  ! inc storage volume attach "incustest-$(basename "${INCUS_DIR}")" c1 foo || false

  # cannot change storage volume config
  ! inc storage volume set "incustest-$(basename "${INCUS_DIR}")" foo size=1GiB || false

  # copy volume
  inc storage volume copy "incustest-$(basename "${INCUS_DIR}")"/foo "incustest-$(basename "${INCUS_DIR}")"/bar
  inc storage volume show "incustest-$(basename "${INCUS_DIR}")" bar | grep -q 'content_type: iso'

  # cannot refresh copy
  ! inc storage volume copy "incustest-$(basename "${INCUS_DIR}")"/foo "incustest-$(basename "${INCUS_DIR}")"/bar --refresh || false

  # can change description
  inc storage volume show "incustest-$(basename "${INCUS_DIR}")" foo | sed 's/^description:.*/description: foo/' | inc storage volume edit "incustest-$(basename "${INCUS_DIR}")" foo
  inc storage volume show "incustest-$(basename "${INCUS_DIR}")" foo | grep -q 'description: foo'

  # cleanup
  inc delete -f c1
  inc storage volume delete "incustest-$(basename "${INCUS_DIR}")" foo
  inc storage volume delete "incustest-$(basename "${INCUS_DIR}")" bar
  inc storage volume delete "incustest-$(basename "${INCUS_DIR}")" foobar

  rm -f foo.iso foo.img
}
