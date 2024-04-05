package main

import (
	"context"
	"fmt"

	internalInstance "github.com/lxc/incus/v6/internal/instance"
	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/db/cluster"
	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/instance/instancetype"
	"github.com/lxc/incus/v6/internal/server/project"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/api"
)

func doProfileUpdate(ctx context.Context, s *state.State, p api.Project, profileName string, id int64, profile *api.Profile, req api.ProfilePut) error {
	// Check project limits.
	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		return project.AllowProfileUpdate(tx, p.Name, profileName, req)
	})
	if err != nil {
		return err
	}

	// Quick checks.
	err = instance.ValidConfig(s.OS, req.Config, false, instancetype.Any)
	if err != nil {
		return err
	}

	// Profiles can be applied to any instance type, so just use instancetype.Any type for validation so that
	// instance type specific validation checks are not performed.
	err = instance.ValidDevices(s, p, instancetype.Any, deviceConfig.NewDevices(req.Devices), nil)
	if err != nil {
		return err
	}

	insts, projects, err := getProfileInstancesInfo(ctx, s.DB.Cluster, p.Name, profileName)
	if err != nil {
		return fmt.Errorf("Failed to query instances associated with profile %q: %w", profileName, err)
	}

	// Check if the root disk device's pool would be changed or removed and prevent that if there are instances
	// using that root disk device.
	oldProfileRootDiskDeviceKey, oldProfileRootDiskDevice, _ := internalInstance.GetRootDiskDevice(profile.Devices)
	_, newProfileRootDiskDevice, _ := internalInstance.GetRootDiskDevice(req.Devices)
	if len(insts) > 0 && oldProfileRootDiskDevice["pool"] != "" && newProfileRootDiskDevice["pool"] == "" || (oldProfileRootDiskDevice["pool"] != newProfileRootDiskDevice["pool"]) {
		// Check for instances using the device.
		for _, inst := range insts {
			// Check if the device is locally overridden.
			k, v, _ := internalInstance.GetRootDiskDevice(inst.Devices.CloneNative())
			if k != "" && v["pool"] != "" {
				continue
			}

			err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
				// Check what profile the device comes from by working backwards along the profiles list.
				for i := len(inst.Profiles) - 1; i >= 0; i-- {
					_, profile, err := tx.GetProfile(ctx, p.Name, inst.Profiles[i].Name)
					if err != nil {
						return err
					}

					// Check if we find a match for the device.
					_, ok := profile.Devices[oldProfileRootDiskDeviceKey]
					if ok {
						// Found the profile.
						if inst.Profiles[i].Name == profileName {
							// If it's the current profile, then we can't modify that root device.
							return fmt.Errorf("At least one instance relies on this profile's root disk device")
						}

						// If it's not, then move on to the next instance.
						break
					}
				}

				return nil
			})
			if err != nil {
				return err
			}
		}
	}

	// Update the database.
	err = s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		devices, err := cluster.APIToDevices(req.Devices)
		if err != nil {
			return err
		}

		err = cluster.UpdateProfile(ctx, tx.Tx(), p.Name, profileName, cluster.Profile{
			Project:     p.Name,
			Name:        profileName,
			Description: req.Description,
		})
		if err != nil {
			return err
		}

		id, err := cluster.GetProfileID(ctx, tx.Tx(), p.Name, profileName)
		if err != nil {
			return err
		}

		err = cluster.UpdateProfileConfig(ctx, tx.Tx(), id, req.Config)
		if err != nil {
			return err
		}

		err = cluster.UpdateProfileDevices(ctx, tx.Tx(), id, devices)
		if err != nil {
			return err
		}

		newProfiles, err := cluster.GetProfilesIfEnabled(ctx, tx.Tx(), p.Name, []string{profileName})
		if err != nil {
			return err
		}

		if len(newProfiles) != 1 {
			return fmt.Errorf("Failed to find profile %q in project %q", profileName, p.Name)
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Update all the instances on this node using the profile. Must be done after db.TxCommit due to DB lock.
	failures := map[*db.InstanceArgs]error{}
	for _, it := range insts {
		inst := it // Local var for instance pointer.

		if inst.Node != "" && inst.Node != s.ServerName {
			continue // This instance does not belong to this member, skip.
		}

		err := doProfileUpdateInstance(ctx, s, inst, *projects[inst.Project])
		if err != nil {
			failures[&inst] = err
		}
	}

	if len(failures) != 0 {
		msg := "The following instances failed to update (profile change still saved):\n"
		for inst, err := range failures {
			msg += fmt.Sprintf(" - Project: %s, Instance: %s: %v\n", inst.Project, inst.Name, err)
		}

		return fmt.Errorf("%s", msg)
	}

	return nil
}

