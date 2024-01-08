package cluster

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/state"
	storagePools "github.com/lxc/incus/internal/server/storage"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/logger"
)

// getLoadAvgs returns the host's load averages from /proc/loadavg.
func getLoadAvgs() ([]float64, error) {
	loadAvgs := make([]float64, 3)

	loadAvgsBuf, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return nil, err
	}

	loadAvgFields := strings.Fields(string(loadAvgsBuf))

	loadAvgs[0], err = strconv.ParseFloat(loadAvgFields[0], 64)
	if err != nil {
		return nil, err
	}

	loadAvgs[1], err = strconv.ParseFloat(loadAvgFields[1], 64)
	if err != nil {
		return nil, err
	}

	loadAvgs[2], err = strconv.ParseFloat(loadAvgFields[2], 64)
	if err != nil {
		return nil, err
	}

	return loadAvgs, nil
}

// MemberState retrieves state information about the cluster member.
func MemberState(ctx context.Context, s *state.State, memberName string) (*api.ClusterMemberState, error) {
	var err error
	var memberState api.ClusterMemberState

	// Get system info.
	info := unix.Sysinfo_t{}
	err = unix.Sysinfo(&info)
	if err != nil {
		logger.Warn("Failed getting sysinfo", logger.Ctx{"err": err})

		return nil, err
	}

	// Account for different representations of Sysinfo_t on different architectures.
	memberState.SysInfo.Uptime = int64(info.Uptime)
	memberState.SysInfo.TotalRAM = uint64(info.Totalram)
	memberState.SysInfo.SharedRAM = uint64(info.Sharedram)
	memberState.SysInfo.BufferRAM = uint64(info.Bufferram)
	memberState.SysInfo.FreeRAM = uint64(info.Freeram)
	memberState.SysInfo.TotalSwap = uint64(info.Totalswap)
	memberState.SysInfo.FreeSwap = uint64(info.Freeswap)

	memberState.SysInfo.Processes = info.Procs
	memberState.SysInfo.LoadAverages, err = getLoadAvgs()
	if err != nil {
		return nil, fmt.Errorf("Failed getting load averages: %w", err)
	}

	// Get storage pool states.
	stateCreated := db.StoragePoolCreated

	var pools map[int64]api.StoragePool
	var poolMembers map[int64]map[int64]db.StoragePoolNode

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		pools, poolMembers, err = tx.GetStoragePools(ctx, &stateCreated)

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("Failed loading storage pools: %w", err)
	}

	memberState.StoragePools = make(map[string]api.StoragePoolState, len(pools))

	for poolID := range pools {
		pool, err := storagePools.LoadByRecord(s, poolID, pools[poolID], poolMembers[poolID])
		if err != nil {
			return nil, fmt.Errorf("Failed loading storage pool %q: %w", pools[poolID].Name, err)
		}

		res, err := pool.GetResources()
		if err != nil {
			return nil, fmt.Errorf("Failed getting storage pool resources %q: %w", pools[poolID].Name, err)
		}

		memberState.StoragePools[pools[poolID].Name] = api.StoragePoolState{
			ResourcesStoragePool: *res,
		}
	}

	return &memberState, nil
}
