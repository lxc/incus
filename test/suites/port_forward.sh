test_port_forward() {
    echo "==> API extension instance_port_forward"

    ensure_import_testimage
    ensure_has_localhost_remote "${INCUS_ADDR}"

    MESSAGE="Port forward test string"
    HOST_TCP_PORT=$(local_tcp_port)
    incus launch testimage pfTester

    # Check basic request validation.
    ! incus port-forward pfTester 99999 8080 || false
    ! incus query -X POST -d '{"address": "127.0.0.1", "port": 4321}' /1.0/instances/pfTester/port-forward || false

    # Start a listener inside the container.
    nsenter -n -U -t "$(incus query /1.0/instances/pfTester/state | jq .pid)" -- socat tcp4-listen:4321,fork exec:/bin/cat &
    NSENTER_PID=$!

    # Start the port forwarder.
    incus port-forward pfTester 4321 "127.0.0.1:${HOST_TCP_PORT}" &
    FORWARD_PID=$!
    sleep 1

    # Check that the data makes a full roundtrip.
    ECHO=$( (
        echo "${MESSAGE}"
        sleep 0.5
    ) | socat - tcp4:127.0.0.1:"${HOST_TCP_PORT}")

    if [ "${ECHO}" != "${MESSAGE}" ]; then
        echo "Port forwarding did not properly send data to the container"
        false
    fi

    # Check that a second connection also works.
    ECHO=$( (
        echo "${MESSAGE}"
        sleep 0.5
    ) | socat - tcp4:127.0.0.1:"${HOST_TCP_PORT}")

    if [ "${ECHO}" != "${MESSAGE}" ]; then
        echo "Port forwarding did not handle a second connection"
        false
    fi

    kill "${NSENTER_PID}" 2> /dev/null || true
    wait "${NSENTER_PID}" 2> /dev/null || true

    # Check that connections to a closed port inside the instance fail cleanly.
    ECHO=$( (
        echo "${MESSAGE}"
        sleep 0.5
    ) | socat - tcp4:127.0.0.1:"${HOST_TCP_PORT}" || true)

    if [ "${ECHO}" = "${MESSAGE}" ]; then
        echo "Port forwarding should have failed on a closed port"
        false
    fi

    kill "${FORWARD_PID}" 2> /dev/null || true
    wait "${FORWARD_PID}" 2> /dev/null || true

    incus delete -f pfTester
}
