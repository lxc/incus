test_network_hwaddr_pattern() {
    ensure_import_testimage
    ensure_has_localhost_remote "${INCUS_ADDR}"

    incus network create "inct$$"
    incus init testimage nettest -n "inct$$"
    incus start nettest

    # First, check for the default Zabbly MAC
    ip link show "inct$$" | grep -q 'link/ether 10:66:6a:'
    incus exec nettest -- ip link show eth0 | grep -q 'link/ether 10:66:6a:'
    incus stop -f nettest

    # Then, override the MAC pattern globally
    incus config set network.hwaddr_pattern=00:11:22:xx:xx:xx
    incus config unset nettest volatile.eth0.hwaddr
    # Refresh the bridge
    incus network set "inct$$" bridge.mtu=1500
    ip link show "inct$$" | grep -q 'link/ether 00:11:22:'
    incus start nettest
    incus exec nettest -- ip link show eth0 | grep -q 'link/ether 00:11:22:'
    incus stop -f nettest

    # Finally, create a project and override its MAC pattern
    incus project create foo
    incus project switch foo
    ensure_import_testimage
    incus profile device add default root disk path="/" pool="incustest-$(basename "${INCUS_DIR}")"
    incus project switch default
    incus project set foo network.hwaddr_pattern=00:33:44:xx:xx:xx
    incus config unset nettest volatile.eth0.hwaddr
    incus start nettest
    incus exec nettest -- ip link show eth0 | grep -q 'link/ether 00:11:22:'
    incus stop -f nettest
    incus init --project foo testimage nettest-foo -n "inct$$"
    incus start --project foo nettest-foo
    incus exec --project foo nettest-foo -- ip link show eth0 | grep -q 'link/ether 00:33:44:'
    incus stop --project foo -f nettest-foo
    incus delete --project foo -f nettest-foo

    incus project set foo features.networks=true
    if incus network create --project foo "inct$$-foo"; then
        ip link show "inct$$-foo" | grep -q 'link/ether 00:33:44:'
        incus network delete --project foo "inct$$-foo"
    else
        echo "==> SKIP: Skipping OVN tests"
    fi

    incus image remove --project foo testimage
    incus project delete foo
    incus delete -f nettest
    incus network delete "inct$$"
}
