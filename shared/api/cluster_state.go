package api

// ClusterMemberSysInfo represents the sysinfo of a cluster member.
//
// swagger:model
//
// API extension: cluster_member_state.
type ClusterMemberSysInfo struct {
	Uptime       int64     `json:"uptime" yaml:"uptime"`
	LoadAverages []float64 `json:"load_averages" yaml:"load_averages"`
	TotalRAM     uint64    `json:"total_ram" yaml:"total_ram"`
	FreeRAM      uint64    `json:"free_ram" yaml:"free_ram"`
	SharedRAM    uint64    `json:"shared_ram" yaml:"shared_ram"`
	BufferRAM    uint64    `json:"buffered_ram" yaml:"buffered_ram"`
	TotalSwap    uint64    `json:"total_swap" yaml:"total_swap"`
	FreeSwap     uint64    `json:"free_swap" yaml:"free_swap"`
	Processes    uint16    `json:"processes" yaml:"processes"`
}

// ClusterMemberState represents the state of a cluster member.
//
// swagger:model
//
// API extension: cluster_member_state.
type ClusterMemberState struct {
	SysInfo      ClusterMemberSysInfo        `json:"sysinfo" yaml:"sysinfo"`
	StoragePools map[string]StoragePoolState `json:"storage_pools" yaml:"storage_pools"`
}
