test_clustering_enable() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  INCUS_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_INIT_DIR}"
  spawn_incus "${INCUS_INIT_DIR}" false

  # Test specified core.https_address with no cluster.https_address
  (
    set -e
    # shellcheck disable=SC2034,SC2030
    INCUS_DIR=${INCUS_INIT_DIR}

    incus config show | grep "core.https_address" | grep -qE "127.0.0.1:[0-9]{4,5}$"
    # Launch a container.
    ensure_import_testimage
    incus storage create default dir
    incus profile device add default root disk path="/" pool="default"
    incus launch testimage c1

    # Enable clustering.
    incus cluster enable node1
    incus cluster list | grep -q node1

    # The container is still there and now shows up as
    # running on node 1.
    incus list | grep c1 | grep -q node1

    # Clustering can't be enabled on an already clustered instance.
    ! incus cluster enable node2 || false

    # Delete the container
    incus stop c1 --force
    incus delete c1
  )

  kill_incus "${INCUS_INIT_DIR}"

  # Test wildcard core.https_address with no cluster.https_address
  INCUS_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_INIT_DIR}"
  spawn_incus "${INCUS_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034,SC2030
    INCUS_DIR=${INCUS_INIT_DIR}
    incus config set core.https_address ::
    # Enable clustering.
    ! incus cluster enable node1 || false
  )

  kill_incus "${INCUS_INIT_DIR}"

  # Test default port core.https_address with no cluster.https_address
  INCUS_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_INIT_DIR}"
  spawn_incus "${INCUS_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034,SC2030
    INCUS_DIR=${INCUS_INIT_DIR}
    incus config set core.https_address 127.0.0.1
    # Enable clustering.
    incus cluster enable node1
    incus cluster list | grep -q 127.0.0.1:8443
  )

  kill_incus "${INCUS_INIT_DIR}"

  # Test wildcard core.https_address with valid cluster.https_address
  INCUS_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_INIT_DIR}"
  spawn_incus "${INCUS_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034,SC2030
    INCUS_DIR=${INCUS_INIT_DIR}
    incus config set core.https_address ::
    incus config set cluster.https_address 127.0.0.1:8443
    # Enable clustering.
    incus cluster enable node1
    incus cluster list | grep -q 127.0.0.1:8443
  )

  kill_incus "${INCUS_INIT_DIR}"

  # Test empty core.https_address with no cluster.https_address
  INCUS_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_INIT_DIR}"
  spawn_incus "${INCUS_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034,SC2030
    INCUS_DIR=${INCUS_INIT_DIR}
    incus config unset core.https_address
    # Enable clustering.
    ! incus cluster enable node1 || false
  )

  kill_incus "${INCUS_INIT_DIR}"

  # Test empty core.https_address with valid cluster.https_address
  INCUS_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_INIT_DIR}"
  spawn_incus "${INCUS_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034,SC2030
    INCUS_DIR=${INCUS_INIT_DIR}
    incus config unset core.https_address
    incus config set cluster.https_address 127.0.0.1:8443
    # Enable clustering.
    incus cluster enable node1
    incus cluster list | grep -q 127.0.0.1:8443
  )

  kill_incus "${INCUS_INIT_DIR}"

  # Test empty core.https_address with default port cluster.https_address
  INCUS_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_INIT_DIR}"
  spawn_incus "${INCUS_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034,SC2030
    INCUS_DIR=${INCUS_INIT_DIR}
    incus config unset core.https_address
    incus config set cluster.https_address 127.0.0.1
    # Enable clustering.
    incus cluster enable node1
    incus cluster list | grep -q 127.0.0.1:8443
  )

  kill_incus "${INCUS_INIT_DIR}"

  # Test covered cluster.https_address
  INCUS_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_INIT_DIR}"
  spawn_incus "${INCUS_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034,SC2030
    INCUS_DIR=${INCUS_INIT_DIR}
    incus config set core.https_address 127.0.0.1:8443
    incus config set cluster.https_address 127.0.0.1:8443
    # Enable clustering.
    incus cluster enable node1
    incus cluster list | grep -q 127.0.0.1:8443
  )

  kill_incus "${INCUS_INIT_DIR}"

  # Test cluster listener after reload
  INCUS_INIT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_INIT_DIR}"
  spawn_incus "${INCUS_INIT_DIR}" false

  (
    set -e
    # shellcheck disable=SC2034,SC2030
    INCUS_DIR=${INCUS_INIT_DIR}
    incus config set cluster.https_address 127.0.0.1:8443
    kill -9 "$(cat "${INCUS_DIR}/incus.pid")"
    respawn_incus "${INCUS_DIR}" true
    # Enable clustering.
    incus cluster enable node1
    incus cluster list | grep -q 127.0.0.1:8443
  )

  kill_incus "${INCUS_INIT_DIR}"
}

test_clustering_membership() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  INCUS_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_TWO_DIR}"
  ns2="${prefix}2"
  spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}"

  # Configuration keys can be changed on any node.
  INCUS_DIR="${INCUS_TWO_DIR}" incus config set cluster.offline_threshold 11
  INCUS_DIR="${INCUS_ONE_DIR}" incus info | grep -q 'cluster.offline_threshold: "11"'
  INCUS_DIR="${INCUS_TWO_DIR}" incus info | grep -q 'cluster.offline_threshold: "11"'

  # The preseeded network bridge exists on all nodes.
  ns1_pid="$(cat "${TEST_DIR}/ns/${ns1}/PID")"
  ns2_pid="$(cat "${TEST_DIR}/ns/${ns2}/PID")"
  nsenter -m -n -t "${ns1_pid}" -- ip link show "${bridge}" > /dev/null
  nsenter -m -n -t "${ns2_pid}" -- ip link show "${bridge}" > /dev/null

  # Create a pending network and pool, to show that they are not
  # considered when checking if the joining node has all the required
  # networks and pools.
  INCUS_DIR="${INCUS_TWO_DIR}" incus storage create pool1 dir --target node1
  INCUS_DIR="${INCUS_ONE_DIR}" incus network create net1 --target node2

  # Spawn a third node, using the non-leader node2 as join target.
  setup_clustering_netns 3
  INCUS_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_THREE_DIR}"
  ns3="${prefix}3"
  spawn_incus_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 2 "${INCUS_THREE_DIR}" "${INCUS_ONE_DIR}"

  # Spawn a fourth node, this will be a non-database node.
  setup_clustering_netns 4
  INCUS_FOUR_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_FOUR_DIR}"
  ns4="${prefix}4"
  spawn_incus_and_join_cluster "${ns4}" "${bridge}" "${cert}" 4 1 "${INCUS_FOUR_DIR}" "${INCUS_ONE_DIR}"

  # Spawn a fifth node, using non-database node4 as join target.
  setup_clustering_netns 5
  INCUS_FIVE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_FIVE_DIR}"
  ns5="${prefix}5"
  spawn_incus_and_join_cluster "${ns5}" "${bridge}" "${cert}" 5 4 "${INCUS_FIVE_DIR}" "${INCUS_ONE_DIR}"

  # List all nodes, using clients points to different nodes and
  # checking which are database nodes and which are database-standby nodes.
  INCUS_DIR="${INCUS_THREE_DIR}" incus cluster list
  INCUS_DIR="${INCUS_TWO_DIR}" incus cluster show node1 | grep -q "\- database-leader$"
  INCUS_DIR="${INCUS_THREE_DIR}" incus cluster list | grep -Fc "database-standby" | grep -Fx 2
  INCUS_DIR="${INCUS_FIVE_DIR}" incus cluster list | grep -Fc "database " | grep -Fx 3

  # Show a single node
  INCUS_DIR="${INCUS_TWO_DIR}" incus cluster show node5 | grep -q "node5"

  # Client certificate are shared across all nodes.
  token="$(INCUS_DIR=${INCUS_ONE_DIR} incus config trust add foo -q)"
  incus remote add cluster 10.1.1.101:8443 --accept-certificate --token "${token}"
  incus remote set-url cluster https://10.1.1.102:8443
  incus network list cluster: | grep -q "${bridge}"
  incus remote remove cluster

  # Check info for single node (from local and remote node).
  INCUS_DIR="${INCUS_FIVE_DIR}" incus cluster info node5
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster info node5

  # Disable image replication
  INCUS_DIR="${INCUS_ONE_DIR}" incus config set cluster.images_minimal_replica 1

  # Shutdown a database node, and wait a few seconds so it will be
  # detected as down.
  INCUS_DIR="${INCUS_ONE_DIR}" incus config set cluster.offline_threshold 11
  INCUS_DIR="${INCUS_THREE_DIR}" incus admin shutdown
  sleep 12
  INCUS_DIR="${INCUS_TWO_DIR}" incus cluster list
  INCUS_DIR="${INCUS_TWO_DIR}" incus cluster show node3 | grep -q "status: Offline"

  # Gracefully remove a node and check trust certificate is removed.
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster list | grep node4
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global 'SELECT name FROM certificates WHERE type = 2' | grep node4
  INCUS_DIR="${INCUS_TWO_DIR}" incus cluster remove node4
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus cluster list | grep node4 || false
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global 'SELECT name FROM certificates WHERE type = 2' | grep node4 || false

  # The node isn't clustered anymore.
  ! INCUS_DIR="${INCUS_FOUR_DIR}" incus cluster list || false

  # Generate a join token for the sixth node.
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster list
  token=$(INCUS_DIR="${INCUS_ONE_DIR}" incus cluster add node6 --quiet)

  # Check token is associated to correct name.
  INCUS_DIR="${INCUS_TWO_DIR}" incus cluster list-tokens | grep node6 | grep "${token}"

  # Spawn a sixth node, using join token.
  setup_clustering_netns 6
  INCUS_SIX_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_SIX_DIR}"
  ns6="${prefix}6"

  # shellcheck disable=SC2034
  spawn_incus_and_join_cluster "${ns6}" "${bridge}" "${cert}" 6 2 "${INCUS_SIX_DIR}" "${token}"

  # Check token has been deleted after join.
  INCUS_DIR="${INCUS_TWO_DIR}" incus cluster list-tokens
  ! INCUS_DIR="${INCUS_TWO_DIR}" incus cluster list-tokens | grep node6 || false

  # Generate a join token for a seventh node
  token=$(INCUS_DIR="${INCUS_ONE_DIR}" incus cluster add node7 --quiet)

  # Check token is associated to correct name
  INCUS_DIR="${INCUS_TWO_DIR}" incus cluster list-tokens | grep node7 | grep "${token}"

  # Revoke the token
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster revoke-token node7 | tail -n 1

  # Check token has been deleted
  INCUS_DIR="${INCUS_TWO_DIR}" incus cluster list-tokens
  ! INCUS_DIR="${INCUS_TWO_DIR}" incus cluster list-tokens | grep node7 || false

  # Set cluster token expiry to 30 seconds
  INCUS_DIR="${INCUS_ONE_DIR}" incus config set cluster.join_token_expiry=30S

  # Generate a join token for an eigth and ninth node
  token_valid=$(INCUS_DIR="${INCUS_ONE_DIR}" incus cluster add node8 --quiet)

  # Spawn an eigth node, using join token.
  setup_clustering_netns 8
  INCUS_EIGHT_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_EIGHT_DIR}"
  ns8="${prefix}8"

  # shellcheck disable=SC2034
  spawn_incus_and_join_cluster "${ns8}" "${bridge}" "${cert}" 8 2 "${INCUS_EIGHT_DIR}" "${token_valid}"

  # This will cause the token to expire
  INCUS_DIR="${INCUS_ONE_DIR}" incus config set cluster.join_token_expiry=5S
  token_expired=$(INCUS_DIR="${INCUS_ONE_DIR}" incus cluster add node9 --quiet)
  sleep 6

  # Spawn a ninth node, using join token.
  setup_clustering_netns 9
  INCUS_NINE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_NINE_DIR}"
  ns9="${prefix}9"

  # shellcheck disable=SC2034
  ! spawn_incus_and_join_cluster "${ns9}" "${bridge}" "${cert}" 9 2 "${INCUS_NINE_DIR}" "${token_expired}" || false

  # Unset join_token_expiry which will set it to the default value of 3h
  INCUS_DIR="${INCUS_ONE_DIR}" incus config unset cluster.join_token_expiry

  INCUS_DIR="${INCUS_NINE_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_EIGHT_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_SIX_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_FIVE_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_FOUR_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_TWO_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin shutdown
  sleep 0.5
  rm -f "${INCUS_NINE_DIR}/unix.socket"
  rm -f "${INCUS_EIGHT_DIR}/unix.socket"
  rm -f "${INCUS_SIX_DIR}/unix.socket"
  rm -f "${INCUS_FIVE_DIR}/unix.socket"
  rm -f "${INCUS_FOUR_DIR}/unix.socket"
  rm -f "${INCUS_THREE_DIR}/unix.socket"
  rm -f "${INCUS_TWO_DIR}/unix.socket"
  rm -f "${INCUS_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_incus "${INCUS_ONE_DIR}"
  kill_incus "${INCUS_TWO_DIR}"
  kill_incus "${INCUS_THREE_DIR}"
  kill_incus "${INCUS_FOUR_DIR}"
  kill_incus "${INCUS_FIVE_DIR}"
  kill_incus "${INCUS_SIX_DIR}"
  kill_incus "${INCUS_EIGHT_DIR}"
  kill_incus "${INCUS_NINE_DIR}"
}

test_clustering_containers() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  INCUS_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_TWO_DIR}"
  ns2="${prefix}2"
  spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}"

  # Spawn a third node
  setup_clustering_netns 3
  INCUS_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_THREE_DIR}"
  ns3="${prefix}3"
  spawn_incus_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${INCUS_THREE_DIR}" "${INCUS_ONE_DIR}"

  # Init a container on node2, using a client connected to node1
  INCUS_DIR="${INCUS_TWO_DIR}" ensure_import_testimage
  INCUS_DIR="${INCUS_ONE_DIR}" incus init --target node2 testimage foo

  # The container is visible through both nodes
  INCUS_DIR="${INCUS_ONE_DIR}" incus list | grep foo | grep -q STOPPED
  INCUS_DIR="${INCUS_ONE_DIR}" incus list | grep foo | grep -q node2
  INCUS_DIR="${INCUS_TWO_DIR}" incus list | grep foo | grep -q STOPPED

  # A Location: field indicates on which node the container is running
  INCUS_DIR="${INCUS_ONE_DIR}" incus info foo | grep -q "Location: node2"

  # Start the container via node1
  INCUS_DIR="${INCUS_ONE_DIR}" incus start foo
  INCUS_DIR="${INCUS_TWO_DIR}" incus info foo | grep -q "Status: RUNNING"
  INCUS_DIR="${INCUS_ONE_DIR}" incus list | grep foo | grep -q RUNNING

  # Trying to delete a node which has container results in an error
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus cluster remove node2 || false

  # Exec a command in the container via node1
  INCUS_DIR="${INCUS_ONE_DIR}" incus exec foo -- ls / | grep -qxF proc

  # Pull, push and delete files from the container via node1
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus file pull foo/non-existing-file "${TEST_DIR}/non-existing-file" || false
  mkdir "${TEST_DIR}/hello-world"
  echo "hello world" > "${TEST_DIR}/hello-world/text"
  INCUS_DIR="${INCUS_ONE_DIR}" incus file push "${TEST_DIR}/hello-world/text" foo/hello-world-text
  INCUS_DIR="${INCUS_ONE_DIR}" incus file pull foo/hello-world-text "${TEST_DIR}/hello-world-text"
  grep -q "hello world" "${TEST_DIR}/hello-world-text"
  rm "${TEST_DIR}/hello-world-text"
  INCUS_DIR="${INCUS_ONE_DIR}" incus file push --recursive "${TEST_DIR}/hello-world" foo/
  rm -r "${TEST_DIR}/hello-world"
  INCUS_DIR="${INCUS_ONE_DIR}" incus file pull --recursive foo/hello-world "${TEST_DIR}"
  grep -q "hello world" "${TEST_DIR}/hello-world/text"
  rm -r "${TEST_DIR}/hello-world"
  INCUS_DIR="${INCUS_ONE_DIR}" incus file delete foo/hello-world/text
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus file pull foo/hello-world/text "${TEST_DIR}/hello-world-text" || false

  # Stop the container via node1
  INCUS_DIR="${INCUS_ONE_DIR}" incus stop foo --force

  # Rename the container via node1
  INCUS_DIR="${INCUS_ONE_DIR}" incus rename foo foo2
  INCUS_DIR="${INCUS_TWO_DIR}" incus list | grep -q foo2
  INCUS_DIR="${INCUS_ONE_DIR}" incus rename foo2 foo

  # Show lxc.log via node1
  INCUS_DIR="${INCUS_ONE_DIR}" incus info --show-log foo | grep -q Log

  # Create, rename and delete a snapshot of the container via node1
  INCUS_DIR="${INCUS_ONE_DIR}" incus snapshot create foo foo-bak
  INCUS_DIR="${INCUS_ONE_DIR}" incus info foo | grep -q foo-bak
  INCUS_DIR="${INCUS_ONE_DIR}" incus snapshot rename foo foo-bak foo-bak-2
  INCUS_DIR="${INCUS_ONE_DIR}" incus snapshot delete foo foo-bak-2
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus info foo | grep -q foo-bak-2 || false

  # Export from node1 the image that was imported on node2
  INCUS_DIR="${INCUS_ONE_DIR}" incus image export testimage "${TEST_DIR}/testimage"
  rm "${TEST_DIR}/testimage.tar.xz"

  # Create a container on node1 using the image that was stored on
  # node2.
  INCUS_DIR="${INCUS_TWO_DIR}" incus launch --target node1 testimage bar
  INCUS_DIR="${INCUS_TWO_DIR}" incus stop bar --force
  INCUS_DIR="${INCUS_ONE_DIR}" incus delete bar
  ! INCUS_DIR="${INCUS_TWO_DIR}" incus list | grep -q bar || false

  # Create a container on node1 using a snapshot from node2.
  INCUS_DIR="${INCUS_ONE_DIR}" incus snapshot create foo foo-bak
  INCUS_DIR="${INCUS_TWO_DIR}" incus copy foo/foo-bak bar --target node1
  INCUS_DIR="${INCUS_TWO_DIR}" incus info bar | grep -q "Location: node1"
  INCUS_DIR="${INCUS_THREE_DIR}" incus delete bar

  # Copy the container on node2 to node3, using a client connected to
  # node1.
  INCUS_DIR="${INCUS_ONE_DIR}" incus copy foo bar --target node3
  INCUS_DIR="${INCUS_TWO_DIR}" incus info bar | grep -q "Location: node3"

  # Move the container on node3 to node1, using a client connected to
  # node2 and a different container name than the original one. The
  # volatile.apply_template config key is preserved.
  apply_template1=$(INCUS_DIR="${INCUS_TWO_DIR}" incus config get bar volatile.apply_template)

  INCUS_DIR="${INCUS_TWO_DIR}" incus move bar egg --target node2
  INCUS_DIR="${INCUS_ONE_DIR}" incus info egg | grep -q "Location: node2"
  apply_template2=$(INCUS_DIR="${INCUS_TWO_DIR}" incus config get egg volatile.apply_template)
  [ "${apply_template1}" =  "${apply_template2}" ] || false

  # Move back to node3 the container on node1, keeping the same name.
  apply_template1=$(INCUS_DIR="${INCUS_TWO_DIR}" incus config get egg volatile.apply_template)
  INCUS_DIR="${INCUS_TWO_DIR}" incus move egg --target node3
  INCUS_DIR="${INCUS_ONE_DIR}" incus info egg | grep -q "Location: node3"
  apply_template2=$(INCUS_DIR="${INCUS_TWO_DIR}" incus config get egg volatile.apply_template)
  [ "${apply_template1}" =  "${apply_template2}" ] || false

  if command -v criu >/dev/null 2>&1; then
    # If CRIU supported, then try doing a live move using same name,
    # as CRIU doesn't work when moving to a different name.
    INCUS_DIR="${INCUS_TWO_DIR}" incus config set egg raw.lxc=lxc.console.path=none
    INCUS_DIR="${INCUS_TWO_DIR}" incus start egg
    INCUS_DIR="${INCUS_TWO_DIR}" incus exec egg -- umount /dev/.incus-mounts
    INCUS_DIR="${INCUS_TWO_DIR}" incus move egg --target node1
    INCUS_DIR="${INCUS_ONE_DIR}" incus info egg | grep -q "Location: node1"
    INCUS_DIR="${INCUS_TWO_DIR}" incus move egg --target node3 --stateless
    INCUS_DIR="${INCUS_TWO_DIR}" incus stop -f egg
  fi

  # Create backup and attempt to move container. Move should fail and container should remain on node1.
  INCUS_DIR="${INCUS_THREE_DIR}" incus query -X POST --wait -d '{\"name\":\"foo\"}' /1.0/instances/egg/backups
  ! INCUS_DIR="${INCUS_THREE_DIR}" incus move egg --target node2 || false
  INCUS_DIR="${INCUS_THREE_DIR}" incus info egg | grep -q "Location: node3"

  INCUS_DIR="${INCUS_THREE_DIR}" incus delete egg

  # Delete the network now, since we're going to shutdown node2 and it
  # won't be possible afterwise.
  INCUS_DIR="${INCUS_TWO_DIR}" incus network delete "${bridge}"

  # Shutdown node 2, wait for it to be considered offline, and list
  # containers.
  INCUS_DIR="${INCUS_THREE_DIR}" incus config set cluster.offline_threshold 11
  INCUS_DIR="${INCUS_TWO_DIR}" incus admin shutdown
  sleep 12
  INCUS_DIR="${INCUS_ONE_DIR}" incus list | grep foo | grep -q ERROR

  # Start a container without specifying any target. It will be placed
  # on node1 since node2 is offline and both node1 and node3 have zero
  # containers, but node1 has a lower node ID.
  INCUS_DIR="${INCUS_THREE_DIR}" incus launch testimage bar
  INCUS_DIR="${INCUS_THREE_DIR}" incus info bar | grep -q "Location: node1"

  # Start a container without specifying any target. It will be placed
  # on node3 since node2 is offline and node1 already has a container.
  INCUS_DIR="${INCUS_THREE_DIR}" incus launch testimage egg
  INCUS_DIR="${INCUS_THREE_DIR}" incus info egg | grep -q "Location: node3"

  INCUS_DIR="${INCUS_ONE_DIR}" incus stop egg --force
  INCUS_DIR="${INCUS_ONE_DIR}" incus stop bar --force

  INCUS_DIR="${INCUS_THREE_DIR}" incus admin shutdown
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

