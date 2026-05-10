//go:build linux && cgo && !agent

package selinux

import (
	"fmt"
	"sync"

	goselinux "github.com/opencontainers/selinux/go-selinux"

	"github.com/lxc/incus/v7/shared/logger"
)

// allocMu is used to serialize SELinux MCS level allocation across
// concurrently starting instances, so collision detection against
// already-used levels stays race-free.
var allocMu sync.Mutex

const AllocMaxAttempts = 16

// AllocateLevel returns a SELinux MCS level that does not collide
// with any level currently in use by other instances on this host. It
// delegates generation to the go-selinux package and retries on
// collision.
func AllocateLevel(used map[string]struct{}) (string, func(), error) {
	allocMu.Lock()

	for i := 0; i < AllocMaxAttempts; i++ {
		label, _ := goselinux.InitContainerLabels()
		if label == "" {
			allocMu.Unlock()
			return "", nil, fmt.Errorf("Failed to allocate SELinux label (empty process label returned)")
		}

		parsed, err := goselinux.NewContext(label)
		if err != nil {
			allocMu.Unlock()
			return "", nil, fmt.Errorf("Failed to parse allocated SELinux label %q: %w", label, err)
		}

		level := parsed["level"]
		if level == "" {
			allocMu.Unlock()
			return "", nil, fmt.Errorf("Allocated SELinux label %q has empty level", label)
		}

		_, clash := used[level]
		if !clash {
			goselinux.ReleaseLabel(label)
			return level, allocMu.Unlock, nil
		}

		logger.Debug("SELinux level collision, retrying", logger.Ctx{"level": level, "attempt": i + 1})
	}

	allocMu.Unlock()
	return "", nil, fmt.Errorf("Failed to allocate collision-free SELinux level after %d attempts", AllocMaxAttempts)
}

// UsedLevels extracts the set of MCS levels currently in use, given a
// slice of instance expanded configs. The caller is responsible for
// providing the configs from the database.
func UsedLevels(configs []map[string]string) map[string]struct{} {
	used := make(map[string]struct{}, len(configs))

	for _, cfg := range configs {

		// Explicit per-instance override (takes precedence).
		if lvl := cfg["security.selinux.level"]; lvl != "" {
			used[lvl] = struct{}{}
			continue
		}

		// Previously persisted context (running or stopped instance).
		if vc := cfg["volatile.selinux.context"]; vc != "" {
			if parsed, err := goselinux.NewContext(vc); err == nil {
				if lvl := parsed["level"]; lvl != "" {
					used[lvl] = struct{}{}
				}
			}
		}
	}

	return used
}
