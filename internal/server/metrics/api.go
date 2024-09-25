package metrics

// Metrics represents instance metrics.
type Metrics struct {
	CPU            []CPUMetrics        `json:"cpu_seconds_total" yaml:"cpu_seconds_total"`
	CPUs           int                 `json:"cpus" yaml:"cpus"`
	Disk           []DiskMetrics       `json:"disk" yaml:"disk"`
	Filesystem     []FilesystemMetrics `json:"filesystem" yaml:"filesystem"`
	Memory         MemoryMetrics       `json:"memory" yaml:"memory"`
	Network        []NetworkMetrics    `json:"network" yaml:"network"`
	ProcessesTotal uint64              `json:"procs_total" yaml:"procs_total"`
}

// CPUMetrics represents CPU metrics for an instance.
type CPUMetrics struct {
	CPU            string  `json:"cpu" yaml:"cpu"`
	SecondsUser    float64 `json:"cpu_seconds_user" yaml:"cpu_seconds_user"`
	SecondsNice    float64 `json:"cpu_seconds_nice" yaml:"cpu_seconds_nice"`
	SecondsSystem  float64 `json:"cpu_seconds_system" yaml:"cpu_seconds_system"`
	SecondsIdle    float64 `json:"cpu_seconds_idle" yaml:"cpu_seconds_idle"`
	SecondsIOWait  float64 `json:"cpu_seconds_iowait" yaml:"cpu_seconds_iowait"`
	SecondsIRQ     float64 `json:"cpu_seconds_irq" yaml:"cpu_seconds_irq"`
	SecondsSoftIRQ float64 `json:"cpu_seconds_softirq" yaml:"cpu_seconds_softirq"`
	SecondsSteal   float64 `json:"cpu_seconds_steal" yaml:"cpu_seconds_steal"`
}

// DiskMetrics represents disk metrics for an instance.
type DiskMetrics struct {
	Device          string `json:"device" yaml:"device"`
	ReadBytes       uint64 `json:"disk_read_bytes" yaml:"disk_read_bytes"`
	ReadsCompleted  uint64 `json:"disk_reads_completed" yaml:"disk_reads_completes"`
	WrittenBytes    uint64 `json:"disk_written_bytes" yaml:"disk_written_bytes"`
	WritesCompleted uint64 `json:"disk_writes_completed" yaml:"disk_writes_completed"`
}

// FilesystemMetrics represents filesystem metrics for an instance.
type FilesystemMetrics struct {
	Device         string `json:"device" yaml:"device"`
	Mountpoint     string `json:"mountpoint" yaml:"mountpoint"`
	FSType         string `json:"fstype" yaml:"fstype"`
	AvailableBytes uint64 `json:"filesystem_avail_bytes" yaml:"filesystem_avail_bytes"`
	FreeBytes      uint64 `json:"filesystem_free_bytes" yaml:"filesystem_free_bytes"`
	SizeBytes      uint64 `json:"filesystem_size_bytes" yaml:"filesystem_size_bytes"`
}

// MemoryMetrics represents memory metrics for an instance.
type MemoryMetrics struct {
	ActiveAnonBytes     uint64 `json:"memory_active_anon_bytes" yaml:"memory_active_anon_bytes"`
	ActiveFileBytes     uint64 `json:"memory_active_file_bytes" yaml:"memory_active_file_bytes"`
	ActiveBytes         uint64 `json:"memory_active_bytes" yaml:"memory_active_bytes"`
	CachedBytes         uint64 `json:"memory_cached_bytes" yaml:"memory_cached_bytes"`
	DirtyBytes          uint64 `json:"memory_dirty_bytes" yaml:"memory_dirty_bytes"`
	HugepagesFreeBytes  uint64 `json:"memory_hugepages_free_bytes" yaml:"memory_hugepages_Free_bytes"`
	HugepagesTotalBytes uint64 `json:"memory_hugepages_total_bytes" yaml:"memory_hugepages_total_bytes"`
	InactiveAnonBytes   uint64 `json:"memory_inactive_anon_bytes" yaml:"memory_inactive_anon_bytes"`
	InactiveFileBytes   uint64 `json:"memory_inactive_file_bytes" yaml:"memory_inactive_file_bytes"`
	InactiveBytes       uint64 `json:"memory_inactive_bytes" yaml:"memory_inactive_bytes"`
	MappedBytes         uint64 `json:"memory_mapped_bytes" yaml:"memory_mapped_bytes"`
	MemAvailableBytes   uint64 `json:"memory_mem_available_bytes" yaml:"memory_mem_available_bytes"`
	MemFreeBytes        uint64 `json:"memory_mem_free_bytes" yaml:"memory_mem_Free_bytes"`
	MemTotalBytes       uint64 `json:"memory_mem_total_bytes" yaml:"memory_mem_total_bytes"`
	RSSBytes            uint64 `json:"memory_rss_bytes" yaml:"memory_rss_bytes"`
	ShmemBytes          uint64 `json:"memory_shmem_bytes" yaml:"memory_shmem_bytes"`
	SwapBytes           uint64 `json:"memory_swap_bytes" yaml:"memory_swap_bytes"`
	UnevictableBytes    uint64 `json:"memory_unevictable_bytes" yaml:"memory_unevictable_bytes"`
	WritebackBytes      uint64 `json:"memory_writeback_bytes" yaml:"memory_writeback_bytes"`
	OOMKills            uint64 `json:"memory_oom_kills" yaml:"memory_oom_kills"`
}

// NetworkMetrics represents network metrics for an instance.
type NetworkMetrics struct {
	Device          string `json:"device" yaml:"device"`
	ReceiveBytes    uint64 `json:"network_receive_bytes" yaml:"network_receive_bytes"`
	ReceiveDrop     uint64 `json:"network_receive_drop" yaml:"network_receive_drop"`
	ReceiveErrors   uint64 `json:"network_receive_errs" yaml:"network_receive_errs"`
	ReceivePackets  uint64 `json:"network_receive_packets" yaml:"network_receive_packets"`
	TransmitBytes   uint64 `json:"network_transmit_bytes" yaml:"network_transmit_bytes"`
	TransmitDrop    uint64 `json:"network_transmit_drop" yaml:"network_transmit_drop"`
	TransmitErrors  uint64 `json:"network_transmit_errs" yaml:"network_transmit_errs"`
	TransmitPackets uint64 `json:"network_transmit_packets" yaml:"network_transmit_packets"`
}
