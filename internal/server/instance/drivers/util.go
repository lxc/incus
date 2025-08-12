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
	"time"

	yaml "gopkg.in/yaml.v2"

	"github.com/lxc/incus/v6/internal/linux"
	"github.com/lxc/incus/v6/internal/server/cluster"
	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/instance/drivers/cfg"
	"github.com/lxc/incus/v6/internal/server/instance/drivers/qmp"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/state"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/resources"
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
		// Skip if not in the list of servers we're interested in.
		if servers != nil && !slices.Contains(servers, node.Name) {
			continue
		}

		// Get node resources.
		res, err := getNodeResources(s, node.Name, node.Address)
		if err != nil {
			return nil, fmt.Errorf("Failed to get resources for %s: %w", node.Name, err)
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
	return strings.ReplaceAll(value, ",", ",,")
}

// roundDownToBlockSize returns the largest multiple of blockSize less than or equal to the input value.
func roundDownToBlockSize(value int64, blockSize int64) int64 {
	if value%blockSize == 0 {
		return value
	}

	return ((value / blockSize) - 1) * blockSize
}

// memoryConfigSectionToMap converts a memory object of type cfg.Section to type map[string]any.
func memoryConfigSectionToMap(section *cfg.Section) map[string]any {
	const blockSize = 128 * 1024 * 1024 // 128MiB
	obj := map[string]any{}
	hostNodes := []int{}

	for key, value := range section.Entries {
		if strings.HasPrefix(key, "host-nodes") {
			hostNode, err := strconv.Atoi(value)
			if err != nil {
				continue
			}

			hostNodes = append(hostNodes, hostNode)
		} else if key == "size" {
			// Size in the config is specified in the format: 1024M, so the last character needs to be removed before parsing.
			memSizeMB, err := strconv.Atoi(value[:len(value)-1])
			if err != nil {
				continue
			}

			obj["size"] = roundDownToBlockSize(int64(memSizeMB)*1024*1024, blockSize)
		} else if key == "merge" || key == "dump" || key == "prealloc" || key == "share" || key == "reserve" {
			val := false
			if value == "on" {
				val = true
			}

			obj[key] = val
		} else {
			obj[key] = value
		}
	}

	if len(hostNodes) > 0 {
		obj["host-nodes"] = hostNodes
	}

	return obj
}

// extractTrailingNumber extracts the trailing number from a string.
// For example, given "dimm1", it returns 1.
func extractTrailingNumber(s string, prefix string) (int, error) {
	if !strings.HasPrefix(s, prefix) {
		return -1, fmt.Errorf("Prefix %s not found in %s", prefix, s)
	}

	trimmed := strings.TrimPrefix(s, prefix)
	num, err := strconv.Atoi(trimmed)
	if err != nil {
		return -1, err
	}

	return num, nil
}

// findNextDimmIndex finds the next available index for a pc-dimm device
// whose ID starts with the prefix "dimm".
func findNextDimmIndex(monitor *qmp.Monitor) (int, error) {
	devices, err := monitor.GetDimmDevices()
	if err != nil {
		return -1, err
	}

	index := -1
	for _, dev := range devices {
		i, err := extractTrailingNumber(dev.ID, "dimm")
		if err != nil {
			continue
		}

		if i > index {
			index = i
		}
	}

	return index + 1, nil
}

// findNextMemoryIndex finds the next available index for a memory object
// whose ID starts with the prefix "mem".
func findNextMemoryIndex(monitor *qmp.Monitor) (int, error) {
	memDevs, err := monitor.GetMemdev()
	if err != nil {
		return -1, err
	}

	memIndex := -1
	for _, mem := range memDevs {
		var index int
		index, err := extractTrailingNumber(mem.ID, "mem")
		if err != nil {
			continue
		}

		if index > memIndex {
			memIndex = index
		}
	}

	return memIndex + 1, nil
}

// getNodeResources updates the cluster resource cache..
func getNodeResources(s *state.State, name string, address string) (*api.Resources, error) {
	resourcesPath := internalUtil.CachePath("resources", fmt.Sprintf("%s.yaml", name))

	// Check if cache is recent (less than 24 hours).
	fi, err := os.Stat(resourcesPath)
	if err == nil && time.Since(fi.ModTime()) < 24*time.Hour {
		data, err := os.ReadFile(resourcesPath)
		if err == nil {
			var res api.Resources
			if yaml.Unmarshal(data, &res) == nil {
				return &res, nil
			}
		}
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	var res *api.Resources
	if name == s.ServerName {
		// Handle the local node.
		// We still cache the data as it's not particularly cheap to get.
		res, err = resources.GetResources()
		if err != nil {
			return nil, err
		}
	} else {
		// Handle remote nodes.
		client, err := cluster.Connect(address, s.Endpoints.NetworkCert(), s.ServerCert(), nil, true)
		if err != nil {
			return nil, err
		}

		res, err = client.GetServerResources()
		if err != nil {
			return nil, err
		}
	}

	// Cache the data.
	data, err := yaml.Marshal(res)
	if err == nil {
		_ = os.WriteFile(resourcesPath, data, 0o600)
	}

	return res, nil
}
