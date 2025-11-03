//go:build linux && cgo && !agent

package sys

import (
	"os"
	"strings"

	"github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/db/warningtype"
	internalUtil "github.com/lxc/incus/v6/internal/util"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/util"
)

// Initialize SELinux-specific attributes.
func (s *OS) initSELinux() []cluster.Warning {
	var dbWarnings []cluster.Warning

	// SELinux support is currently opt-in.
	if os.Getenv("INCUS_SECURITY_SELINUX") == "" {
		return dbWarnings
	}

	// Detect SELinux availability.
	if util.IsFalse(os.Getenv("INCUS_SECURITY_SELINUX")) {
		logger.Warnf("SELinux support has been manually disabled")
		dbWarnings = append(dbWarnings, cluster.Warning{
			TypeCode:    warningtype.SELinuxNotAvailable,
			LastMessage: "Manually disabled",
		})

		return dbWarnings
	} else if !internalUtil.IsDir("/sys/fs/selinux") {
		logger.Warnf("SELinux support has been disabled because of lack of kernel support")
		dbWarnings = append(dbWarnings, cluster.Warning{
			TypeCode:    warningtype.SELinuxNotAvailable,
			LastMessage: "Disabled because of lack of kernel support",
		})

		return dbWarnings
	}

	s.SELinuxAvailable = true

	// Read our own context.
	content, err := os.ReadFile("/proc/self/attr/current")
	if err != nil {
		logger.Warnf("SELinux support has been disabled because of unaccessible context data")
		dbWarnings = append(dbWarnings, cluster.Warning{
			TypeCode:    warningtype.SELinuxNotAvailable,
			LastMessage: "Disabled because of unaccessible context data",
		})

		return dbWarnings
	}

	s.SELinuxContextDaemon = strings.TrimRight(strings.TrimSpace(string(content)), "\x00")

	// Handle the various SELinux policy variants here.
	if s.SELinuxContextDaemon == "system_u:system_r:container_runtime_t:s0" {
		logger.Debugf("Detected Fedora-style SELinux setup")
		s.SELinuxContextInstanceLXC = "system_u:system_r:spc_t:s0"
	} else if s.SELinuxContextDaemon == "system_u:system_r:incusd_t:s0" {
		logger.Debugf("Detected SELinux refpolicy setup")
		s.SELinuxContextInstanceLXC = "system_u:system_r:container_init_t:s0"
	}

	return dbWarnings
}
