//go:build linux && cgo && !agent

package project

import (
	"context"
	"slices"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/shared/util"
)

// InstanceNetworkReferenceKey distinguishes devices even when bridge instances
// have equal names in different projects. It also keys current-device exclusion.
type InstanceNetworkReferenceKey struct {
	InstanceID int64
	ProjectID  int64
	Device     string
}

// CurrentInstanceNetworkReference is current desired NIC usage, not applied state.
type CurrentInstanceNetworkReference struct {
	db.NetworkReference
	Key             InstanceNetworkReferenceKey
	InstanceName    string
	InstanceProject string
	MemberID        int64
}

// CurrentProfileNetworkReference preserves standalone profile NIC references,
// even when no instance consumes the profile or a consumer overrides the device.
// Resource resolution here uses the profile owner's project; each consuming
// instance is expanded and resolved separately in CurrentInstances.
type CurrentProfileNetworkReference struct {
	db.NetworkReference
	ProfileID   int64
	ProjectID   int64
	ProfileName string
	ProjectName string
}

// NetworkReferenceUsage is an unwired managed-NIC usage union. Its three owner
// sets are independent; neither current deduplication nor instance-device
// exclusion may remove retained protection. None of these sets proves a safe
// live reload target: current is desired, retained is conservative historical
// protection, and an unknown outcome requires separate application evidence.
// Network-level ACL config and ACL rule/address-set dependencies remain separate
// inputs for future adapters. This reader performs no backend or cleanup work.
type NetworkReferenceUsage struct {
	CurrentInstances []CurrentInstanceNetworkReference
	CurrentProfiles  []CurrentProfileNetworkReference
	Retained         []db.RetainedProfileReference
}

// CurrentInstanceNetworkReferences reads all current managed NIC references in
// the caller's transaction. CaptureProfileReferenceSnapshot uses the real
// GetInstanceProfiles order, ExpandInstanceDevices whole-device overrides, and
// NetworkProjectForNameFromRecord separately for every device. It neither needs
// nor creates an applied baseline. Snapshots of instances are not instance rows.
func CurrentInstanceNetworkReferences(ctx context.Context, tx *db.ClusterTx, filter db.NetworkReferenceFilter, exclude *InstanceNetworkReferenceKey) ([]CurrentInstanceNetworkReference, error) {
	err := filter.Validate()
	if err != nil {
		return nil, err
	}

	instances, err := cluster.GetInstances(ctx, tx.Tx())
	if err != nil {
		return nil, err
	}

	result := []CurrentInstanceNetworkReference{}
	for _, instance := range instances {
		snapshot, err := CaptureProfileReferenceSnapshot(ctx, tx, int64(instance.ID))
		if err != nil {
			return nil, err
		}

		for _, resource := range snapshot.Resources {
			key := InstanceNetworkReferenceKey{InstanceID: snapshot.InstanceID, ProjectID: snapshot.ProjectID, Device: resource.Device}
			if (exclude != nil && key == *exclude) || !filter.Matches(resource) {
				continue
			}

			reference, err := tx.ResolveNetworkReference(ctx, resource)
			if err != nil {
				return nil, err
			}

			result = append(result, CurrentInstanceNetworkReference{NetworkReference: *reference, Key: key, InstanceName: instance.Name, InstanceProject: snapshot.Project.Name, MemberID: snapshot.MemberID})
		}
	}
	return result, nil
}

// CurrentProfileNetworkReferences resolves each standalone managed profile NIC
// by its own network name, including named shared-default networks.
func CurrentProfileNetworkReferences(ctx context.Context, tx *db.ClusterTx, filter db.NetworkReferenceFilter) ([]CurrentProfileNetworkReference, error) {
	err := filter.Validate()
	if err != nil {
		return nil, err
	}

	profiles, err := cluster.GetProfiles(ctx, tx.Tx())
	if err != nil {
		return nil, err
	}

	result := []CurrentProfileNetworkReference{}
	for _, profile := range profiles {
		owner, err := cluster.GetProject(ctx, tx.Tx(), profile.Project)
		if err != nil {
			return nil, err
		}

		project, err := owner.ToAPI(ctx, tx.Tx())
		if err != nil {
			return nil, err
		}

		value, err := profile.ToAPI(ctx, tx.Tx(), nil, nil)
		if err != nil {
			return nil, err
		}

		names := make([]string, 0, len(value.Devices))
		for name := range value.Devices {
			names = append(names, name)
		}

		slices.Sort(names)
		for _, name := range names {
			device := value.Devices[name]
			if device["type"] != "nic" || device["network"] == "" {
				continue
			}

			networkProject := NetworkProjectForNameFromRecord(project, device["network"])
			networkID, network, _, err := tx.GetNetworkInAnyState(ctx, networkProject, device["network"])
			if err != nil {
				return nil, err
			}

			projectID, err := cluster.GetProjectID(ctx, tx.Tx(), networkProject)
			if err != nil {
				return nil, err
			}

			resource := db.ProfileReferenceResource{Device: name, NetworkID: networkID, NetworkProjectID: projectID, NetworkType: network.Type, Config: device}
			resources := []db.ProfileReferenceResource{resource}
			acls := util.SplitNTrimSpace(device["security.acls"], ",", -1, true)
			slices.Sort(acls)
			for _, acl := range slices.Compact(acls) {
				resource.ACLID, err = cluster.GetNetworkACLID(ctx, tx.Tx(), networkProject, acl)
				if err != nil {
					return nil, err
				}

				resource.ACLProjectID = projectID
				resources = append(resources, resource)
			}

			for _, resource := range resources {
				if !filter.Matches(resource) {
					continue
				}

				reference, err := tx.ResolveNetworkReference(ctx, resource)
				if err != nil {
					return nil, err
				}

				result = append(result, CurrentProfileNetworkReference{NetworkReference: *reference, ProfileID: int64(profile.ID), ProjectID: int64(owner.ID), ProfileName: profile.Name, ProjectName: profile.Project})
			}
		}
	}

	return result, nil
}

// ReadNetworkReferenceUsage takes one transactionally consistent union. Exclusion
// applies only to current instance devices; retained owners are always read with
// only the exact resource filter, including the excluded device's own attempts.
// On any read error no partial union is returned as evidence of absence.
func ReadNetworkReferenceUsage(ctx context.Context, tx *db.ClusterTx, filter db.NetworkReferenceFilter, exclude *InstanceNetworkReferenceKey) (*NetworkReferenceUsage, error) {
	retained, err := tx.RetainedProfileReferenceUsage(ctx, filter)
	if err != nil {
		return nil, err
	}

	instances, err := CurrentInstanceNetworkReferences(ctx, tx, filter, exclude)
	if err != nil {
		return nil, err
	}

	profiles, err := CurrentProfileNetworkReferences(ctx, tx, filter)
	if err != nil {
		return nil, err
	}

	return &NetworkReferenceUsage{CurrentInstances: instances, CurrentProfiles: profiles, Retained: retained}, nil
}
