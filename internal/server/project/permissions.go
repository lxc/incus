package project

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lxc/incus/internal/instance"
	"github.com/lxc/incus/internal/server/auth"
	"github.com/lxc/incus/internal/server/db"
	"github.com/lxc/incus/internal/server/db/cluster"
	deviceconfig "github.com/lxc/incus/internal/server/device/config"
	"github.com/lxc/incus/internal/server/instance/instancetype"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/idmap"
	"github.com/lxc/incus/shared/units"
	"github.com/lxc/incus/shared/util"
	"github.com/lxc/incus/shared/validate"
)

// AllowInstanceCreation returns an error if any project-specific limit or
// restriction is violated when creating a new instance.
func AllowInstanceCreation(tx *db.ClusterTx, projectName string, req api.InstancesPost) error {
	info, err := fetchProject(tx, projectName, true)
	if err != nil {
		return err
	}

	if info == nil {
		return nil
	}

	var instanceType instancetype.Type
	switch req.Type {
	case api.InstanceTypeContainer:
		instanceType = instancetype.Container
	case api.InstanceTypeVM:
		instanceType = instancetype.VM
	default:
		return fmt.Errorf("Unexpected instance type %q", req.Type)
	}

	if req.Profiles == nil {
		req.Profiles = []string{"default"}
	}

	err = checkInstanceCountLimit(info, instanceType)
	if err != nil {
		return err
	}

	err = checkTotalInstanceCountLimit(info)
	if err != nil {
		return err
	}

	// Add the instance being created.
	info.Instances = append(info.Instances, api.Instance{
		Name:        req.Name,
		Project:     projectName,
		InstancePut: req.InstancePut,
	})

	// Special case restriction checks on volatile.* keys.
	strip := false

	if util.ValueInSlice(req.Source.Type, []string{"copy", "migration"}) {
		// Allow stripping volatile keys if dealing with a copy or migration.
		strip = true
	}

	err = checkRestrictionsOnVolatileConfig(
		info.Project, instanceType, req.Name, req.Config, map[string]string{}, strip)
	if err != nil {
		return err
	}

	err = checkRestrictionsAndAggregateLimits(tx, info)
	if err != nil {
		return fmt.Errorf("Failed checking if instance creation allowed: %w", err)
	}

	return nil
}

// Check that we have not exceeded the maximum total allotted number of instances for both containers and vms.
func checkTotalInstanceCountLimit(info *projectInfo) error {
	count, limit, err := getTotalInstanceCountLimit(info)
	if err != nil {
		return err
	}

	if limit >= 0 && count >= limit {
		return fmt.Errorf("Reached maximum number of instances in project %q", info.Project.Name)
	}

	return nil
}

func getTotalInstanceCountLimit(info *projectInfo) (int, int, error) {
	overallValue, ok := info.Project.Config["limits.instances"]
	if ok {
		limit, err := strconv.Atoi(overallValue)
		if err != nil {
			return -1, -1, err
		}

		return len(info.Instances), limit, nil
	}

	return len(info.Instances), -1, nil
}

// Check that we have not reached the maximum number of instances for this type.
func checkInstanceCountLimit(info *projectInfo, instanceType instancetype.Type) error {
	count, limit, err := getInstanceCountLimit(info, instanceType)
	if err != nil {
		return err
	}

	if limit >= 0 && count >= limit {
		return fmt.Errorf("Reached maximum number of instances of type %q in project %q", instanceType, info.Project.Name)
	}

	return nil
}

func getInstanceCountLimit(info *projectInfo, instanceType instancetype.Type) (int, int, error) {
	var key string
	switch instanceType {
	case instancetype.Container:
		key = "limits.containers"
	case instancetype.VM:
		key = "limits.virtual-machines"
	default:
		return -1, -1, fmt.Errorf("Unexpected instance type %q", instanceType)
	}

	instanceCount := 0
	for _, inst := range info.Instances {
		if inst.Type == instanceType.String() {
			instanceCount++
		}
	}

	value, ok := info.Project.Config[key]
	if ok {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 0 {
			return -1, -1, fmt.Errorf("Unexpected %q value: %q", key, value)
		}

		return instanceCount, limit, nil
	}

	return instanceCount, -1, nil
}

// Check restrictions on setting volatile.* keys.
func checkRestrictionsOnVolatileConfig(project api.Project, instanceType instancetype.Type, instanceName string, config, currentConfig map[string]string, strip bool) error {
	if project.Config["restrict"] == "false" {
		return nil
	}

	var restrictedLowLevel string
	switch instanceType {
	case instancetype.Container:
		restrictedLowLevel = "restricted.containers.lowlevel"
	case instancetype.VM:
		restrictedLowLevel = "restricted.virtual-machines.lowlevel"
	}

	if project.Config[restrictedLowLevel] == "allow" {
		return nil
	}

	// Checker for safe volatile keys.
	isSafeKey := func(key string) bool {
		if util.ValueInSlice(key, []string{"volatile.apply_template", "volatile.base_image", "volatile.last_state.power"}) {
			return true
		}

		if strings.HasPrefix(key, instance.ConfigVolatilePrefix) {
			if strings.HasSuffix(key, ".apply_quota") {
				return true
			}

			if strings.HasSuffix(key, ".hwaddr") {
				return true
			}
		}

		return false
	}

	for key, value := range config {
		if !strings.HasPrefix(key, instance.ConfigVolatilePrefix) {
			continue
		}

		// Allow given safe volatile keys to be set
		if isSafeKey(key) {
			continue
		}

		if strip {
			delete(config, key)
			continue
		}

		currentValue, ok := currentConfig[key]
		if !ok {
			return fmt.Errorf("Setting %q on %s %q in project %q is forbidden", key, instanceType, instanceName, project.Name)
		}

		if currentValue != value {
			return fmt.Errorf("Changing %q on %s %q in project %q is forbidden", key, instanceType, instanceName, project.Name)
		}
	}

	return nil
}

