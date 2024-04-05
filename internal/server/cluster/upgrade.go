package cluster

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"time"

	"github.com/cowsql/go-cowsql/client"

	"github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/internal/server/db"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/subprocess"
	localtls "github.com/lxc/incus/v6/shared/tls"
)

// NotifyUpgradeCompleted sends a notification to all other nodes in the
// cluster that any possible pending database update has been applied, and any
// nodes which was waiting for this node to be upgraded should re-check if it's
// okay to move forward.
func NotifyUpgradeCompleted(state *state.State, networkCert *localtls.CertInfo, serverCert *localtls.CertInfo) error {
	notifier, err := NewNotifier(state, networkCert, serverCert, NotifyTryAll)
	if err != nil {
		return err
	}

	return notifier(func(client incus.InstanceServer) error {
		info, err := client.GetConnectionInfo()
		if err != nil {
			return fmt.Errorf("failed to get connection info: %w", err)
		}

		url := fmt.Sprintf("%s%s", info.Addresses[0], databaseEndpoint)
		request, err := http.NewRequest("PATCH", url, nil)
		if err != nil {
			return fmt.Errorf("failed to create database notify upgrade request: %w", err)
		}

		setDqliteVersionHeader(request)

		httpClient, err := client.GetHTTPClient()
		if err != nil {
			return fmt.Errorf("failed to get HTTP client: %w", err)
		}

		httpClient.Timeout = 5 * time.Second
		response, err := httpClient.Do(request)
		if err != nil {
			return fmt.Errorf("failed to notify node about completed upgrade: %w", err)
		}

		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("database upgrade notification failed: %s", response.Status)
		}

		return nil
	})
}

// MaybeUpdate Check this node's version and possibly run INCUS_CLUSTER_UPDATE.
func MaybeUpdate(state *state.State) error {
	shouldUpdate := false

	enabled, err := Enabled(state.DB.Node)
	if err != nil {
		return fmt.Errorf("Failed to check clustering is enabled: %w", err)
	}

	if !enabled {
		return nil
	}

	if state.DB.Cluster == nil {
		return fmt.Errorf("Failed checking cluster update, state not initialized yet")
	}

	err = state.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		outdated, err := tx.NodeIsOutdated(ctx)
		if err != nil {
			return err
		}

		shouldUpdate = outdated
		return nil
	})

	if err != nil {
		// Just log the error and return.
		return fmt.Errorf("Failed to check if this node is out-of-date: %w", err)
	}

	if !shouldUpdate {
		logger.Debugf("Cluster node is up-to-date")
		return nil
	}

	return triggerUpdate()
}

func triggerUpdate() error {
	logger.Warn("Member is out-of-date with respect to other cluster members")

	updateExecutable := os.Getenv("INCUS_CLUSTER_UPDATE")
	if updateExecutable == "" {
		logger.Debug("No INCUS_CLUSTER_UPDATE variable set, skipping auto-update")
		return nil
	}

	// Wait a random amout of seconds (up to 30) in order to avoid
	// restarting all cluster members at the same time, and make the
	// upgrade more graceful.
	wait := time.Duration(rand.Intn(30)) * time.Second
	logger.Info("Triggering cluster auto-update soon", logger.Ctx{"wait": wait, "updateExecutable": updateExecutable})
	time.Sleep(wait)

	logger.Info("Triggering cluster auto-update now")
	_, err := subprocess.RunCommand(updateExecutable)
	if err != nil {
		logger.Error("Triggering cluster update failed", logger.Ctx{"err": err})
		return err
	}

	logger.Info("Triggering cluster auto-update succeeded")

	return nil
}

// UpgradeMembersWithoutRole assigns the Spare raft role to all cluster members that are not currently part of the
// raft configuration. It's used for upgrading a cluster from a version without roles support.
func UpgradeMembersWithoutRole(gateway *Gateway, members []db.NodeInfo) error {
	nodes, err := gateway.currentRaftNodes()
	if err == ErrNotLeader {
		return nil
	}

	if err != nil {
		return fmt.Errorf("Failed to get current raft members: %w", err)
	}

	// Convert raft node list to map keyed on ID.
	raftNodeIDs := map[uint64]bool{}
	for _, node := range nodes {
		raftNodeIDs[node.ID] = true
	}

	dqliteClient, err := gateway.getClient()
	if err != nil {
		return fmt.Errorf("Failed to connect to local dqlite member: %w", err)
	}

	defer func() { _ = dqliteClient.Close() }()

	// Check that each member is present in the raft configuration, and add it if not.
	for _, member := range members {
		found := false
		for _, node := range nodes {
			if member.ID == 1 && node.ID == 1 || member.Address == node.Address {
				found = true
				break
			}
		}
		if found {
			continue
		}

		// Try to use the same ID as the node, but it might not be possible if it's use.
		id := uint64(member.ID)
		_, ok := raftNodeIDs[id]
		if ok {
			for _, other := range members {
				_, ok := raftNodeIDs[uint64(other.ID)]
				if !ok {
					id = uint64(other.ID) // Found unused raft ID for member.
					break
				}
			}

			// This can't really happen (but has in the past) since there are always at least as many
			// members as there are nodes, and all of them have different IDs.
			if id == uint64(member.ID) {
				logger.Error("No available raft ID for cluster member", logger.Ctx{"memberID": member.ID, "members": members, "raftMembers": nodes})
				return fmt.Errorf("No available raft ID for cluster member ID %d", member.ID)
			}
		}
		raftNodeIDs[id] = true

		info := db.RaftNode{
			NodeInfo: client.NodeInfo{
				ID:      id,
				Address: member.Address,
				Role:    db.RaftSpare,
			},
			Name: "",
		}

		logger.Info("Add spare dqlite node", logger.Ctx{"id": info.ID, "address": info.Address})

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = dqliteClient.Add(ctx, info.NodeInfo)
		if err != nil {
			return fmt.Errorf("Failed to add dqlite member: %w", err)
		}
	}

	return nil
}
