// Package mirror keeps a persistent copy of files that are written to a local directory.
package mirror

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"k8s.io/utils/inotify"

	"github.com/lxc/incus/v7/shared/logger"
)

// Mirror replicates the files of a local directory into a persistent directory.
type Mirror struct {
	local      string
	persistent string
	filter     func(name string) bool
	logger     logger.Logger

	mu      sync.Mutex
	pending map[string]struct{}
	timer   *time.Timer
	watcher *inotify.Watcher
	exited  chan struct{}

	syncMu sync.Mutex
}

// Delay between the first change and its replication.
const delay = 500 * time.Millisecond

var (
	mirrorsMu sync.Mutex
	mirrors   = map[string]*Mirror{}
)

// Start replicates the files of local matching filter into persistent, syncing them all first.
func Start(local string, persistent string, filter func(name string) bool) error {
	mirrorsMu.Lock()
	defer mirrorsMu.Unlock()

	_, ok := mirrors[local]
	if ok {
		return nil
	}

	m := &Mirror{
		local:      local,
		persistent: persistent,
		filter:     filter,
		logger:     logger.AddContext(logger.Ctx{"local": local, "persistent": persistent}),
		pending:    map[string]struct{}{},
	}

	err := m.syncAll()
	if err != nil {
		return err
	}

	err = m.watch()
	if err != nil {
		return err
	}

	mirrors[local] = m

	return nil
}

// Stop ends the replication of local after a final sync of its files.
func Stop(local string) error {
	mirrorsMu.Lock()
	m, ok := mirrors[local]
	delete(mirrors, local)
	mirrorsMu.Unlock()

	if !ok {
		return nil
	}

	m.unwatch()

	return m.syncAll()
}

// Active returns whether local is being replicated.
func Active(local string) bool {
	mirrorsMu.Lock()
	defer mirrorsMu.Unlock()

	_, ok := mirrors[local]

	return ok
}

// Flush syncs the files of every mirror whose local directory is under prefix.
func Flush(prefix string) error {
	for _, m := range find(prefix) {
		err := m.syncAll()
		if err != nil {
			return err
		}
	}

	return nil
}

// Pause syncs and stops watching every mirror whose local directory is under prefix.
func Pause(prefix string) error {
	for _, m := range find(prefix) {
		m.unwatch()

		err := m.syncAll()
		if err != nil {
			return err
		}
	}

	return nil
}

// Resume syncs and restarts watching every paused mirror whose local directory is under prefix.
func Resume(prefix string) error {
	for _, m := range find(prefix) {
		err := m.syncAll()
		if err != nil {
			return err
		}

		err = m.watch()
		if err != nil {
			return err
		}
	}

	return nil
}

func find(prefix string) []*Mirror {
	mirrorsMu.Lock()
	defer mirrorsMu.Unlock()

	found := []*Mirror{}
	for local, m := range mirrors {
		if local == prefix || strings.HasPrefix(local, prefix+"/") {
			found = append(found, m)
		}
	}

	return found
}

func (m *Mirror) watch() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.watcher != nil {
		return nil
	}

	watcher, err := inotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("Failed creating watcher: %w", err)
	}

	err = watcher.AddWatch(m.local, inotify.InCloseWrite|inotify.InModify|inotify.InMovedTo|inotify.InMovedFrom|inotify.InDelete)
	if err != nil {
		_ = watcher.Close()
		return fmt.Errorf("Failed watching %q: %w", m.local, err)
	}

	m.watcher = watcher
	m.exited = make(chan struct{})

	go m.handleEvents(watcher, m.exited)

	return nil
}

func (m *Mirror) unwatch() {
	m.mu.Lock()
	watcher := m.watcher
	exited := m.exited
	m.watcher = nil

	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}

	m.mu.Unlock()

	if watcher == nil {
		return
	}

	_ = watcher.Close()
	<-exited
}

func (m *Mirror) handleEvents(watcher *inotify.Watcher, exited chan struct{}) {
	defer close(exited)

	for {
		select {
		case event, ok := <-watcher.Event:
			if !ok {
				return
			}

			name := filepath.Base(event.Name)
			if !m.filter(name) {
				continue
			}

			m.mu.Lock()
			m.pending[name] = struct{}{}
			if m.timer == nil {
				m.timer = time.AfterFunc(delay, m.syncPending)
			}

			m.mu.Unlock()

		case err, ok := <-watcher.Error:
			if !ok {
				return
			}

			m.logger.Warn("Mirror watcher error", logger.Ctx{"err": err})
		}
	}
}

func (m *Mirror) syncPending() {
	m.mu.Lock()
	pending := m.pending
	m.pending = map[string]struct{}{}
	m.timer = nil
	m.mu.Unlock()

	m.syncMu.Lock()
	defer m.syncMu.Unlock()

	for name := range pending {
		err := m.syncFile(name)
		if err != nil {
			m.logger.Warn("Failed mirroring file", logger.Ctx{"name": name, "err": err})
		}
	}
}

// syncAll replicates every matching local file and removes persistent files that no longer exist locally.
func (m *Mirror) syncAll() error {
	m.syncMu.Lock()
	defer m.syncMu.Unlock()

	m.mu.Lock()
	m.pending = map[string]struct{}{}
	m.mu.Unlock()

	localNames, err := m.list(m.local)
	if err != nil {
		return err
	}

	for _, name := range localNames {
		err := m.syncFile(name)
		if err != nil {
			return err
		}
	}

	persistentNames, err := m.list(m.persistent)
	if err != nil {
		return err
	}

	for _, name := range persistentNames {
		_, err := os.Lstat(filepath.Join(m.local, name))
		if err == nil {
			continue
		}

		err = m.syncFile(name)
		if err != nil {
			return err
		}
	}

	return nil
}

// list returns the regular files of dir accepted by the filter.
func (m *Mirror) list(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("Failed reading %q: %w", dir, err)
	}

	names := []string{}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !m.filter(entry.Name()) {
			continue
		}

		names = append(names, entry.Name())
	}

	return names, nil
}

// syncFile replicates a single file, removing the persistent copy if it's gone locally.
func (m *Mirror) syncFile(name string) error {
	src := filepath.Join(m.local, name)
	dst := filepath.Join(m.persistent, name)

	fi, err := os.Lstat(src)
	if errors.Is(err, os.ErrNotExist) {
		err = os.Remove(dst)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}

		return nil
	}

	if err != nil {
		return err
	}

	if !fi.Mode().IsRegular() {
		return nil
	}

	return CopyFile(src, dst)
}
