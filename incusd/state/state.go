//go:build linux && cgo && !agent

package state

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/lxc/incus/incusd/bgp"
	clusterConfig "github.com/lxc/incus/incusd/cluster/config"
	"github.com/lxc/incus/incusd/db"
	"github.com/lxc/incus/incusd/dns"
	"github.com/lxc/incus/incusd/endpoints"
	"github.com/lxc/incus/incusd/events"
	"github.com/lxc/incus/incusd/firewall"
	"github.com/lxc/incus/incusd/fsmonitor"
	"github.com/lxc/incus/incusd/instance/instancetype"
	"github.com/lxc/incus/incusd/node"
	"github.com/lxc/incus/incusd/sys"
	"github.com/lxc/incus/shared"
)

// State is a gateway to the two main stateful components of LXD, the database
// and the operating system. It's typically used by model entities such as
// containers, volumes, etc. in order to perform changes.
type State struct {
	// Shutdown Context
	ShutdownCtx context.Context

	// Databases
	DB *db.DB

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
	DevIncusEvents *events.DevIncusServer
	Events         *events.Server

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
