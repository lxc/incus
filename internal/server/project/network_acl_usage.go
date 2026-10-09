//go:build linux && cgo && !agent

package project

import (
	"context"
	"fmt"
	"strings"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/shared/api"
	"github.com/lxc/incus/v7/shared/util"
	"github.com/lxc/incus/v7/shared/validate"
)

// NetworkUsageKey identifies a network independently of its consumers.
type NetworkUsageKey struct {
	ProjectID int64
	NetworkID int64
}

// ACLUsageKey identifies an ACL independently of its consumers.
type ACLUsageKey struct {
	ProjectID int64
	ACLID     int64
}

// ProfileNetworkReferenceKey identifies one standalone profile device.
type ProfileNetworkReferenceKey struct {
	ProjectID int64
	ProfileID int64
	Device    string
}

// ACLCurrentExclusion selects exactly one current owner; retained rows are never excluded.
type ACLCurrentExclusion struct {
	Kind     string
	Instance *InstanceNetworkReferenceKey
	Profile  *ProfileNetworkReferenceKey
	Network  *NetworkUsageKey
}

// NetworkACLRoot preserves a network configuration's independent ownership.
type NetworkACLRoot struct {
	Network NetworkUsageKey
	ACL     ACLUsageKey
}

// ACLSubjectEdge records one source-bound rule subject, including disabled rules.
type ACLSubjectEdge struct {
	Source    ACLUsageKey
	Target    ACLUsageKey
	Direction string
	RuleIndex int
	State     string
}

// NetworkACLProtection is conservative ownership, never an application target.
type NetworkACLProtection struct {
	ProjectID    int64
	ProjectName  string
	ACLs         map[ACLUsageKey]api.NetworkACL
	Networks     map[NetworkUsageKey]api.Network
	Owners       NetworkReferenceUsage
	NetworkRoots []NetworkACLRoot
	Subjects     []ACLSubjectEdge
}

func validateACLCurrentExclusion(ctx context.Context, tx *db.ClusterTx, projectID int64, e ACLCurrentExclusion) error {
	branches := 0
	if e.Instance != nil {
		branches++
	}

	if e.Profile != nil {
		branches++
	}

	if e.Network != nil {
		branches++
	}

	if e.Kind == "" && branches == 0 {
		return nil
	}

	if branches != 1 {
		return fmt.Errorf("Invalid ACL current exclusion branches")
	}

	var found int64
	switch e.Kind {
	case "instance":
		if e.Instance == nil || e.Instance.InstanceID <= 0 || e.Instance.ProjectID <= 0 || e.Instance.Device == "" {
			return fmt.Errorf("Invalid instance exclusion")
		}

		return tx.Tx().QueryRowContext(ctx, `SELECT id FROM instances WHERE id=? AND project_id=?`, e.Instance.InstanceID, e.Instance.ProjectID).Scan(&found)
	case "profile":
		if e.Profile == nil || e.Profile.ProfileID <= 0 || e.Profile.ProjectID <= 0 || e.Profile.Device == "" {
			return fmt.Errorf("Invalid profile exclusion")
		}

		return tx.Tx().QueryRowContext(ctx, `SELECT id FROM profiles WHERE id=? AND project_id=?`, e.Profile.ProfileID, e.Profile.ProjectID).Scan(&found)
	case "network":
		if e.Network == nil || e.Network.NetworkID <= 0 || e.Network.ProjectID <= 0 || e.Network.ProjectID != projectID {
			return fmt.Errorf("Invalid network exclusion")
		}

		return tx.Tx().QueryRowContext(ctx, `SELECT id FROM networks WHERE id=? AND project_id=?`, e.Network.NetworkID, e.Network.ProjectID).Scan(&found)
	default:
		return fmt.Errorf("Unknown ACL current exclusion %q", e.Kind)
	}
}

// aclSubjectName classifies reserved subjects before exact ACL catalog lookup.
func aclSubjectName(token string) bool {
	return token != "#internal" && token != "#external" && !strings.HasPrefix(token, "@") && !strings.HasPrefix(token, "$") &&
		validate.IsNetworkAddress(token) != nil && validate.IsNetworkAddressCIDR(token) != nil && validate.IsNetworkRange(token) != nil
}