// Like doProfileUpdate but does not update the database, since it was already
// updated by doProfileUpdate itself, called on the notifying node.
func doProfileUpdateCluster(ctx context.Context, s *state.State, projectName string, profileName string, old api.ProfilePut) error {
	insts, projects, err := getProfileInstancesInfo(ctx, s.DB.Cluster, projectName, profileName)
	if err != nil {
		return fmt.Errorf("Failed to query instances associated with profile %q: %w", profileName, err)
	}

	failures := map[*db.InstanceArgs]error{}
	for _, it := range insts {
		inst := it // Local var for instance pointer.

		if inst.Node != "" && inst.Node != s.ServerName {
			continue // This instance does not belong to this member, skip.
		}

		for i, profile := range inst.Profiles {
			if profile.Name == profileName {
				// As profile has already been updated in the database by this point, overwrite the
				// new config from the database with the old config and devices, so that
				// doProfileUpdateInstance will detect the changes and apply them.
				inst.Profiles[i].Config = old.Config
				inst.Profiles[i].Devices = old.Devices
				break
			}
		}

		err := doProfileUpdateInstance(ctx, s, inst, *projects[inst.Project])
		if err != nil {
			failures[&inst] = err
		}
	}

	if len(failures) != 0 {
		msg := "The following instances failed to update (profile change still saved):\n"
		for inst, err := range failures {
			msg += fmt.Sprintf(" - Project: %s, Instance: %s: %v\n", inst.Project, inst.Name, err)
		}

		return fmt.Errorf("%s", msg)
	}

	return nil
}

// Profile update of a single instance.
func doProfileUpdateInstance(ctx context.Context, s *state.State, args db.InstanceArgs, p api.Project) error {
	profileNames := make([]string, 0, len(args.Profiles))

	for _, profile := range args.Profiles {
		profileNames = append(profileNames, profile.Name)
	}

	var profiles []api.Profile

	err := s.DB.Cluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		profiles, err = tx.GetProfiles(ctx, args.Project, profileNames)

		return err
	})
	if err != nil {
		return err
	}

	// Load the instance using the old profile config.
	inst, err := instance.Load(s, args, p)
	if err != nil {
		return err
	}

	// Update will internally load the new profile configs and detect the changes to apply.
	return inst.Update(db.InstanceArgs{
		Architecture: inst.Architecture(),
		Config:       inst.LocalConfig(),
		Description:  inst.Description(),
		Devices:      inst.LocalDevices(),
		Ephemeral:    inst.IsEphemeral(),
		Profiles:     profiles, // Supply with new profile config.
		Project:      inst.Project().Name,
		Type:         inst.Type(),
		Snapshot:     inst.IsSnapshot(),
	}, true)
}

// Query the db for information about instances associated with the given profile.
func getProfileInstancesInfo(ctx context.Context, dbCluster *db.Cluster, projectName string, profileName string) (map[int]db.InstanceArgs, map[string]*api.Project, error) {
	var projectInstNames map[string][]string

	// Query the db for information about instances associated with the given profile.
	err := dbCluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error

		projectInstNames, err = tx.GetInstancesWithProfile(ctx, projectName, profileName)

		return err
	})
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to query instances with profile %q: %w", profileName, err)
	}

	var instances map[int]db.InstanceArgs
	projects := make(map[string]*api.Project)

	err = dbCluster.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var dbInstances []cluster.Instance

		for instProject, instNames := range projectInstNames {
			// Load project if not already loaded.
			_, found := projects[instProject]
			if !found {
				dbProject, err := cluster.GetProject(context.Background(), tx.Tx(), instProject)
				if err != nil {
					return err
				}

				projects[instProject], err = dbProject.ToAPI(ctx, tx.Tx())
				if err != nil {
					return err
				}
			}

			for _, instName := range instNames {
				dbInst, err := cluster.GetInstance(ctx, tx.Tx(), instProject, instName)
				if err != nil {
					return err
				}

				dbInstances = append(dbInstances, *dbInst)
			}
		}

		instances, err = tx.InstancesToInstanceArgs(ctx, true, dbInstances...)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to fetch instances: %w", err)
	}

	return instances, projects, nil
}
