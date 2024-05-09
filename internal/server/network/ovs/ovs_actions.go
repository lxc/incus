package ovs

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	ovsdbClient "github.com/ovn-org/libovsdb/client"
	ovsdbModel "github.com/ovn-org/libovsdb/model"
	"github.com/ovn-org/libovsdb/ovsdb"

	"github.com/lxc/incus/v6/internal/server/ip"
	ovsSwitch "github.com/lxc/incus/v6/internal/server/network/ovs/schema/ovs"
	"github.com/lxc/incus/v6/shared/util"
)

// ovnBridgeMappingMutex locks access to read/write external-ids:ovn-bridge-mappings.
var ovnBridgeMappingMutex sync.Mutex

// Installed returns true if the OVS tools are installed.
func (o *VSwitch) Installed() bool {
	return util.PathExists("/run/openvswitch/db.sock")
}

// GetBridge returns a bridge entry.
func (o *VSwitch) GetBridge(ctx context.Context, bridgeName string) (*ovsSwitch.Bridge, error) {
	bridge := &ovsSwitch.Bridge{Name: bridgeName}

	err := o.client.Get(ctx, bridge)
	if err != nil {
		return nil, err
	}

	return bridge, nil
}

// CreateBridge adds a new bridge.
func (o *VSwitch) CreateBridge(ctx context.Context, bridgeName string, mayExist bool, hwaddr net.HardwareAddr, mtu uint32) error {
	// Create interface.
	iface := ovsSwitch.Interface{
		UUID: "interface",
		Name: bridgeName,
	}

	if mtu > 0 {
		mtu := int(mtu)
		iface.MTURequest = &mtu
	}

	interfaceOps, err := o.client.Create(&iface)
	if err != nil {
		return err
	}

	// Create port.
	port := ovsSwitch.Port{
		UUID:       "port",
		Name:       bridgeName,
		Interfaces: []string{iface.UUID},
	}

	portOps, err := o.client.Create(&port)
	if err != nil {
		return err
	}

	// Create bridge.
	bridge := ovsSwitch.Bridge{
		UUID:  "bridge",
		Name:  bridgeName,
		Ports: []string{port.UUID},
	}

	if hwaddr != nil {
		bridge.OtherConfig = map[string]string{"hwaddr": hwaddr.String()}
	}

	bridgeOps, err := o.client.Create(&bridge)
	if err != nil {
		return err
	}

	if mayExist {
		err = o.client.Get(ctx, &bridge)
		if err != nil && err != ovsdbClient.ErrNotFound {
			return err
		}

		if bridge.UUID != "bridge" {
			// Bridge already exists.
			return nil
		}
	}

	// Create switch entry.
	ovsRow := ovsSwitch.OpenvSwitch{
		UUID: o.rootUUID,
	}

	mutateOps, err := o.client.Where(&ovsRow).Mutate(&ovsRow, ovsdbModel.Mutation{
		Field:   &ovsRow.Bridges,
		Mutator: ovsdb.MutateOperationInsert,
		Value:   []string{bridge.UUID},
	})
	if err != nil {
		return err
	}

	operations := append(interfaceOps, portOps...)
	operations = append(operations, bridgeOps...)
	operations = append(operations, mutateOps...)

	resp, err := o.client.Transact(ctx, operations...)
	if err != nil {
		return err
	}

	_, err = ovsdb.CheckOperationResults(resp, operations)
	if err != nil {
		return err
	}

	// Wait for kernel interface to appear.
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)

		if util.PathExists(fmt.Sprintf("/sys/class/net/%s", bridgeName)) {
			return nil
		}
	}

	return fmt.Errorf("Bridge interface failed to appear")
}

