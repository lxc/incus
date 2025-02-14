package drivers

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"

	yaml "gopkg.in/yaml.v2"

	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/state"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/units"
)

// GetClusterCPUFlags returns the list of shared CPU flags across.
func GetClusterCPUFlags(ctx context.Context, s *state.State, servers []string, archName string) ([]string, error) {
	// Get the list of cluster members.
	var nodes []db.RaftNode
	err := s.DB.Node.Transaction(ctx, func(ctx context.Context, tx *db.NodeTx) error {
		var err error
		nodes, err = tx.GetRaftNodes(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}

	// Get all the CPU flags for the architecture.
	flagMembers := map[string]int{}
	coreCount := 0

	for _, node := range nodes {
		// Skip if not in the list.
		if servers != nil && !slices.Contains(servers, node.Name) {
			continue
		}

		// Attempt to load the cached resources.
		resourcesPath := internalUtil.CachePath("resources", fmt.Sprintf("%s.yaml", node.Name))

		data, err := os.ReadFile(resourcesPath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}

			return nil, err
		}

		res := api.Resources{}
		err = yaml.Unmarshal(data, &res)
		if err != nil {
			return nil, err
		}

		// Skip if not the correct architecture.
		if res.CPU.Architecture != archName {
			continue
		}

		// Add the CPU flags to the map.
		for _, socket := range res.CPU.Sockets {
			for _, core := range socket.Cores {
				coreCount += 1
				for _, flag := range core.Flags {
					flagMembers[flag] += 1
				}
			}
		}
	}

	// Get the host flags.
	info := DriverStatuses()[instancetype.VM].Info
	hostFlags, ok := info.Features["flags"].(map[string]bool)
	if !ok {
		// No CPU flags found.
		return nil, nil
	}

	// Build a set of flags common to all cores.
	flags := []string{}

	for k, v := range flagMembers {
		if v != coreCount {
			continue
		}

		hostVal, ok := hostFlags[k]
		if !ok || hostVal {
			continue
		}

		flags = append(flags, k)
	}

	return flags, nil
}

// ParseMemoryStr parses a human representation of memory value as int64 type.
func ParseMemoryStr(memory string) (valueInt int64, err error) {
	if strings.HasSuffix(memory, "%") {
		var percent, memoryTotal int64

		percent, err = strconv.ParseInt(strings.TrimSuffix(memory, "%"), 10, 64)
		if err != nil {
			return 0, err
		}

		memoryTotal, err = linux.DeviceTotalMemory()
		if err != nil {
			return 0, err
		}

		valueInt = (memoryTotal / 100) * percent
	} else {
		valueInt, err = units.ParseByteSizeString(memory)
	}

	return valueInt, err
}

func qemuEscapeCmdline(value string) string {
	return strings.Replace(value, ",", ",,", -1)
}
