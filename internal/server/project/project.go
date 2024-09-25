package project

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/util"
)

// separator is used to delimit the project name from the suffix.
const separator = "_"

// projectLimitDiskPool is the prefix used for pool-specific disk limits.
var projectLimitDiskPool = "limits.disk.pool."

// Instance adds the "<project>_" prefix to instance name when the given project name is not "default".
func Instance(projectName string, instanceName string) string {
	if projectName != api.ProjectDefaultName {
		return fmt.Sprintf("%s%s%s", projectName, separator, instanceName)
	}

	return instanceName
}

// DNS adds ".<project>" as a suffix to instance name when the given project name is not "default".
func DNS(projectName string, instanceName string) string {
	if projectName != api.ProjectDefaultName {
		return fmt.Sprintf("%s.%s", instanceName, projectName)
	}

	return instanceName
}

// InstanceParts takes a project prefixed Instance name string and returns the project and instance name.
// If a non-project prefixed Instance name is supplied, then the project is returned as "default" and the instance
// name is returned unmodified in the 2nd return value. This is suitable for passing back into Instance().
// Note: This should only be used with Instance names (because they cannot contain the project separator) and this
// function relies on this rule as project names can contain the project separator.
func InstanceParts(projectInstanceName string) (string, string) {
	i := strings.LastIndex(projectInstanceName, separator)
	if i < 0 {
		// This string is not project prefixed or is part of default project.
		return api.ProjectDefaultName, projectInstanceName
	}

	// As project names can container separator, we effectively split once from the right hand side as
	// Instance names are not allowed to container the separator value.
	return projectInstanceName[0:i], projectInstanceName[i+1:]
}

// StorageVolume adds the "<project>_prefix" to the storage volume name. Even if the project name is "default".
func StorageVolume(projectName string, storageVolumeName string) string {
	return fmt.Sprintf("%s%s%s", projectName, separator, storageVolumeName)
}

// StorageVolumeParts takes a project prefixed storage volume name and returns the project and storage volume
// name as separate variables.
func StorageVolumeParts(projectStorageVolumeName string) (string, string) {
	parts := strings.SplitN(projectStorageVolumeName, "_", 2)

	// If the given name doesn't contain any project, only return the volume name.
	if len(parts) == 1 {
		return "", projectStorageVolumeName
	}

	return parts[0], parts[1]
}

// StorageVolumeProject returns the project name to use to for the volume based on the requested project.
// For image volume types the default project is always returned.
// For custom volume type, if the project specified has the "features.storage.volumes" flag enabled then the
// project name is returned, otherwise the default project name is returned.
// For all other volume types the supplied project name is returned.
func StorageVolumeProject(c *db.Cluster, projectName string, volumeType int) (string, error) {
	// Image volumes are effectively a cache and so are always linked to default project.
	// Optimisation to avoid loading project record.
	if volumeType == db.StoragePoolVolumeTypeImage {
		return api.ProjectDefaultName, nil
	}

	var project *api.Project
	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbProject, err := cluster.GetProject(ctx, tx.Tx(), projectName)
		if err != nil {
			return err
		}

		project, err = dbProject.ToAPI(ctx, tx.Tx())

		return err
	})
	if err != nil {
		return "", fmt.Errorf("Failed to load project %q: %w", projectName, err)
	}

	return StorageVolumeProjectFromRecord(project, volumeType), nil
}

// StorageVolumeProjectFromRecord returns the project name to use to for the volume based on the supplied project.
// For image volume types the default project is always returned.
// For custom volume type, if the project supplied has the "features.storage.volumes" flag enabled then the
// project name is returned, otherwise the default project name is returned.
// For all other volume types the supplied project's name is returned.
func StorageVolumeProjectFromRecord(p *api.Project, volumeType int) string {
	// Image volumes are effectively a cache and so are always linked to default project.
	if volumeType == db.StoragePoolVolumeTypeImage {
		return api.ProjectDefaultName
	}

	// Non-custom volumes always use the project specified.
	if volumeType != db.StoragePoolVolumeTypeCustom {
		return p.Name
	}

	// Custom volumes only use the project specified if the project has the features.storage.volumes feature
	// enabled, otherwise the legacy behaviour of using the default project for custom volumes is used.
	if util.IsTrue(p.Config["features.storage.volumes"]) {
		return p.Name
	}

	return api.ProjectDefaultName
}

