package node

import (
	"context"
	"fmt"

	"github.com/lxc/incus/internal/ports"
	"github.com/lxc/incus/internal/server/config"
	"github.com/lxc/incus/internal/server/db"
	internalUtil "github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/shared/validate"
)

// Config holds member-local configuration values.
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

// HTTPSAddress returns the address and port this server should expose its // API to, if any.
func (c *Config) HTTPSAddress() string {
	networkAddress := c.m.GetString("core.https_address")
	if networkAddress != "" {
		return internalUtil.CanonicalNetworkAddress(networkAddress, ports.HTTPSDefaultPort)
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

// ClusterAddress returns the address and port this cluster member should use for cluster communication.
func (c *Config) ClusterAddress() string {
	clusterAddress := c.m.GetString("cluster.https_address")
	if clusterAddress != "" {
		return internalUtil.CanonicalNetworkAddress(clusterAddress, ports.HTTPSDefaultPort)
	}

	return clusterAddress
}

// DebugAddress returns the address and port to setup the pprof listener on.
func (c *Config) DebugAddress() string {
	debugAddress := c.m.GetString("core.debug_address")
	if debugAddress != "" {
		return internalUtil.CanonicalNetworkAddress(debugAddress, ports.HTTPDebugDefaultPort)
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
		return internalUtil.CanonicalNetworkAddress(metricsAddress, ports.HTTPSMetricsDefaultPort)
	}

	return metricsAddress
}

// StorageBucketsAddress returns the address and port to setup the storage buckets listener on.
func (c *Config) StorageBucketsAddress() string {
	objectAddress := c.m.GetString("core.storage_buckets_address")
	if objectAddress != "" {
		return internalUtil.CanonicalNetworkAddress(objectAddress, ports.HTTPSStorageBucketsDefaultPort)
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

// SyslogSocket returns true if the syslog socket is enabled, otherwise false.
func (c *Config) SyslogSocket() bool {
	return c.m.GetBool("core.syslog_socket")
}

// Dump current configuration keys and their values. Keys with values matching
// their defaults are omitted.
func (c *Config) Dump() map[string]string {
	return c.m.Dump()
}

// Replace the current configuration with the given values.
func (c *Config) Replace(values map[string]string) (map[string]string, error) {
	return c.update(values)
}

// Patch changes only the configuration keys in the given map.
func (c *Config) Patch(patch map[string]string) (map[string]string, error) {
	values := c.Dump() // Use current values as defaults
	for name, value := range patch {
		values[name] = value
	}

	return c.update(values)
}

func (c *Config) update(values map[string]string) (map[string]string, error) {
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
	// Network address for this server

	// gendoc:generate(entity=server, group=core, key=core.https_address)
	// See {ref}`server-expose`.
	// ---
	//  type: string
	//  scope: local
	//  shortdesc: Address to bind for the remote API (HTTPS)
	"core.https_address": {Validator: validate.Optional(validate.IsListenAddress(true, true, false))},

	// Network address for cluster communication

	// gendoc:generate(entity=server, group=cluster, key=cluster.https_address)
	// See {ref}`cluster-https-address`.
	// ---
	//  type: string
	//  scope: local
	//  shortdesc: Address to use for clustering traffic
	"cluster.https_address": {Validator: validate.Optional(validate.IsListenAddress(true, false, false))},

	// Network address for the BGP server

	// gendoc:generate(entity=server, group=core, key=core.bgp_address)
	// See {ref}`network-bgp`.
	// ---
	//  type: string
	//  scope: local
	//  shortdesc: Address to bind the BGP server to
	"core.bgp_address": {Validator: validate.Optional(validate.IsListenAddress(true, true, false))},

	// Unique router ID for the BGP server

	// gendoc:generate(entity=server, group=core, key=core.bgp_routerid)
	// The identifier must be formatted as an IPv4 address.
	// ---
	//  type: string
	//  scope: local
	//  shortdesc: A unique identifier for the BGP server
	"core.bgp_routerid": {Validator: validate.Optional(validate.IsNetworkAddressV4)},

	// Network address for the debug server

	// gendoc:generate(entity=server, group=core, key=core.debug_address)
	//
	// ---
	//  type: string
	//  scope: local
	//  shortdesc: Address to bind the `pprof` debug server to (HTTP)
	"core.debug_address": {Validator: validate.Optional(validate.IsListenAddress(true, true, false))},

	// Network address for the DNS server

	// gendoc:generate(entity=server, group=core, key=core.dns_address)
	// See {ref}`network-dns-server`.
	// ---
	//  type: string
	//  scope: local
	//  shortdesc: Address to bind the authoritative DNS server to
	"core.dns_address": {Validator: validate.Optional(validate.IsListenAddress(true, true, false))},

	// Network address for the metrics server

	// gendoc:generate(entity=server, group=core, key=core.metrics_address)
	// See {ref}`metrics`.
	// ---
	//  type: string
	//  scope: local
	//  shortdesc: Address to bind the metrics server to (HTTPS)
	"core.metrics_address": {Validator: validate.Optional(validate.IsListenAddress(true, true, false))},

	// Network address for the storage buckets server

	// gendoc:generate(entity=server, group=core, key=core.storage_buckets_address)
	// See {ref}`howto-storage-buckets`.
	// ---
	//  type: string
	//  scope: local
	//  shortdesc: Address to bind the storage object server to (HTTPS)
	"core.storage_buckets_address": {Validator: validate.Optional(validate.IsListenAddress(true, true, false))},

	// Syslog socket

	// gendoc:generate(entity=server, group=core, key=core.syslog_socket)
	// Set this option to `true` to enable the syslog unixgram socket to receive log messages from external processes.
	// ---
	//  type: bool
	//  scope: local
	//  defaultdesc: `false`
	//  shortdesc: Whether to enable the syslog unixgram socket listener
	"core.syslog_socket": {Validator: validate.Optional(validate.IsBool), Type: config.Bool},

	// Storage volumes to store backups/images on

	// gendoc:generate(entity=server, group=miscellaneous, key=storage.backups_volume)
	// Specify the volume using the syntax `POOL/VOLUME`.
	// ---
	//  type: string
	//  scope: local
	//  shortdesc: Volume to use to store backup tarballs
	"storage.backups_volume": {},
	// gendoc:generate(entity=server, group=miscellaneous, key=storage.images_volume)
	// Specify the volume using the syntax `POOL/VOLUME`.
	// ---
	//  type: string
	//  scope: local
	//  shortdesc: Volume to use to store the image tarballs
	"storage.images_volume": {},
}
