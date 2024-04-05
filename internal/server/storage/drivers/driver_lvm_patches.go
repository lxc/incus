package drivers

import (
	"fmt"
	"strings"

	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/subprocess"
)

// patchStorageSkipActivation set skipactivation=y on all Incus LVM logical volumes (excluding thin pool volumes).
func (d *lvm) patchStorageSkipActivation() error {
	out, err := subprocess.RunCommand("lvs", "--noheadings", "-o", "lv_name,lv_attr", d.config["lvm.vg_name"])
	if err != nil {
		return fmt.Errorf("Error getting LVM logical volume list for storage pool %q: %w", d.config["lvm.vg_name"], err)
	}

	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 {
			continue
		}

		volName := fields[0]
		volAttr := fields[1]

		// Ignore non-Incus prefixes, and thinpool volumes (these should remain auto activated).
		if !strings.HasPrefix(volName, "images_") && !strings.HasPrefix(volName, "containers_") && !strings.HasPrefix(volName, "virtual-machines_") && !strings.HasPrefix(volName, "custom_") {
			continue
		}

		// Skip volumes that already have k flag set, meaning setactivationskip=y.
		if strings.HasSuffix(volAttr, "k") {
			logger.Infof("Skipping volume %q that already has skipactivation=y set in pool %q", volName, d.config["lvm.vg_name"])
			continue
		}

		// Set the --setactivationskip flag enabled on the volume.
		_, err = subprocess.RunCommand("lvchange", "--setactivationskip", "y", fmt.Sprintf("%s/%s", d.config["lvm.vg_name"], volName))
		if err != nil {
			return fmt.Errorf("Error setting setactivationskip=y on LVM logical volume %q for storage pool %q: %w", volName, d.config["lvm.vg_name"], err)
		}

		logger.Infof("Set setactivationskip=y on volume %q in pool %q", volName, d.config["lvm.vg_name"])
	}

	return nil
}