// StorageBucket adds the "<project>_prefix" to the storage bucket name. Even if the project name is "default".
func StorageBucket(projectName string, storageBucketName string) string {
	return fmt.Sprintf("%s%s%s", projectName, separator, storageBucketName)
}

// StorageBucketProject returns the effective project name to use to for the bucket based on the requested project.
// If the project specified has the "features.storage.buckets" flag enabled then the project name is returned,
// otherwise the default project name is returned.
func StorageBucketProject(ctx context.Context, c *db.Cluster, projectName string) (string, error) {
	var p *api.Project
	err := c.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		dbProject, err := cluster.GetProject(ctx, tx.Tx(), projectName)
		if err != nil {
			return err
		}

		p, err = dbProject.ToAPI(ctx, tx.Tx())

		return err
	})
	if err != nil {
		return "", fmt.Errorf("Failed to load project %q: %w", projectName, err)
	}

	return StorageBucketProjectFromRecord(p), nil
}

// StorageBucketProjectFromRecord returns the project name to use to for the bucket based on the supplied project.
// If the project supplied has the "features.storage.buckets" flag enabled then the project name is returned,
// otherwise the default project name is returned.
func StorageBucketProjectFromRecord(p *api.Project) string {
	// Buckets only use the project specified if the project has the features.storage.buckets feature
	// enabled, otherwise the default project is used.
	if util.IsTrue(p.Config["features.storage.buckets"]) {
		return p.Name
	}

	return api.ProjectDefaultName
}

// NetworkProject returns the effective project name to use for the network based on the requested project.
// If the requested project has the "features.networks" flag enabled then the requested project's name is returned,
// otherwise the default project name is returned.
// The second return value is always the requested project's info.
func NetworkProject(c *db.Cluster, projectName string) (string, *api.Project, error) {
	var p *api.Project
	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbProject, err := cluster.GetProject(ctx, tx.Tx(), projectName)
		if err != nil {
			return err
		}

		p, err = dbProject.ToAPI(ctx, tx.Tx())

		return err
	})
	if err != nil {
		return "", nil, fmt.Errorf("Failed to load project %q: %w", projectName, err)
	}

	effectiveProjectName := NetworkProjectFromRecord(p)

	return effectiveProjectName, p, nil
}

// NetworkProjectFromRecord returns the project name to use for the network based on the supplied project.
// If the project supplied has the "features.networks" flag enabled then the project name is returned,
// otherwise the default project name is returned.
func NetworkProjectFromRecord(p *api.Project) string {
	// Networks only use the project specified if the project has the features.networks feature enabled,
	// otherwise the legacy behaviour of using the default project for networks is used.
	if util.IsTrue(p.Config["features.networks"]) {
		return p.Name
	}

	return api.ProjectDefaultName
}

// NetworkAllowed returns whether access is allowed to a particular network based on projectConfig.
func NetworkAllowed(reqProjectConfig map[string]string, networkName string, isManaged bool) bool {
	// If project is not restricted, then access to network is allowed.
	if util.IsFalseOrEmpty(reqProjectConfig["restricted"]) {
		return true
	}

	// If project has no access to NIC devices then also block access to all networks.
	if reqProjectConfig["restricted.devices.nic"] == "block" {
		return false
	}

	// Don't allow access to unmanaged networks if only managed network access is allowed.
	if slices.Contains([]string{"managed", ""}, reqProjectConfig["restricted.devices.nic"]) && !isManaged {
		return false
	}

	// If restricted.networks.access is not set then allow access to all networks.
	if reqProjectConfig["restricted.networks.access"] == "" {
		return true
	}

	// Check if reqquested network is in list of allowed networks.
	allowedRestrictedNetworks := util.SplitNTrimSpace(reqProjectConfig["restricted.networks.access"], ",", -1, false)
	return slices.Contains(allowedRestrictedNetworks, networkName)
}