// DeleteBridge deletes a bridge.
func (o *VSwitch) DeleteBridge(ctx context.Context, bridgeName string) error {
	bridge := ovsSwitch.Bridge{
		Name: bridgeName,
	}

	err := o.client.Get(ctx, &bridge)
	if err != nil {
		return err
	}

	ovsRow := ovsSwitch.OpenvSwitch{
		UUID: o.rootUUID,
	}

	operations, err := o.client.Where(&ovsRow).Mutate(&ovsRow, ovsdbModel.Mutation{
		Field:   &ovsRow.Bridges,
		Mutator: "delete",
		Value:   []string{bridge.UUID},
	})
	if err != nil {
		return err
	}

	resp, err := o.client.Transact(ctx, operations...)
	if err != nil {
		return err
	}

	_, err = ovsdb.CheckOperationResults(resp, operations)
	if err != nil {
		return err
	}

	return nil
}

// CreateBridgePort adds a port to the bridge.
func (o *VSwitch) CreateBridgePort(ctx context.Context, bridgeName string, portName string, mayExist bool) error {
	// Get the bridge.
	bridge := ovsSwitch.Bridge{
		Name: bridgeName,
	}

	err := o.client.Get(ctx, &bridge)
	if err != nil {
		return err
	}

	// Create the interface.
	iface := ovsSwitch.Interface{
		UUID: "interface",
		Name: portName,
	}

	interfaceOps, err := o.client.Create(&iface)
	if err != nil {
		return err
	}

	// Create the port.
	port := ovsSwitch.Port{
		Name: portName,
	}

	err = o.client.Get(ctx, &port)
	if err != nil && err != ovsdbClient.ErrNotFound {
		return err
	}

	if port.UUID != "" {
		if mayExist {
			// Already exists.
			return nil
		}

		return fmt.Errorf("OVS port %q already exists on %q", portName, bridgeName)
	}

	port.UUID = "port"
	port.Interfaces = []string{iface.UUID}
	portOps, err := o.client.Create(&port)
	if err != nil {
		return err
	}

	// Create the bridge port entry.
	mutateOps, err := o.client.Where(&bridge).Mutate(&bridge, ovsdbModel.Mutation{
		Field:   &bridge.Ports,
		Mutator: ovsdb.MutateOperationInsert,
		Value:   []string{port.UUID},
	})
	if err != nil {
		return err
	}

	operations := append(interfaceOps, portOps...)
	operations = append(operations, mutateOps...)

	resp, err := o.client.Transact(ctx, operations...)
	if err != nil {
		return err
	}

	_, err = ovsdb.CheckOperationResults(resp, operations)
	if err != nil {
		return err
	}

	return nil
}

// DeleteBridgePort deletes a port from the bridge (if already detached does nothing).
func (o *VSwitch) DeleteBridgePort(ctx context.Context, bridgeName string, portName string) error {
	operations := []ovsdb.Operation{}

	// Get the bridge port.
	bridgePort := ovsSwitch.Port{
		Name: string(portName),
	}

	err := o.client.Get(ctx, &bridgePort)
	if err != nil {
		// Logical switch port is already gone.
		if err == ErrNotFound {
			return nil
		}

		return err
	}

	// Remove the port from the bridge.
	bridge := ovsSwitch.Bridge{
		Name: string(bridgeName),
	}

	updateOps, err := o.client.Where(&bridge).Mutate(&bridge, ovsdbModel.Mutation{
		Field:   &bridge.Ports,
		Mutator: ovsdb.MutateOperationDelete,
		Value:   []string{bridgePort.UUID},
	})
	if err != nil {
		return err
	}

	operations = append(operations, updateOps...)

	// Delete the port itself.
	deleteOps, err := o.client.Where(&bridgePort).Delete()
	if err != nil {
		return err
	}

	operations = append(operations, deleteOps...)

	// Apply the changes.
	resp, err := o.client.Transact(ctx, operations...)
	if err != nil {
		return err
	}

	_, err = ovsdb.CheckOperationResults(resp, operations)
	if err != nil {
		return err
	}

	return nil
}

