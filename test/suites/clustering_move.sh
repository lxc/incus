test_clustering_move() {
    # shellcheck disable=2039,3043,SC2034
    local INCUS_DIR

    setup_clustering_bridge
    prefix="inc$$"
    bridge="${prefix}"

    # The random storage backend is not supported in clustering tests,
    # since we need to have the same storage driver on all nodes, so use the driver chosen for the standalone pool.
    poolDriver=$(incus storage show "$(incus profile device get default root pool)" | awk '/^driver:/ {print $2}')

    setup_clustering_netns 1
    INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${INCUS_ONE_DIR}"
    ns1="${prefix}1"
    spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}" "${poolDriver}"

    # Add a newline at the end of each line. YAML as weird rules..
    cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")

    # Spawn a second node
    setup_clustering_netns 2
    INCUS_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${INCUS_TWO_DIR}"
    ns2="${prefix}2"
    spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}" "${poolDriver}"

    # Spawn a third node
    setup_clustering_netns 3
    INCUS_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${INCUS_THREE_DIR}"
    ns3="${prefix}3"
    spawn_incus_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${INCUS_THREE_DIR}" "${INCUS_ONE_DIR}" "${poolDriver}"

    ensure_import_testimage

    # Preparation
    INCUS_DIR="${INCUS_ONE_DIR}" incus cluster group create foobar1
    INCUS_DIR="${INCUS_ONE_DIR}" incus cluster group assign node1 foobar1 default

    INCUS_DIR="${INCUS_ONE_DIR}" incus cluster group create foobar2
    INCUS_DIR="${INCUS_ONE_DIR}" incus cluster group assign node2 foobar2 default

    INCUS_DIR="${INCUS_ONE_DIR}" incus cluster group create foobar3
    INCUS_DIR="${INCUS_ONE_DIR}" incus cluster group assign node3 foobar3 default

    INCUS_DIR="${INCUS_ONE_DIR}" incus init testimage c1 --target node1
    INCUS_DIR="${INCUS_ONE_DIR}" incus init testimage c2 --target node2
    INCUS_DIR="${INCUS_ONE_DIR}" incus init testimage c3 --target node3

    # Perform default move tests falling back to the built in logic of choosing the node
    # with the least number of instances when targeting a cluster group.
    INCUS_DIR="${INCUS_ONE_DIR}" incus move c1 --target node2
    INCUS_DIR="${INCUS_ONE_DIR}" incus move c1 --target @foobar1
    INCUS_DIR="${INCUS_ONE_DIR}" incus info c1 | grep -q "Location: node1"

    # c1 can be moved within the same cluster group if it has multiple members
    current_location="$(INCUS_DIR="${INCUS_ONE_DIR}" incus query /1.0/instances/c1 | jq -r '.location')"
    INCUS_DIR="${INCUS_ONE_DIR}" incus move c1 --target=@default
    INCUS_DIR="${INCUS_ONE_DIR}" incus query /1.0/instances/c1 | jq -re ".location != \"$current_location\""
    current_location="$(INCUS_DIR="${INCUS_ONE_DIR}" incus query /1.0/instances/c1 | jq -r '.location')"
    INCUS_DIR="${INCUS_ONE_DIR}" incus move c1 --target=@default
    INCUS_DIR="${INCUS_ONE_DIR}" incus query /1.0/instances/c1 | jq -re ".location != \"$current_location\""

    # c1 cannot be moved within the same cluster group if it has a single member
    INCUS_DIR="${INCUS_ONE_DIR}" incus move c1 --target=@foobar3
    INCUS_DIR="${INCUS_ONE_DIR}" incus info c1 | grep -q "Location: node3"
    ! INCUS_DIR="${INCUS_ONE_DIR}" incus move c1 --target=@foobar3 || false

    # Perform standard move tests using the `scheduler.instance` cluster member setting.
    INCUS_DIR="${INCUS_ONE_DIR}" incus cluster set node2 scheduler.instance=group
    INCUS_DIR="${INCUS_ONE_DIR}" incus cluster set node3 scheduler.instance=manual

    # At this stage we have:
    # - node1 in group foobar1,default accepting all instances
    # - node2 in group foobar2,default accepting group-only targeting
    # - node3 in group foobar3,default accepting manual targeting only
    # - c1 is deployed on node1
    # - c2 is deployed on node2
    # - c3 is deployed on node3

    # c1 can be moved to node2 by group targeting.
    INCUS_DIR="${INCUS_ONE_DIR}" incus move c1 --target=@foobar2
    INCUS_DIR="${INCUS_ONE_DIR}" incus info c1 | grep -q "Location: node2"

    # c2 can be moved to node1 by manual targeting.
    INCUS_DIR="${INCUS_ONE_DIR}" incus move c2 --target=node1
    INCUS_DIR="${INCUS_ONE_DIR}" incus info c2 | grep -q "Location: node1"

    # c1 cannot be moved to node3 by group targeting.
    ! INCUS_DIR="${INCUS_ONE_DIR}" incus move c1 --target=@foobar3 || false

    # c2 can be moved to node2 by manual targeting.
    INCUS_DIR="${INCUS_ONE_DIR}" incus move c2 --target=node2

    # c3 can be moved to node1 by manual targeting.
    INCUS_DIR="${INCUS_ONE_DIR}" incus move c3 --target=node1
    INCUS_DIR="${INCUS_ONE_DIR}" incus info c3 | grep -q "Location: node1"

    # c3 can be moved back to node by by manual targeting.
    INCUS_DIR="${INCUS_ONE_DIR}" incus move c3 --target=node3
    INCUS_DIR="${INCUS_ONE_DIR}" incus info c3 | grep -q "Location: node3"

    # Clean up
    INCUS_DIR="${INCUS_ONE_DIR}" incus cluster unset node2 scheduler.instance
    INCUS_DIR="${INCUS_ONE_DIR}" incus cluster unset node3 scheduler.instance
    INCUS_DIR="${INCUS_ONE_DIR}" incus move c1 --target node1

    # Perform extended scheduler tests involving the `instance.placement.scriptlet` global setting.
    # Start by statically targeting node3 (index 0).
    cat << EOF | INCUS_DIR="${INCUS_ONE_DIR}" incus config set instances.placement.scriptlet=-
