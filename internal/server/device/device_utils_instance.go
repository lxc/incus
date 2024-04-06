package device

import (
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
)

// instanceSupported is a helper function to check instance type is supported for validation.
// Always returns true if supplied instance type is Any, to support profile validation.
func instanceSupported(instType instancetype.Type, supportedTypes ...instancetype.Type) bool {
	// If instance type is Any, then profile validation is occurring and we need to support this.
	if instType == instancetype.Any {
		return true
	}

	for _, supportedType := range supportedTypes {
		if instType == supportedType {
			return true
		}
	}

	return false
}