// UpdateBridgePortVLANs sets the VLAN mode and VLAN IDs on the port.
func (o *VSwitch) UpdateBridgePortVLANs(ctx context.Context, portName string, mode string, tag int, trunks []int) error {
	// Get the port.
	port := &ovsSwitch.Port{
		Name: portName,
	}

	err := o.client.Get(ctx, port)
	if err != nil {
		return err
	}

	// Set the options.
	if mode != "" {
		port.VLANMode = &mode
	} else {
		port.VLANMode = nil
	}

	if tag > 0 {
		port.Tag = &tag
	} else {
		port.Tag = nil
	}

	port.Trunks = trunks

	// Update the record.
	operations, err := o.client.Where(port).Update(port)
	if err != nil {
		return err
	}

	resp, err := o.client.Transact(ctx, operations...)
	if err != nil {
		return err
	}

	_, err = ovsdb.CheckOperationResults(resp, operations)
	if err != nil {
		return err
	}

	return nil
}

// AssociateInterfaceOVNSwitchPort removes any existing switch ports associated to the specified ovnSwitchPortName
// and then associates the specified interfaceName to the OVN switch port.
func (o *VSwitch) AssociateInterfaceOVNSwitchPort(ctx context.Context, interfaceName string, ovnSwitchPortName string) error {
	// Get the interfaces.
	interfaceList := []ovsSwitch.Interface{}

	err := o.client.WhereCache(func(iface *ovsSwitch.Interface) bool {
		return iface.ExternalIDs["iface-id"] == string(ovnSwitchPortName)
	}).List(ctx, &interfaceList)
	if err != nil {
		return err
	}

	// Delete the matching ports.
	for _, iface := range interfaceList {
		// Get the port.
		portList := []ovsSwitch.Port{}

		err := o.client.WhereCache(func(port *ovsSwitch.Port) bool {
			return slices.Contains(port.Interfaces, iface.UUID)
		}).List(ctx, &portList)
		if err != nil {
			return err
		}

		if len(portList) != 1 {
			return fmt.Errorf("Failed to find port for interface %q", iface.Name)
		}

		port := portList[0]

		// Get the bridge.
		bridgeList := []ovsSwitch.Bridge{}

		err = o.client.WhereCache(func(bridge *ovsSwitch.Bridge) bool {
			return slices.Contains(bridge.Ports, port.UUID)
		}).List(ctx, &bridgeList)
		if err != nil {
			return err
		}

		if len(bridgeList) != 1 {
			return fmt.Errorf("Failed to find bridge for port %q", portList[0].Name)
		}

		bridge := bridgeList[0]

		// Update the records.
		var operations []ovsdb.Operation

		// Delete the bridge port.
		updateOps, err := o.client.Where(&bridge).Mutate(&bridge, ovsdbModel.Mutation{
			Field:   &bridge.Ports,
			Mutator: ovsdb.MutateOperationDelete,
			Value:   []string{port.UUID},
		})
		if err != nil {
			return err
		}

		operations = append(operations, updateOps...)

		// Delete the port.
		deleteOps, err := o.client.Where(&port).Delete()
		if err != nil {
			return err
		}

		operations = append(operations, deleteOps...)

		// Delete the interface.
		deleteOps, err = o.client.Where(&iface).Delete()
		if err != nil {
			return err
		}

		operations = append(operations, deleteOps...)

		// Apply the changes.
		resp, err := o.client.Transact(ctx, operations...)
		if err != nil {
			return err
		}

		_, err = ovsdb.CheckOperationResults(resp, operations)
		if err != nil {
			return err
		}

		// Atempt to remove port, but don't fail if doesn't exist or can't be removed, at least
		// the switch association has been successfully removed, so the new port being added next
		// won't fail to work properly.
		link := &ip.Link{Name: iface.Name}
		_ = link.Delete()
	}

	// Get the new interface.
	ovsInterface := &ovsSwitch.Interface{
		Name: interfaceName,
	}

	err = o.client.Get(ctx, ovsInterface)
	if err != nil {
		return err
	}

	// Update the record.
	if ovsInterface.ExternalIDs == nil {
		ovsInterface.ExternalIDs = map[string]string{}
	}

	ovsInterface.ExternalIDs["iface-id"] = string(ovnSwitchPortName)

	operations, err := o.client.Where(ovsInterface).Update(ovsInterface)
	if err != nil {
		return err
	}

	resp, err := o.client.Transact(ctx, operations...)
	if err != nil {
		return err
	}

	_, err = ovsdb.CheckOperationResults(resp, operations)
	if err != nil {
		return err
	}

	return nil
}

