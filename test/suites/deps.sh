test_check_deps() {
  ! ldd "$(command -v inc)" | grep -q liblxc || false
}
