//go:build linux && cgo && !agent

package state

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/canonical/lxd/lxd/bgp"
	clusterConfig "github.com/canonical/lxd/lxd/cluster/config"
	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/dns"
	"github.com/canonical/lxd/lxd/endpoints"
	"github.com/canonical/lxd/lxd/events"
	"github.com/canonical/lxd/lxd/firewall"
	"github.com/canonical/lxd/lxd/fsmonitor"
	"github.com/canonical/lxd/lxd/instance/instancetype"
	"github.com/canonical/lxd/lxd/maas"
	"github.com/canonical/lxd/lxd/node"
	"github.com/canonical/lxd/lxd/sys"
	"github.com/canonical/lxd/shared"
)

// State is a gateway to the two main stateful components of LXD, the database
// and the operating system. It's typically used by model entities such as
// containers, volumes, etc. in order to perform changes.
type State struct {
	// Shutdown Context
	ShutdownCtx context.Context

	// Databases
	DB *db.DB

	// MAAS server
	MAAS *maas.Controller

	// BGP server
	BGP *bgp.Server

	// DNS server
	DNS *dns.Server

	// OS access
	OS    *sys.OS
	Proxy func(req *http.Request) (*url.URL, error)

	// LXD server
	Endpoints *endpoints.Endpoints

	// Event server
	DevlxdEvents *events.DevLXDServer
	Events       *events.Server

	// Firewall instance
	Firewall firewall.Firewall

	// Server certificate
	ServerCert             func() *shared.CertInfo
	UpdateCertificateCache func()

	// Available instance types based on operational drivers.
	InstanceTypes map[instancetype.Type]error

	// Filesystem monitor
	DevMonitor fsmonitor.FSMonitor

	// Global configuration
	GlobalConfig *clusterConfig.Config

	// Local configuration
	LocalConfig *node.Config

	// Local server name.
	ServerName string

	// Local server start time.
	StartTime time.Time
}
