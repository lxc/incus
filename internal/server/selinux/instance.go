//go:build linux && cgo && !agent

package selinux

import (
	"fmt"

	goselinux "github.com/opencontainers/selinux/go-selinux"

	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/internal/server/sys"
	"github.com/lxc/incus/v7/shared/logger"
)

// InstanceContext derives the SELinux process context for an instance.
//
// allocLevel is invoked only when a fresh MCS level must be generated
// (i.e. no explicit override via security.selinux.level and no previously
// persisted volatile.selinux.context). The caller is responsible for
// ensuring collision-freeness against other instances on this host.
func InstanceContext(s *sys.OS, instType instancetype.Type, localConfig map[string]string, expandedConfig map[string]string, allocLevel func() (string, func(), error)) (string, bool, func(), error) {
	if !s.SELinuxEnabled {
		return "", false, nil, nil
	}

	release := func() {}

	daemonCtx, err := goselinux.NewContext(s.SELinuxContextDaemon)
	if err != nil {
		return "", false, release, fmt.Errorf("Failed to parse daemon SELinux label %q: %w", s.SELinuxContextDaemon, err)
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
			return "", false, release, fmt.Errorf("Unsupported instance type for SELinux context")
		}
	}

	// Resolve MCS level: explicit override -> cached volatile -> auto-generated.
	var seLevel string

	localLvl := localConfig["security.selinux.level"]
	localCtx := localConfig["volatile.selinux.context"]
	if localLvl != "" {
		// Explicit override (user provides full level, e.g. "s0:c100,c200").
		seLevel = localLvl
	} else if localCtx != "" {
		// Reuse level from previously persisted context.
		ctxCached, err := goselinux.NewContext(localCtx)
		if err != nil {
			return "", false, release, fmt.Errorf("Failed to parse cached SELinux context %q: %w", localCtx, err)
		}

		seLevel = ctxCached["level"]
	} else if goselinux.MLSEnabled() {
		// Allocate new random level.
		if allocLevel == nil {
			return "", false, release, fmt.Errorf("SELinux: no level allocator provided")
		}

		var lvl string
		lvl, release, err = allocLevel()
		if err != nil {
			return "", false, release, fmt.Errorf("SELinux: failed to allocate level: %w", err)
		}

		seLevel = lvl
	} else {
		return "", false, release, fmt.Errorf("SELinux MLS not enabled on this system or could not access selinuxfs")
	}

	ctx := fmt.Sprintf("%s:%s:%s:%s", daemonCtx["user"], daemonCtx["role"], seDomain, seLevel)

	logger.Debug("Resolved SELinux context", logger.Ctx{"context": ctx})

	// Persist when context differs from cached volatile.
	needsPersist := localCtx != ctx

	return ctx, needsPersist, release, nil
}

// InstanceFileContext derives a file context from an instance process context and type.
func InstanceFileContext(processCtx string, instType instancetype.Type, expandedConfig map[string]string) string {
	ctx, err := goselinux.NewContext(processCtx)
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
