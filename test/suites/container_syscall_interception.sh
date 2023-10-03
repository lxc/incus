test_container_syscall_interception() {
  ensure_import_testimage
  ensure_has_localhost_remote "${INCUS_ADDR}"

  if [ "$(awk '/^Seccomp:/ {print $2}' "/proc/self/status")" -eq "0" ]; then
    echo "==> SKIP: syscall interception (seccomp filtering is externally enabled)"
    return
  fi

  (
    cd syscall/sysinfo || return
    # Use -buildvcs=false here to prevent git complaining about untrusted directory when tests are run as root.
    go build -v -buildvcs=false ./...
  )

  incus init testimage c1
  incus config set c1 limits.memory=123MiB
  incus start c1
  incus file push syscall/sysinfo/sysinfo c1/root/sysinfo
  incus exec c1 -- /root/sysinfo
  ! incus exec c1 -- /root/sysinfo | grep "Totalram:128974848 " || false
  incus stop -f c1
  incus config set c1 security.syscalls.intercept.sysinfo=true
  incus start c1
  incus exec c1 -- /root/sysinfo
  incus exec c1 -- /root/sysinfo | grep "Totalram:128974848 "
  incus delete -f c1
}
