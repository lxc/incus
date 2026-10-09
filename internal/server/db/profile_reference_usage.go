//go:build linux && cgo && !agent

package db

import (
	"context"
	"fmt"
	"maps"
)

// NetworkReferenceFilter selects exact resource identities. Zero is a wildcard;
// all nonzero fields are conjunctive. Project IDs identify resource owners, not
// the project of the consuming instance or profile. Names are never filters.
type NetworkReferenceFilter struct {
	NetworkID        int64
	NetworkProjectID int64
	ACLID            int64
	ACLProjectID     int64
}

// Validate rejects invalid IDs rather than silently broadening a usage query.
func (f NetworkReferenceFilter) Validate() error {
	if f.NetworkID < 0 || f.NetworkProjectID < 0 || f.ACLID < 0 || f.ACLProjectID < 0 {
		return fmt.Errorf("Network reference filters require nonnegative IDs")
	}

	return nil
}

// Matches includes network-only rows unless an ACL identity was requested.
func (f NetworkReferenceFilter) Matches(r ProfileReferenceResource) bool {
	return (f.NetworkID == 0 || f.NetworkID == r.NetworkID) &&
		(f.NetworkProjectID == 0 || f.NetworkProjectID == r.NetworkProjectID) &&
		(f.ACLID == 0 || f.ACLID == r.ACLID) &&
		(f.ACLProjectID == 0 || f.ACLProjectID == r.ACLProjectID)
}

// NetworkReference adds diagnostic names to an exact managed NIC reference.
// Config is an independent copy of the captured NIC; its old network/ACL names
// are not rewritten when an identity has since been renamed.
type NetworkReference struct {
	ProfileReferenceResource
	ACLName    string
	ACLProject string
}

// ResolveNetworkReference resolves names by the supplied IDs and recorded
// resource project IDs. It never consults current project sharing settings or
// resolves Config names again. Missing or inconsistent identities fail closed.
// No lifecycle-state check is made: non-Created resources still have usage.
func (c *ClusterTx) ResolveNetworkReference(ctx context.Context, resource ProfileReferenceResource) (*NetworkReference, error) {
	r := &NetworkReference{ProfileReferenceResource: resource}
	r.Config = maps.Clone(resource.Config)
	err := c.tx.QueryRowContext(ctx, `SELECT networks.name, projects.name FROM networks JOIN projects ON projects.id=networks.project_id WHERE networks.id=? AND projects.id=?`, r.NetworkID, r.NetworkProjectID).Scan(&r.NetworkName, &r.NetworkProject)
	if err != nil {
		return nil, fmt.Errorf("Resolve network reference ID %d in project ID %d: %w", r.NetworkID, r.NetworkProjectID, err)
	}

	if r.ACLID == 0 {
		if r.ACLProjectID != 0 {
			return nil, fmt.Errorf("Network-only reference has an ACL project ID")
		}

		return r, nil
	}

	err = c.tx.QueryRowContext(ctx, `SELECT networks_acls.name, projects.name FROM networks_acls JOIN projects ON projects.id=networks_acls.project_id WHERE networks_acls.id=? AND projects.id=?`, r.ACLID, r.ACLProjectID).Scan(&r.ACLName, &r.ACLProject)
	if err != nil {
		return nil, fmt.Errorf("Resolve ACL reference ID %d in project ID %d: %w", r.ACLID, r.ACLProjectID, err)
	}

	return r, nil
}

// RetainedProfileReference is conservative protection, never a live apply or
// reload target. Role describes capture provenance only. In particular, a
// baseline role can outlive application or an unknown/failed outcome; it does
// not prove which NIC config is currently applied. This projection deliberately
// provides no inferred applied baseline or authority to mutate a live device.
type RetainedProfileReference struct {
	ProfileReferenceUsage
	InstanceName    string
	InstanceProject string
	ACLName         string
	ACLProject      string
}

// RetainedProfileReferenceUsage projects every matching retained owner without
// deduplication or current-device exclusion. Consumer and attempt rows, roles,
// sequence, placement and captured NIC config retain their independent identity.
func (c *ClusterTx) RetainedProfileReferenceUsage(ctx context.Context, filter NetworkReferenceFilter) ([]RetainedProfileReference, error) {
	err := filter.Validate()
	if err != nil {
		return nil, err
	}

	usage, err := c.ProfileReferenceUsage(ctx, filter.NetworkID, filter.ACLID)
	if err != nil {
		return nil, err
	}

	result := []RetainedProfileReference{}
	for _, owner := range usage {
		if !filter.Matches(owner.ProfileReferenceResource) {
			continue
		}

		reference, err := c.ResolveNetworkReference(ctx, owner.ProfileReferenceResource)
		if err != nil {
			return nil, err
		}

		owner.ProfileReferenceResource = reference.ProfileReferenceResource
		entry := RetainedProfileReference{ProfileReferenceUsage: owner, ACLName: reference.ACLName, ACLProject: reference.ACLProject}
		// Resolve the captured project separately from today's instance placement.
		// A move cannot reinterpret the recorded owner project or placement revision.
		err = c.tx.QueryRowContext(ctx, `SELECT instances.name, projects.name FROM instances CROSS JOIN projects WHERE instances.id=? AND projects.id=?`, owner.InstanceID, owner.ProjectID).Scan(&entry.InstanceName, &entry.InstanceProject)
		if err != nil {
			return nil, fmt.Errorf("Resolve retained instance ID %d and project ID %d: %w", owner.InstanceID, owner.ProjectID, err)
		}

		result = append(result, entry)
	}

	return result, nil
}
