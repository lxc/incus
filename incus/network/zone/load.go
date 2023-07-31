package zone

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/state"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

// LoadByName loads and initialises a Network zone from the database by name.
func LoadByName(s *state.State, name string) (NetworkZone, error) {
	id, projectName, zoneInfo, err := s.DB.Cluster.GetNetworkZone(name)
	if err != nil {
		return nil, err
	}

	var zone NetworkZone = &zone{}
	zone.init(s, id, projectName, zoneInfo)

	return zone, nil
}

// LoadByNameAndProject loads and initialises a Network zone from the database by project and name.
func LoadByNameAndProject(s *state.State, projectName string, name string) (NetworkZone, error) {
	id, zoneInfo, err := s.DB.Cluster.GetNetworkZoneByProject(projectName, name)
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
	if shared.IsTrue(p.Config["restricted"]) {
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

	// Insert DB record.
	_, err = s.DB.Cluster.CreateNetworkZone(projectName, zoneInfo)
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
	for _, zoneName := range name {
		_, _, _, err := s.DB.Cluster.GetNetworkZone(zoneName)
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
}
