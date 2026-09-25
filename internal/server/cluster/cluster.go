package cluster

import (
	"context"
	"crypto/x509"
	"database/sql"
	"fmt"
	"slices"
	"time"

	cowsqldb "github.com/cowsql/go-cowsql/cluster/db"
	"github.com/cowsql/go-cowsql/cluster/db/transaction"

	"github.com/lxc/incus/v7/internal/server/certificate"
	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/db/cluster"
	"github.com/lxc/incus/v7/internal/server/db/query"
	"github.com/lxc/incus/v7/internal/server/db/warningtype"
)

type clusterWrapper struct {
	internalDB *db.Cluster
	db         transaction.DBTX
}

// EnterExclusive implements [db.Cluster].
func (c *clusterWrapper) EnterExclusive() error {
	return c.internalDB.EnterExclusive()
}

// OnTxStart implements [transacton.Transactor].
func (c *clusterWrapper) OnTxStart(exclusive bool, f func(ctx context.Context) error) (func(context.Context) error, func()) {
	var cancel context.CancelFunc
	cleanup := func() {
		defer func() {
			if cancel != nil {
				cancel()
			}
		}()

		c.internalDB.ReleaseTx(exclusive)
	}

	c.internalDB.StartTx(exclusive)
	return func(ctx context.Context) error {
		var timeoutCtx context.Context
		timeoutCtx, cancel = context.WithTimeout(ctx, time.Second*30)

		return f(timeoutCtx)
	}, cleanup
}

// OnTxStartForce implements [transacton.Transactor].
func (c *clusterWrapper) OnTxStartForce(exclusive bool, f func(ctx context.Context, tx transaction.TX) error) (func(context.Context, transaction.TX) error, func()) {
	var cancel context.CancelFunc
	cleanup := func() {
		defer func() {
			if cancel != nil {
				cancel()
			}
		}()

		c.internalDB.ReleaseTx(exclusive)
	}

	c.internalDB.StartTx(exclusive)
	return func(ctx context.Context, tx transaction.TX) error {
		var timeoutCtx context.Context
		timeoutCtx, cancel = context.WithTimeout(ctx, time.Second*30)

		return f(timeoutCtx, tx)
	}, cleanup
}

// DBTX implements [transacton.Transactor].
func (c *clusterWrapper) DBTX() transaction.DBTX {
	return c.db
}

// MaxRetries implements [transacton.Transactor].
func (c *clusterWrapper) MaxRetries() int {
	return query.MaxRetries
}

// DB implements [db.Cluster].
func (c *clusterWrapper) DB() *sql.DB {
	return c.internalDB.DB
}

// SetNodeHeartbeat implements [db.Cluster].
func (c *clusterWrapper) SetNodeHeartbeat(ctx context.Context, address string, heartbeatTime time.Time) error {
	return c.tx(ctx, func(ctx context.Context, ct *db.ClusterTx) error {
		return ct.SetNodeHeartbeat(address, heartbeatTime)
	})
}

