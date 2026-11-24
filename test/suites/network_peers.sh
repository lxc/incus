test_network_peers() {
    if ! incus network create inct$$-ovn1 -t ovn network=none; then
        echo "==> SKIP: Skipping OVN tests"
        return
    fi

    incus network create inct$$-ovn2 -t ovn network=none

    incus network peer create inct$$-ovn1 inct$$-peer-1-2 inct$$-ovn2
    incus network peer create inct$$-ovn2 inct$$-peer-2-1 inct$$-ovn1
    incus network peer list inct$$-ovn1 | grep inct$$-peer-1-2 | grep -q CREATED

    incus network peer delete inct$$-ovn1 inct$$-peer-1-2
    incus network peer delete inct$$-ovn2 inct$$-peer-2-1
    incus network delete inct$$-ovn1
    incus network delete inct$$-ovn2
}
