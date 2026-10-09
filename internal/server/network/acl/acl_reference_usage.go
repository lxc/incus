package acl

import (
	"context"
	"errors"
	"fmt"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/project"
	"github.com/lxc/incus/v7/internal/version"
	"github.com/lxc/incus/v7/shared/api"
)

// referenceUsage validates this ACL before reading its current and retained NIC owners.
func (d *common) referenceUsage() (*project.NetworkReferenceUsage, error) {
	if d.id <= 0 || d.projectName == "" || d.info == nil || d.info.Name == "" {
		return nil, errors.New("Invalid network ACL identity")
	}

	var usage *project.NetworkReferenceUsage
	err := d.state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		id, err := cluster.GetNetworkACLID(ctx, tx.Tx(), d.projectName, d.info.Name)
		if err != nil {
			return err
		}

		if id <= 0 || id != d.id {
			return errors.New("Network ACL identity changed")
		}

		projectID, err := cluster.GetProjectID(ctx, tx.Tx(), d.projectName)
		if err != nil {
			return err
		}

		if projectID <= 0 {
			return errors.New("Invalid network ACL project identity")
		}

		usage, err = project.ReadNetworkReferenceUsage(ctx, tx, db.NetworkReferenceFilter{ACLID: id, ACLProjectID: projectID}, nil)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("Failed getting ACL reference usage: %w", err)
	}

	return usage, nil
}

// referenceUsedBy renders ownership for API usage and conservative destructive admission.
func (d *common) referenceUsedBy(firstOnly bool) ([]string, error) {
	// Validate identity even when an ordinary dependency could satisfy firstOnly.
	usage, err := d.referenceUsage()
	if err != nil {
		return nil, err
	}

	usedBy := []string{}
	seen := map[string]struct{}{}
	appendURL := func(kind string, name string, projectName string) {
		uri := api.NewURL().Path(version.APIVersion, kind, name).Project(projectName).String()
		_, found := seen[uri]
		if !found {
			seen[uri] = struct{}{}
			usedBy = append(usedBy, uri)
		}
	}

	// Deduplicate presentation only; retained owners remain independent of current owners.
	for _, owner := range usage.CurrentInstances {
		appendURL("instances", owner.InstanceName, owner.InstanceProject)
	}

	for _, owner := range usage.CurrentProfiles {
		appendURL("profiles", owner.ProfileName, owner.ProjectName)
	}

	for _, owner := range usage.Retained {
		appendURL("instances", owner.InstanceName, owner.InstanceProject)
	}

	if firstOnly && len(usedBy) > 0 {
		return usedBy[:1], nil
	}

	err = UsedBy(d.state, d.projectName, func(ctx context.Context, tx *db.ClusterTx, _ []string, usageType any, _ string, _ map[string]string) error {
		switch u := usageType.(type) {
		case db.InstanceArgs, cluster.Profile:
			// Managed NICs above use named network resolution; the legacy project-only lookup does not.
			return nil
		case *api.Network:
			appendURL("networks", u.Name, d.projectName)
		case *api.NetworkACL:
			appendURL("network-acls", u.Name, d.projectName)
		default:
			return fmt.Errorf("Unrecognised usage type %T", u)
		}

		if firstOnly {
			return db.ErrInstanceListStop
		}

		return nil
	}, d.info.Name)
	if err != nil && !errors.Is(err, db.ErrInstanceListStop) {
		return nil, fmt.Errorf("Failed getting ACL usage: %w", err)
	}

	return usedBy, nil
}
