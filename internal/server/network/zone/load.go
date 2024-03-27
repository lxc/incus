package zone

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/db/cluster"
	"github.com/lxc/incus/internal/server/state"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/util"
)

// LoadByName loads and initializes a Network zone from the database by name.
func LoadByName(s *state.State, name string) (NetworkZone, error) {
	var id int64
	var projectName string
	var zoneInfo *api.NetworkZone

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		id, projectName, zoneInfo, err = tx.GetNetworkZone(ctx, name)

		return err
	})
	if err != nil {
		return nil, err
	}

	var zone NetworkZone = &zone{}
	zone.init(s, id, projectName, zoneInfo)

	return zone, nil
}

// LoadByNameAndProject loads and initializes a Network zone from the database by project and name.
func LoadByNameAndProject(s *state.State, projectName string, name string) (NetworkZone, error) {
	var id int64
	var zoneInfo *api.NetworkZone

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		id, zoneInfo, err = tx.GetNetworkZoneByProject(ctx, projectName, name)

		return err
	})
	if err != nil {
		return nil, err
	}

	var zone NetworkZone = &zone{}
	zone.init(s, id, projectName, zoneInfo)

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
		// Insert DB record.
		_, err = tx.CreateNetworkZone(ctx, projectName, zoneInfo)

		return err
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
			_, _, _, err := tx.GetNetworkZone(ctx, zoneName)
			if err != nil {
				return fmt.Errorf("Network zone %q does not exist", zoneName)
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
