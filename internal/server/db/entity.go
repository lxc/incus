//go:build linux && cgo && !agent

package db

import (
	"context"
	"fmt"
	"strings"

	"github.com/lxc/incus/internal/server/db/cluster"
	"github.com/lxc/incus/shared/api"
)

// ErrUnknownEntityID describes the unknown entity ID error.
var ErrUnknownEntityID = fmt.Errorf("Unknown entity ID")

// GetURIFromEntity returns the URI for the given entity type and entity ID.
func (c *Cluster) GetURIFromEntity(entityType int, entityID int) (string, error) {
	if entityID == -1 || entityType == -1 {
		return "", nil
	}

	_, ok := cluster.EntityNames[entityType]
	if !ok {
		return "", fmt.Errorf("Unknown entity type")
	}

	var err error
	var uri string

	switch entityType {
	case cluster.TypeImage:
		var images []cluster.Image

		err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
			images, err = cluster.GetImages(ctx, tx.tx)
			if err != nil {
				return err
			}

			return nil
		})
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
		var profiles []cluster.Profile

		err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
			profiles, err = cluster.GetProfiles(ctx, tx.Tx())
			if err != nil {
				return err
			}

			return nil
		})
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
		projects := make(map[int64]string)

		err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
			projects, err = cluster.GetProjectIDsToNames(context.Background(), tx.tx)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return "", fmt.Errorf("Failed to get project names and IDs: %w", err)
		}

		name, ok := projects[int64(entityID)]
		if !ok {
			return "", ErrUnknownEntityID
		}

		uri = fmt.Sprintf(cluster.EntityURIs[entityType], name)
	case cluster.TypeCertificate:
		var certificates []cluster.Certificate

		err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
			certificates, err = cluster.GetCertificates(context.Background(), tx.tx)
			if err != nil {
				return err
			}

			return nil
		})
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
		var instances []cluster.Instance

		err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
			instances, err = cluster.GetInstances(ctx, tx.tx)
			if err != nil {
				return err
			}

			return nil
		})
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
		var instances []cluster.Instance
		var instanceBackup InstanceBackup

		err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
			instanceBackup, err = tx.GetInstanceBackupWithID(ctx, entityID)
			if err != nil {
				return fmt.Errorf("Failed to get instance backup: %w", err)
			}

			instances, err = cluster.GetInstances(ctx, tx.tx)
			if err != nil {
				return fmt.Errorf("Failed to get instances: %w", err)
			}

			return nil
		})
		if err != nil {
			return "", err
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
		var snapshots []cluster.InstanceSnapshot

		err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
			snapshots, err = cluster.GetInstanceSnapshots(ctx, tx.Tx())
			if err != nil {
				return err
			}

			return nil
		})
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
		var networkName string
		var projectName string

		err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
			networkName, projectName, err = tx.GetNetworkNameAndProjectWithID(ctx, entityID)

			return err
		})
		if err != nil {
			return "", fmt.Errorf("Failed to get network name and project name: %w", err)
		}

		uri = fmt.Sprintf(cluster.EntityURIs[entityType], networkName, projectName)
	case cluster.TypeNetworkACL:
		var networkACLName string
		var projectName string

		err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
			var err error

			networkACLName, projectName, err = tx.GetNetworkACLNameAndProjectWithID(ctx, entityID)

			return err
		})
		if err != nil {
			return "", fmt.Errorf("Failed to get network ACL name and project name: %w", err)
		}

		uri = fmt.Sprintf(cluster.EntityURIs[entityType], networkACLName, projectName)
	case cluster.TypeNode:
		var nodeInfo NodeInfo

		err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
			nodeInfo, err = tx.GetNodeWithID(ctx, entityID)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return "", fmt.Errorf("Failed to get node information: %w", err)
		}

		uri = fmt.Sprintf(cluster.EntityURIs[entityType], nodeInfo.Name)
	case cluster.TypeOperation:
		var op cluster.Operation

		err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
			id := int64(entityID)
			filter := cluster.OperationFilter{ID: &id}
			ops, err := cluster.GetOperations(ctx, tx.tx, filter)
			if err != nil {
				return err
			}

			if len(ops) > 1 {
				return fmt.Errorf("More than one operation matches")
			}

			op = ops[0]
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("Failed to get operation: %w", err)
		}

		uri = fmt.Sprintf(cluster.EntityURIs[entityType], op.UUID)
	case cluster.TypeStoragePool:
		var pool *api.StoragePool

		err = c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
			_, pool, _, err = tx.GetStoragePoolWithID(ctx, entityID)

			return err
		})
		if err != nil {
			return "", fmt.Errorf("Failed to get storage pool: %w", err)
		}

		uri = fmt.Sprintf(cluster.EntityURIs[entityType], pool.Name)
	case cluster.TypeStorageVolume:
		var args StorageVolumeArgs

		err := c.Transaction(c.closingCtx, func(ctx context.Context, tx *ClusterTx) error {
			args, err = tx.GetStoragePoolVolumeWithID(ctx, entityID)
			if err != nil {
				return err
			}

			return nil
		})
		if err != nil {
			return "", fmt.Errorf("Failed to get storage volume: %w", err)
		}

		uri = fmt.Sprintf(cluster.EntityURIs[entityType], args.PoolName, args.TypeName, args.Name, args.ProjectName)
	case cluster.TypeStorageVolumeBackup:
		var volume StorageVolumeArgs
		var backup StoragePoolVolumeBackup

		err = c.Transaction(c.closingCtx, func(ctx context.Context, tx *ClusterTx) error {
			backup, err := tx.GetStoragePoolVolumeBackupWithID(ctx, entityID)
			if err != nil {
				return fmt.Errorf("Failed to get volume backup: %w", err)
			}

			volume, err = tx.GetStoragePoolVolumeWithID(ctx, int(backup.VolumeID))
			if err != nil {
				return fmt.Errorf("Failed to get storage volume: %w", err)
			}

			return nil
		})
		if err != nil {
			return "", err
		}

		uri = fmt.Sprintf(cluster.EntityURIs[entityType], volume.PoolName, volume.TypeName, volume.Name, backup.Name, volume.ProjectName)
	case cluster.TypeStorageVolumeSnapshot:
		var snapshot StorageVolumeArgs

		err := c.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
			snapshot, err = tx.GetStorageVolumeSnapshotWithID(ctx, entityID)

			return err
		})
		if err != nil {
			return "", fmt.Errorf("Failed to get volume snapshot: %w", err)
		}

		fields := strings.Split(snapshot.Name, "/")

		uri = fmt.Sprintf(cluster.EntityURIs[entityType], snapshot.PoolName, snapshot, snapshot.TypeName, fields[0], fields[1], snapshot.ProjectName)
	}

	return uri, nil
}
