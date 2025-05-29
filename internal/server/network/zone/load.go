package zone

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/util"
)

// LoadByName loads and initializes a Network zone from the database by name.
func LoadByName(s *state.State, name string) (NetworkZone, error) {
	var dbZone []cluster.NetworkZone
	var zoneInfo *api.NetworkZone

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		filter := cluster.NetworkZoneFilter{Name: &name}
		dbZone, err = cluster.GetNetworkZones(ctx, tx.Tx(), filter)
		if err != nil {
			return err
		}

		if len(dbZone) != 1 {
			return fmt.Errorf("Loading network zone named %s returned an unexpected amount of results: %d", name, len(dbZone))
		}

		zoneInfo, err = dbZone[0].ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	var zone NetworkZone = &zone{}
	zone.init(s, int64(dbZone[0].ID), dbZone[0].Project, zoneInfo)

	return zone, nil
}

// LoadByNameAndProject loads and initializes a Network zone from the database by project and name.
func LoadByNameAndProject(s *state.State, projectName string, name string) (NetworkZone, error) {
	var dbZone *cluster.NetworkZone
	var zoneInfo *api.NetworkZone

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		filter := cluster.NetworkZoneFilter{
			Project: &projectName,
			Name:    &name,
		}

		zones, err := cluster.GetNetworkZones(ctx, tx.Tx(), filter)
		if err != nil {
			return err
		}

		if len(zones) == 0 {
			return api.StatusErrorf(http.StatusNotFound, "Network zone not found")
		}

		dbZone = &zones[0]
		zoneInfo, err = dbZone.ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	var zone NetworkZone = &zone{}
	zone.init(s, int64(dbZone.ID), projectName, zoneInfo)

	return zone, nil
}

// Create validates supplied record and creates new Network zone record in the database.
func Create(s *state.State, projectName string, zoneInfo *api.NetworkZonesPost) error {
	var zone NetworkZone = &zone{}
	zone.init(s, -1, projectName, nil)

	err := zone.validateName(zoneInfo.Name)
	if err != nil {
		return err
	}

	err = zone.validateConfig(&zoneInfo.NetworkZonePut)
	if err != nil {
		return err
	}

	// Load the project.
	var p *api.Project
	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		project, err := cluster.GetProject(ctx, tx.Tx(), projectName)
		if err != nil {
			return err
		}

		p, err = project.ToAPI(ctx, tx.Tx())

		return err
	})
	if err != nil {
		return err
	}

	// Validate restrictions.
	if util.IsTrue(p.Config["restricted"]) {
		found := false
		for _, entry := range strings.Split(p.Config["restricted.networks.zones"], ",") {
			entry = strings.TrimSpace(entry)

			if zoneInfo.Name == entry || strings.HasSuffix(zoneInfo.Name, "."+entry) {
				found = true
				break
			}
		}

		if !found {
			return api.StatusErrorf(http.StatusForbidden, "Project isn't allowed to use this DNS zone")
		}
	}

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbZone := cluster.NetworkZone{
			Project:     projectName,
			Name:        zoneInfo.Name,
			Description: zoneInfo.Description,
		}

		id, err := cluster.CreateNetworkZone(ctx, tx.Tx(), dbZone)
		if err != nil {
			return err
		}

		err = cluster.CreateNetworkZoneConfig(ctx, tx.Tx(), id, zoneInfo.Config)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Trigger a refresh of the TSIG entries.
	err = s.DNS.UpdateTSIG()
	if err != nil {
		return err
	}

	return nil
}

// Exists checks the zone name(s) provided exists.
// If multiple names are provided, also checks that duplicate names aren't specified in the list.
func Exists(s *state.State, name ...string) error {
	checkedzoneNames := make(map[string]struct{}, len(name))

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		for _, zoneName := range name {
			filter := cluster.NetworkZoneFilter{Name: &zoneName}
			dbZone, err := cluster.GetNetworkZones(ctx, tx.Tx(), filter)
			if err != nil {
				return err
			}

			if len(dbZone) != 1 {
				return fmt.Errorf("Loading network zone named %s returned an unexpected amount of results: %d", zoneName, len(dbZone))
			}

			status, err := cluster.NetworkZoneExists(ctx, tx.Tx(), dbZone[0].Project, zoneName)
			if !status {
				return fmt.Errorf("Network zone %q does not exist", zoneName)
			}

			if err != nil {
				return fmt.Errorf("Error when checking existence of %q network zone in project %q", zoneName, dbZone[0].Project)
			}

			_, found := checkedzoneNames[zoneName]
			if found {
				return fmt.Errorf("Network zone %q specified multiple times", zoneName)
			}

			checkedzoneNames[zoneName] = struct{}{}
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}
