package drivers

import (
	"fmt"

	"github.com/lxc/incus/internal/revert"
	"github.com/lxc/incus/internal/server/storage/quota"
	"github.com/lxc/incus/shared/logger"
	"github.com/lxc/incus/shared/units"
)

// withoutGetVolID returns a copy of this struct but with a volIDFunc which will cause quotas to be skipped.
func (d *dir) withoutGetVolID() Driver {
	newDriver := &dir{}
	getVolID := func(volType VolumeType, volName string) (int64, error) { return volIDQuotaSkip, nil }
	newDriver.init(d.state, d.name, d.config, d.logger, getVolID, d.commonRules)
	_ = newDriver.load()

	return newDriver
}

// setupInitialQuota enables quota on a new volume and sets with an initial quota from config.
// Returns a revert fail function that can be used to undo this function if a subsequent step fails.
func (d *dir) setupInitialQuota(vol Volume) (revert.Hook, error) {
	if vol.IsVMBlock() {
		return nil, nil
	}

	volPath := vol.MountPath()

	// Get the volume ID for the new volume, which is used to set project quota.
	volID, err := d.getVolID(vol.volType, vol.name)
	if err != nil {
		return nil, err
	}

	revert := revert.New()
	defer revert.Fail()

	// Define a function to revert the quota being setup.
	revertFunc := func() { _ = d.deleteQuota(volPath, volID) }
	revert.Add(revertFunc)

	// Initialize the volume's project using the volume ID and set the quota.
	sizeBytes, err := units.ParseByteSizeString(vol.ConfigSize())
	if err != nil {
		return nil, err
	}

	err = d.setQuota(volPath, volID, sizeBytes)
	if err != nil {
		return nil, err
	}

	revert.Success()
	return revertFunc, nil
}

// deleteQuota removes the project quota for a volID from a path.
func (d *dir) deleteQuota(path string, volID int64) error {
	if volID == volIDQuotaSkip {
		// Disabled on purpose, just ignore
		return nil
	}

	if volID == 0 {
		return fmt.Errorf("Missing volume ID")
	}

	ok, err := quota.Supported(path)
	if err != nil || !ok {
		// Skipping quota as underlying filesystem doesn't support project quotas.
		return nil
	}

	err = quota.DeleteProject(path, d.quotaProjectID(volID))
	if err != nil {
		return err
	}

	return nil
}

// quotaProjectID generates a project quota ID from a volume ID.
func (d *dir) quotaProjectID(volID int64) uint32 {
	if volID == volIDQuotaSkip {
		// Disabled on purpose, just ignore
		return 0
	}

	return uint32(volID + 10000)
}

// setQuota sets the project quota on the path. The volID generates a quota project ID.
func (d *dir) setQuota(path string, volID int64, sizeBytes int64) error {
	if volID == volIDQuotaSkip {
		// Disabled on purpose, just ignore.
		return nil
	}

	if volID == 0 {
		return fmt.Errorf("Missing volume ID")
	}

	ok, err := quota.Supported(path)
	if err != nil || !ok {
		if sizeBytes > 0 {
			// Skipping quota as underlying filesystem doesn't support project quotas.
			d.logger.Warn("The backing filesystem doesn't support quotas, skipping set quota", logger.Ctx{"path": path, "size": sizeBytes, "volID": volID})
		}

		return nil
	}

	projectID := d.quotaProjectID(volID)
	currentProjectID, err := quota.GetProject(path)
	if err != nil {
		return err
	}

	// Remove current project if desired project ID is different.
	if currentProjectID != d.quotaProjectID(volID) {
		err = quota.DeleteProject(path, currentProjectID)
		if err != nil {
			return err
		}
	}

	// Initialize the project.
	err = quota.SetProject(path, projectID)
	if err != nil {
		return err
	}

	// Set the project quota size.
	return quota.SetProjectQuota(path, projectID, sizeBytes)
}
