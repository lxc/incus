//go:build linux && cgo && !agent

package cluster

// Code generation directives.
// generate-database:mapper target networks_load_balancers.mapper.go
// generate-database:mapper reset -i -b "//go:build linux && cgo && !agent"
//
// generate the CRUD statements for our entity “network_load_balancer”
// generate-database:mapper stmt -e network_load_balancer objects
// generate-database:mapper stmt -e network_load_balancer objects-by-NetworkID
// generate-database:mapper stmt -e network_load_balancer objects-by-NetworkID-and-ListenAddress
// generate-database:mapper stmt -e network_load_balancer id
// generate-database:mapper stmt -e network_load_balancer create
// generate-database:mapper stmt -e network_load_balancer update
// generate-database:mapper stmt -e network_load_balancer delete-by-NetworkID-and-ID
//
// generate methods on ClusterTx
// generate-database:mapper method -i -e network_load_balancer GetMany references=Config
// generate-database:mapper method -i -e network_load_balancer GetOne
// generate-database:mapper method -i -e network_load_balancer ID
// generate-database:mapper method -i -e network_load_balancer Create references=Config
// generate-database:mapper method -i -e network_load_balancer Update references=Config
// generate-database:mapper method -i -e network_load_balancer DeleteOne-by-NetworkID-and-ID
//
// NetworkLoadBalancer is the generated entity backing the networks_load_balancers table.
type NetworkLoadBalancer struct {
	ID            int
	NetworkID     int    `db:"primary=yes&column=network_id"`
	NodeID        *int   `db:"column=node_id&join=nodes.id"`
	ListenAddress string `db:"primary=yes"`
	Description   string
	Backends      string
	Ports         string
}

// NetworkLoadBalancerFilter defines the optional WHERE-clause fields.
type NetworkLoadBalancerFilter struct {
	ID            *int
	NetworkID     *int
	ListenAddress *string
	NodeID        *int
}
