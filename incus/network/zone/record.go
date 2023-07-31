package zone

import (
	"fmt"

	"github.com/miekg/dns"

	"github.com/canonical/lxd/lxd/cluster/request"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
)

func (d *zone) AddRecord(req api.NetworkZoneRecordsPost) error {
	// Validate.
	err := d.validateRecordConfig(req.NetworkZoneRecordPut)
	if err != nil {
		return err
	}

	// Validate entries.
	err = d.validateEntries(req.NetworkZoneRecordPut)
	if err != nil {
		return err
	}

	// Add the new record.
	_, err = d.state.DB.Cluster.CreateNetworkZoneRecord(d.id, req)
	if err != nil {
		return err
	}

	return nil
}

func (d *zone) GetRecords() ([]api.NetworkZoneRecord, error) {
	// Get the record names.
	names, err := d.state.DB.Cluster.GetNetworkZoneRecordNames(d.id)
	if err != nil {
		return nil, err
	}

	// Load all the records.
	records := []api.NetworkZoneRecord{}
	for _, name := range names {
		_, record, err := d.state.DB.Cluster.GetNetworkZoneRecord(d.id, name)
		if err != nil {
			return nil, err
		}

		records = append(records, *record)
	}

	return records, nil
}

func (d *zone) GetRecord(name string) (*api.NetworkZoneRecord, error) {
	// Get the record.
	_, record, err := d.state.DB.Cluster.GetNetworkZoneRecord(d.id, name)
	if err != nil {
		return nil, err
	}

	return record, nil
}

func (d *zone) UpdateRecord(name string, req api.NetworkZoneRecordPut, clientType request.ClientType) error {
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

	// Get the record.
	id, _, err := d.state.DB.Cluster.GetNetworkZoneRecord(d.id, name)
	if err != nil {
		return err
	}

	// Update the record.
	err = d.state.DB.Cluster.UpdateNetworkZoneRecord(id, req)
	if err != nil {
		return err
	}

	return nil
}

func (d *zone) DeleteRecord(name string) error {
	// Get the record.
	id, _, err := d.state.DB.Cluster.GetNetworkZoneRecord(d.id, name)
	if err != nil {
		return err
	}

	// Delete the record.
	err = d.state.DB.Cluster.DeleteNetworkZoneRecord(id)
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
		if shared.StringInSlice(entryID, uniqueEntries) {
			return fmt.Errorf("Duplicate record for type %q and value %q", entry.Type, entry.Value)
		}

		uniqueEntries = append(uniqueEntries, entryID)
	}

	return nil
}
