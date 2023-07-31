package device

import (
	"fmt"
	"strings"

	"github.com/cyphar/incus/shared"
)

// deviceJoinPath joins together prefix and text delimited by a "." for device path generation.
func deviceJoinPath(parts ...string) string {
	return strings.Join(parts, ".")
}

// validatePCIDevice returns whether a configured PCI device exists under the given address.
// It also returns nil, if an empty address is supplied.
func validatePCIDevice(address string) error {
	if address != "" && !shared.PathExists(fmt.Sprintf("/sys/bus/pci/devices/%s", address)) {
		return fmt.Errorf("Invalid PCI address (no device found): %s", address)
	}

	return nil
}