def instance_placement(request, candidate_members):
        if request.reason != "relocation":
                return "Expecting reason relocation"

        # Set statically target to 1st member.
        candidate_names = sorted([candidate.server_name for candidate in candidate_members])
        set_target(candidate_names[0])

        return
EOF

    INCUS_DIR="${INCUS_ONE_DIR}" incus move c1 --target @foobar3
    INCUS_DIR="${INCUS_ONE_DIR}" incus info c1 | grep -q "Location: node3"
    INCUS_DIR="${INCUS_ONE_DIR}" incus move c2 --target @foobar3
    INCUS_DIR="${INCUS_ONE_DIR}" incus info c2 | grep -q "Location: node3"

    # Ensure that setting an invalid target causes the error to be raised.
    INCUS_DIR="${INCUS_ONE_DIR}" incus move c2 --target node2

    cat << EOF | INCUS_DIR="${INCUS_ONE_DIR}" incus config set instances.placement.scriptlet=-
def instance_placement(request, candidate_members):
        # Set invalid member target.
        result = set_target("foo")
        log_warn("Setting invalid member target result: ", result)

        return
EOF

    ! INCUS_DIR="${INCUS_ONE_DIR}" incus move c1 --target @foobar1 || false

    # If the scriptlet produces a runtime error, the move fails.
    cat << EOF | INCUS_DIR="${INCUS_ONE_DIR}" incus config set instances.placement.scriptlet=-
def instance_placement(request, candidate_members):
        # Try to access an invalid index (non existing member)
        log_info("Accessing invalid field ", candidate_members[42])

        return
EOF

    ! INCUS_DIR="${INCUS_ONE_DIR}" incus move c1 --target @foobar2 || false

    # If the scriptlet intentionally runs into an error, the move fails.
    cat << EOF | INCUS_DIR="${INCUS_ONE_DIR}" incus config set instances.placement.scriptlet=-
def instance_placement(request, candidate_members):
        log_error("instance placement not allowed") # Log placement error.

        fail("Instance not allowed") # Fail to prevent instance creation.
