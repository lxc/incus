test_container_devices_tpm() {
  if ! command -v swtpm >/dev/null 2>&1; then
    echo "==> SKIP: No swtpm binary could be found"
    return
  fi

  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"
  ctName="ct$$"
  lxc launch testimage "${ctName}"

  # Check adding a device with no path
  ! lxc config device add "${ctName}" test-dev-invalid

  # Add device
  lxc config device add "${ctName}" test-dev1 tpm path=/dev/tpm0 pathrm=/dev/tpmrm0
  lxc exec "${ctName}" -- stat /dev/tpm0
  lxc exec "${ctName}" -- stat /dev/tpmrm0

  # Remove device
  lxc config device rm "${ctName}" test-dev1
  ! lxc exec "${ctName}" -- stat /dev/tpm0
  ! lxc exec "${ctName}" -- stat /dev/tpmrm0

  # Clean up
  lxc rm -f "${ctName}"
}
