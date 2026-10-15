#!/bin/sh
# Runs an Incus cluster across nested virtual machines, with both local and shared (lvmcluster)
# storage, then exercises live migration and cluster healing on it.
#
# The host Incus must be able to run virtual machines with nested virtualization.
# INCUS_BIN_DIR holds the incusd, incus and incus-agent binaries to test.
set -eu

INCUS_BIN_DIR="${INCUS_BIN_DIR:-$(go env GOPATH)/bin}"
MEMBERS="${CLUSTER_VM_MEMBERS:-3}"
PREFIX="${CLUSTER_VM_PREFIX:-cvm}"
IMAGE="${CLUSTER_VM_IMAGE:-images:debian/13}"
ARCH="$(uname -m)"
REMOTE="${PREFIX}-cluster"
SHARED_DISK="/dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_shared"
TMP="$(mktemp -d)"

POOL="$(incus profile device get default root pool)"

log() {
    echo "==> $*"
}

member() {
    echo "${PREFIX}-${1}"
}

member_ip() {
    incus exec "${1}" -- sh -c "ip -4 -o addr show scope global | awk '{print \$4}' | cut -d/ -f1 | head -n1"
}

dump_logs() {
    for i in $(seq "${MEMBERS}"); do
        echo "==> Logs for $(member "${i}")"
        incus exec "$(member "${i}")" -- sh -c "journalctl -u incus -u sanlock -u lvmlockd --no-pager -n 100; tail -n 200 /var/log/incus/incusd.log; dmesg | tail -n 30; sanlock client status; lvmlockctl -i" || true
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
incus storage volume create "${POOL}" "${PREFIX}-shared" --type=block size=20GiB security.shared=true

log "Launch ${MEMBERS} virtual machines"
for i in $(seq "${MEMBERS}"); do
    incus init "${IMAGE}" "$(member "${i}")" --vm -c security.nesting=true -c limits.cpu=2 -c limits.memory=4GiB -d root,size=20GiB
    incus config device add "$(member "${i}")" shared disk pool="${POOL}" source="${PREFIX}-shared"
    incus start "$(member "${i}")"
done

for i in $(seq "${MEMBERS}"); do
    incus wait "$(member "${i}")" agent --timeout=300 --interval=2
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

log "Launch a VM on local storage and one on shared storage"
incus init "${IMAGE}" "${REMOTE}:v1" --vm -s local --target "$(member 1)" -c migration.stateful=true
incus init "${IMAGE}" "${REMOTE}:v2" --vm -s shared --target "$(member 1)" -c migration.stateful=true

log "Attach custom block and ISO volumes to the shared storage VM"
incus storage volume create "${REMOTE}:shared" vol1 --type=block size=64MiB
incus storage volume attach "${REMOTE}:shared" vol1 v2

echo foo > "${TMP}/foo"
genisoimage -quiet -o "${TMP}/vol2.iso" "${TMP}/foo"
incus storage volume import "${REMOTE}:shared" "${TMP}/vol2.iso" vol2
incus storage volume attach "${REMOTE}:shared" vol2 v2

incus start "${REMOTE}:v1" "${REMOTE}:v2"
incus wait "${REMOTE}:v1" agent --timeout=600 --interval=2
incus wait "${REMOTE}:v2" agent --timeout=600 --interval=2

# The by-id links are created asynchronously at boot.
# shellcheck disable=SC2016
incus exec "${REMOTE}:v2" -- sh -c 'for _ in $(seq 60); do [ -e /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_vol1 ] && [ -e /dev/disk/by-id/scsi-0QEMU_QEMU_CD-ROM_incus_vol2 ] && exit 0; sleep 1; done; ls -l /dev/disk/by-id/; exit 1'
incus exec "${REMOTE}:v2" -- sh -c "mkfs.ext4 -q /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_vol1 && mount /dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK_incus_vol1 /mnt && echo data > /mnt/marker && umount /mnt"

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

incus exec "${REMOTE}:v1" -- sh -c "echo live > /dev/shm/marker"
incus exec "${REMOTE}:v2" -- sh -c "echo live > /dev/shm/marker"
check_volumes

log "Live migrate the shared storage VM across the members"
for i in $(seq 2 "${MEMBERS}") 1; do
    incus move "${REMOTE}:v2" "${REMOTE}:v2" --target "$(member "${i}")"
    check_vm v2 "$(member "${i}")"
    check_volumes
done

log "Live migrate the local storage VM"
incus move "${REMOTE}:v1" "${REMOTE}:v1" --target "$(member 2)"
check_vm v1 "$(member 2)"
incus move "${REMOTE}:v1" "${REMOTE}:v1" --target "$(member 1)"
check_vm v1 "$(member 1)"

log "Heal the cluster after losing a member"
incus config set "${REMOTE}:" cluster.offline_threshold=11 cluster.healing_threshold=15
incus move "${REMOTE}:v2" "${REMOTE}:v2" --target "$(member 2)"
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

log "Cleanup"
incus delete -f "${REMOTE}:v1" "${REMOTE}:v2"
incus storage volume delete "${REMOTE}:shared" vol1
incus storage volume delete "${REMOTE}:shared" vol2
incus storage delete "${REMOTE}:shared"

log "PASS"