// NetworkIntegrationAllowed returns whether access is allowed for a particular network integration based on projectConfig.
func NetworkIntegrationAllowed(reqProjectConfig map[string]string, integrationName string) bool {
	// If project is not restricted, then access to network is allowed.
	if util.IsFalseOrEmpty(reqProjectConfig["restricted"]) {
		return true
	}

	// If restricted.networks.integrations is not set then allow access to all networks.
	if reqProjectConfig["restricted.networks.integrations"] == "" {
		return true
	}

	// Check if reqquested integration is in list of allowed network integrations.
	allowedRestrictedIntegrations := util.SplitNTrimSpace(reqProjectConfig["restricted.networks.integrations"], ",", -1, false)
	return slices.Contains(allowedRestrictedIntegrations, integrationName)
}

// ImageProjectFromRecord returns the project name to use for the image based on the supplied project.
// If the project supplied has the "features.images" flag enabled then the project name is returned,
// otherwise the default project name is returned.
func ImageProjectFromRecord(p *api.Project) string {
	// Images only use the project specified if the project has the features.images feature enabled,
	// otherwise the default project for profiles is used.
	if util.IsTrue(p.Config["features.images"]) {
		return p.Name
	}

	return api.ProjectDefaultName
}

// ProfileProject returns the effective project to use for the profile based on the requested project.
// If the requested project has the "features.profiles" flag enabled then the requested project's info is returned,
// otherwise the default project's info is returned.
func ProfileProject(c *db.Cluster, projectName string) (*api.Project, error) {
	var p *api.Project
	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbProject, err := cluster.GetProject(ctx, tx.Tx(), projectName)
		if err != nil {
			return fmt.Errorf("Failed loading project %q: %w", projectName, err)
		}

		p, err = dbProject.ToAPI(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed loading config for project %q: %w", projectName, err)
		}

		effectiveProjectName := ProfileProjectFromRecord(p)

		if effectiveProjectName == api.ProjectDefaultName {
			dbProject, err = cluster.GetProject(ctx, tx.Tx(), effectiveProjectName)
			if err != nil {
				return fmt.Errorf("Failed loading project %q: %w", effectiveProjectName, err)
			}
		}

		p, err = dbProject.ToAPI(ctx, tx.Tx())
		if err != nil {
			return fmt.Errorf("Failed loading config for project %q: %w", dbProject.Name, err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return p, nil
}

// ProfileProjectFromRecord returns the project name to use for the profile based on the supplied project.
// If the project supplied has the "features.profiles" flag enabled then the project name is returned,
// otherwise the default project name is returned.
func ProfileProjectFromRecord(p *api.Project) string {
	// Profiles only use the project specified if the project has the features.profiles feature enabled,
	// otherwise the default project for profiles is used.
	if util.IsTrue(p.Config["features.profiles"]) {
		return p.Name
	}

	return api.ProjectDefaultName
}

// NetworkZoneProject returns the effective project name to use for network zone based on the requested project.
// If the requested project has the "features.networks.zones" flag enabled then the requested project's name is
// returned, otherwise the default project name is returned.
// The second return value is always the requested project's info.
func NetworkZoneProject(c *db.Cluster, projectName string) (string, *api.Project, error) {
	var p *api.Project
	err := c.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		dbProject, err := cluster.GetProject(ctx, tx.Tx(), projectName)
		if err != nil {
			return err
		}

		p, err = dbProject.ToAPI(ctx, tx.Tx())

		return err
	})
	if err != nil {
		return "", nil, fmt.Errorf("Failed to load project %q: %w", projectName, err)
	}

	effectiveProjectName := NetworkZoneProjectFromRecord(p)

	return effectiveProjectName, p, nil
}

// NetworkZoneProjectFromRecord returns the project name to use for the network zone based on the supplied project.
// If the project supplied has the "features.networks.zones" flag enabled then the project name is returned,
// otherwise the default project name is returned.
func NetworkZoneProjectFromRecord(p *api.Project) string {
	// Network zones only use the project specified if the project has the features.networks.zones feature
	// enabled, otherwise the legacy behaviour of using the default project for network zones is used.
	if util.IsTrue(p.Config["features.networks.zones"]) {
		return p.Name
	}

	return api.ProjectDefaultName
}