test_clustering_storage() {
  # shellcheck disable=2039,3043
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

  # The state of the preseeded storage pool shows up as CREATED
  INCUS_DIR="${INCUS_ONE_DIR}" incus storage list | grep data | grep -q CREATED

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  INCUS_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_TWO_DIR}"
  ns2="${prefix}2"
  spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}" "${poolDriver}"

  # The state of the preseeded storage pool is still CREATED
  INCUS_DIR="${INCUS_ONE_DIR}" incus storage list | grep data | grep -q CREATED

  # Check both nodes show preseeded storage pool created.
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'data' AND nodes.name = 'node1'" | grep "| node1 | 1     |"
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'data' AND nodes.name = 'node2'" | grep "| node2 | 1     |"

  # Trying to pass config values other than 'source' results in an error
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus storage create pool1 dir source=/foo size=123 --target node1 || false

  # Test storage pool node state tracking using a dir pool.
  if [ "${poolDriver}" = "dir" ]; then
    # Create pending nodes.
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage create pool1 "${poolDriver}" --target node1
    INCUS_DIR="${INCUS_TWO_DIR}" incus storage create pool1 "${poolDriver}" --target node2
    INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'pool1' AND nodes.name = 'node1'" | grep "| node1 | 0     |"
    INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'pool1' AND nodes.name = 'node2'" | grep "| node2 | 0     |"

    # Modify first pending node with invalid config and check it fails and all nodes are pending.
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage set pool1 source=/tmp/not/exist --target node1
    ! INCUS_DIR="${INCUS_ONE_DIR}" incus storage create pool1 "${poolDriver}" || false
    INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'pool1' AND nodes.name = 'node1'" | grep "| node1 | 0     |"
    INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'pool1' AND nodes.name = 'node2'" | grep "| node2 | 0     |"

    # Run create on second node, so it succeeds and then fails notifying first node.
    ! INCUS_DIR="${INCUS_TWO_DIR}" incus storage create pool1 "${poolDriver}" || false
    INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'pool1' AND nodes.name = 'node1'" | grep "| node1 | 0     |"
    INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'pool1' AND nodes.name = 'node2'" | grep "| node2 | 1     |"

    # Check we cannot update global config while in pending state.
    ! INCUS_DIR="${INCUS_ONE_DIR}" incus storage set pool1 rsync.bwlimit 10 || false
    ! INCUS_DIR="${INCUS_TWO_DIR}" incus storage set pool1 rsync.bwlimit 10 || false

    # Check can delete pending pool and created nodes are cleaned up.
    INCUS_DIR="${INCUS_TWO_DIR}" incus storage show pool1 --target=node2
    INCUS_TWO_SOURCE="$(INCUS_DIR="${INCUS_TWO_DIR}" incus storage get pool1 source --target=node2)"
    stat "${INCUS_TWO_SOURCE}/containers"
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage delete pool1
    ! stat "${INCUS_TWO_SOURCE}/containers" || false

    # Create new partially created pool and check we can fix it.
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage create pool1 "${poolDriver}" source=/tmp/not/exist --target node1
    INCUS_DIR="${INCUS_TWO_DIR}" incus storage create pool1 "${poolDriver}" --target node2
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage show pool1 | grep status: | grep -q Pending
    ! INCUS_DIR="${INCUS_TWO_DIR}" incus storage create pool1 "${poolDriver}" || false
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage show pool1 | grep status: | grep -q Errored
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage unset pool1 source --target node1
    ! INCUS_DIR="${INCUS_TWO_DIR}" incus storage create pool1 "${poolDriver}" rsync.bwlimit=1000 || false # Check global config is rejected on re-create.
    INCUS_DIR="${INCUS_TWO_DIR}" incus storage create pool1 "${poolDriver}"
    ! INCUS_DIR="${INCUS_TWO_DIR}" incus storage create pool1 "${poolDriver}" || false # Check re-create after successful create is rejected.
    INCUS_ONE_SOURCE="$(INCUS_DIR="${INCUS_ONE_DIR}" incus storage get pool1 source --target=node1)"
    INCUS_TWO_SOURCE="$(INCUS_DIR="${INCUS_TWO_DIR}" incus storage get pool1 source --target=node2)"
    stat "${INCUS_ONE_SOURCE}/containers"
    stat "${INCUS_TWO_SOURCE}/containers"

    # Check both nodes marked created.
    INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'pool1' AND nodes.name = 'node1'" | grep "| node1 | 1     |"
    INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global "SELECT nodes.name,storage_pools_nodes.state FROM nodes JOIN storage_pools_nodes ON storage_pools_nodes.node_id = nodes.id JOIN storage_pools ON storage_pools.id = storage_pools_nodes.storage_pool_id WHERE storage_pools.name = 'pool1' AND nodes.name = 'node2'" | grep "| node2 | 1     |"

    # Check copying storage volumes works.
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage volume create pool1 vol1 --target=node1
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage volume copy pool1/vol1 pool1/vol1 --target=node1 --destination-target=node2
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage volume copy pool1/vol1 pool1/vol1 --target=node1 --destination-target=node2 --refresh

    # Check renaming storage volume works.
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage volume create pool1 vol2 --target=node1
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage volume move pool1/vol2 pool1/vol3 --target=node1
    INCUS_DIR="${INCUS_TWO_DIR}" incus storage volume show pool1 vol3 | grep -q node1
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage volume move pool1/vol3 pool1/vol2 --target=node1 --destination-target=node2
    INCUS_DIR="${INCUS_TWO_DIR}" incus storage volume show pool1 vol2 | grep -q node2
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage volume rename pool1 vol2 vol3 --target=node2
    INCUS_DIR="${INCUS_TWO_DIR}" incus storage volume show pool1 vol3 | grep -q node2

    # Delete pool and check cleaned up.
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage volume delete pool1 vol1 --target=node1
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage volume delete pool1 vol1 --target=node2
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage volume delete pool1 vol3 --target=node2
    INCUS_DIR="${INCUS_TWO_DIR}" incus storage delete pool1
    ! stat "${INCUS_ONE_SOURCE}/containers" || false
    ! stat "${INCUS_TWO_SOURCE}/containers" || false
  fi

  # Set up node-specific storage pool keys for the selected backend.
  driver_config=""
  if [ "${poolDriver}" = "btrfs" ] || [ "${poolDriver}" = "lvm" ] || [ "${poolDriver}" = "zfs" ]; then
      driver_config="size=1GiB"
  fi

  if [ "${poolDriver}" = "ceph" ]; then
      driver_config="source=incustest-$(basename "${TEST_DIR}")-pool1"
  fi

  # Define storage pools on the two nodes
  driver_config_node1="${driver_config}"
  driver_config_node2="${driver_config}"

  if [ "${poolDriver}" = "zfs" ]; then
      driver_config_node1="${driver_config_node1} zfs.pool_name=pool1-$(basename "${TEST_DIR}")-${ns1}"
      driver_config_node2="${driver_config_node1} zfs.pool_name=pool1-$(basename "${TEST_DIR}")-${ns2}"
  fi

  if [ "${poolDriver}" = "lvm" ]; then
      driver_config_node1="${driver_config_node1} lvm.vg_name=pool1-$(basename "${TEST_DIR}")-${ns1}"
      driver_config_node2="${driver_config_node1} lvm.vg_name=pool1-$(basename "${TEST_DIR}")-${ns2}"
  fi

  if [ -n "${driver_config_node1}" ]; then
    # shellcheck disable=SC2086
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage create pool1 "${poolDriver}" ${driver_config_node1} --target node1
  else
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage create pool1 "${poolDriver}" --target node1
  fi

  INCUS_DIR="${INCUS_TWO_DIR}" incus storage show pool1 | grep -q node1
  ! INCUS_DIR="${INCUS_TWO_DIR}" incus storage show pool1 | grep -q node2 || false
  if [ -n "${driver_config_node2}" ]; then
    # shellcheck disable=SC2086
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage create pool1 "${poolDriver}" ${driver_config_node2} --target node2
  else
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage create pool1 "${poolDriver}" --target node2
  fi
  INCUS_DIR="${INCUS_ONE_DIR}" incus storage show pool1 | grep status: | grep -q Pending

  # A container can't be created when associated with a pending pool.
  INCUS_DIR="${INCUS_TWO_DIR}" ensure_import_testimage
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus init --target node2 -s pool1 testimage bar || false
  INCUS_DIR="${INCUS_ONE_DIR}" incus image delete testimage

  # The source config key is not legal for the final pool creation
  if [ "${poolDriver}" = "dir" ]; then
    ! INCUS_DIR="${INCUS_ONE_DIR}" incus storage create pool1 dir source=/foo || false
  fi

  # Create the storage pool
  if [ "${poolDriver}" = "lvm" ]; then
      INCUS_DIR="${INCUS_TWO_DIR}" incus storage create pool1 "${poolDriver}" volume.size=25MiB
  elif [ "${poolDriver}" = "ceph" ]; then
      INCUS_DIR="${INCUS_TWO_DIR}" incus storage create pool1 "${poolDriver}" volume.size=25MiB ceph.osd.pg_num=16
  else
      INCUS_DIR="${INCUS_TWO_DIR}" incus storage create pool1 "${poolDriver}"
  fi
  INCUS_DIR="${INCUS_ONE_DIR}" incus storage show pool1 | grep status: | grep -q Created

  # The 'source' config key is omitted when showing the cluster
  # configuration, and included when showing the node-specific one.
  ! INCUS_DIR="${INCUS_TWO_DIR}" incus storage show pool1 | grep -q source || false
  source1="$(basename "${INCUS_ONE_DIR}")"
  source2="$(basename "${INCUS_TWO_DIR}")"
  if [ "${poolDriver}" = "ceph" ]; then
    # For ceph volume the source field is the name of the underlying ceph pool
    source1="incustest-$(basename "${TEST_DIR}")"
    source2="${source1}"
  fi
  INCUS_DIR="${INCUS_ONE_DIR}" incus storage show pool1 --target node1 | grep source | grep -q "${source1}"
  INCUS_DIR="${INCUS_ONE_DIR}" incus storage show pool1 --target node2 | grep source | grep -q "${source2}"

  # Update the storage pool
  if [ "${poolDriver}" = "dir" ]; then
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage set pool1 rsync.bwlimit 10
    INCUS_DIR="${INCUS_TWO_DIR}" incus storage show pool1 | grep rsync.bwlimit | grep -q 10
    INCUS_DIR="${INCUS_TWO_DIR}" incus storage unset pool1 rsync.bwlimit
    ! INCUS_DIR="${INCUS_ONE_DIR}" incus storage show pool1 | grep -q rsync.bwlimit || false
  fi

  if [ "${poolDriver}" = "ceph" ]; then
    # Test migration of ceph-based containers
    INCUS_DIR="${INCUS_TWO_DIR}" ensure_import_testimage
    INCUS_DIR="${INCUS_ONE_DIR}" incus launch --target node2 -s pool1 testimage foo

    # The container can't be moved if it's running
    ! INCUS_DIR="${INCUS_TWO_DIR}" incus move foo --target node1 || false

    # Stop the container and create a snapshot
    INCUS_DIR="${INCUS_ONE_DIR}" incus stop foo --force
    INCUS_DIR="${INCUS_ONE_DIR}" incus snapshot create foo snap-test

    # Move the container to node1
    INCUS_DIR="${INCUS_TWO_DIR}" incus move foo --target node1
    INCUS_DIR="${INCUS_TWO_DIR}" incus info foo | grep -q "Location: node1"
    INCUS_DIR="${INCUS_TWO_DIR}" incus info foo | grep -q "snap-test"

    # Start and stop the container on its new node1 host
    INCUS_DIR="${INCUS_TWO_DIR}" incus start foo
    INCUS_DIR="${INCUS_TWO_DIR}" incus stop foo --force

    # Init a new container on node2 using the snapshot on node1
    INCUS_DIR="${INCUS_ONE_DIR}" incus copy foo/snap-test egg --target node2
    INCUS_DIR="${INCUS_TWO_DIR}" incus start egg
    INCUS_DIR="${INCUS_ONE_DIR}" incus stop egg --force
    INCUS_DIR="${INCUS_ONE_DIR}" incus delete egg
  fi

  # If the driver has the same per-node storage pool config (e.g. size), make sure it's included in the
  # member_config, and actually added to a joining node so we can validate it.
  if [ "${poolDriver}" = "zfs" ] || [ "${poolDriver}" = "btrfs" ] || [ "${poolDriver}" = "ceph" ] || [ "${poolDriver}" = "lvm" ]; then
    # Spawn a third node
    setup_clustering_netns 3
    INCUS_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
    chmod +x "${INCUS_THREE_DIR}"
    ns3="${prefix}3"
    INCUS_NETNS="${ns3}" spawn_incus "${INCUS_THREE_DIR}" false

    key=$(echo "${driver_config}" | cut -d'=' -f1)
    value=$(echo "${driver_config}" | cut -d'=' -f2-)

    # Set member_config to match `spawn_incus_and_join_cluster` for 'data' and `driver_config` for 'pool1'.
    member_config="{\"entity\": \"storage-pool\",\"name\":\"pool1\",\"key\":\"${key}\",\"value\":\"${value}\"}"
    if [ "${poolDriver}" = "zfs" ] || [ "${poolDriver}" = "btrfs" ] || [ "${poolDriver}" = "lvm" ] ; then
      member_config="{\"entity\": \"storage-pool\",\"name\":\"data\",\"key\":\"size\",\"value\":\"1GiB\"},${member_config}"
    fi

    # Manually send the join request.
    cert=$(sed ':a;N;$!ba;s/\n/\\n/g' "${INCUS_ONE_DIR}/cluster.crt")
    token="$(incus cluster add node3 --quiet)"
    op=$(curl --unix-socket "${INCUS_THREE_DIR}/unix.socket" -X PUT "incus/1.0/cluster" -d "{\"server_name\":\"node3\",\"enabled\":true,\"member_config\":[${member_config}],\"server_address\":\"10.1.1.103:8443\",\"cluster_address\":\"10.1.1.101:8443\",\"cluster_certificate\":\"${cert}\",\"cluster_token\":\"${token}\"}" | jq -r .operation)
    curl --unix-socket "${INCUS_THREE_DIR}/unix.socket" "incus${op}/wait"

    # Ensure that node-specific config appears on all nodes,
    # regardless of the pool being created before or after the node joined.
    for n in node1 node2 node3 ; do
      INCUS_DIR="${INCUS_ONE_DIR}" incus storage get pool1 "${key}" --target "${n}" | grep -q "${value}"
    done

    # Other storage backends will be finished with the third node, so we can remove it.
    if [ "${poolDriver}" != "ceph" ]; then
      INCUS_DIR="${INCUS_ONE_DIR}" incus cluster remove node3 --yes
    fi
  fi

  if [ "${poolDriver}" = "ceph" ]; then

    # Move the container to node3, renaming it
    INCUS_DIR="${INCUS_TWO_DIR}" incus move foo bar --target node3
    INCUS_DIR="${INCUS_TWO_DIR}" incus info bar | grep -q "Location: node3"
    INCUS_DIR="${INCUS_ONE_DIR}" incus info bar | grep -q "snap-test"

    # Shutdown node 3, and wait for it to be considered offline.
    INCUS_DIR="${INCUS_THREE_DIR}" incus config set cluster.offline_threshold 11
    INCUS_DIR="${INCUS_THREE_DIR}" incus admin shutdown
    sleep 12

    # Move the container back to node2, even if node3 is offline
    INCUS_DIR="${INCUS_ONE_DIR}" incus move bar --target node2
    INCUS_DIR="${INCUS_ONE_DIR}" incus info bar | grep -q "Location: node2"
    INCUS_DIR="${INCUS_TWO_DIR}" incus info bar | grep -q "snap-test"

    # Start and stop the container on its new node2 host
    INCUS_DIR="${INCUS_TWO_DIR}" incus start bar
    INCUS_DIR="${INCUS_ONE_DIR}" incus stop bar --force

    INCUS_DIR="${INCUS_ONE_DIR}" incus cluster remove node3 --force --yes

    INCUS_DIR="${INCUS_ONE_DIR}" incus delete bar

    # Attach a custom volume to a container on node1
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage volume create pool1 v1
    INCUS_DIR="${INCUS_ONE_DIR}" incus init --target node1 -s pool1 testimage baz
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage volume attach pool1 custom/v1 baz testDevice /opt

    # Trying to attach a custom volume to a container on another node fails
    INCUS_DIR="${INCUS_TWO_DIR}" incus init --target node2 -s pool1 testimage buz
    ! INCUS_DIR="${INCUS_TWO_DIR}" incus storage volume attach pool1 custom/v1 buz testDevice /opt || false

    # Create an unrelated volume and rename it on a node which differs from the
    # one running the container (issue #6435).
    INCUS_DIR="${INCUS_TWO_DIR}" incus storage volume create pool1 v2
    INCUS_DIR="${INCUS_TWO_DIR}" incus storage volume rename pool1 v2 v2-renamed
    INCUS_DIR="${INCUS_TWO_DIR}" incus storage volume delete pool1 v2-renamed

    INCUS_DIR="${INCUS_ONE_DIR}" incus storage volume detach pool1 v1 baz

    INCUS_DIR="${INCUS_ONE_DIR}" incus storage volume delete pool1 v1
    INCUS_DIR="${INCUS_ONE_DIR}" incus delete baz
    INCUS_DIR="${INCUS_ONE_DIR}" incus delete buz

    INCUS_DIR="${INCUS_ONE_DIR}" incus image delete testimage
  fi

  # Test migration of zfs/btrfs-based containers
  if [ "${poolDriver}" = "zfs" ] || [ "${poolDriver}" = "btrfs" ]; then
    # Launch a container on node2
    INCUS_DIR="${INCUS_TWO_DIR}" ensure_import_testimage
    INCUS_DIR="${INCUS_ONE_DIR}" incus launch --target node2 testimage foo
    INCUS_DIR="${INCUS_ONE_DIR}" incus info foo | grep -q "Location: node2"

    # Stop the container and move it to node1
    INCUS_DIR="${INCUS_ONE_DIR}" incus stop foo --force
    INCUS_DIR="${INCUS_TWO_DIR}" incus move foo bar --target node1
    INCUS_DIR="${INCUS_ONE_DIR}" incus info bar | grep -q "Location: node1"

    # Start and stop the migrated container on node1
    INCUS_DIR="${INCUS_TWO_DIR}" incus start bar
    INCUS_DIR="${INCUS_ONE_DIR}" incus stop bar --force

    # Rename the container locally on node1
    INCUS_DIR="${INCUS_TWO_DIR}" incus rename bar foo
    INCUS_DIR="${INCUS_ONE_DIR}" incus info foo | grep -q "Location: node1"

    # Copy the container without specifying a target, it will be placed on node2
    # since it's the one with the least number of containers (0 vs 1)
    sleep 6 # Wait for pending operations to be removed from the database
    INCUS_DIR="${INCUS_ONE_DIR}" incus copy foo bar
    INCUS_DIR="${INCUS_ONE_DIR}" incus info bar | grep -q "Location: node2"

    # Start and stop the copied container on node2
    INCUS_DIR="${INCUS_TWO_DIR}" incus start bar
    INCUS_DIR="${INCUS_ONE_DIR}" incus stop bar --force

    # Purge the containers
    INCUS_DIR="${INCUS_ONE_DIR}" incus delete bar
    INCUS_DIR="${INCUS_ONE_DIR}" incus delete foo

    # Delete the image too.
    INCUS_DIR="${INCUS_ONE_DIR}" incus image delete testimage
  fi

  # Delete the storage pool
  INCUS_DIR="${INCUS_ONE_DIR}" incus storage delete pool1
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus storage list | grep -q pool1 || false

  if [ "${poolDriver}" != "ceph" ]; then
    # Create a volume on node1
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage volume create data web
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage volume list data | grep web | grep -q node1
    INCUS_DIR="${INCUS_TWO_DIR}" incus storage volume list data | grep web | grep -q node1

    # Since the volume name is unique to node1, it's possible to show, rename,
    # get the volume without specifying the --target parameter.
    INCUS_DIR="${INCUS_TWO_DIR}" incus storage volume show data web | grep -q "location: node1"
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage volume rename data web webbaz
    INCUS_DIR="${INCUS_TWO_DIR}" incus storage volume rename data webbaz web
    INCUS_DIR="${INCUS_TWO_DIR}" incus storage volume get data web size

    # Create another volume on node2 with the same name of the one on
    # node1.
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage volume create --target node2 data web

    # Trying to show, rename or delete the web volume without --target
    # fails, because it's not unique.
    ! INCUS_DIR="${INCUS_TWO_DIR}" incus storage volume show data web || false
    ! INCUS_DIR="${INCUS_TWO_DIR}" incus storage volume rename data web webbaz || false
    ! INCUS_DIR="${INCUS_TWO_DIR}" incus storage volume delete data web || false

    # Specifying the --target parameter shows, renames and deletes the
    # proper volume.
    INCUS_DIR="${INCUS_TWO_DIR}" incus storage volume show --target node1 data web | grep -q "location: node1"
    INCUS_DIR="${INCUS_TWO_DIR}" incus storage volume show --target node2 data web | grep -q "location: node2"
    INCUS_DIR="${INCUS_TWO_DIR}" incus storage volume rename --target node1 data web webbaz
    INCUS_DIR="${INCUS_TWO_DIR}" incus storage volume rename --target node2 data web webbaz
    INCUS_DIR="${INCUS_TWO_DIR}" incus storage volume delete --target node2 data webbaz

    # Since now there's only one volume in the pool left named webbaz,
    # it's possible to delete it without specifying --target.
    INCUS_DIR="${INCUS_TWO_DIR}" incus storage volume delete data webbaz
  fi

  printf 'config: {}\ndevices: {}' | INCUS_DIR="${INCUS_ONE_DIR}" incus profile edit default
  INCUS_DIR="${INCUS_TWO_DIR}" incus storage delete data

  INCUS_DIR="${INCUS_TWO_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin shutdown
  sleep 0.5
  rm -f "${INCUS_TWO_DIR}/unix.socket"
  rm -f "${INCUS_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_incus "${INCUS_ONE_DIR}"
  kill_incus "${INCUS_TWO_DIR}"
  if [ -n "${INCUS_THREE_DIR:-}" ]; then
    kill_incus "${INCUS_THREE_DIR}"
  fi
}

