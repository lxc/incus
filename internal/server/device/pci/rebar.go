package pci

import (
	"fmt"
	"math/bits"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/lxc/incus/v7/shared/units"
	"github.com/lxc/incus/v7/shared/util"
)

// DeviceBARSizes returns the current size of each resizable BAR of a PCI device.
func DeviceBARSizes(slotName string) (map[int]int64, error) {
	devicePath := filepath.Join("/sys/bus/pci/devices", slotName)

	content, err := os.ReadFile(filepath.Join(devicePath, "resource"))
	if err != nil {
		return nil, fmt.Errorf("Failed reading resources of device %q: %w", slotName, err)
	}

	sizes := map[int]int64{}
	for bar, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		if bar > 5 {
			break
		}

		// Only BARs with a Resizable BAR control register get a resize file.
		if !util.PathExists(filepath.Join(devicePath, fmt.Sprintf("resource%d_resize", bar))) {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		start, err := strconv.ParseUint(fields[0], 0, 64)
		if err != nil {
			continue
		}

		end, err := strconv.ParseUint(fields[1], 0, 64)
		if err != nil || end <= start {
			continue
		}

		sizes[bar] = int64(end - start + 1)
	}

	return sizes, nil
}

// DeviceBARSupportedSizes returns the sizes a resizable BAR of a PCI device can be set to.
func DeviceBARSupportedSizes(slotName string, bar int) ([]int64, error) {
	resizePath := filepath.Join("/sys/bus/pci/devices", slotName, fmt.Sprintf("resource%d_resize", bar))

	content, err := os.ReadFile(resizePath)
	if err != nil {
		return nil, fmt.Errorf("Failed reading %q: %w", resizePath, err)
	}

	// The file holds a bitmask of supported sizes, bit 0 being 1MiB.
	mask, err := strconv.ParseUint(strings.TrimSpace(string(content)), 16, 64)
	if err != nil {
		return nil, fmt.Errorf("Failed parsing %q: %w", resizePath, err)
	}

	sizes := []int64{}
	for bit := range 32 {
		if mask&(1<<bit) != 0 {
			sizes = append(sizes, int64(1)<<(bit+20))
		}
	}

	return sizes, nil
}

// DeviceBARResize sets the size of a resizable BAR, the device must not be bound to a driver.
func DeviceBARResize(slotName string, bar int, size int64) error {
	supported, err := DeviceBARSupportedSizes(slotName, bar)
	if err != nil {
		return err
	}

	if !slices.Contains(supported, size) {
		names := make([]string, 0, len(supported))
		for _, supportedSize := range supported {
			names = append(names, units.GetByteSizeStringIEC(supportedSize, 0))
		}

		return fmt.Errorf("BAR %d of device %q cannot be set to %s (supported sizes: %s)", bar, slotName, units.GetByteSizeStringIEC(size, 0), strings.Join(names, ", "))
	}

	// The kernel takes the encoded size, 0 being 1MiB and each step doubling it.
	encoded := bits.TrailingZeros64(uint64(size)) - 20

	resizePath := filepath.Join("/sys/bus/pci/devices", slotName, fmt.Sprintf("resource%d_resize", bar))
	err = os.WriteFile(resizePath, []byte(strconv.Itoa(encoded)), 0o600)
	if err != nil {
		return fmt.Errorf("Failed resizing BAR %d of device %q via %q: %w", bar, slotName, resizePath, err)
	}

	return nil
}
