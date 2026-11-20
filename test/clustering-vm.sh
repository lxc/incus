#!/bin/sh
# Runs an Incus cluster across nested virtual machines, with both local and shared (lvmcluster)
# storage, then exercises live migration and cluster healing on it.
#
# The host Incus must be able to run virtual machines with nested virtualization.
# INCUS_BIN_DIR holds the incusd, incus and incus-agent binaries to test.
# The optional arguments pick the sections to run, always in the order moves, snapshots, copies, heal (default: all).
set -eu

INCUS_BIN_DIR="${INCUS_BIN_DIR:-$(go env GOPATH)/bin}"
MEMBERS="${CLUSTER_VM_MEMBERS:-3}"
PREFIX="${CLUSTER_VM_PREFIX:-cvm}"
IMAGE="${CLUSTER_VM_IMAGE:-images:debian/13}"
ARCH="$(uname -m)"
REMOTE="${PREFIX}-cluster"
SHARED_DISK="/dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_shared"

if [ "$#" -eq 0 ]; then
    set -- snapshots copies moves heal
fi

for section in "$@"; do
    case "${section}" in
        snapshots | copies | moves | heal) ;;
        *)
            echo "Usage: ${0} [snapshots] [copies] [moves] [heal]" >&2
            exit 1
            ;;
    esac
done

SECTIONS="$*"

TMP="$(mktemp -d)"

POOL="$(incus profile device get default root pool)"

log() {
    echo "==> $*"
}

# section_enabled checks that the given section was requested.
section_enabled() {
    case " ${SECTIONS} " in
        *" ${1} "*) return 0 ;;
    esac

    return 1
}

member() {
    echo "${PREFIX}-${1}"
}

member_ip() {
    incus exec "${1}" -- sh -c "ip -4 -o addr show scope global | awk '{print \$4}' | cut -d/ -f1 | head -n1"
}

# Wait for the given disks to show up in the guest, dumping the guest's view on timeout.
wait_disks() {
    vm="${1}"
    shift
    # shellcheck disable=SC2016
    incus exec "${vm}" -- sh -c 'for _ in $(seq 120); do missing=0; for disk in "$@"; do [ -e "/dev/disk/by-id/${disk}" ] || missing=1; done; [ "${missing}" = 0 ] && exit 0; sleep 1; done; echo "Timed out waiting for $*"; ls -l /dev/disk/by-id/; lsblk; dmesg | tail -n 20; exit 1' sh "$@"
}

# check_vm checks that a VM runs on the given member and kept its memory state.
check_vm() {
    incus info "${REMOTE}:${1}" | grep -q "Location: ${2}"
    incus info "${REMOTE}:${1}" | grep -q "Status: RUNNING"

    # The agent reconnects shortly after a live migration.
    incus wait "${REMOTE}:${1}" agent --timeout=120 --interval=2
    incus exec "${REMOTE}:${1}" -- cat /dev/shm/marker | grep -Fx live
}

# check_volumes checks that the custom volumes attached to v2 are still readable.
check_volumes() {
    incus exec "${REMOTE}:v2" -- sh -c "mount /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_vol1 /mnt && cat /mnt/marker && umount /mnt" | grep -Fx data
    incus exec "${REMOTE}:v2" -- sh -c "mount -o ro /dev/disk/by-id/scsi-0QEMU_QEMU_CD-ROM_incus_vol2 /mnt && cat /mnt/foo && umount /mnt" | grep -Fx foo
}

# check_following_volume checks that the volume following v1 is readable and only exists on the given member.
check_following_volume() {
    incus exec "${REMOTE}:v1" -- sh -c "mount /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_vol3 /mnt && cat /mnt/marker && umount /mnt" | grep -Fx data
    [ "$(incus storage volume list "${REMOTE}:local" --format csv --columns nL | grep -c '^vol3,')" = "1" ]
    incus storage volume list "${REMOTE}:local" --format csv --columns nL | grep -Fx "vol3,${1}"
}

# lv_locked checks that the given member can't activate a logical volume of the shared pool, even shared.
lv_locked() {
    if incus exec "${1}" -- lvchange --activate sy --ignoreactivationskip "shared/${2}"; then
        incus exec "${1}" -- lvchange --activate n --ignoreactivationskip "shared/${2}"
        return 1
    fi

    return 0
}

# wait_locked waits for a logical volume to be locked by another member than the given one.
wait_locked() {
    # The lock is only promoted once the previous member released the volume.
    for _ in $(seq 30); do
        if lv_locked "${1}" "${2}"; then
            return 0
        fi

        sleep 2
    done

    lv_locked "${1}" "${2}"
}

# check_locks checks that the volumes of v2 are locked exclusively by the member running it.
check_locks() {
    for lv in virtual-machines_v2.block virtual-machines_v2 custom_default_vol4.block; do
        wait_locked "${1}" "${lv}"
    done

    ! lv_locked "${1}" custom_default_vol1.block || false
}

# check_marker checks the on-disk marker of a freshly booted VM.
check_marker() {
    incus wait "${REMOTE}:${1}" agent --timeout=600 --interval=2
    actual="$(incus exec "${REMOTE}:${1}" -- cat /root/marker)"
    echo "${actual}"
    if [ "${actual}" != "${2}" ]; then
        echo "Unexpected marker in ${1}: \"${actual}\", expected \"${2}\""
        return 1
    fi
}

# snapshots prints the snapshot names of a VM on one line.
snapshots() {
    incus snapshot list "${REMOTE}:${1}" --format csv -c n | xargs
}

