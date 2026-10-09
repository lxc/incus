package acl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/instance"
	"github.com/lxc/incus/v7/internal/server/network/ovn"
	"github.com/lxc/incus/v7/internal/server/project"
	"github.com/lxc/incus/v7/shared/api"
)

// normalizeACLCurrentExclusion checks caller identities without inventing name-based authority.
func normalizeACLCurrentExclusion(ctx context.Context, tx *db.ClusterTx, value any, device string) (project.ACLCurrentExclusion, error) {
	none := project.ACLCurrentExclusion{}
	if value == nil {
		if device != "" {
			return none, fmt.Errorf("Device exclusion requires an owner")
		}

		return none, nil
	}

	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if reflected.IsNil() {
			return none, fmt.Errorf("Nil ACL exclusion owner")
		}
	}
	switch owner := value.(type) {
	case instance.Instance:
		if owner.ID() <= 0 || owner.Project().Name == "" || device == "" {
			return none, fmt.Errorf("Invalid instance exclusion identity")
		}

		projectID, err := cluster.GetProjectID(ctx, tx.Tx(), owner.Project().Name)
		if err != nil {
			return none, err
		}

		return project.ACLCurrentExclusion{Kind: "instance", Instance: &project.InstanceNetworkReferenceKey{InstanceID: int64(owner.ID()), ProjectID: projectID, Device: device}}, nil
	case cluster.Profile:
		if owner.ID <= 0 || owner.Project == "" || device == "" {
			return none, fmt.Errorf("Invalid profile exclusion identity")
		}

		projectID, err := cluster.GetProjectID(ctx, tx.Tx(), owner.Project)
		if err != nil {
			return none, err
		}

		return project.ACLCurrentExclusion{Kind: "profile", Profile: &project.ProfileNetworkReferenceKey{ProfileID: int64(owner.ID), ProjectID: projectID, Device: device}}, nil
	case *api.Network:
		if owner.Name == "" || device != "" {
			return none, fmt.Errorf("Invalid legacy network exclusion")
		}
		// A name-only request cannot prove stable identity; preserve every network configuration root.
		return none, nil
	case project.NetworkUsageKey:
		if device != "" {
			return none, fmt.Errorf("Network exclusion cannot specify a device")
		}

		return project.ACLCurrentExclusion{Kind: "network", Network: &owner}, nil
	default:
		return none, fmt.Errorf("Unsupported ACL exclusion owner %T", value)
	}
}

type aclPortGroupIdentity struct {
	aclID     int64
	networkID int64
}

func canonicalACLID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != value {
		return 0, fmt.Errorf("Invalid canonical ACL port group ID %q", value)
	}

	return id, nil
}

// parseACLPortGroup recognizes only existing constructors, preserving unrelated grammar.
func parseACLPortGroup(name ovn.OVNPortGroup) (*aclPortGroupIdentity, error) {
	raw, ok := strings.CutPrefix(string(name), ovnACLPortGroupPrefix)
	if !ok {
		return nil, nil
	}

	acl, suffix, ok := strings.Cut(raw, "_")
	if !ok {
		return nil, nil
	}

	directional := slices.Contains([]string{"all", "ingress", "ingress_reversed", "egress", "egress_reversed"}, suffix)
	network, isNetwork := strings.CutPrefix(suffix, "net")
	if !directional && !isNetwork {
		return nil, nil
	}

	id, err := canonicalACLID(acl)
	if err != nil {
		return nil, err
	}

	identity := &aclPortGroupIdentity{aclID: id}
	if isNetwork {
		identity.networkID, err = canonicalACLID(network)
		if err != nil {
			return nil, err
		}

		if OVNACLNetworkPortGroupName(id, identity.networkID) != name {
			return nil, fmt.Errorf("Invalid network port group %q", name)
		}
	} else if !slices.Contains(OVNACLDirectionalPortGroups(id).PortGroups(), name) {
		return nil, fmt.Errorf("Invalid directional port group %q", name)
	}

	return identity, nil
}

// aclOwnPortGroups returns the well-formed directional and network port groups named for one ACL.
func aclOwnPortGroups(aclID int64, names []ovn.OVNPortGroup) []ovn.OVNPortGroup {
	own := []ovn.OVNPortGroup{}
	for _, name := range names {
		identity, err := parseACLPortGroup(name)
		if err == nil && identity != nil && identity.aclID == aclID {
			own = append(own, name)
		}
	}

	slices.Sort(own)
	return own
}

// selectUnusedACLPortGroups produces a snapshot selection, not durable backend deletion authority.
func selectUnusedACLPortGroups(ctx context.Context, clusterDB *db.Cluster, expectedProjectID int64, projectName string, ignore any, device string, keepNames []string, candidates []ovn.OVNPortGroup) ([]ovn.OVNPortGroup, error) {
	var plan []ovn.OVNPortGroup
	err := clusterDB.Transaction(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		plan, err = selectUnusedACLPortGroupsTx(ctx, tx, expectedProjectID, projectName, ignore, device, keepNames, candidates)
		return err
	})
	if err != nil {
		return nil, err
	}

	return plan, nil
}

