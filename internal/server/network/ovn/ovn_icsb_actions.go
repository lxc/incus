package ovn

import (
	"context"

	ovnICSB "github.com/lxc/incus/v6/internal/server/network/ovn/schema/ovn-ic-sb"
)

// GetGateways returns a slice of gateways for the specified availability zone.
func (o *ICSB) GetGateways(ctx context.Context, name string) ([]string, error) {
	// Get the availability zone.
	availabilityZone := ovnICSB.AvailabilityZone{
		Name: name,
	}

	err := o.client.Get(ctx, &availabilityZone)
	if err != nil {
		return nil, err
	}

	// Get the gateways in the availability zone.
	gateways := []ovnICSB.Gateway{}
	gateway := ovnICSB.Gateway{
		AvailabilityZone: availabilityZone.UUID,
	}

	err = o.client.Where(&gateway).List(ctx, &gateways)
	if err != nil {
		return nil, err
	}

	// Extract the names.
	names := make([]string, len(gateways))
	for _, entry := range gateways {
		names = append(names, entry.Name)
	}

	return names, nil
}
