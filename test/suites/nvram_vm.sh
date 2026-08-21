test_nvram_vm() {
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

    echo "==> Create VM"
    incus init images:debian/13 v1 --vm -s "${poolName}"

    # Update the NVRAM.
    printf "\7\0" | incus low-level nvram set v1 Timeout=- --format=binary
    echo '{"data":20,"attributes":["NON_VOLATILE","BOOTSERVICE_ACCESS","RUNTIME_ACCESS"]}' | incus low-level nvram set v1 MTC:MTC=- --format=json
    incus low-level nvram set v1 Foo=bar --format=binary
    incus low-level nvram set v1 00112233-4455-6677-8899-aabbccddeeff:Foo=baz --format=binary
    incus low-level nvram set v1 FooBar=baz --format=binary --attributes=3

    # Explore the NVRAM.
    [ "$(incus low-level nvram get v1 Boot0000 --format=json | jq -r .data.paths.[0].[0])" = "Fv(7cb8bdc9-f8eb-4f34-aaea-3ee4af6516a1)/FvFile(462caa21-7614-4503-836e-8ab6f4662331)" ]
    printf '\7\0\0\0\7\0' | { exec 3<&0; incus low-level nvram get v1 Timeout --format=efivarfs | cmp -s - /dev/fd/3; }
    [ "$(incus low-level nvram get v1 MTC:MTC --format=base64)" = "FAAAAA==" ]
    [ "$(incus low-level nvram get v1 Foo --format=json)" = '{"attributes":["NON_VOLATILE","BOOTSERVICE_ACCESS","RUNTIME_ACCESS"],"binary":"YmFy"}' ]
    [ "$(incus low-level nvram get v1 00112233-4455-6677-8899-aabbccddeeff:Foo --format=json)" = '{"attributes":["NON_VOLATILE","BOOTSERVICE_ACCESS","RUNTIME_ACCESS"],"binary":"YmF6"}' ]
    [ "$(incus low-level nvram get v1 FooBar --format=json)" = '{"attributes":["NON_VOLATILE","BOOTSERVICE_ACCESS"],"binary":"YmF6"}' ]

    # Bulk-update the NVRAM.
    echo '{"data":6,"attributes":["NON_VOLATILE","BOOTSERVICE_ACCESS","RUNTIME_ACCESS"]}' | incus low-level nvram set v1 Timeout=- FooBar= --format=json
    ! incus low-level nvram get v1 FooBar || false
    [ "$(incus low-level nvram get v1 Timeout --format=hex)" = "0600" ]

    # Check that the OS boots and our NVRAM changes survive it.
    incus start v1
    incus wait v1 agent
    incus stop v1
    [ "$(incus low-level nvram get v1 Boot0000 --format=json | jq -r .data.paths.[0].[0])" = "Fv(7cb8bdc9-f8eb-4f34-aaea-3ee4af6516a1)/FvFile(462caa21-7614-4503-836e-8ab6f4662331)" ]
    # We don’t re-check Timeout and MTC as the firmware surely has messed with them.
    [ "$(incus low-level nvram get v1 Foo --format=binary)" = "bar" ]
    [ "$(incus low-level nvram get v1 00112233-4455-6677-8899-aabbccddeeff:Foo --format=binary)" = "baz" ]

    # Break and repair Secure Boot.
    incus low-level secureboot list v1 db --columns=fs --format=csv | grep -i "microsoft.*uefi" | cut -d, -f1 | xargs incus low-level secureboot remove v1 db
    incus start v1
    sleep 5
    incus console --show-log v1 | grep "Access Denied"
    incus stop -f v1
    curl -L "https://go.microsoft.com/fwlink/?linkid=2239872" | incus low-level secureboot add v1 db - --owner=microsoft
    incus start v1
    sleep 5
    ! incus console --show-log v1 | grep "Access Denied" || false
    incus wait v1 agent
    incus stop v1

    # Force our way into and out of the setup menu.
    incus start v1 --override-boot=0
    sleep 10
    incus console --show-log v1 | grep "Boot Maintenance Manager"
    { sleep 3; printf "\033[A\033[A\r"; } | timeout -p 5 script -efc "incus console v1" /dev/null
    if [ -t 0 ]; then
        stty sane
        tput sgr0
        tput cnorm
    fi
    incus wait v1 agent
    incus stop v1

    echo "==> Deleting VM"
    incus rm -f v1

    echo "==> Deleting storage pool"
    incus storage delete "${poolName}"

    echo "==> Restoring profile and deleting network"
    incus profile device remove default eth0
    incus profile device add default eth0 nic nictype=p2p name=eth0
    incus network delete incusbr0
}