// GetInterfaceAssociatedOVNSwitchPort returns the OVN switch port associated to the interface.
func (o *VSwitch) GetInterfaceAssociatedOVNSwitchPort(ctx context.Context, interfaceName string) (string, error) {
	// Get the OVS interface.
	ovsInterface := ovsSwitch.Interface{
		Name: interfaceName,
	}

	err := o.client.Get(ctx, &ovsInterface)
	if err != nil {
		return "", err
	}

	// Return the iface-id.
	return ovsInterface.ExternalIDs["iface-id"], nil
}

// GetChassisID returns the local chassis ID.
func (o *VSwitch) GetChassisID(ctx context.Context) (string, error) {
	// Get the root switch.
	vSwitch := &ovsSwitch.OpenvSwitch{
		UUID: o.rootUUID,
	}

	err := o.client.Get(ctx, vSwitch)
	if err != nil {
		return "", err
	}

	// Return the system-id.
	return vSwitch.ExternalIDs["system-id"], nil
}

// GetOVNEncapIP returns the enscapsulation IP used for OVN underlay tunnels.
func (o *VSwitch) GetOVNEncapIP(ctx context.Context) (net.IP, error) {
	// Get the root switch.
	vSwitch := &ovsSwitch.OpenvSwitch{
		UUID: o.rootUUID,
	}

	err := o.client.Get(ctx, vSwitch)
	if err != nil {
		return nil, err
	}

	// Return the system-id.
	encapIP := net.ParseIP(vSwitch.ExternalIDs["ovn-encap-ip"])
	if encapIP == nil {
		return nil, fmt.Errorf("Invalid ovn-encap-ip address %q", vSwitch.ExternalIDs["ovn-encap-ip"])
	}

	return encapIP, nil
}

// GetOVNBridgeMappings gets the current OVN bridge mappings.
func (o *VSwitch) GetOVNBridgeMappings(ctx context.Context, bridgeName string) ([]string, error) {
	// Get the root switch.
	vSwitch := &ovsSwitch.OpenvSwitch{
		UUID: o.rootUUID,
	}

	err := o.client.Get(ctx, vSwitch)
	if err != nil {
		return nil, err
	}

	// Return the bridge mappings.
	val := vSwitch.ExternalIDs["ovn-bridge-mappings"]
	if val == "" {
		return []string{}, nil
	}

	return strings.SplitN(val, ",", -1), nil
}

// AddOVNBridgeMapping appends an OVN bridge mapping between a bridge and the logical provider name.
func (o *VSwitch) AddOVNBridgeMapping(ctx context.Context, bridgeName string, providerName string) error {
	// Prevent concurrent changes.
	ovnBridgeMappingMutex.Lock()
	defer ovnBridgeMappingMutex.Unlock()

	// Get the root switch.
	vSwitch := &ovsSwitch.OpenvSwitch{
		UUID: o.rootUUID,
	}

	err := o.client.Get(ctx, vSwitch)
	if err != nil {
		return err
	}

	// Get the current bridge mappings.
	val := vSwitch.ExternalIDs["ovn-bridge-mappings"]

	var mappings []string
	if val != "" {
		mappings = strings.SplitN(val, ",", -1)
	} else {
		mappings = []string{}
	}

	// Check if the mapping is already present.
	newMapping := fmt.Sprintf("%s:%s", providerName, bridgeName)
	for _, mapping := range mappings {
		if mapping == newMapping {
			return nil // Mapping is already present, nothing to do.
		}
	}

	// Add the new mapping.
	mappings = append(mappings, newMapping)

	if vSwitch.ExternalIDs == nil {
		vSwitch.ExternalIDs = map[string]string{}
	}

	vSwitch.ExternalIDs["ovn-bridge-mappings"] = strings.Join(mappings, ",")

	// Update the record.
	operations, err := o.client.Where(vSwitch).Update(vSwitch)
	if err != nil {
		return err
	}

	resp, err := o.client.Transact(ctx, operations...)
	if err != nil {
		return err
	}

	_, err = ovsdb.CheckOperationResults(resp, operations)
	if err != nil {
		return err
	}

	return nil
}

