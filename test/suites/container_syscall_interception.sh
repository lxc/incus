test_container_syscall_interception() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  if [ "$(inc query /1.0 | jq -r .environment.lxc_features.seccomp_notify)" != "true" ]; then
    echo "==> SKIP: Seccomp notify not supported"
    return
  fi

  if [ "$(awk '/^Seccomp:/ {print $2}' "/proc/self/status")" -eq "0" ]; then
    echo "==> SKIP: syscall interception (seccomp filtering is externally enabled)"
    return
  fi

  (
    cd syscall/sysinfo || return
    # Use -buildvcs=false here to prevent git complaining about untrusted directory when tests are run as root.
    go build -v -buildvcs=false ./...
  )

  inc init testimage c1
  inc config set c1 limits.memory=123MiB
  inc start c1
  inc file push syscall/sysinfo/sysinfo c1/root/sysinfo
  inc exec c1 -- /root/sysinfo
  ! inc exec c1 -- /root/sysinfo | grep "Totalram:128974848 " || false
  inc stop -f c1
  inc config set c1 security.syscalls.intercept.sysinfo=true
  inc start c1
  inc exec c1 -- /root/sysinfo
  inc exec c1 -- /root/sysinfo | grep "Totalram:128974848 "
  inc delete -f c1
}