# On a single-node cluster storage pools can be created either with the
# two-stage process required multi-node clusters, or directly with the normal
# procedure for non-clustered daemons.
test_clustering_storage_single_node() {
  # shellcheck disable=2039,3043
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

  # Create a pending storage pool on the node.
  driver_config=""
  if [ "${poolDriver}" = "btrfs" ]; then
      driver_config="size=1GiB"
  fi
  if [ "${poolDriver}" = "zfs" ]; then
      driver_config="size=1GiB"
  fi
  if [ "${poolDriver}" = "ceph" ]; then
      driver_config="source=incustest-$(basename "${TEST_DIR}")-pool1"
  fi
  driver_config_node="${driver_config}"
  if [ "${poolDriver}" = "zfs" ]; then
      driver_config_node="${driver_config_node} zfs.pool_name=pool1-$(basename "${TEST_DIR}")-${ns1}"
  fi

  if [ -n "${driver_config_node}" ]; then
    # shellcheck disable=SC2086
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage create pool1 "${poolDriver}" ${driver_config_node} --target node1
  else
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage create pool1 "${poolDriver}" --target node1
  fi

  # Finalize the storage pool creation
  INCUS_DIR="${INCUS_ONE_DIR}" incus storage create pool1 "${poolDriver}"

  INCUS_DIR="${INCUS_ONE_DIR}" incus storage show pool1 | grep status: | grep -q Created

  # Delete the storage pool
  INCUS_DIR="${INCUS_ONE_DIR}" incus storage delete pool1

  # Create the storage pool directly, without the two-stage process.
  if [ -n "${driver_config_node}" ]; then
    # shellcheck disable=SC2086
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage create pool1 "${poolDriver}" ${driver_config_node}
  else
    INCUS_DIR="${INCUS_ONE_DIR}" incus storage create pool1 "${poolDriver}"
  fi

  # Delete the storage pool
  INCUS_DIR="${INCUS_ONE_DIR}" incus storage delete pool1

  printf 'config: {}\ndevices: {}' | INCUS_DIR="${INCUS_ONE_DIR}" incus profile edit default
  INCUS_DIR="${INCUS_ONE_DIR}" incus storage delete data
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin shutdown
  sleep 0.5
  rm -f "${INCUS_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_incus "${INCUS_ONE_DIR}"
}

test_clustering_network() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

  # The state of the preseeded network shows up as CREATED
  INCUS_DIR="${INCUS_ONE_DIR}" incus network list | grep "${bridge}" | grep -q CREATED

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")

  # Create a project with restricted.networks.subnets set to check the default networks are created before projects
  # when a member joins the cluster.
  INCUS_DIR="${INCUS_ONE_DIR}" incus network set "${bridge}" ipv4.routes=192.0.2.0/24
  INCUS_DIR="${INCUS_ONE_DIR}" incus project create foo \
    -c restricted=true \
    -c features.networks=true \
    -c restricted.networks.subnets="${bridge}":192.0.2.0/24

  # Spawn a second node
  setup_clustering_netns 2
  INCUS_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_TWO_DIR}"
  ns2="${prefix}2"
  spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}"

  # The state of the preseeded network is still CREATED
  INCUS_DIR="${INCUS_ONE_DIR}" incus network list| grep "${bridge}" | grep -q CREATED

  # Check both nodes show network created.
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global "SELECT nodes.name,networks_nodes.state FROM nodes JOIN networks_nodes ON networks_nodes.node_id = nodes.id JOIN networks ON networks.id = networks_nodes.network_id WHERE networks.name = '${bridge}' AND nodes.name = 'node1'" | grep "| node1 | 1     |"
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global "SELECT nodes.name,networks_nodes.state FROM nodes JOIN networks_nodes ON networks_nodes.node_id = nodes.id JOIN networks ON networks.id = networks_nodes.network_id WHERE networks.name = '${bridge}' AND nodes.name = 'node2'" | grep "| node2 | 1     |"

  # Trying to pass config values other than
  # 'bridge.external_interfaces' results in an error
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus network create foo ipv4.address=auto --target node1 || false

  net="${bridge}x"

  # Define networks on the two nodes
  INCUS_DIR="${INCUS_ONE_DIR}" incus network create "${net}" --target node1
  INCUS_DIR="${INCUS_TWO_DIR}" incus network show  "${net}" | grep -q node1
  ! INCUS_DIR="${INCUS_TWO_DIR}" incus network show "${net}" | grep -q node2 || false
  INCUS_DIR="${INCUS_ONE_DIR}" incus network create "${net}" --target node2
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus network create "${net}" --target node2 || false
  INCUS_DIR="${INCUS_ONE_DIR}" incus network show "${net}" | grep status: | grep -q Pending

  # A container can't be created when its NIC is associated with a pending network.
  INCUS_DIR="${INCUS_TWO_DIR}" ensure_import_testimage
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus init --target node2 -n "${net}" testimage bar || false

  # The bridge.external_interfaces config key is not legal for the final network creation
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus network create "${net}" bridge.external_interfaces=foo || false

  # Create the network
  INCUS_DIR="${INCUS_TWO_DIR}" incus network create "${net}"
  INCUS_DIR="${INCUS_ONE_DIR}" incus network show "${net}" | grep status: | grep -q Created
  INCUS_DIR="${INCUS_ONE_DIR}" incus network show "${net}" --target node2 | grep status: | grep -q Created

  # FIXME: rename the network is not supported with clustering
  ! INCUS_DIR="${INCUS_TWO_DIR}" incus network rename "${net}" "${net}-foo" || false

  # Delete the networks
  INCUS_DIR="${INCUS_TWO_DIR}" incus network delete "${net}"
  INCUS_DIR="${INCUS_TWO_DIR}" incus network delete "${bridge}"

  INCUS_PID1="$(INCUS_DIR="${INCUS_ONE_DIR}" incus query /1.0 | jq .environment.server_pid)"
  INCUS_PID2="$(INCUS_DIR="${INCUS_TWO_DIR}" incus query /1.0 | jq .environment.server_pid)"

  # Test network create partial failures.
  nsenter -n -t "${INCUS_PID1}" -- ip link add "${net}" type dummy # Create dummy interface to conflict with network.
  INCUS_DIR="${INCUS_ONE_DIR}" incus network create "${net}" --target node1
  INCUS_DIR="${INCUS_ONE_DIR}" incus network create "${net}" --target node2
  INCUS_DIR="${INCUS_ONE_DIR}" incus network show "${net}" | grep status: | grep -q Pending # Check has pending status.

  # Run network create on other node1 (expect this to fail early due to existing interface).
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus network create "${net}" || false
  INCUS_DIR="${INCUS_ONE_DIR}" incus network show "${net}" | grep status: | grep -q Errored # Check has errored status.

  # Check each node status (expect both node1 and node2 to be pending as local member running created failed first).
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global "SELECT nodes.name,networks_nodes.state FROM nodes JOIN networks_nodes ON networks_nodes.node_id = nodes.id JOIN networks ON networks.id = networks_nodes.network_id WHERE networks.name = '${net}' AND nodes.name = 'node1'" | grep "| node1 | 0     |"
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global "SELECT nodes.name,networks_nodes.state FROM nodes JOIN networks_nodes ON networks_nodes.node_id = nodes.id JOIN networks ON networks.id = networks_nodes.network_id WHERE networks.name = '${net}' AND nodes.name = 'node2'" | grep "| node2 | 0     |"

  # Run network create on other node2 (still excpect to fail on node1, but expect node2 create to succeed).
  ! INCUS_DIR="${INCUS_TWO_DIR}" incus network create "${net}" || false

  # Check each node status (expect node1 to be pending and node2 to be created).
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global "SELECT nodes.name,networks_nodes.state FROM nodes JOIN networks_nodes ON networks_nodes.node_id = nodes.id JOIN networks ON networks.id = networks_nodes.network_id WHERE networks.name = '${net}' AND nodes.name = 'node1'" | grep "| node1 | 0     |"
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global "SELECT nodes.name,networks_nodes.state FROM nodes JOIN networks_nodes ON networks_nodes.node_id = nodes.id JOIN networks ON networks.id = networks_nodes.network_id WHERE networks.name = '${net}' AND nodes.name = 'node2'" | grep "| node2 | 1     |"

  # Check interfaces are expected types (dummy on node1 and bridge on node2).
  nsenter -n -t "${INCUS_PID1}" -- ip -details link show "${net}" | grep dummy
  nsenter -n -t "${INCUS_PID2}" -- ip -details link show "${net}" | grep bridge

  # Check we cannot update network global config while in pending state on either node.
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus network set "${net}" ipv4.dhcp false || false
  ! INCUS_DIR="${INCUS_TWO_DIR}" incus network set "${net}" ipv4.dhcp false || false

  # Check we can update node-specific config on the node that has been created (and that it is applied).
  nsenter -n -t "${INCUS_PID2}" -- ip link add "ext-${net}" type dummy # Create dummy interface to add to bridge.
  INCUS_DIR="${INCUS_TWO_DIR}" incus network set "${net}" bridge.external_interfaces "ext-${net}" --target node2
  nsenter -n -t "${INCUS_PID2}" -- ip link show "ext-${net}" | grep "master ${net}"

  # Check we can update node-specific config on the node that hasn't been created (and that only DB is updated).
  nsenter -n -t "${INCUS_PID1}" -- ip link add "ext-${net}" type dummy # Create dummy interface to add to bridge.
  nsenter -n -t "${INCUS_PID1}" -- ip address add 192.0.2.1/32 dev "ext-${net}" # Add address to prevent attach.
  INCUS_DIR="${INCUS_ONE_DIR}" incus network set "${net}" bridge.external_interfaces "ext-${net}" --target node1
  ! nsenter -n -t "${INCUS_PID1}" -- ip link show "ext-${net}" | grep "master ${net}" || false  # Don't expect to be attached.

  # Delete partially created network and check nodes that were created are cleaned up.
  INCUS_DIR="${INCUS_ONE_DIR}" incus network delete "${net}"
  ! nsenter -n -t "${INCUS_PID2}" -- ip link show "${net}" || false # Check bridge is removed.
  nsenter -n -t "${INCUS_PID2}" -- ip link show "ext-${net}" # Check external interface still exists.
  nsenter -n -t "${INCUS_PID1}" -- ip -details link show "${net}" | grep dummy # Check node1 conflict still exists.

  # Create new partially created network and check we can fix it.
  INCUS_DIR="${INCUS_ONE_DIR}" incus network create "${net}" --target node1
  INCUS_DIR="${INCUS_ONE_DIR}" incus network create "${net}" --target node2
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus network create "${net}" ipv4.address=192.0.2.1/24 ipv6.address=2001:db8::1/64|| false
  INCUS_DIR="${INCUS_ONE_DIR}" incus network show "${net}" | grep status: | grep -q Errored # Check has errored status.
  nsenter -n -t "${INCUS_PID1}" -- ip link delete "${net}" # Remove conflicting interface.
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus network create "${net}" ipv4.dhcp=false || false # Check supplying global config on re-create is blocked.
  INCUS_DIR="${INCUS_ONE_DIR}" incus network create "${net}" # Check re-create succeeds.
  INCUS_DIR="${INCUS_ONE_DIR}" incus network show "${net}" | grep status: | grep -q Created # Check is created after fix.
  nsenter -n -t "${INCUS_PID1}" -- ip -details link show "${net}" | grep bridge # Check bridge exists.
  nsenter -n -t "${INCUS_PID2}" -- ip -details link show "${net}" | grep bridge # Check bridge exists.
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus network create "${net}" || false # Check re-create is blocked after success.

  # Check both nodes marked created.
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global "SELECT nodes.name,networks_nodes.state FROM nodes JOIN networks_nodes ON networks_nodes.node_id = nodes.id JOIN networks ON networks.id = networks_nodes.network_id WHERE networks.name = '${net}' AND nodes.name = 'node1'" | grep "| node1 | 1     |"
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global "SELECT nodes.name,networks_nodes.state FROM nodes JOIN networks_nodes ON networks_nodes.node_id = nodes.id JOIN networks ON networks.id = networks_nodes.network_id WHERE networks.name = '${net}' AND nodes.name = 'node2'" | grep "| node2 | 1     |"

  # Check instance can be connected to created network and assign static DHCP allocations.
  INCUS_DIR="${INCUS_ONE_DIR}" incus network show "${net}"
  INCUS_DIR="${INCUS_ONE_DIR}" incus init --target node1 -n "${net}" testimage c1
  INCUS_DIR="${INCUS_ONE_DIR}" incus config device set c1 eth0 ipv4.address=192.0.2.2

  # Check cannot assign static IPv6 without stateful DHCPv6 enabled.
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus config device set c1 eth0 ipv6.address=2001:db8::2 || false
  INCUS_DIR="${INCUS_ONE_DIR}" incus network set "${net}" ipv6.dhcp.stateful=true
  INCUS_DIR="${INCUS_ONE_DIR}" incus config device set c1 eth0 ipv6.address=2001:db8::2

  # Check duplicate static DHCP allocation detection is working for same server as c1.
  INCUS_DIR="${INCUS_ONE_DIR}" incus init --target node1 -n "${net}" testimage c2
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus config device set c2 eth0 ipv4.address=192.0.2.2 || false
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus config device set c2 eth0 ipv6.address=2001:db8::2 || false

  # Check duplicate static DHCP allocation is allowed for instance on a different server.
  INCUS_DIR="${INCUS_ONE_DIR}" incus init --target node2 -n "${net}" testimage c3
  INCUS_DIR="${INCUS_ONE_DIR}" incus config device set c3 eth0 ipv4.address=192.0.2.2
  INCUS_DIR="${INCUS_ONE_DIR}" incus config device set c3 eth0 ipv6.address=2001:db8::2

  # Check duplicate MAC address assignment detection is working using both network and parent keys.
  c1MAC=$(INCUS_DIR="${INCUS_ONE_DIR}" incus config get c1 volatile.eth0.hwaddr)
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus config device set c2 eth0 hwaddr="${c1MAC}" || false
  INCUS_DIR="${INCUS_ONE_DIR}" incus config device set c3 eth0 hwaddr="${c1MAC}"

  # Check duplicate static MAC assignment detection is working for same server as c1.
  INCUS_DIR="${INCUS_ONE_DIR}" incus config device remove c2 eth0
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus config device add c2 eth0 nic hwaddr="${c1MAC}" nictype=bridged parent="${net}" || false

  # Check duplicate static MAC assignment is allowed for instance on a different server.
  INCUS_DIR="${INCUS_ONE_DIR}" incus config device remove c3 eth0
  INCUS_DIR="${INCUS_ONE_DIR}" incus config device add c3 eth0 nic hwaddr="${c1MAC}" nictype=bridged parent="${net}"

  # Cleanup instances and image.
  INCUS_DIR="${INCUS_ONE_DIR}" incus delete -f c1 c2 c3
  INCUS_DIR="${INCUS_ONE_DIR}" incus image delete testimage

  # Delete network.
  INCUS_DIR="${INCUS_ONE_DIR}" incus network delete "${net}"
  ! nsenter -n -t "${INCUS_PID1}" -- ip link show "${net}" || false # Check bridge is removed.
  ! nsenter -n -t "${INCUS_PID2}" -- ip link show "${net}" || false # Check bridge is removed.

  # Check creating native bridge network with external interface using the extended format.
  ifp="${bridge}p"
  if1="${bridge}i1"
  vlan="2345"

  nsenter -n -t "${INCUS_PID1}" -- ip link add "${ifp}" type dummy # Create dummy parent interface.
  INCUS_DIR="${INCUS_ONE_DIR}" incus network create "${net}" bridge.external_interfaces="${if1}/${ifp}/${vlan}" --target node1
  INCUS_DIR="${INCUS_ONE_DIR}" incus network create "${net}" --target node2
  INCUS_DIR="${INCUS_ONE_DIR}" incus network create "${net}" bridge.driver=native # Check create succeeds.

  nsenter -n -t "${INCUS_PID1}" -- ip link show "${if1}" # Check external interface was created on node 1.
  ! nsenter -n -t "${INCUS_PID2}" -- ip link show "${if1}" || false # Check external interface does not exist on node 2.

  # Check updating the network
  INCUS_DIR="${INCUS_ONE_DIR}" incus network set "${net}" ipv6.dhcp.stateful=true

  # Check creating external interface with extended format fails if already in use.
  net2="${bridge}x2"
  if2="${bridge}i2"

  INCUS_DIR="${INCUS_ONE_DIR}" incus network create "${net2}" bridge.external_interfaces="${if2}/${ifp}/${vlan}" --target node1
  INCUS_DIR="${INCUS_ONE_DIR}" incus network create "${net2}" --target node2
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus network create "${net2}" bridge.driver=native || false
  INCUS_DIR="${INCUS_ONE_DIR}" incus network show "${net2}" | grep status: | grep -q Errored # Check has errored status.

  # Delete failed network.
  INCUS_DIR="${INCUS_ONE_DIR}" incus network delete "${net2}"

  # Check adding second network with the same external interface.
  INCUS_DIR="${INCUS_ONE_DIR}" incus network create "${net2}" bridge.external_interfaces="${if1}/${ifp}/${vlan}" --target node1
  INCUS_DIR="${INCUS_ONE_DIR}" incus network create "${net2}" --target node2
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus network create "${net2}" bridge.driver=native || false
  INCUS_DIR="${INCUS_ONE_DIR}" incus network show "${net2}" | grep status: | grep -q Errored # Check has errored status.

  # Delete failed network.
  INCUS_DIR="${INCUS_ONE_DIR}" incus network delete "${net2}"

  # Cleanup external interface with extended format test.
  INCUS_DIR="${INCUS_ONE_DIR}" incus network delete "${net}"

  # Check that the external interface was deleted
  ! nsenter -n -t "${INCUS_PID1}" -- ip link show "${if1}" || false # Check external interface does not exist on node 1.

  # Delete dummy parent interface.
  nsenter -n -t "${INCUS_PID1}" -- ip link delete "${ifp}" # Delete the dummy parent interface.

  INCUS_DIR="${INCUS_ONE_DIR}" incus project delete foo

  INCUS_DIR="${INCUS_TWO_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin shutdown
  sleep 0.5
  rm -f "${INCUS_TWO_DIR}/unix.socket"
  rm -f "${INCUS_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_incus "${INCUS_ONE_DIR}"
  kill_incus "${INCUS_TWO_DIR}"
}