// RemoveOVNBridgeMapping deletes an OVN bridge mapping between a bridge and the logical provider name.
func (o *VSwitch) RemoveOVNBridgeMapping(ctx context.Context, bridgeName string, providerName string) error {
	// Prevent concurrent changes.
	ovnBridgeMappingMutex.Lock()
	defer ovnBridgeMappingMutex.Unlock()

	// Get the root switch.
	vSwitch := &ovsSwitch.OpenvSwitch{
		UUID: o.rootUUID,
	}

	err := o.client.Get(ctx, vSwitch)
	if err != nil {
		return err
	}

	// Get the current bridge mappings.
	val := vSwitch.ExternalIDs["ovn-bridge-mappings"]
	mappings := strings.SplitN(val, ",", -1)
	newMappings := []string{}

	// Remove the mapping from the list.
	currentMapping := fmt.Sprintf("%s:%s", providerName, bridgeName)
	for _, mapping := range mappings {
		if mapping == currentMapping {
			continue
		}

		newMappings = append(newMappings, mapping)
	}

	// If no more mappings, remove the key completely.
	if len(newMappings) == 0 {
		delete(vSwitch.ExternalIDs, "ovn-bridge-mappings")
	}

	// Update the record.
	operations, err := o.client.Where(vSwitch).Update(vSwitch)
	if err != nil {
		return err
	}

	resp, err := o.client.Transact(ctx, operations...)
	if err != nil {
		return err
	}

	_, err = ovsdb.CheckOperationResults(resp, operations)
	if err != nil {
		return err
	}

	return nil
}

// GetBridgePorts returns a list of ports that are connected to the bridge.
func (o *VSwitch) GetBridgePorts(ctx context.Context, bridgeName string) ([]string, error) {
	// Get the bridge.
	bridge := &ovsSwitch.Bridge{
		Name: bridgeName,
	}

	err := o.client.Get(ctx, bridge)
	if err != nil {
		return nil, err
	}

	// Get the ports.
	portNames := make([]string, 0, len(bridge.Ports))
	for _, portUUID := range bridge.Ports {
		port := &ovsSwitch.Port{
			UUID: portUUID,
		}

		err = o.client.Get(ctx, port)
		if err != nil {
			return nil, err
		}

		portNames = append(portNames, port.Name)
	}

	return portNames, nil
}

// GetHardwareOffload returns true if hardware offloading is enabled.
func (o *VSwitch) GetHardwareOffload(ctx context.Context) (bool, error) {
	// Get the root switch.
	vSwitch := &ovsSwitch.OpenvSwitch{
		UUID: o.rootUUID,
	}

	err := o.client.Get(ctx, vSwitch)
	if err != nil {
		return false, err
	}

	// Return the hw-offload state.
	return vSwitch.OtherConfig["hw-offload"] == "true", nil
}

// GetOVNSouthboundDBRemoteAddress gets the address of the southbound ovn database.
func (o *VSwitch) GetOVNSouthboundDBRemoteAddress(ctx context.Context) (string, error) {
	vSwitch := &ovsSwitch.OpenvSwitch{
		UUID: o.rootUUID,
	}

	err := o.client.Get(ctx, vSwitch)
	if err != nil {
		return "", err
	}

	val := vSwitch.ExternalIDs["ovn-remote"]

	return val, nil
}
