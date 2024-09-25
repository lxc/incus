package incus

import (
	"github.com/lxc/incus/v6/shared/api"
)

// GetNetworkLoadBalancerAddresses returns a list of network load balancer listen addresses.
func (r *ProtocolIncus) GetNetworkLoadBalancerAddresses(networkName string) ([]string, error) {
	err := r.CheckExtension("network_load_balancer")
	if err != nil {
		return nil, err
	}

	// Fetch the raw URL values.
	urls := []string{}
	u := api.NewURL().Path("networks", networkName, "load-balancers")
	_, err = r.queryStruct("GET", u.String(), nil, "", &urls)
	if err != nil {
		return nil, err
	}

	// Parse it.
	return urlsToResourceNames(u.String(), urls...)
}

// GetNetworkLoadBalancers returns a list of Network load balancer structs.
func (r *ProtocolIncus) GetNetworkLoadBalancers(networkName string) ([]api.NetworkLoadBalancer, error) {
	err := r.CheckExtension("network_load_balancer")
	if err != nil {
		return nil, err
	}

	loadBalancers := []api.NetworkLoadBalancer{}

	// Fetch the raw value.
	u := api.NewURL().Path("networks", networkName, "load-balancers").WithQuery("recursion", "1")
	_, err = r.queryStruct("GET", u.String(), nil, "", &loadBalancers)
	if err != nil {
		return nil, err
	}

	return loadBalancers, nil
}

// GetNetworkLoadBalancer returns a Network load balancer entry for the provided network and listen address.
func (r *ProtocolIncus) GetNetworkLoadBalancer(networkName string, listenAddress string) (*api.NetworkLoadBalancer, string, error) {
	err := r.CheckExtension("network_load_balancer")
	if err != nil {
		return nil, "", err
	}

	loadBalancer := api.NetworkLoadBalancer{}

	// Fetch the raw value.
	u := api.NewURL().Path("networks", networkName, "load-balancers", listenAddress)
	etag, err := r.queryStruct("GET", u.String(), nil, "", &loadBalancer)
	if err != nil {
		return nil, "", err
	}

	return &loadBalancer, etag, nil
}

// CreateNetworkLoadBalancer defines a new network load balancer using the provided struct.
func (r *ProtocolIncus) CreateNetworkLoadBalancer(networkName string, loadBalancer api.NetworkLoadBalancersPost) error {
	err := r.CheckExtension("network_load_balancer")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("networks", networkName, "load-balancers")
	_, _, err = r.query("POST", u.String(), loadBalancer, "")
	if err != nil {
		return err
	}

	return nil
}

// UpdateNetworkLoadBalancer updates the network load balancer to match the provided struct.
func (r *ProtocolIncus) UpdateNetworkLoadBalancer(networkName string, listenAddress string, loadBalancer api.NetworkLoadBalancerPut, ETag string) error {
	err := r.CheckExtension("network_load_balancer")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("networks", networkName, "load-balancers", listenAddress)
	_, _, err = r.query("PUT", u.String(), loadBalancer, ETag)
	if err != nil {
		return err
	}

	return nil
}

// DeleteNetworkLoadBalancer deletes an existing network load balancer.
func (r *ProtocolIncus) DeleteNetworkLoadBalancer(networkName string, listenAddress string) error {
	err := r.CheckExtension("network_load_balancer")
	if err != nil {
		return err
	}

	// Send the request.
	u := api.NewURL().Path("networks", networkName, "load-balancers", listenAddress)
	_, _, err = r.query("DELETE", u.String(), nil, "")
	if err != nil {
		return err
	}

	return nil
}

// GetNetworkLoadBalancerState returns a Network load balancer state for the provided network and listen address.
func (r *ProtocolIncus) GetNetworkLoadBalancerState(networkName string, listenAddress string) (*api.NetworkLoadBalancerState, error) {
	err := r.CheckExtension("network_load_balancer_state")
	if err != nil {
		return nil, err
	}

	lbState := api.NetworkLoadBalancerState{}

	// Fetch the raw value.
	u := api.NewURL().Path("networks", networkName, "load-balancers", listenAddress, "state")
	_, err = r.queryStruct("GET", u.String(), nil, "", &lbState)
	if err != nil {
		return nil, err
	}

	return &lbState, nil
}
