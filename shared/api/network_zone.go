package api

// NetworkZonesPost represents the fields of a new network zone
//
// swagger:model
//
// API extension: network_dns.
type NetworkZonesPost struct {
	NetworkZonePut `yaml:",inline"`

	// The name of the zone (DNS domain name)
	// Example: example.net
	Name string `json:"name" yaml:"name"`
}

// NetworkZonePut represents the modifiable fields of a network zone
//
// swagger:model
//
// API extension: network_dns.
type NetworkZonePut struct {
	// Description of the network zone
	// Example: Internal domain
	Description string `json:"description" yaml:"description"`

	// Zone configuration map (refer to doc/network-zones.md)
	// Example: {"user.mykey": "foo"}
	Config map[string]string `json:"config" yaml:"config"`
}

// NetworkZone represents a network zone (DNS).
//
// swagger:model
//
// API extension: network_dns.
type NetworkZone struct {
	NetworkZonePut `yaml:",inline"`

	// The name of the zone (DNS domain name)
	// Example: example.net
	Name string `json:"name" yaml:"name"`

	// List of URLs of objects using this network zone
	// Read only: true
	// Example: ["/1.0/networks/foo", "/1.0/networks/bar"]
	UsedBy []string `json:"used_by" yaml:"used_by"` // Resources that use the zone.

	// Project name
	// Example: project1
	//
	// API extension: network_zones_all_projects
	Project string `json:"project" yaml:"project"`
}

// Writable converts a full NetworkZone struct into a NetworkZonePut struct (filters read-only fields).
func (f *NetworkZone) Writable() NetworkZonePut {
	return f.NetworkZonePut
}

// NetworkZoneRecordsPost represents the fields of a new network zone record
//
// swagger:model
//
// API extension: network_dns_records.
type NetworkZoneRecordsPost struct {
	NetworkZoneRecordPut `yaml:",inline"`

	// The record name in the zone
	// Example: @
	Name string `json:"name" yaml:"name"`
}

// NetworkZoneRecordPut represents the modifiable fields of a network zone record
//
// swagger:model
//
// API extension: network_dns_records.
type NetworkZoneRecordPut struct {
	// Description of the record
	// Example: SPF record
	Description string `json:"description" yaml:"description"`

	// Entries in the record
	Entries []NetworkZoneRecordEntry `json:"entries" yaml:"entries"`

	// Advanced configuration for the record
	// Example: {"user.mykey": "foo"}
	Config map[string]string `json:"config" yaml:"config"`
}

// NetworkZoneRecordEntry represents the fields in a record entry
//
// swagger:model
//
// API extension: network_dns_records.
type NetworkZoneRecordEntry struct {
	// Type of DNS entry
	// Example: TXT
	Type string `json:"type" yaml:"type"`

	// TTL for the entry
	// Example: 3600
	TTL uint64 `json:"ttl,omitempty" yaml:"ttl,omitempty"`

	// Value for the record
	// Example: v=spf1 mx ~all
	Value string `json:"value" yaml:"value"`
}

// NetworkZoneRecord represents a network zone (DNS) record.
//
// swagger:model
//
// API extension: network_dns_records.
type NetworkZoneRecord struct {
	NetworkZoneRecordPut `yaml:",inline"`

	// The name of the record
	// Example: @
	Name string `json:"name" yaml:"name"`
}

// Writable converts a full NetworkZoneRecord struct into a NetworkZoneRecordPut struct (filters read-only fields).
func (f *NetworkZoneRecord) Writable() NetworkZoneRecordPut {
	return f.NetworkZoneRecordPut
}
