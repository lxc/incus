//go:build linux && cgo && !agent

package db

import (
	"context"
	"fmt"
	"net/http"

	"github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
)

// GetNetworkACLIDsByNames returns a map of names to IDs of existing Network ACLs.
func (c *ClusterTx) GetNetworkACLIDsByNames(ctx context.Context, project string) (map[string]int64, error) {
	acls, err := cluster.GetNetworkACLs(ctx, c.tx, cluster.NetworkACLFilter{Project: &project})
	if err != nil {
		return nil, err
	}

	out := make(map[string]int64, len(acls))
	for _, acl := range acls {
		out[acl.Name] = int64(acl.ID)
	}

	return out, nil
}

// GetNetworkACL returns the Network ACL with the given name in the given project.
func (c *ClusterTx) GetNetworkACL(ctx context.Context, projectName, name string) (int64, *api.NetworkACL, error) {
	acls, err := cluster.GetNetworkACLs(ctx, c.tx, cluster.NetworkACLFilter{Project: &projectName, Name: &name})
	if err != nil {
		return -1, nil, err
	}

	if len(acls) == 0 {
		return -1, nil, api.StatusErrorf(http.StatusNotFound, "Network ACL not found")
	}

	acl := acls[0]
	apiACL, err := acl.ToAPI(ctx, c.tx)
	if err != nil {
		return -1, nil, fmt.Errorf("Failed loading config: %w", err)
	}

	return int64(acl.ID), apiACL, nil
}

// GetNetworkACLNameAndProjectWithID returns the network ACL name and project name for the given ID.
func (c *ClusterTx) GetNetworkACLNameAndProjectWithID(ctx context.Context, networkACLID int) (string, string, error) {
	filter := cluster.NetworkACLFilter{ID: &networkACLID}
	acls, err := cluster.GetNetworkACLs(ctx, c.tx, filter)
	if err != nil {
		return "", "", err
	}

	if len(acls) == 0 {
		return "", "", api.StatusErrorf(http.StatusNotFound, "Network ACL not found")
	}

	return acls[0].Name, acls[0].Project, nil
}

// CreateNetworkACL creates a new Network ACL.
func (c *ClusterTx) CreateNetworkACL(ctx context.Context, projectName string, info *api.NetworkACLsPost) (int64, error) {
	acl := cluster.NetworkACL{
		Project:     projectName,
		Name:        info.Name,
		Description: info.Description,
		Ingress:     info.Ingress,
		Egress:      info.Egress,
	}

	id, err := cluster.CreateNetworkACL(ctx, c.tx, acl)
	if err != nil {
		return -1, err
	}

	if info.Config != nil {
		err := cluster.CreateNetworkACLConfig(ctx, c.tx, id, info.Config)
		if err != nil {
			return -1, err
		}
	}

	return id, nil
}

// UpdateNetworkACL updates the Network ACL with the given ID.
func (c *ClusterTx) UpdateNetworkACL(ctx context.Context, id int64, put *api.NetworkACLPut) error {
	// Fetch existing to recover project and name.
	idInt := int(id)
	acls, err := cluster.GetNetworkACLs(ctx, c.tx, cluster.NetworkACLFilter{ID: &idInt})
	if err != nil {
		return err
	}

	if len(acls) == 0 {
		return api.StatusErrorf(http.StatusNotFound, "Network ACL not found")
	}

	curr := acls[0]
	upd := cluster.NetworkACL{
		Project:     curr.Project,
		Name:        curr.Name,
		Description: put.Description,
		Ingress:     put.Ingress,
		Egress:      put.Egress,
	}

	err = cluster.UpdateNetworkACL(ctx, c.tx, curr.Project, curr.Name, upd)
	if err != nil {
		return err
	}

	err = cluster.UpdateNetworkACLConfig(ctx, c.tx, id, put.Config)
	if err != nil {
		return err
	}

	return nil
}

// RenameNetworkACL renames a Network ACL.
func (c *ClusterTx) RenameNetworkACL(ctx context.Context, id int64, newName string) error {
	idInt := int(id)
	filter := cluster.NetworkACLFilter{ID: &idInt}
	acls, err := cluster.GetNetworkACLs(ctx, c.tx, filter)
	if err != nil {
		return err
	}

	if len(acls) == 0 {
		return api.StatusErrorf(http.StatusNotFound, "Network ACL not found")
	}

	return cluster.RenameNetworkACL(ctx, c.tx, acls[0].Project, acls[0].Name, newName)
}

// DeleteNetworkACL deletes the Network ACL.
func (c *ClusterTx) DeleteNetworkACL(ctx context.Context, id int64) error {
	idInt := int(id)
	filter := cluster.NetworkACLFilter{ID: &idInt}
	acls, err := cluster.GetNetworkACLs(ctx, c.tx, filter)
	if err != nil {
		return err
	}

	if len(acls) == 0 {
		return api.StatusErrorf(http.StatusNotFound, "Network ACL not found")
	}

	return cluster.DeleteNetworkACL(ctx, c.tx, acls[0].Project, acls[0].Name)
}

// GetNetworkACLURIs returns the URIs for the network ACLs with the given project.
func (c *ClusterTx) GetNetworkACLURIs(ctx context.Context, projectID int, project string) ([]string, error) {
	filter := cluster.NetworkACLFilter{Project: &project}
	acls, err := cluster.GetNetworkACLs(ctx, c.tx, filter)
	if err != nil {
		return nil, fmt.Errorf("Unable to get URIs for network acl: %w", err)
	}

	uris := make([]string, len(acls))
	for i, acl := range acls {
		uris[i] = api.NewURL().
			Path(version.APIVersion, "network-acls", acl.Name).
			Project(project).
			String()
	}

	return uris, nil
}
