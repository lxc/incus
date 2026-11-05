package main

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/lxc/incus/v7/internal/server/backup"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/internal/server/project"
	"github.com/lxc/incus/v7/internal/server/response"
	"github.com/lxc/incus/v7/internal/server/state"
	storagePools "github.com/lxc/incus/v7/internal/server/storage"
	"github.com/lxc/incus/v7/internal/version"
	"github.com/lxc/incus/v7/shared/api"
)

var supportedVolumeTypes = []int{db.StoragePoolVolumeTypeContainer, db.StoragePoolVolumeTypeVM, db.StoragePoolVolumeTypeCustom, db.StoragePoolVolumeTypeImage}

func storagePoolVolumeUpdateUsers(ctx context.Context, s *state.State, projectName string, oldPoolName string, oldVol *api.StorageVolume, newPoolName string, newVol *api.StorageVolume) error {
	// Collect the instances using the volume inside the listing transaction, then update each one
	// outside it, so no database query runs in the callback and each write holds the instance lock.
	type instRef struct {
		project string
		name    string
	}

	var instRefs []instRef
	err := storagePools.VolumeUsedByInstanceDevices(s, oldPoolName, projectName, oldVol, false, func(dbInst db.InstanceArgs, project api.Project, usedByDevices []string) error {
		instRefs = append(instRefs, instRef{project: dbInst.Project, name: dbInst.Name})
		return nil
	})
	if err != nil {
		return err
	}

	for _, ref := range instRefs {
		err := storagePoolVolumeUpdateInstance(s, ref.project, ref.name, oldPoolName, oldVol, newPoolName, newVol)
		if err != nil {
			return err
		}
	}

	// Update all profiles that are using the volume with a device.
	err = storagePools.VolumeUsedByProfileDevices(s, oldPoolName, projectName, oldVol, func(profileID int64, profile api.Profile, p api.Project, usedByDevices []string) error {
		for name, dev := range profile.Devices {
			if slices.Contains(usedByDevices, name) {
				dev["pool"] = newPoolName

				volFields := strings.SplitN(dev["source"], "/", 2)
				volFields[0] = newVol.Name
				dev["source"] = strings.Join(volFields, "/")
			}
		}

		pUpdate := api.ProfilePut{}
		pUpdate.Config = profile.Config
		pUpdate.Description = profile.Description
		pUpdate.Devices = profile.Devices
		err = doProfileUpdate(ctx, s, p, profile.Name, &profile, pUpdate)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

// storagePoolVolumeUpdateInstance rewrites one instance's disk devices that point at a renamed or
// moved custom volume, holding the instance operation lock and reloading the current record so a
// concurrent instance update can't be reverted by writing back a stale copy.
func storagePoolVolumeUpdateInstance(s *state.State, projectName string, instName string, oldPoolName string, oldVol *api.StorageVolume, newPoolName string, newVol *api.StorageVolume) error {
	unlock, err := instanceOperationLock(s.ShutdownCtx, projectName, instName)
	if err != nil {
		return err
	}

	defer unlock()

	inst, err := instance.LoadByProjectAndName(s, projectName, instName)
	if err != nil {
		return err
	}

	localDevices := inst.LocalDevices()
	for _, dev := range localDevices {
		if dev["type"] != "disk" || dev["pool"] != oldPoolName {
			continue
		}

		volFields := strings.SplitN(dev["source"], "/", 2)
		if volFields[0] != oldVol.Name {
			continue
		}

		dev["pool"] = newPoolName
		volFields[0] = newVol.Name
		dev["source"] = strings.Join(volFields, "/")
	}

	args := db.InstanceArgs{
		Architecture: inst.Architecture(),
		Description:  inst.Description(),
		Config:       inst.LocalConfig(),
		Devices:      localDevices,
		Ephemeral:    inst.IsEphemeral(),
		Profiles:     inst.Profiles(),
		Project:      inst.Project().Name,
		Type:         inst.Type(),
		Snapshot:     inst.IsSnapshot(),
	}

	return inst.Update(args, false)
}

// storagePoolVolumeUsedByGet returns a list of URL resources that use the volume.
func storagePoolVolumeUsedByGet(s *state.State, requestProjectName string, poolName string, vol *db.StorageVolume) ([]string, error) {
	// Handle instance volumes.
	if vol.Type == db.StoragePoolVolumeTypeNameContainer || vol.Type == db.StoragePoolVolumeTypeNameVM {
		volName, snapName, isSnap := api.GetParentAndSnapshotName(vol.Name)
		if isSnap {
			return []string{api.NewURL().Path(version.APIVersion, "instances", volName, "snapshots", snapName).Project(vol.Project).String()}, nil
		}

		return []string{api.NewURL().Path(version.APIVersion, "instances", volName).Project(vol.Project).String()}, nil
	}

	// Handle image volumes.
	if vol.Type == db.StoragePoolVolumeTypeNameImage {
		// Fall back to the volume's project when listing all projects.
		projectName := requestProjectName
		if projectName == "" {
			projectName = vol.Project
		}

		return []string{api.NewURL().Path(version.APIVersion, "images", vol.Name).Project(projectName).Target(vol.Location).String()}, nil
	}

	// Check if the daemon itself is using it.
	frag, err := storagePools.VolumeUsedByDaemon(s, poolName, vol.Name)
	if err != nil {
		return []string{}, err
	}

	if frag != "" {
		return []string{api.NewURL().Path(version.APIVersion).Target(vol.Location).Fragment(frag).String()}, nil
	}

	// Look for instances using this volume.
	volumeUsedBy := []string{}

	// Pass false to expandDevices, as we only want to see instances directly using a volume, rather than their
	// profiles using a volume.
	err = storagePools.VolumeUsedByInstanceDevices(s, poolName, vol.Project, &vol.StorageVolume, false, func(inst db.InstanceArgs, p api.Project, usedByDevices []string) error {
		volumeUsedBy = append(volumeUsedBy, api.NewURL().Path(version.APIVersion, "instances", inst.Name).Project(inst.Project).String())
		return nil
	})
	if err != nil {
		return []string{}, err
	}

	// Match profiles against the volume's own project, the request project may be empty or a non-storage one.
	err = storagePools.VolumeUsedByProfileDevices(s, poolName, vol.Project, &vol.StorageVolume, func(profileID int64, profile api.Profile, p api.Project, usedByDevices []string) error {
		volumeUsedBy = append(volumeUsedBy, api.NewURL().Path(version.APIVersion, "profiles", profile.Name).Project(p.Name).String())
		return nil
	})
	if err != nil {
		return []string{}, err
	}

	return volumeUsedBy, nil
}

func storagePoolVolumeBackupLoadByName(ctx context.Context, s *state.State, projectName, poolName, backupName string) (*backup.VolumeBackup, error) {
	var b db.StoragePoolVolumeBackup

	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		b, err = tx.GetStoragePoolVolumeBackup(ctx, projectName, poolName, backupName)
		return err
	})
	if err != nil {
		return nil, err
	}

	volumeName, _, _ := strings.Cut(backupName, "/")
	volBackup := backup.NewVolumeBackup(s, projectName, poolName, volumeName, b.ID, b.Name, b.CreationDate, b.ExpiryDate, b.VolumeOnly, b.OptimizedStorage)

	return volBackup, nil
}

// storagePoolMemberAllowed checks that the project may place member-local storage on this cluster member.
func storagePoolMemberAllowed(s *state.State, r *http.Request, poolName string, projectName string) response.Response {
	if !s.ServerClustered {
		return nil
	}

	pool, err := storagePools.LoadByName(s, poolName)
	if err != nil {
		return response.SmartError(err)
	}

	if pool.Driver().Info().Remote {
		return nil
	}

	err = s.DB.Cluster.Transaction(r.Context(), func(ctx context.Context, tx *db.ClusterTx) error {
		return project.CheckMemberAllowed(ctx, tx, projectName, s.ServerName)
	})
	if err != nil {
		return response.SmartError(err)
	}

	return nil
}