# check_snapshots checks the snapshot names of a VM, printing them on mismatch.
check_snapshots() {
    actual="$(snapshots "${1}")"
    if [ "${actual}" != "${2}" ]; then
        echo "Unexpected snapshots of ${1}: \"${actual}\", expected \"${2}\""
        return 1
    fi
}

# volume_snapshots prints the snapshot names of a custom volume on one line.
volume_snapshots() {
    incus storage volume snapshot list "${REMOTE}:${1}" "${2}" --format csv -c n | xargs
}

# write_volume_marker writes a marker on a custom block volume attached to v2, formatting it first if asked.
write_volume_marker() {
    wait_disks "${REMOTE}:v2" "scsi-0QEMU_QEMU_HARDDISK_incus_${1}"
    if [ -n "${3:-}" ]; then
        incus exec "${REMOTE}:v2" -- mkfs.ext4 -q "/dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_${1}"
    fi

    incus exec "${REMOTE}:v2" -- sh -c "mount /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_${1} /mnt && echo ${2} > /mnt/marker && umount /mnt"
}

# check_volume_marker checks the marker on a custom block volume attached to v2.
check_volume_marker() {
    wait_disks "${REMOTE}:v2" "scsi-0QEMU_QEMU_HARDDISK_incus_${1}"
    incus exec "${REMOTE}:v2" -- sh -c "mount /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_${1} /mnt && cat /mnt/marker && umount /mnt" | grep -Fx "${2}"
}

dump_logs() {
    for i in $(seq "${MEMBERS}"); do
        echo "==> Logs for $(member "${i}")"
        incus exec "$(member "${i}")" -- sh -c "journalctl -u incus -u sanlock -u lvmlockd --no-pager -n 100; tail -n 200 /var/log/incus/incusd.log; dmesg | tail -n 30; df -h /; sanlock client status; lvmlockctl -i" || true
    done
}

cleanup() {
    rc="$?"
    set +e

    if [ "${rc}" != "0" ]; then
        echo "==> FAIL: exit code ${rc}"
        dump_logs
    fi

    if [ -n "${CLUSTER_VM_KEEP:-}" ]; then
        echo "==> Keeping the virtual machines around"
        return
    fi

    incus remote remove "${REMOTE}" > /dev/null 2>&1

    for i in $(seq "${MEMBERS}"); do
        incus delete -f "$(member "${i}")" > /dev/null 2>&1
    done

    incus storage volume delete "${POOL}" "${PREFIX}-shared" > /dev/null 2>&1
    rm -rf "${TMP}"
}

trap cleanup EXIT HUP INT TERM

for bin in incusd incus incus-agent; do
    if [ ! -x "${INCUS_BIN_DIR}/${bin}" ]; then
        echo "Missing ${INCUS_BIN_DIR}/${bin}"
        exit 1
    fi
done

# Generate the client certificate early so a slight clock skew with the members cannot make it invalid yet.
incus remote generate-certificate --quiet 2> /dev/null || true

log "Runner has $(nproc) CPUs, $(free -g | awk '/^Mem:/ {print $2}') GiB of RAM and $(df -h --output=avail / | tail -n 1 | tr -d " ") free on /"

log "Create the shared disk"
incus storage volume create "${POOL}" "${PREFIX}-shared" --type=block size=40GiB security.shared=true

log "Launch ${MEMBERS} virtual machines"
for i in $(seq "${MEMBERS}"); do
    incus init "${IMAGE}" "$(member "${i}")" --vm -c security.nesting=true -c limits.cpu=2 -c limits.memory=4GiB -d root,size=30GiB
    incus start "$(member "${i}")"
done

for i in $(seq "${MEMBERS}"); do
    incus wait "$(member "${i}")" agent --timeout=300 --interval=2
done

# The image grows its root partition assuming the root disk is the first one, so attach the shared disk only once booted.
for i in $(seq "${MEMBERS}"); do
    incus config device add "$(member "${i}")" shared disk pool="${POOL}" source="${PREFIX}-shared"
    wait_disks "$(member "${i}")" scsi-0QEMU_QEMU_HARDDISK_incus_shared
done

# Member setup script, run inside each virtual machine.
cat > "${TMP}/setup.sh" << 'EOF'
#!/bin/sh
set -eu
export DEBIAN_FRONTEND=noninteractive

if [ ! -e /dev/kvm ]; then
    echo "Nested virtualization isn't available"
    exit 1
fi

# Wait for the network to come up.
for _ in $(seq 60); do
    getent hosts pkgs.zabbly.com > /dev/null 2>&1 && break
    sleep 1
done

apt-get update -q
apt-get install -q -y --no-install-recommends btrfs-progs curl lvm2 lvm2-lockd sanlock
curl -fsSL https://pkgs.zabbly.com/get/incus-stable | sh
systemctl stop incus.service incus.socket

# Enable clustered LVM locking with a unique host id.
sed -i "s/^\([[:space:]]*\)#\{0,1\}[[:space:]]*use_lvmlockd[[:space:]]*=.*/\1use_lvmlockd = 1/" /etc/lvm/lvm.conf
sed -i "s/^\([[:space:]]*\)#\{0,1\}[[:space:]]*host_id[[:space:]]*=.*/\1host_id = ${1}/" /etc/lvm/lvmlocal.conf
[ "$(lvmconfig --typeconfig current global/use_lvmlockd)" = "use_lvmlockd=1" ]
[ "$(lvmconfig --typeconfig current local/host_id)" = "host_id=${1}" ]
# Give sanlock more time for lease I/O as nested virtualization can be slow.
mkdir -p /etc/systemd/system/lvmlockd.service.d
printf "[Service]\nExecStart=\nExecStart=/usr/sbin/lvmlockd --foreground --sanlock-timeout 20\n" > /etc/systemd/system/lvmlockd.service.d/sanlock-timeout.conf
systemctl daemon-reload
systemctl enable --now sanlock lvmlockd
# The package install already started the daemon with the stock command line.
systemctl restart lvmlockd
pgrep -a lvmlockd | grep -q -- "--sanlock-timeout 20"
EOF

