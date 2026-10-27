test_network_dhcp_routes() {
    ensure_import_testimage
    ensure_has_localhost_remote "${INCUS_ADDR}"

    # bridge network
    incus network create inct$$
    incus network set inct$$ ipv4.address 10.13.37.1/24
    incus network set inct$$ ipv4.dhcp.routes 1.2.0.0/16,10.13.37.5,2.3.0.0/16,10.13.37.7

    incus launch testimage nettest -n inct$$

    cat > "${TEST_DIR}"/udhcpc.sh << EOL
#!/bin/sh
[ "\$1" = "bound" ] && echo "STATICROUTES: \$staticroutes"
EOL

    incus file push "${TEST_DIR}/udhcpc.sh" nettest/udhcpc.sh
    incus exec nettest -- chmod a+rx /udhcpc.sh

    staticroutes_output=$(incus exec nettest -- udhcpc -s /udhcpc.sh 2> /dev/null | grep STATICROUTES)

    echo "$staticroutes_output" | grep -q "1.2.0.0/16 10.13.37.5"
    echo "$staticroutes_output" | grep -q "2.3.0.0/16 10.13.37.7"

    incus delete nettest -f

    if [ -n "${INCUS_OFFLINE:-}" ]; then
        echo "==> SKIP: Skipping OCI tests as running offline"
    else
        ensure_has_localhost_remote "${INCUS_ADDR}"

        incus remote add docker https://docker.io --protocol=oci
        incus launch docker:alpine nettest --network=inct$$

        incus exec nettest -- ip route list | grep -q "1.2.0.0/16 via 10.13.37.5"
        incus exec nettest -- ip route list | grep -q "2.3.0.0/16 via 10.13.37.7"

        incus delete -f nettest
        incus remote remove docker
    fi

    incus network delete inct$$

    # ovn network
    if incus network create inct$$ -t ovn network=none; then
        incus network set inct$$ ipv4.address 10.13.37.1/24
        incus network set inct$$ ipv4.dhcp.routes 1.2.0.0/16,10.13.37.5,2.3.0.0/16,10.13.37.7

        incus launch testimage nettest -n inct$$

        incus exec nettest -- ip route list | grep -q "1.2.0.0/16 via 10.13.37.5"
        incus exec nettest -- ip route list | grep -q "2.3.0.0/16 via 10.13.37.7"

        incus delete nettest -f
        incus network delete inct$$
    else
        echo "==> SKIP: Skipping OVN tests"
    fi
}
