package backup

import (
	"os"
	"strings"
	"time"

	"github.com/cyphar/incus/lxd/project"
	"github.com/cyphar/incus/lxd/revert"
	"github.com/cyphar/incus/lxd/state"
	"github.com/cyphar/incus/shared"
	"github.com/cyphar/incus/shared/api"
)

// VolumeBackup represents a custom volume backup.
type VolumeBackup struct {
	CommonBackup

	projectName string
	poolName    string
	volumeName  string
	volumeOnly  bool
}

// NewVolumeBackup instantiates a new VolumeBackup struct.
func NewVolumeBackup(state *state.State, projectName, poolName, volumeName string, ID int, name string, creationDate, expiryDate time.Time, volumeOnly, optimizedStorage bool) *VolumeBackup {
	return &VolumeBackup{
		CommonBackup: CommonBackup{
			state:            state,
			id:               ID,
			name:             name,
			creationDate:     creationDate,
			expiryDate:       expiryDate,
			optimizedStorage: optimizedStorage,
		},
		projectName: projectName,
		poolName:    poolName,
		volumeName:  volumeName,
		volumeOnly:  volumeOnly,
	}
}

// VolumeOnly returns whether only the volume itself is to be backed up.
func (b *VolumeBackup) VolumeOnly() bool {
	return b.volumeOnly
}

// OptimizedStorage returns whether the backup is to be performed using optimization format of the storage driver.
func (b *VolumeBackup) OptimizedStorage() bool {
	return b.optimizedStorage
}

// Rename renames a volume backup.
func (b *VolumeBackup) Rename(newName string) error {
	oldBackupPath := shared.VarPath("backups", "custom", b.poolName, project.StorageVolume(b.projectName, b.name))
	newBackupPath := shared.VarPath("backups", "custom", b.poolName, project.StorageVolume(b.projectName, newName))

	// Extract the old and new parent backup paths from the old and new backup names rather than use
	// instance.Name() as this may be in flux if the instance itself is being renamed, whereas the relevant
	// instance name is encoded into the backup names.
	oldParentName, _, _ := api.GetParentAndSnapshotName(b.name)
	oldParentBackupsPath := shared.VarPath("backups", "custom", b.poolName, project.StorageVolume(b.projectName, oldParentName))
	newParentName, _, _ := api.GetParentAndSnapshotName(newName)
	newParentBackupsPath := shared.VarPath("backups", "custom", b.poolName, project.StorageVolume(b.projectName, newParentName))

	revert := revert.New()
	defer revert.Fail()

	// Create the new backup path if doesn't exist.
	if !shared.PathExists(newParentBackupsPath) {
		err := os.MkdirAll(newParentBackupsPath, 0700)
		if err != nil {
			return err
		}
	}

	// Rename the backup directory.
	err := os.Rename(oldBackupPath, newBackupPath)
	if err != nil {
		return err
	}

	revert.Add(func() { _ = os.Rename(newBackupPath, oldBackupPath) })

	// Check if we can remove the old parent directory.
	empty, _ := shared.PathIsEmpty(oldParentBackupsPath)
	if empty {
		err := os.Remove(oldParentBackupsPath)
		if err != nil {
			return err
		}
	}

	// Rename the database record.
	err = b.state.DB.Cluster.RenameVolumeBackup(b.name, newName)
	if err != nil {
		return err
	}

	revert.Success()
	return nil
}

// Delete removes a volume backup.
func (b *VolumeBackup) Delete() error {
	backupPath := shared.VarPath("backups", "custom", b.poolName, project.StorageVolume(b.projectName, b.name))
	// Delete the on-disk data.
	if shared.PathExists(backupPath) {
		err := os.RemoveAll(backupPath)
		if err != nil {
			return err
		}
	}

	// Check if we can remove the volume directory.
	backupsPath := shared.VarPath("backups", "custom", b.poolName, project.StorageVolume(b.projectName, b.volumeName))
	empty, _ := shared.PathIsEmpty(backupsPath)
	if empty {
		err := os.Remove(backupsPath)
		if err != nil {
			return err
		}
	}

	// Remove the database record.
	err := b.state.DB.Cluster.DeleteStoragePoolVolumeBackup(b.name)
	if err != nil {
		return err
	}

	return nil
}

// Render returns a VolumeBackup struct of the backup.
func (b *VolumeBackup) Render() *api.StoragePoolVolumeBackup {
	return &api.StoragePoolVolumeBackup{
		Name:             strings.SplitN(b.name, "/", 2)[1],
		CreatedAt:        b.creationDate,
		ExpiresAt:        b.expiryDate,
		VolumeOnly:       b.volumeOnly,
		OptimizedStorage: b.optimizedStorage,
	}
}
