package ovs

import (
	"context"
	"errors"
	"runtime"
	"time"

	"github.com/cenkalti/backoff/v7"
	"github.com/go-logr/logr"
	ovsdbClient "github.com/ovn-kubernetes/libovsdb/client"

	ovsSwitch "github.com/lxc/incus/v7/internal/server/network/ovs/schema/ovs"
	backendDB "github.com/lxc/incus/v7/internal/server/network/ovsdb"
)

// VSwitch client.
type VSwitch struct {
	client    ovsdbClient.Client
	cookie    ovsdbClient.MonitorCookie
	rootUUID  string
	backendID string
}

// BackendID returns the root UUID acknowledged at construction, or empty for an unfenced client.
func (o *VSwitch) BackendID() string {
	return o.backendID
}

// NewVSwitch initializes a new vSwitch client..
func NewVSwitch(dbAddr string, owner ...string) (*VSwitch, error) {
	// Prepare the OVSDB client.
	dbSchema, err := ovsSwitch.FullDatabaseModel()
	if err != nil {
		return nil, err
	}

	discard := logr.Discard()

	options := []ovsdbClient.Option{
		ovsdbClient.WithLogger(&discard),
		ovsdbClient.WithEndpoint(dbAddr),
		ovsdbClient.WithReconnect(5*time.Second, &backoff.ZeroBackOff{}),
	}

	// Connect to OVSDB.
	ovs, err := ovsdbClient.NewOVSDBClient(dbSchema, options...)
	if err != nil {
		return nil, err
	}

	// Bound connection, monitor setup and the initial generation acknowledgement.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err = ovs.Connect(ctx)
	if err != nil {
		return nil, err
	}

	err = ovs.Echo(ctx)
	if err != nil {
		ovs.Close()
		return nil, err
	}

	monitorCookie, err := ovs.MonitorAll(ctx)
	if err != nil {
		ovs.Close()
		return nil, err
	}

	backend := ovs
	backendID := ""
	if len(owner) > 0 && owner[0] != "" {
		fenced, err := backendDB.NewFencedClient(ctx, ovs, "Open_vSwitch", owner[0])
		if err != nil {
			ovs.Close()
			return nil, err
		}

		backend = fenced
		backendID = fenced.RootUUID()
	}

	// Create the switch client.
	client := &VSwitch{
		client:    backend,
		cookie:    monitorCookie,
		backendID: backendID,
	}

	// Set finalizer to stop the monitor.
	runtime.SetFinalizer(client, func(o *VSwitch) {
		_ = ovs.MonitorCancel(context.Background(), o.cookie)
		ovs.Close()
	})

	// Get the root UUID.
	rows := ovs.Cache().Table("Open_vSwitch").Rows()
	if len(rows) != 1 {
		return nil, errors.New("Cannot find the OVS root switch")
	}

	for uuid := range rows {
		client.rootUUID = uuid
	}

	return client, nil
}

// Close releases the owned OVSDB connection and disables its deferred finalizer.
func (o *VSwitch) Close() {
	runtime.SetFinalizer(o, nil)
	o.client.Close()
}
