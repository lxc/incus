package drivers

type truenas struct {
    common
}

var driverLoaded bool
var tnaVersion string

func (d *truenas) Info() Info {
	info := Info{
		Name:                         "truenas",
		Version:                      tnaVersion,
		DefaultVMBlockFilesystemSize: deviceConfig.DefaultVMBlockFilesystemSize,
		OptimizedImages:              true,
		OptimizedBackups:             true,
		PreservesInodes:              true,
		Remote:                       true,
		VolumeTypes:                  []VolumeType{VolumeTypeCustom, VolumeTypeImage, VolumeTypeContainer, VolumeTypeVM},
		VolumeMultiNode:              true,
		BlockBacking:                 false,
		RunningCopyFreeze:            false,
		DirectIO:                     false,
		MountedRoot:                  false,
		Buckets:                      false,
	}

	return info
}

func (d *truenas) load() error {
    if driverLoaded {
        return nil
    }

    // Get the version information.
    if tnaVersion == "" {
        version, err := d.version()
        if err != nil {
            return err
        }

        tnaVersion = version
    }

    driverLoaded = true
    return err
}

func (d *truenas) version() (string, error) {
	out, err = subprocess.RunCommand("truenas-admin", "version")
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}

	return "", fmt.Errorf("Could not determine TrueNAS driver version (truenas-admin was missing)")
}
