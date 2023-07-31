//go:build linux && cgo && !agent

package state

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/cyphar/incus/lxd/bgp"
	clusterConfig "github.com/cyphar/incus/lxd/cluster/config"
	"github.com/cyphar/incus/lxd/db"
	"github.com/cyphar/incus/lxd/dns"
	"github.com/cyphar/incus/lxd/endpoints"
	"github.com/cyphar/incus/lxd/events"
	"github.com/cyphar/incus/lxd/firewall"
	"github.com/cyphar/incus/lxd/fsmonitor"
	"github.com/cyphar/incus/lxd/instance/instancetype"
	"github.com/cyphar/incus/lxd/maas"
	"github.com/cyphar/incus/lxd/node"
	"github.com/cyphar/incus/lxd/sys"
	"github.com/cyphar/incus/shared"
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