log "Prepare the cluster members"
for i in $(seq "${MEMBERS}"); do
    name="$(member "${i}")"

    incus file push --mode 0755 "${TMP}/setup.sh" "${name}/root/setup.sh"
    incus exec "${name}" -- /root/setup.sh "${i}"

    # Swap in the binaries under test.
    incus file push --mode 0755 "${INCUS_BIN_DIR}/incusd" "${name}/opt/incus/bin/incusd"
    incus file push --mode 0755 "${INCUS_BIN_DIR}/incus" "${name}/opt/incus/bin/incus"
    incus file push --mode 0755 "${INCUS_BIN_DIR}/incus-agent" "${name}/opt/incus/agent/incus-agent.linux.${ARCH}"
    ! incus exec "${name}" -- sh -c "LD_LIBRARY_PATH=/opt/incus/lib ldd /opt/incus/bin/incusd" | grep "not found" || false
    incus exec "${name}" -- systemctl start incus.service
    incus exec "${name}" -- incus version
done

log "Bootstrap the cluster"
IP1="$(member_ip "$(member 1)")"
cat > "${TMP}/bootstrap.yaml" << EOF
config:
  core.https_address: ${IP1}:8443
networks:
- name: incusbr0
  type: bridge
  config:
    ipv4.address: auto
    ipv6.address: auto
storage_pools:
- name: local
  driver: dir
profiles:
- name: default
  devices:
    eth0:
      name: eth0
      network: incusbr0
      type: nic
    root:
      path: /
      pool: local
      type: disk
cluster:
  server_name: $(member 1)
  enabled: true
EOF
incus exec "$(member 1)" -- incus admin init --preseed < "${TMP}/bootstrap.yaml"
incus exec "$(member 1)" -- cat /var/lib/incus/cluster.crt | sed "s/^/    /" > "${TMP}/cluster.crt"

for i in $(seq 2 "${MEMBERS}"); do
    name="$(member "${i}")"
    log "Join ${name}"

    token="$(incus exec "$(member 1)" -- incus cluster add "${name}" --quiet)"
    cat > "${TMP}/join.yaml" << EOF
cluster:
  enabled: true
  server_name: ${name}
  server_address: $(member_ip "${name}"):8443
  cluster_address: ${IP1}:8443
  cluster_token: ${token}
  cluster_certificate: |
$(cat "${TMP}/cluster.crt")
  member_config:
  - entity: storage-pool
    name: local
    key: source
    value: ""
EOF
    incus exec "${name}" -- incus admin init --preseed < "${TMP}/join.yaml"
done

log "Add the cluster as a remote"
token="$(incus exec "$(member 1)" -- incus config trust add "${PREFIX}-host" --quiet)"
incus remote add "${REMOTE}" "${token}"
incus cluster list "${REMOTE}:"

log "Create the shared storage pool"
for i in $(seq "${MEMBERS}"); do
    incus storage create "${REMOTE}:shared" lvmcluster source="${SHARED_DISK}" --target "$(member "${i}")"
done
incus storage create "${REMOTE}:shared" lvmcluster
incus storage show "${REMOTE}:shared" | grep -q "status: Created"

if section_enabled moves; then
    log "Launch a VM on local storage with a custom block volume following it"
    incus init "${IMAGE}" "${REMOTE}:v1" --vm -s local --target "$(member 1)" -c migration.stateful=true
    incus storage volume create "${REMOTE}:local" vol3 --type=block size=64MiB migration.follow_instance=true --target "$(member 1)"
    incus storage volume attach "${REMOTE}:local" vol3 v1
    incus start "${REMOTE}:v1"
fi

log "Launch a VM on shared storage with custom block and ISO volumes"
incus init "${IMAGE}" "${REMOTE}:v2" --vm -s shared -d root,size=4GiB -d root,size.state=1GiB --target "$(member 1)" -c migration.stateful=true
incus storage volume create "${REMOTE}:shared" vol1 --type=block size=64MiB
incus storage volume attach "${REMOTE}:shared" vol1 v2

echo foo > "${TMP}/foo"
genisoimage -quiet -o "${TMP}/vol2.iso" "${TMP}/foo"
incus storage volume import "${REMOTE}:shared" "${TMP}/vol2.iso" vol2
incus storage volume attach "${REMOTE}:shared" vol2 v2

incus start "${REMOTE}:v2"
incus wait "${REMOTE}:v2" agent --timeout=600 --interval=2

# The by-id links are created asynchronously at boot.
wait_disks "${REMOTE}:v2" scsi-0QEMU_QEMU_HARDDISK_incus_vol1 scsi-0QEMU_QEMU_CD-ROM_incus_vol2
incus exec "${REMOTE}:v2" -- sh -c "mkfs.ext4 -q /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_vol1 && mount /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_vol1 /mnt && echo data > /mnt/marker && umount /mnt"

incus exec "${REMOTE}:v2" -- sh -c "echo live > /dev/shm/marker"
check_volumes

