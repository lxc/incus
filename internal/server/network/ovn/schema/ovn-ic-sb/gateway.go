// Code generated by "libovsdb.modelgen"
// DO NOT EDIT.

package ovsmodel

const GatewayTable = "Gateway"

// Gateway defines an object in Gateway table
type Gateway struct {
	UUID             string            `ovsdb:"_uuid"`
	AvailabilityZone string            `ovsdb:"availability_zone"`
	Encaps           []string          `ovsdb:"encaps"`
	ExternalIDs      map[string]string `ovsdb:"external_ids"`
	Hostname         string            `ovsdb:"hostname"`
	Name             string            `ovsdb:"name"`
}
