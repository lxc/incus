package zone

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/utils/inotify"

	"github.com/lxc/incus/v7/internal/server/db"
	"github.com/lxc/incus/v7/internal/server/network"
	"github.com/lxc/incus/v7/internal/server/state"
	internalUtil "github.com/lxc/incus/v7/internal/util"
	"github.com/lxc/incus/v7/shared/logger"
	"github.com/lxc/incus/v7/shared/util"
)

// StartLeasesWatcher monitors dnsmasq lease and static host files for changes
// and triggers DNS NOTIFY for the network zones of the affected networks.
func StartLeasesWatcher(ctx context.Context, s *state.State) error {
	watcher, err := inotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("Failed to initialize inotify: %w", err)
	}

	networksPath := internalUtil.VarPath("networks")

	// Watch for new network directories.
	err = watcher.AddWatch(networksPath, inotify.InCreate) // codespell:ignore increate
	if err != nil {
		_ = watcher.Close()
		return fmt.Errorf("Failed to watch %q: %w", networksPath, err)
	}

	// Watch the existing network directories.
	entries, err := os.ReadDir(networksPath)
	if err != nil {
		_ = watcher.Close()
		return fmt.Errorf("Failed to list %q: %w", networksPath, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		watchNetworkDir(watcher, filepath.Join(networksPath, entry.Name()))
	}

	go func() {
		defer func() { _ = watcher.Close() }()

		for {
			select {
			case <-ctx.Done():
				return
			case event := <-watcher.Event:
				handleLeasesEvent(s, watcher, networksPath, event)
			case err := <-watcher.Error:
				logger.Warn("Network zones watcher error", logger.Ctx{"err": err})
			}
		}
	}()

	return nil
}

// watchNetworkDir sets up the watches for a network directory.
func watchNetworkDir(watcher *inotify.Watcher, dirPath string) {
	err := watcher.AddWatch(dirPath, inotify.InCreate|inotify.InModify|inotify.InCloseWrite|inotify.InMovedTo) // codespell:ignore increate
	if err != nil {
		logger.Warn("Failed to watch network directory", logger.Ctx{"path": dirPath, "err": err})
		return
	}

	// Watch the static host entries if present.
	hostsPath := filepath.Join(dirPath, "dnsmasq.hosts")
	if util.PathExists(hostsPath) {
		err = watcher.AddWatch(hostsPath, inotify.InCloseWrite|inotify.InMovedTo|inotify.InDelete)
		if err != nil {
			logger.Warn("Failed to watch network hosts directory", logger.Ctx{"path": hostsPath, "err": err})
		}
	}
}

// handleLeasesEvent processes a single inotify event from the networks directory tree.
func handleLeasesEvent(s *state.State, watcher *inotify.Watcher, networksPath string, event *inotify.Event) {
	rel, err := filepath.Rel(networksPath, filepath.Clean(event.Name))
	if err != nil || strings.HasPrefix(rel, "..") {
		return
	}

	fields := strings.Split(rel, string(os.PathSeparator))
	switch len(fields) {
	case 1:
		// New network directory.
		if event.Mask&inotify.InCreate != 0 && event.Mask&inotify.InIsdir != 0 { // codespell:ignore increate
			watchNetworkDir(watcher, event.Name)
		}

	case 2:
		// Change within a network directory.
		if fields[1] == "dnsmasq.hosts" && event.Mask&inotify.InCreate != 0 && event.Mask&inotify.InIsdir != 0 { // codespell:ignore increate
			err := watcher.AddWatch(event.Name, inotify.InCloseWrite|inotify.InMovedTo|inotify.InDelete)
			if err != nil {
				logger.Warn("Failed to watch network hosts directory", logger.Ctx{"path": event.Name, "err": err})
			}

			return
		}

		if fields[1] == "dnsmasq.leases" && event.Mask&(inotify.InModify|inotify.InCloseWrite|inotify.InMovedTo) != 0 {
			notifyNetworkZones(s, fields[0])
		}

	case 3:
		// Change to a static host entry.
		if fields[1] == "dnsmasq.hosts" {
			notifyNetworkZones(s, fields[0])
		}
	}
}

// notifyNetworkZones triggers DNS NOTIFY for all zones used by the given network.
func notifyNetworkZones(s *state.State, networkName string) {
	var netConfigs []map[string]string

	err := s.DB.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		projectNetworks, err := tx.GetCreatedNetworks(ctx)
		if err != nil {
			return err
		}

		for _, networks := range projectNetworks {
			for _, netInfo := range networks {
				if netInfo.Name != networkName {
					continue
				}

				netConfigs = append(netConfigs, netInfo.Config)
			}
		}

		return nil
	})
	if err != nil {
		logger.Warn("Failed to find network zones to notify", logger.Ctx{"network": networkName, "err": err})
		return
	}

	for _, netConfig := range netConfigs {
		network.DNSNotifyZones(s, netConfig)
	}
}
