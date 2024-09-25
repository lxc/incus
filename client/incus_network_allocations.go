package incus

import (
	"github.com/lxc/incus/v6/shared/api"
)

// GetNetworkAllocations returns a list of Network allocations for a specific project.
func (r *ProtocolIncus) GetNetworkAllocations() ([]api.NetworkAllocations, error) {
	err := r.CheckExtension("network_allocations")
	if err != nil {
		return nil, err
	}

	// Fetch the raw value.
	netAllocations := []api.NetworkAllocations{}
	_, err = r.queryStruct("GET", "/network-allocations", nil, "", &netAllocations)
	if err != nil {
		return nil, err
	}

	return netAllocations, nil
}

// GetNetworkAllocationsAllProjects returns a list of Network allocations across all projects.
func (r *ProtocolIncus) GetNetworkAllocationsAllProjects() ([]api.NetworkAllocations, error) {
	err := r.CheckExtension("network_allocations")
	if err != nil {
		return nil, err
	}

	// Fetch the raw value.
	netAllocations := []api.NetworkAllocations{}
	_, err = r.queryStruct("GET", "/network-allocations?all-projects=true", nil, "", &netAllocations)
	if err != nil {
		return nil, err
	}

	return netAllocations, nil
}
