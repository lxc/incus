# Activating SR-IOV VFs:
# Mellanox:
# sudo rmmod mlx4_ib mlx4_en mlx4_core
# sudo modprobe mlx4_core num_vfs=2,0 probe_vf=2,0
#
# Intel:
# sudo rmmod igb
# sudo modprobe igb max_vfs=2
test_container_devices_nic_sriov() {
    ensure_import_testimage
    ensure_has_localhost_remote "${INCUS_ADDR}"

    IF_UP_TIMEOUT=15

    parent=${INCUS_NIC_SRIOV_PARENT:-""}

    if [ "$parent" = "" ]; then
        echo "==> SKIP: No SR-IOV NIC parent specified"
        return
    fi

    ctName="nt$$"
    macRand=$(shuf -i 0-9 -n 1)
    ctMAC1="da:da:9d:42:e5:f${macRand}"
    ctMAC2="da:da:9d:42:e5:e${macRand}"

    # Set a known start point config
    ip link set "${parent}" up

    # Record how many nics we started with.
    startNicCount=$(find /sys/class/net | wc -l)

    # Test basic container with SR-IOV NIC. Add 2 devices to check reservation system works.
    incus init testimage "${ctName}"
    incus config device add "${ctName}" eth0 nic \
        nictype=sriov \
        parent="${parent}"
    incus config device add "${ctName}" eth1 nic \
        nictype=sriov \
        parent="${parent}"
    incus start "${ctName}"

    # Check spoof checking has been disabled (the default).
    vfID=$(incus config get "${ctName}" volatile.eth0.last_state.vf.id)
    if ip link show "${parent}" | grep "vf ${vfID}" | grep "spoof checking on"; then
        echo "spoof checking is still enabled"
        false
    fi

    # Check trusted has been disabled (the default if NIC supports it).
    vfID=$(incus config get "${ctName}" volatile.eth0.last_state.vf.id)
    if ip link show "${parent}" | grep "vf ${vfID}" | grep "trust on"; then
        echo "trusted is still enabled"
        false
    fi

    incus config device set "${ctName}" eth0 vlan 1234

    # Check custom vlan has been enabled.
    vfID=$(incus config get "${ctName}" volatile.eth0.last_state.vf.id)
    if ! ip link show "${parent}" | grep "vf ${vfID}" | grep "vlan 1234"; then
        echo "vlan not set"
        false
    fi

    incus config device set "${ctName}" eth0 security.mac_filtering true

    # Check spoof checking has been enabled
    vfID=$(incus config get "${ctName}" volatile.eth0.last_state.vf.id)
    if ! ip link show "${parent}" | grep "vf ${vfID}" | grep "spoof checking on"; then
        echo "spoof checking is still disabled"
        false
    fi

    incus config device set "${ctName}" eth0 vlan 0

    # Check custom vlan has been disabled.
    vfID=$(incus config get "${ctName}" volatile.eth0.last_state.vf.id)
    if ip link show "${parent}" | grep "vf ${vfID}" | grep "vlan"; then
        # Mellanox cards display vlan 0 as vlan 4095!
        if ! ip link show "${parent}" | grep "vf ${vfID}" | grep "vlan 4095"; then
            echo "vlan still set"
            false
        fi
    fi

    # Check volatile cleanup on stop.
    incus stop -f "${ctName}"
    if incus config show "${ctName}" | grep volatile.eth0 | grep -v volatile.eth0.hwaddr | grep -v volatile.eth0.name; then
        echo "unexpected volatile key remains"
        false
    fi

    # Remove 2nd device whilst stopped.
    incus config device remove "${ctName}" eth1

    # Set custom MAC
    incus config device set "${ctName}" eth0 hwaddr "${ctMAC1}"
    incus start "${ctName}"

    # Check custom MAC is applied.
    vfID=$(incus config get "${ctName}" volatile.eth0.last_state.vf.id)
    if ! ip link show "${parent}" | grep "vf ${vfID}" | grep "${ctMAC1}"; then
        echo "eth0 MAC not set"
        false
    fi

    incus stop -f "${ctName}"

    # Disable mac filtering and try fresh boot.
    incus config device set "${ctName}" eth0 security.mac_filtering false
    incus start "${ctName}"

    # Check spoof checking has been disabled (the default).
    vfID=$(incus config get "${ctName}" volatile.eth0.last_state.vf.id)
    if ! ip link show "${parent}" | grep "vf ${vfID}" | grep "spoof checking off"; then
        echo "spoof checking is still enabled"
        false
    fi

    # Hot plug fresh device.
    incus config device add "${ctName}" eth1 nic \
        nictype=sriov \
        parent="${parent}" \
        security.mac_filtering=true

    # Check spoof checking has been enabled.
    vfID=$(incus config get "${ctName}" volatile.eth1.last_state.vf.id)
    if ! ip link show "${parent}" | grep "vf ${vfID}" | grep "spoof checking on"; then
        echo "spoof checking is still disabled"
        false
    fi

    incus stop -f "${ctName}"

    # Remove 2nd device whilst stopped.
    incus config device remove "${ctName}" eth1

    incus start "${ctName}"

    incus config device set "${ctName}" eth0 security.trusted true

    # Check trusted property has been enabled
    vfID=$(incus config get "${ctName}" volatile.eth0.last_state.vf.id)
    if ! ip link show "${parent}" | grep "vf ${vfID}" | grep "trust on"; then
        echo "trusted is still disabled"
        false
    fi

    incus stop -f "${ctName}"

    # Disable trusted and try fresh boot.
    incus config device set "${ctName}" eth0 security.trusted false
    incus start "${ctName}"

    # Check trusted has been disabled (the default).
    vfID=$(incus config get "${ctName}" volatile.eth0.last_state.vf.id)
    if ! ip link show "${parent}" | grep "vf ${vfID}" | grep "trust off"; then
        echo "trusted is still enabled"
        false
    fi

    # Hot plug fresh device.
    incus config device add "${ctName}" eth1 nic \
        nictype=sriov \
        parent="${parent}" \
        security.trusted=true

    # Check trusted has been enabled.
    vfID=$(incus config get "${ctName}" volatile.eth1.last_state.vf.id)
    if ! ip link show "${parent}" | grep "vf ${vfID}" | grep "trust on"; then
        echo "trusted is still disabled"
        false
    fi

    incus stop -f "${ctName}"

    # Test setting MAC offline.
    incus config device set "${ctName}" eth1 hwaddr "${ctMAC2}"
    incus start "${ctName}"

    # Check custom MAC is applied.
    vfID=$(incus config get "${ctName}" volatile.eth1.last_state.vf.id)
    if ! ip link show "${parent}" | grep "vf ${vfID}" | grep "${ctMAC2}"; then
        echo "eth1 MAC not set"
        false
    fi

    incus stop -f "${ctName}"
    incus config device remove "${ctName}" eth0
    incus config device remove "${ctName}" eth1

    # Create sriov network and add NIC device using that network.
    incus network create "${ctName}net" --type=sriov parent="${parent}"
    incus config device add "${ctName}" eth0 nic \
        network="${ctName}net" \
        name=eth0 \
        hwaddr="${ctMAC1}"
    incus start "${ctName}"

    # Check custom MAC is applied.
    vfID=$(incus config get "${ctName}" volatile.eth0.last_state.vf.id)
    if ! ip link show "${parent}" | grep "vf ${vfID}" | grep "${ctMAC1}"; then
        echo "eth0 MAC not set"
        false
    fi

    # Test attached key.
    incus config device remove "${ctName}" eth0
    incus config device add "${ctName}" eth0 nic network="${ctName}net" name=eth0
    incus exec "${ctName}" ip link set eth0 up

    # Interfaces don't immediately become operationally up
    for attempt in $(seq "${IF_UP_TIMEOUT}"); do
        if [ "$(incus file pull "${ctName}/sys/class/net/eth0/operstate" -)" = "up" ]; then
            break
        fi
        if [ "${attempt}" -eq "${IF_UP_TIMEOUT}" ]; then
            echo "eth0 in instance did not enter operstate up in time"
            false
        fi
        sleep 1
    done

    incus config device set "${ctName}" eth0 attached=false
    ! incus file pull "${ctName}/sys/class/net/eth0/operstate" - || false

    incus config device set "${ctName}" eth0 attached=true
    incus exec "${ctName}" ip link set eth0 up

    # Interfaces don't immediately become operationally up
    for attempt in $(seq "${IF_UP_TIMEOUT}"); do
        if [ "$(incus file pull "${ctName}/sys/class/net/eth0/operstate" -)" = "up" ]; then
            break
        fi
        if [ "${attempt}" -eq "${IF_UP_TIMEOUT}" ]; then
            echo "eth0 in instance did not enter operstate up in time"
            false
        fi
        sleep 1
    done

    incus config device remove "${ctName}" eth0

    incus network delete "${ctName}net"

    incus delete -f "${ctName}"

    # Check we haven't left any NICS lying around.
    endNicCount=$(find /sys/class/net | wc -l)
    if [ "$startNicCount" != "$endNicCount" ]; then
        echo "leftover NICS detected"
        false
    fi
}