if section_enabled moves; then
    incus wait "${REMOTE}:v1" agent --timeout=600 --interval=2
    wait_disks "${REMOTE}:v1" scsi-0QEMU_QEMU_HARDDISK_incus_vol3
    incus exec "${REMOTE}:v1" -- sh -c "mkfs.ext4 -q /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_vol3 && mount /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_vol3 /mnt && echo data > /mnt/marker && umount /mnt"
    incus exec "${REMOTE}:v1" -- sh -c "echo live > /dev/shm/marker"
    check_following_volume "$(member 1)"

    log "Live migrate the shared storage VM across the members"
    for i in $(seq 2 "${MEMBERS}") 1; do
        incus move "${REMOTE}:v2" "${REMOTE}:v2" --target "$(member "${i}")"
        check_vm v2 "$(member "${i}")"
        check_volumes
    done

    log "Check the storage locks of the shared storage VM"
    incus storage volume create "${REMOTE}:shared" vol4 --type=block size=64MiB
    incus config device add "${REMOTE}:v2" vol4 disk pool=shared source=vol4 dependent=true
    check_locks "$(member 2)"

    incus move "${REMOTE}:v2" "${REMOTE}:v2" --target "$(member 2)"
    check_vm v2 "$(member 2)"
    check_locks "$(member 1)"

    incus snapshot create "${REMOTE}:v2" locks
    check_locks "$(member 1)"
    ! lv_locked "$(member 1)" virtual-machines_v2-locks.block || false
    ! lv_locked "$(member 1)" custom_default_vol4-locks.block || false
    incus snapshot delete "${REMOTE}:v2" locks
    check_locks "$(member 1)"

    incus stop -f "${REMOTE}:v2"
    incus snapshot create "${REMOTE}:v2" locks
    incus move "${REMOTE}:v2" "${REMOTE}:v2" --target "$(member 1)"
    incus start "${REMOTE}:v2"
    incus wait "${REMOTE}:v2" agent --timeout=120 --interval=2
    [ "$(incus list "${REMOTE}:" v2 -c L -f csv)" = "$(member 1)" ]
    check_locks "$(member 2)"
    ! lv_locked "$(member 2)" virtual-machines_v2-locks.block || false
    ! lv_locked "$(member 2)" custom_default_vol4-locks.block || false
    incus snapshot delete "${REMOTE}:v2" locks
    incus exec "${REMOTE}:v2" -- sh -c "echo live > /dev/shm/marker"

    incus config device remove "${REMOTE}:v2" vol4
    incus storage volume delete "${REMOTE}:shared" vol4
    check_volumes

    log "Live migrate the local storage VM"
    incus storage volume create "${REMOTE}:shared" vol5 --type=block size=64MiB
    incus config device add "${REMOTE}:v1" vol5 disk pool=shared source=vol5 dependent=true
    incus move "${REMOTE}:v1" "${REMOTE}:v1" --target "$(member 2)"
    check_vm v1 "$(member 2)"
    check_following_volume "$(member 2)"
    wait_locked "$(member 1)" custom_default_vol5.block

    log "Live move the dependent disk of the local storage VM to local storage"
    incus move "${REMOTE}:v1" "${REMOTE}:v1" --target "$(member 1)" -d vol5,pool=local
    check_vm v1 "$(member 1)"
    check_following_volume "$(member 1)"
    [ "$(incus config device get "${REMOTE}:v1" vol5 pool)" = "local" ]
    incus storage volume list "${REMOTE}:local" --format csv --columns nL | grep -Fx "vol5,$(member 1)"

    log "Live move the local dependent disk of the local storage VM to shared storage"
    incus move "${REMOTE}:v1" "${REMOTE}:v1" --target "$(member 2)" -d vol5,pool=shared
    check_vm v1 "$(member 2)"
    check_following_volume "$(member 2)"
    [ "$(incus config device get "${REMOTE}:v1" vol5 pool)" = "shared" ]
    wait_locked "$(member 1)" custom_default_vol5.block
    incus move "${REMOTE}:v1" "${REMOTE}:v1" --target "$(member 1)"
    check_vm v1 "$(member 1)"
    check_following_volume "$(member 1)"
    wait_locked "$(member 2)" custom_default_vol5.block
    incus config device remove "${REMOTE}:v1" vol5
    incus storage volume delete "${REMOTE}:shared" vol5

    log "Move the stopped local storage VM"
    incus stop -f "${REMOTE}:v1"
    incus move "${REMOTE}:v1" "${REMOTE}:v1" --target "$(member "${MEMBERS}")"
    incus info "${REMOTE}:v1" | grep -q "Location: $(member "${MEMBERS}")"
    incus start "${REMOTE}:v1"
    incus wait "${REMOTE}:v1" agent --timeout=600 --interval=2
    check_following_volume "$(member "${MEMBERS}")"

    log "Live move the shared storage VM to local storage"
    incus exec "${REMOTE}:v2" -- sh -c "echo snap0 > /root/marker && sync"
    incus snapshot create "${REMOTE}:v2" snap0
    incus snapshot rename "${REMOTE}:v2" snap0 moved0
    # Moving to a dir pool has to cope with this lvmcluster-only key.
    incus storage volume set "${REMOTE}:shared" virtual-machine/v2 lvmcluster.remove_snapshots=true
    incus exec "${REMOTE}:v2" -- sh -c "echo current > /root/marker && sync"
    incus storage volume create "${REMOTE}:shared" vol4 --type=block size=64MiB
    incus config device add "${REMOTE}:v2" vol4 disk pool=shared source=vol4 dependent=true
    incus move "${REMOTE}:v2" "${REMOTE}:v2" -s local --target "$(member 2)"
    check_vm v2 "$(member 2)"
    check_volumes
    [ "$(incus config device get "${REMOTE}:v2" root pool)" = "local" ]
    check_snapshots v2 "moved0"
    wait_locked "$(member 1)" custom_default_vol4.block

    log "Live move the VM back to shared storage"
    incus move "${REMOTE}:v2" "${REMOTE}:v2" -s shared --target "$(member 1)"
    check_vm v2 "$(member 1)"
    check_volumes
    [ "$(incus config device get "${REMOTE}:v2" root pool)" = "shared" ]
    check_locks "$(member 2)"
    # Snapshots later expects restoring an older snapshot to fail.
    incus storage volume unset "${REMOTE}:shared" virtual-machine/v2 lvmcluster.remove_snapshots

    log "Live move the dependent disk of the shared storage VM to local storage"
    incus move "${REMOTE}:v2" "${REMOTE}:v2" --target "$(member 2)" -d vol4,pool=local
    check_vm v2 "$(member 2)"
    check_volumes
    [ "$(incus config device get "${REMOTE}:v2" vol4 pool)" = "local" ]

    log "Live move the shared storage VM with a local dependent disk"
    incus move "${REMOTE}:v2" "${REMOTE}:v2" --target "$(member 1)"
    check_vm v2 "$(member 1)"
    incus storage volume list "${REMOTE}:local" --format csv --columns nL | grep -Fx "vol4,$(member 1)"

    log "Live move the local dependent disk of the shared storage VM to shared storage"
    incus move "${REMOTE}:v2" "${REMOTE}:v2" --target "$(member 2)" -d vol4,pool=shared
    check_vm v2 "$(member 2)"
    check_volumes
    [ "$(incus config device get "${REMOTE}:v2" vol4 pool)" = "shared" ]
    wait_locked "$(member 1)" custom_default_vol4.block
    incus move "${REMOTE}:v2" "${REMOTE}:v2" --target "$(member 1)" -d vol4,pool=local
    check_vm v2 "$(member 1)"
    incus storage volume list "${REMOTE}:local" --format csv --columns nL | grep -Fx "vol4,$(member 1)"

    log "Live move the shared storage VM and its dependent disk to local storage"
    incus storage volume create "${REMOTE}:shared" vol6 --type=block size=64MiB
    incus config device add "${REMOTE}:v2" vol6 disk pool=shared source=vol6 dependent=true
    incus move "${REMOTE}:v2" "${REMOTE}:v2" -s local --target "$(member 2)" -d vol6,pool=local
    check_vm v2 "$(member 2)"
    check_volumes
    [ "$(incus config device get "${REMOTE}:v2" root pool)" = "local" ]
    [ "$(incus config device get "${REMOTE}:v2" vol6 pool)" = "local" ]
    incus storage volume list "${REMOTE}:local" --format csv --columns nL | grep -Fx "vol4,$(member 2)"

    log "Live move the VM and its dependent disk back to shared storage"
    incus move "${REMOTE}:v2" "${REMOTE}:v2" -s shared --target "$(member 1)" -d vol6,pool=shared
    check_vm v2 "$(member 1)"
    check_volumes
    [ "$(incus config device get "${REMOTE}:v2" root pool)" = "shared" ]
    [ "$(incus config device get "${REMOTE}:v2" vol6 pool)" = "shared" ]
    wait_locked "$(member 2)" custom_default_vol6.block
    incus config device remove "${REMOTE}:v2" vol4
    incus config device remove "${REMOTE}:v2" vol6
    incus storage volume delete "${REMOTE}:local" vol4 --target "$(member 1)"
    incus storage volume delete "${REMOTE}:shared" vol6
    incus restart -f "${REMOTE}:v2"
    check_marker v2 current
    incus snapshot restore "${REMOTE}:v2" moved0
    check_marker v2 snap0
    incus snapshot delete "${REMOTE}:v2" moved0
    incus exec "${REMOTE}:v2" -- sh -c "echo live > /dev/shm/marker"

    incus delete -f "${REMOTE}:v1"
    incus storage volume delete "${REMOTE}:local" vol3
