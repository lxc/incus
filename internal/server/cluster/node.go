package cluster

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	cowsqldb "github.com/cowsql/go-cowsql/cluster/db"
	"github.com/cowsql/go-cowsql/cluster/db/transaction"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/node"
)

type nodeWrapper struct {
	internalDB *db.Node
	db         transaction.DBTX
}

// EnterExclusive implements [db.Node].
func (n *nodeWrapper) EnterExclusive() error {
	return nil
}

// OnTxStart implements [db.Node].
func (n *nodeWrapper) OnTxStart(_ bool, f func(context.Context) error) (func(context.Context) error, func()) {
	var cancel context.CancelFunc
	cleanup := func() {
		if cancel != nil {
			cancel()
		}
	}

	return func(ctx context.Context) error {
		var timeoutCtx context.Context
		timeoutCtx, cancel = context.WithTimeout(ctx, time.Second*30)

		return f(timeoutCtx)
	}, cleanup
}

// OnTxStartForce implements [db.Node].
func (n *nodeWrapper) OnTxStartForce(_ bool, f func(context.Context, transaction.TX) error) (func(context.Context, transaction.TX) error, func()) {
	var cancel context.CancelFunc
	cleanup := func() {
		if cancel != nil {
			cancel()
		}
	}

	return func(ctx context.Context, tx transaction.TX) error {
		var timeoutCtx context.Context
		timeoutCtx, cancel = context.WithTimeout(ctx, time.Second*30)

		return f(timeoutCtx, tx)
	}, cleanup
}

// MaxRetries implements [db.Node].
func (n *nodeWrapper) MaxRetries() int {
	return 0
}

// DBTX implements [db.Node].
func (n *nodeWrapper) DBTX() transaction.DBTX {
	return n.db
}

// DB implements [db.Node].
func (n *nodeWrapper) DB() *sql.DB {
	return n.internalDB.DB
}

// LogPath implements [db.Node].
func (n *nodeWrapper) LogPath() string {
	return filepath.Join(n.GlobalDatabaseDir(), "logs.db")
}

// GlobalDatabaseDir implements [db.Node].
func (n *nodeWrapper) GlobalDatabaseDir() string {
	return filepath.Join(n.internalDB.Dir(), "global")
}

// GetRaftNodes implements [db.Node].
func (n *nodeWrapper) GetRaftNodes(ctx context.Context) ([]cowsqldb.RaftNode, error) {
	var raftNodes []cowsqldb.RaftNode

	err := n.tx(ctx, func(ctx context.Context, nt *db.NodeTx) error {
		var err error
		raftNodes, err = nt.GetRaftNodes(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}

	return raftNodes, nil
}

// GetRaftNode implements [db.Node].
func (n *nodeWrapper) GetRaftNode(ctx context.Context, id int64) (*cowsqldb.RaftNode, bool, error) {
	var raftNode *cowsqldb.RaftNode
	err := n.tx(ctx, func(ctx context.Context, nt *db.NodeTx) error {
		var err error
		raftNodes, err := nt.GetRaftNodes(ctx)
		if err != nil {
			return err
		}

		for _, node := range raftNodes {
			if int64(node.ID) == id {
				raftNode = &node
				return nil
			}
		}

		return nil
	})
	if err != nil {
		return nil, false, err
	}

	return raftNode, raftNode != nil, nil
}

// CreateFirstRaftNode implements [db.Node].
func (n *nodeWrapper) CreateFirstRaftNode(ctx context.Context, address string, name string) error {
	return n.tx(ctx, func(ctx context.Context, nt *db.NodeTx) error {
		return nt.CreateFirstRaftNode(address, name)
	})
}

// ReplaceRaftNodes implements [db.Node].
func (n *nodeWrapper) ReplaceRaftNodes(ctx context.Context, nodes []cowsqldb.RaftNode) error {
	return n.tx(ctx, func(ctx context.Context, nt *db.NodeTx) error {
		return nt.ReplaceRaftNodes(nodes)
	})
}

// DetermineRaftNode implements [db.Node].
func (n *nodeWrapper) DetermineRaftNode(ctx context.Context) (*cowsqldb.RaftNode, error) {
	var raftNode *cowsqldb.RaftNode

	err := n.tx(ctx, func(ctx context.Context, nt *db.NodeTx) error {
		var err error
		raftNode, err = node.DetermineRaftNode(ctx, nt)
		return err
	})
	if err != nil {
		return nil, err
	}

	return raftNode, nil
}

// GetClusterAddress implements [db.Node].
func (n *nodeWrapper) GetClusterAddress(ctx context.Context) (string, error) {
	var addr string
	err := n.tx(ctx, func(ctx context.Context, nt *db.NodeTx) error {
		// Fetch current network address and raft nodes
		config, err := node.ConfigLoad(ctx, nt)
		if err != nil {
			return fmt.Errorf("Failed to fetch node configuration: %w", err)
		}

		addr = config.ClusterAddress()
		return nil
	})
	if err != nil {
		return "", err
	}

	return addr, nil
}

// GetRaftNodeAddresses implements [db.Node].
func (n *nodeWrapper) GetRaftNodeAddresses(ctx context.Context) ([]string, error) {
	var addrs []string
	err := n.tx(ctx, func(ctx context.Context, nt *db.NodeTx) error {
		var err error
		addrs, err = nt.GetRaftNodeAddresses(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}

	return addrs, nil
}

// SetClusterAddress implements [db.Node].
func (n *nodeWrapper) SetClusterAddress(ctx context.Context, address string) error {
	return n.tx(ctx, func(ctx context.Context, nt *db.NodeTx) error {
		config, err := node.ConfigLoad(ctx, nt)
		if err != nil {
			return err
		}

		newConfig := map[string]string{"cluster.https_address": address}
		_, err = config.Patch(newConfig)

		return err
	})
}

// tx is a helper that forcibly opens a transaction if one is not open, and extracts the underlying NodeTx.
func (n *nodeWrapper) tx(ctx context.Context, f func(context.Context, *db.NodeTx) error) error {
	return transaction.ForceTx(ctx, n, func(ctx context.Context, tx transaction.TX) error {
		ct, ok := tx.(*db.NodeTx)
		if !ok {
			return fmt.Errorf("Unexpected cluster transaction container %T", tx)
		}

		return f(ctx, ct)
	})
}

func cowsqlNode(database *db.Node) cowsqldb.Node {
	return &nodeWrapper{
		db:         transaction.Enable(database),
		internalDB: database,
	}
}
