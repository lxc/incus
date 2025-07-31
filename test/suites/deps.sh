test_check_deps() {
    ! ldd "$(command -v incus)" | grep -q liblxc || false
}
