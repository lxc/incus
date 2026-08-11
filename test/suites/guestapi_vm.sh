test_guestapi_vm() {
    if [ -n "${INCUS_OFFLINE:-}" ]; then
        echo "==> SKIP: External connectivity needed to pull test image"
        export TEST_UNMET_REQUIREMENT="external connectivity needed to pull test image"
        return
    fi

    if [ ! -e /dev/kvm ] || ! command -v "qemu-system-$(uname -m)" > /dev/null 2>&1; then
        echo "==> SKIP: QEMU and KVM needed to run virtual machines"
        export TEST_UNMET_REQUIREMENT="QEMU and KVM needed to run virtual machines"
        return
    fi

    incus network create incusbr0
    incus profile device remove default eth0
    incus profile device add default eth0 nic network=incusbr0 name=eth0

    poolName="vmpool$$"

    echo "==> Create storage pool"
    incus storage create "${poolName}" dir

    echo "==> Create VM and boot"
    incus init images:debian/13 v1 --vm -s "${poolName}"
    incus start v1
    incus wait v1 agent --timeout=90 --interval=1
    incus info v1

    # Install curl
    incus exec v1 -- sh -c "apt-get update && apt-get install --no-install-recommends --yes curl"

    echo "==> Checking guestapi is working"

    # guestapi is enabled by default and should work
    incus exec v1 -- curl -s --unix-socket /dev/incus/sock http://custom.socket/1.0 | jq
    incus exec v1 -- curl -s --unix-socket /dev/incus/sock http://custom.socket/1.0/devices | jq
    incus exec v1 -- curl -s --unix-socket /dev/incus/sock http://custom.socket/1.0/config | jq
    incus exec v1 -- curl -s --unix-socket /dev/incus/sock http://custom.socket/1.0/meta-data | grep -q '#cloud-config'

    incus restart v1
    incus wait v1 agent --timeout=90 --interval=1

    # guestapi should be running after restart
    incus exec v1 -- curl -s --unix-socket /dev/incus/sock http://custom.socket/1.0 | jq
    incus exec v1 -- curl -s --unix-socket /dev/incus/sock http://custom.socket/1.0/devices | jq
    incus exec v1 -- curl -s --unix-socket /dev/incus/sock http://custom.socket/1.0/config | jq
    incus exec v1 -- curl -s --unix-socket /dev/incus/sock http://custom.socket/1.0/meta-data | grep -q '#cloud-config'

    # Disable guestapi
    incus config set v1 security.guestapi false

    echo "==> Checking guestapi is not working"

    ! incus exec v1 -- curl -s --unix-socket /dev/incus/sock http://custom.socket/1.0 || false

    incus restart v1
    incus wait v1 agent --timeout=90 --interval=1

    # guestapi should not be running after restart
    ! incus exec v1 -- curl -s --unix-socket /dev/incus/sock http://custom.socket/1.0 || false

    echo "==> Checking guestapi can be enabled live"

    # Enable guestapi
    incus config set v1 security.guestapi true

    # guestapi should be running after the config is enabled
    incus exec v1 -- curl -s --unix-socket /dev/incus/sock http://custom.socket/1.0 | jq
    incus exec v1 -- curl -s --unix-socket /dev/incus/sock http://custom.socket/1.0/devices | jq
    incus exec v1 -- curl -s --unix-socket /dev/incus/sock http://custom.socket/1.0/config | jq
    incus exec v1 -- curl -s --unix-socket /dev/incus/sock http://custom.socket/1.0/meta-data | grep -q '#cloud-config'

    # test instance Ready state
    incus exec v1 -- curl -s --unix-socket /dev/incus/sock -X PATCH -d '{\"state\":\"Ready\"}' http://custom.socket/1.0
    [ "$(incus config get v1 volatile.last_state.ready)" = "true" ]

    incus exec v1 -- curl -s --unix-socket /dev/incus/sock -X PATCH -d '{\"state\":\"Started\"}' http://custom.socket/1.0
    [ "$(incus config get v1 volatile.last_state.ready)" = "false" ]

    incus exec v1 -- curl -s --unix-socket /dev/incus/sock -X PATCH -d '{\"state\":\"Ready\"}' http://custom.socket/1.0
    [ "$(incus config get v1 volatile.last_state.ready)" = "true" ]
    incus stop -f v1
    sleep 5
    [ "$(incus config get v1 volatile.last_state.ready)" = "false" ]

    # Test nested VM functionality.
    incus start v1
    incus wait v1 agent --timeout=90 --interval=1
    sleep 30

    # Install Incus
    curl -sL https://pkgs.zabbly.com/get/incus-daily | incus exec v1 -- sh
    incus exec v1 -- incus admin init --auto
    incus exec v1 -- incus launch images:debian/13 v1v1 --vm
    sleep 30
    incus exec v1 -- incus info v1v1 | grep -F RUNNING

    echo "==> Deleting VM"
    incus delete -f v1

    echo "==> Deleting storage pool"
    incus storage delete "${poolName}"

    echo "==> Restoring profile and deleting network"
    incus profile device remove default eth0
    incus profile device add default eth0 nic nictype=p2p name=eth0
    incus network delete incusbr0
}