fi

if section_enabled snapshots; then
    log "Snapshot the running shared storage VM across the members"
    incus exec "${REMOTE}:v2" -- sh -c "echo snap0 > /root/marker && sync"
    incus snapshot create "${REMOTE}:v2" snap0

    for i in 1 2 3; do
        incus exec "${REMOTE}:v2" -- sh -c "echo snap${i} > /root/marker && sync"
        incus snapshot create "${REMOTE}:v2" "snap${i}"

        next="$(member "$((i % MEMBERS + 1))")"
        incus move "${REMOTE}:v2" "${REMOTE}:v2" --target "${next}"
        check_vm v2 "${next}"
        check_volumes
    done

    incus exec "${REMOTE}:v2" -- sh -c "echo current > /root/marker && sync"

    log "Delete a snapshot in the middle of the chain of the running VM"
    incus snapshot delete "${REMOTE}:v2" snap2
    check_snapshots v2 "snap0 snap1 snap3"
    check_vm v2 "${next}"

    log "Delete the oldest snapshot of the running VM"
    incus snapshot delete "${REMOTE}:v2" snap0
    check_snapshots v2 "snap1 snap3"
    check_vm v2 "${next}"

    log "Delete the newest snapshot of the running VM"
    incus exec "${REMOTE}:v2" -- sh -c "echo snap4 > /root/marker && sync"
    incus snapshot create "${REMOTE}:v2" snap4
    incus exec "${REMOTE}:v2" -- sh -c "echo current > /root/marker && sync"
    incus snapshot delete "${REMOTE}:v2" snap4
    check_snapshots v2 "snap1 snap3"

    next="$(member "$((4 % MEMBERS + 1))")"
    incus move "${REMOTE}:v2" "${REMOTE}:v2" --target "${next}"
    check_vm v2 "${next}"
    check_volumes

    log "Copy a snapshot to a new VM on another member"
    incus copy "${REMOTE}:v2/snap1" "${REMOTE}:v3" --target "$(member 2)"
    incus config device remove "${REMOTE}:v3" vol1 vol2
    incus start "${REMOTE}:v3"
    check_marker v3 snap1
    incus info "${REMOTE}:v3" | grep -q "Location: $(member 2)"
    incus delete -f "${REMOTE}:v3"

    log "Copy a snapshot to local storage"
    incus copy "${REMOTE}:v2/snap3" "${REMOTE}:v3" -s local --target "$(member 2)"
    incus config device remove "${REMOTE}:v3" vol1 vol2
    incus start "${REMOTE}:v3"
    check_marker v3 snap3
    incus delete -f "${REMOTE}:v3"

    log "Copy the running VM with its snapshots"
    incus copy "${REMOTE}:v2" "${REMOTE}:v4" --target "$(member "${MEMBERS}")" --stateless
    incus config device remove "${REMOTE}:v4" vol1 vol2
    check_snapshots v4 "snap1 snap3"
    incus start "${REMOTE}:v4"
    check_marker v4 current

    log "Restore the snapshots of the shared storage VM"
    ! incus snapshot restore "${REMOTE}:v2" snap1 || false
    incus snapshot restore "${REMOTE}:v2" snap3
    check_marker v2 snap3
    incus storage volume set "${REMOTE}:shared" virtual-machine/v2 lvmcluster.remove_snapshots=true
    incus snapshot restore "${REMOTE}:v2" snap1
    check_marker v2 snap1
    check_snapshots v2 "snap1"
    check_volumes

    log "Check the copied VM doesn't depend on the source snapshots"
    incus restart -f "${REMOTE}:v4"
    check_marker v4 current
    incus snapshot restore "${REMOTE}:v4" snap3 --diskonly
    check_marker v4 snap3
    incus delete -f "${REMOTE}:v4"

    log "Take a stateful snapshot of the running VM"
    incus exec "${REMOTE}:v2" -- sh -c "echo stateful > /root/marker && sync && echo stateful > /dev/shm/marker"
    incus snapshot create "${REMOTE}:v2" snap5 --stateful
    incus exec "${REMOTE}:v2" -- sh -c "echo current > /root/marker && sync && echo live > /dev/shm/marker"

    next="$(member "$((5 % MEMBERS + 1))")"
    incus move "${REMOTE}:v2" "${REMOTE}:v2" --target "${next}"
    check_vm v2 "${next}"

    log "Restore the stateful snapshot"
    incus snapshot restore "${REMOTE}:v2" snap5 --stateful
    incus wait "${REMOTE}:v2" agent --timeout=120 --interval=2
    incus exec "${REMOTE}:v2" -- cat /dev/shm/marker | grep -Fx stateful
    incus restart -f "${REMOTE}:v2"
    check_marker v2 stateful

    log "Delete the stateful snapshot of the running VM"
    incus snapshot delete "${REMOTE}:v2" snap5
    check_snapshots v2 "snap1"
    check_volumes

    incus exec "${REMOTE}:v2" -- sh -c "echo live > /dev/shm/marker"
    if [ "${next}" != "$(member 1)" ]; then
        incus move "${REMOTE}:v2" "${REMOTE}:v2" --target "$(member 1)"
        check_vm v2 "$(member 1)"
    fi

    log "Rename the snapshots of the running shared storage VM"
    incus exec "${REMOTE}:v2" -- sh -c "echo snap6 > /root/marker && sync"
    incus snapshot create "${REMOTE}:v2" snap6
    incus exec "${REMOTE}:v2" -- sh -c "echo current > /root/marker && sync"
    incus snapshot rename "${REMOTE}:v2" snap1 renamed1
    incus snapshot rename "${REMOTE}:v2" snap6 renamed6
    check_snapshots v2 "renamed1 renamed6"

    incus move "${REMOTE}:v2" "${REMOTE}:v2" --target "$(member 2)"
    check_vm v2 "$(member 2)"
    check_volumes

    incus snapshot delete "${REMOTE}:v2" renamed1
    check_snapshots v2 "renamed6"
    check_vm v2 "$(member 2)"
    incus snapshot restore "${REMOTE}:v2" renamed6
    check_marker v2 snap6

    log "Rename the stopped shared storage VM"
    incus stop -f "${REMOTE}:v2"
    incus move "${REMOTE}:v2" "${REMOTE}:v5"
    check_snapshots v5 "renamed6"
    incus move "${REMOTE}:v5" "${REMOTE}:v5" --target "$(member 1)"
    incus move "${REMOTE}:v5" "${REMOTE}:v2"
    check_snapshots v2 "renamed6"
    incus start "${REMOTE}:v2"
    check_marker v2 snap6
    incus exec "${REMOTE}:v2" -- sh -c "echo current > /root/marker && sync && echo live > /dev/shm/marker"
    check_volumes
    incus snapshot delete "${REMOTE}:v2" renamed6
    check_snapshots v2 ""
