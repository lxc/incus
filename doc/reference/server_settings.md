(server-settings)=
# Server settings for a Incus production setup

To allow your Incus server to run a large number of instances, configure the following settings to avoid hitting server limits.

The `Value` column contains the suggested value for each parameter.

## `/etc/security/limits.conf`

Domain  | Type  | Item      | Value       | Default   | Description
:-----  | :---  | :----     | :---------- | :-------- | :----------
`*`     | soft  | `nofile`  | `1048576`   | unset     | Maximum number of open files
`*`     | hard  | `nofile`  | `1048576`   | unset     | Maximum number of open files
`root`  | soft  | `nofile`  | `1048576`   | unset     | Maximum number of open files
`root`  | hard  | `nofile`  | `1048576`   | unset     | Maximum number of open files
`*`     | soft  | `memlock` | `unlimited` | unset     | Maximum locked-in-memory address space (KB)
`*`     | hard  | `memlock` | `unlimited` | unset     | Maximum locked-in-memory address space (KB)
`root`  | soft  | `memlock` | `unlimited` | unset     | Maximum locked-in-memory address space (KB), only need with `bpf` syscall supervision
`root`  | hard  | `memlock` | `unlimited` | unset     | Maximum locked-in-memory address space (KB), only need with `bpf` syscall supervision

## `/etc/sysctl.conf`

```{note}
Reboot the server after changing any of these parameters.
```

Parameter                           | Value      | Default   | Description
:-----                              | :---       | :---      | :---
`fs.aio-max-nr`                     | `524288`   | `65536`   | Maximum number of concurrent asynchronous I/O operations (you might need to increase this limit further if you have a lot of workloads that use the AIO subsystem, for example, MySQL)
`fs.inotify.max_queued_events`      | `1048576`  | `16384`   | Upper limit on the number of events that can be queued to the corresponding `inotify` instance (see [`inotify`](https://man7.org/linux/man-pages/man7/inotify.7.html))
`fs.inotify.max_user_instances`     | `1048576`  | `128`     | Upper limit on the number of `inotify` instances that can be created per real user ID (see [`inotify`](https://man7.org/linux/man-pages/man7/inotify.7.html))
`fs.inotify.max_user_watches`       | `1048576`  | `8192`    | Upper limit on the number of watches that can be created per real user ID (see [`inotify`](https://man7.org/linux/man-pages/man7/inotify.7.html))
`kernel.dmesg_restrict`             | `1`        | `0`       | Whether to deny container access to the messages in the kernel ring buffer (note that this will also deny access to non-root users on the host system)
`kernel.keys.maxbytes`              | `2000000`  | `20000`   | Maximum size of the key ring that non-root users can use
`kernel.keys.maxkeys`               | `2000`     | `200`     | Maximum number of keys that a non-root user can use (the value should be higher than the number of instances)
`net.core.bpf_jit_limit`            | `1000000000` | varies  | Limit on the size of eBPF JIT allocations (on kernels < 5.15 that are compiled with `CONFIG_BPF_JIT_ALWAYS_ON=y`, this value might limit the amount of instances that can be created)
`net.ipv4.neigh.default.gc_thresh3` | `8192`     | `1024`    | Maximum number of entries in the IPv4 ARP table (increase this value if you plan to create over 1024 instances - otherwise, you will get the error `neighbour: ndisc_cache: neighbor table overflow!` when the ARP table gets full and the instances cannot get a network configuration; see [`ip-sysctl`](https://www.kernel.org/doc/Documentation/networking/ip-sysctl.txt))
`net.ipv6.neigh.default.gc_thresh3` | `8192`     | `1024`    | Maximum number of entries in IPv6 ARP table (increase this value if you plan to create over 1024 instances - otherwise, you will get the error `neighbour: ndisc_cache: neighbor table overflow!` when the ARP table gets full and the instances cannot get a network configuration; see [`ip-sysctl`](https://www.kernel.org/doc/Documentation/networking/ip-sysctl.txt))
`vm.max_map_count`                  | `262144`   | `65530`   | Maximum number of memory map areas a process may have (memory map areas are used as a side-effect of calling `malloc`, directly by `mmap` and `mprotect`, and also when loading shared libraries)
