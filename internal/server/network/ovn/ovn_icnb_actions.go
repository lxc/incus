package ovn

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"net/netip"
	"slices"
	"strings"

	ovsdbClient "github.com/ovn-org/libovsdb/client"
	"github.com/ovn-org/libovsdb/ovsdb"

	ovnICNB "github.com/lxc/incus/v6/internal/server/network/ovn/schema/ovn-ic-nb"
)

// CreateTransitSwitch creates a new managed transit switch.
func (o *ICNB) CreateTransitSwitch(ctx context.Context, name string, mayExist bool) error {
	// Look for an existing transit switch.
	transitSwitch := ovnICNB.TransitSwitch{
		Name: name,
	}

	err := o.client.Get(ctx, &transitSwitch)
	if err != nil && !errors.Is(err, ovsdbClient.ErrNotFound) {
		return err
	}

	// Handle existing switches.
	if transitSwitch.UUID != "" {
		if !mayExist {
			return ErrExists
		}

		return nil
	}

	// Generate a random IPv4 subnet (/28).
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, rand.Uint32())
	buf[0] = 169
	buf[1] = 254
	ipv4 := net.IP(buf)
	ipv4Net := net.IPNet{IP: ipv4.Mask(net.CIDRMask(28, 32)), Mask: net.CIDRMask(28, 32)}

	// Mark new switches as managed by Incus.
	transitSwitch.ExternalIDs = map[string]string{
		"incus-managed":     "true",
		"incus-subnet-ipv4": ipv4Net.String(),
		"incus-subnet-ipv6": fmt.Sprintf("fd42:%x:%x:%x::/64", rand.Intn(65535), rand.Intn(65535), rand.Intn(65535)),
	}

	// Create the switch.
	ops, err := o.client.Create(&transitSwitch)
	if err != nil {
		return err
	}

	resp, err := o.client.Transact(ctx, ops...)
	if err != nil {
		return err
	}

	_, err = ovsdb.CheckOperationResults(resp, ops)
	if err != nil {
		return err
	}

	return nil
}

// CreateTransitSwitchAllocation creates a new allocation on the switch.
func (o *ICNB) CreateTransitSwitchAllocation(ctx context.Context, switchName string, azName string) (*net.IPNet, *net.IPNet, error) {
	// Get the switch.
	transitSwitch := ovnICNB.TransitSwitch{
		Name: switchName,
	}

	err := o.client.Get(ctx, &transitSwitch)
	if err != nil {
		return nil, nil, err
	}

	// Check that it's managed by us.
	if transitSwitch.ExternalIDs == nil || transitSwitch.ExternalIDs["incus-managed"] != "true" {
		return nil, nil, errors.New("Transit switch isn't Incus managed")
	}

	// Check that prefixes are set.
	if transitSwitch.ExternalIDs["incus-subnet-ipv4"] == "" || transitSwitch.ExternalIDs["incus-subnet-ipv6"] == "" {
		return nil, nil, errors.New("No configured subnets on the transit switch")
	}

	// Get the allocated addresses.
	v4Addresses := []string{}
	v6Addresses := []string{}
	for k, v := range transitSwitch.ExternalIDs {
		if !strings.HasPrefix(k, "incus-allocation-") {
			continue
		}

		fields := strings.Split(v, ",")
		if len(fields) != 2 {
			continue
		}

		v4Addresses = append(v4Addresses, fields[0])
		v6Addresses = append(v6Addresses, fields[1])
	}

	// Get the prefixes.
	v4Prefix, err := netip.ParsePrefix(transitSwitch.ExternalIDs["incus-subnet-ipv4"])
	if err != nil {
		return nil, nil, err
	}

	v6Prefix, err := netip.ParsePrefix(transitSwitch.ExternalIDs["incus-subnet-ipv6"])
	if err != nil {
		return nil, nil, err
	}

	// Allocate new IPs in the subnet.
	v4Addr := v4Prefix.Addr()
	for {
		v4Addr = v4Addr.Next()
		if !v4Prefix.Contains(v4Addr) {
			return nil, nil, errors.New("Transit switch is out of IPv4 addresses")
		}

		if !slices.Contains(v4Addresses, v4Addr.String()) {
			break
		}
	}

	v6Addr := v6Prefix.Addr()
	for {
		v6Addr = v6Addr.Next()
		if !v6Prefix.Contains(v6Addr) {
			return nil, nil, errors.New("Transit switch is out of IPv6 addresses")
		}

		if !slices.Contains(v6Addresses, v6Addr.String()) {
			break
		}
	}

	// Update the record.
	transitSwitch.ExternalIDs[fmt.Sprintf("incus-allocation-%s", azName)] = fmt.Sprintf("%s,%s", v4Addr.String(), v6Addr.String())

	ops, err := o.client.Where(&transitSwitch).Update(&transitSwitch)
	if err != nil {
		return nil, nil, err
	}

	resp, err := o.client.Transact(ctx, ops...)
	if err != nil {
		return nil, nil, err
	}

	_, err = ovsdb.CheckOperationResults(resp, ops)
	if err != nil {
		return nil, nil, err
	}

	return &net.IPNet{IP: net.IP(v4Addr.AsSlice()), Mask: net.CIDRMask(v4Prefix.Bits(), 32)}, &net.IPNet{IP: net.IP(v6Addr.AsSlice()), Mask: net.CIDRMask(v6Prefix.Bits(), 128)}, nil
}

// DeleteTransitSwitchAllocation removes a current allocation from the switch.
func (o *ICNB) DeleteTransitSwitchAllocation(ctx context.Context, switchName string, azName string) error {
	// Get the switch.
	transitSwitch := ovnICNB.TransitSwitch{
		Name: switchName,
	}

	err := o.client.Get(ctx, &transitSwitch)
	if err != nil {
		return err
	}

	// Check that it's managed by us.
	if transitSwitch.ExternalIDs == nil || transitSwitch.ExternalIDs["incus-managed"] != "true" {
		return errors.New("Transit switch isn't Incus managed")
	}

	// Update the record.
	delete(transitSwitch.ExternalIDs, fmt.Sprintf("incus-allocation-%s", azName))

	ops, err := o.client.Where(&transitSwitch).Update(&transitSwitch)
	if err != nil {
		return err
	}

	resp, err := o.client.Transact(ctx, ops...)
	if err != nil {
		return err
	}

	_, err = ovsdb.CheckOperationResults(resp, ops)
	if err != nil {
		return err
	}

	return nil
}

// DeleteTransitSwitch deletes an existing transit switch.
// The force parameter is required to delete a transit switch which wasn't created by Incus.
func (o *ICNB) DeleteTransitSwitch(ctx context.Context, name string, force bool) error {
	// Get the current transit switch.
	transitSwitch := ovnICNB.TransitSwitch{
		Name: name,
	}

	err := o.client.Get(ctx, &transitSwitch)
	if err != nil {
		// Already deleted.
		if errors.Is(err, ErrNotFound) {
			return nil
		}

		return err
	}

	// Check if the switch is incus-managed.
	if !force && transitSwitch.ExternalIDs["incus-managed"] != "true" {
		return ErrNotManaged
	}

	// Delete the switch.
	deleteOps, err := o.client.Where(&transitSwitch).Delete()
	if err != nil {
		return err
	}

	resp, err := o.client.Transact(ctx, deleteOps...)
	if err != nil {
		return err
	}

	_, err = ovsdb.CheckOperationResults(resp, deleteOps)
	if err != nil {
		return err
	}

	return nil
}