// GetNodes implements [db.Cluster].
func (c *clusterWrapper) GetNodes(ctx context.Context) ([]cowsqldb.NodeInfo, error) {
	var info []cowsqldb.NodeInfo
	err := c.tx(ctx, func(ctx context.Context, ct *db.ClusterTx) error {
		var err error
		info, err = ct.GetNodes(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}

	return info, nil
}

// GetNodesCount implements [db.Cluster].
func (c *clusterWrapper) GetNodesCount(ctx context.Context) (int, error) {
	var count int
	err := c.tx(ctx, func(ctx context.Context, ct *db.ClusterTx) error {
		var err error
		count, err = ct.GetNodesCount(ctx)
		return err
	})
	if err != nil {
		return -1, err
	}

	return count, nil
}

// RemoveNode implements [db.Cluster].
func (c *clusterWrapper) RemoveNode(ctx context.Context, name string) error {
	return c.tx(ctx, func(ctx context.Context, ct *db.ClusterTx) error {
		node, err := ct.GetNodeByName(ctx, name)
		if err != nil {
			return err
		}

		err = ct.RemoveNode(node.ID)
		if err != nil {
			return err
		}

		return cluster.DeleteCertificates(ctx, ct.Tx(), name, certificate.TypeServer)
	})
}

// GetNodesFailureDomains implements [db.Cluster].
func (c *clusterWrapper) GetNodesFailureDomains(ctx context.Context) (map[string]uint64, error) {
	var fds map[string]uint64
	err := c.tx(ctx, func(ctx context.Context, ct *db.ClusterTx) error {
		var err error
		fds, err = ct.GetNodesFailureDomains(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}

	return fds, nil
}

// GetNodeOfflineThreshold implements [db.Cluster].
func (c *clusterWrapper) GetNodeOfflineThreshold(ctx context.Context) (time.Duration, error) {
	var threshold time.Duration
	err := c.tx(ctx, func(ctx context.Context, ct *db.ClusterTx) error {
		var err error
		threshold, err = ct.GetNodeOfflineThreshold(ctx)
		return err
	})
	if err != nil {
		return -1, err
	}

	return threshold, nil
}

// NodeIsOutdated implements [db.Cluster].
func (c *clusterWrapper) NodeIsOutdated(ctx context.Context) (bool, error) {
	var outdated bool
	err := c.tx(ctx, func(ctx context.Context, ct *db.ClusterTx) error {
		var err error
		outdated, err = ct.NodeIsOutdated(ctx)
		return err
	})
	if err != nil {
		return false, err
	}

	return outdated, nil
}

// GetNodeByAddress implements [db.Cluster].
func (c *clusterWrapper) GetNodeByAddress(ctx context.Context, address string, pending bool) (cowsqldb.NodeInfo, error) {
	var info cowsqldb.NodeInfo
	err := c.tx(ctx, func(ctx context.Context, ct *db.ClusterTx) error {
		var err error
		if pending {
			info, err = ct.GetPendingNodeByAddress(ctx, address)
		} else {
			info, err = ct.GetNodeByAddress(ctx, address)
		}

		return err
	})
	if err != nil {
		return cowsqldb.NodeInfo{}, err
	}

	return info, nil
}

// GetNodeByName implements [db.Cluster].
func (c *clusterWrapper) GetNodeByName(ctx context.Context, name string, pending bool) (cowsqldb.NodeInfo, error) {
	var info cowsqldb.NodeInfo
	err := c.tx(ctx, func(ctx context.Context, ct *db.ClusterTx) error {
		var err error
		if pending {
			info, err = ct.GetPendingNodeByName(ctx, name)
		} else {
			info, err = ct.GetNodeByName(ctx, name)
		}

		return err
	})
	if err != nil {
		return cowsqldb.NodeInfo{}, err
	}

	return info, nil
}

// BootstrapNode implements [db.Cluster].
func (c *clusterWrapper) BootstrapNode(ctx context.Context, serverName string, clusterAddress string) error {
	return c.tx(ctx, func(ctx context.Context, ct *db.ClusterTx) error {
		return ct.BootstrapNode(serverName, clusterAddress)
	})
}

// CreateNode implements [db.Cluster].
func (c *clusterWrapper) CreateNode(ctx context.Context, name string, address string, arch int) (int64, error) {
	var id int64
	err := c.tx(ctx, func(ctx context.Context, ct *db.ClusterTx) error {
		var err error
		id, err = ct.CreateNodeWithArch(name, address, arch)
		return err
	})
	if err != nil {
		return -1, err
	}

	return id, nil
}

// SetNodePendingFlag implements [db.Cluster].
func (c *clusterWrapper) SetNodePendingFlag(ctx context.Context, nodeID int64, flag bool) error {
	return c.tx(ctx, func(ctx context.Context, ct *db.ClusterTx) error {
		return ct.SetNodePendingFlag(nodeID, flag)
	})
}

// SetNodeCertificateByName implements [db.Cluster].
func (c *clusterWrapper) SetNodeCertificateByName(ctx context.Context, serverName string, serverCert *x509.Certificate) error {
	return c.tx(ctx, func(ctx context.Context, ct *db.ClusterTx) error {
		return EnsureServerCertificateTrusted(serverName, serverCert, ct)
	})
}

// ClearNode implements [db.ClusterExternal].
func (c *clusterWrapper) ClearNode(ctx context.Context, nodeID int64) error {
	return c.tx(ctx, func(ctx context.Context, ct *db.ClusterTx) error {
		return ct.ClearNode(ctx, nodeID)
	})
}

// NodeIsEmpty implements [db.ClusterExternal].
func (c *clusterWrapper) NodeIsEmpty(ctx context.Context, nodeID int64) (string, error) {
	var msg string
	err := c.tx(ctx, func(ctx context.Context, ct *db.ClusterTx) error {
		var err error
		msg, err = ct.NodeIsEmpty(ctx, nodeID)
		return err
	})
	if err != nil {
		return "", err
	}

	return msg, nil
}

// GetLocalResources implements [db.ClusterExternal].
func (c *clusterWrapper) GetLocalResources(ctx context.Context) (ClusterResources, error) {
	var r ClusterResources

	err := c.tx(ctx, func(ctx context.Context, tx *db.ClusterTx) error {
		var err error
		r.Pools, err = tx.GetStoragePoolsLocalConfig(ctx)
		if err != nil {
			return err
		}

		r.Networks, err = tx.GetNetworksLocalConfig(ctx)
		if err != nil {
			return err
		}

		nodeID := tx.GetNodeID()
		filter := cluster.OperationFilter{NodeID: &nodeID}
		r.Operations, err = cluster.GetOperations(ctx, tx.Tx(), filter)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return ClusterResources{}, err
	}

	return r, nil
}

// UpdateClusterResources implements [db.ClusterExernal].
func (c *clusterWrapper) UpdateClusterResources(ctx context.Context, node cowsqldb.NodeInfo, t ClusterResources) error {
	r := ClusterResources(t)
	return c.tx(ctx, func(ctx context.Context, ct *db.ClusterTx) error {
		c.internalDB.NodeID(node.ID)
		ct.NodeID(node.ID)

		// Storage pools.
		ids, err := ct.GetNonPendingStoragePoolsNamesToIDs(ctx)
		if err != nil {
			return fmt.Errorf("Failed to get cluster storage pool IDs: %w", err)
		}

		for name, id := range ids {
			err := ct.UpdateStoragePoolAfterNodeJoin(id, node.ID)
			if err != nil {
				return fmt.Errorf("Failed to add joining node's to the pool: %w", err)
			}

			driver, err := ct.GetStoragePoolDriver(ctx, id)
			if err != nil {
				return fmt.Errorf("Failed to get storage pool driver: %w", err)
			}

			// For all pools we add the config provided by the joining node.
			config, ok := r.Pools[name]
			if !ok {
				return fmt.Errorf("Joining member has no config for pool %s", name)
			}

			err = ct.CreateStoragePoolConfig(id, node.ID, config)
			if err != nil {
				return fmt.Errorf("Failed to add joining node's pool config: %w", err)
			}

			if slices.Contains(db.StorageRemoteDriverNames(), driver) {
				// For remote pools we have to create volume entries for the joining node.
				err := ct.UpdateRemoteStoragePoolAfterNodeJoin(ctx, id, node.ID)
				if err != nil {
					return fmt.Errorf("Failed to create remote volumes for joining node: %w", err)
				}
			}
		}

		// Networks.
		netids, err := ct.GetNonPendingNetworkIDs(ctx)
		if err != nil {
			return fmt.Errorf("Failed to get cluster network IDs: %w", err)
		}

		for _, network := range netids {
			for name, id := range network {
				config, ok := r.Networks[name]
				if !ok {
					// Not all networks are present as virtual networks (OVN) don't need entries.
					continue
				}

				err := ct.NetworkNodeJoin(id, node.ID)
				if err != nil {
					return fmt.Errorf("Failed to add joining node's to the network: %w", err)
				}

				err = ct.CreateNetworkConfig(id, node.ID, config)
				if err != nil {
					return fmt.Errorf("Failed to add joining node's network config: %w", err)
				}
			}
		}

		// Migrate outstanding operations.
		for _, operation := range r.Operations {
			op := cluster.Operation{
				UUID:   operation.UUID,
				Type:   operation.Type,
				NodeID: ct.GetNodeID(),
			}

			_, err := cluster.CreateOrReplaceOperation(ctx, ct.Tx(), op)
			if err != nil {
				return fmt.Errorf("Failed to migrate operation %s: %w", operation.UUID, err)
			}
		}

		return nil
	})
}

// EmitOfflineMemberWarning implements [db.ClusterWarningHandler].
func (c *clusterWrapper) EmitOfflineMemberWarning(ctx context.Context, nodeID int64, nodeName string, msg string) error {
	return c.tx(ctx, func(ctx context.Context, ct *db.ClusterTx) error {
		return ct.UpsertWarning(ctx, nodeName, "", cluster.TypeNode, int(nodeID), warningtype.OfflineClusterMember, msg)
	})
}

// ResolveOfflineMemberWarning implements [db.ClusterWarningHandler].
func (c *clusterWrapper) ResolveOfflineMemberWarning(ctx context.Context, nodeID int64, nodeName string) error {
	return c.tx(ctx, func(ctx context.Context, ct *db.ClusterTx) error {
		return ct.ResolveWarningsByNodeAndProjectAndTypeAndEntity(ctx, nodeName, "", warningtype.OfflineClusterMember, cluster.TypeNode, int(nodeID))
	})
}

// EmitTimeSkewWarning implements [db.ClusterWarningHandler].
func (c *clusterWrapper) EmitTimeSkewWarning(ctx context.Context, serverName string, msg string) error {
	return c.tx(ctx, func(ctx context.Context, ct *db.ClusterTx) error {
		return ct.UpsertWarning(ctx, serverName, "", -1, -1, warningtype.ClusterTimeSkew, msg)
	})
}

// ResolveTimeSkewWarning implements [db.ClusterWarningHandler].
func (c *clusterWrapper) ResolveTimeSkewWarning(ctx context.Context, serverName string) error {
	return c.tx(ctx, func(ctx context.Context, ct *db.ClusterTx) error {
		return ct.ResolveWarningsByNodeAndType(ctx, serverName, warningtype.ClusterTimeSkew)
	})
}

func (c *clusterWrapper) tx(ctx context.Context, f func(context.Context, *db.ClusterTx) error) error {
	return transaction.ForceTx(ctx, c, func(ctx context.Context, tx transaction.TX) error {
		ct, ok := tx.(*db.ClusterTx)
		if !ok {
			return fmt.Errorf("Unexpected cluster transaction container %T", tx)
		}

		return f(ctx, ct)
	})
}

func cowsqlCluster(database *db.Cluster) cowsqldb.Cluster {
	return &clusterWrapper{
		internalDB: database,
		db:         transaction.Enable(database),
	}
}

var _ interface {
	cowsqldb.Cluster
	cowsqldb.ClusterWarningHandler
	cowsqldb.ClusterExternal[ClusterResources]
} = &clusterWrapper{}
