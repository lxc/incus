test_idmap() {
    # Check that we have a big enough range for this test
    if [ ! -e /etc/subuid ] && [ ! -e /etc/subgid ]; then
        UIDs=1000000000
        GIDs=1000000000
        UID_BASE=1000000
        GID_BASE=1000000
    else
        UIDs=0
        GIDs=0
        UID_BASE=0
        GID_BASE=0
        LARGEST_UIDs=0
        LARGEST_GIDs=0

        # shellcheck disable=SC2013
        for entry in $(grep ^root: /etc/subuid); do
            COUNT=$(echo "${entry}" | cut -d: -f3)
            UIDs=$((UIDs + COUNT))

            if [ "${COUNT}" -gt "${LARGEST_UIDs}" ]; then
                LARGEST_UIDs=${COUNT}
                UID_BASE=$(echo "${entry}" | cut -d: -f2)
            fi
        done

        # shellcheck disable=SC2013
        for entry in $(grep ^root: /etc/subgid); do
            COUNT=$(echo "${entry}" | cut -d: -f3)
            GIDs=$((GIDs + COUNT))

            if [ "${COUNT}" -gt "${LARGEST_GIDs}" ]; then
                LARGEST_GIDs=${COUNT}
                GID_BASE=$(echo "${entry}" | cut -d: -f2)
            fi
        done
    fi

    if [ "${UIDs}" -lt 500000 ] || [ "${GIDs}" -lt 500000 ]; then
        echo "==> SKIP: The idmap test requires at least 500000 uids and gids"
        return
    fi

    # Setup daemon
    ensure_import_testimage

    # Check a normal, non-isolated container (full Incus id range)
    incus launch testimage idmap

    incus_backend=$(storage_backend "$INCUS_DIR")
    if [ "$incus_backend" = "btrfs" ]; then
        incus exec idmap -- btrfs subvolume create -r /aaa || true
    fi

    [ "$(incus exec idmap -- cat /proc/self/uid_map | awk '{print $2}')" = "${UID_BASE}" ]
    [ "$(incus exec idmap -- cat /proc/self/gid_map | awk '{print $2}')" = "${GID_BASE}" ]
    [ "$(incus exec idmap -- cat /proc/self/uid_map | awk '{print $3}')" = "${UIDs}" ]
    [ "$(incus exec idmap -- cat /proc/self/gid_map | awk '{print $3}')" = "${GIDs}" ]

    # Confirm that we don't allow double mappings
    ! echo "uid $((UID_BASE + 1)) 1000" | incus config set idmap raw.idmap - || false
    ! echo "gid $((GID_BASE + 1)) 1000" | incus config set idmap raw.idmap - || false

    # Convert container to isolated and confirm it's not using the first range
    incus config set idmap security.idmap.isolated true
    incus restart idmap --force
    [ "$(incus exec idmap -- cat /proc/self/uid_map | awk '{print $2}')" = "$((UID_BASE + 65536))" ]
    [ "$(incus exec idmap -- cat /proc/self/gid_map | awk '{print $2}')" = "$((GID_BASE + 65536))" ]
    [ "$(incus exec idmap -- cat /proc/self/uid_map | awk '{print $3}')" = "65536" ]
    [ "$(incus exec idmap -- cat /proc/self/gid_map | awk '{print $3}')" = "65536" ]

    # Bump allocation size
    incus config set idmap security.idmap.size 100000
    incus restart idmap --force
    [ "$(incus exec idmap -- cat /proc/self/uid_map | awk '{print $2}')" != "${UID_BASE}" ]
    [ "$(incus exec idmap -- cat /proc/self/gid_map | awk '{print $2}')" != "${GID_BASE}" ]
    [ "$(incus exec idmap -- cat /proc/self/uid_map | awk '{print $3}')" = "100000" ]
    [ "$(incus exec idmap -- cat /proc/self/gid_map | awk '{print $3}')" = "100000" ]

    # Test using a custom base
    incus config set idmap security.idmap.base $((UID_BASE + 12345))
    incus config set idmap security.idmap.size 110000
    incus restart idmap --force
    [ "$(incus exec idmap -- cat /proc/self/uid_map | awk '{print $2}')" = "$((UID_BASE + 12345))" ]
    [ "$(incus exec idmap -- cat /proc/self/gid_map | awk '{print $2}')" = "$((GID_BASE + 12345))" ]
    [ "$(incus exec idmap -- cat /proc/self/uid_map | awk '{print $3}')" = "110000" ]
    [ "$(incus exec idmap -- cat /proc/self/gid_map | awk '{print $3}')" = "110000" ]

    # Switch back to full Incus range
    incus config unset idmap security.idmap.base
    incus config unset idmap security.idmap.isolated
    incus config unset idmap security.idmap.size
    incus restart idmap --force
    [ "$(incus exec idmap -- cat /proc/self/uid_map | awk '{print $2}')" = "${UID_BASE}" ]
    [ "$(incus exec idmap -- cat /proc/self/gid_map | awk '{print $2}')" = "${GID_BASE}" ]
    [ "$(incus exec idmap -- cat /proc/self/uid_map | awk '{print $3}')" = "${UIDs}" ]
    [ "$(incus exec idmap -- cat /proc/self/gid_map | awk '{print $3}')" = "${GIDs}" ]
    incus delete idmap --force

    # Confirm id recycling
    incus launch testimage idmap -c security.idmap.isolated=true
    [ "$(incus exec idmap -- cat /proc/self/uid_map | awk '{print $2}')" = "$((UID_BASE + 65536))" ]
    [ "$(incus exec idmap -- cat /proc/self/gid_map | awk '{print $2}')" = "$((GID_BASE + 65536))" ]
    [ "$(incus exec idmap -- cat /proc/self/uid_map | awk '{print $3}')" = "65536" ]
    [ "$(incus exec idmap -- cat /proc/self/gid_map | awk '{print $3}')" = "65536" ]

    # Copy and check that the base differs
    incus copy idmap idmap1
    incus start idmap1
    [ "$(incus exec idmap1 -- cat /proc/self/uid_map | awk '{print $2}')" = "$((UID_BASE + 131072))" ]
    [ "$(incus exec idmap1 -- cat /proc/self/gid_map | awk '{print $2}')" = "$((GID_BASE + 131072))" ]
    [ "$(incus exec idmap1 -- cat /proc/self/uid_map | awk '{print $3}')" = "65536" ]
    [ "$(incus exec idmap1 -- cat /proc/self/gid_map | awk '{print $3}')" = "65536" ]

    # Validate non-overlapping maps
    incus exec idmap -- touch /a
    ! incus exec idmap -- chown 65536 /a || false
    incus exec idmap -- chown 65535 /a
    PID_1=$(incus info idmap | awk '/^PID/ {print $2}')
    UID_1=$(stat -c '%u' "/proc/${PID_1}/root/a")

    incus exec idmap1 -- touch /a
    PID_2=$(incus info idmap1 | awk '/^PID/ {print $2}')
    UID_2=$(stat -c '%u' "/proc/${PID_2}/root/a")

    [ "${UID_1}" != "${UID_2}" ]
    [ "${UID_2}" = "$((UID_1 + 1))" ]

    # Check profile inheritance
    incus profile create idmap
    incus profile set idmap security.idmap.isolated true
    incus profile set idmap security.idmap.size 100000

    incus launch testimage idmap2
    [ "$(incus exec idmap2 -- cat /proc/self/uid_map | awk '{print $2}')" = "${UID_BASE}" ]
    [ "$(incus exec idmap2 -- cat /proc/self/gid_map | awk '{print $2}')" = "${GID_BASE}" ]
    [ "$(incus exec idmap2 -- cat /proc/self/uid_map | awk '{print $3}')" = "${UIDs}" ]
    [ "$(incus exec idmap2 -- cat /proc/self/gid_map | awk '{print $3}')" = "${GIDs}" ]

    incus profile add idmap idmap
    incus profile add idmap1 idmap
    incus profile add idmap2 idmap
    incus restart idmap idmap1 idmap2 --force
    incus launch testimage idmap3 -p default -p idmap

    UID_1=$(incus exec idmap -- cat /proc/self/uid_map | awk '{print $2}')
    GID_1=$(incus exec idmap -- cat /proc/self/gid_map | awk '{print $2}')
    [ "$(incus exec idmap -- cat /proc/self/uid_map | awk '{print $2}')" != "${UID_BASE}" ]
    [ "$(incus exec idmap -- cat /proc/self/gid_map | awk '{print $2}')" != "${GID_BASE}" ]
    [ "$(incus exec idmap -- cat /proc/self/uid_map | awk '{print $3}')" = "100000" ]
    [ "$(incus exec idmap -- cat /proc/self/gid_map | awk '{print $3}')" = "100000" ]

    UID_2=$(incus exec idmap1 -- cat /proc/self/uid_map | awk '{print $2}')
    GID_2=$(incus exec idmap1 -- cat /proc/self/gid_map | awk '{print $2}')
    [ "$(incus exec idmap1 -- cat /proc/self/uid_map | awk '{print $2}')" != "${UID_BASE}" ]
    [ "$(incus exec idmap1 -- cat /proc/self/gid_map | awk '{print $2}')" != "${GID_BASE}" ]
    [ "$(incus exec idmap1 -- cat /proc/self/uid_map | awk '{print $3}')" = "100000" ]
    [ "$(incus exec idmap1 -- cat /proc/self/gid_map | awk '{print $3}')" = "100000" ]

    UID_3=$(incus exec idmap2 -- cat /proc/self/uid_map | awk '{print $2}')
    GID_3=$(incus exec idmap2 -- cat /proc/self/gid_map | awk '{print $2}')
    [ "$(incus exec idmap2 -- cat /proc/self/uid_map | awk '{print $2}')" != "${UID_BASE}" ]
    [ "$(incus exec idmap2 -- cat /proc/self/gid_map | awk '{print $2}')" != "${GID_BASE}" ]
    [ "$(incus exec idmap2 -- cat /proc/self/uid_map | awk '{print $3}')" = "100000" ]
    [ "$(incus exec idmap2 -- cat /proc/self/gid_map | awk '{print $3}')" = "100000" ]

    UID_4=$(incus exec idmap3 -- cat /proc/self/uid_map | awk '{print $2}')
    GID_4=$(incus exec idmap3 -- cat /proc/self/gid_map | awk '{print $2}')
    [ "$(incus exec idmap3 -- cat /proc/self/uid_map | awk '{print $2}')" != "${UID_BASE}" ]
    [ "$(incus exec idmap3 -- cat /proc/self/gid_map | awk '{print $2}')" != "${GID_BASE}" ]
    [ "$(incus exec idmap3 -- cat /proc/self/uid_map | awk '{print $3}')" = "100000" ]
    [ "$(incus exec idmap3 -- cat /proc/self/gid_map | awk '{print $3}')" = "100000" ]

    [ "${UID_1}" != "${UID_2}" ]
    [ "${UID_1}" != "${UID_3}" ]
    [ "${UID_1}" != "${UID_4}" ]
    [ "${UID_2}" != "${UID_3}" ]
    [ "${UID_2}" != "${UID_4}" ]
    [ "${UID_3}" != "${UID_4}" ]

    [ "${GID_1}" != "${GID_2}" ]
    [ "${GID_1}" != "${GID_3}" ]
    [ "${GID_1}" != "${GID_4}" ]
    [ "${GID_2}" != "${GID_3}" ]
    [ "${GID_2}" != "${GID_4}" ]
    [ "${UID_3}" != "${UID_4}" ]

    incus delete idmap1 idmap2 idmap3 --force

    # Test running out of ids
    ! incus launch testimage idmap1 -c security.idmap.isolated=true -c security.idmap.size=$((UIDs + 1)) || false

    # Test raw id maps
    (
        cat << EOF
uid ${UID_BASE} 1000000
gid $((GID_BASE + 1)) 1000000
both $((UID_BASE + 2)) 2000000
EOF
    ) | incus config set idmap raw.idmap -
    incus restart idmap --force
    PID=$(incus info idmap | awk '/^PID/ {print $2}')

    incus exec idmap -- touch /a
    incus exec idmap -- chown 1000000:1000000 /a
    [ "$(stat -c '%u:%g' "/proc/${PID}/root/a")" = "${UID_BASE}:$((GID_BASE + 1))" ]

    incus exec idmap -- touch /b
    incus exec idmap -- chown 2000000:2000000 /b
    [ "$(stat -c '%u:%g' "/proc/${PID}/root/b")" = "$((UID_BASE + 2)):$((GID_BASE + 2))" ]

    # Test id ranges
    (
        cat << EOF
uid $((UID_BASE + 10))-$((UID_BASE + 19)) 3000000-3000009
gid $((GID_BASE + 10))-$((GID_BASE + 19)) 3000000-3000009
both $((GID_BASE + 20))-$((GID_BASE + 29)) 4000000-4000009
EOF
    ) | incus config set idmap raw.idmap -
    incus restart idmap --force
    PID=$(incus info idmap | awk '/^PID/ {print $2}')

    incus exec idmap -- touch /c
    incus exec idmap -- chown 3000009:3000009 /c
    [ "$(stat -c '%u:%g' "/proc/${PID}/root/c")" = "$((UID_BASE + 19)):$((GID_BASE + 19))" ]

    incus exec idmap -- touch /d
    incus exec idmap -- chown 4000009:4000009 /d
    [ "$(stat -c '%u:%g' "/proc/${PID}/root/d")" = "$((UID_BASE + 29)):$((GID_BASE + 29))" ]

    incus delete idmap --force
}
