package cluster

import (
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cowsql/go-cowsql/client"
	cowsqlapi "github.com/cowsql/go-cowsql/cluster/api"
	cowsqldb "github.com/cowsql/go-cowsql/cluster/db"

	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/internal/server/state"
	"github.com/lxc/incus/v7/shared/logger"
	"github.com/lxc/incus/v7/shared/subprocess"
	localtls "github.com/lxc/incus/v7/shared/tls"
)

// The API endpoint path that gets routed to a cowsql server handler for
// performing SQL queries against the cowsql server running on this node.
const databaseEndpoint = "/internal/database"

// PreUpdateCheck checks for the INCUS_CLUSTER_UPDATE env var and returns the resulting executable path.
func PreUpdateCheck(s *state.State) (func() error, error) {
	// If on IncusOS, start by trying an automatic update.
	if s.OS.IncusOS != nil {
		err := s.OS.IncusOS.TriggerSystemUpdateCheck()
		if err != nil {
			return nil, err
		}
	}

	updateExecutable := os.Getenv("INCUS_CLUSTER_UPDATE")
	if updateExecutable == "" {
		logger.Debug("No INCUS_CLUSTER_UPDATE variable set, skipping auto-update")
		return nil, nil
	}

	return func() error {
		// Wait a random amount of seconds (up to 30) in order to avoid
		// restarting all cluster members at the same time, and make the
		// upgrade more graceful.
		wait := time.Duration(rand.Intn(30)) * time.Second
		slog.Info("Triggering cluster auto-update soon", "wait", wait, "updateExecutable", updateExecutable)
		time.Sleep(wait)

		slog.Info("Triggering cluster auto-update now")
		_, err := subprocess.RunCommand(updateExecutable)
		if err != nil {
			slog.Error("Triggering cluster update failed", "err", err)
			return err
		}

		slog.Info("Triggering cluster auto-update succeeded")

		return nil
	}, nil
}

// NotifyUpgradeCompleted sends a notification to all other nodes in the
// cluster that any possible pending database update has been applied, and any
// nodes which was waiting for this node to be upgraded should re-check if it's
// okay to move forward.
func NotifyUpgradeCompleted(s *state.State, networkCert *localtls.CertInfo, serverCert *localtls.CertInfo) error {
	notifier, err := NewNotifier(s, networkCert, serverCert, NotifyTryAll)
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

		cowsqlapi.SetCOWSQLVersionHeader(request)

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

func createReconfigurePatch(database cowsqldb.Node, nodes []client.NodeInfo) error {
	var content strings.Builder
	for _, raftNode := range nodes {
		fmt.Fprintf(&content, "UPDATE nodes SET address = %q WHERE id = %d;\n", raftNode.Address, raftNode.ID)
	}

	if len(content.String()) > 0 {
		filePath := filepath.Join(filepath.Dir(database.GlobalDatabaseDir()), "patch.global.sql")
		file, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}

		defer func() {
			err := file.Close()
			if err != nil && !errors.Is(err, os.ErrClosed) {
				slog.Warn("Failed to close file", "err", err)
			}
		}()

		_, err = file.Write([]byte(content.String()))
		if err != nil {
			return err
		}

		err = file.Close()
		if err != nil {
			return err
		}
	}

	return nil
}
