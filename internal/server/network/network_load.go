package network

import (
	"context"
	"fmt"
	"sync"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/api"
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

var (
	unavailableNetworks   = make(map[ProjectNetwork]struct{})
	unavailableNetworksMu = sync.Mutex{}
)

// LoadByType loads a network by driver type.
func LoadByType(driverType string) (Type, error) {
	driverFunc, ok := drivers[driverType]
	if !ok {
		return nil, ErrUnknownDriver
	}

	n := driverFunc()
	err := n.init(nil, -1, "", &api.Network{Type: driverType}, nil)
	if err != nil {
		return nil, err
	}

	return n, nil
}

// LoadByName loads an instantiated network from the database by project and name.
func LoadByName(s *state.State, projectName string, name string) (Network, error) {
	var id int64
	var netInfo *api.Network
	var netNodes map[int64]db.NetworkNode

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		id, netInfo, netNodes, err = tx.GetNetworkInAnyState(ctx, projectName, name)

		return err
	})
	if err != nil {
		return nil, err
	}

	driverFunc, ok := drivers[netInfo.Type]
	if !ok {
		return nil, ErrUnknownDriver
	}

	n := driverFunc()
	err = n.init(s, id, projectName, netInfo, netNodes)
	if err != nil {
		return nil, err
	}

	return n, nil
}

// LoadAllCreated loads all created networks across all projects using a single database transaction.
// Networks that fail to instantiate are present in the returned map with a nil value so that callers
// can handle them individually through LoadByName.
func LoadAllCreated(ctx context.Context, s *state.State) (map[ProjectNetwork]Network, error) {
	var networksInfo map[string]map[int64]db.NetworkInfo

	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		networksInfo, err = tx.GetCreatedNetworksInfo(ctx)

		return err
	})
	if err != nil {
		return nil, err
	}

	networks := make(map[ProjectNetwork]Network)

	for projectName, projectNetworks := range networksInfo {
		for networkID, netInfo := range projectNetworks {
			pn := ProjectNetwork{
				ProjectName: projectName,
				NetworkName: netInfo.Info.Name,
			}

			driverFunc, ok := drivers[netInfo.Info.Type]
			if !ok {
				networks[pn] = nil
				continue
			}

			n := driverFunc()
			err = n.init(s, networkID, projectName, netInfo.Info, netInfo.Nodes)
			if err != nil {
				networks[pn] = nil
				continue
			}

			networks[pn] = n
		}
	}

	return networks, nil
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
		return fmt.Errorf("Unavailable networks: %v", unavailableNetworkNames)
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
