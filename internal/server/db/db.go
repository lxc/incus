//go:build linux && cgo && !agent

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cowsql/go-cowsql/driver"

	"github.com/lxc/incus/internal/server/db/cluster"
	"github.com/lxc/incus/internal/server/db/node"
	"github.com/lxc/incus/internal/server/db/query"
	internalUtil "github.com/lxc/incus/internal/util"
	"github.com/lxc/incus/shared/logger"
)

// DB represents access to global and local databases.
type DB struct {
	Node    *Node
	Cluster *Cluster
}

// Node mediates access to data stored in the node-local SQLite database.
type Node struct {
	db  *sql.DB // Handle to the node-local SQLite database file.
	dir string  // Reference to the directory where the database file lives.
}

// OpenNode creates a new Node object.
//
// The fresh hook parameter is used by the daemon to mark all known patch names
// as applied when a brand new database is created.
//
// Return the newly created Node object.
func OpenNode(dir string, fresh func(*Node) error) (*Node, error) {
	db, err := node.Open(dir)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	initial, err := node.EnsureSchema(db, dir)
	if err != nil {
		return nil, err
	}

	node := &Node{
		db:  db,
		dir: dir,
	}

	if initial == 0 {
		if fresh != nil {
			err := fresh(node)
			if err != nil {
				return nil, err
			}
		}
	}

	return node, nil
}

// DirectAccess is a bit of a hack which allows getting a database Node struct from any standard Go sql.DB.
// This is primarily used to access the "db.bin" read-only copy of the database during startup.
func DirectAccess(db *sql.DB) *Node {
	return &Node{db: db}
}

// DB returns the low level database handle to the node-local SQLite
// database.
//
//	FIXME: this is used for compatibility with some legacy code, and should be
//		dropped once there are no call sites left.
func (n *Node) DB() *sql.DB {
	return n.db
}

// Dir returns the directory of the underlying SQLite database file.
func (n *Node) Dir() string {
	return n.dir
}

// Transaction creates a new NodeTx object and transactionally executes the
// node-level database interactions invoked by the given function. If the
// function returns no error, all database changes are committed to the
// node-level database, otherwise they are rolled back.
func (n *Node) Transaction(ctx context.Context, f func(context.Context, *NodeTx) error) error {
	nodeTx := &NodeTx{}
	return query.Transaction(ctx, n.db, func(ctx context.Context, tx *sql.Tx) error {
		nodeTx.tx = tx
		return f(ctx, nodeTx)
	})
}

// Close the database facade.
func (n *Node) Close() error {
	return n.db.Close()
}

// Cluster mediates access to data stored in the cluster dqlite database.
type Cluster struct {
	db         *sql.DB // Handle to the cluster dqlite database, gated behind gRPC SQL.
	nodeID     int64   // Node ID of this server.
	mu         sync.RWMutex
	closingCtx context.Context
}