fi

if section_enabled copies; then
    log "Refresh a copy of the running VM"
    # Give the refresh a snapshot both sides share, so it's incremental.
    incus snapshot create "${REMOTE}:v2" base
    incus copy "${REMOTE}:v2" "${REMOTE}:v3" --target "$(member 2)" --stateless
    incus config device remove "${REMOTE}:v3" vol1 vol2

    incus exec "${REMOTE}:v2" -- sh -c "echo snap7 > /root/marker && sync"
    incus snapshot create "${REMOTE}:v2" snap7
    incus exec "${REMOTE}:v2" -- sh -c "echo refreshed > /root/marker && sync"
    # A refresh brings the source devices back and vol1 can't be attached twice.
    incus config device remove "${REMOTE}:v2" vol1
    incus copy "${REMOTE}:v2" "${REMOTE}:v3" --refresh
    incus config device remove "${REMOTE}:v3" vol2
    check_snapshots v3 "base snap7"
    incus start "${REMOTE}:v3"
    check_marker v3 refreshed
    incus snapshot restore "${REMOTE}:v3" snap7
    check_marker v3 snap7
    incus delete -f "${REMOTE}:v3"
    incus storage volume attach "${REMOTE}:shared" vol1 v2
    wait_disks "${REMOTE}:v2" scsi-0QEMU_QEMU_HARDDISK_incus_vol1
    check_vm v2 "$(member 1)"
    check_volumes

    log "Copy the running VM to local storage"
    incus snapshot delete "${REMOTE}:v2" base
    check_snapshots v2 "snap7"
    incus copy "${REMOTE}:v2" "${REMOTE}:v6" -s local --target "$(member 2)" --stateless
    incus config device remove "${REMOTE}:v6" vol1 vol2
    check_snapshots v6 "snap7"
    incus start "${REMOTE}:v6"
    check_marker v6 refreshed

    log "Copy the running local storage VM to shared storage"
    incus copy "${REMOTE}:v6" "${REMOTE}:v7" -s shared --target "$(member "${MEMBERS}")" --stateless
    check_snapshots v7 "snap7"

    log "Refresh a copy on shared storage from local storage"
    incus exec "${REMOTE}:v6" -- sh -c "echo snap8 > /root/marker && sync"
    incus snapshot create "${REMOTE}:v6" snap8
    incus exec "${REMOTE}:v6" -- sh -c "echo refreshed > /root/marker && sync"
    incus copy "${REMOTE}:v6" "${REMOTE}:v7" --refresh
    check_snapshots v7 "snap7 snap8"
    incus start "${REMOTE}:v7"
    check_marker v7 refreshed
    incus snapshot restore "${REMOTE}:v7" snap8 --diskonly
    check_marker v7 snap8
    incus delete -f "${REMOTE}:v6" "${REMOTE}:v7"

    log "Export the running shared storage VM"
    incus export "${REMOTE}:v2" "${TMP}/v2.tar.gz"

    log "Restore the shared storage VM from its backup"
    incus delete -f "${REMOTE}:v2"
    incus import "${REMOTE}:" "${TMP}/v2.tar.gz" -s shared
    rm "${TMP}/v2.tar.gz"
    check_snapshots v2 "snap7"
    incus start "${REMOTE}:v2"
    check_marker v2 refreshed
    incus snapshot restore "${REMOTE}:v2" snap7
    check_marker v2 snap7
    wait_disks "${REMOTE}:v2" scsi-0QEMU_QEMU_HARDDISK_incus_vol1 scsi-0QEMU_QEMU_CD-ROM_incus_vol2
    incus exec "${REMOTE}:v2" -- sh -c "echo live > /dev/shm/marker"
    if [ "$(incus list "${REMOTE}:" v2 -c L -f csv)" != "$(member 1)" ]; then
        incus move "${REMOTE}:v2" "${REMOTE}:v2" --target "$(member 1)"
    fi

    check_vm v2 "$(member 1)"
    check_volumes

    log "Snapshot a custom block volume attached to the running shared storage VM"
    incus storage volume create "${REMOTE}:shared" vol7 --type=block size=64MiB
    incus storage volume attach "${REMOTE}:shared" vol7 v2
    write_volume_marker vol7 s0 format
    incus storage volume snapshot create "${REMOTE}:shared" vol7 s0
    write_volume_marker vol7 current
    incus move "${REMOTE}:v2" "${REMOTE}:v2" --target "$(member 2)"
    check_vm v2 "$(member 2)"
    check_volume_marker vol7 current

    log "Restore the custom volume snapshot"
    incus storage volume detach "${REMOTE}:shared" vol7 v2
    incus storage volume snapshot restore "${REMOTE}:shared" vol7 s0
    incus storage volume attach "${REMOTE}:shared" vol7 v2
    check_volume_marker vol7 s0
    write_volume_marker vol7 copied
    incus storage volume detach "${REMOTE}:shared" vol7 v2

    log "Copy the custom volume to local and shared storage"
    incus storage volume copy "${REMOTE}:shared/vol7" "${REMOTE}:local/vol8" --destination-target "$(member "${MEMBERS}")"
    incus storage volume copy "${REMOTE}:shared/vol7" "${REMOTE}:shared/vol9"
    incus storage volume delete "${REMOTE}:shared" vol7

    log "Move the local copy back to shared storage"
    incus storage volume move "${REMOTE}:local/vol8" "${REMOTE}:shared/vol8" --target "$(member "${MEMBERS}")"
    [ "$(volume_snapshots shared vol8)" = "s0" ]
    [ "$(volume_snapshots shared vol9)" = "s0" ]
    incus storage volume attach "${REMOTE}:shared" vol8 v2
    incus storage volume attach "${REMOTE}:shared" vol9 v2
    check_volume_marker vol8 copied
    check_volume_marker vol9 copied

    log "Live migrate the shared storage VM with the copied volumes"
    incus move "${REMOTE}:v2" "${REMOTE}:v2" --target "$(member 1)"
    check_vm v2 "$(member 1)"
    check_volume_marker vol8 copied
    check_volume_marker vol9 copied

    log "Restore the snapshot of a copied custom volume"
    incus storage volume detach "${REMOTE}:shared" vol9 v2
    incus storage volume snapshot restore "${REMOTE}:shared" vol9 s0
    incus storage volume attach "${REMOTE}:shared" vol9 v2
    check_volume_marker vol9 s0
    incus storage volume detach "${REMOTE}:shared" vol8 v2
    incus storage volume detach "${REMOTE}:shared" vol9 v2
    incus storage volume delete "${REMOTE}:shared" vol8
    incus storage volume delete "${REMOTE}:shared" vol9

    log "Restore a snapshot of the running VM with a dependent disk"
    # Sorting after vol2 keeps the SCSI ids gapless once it is unplugged, which live migration still needs.
    incus storage volume create "${REMOTE}:shared" vol9 --type=block size=64MiB
    incus config device add "${REMOTE}:v2" vol9 disk pool=shared source=vol9 dependent=true
    write_volume_marker vol9 dep format
    incus snapshot create "${REMOTE}:v2" dep
    write_volume_marker vol9 current
    incus snapshot restore "${REMOTE}:v2" dep
    incus wait "${REMOTE}:v2" agent --timeout=600 --interval=2
    check_volume_marker vol9 dep
    incus snapshot delete "${REMOTE}:v2" dep
    incus config device remove "${REMOTE}:v2" vol9
    incus storage volume delete "${REMOTE}:shared" vol9
    incus exec "${REMOTE}:v2" -- sh -c "echo live > /dev/shm/marker"

    log "Live migrate the shared storage VM after removing the dependent disk"
    incus move "${REMOTE}:v2" "${REMOTE}:v2" --target "$(member 2)"
    check_vm v2 "$(member 2)"
    check_volumes
