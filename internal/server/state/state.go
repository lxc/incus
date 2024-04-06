//go:build linux && cgo && !agent

package state

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/lxc/incus/v6/internal/server/auth"
	"github.com/lxc/incus/v6/internal/server/bgp"
	clusterConfig "github.com/lxc/incus/v6/internal/server/cluster/config"
	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/dns"
	"github.com/lxc/incus/v6/internal/server/endpoints"
	"github.com/lxc/incus/v6/internal/server/events"
	"github.com/lxc/incus/v6/internal/server/firewall"
	"github.com/lxc/incus/v6/internal/server/fsmonitor"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/network/ovn"
	"github.com/lxc/incus/v6/internal/server/node"
	"github.com/lxc/incus/v6/internal/server/sys"
	localtls "github.com/lxc/incus/v6/shared/tls"
)

// State is a gateway to the two main stateful components, the database
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

	// REST endpoints
	Endpoints *endpoints.Endpoints

	// Event server
	DevIncusEvents *events.DevIncusServer
	Events         *events.Server

	// Firewall instance
	Firewall firewall.Firewall

	// Server certificate
	ServerCert             func() *localtls.CertInfo
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

	// Whether the server is clustered.
	ServerClustered bool

	// Local server start time.
	StartTime time.Time

	// Authorizer.
	Authorizer auth.Authorizer

	// OVN.
	OVNNB *ovn.NB
	OVNSB *ovn.SB
}
