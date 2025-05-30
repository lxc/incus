//go:build linux && cgo && !agent

package cluster

import (
	"context"
	"database/sql"

	"github.com/lxc/incus/v6/shared/api"
)

// Code generation directives.
//
//generate-database:mapper target networks_load_balancers.mapper.go
//generate-database:mapper reset -i -b "//go:build linux && cgo && !agent"
//
//generate-database:mapper stmt -e network_load_balancer objects table=networks_load_balancers
//generate-database:mapper stmt -e network_load_balancer objects-by-NetworkID table=networks_load_balancers
//generate-database:mapper stmt -e network_load_balancer objects-by-NetworkID-and-ListenAddress table=networks_load_balancers
//generate-database:mapper stmt -e network_load_balancer id table=networks_load_balancers
//generate-database:mapper stmt -e network_load_balancer create table=networks_load_balancers
//generate-database:mapper stmt -e network_load_balancer update table=networks_load_balancers
//generate-database:mapper stmt -e network_load_balancer delete-by-NetworkID-and-ID table=networks_load_balancers
//
//generate-database:mapper method -i -e network_load_balancer GetMany references=Config table=networks_load_balancers
//generate-database:mapper method -i -e network_load_balancer GetOne table=networks_load_balancers
//generate-database:mapper method -i -e network_load_balancer ID table=networks_load_balancers
//generate-database:mapper method -i -e network_load_balancer Create references=Config table=networks_load_balancers
//generate-database:mapper method -i -e network_load_balancer Update references=Config table=networks_load_balancers
//generate-database:mapper method -i -e network_load_balancer DeleteOne-by-NetworkID-and-ID table=networks_load_balancers

// NetworkLoadBalancer is the generated entity backing the networks_load_balancers table.
type NetworkLoadBalancer struct {
	ID            int64
	NetworkID     int64  `db:"primary=yes&column=network_id"`
	ListenAddress string `db:"primary=yes"`
	Description   string
	Backends      []api.NetworkLoadBalancerBackend `db:"marshal=json"`
	Ports         []api.NetworkLoadBalancerPort    `db:"marshal=json"`
}

// NetworkLoadBalancerFilter defines the optional WHERE-clause fields.
type NetworkLoadBalancerFilter struct {
	ID            *int64
	NetworkID     *int64
	ListenAddress *string
}

// ToAPI converts the DB record into the external API type.
func (n *NetworkLoadBalancer) ToAPI(ctx context.Context, tx *sql.Tx) (*api.NetworkLoadBalancer, error) {
	// Get the config.
	cfg, err := GetNetworkLoadBalancerConfig(ctx, tx, int(n.ID))
	if err != nil {
		return nil, err
	}

	out := api.NetworkLoadBalancer{
		NetworkLoadBalancerPut: api.NetworkLoadBalancerPut{
			Description: n.Description,
			Config:      cfg,
			Backends:    n.Backends,
			Ports:       n.Ports,
		},

		ListenAddress: n.ListenAddress,
	}

	return &out, nil
}