# Perform an upgrade of a 2-member cluster, then a join a third member and
# perform one more upgrade
test_clustering_upgrade() {
  # shellcheck disable=2039,3043
  local INCUS_DIR INCUS_NETNS

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  # First, test the upgrade with a 2-node cluster
  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  INCUS_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_TWO_DIR}"
  ns2="${prefix}2"
  spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}"

  # Respawn the second node, making it believe it has an higher
  # version than it actually has.
  export INCUS_ARTIFICIALLY_BUMP_API_EXTENSIONS=1
  shutdown_incus "${INCUS_TWO_DIR}"
  INCUS_NETNS="${ns2}" respawn_incus "${INCUS_TWO_DIR}" false

  # The second daemon is blocked waiting for the other to be upgraded
  ! INCUS_DIR="${INCUS_TWO_DIR}" incus admin waitready --timeout=5 || false

  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster show node1 | grep -q "message: Fully operational"
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster show node2 | grep -q "message: waiting for other nodes to be upgraded"

  # Respawn the first node, so it matches the version the second node
  # believes to have.
  shutdown_incus "${INCUS_ONE_DIR}"
  INCUS_NETNS="${ns1}" respawn_incus "${INCUS_ONE_DIR}" true

  # The second daemon has now unblocked
  INCUS_DIR="${INCUS_TWO_DIR}" incus admin waitready --timeout=30

  # The cluster is again operational
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus cluster list | grep -q "OFFLINE" || false

  # Now spawn a third node and test the upgrade with a 3-node cluster.
  setup_clustering_netns 3
  INCUS_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_THREE_DIR}"
  ns3="${prefix}3"
  spawn_incus_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${INCUS_THREE_DIR}" "${INCUS_ONE_DIR}"

  # Respawn the second node, making it believe it has an higher
  # version than it actually has.
  export INCUS_ARTIFICIALLY_BUMP_API_EXTENSIONS=2
  shutdown_incus "${INCUS_TWO_DIR}"
  INCUS_NETNS="${ns2}" respawn_incus "${INCUS_TWO_DIR}" false

  # The second daemon is blocked waiting for the other two to be
  # upgraded
  ! INCUS_DIR="${INCUS_TWO_DIR}" incus admin waitready --timeout=5 || false

  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster show node1 | grep -q "message: Fully operational"
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster show node2 | grep -q "message: waiting for other nodes to be upgraded"
  INCUS_DIR="${INCUS_THREE_DIR}" incus cluster show node3 | grep -q "message: Fully operational"

  # Respawn the first node and third node, so they match the version
  # the second node believes to have.
  shutdown_incus "${INCUS_ONE_DIR}"
  INCUS_NETNS="${ns1}" respawn_incus "${INCUS_ONE_DIR}" false
  shutdown_incus "${INCUS_THREE_DIR}"
  INCUS_NETNS="${ns3}" respawn_incus "${INCUS_THREE_DIR}" true

  # The cluster is again operational
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus cluster list | grep -q "OFFLINE" || false

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

# Perform an upgrade of an 8-member cluster.
test_clustering_upgrade_large() {
  # shellcheck disable=2039,3043
  local INCUS_DIR INCUS_NETNS N

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  INCUS_CLUSTER_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  N=8

  setup_clustering_netns 1
  INCUS_ONE_DIR="${INCUS_CLUSTER_DIR}/1"
  mkdir -p "${INCUS_ONE_DIR}"
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")

  for i in $(seq 2 "${N}"); do
    setup_clustering_netns "${i}"
    INCUS_ITH_DIR="${INCUS_CLUSTER_DIR}/${i}"
    mkdir -p "${INCUS_ITH_DIR}"
    chmod +x "${INCUS_ITH_DIR}"
    nsi="${prefix}${i}"
    spawn_incus_and_join_cluster "${nsi}" "${bridge}" "${cert}" "${i}" 1 "${INCUS_ITH_DIR}" "${INCUS_ONE_DIR}"
  done

  # Respawn all nodes in sequence, as if their version had been upgrade.
  export INCUS_ARTIFICIALLY_BUMP_API_EXTENSIONS=1
  for i in $(seq "${N}" -1 1); do
    shutdown_incus "${INCUS_CLUSTER_DIR}/${i}"
    INCUS_NETNS="${prefix}${i}" respawn_incus "${INCUS_CLUSTER_DIR}/${i}" false
  done

  INCUS_DIR="${INCUS_ONE_DIR}" incus admin waitready --timeout=10
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus cluster list | grep -q "OFFLINE" || false

  for i in $(seq "${N}" -1 1); do
    INCUS_DIR="${INCUS_CLUSTER_DIR}/${i}" incus admin shutdown
  done
  sleep 0.5
  for i in $(seq "${N}"); do
    rm -f "${INCUS_CLUSTER_DIR}/${i}/unix.socket"
  done

  teardown_clustering_netns
  teardown_clustering_bridge

  for i in $(seq "${N}"); do
    kill_incus "${INCUS_CLUSTER_DIR}/${i}"
  done
}

test_clustering_publish() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  INCUS_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_TWO_DIR}"
  ns2="${prefix}2"
  spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}"

  # Give Incus a couple of seconds to get event API connected properly
  sleep 2

  # Init a container on node2, using a client connected to node1
  INCUS_DIR="${INCUS_TWO_DIR}" ensure_import_testimage
  INCUS_DIR="${INCUS_ONE_DIR}" incus init --target node2 testimage foo

  INCUS_DIR="${INCUS_ONE_DIR}" incus publish foo --alias=foo-image
  INCUS_DIR="${INCUS_ONE_DIR}" incus image show foo-image | grep -q "public: false"
  INCUS_DIR="${INCUS_TWO_DIR}" incus image delete foo-image

  INCUS_DIR="${INCUS_TWO_DIR}" incus snapshot create foo backup
  INCUS_DIR="${INCUS_ONE_DIR}" incus publish foo/backup --alias=foo-backup-image
  INCUS_DIR="${INCUS_ONE_DIR}" incus image show foo-backup-image | grep -q "public: false"

  INCUS_DIR="${INCUS_TWO_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin shutdown
  sleep 0.5
  rm -f "${INCUS_TWO_DIR}/unix.socket"
  rm -f "${INCUS_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_incus "${INCUS_ONE_DIR}"
  kill_incus "${INCUS_TWO_DIR}"
}

test_clustering_profiles() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  INCUS_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_TWO_DIR}"
  ns2="${prefix}2"
  spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}"

  # Create an empty profile.
  INCUS_DIR="${INCUS_TWO_DIR}" incus profile create web

  # Launch two containers on the two nodes, using the above profile.
  INCUS_DIR="${INCUS_TWO_DIR}" ensure_import_testimage
  # TODO: Fix known race in importing small images that complete before event listener is setup.
  sleep 2
  INCUS_DIR="${INCUS_ONE_DIR}" incus launch --target node1 -p default -p web testimage c1
  INCUS_DIR="${INCUS_ONE_DIR}" incus launch --target node2 -p default -p web testimage c2

  # Edit the profile.
  source=$(mktemp -d -p "${TEST_DIR}" XXX)
  touch "${source}/hello"
  chmod 755 "${source}"
  chmod 644 "${source}/hello"
  (
    cat <<EOF
config: {}
description: ""
devices:
  web:
    path: /mnt
    source: "${source}"
    type: disk
name: web
used_by:
- /1.0/instances/c1
- /1.0/instances/c2
EOF
  ) | INCUS_DIR="${INCUS_TWO_DIR}" incus profile edit web

  INCUS_DIR="${INCUS_TWO_DIR}" incus exec c1 -- ls /mnt | grep -qxF hello
  INCUS_DIR="${INCUS_TWO_DIR}" incus exec c2 -- ls /mnt | grep -qxF hello

  INCUS_DIR="${INCUS_TWO_DIR}" incus stop c1 --force
  INCUS_DIR="${INCUS_ONE_DIR}" incus stop c2 --force

  INCUS_DIR="${INCUS_TWO_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin shutdown
  sleep 0.5
  rm -f "${INCUS_TWO_DIR}/unix.socket"
  rm -f "${INCUS_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_incus "${INCUS_ONE_DIR}"
  kill_incus "${INCUS_TWO_DIR}"
}

test_clustering_update_cert() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  # Bootstrap a node to steal its certs
  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

  cert_path=$(mktemp -p "${TEST_DIR}" XXX)
  key_path=$(mktemp -p "${TEST_DIR}" XXX)

  # Save the certs
  cp "${INCUS_ONE_DIR}/cluster.crt" "${cert_path}"
  cp "${INCUS_ONE_DIR}/cluster.key" "${key_path}"

  # Tear down the instance
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin shutdown
  sleep 0.5
  rm -f "${INCUS_ONE_DIR}/unix.socket"
  teardown_clustering_netns
  teardown_clustering_bridge
  kill_incus "${INCUS_ONE_DIR}"

  # Set up again
  setup_clustering_bridge

  # Bootstrap the first node
  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

  # quick check
  ! cmp -s "${INCUS_ONE_DIR}/cluster.crt" "${cert_path}" || false
  ! cmp -s "${INCUS_ONE_DIR}/cluster.key" "${key_path}" || false

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  INCUS_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_TWO_DIR}"
  ns2="${prefix}2"
  spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}"

  # Send update request
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster update-cert "${cert_path}" "${key_path}" -q

  cmp -s "${INCUS_ONE_DIR}/cluster.crt" "${cert_path}" || false
  cmp -s "${INCUS_TWO_DIR}/cluster.crt" "${cert_path}" || false

  cmp -s "${INCUS_ONE_DIR}/cluster.key" "${key_path}" || false
  cmp -s "${INCUS_TWO_DIR}/cluster.key" "${key_path}" || false

  INCUS_DIR="${INCUS_ONE_DIR}" incus info --target node2 | grep -q "server_name: node2"
  INCUS_DIR="${INCUS_TWO_DIR}" incus info --target node1 | grep -q "server_name: node1"

  INCUS_DIR="${INCUS_TWO_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin shutdown
  sleep 0.5
  rm -f "${INCUS_TWO_DIR}/unix.socket"
  rm -f "${INCUS_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_incus "${INCUS_ONE_DIR}"
  kill_incus "${INCUS_TWO_DIR}"
}

test_clustering_update_cert_reversion() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  # Bootstrap a node to steal its certs
  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

  cert_path=$(mktemp -p "${TEST_DIR}" XXX)
  key_path=$(mktemp -p "${TEST_DIR}" XXX)

  # Save the certs
  cp "${INCUS_ONE_DIR}/cluster.crt" "${cert_path}"
  cp "${INCUS_ONE_DIR}/cluster.key" "${key_path}"

  # Tear down the instance
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin shutdown
  sleep 0.5
  rm -f "${INCUS_ONE_DIR}/unix.socket"
  teardown_clustering_netns
  teardown_clustering_bridge
  kill_incus "${INCUS_ONE_DIR}"

  # Set up again
  setup_clustering_bridge

  # Bootstrap the first node
  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

  # quick check
  ! cmp -s "${INCUS_ONE_DIR}/cluster.crt" "${cert_path}" || false
  ! cmp -s "${INCUS_ONE_DIR}/cluster.key" "${key_path}" || false

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  INCUS_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_TWO_DIR}"
  ns2="${prefix}2"
  spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}"

  # Spawn a third node
  setup_clustering_netns 3
  INCUS_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_THREE_DIR}"
  ns3="${prefix}3"
  spawn_incus_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${INCUS_THREE_DIR}" "${INCUS_ONE_DIR}"

  # Shutdown third node
  INCUS_DIR="${INCUS_THREE_DIR}" incus admin shutdown
  sleep 0.5
  rm -f "${INCUS_THREE_DIR}/unix.socket"
  kill_incus "${INCUS_THREE_DIR}"

  # Send update request
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus cluster update-cert "${cert_path}" "${key_path}" -q || false

  ! cmp -s "${INCUS_ONE_DIR}/cluster.crt" "${cert_path}" || false
  ! cmp -s "${INCUS_TWO_DIR}/cluster.crt" "${cert_path}" || false

  ! cmp -s "${INCUS_ONE_DIR}/cluster.key" "${key_path}" || false
  ! cmp -s "${INCUS_TWO_DIR}/cluster.key" "${key_path}" || false

  INCUS_DIR="${INCUS_ONE_DIR}" incus info --target node2 | grep -q "server_name: node2"
  INCUS_DIR="${INCUS_TWO_DIR}" incus info --target node1 | grep -q "server_name: node1"

  INCUS_DIR="${INCUS_ONE_DIR}" incus warning list | grep -q "Unable to update cluster certificate"

  INCUS_DIR="${INCUS_TWO_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin shutdown
  sleep 0.5
  rm -f "${INCUS_TWO_DIR}/unix.socket"
  rm -f "${INCUS_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_incus "${INCUS_ONE_DIR}"
  kill_incus "${INCUS_TWO_DIR}"
}

test_clustering_join_api() {
  # shellcheck disable=2039,2034,3043
  local INCUS_DIR INCUS_NETNS

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

  cert=$(sed ':a;N;$!ba;s/\n/\\n/g' "${INCUS_ONE_DIR}/cluster.crt")

  setup_clustering_netns 2
  INCUS_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_TWO_DIR}"
  ns2="${prefix}2"
  INCUS_NETNS="${ns2}" spawn_incus "${INCUS_TWO_DIR}" false

  token="$(incus cluster add node2 --quiet)"
  op=$(curl --unix-socket "${INCUS_TWO_DIR}/unix.socket" -X PUT "incus/1.0/cluster" -d "{\"server_name\":\"node2\",\"enabled\":true,\"member_config\":[{\"entity\": \"storage-pool\",\"name\":\"data\",\"key\":\"source\",\"value\":\"\"}],\"server_address\":\"10.1.1.102:8443\",\"cluster_address\":\"10.1.1.101:8443\",\"cluster_certificate\":\"${cert}\",\"cluster_token\":\"${token}\"}" | jq -r .operation)
  curl --unix-socket "${INCUS_TWO_DIR}/unix.socket" "incus${op}/wait"

  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster show node2 | grep -q "message: Fully operational"

  INCUS_DIR="${INCUS_TWO_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin shutdown
  sleep 0.5
  rm -f "${INCUS_TWO_DIR}/unix.socket"
  rm -f "${INCUS_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_incus "${INCUS_TWO_DIR}"
  kill_incus "${INCUS_ONE_DIR}"
}

test_clustering_shutdown_nodes() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  INCUS_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_TWO_DIR}"
  ns2="${prefix}2"
  spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}"

  # Spawn a third node
  setup_clustering_netns 3
  INCUS_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_THREE_DIR}"
  ns3="${prefix}3"
  spawn_incus_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${INCUS_THREE_DIR}" "${INCUS_ONE_DIR}"

  # Init a container on node1, using a client connected to node1
  INCUS_DIR="${INCUS_ONE_DIR}" ensure_import_testimage
  INCUS_DIR="${INCUS_ONE_DIR}" incus launch --target node1 testimage foo

  # Get container PID
  instance_pid=$(INCUS_DIR="${INCUS_ONE_DIR}" incus info foo | awk '/^PID:/ {print $2}')

  # Get server PIDs
  daemon_pid1=$(INCUS_DIR="${INCUS_ONE_DIR}" incus info | awk '/server_pid/{print $2}')
  daemon_pid2=$(INCUS_DIR="${INCUS_TWO_DIR}" incus info | awk '/server_pid/{print $2}')
  daemon_pid3=$(INCUS_DIR="${INCUS_THREE_DIR}" incus info | awk '/server_pid/{print $2}')

  INCUS_DIR="${INCUS_TWO_DIR}" incus admin shutdown
  wait "${daemon_pid2}"

  INCUS_DIR="${INCUS_THREE_DIR}" incus admin shutdown
  wait "${daemon_pid3}"

  # Wait for raft election to take place and become aware that quorum has been lost (should take 3-6s).
  sleep 10

  # Make sure the database is not available to the first node
  ! INCUS_DIR="${INCUS_ONE_DIR}" timeout -k 5 5 incus cluster ls || false

  INCUS_DIR="${INCUS_ONE_DIR}" incus admin shutdown

  # Wait for Incus to terminate, otherwise the db will not be empty, and the
  # cleanup code will fail
  wait "${daemon_pid1}"

  # Container foo shouldn't be running anymore
  [ ! -e "/proc/${instance_pid}" ]

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_incus "${INCUS_TWO_DIR}"
  kill_incus "${INCUS_ONE_DIR}"
  kill_incus "${INCUS_THREE_DIR}"
}