// AllowVolumeCreation returns an error if any project-specific limit or
// restriction is violated when creating a new custom volume in a project.
func AllowVolumeCreation(tx *db.ClusterTx, projectName string, req api.StorageVolumesPost) error {
	info, err := fetchProject(tx, projectName, true)
	if err != nil {
		return err
	}

	if info == nil {
		return nil
	}

	// If "limits.disk" is not set, there's nothing to do.
	if info.Project.Config["limits.disk"] == "" {
		return nil
	}

	// Add the volume being created.
	info.Volumes = append(info.Volumes, db.StorageVolumeArgs{
		Name:   req.Name,
		Config: req.Config,
	})

	err = checkRestrictionsAndAggregateLimits(tx, info)
	if err != nil {
		return fmt.Errorf("Failed checking if volume creation allowed: %w", err)
	}

	return nil
}

// GetImageSpaceBudget returns how much disk space is left in the given project
// for writing images.
//
// If no limit is in place, return -1.
func GetImageSpaceBudget(tx *db.ClusterTx, projectName string) (int64, error) {
	info, err := fetchProject(tx, projectName, true)
	if err != nil {
		return -1, err
	}

	if info == nil {
		return -1, nil
	}

	// If "features.images" is not enabled, the budget is unlimited.
	if util.IsFalse(info.Project.Config["features.images"]) {
		return -1, nil
	}

	// If "limits.disk" is not set, the budget is unlimited.
	if info.Project.Config["limits.disk"] == "" {
		return -1, nil
	}

	parser := aggregateLimitConfigValueParsers["limits.disk"]
	quota, err := parser(info.Project.Config["limits.disk"])
	if err != nil {
		return -1, err
	}

	instances, err := expandInstancesConfigAndDevices(info.Instances, info.Profiles)
	if err != nil {
		return -1, err
	}

	info.Instances = instances

	totals, err := getTotalsAcrossProjectEntities(info, []string{"limits.disk"}, false)
	if err != nil {
		return -1, err
	}

	if totals["limits.disk"] < quota {
		return quota - totals["limits.disk"], nil
	}

	return 0, nil
}

// Check that we would not violate the project limits or restrictions if we
// were to commit the given instances and profiles.
func checkRestrictionsAndAggregateLimits(tx *db.ClusterTx, info *projectInfo) error {
	// List of config keys for which we need to check aggregate values
	// across all project instances.
	aggregateKeys := []string{}
	isRestricted := false
	for key, value := range info.Project.Config {
		if util.ValueInSlice(key, allAggregateLimits) {
			aggregateKeys = append(aggregateKeys, key)
			continue
		}

		if key == "restricted" && util.IsTrue(value) {
			isRestricted = true
			continue
		}
	}

	if len(aggregateKeys) == 0 && !isRestricted {
		return nil
	}

	instances, err := expandInstancesConfigAndDevices(info.Instances, info.Profiles)
	if err != nil {
		return err
	}

	info.Instances = instances

	err = checkAggregateLimits(info, aggregateKeys)
	if err != nil {
		return err
	}

	if isRestricted {
		err = checkRestrictions(info.Project, info.Instances, info.Profiles)
		if err != nil {
			return err
		}
	}

	return nil
}

func getAggregateLimits(info *projectInfo, aggregateKeys []string) (map[string]api.ProjectStateResource, error) {
	result := map[string]api.ProjectStateResource{}

	if len(aggregateKeys) == 0 {
		return result, nil
	}

	totals, err := getTotalsAcrossProjectEntities(info, aggregateKeys, true)
	if err != nil {
		return nil, err
	}

	for _, key := range aggregateKeys {
		max := int64(-1)
		limit := info.Project.Config[key]
		if limit != "" {
			parser := aggregateLimitConfigValueParsers[key]
			max, err = parser(info.Project.Config[key])
			if err != nil {
				return nil, err
			}
		}

		resource := api.ProjectStateResource{
			Usage: totals[key],
			Limit: max,
		}

		result[key] = resource
	}

	return result, nil
}

func checkAggregateLimits(info *projectInfo, aggregateKeys []string) error {
	if len(aggregateKeys) == 0 {
		return nil
	}

	totals, err := getTotalsAcrossProjectEntities(info, aggregateKeys, false)
	if err != nil {
		return fmt.Errorf("Failed getting usage of project entities: %w", err)
	}

	for _, key := range aggregateKeys {
		parser := aggregateLimitConfigValueParsers[key]
		max, err := parser(info.Project.Config[key])
		if err != nil {
			return err
		}

		if totals[key] > max {
			return fmt.Errorf("Reached maximum aggregate value %q for %q in project %q", info.Project.Config[key], key, info.Project.Name)
		}
	}
	return nil
}

// parseHostIDMapRange parse the supplied list of host ID map ranges into a idmap.Entry slice.
func parseHostIDMapRange(isUID bool, isGID bool, listValue string) ([]idmap.Entry, error) {
	var idmaps []idmap.Entry

	for _, listItem := range util.SplitNTrimSpace(listValue, ",", -1, true) {
		rangeStart, rangeSize, err := validate.ParseUint32Range(listItem)
		if err != nil {
			return nil, err
		}

		idmaps = append(idmaps, idmap.Entry{
			HostID:   int64(rangeStart),
			MapRange: int64(rangeSize),
			IsUID:    isUID,
			IsGID:    isGID,
			NSID:     -1, // We don't have this as we are just parsing host IDs.
		})
	}

	return idmaps, nil
}

