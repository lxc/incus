package drivers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	in "k8s.io/utils/inotify"

	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/util"
)

var inotifyLoaded bool

type inotify struct {
	common

	watcher *in.Watcher
}

func (d *inotify) Name() string {
	return "inotify"
}

func (d *inotify) load(ctx context.Context) error {
	if inotifyLoaded {
		return nil
	}

	var err error

	d.watcher, err = in.NewWatcher()
	if err != nil {
		return fmt.Errorf("Failed to initialize: %w", err)
	}

	err = d.watchFSTree(d.prefixPath)
	if err != nil {
		_ = d.watcher.Close()
		inotifyLoaded = false
		return fmt.Errorf("Failed to watch directory %q: %w", d.prefixPath, err)
	}

	go d.getEvents(ctx)

	inotifyLoaded = true

	return nil
}

func (d *inotify) getEvents(ctx context.Context) {
	for {
		select {
		// Clean up if context is done.
		case <-ctx.Done():
			_ = d.watcher.Close()
			inotifyLoaded = false
			return
		case event := <-d.watcher.Event:
			event.Name = filepath.Clean(event.Name)
			isCreate := event.Mask&in.InCreate != 0
			isDelete := event.Mask&in.InDelete != 0

			// Only consider create and delete events.
			if !isCreate && !isDelete {
				continue
			}

			// New event for a directory.
			if event.Mask&in.InIsdir != 0 {
				// If it's a create event, then setup watches on any sub-directories.
				if isCreate {
					_ = d.watchFSTree(event.Name)
				}

				// Check whether there's a watch on the directory.
				d.mu.Lock()
				var action Event

				if isCreate {
					action = Add
				} else {
					action = Remove
				}

				for path := range d.watches {
					// Always call the handlers that have a prefix of the event path,
					// in case a watched file is inside the newly created or now deleted
					// directory, otherwise we'll miss the event. The handlers themselves are
					// expected to check the state of the specific path they are interested in.
					if !strings.HasPrefix(path, event.Name) {
						continue
					}

					for identifier, f := range d.watches[path] {
						ret := f(path, action.String())
						if !ret {
							delete(d.watches[path], identifier)

							if len(d.watches[path]) == 0 {
								delete(d.watches, path)
							}
						}
					}
				}
				d.mu.Unlock()
				continue
			}

			// Check whether there's a watch on a specific file or directory.
			d.mu.Lock()
			var action Event
			if isCreate {
				action = Add
			} else {
				action = Remove
			}

			for path := range d.watches {
				if event.Name != path {
					continue
				}

				for identifier, f := range d.watches[path] {
					ret := f(path, action.String())
					if !ret {
						delete(d.watches[path], identifier)

						if len(d.watches[path]) == 0 {
							delete(d.watches, path)
						}
					}
				}

				break
			}

			d.mu.Unlock()
		case err := <-d.watcher.Error:
			d.logger.Error("Received event error", logger.Ctx{"err": err})
		}
	}
}

func (d *inotify) watchFSTree(path string) error {
	if !util.PathExists(path) {
		return errors.New("Path doesn't exist")
	}

	err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		// Check for errors here as we only care about directories. Files and symlinks aren't of interest for this.
		if err != nil {
			if os.IsPermission(err) {
				return nil
			}

			d.logger.Warn("Error visiting path", logger.Ctx{"path": path, "err": err})
			return nil
		}

		// Ignore files and symlinks.
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		// Only watch on real paths for CREATE and DELETE events.
		err = d.watcher.AddWatch(path, in.InCreate|in.InDelete)
		if err != nil {
			d.logger.Warn("Failed to watch path", logger.Ctx{"path": path, "err": err})
			return nil
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("Failed to watch directory tree: %w", err)
	}

	return nil
}