test_clustering_projects() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  INCUS_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_TWO_DIR}"
  ns2="${prefix}2"
  spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}"

  # Create a test project
  INCUS_DIR="${INCUS_ONE_DIR}" incus project create p1
  INCUS_DIR="${INCUS_ONE_DIR}" incus project switch p1
  INCUS_DIR="${INCUS_ONE_DIR}" incus profile device add default root disk path="/" pool="data"

  # Create a container in the project.
  INCUS_DIR="${INCUS_ONE_DIR}" deps/import-busybox --project p1 --alias testimage
  INCUS_DIR="${INCUS_ONE_DIR}" incus init --target node2 testimage c1

  # The container is visible through both nodes
  INCUS_DIR="${INCUS_ONE_DIR}" incus list | grep -q c1
  INCUS_DIR="${INCUS_TWO_DIR}" incus list | grep -q c1

  INCUS_DIR="${INCUS_ONE_DIR}" incus delete -f c1

  # Remove the image file and DB record from node1.
  rm "${INCUS_ONE_DIR}"/images/*
  INCUS_DIR="${INCUS_TWO_DIR}" incus admin sql global 'delete from images_nodes where node_id = 1'

  # Check image import from node2 by creating container on node1 in other project.
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster list
  INCUS_DIR="${INCUS_ONE_DIR}" incus init --target node1 testimage c2 --project p1
  INCUS_DIR="${INCUS_ONE_DIR}" incus delete -f c2 --project p1

  INCUS_DIR="${INCUS_ONE_DIR}" incus image delete testimage

  INCUS_DIR="${INCUS_ONE_DIR}" incus project switch default

  INCUS_DIR="${INCUS_TWO_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin shutdown
  sleep 0.5
  rm -f "${INCUS_TWO_DIR}/unix.socket"
  rm -f "${INCUS_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_incus "${INCUS_ONE_DIR}"
  kill_incus "${INCUS_TWO_DIR}"
}

test_clustering_address() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"

  # Bootstrap the first node using a custom cluster port
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}" "dir" "8444"

  # The bootstrap node appears in the list with its cluster-specific port
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster list | grep -q 8444
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster show node1 | grep -q "database: true"

  # Add a remote using the core.https_address of the bootstrap node, and check
  # that the REST API is exposed.
  url="https://10.1.1.101:8443"
  token="$(INCUS_DIR="${INCUS_ONE_DIR}" incus config trust add foo --quiet)"
  incus remote add cluster --token "${token}" --accept-certificate "${url}"
  incus storage list cluster: | grep -q data

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")

  # Spawn a second node using a custom cluster port
  setup_clustering_netns 2
  INCUS_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_TWO_DIR}"
  ns2="${prefix}2"
  spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}" "dir" "8444"

  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster list | grep -q node2
  INCUS_DIR="${INCUS_TWO_DIR}" incus cluster show node2 | grep -q "database: true"

  # The new node appears with its custom cluster port
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster show node2 | grep ^url | grep -q 8444

  # The core.https_address config value can be changed and the REST API is still
  # accessible.
  INCUS_DIR="${INCUS_ONE_DIR}" incus config set "core.https_address" 10.1.1.101:9999
  url="https://10.1.1.101:9999"
  incus remote set-url cluster "${url}"
  incus storage list cluster:| grep -q data

  # The cluster.https_address config value can't be changed.
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus config set "cluster.https_address" "10.1.1.101:8448" || false

  # Create a container using the REST API exposed over core.https_address.
  INCUS_DIR="${INCUS_ONE_DIR}" deps/import-busybox --alias testimage
  incus init --target node2 testimage cluster:c1
  incus list cluster: | grep -q c1

  # The core.https_address config value can be set to a wildcard address if
  # the port is the same as cluster.https_address.
  INCUS_DIR="${INCUS_ONE_DIR}" incus config set "core.https_address" "0.0.0.0:8444"

  INCUS_DIR="${INCUS_TWO_DIR}" incus delete c1

  INCUS_DIR="${INCUS_TWO_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin shutdown
  sleep 0.5
  rm -f "${INCUS_TWO_DIR}/unix.socket"
  rm -f "${INCUS_ONE_DIR}/unix.socket"

  incus remote remove cluster

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_incus "${INCUS_ONE_DIR}"
  kill_incus "${INCUS_TWO_DIR}"
}

test_clustering_image_replication() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  INCUS_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_TWO_DIR}"
  ns2="${prefix}2"
  spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}"

  # Image replication will be performed across all nodes in the cluster by default
  images_minimal_replica1=$(INCUS_DIR="${INCUS_ONE_DIR}" incus config get cluster.images_minimal_replica)
  images_minimal_replica2=$(INCUS_DIR="${INCUS_TWO_DIR}" incus config get cluster.images_minimal_replica)
  [ "$images_minimal_replica1" = "" ] || false
  [ "$images_minimal_replica2" = "" ] || false

  # Import the test image on node1
  INCUS_DIR="${INCUS_ONE_DIR}" ensure_import_testimage

  # The image is visible through both nodes
  INCUS_DIR="${INCUS_ONE_DIR}" incus image list | grep -q testimage
  INCUS_DIR="${INCUS_TWO_DIR}" incus image list | grep -q testimage

  # The image tarball is available on both nodes
  fingerprint=$(INCUS_DIR="${INCUS_ONE_DIR}" incus image info testimage | awk '/^Fingerprint/ {print $2}')
  [ -f "${INCUS_ONE_DIR}/images/${fingerprint}" ] || false
  [ -f "${INCUS_TWO_DIR}/images/${fingerprint}" ] || false

  # Spawn a third node
  setup_clustering_netns 3
  INCUS_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_THREE_DIR}"
  ns3="${prefix}3"
  spawn_incus_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${INCUS_THREE_DIR}" "${INCUS_ONE_DIR}"

  # Wait for the test image to be synced into the joined node on the background
  retries=10
  while [ "${retries}" != "0" ]; do
    if [ ! -f "${INCUS_THREE_DIR}/images/${fingerprint}" ]; then
        sleep 0.5
        retries=$((retries-1))
        continue
    fi
    break
  done

  if [ "${retries}" -eq 0 ]; then
      echo "Images failed to synced into the joined node"
      return 1
  fi

  # Delete the imported image
  INCUS_DIR="${INCUS_ONE_DIR}" incus image delete testimage
  [ ! -f "${INCUS_ONE_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${INCUS_TWO_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${INCUS_THREE_DIR}/images/${fingerprint}" ] || false

  # Import the test image on node3
  INCUS_DIR="${INCUS_THREE_DIR}" ensure_import_testimage

  # The image is visible through all three nodes
  INCUS_DIR="${INCUS_ONE_DIR}" incus image list | grep -q testimage
  INCUS_DIR="${INCUS_TWO_DIR}" incus image list | grep -q testimage
  INCUS_DIR="${INCUS_THREE_DIR}" incus image list | grep -q testimage

  # The image tarball is available on all three nodes
  fingerprint=$(INCUS_DIR="${INCUS_ONE_DIR}" incus image info testimage | awk '/^Fingerprint/ {print $2}')
  [ -f "${INCUS_ONE_DIR}/images/${fingerprint}" ] || false
  [ -f "${INCUS_TWO_DIR}/images/${fingerprint}" ] || false
  [ -f "${INCUS_THREE_DIR}/images/${fingerprint}" ] || false

  # Delete the imported image
  INCUS_DIR="${INCUS_ONE_DIR}" incus image delete testimage
  [ ! -f "${INCUS_ONE_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${INCUS_TWO_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${INCUS_THREE_DIR}/images/${fingerprint}" ] || false

  # Import the image from the container
  INCUS_DIR="${INCUS_ONE_DIR}" ensure_import_testimage
  incus launch testimage c1

  # Modify the container's rootfs and create a new image from the container
  incus exec c1 -- touch /a
  incus stop c1 --force
  incus publish c1 --alias new-image

  fingerprint=$(INCUS_DIR="${INCUS_ONE_DIR}" incus image info new-image | awk '/^Fingerprint/ {print $2}')
  [ -f "${INCUS_ONE_DIR}/images/${fingerprint}" ] || false
  [ -f "${INCUS_TWO_DIR}/images/${fingerprint}" ] || false
  [ -f "${INCUS_THREE_DIR}/images/${fingerprint}" ] || false

  # Delete the imported image
  INCUS_DIR="${INCUS_TWO_DIR}" incus image delete new-image
  [ ! -f "${INCUS_ONE_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${INCUS_TWO_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${INCUS_THREE_DIR}/images/${fingerprint}" ] || false

  # Delete the container
  incus delete c1

  # Delete the imported image
  fingerprint=$(INCUS_DIR="${INCUS_ONE_DIR}" incus image info testimage | awk '/^Fingerprint/ {print $2}')
  INCUS_DIR="${INCUS_ONE_DIR}" incus image delete testimage
  [ ! -f "${INCUS_ONE_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${INCUS_TWO_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${INCUS_THREE_DIR}/images/${fingerprint}" ] || false

  # Disable the image replication
  INCUS_DIR="${INCUS_TWO_DIR}" incus config set cluster.images_minimal_replica 1
  INCUS_DIR="${INCUS_ONE_DIR}" incus info | grep -q 'cluster.images_minimal_replica: "1"'
  INCUS_DIR="${INCUS_TWO_DIR}" incus info | grep -q 'cluster.images_minimal_replica: "1"'
  INCUS_DIR="${INCUS_THREE_DIR}" incus info | grep -q 'cluster.images_minimal_replica: "1"'

  # Import the test image on node2
  INCUS_DIR="${INCUS_TWO_DIR}" ensure_import_testimage

  # The image is visible through all three nodes
  INCUS_DIR="${INCUS_ONE_DIR}" incus image list | grep -q testimage
  INCUS_DIR="${INCUS_TWO_DIR}" incus image list | grep -q testimage
  INCUS_DIR="${INCUS_THREE_DIR}" incus image list | grep -q testimage

  # The image tarball is only available on node2
  fingerprint=$(INCUS_DIR="${INCUS_TWO_DIR}" incus image info testimage | awk '/^Fingerprint/ {print $2}')
  [ -f "${INCUS_TWO_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${INCUS_ONE_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${INCUS_THREE_DIR}/images/${fingerprint}" ] || false

  # Delete the imported image
  INCUS_DIR="${INCUS_TWO_DIR}" incus image delete testimage
  [ ! -f "${INCUS_ONE_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${INCUS_TWO_DIR}/images/${fingerprint}" ] || false
  [ ! -f "${INCUS_THREE_DIR}/images/${fingerprint}" ] || false

  INCUS_DIR="${INCUS_ONE_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_TWO_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_THREE_DIR}" incus admin shutdown
  sleep 0.5
  rm -f "${INCUS_ONE_DIR}/unix.socket"
  rm -f "${INCUS_TWO_DIR}/unix.socket"
  rm -f "${INCUS_THREE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_incus "${INCUS_ONE_DIR}"
  kill_incus "${INCUS_TWO_DIR}"
  kill_incus "${INCUS_THREE_DIR}"
}

test_clustering_recover() {
  # shellcheck disable=2039,2034,3043
  local INCUS_DIR

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  INCUS_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_TWO_DIR}"
  ns2="${prefix}2"
  spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}"

  # Spawn a third node
  setup_clustering_netns 3
  INCUS_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_THREE_DIR}"
  ns3="${prefix}3"
  spawn_incus_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${INCUS_THREE_DIR}" "${INCUS_ONE_DIR}"

  # Wait a bit for raft roles to update.
  sleep 5

  # Check the current database nodes
  INCUS_DIR="${INCUS_ONE_DIR}" incusd cluster list-database | grep -q "10.1.1.101:8443"
  INCUS_DIR="${INCUS_ONE_DIR}" incusd cluster list-database | grep -q "10.1.1.102:8443"
  INCUS_DIR="${INCUS_ONE_DIR}" incusd cluster list-database | grep -q "10.1.1.103:8443"

  # Create a test project, just to insert something in the database.
  INCUS_DIR="${INCUS_ONE_DIR}" incus project create p1

  # Trying to recover a running daemon results in an error.
  ! INCUS_DIR="${INCUS_ONE_DIR}" incusd cluster recover-from-quorum-loss || false

  # Shutdown all nodes.
  INCUS_DIR="${INCUS_THREE_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_TWO_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin shutdown
  sleep 0.5

  # Now recover the first node and restart it.
  INCUS_DIR="${INCUS_ONE_DIR}" incusd cluster recover-from-quorum-loss -q
  respawn_incus_cluster_member "${ns1}" "${INCUS_ONE_DIR}"

  # The project we had created is still there
  INCUS_DIR="${INCUS_ONE_DIR}" incus project list | grep -q p1

  # The database nodes have been updated
  INCUS_DIR="${INCUS_ONE_DIR}" incusd cluster list-database | grep -q "10.1.1.101:8443"
  ! INCUS_DIR="${INCUS_ONE_DIR}" incusd cluster list-database | grep -q "10.1.1.102:8443" || false

  # Cleanup the dead node.
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster remove node2 --force --yes
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster remove node3 --force --yes

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

# When a voter cluster member is shutdown, its role gets transferred to a spare
# node.
test_clustering_handover() {
  # shellcheck disable=2039,2034,3043
  local INCUS_DIR

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

  echo "Launched member 1"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  INCUS_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_TWO_DIR}"
  ns2="${prefix}2"
  spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}"

  echo "Launched member 2"

  # Spawn a third node
  setup_clustering_netns 3
  INCUS_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_THREE_DIR}"
  ns3="${prefix}3"
  spawn_incus_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${INCUS_THREE_DIR}" "${INCUS_ONE_DIR}"

  echo "Launched member 3"

  # Spawn a fourth node, this will be a non-voter, stand-by node.
  setup_clustering_netns 4
  INCUS_FOUR_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_FOUR_DIR}"
  ns4="${prefix}4"
  spawn_incus_and_join_cluster "${ns4}" "${bridge}" "${cert}" 4 1 "${INCUS_FOUR_DIR}" "${INCUS_ONE_DIR}"

  echo "Launched member 4"

  INCUS_DIR="${INCUS_TWO_DIR}" incus cluster list
  INCUS_DIR="${INCUS_TWO_DIR}" incus cluster list | grep -Fc "database-standby" | grep -Fx 1

  # Shutdown the first node.
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin shutdown

  echo "Stopped member 1"

  # The fourth node has been promoted, while the first one demoted.
  INCUS_DIR="${INCUS_THREE_DIR}" incus admin sql local 'select * from raft_nodes'
  INCUS_DIR="${INCUS_THREE_DIR}" incus cluster ls
  INCUS_DIR="${INCUS_TWO_DIR}" incus cluster show node4
  INCUS_DIR="${INCUS_THREE_DIR}" incus cluster show node1
  INCUS_DIR="${INCUS_TWO_DIR}" incus cluster show node4 | grep -q "\- database$"
  INCUS_DIR="${INCUS_THREE_DIR}" incus cluster show node1 | grep -q "database: false"

  # Even if we shutdown one more node, the cluster is still available.
  INCUS_DIR="${INCUS_TWO_DIR}" incus admin shutdown

  echo "Stopped member 2"

  INCUS_DIR="${INCUS_THREE_DIR}" incus cluster list

  # Respawn the first node, which is now a spare, and the second node, which
  # is still a voter.
  echo "Respawning cluster members 1 and 2..."
  respawn_incus_cluster_member "${ns1}" "${INCUS_ONE_DIR}"
  respawn_incus_cluster_member "${ns2}" "${INCUS_TWO_DIR}"

  echo "Started members 1 and 2"

  # Shutdown two voters concurrently.
  echo "Shutting down cluster members 2 and 3..."
  INCUS_DIR="${INCUS_TWO_DIR}" incus admin shutdown &
  pid1="$!"
  INCUS_DIR="${INCUS_THREE_DIR}" incus admin shutdown &
  pid2="$!"

  wait "$pid1"
  wait "$pid2"
  echo "Cluster members 2 and 3 stopped..."

  echo "Stopped members 2 and 3"

  # Bringing back one of them restore the quorum.
  echo "Respawning cluster member 2..."
  respawn_incus_cluster_member "${ns2}" "${INCUS_TWO_DIR}"

  echo "Started member 2"

  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster list

  INCUS_DIR="${INCUS_ONE_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_TWO_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_FOUR_DIR}" incus admin shutdown
  sleep 0.5
  rm -f "${INCUS_ONE_DIR}/unix.socket"
  rm -f "${INCUS_TWO_DIR}/unix.socket"
  rm -f "${INCUS_THREE_DIR}/unix.socket"
  rm -f "${INCUS_FOUR_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_incus "${INCUS_ONE_DIR}"
  kill_incus "${INCUS_TWO_DIR}"
  kill_incus "${INCUS_THREE_DIR}"
  kill_incus "${INCUS_FOUR_DIR}"
}

# If a voter node crashes and is detected as offline, its role is migrated to a
# stand-by.
test_clustering_rebalance() {
  # shellcheck disable=2039,2034,3043
  local INCUS_DIR

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  INCUS_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_TWO_DIR}"
  ns2="${prefix}2"
  spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}"

  # Spawn a third node
  setup_clustering_netns 3
  INCUS_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_THREE_DIR}"
  ns3="${prefix}3"
  spawn_incus_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${INCUS_THREE_DIR}" "${INCUS_ONE_DIR}"

  # Spawn a fourth node
  setup_clustering_netns 4
  INCUS_FOUR_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_FOUR_DIR}"
  ns4="${prefix}4"
  spawn_incus_and_join_cluster "${ns4}" "${bridge}" "${cert}" 4 1 "${INCUS_FOUR_DIR}" "${INCUS_ONE_DIR}"

  # Wait a bit for raft roles to update.
  sleep 5

  # Check there is one database-standby member.
  INCUS_DIR="${INCUS_TWO_DIR}" incus cluster list
  INCUS_DIR="${INCUS_TWO_DIR}" incus cluster list | grep -Fc "database-standby" | grep -Fx 1

  # Kill the second node.
  INCUS_DIR="${INCUS_ONE_DIR}" incus config set cluster.offline_threshold 11
  kill -9 "$(cat "${INCUS_TWO_DIR}/incus.pid")"

  # Wait for the second node to be considered offline and be replaced by the
  # fourth node.
  sleep 15

  # The second node is offline and has been demoted.
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster show node2 | grep -q "status: Offline"
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster show node2 | grep -q "database: false"
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster show node4 | grep -q "status: Online"
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster show node4 | grep -q "\- database$"

  # Respawn the second node. It won't be able to disrupt the current leader,
  # since dqlite uses pre-vote.
  respawn_incus_cluster_member "${ns2}" "${INCUS_TWO_DIR}"
  sleep 12

  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster list
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster show node2 | grep -q "status: Online"
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster show node2 | grep -q "database: true"

  INCUS_DIR="${INCUS_ONE_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_TWO_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_THREE_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_FOUR_DIR}" incus admin shutdown
  sleep 0.5
  rm -f "${INCUS_ONE_DIR}/unix.socket"
  rm -f "${INCUS_TWO_DIR}/unix.socket"
  rm -f "${INCUS_THREE_DIR}/unix.socket"
  rm -f "${INCUS_FOUR_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_incus "${INCUS_ONE_DIR}"
  kill_incus "${INCUS_TWO_DIR}"
  kill_incus "${INCUS_THREE_DIR}"
  kill_incus "${INCUS_FOUR_DIR}"
}

# Recover a cluster where a raft node was removed from the nodes table but not
# from the raft configuration.
test_clustering_remove_raft_node() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  INCUS_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_TWO_DIR}"
  ns2="${prefix}2"
  spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}"

  # Configuration keys can be changed on any node.
  INCUS_DIR="${INCUS_TWO_DIR}" incus config set cluster.offline_threshold 11
  INCUS_DIR="${INCUS_ONE_DIR}" incus info | grep -q 'cluster.offline_threshold: "11"'
  INCUS_DIR="${INCUS_TWO_DIR}" incus info | grep -q 'cluster.offline_threshold: "11"'

  # The preseeded network bridge exists on all nodes.
  ns1_pid="$(cat "${TEST_DIR}/ns/${ns1}/PID")"
  ns2_pid="$(cat "${TEST_DIR}/ns/${ns2}/PID")"
  nsenter -m -n -t "${ns1_pid}" -- ip link show "${bridge}" > /dev/null
  nsenter -m -n -t "${ns2_pid}" -- ip link show "${bridge}" > /dev/null

  # Create a pending network and pool, to show that they are not
  # considered when checking if the joining node has all the required
  # networks and pools.
  INCUS_DIR="${INCUS_TWO_DIR}" incus storage create pool1 dir --target node1
  INCUS_DIR="${INCUS_ONE_DIR}" incus network create net1 --target node2

  # Spawn a third node, using the non-leader node2 as join target.
  setup_clustering_netns 3
  INCUS_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_THREE_DIR}"
  ns3="${prefix}3"
  spawn_incus_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 2 "${INCUS_THREE_DIR}" "${INCUS_ONE_DIR}"

  # Spawn a fourth node, this will be a database-standby node.
  setup_clustering_netns 4
  INCUS_FOUR_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_FOUR_DIR}"
  ns4="${prefix}4"
  spawn_incus_and_join_cluster "${ns4}" "${bridge}" "${cert}" 4 1 "${INCUS_FOUR_DIR}" "${INCUS_ONE_DIR}"

  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster list

  # Kill the second node, to prevent it from transferring its database role at shutdown.
  kill -9 "$(cat "${INCUS_TWO_DIR}/incus.pid")"

  # Remove the second node from the database but not from the raft configuration.
  retries=10
  while [ "${retries}" != "0" ]; do
    INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global "DELETE FROM nodes WHERE address = '10.1.1.102:8443'" && break
    sleep 0.5
    retries=$((retries-1))
  done

  if [ "${retries}" -eq 0 ]; then
      echo "Failed to remove node from database"
      return 1
  fi

  # Let the heartbeats catch up.
  sleep 12

  # The node does not appear anymore in the cluster list.
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus cluster list | grep -q "node2" || false

  # There are only 2 database nodes.
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster list
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster show node1 | grep -q "\- database-leader$"
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster show node3 | grep -q "\- database$"
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster show node4 | grep -q "\- database$"

  # The second node is still in the raft_nodes table.
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql local "SELECT * FROM raft_nodes" | grep -q "10.1.1.102"

  # Force removing the raft node.
  INCUS_DIR="${INCUS_ONE_DIR}" incusd cluster remove-raft-node -q "10.1.1.102"

  # Wait for a heartbeat to propagate and a rebalance to be performed.
  sleep 12

  # We're back to 3 database nodes.
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster list
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster show node1 | grep -q "\- database-leader$"
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster show node3 | grep -q "\- database$"
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster show node4 | grep -q "\- database$"

  # The second node is gone from the raft_nodes_table.
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql local "SELECT * FROM raft_nodes" | grep -q "10.1.1.102" || false

  INCUS_DIR="${INCUS_ONE_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_THREE_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_FOUR_DIR}" incus admin shutdown
  sleep 0.5
  rm -f "${INCUS_ONE_DIR}/unix.socket"
  rm -f "${INCUS_TWO_DIR}/unix.socket"
  rm -f "${INCUS_THREE_DIR}/unix.socket"
  rm -f "${INCUS_FOUR_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_incus "${INCUS_ONE_DIR}"
  kill_incus "${INCUS_TWO_DIR}"
  kill_incus "${INCUS_THREE_DIR}"
  kill_incus "${INCUS_FOUR_DIR}"
}

test_clustering_failure_domains() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  INCUS_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_TWO_DIR}"
  ns2="${prefix}2"
  spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}"

  # Spawn a third node, using the non-leader node2 as join target.
  setup_clustering_netns 3
  INCUS_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_THREE_DIR}"
  ns3="${prefix}3"
  spawn_incus_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 2 "${INCUS_THREE_DIR}" "${INCUS_ONE_DIR}"

  # Spawn a fourth node, this will be a non-database node.
  setup_clustering_netns 4
  INCUS_FOUR_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_FOUR_DIR}"
  ns4="${prefix}4"
  spawn_incus_and_join_cluster "${ns4}" "${bridge}" "${cert}" 4 1 "${INCUS_FOUR_DIR}" "${INCUS_ONE_DIR}"

  # Spawn a fifth node, using non-database node4 as join target.
  setup_clustering_netns 5
  INCUS_FIVE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_FIVE_DIR}"
  ns5="${prefix}5"
  spawn_incus_and_join_cluster "${ns5}" "${bridge}" "${cert}" 5 4 "${INCUS_FIVE_DIR}" "${INCUS_ONE_DIR}"

  # Spawn a sixth node, using non-database node4 as join target.
  setup_clustering_netns 6
  INCUS_SIX_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_SIX_DIR}"
  ns6="${prefix}6"
  spawn_incus_and_join_cluster "${ns6}" "${bridge}" "${cert}" 6 4 "${INCUS_SIX_DIR}" "${INCUS_ONE_DIR}"

  # Default failure domain
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster show node2 | grep -q "failure_domain: default"

  # Set failure domains

  # shellcheck disable=SC2039
  printf "roles: [\"database\"]\nfailure_domain: \"az1\"\ngroups: [\"default\"]" | INCUS_DIR="${INCUS_THREE_DIR}" incus cluster edit node1
  # shellcheck disable=SC2039
  printf "roles: [\"database\"]\nfailure_domain: \"az2\"\ngroups: [\"default\"]" | INCUS_DIR="${INCUS_THREE_DIR}" incus cluster edit node2
  # shellcheck disable=SC2039
  printf "roles: [\"database\"]\nfailure_domain: \"az3\"\ngroups: [\"default\"]" | INCUS_DIR="${INCUS_THREE_DIR}" incus cluster edit node3
  # shellcheck disable=SC2039
  printf "roles: []\nfailure_domain: \"az1\"\ngroups: [\"default\"]" | INCUS_DIR="${INCUS_THREE_DIR}" incus cluster edit node4
  # shellcheck disable=SC2039
  printf "roles: []\nfailure_domain: \"az2\"\ngroups: [\"default\"]" | INCUS_DIR="${INCUS_THREE_DIR}" incus cluster edit node5
  # shellcheck disable=SC2039
  printf "roles: []\nfailure_domain: \"az3\"\ngroups: [\"default\"]" | INCUS_DIR="${INCUS_THREE_DIR}" incus cluster edit node6

  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster show node2 | grep -q "failure_domain: az2"

  # Shutdown a node in az2, its replacement is picked from az2.
  INCUS_DIR="${INCUS_TWO_DIR}" incus admin shutdown
  sleep 3

  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster list
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster show node2 | grep -q "database: false"
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster show node5 | grep -q "database: true"

  INCUS_DIR="${INCUS_SIX_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_FIVE_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_FOUR_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_THREE_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin shutdown
  sleep 0.5
  rm -f "${INCUS_SIX_DIR}/unix.socket"
  rm -f "${INCUS_FIVE_DIR}/unix.socket"
  rm -f "${INCUS_FOUR_DIR}/unix.socket"
  rm -f "${INCUS_THREE_DIR}/unix.socket"
  rm -f "${INCUS_TWO_DIR}/unix.socket"
  rm -f "${INCUS_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_incus "${INCUS_ONE_DIR}"
  kill_incus "${INCUS_TWO_DIR}"
  kill_incus "${INCUS_THREE_DIR}"
  kill_incus "${INCUS_FOUR_DIR}"
  kill_incus "${INCUS_FIVE_DIR}"
  kill_incus "${INCUS_SIX_DIR}"
}

test_clustering_image_refresh() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  # The random storage backend is not supported in clustering tests,
  # since we need to have the same storage driver on all nodes, so use the driver chosen for the standalone pool.
  poolDriver=$(incus storage show "$(incus profile device get default root pool)" | awk '/^driver:/ {print $2}')

  # Spawn first node
  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}" "${poolDriver}"

  INCUS_DIR="${INCUS_ONE_DIR}" incus config set cluster.images_minimal_replica 1
  INCUS_DIR="${INCUS_ONE_DIR}" incus config set images.auto_update_interval 1

  # The state of the preseeded storage pool shows up as CREATED
  INCUS_DIR="${INCUS_ONE_DIR}" incus storage list | grep data | grep -q CREATED

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

  # Spawn public node which has a public testimage
  setup_clustering_netns 4
  INCUS_REMOTE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_REMOTE_DIR}"
  ns4="${prefix}4"

  INCUS_NETNS="${ns4}" spawn_incus "${INCUS_REMOTE_DIR}" false
  dir_configure "${INCUS_REMOTE_DIR}"
  INCUS_DIR="${INCUS_REMOTE_DIR}" deps/import-busybox --alias testimage --public

  INCUS_DIR="${INCUS_REMOTE_DIR}" incus config set core.https_address "10.1.1.104:8443"

  # Add remotes
  token="$(INCUS_DIR="${INCUS_ONE_DIR}" incus config trust add foo --quiet)"
  incus remote add public "https://10.1.1.104:8443" --accept-certificate --token foo --public
  token="$(INCUS_DIR="${INCUS_ONE_DIR}" incus config trust add foo --quiet)"
  incus remote add cluster "https://10.1.1.101:8443" --accept-certificate --token "${token}"

  INCUS_DIR="${INCUS_REMOTE_DIR}" incus init testimage c1

  # Create additional projects
  INCUS_DIR="${INCUS_ONE_DIR}" incus project create foo
  INCUS_DIR="${INCUS_ONE_DIR}" incus project create bar

  # Copy default profile to all projects (this includes the root disk)
  INCUS_DIR="${INCUS_ONE_DIR}" incus profile show default | INCUS_DIR="${INCUS_ONE_DIR}" incus profile edit default --project foo
  INCUS_DIR="${INCUS_ONE_DIR}" incus profile show default | INCUS_DIR="${INCUS_ONE_DIR}" incus profile edit default --project bar

  for project in default foo bar; do
    # Copy the public image to each project
    INCUS_DIR="${INCUS_ONE_DIR}" incus image copy public:testimage local: --alias testimage --project "${project}"

    # Diable autoupdate for testimage in project foo
    if [ "${project}" = "foo" ]; then
      auto_update=false
    else
      auto_update=true
    fi

    INCUS_DIR="${INCUS_ONE_DIR}" incus image show testimage --project "${project}" | sed -r "s/auto_update: .*/auto_update: ${auto_update}/g" | INCUS_DIR="${INCUS_ONE_DIR}" incus image edit testimage --project "${project}"

    # Create a container in each project
    INCUS_DIR="${INCUS_ONE_DIR}" incus init testimage c1 --project "${project}"
  done

  # Modify public testimage
  old_fingerprint="$(INCUS_DIR="${INCUS_REMOTE_DIR}" incus image ls testimage -c f --format csv)"
  dd if=/dev/urandom count=32 | INCUS_DIR="${INCUS_REMOTE_DIR}" incus file push - c1/foo
  INCUS_DIR="${INCUS_REMOTE_DIR}" incus publish c1 --alias testimage --reuse --public
  new_fingerprint="$(INCUS_DIR="${INCUS_REMOTE_DIR}" incus image ls testimage -c f --format csv)"

  pids=""

  if [ "${poolDriver}" != "dir" ]; then
    # Check image storage volume records exist.
    incus admin sql global 'select name from storage_volumes'
    if [ "${poolDriver}" = "ceph" ]; then
      incus admin sql global 'select name from storage_volumes' | grep -Fc "${old_fingerprint}" | grep -Fx 1
    else
      incus admin sql global 'select name from storage_volumes' | grep -Fc "${old_fingerprint}" | grep -Fx 3
    fi
  fi

  # Trigger image refresh on all nodes
  for incus_dir in "${INCUS_ONE_DIR}" "${INCUS_TWO_DIR}" "${INCUS_THREE_DIR}"; do
    INCUS_DIR="${incus_dir}" incus query /internal/testing/image-refresh &
    pids="$! ${pids}"
  done

  # Wait for the image to be refreshed
  for pid in ${pids}; do
    # Don't fail if PID isn't available as the process could be done already.
    wait "${pid}" || true
  done

  if [ "${poolDriver}" != "dir" ]; then
    incus admin sql global 'select name from storage_volumes'
    # Check image storage volume records actually removed from relevant members and replaced with new fingerprint.
    if [ "${poolDriver}" = "ceph" ]; then
      incus admin sql global 'select name from storage_volumes' | grep -Fc "${old_fingerprint}" | grep -Fx 0
      incus admin sql global 'select name from storage_volumes' | grep -Fc "${new_fingerprint}" | grep -Fx 1
    else
      incus admin sql global 'select name from storage_volumes' | grep -Fc "${old_fingerprint}" | grep -Fx 1
      incus admin sql global 'select name from storage_volumes' | grep -Fc "${new_fingerprint}" | grep -Fx 2
    fi
  fi

  # The projects default and bar should have received the new image
  # while project foo should still have the old image.
  # Also, it should only show 1 entry for the old image and 2 entries
  # for the new one.
  echo 'select images.fingerprint from images join projects on images.project_id=projects.id where projects.name="foo"' | INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global - | grep "${old_fingerprint}"
  [ "$(INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global 'select images.fingerprint from images' | grep -c "${old_fingerprint}")" -eq 1 ] || false

  echo 'select images.fingerprint from images join projects on images.project_id=projects.id where projects.name="default"' | INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global - | grep "${new_fingerprint}"
  echo 'select images.fingerprint from images join projects on images.project_id=projects.id where projects.name="bar"' | INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global - | grep "${new_fingerprint}"
  [ "$(INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global 'select images.fingerprint from images' | grep -c "${new_fingerprint}")" -eq 2 ] || false

  pids=""

  # Trigger image refresh on all nodes. This shouldn't do anything as the image
  # is already up-to-date.
  for incus_dir in "${INCUS_ONE_DIR}" "${INCUS_TWO_DIR}" "${INCUS_THREE_DIR}"; do
    INCUS_DIR="${incus_dir}" incus query /internal/testing/image-refresh &
    pids="$! ${pids}"
  done

  # Wait for the image to be refreshed
  for pid in ${pids}; do
    # Don't fail if PID isn't available as the process could be done already.
    wait "${pid}" || true
  done

  echo 'select images.fingerprint from images join projects on images.project_id=projects.id where projects.name="foo"' | INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global - | grep "${old_fingerprint}"
  [ "$(INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global 'select images.fingerprint from images' | grep -c "${old_fingerprint}")" -eq 1 ] || false

  echo 'select images.fingerprint from images join projects on images.project_id=projects.id where projects.name="default"' | INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global - | grep "${new_fingerprint}"
  echo 'select images.fingerprint from images join projects on images.project_id=projects.id where projects.name="bar"' | INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global - | grep "${new_fingerprint}"
  [ "$(INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global 'select images.fingerprint from images' | grep -c "${new_fingerprint}")" -eq 2 ] || false

  # Modify public testimage
  dd if=/dev/urandom count=32 | INCUS_DIR="${INCUS_REMOTE_DIR}" incus file push - c1/foo
  INCUS_DIR="${INCUS_REMOTE_DIR}" incus publish c1 --alias testimage --reuse --public
  new_fingerprint="$(INCUS_DIR="${INCUS_REMOTE_DIR}" incus image ls testimage -c f --format csv)"

  pids=""

  # Trigger image refresh on all nodes
  for incus_dir in "${INCUS_ONE_DIR}" "${INCUS_TWO_DIR}" "${INCUS_THREE_DIR}"; do
    INCUS_DIR="${incus_dir}" incus query /internal/testing/image-refresh &
    pids="$! ${pids}"
  done

  # Wait for the image to be refreshed
  for pid in ${pids}; do
    # Don't fail if PID isn't available as the process could be done already.
    wait "${pid}" || true
  done

  pids=""

  echo 'select images.fingerprint from images join projects on images.project_id=projects.id where projects.name="foo"' | INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global - | grep "${old_fingerprint}"
  [ "$(INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global 'select images.fingerprint from images' | grep -c "${old_fingerprint}")" -eq 1 ] || false

  echo 'select images.fingerprint from images join projects on images.project_id=projects.id where projects.name="default"' | INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global - | grep "${new_fingerprint}"
  echo 'select images.fingerprint from images join projects on images.project_id=projects.id where projects.name="bar"' | INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global - | grep "${new_fingerprint}"
  [ "$(INCUS_DIR="${INCUS_ONE_DIR}" incus admin sql global 'select images.fingerprint from images' | grep -c "${new_fingerprint}")" -eq 2 ] || false

  # Clean up everything
  for project in default foo bar; do
    # shellcheck disable=SC2046
    INCUS_DIR="${INCUS_ONE_DIR}" incus image rm --project "${project}" $(INCUS_DIR="${INCUS_ONE_DIR}" incus image ls --format csv --project "${project}" | cut -d, -f2)
    # shellcheck disable=SC2046
    INCUS_DIR="${INCUS_ONE_DIR}" incus rm --project "${project}" $(INCUS_DIR="${INCUS_ONE_DIR}" incus ls --format csv --project "${project}" | cut -d, -f1)
  done

  # shellcheck disable=SC2046
  INCUS_DIR="${INCUS_REMOTE_DIR}" incus image rm $(INCUS_DIR="${INCUS_REMOTE_DIR}" incus image ls --format csv | cut -d, -f2)
  # shellcheck disable=SC2046
  INCUS_DIR="${INCUS_REMOTE_DIR}" incus rm $(INCUS_DIR="${INCUS_REMOTE_DIR}" incus ls --format csv | cut -d, -f1)

  INCUS_DIR="${INCUS_ONE_DIR}" incus project delete foo
  INCUS_DIR="${INCUS_ONE_DIR}" incus project delete bar
  printf 'config: {}\ndevices: {}' | INCUS_DIR="${INCUS_ONE_DIR}" incus profile edit default
  INCUS_DIR="${INCUS_ONE_DIR}" incus storage delete data

  INCUS_DIR="${INCUS_ONE_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_TWO_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_THREE_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_REMOTE_DIR}" incus admin shutdown
  sleep 0.5
  rm -f "${INCUS_ONE_DIR}/unix.socket"
  rm -f "${INCUS_TWO_DIR}/unix.socket"
  rm -f "${INCUS_THREE_DIR}/unix.socket"
  rm -f "${INCUS_REMOTE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_incus "${INCUS_ONE_DIR}"
  kill_incus "${INCUS_TWO_DIR}"
  kill_incus "${INCUS_THREE_DIR}"
  kill_incus "${INCUS_REMOTE_DIR}"

  incus remote rm cluster

  # shellcheck disable=SC2034
  INCUS_NETNS=
}

