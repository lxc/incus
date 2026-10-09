package ovn

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/ovn-kubernetes/libovsdb/ovsdb"

	ovnNB "github.com/lxc/incus/v7/internal/server/network/ovn/schema/ovn-nb"
	ovnSB "github.com/lxc/incus/v7/internal/server/network/ovn/schema/ovn-sb"
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
		return "", errors.New("No chassis found")
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

// DeleteMACBindings invalidates only the captured versions of dynamic MAC bindings on the logical port.
func (o *SB) DeleteMACBindings(ctx context.Context, portName OVNRouterPort, ips ...net.IP) error {
	if len(ips) == 0 {
		return nil
	}

	if o.backendID == "" {
		return errors.New("OVN southbound database identity is unavailable")
	}

	zero := 0
	root := ovsdb.UUID{GoUUID: o.backendID}
	guard := ovsdb.Operation{
		Op: ovsdb.OperationWait, Table: "SB_Global", Timeout: &zero,
		Where:   []ovsdb.Condition{{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: root}},
		Columns: []string{"_uuid"}, Until: "==", Rows: []ovsdb.Row{{"_uuid": root}},
	}

	reads := []ovsdb.Operation{guard}
	for _, ip := range ips {
		reads = append(reads, ovsdb.Operation{
			Op: ovsdb.OperationSelect, Table: "MAC_Binding",
			Where: []ovsdb.Condition{
				{Column: "logical_port", Function: ovsdb.ConditionEqual, Value: string(portName)},
				{Column: "ip", Function: ovsdb.ConditionEqual, Value: ip.String()},
			},
			Columns: []string{"_uuid", "_version", "mac"},
		})
	}

	reply, err := o.client.Transact(ctx, reads...)
	if err != nil {
		return err
	}

	_, err = ovsdb.CheckOperationResults(reply, reads)
	if err != nil {
		return err
	}

	if len(reply) != len(reads) {
		return errors.New("Unexpected OVN MAC binding snapshot result count")
	}

	operations := []ovsdb.Operation{guard}
	if o.owner != "" {
		// This comment is diagnostic provenance, not an authorization or generation prerequisite.
		comment := "incus:ovn-lifecycle:" + o.owner
		operations = append(operations, ovsdb.Operation{Op: ovsdb.OperationComment, Comment: &comment})
	}

	deletes := 0
	for i, result := range reply[1:] {
		for _, row := range result.Rows {
			id, validID := row["_uuid"].(ovsdb.UUID)
			version, validVersion := row["_version"].(ovsdb.UUID)
			_, idErr := uuid.Parse(id.GoUUID)
			_, versionErr := uuid.Parse(version.GoUUID)
			if !validID || !validVersion || idErr != nil || versionErr != nil {
				return errors.New("Invalid OVN MAC binding row identity or version")
			}

			// A delayed delete may invalidate an unchanged old cache entry, but cannot remove a
			// replacement row or a relearn that changed its MAC. Row versions are server-local
			// and this write runs on the cluster leader, so the content is guarded instead.
			operations = append(operations, ovsdb.Operation{Op: ovsdb.OperationDelete, Table: "MAC_Binding", Where: []ovsdb.Condition{
				{Column: "_uuid", Function: ovsdb.ConditionEqual, Value: id},
				{Column: "mac", Function: ovsdb.ConditionEqual, Value: row["mac"]},
				{Column: "logical_port", Function: ovsdb.ConditionEqual, Value: string(portName)},
				{Column: "ip", Function: ovsdb.ConditionEqual, Value: ips[i].String()},
			}})
			deletes++
		}
	}

	if deletes == 0 {
		return nil
	}

	operations = append(operations, guard)
	reply, err = o.client.Transact(ctx, operations...)
	if err != nil {
		return err
	}

	_, err = ovsdb.CheckOperationResults(reply, operations)
	if err != nil {
		return err
	}

	if len(reply) != len(operations) {
		return errors.New("Unexpected OVN MAC binding delete result count")
	}

	return nil
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

// CheckLoadBalancerOnline checks all backends for a particular load-balancer.
func (o *SB) CheckLoadBalancerOnline(ctx context.Context, lb ovnNB.LoadBalancer) (bool, error) {
	// Invalid load balancers should be kept offline.
	if lb.Protocol == nil {
		return false, nil
	}

	// Load-balancers with no service checks should be kept online.
	if len(lb.HealthCheck) == 0 {
		return true, nil
	}

	for _, v := range lb.Vips {
		for backend := range strings.SplitSeq(v, ",") {
			host, port, err := net.SplitHostPort(backend)
			if err != nil {
				return false, err
			}

			portInt, err := strconv.Atoi(port)
			if err != nil {
				return false, err
			}

			status, err := o.GetServiceHealth(ctx, host, *lb.Protocol, portInt)
			if err != nil {
				return false, err
			}

			if status == "online" {
				return true, nil
			}
		}
	}

	return false, nil
}
