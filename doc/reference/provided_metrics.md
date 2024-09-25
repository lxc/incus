(provided-metrics)=
# Provided metrics

Incus provides a number of instance metrics and internal metrics.
See {ref}`metrics` for instructions on how to work with these metrics.

## Instance metrics

The following instance metrics are provided:

```{list-table}
   :header-rows: 1

* - Metric
  - Description
* - `incus_cpu_effective_total`
  - Total number of effective CPUs
* - `incus_cpu_seconds_total{cpu="<cpu>", mode="<mode>"}`
  - Total number of CPU time used (in seconds)
* - `incus_disk_read_bytes_total{device="<dev>"}`
  - Total number of bytes read
* - `incus_disk_reads_completed_total{device="<dev>"}`
  - Total number of completed reads
* - `incus_disk_written_bytes_total{device="<dev>"}`
  - Total number of bytes written
* - `incus_disk_writes_completed_total{device="<dev>"}`
  - Total number of completed writes
* - `incus_filesystem_avail_bytes{device="<dev>",fstype="<type>"}`
  - Available space (in bytes)
* - `incus_filesystem_free_bytes{device="<dev>",fstype="<type>"}`
  - Free space (in bytes)
* - `incus_filesystem_size_bytes{device="<dev>",fstype="<type>"}`
  - Size of the file system (in bytes)
* - `incus_memory_Active_anon_bytes`
  - Amount of anonymous memory on active LRU list
* - `incus_memory_Active_bytes`
  - Amount of memory on active LRU list
* - `incus_memory_Active_file_bytes`
  - Amount of file-backed memory on active LRU list
* - `incus_memory_Cached_bytes`
  - Amount of cached memory
* - `incus_memory_Dirty_bytes`
  - Amount of memory waiting to be written back to the disk
* - `incus_memory_HugepagesFree_bytes`
  - Amount of free memory for `hugetlb`
* - `incus_memory_HugepagesTotal_bytes`
  - Amount of used memory for `hugetlb`
* - `incus_memory_Inactive_anon_bytes`
  - Amount of anonymous memory on inactive LRU list
* - `incus_memory_Inactive_bytes`
  - Amount of memory on inactive LRU list
* - `incus_memory_Inactive_file_bytes`
  - Amount of file-backed memory on inactive LRU list
* - `incus_memory_Mapped_bytes`
  - Amount of mapped memory
* - `incus_memory_MemAvailable_bytes`
  - Amount of available memory
* - `incus_memory_MemFree_bytes`
  - Amount of free memory
* - `incus_memory_MemTotal_bytes`
  - Amount of used memory
* - `incus_memory_OOM_kills_total`
  - The number of out-of-memory kills
* - `incus_memory_RSS_bytes`
  - Amount of anonymous and swap cache memory
* - `incus_memory_Shmem_bytes`
  - Amount of cached file system data that is swap-backed
* - `incus_memory_Swap_bytes`
  - Amount of used swap memory
* - `incus_memory_Unevictable_bytes`
  - Amount of unevictable memory
* - `incus_memory_Writeback_bytes`
  - Amount of memory queued for syncing to disk
* - `incus_network_receive_bytes_total{device="<dev>"}`
  - Amount of received bytes on a given interface
* - `incus_network_receive_drop_total{device="<dev>"}`
  - Amount of received dropped bytes on a given interface
* - `incus_network_receive_errs_total{device="<dev>"}`
  - Amount of received errors on a given interface
* - `incus_network_receive_packets_total{device="<dev>"}`
  - Amount of received packets on a given interface
* - `incus_network_transmit_bytes_total{device="<dev>"}`
  - Amount of transmitted bytes on a given interface
* - `incus_network_transmit_drop_total{device="<dev>"}`
  - Amount of transmitted dropped bytes on a given interface
* - `incus_network_transmit_errs_total{device="<dev>"}`
  - Amount of transmitted errors on a given interface
* - `incus_network_transmit_packets_total{device="<dev>"}`
  - Amount of transmitted packets on a given interface
* - `incus_procs_total`
  - Number of running processes
```

## Internal metrics

The following internal metrics are provided:

```{list-table}
   :header-rows: 1

* - Metric
  - Description
* - `incus_go_alloc_bytes_total`
  - Total number of bytes allocated (even if freed)
* - `incus_go_alloc_bytes`
  - Number of bytes allocated and still in use
* - `incus_go_buck_hash_sys_bytes`
  - Number of bytes used by the profiling bucket hash table
* - `incus_go_frees_total`
  - Total number of frees
* - `incus_go_gc_sys_bytes`
  - Number of bytes used for garbage collection system metadata
* - `incus_go_goroutines`
  - Number of goroutines that currently exist
* - `incus_go_heap_alloc_bytes`
  - Number of heap bytes allocated and still in use
* - `incus_go_heap_idle_bytes`
  - Number of heap bytes waiting to be used
* - `incus_go_heap_inuse_bytes`
  - Number of heap bytes that are in use
* - `incus_go_heap_objects`
  - Number of allocated objects
* - `incus_go_heap_released_bytes`
  - Number of heap bytes released to OS
* - `incus_go_heap_sys_bytes`
  - Number of heap bytes obtained from system
* - `incus_go_lookups_total`
  - Total number of pointer lookups
* - `incus_go_mallocs_total`
  - Total number of `mallocs`
* - `incus_go_mcache_inuse_bytes`
  - Number of bytes in use by `mcache` structures
* - `incus_go_mcache_sys_bytes`
  - Number of bytes used for `mcache` structures obtained from system
* - `incus_go_mspan_inuse_bytes`
  - Number of bytes in use by `mspan` structures
* - `incus_go_mspan_sys_bytes`
  - Number of bytes used for `mspan` structures obtained from system
* - `incus_go_next_gc_bytes`
  - Number of heap bytes when next garbage collection will take place
* - `incus_go_other_sys_bytes`
  - Number of bytes used for other system allocations
* - `incus_go_stack_inuse_bytes`
  - Number of bytes in use by the stack allocator
* - `incus_go_stack_sys_bytes`
  - Number of bytes obtained from system for stack allocator
* - `incus_go_sys_bytes`
  - Number of bytes obtained from system
* - `incus_operations_total`
  - Number of running operations
* - `incus_uptime_seconds`
  - Daemon uptime (in seconds)
* - `incus_warnings_total`
  - Number of active warnings
```