test_clustering_evacuation() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  # The random storage backend is not supported in clustering tests,
  # since we need to have the same storage driver on all nodes, so use the driver chosen for the standalone pool.
  poolDriver=$(incus storage show "$(incus profile device get default root pool)" | awk '/^driver:/ {print $2}')

  # Spawn first node
  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}" "${poolDriver}"

  # The state of the preseeded storage pool shows up as CREATED
  INCUS_DIR="${INCUS_ONE_DIR}" incus storage list | grep data | grep -q CREATED

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

  # Create local pool
  INCUS_DIR="${INCUS_ONE_DIR}" incus storage create pool1 dir --target node1
  INCUS_DIR="${INCUS_ONE_DIR}" incus storage create pool1 dir --target node2
  INCUS_DIR="${INCUS_ONE_DIR}" incus storage create pool1 dir --target node3
  INCUS_DIR="${INCUS_ONE_DIR}" incus storage create pool1 dir

  # Create local storage volume
  INCUS_DIR="${INCUS_ONE_DIR}" incus storage volume create pool1 vol1

  INCUS_DIR="${INCUS_ONE_DIR}" ensure_import_testimage

  INCUS_DIR="${INCUS_ONE_DIR}" incus launch testimage c1 --target=node1
  INCUS_DIR="${INCUS_ONE_DIR}" incus config set c1 boot.host_shutdown_timeout=1

  INCUS_DIR="${INCUS_ONE_DIR}" incus launch testimage c2 --target=node1 -c cluster.evacuate=auto -s pool1
  INCUS_DIR="${INCUS_ONE_DIR}" incus config set c2 boot.host_shutdown_timeout=1

  INCUS_DIR="${INCUS_ONE_DIR}" incus launch testimage c3 --target=node1 -c cluster.evacuate=stop
  INCUS_DIR="${INCUS_ONE_DIR}" incus config set c3 boot.host_shutdown_timeout=1

  INCUS_DIR="${INCUS_ONE_DIR}" incus launch testimage c4 --target=node1 -c cluster.evacuate=migrate -s pool1
  INCUS_DIR="${INCUS_ONE_DIR}" incus config set c4 boot.host_shutdown_timeout=1

  INCUS_DIR="${INCUS_ONE_DIR}" incus init testimage c5 --target=node1
  INCUS_DIR="${INCUS_ONE_DIR}" incus config set c5 boot.host_shutdown_timeout=1

  INCUS_DIR="${INCUS_ONE_DIR}" incus launch testimage c6 --target=node2
  INCUS_DIR="${INCUS_ONE_DIR}" incus config set c6 boot.host_shutdown_timeout=1

  # For debugging
  INCUS_DIR="${INCUS_TWO_DIR}" incus list

  # Evacuate first node
  INCUS_DIR="${INCUS_TWO_DIR}" incus cluster evacuate node1 --force

  # Ensure the node is evacuated
  INCUS_DIR="${INCUS_TWO_DIR}" incus cluster show node1 | grep -q "status: Evacuated"

  # For debugging
  INCUS_DIR="${INCUS_TWO_DIR}" incus list

  # Check instance status
  INCUS_DIR="${INCUS_TWO_DIR}" incus info c1 | grep -q "Status: RUNNING"
  ! INCUS_DIR="${INCUS_TWO_DIR}" incus info c1 | grep -q "Location: node1" || false
  INCUS_DIR="${INCUS_TWO_DIR}" incus info c2 | grep -q "Status: RUNNING"
  ! INCUS_DIR="${INCUS_TWO_DIR}" incus info c2 | grep -q "Location: node1" || false
  INCUS_DIR="${INCUS_TWO_DIR}" incus info c3 | grep -q "Status: STOPPED"
  INCUS_DIR="${INCUS_TWO_DIR}" incus info c3 | grep -q "Location: node1"
  INCUS_DIR="${INCUS_TWO_DIR}" incus info c4 | grep -q "Status: RUNNING"
  ! INCUS_DIR="${INCUS_TWO_DIR}" incus info c4 | grep -q "Location: node1" || false
  INCUS_DIR="${INCUS_TWO_DIR}" incus info c5 | grep -q "Status: STOPPED"
  ! INCUS_DIR="${INCUS_TWO_DIR}" incus info c5 | grep -q "Location: node1" || false
  INCUS_DIR="${INCUS_TWO_DIR}" incus info c6 | grep -q "Status: RUNNING"
  INCUS_DIR="${INCUS_TWO_DIR}" incus info c6 | grep -q "Location: node2"

  # Ensure instances cannot be created on the evacuated node
  ! INCUS_DIR="${INCUS_TWO_DIR}" incus launch testimage c7 --target=node1 || false

  # Restore first node
  INCUS_DIR="${INCUS_TWO_DIR}" incus cluster restore node1 --force

  # For debugging
  INCUS_DIR="${INCUS_TWO_DIR}" incus list

  # Ensure the instances were moved back to the origin
  INCUS_DIR="${INCUS_TWO_DIR}" incus info c1 | grep -q "Status: RUNNING"
  INCUS_DIR="${INCUS_TWO_DIR}" incus info c1 | grep -q "Location: node1"
  INCUS_DIR="${INCUS_TWO_DIR}" incus info c2 | grep -q "Status: RUNNING"
  INCUS_DIR="${INCUS_TWO_DIR}" incus info c2 | grep -q "Location: node1"
  INCUS_DIR="${INCUS_TWO_DIR}" incus info c3 | grep -q "Status: RUNNING"
  INCUS_DIR="${INCUS_TWO_DIR}" incus info c3 | grep -q "Location: node1"
  INCUS_DIR="${INCUS_TWO_DIR}" incus info c4 | grep -q "Status: RUNNING"
  INCUS_DIR="${INCUS_TWO_DIR}" incus info c4 | grep -q "Location: node1"
  INCUS_DIR="${INCUS_TWO_DIR}" incus info c5 | grep -q "Status: STOPPED"
  INCUS_DIR="${INCUS_TWO_DIR}" incus info c5 | grep -q "Location: node1"
  INCUS_DIR="${INCUS_TWO_DIR}" incus info c6 | grep -q "Status: RUNNING"
  INCUS_DIR="${INCUS_TWO_DIR}" incus info c6 | grep -q "Location: node2"

  # Clean up
  INCUS_DIR="${INCUS_TWO_DIR}" incus rm -f c1
  INCUS_DIR="${INCUS_TWO_DIR}" incus rm -f c2
  INCUS_DIR="${INCUS_TWO_DIR}" incus rm -f c3
  INCUS_DIR="${INCUS_TWO_DIR}" incus rm -f c4
  INCUS_DIR="${INCUS_TWO_DIR}" incus rm -f c5
  INCUS_DIR="${INCUS_TWO_DIR}" incus rm -f c6
  INCUS_DIR="${INCUS_TWO_DIR}" incus image rm testimage

  printf 'config: {}\ndevices: {}' | INCUS_DIR="${INCUS_ONE_DIR}" incus profile edit default
  INCUS_DIR="${INCUS_ONE_DIR}" incus storage delete data

  # Shut down cluster
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_TWO_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_THREE_DIR}" incus admin shutdown
  sleep 0.5
  rm -f "${INCUS_ONE_DIR}/unix.socket"
  rm -f "${INCUS_TWO_DIR}/unix.socket"
  rm -f "${INCUS_THREE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_incus "${INCUS_ONE_DIR}"
  kill_incus "${INCUS_TWO_DIR}"
  kill_incus "${INCUS_THREE_DIR}"

  # shellcheck disable=SC2034
  INCUS_NETNS=
}