// selectUnusedACLPortGroupsTx keeps selection inside a caller's existing normal reservation.
func selectUnusedACLPortGroupsTx(ctx context.Context, tx *db.ClusterTx, expectedProjectID int64, projectName string, ignore any, device string, keepNames []string, candidates []ovn.OVNPortGroup) ([]ovn.OVNPortGroup, error) {
	var plan []ovn.OVNPortGroup
	err := func() error {
		if expectedProjectID <= 0 || projectName == "" {
			return fmt.Errorf("Invalid ACL collector project identity")
		}

		projectID, err := cluster.GetProjectID(ctx, tx.Tx(), projectName)
		if err != nil {
			return err
		}

		if projectID != expectedProjectID {
			return fmt.Errorf("ACL collector project identity changed")
		}

		exclusion, err := normalizeACLCurrentExclusion(ctx, tx, ignore, device)
		if err != nil {
			return err
		}

		protection, err := project.ReadNetworkACLProtection(ctx, tx, projectID, exclusion)
		if err != nil {
			return err
		}

		keep := map[project.ACLUsageKey]bool{}
		byName := map[string]project.ACLUsageKey{}
		for key, value := range protection.ACLs {
			byName[value.Name] = key
		}

		for _, name := range keepNames {
			key, ok := byName[name]
			if !ok {
				return fmt.Errorf("Unknown kept ACL %q", name)
			}

			keep[key] = true
		}

		roots := map[project.ACLUsageKey]bool{}
		protected := map[ovn.OVNPortGroup]bool{}
		for key := range keep {
			roots[key] = true
		}

		add := func(network project.NetworkUsageKey, acl project.ACLUsageKey) {
			if acl.ACLID == 0 || protection.Networks[network].Type != "ovn" {
				return
			}

			roots[acl] = true
			protected[OVNACLNetworkPortGroupName(acl.ACLID, network.NetworkID)] = true
		}

		for _, root := range protection.NetworkRoots {
			add(root.Network, root.ACL)
		}

		addReference := func(r db.ProfileReferenceResource) {
			add(project.NetworkUsageKey{ProjectID: r.NetworkProjectID, NetworkID: r.NetworkID}, project.ACLUsageKey{ProjectID: r.ACLProjectID, ACLID: r.ACLID})
		}

		for _, owner := range protection.Owners.CurrentInstances {
			addReference(owner.ProfileReferenceResource)
		}

		for _, owner := range protection.Owners.CurrentProfiles {
			addReference(owner.ProfileReferenceResource)
		}

		for _, owner := range protection.Owners.Retained {
			addReference(owner.ProfileReferenceResource)
		}

		protectDirectional := func(key project.ACLUsageKey) {
			for _, name := range OVNACLDirectionalPortGroups(key.ACLID).PortGroups() {
				protected[name] = true
			}
		}
		for key := range roots {
			protectDirectional(key)
		}
		// Subjects do not become roots; only the fixed direct/keep roots contribute edges.
		for _, edge := range protection.Subjects {
			if roots[edge.Source] {
				protectDirectional(edge.Target)
			}
		}
		eligible := map[ovn.OVNPortGroup]bool{}
		for _, name := range candidates {
			identity, err := parseACLPortGroup(name)
			if err != nil {
				return err
			}

			if identity == nil {
				continue
			}

			// Database IDs are never reused, so a group naming a deleted ACL or network has no
			// owner that can protect it. Backend deletion still refuses a group with ports.
			var aclProjectID int64
			err = tx.Tx().QueryRowContext(ctx, `SELECT project_id FROM networks_acls WHERE id=?`, identity.aclID).Scan(&aclProjectID)
			if errors.Is(err, sql.ErrNoRows) {
				eligible[name] = true
				continue
			}

			if err != nil {
				return fmt.Errorf("Resolve candidate ACL %q: %w", name, err)
			}

			networkProjectID := aclProjectID
			var networkType int
			if identity.networkID != 0 {
				err = tx.Tx().QueryRowContext(ctx, `SELECT project_id, type FROM networks WHERE id=?`, identity.networkID).Scan(&networkProjectID, &networkType)
				if errors.Is(err, sql.ErrNoRows) {
					// Collect only this project's orphan; a foreign ACL's group is never adopted.
					if aclProjectID == projectID {
						eligible[name] = true
					}

					continue
				}

				if err != nil {
					return fmt.Errorf("Resolve candidate network %q: %w", name, err)
				}
			}
			if aclProjectID != projectID || networkProjectID != projectID {
				continue
			}

			if identity.networkID != 0 && networkType != int(db.NetworkTypeOVN) {
				return fmt.Errorf("Candidate %q names a non-OVN network", name)
			}

			key := project.ACLUsageKey{ProjectID: projectID, ACLID: identity.aclID}
			if !protected[name] && !keep[key] {
				eligible[name] = true
			}
		}
		// No plan escapes until every catalog, owner, keep name and candidate has passed validation.
		plan = make([]ovn.OVNPortGroup, 0, len(eligible))
		for name := range eligible {
			plan = append(plan, name)
		}

		slices.Sort(plan)
		return ctx.Err()
	}()
	if err != nil {
		return nil, err
	}

	return plan, nil
}
