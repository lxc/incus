//go:build linux && cgo && !agent

package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/lxc/incus/v6/internal/server/db/cluster"
)

// ErrUnknownEntityID describes the unknown entity ID error.
var ErrUnknownEntityID = fmt.Errorf("Unknown entity ID")

// GetURIFromEntity returns the URI for the given entity type and entity ID.
func (c *ClusterTx) GetURIFromEntity(ctx context.Context, entityType int, entityID int) (string, error) {
	if entityID == -1 || entityType == -1 {
		return "", nil
	}

	_, ok := cluster.EntityNames[entityType]
	if !ok {
		return "", fmt.Errorf("Unknown entity type")
	}

	var uri string

	switch entityType {
	case cluster.TypeImage:
		images, err := cluster.GetImages(ctx, c.tx)
		if err != nil {
			return "", fmt.Errorf("Failed to get images: %w", err)
		}

		for _, image := range images {
			if image.ID != entityID {
				continue
			}

			uri = fmt.Sprintf(cluster.EntityURIs[entityType], image.Fingerprint, image.Project)
			break
		}

		if uri == "" {
			return "", ErrUnknownEntityID
		}

	case cluster.TypeProfile:
		profiles, err := cluster.GetProfiles(ctx, c.Tx())
		if err != nil {
			return "", fmt.Errorf("Failed to get profiles: %w", err)
		}

		for _, profile := range profiles {
			if profile.ID != entityID {
				continue
			}

			uri = fmt.Sprintf(cluster.EntityURIs[entityType], profile.Name, profile.Project)
			break
		}

		if uri == "" {
			return "", ErrUnknownEntityID
		}

	case cluster.TypeProject:
		projects, err := cluster.GetProjectIDsToNames(ctx, c.tx)
		if err != nil {
			return "", fmt.Errorf("Failed to get project names and IDs: %w", err)
		}

		name, ok := projects[int64(entityID)]
		if !ok {
			return "", ErrUnknownEntityID
		}

		uri = fmt.Sprintf(cluster.EntityURIs[entityType], name)
	case cluster.TypeCertificate:
		certificates, err := cluster.GetCertificates(ctx, c.tx)
		if err != nil {
			return "", fmt.Errorf("Failed to get certificates: %w", err)
		}

		for _, cert := range certificates {
			if cert.ID != entityID {
				continue
			}

			uri = fmt.Sprintf(cluster.EntityURIs[entityType], cert.Name)
			break
		}

		if uri == "" {
			return "", ErrUnknownEntityID
		}

	case cluster.TypeContainer:
		fallthrough
	case cluster.TypeInstance:
		instances, err := cluster.GetInstances(ctx, c.tx)
		if err != nil {
			return "", fmt.Errorf("Failed to get instances: %w", err)
		}

		for _, instance := range instances {
			if instance.ID != entityID {
				continue
			}

			uri = fmt.Sprintf(cluster.EntityURIs[entityType], instance.Name, instance.Project)
			break
		}

		if uri == "" {
			return "", ErrUnknownEntityID
		}

	case cluster.TypeInstanceBackup:
		instanceBackup, err := c.GetInstanceBackupWithID(ctx, entityID)
		if err != nil {
			return "", fmt.Errorf("Failed to get instance backup: %w", err)
		}

		instances, err := cluster.GetInstances(ctx, c.tx)
		if err != nil {
			return "", fmt.Errorf("Failed to get instances: %w", err)
		}

		for _, instance := range instances {
			if instance.ID != instanceBackup.InstanceID {
				continue
			}

			uri = fmt.Sprintf(cluster.EntityURIs[entityType], instance.Name, instanceBackup.Name, instance.Project)
			break
		}

		if uri == "" {
			return "", ErrUnknownEntityID
		}

	case cluster.TypeInstanceSnapshot:
		snapshots, err := cluster.GetInstanceSnapshots(ctx, c.Tx())
		if err != nil {
			return "", fmt.Errorf("Failed to get instance snapshots: %w", err)
		}

		for _, snapshot := range snapshots {
			if snapshot.ID != entityID {
				continue
			}

			uri = fmt.Sprintf(cluster.EntityURIs[entityType], snapshot.Name, snapshot.Project)
			break
		}

		if uri == "" {
			return "", ErrUnknownEntityID
		}

	case cluster.TypeNetwork:
		networkName, projectName, err := c.GetNetworkNameAndProjectWithID(ctx, entityID)
		if err != nil {
			return "", fmt.Errorf("Failed to get network name and project name: %w", err)
		}

		uri = fmt.Sprintf(cluster.EntityURIs[entityType], networkName, projectName)
	case cluster.TypeNetworkACL:
		networkACLName, projectName, err := c.GetNetworkACLNameAndProjectWithID(ctx, entityID)
		if err != nil {
			return "", fmt.Errorf("Failed to get network ACL name and project name: %w", err)
		}

		uri = fmt.Sprintf(cluster.EntityURIs[entityType], networkACLName, projectName)
	case cluster.TypeNode:
		nodeInfo, err := c.GetNodeWithID(ctx, entityID)
		if err != nil {
			return "", fmt.Errorf("Failed to get node information: %w", err)
		}

		uri = fmt.Sprintf(cluster.EntityURIs[entityType], nodeInfo.Name)
	case cluster.TypeOperation:
		id := int64(entityID)
		filter := cluster.OperationFilter{ID: &id}

		ops, err := cluster.GetOperations(ctx, c.tx, filter)
		if err != nil {
			return "", fmt.Errorf("Failed to get operation: %w", err)
		}

		if len(ops) > 1 {
			return "", fmt.Errorf("Failed to get operation: More than one operation matches")
		}

		op := ops[0]

		uri = fmt.Sprintf(cluster.EntityURIs[entityType], op.UUID)
	case cluster.TypeStoragePool:
		_, pool, _, err := c.GetStoragePoolWithID(ctx, entityID)
		if err != nil {
			return "", fmt.Errorf("Failed to get storage pool: %w", err)
		}

		uri = fmt.Sprintf(cluster.EntityURIs[entityType], pool.Name)
	case cluster.TypeStorageVolume:
		args, err := c.GetStoragePoolVolumeWithID(ctx, entityID)
		if err != nil {
			return "", fmt.Errorf("Failed to get storage volume: %w", err)
		}

		uri = fmt.Sprintf(cluster.EntityURIs[entityType], args.PoolName, args.TypeName, args.Name, args.ProjectName)
	case cluster.TypeStorageVolumeBackup:
		backup, err := c.GetStoragePoolVolumeBackupWithID(ctx, entityID)
		if err != nil {
			return "", fmt.Errorf("Failed to get volume backup: %w", err)
		}

		volume, err := c.GetStoragePoolVolumeWithID(ctx, int(backup.VolumeID))
		if err != nil {
			return "", fmt.Errorf("Failed to get storage volume: %w", err)
		}

		uri = fmt.Sprintf(cluster.EntityURIs[entityType], volume.PoolName, volume.TypeName, volume.Name, backup.Name, volume.ProjectName)
	case cluster.TypeStorageVolumeSnapshot:
		snapshot, err := c.GetStorageVolumeSnapshotWithID(ctx, entityID)
		if err != nil {
			return "", fmt.Errorf("Failed to get volume snapshot: %w", err)
		}

		fields := strings.Split(snapshot.Name, "/")

		uri = fmt.Sprintf(cluster.EntityURIs[entityType], snapshot.PoolName, snapshot, snapshot.TypeName, fields[0], fields[1], snapshot.ProjectName)
	}

	return uri, nil
}
