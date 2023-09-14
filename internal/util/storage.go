package util

// PoolType represents a type of storage pool (local, remote or any).
type PoolType string

// PoolTypeAny represents any storage pool (local or remote).
const PoolTypeAny PoolType = ""

// PoolTypeLocal represents local storage pools.
const PoolTypeLocal PoolType = "local"

// PoolTypeRemote represents remote storage pools.
const PoolTypeRemote PoolType = "remote"