// Check that the project's restrictions are not violated across the given
// instances and profiles.
func checkRestrictions(project api.Project, instances []api.Instance, profiles []api.Profile) error {
	containerConfigChecks := map[string]func(value string) error{}
	devicesChecks := map[string]func(value map[string]string) error{}

	allowContainerLowLevel := false
	allowVMLowLevel := false
	var allowedIDMapHostUIDs, allowedIDMapHostGIDs []idmap.Entry

	for i := range allRestrictions {
		// Check if this particular restriction is defined explicitly in the project config.
		// If not, use the default value. Assign to local var so it doesn't change to the default value of
		// another restriction by time check functions run.
		restrictionKey := i
		restrictionValue, ok := project.Config[restrictionKey]
		if !ok {
			restrictionValue = allRestrictions[restrictionKey]
		}

		switch restrictionKey {
		case "restricted.containers.interception":
			for _, key := range allowableIntercept {
				containerConfigChecks[key] = func(instanceValue string) error {
					disabled := util.IsFalseOrEmpty(instanceValue)

					if restrictionValue != "allow" && !disabled {
						return fmt.Errorf("Container syscall interception is forbidden")
					}

					return nil
				}
			}
		case "restricted.containers.nesting":
			containerConfigChecks["security.nesting"] = func(instanceValue string) error {
				if restrictionValue == "block" && util.IsTrue(instanceValue) {
					return fmt.Errorf("Container nesting is forbidden")
				}

				return nil
			}

		case "restricted.containers.lowlevel":
			if restrictionValue == "allow" {
				allowContainerLowLevel = true
			}

		case "restricted.containers.privilege":
			containerConfigChecks["security.privileged"] = func(instanceValue string) error {
				if restrictionValue != "allow" && util.IsTrue(instanceValue) {
					return fmt.Errorf("Privileged containers are forbidden")
				}

				return nil
			}

			containerConfigChecks["security.idmap.isolated"] = func(instanceValue string) error {
				if restrictionValue == "isolated" && util.IsFalseOrEmpty(instanceValue) {
					return fmt.Errorf("Non-isolated containers are forbidden")
				}

				return nil
			}

		case "restricted.virtual-machines.lowlevel":
			if restrictionValue == "allow" {
				allowVMLowLevel = true
			}

		case "restricted.devices.unix-char":
			devicesChecks["unix-char"] = func(device map[string]string) error {
				if restrictionValue != "allow" {
					return fmt.Errorf("Unix character devices are forbidden")
				}

				return nil
			}

		case "restricted.devices.unix-block":
			devicesChecks["unix-block"] = func(device map[string]string) error {
				if restrictionValue != "allow" {
					return fmt.Errorf("Unix block devices are forbidden")
				}

				return nil
			}

		case "restricted.devices.unix-hotplug":
			devicesChecks["unix-hotplug"] = func(device map[string]string) error {
				if restrictionValue != "allow" {
					return fmt.Errorf("Unix hotplug devices are forbidden")
				}

				return nil
			}

		case "restricted.devices.infiniband":
			devicesChecks["infiniband"] = func(device map[string]string) error {
				if restrictionValue != "allow" {
					return fmt.Errorf("Infiniband devices are forbidden")
				}

				return nil
			}

		case "restricted.devices.gpu":
			devicesChecks["gpu"] = func(device map[string]string) error {
				if restrictionValue != "allow" {
					return fmt.Errorf("GPU devices are forbidden")
				}

				return nil
			}

		case "restricted.devices.usb":
			devicesChecks["usb"] = func(device map[string]string) error {
				if restrictionValue != "allow" {
					return fmt.Errorf("USB devices are forbidden")
				}

				return nil
			}

		case "restricted.devices.pci":
			devicesChecks["pci"] = func(device map[string]string) error {
				if restrictionValue != "allow" {
					return fmt.Errorf("PCI devices are forbidden")
				}

				return nil
			}

		case "restricted.devices.proxy":
			devicesChecks["proxy"] = func(device map[string]string) error {
				if restrictionValue != "allow" {
					return fmt.Errorf("Proxy devices are forbidden")
				}

				return nil
			}

		case "restricted.devices.nic":
			devicesChecks["nic"] = func(device map[string]string) error {
				// Check if the NICs are allowed at all.
				switch restrictionValue {
				case "block":
					return fmt.Errorf("Network devices are forbidden")
				case "managed":
					if device["network"] == "" {
						return fmt.Errorf("Only managed network devices are allowed")
					}
				}

				// Check if the NIC's parent/network setting is allowed based on the
				// restricted.devices.nic and restricted.networks.access settings.
				if device["network"] != "" {
					if !NetworkAllowed(project.Config, device["network"], true) {
						return fmt.Errorf("Network not allowed in project")
					}
				} else if device["parent"] != "" {
					if !NetworkAllowed(project.Config, device["parent"], false) {
						return fmt.Errorf("Network not allowed in project")
					}
				}

				return nil
			}

		case "restricted.devices.disk":
			devicesChecks["disk"] = func(device map[string]string) error {
				// The root device is always allowed.
				if device["path"] == "/" && device["pool"] != "" {
					return nil
				}

				// Always allow the cloud-init config drive.
				if device["path"] == "" && device["source"] == "cloud-init:config" {
					return nil
				}

				switch restrictionValue {
				case "block":
					return fmt.Errorf("Disk devices are forbidden")
				case "managed":
					if device["pool"] == "" {
						return fmt.Errorf("Attaching disks not backed by a pool is forbidden")
					}

				case "allow":
					var allowed bool
					allowed, _ = CheckRestrictedDevicesDiskPaths(project.Config, device["source"])
					if !allowed {
						return fmt.Errorf("Disk source path %q not allowed", device["source"])
					}
				}

				return nil
			}

		case "restricted.idmap.uid":
			var err error
			allowedIDMapHostUIDs, err = parseHostIDMapRange(true, false, restrictionValue)
			if err != nil {
				return fmt.Errorf("Failed parsing %q: %w", "restricted.idmap.uid", err)
			}

		case "restricted.idmap.gid":
			var err error
			allowedIDMapHostGIDs, err = parseHostIDMapRange(false, true, restrictionValue)
			if err != nil {
				return fmt.Errorf("Failed parsing %q: %w", "restricted.idmap.uid", err)
			}
		}
	}

	// Common config check logic between instances and profiles.
	entityConfigChecker := func(instType instancetype.Type, entityName string, config map[string]string) error {
		entityTypeLabel := instType.String()
		if instType == instancetype.Any {
			entityTypeLabel = "profile"
		}

		isContainerOrProfile := instType == instancetype.Container || instType == instancetype.Any
		isVMOrProfile := instType == instancetype.VM || instType == instancetype.Any

		// Check if unrestricted low-level options are available. For profiles we require that low-level
		// features for both containers and VMs are enabled as we don't know which instance the profile
		// will be used on.
		allowUnrestrictedLowLevel := (instType == instancetype.Any && allowContainerLowLevel && allowVMLowLevel) ||
			(instType == instancetype.Container && allowContainerLowLevel) ||
			(instType == instancetype.VM && allowVMLowLevel)

		for key, value := range config {
			if !allowUnrestrictedLowLevel {
				if key == "raw.idmap" {
					// If the low-level raw.idmap is used check whether the raw.idmap host IDs
					// are allowed based on the project's allowed ID map Host UIDs and GIDs.
					idmaps, err := idmap.NewSetFromIncusIDMap(value)
					if err != nil {
						return err
					}

					for i, entry := range idmaps.Entries {
						if !entry.HostIDsCoveredBy(allowedIDMapHostUIDs, allowedIDMapHostGIDs) {
							return fmt.Errorf(`Use of low-level "raw.idmap" element %d on %s %q of project %q is forbidden`, i, entityTypeLabel, entityName, project.Name)
						}
					}
				} else if (isContainerOrProfile && isContainerLowLevelOptionForbidden(key)) || (isVMOrProfile && isVMLowLevelOptionForbidden(key)) {
					// Otherwise check if the key is a forbidden low-level one.
					return fmt.Errorf("Use of low-level config %q on %s %q of project %q is forbidden", key, entityTypeLabel, entityName, project.Name)
				}
			}

			var checker func(value string) error
			if isContainerOrProfile {
				checker = containerConfigChecks[key]
			}

			if checker == nil {
				continue
			}

			err := checker(value)
			if err != nil {
				return fmt.Errorf("Invalid value %q for config %q on %s %q of project %q: %w", value, key, instType, entityName, project.Name, err)
			}
		}

		return nil
	}

	// Common devices check logic between instances and profiles.
	entityDevicesChecker := func(instType instancetype.Type, entityName string, devices map[string]map[string]string) error {
		entityTypeLabel := instType.String()
		if instType == instancetype.Any {
			entityTypeLabel = "profile"
		}

		for name, device := range devices {
			check, ok := devicesChecks[device["type"]]
			if !ok {
				continue
			}

			err := check(device)
			if err != nil {
				return fmt.Errorf("Invalid device %q on %s %q of project %q: %w", name, entityTypeLabel, entityName, project.Name, err)
			}
		}
		return nil
	}

	for _, instance := range instances {
		instType, err := instancetype.New(instance.Type)
		if err != nil {
			return err
		}

		err = entityConfigChecker(instType, instance.Name, instance.Config)
		if err != nil {
			return err
		}

		err = entityDevicesChecker(instType, instance.Name, instance.Devices)
		if err != nil {
			return err
		}
	}

	for _, profile := range profiles {
		err := entityConfigChecker(instancetype.Any, profile.Name, profile.Config)
		if err != nil {
			return err
		}

		err = entityDevicesChecker(instancetype.Any, profile.Name, profile.Devices)
		if err != nil {
			return err
		}
	}

	return nil
}

