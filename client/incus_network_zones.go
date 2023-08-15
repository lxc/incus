package incus

import (
	"fmt"
	"net/url"

	"github.com/lxc/incus/shared/api"
)

// GetNetworkZoneNames returns a list of network zone names.
func (r *ProtocolIncus) GetNetworkZoneNames() ([]string, error) {
	if !r.HasExtension("network_dns") {
		return nil, fmt.Errorf(`The server is missing the required "network_dns" API extension`)
	}

	// Fetch the raw URL values.
	urls := []string{}
	baseURL := "/network-zones"
	_, err := r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetNetworkZones returns a list of Network zone structs.
func (r *ProtocolIncus) GetNetworkZones() ([]api.NetworkZone, error) {
	if !r.HasExtension("network_dns") {
		return nil, fmt.Errorf(`The server is missing the required "network_dns" API extension`)
	}

	zones := []api.NetworkZone{}

	// Fetch the raw value.
	_, err := r.queryStruct("GET", "/network-zones?recursion=1", nil, "", &zones)
	if err != nil {
		return nil, err
	}

	return zones, nil
}

// GetNetworkZone returns a Network zone entry for the provided name.
func (r *ProtocolIncus) GetNetworkZone(name string) (*api.NetworkZone, string, error) {
	if !r.HasExtension("network_dns") {
		return nil, "", fmt.Errorf(`The server is missing the required "network_dns" API extension`)
	}

	zone := api.NetworkZone{}

	// Fetch the raw value.
	etag, err := r.queryStruct("GET", fmt.Sprintf("/network-zones/%s", url.PathEscape(name)), nil, "", &zone)
	if err != nil {
		return nil, "", err
	}

	return &zone, etag, nil
}

// CreateNetworkZone defines a new Network zone using the provided struct.
func (r *ProtocolIncus) CreateNetworkZone(zone api.NetworkZonesPost) error {
	if !r.HasExtension("network_dns") {
		return fmt.Errorf(`The server is missing the required "network_dns" API extension`)
	}

	// Send the request.
	_, _, err := r.query("POST", "/network-zones", zone, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateNetworkZone updates the network zone to match the provided struct.
func (r *ProtocolIncus) UpdateNetworkZone(name string, zone api.NetworkZonePut, ETag string) error {
	if !r.HasExtension("network_dns") {
		return fmt.Errorf(`The server is missing the required "network_dns" API extension`)
	}

	// Send the request.
	_, _, err := r.query("PUT", fmt.Sprintf("/network-zones/%s", url.PathEscape(name)), zone, ETag)
	if err != nil {
		return err
	}

	return nil
}

// DeleteNetworkZone deletes an existing network zone.
func (r *ProtocolIncus) DeleteNetworkZone(name string) error {
	if !r.HasExtension("network_dns") {
		return fmt.Errorf(`The server is missing the required "network_dns" API extension`)
	}

	// Send the request.
	_, _, err := r.query("DELETE", fmt.Sprintf("/network-zones/%s", url.PathEscape(name)), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// GetNetworkZoneRecordNames returns a list of network zone record names.
func (r *ProtocolIncus) GetNetworkZoneRecordNames(zone string) ([]string, error) {
	if !r.HasExtension("network_dns_records") {
		return nil, fmt.Errorf(`The server is missing the required "network_dns_records" API extension`)
	}

	// Fetch the raw URL values.
	urls := []string{}
	baseURL := fmt.Sprintf("/network-zones/%s/records", url.PathEscape(zone))
	_, err := r.queryStruct("GET", baseURL, nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(baseURL, urls...)
}

// GetNetworkZoneRecords returns a list of Network zone record structs.
func (r *ProtocolIncus) GetNetworkZoneRecords(zone string) ([]api.NetworkZoneRecord, error) {
	if !r.HasExtension("network_dns_records") {
		return nil, fmt.Errorf(`The server is missing the required "network_dns_records" API extension`)
	}

	records := []api.NetworkZoneRecord{}

	// Fetch the raw value.
	_, err := r.queryStruct("GET", fmt.Sprintf("/network-zones/%s/records?recursion=1", url.PathEscape(zone)), nil, "", &records)
	if err != nil {
		return nil, err
	}

	return records, nil
}

// GetNetworkZoneRecord returns a Network zone record entry for the provided zone and name.
func (r *ProtocolIncus) GetNetworkZoneRecord(zone string, name string) (*api.NetworkZoneRecord, string, error) {
	if !r.HasExtension("network_dns_records") {
		return nil, "", fmt.Errorf(`The server is missing the required "network_dns_records" API extension`)
	}

	record := api.NetworkZoneRecord{}

	// Fetch the raw value.
	etag, err := r.queryStruct("GET", fmt.Sprintf("/network-zones/%s/records/%s", url.PathEscape(zone), url.PathEscape(name)), nil, "", &record)
	if err != nil {
		return nil, "", err
	}

	return &record, etag, nil
}

// CreateNetworkZoneRecord defines a new Network zone record using the provided struct.
func (r *ProtocolIncus) CreateNetworkZoneRecord(zone string, record api.NetworkZoneRecordsPost) error {
	if !r.HasExtension("network_dns_records") {
		return fmt.Errorf(`The server is missing the required "network_dns_records" API extension`)
	}

	// Send the request.
	_, _, err := r.query("POST", fmt.Sprintf("/network-zones/%s/records", url.PathEscape(zone)), record, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateNetworkZoneRecord updates the network zone record to match the provided struct.
func (r *ProtocolIncus) UpdateNetworkZoneRecord(zone string, name string, record api.NetworkZoneRecordPut, ETag string) error {
	if !r.HasExtension("network_dns_records") {
		return fmt.Errorf(`The server is missing the required "network_dns_records" API extension`)
	}

	// Send the request.
	_, _, err := r.query("PUT", fmt.Sprintf("/network-zones/%s/records/%s", url.PathEscape(zone), url.PathEscape(name)), record, ETag)
	if err != nil {
		return err
	}

	return nil
}

// DeleteNetworkZoneRecord deletes an existing network zone record.
func (r *ProtocolIncus) DeleteNetworkZoneRecord(zone string, name string) error {
	if !r.HasExtension("network_dns_records") {
		return fmt.Errorf(`The server is missing the required "network_dns_records" API extension`)
	}

	// Send the request.
	_, _, err := r.query("DELETE", fmt.Sprintf("/network-zones/%s/records/%s", url.PathEscape(zone), url.PathEscape(name)), nil, "")
	if err != nil {
		return err
	}

	return nil
}
