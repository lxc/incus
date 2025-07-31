test_network_peers() {
    if incus network create inct$$ -t ovn network=none; then
        incus network create inct$$

        incus network create -t ovn inct$$-ovn1
        incus network create -t ovn inct$$-ovn2

        incus network peer create inct$$-ovn1 inct$$-peer-1-2 inct$$-ovn2
        incus network peer create inct$$-ovn2 inct$$-peer-2-1 inct$$-ovn1
    else
        echo "==> SKIP: Skipping OVN tests"
    fi
}