// ReadNetworkACLProtection reads all protection in the caller's transaction, in any lifecycle state.
func ReadNetworkACLProtection(ctx context.Context, tx *db.ClusterTx, projectID int64, exclusion ACLCurrentExclusion) (*NetworkACLProtection, error) {
	if projectID <= 0 {
		return nil, fmt.Errorf("Invalid ACL resource project ID")
	}

	p := &NetworkACLProtection{ProjectID: projectID, ACLs: map[ACLUsageKey]api.NetworkACL{}, Networks: map[NetworkUsageKey]api.Network{}}
	err := tx.Tx().QueryRowContext(ctx, `SELECT name FROM projects WHERE id=?`, projectID).Scan(&p.ProjectName)
	if err != nil {
		return nil, err
	}

	err = validateACLCurrentExclusion(ctx, tx, projectID, exclusion)
	if err != nil {
		return nil, err
	}

	acls, err := cluster.GetNetworkACLs(ctx, tx.Tx(), cluster.NetworkACLFilter{Project: &p.ProjectName})
	if err != nil {
		return nil, err
	}

	names := map[string]ACLUsageKey{}
	for _, acl := range acls {
		id, value, err := cluster.GetNetworkACLAPI(ctx, tx.Tx(), p.ProjectName, acl.Name)
		if err != nil {
			return nil, err
		}

		_, duplicate := names[acl.Name]
		if id <= 0 || id != acl.ID || duplicate {
			return nil, fmt.Errorf("Invalid ACL catalog identity")
		}

		key := ACLUsageKey{ProjectID: projectID, ACLID: int64(id)}
		names[acl.Name] = key
		p.ACLs[key] = *value
	}

	networkNames, err := tx.GetNetworks(ctx, p.ProjectName)
	if err != nil {
		return nil, err
	}

	for _, name := range networkNames {
		id, network, _, err := tx.GetNetworkInAnyState(ctx, p.ProjectName, name)
		if err != nil {
			return nil, err
		}

		if id <= 0 {
			return nil, fmt.Errorf("Invalid network catalog identity")
		}

		key := NetworkUsageKey{ProjectID: projectID, NetworkID: id}
		p.Networks[key] = *network
		for _, name := range util.SplitNTrimSpace(network.Config["security.acls"], ",", -1, true) {
			acl, ok := names[name]
			if !ok {
				return nil, fmt.Errorf("Unknown network ACL %q", name)
			}

			if exclusion.Network == nil || key != *exclusion.Network {
				p.NetworkRoots = append(p.NetworkRoots, NetworkACLRoot{Network: key, ACL: acl})
			}
		}
	}

	usage, err := ReadNetworkReferenceUsage(ctx, tx, db.NetworkReferenceFilter{NetworkProjectID: projectID}, exclusion.Instance)
	if err != nil {
		return nil, err
	}

	for _, owner := range usage.CurrentProfiles {
		key := ProfileNetworkReferenceKey{ProjectID: owner.ProjectID, ProfileID: owner.ProfileID, Device: owner.Device}
		if exclusion.Profile == nil || key != *exclusion.Profile {
			p.Owners.CurrentProfiles = append(p.Owners.CurrentProfiles, owner)
		}
	}
	p.Owners.CurrentInstances = usage.CurrentInstances
	p.Owners.Retained = usage.Retained
	validateResource := func(r db.ProfileReferenceResource) error {
		network, ok := p.Networks[NetworkUsageKey{ProjectID: r.NetworkProjectID, NetworkID: r.NetworkID}]
		if !ok || network.Type != r.NetworkType {
			return fmt.Errorf("Network reference identity or type changed")
		}

		if r.ACLID == 0 && r.ACLProjectID == 0 {
			return nil
		}

		_, ok = p.ACLs[ACLUsageKey{ProjectID: r.ACLProjectID, ACLID: r.ACLID}]
		if !ok {
			return fmt.Errorf("ACL reference identity changed")
		}

		return nil
	}

	for _, owner := range usage.CurrentInstances {
		err = validateResource(owner.ProfileReferenceResource)
		if err != nil {
			return nil, err
		}
	}
	for _, owner := range usage.CurrentProfiles {
		err = validateResource(owner.ProfileReferenceResource)
		if err != nil {
			return nil, err
		}
	}
	for _, owner := range usage.Retained {
		err = validateResource(owner.ProfileReferenceResource)
		if err != nil {
			return nil, err
		}
	}
	for source, acl := range p.ACLs {
		for _, direction := range []string{"ingress", "egress"} {
			rules := acl.Ingress
			if direction == "egress" {
				rules = acl.Egress
			}

			for index, rule := range rules {
				subject := rule.Source
				if direction == "egress" {
					subject = rule.Destination
				}

				for _, token := range util.SplitNTrimSpace(subject, ",", -1, true) {
					if !aclSubjectName(token) {
						continue
					}

					target, ok := names[token]
					if !ok {
						return nil, fmt.Errorf("Unknown ACL rule subject %q", token)
					}

					p.Subjects = append(p.Subjects, ACLSubjectEdge{Source: source, Target: target, Direction: direction, RuleIndex: index, State: rule.State})
				}
			}
		}
	}
	return p, nil
}
