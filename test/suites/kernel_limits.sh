test_kernel_limits() {
  lxc_version=$(incus info | awk '/driver_version:/ {print $NF}')
  lxc_major=$(echo "${lxc_version}" | cut -d. -f1)
  lxc_minor=$(echo "${lxc_version}" | cut -d. -f2)

  if [ "${lxc_major}" -lt 2 ] || { [ "${lxc_major}" = "2" ] && [ "${lxc_minor}" -lt "1" ]; }; then
    echo "==> SKIP: kernel_limits require liblxc 2.1 or higher"
    return
  fi

  echo "==> API extension kernel_limits"

  ensure_import_testimage
  incus init testimage limits
  # Set it to a limit < 65536 because older systemd's do not have my nofile
  # limit patch.
  incus config set limits limits.kernel.nofile 3000
  incus start limits
  pid="$(incus info limits | awk '/^PID/ {print $2}')"
  soft="$(awk '/^Max open files/ {print $4}' /proc/"${pid}"/limits)"
  hard="$(awk '/^Max open files/ {print $5}' /proc/"${pid}"/limits)"

  incus delete --force limits

  [ "${soft}" = "3000" ] && [ "${hard}" = "3000" ]
}
