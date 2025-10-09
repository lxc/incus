assert_systemd_credentials_entries() {
    entries=$(incus exec foo -- ls -A1 /dev/.incus-systemd-credentials | wc -l)
    if [ "$entries" != "$1" ]; then
        printf "Expected %s entries in systemd credentials directory; got %s\n" "$1" "$entries"
        false
    fi
}

assert_systemd_credentials_value() {
    value=$(incus exec foo cat "/dev/.incus-systemd-credentials/$1")
    if [ "$value" != "$2" ]; then
        printf "Expected %s for systemd credential %s; got %s\n" "$2" "$1" "$value"
        false
    fi
}

test_systemd() {
    ensure_import_testimage
    ensure_has_localhost_remote "${INCUS_ADDR}"

    incus launch testimage foo
    stat=$(incus exec foo -- stat -c%a /dev/.incus-systemd-credentials)
    if [ "$stat" != "100" ]; then
        echo "Wrong permissions on systemd credentials directory"
        false
    fi

    assert_systemd_credentials_entries 0

    # Regular credential
    incus config set foo systemd.credential.foo bar
    assert_systemd_credentials_entries 1
    assert_systemd_credentials_value foo bar

    # Base64 credential
    incus config set foo systemd.credential-binary.xxx eXl5
    assert_systemd_credentials_entries 2
    assert_systemd_credentials_value foo bar
    assert_systemd_credentials_value xxx yyy

    # Mutually exclusive credential and credential-binary keys
    ! incus config set foo systemd.credential-binary.foo YmF6========= || false
    incus config unset foo systemd.credential.foo

    # Base64 credential with superfluous padding
    incus config set foo systemd.credential-binary.foo YmF6=========
    assert_systemd_credentials_entries 2
    assert_systemd_credentials_value foo baz
    assert_systemd_credentials_value xxx yyy

    # Consistency after reboot
    incus restart foo --force
    assert_systemd_credentials_entries 2
    assert_systemd_credentials_value foo baz
    assert_systemd_credentials_value xxx yyy

    # Credential deletion
    incus config unset foo systemd.credential-binary.foo
    assert_systemd_credentials_entries 1
    assert_systemd_credentials_value xxx yyy

    incus rm -f foo
}