test_clustering_edit_configuration() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  # Bootstrap the first node
  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  INCUS_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_TWO_DIR}"
  ns2="${prefix}2"
  spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}"

  # Spawn 6 nodes in total for role coverage.
  setup_clustering_netns 3
  INCUS_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_THREE_DIR}"
  ns3="${prefix}3"
  spawn_incus_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${INCUS_THREE_DIR}" "${INCUS_ONE_DIR}"

  setup_clustering_netns 4
  INCUS_FOUR_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_FOUR_DIR}"
  ns4="${prefix}4"
  spawn_incus_and_join_cluster "${ns4}" "${bridge}" "${cert}" 4 1 "${INCUS_FOUR_DIR}" "${INCUS_ONE_DIR}"

  setup_clustering_netns 5
  INCUS_FIVE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_FIVE_DIR}"
  ns5="${prefix}5"
  spawn_incus_and_join_cluster "${ns5}" "${bridge}" "${cert}" 5 1 "${INCUS_FIVE_DIR}" "${INCUS_ONE_DIR}"

  setup_clustering_netns 6
  INCUS_SIX_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_SIX_DIR}"
  ns6="${prefix}6"
  spawn_incus_and_join_cluster "${ns6}" "${bridge}" "${cert}" 6 1 "${INCUS_SIX_DIR}" "${INCUS_ONE_DIR}"

  INCUS_DIR="${INCUS_ONE_DIR}" incus config set cluster.offline_threshold 11

  # Ensure successful communication
  INCUS_DIR="${INCUS_ONE_DIR}" incus info --target node2 | grep -q "server_name: node2"
  INCUS_DIR="${INCUS_TWO_DIR}" incus info --target node1 | grep -q "server_name: node1"
  INCUS_DIR="${INCUS_THREE_DIR}" incus info --target node1 | grep -q "server_name: node1"
  INCUS_DIR="${INCUS_FOUR_DIR}" incus info --target node1 | grep -q "server_name: node1"
  INCUS_DIR="${INCUS_FIVE_DIR}" incus info --target node1 | grep -q "server_name: node1"
  INCUS_DIR="${INCUS_SIX_DIR}" incus info --target node1 | grep -q "server_name: node1"

  # Shut down all nodes, de-syncing the roles tables
  shutdown_incus "${INCUS_ONE_DIR}"
  shutdown_incus "${INCUS_TWO_DIR}"
  shutdown_incus "${INCUS_THREE_DIR}"
  shutdown_incus "${INCUS_FOUR_DIR}"

  # Force-kill the last two to prevent leadership loss.
  daemon_pid=$(cat "${INCUS_FIVE_DIR}/incus.pid")
  kill -9 "${daemon_pid}" 2>/dev/null || true
  daemon_pid=$(cat "${INCUS_SIX_DIR}/incus.pid")
  kill -9 "${daemon_pid}" 2>/dev/null || true

  config=$(mktemp -p "${TEST_DIR}" XXX)
  # Update the cluster configuration with new port numbers
  INCUS_DIR="${INCUS_ONE_DIR}" incusd cluster show > "${config}"
  sed -e "s/:8443/:9393/" -i "${config}"
  INCUS_DIR="${INCUS_ONE_DIR}" incusd cluster edit < "${config}"

  INCUS_DIR="${INCUS_TWO_DIR}" incusd cluster show > "${config}"
  sed -e "s/:8443/:9393/" -i "${config}"
  INCUS_DIR="${INCUS_TWO_DIR}" incusd cluster edit < "${config}"

  INCUS_DIR="${INCUS_THREE_DIR}" incusd cluster show > "${config}"
  sed -e "s/:8443/:9393/" -i "${config}"
  INCUS_DIR="${INCUS_THREE_DIR}" incusd cluster edit < "${config}"

  INCUS_DIR="${INCUS_FOUR_DIR}" incusd cluster show > "${config}"
  sed -e "s/:8443/:9393/" -i "${config}"
  INCUS_DIR="${INCUS_FOUR_DIR}" incusd cluster edit < "${config}"

  INCUS_DIR="${INCUS_FIVE_DIR}" incusd cluster show > "${config}"
  sed -e "s/:8443/:9393/" -i "${config}"
  INCUS_DIR="${INCUS_FIVE_DIR}" incusd cluster edit < "${config}"

  INCUS_DIR="${INCUS_SIX_DIR}" incusd cluster show > "${config}"
  sed -e "s/:8443/:9393/" -i "${config}"
  INCUS_DIR="${INCUS_SIX_DIR}" incusd cluster edit < "${config}"

  # Respawn the nodes
  INCUS_NETNS="${ns1}" respawn_incus "${INCUS_ONE_DIR}" false
  INCUS_NETNS="${ns2}" respawn_incus "${INCUS_TWO_DIR}" false
  INCUS_NETNS="${ns3}" respawn_incus "${INCUS_THREE_DIR}" false
  INCUS_NETNS="${ns4}" respawn_incus "${INCUS_FOUR_DIR}" false
  INCUS_NETNS="${ns5}" respawn_incus "${INCUS_FIVE_DIR}" false
  # Only wait on the last node, because we don't know who the voters are
  INCUS_NETNS="${ns6}" respawn_incus "${INCUS_SIX_DIR}" true

  # Let the heartbeats catch up
  sleep 12

  # Ensure successful communication
  INCUS_DIR="${INCUS_ONE_DIR}"   incus info --target node2 | grep -q "server_name: node2"
  INCUS_DIR="${INCUS_TWO_DIR}"   incus info --target node1 | grep -q "server_name: node1"
  INCUS_DIR="${INCUS_THREE_DIR}" incus info --target node1 | grep -q "server_name: node1"
  INCUS_DIR="${INCUS_FOUR_DIR}"  incus info --target node1 | grep -q "server_name: node1"
  INCUS_DIR="${INCUS_FIVE_DIR}"  incus info --target node1 | grep -q "server_name: node1"
  INCUS_DIR="${INCUS_SIX_DIR}"   incus info --target node1 | grep -q "server_name: node1"
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus cluster ls | grep -q "No heartbeat" || false

  # Clean up
  shutdown_incus "${INCUS_ONE_DIR}"
  shutdown_incus "${INCUS_TWO_DIR}"
  shutdown_incus "${INCUS_THREE_DIR}"
  shutdown_incus "${INCUS_FOUR_DIR}"

  # Force-kill the last two to prevent leadership loss.
  daemon_pid=$(cat "${INCUS_FIVE_DIR}/incus.pid")
  kill -9 "${daemon_pid}" 2>/dev/null || true
  daemon_pid=$(cat "${INCUS_SIX_DIR}/incus.pid")
  kill -9 "${daemon_pid}" 2>/dev/null || true

  rm -f "${INCUS_ONE_DIR}/unix.socket"
  rm -f "${INCUS_TWO_DIR}/unix.socket"
  rm -f "${INCUS_THREE_DIR}/unix.socket"
  rm -f "${INCUS_FOUR_DIR}/unix.socket"
  rm -f "${INCUS_FIVE_DIR}/unix.socket"
  rm -f "${INCUS_SIX_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_incus "${INCUS_ONE_DIR}"
  kill_incus "${INCUS_TWO_DIR}"
  kill_incus "${INCUS_THREE_DIR}"
  kill_incus "${INCUS_FOUR_DIR}"
  kill_incus "${INCUS_FIVE_DIR}"
  kill_incus "${INCUS_SIX_DIR}"
}

test_clustering_remove_members() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  # Bootstrap the first node
  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  INCUS_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_TWO_DIR}"
  ns2="${prefix}2"
  spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}"

  # Spawn a three node
  setup_clustering_netns 3
  INCUS_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_THREE_DIR}"
  ns3="${prefix}3"
  spawn_incus_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${INCUS_THREE_DIR}" "${INCUS_ONE_DIR}"

  # Spawn a four node
  setup_clustering_netns 4
  INCUS_FOUR_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_FOUR_DIR}"
  ns4="${prefix}4"
  spawn_incus_and_join_cluster "${ns4}" "${bridge}" "${cert}" 4 1 "${INCUS_FOUR_DIR}" "${INCUS_ONE_DIR}"

  # Spawn a five node
  setup_clustering_netns 5
  INCUS_FIVE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_FIVE_DIR}"
  ns5="${prefix}5"
  spawn_incus_and_join_cluster "${ns5}" "${bridge}" "${cert}" 5 1 "${INCUS_FIVE_DIR}" "${INCUS_ONE_DIR}"

  # Spawn a sixth node
  setup_clustering_netns 6
  INCUS_SIX_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_SIX_DIR}"
  ns6="${prefix}6"
  spawn_incus_and_join_cluster "${ns6}" "${bridge}" "${cert}" 6 1 "${INCUS_SIX_DIR}" "${INCUS_ONE_DIR}"

  INCUS_DIR="${INCUS_ONE_DIR}" incus info --target node2 | grep -q "server_name: node2"
  INCUS_DIR="${INCUS_TWO_DIR}" incus info --target node1 | grep -q "server_name: node1"
  INCUS_DIR="${INCUS_THREE_DIR}" incus info --target node1 | grep -q "server_name: node1"
  INCUS_DIR="${INCUS_FOUR_DIR}" incus info --target node1 | grep -q "server_name: node1"
  INCUS_DIR="${INCUS_FIVE_DIR}" incus info --target node1 | grep -q "server_name: node1"
  INCUS_DIR="${INCUS_SIX_DIR}" incus info --target node1 | grep -q "server_name: node1"

  # stop node 6
  shutdown_incus "${INCUS_SIX_DIR}"

  # Remove node2 node3 node4 node5
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster rm node2
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster rm node3
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster rm node4
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster rm node5

  # Ensure the remaining node is working and node2, node3, node4,node5 successful reomve from cluster
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus cluster list | grep -q "node2" || false
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus cluster list | grep -q "node3" || false
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus cluster list | grep -q "node4" || false
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus cluster list | grep -q "node5" || false
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster list | grep -q "node1"

  # Start node 6
  INCUS_NETNS="${ns6}" respawn_incus "${INCUS_SIX_DIR}" true

  # make sure node6 is a spare ndoe
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster list | grep -q "node6"
  ! INCUS_DIR="${INCUS_ONE_DIR}" incus cluster show node6 | grep -qE "\- database-standy$|\- database-leader$|\- database$" || false

  # waite for leader update table raft_node of local database by heartbeat
  sleep 10s

  # Remove the leader, via the spare node
  INCUS_DIR="${INCUS_SIX_DIR}" incus cluster rm node1

  # Ensure the remaining node is working and node1 had successful remove
  ! INCUS_DIR="${INCUS_SIX_DIR}" incus cluster list | grep -q "node1" || false
  INCUS_DIR="${INCUS_SIX_DIR}" incus cluster list | grep -q "node6"

  # Check whether node6 is changed from a spare node to a leader node.
  INCUS_DIR="${INCUS_SIX_DIR}" incus cluster show node6 | grep -q "\- database-leader$"
  INCUS_DIR="${INCUS_SIX_DIR}" incus cluster show node6 | grep -q "\- database$"

  # Spawn a sixth node
  setup_clustering_netns 7
  INCUS_SEVEN_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_SEVEN_DIR}"
  ns7="${prefix}7"
  spawn_incus_and_join_cluster "${ns7}" "${bridge}" "${cert}" 7 6 "${INCUS_SEVEN_DIR}" "${INCUS_SIX_DIR}"

  # Ensure the remaining node is working by join a new node7
  INCUS_DIR="${INCUS_SIX_DIR}" incus info --target node7 | grep -q "server_name: node7"
  INCUS_DIR="${INCUS_SEVEN_DIR}" incus info --target node6 | grep -q "server_name: node6"

  # Clean up
  shutdown_incus "${INCUS_ONE_DIR}"
  shutdown_incus "${INCUS_TWO_DIR}"
  shutdown_incus "${INCUS_THREE_DIR}"
  shutdown_incus "${INCUS_FOUR_DIR}"
  shutdown_incus "${INCUS_FIVE_DIR}"
  shutdown_incus "${INCUS_SIX_DIR}"
  shutdown_incus "${INCUS_SEVEN_DIR}"

  rm -f "${INCUS_ONE_DIR}/unix.socket"
  rm -f "${INCUS_TWO_DIR}/unix.socket"
  rm -f "${INCUS_THREE_DIR}/unix.socket"
  rm -f "${INCUS_FOUR_DIR}/unix.socket"
  rm -f "${INCUS_FIVE_DIR}/unix.socket"
  rm -f "${INCUS_SIX_DIR}/unix.socket"
  rm -f "${INCUS_SEVEN_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_incus "${INCUS_ONE_DIR}"
  kill_incus "${INCUS_TWO_DIR}"
  kill_incus "${INCUS_THREE_DIR}"
  kill_incus "${INCUS_FOUR_DIR}"
  kill_incus "${INCUS_FIVE_DIR}"
  kill_incus "${INCUS_SIX_DIR}"
  kill_incus "${INCUS_SEVEN_DIR}"
}

test_clustering_autotarget() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  INCUS_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_TWO_DIR}"
  ns2="${prefix}2"
  spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}"

 # Use node1 for all cluster actions.
 INCUS_DIR="${INCUS_ONE_DIR}"

 # Spawn c1 on node2 from node1
 ensure_import_testimage
  incus init --target node2 testimage c1
 incus ls | grep c1 | grep -q node2

 # Set node1 config to disable autotarget
 incus cluster set node1 scheduler.instance manual

 # Spawn another node, autotargeting node2 although it has more instances.
 incus init testimage c2
 incus ls | grep c2 | grep -q node2

  shutdown_incus "${INCUS_ONE_DIR}"
  shutdown_incus "${INCUS_TWO_DIR}"
  sleep 0.5
  rm -f "${INCUS_TWO_DIR}/unix.socket"
  rm -f "${INCUS_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_incus "${INCUS_ONE_DIR}"
  kill_incus "${INCUS_TWO_DIR}"
}

