//go:build linux && cgo && !agent

package sys

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/server/db/warningtype"

	"golang.org/x/sys/unix"

	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/util"
	"github.com/opencontainers/selinux/go-selinux"
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
	} else if !selinux.GetEnabled() {
		logger.Warnf("SELinux support has been disabled because of lack of kernel support")
		dbWarnings = append(dbWarnings, cluster.Warning{
			TypeCode:    warningtype.SELinuxNotAvailable,
			LastMessage: "Disabled because of lack of kernel support",
		})
		return dbWarnings
	}

	label, err := selinux.CurrentLabel()
	if err != nil {
		logger.Warn("Failed to get current SELinux label", logger.Ctx{"error": err})
		return dbWarnings
	}

	ctx, err := selinux.NewContext(label)
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
		"mlsEnabled":    selinux.MLSEnabled(),
	})

	return dbWarnings
}

// SELinuxInstanceFileContext derives a file context from an instance process context and type.
func SELinuxInstanceFileContext(processCtx string, instType instancetype.Type, expandedConfig map[string]string) string {
	ctx, err := selinux.NewContext(processCtx)
	if err != nil {
		logger.Warn("Failed to parse process label for file context", logger.Ctx{"label": processCtx, "error": err})
		return ""
	}

	seType := expandedConfig["security.selinux.type"]
	if seType == "" {
		seType = "container_file_t"
		if instType == instancetype.VM {
			seType = "qemu_image_t"
		}
	}

	return fmt.Sprintf("%s:%s:%s:%s", ctx["user"], "object_r", seType, ctx["level"])
}

// SELinuxLabelTree recursively labels all entries under path with the given SELinux label without crossing filesystem boundaries.
func SELinuxLabelTree(path string, label string, skipPath string) error {
	var rootStat unix.Stat_t

	if path == "" {
		return fmt.Errorf("Path is empty: %q", path)
	}
	if label == "" {
		return fmt.Errorf("Label is empty: %q", label)
	}

	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("Failed to resolve rootfs symlink for SELinux labeling: %q: %w", path, err)
	}

	err = unix.Lstat(target, &rootStat)
	if err != nil {
		return fmt.Errorf("Failed to stat SELinux label root %q: %w", target, err)
	}
	rootDev := rootStat.Dev

	logger.Debug("SELinux: Labeling instance path", logger.Ctx{"path": target, "skipPath": skipPath})

	return filepath.WalkDir(target, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("SELinux label tree walk error: %q: %w", p, err)
		}

		if skipPath != "" && strings.HasPrefix(p, skipPath) {
			return nil
		}

		if skipPath != "" {
			rel, _ := filepath.Rel(skipPath, p)
			if rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel)) {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
		}

		// Don't cross filesystem boundaries.
		var dirStat unix.Stat_t
		if statErr := unix.Lstat(p, &dirStat); statErr != nil {
			return fmt.Errorf("Failed to stat %q: %w", p, statErr)
		}
		if dirStat.Dev != rootDev {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		labelErr := selinux.LsetFileLabel(p, label)
		if labelErr != nil {
			return fmt.Errorf("Failed to set SELinux label on %q: %w", p, labelErr)
		}

		return nil
	})
}

// SELinuxInstanceContext derives the SELinux process context for an instance.
//
// allocLevel is invoked only when a fresh MCS level must be generated
// (i.e. no explicit override via security.selinux.level and no previously
// persisted volatile.selinux.context). The caller is responsible for
// ensuring collision-freeness against other instances on this host.
func (s *OS) SELinuxInstanceContext(instType instancetype.Type, localConfig map[string]string, expandedConfig map[string]string, allocLevel func() (string, error)) (string, bool, error) {
	if !s.SELinuxEnabled {
		return "", false, nil
	}

	daemonCtx, err := selinux.NewContext(s.SELinuxContextDaemon)
	if err != nil {
		return "", false, fmt.Errorf("Failed to parse daemon SELinux label %q: %w", s.SELinuxContextDaemon, err)
	}

	// Resolve type: explicit override -> auto-detected.
	seDomain := expandedConfig["security.selinux.domain"]
	if seDomain == "" {
		switch instType {
		case instancetype.Container:
			seDomain = s.SELinuxContainerType
		case instancetype.VM:
			seDomain = s.SELinuxVMType
		default:
			return "", false, fmt.Errorf("Unsupported instance type for SELinux context")
		}
	}

	// Resolve MCS level: explicit override -> cached volatile -> auto-generated.
	seLevel := "s0"

	if cl := localConfig["security.selinux.level"]; cl != "" {
		// Explicit override (user provides full level, e.g. "s0:c100,c200").
		seLevel = cl
	} else if vc := localConfig["volatile.selinux.context"]; vc != "" {
		// Reuse level from previously persisted context.
		ctxCached, err := selinux.NewContext(vc)
		if err != nil {
			return "", false, fmt.Errorf("Failed to parse cached SELinux context %q: %w", vc, err)
		}
		seLevel = ctxCached["level"]
	} else if selinux.MLSEnabled() {
		if allocLevel == nil {
			return "", false, fmt.Errorf("SELinux: no level allocator provided")
		}
		lvl, err := allocLevel()
		if err != nil {
			return "", false, fmt.Errorf("SELinux: failed to allocate level: %w", err)
		}
		seLevel = lvl
	} else {
		logger.Warn("SELinux MLS not enabled on this system or could not access selinuxfs")
	}

	ctx := fmt.Sprintf("%s:%s:%s:%s", daemonCtx["user"], daemonCtx["role"], seDomain, seLevel)

	logger.Debug("Resolved SELinux context", logger.Ctx{"context": ctx})

	// Persist when context differs from cached volatile.
	needsPersist := localConfig["volatile.selinux.context"] != ctx

	return ctx, needsPersist, nil
}

// SELinuxSetExecContext sets the SELinux transition context for the next exec on the current thread.
// Returns a cleanup function that must always be called to clear the context and release the thread lock.
func SELinuxSetExecContext(ctx string) (func(), error) {
	runtime.LockOSThread()

	err := selinux.SetExecLabel(ctx)
	if err != nil {
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("Failed to set SELinux exec context: %w", err)
	}

	cleanup := func() {
		_ = selinux.SetExecLabel("")
		runtime.UnlockOSThread()
	}

	return cleanup, nil
}
