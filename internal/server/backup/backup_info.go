package backup

import (
	"fmt"
	"io"

	"gopkg.in/yaml.v2"

	"github.com/lxc/incus/v6/internal/server/backup/config"
	"github.com/lxc/incus/v6/internal/server/sys"
	"github.com/lxc/incus/v6/shared/api"
)

// Type indicates the type of backup.
type Type string

// TypeUnknown defines the backup type value for unknown backups.
const TypeUnknown = Type("")

// TypeContainer defines the backup type value for a container.
const TypeContainer = Type("container")

// TypeVM defines the backup type value for a virtual-machine.
const TypeVM = Type("virtual-machine")

// TypeCustom defines the backup type value for a custom volume.
const TypeCustom = Type("custom")

// TypeBucket defines the backup type value for a storage bucket.
const TypeBucket = Type("bucket")

const backupIndexPath = "backup/index.yaml"

// InstanceTypeToBackupType converts instance type to backup type.
func InstanceTypeToBackupType(instanceType api.InstanceType) Type {
	switch instanceType {
	case api.InstanceTypeContainer:
		return TypeContainer
	case api.InstanceTypeVM:
		return TypeVM
	}

	return TypeUnknown
}

// Info represents exported backup information.
type Info struct {
	Project          string         `json:"-" yaml:"-"` // Project is set during import based on current project.
	Name             string         `json:"name" yaml:"name"`
	Backend          string         `json:"backend" yaml:"backend"`
	Pool             string         `json:"pool" yaml:"pool"`
	Snapshots        []string       `json:"snapshots,omitempty" yaml:"snapshots,omitempty"`
	OptimizedStorage *bool          `json:"optimized,omitempty" yaml:"optimized,omitempty"`               // Optional field to handle older optimized backups that don't have this field.
	OptimizedHeader  *bool          `json:"optimized_header,omitempty" yaml:"optimized_header,omitempty"` // Optional field to handle older optimized backups that don't have this field.
	Type             Type           `json:"type,omitempty" yaml:"type,omitempty"`                         // Type of backup.
	Config           *config.Config `json:"config,omitempty" yaml:"config,omitempty"`                     // Equivalent of backup.yaml but embedded in index for quick retrieval.
}

// GetInfo extracts backup information from a given ReadSeeker.
func GetInfo(r io.ReadSeeker, sysOS *sys.OS, outputPath string) (*Info, error) {
	result := Info{}
	hasIndexFile := false

	// Define some bools used to create points for OptimizedStorage field.
	optimizedStorageFalse := false
	optimizedHeaderFalse := false

	// Extract.
	tr, cancelFunc, err := TarReader(r, sysOS, outputPath)
	if err != nil {
		return nil, err
	}

	defer cancelFunc()

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break // End of archive.
		}

		if err != nil {
			return nil, fmt.Errorf("Error reading backup file info: %w", err)
		}

		if hdr.Name == backupIndexPath {
			err = yaml.NewDecoder(tr).Decode(&result)
			if err != nil {
				return nil, err
			}

			hasIndexFile = true

			// Default to container if index doesn't specify instance type.
			if result.Type == TypeUnknown {
				result.Type = TypeContainer
			}

			// Default to no optimized header if not specified.
			if result.OptimizedHeader == nil {
				result.OptimizedHeader = &optimizedHeaderFalse
			}

			if result.OptimizedStorage != nil {
				// No need to continue looking for optimized storage hint using the presence of the
				// container.bin file below, as the index.yaml file tells us directly.
				break
			} else {
				// Default to non-optimized if not specified and continue reading to see if
				// optimized container.bin file present.
				result.OptimizedStorage = &optimizedStorageFalse
			}
		}

		// If the tarball contains a binary dump of the container, then this is an optimized backup.
		// This check is only for legacy backups before we introduced the Type and OptimizedStorage fields
		// in index.yaml, so there is no need to perform this type of check for other types of backups that
		// have always had these fields populated.
		if hdr.Name == "backup/container.bin" {
			optimizedStorageTrue := true
			result.OptimizedStorage = &optimizedStorageTrue

			// Stop read loop if index.yaml already parsed.
			if hasIndexFile {
				break
			}
		}
	}

	cancelFunc() // Done reading archive.

	if !hasIndexFile {
		return nil, fmt.Errorf("Backup is missing at %q", backupIndexPath)
	}

	return &result, nil
}