test_clustering_groups() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  INCUS_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_TWO_DIR}"
  ns2="${prefix}2"
  spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}"

  # Spawn a third node
  setup_clustering_netns 3
  INCUS_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_THREE_DIR}"
  ns3="${prefix}3"
  spawn_incus_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${INCUS_THREE_DIR}" "${INCUS_ONE_DIR}"

  token="$(INCUS_DIR="${INCUS_ONE_DIR}" incus config trust add foo --quiet)"
  incus remote add cluster --token "${token}" --accept-certificate "https://10.1.1.101:8443"

  # Initially, there is only the default group
  incus cluster group show cluster:default
  [ "$(incus query cluster:/1.0/cluster/groups | jq 'length')" -eq 1 ]

  # All nodes initially belong to the default group
  [ "$(incus query cluster:/1.0/cluster/groups/default | jq '.members | length')" -eq 3 ]

  # Renaming the default group is not allowed
  ! incus cluster group rename cluster:default foobar || false

  incus cluster list cluster:
  # Nodes need to belong to at least one group, removing it from the default group should therefore fail
  ! incus cluster group remove cluster:node1 default || false

  # Create new cluster group which should be empty
  incus cluster group create cluster:foobar
  [ "$(incus query cluster:/1.0/cluster/groups/foobar | jq '.members | length')" -eq 0 ]

  # Copy both description and members from default group
  incus cluster group show cluster:default | incus cluster group edit cluster:foobar
  [ "$(incus query cluster:/1.0/cluster/groups/foobar | jq '.description == "Default cluster group"')" = "true" ]
  [ "$(incus query cluster:/1.0/cluster/groups/foobar | jq '.members | length')" -eq 3 ]

  # Delete all members from new group
  incus cluster group remove cluster:node1 foobar
  incus cluster group remove cluster:node2 foobar
  incus cluster group remove cluster:node3 foobar

  # Add second node to new group. Node2 will now belong to both groups.
  incus cluster group assign cluster:node2 default,foobar
  [ "$(incus query cluster:/1.0/cluster/members/node2 | jq 'any(.groups[] == "default"; .)')" = "true" ]
  [ "$(incus query cluster:/1.0/cluster/members/node2 | jq 'any(.groups[] == "foobar"; .)')" = "true" ]

  # Deleting the "foobar" group should fail as it still has members
  ! incus cluster group delete cluster:foobar || false

  # Since node2 now belongs to two groups, it can be removed from the default group
  incus cluster group remove cluster:node2 default
  incus query cluster:/1.0/cluster/members/node2

  [ "$(incus query cluster:/1.0/cluster/members/node2 | jq 'any(.groups[] == "default"; .)')" = "false" ]
  [ "$(incus query cluster:/1.0/cluster/members/node2 | jq 'any(.groups[] == "foobar"; .)')" = "true" ]

  # Rename group "foobar" to "blah"
  incus cluster group rename cluster:foobar blah
  [ "$(incus query cluster:/1.0/cluster/members/node2 | jq 'any(.groups[] == "blah"; .)')" = "true" ]

  incus cluster group create cluster:foobar2
  incus cluster group assign cluster:node3 default,foobar2

  # Create a new group "newgroup"
  incus cluster group create cluster:newgroup
  [ "$(incus query cluster:/1.0/cluster/groups/newgroup | jq '.members | length')" -eq 0 ]

  # Add node1 to the "newgroup" group
  incus cluster group add cluster:node1 newgroup
  [ "$(incus query cluster:/1.0/cluster/members/node1 | jq 'any(.groups[] == "newgroup"; .)')" = "true" ]

  # remove node1 from "newgroup"
  incus cluster group remove cluster:node1 newgroup

  # delete cluster group "newgroup"
  incus cluster group delete cluster:newgroup

  # With these settings:
  # - node1 will receive instances unless a different node is directly targeted (not via group)
  # - node2 will receive instances if either targeted by group or directly
  # - node3 will only receive instances if targeted directly
  incus cluster set cluster:node2 scheduler.instance=group
  incus cluster set cluster:node3 scheduler.instance=manual

  ensure_import_testimage

  # Cluster group "foobar" doesn't exist and should therefore fail
  ! incus init testimage cluster:c1 --target=@foobar || false

  # At this stage we have:
  # - node1 in group default accepting all instances
  # - node2 in group blah accepting group-only targeting
  # - node3 in group default accepting direct targeting only

  # c1 should go to node1
  incus init testimage cluster:c1
  incus info cluster:c1 | grep -q "Location: node1"

  # c2 should go to node2
  incus init testimage cluster:c2 --target=@blah
  incus info cluster:c2 | grep -q "Location: node2"

  # c3 should go to node2 again
  incus init testimage cluster:c3 --target=@blah
  incus info cluster:c3 | grep -q "Location: node2"

  # Direct targeting of node2 should work
  incus init testimage cluster:c4 --target=node2
  incus info cluster:c4 | grep -q "Location: node2"

  # Direct targeting of node3 should work
  incus init testimage cluster:c5 --target=node3
  incus info cluster:c5 | grep -q "Location: node3"

  # Clean up
  incus rm -f c1 c2 c3 c4 c5

  # Restricted project tests
  incus project create foo -c features.images=false -c restricted=true -c restricted.cluster.groups=blah
  incus profile show default | incus profile edit default --project foo

  # Check cannot create instance in restricted project that only allows blah group, when the only member that
  # exists in the blah group also has scheduler.instance=group set (so it must be targeted via group or directly).
  ! incus init testimage cluster:c1 --project foo || false

  # Check cannot create instance in restricted project when targeting a member that isn't in the restricted
  # project's allowed cluster groups list.
  ! incus init testimage cluster:c1 --project foo --target=node1 || false
  ! incus init testimage cluster:c1 --project foo --target=@foobar2 || false

  # Check can create instance in restricted project when not targeting any specific member, but that it will only
  # be created on members within the project's allowed cluster groups list.
  incus cluster unset cluster:node2 scheduler.instance
  incus init testimage cluster:c1 --project foo
  incus init testimage cluster:c2 --project foo
  incus info cluster:c1 --project foo | grep -q "Location: node2"
  incus info cluster:c2 --project foo | grep -q "Location: node2"
  incus delete -f c1 c2 --project foo

  # Check can specify any member or group when restricted.cluster.groups is empty.
  incus project unset foo restricted.cluster.groups
  incus init testimage cluster:c1 --project foo --target=node1
  incus info cluster:c1 --project foo | grep -q "Location: node1"

  incus init testimage cluster:c2 --project foo --target=@blah
  incus info cluster:c2 --project foo | grep -q "Location: node2"

  incus delete -f c1 c2 --project foo

  incus project delete foo

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

  incus remote rm cluster
}

test_clustering_events() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

  # Add a newline at the end of each line. YAML has weird rules...
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")

  # Spawn a second node.
  setup_clustering_netns 2
  INCUS_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_TWO_DIR}"
  ns2="${prefix}2"
  spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}"

  # Spawn a third node.
  setup_clustering_netns 3
  INCUS_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_THREE_DIR}"
  ns3="${prefix}3"
  spawn_incus_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${INCUS_THREE_DIR}" "${INCUS_ONE_DIR}"

  # Spawn a fourth node.
  setup_clustering_netns 4
  INCUS_FOUR_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_FOUR_DIR}"
  ns4="${prefix}4"
  spawn_incus_and_join_cluster "${ns4}" "${bridge}" "${cert}" 4 1 "${INCUS_FOUR_DIR}" "${INCUS_ONE_DIR}"

  # Spawn a firth node.
  setup_clustering_netns 5
  INCUS_FIVE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_FIVE_DIR}"
  ns5="${prefix}5"
  spawn_incus_and_join_cluster "${ns5}" "${bridge}" "${cert}" 5 1 "${INCUS_FIVE_DIR}" "${INCUS_ONE_DIR}"

  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster list
  INCUS_DIR="${INCUS_ONE_DIR}" incus info | grep -F "server_event_mode: full-mesh"
  INCUS_DIR="${INCUS_TWO_DIR}" incus info | grep -F "server_event_mode: full-mesh"
  INCUS_DIR="${INCUS_THREE_DIR}" incus info | grep -F "server_event_mode: full-mesh"
  INCUS_DIR="${INCUS_FOUR_DIR}" incus info | grep -F "server_event_mode: full-mesh"
  INCUS_DIR="${INCUS_FIVE_DIR}" incus info | grep -F "server_event_mode: full-mesh"

  ensure_import_testimage

  # c1 should go to node1.
  INCUS_DIR="${INCUS_ONE_DIR}" incus launch testimage c1 --target=node1
  INCUS_DIR="${INCUS_ONE_DIR}" incus info c1 | grep -q "Location: node1"
  INCUS_DIR="${INCUS_ONE_DIR}" incus launch testimage c2 --target=node2

  INCUS_DIR="${INCUS_ONE_DIR}" stdbuf -oL incus monitor --type=lifecycle > "${TEST_DIR}/node1.log" &
  monitorNode1PID=$!
  INCUS_DIR="${INCUS_TWO_DIR}" stdbuf -oL incus monitor --type=lifecycle > "${TEST_DIR}/node2.log" &
  monitorNode2PID=$!
  INCUS_DIR="${INCUS_THREE_DIR}" stdbuf -oL incus monitor --type=lifecycle > "${TEST_DIR}/node3.log" &
  monitorNode3PID=$!

  # Restart instance generating restart lifecycle event.
  INCUS_DIR="${INCUS_ONE_DIR}" incus restart -f c1
  INCUS_DIR="${INCUS_THREE_DIR}" incus restart -f c2
  sleep 2

  # Check events were distributed.
  for i in 1 2 3; do
    cat "${TEST_DIR}/node${i}.log"
    grep -Fc "instance-restarted" "${TEST_DIR}/node${i}.log" | grep -Fx 2
  done

  # Switch into event-hub mode.
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster role add node1 event-hub
  INCUS_DIR="${INCUS_TWO_DIR}" incus cluster role add node2 event-hub
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster list
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster list | grep -Fc event-hub | grep -Fx 2

  # Check events were distributed.
  for i in 1 2 3; do
    grep -Fc "cluster-member-updated" "${TEST_DIR}/node${i}.log" | grep -Fx 2
  done

  sleep 2 # Wait for notification heartbeat to distribute new roles.
  INCUS_DIR="${INCUS_ONE_DIR}" incus info | grep -F "server_event_mode: hub-server"
  INCUS_DIR="${INCUS_TWO_DIR}" incus info | grep -F "server_event_mode: hub-server"
  INCUS_DIR="${INCUS_THREE_DIR}" incus info | grep -F "server_event_mode: hub-client"
  INCUS_DIR="${INCUS_FOUR_DIR}" incus info | grep -F "server_event_mode: hub-client"
  INCUS_DIR="${INCUS_FIVE_DIR}" incus info | grep -F "server_event_mode: hub-client"

  # Restart instance generating restart lifecycle event.
  INCUS_DIR="${INCUS_ONE_DIR}" incus restart -f c1
  INCUS_DIR="${INCUS_THREE_DIR}" incus restart -f c2
  sleep 2

  # Check events were distributed.
  for i in 1 2 3; do
    cat "${TEST_DIR}/node${i}.log"
    grep -Fc "instance-restarted" "${TEST_DIR}/node${i}.log" | grep -Fx 4
  done

  # Launch container on node3 to check image distribution events work during event-hub mode.
  INCUS_DIR="${INCUS_THREE_DIR}" incus launch testimage c3 --target=node3

  for i in 1 2 3; do
    grep -Fc "instance-created" "${TEST_DIR}/node${i}.log" | grep -Fx 1
  done

  # Switch into full-mesh mode by removing one event-hub role so there is <2 in the cluster.
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster role remove node1 event-hub
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster list | grep -Fc event-hub | grep -Fx 1

  sleep 1 # Wait for notification heartbeat to distribute new roles.
  INCUS_DIR="${INCUS_ONE_DIR}" incus info | grep -F "server_event_mode: full-mesh"
  INCUS_DIR="${INCUS_TWO_DIR}" incus info | grep -F "server_event_mode: full-mesh"
  INCUS_DIR="${INCUS_THREE_DIR}" incus info | grep -F "server_event_mode: full-mesh"
  INCUS_DIR="${INCUS_FOUR_DIR}" incus info | grep -F "server_event_mode: full-mesh"
  INCUS_DIR="${INCUS_FIVE_DIR}" incus info | grep -F "server_event_mode: full-mesh"

  # Check events were distributed.
  for i in 1 2 3; do
    grep -Fc "cluster-member-updated" "${TEST_DIR}/node${i}.log" | grep -Fx 3
  done

  # Restart instance generating restart lifecycle event.
  INCUS_DIR="${INCUS_ONE_DIR}" incus restart -f c1
  INCUS_DIR="${INCUS_THREE_DIR}" incus restart -f c2
  sleep 2

  # Check events were distributed.
  for i in 1 2 3; do
    cat "${TEST_DIR}/node${i}.log"
    grep -Fc "instance-restarted" "${TEST_DIR}/node${i}.log" | grep -Fx 6
  done

  # Switch back into event-hub mode by giving the role to node4 and node5.
  INCUS_DIR="${INCUS_TWO_DIR}" incus cluster role remove node2 event-hub
  INCUS_DIR="${INCUS_FOUR_DIR}" incus cluster role add node4 event-hub
  INCUS_DIR="${INCUS_FIVE_DIR}" incus cluster role add node5 event-hub

  sleep 2 # Wait for notification heartbeat to distribute new roles.
  INCUS_DIR="${INCUS_ONE_DIR}" incus info | grep -F "server_event_mode: hub-client"
  INCUS_DIR="${INCUS_TWO_DIR}" incus info | grep -F "server_event_mode: hub-client"
  INCUS_DIR="${INCUS_THREE_DIR}" incus info | grep -F "server_event_mode: hub-client"
  INCUS_DIR="${INCUS_FOUR_DIR}" incus info | grep -F "server_event_mode: hub-server"
  INCUS_DIR="${INCUS_FIVE_DIR}" incus info | grep -F "server_event_mode: hub-server"

  # Shutdown the hub servers.
  INCUS_DIR="${INCUS_ONE_DIR}" incus config set cluster.offline_threshold 11
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster ls

  INCUS_DIR="${INCUS_FOUR_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_FIVE_DIR}" incus admin shutdown

  sleep 12
  INCUS_DIR="${INCUS_ONE_DIR}" incus cluster ls

  # Confirm that local operations are not blocked by having no event hubs running, but that events are not being
  # distributed.
  INCUS_DIR="${INCUS_ONE_DIR}" incus restart -f c1
  sleep 2

  grep -Fc "instance-restarted" "${TEST_DIR}/node1.log" | grep -Fx 7
  for i in 2 3; do
    cat "${TEST_DIR}/node${i}.log"
    grep -Fc "instance-restarted" "${TEST_DIR}/node${i}.log" | grep -Fx 6
  done

  # Kill monitors.
  kill -9 ${monitorNode1PID} || true
  kill -9 ${monitorNode2PID} || true
  kill -9 ${monitorNode3PID} || true

  # Cleanup.
  INCUS_DIR="${INCUS_ONE_DIR}" incus delete -f c1
  INCUS_DIR="${INCUS_TWO_DIR}" incus delete -f c2
  INCUS_DIR="${INCUS_THREE_DIR}" incus delete -f c3
  INCUS_DIR="${INCUS_THREE_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_TWO_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin shutdown
  sleep 0.5
  rm -f "${INCUS_FIVE_DIR}/unix.socket"
  rm -f "${INCUS_FOUR_DIR}/unix.socket"
  rm -f "${INCUS_THREE_DIR}/unix.socket"
  rm -f "${INCUS_TWO_DIR}/unix.socket"
  rm -f "${INCUS_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_incus "${INCUS_ONE_DIR}"
  kill_incus "${INCUS_TWO_DIR}"
  kill_incus "${INCUS_THREE_DIR}"
  kill_incus "${INCUS_FOUR_DIR}"
  kill_incus "${INCUS_FIVE_DIR}"
}

test_clustering_uuid() {
  # shellcheck disable=2039,3043
  local INCUS_DIR

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  # create two cluster nodes
  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")

  setup_clustering_netns 2
  INCUS_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_TWO_DIR}"
  ns2="${prefix}2"
  spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}" "${INCUS_ONE_DIR}"

  ensure_import_testimage

  # spawn an instance on the first Incus node
  INCUS_DIR="${INCUS_ONE_DIR}" incus launch testimage c1 --target=node1
  # get its volatile.uuid
  uuid_before_move=$(INCUS_DIR="${INCUS_ONE_DIR}" incus config get c1 volatile.uuid)
  # stop the instance
  INCUS_DIR="${INCUS_ONE_DIR}" incus stop -f c1
  # move the instance to the second Incus node
  INCUS_DIR="${INCUS_ONE_DIR}" incus move c1 --target=node2
  # get the volatile.uuid of the moved instance on the second node
  uuid_after_move=$(INCUS_DIR="${INCUS_TWO_DIR}" incus config get c1 volatile.uuid)

  # check that the uuid have not changed, else return an error
  if [ "${uuid_before_move}" != "${uuid_after_move}" ]; then
    echo "UUID changed after move"
    false
  fi

  # cleanup
  INCUS_DIR="${INCUS_TWO_DIR}" incus delete c1 -f
  INCUS_DIR="${INCUS_TWO_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin shutdown
  sleep 0.5
  rm -f "${INCUS_TWO_DIR}/unix.socket"
  rm -f "${INCUS_ONE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_incus "${INCUS_ONE_DIR}"
  kill_incus "${INCUS_TWO_DIR}"
}

test_clustering_openfga() {
  if ! command -v openfga >/dev/null 2>&1 || ! command -v fga >/dev/null 2>&1; then
    echo "==> SKIP: Missing OpenFGA"
    return
  fi

  if true; then
      echo "==> SKIP: Can't validate due to netns"
      return
  fi

  # shellcheck disable=2039,3043
  local INCUS_DIR

  setup_clustering_bridge
  prefix="inc$$"
  bridge="${prefix}"

  setup_clustering_netns 1
  INCUS_ONE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_ONE_DIR}"
  ns1="${prefix}1"
  spawn_incus_and_bootstrap_cluster "${ns1}" "${bridge}" "${INCUS_ONE_DIR}"

  # Run OIDC server.
  spawn_oidc
  set_oidc user1

  INCUS_DIR="${INCUS_ONE_DIR}" incus config set "oidc.issuer=http://127.0.0.1:$(cat "${TEST_DIR}/oidc.port")/"
  INCUS_DIR="${INCUS_ONE_DIR}" incus config set "oidc.client.id=device"

  BROWSER=curl incus remote add --accept-certificate oidc-openfga "https://10.1.1.101:8443" --auth-type oidc
  ! incus_remote info oidc-openfga: | grep -Fq 'core.https_address' || false

  run_openfga

  # Create store and get store ID.
  OPENFGA_STORE_ID="$(fga store create --name "test" | jq -r '.store.id')"

  # Configure OpenFGA using the oidc-openfga remote.
  INCUS_DIR="${INCUS_ONE_DIR}" incus config set oidc-openfga: openfga.api.url "$(fga_address)"
  INCUS_DIR="${INCUS_ONE_DIR}" incus config set oidc-openfga: openfga.api.token "$(fga_token)"
  INCUS_DIR="${INCUS_ONE_DIR}" incus config set oidc-openfga: openfga.store.id "${OPENFGA_STORE_ID}"
  sleep 1

  # Add a newline at the end of each line. YAML as weird rules..
  cert=$(sed ':a;N;$!ba;s/\n/\n\n/g' "${INCUS_ONE_DIR}/cluster.crt")

  # Spawn a second node
  setup_clustering_netns 2
  INCUS_TWO_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_TWO_DIR}"
  ns2="${prefix}2"
  spawn_incus_and_join_cluster "${ns2}" "${bridge}" "${cert}" 2 1 "${INCUS_TWO_DIR}"

  # After the second node has joined there should exist only one authorization model.
  [ "$(fga model list --store-id "${OPENFGA_STORE_ID}" | jq '.authorization_models | length')" = 1 ]

  BROWSER=curl incus remote add --accept-certificate node2 "https://10.1.1.102:8443" --auth-type oidc
  ! incus_remote info node2: | grep -Fq 'core.https_address' || false

  # Add self as server admin. Should be able to see config now.
  fga tuple write --store-id "${OPENFGA_STORE_ID}" user:user1 admin server:incus
  incus_remote info node2: | grep -Fq 'core.https_address'

  # Spawn a third node. Should be able to join while OpenFGA is running.
  setup_clustering_netns 3
  INCUS_THREE_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)
  chmod +x "${INCUS_THREE_DIR}"
  ns3="${prefix}3"
  spawn_incus_and_join_cluster "${ns3}" "${bridge}" "${cert}" 3 1 "${INCUS_THREE_DIR}"

  # cleanup
  incus remote rm node2
  incus remote rm oidc-openfga
  shutdown_openfga
  INCUS_DIR="${INCUS_ONE_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_TWO_DIR}" incus admin shutdown
  INCUS_DIR="${INCUS_THREE_DIR}" incus admin shutdown
  sleep 0.5
  rm -f "${INCUS_ONE_DIR}/unix.socket"
  rm -f "${INCUS_TWO_DIR}/unix.socket"
  rm -f "${INCUS_THREE_DIR}/unix.socket"

  teardown_clustering_netns
  teardown_clustering_bridge

  kill_incus "${INCUS_ONE_DIR}"
  kill_incus "${INCUS_TWO_DIR}"
  kill_incus "${INCUS_THREE_DIR}"
}
