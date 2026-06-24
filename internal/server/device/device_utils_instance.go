package device

import (
	"path/filepath"
	"slices"

	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
	"github.com/lxc/incus/v7/shared/util"
)

// instanceSupported is a helper function to check instance type is supported for validation.
// Always returns true if supplied instance type is Any, to support profile validation.
func instanceSupported(instType instancetype.Type, supportedTypes ...instancetype.Type) bool {
	// If instance type is Any, then profile validation is occurring and we need to support this.
	if instType == instancetype.Any {
		return true
	}

	return slices.Contains(supportedTypes, instType)
}

// instanceIsOCI reports whether the instance is an OCI application container.
func instanceIsOCI(inst instance.Instance) bool {
	if inst == nil {
		return false
	}

	// Rely on the volatile marker when set, falling back to the on-disk OCI config in case it's missing.
	if util.IsTrue(inst.ExpandedConfig()["volatile.container.oci"]) {
		return true
	}

	return util.PathExists(filepath.Join(inst.Path(), "config.json"))
}
