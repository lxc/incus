# shellcheck shell=sh

test_storage_driver_linstor() {
    # shellcheck disable=2039,3043
    local INCUS_STORAGE_DIR incus_backend

    incus_backend=$(storage_backend "$INCUS_DIR")
    if [ "$incus_backend" != "linstor" ]; then
        return
    fi

    INCUS_STORAGE_DIR=$(mktemp -d -p "${TEST_DIR}" XXXXXXXXX)
    chmod +x "${INCUS_STORAGE_DIR}"
    spawn_incus "${INCUS_STORAGE_DIR}" false
    linstor_preconfigure "${INCUS_STORAGE_DIR}"

    (
        set -e
        # shellcheck disable=2030
        INCUS_DIR="${INCUS_STORAGE_DIR}"

        # shellcheck disable=SC1009
        incus storage create "incustest-$(basename "${INCUS_DIR}")-pool1" linstor linstor.resource_group.place_count=1

        # Set default storage pool for image import.
        incus profile device add default root disk path="/" pool="incustest-$(basename "${INCUS_DIR}")-pool1"

        # Import image into default storage pool.
        ensure_import_testimage

        # Test that no invalid LINSTOR storage pool configuration keys can be set.
        ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-linstor-pool-config" linstor lvm.thinpool_name=bla || false
        ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-linstor-pool-config" linstor lvm.use_thinpool=false || false
        ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-linstor-pool-config" linstor lvm.vg_name=bla || false
        ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-linstor-pool-config" linstor drbd.auto_diskful=bla || false
        ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-linstor-pool-config" linstor drbd.auto_diskful=1s || false
        ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-linstor-pool-config" linstor drbd.on_no_quorum=bla || false
        ! incus storage create "incustest-$(basename "${INCUS_DIR}")-invalid-linstor-pool-config" linstor drbd.auto_add_quorum_tiebreaker=bla || false

        # Test that all valid LINSTOR storage pool configuration keys can be set.
        incus storage create "incustest-$(basename "${INCUS_DIR}")-valid-linstor-pool-config" linstor linstor.resource_group.place_count=2 linstor.resource_group.storage_pool=foo linstor.volume.prefix=bar drbd.auto_diskful=1h drbd.on_no_quorum=io-error drbd.auto_add_quorum_tiebreaker=true
        linstor resource-group list-properties "incustest-$(basename "${INCUS_DIR}")-valid-linstor-pool-config" | grep 'DrbdOptions/auto-diskful.*60'
        linstor resource-group list-properties "incustest-$(basename "${INCUS_DIR}")-valid-linstor-pool-config" | grep 'DrbdOptions/Resource/on-no-quorum.*io-error'
        linstor resource-group list-properties "incustest-$(basename "${INCUS_DIR}")-valid-linstor-pool-config" | grep 'DrbdOptions/auto-add-quorum-tiebreaker.*true'
        incus storage delete "incustest-$(basename "${INCUS_DIR}")-valid-linstor-pool-config"

        # Test that all valid LINSTOR volume configuration keys can be set (except DrbdOptions/Resource/on-no-quorum, which won’t appear here).
        incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool1" c3 drbd.auto_diskful=5m drbd.auto_add_quorum_tiebreaker=true
        linstor -m resource-definition list | jq -e 'any(.[0][];
                     .props["DrbdOptions/auto-add-quorum-tiebreaker"] == "true" and
                     .props["DrbdOptions/auto-diskful"] == "300" and
                     .props["Aux/Incus/name"] == "incus-volume-default_c3")'
        incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool1" c3

        # Muck around with some containers on our pool.
        incus init testimage c1 -s "incustest-$(basename "${INCUS_DIR}")-pool1"
        incus list -c b c1 | grep "incustest-$(basename "${INCUS_DIR}")-pool1"

        incus launch testimage c2 -s "incustest-$(basename "${INCUS_DIR}")-pool1"
        incus list -c b c2 | grep "incustest-$(basename "${INCUS_DIR}")-pool1"

        incus init testimage a -s "incustest-$(basename "${INCUS_DIR}")-pool1"
        incus copy a b
        incus delete a
        incus init testimage a -s "incustest-$(basename "${INCUS_DIR}")-pool1"
        incus copy a c
        incus delete a
        incus delete b
        incus delete c

        incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool1" c1
        incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool1" c1 c1 testDevice /opt
        ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool1" c1 c1 testDevice2 /opt || false
        incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool1" c1 c1
        incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool1" custom/c1 c1 testDevice /opt
        ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool1" custom/c1 c1 testDevice2 /opt || false
        incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool1" c1 c1

        incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool1" c2
        incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool1" c2 c2 testDevice /opt
        ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool1" c2 c2 testDevice2 /opt || false
        incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool1" c2 c2
        incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool1" c2 c2 testDevice /opt
        ! incus storage volume attach "incustest-$(basename "${INCUS_DIR}")-pool1" c2 c2 testDevice2 /opt || false
        incus storage volume detach "incustest-$(basename "${INCUS_DIR}")-pool1" c2 c2

        incus delete -f c1
        incus delete -f c2

        incus storage volume set "incustest-$(basename "${INCUS_DIR}")-pool1" c1 size 500MiB
        incus storage volume unset "incustest-$(basename "${INCUS_DIR}")-pool1" c1 size

        # Validate that we can restore to previous snapshots given that linstor.remove_snapshots is set
        incus storage volume create "incustest-$(basename "${INCUS_DIR}")-pool1" c3
        incus storage volume snapshot create "incustest-$(basename "${INCUS_DIR}")-pool1" c3 snap0
        incus storage volume snapshot create "incustest-$(basename "${INCUS_DIR}")-pool1" c3 snap1
        ! incus storage volume snapshot restore "incustest-$(basename "${INCUS_DIR}")-pool1" c3 snap0 || false
        incus storage volume set "incustest-$(basename "${INCUS_DIR}")-pool1" c3 linstor.remove_snapshots=true
        incus storage volume snapshot restore "incustest-$(basename "${INCUS_DIR}")-pool1" c3 snap0 || false
        incus storage volume list "incustest-$(basename "${INCUS_DIR}")-pool1" | grep snap0
        ! incus storage volume list "incustest-$(basename "${INCUS_DIR}")-pool1" | grep snap1 || false

        # Cleanup
        incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool1" c1
        incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool1" c2
        incus storage volume delete "incustest-$(basename "${INCUS_DIR}")-pool1" c3
        incus image delete testimage
        incus profile device remove default root
        incus storage delete "incustest-$(basename "${INCUS_DIR}")-pool1"
    )

    # shellcheck disable=SC2031
    kill_incus "${INCUS_STORAGE_DIR}"
}
