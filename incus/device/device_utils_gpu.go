package device

import (
	"strings"

	"github.com/cyphar/incus/shared/validate"
)

// gpuValidMigUUID validates Nvidia MIG (Multi Instance GPU) UUID with or without "MIG-" prefix.
func gpuValidMigUUID(value string) error {
	if value == "" {
		return nil
	}

	return validate.IsUUID(strings.TrimPrefix(value, "MIG-"))
}
