package ovs

import (
	"context"
	"fmt"
	"runtime"

	"github.com/go-logr/logr"
	ovsdbClient "github.com/ovn-org/libovsdb/client"

	ovsSwitch "github.com/lxc/incus/internal/server/network/ovs/schema/ovs"
)

// VSwitch client.
type VSwitch struct {
	client   ovsdbClient.Client
	cookie   ovsdbClient.MonitorCookie
	rootUUID string
}

// NewVSwitch initialises a new vSwitch client..
func NewVSwitch() (*VSwitch, error) {
	// Prepare the OVSDB client.
	dbSchema, err := ovsSwitch.FullDatabaseModel()
	if err != nil {
		return nil, err
	}

	discard := logr.Discard()

	options := []ovsdbClient.Option{
		ovsdbClient.WithLogger(&discard),
		ovsdbClient.WithEndpoint("unix:///run/openvswitch/db.sock"),
	}

	// Connect to OVSDB.
	ovs, err := ovsdbClient.NewOVSDBClient(dbSchema, options...)
	if err != nil {
		return nil, err
	}

	err = ovs.Connect(context.TODO())
	if err != nil {
		return nil, err
	}

	err = ovs.Echo(context.TODO())
	if err != nil {
		return nil, err
	}

	monitorCookie, err := ovs.MonitorAll(context.TODO())
	if err != nil {
		return nil, err
	}

	// Create the SB struct.
	client := &VSwitch{
		client: ovs,
		cookie: monitorCookie,
	}

	// Set finalizer to stop the monitor.
	runtime.SetFinalizer(client, func(o *VSwitch) {
		_ = ovs.MonitorCancel(context.Background(), o.cookie)
		ovs.Close()
	})

	// Get the root UUID.
	rows := ovs.Cache().Table("Open_vSwitch").Rows()
	if len(rows) != 1 {
		return nil, fmt.Errorf("Cannot find the OVS root switch")
	}

	for uuid := range rows {
		client.rootUUID = uuid
	}

	return client, nil
}
