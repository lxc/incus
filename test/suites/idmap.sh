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
      UIDs=$((UIDs+COUNT))

      if [ "${COUNT}" -gt "${LARGEST_UIDs}" ]; then
        LARGEST_UIDs=${COUNT}
        UID_BASE=$(echo "${entry}" | cut -d: -f2)
      fi
    done

    # shellcheck disable=SC2013
    for entry in $(grep ^root: /etc/subgid); do
      COUNT=$(echo "${entry}" | cut -d: -f3)
      GIDs=$((GIDs+COUNT))

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
  inc launch testimage idmap

  incus_backend=$(storage_backend "$INCUS_DIR")
  if [ "$incus_backend" = "btrfs" ]; then
    inc exec idmap -- btrfs subvolume create -r /aaa || true
  fi

  [ "$(inc exec idmap -- cat /proc/self/uid_map | awk '{print $2}')" = "${UID_BASE}" ]
  [ "$(inc exec idmap -- cat /proc/self/gid_map | awk '{print $2}')" = "${GID_BASE}" ]
  [ "$(inc exec idmap -- cat /proc/self/uid_map | awk '{print $3}')" = "${UIDs}" ]
  [ "$(inc exec idmap -- cat /proc/self/gid_map | awk '{print $3}')" = "${GIDs}" ]

  # Confirm that we don't allow double mappings
  ! echo "uid $((UID_BASE+1)) 1000" | inc config set idmap raw.idmap - || false
  ! echo "gid $((GID_BASE+1)) 1000" | inc config set idmap raw.idmap - || false

  # Convert container to isolated and confirm it's not using the first range
  inc config set idmap security.idmap.isolated true
  inc restart idmap --force
  [ "$(inc exec idmap -- cat /proc/self/uid_map | awk '{print $2}')" = "$((UID_BASE+65536))" ]
  [ "$(inc exec idmap -- cat /proc/self/gid_map | awk '{print $2}')" = "$((GID_BASE+65536))" ]
  [ "$(inc exec idmap -- cat /proc/self/uid_map | awk '{print $3}')" = "65536" ]
  [ "$(inc exec idmap -- cat /proc/self/gid_map | awk '{print $3}')" = "65536" ]

  # Bump allocation size
  inc config set idmap security.idmap.size 100000
  inc restart idmap --force
  [ "$(inc exec idmap -- cat /proc/self/uid_map | awk '{print $2}')" != "${UID_BASE}" ]
  [ "$(inc exec idmap -- cat /proc/self/gid_map | awk '{print $2}')" != "${GID_BASE}" ]
  [ "$(inc exec idmap -- cat /proc/self/uid_map | awk '{print $3}')" = "100000" ]
  [ "$(inc exec idmap -- cat /proc/self/gid_map | awk '{print $3}')" = "100000" ]

  # Test using a custom base
  inc config set idmap security.idmap.base $((UID_BASE+12345))
  inc config set idmap security.idmap.size 110000
  inc restart idmap --force
  [ "$(inc exec idmap -- cat /proc/self/uid_map | awk '{print $2}')" = "$((UID_BASE+12345))" ]
  [ "$(inc exec idmap -- cat /proc/self/gid_map | awk '{print $2}')" = "$((GID_BASE+12345))" ]
  [ "$(inc exec idmap -- cat /proc/self/uid_map | awk '{print $3}')" = "110000" ]
  [ "$(inc exec idmap -- cat /proc/self/gid_map | awk '{print $3}')" = "110000" ]

  # Switch back to full Incus range
  inc config unset idmap security.idmap.base
  inc config unset idmap security.idmap.isolated
  inc config unset idmap security.idmap.size
  inc restart idmap --force
  [ "$(inc exec idmap -- cat /proc/self/uid_map | awk '{print $2}')" = "${UID_BASE}" ]
  [ "$(inc exec idmap -- cat /proc/self/gid_map | awk '{print $2}')" = "${GID_BASE}" ]
  [ "$(inc exec idmap -- cat /proc/self/uid_map | awk '{print $3}')" = "${UIDs}" ]
  [ "$(inc exec idmap -- cat /proc/self/gid_map | awk '{print $3}')" = "${GIDs}" ]
  inc delete idmap --force

  # Confirm id recycling
  inc launch testimage idmap -c security.idmap.isolated=true
  [ "$(inc exec idmap -- cat /proc/self/uid_map | awk '{print $2}')" = "$((UID_BASE+65536))" ]
  [ "$(inc exec idmap -- cat /proc/self/gid_map | awk '{print $2}')" = "$((GID_BASE+65536))" ]
  [ "$(inc exec idmap -- cat /proc/self/uid_map | awk '{print $3}')" = "65536" ]
  [ "$(inc exec idmap -- cat /proc/self/gid_map | awk '{print $3}')" = "65536" ]

  # Copy and check that the base differs
  inc copy idmap idmap1
  inc start idmap1
  [ "$(inc exec idmap1 -- cat /proc/self/uid_map | awk '{print $2}')" = "$((UID_BASE+131072))" ]
  [ "$(inc exec idmap1 -- cat /proc/self/gid_map | awk '{print $2}')" = "$((GID_BASE+131072))" ]
  [ "$(inc exec idmap1 -- cat /proc/self/uid_map | awk '{print $3}')" = "65536" ]
  [ "$(inc exec idmap1 -- cat /proc/self/gid_map | awk '{print $3}')" = "65536" ]

  # Validate non-overlapping maps
  inc exec idmap -- touch /a
  ! inc exec idmap -- chown 65536 /a || false
  inc exec idmap -- chown 65535 /a
  PID_1=$(inc info idmap | awk '/^PID/ {print $2}')
  UID_1=$(stat -c '%u' "/proc/${PID_1}/root/a")

  inc exec idmap1 -- touch /a
  PID_2=$(inc info idmap1 | awk '/^PID/ {print $2}')
  UID_2=$(stat -c '%u' "/proc/${PID_2}/root/a")

  [ "${UID_1}" != "${UID_2}" ]
  [ "${UID_2}" = "$((UID_1+1))" ]

  # Check profile inheritance
  inc profile create idmap
  inc profile set idmap security.idmap.isolated true
  inc profile set idmap security.idmap.size 100000

  inc launch testimage idmap2
  [ "$(inc exec idmap2 -- cat /proc/self/uid_map | awk '{print $2}')" = "${UID_BASE}" ]
  [ "$(inc exec idmap2 -- cat /proc/self/gid_map | awk '{print $2}')" = "${GID_BASE}" ]
  [ "$(inc exec idmap2 -- cat /proc/self/uid_map | awk '{print $3}')" = "${UIDs}" ]
  [ "$(inc exec idmap2 -- cat /proc/self/gid_map | awk '{print $3}')" = "${GIDs}" ]

  inc profile add idmap idmap
  inc profile add idmap1 idmap
  inc profile add idmap2 idmap
  inc restart idmap idmap1 idmap2 --force
  inc launch testimage idmap3 -p default -p idmap

  UID_1=$(inc exec idmap -- cat /proc/self/uid_map | awk '{print $2}')
  GID_1=$(inc exec idmap -- cat /proc/self/gid_map | awk '{print $2}')
  [ "$(inc exec idmap -- cat /proc/self/uid_map | awk '{print $2}')" != "${UID_BASE}" ]
  [ "$(inc exec idmap -- cat /proc/self/gid_map | awk '{print $2}')" != "${GID_BASE}" ]
  [ "$(inc exec idmap -- cat /proc/self/uid_map | awk '{print $3}')" = "100000" ]
  [ "$(inc exec idmap -- cat /proc/self/gid_map | awk '{print $3}')" = "100000" ]

  UID_2=$(inc exec idmap1 -- cat /proc/self/uid_map | awk '{print $2}')
  GID_2=$(inc exec idmap1 -- cat /proc/self/gid_map | awk '{print $2}')
  [ "$(inc exec idmap1 -- cat /proc/self/uid_map | awk '{print $2}')" != "${UID_BASE}" ]
  [ "$(inc exec idmap1 -- cat /proc/self/gid_map | awk '{print $2}')" != "${GID_BASE}" ]
  [ "$(inc exec idmap1 -- cat /proc/self/uid_map | awk '{print $3}')" = "100000" ]
  [ "$(inc exec idmap1 -- cat /proc/self/gid_map | awk '{print $3}')" = "100000" ]

  UID_3=$(inc exec idmap2 -- cat /proc/self/uid_map | awk '{print $2}')
  GID_3=$(inc exec idmap2 -- cat /proc/self/gid_map | awk '{print $2}')
  [ "$(inc exec idmap2 -- cat /proc/self/uid_map | awk '{print $2}')" != "${UID_BASE}" ]
  [ "$(inc exec idmap2 -- cat /proc/self/gid_map | awk '{print $2}')" != "${GID_BASE}" ]
  [ "$(inc exec idmap2 -- cat /proc/self/uid_map | awk '{print $3}')" = "100000" ]
  [ "$(inc exec idmap2 -- cat /proc/self/gid_map | awk '{print $3}')" = "100000" ]

  UID_4=$(inc exec idmap3 -- cat /proc/self/uid_map | awk '{print $2}')
  GID_4=$(inc exec idmap3 -- cat /proc/self/gid_map | awk '{print $2}')
  [ "$(inc exec idmap3 -- cat /proc/self/uid_map | awk '{print $2}')" != "${UID_BASE}" ]
  [ "$(inc exec idmap3 -- cat /proc/self/gid_map | awk '{print $2}')" != "${GID_BASE}" ]
  [ "$(inc exec idmap3 -- cat /proc/self/uid_map | awk '{print $3}')" = "100000" ]
  [ "$(inc exec idmap3 -- cat /proc/self/gid_map | awk '{print $3}')" = "100000" ]

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

  inc delete idmap1 idmap2 idmap3 --force

  # Test running out of ids
  ! inc launch testimage idmap1 -c security.idmap.isolated=true -c security.idmap.size=$((UIDs+1)) || false

  # Test raw id maps
  (
  cat << EOF
uid ${UID_BASE} 1000000
gid $((GID_BASE+1)) 1000000
both $((UID_BASE+2)) 2000000
EOF
  ) | inc config set idmap raw.idmap -
  inc restart idmap --force
  PID=$(inc info idmap | awk '/^PID/ {print $2}')

  inc exec idmap -- touch /a
  inc exec idmap -- chown 1000000:1000000 /a
  [ "$(stat -c '%u:%g' "/proc/${PID}/root/a")" = "${UID_BASE}:$((GID_BASE+1))" ]

  inc exec idmap -- touch /b
  inc exec idmap -- chown 2000000:2000000 /b
  [ "$(stat -c '%u:%g' "/proc/${PID}/root/b")" = "$((UID_BASE+2)):$((GID_BASE+2))" ]

  # Test id ranges
  (
  cat << EOF
uid $((UID_BASE+10))-$((UID_BASE+19)) 3000000-3000009
gid $((GID_BASE+10))-$((GID_BASE+19)) 3000000-3000009
both $((GID_BASE+20))-$((GID_BASE+29)) 4000000-4000009
EOF
  ) | inc config set idmap raw.idmap -
  inc restart idmap --force
  PID=$(inc info idmap | awk '/^PID/ {print $2}')

  inc exec idmap -- touch /c
  inc exec idmap -- chown 3000009:3000009 /c
  [ "$(stat -c '%u:%g' "/proc/${PID}/root/c")" = "$((UID_BASE+19)):$((GID_BASE+19))" ]

  inc exec idmap -- touch /d
  inc exec idmap -- chown 4000009:4000009 /d
  [ "$(stat -c '%u:%g' "/proc/${PID}/root/d")" = "$((UID_BASE+29)):$((GID_BASE+29))" ]

  inc delete idmap --force
}