// CheckRestrictedDevicesDiskPaths checks whether the disk's source path is within the allowed paths specified in
// the project's restricted.devices.disk.paths config setting.
// If no allowed paths are specified in project, then it allows all paths, and returns true and empty string.
// If allowed paths are specified, and one matches, returns true and the matching allowed parent source path.
// Otherwise if sourcePath not allowed returns false and empty string.
func CheckRestrictedDevicesDiskPaths(projectConfig map[string]string, sourcePath string) (bool, string) {
	if projectConfig["restricted.devices.disk.paths"] == "" {
		return true, ""
	}

	// Clean, then add trailing slash, to ensure we are prefix matching on whole path.
	sourcePath = fmt.Sprintf("%s/", filepath.Clean(sourcePath))
	for _, parentSourcePath := range strings.SplitN(projectConfig["restricted.devices.disk.paths"], ",", -1) {
		// Clean, then add trailing slash, to ensure we are prefix matching on whole path.
		parentSourcePathTrailing := fmt.Sprintf("%s/", filepath.Clean(parentSourcePath))
		if strings.HasPrefix(sourcePath, parentSourcePathTrailing) {
			return true, parentSourcePath
		}
	}

	return false, ""
}

var allAggregateLimits = []string{
	"limits.cpu",
	"limits.disk",
	"limits.memory",
	"limits.processes",
}

// allRestrictions lists all available 'restrict.*' config keys along with their default setting.
var allRestrictions = map[string]string{
	"restricted.backups":                   "block",
	"restricted.cluster.groups":            "",
	"restricted.cluster.target":            "block",
	"restricted.containers.nesting":        "block",
	"restricted.containers.interception":   "block",
	"restricted.containers.lowlevel":       "block",
	"restricted.containers.privilege":      "unprivileged",
	"restricted.virtual-machines.lowlevel": "block",
	"restricted.devices.unix-char":         "block",
	"restricted.devices.unix-block":        "block",
	"restricted.devices.unix-hotplug":      "block",
	"restricted.devices.infiniband":        "block",
	"restricted.devices.gpu":               "block",
	"restricted.devices.usb":               "block",
	"restricted.devices.pci":               "block",
	"restricted.devices.proxy":             "block",
	"restricted.devices.nic":               "managed",
	"restricted.devices.disk":              "managed",
	"restricted.devices.disk.paths":        "",
	"restricted.idmap.uid":                 "",
	"restricted.idmap.gid":                 "",
	"restricted.networks.access":           "",
	"restricted.snapshots":                 "block",
}

// allowableIntercept lists all syscall interception keys which may be allowed.
var allowableIntercept = []string{
	"security.syscalls.intercept.bpf",
	"security.syscalls.intercept.bpf.devices",
	"security.syscalls.intercept.mknod",
	"security.syscalls.intercept.mount",
	"security.syscalls.intercept.mount.fuse",
	"security.syscalls.intercept.setxattr",
	"security.syscalls.intercept.sysinfo",
}

