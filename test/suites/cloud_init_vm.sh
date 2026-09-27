test_cloud_init_vm() {
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

    echo "==> Create VM with cloud-init user data"
    incus init images:debian/13/cloud v1 --vm -s "${poolName}"
    incus config set v1 cloud-init.user-data=- << EOF
#cloud-config
runcmd:
  - [ touch, /root/cloud-init-ran ]
EOF
    [ "$(incus config get v1 volatile.apply_template)" = "create" ]

    # The agent seeds cloud-init on first boot and reboots once, then comes up.
    echo "==> Boot the VM"
    incus start v1
    for _ in $(seq 300); do
        incus exec v1 -- true > /dev/null 2>&1 && break
        sleep 1
    done
    incus exec v1 -- true
    [ "$(incus config get v1 volatile.apply_template)" = "" ]
    [ "$(incus config get v1 volatile.vm.needs_reset)" = "" ]

    # Check that cloud-init consumed the seed.
    incus exec v1 -- test -e /var/lib/cloud/seed/nocloud-net/user-data
    for _ in $(seq 60); do
        incus exec v1 -- test -e /root/cloud-init-ran && break
        sleep 1
    done
    incus exec v1 -- test -e /root/cloud-init-ran

    # Check that the VM stays up rather than rebooting again.
    bootID="$(incus exec v1 -- cat /proc/sys/kernel/random/boot_id)"
    sleep 30
    [ "$(incus exec v1 -- cat /proc/sys/kernel/random/boot_id)" = "${bootID}" ]

    # A restart must not seed cloud-init again.
    echo "==> Restart the VM"
    incus restart v1
    for _ in $(seq 300); do
        incus exec v1 -- true > /dev/null 2>&1 && break
        sleep 1
    done
    incus exec v1 -- true
    [ "$(incus config get v1 volatile.apply_template)" = "" ]
    bootID="$(incus exec v1 -- cat /proc/sys/kernel/random/boot_id)"
    sleep 30
    [ "$(incus exec v1 -- cat /proc/sys/kernel/random/boot_id)" = "${bootID}" ]

    echo "==> Deleting VM"
    incus delete -f v1

    echo "==> Deleting storage pool"
    incus storage delete "${poolName}"

    echo "==> Restoring profile and deleting network"
    incus profile device remove default eth0
    incus profile device add default eth0 nic nictype=p2p name=eth0
    incus network delete incusbr0
}