// OpenCluster creates a new Cluster object for interacting with the dqlite
// database.
//
// - name: Basename of the database file holding the data. Typically "db.bin".
// - dialer: Function used to connect to the dqlite backend via gRPC SQL.
// - address: Network address of this node (or empty string).
// - dir: Base database directory (e.g. /var/lib/incus/database)
// - timeout: Give up trying to open the database after this amount of time.
//
// The address and api parameters will be used to determine if the cluster
// database matches our version, and possibly trigger a schema update. If the
// schema update can't be performed right now, because some nodes are still
// behind, an Upgrading error is returned.
// Accepts a closingCtx context argument used to indicate when the daemon is shutting down.
func OpenCluster(closingCtx context.Context, name string, store driver.NodeStore, address, dir string, timeout time.Duration, options ...driver.Option) (*Cluster, error) {
	db, err := cluster.Open(name, store, options...)
	if err != nil {
		return nil, fmt.Errorf("Failed to open database: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	// Test that the cluster database is operational. We wait up to the
	// given timeout , in case there's no quorum of nodes online yet.
	connectCtx, connectCancel := context.WithTimeout(closingCtx, timeout)
	defer connectCancel()
	for i := 0; ; i++ {
		// Log initial attempts at debug level, but use warn
		// level after the 5'th attempt (about 10 seconds).
		// After the 15'th attempt (about 30 seconds), log
		// only one attempt every 5.
		logPriority := 1 // 0 is discard, 1 is Debug, 2 is Error
		if i > 5 {
			logPriority = 2
			if i > 15 && !((i % 5) == 0) {
				logPriority = 0
			}
		}

		logger.Info("Connecting to global database")
		pingCtx, pingCancel := context.WithTimeout(connectCtx, time.Second*5)
		err = db.PingContext(pingCtx)
		pingCancel()
		logCtx := logger.Ctx{"err": err, "attempt": i}
		if err != nil && !errors.Is(err, driver.ErrNoAvailableLeader) {
			return nil, err
		} else if err == nil {
			logger.Info("Connected to global database")
			break
		}

		switch logPriority {
		case 1:
			logger.Debug("Failed connecting to global database", logCtx)
		case 2:
			logger.Error("Failed connecting to global database", logCtx)
		}

		select {
		case <-connectCtx.Done():
			return nil, connectCtx.Err()
		default:
			time.Sleep(2 * time.Second)
		}
	}

	// FIXME: https://github.com/canonical/dqlite/issues/163
	_, err = db.Exec("PRAGMA cache_size=-50000")
	if err != nil {
		return nil, fmt.Errorf("Failed to set page cache size: %w", err)
	}

	nodesVersionsMatch, err := cluster.EnsureSchema(db, address, dir)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure schema: %w", err)
	}

	if !nodesVersionsMatch {
		cluster := &Cluster{
			db:         db,
			closingCtx: closingCtx,
		}

		return cluster, ErrSomeNodesAreBehind
	}

	stmts, err := cluster.PrepareStmts(db, false)
	if err != nil {
		return nil, fmt.Errorf("Failed to prepare statements: %w", err)
	}

	cluster.PreparedStmts = stmts

	clusterDB := &Cluster{
		db:         db,
		closingCtx: closingCtx,
	}

	err = clusterDB.Transaction(context.TODO(), func(ctx context.Context, tx *ClusterTx) error {
		// Figure out the ID of this node.
		members, err := tx.GetNodes(ctx)
		if err != nil {
			return fmt.Errorf("Failed getting cluster members: %w", err)
		}

		memberID := int64(-1)
		if len(members) == 1 && members[0].Address == "0.0.0.0" {
			// We're not clustered
			memberID = 1
		} else {
			for _, member := range members {
				if member.Address == address {
					memberID = member.ID
					break
				}
			}
		}

		if memberID < 0 {
			return fmt.Errorf("No node registered with address %s", address)
		}

		// Set the local member ID
		clusterDB.NodeID(memberID)

		// Delete any operation tied to this member
		err = cluster.DeleteOperations(ctx, tx.tx, memberID)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return clusterDB, err
}

// ErrSomeNodesAreBehind is returned by OpenCluster if some of the nodes in the
// cluster have a schema or API version that is less recent than this node.
var ErrSomeNodesAreBehind = fmt.Errorf("some nodes are behind this node's version")

// ForLocalInspection is a aid for the hack in initializeDbObject, which
// sets the db-related Deamon attributes upfront, to be backward compatible
// with the legacy patches that need to interact with the database.
func ForLocalInspection(db *sql.DB) *Cluster {
	return &Cluster{
		db:         db,
		closingCtx: context.Background(),
	}
}

// ForLocalInspectionWithPreparedStmts is the same as ForLocalInspection but it
// also prepares the statements used in auto-generated database code.
func ForLocalInspectionWithPreparedStmts(db *sql.DB) (*Cluster, error) {
	c := ForLocalInspection(db)

	stmts, err := cluster.PrepareStmts(c.db, true)
	if err != nil {
		return nil, fmt.Errorf("Prepare database statements: %w", err)
	}

	cluster.PreparedStmts = stmts

	return c, nil
}

// GetNodeID returns the current nodeID (0 if not set).
func (c *Cluster) GetNodeID() int64 {
	return c.nodeID
}

// Transaction creates a new ClusterTx object and transactionally executes the
// cluster database interactions invoked by the given function. If the function
// returns no error, all database changes are committed to the cluster database
// database, otherwise they are rolled back.
//
// If EnterExclusive has been called before, calling Transaction will block
// until ExitExclusive has been called as well to release the lock.
func (c *Cluster) Transaction(ctx context.Context, f func(context.Context, *ClusterTx) error) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.transaction(ctx, f)
}

// EnterExclusive acquires a lock on the cluster db, so any successive call to
// Transaction will block until ExitExclusive has been called.
func (c *Cluster) EnterExclusive() error {
	logger.Debug("Acquiring exclusive lock on cluster db")

	ch := make(chan struct{})
	go func() {
		c.mu.Lock()
		ch <- struct{}{}
	}()

	timeout := 20 * time.Second
	select {
	case <-ch:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("timeout (%s)", timeout)
	}
}

// ExitExclusive runs the given transaction and then releases the lock acquired
// with EnterExclusive.
func (c *Cluster) ExitExclusive(ctx context.Context, f func(context.Context, *ClusterTx) error) error {
	logger.Debug("Releasing exclusive lock on cluster db")
	defer c.mu.Unlock()
	return c.transaction(ctx, f)
}

func (c *Cluster) transaction(ctx context.Context, f func(context.Context, *ClusterTx) error) error {
	clusterTx := &ClusterTx{
		nodeID: c.nodeID,
	}

	return query.Retry(ctx, func(ctx context.Context) error {
		txFunc := func(ctx context.Context, tx *sql.Tx) error {
			clusterTx.tx = tx
			return f(ctx, clusterTx)
		}

		err := query.Transaction(ctx, c.db, txFunc)
		if errors.Is(err, context.DeadlineExceeded) {
			// If the query timed out it likely means that the leader has abruptly become unreachable.
			// Now that this query has been cancelled, a leader election should have taken place by now.
			// So let's retry the transaction once more in case the global database is now available again.
			logger.Warn("Transaction timed out. Retrying once", logger.Ctx{"member": c.nodeID, "err": err})
			return query.Transaction(ctx, c.db, txFunc)
		}

		return err
	})
}

// NodeID sets the node NodeID associated with this cluster instance. It's used for
// backward-compatibility of all db-related APIs that were written before
// clustering and don't accept a node NodeID, so in those cases we automatically
// use this value as implicit node NodeID.
func (c *Cluster) NodeID(id int64) {
	c.nodeID = id
}

// Close the database facade.
func (c *Cluster) Close() error {
	for _, stmt := range cluster.PreparedStmts {
		_ = stmt.Close()
	}

	return c.db.Close()
}

// DB returns the low level database handle to the cluster database.
//
//	FIXME: this is used for compatibility with some legacy code, and should be
//		dropped once there are no call sites left.
func (c *Cluster) DB() *sql.DB {
	return c.db
}

// Begin a new transaction against the cluster database.
//
// FIXME: legacy method.
func (c *Cluster) Begin() (*sql.Tx, error) {
	return begin(c.db)
}

func begin(db *sql.DB) (*sql.Tx, error) {
	for i := 0; i < 1000; i++ {
		tx, err := db.Begin()
		if err == nil {
			return tx, nil
		}

		if !query.IsRetriableError(err) {
			logger.Debugf("DbBegin: error %q", err)
			return nil, err
		}

		time.Sleep(30 * time.Millisecond)
	}

	logger.Debugf("DbBegin: DB still locked")
	logger.Debugf(logger.GetStack())
	return nil, fmt.Errorf("DB is locked")
}

// TxCommit commits the given transaction.
func TxCommit(tx *sql.Tx) error {
	err := tx.Commit()
	if err == nil || err == sql.ErrTxDone { // Ignore duplicate commits/rollbacks
		return nil
	}

	return err
}

// DqliteLatestSegment returns the latest segment ID in the global database.
func DqliteLatestSegment() (string, error) {
	dir := internalUtil.VarPath("database", "global")
	file, err := os.Open(dir)
	if err != nil {
		return "", fmt.Errorf("Unable to open directory %s with error %v", dir, err)
	}

	defer func() { _ = file.Close() }()

	fileNames, err := file.Readdirnames(0)
	if err != nil {
		return "", fmt.Errorf("Unable to read file names in directory %s with error %v", dir, err)
	}

	if len(fileNames) == 0 {
		return "none", nil
	}

	sort.Strings(fileNames)

	r, err := regexp.Compile(`^[0-9]+-[0-9]+$`)
	if err != nil {
		return "none", err
	}

	for i := range fileNames {
		fileName := fileNames[len(fileNames)-1-i]
		if r.MatchString(fileName) {
			segment := strings.Split(fileName, "-")[1]
			// Trim leading o's.
			index := 0
			for i, c := range segment {
				index = i
				if c != '0' {
					break
				}
			}

			return segment[index:], nil
		}
	}

	return "none", nil
}

func dbQueryRowScan(ctx context.Context, c *ClusterTx, q string, args []any, outargs []any) error {
	return c.tx.QueryRowContext(ctx, q, args...).Scan(outargs...)
}

/*
 * . db a reference to a sql.DB instance
 * . q is the database query
 * . inargs is an array of interfaces containing the query arguments
 * . outfmt is an array of interfaces containing the right types of output
 *   arguments, i.e.
 *      var arg1 string
 *      var arg2 int
 *      outfmt := {}any{arg1, arg2}
 *
 * The result will be an array (one per output row) of arrays (one per output argument)
 * of interfaces, containing pointers to the actual output arguments.
 */
func queryScan(ctx context.Context, c *ClusterTx, q string, inargs []any, outfmt []any) ([][]any, error) {
	result := [][]any{}

	rows, err := c.tx.QueryContext(ctx, q, inargs...)
	if err != nil {
		return [][]any{}, err
	}

	defer func() { _ = rows.Close() }()

	for rows.Next() {
		ptrargs := make([]any, len(outfmt))
		for i := range outfmt {
			switch t := outfmt[i].(type) {
			case string:
				str := ""
				ptrargs[i] = &str
			case int:
				integer := 0
				ptrargs[i] = &integer
			case int64:
				integer := int64(0)
				ptrargs[i] = &integer
			case bool:
				boolean := bool(false)
				ptrargs[i] = &boolean
			default:
				return [][]any{}, fmt.Errorf("Bad interface type: %s", t)
			}
		}
		err = rows.Scan(ptrargs...)
		if err != nil {
			return [][]any{}, err
		}

		newargs := make([]any, len(outfmt))
		for i := range ptrargs {
			switch t := outfmt[i].(type) {
			case string:
				newargs[i] = *ptrargs[i].(*string)
			case int:
				newargs[i] = *ptrargs[i].(*int)
			case int64:
				newargs[i] = *ptrargs[i].(*int64)
			case bool:
				newargs[i] = *ptrargs[i].(*bool)
			default:
				return [][]any{}, fmt.Errorf("Bad interface type: %s", t)
			}
		}
		result = append(result, newargs)
	}

	err = rows.Err()
	if err != nil {
		return [][]any{}, err
	}

	return result, nil
}
