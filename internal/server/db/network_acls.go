//go:build linux && cgo && !agent

package db

import (
	"context"
	"fmt"
	"net/http"

	"github.com/lxc/incus/v6/internal/server/db/cluster"
	"github.com/lxc/incus/v6/shared/api"
)

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
