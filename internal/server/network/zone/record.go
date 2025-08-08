package zone

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"github.com/miekg/dns"

	"github.com/lxc/incus/v6/internal/server/cluster/request"
	"github.com/lxc/incus/v6/internal/server/db"
	dbCluster "github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/shared/api"
)

func (d *zone) AddRecord(req api.NetworkZoneRecordsPost) error {
	// Validate.
	err := d.validateName(req.Name)
	if err != nil {
		return err
	}

	err = d.validateRecordConfig(req.NetworkZoneRecordPut)
	if err != nil {
		return err
	}

	// Validate entries.
	err = d.validateEntries(req.NetworkZoneRecordPut)
	if err != nil {
		return err
	}

	err = d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		// Create the network zone record object.
		dbRecord := dbCluster.NetworkZoneRecord{
			NetworkZoneID: int(d.id),
			Name:          req.Name,
			Description:   req.Description,
			Entries:       req.Entries,
		}

		// Add the new record.
		id, err := dbCluster.CreateNetworkZoneRecord(ctx, tx.Tx(), dbRecord)
		if err != nil {
			return err
		}

		// Add the config.
		err = dbCluster.CreateNetworkZoneRecordConfig(ctx, tx.Tx(), id, req.Config)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

func (d *zone) GetRecords() ([]api.NetworkZoneRecord, error) {
	s := d.state

	records := []api.NetworkZoneRecord{}

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		zoneID := int(d.id)
		filter := dbCluster.NetworkZoneRecordFilter{
			NetworkZoneID: &zoneID,
		}

		dbRecords, err := dbCluster.GetNetworkZoneRecords(ctx, tx.Tx(), filter)
		if err != nil {
			return err
		}

		// Convert each record to API format.
		for _, dbRecord := range dbRecords {
			apiRecord, err := dbRecord.ToAPI(ctx, tx.Tx())
			if err != nil {
				return err
			}

			records = append(records, *apiRecord)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return records, nil
}

func (d *zone) GetRecord(name string) (*api.NetworkZoneRecord, error) {
	var record *api.NetworkZoneRecord

	err := d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		zoneID := int(d.id)
		filter := dbCluster.NetworkZoneRecordFilter{
			NetworkZoneID: &zoneID,
			Name:          &name,
		}

		dbRecords, err := dbCluster.GetNetworkZoneRecords(ctx, tx.Tx(), filter)
		if err != nil {
			return err
		}

		if len(dbRecords) == 0 {
			return api.StatusErrorf(http.StatusNotFound, "Network zone record not found")
		}

		// Convert to API format.
		apiRecord, err := dbRecords[0].ToAPI(ctx, tx.Tx())
		if err != nil {
			return err
		}

		record = apiRecord
		return nil
	})
	if err != nil {
		return nil, err
	}

	return record, nil
}

func (d *zone) UpdateRecord(name string, req api.NetworkZoneRecordPut, clientType request.ClientType) error {
	s := d.state

	// Validate.
	err := d.validateRecordConfig(req)
	if err != nil {
		return err
	}

	// Validate entries.
	err = d.validateEntries(req)
	if err != nil {
		return err
	}

	err = s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		zoneID := int(d.id)
		filter := dbCluster.NetworkZoneRecordFilter{
			NetworkZoneID: &zoneID,
			Name:          &name,
		}

		// Get the records matching the filter, not using exist because we need record ID and it would be redundant with Get.
		dbRecords, err := dbCluster.GetNetworkZoneRecords(ctx, tx.Tx(), filter)
		if err != nil {
			return err
		}

		if len(dbRecords) == 0 {
			return api.StatusErrorf(http.StatusNotFound, "Network zone record not found")
		}

		// Update the record.
		dbRecord := dbRecords[0]
		dbRecord.Description = req.Description
		dbRecord.Entries = req.Entries

		err = dbCluster.UpdateNetworkZoneRecord(ctx, tx.Tx(), zoneID, name, dbRecord)
		if err != nil {
			return err
		}

		// Update the config.
		err = dbCluster.UpdateNetworkZoneRecordConfig(ctx, tx.Tx(), int64(dbRecord.ID), req.Config)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

func (d *zone) DeleteRecord(name string) error {
	s := d.state

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		zoneID := int(d.id)
		filter := dbCluster.NetworkZoneRecordFilter{
			NetworkZoneID: &zoneID,
			Name:          &name,
		}

		// Get the records matching the filter, not using exist because we need record ID and it would be redundant with Get.
		dbRecords, err := dbCluster.GetNetworkZoneRecords(ctx, tx.Tx(), filter)
		if err != nil {
			return err
		}

		if len(dbRecords) == 0 {
			return api.StatusErrorf(http.StatusNotFound, "Network zone record not found")
		}

		// Delete the record.
		err = dbCluster.DeleteNetworkZoneRecord(ctx, tx.Tx(), int(d.id), dbRecords[0].ID)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

// validateRecordConfig checks the config and rules are valid.
func (d *zone) validateRecordConfig(info api.NetworkZoneRecordPut) error {
	rules := map[string]func(value string) error{}

	err := d.validateConfigMap(info.Config, rules)
	if err != nil {
		return err
	}

	return nil
}

// validateEntries checks the validity of the DNS entries.
func (d *zone) validateEntries(info api.NetworkZoneRecordPut) error {
	uniqueEntries := make([]string, 0, len(info.Entries))

	for _, entry := range info.Entries {
		if entry.TTL == 0 {
			entry.TTL = 300
		}

		_, err := dns.NewRR(fmt.Sprintf("record %d IN %s %s", entry.TTL, entry.Type, entry.Value))
		if err != nil {
			return fmt.Errorf("Bad zone record entry: %w", err)
		}

		entryID := entry.Type + "/" + entry.Value
		if slices.Contains(uniqueEntries, entryID) {
			return fmt.Errorf("Duplicate record for type %q and value %q", entry.Type, entry.Value)
		}

		uniqueEntries = append(uniqueEntries, entryID)
	}

	return nil
}