EOF

    ! INCUS_DIR="${INCUS_ONE_DIR}" incus move c1 --target @foobar2 || false

    # Cleanup
    INCUS_DIR="${INCUS_ONE_DIR}" incus config unset instances.placement.scriptlet

    # Perform near-live migration tests.
    if [ "${poolDriver}" = "zfs" ] || [ "${poolDriver}" = "btrfs" ]; then
        INCUS_DIR="${INCUS_ONE_DIR}" incus launch testimage c4 --target node1
        INCUS_DIR="${INCUS_ONE_DIR}" incus config set c4 boot.host_shutdown_timeout=1

        # --refresh can be called only with --stateless.
        ! INCUS_DIR="${INCUS_ONE_DIR}" incus move c4 --target node2 --refresh || false

        # c4 can be moved to node2 while running.
        INCUS_DIR="${INCUS_ONE_DIR}" incus move c4 --target node2 --stateless --refresh
        INCUS_DIR="${INCUS_ONE_DIR}" incus info c4 | grep -q "Location: node2"
        INCUS_DIR="${INCUS_ONE_DIR}" incus info c4 | grep -q "Status: RUNNING"

        # The pre-copy snapshots and the temporary copy are gone.
        [ "$(INCUS_DIR="${INCUS_ONE_DIR}" incus query /1.0/instances/c4/snapshots | jq 'length')" = "0" ]
        ! INCUS_DIR="${INCUS_ONE_DIR}" incus list --format csv --columns n | grep -q '^move-of-' || false

        # Existing snapshots are carried over.
        INCUS_DIR="${INCUS_ONE_DIR}" incus snapshot create c4 snap0
        INCUS_DIR="${INCUS_ONE_DIR}" incus move c4 --target node1 --stateless --refresh
        INCUS_DIR="${INCUS_ONE_DIR}" incus info c4 | grep -q "Location: node1"
        INCUS_DIR="${INCUS_ONE_DIR}" incus info c4 | grep -q "Status: RUNNING"
        [ "$(INCUS_DIR="${INCUS_ONE_DIR}" incus query /1.0/instances/c4/snapshots | jq 'length')" = "1" ]
        INCUS_DIR="${INCUS_ONE_DIR}" incus query /1.0/instances/c4/snapshots | jq -re '.[0] | endswith("/snap0")'

        # A stopped container falls back to a regular migration.
        INCUS_DIR="${INCUS_ONE_DIR}" incus stop -f c4
        INCUS_DIR="${INCUS_ONE_DIR}" incus move c4 --target node2 --stateless --refresh
        INCUS_DIR="${INCUS_ONE_DIR}" incus info c4 | grep -q "Location: node2"
        INCUS_DIR="${INCUS_ONE_DIR}" incus info c4 | grep -q "Status: STOPPED"

        # Dependent volumes are moved along with the instance.
        INCUS_DIR="${INCUS_ONE_DIR}" incus launch testimage c5 --target node1
        INCUS_DIR="${INCUS_ONE_DIR}" incus config set c5 boot.host_shutdown_timeout=1
        INCUS_DIR="${INCUS_ONE_DIR}" incus storage volume create data vol1
        INCUS_DIR="${INCUS_ONE_DIR}" incus storage volume attach data vol1 c5 vol1 /mnt/vol1
        INCUS_DIR="${INCUS_ONE_DIR}" incus config device set c5 vol1 dependent=true

        # A dependent volume on local storage is transferred along with the instance.
        INCUS_DIR="${INCUS_ONE_DIR}" incus move c5 --target node2 --stateless --refresh
        INCUS_DIR="${INCUS_ONE_DIR}" incus info c5 | grep -q "Location: node2"
        INCUS_DIR="${INCUS_ONE_DIR}" incus info c5 | grep -q "Status: RUNNING"

        # The volume followed the instance, and is gone from node1.
        [ "$(INCUS_DIR="${INCUS_ONE_DIR}" incus storage volume list data --format csv --columns nL | grep -c '^vol1,node2$')" = "1" ]
        [ "$(INCUS_DIR="${INCUS_ONE_DIR}" incus storage volume snapshot list data vol1 --format json | jq 'length')" = "0" ]

        # And it moves back the same way.
        INCUS_DIR="${INCUS_ONE_DIR}" incus move c5 --target node1 --stateless --refresh
        [ "$(INCUS_DIR="${INCUS_ONE_DIR}" incus storage volume list data --format csv --columns nL | grep -c '^vol1,node1$')" = "1" ]

        # Cleanup
        INCUS_DIR="${INCUS_ONE_DIR}" incus delete -f c4
        INCUS_DIR="${INCUS_ONE_DIR}" incus config device remove c5 vol1
        INCUS_DIR="${INCUS_ONE_DIR}" incus storage volume delete data vol1
        INCUS_DIR="${INCUS_ONE_DIR}" incus delete -f c5
    fi

    # Perform project restriction tests.
    # At this stage we have:
    # - node1 in group foobar1,default
    # - node2 in group foobar2,default
    # - node3 in group foobar3,default
    # - c1 is deployed on node1
    # - c2 is deployed on node2
    # - c3 is deployed on node3
    # - default project restricted to cluster groups foobar1,foobar2
    INCUS_DIR="${INCUS_ONE_DIR}" incus project set default restricted=true
    INCUS_DIR="${INCUS_ONE_DIR}" incus project set default restricted.cluster.groups=foobar1,foobar2

    # Moving to a node that is not a member of foobar1 or foobar2 will fail.
    # The same applies for an unlisted group
    ! INCUS_DIR="${INCUS_ONE_DIR}" incus move c1 --target @foobar3 || false
    ! INCUS_DIR="${INCUS_ONE_DIR}" incus move c2 --target node3 || false

    # Moving instances in between the restricted groups
    INCUS_DIR="${INCUS_ONE_DIR}" incus move c1 --target node2
    INCUS_DIR="${INCUS_ONE_DIR}" incus move c2 --target @foobar1
    INCUS_DIR="${INCUS_ONE_DIR}" incus move c3 --target node1

    # Cleanup
    INCUS_DIR="${INCUS_ONE_DIR}" incus delete -f c1 c2 c3

    INCUS_DIR="${INCUS_THREE_DIR}" incus admin shutdown
    INCUS_DIR="${INCUS_TWO_DIR}" incus admin shutdown
    INCUS_DIR="${INCUS_ONE_DIR}" incus admin shutdown
    sleep 0.5
    rm -f "${INCUS_THREE_DIR}/unix.socket"
    rm -f "${INCUS_TWO_DIR}/unix.socket"
    rm -f "${INCUS_ONE_DIR}/unix.socket"

    teardown_clustering_netns
    teardown_clustering_bridge

    kill_incus "${INCUS_ONE_DIR}"
    kill_incus "${INCUS_TWO_DIR}"
    kill_incus "${INCUS_THREE_DIR}"
}
