package project

import (
	"context"
	"fmt"
	"strconv"

	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/shared/api"
)

// GetCurrentAllocations returns the current resource utilization for a given project.
func GetCurrentAllocations(ctx context.Context, tx *db.ClusterTx, projectName string) (map[string]api.ProjectStateResource, error) {
	result := map[string]api.ProjectStateResource{}

	// Get the project.
	info, err := fetchProject(tx, projectName, false)
	if err != nil {
		return nil, err
	}

	if info == nil {
		return nil, fmt.Errorf("Project %q returned empty info struct", projectName)
	}

	info.Instances, err = expandInstancesConfigAndDevices(info.Instances, info.Profiles)
	if err != nil {
		return nil, err
	}

	// Get the instance aggregated values.
	raw, err := getAggregateLimits(info, allAggregateLimits)
	if err != nil {
		return nil, err
	}

	result["cpu"] = raw["limits.cpu"]
	result["disk"] = raw["limits.disk"]
	result["memory"] = raw["limits.memory"]
	result["networks"] = raw["limits.networks"]
	result["processes"] = raw["limits.processes"]

	// Get the instance count values.
	count, limit, err := getTotalInstanceCountLimit(info)
	if err != nil {
		return nil, err
	}

	result["instances"] = api.ProjectStateResource{
		Limit: int64(limit),
		Usage: int64(count),
	}

	count, limit, err = getInstanceCountLimit(info, instancetype.Container)
	if err != nil {
		return nil, err
	}

	result["containers"] = api.ProjectStateResource{
		Limit: int64(limit),
		Usage: int64(count),
	}

	count, limit, err = getInstanceCountLimit(info, instancetype.VM)
	if err != nil {
		return nil, err
	}

	result["virtual-machines"] = api.ProjectStateResource{
		Limit: int64(limit),
		Usage: int64(count),
	}

	// Get the network limit and usage.
	overallValue, ok := info.Project.Config["limits.networks"]
	limit = -1
	if ok {
		limit, err = strconv.Atoi(overallValue)
		if err != nil {
			return nil, err
		}
	}

	networks, err := tx.GetCreatedNetworks(ctx)
	if err != nil {
		return nil, err
	}

	result["networks"] = api.ProjectStateResource{
		Limit: int64(limit),
		Usage: int64(len(networks[projectName])),
	}

	return result, nil
}
