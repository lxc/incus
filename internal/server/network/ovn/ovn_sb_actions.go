package ovn

import (
	"context"
	"fmt"

	ovnSB "github.com/lxc/incus/v6/internal/server/network/ovn/schema/ovn-sb"
)

// GetLogicalRouterPortActiveChassisHostname gets the hostname of the chassis managing the logical router port.
func (o *SB) GetLogicalRouterPortActiveChassisHostname(ctx context.Context, ovnRouterPort OVNRouterPort) (string, error) {
	// Look for the port binding.
	pb := &ovnSB.PortBinding{
		LogicalPort: fmt.Sprintf("cr-%s", ovnRouterPort),
	}

	err := o.client.Get(ctx, pb)
	if err != nil {
		return "", err
	}

	if pb.Chassis == nil {
		return "", fmt.Errorf("No chassis found")
	}

	// Get the associated chassis.
	chassis := &ovnSB.Chassis{
		UUID: *pb.Chassis,
	}

	err = o.client.Get(ctx, chassis)
	if err != nil {
		return "", err
	}

	return chassis.Hostname, nil
}

// GetServiceHealth returns the current health record for a particular server and port.
func (o *SB) GetServiceHealth(ctx context.Context, address string, protocol string, port int) (string, error) {
	services := []ovnSB.ServiceMonitor{}

	err := o.client.WhereCache(func(srv *ovnSB.ServiceMonitor) bool {
		return srv.Protocol != nil && *srv.Protocol == protocol && srv.IP == address && srv.Port == port && srv.Status != nil
	}).List(ctx, &services)
	if err != nil {
		return "", err
	}

	if len(services) != 1 {
		return "unknown", nil
	}

	return *services[0].Status, nil
}
