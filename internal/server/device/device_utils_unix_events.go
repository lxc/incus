package device

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/logger"
	"github.com/lxc/incus/v6/shared/util"
)

// UnixEvent represents the properties of a Unix device inotify event.
type UnixEvent struct {
	Action string // The type of event, either add or remove.
	Path   string // The absolute source path on the host.
}

// UnixSubscription used to subcribe to specific events.
type UnixSubscription struct {
	Path    string                                           // The absolute source path on the host.
	Handler func(UnixEvent) (*deviceConfig.RunConfig, error) // The function to run when an event occurs.
}

// unixHandlers stores the event handler callbacks for Unix events.
var unixHandlers = map[string]UnixSubscription{}

// unixMutex controls access to the unixHandlers map.
var unixMutex sync.Mutex

// unixRegisterHandler registers a handler function to be called whenever a Unix device event occurs.
func unixRegisterHandler(s *state.State, inst instance.Instance, deviceName, path string, handler func(UnixEvent) (*deviceConfig.RunConfig, error)) error {
	if path == "" || handler == nil {
		return fmt.Errorf("Invalid subscription")
	}

	unixMutex.Lock()

	// Null delimited string of project name, instance name and device name.
	key := fmt.Sprintf("%s\000%s\000%s", inst.Project().Name, inst.Name(), deviceName)
	unixHandlers[key] = UnixSubscription{
		Path:    path,
		Handler: handler,
	}

	unixMutex.Unlock()
	identifier := fmt.Sprintf("%d_%s", inst.ID(), deviceName)

	path = filepath.Clean(path)

	// Add inotify watcher to its nearest existing ancestor.
	err := s.DevMonitor.Watch(path, identifier, func(path, event string) bool {
		e := unixNewEvent(event, path)
		unixRunHandlers(s, &e)
		return true
	})
	if err != nil {
		return fmt.Errorf("Failed to add %q to watch targets: %w", path, err)
	}

	logger.Debug("Added watch target", logger.Ctx{"path": path})
	return nil
}

// unixUnregisterHandler removes a registered Unix handler function for a device.
func unixUnregisterHandler(s *state.State, inst instance.Instance, deviceName string) error {
	unixMutex.Lock()

	// Null delimited string of project name, instance name and device name.
	key := fmt.Sprintf("%s\000%s\000%s", inst.Project().Name, inst.Name(), deviceName)

	sub, exists := unixHandlers[key]
	if !exists {
		unixMutex.Unlock()
		return nil
	}

	// Remove active subscription for this device.
	delete(unixHandlers, key)
	unixMutex.Unlock()

	identifier := fmt.Sprintf("%d_%s", inst.ID(), deviceName)

	err := s.DevMonitor.Unwatch(sub.Path, identifier)
	if err != nil {
		return fmt.Errorf("Failed to remove %q from inotify targets: %w", sub.Path, err)
	}

	return nil
}

// unixRunHandlers executes any handlers registered for Unix events.
func unixRunHandlers(state *state.State, event *UnixEvent) {
	unixMutex.Lock()
	defer unixMutex.Unlock()

	for key, sub := range unixHandlers {
		keyParts := strings.SplitN(key, "\000", 3)
		projectName := keyParts[0]
		instanceName := keyParts[1]
		deviceName := keyParts[2]

		// Delete subscription if no handler function defined.
		if sub.Handler == nil {
			delete(unixHandlers, key)
			continue
		}

		// Don't execute handler if subscription path and event paths don't match.
		if sub.Path != event.Path {
			continue
		}

		// Run handler function.
		runConf, err := sub.Handler(*event)
		if err != nil {
			logger.Error("Unix event hook failed", logger.Ctx{"project": projectName, "instance": instanceName, "device": deviceName, "path": sub.Path, "action": event.Action, "err": err})
			continue
		}

		// If runConf supplied, load instance and call its Unix event handler function so
		// any instance specific device actions can occur.
		if runConf != nil {
			instance, err := instance.LoadByProjectAndName(state, projectName, instanceName)
			if err != nil {
				logger.Error("Unix event loading instance failed", logger.Ctx{"err": err, "project": projectName, "instance": instanceName, "device": deviceName})
				continue
			}

			err = instance.DeviceEventHandler(runConf)
			if err != nil {
				logger.Error("Unix event instance handler failed", logger.Ctx{"err": err, "project": projectName, "instance": instanceName, "device": deviceName})
				continue
			}
		}
	}
}

// unixNewEvent returns a newly created Unix device event struct.
// If an empty action is supplied then the action of the event is derived from whether the path
// exists (add) or not (removed). This allows the peculiarities of the inotify API to be somewhat
// masked by the consuming event handler functions.
func unixNewEvent(action string, path string) UnixEvent {
	if action == "" {
		if util.PathExists(path) {
			action = "add"
		} else {
			action = "remove"
		}
	}

	return UnixEvent{
		Action: action,
		Path:   path,
	}
}