// Return true if a low-level container option is forbidden.
func isContainerLowLevelOptionForbidden(key string) bool {
	if strings.HasPrefix(key, "security.syscalls.intercept") && !util.ValueInSlice(key, allowableIntercept) {
		return true
	}

	if util.ValueInSlice(key, []string{
		"boot.host_shutdown_action",
		"boot.host_shutdown_timeout",
		"linux.kernel_modules",
		"raw.apparmor",
		"raw.idmap",
		"raw.lxc",
		"raw.seccomp",
		"security.guestapi.images",
		"security.idmap.base",
		"security.idmap.size",
	}) {
		return true
	}

	return false
}

// Return true if a low-level VM option is forbidden.
func isVMLowLevelOptionForbidden(key string) bool {
	return util.ValueInSlice(key, []string{
		"boot.host_shutdown_action",
		"boot.host_shutdown_timeout",
		"limits.memory.hugepages",
		"raw.idmap",
		"raw.qemu",
	})
}

// AllowInstanceUpdate returns an error if any project-specific limit or
// restriction is violated when updating an existing instance.
func AllowInstanceUpdate(tx *db.ClusterTx, projectName, instanceName string, req api.InstancePut, currentConfig map[string]string) error {
	var updatedInstance *api.Instance
	info, err := fetchProject(tx, projectName, true)
	if err != nil {
		return err
	}

	if info == nil {
		return nil
	}

	// Change the instance being updated.
	for i, instance := range info.Instances {
		if instance.Name != instanceName {
			continue
		}

		info.Instances[i].Profiles = req.Profiles
		info.Instances[i].Config = req.Config
		info.Instances[i].Devices = req.Devices
		updatedInstance = &info.Instances[i]
	}

	instType, err := instancetype.New(updatedInstance.Type)
	if err != nil {
		return err
	}

	// Special case restriction checks on volatile.* keys, since we want to
	// detect if they were changed or added.
	err = checkRestrictionsOnVolatileConfig(
		info.Project, instType, updatedInstance.Name, req.Config, currentConfig, false)
	if err != nil {
		return err
	}

	err = checkRestrictionsAndAggregateLimits(tx, info)
	if err != nil {
		return fmt.Errorf("Failed checking if instance update allowed: %w", err)
	}

	return nil
}

// AllowVolumeUpdate returns an error if any project-specific limit or
// restriction is violated when updating an existing custom volume.
func AllowVolumeUpdate(tx *db.ClusterTx, projectName, volumeName string, req api.StorageVolumePut, currentConfig map[string]string) error {
	info, err := fetchProject(tx, projectName, true)
	if err != nil {
		return err
	}

	if info == nil {
		return nil
	}

	// If "limits.disk" is not set, there's nothing to do.
	if info.Project.Config["limits.disk"] == "" {
		return nil
	}

	// Change the volume being updated.
	for i, volume := range info.Volumes {
		if volume.Name != volumeName {
			continue
		}

		info.Volumes[i].Config = req.Config
	}

	err = checkRestrictionsAndAggregateLimits(tx, info)
	if err != nil {
		return fmt.Errorf("Failed checking if volume update allowed: %w", err)
	}

	return nil
}

// AllowProfileUpdate checks that project limits and restrictions are not
// violated when changing a profile.
func AllowProfileUpdate(tx *db.ClusterTx, projectName, profileName string, req api.ProfilePut) error {
	info, err := fetchProject(tx, projectName, true)
	if err != nil {
		return err
	}

	if info == nil {
		return nil
	}

	// Change the profile being updated.
	for i, profile := range info.Profiles {
		if profile.Name != profileName {
			continue
		}

		info.Profiles[i].Config = req.Config
		info.Profiles[i].Devices = req.Devices
	}

	err = checkRestrictionsAndAggregateLimits(tx, info)
	if err != nil {
		return fmt.Errorf("Failed checking if profile update allowed: %w", err)
	}

	return nil
}

