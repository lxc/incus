//go:build linux && cgo && !agent

package sys

import (
	"os"

	goselinux "github.com/opencontainers/selinux/go-selinux"

	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/db/warningtype"
	"github.com/lxc/incus/v7/shared/logger"
	"github.com/lxc/incus/v7/shared/util"
)

// initSELinux detects SELinux state and configures instance type mappings.
func (s *OS) initSELinux() []cluster.Warning {
	var dbWarnings []cluster.Warning
	s.SELinuxEnabled = false

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
	} else if !goselinux.GetEnabled() {
		logger.Warnf("SELinux support has been disabled because of lack of kernel support")
		dbWarnings = append(dbWarnings, cluster.Warning{
			TypeCode:    warningtype.SELinuxNotAvailable,
			LastMessage: "Disabled because of lack of kernel support",
		})
		return dbWarnings
	}

	label, err := goselinux.CurrentLabel()
	if err != nil {
		logger.Warn("Failed to get current SELinux label", logger.Ctx{"error": err})
		return dbWarnings
	}

	ctx, err := goselinux.NewContext(label)
	if err != nil {
		logger.Warn("SELinux disabled: failed to parse daemon label", logger.Ctx{"label": label, "error": err})
		return dbWarnings
	}

	// Map daemon type to instance types.
	switch ctx["type"] {
	case "container_runtime_t":
		s.SELinuxContainerType = "spc_t"
		s.SELinuxVMType = "svirt_t"
		logger.Debug("Setting SELinux instance contexts based on incusd context", logger.Ctx{"incusd": ctx["type"], "lxc": s.SELinuxContainerType, "qemu": s.SELinuxVMType})
	case "incusd_t":
		s.SELinuxContainerType = "container_init_t"
		s.SELinuxVMType = "qemu_t"
		logger.Debug("Setting SELinux instance contexts based on incusd context", logger.Ctx{"incusd": ctx["type"], "lxc": s.SELinuxContainerType, "qemu": s.SELinuxVMType})
	default:
		logger.Warn("SELinux daemon type not supported for instance confinement, disabling SELinux for instances", logger.Ctx{"type": ctx["type"]})
		return dbWarnings
	}

	s.SELinuxContextDaemon = label
	s.SELinuxEnabled = true

	logger.Info("SELinux support enabled", logger.Ctx{
		"label":         label,
		"containerType": s.SELinuxContainerType,
		"vmType":        s.SELinuxVMType,
		"mlsEnabled":    goselinux.MLSEnabled(),
	})

	return dbWarnings
}
