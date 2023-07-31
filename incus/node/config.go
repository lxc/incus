package node

import (
	"context"
	"fmt"

	"github.com/cyphar/incus/incus/config"
	"github.com/cyphar/incus/incus/db"
	"github.com/cyphar/incus/incus/util"
	"github.com/cyphar/incus/shared"
	"github.com/cyphar/incus/shared/validate"
)

// Config holds node-local configuration values for a certain LXD instance.
type Config struct {
	tx *db.NodeTx // DB transaction the values in this config are bound to
	m  config.Map // Low-level map holding the config values.
}

// ConfigLoad loads a new Config object with the current node-local configuration
// values fetched from the database. An optional list of config value triggers
// can be passed, each config key must have at most one trigger.
func ConfigLoad(ctx context.Context, tx *db.NodeTx) (*Config, error) {
	// Load current raw values from the database, any error is fatal.
	values, err := tx.Config(ctx)
	if err != nil {
		return nil, fmt.Errorf("Cannot fetch node config from database: %w", err)
	}

	m, err := config.SafeLoad(ConfigSchema, values)
	if err != nil {
		return nil, fmt.Errorf("Failed to load node config: %w", err)
	}

	return &Config{tx: tx, m: m}, nil
}

// HTTPSAddress returns the address and port this LXD node should expose its
// API to, if any.
func (c *Config) HTTPSAddress() string {
	networkAddress := c.m.GetString("core.https_address")
	if networkAddress != "" {
		return util.CanonicalNetworkAddress(networkAddress, shared.HTTPSDefaultPort)
	}

	return networkAddress
}

// BGPAddress returns the address and port to setup the BGP listener on.
func (c *Config) BGPAddress() string {
	return c.m.GetString("core.bgp_address")
}

// BGPRouterID returns the address to use as a router ID.
func (c *Config) BGPRouterID() string {
	return c.m.GetString("core.bgp_routerid")
}

// ClusterAddress returns the address and port this LXD node should use for
// cluster communication.
func (c *Config) ClusterAddress() string {
	clusterAddress := c.m.GetString("cluster.https_address")
	if clusterAddress != "" {
		return util.CanonicalNetworkAddress(clusterAddress, shared.HTTPSDefaultPort)
	}

	return clusterAddress
}

// DebugAddress returns the address and port to setup the pprof listener on.
func (c *Config) DebugAddress() string {
	debugAddress := c.m.GetString("core.debug_address")
	if debugAddress != "" {
		return util.CanonicalNetworkAddress(debugAddress, shared.HTTPDefaultPort)
	}

	return debugAddress
}

// DNSAddress returns the address and port to setup the DNS listener on.
func (c *Config) DNSAddress() string {
	return c.m.GetString("core.dns_address")
}

// MetricsAddress returns the address and port to setup the metrics listener on.
func (c *Config) MetricsAddress() string {
	metricsAddress := c.m.GetString("core.metrics_address")
	if metricsAddress != "" {
		return util.CanonicalNetworkAddress(metricsAddress, shared.HTTPSMetricsDefaultPort)
	}

	return metricsAddress
}

// MAASMachine returns the MAAS machine this instance is associated with, if
// any.
func (c *Config) MAASMachine() string {
	return c.m.GetString("maas.machine")
}

// StorageBucketsAddress returns the address and port to setup the storage buckets listener on.
func (c *Config) StorageBucketsAddress() string {
	objectAddress := c.m.GetString("core.storage_buckets_address")
	if objectAddress != "" {
		return util.CanonicalNetworkAddress(objectAddress, shared.HTTPSStorageBucketsDefaultPort)
	}

	return objectAddress
}

// StorageBackupsVolume returns the name of the pool/volume to use for storing backup tarballs.
func (c *Config) StorageBackupsVolume() string {
	return c.m.GetString("storage.backups_volume")
}

// StorageImagesVolume returns the name of the pool/volume to use for storing image tarballs.
func (c *Config) StorageImagesVolume() string {
	return c.m.GetString("storage.images_volume")
}

// Dump current configuration keys and their values. Keys with values matching
// their defaults are omitted.
func (c *Config) Dump() map[string]any {
	return c.m.Dump()
}

// Replace the current configuration with the given values.
func (c *Config) Replace(values map[string]any) (map[string]string, error) {
	return c.update(values)
}

// Patch changes only the configuration keys in the given map.
func (c *Config) Patch(patch map[string]any) (map[string]string, error) {
	values := c.Dump() // Use current values as defaults
	for name, value := range patch {
		values[name] = value
	}

	return c.update(values)
}

func (c *Config) update(values map[string]any) (map[string]string, error) {
	changed, err := c.m.Change(values)
	if err != nil {
		return nil, err
	}

	err = c.tx.UpdateConfig(changed)
	if err != nil {
		return nil, fmt.Errorf("Cannot persist local configuration changes: %w", err)
	}

	return changed, nil
}

// ConfigSchema defines available server configuration keys.
var ConfigSchema = config.Schema{
	// Network address for this LXD server
	"core.https_address": {Validator: validate.Optional(validate.IsListenAddress(true, true, false))},

	// Network address for cluster communication
	"cluster.https_address": {Validator: validate.Optional(validate.IsListenAddress(true, false, false))},

	// Network address for the BGP server
	"core.bgp_address": {Validator: validate.Optional(validate.IsListenAddress(true, true, false))},

	// Unique router ID for the BGP server
	"core.bgp_routerid": {Validator: validate.Optional(validate.IsNetworkAddressV4)},

	// Network address for the debug server
	"core.debug_address": {Validator: validate.Optional(validate.IsListenAddress(true, true, false))},

	// Network address for the DNS server
	"core.dns_address": {Validator: validate.Optional(validate.IsListenAddress(true, true, false))},

	// Network address for the metrics server
	"core.metrics_address": {Validator: validate.Optional(validate.IsListenAddress(true, true, false))},

	// Network address for the storage buckets server
	"core.storage_buckets_address": {Validator: validate.Optional(validate.IsListenAddress(true, true, false))},

	// MAAS machine this LXD instance is associated with
	"maas.machine": {},

	// Storage volumes to store backups/images on
	"storage.backups_volume": {},
	"storage.images_volume":  {},
}