// AllowProjectUpdate checks the new config to be set on a project is valid.
func AllowProjectUpdate(tx *db.ClusterTx, projectName string, config map[string]string, changed []string) error {
	info, err := fetchProject(tx, projectName, false)
	if err != nil {
		return err
	}

	info.Instances, err = expandInstancesConfigAndDevices(info.Instances, info.Profiles)
	if err != nil {
		return err
	}

	// List of keys that need to check aggregate values across all project
	// instances.
	aggregateKeys := []string{}

	for _, key := range changed {
		if strings.HasPrefix(key, "restricted.") {
			project := api.Project{
				Name: projectName,
				ProjectPut: api.ProjectPut{
					Config: config,
				},
			}

			err := checkRestrictions(project, info.Instances, info.Profiles)
			if err != nil {
				return fmt.Errorf("Conflict detected when changing %q in project %q: %w", key, projectName, err)
			}

			continue
		}

		switch key {
		case "limits.instances":
			err := validateTotalInstanceCountLimit(info.Instances, config[key], projectName)
			if err != nil {
				return fmt.Errorf("Can't change limits.instances in project %q: %w", projectName, err)
			}

		case "limits.containers":
			fallthrough
		case "limits.virtual-machines":
			err := validateInstanceCountLimit(info.Instances, key, config[key], projectName)
			if err != nil {
				return fmt.Errorf("Can't change %q in project %q: %w", key, projectName, err)
			}

		case "limits.processes":
			fallthrough
		case "limits.cpu":
			fallthrough
		case "limits.memory":
			fallthrough
		case "limits.disk":
			aggregateKeys = append(aggregateKeys, key)
		}
	}

	if len(aggregateKeys) > 0 {
		totals, err := getTotalsAcrossProjectEntities(info, aggregateKeys, false)
		if err != nil {
			return err
		}

		for _, key := range aggregateKeys {
			err := validateAggregateLimit(totals, key, config[key])
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// Check that limits.instances, i.e. the total limit of containers/virtual machines allocated
// to the user is equal to or above the current count.
func validateTotalInstanceCountLimit(instances []api.Instance, value, project string) error {
	if value == "" {
		return nil
	}

	limit, err := strconv.Atoi(value)
	if err != nil {
		return err
	}

	count := len(instances)

	if limit < count {
		return fmt.Errorf(`"limits.instances" is too low: there currently are %d total instances in project %q`, count, project)
	}

	return nil
}

// Check that limits.containers or limits.virtual-machines is equal or above
// the current count.
func validateInstanceCountLimit(instances []api.Instance, key, value, project string) error {
	if value == "" {
		return nil
	}

	instanceType := countConfigInstanceType[key]
	limit, err := strconv.Atoi(value)
	if err != nil {
		return err
	}

	dbType, err := instancetype.New(string(instanceType))
	if err != nil {
		return err
	}

	count := 0
	for _, instance := range instances {
		if instance.Type == dbType.String() {
			count++
		}
	}

	if limit < count {
		return fmt.Errorf(`%q is too low: there currently are %d instances of type %s in project %q`, key, count, instanceType, project)
	}

	return nil
}

var countConfigInstanceType = map[string]api.InstanceType{
	"limits.containers":       api.InstanceTypeContainer,
	"limits.virtual-machines": api.InstanceTypeVM,
}

// Validates an aggregate limit, checking that the new value is not below the
// current total amount.
func validateAggregateLimit(totals map[string]int64, key, value string) error {
	if value == "" {
		return nil
	}

	parser := aggregateLimitConfigValueParsers[key]
	limit, err := parser(value)
	if err != nil {
		return fmt.Errorf("Invalid value %q for limit %q: %w", value, key, err)
	}

	total := totals[key]
	if limit < total {
		printer := aggregateLimitConfigValuePrinters[key]
		return fmt.Errorf("%q is too low: current total is %q", key, printer(total))
	}

	return nil
}

// Return true if the project has some limits or restrictions set.
func projectHasLimitsOrRestrictions(project api.Project) bool {
	for k, v := range project.Config {
		if strings.HasPrefix(k, "limits.") {
			return true
		}

		if k == "restricted" && util.IsTrue(v) {
			return true
		}
	}

	return false
}

// Hold information associated with the project, such as profiles and
// instances.
type projectInfo struct {
	Project   api.Project
	Profiles  []api.Profile
	Instances []api.Instance
	Volumes   []db.StorageVolumeArgs
}

// Fetch the given project from the database along with its profiles, instances
// and possibly custom volumes.
//
// If the skipIfNoLimits flag is true, then profiles, instances and volumes
// won't be loaded if the profile has no limits set on it, and nil will be
// returned.
func fetchProject(tx *db.ClusterTx, projectName string, skipIfNoLimits bool) (*projectInfo, error) {
	ctx := context.Background()
	dbProject, err := cluster.GetProject(ctx, tx.Tx(), projectName)
	if err != nil {
		return nil, fmt.Errorf("Fetch project database object: %w", err)
	}

	project, err := dbProject.ToAPI(ctx, tx.Tx())
	if err != nil {
		return nil, err
	}

	if skipIfNoLimits && !projectHasLimitsOrRestrictions(*project) {
		return nil, nil
	}

	profilesFilter := cluster.ProfileFilter{}

	// If the project has the profiles feature enabled, we use its own
	// profiles to expand the instances configs, otherwise we use the
	// profiles from the default project.
	defaultProject := api.ProjectDefaultName
	if projectName == api.ProjectDefaultName || util.IsTrue(project.Config["features.profiles"]) {
		profilesFilter.Project = &projectName
	} else {
		profilesFilter.Project = &defaultProject
	}

	dbProfiles, err := cluster.GetProfiles(ctx, tx.Tx(), profilesFilter)
	if err != nil {
		return nil, fmt.Errorf("Fetch profiles from database: %w", err)
	}

	profiles := make([]api.Profile, 0, len(dbProfiles))
	for _, profile := range dbProfiles {
		apiProfile, err := profile.ToAPI(ctx, tx.Tx())
		if err != nil {
			return nil, err
		}

		profiles = append(profiles, *apiProfile)
	}

	dbInstances, err := cluster.GetInstances(ctx, tx.Tx(), cluster.InstanceFilter{Project: &projectName})
	if err != nil {
		return nil, fmt.Errorf("Fetch project instances from database: %w", err)
	}

	instances := make([]api.Instance, 0, len(dbInstances))
	for _, instance := range dbInstances {
		apiInstance, err := instance.ToAPI(ctx, tx.Tx())
		if err != nil {
			return nil, fmt.Errorf("Failed to get API data for instance %q in project %q: %w", instance.Name, instance.Project, err)
		}

		instances = append(instances, *apiInstance)
	}

	volumes, err := tx.GetCustomVolumesInProject(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("Fetch project custom volumes from database: %w", err)
	}

	info := &projectInfo{
		Project:   *project,
		Profiles:  profiles,
		Instances: instances,
		Volumes:   volumes,
	}

	return info, nil
}

// Expand the configuration and devices of the given instances, taking the give
// project profiles into account.
func expandInstancesConfigAndDevices(instances []api.Instance, profiles []api.Profile) ([]api.Instance, error) {
	expandedInstances := make([]api.Instance, len(instances))

	// Index of all profiles by name.
	profilesByName := map[string]api.Profile{}
	for _, profile := range profiles {
		profilesByName[profile.Name] = profile
	}

	for i, instance := range instances {
		apiProfiles := make([]api.Profile, len(instance.Profiles))

		for j, name := range instance.Profiles {
			profile := profilesByName[name]
			apiProfiles[j] = profile
		}

		expandedInstances[i] = instance
		expandedInstances[i].Config = db.ExpandInstanceConfig(instance.Config, apiProfiles)
		expandedInstances[i].Devices = db.ExpandInstanceDevices(deviceconfig.NewDevices(instance.Devices), apiProfiles).CloneNative()
	}

	return expandedInstances, nil
}

// Sum of the effective values for the given limits across all project
// entities (instances and custom volumes).
func getTotalsAcrossProjectEntities(info *projectInfo, keys []string, skipUnset bool) (map[string]int64, error) {
	totals := map[string]int64{}

	for _, key := range keys {
		totals[key] = 0
		if key == "limits.disk" {
			for _, volume := range info.Volumes {
				value, ok := volume.Config["size"]
				if !ok {
					if skipUnset {
						continue
					}

					return nil, fmt.Errorf(`Custom volume %q in project %q has no "size" config set`, volume.Name, info.Project.Name)
				}

				limit, err := units.ParseByteSizeString(value)
				if err != nil {
					return nil, fmt.Errorf(`Parse "size" for custom volume %q in project %q: %w`, volume.Name, info.Project.Name, err)
				}

				totals[key] += limit
			}
		}
	}

	for _, instance := range info.Instances {
		limits, err := getInstanceLimits(instance, keys, skipUnset)
		if err != nil {
			return nil, err
		}

		for _, key := range keys {
			totals[key] += limits[key]
		}
	}

	return totals, nil
}

// Return the effective instance-level values for the limits with the given keys.
func getInstanceLimits(inst api.Instance, keys []string, skipUnset bool) (map[string]int64, error) {
	var err error
	limits := map[string]int64{}

	for _, key := range keys {
		var limit int64
		parser := aggregateLimitConfigValueParsers[key]

		if key == "limits.disk" {
			_, device, err := instance.GetRootDiskDevice(inst.Devices)
			if err != nil {
				return nil, fmt.Errorf("Failed getting root disk device for instance %q in project %q: %w", inst.Name, inst.Project, err)
			}

			value, ok := device["size"]
			if !ok || value == "" {
				if skipUnset {
					continue
				}

				return nil, fmt.Errorf(`Instance %q in project %q has no "size" config set on the root device either directly or via a profile`, inst.Name, inst.Project)
			}

			limit, err = parser(value)
			if err != nil {
				if skipUnset {
					continue
				}

				return nil, fmt.Errorf("Failed parsing %q for instance %q in project %q", key, inst.Name, inst.Project)
			}

			// Add size.state accounting for VM root disks.
			if inst.Type == instancetype.VM.String() {
				sizeStateValue, ok := device["size.state"]
				if !ok {
					// TODO: In case the VMs storage drivers config drive size isn't the default,
					// the limits accounting will be incorrect.
					sizeStateValue = deviceconfig.DefaultVMBlockFilesystemSize
				}

				sizeStateLimit, err := parser(sizeStateValue)
				if err != nil {
					if skipUnset {
						continue
					}

					return nil, fmt.Errorf("Failed parsing %q for instance %q in project %q", "size.state", inst.Name, inst.Project)
				}

				limit += sizeStateLimit
			}
		} else {
			value, ok := inst.Config[key]
			if !ok || value == "" {
				if skipUnset {
					continue
				}

				return nil, fmt.Errorf("Instance %q in project %q has no %q config, either directly or via a profile", inst.Name, inst.Project, key)
			}

			limit, err = parser(value)
			if err != nil {
				if skipUnset {
					continue
				}

				return nil, fmt.Errorf("Failed parsing %q for instance %q in project %q", key, inst.Name, inst.Project)
			}
		}

		limits[key] = limit
	}

	return limits, nil
}

var aggregateLimitConfigValueParsers = map[string]func(string) (int64, error){
	"limits.memory": func(value string) (int64, error) {
		if strings.HasSuffix(value, "%") {
			return -1, fmt.Errorf("Value can't be a percentage")
		}

		return units.ParseByteSizeString(value)
	},
	"limits.processes": func(value string) (int64, error) {
		limit, err := strconv.Atoi(value)
		if err != nil {
			return -1, err
		}

		return int64(limit), nil
	},
	"limits.cpu": func(value string) (int64, error) {
		if strings.Contains(value, ",") || strings.Contains(value, "-") {
			return -1, fmt.Errorf("CPUs can't be pinned if project limits are used")
		}

		limit, err := strconv.Atoi(value)
		if err != nil {
			return -1, err
		}

		return int64(limit), nil
	},
	"limits.disk": func(value string) (int64, error) {
		return units.ParseByteSizeString(value)
	},
}

var aggregateLimitConfigValuePrinters = map[string]func(int64) string{
	"limits.memory": func(limit int64) string {
		return units.GetByteSizeStringIEC(limit, 1)
	},
	"limits.processes": func(limit int64) string {
		return fmt.Sprintf("%d", limit)
	},
	"limits.cpu": func(limit int64) string {
		return fmt.Sprintf("%d", limit)
	},
	"limits.disk": func(limit int64) string {
		return units.GetByteSizeStringIEC(limit, 1)
	},
}

// FilterUsedBy filters a UsedBy list based on project access.
func FilterUsedBy(authorizer auth.Authorizer, r *http.Request, entries []string) []string {
	// Filter the entries.
	usedBy := []string{}
	for _, entry := range entries {
		entityType, projectName, location, pathArgs, err := cluster.URLToEntityType(entry)
		if err != nil {
			continue
		}

		var object auth.Object
		switch entityType {
		case cluster.TypeImage:
			object = auth.ObjectImage(projectName, pathArgs[0])
		case cluster.TypeInstance:
			object = auth.ObjectInstance(projectName, pathArgs[0])
		case cluster.TypeNetwork:
			object = auth.ObjectNetwork(projectName, pathArgs[0])
		case cluster.TypeProfile:
			object = auth.ObjectProfile(projectName, pathArgs[0])
		case cluster.TypeStoragePool:
			object = auth.ObjectStoragePool(pathArgs[0])
		case cluster.TypeStorageVolume:
			object = auth.ObjectStorageVolume(projectName, pathArgs[0], pathArgs[1], pathArgs[2], location)
		case cluster.TypeStorageBucket:
			object = auth.ObjectStorageBucket(projectName, pathArgs[0], pathArgs[1], location)
		default:
			continue
		}

		err = authorizer.CheckPermission(r.Context(), r, object, auth.EntitlementCanView)
		if err != nil {
			continue
		}

		usedBy = append(usedBy, entry)
	}

	return usedBy
}

// Return true if particular restriction in project is violated.
func projectHasRestriction(project *api.Project, restrictionKey string, blockValue string) bool {
	if util.IsFalseOrEmpty(project.Config["restricted"]) {
		return false
	}

	restrictionValue, ok := project.Config[restrictionKey]
	if !ok {
		restrictionValue = allRestrictions[restrictionKey]
	}

	if restrictionValue == blockValue {
		return true
	}

	return false
}

// CheckClusterTargetRestriction check if user is allowed to use cluster member targeting.
func CheckClusterTargetRestriction(authorizer auth.Authorizer, r *http.Request, project *api.Project, targetFlag string) error {
	if projectHasRestriction(project, "restricted.cluster.target", "block") && targetFlag != "" {
		// Allow server administrators to move instances around even when restricted (node evacuation, ...)
		err := authorizer.CheckPermission(r.Context(), r, auth.ObjectServer(), auth.EntitlementCanOverrideClusterTargetRestriction)
		if err != nil && api.StatusErrorCheck(err, http.StatusForbidden) {
			return api.StatusErrorf(http.StatusForbidden, "This project doesn't allow cluster member targeting")
		} else if err != nil {
			return err
		}
	}

	return nil
}

// AllowBackupCreation returns an error if any project-specific restriction is violated
// when creating a new backup in a project.
func AllowBackupCreation(tx *db.ClusterTx, projectName string) error {
	ctx := context.Background()
	dbProject, err := cluster.GetProject(ctx, tx.Tx(), projectName)
	if err != nil {
		return err
	}

	project, err := dbProject.ToAPI(ctx, tx.Tx())
	if err != nil {
		return err
	}

	if projectHasRestriction(project, "restricted.backups", "block") {
		return fmt.Errorf("Project %q doesn't allow for backup creation", projectName)
	}

	return nil
}

// AllowSnapshotCreation returns an error if any project-specific restriction is violated
// when creating a new snapshot in a project.
func AllowSnapshotCreation(p *api.Project) error {
	if projectHasRestriction(p, "restricted.snapshots", "block") {
		return fmt.Errorf("Project %q doesn't allow for snapshot creation", p.Name)
	}

	return nil
}

// GetRestrictedClusterGroups returns a slice of restricted cluster groups for the given project.
func GetRestrictedClusterGroups(p *api.Project) []string {
	return util.SplitNTrimSpace(p.Config["restricted.cluster.groups"], ",", -1, true)
}

// AllowClusterMember returns nil if the given project is allowed to use the cluster member.
func AllowClusterMember(p *api.Project, member *db.NodeInfo) error {
	clusterGroupsAllowed := GetRestrictedClusterGroups(p)

	if util.IsTrue(p.Config["restricted"]) && len(clusterGroupsAllowed) > 0 {
		for _, memberGroupName := range member.Groups {
			if util.ValueInSlice(memberGroupName, clusterGroupsAllowed) {
				return nil
			}
		}

		return fmt.Errorf("Project isn't allowed to use this cluster member: %q", member.Name)
	}

	return nil
}

// AllowClusterGroup returns nil if the given project is allowed to use the cluster groupName.
func AllowClusterGroup(p *api.Project, groupName string) error {
	clusterGroupsAllowed := GetRestrictedClusterGroups(p)

	// Skip the check if the project is not restricted
	if util.IsFalseOrEmpty(p.Config["restricted"]) {
		return nil
	}

	if len(clusterGroupsAllowed) > 0 && !util.ValueInSlice(groupName, clusterGroupsAllowed) {
		return fmt.Errorf("Project isn't allowed to use this cluster group: %q", groupName)
	}

	return nil
}

// CheckTargetMember checks if the given targetMemberName is present in allMembers
// and is allowed for the project.
// If the target member is allowed it returns the resolved node information.
func CheckTargetMember(p *api.Project, targetMemberName string, allMembers []db.NodeInfo) (*db.NodeInfo, error) {
	// Find target member.
	for _, potentialMember := range allMembers {
		if potentialMember.Name == targetMemberName {
			// If restricted groups are specified then check member is in at least one of them.
			err := AllowClusterMember(p, &potentialMember)
			if err != nil {
				return nil, api.StatusErrorf(http.StatusForbidden, err.Error())
			}

			return &potentialMember, nil
		}
	}

	return nil, api.StatusErrorf(http.StatusNotFound, "Cluster member %q not found", targetMemberName)
}

// CheckTargetGroup checks if the given groupName is allowed for the project.
func CheckTargetGroup(ctx context.Context, tx *db.ClusterTx, p *api.Project, groupName string) error {
	// If restricted groups are specified then check the requested group is in the list.
	err := AllowClusterGroup(p, groupName)
	if err != nil {
		return api.StatusErrorf(http.StatusForbidden, err.Error())
	}

	// Check if the target group exists.
	targetGroupExists, err := cluster.ClusterGroupExists(ctx, tx.Tx(), groupName)
	if err != nil {
		return err
	}

	if !targetGroupExists {
		return api.StatusErrorf(http.StatusBadRequest, "Cluster group %q doesn't exist", groupName)
	}

	return nil
}

// CheckTarget checks if the given cluster target (member or group) is allowed.
// If target is a cluster member and is found in allMembers it returns the resolved node information object.
// If target is a cluster group it returns the cluster group name.
// In case of error, neither node information nor cluster group name gets returned.
func CheckTarget(ctx context.Context, authorizer auth.Authorizer, r *http.Request, tx *db.ClusterTx, p *api.Project, target string, allMembers []db.NodeInfo) (*db.NodeInfo, string, error) {
	// Extract the target.
	var targetGroupName string
	var targetMemberName string
	if strings.HasPrefix(target, "@") {
		targetGroupName = strings.TrimPrefix(target, "@")
	} else {
		targetMemberName = target
	}

	// Check manual cluster member targeting restrictions.
	err := CheckClusterTargetRestriction(authorizer, r, p, target)
	if err != nil {
		return nil, "", err
	}

	if targetMemberName != "" {
		member, err := CheckTargetMember(p, targetMemberName, allMembers)
		if err != nil {
			return nil, "", err
		}

		return member, "", nil
	} else if targetGroupName != "" {
		err := CheckTargetGroup(ctx, tx, p, targetGroupName)
		if err != nil {
			return nil, "", err
		}

		return nil, targetGroupName, nil
	}

	return nil, "", nil
}
