test_cgroup() {
    if [ -n "${INCUS_OFFLINE:-}" ]; then
        echo "==> SKIP: External connectivity needed to pull test image"
        export TEST_UNMET_REQUIREMENT="external connectivity needed to pull test image"
        return
    fi

    if ! command -v iperf3 > /dev/null 2>&1; then
        echo "==> SKIP: iperf3 not available"
        export TEST_UNMET_REQUIREMENT="iperf3 not available"
        return
    fi

    if [ ! -d /var/lib/lxcfs/proc ] && [ ! -d /var/lib/incus-lxcfs/proc ]; then
        echo "==> SKIP: lxcfs isn't running"
        export TEST_UNMET_REQUIREMENT="lxcfs isn't running"
        return
    fi

    poolName=$(incus profile device get default root pool)

    incus network create incusbr0
    incus init images:debian/13 c1 -s "${poolName}"
    incus config device add c1 eth0 nic network=incusbr0 name=eth0

    echo "==> Start a container with no limits"
    incus start c1

    echo "==> Validate lxcfs is functional"
    incus exec c1 -- grep -qF lxcfs /proc/self/mounts

    echo "==> Validate default values"
    [ "$(incus exec c1 -- nproc)" = "$(nproc)" ]
    [ "$(incus exec c1 -- grep ^MemTotal /proc/meminfo)" = "$(grep ^MemTotal /proc/meminfo)" ]
    if [ -e "/sys/fs/cgroup/memory/memory.memsw.limit_in_bytes" ] || [ -e "/sys/fs/cgroup/system.slice/memory.swap.max" ]; then
        [ "$(incus exec c1 -- grep ^SwapTotal /proc/meminfo)" = "$(grep ^SwapTotal /proc/meminfo)" ]
    else
        [ "$(incus exec c1 -- grep ^SwapTotal /proc/meminfo)" = "SwapTotal:             0 kB" ]
    fi

    if [ -e "/sys/fs/cgroup/cpu" ]; then
        [ "$(incus exec c1 -- cat /sys/fs/cgroup/cpu/cpu.shares)" = "1024" ]
    else
        [ "$(incus exec c1 -- cat /sys/fs/cgroup/cpu.weight)" = "100" ]
    fi

    if [ -e "/sys/fs/cgroup/pids" ]; then
        [ "$(incus exec c1 -- cat /sys/fs/cgroup/pids/pids.max)" = "max" ]
    else
        [ "$(incus exec c1 -- cat /sys/fs/cgroup/pids.max)" = "max" ]
    fi

    if [ -e "/sys/fs/cgroup/memory" ]; then
        [ "$(incus exec c1 -- cat /sys/fs/cgroup/memory/memory.swappiness)" = "$(cat /sys/fs/cgroup/memory/memory.swappiness)" ]
    fi

    echo "==> Testing CPU limits"
    incus config set c1 limits.cpu=2
    [ "$(incus exec c1 -- nproc)" = "2" ]

    incus config set c1 limits.cpu=200
    [ "$(incus exec c1 -- nproc)" = "$(nproc)" ]

    incus config set c1 limits.cpu=0,2
    [ "$(incus exec c1 -- nproc)" = "2" ]

    incus config set c1 limits.cpu=2-2
    [ "$(incus exec c1 -- nproc)" = "1" ]

    incus config unset c1 limits.cpu
    [ "$(incus exec c1 -- nproc)" = "$(nproc)" ]

    incus config set c1 limits.cpu.priority 5
    if [ -e "/sys/fs/cgroup/cpu" ]; then
        [ "$(incus exec c1 -- cat /sys/fs/cgroup/cpu/cpu.shares)" = "1019" ]
    else
        [ "$(incus exec c1 -- cat /sys/fs/cgroup/cpu.weight)" = "95" ]
    fi

    incus config unset c1 limits.cpu.priority
    if [ -e "/sys/fs/cgroup/cpu" ]; then
        [ "$(incus exec c1 -- cat /sys/fs/cgroup/cpu/cpu.shares)" = "1024" ]
    else
        [ "$(incus exec c1 -- cat /sys/fs/cgroup/cpu.weight)" = "100" ]
    fi

    incus config set c1 limits.cpu.allowance 10%
    if [ -e "/sys/fs/cgroup/cpu" ]; then
        [ "$(incus exec c1 -- cat /sys/fs/cgroup/cpu/cpu.shares)" = "102" ]
    else
        [ "$(incus exec c1 -- cat /sys/fs/cgroup/cpu.weight)" = "10" ]
    fi

    incus config set c1 limits.cpu.allowance 10ms/100ms
    if [ -e "/sys/fs/cgroup/cpu" ]; then
        [ "$(incus exec c1 -- cat /sys/fs/cgroup/cpu/cpu.shares)" = "1024" ]
        [ "$(incus exec c1 -- cat /sys/fs/cgroup/cpu/cpu.cfs_period_us)" = "100000" ]
        [ "$(incus exec c1 -- cat /sys/fs/cgroup/cpu/cpu.cfs_quota_us)" = "10000" ]
    else
        [ "$(incus exec c1 -- cat /sys/fs/cgroup/cpu.weight)" = "100" ]
        [ "$(incus exec c1 -- cat /sys/fs/cgroup/cpu.max)" = "10000 100000" ]
    fi
    incus config unset c1 limits.cpu.allowance

    echo "==> Testing memory limits"
    incus config set c1 limits.memory=2GiB
    [ "$(incus exec c1 -- grep ^MemTotal /proc/meminfo)" = "MemTotal:        2097152 kB" ]
    if [ -e "/sys/fs/cgroup/memory/memory.memsw.limit_in_bytes" ] || [ -e "/sys/fs/cgroup/memory.swap.max" ]; then
        [ "$(incus exec c1 -- grep ^SwapTotal /proc/meminfo)" = "SwapTotal:       2097152 kB" ]
    else
        [ "$(incus exec c1 -- grep ^SwapTotal /proc/meminfo)" = "SwapTotal:             0 kB" ]
    fi

    if [ -e "/sys/fs/cgroup/memory" ]; then
        incus config set c1 limits.memory.swap=false
        [ "$(incus exec c1 -- cat /sys/fs/cgroup/memory/memory.swappiness)" = "0" ]
        [ "$(incus exec c1 -- grep ^SwapTotal /proc/meminfo)" = "SwapTotal:             0 kB" ]

        incus config set c1 limits.memory.swap=true
        incus config set c1 limits.memory.swap.priority=5
        # swappiness is 70 - 5 (priority)
        [ "$(incus exec c1 -- cat /sys/fs/cgroup/memory/memory.swappiness)" = "65" ]
    fi

    if [ -e "/sys/fs/cgroup/memory/memory.memsw.limit_in_bytes" ] || [ -e "/sys/fs/cgroup/memory.swap.max" ]; then
        incus config set c1 limits.memory 128MiB
        [ "$(incus exec c1 -- grep ^MemTotal /proc/meminfo)" = "MemTotal:         131072 kB" ]

        incus exec c1 -- mkdir -p /root/dd
        incus exec c1 -- mount -t tmpfs tmpfs /root/dd -o size=2G
        incus exec c1 -- dd if=/dev/zero of=/root/dd/blob bs=4M count=16
        dmesg -c > /dev/null 2>&1
        # shellcheck disable=SC2251
        ! incus exec c1 -- dd if=/dev/zero of=/root/dd/blob bs=4M count=64
        dmesg | grep -q "Memory cgroup out of memory: Killed process"
        dmesg -c > /dev/null 2>&1

        # shellcheck disable=SC2251
        ! incus stop c1 --force

        # Wait for post-stop to complete
        sleep 2s

        incus config set c1 limits.memory.enforce soft
        incus start c1
        incus exec c1 -- mkdir -p /root/dd
        incus exec c1 -- mount -t tmpfs tmpfs /root/dd -o size=2G
        dmesg -c > /dev/null 2>&1
        incus exec c1 -- dd if=/dev/zero of=/root/dd/blob bs=4M count=64
        dmesg | grep -q "Memory cgroup out of memory: Killed process" && false
        dmesg -c > /dev/null 2>&1

        # shellcheck disable=SC2251
        ! incus stop c1 --force

        # Wait for post-stop to complete
        sleep 2s

        incus config unset c1 limits.memory
        incus config unset c1 limits.memory.enforce
        incus start c1
    fi

    echo "==> Testing process limits"
    incus config set c1 limits.processes=2000
    if [ -e "/sys/fs/cgroup/pids" ]; then
        [ "$(incus exec c1 -- cat /sys/fs/cgroup/pids/pids.max)" = "2000" ]
    else
        [ "$(incus exec c1 -- cat /sys/fs/cgroup/pids.max)" = "2000" ]
    fi

    echo "==> Testing network limits"
    incus restart c1 --force
    sleep 10s
    IP=$(incus network get incusbr0 ipv4.address | cut -d/ -f1)
    incus exec c1 -- apt-get install --no-install-recommends --yes iperf3

    iperf3 -s -D
    incus exec c1 -- iperf3 -c "${IP}" -J > "${TEST_DIR}/iperf.json"
    E1=$(($(jq .end.sum_sent.bits_per_second < "${TEST_DIR}/iperf.json" | cut -d. -f1) / 1024 / 1024))
    incus exec c1 -- iperf3 -R -c "${IP}" -J > "${TEST_DIR}/iperf.json"
    I1=$(($(jq .end.sum_sent.bits_per_second < "${TEST_DIR}/iperf.json" | cut -d. -f1) / 1024 / 1024))
    echo "Throughput before limits: ${E1}Mbps / ${I1}Mbps"

    incus config device set c1 eth0 limits.egress=50Mbit limits.ingress=200Mbit
    incus exec c1 -- iperf3 -c "${IP}" -J > "${TEST_DIR}/iperf.json"
    E2=$(($(jq .end.sum_sent.bits_per_second < "${TEST_DIR}/iperf.json" | cut -d. -f1) / 1024 / 1024))
    incus exec c1 -- iperf3 -R -c "${IP}" -J > "${TEST_DIR}/iperf.json"
    I2=$(($(jq .end.sum_sent.bits_per_second < "${TEST_DIR}/iperf.json" | cut -d. -f1) / 1024 / 1024))
    echo "Throughput after limits: ${E2}Mbps / ${I2}Mbps"
    [ "${E2}" -lt "50" ]
    [ "${I2}" -lt "200" ]

    pkill -9 iperf3
    incus config device set c1 eth0 limits.egress= limits.ingress=

    echo "==> Testing disk limits"
    if [ -e /sys/fs/cgroup/init.scope/io.weight ]; then
        incus config set c1 limits.disk.priority=5
        [ "$(incus exec c1 -- cat /sys/fs/cgroup/io.weight)" = "default 500" ]
    elif [ -e /sys/fs/cgroup/blkio/blkio.weight ]; then
        incus config set c1 limits.disk.priority=5
        [ "$(incus exec c1 -- cat /sys/fs/cgroup/blkio/blkio.weight)" = "500" ]
    fi

    incus config device set c1 root limits.read=10iops limits.write=20iops
    if [ -e /sys/fs/cgroup/init.scope/io.max ]; then
        incus exec c1 -- grep -q "riops=10 wiops=20" /sys/fs/cgroup/io.max
    else
        incus exec c1 -- grep -q "10$" /sys/fs/cgroup/blkio/blkio.throttle.read_iops_device
        incus exec c1 -- grep -q "20$" /sys/fs/cgroup/blkio/blkio.throttle.write_iops_device
    fi

    incus config device set c1 root limits.read=10MB limits.write=20MB
    if [ -e /sys/fs/cgroup/init.scope/io.max ]; then
        incus exec c1 -- grep -q "rbps=10000000 wbps=20000000" /sys/fs/cgroup/io.max
    else
        incus exec c1 -- grep -q "10000000$" /sys/fs/cgroup/blkio/blkio.throttle.read_bps_device
        incus exec c1 -- grep -q "20000000$" /sys/fs/cgroup/blkio/blkio.throttle.write_bps_device
    fi

    incus config device set c1 root limits.read= limits.write=

    echo "==> Testing the freezer"
    incus pause c1
    ! incus exec c1 bash || false
    incus start c1

    incus delete -f c1
    incus network delete incusbr0
}
