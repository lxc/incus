//go:build linux && cgo && !agent

package cluster

// Code generation directives.
//generate-database:mapper target device_nic_ovn_qos.mapper.go
//generate-database:mapper reset -i -b "//go:build linux && cgo && !agent"
//
// Statements:
//generate-database:mapper stmt -e OVNQoS objects table=device_nic_ovn_qos
//generate-database:mapper stmt -e OVNQoS objects-by-ID table=device_nic_ovn_qos
//generate-database:mapper stmt -e OVNQoS objects-by-UUID table=device_nic_ovn_qos
//generate-database:mapper stmt -e OVNQoS objects-by-LogicalSwitch table=device_nic_ovn_qos
//generate-database:mapper stmt -e OVNQoS id table=device_nic_ovn_qos
//generate-database:mapper stmt -e OVNQoS create table=device_nic_ovn_qos
//generate-database:mapper stmt -e OVNQoS update table=device_nic_ovn_qos
//generate-database:mapper stmt -e OVNQoS delete-by-UUID table=device_nic_ovn_qos
//generate-database:mapper stmt -e OVNQoS delete-by-LogicalSwitch table=device_nic_ovn_qos
//
// Methods:
//generate-database:mapper method -i -e OVNQoS GetMany table=device_nic_ovn_qos
//generate-database:mapper method -i -e OVNQoS GetOne table=device_nic_ovn_qos
//generate-database:mapper method -i -e OVNQoS Exists table=device_nic_ovn_qos
//generate-database:mapper method -i -e OVNQoS Create table=device_nic_ovn_qos
//generate-database:mapper method -i -e OVNQoS ID table=device_nic_ovn_qos
//generate-database:mapper method -i -e OVNQoS Update table=device_nic_ovn_qos
//generate-database:mapper method -i -e OVNQoS DeleteOne-by-UUID table=device_nic_ovn_qos
//generate-database:mapper method -i -e OVNQoS DeleteMany-by-LogicalSwitch table=device_nic_ovn_qos

type OVNQoS struct {
	ID            int    `db:"order=yes"`
	UUID          string `db:"primary=yes"`
	Action        string
	Bandwidth     int
	Direction     string
	LogicalSwitch string
	Match         string
	Priority      int
}

type OVNQoSFilter struct {
	ID            *int
	UUID          *string
	LogicalSwitch *string
}