fi

if section_enabled heal; then
    log "Heal the cluster after losing a member"
    incus config set "${REMOTE}:" cluster.offline_threshold=11 cluster.healing_threshold=15
    if [ "$(incus list "${REMOTE}:" v2 -c L -f csv)" != "$(member 2)" ]; then
        incus move "${REMOTE}:v2" "${REMOTE}:v2" --target "$(member 2)"
    fi

    check_vm v2 "$(member 2)"
    incus snapshot create "${REMOTE}:v2" healed

    log "Don't heal a member which lost its network but still runs its VM"
    dev="$(incus exec "$(member 2)" -- sh -c "ip -o route show default | awk '{print \$5}'")"
    incus exec "$(member 2)" -- ip link set "${dev}" down
    sleep 90
    [ "$(incus list "${REMOTE}:" v2 -c L -f csv)" = "$(member 2)" ]
    incus exec "$(member 2)" -- pgrep -f -- "-name v2" > /dev/null
    found=""
    for i in $(seq "${MEMBERS}"); do
        if incus exec "$(member "${i}")" -- grep -q "storage is still in use, retrying on the next run" /var/log/incus/incusd.log; then
            found=1
        fi
    done
    [ -n "${found}" ]

    incus exec "$(member 2)" -- ip link set "${dev}" up
    for _ in $(seq 60); do
        if incus cluster list "${REMOTE}:" --format csv | grep "^$(member 2)," | grep -q "ONLINE"; then
            break
        fi

        sleep 5
    done
    incus cluster list "${REMOTE}:" --format csv | grep "^$(member 2)," | grep -q "ONLINE"
    check_vm v2 "$(member 2)"

    incus stop -f "$(member 2)"
    for _ in $(seq 240); do
        if incus info "${REMOTE}:v2" | grep -q "Status: RUNNING" && ! incus info "${REMOTE}:v2" | grep -q "Location: $(member 2)"; then
            break
        fi

        sleep 2
    done
    incus info "${REMOTE}:v2" | grep -q "Status: RUNNING"
    ! incus info "${REMOTE}:v2" | grep -q "Location: $(member 2)" || false
    incus cluster list "${REMOTE}:" --format csv | grep "^$(member 2)," | grep -q "EVACUATED"
    incus wait "${REMOTE}:v2" agent --timeout=600 --interval=2
    check_volumes

    log "Restore the lost member"
    incus start "$(member 2)"
    incus wait "$(member 2)" agent --timeout=300 --interval=2
    incus exec "${REMOTE}:v2" -- sh -c "echo live > /dev/shm/marker"

    # The member can only be restored once the leader gets a heartbeat from it.
    # Restoring it live migrates the healed VM back.
    for _ in $(seq 60); do
        if incus cluster restore "${REMOTE}:$(member 2)" --force; then
            break
        fi

        sleep 5
    done
    incus cluster list "${REMOTE}:" --format csv | grep "^$(member 2)," | grep -q "ONLINE"
    check_vm v2 "$(member 2)"
    check_volumes
    incus snapshot delete "${REMOTE}:v2" healed
fi

log "Cleanup"
incus delete -f "${REMOTE}:v2"
incus storage volume delete "${REMOTE}:shared" vol1
incus storage volume delete "${REMOTE}:shared" vol2
incus storage delete "${REMOTE}:shared"

log "PASS"
