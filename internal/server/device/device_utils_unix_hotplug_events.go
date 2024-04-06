package device

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	deviceConfig "github.com/lxc/incus/v6/internal/server/device/config"
	"github.com/lxc/incus/v6/internal/server/instance"
	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/logger"
)

// UnixHotplugEvent represents the properties of a Unix hotplug device uevent.
type UnixHotplugEvent struct {
	Action string

	Vendor  string
	Product string

	Path        string
	Major       uint32
	Minor       uint32
	Subsystem   string
	UeventParts []string
	UeventLen   int
}

// unixHotplugHandlers stores the event handler callbacks for Unix hotplug events.
var unixHotplugHandlers = map[string]func(UnixHotplugEvent) (*deviceConfig.RunConfig, error){}

// unixHotplugMutex controls access to the unixHotplugHandlers map.
var unixHotplugMutex sync.Mutex

// unixHotplugRegisterHandler registers a handler function to be called whenever a Unix hotplug device event occurs.
func unixHotplugRegisterHandler(instance instance.Instance, deviceName string, handler func(UnixHotplugEvent) (*deviceConfig.RunConfig, error)) {
	unixHotplugMutex.Lock()
	defer unixHotplugMutex.Unlock()

	// Null delimited string of project name, instance name and device name.
	key := fmt.Sprintf("%s\000%s\000%s", instance.Project().Name, instance.Name(), deviceName)
	unixHotplugHandlers[key] = handler
}

// unixHotplugUnregisterHandler removes a registered Unix hotplug handler function for a device.
func unixHotplugUnregisterHandler(instance instance.Instance, deviceName string) {
	unixHotplugMutex.Lock()
	defer unixHotplugMutex.Unlock()

	// Null delimited string of project name, instance name and device name.
	key := fmt.Sprintf("%s\000%s\000%s", instance.Project().Name, instance.Name(), deviceName)
	delete(unixHotplugHandlers, key)
}

// UnixHotplugRunHandlers executes any handlers registered for Unix hotplug events.
func UnixHotplugRunHandlers(state *state.State, event *UnixHotplugEvent) {
	unixHotplugMutex.Lock()
	defer unixHotplugMutex.Unlock()

	for key, hook := range unixHotplugHandlers {
		keyParts := strings.SplitN(key, "\000", 3)
		projectName := keyParts[0]
		instanceName := keyParts[1]
		deviceName := keyParts[2]

		if hook == nil {
			delete(unixHotplugHandlers, key)
			continue
		}

		runConf, err := hook(*event)
		if err != nil {
			logger.Error("Unix hotplug event hook failed", logger.Ctx{"err": err, "project": projectName, "instance": instanceName, "device": deviceName})
			continue
		}

		// If runConf supplied, load instance and call its Unix hotplug event handler function so
		// any instance specific device actions can occur.
		if runConf != nil {
			instance, err := instance.LoadByProjectAndName(state, projectName, instanceName)
			if err != nil {
				logger.Error("Unix hotplug event loading instance failed", logger.Ctx{"err": err, "project": projectName, "instance": instanceName, "device": deviceName})
				continue
			}

			err = instance.DeviceEventHandler(runConf)
			if err != nil {
				logger.Error("Unix hotplug event instance handler failed", logger.Ctx{"err": err, "project": projectName, "instance": instanceName, "device": deviceName})
				continue
			}
		}
	}
}

// UnixHotplugNewEvent instantiates a new UnixHotplugEvent struct.
func UnixHotplugNewEvent(action string, vendor string, product string, major string, minor string, subsystem string, devname string, ueventParts []string, ueventLen int) (UnixHotplugEvent, error) {
	majorInt, err := strconv.ParseUint(major, 10, 32)
	if err != nil {
		return UnixHotplugEvent{}, err
	}

	minorInt, err := strconv.ParseUint(minor, 10, 32)
	if err != nil {
		return UnixHotplugEvent{}, err
	}

	return UnixHotplugEvent{
		action,
		vendor,
		product,
		devname,
		uint32(majorInt),
		uint32(minorInt),
		subsystem,
		ueventParts,
		ueventLen,
	}, nil
}
