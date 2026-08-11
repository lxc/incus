cpu_vm_wait_count() {
    # Wait for the VM to show the expected CPU count (hotplugged CPUs take a moment to come online).
    # shellcheck disable=2039,3043
    local vmName expected i
    vmName="${1}"
    expected="${2}"

    for i in $(seq 30); do
        if [ "$(incus exec "${vmName}" -- ls /sys/devices/system/cpu | grep -Ec 'cpu[[:digit:]]+')" -eq "${expected}" ]; then
            return 0
        fi

        sleep 1
    done

    echo "VM ${vmName} CPU count didn't reach ${expected} after ${i}s"
    return 1
}

test_cpu_vm() {
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

    architecture="$(uname -m)"
    if [ "${architecture}" != "x86_64" ] && [ "${architecture}" != "s390x" ]; then
        echo "==> SKIP: CPU hotplugging not supported on ${architecture}"
        export TEST_UNMET_REQUIREMENT="CPU hotplugging not supported on ${architecture}"
        return
    fi

    incus network create incusbr0
    incus profile device remove default eth0
    incus profile device add default eth0 nic network=incusbr0 name=eth0

    poolName="vmpool$$"

    echo "==> Create storage pool"
    incus storage create "${poolName}" dir

    echo "==> Create ephemeral VM and boot"
    incus launch images:debian/13 v1 --vm -s "${poolName}" --ephemeral
    incus wait v1 agent --timeout=90 --interval=1
    incus info v1

    # Get number of CPUs
    # shellcheck disable=SC2010
    cpuCount="$(ls /sys/devices/system/cpu | grep -Ec 'cpu[[:digit:]]+')"

    # VMs should have only 1 CPU per default
    cpu_vm_wait_count v1 1

    # Set valid CPU limits (low to high)
    for i in $(seq 2 "${cpuCount}"); do
        incus config set v1 limits.cpu="${i}"
        cpu_vm_wait_count v1 "${i}"
    done

    # Try setting more CPUs than available
    ! incus config set v1 limits.cpu="$((cpuCount + 1))" || false

    # Set valid CPU limits (high to low)
    for i in $(seq "${cpuCount}" -1 1); do
        incus config set v1 limits.cpu="${i}"
        cpu_vm_wait_count v1 "${i}"
    done

    # Try doing pinning while VM is running (shouldn't work)
    ! incus config set v1 limits.cpu=1,2 || false

    # Set max CPU count
    incus config set v1 limits.cpu="${cpuCount}"
    cpu_vm_wait_count v1 "${cpuCount}"

    # Unset CPU limit
    incus config unset v1 limits.cpu

    # Unsetting the limit should leave the VM with 1 CPU
    cpu_vm_wait_count v1 1

    echo "==> Stopping and deleting ephemeral VM"
    incus stop -f v1
    ! incus info v1 || false

    echo "==> Deleting storage pool"
    incus storage delete "${poolName}"

    echo "==> Restoring profile and deleting network"
    incus profile device remove default eth0
    incus profile device add default eth0 nic nictype=p2p name=eth0
    incus network delete incusbr0
}
