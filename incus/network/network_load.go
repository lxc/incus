package network

import (
	"fmt"
	"sync"

	"github.com/cyphar/incus/incus/state"
	"github.com/cyphar/incus/shared/api"
)

var drivers = map[string]func() Network{
	"bridge":   func() Network { return &bridge{} },
	"macvlan":  func() Network { return &macvlan{} },
	"sriov":    func() Network { return &sriov{} },
	"ovn":      func() Network { return &ovn{} },
	"physical": func() Network { return &physical{} },
}

// ProjectNetwork is a composite type of project name and network name.
type ProjectNetwork struct {
	ProjectName string
	NetworkName string
}

var unavailableNetworks = make(map[ProjectNetwork]struct{})
var unavailableNetworksMu = sync.Mutex{}

// LoadByType loads a network by driver type.
func LoadByType(driverType string) (Type, error) {
	driverFunc, ok := drivers[driverType]
	if !ok {
		return nil, ErrUnknownDriver
	}

	n := driverFunc()
	n.init(nil, -1, "", &api.Network{Type: driverType}, nil)

	return n, nil
}

// LoadByName loads an instantiated network from the database by project and name.
func LoadByName(s *state.State, projectName string, name string) (Network, error) {
	id, netInfo, netNodes, err := s.DB.Cluster.GetNetworkInAnyState(projectName, name)
	if err != nil {
		return nil, err
	}

	driverFunc, ok := drivers[netInfo.Type]
	if !ok {
		return nil, ErrUnknownDriver
	}

	n := driverFunc()
	n.init(s, id, projectName, netInfo, netNodes)

	return n, nil
}

// PatchPreCheck checks if there are any unavailable networks.
func PatchPreCheck() error {
	unavailableNetworksMu.Lock()

	if len(unavailableNetworks) > 0 {
		unavailableNetworkNames := make([]string, 0, len(unavailableNetworks))
		for unavailablePoolName := range unavailableNetworks {
			unavailableNetworkNames = append(unavailableNetworkNames, fmt.Sprintf("%s/%s", unavailablePoolName.ProjectName, unavailablePoolName.NetworkName))
		}

		unavailableNetworksMu.Unlock()
		return fmt.Errorf("Unvailable networks: %v", unavailableNetworkNames)
	}

	unavailableNetworksMu.Unlock()

	return nil
}

// IsAvailable checks if a network is available.
func IsAvailable(projectName string, networkName string) bool {
	unavailableNetworksMu.Lock()
	defer unavailableNetworksMu.Unlock()

	pn := ProjectNetwork{
		ProjectName: projectName,
		NetworkName: networkName,
	}

	_, found := unavailableNetworks[pn]
	return !found
}
