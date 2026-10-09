package drivers

import (
	"maps"
	"reflect"
	"slices"
	"testing"

	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
)

func TestNeedsNewInstanceIDNICNames(t *testing.T) {
	tests := []struct {
		name       string
		oldConfig  map[string]string
		newConfig  map[string]string
		oldDevices deviceConfig.Devices
		newDevices deviceConfig.Devices
		want       bool
	}{
		{name: "nil inputs"},
		{name: "empty inputs", oldConfig: map[string]string{}, newConfig: map[string]string{}, oldDevices: deviceConfig.Devices{}, newDevices: deviceConfig.Devices{}},
		{name: "nil and empty equivalent", newConfig: map[string]string{}, newDevices: deviceConfig.Devices{}},
		{name: "unchanged explicit name", oldDevices: deviceConfig.Devices{"lan": {"type": "nic", "name": "eth0"}}, newDevices: deviceConfig.Devices{"lan": {"type": "nic", "name": "eth0"}}},
		{name: "changed explicit name", oldDevices: deviceConfig.Devices{"lan": {"type": "nic", "name": "eth0"}}, newDevices: deviceConfig.Devices{"lan": {"type": "nic", "name": "eth1"}}, want: true},
		{name: "volatile name changed", oldConfig: map[string]string{"volatile.lan.name": "eth0"}, newConfig: map[string]string{"volatile.lan.name": "eth1"}, oldDevices: deviceConfig.Devices{"lan": {"type": "nic"}}, newDevices: deviceConfig.Devices{"lan": {"type": "nic"}}, want: true},
		{name: "unchanged volatile name", oldConfig: map[string]string{"volatile.lan.name": "eth0"}, newConfig: map[string]string{"volatile.lan.name": "eth0"}, oldDevices: deviceConfig.Devices{"lan": {"type": "nic"}}, newDevices: deviceConfig.Devices{"lan": {"type": "nic"}}},
		{name: "old only volatile name", oldConfig: map[string]string{"volatile.lan.name": "eth0"}, oldDevices: deviceConfig.Devices{"lan": {"type": "nic"}}, newDevices: deviceConfig.Devices{"lan": {"type": "nic"}}, want: true},
		{name: "new only volatile name", newConfig: map[string]string{"volatile.lan.name": "eth0"}, oldDevices: deviceConfig.Devices{"lan": {"type": "nic"}}, newDevices: deviceConfig.Devices{"lan": {"type": "nic"}}, want: true},
		{name: "explicit to equivalent volatile", newConfig: map[string]string{"volatile.lan.name": "eth0"}, oldDevices: deviceConfig.Devices{"lan": {"type": "nic", "name": "eth0"}}, newDevices: deviceConfig.Devices{"lan": {"type": "nic"}}},
		{name: "volatile to equivalent explicit", oldConfig: map[string]string{"volatile.lan.name": "eth0"}, oldDevices: deviceConfig.Devices{"lan": {"type": "nic"}}, newDevices: deviceConfig.Devices{"lan": {"type": "nic", "name": "eth0"}}},
		{name: "explicit name takes precedence", oldConfig: map[string]string{"volatile.lan.name": "old"}, newConfig: map[string]string{"volatile.lan.name": "new"}, oldDevices: deviceConfig.Devices{"lan": {"type": "nic", "name": "eth0"}}, newDevices: deviceConfig.Devices{"lan": {"type": "nic", "name": "eth0"}}},
		{name: "explicit change overrides unchanged volatile", oldConfig: map[string]string{"volatile.lan.name": "eth0"}, newConfig: map[string]string{"volatile.lan.name": "eth0"}, oldDevices: deviceConfig.Devices{"lan": {"type": "nic", "name": "eth0"}}, newDevices: deviceConfig.Devices{"lan": {"type": "nic", "name": "eth1"}}, want: true},
		{name: "absent volatile uses device key", oldDevices: deviceConfig.Devices{"eth0": {"type": "nic"}}, newDevices: deviceConfig.Devices{"lan": {"type": "nic", "name": "eth0"}}},
		{name: "empty volatile uses device key", oldConfig: map[string]string{"volatile.eth0.name": ""}, oldDevices: deviceConfig.Devices{"eth0": {"type": "nic"}}, newDevices: deviceConfig.Devices{"lan": {"type": "nic", "name": "eth0"}}},
		{name: "absent and empty volatile equivalent", newConfig: map[string]string{"volatile.lan.name": ""}, oldDevices: deviceConfig.Devices{"lan": {"type": "nic"}}, newDevices: deviceConfig.Devices{"lan": {"type": "nic"}}},
		{name: "volatile to equivalent fallback", oldConfig: map[string]string{"volatile.eth0.name": "eth0"}, oldDevices: deviceConfig.Devices{"eth0": {"type": "nic"}}, newDevices: deviceConfig.Devices{"eth0": {"type": "nic"}}},
		{name: "fallback to equivalent volatile", newConfig: map[string]string{"volatile.eth0.name": "eth0"}, oldDevices: deviceConfig.Devices{"eth0": {"type": "nic"}}, newDevices: deviceConfig.Devices{"eth0": {"type": "nic"}}},
		{name: "added NIC", newDevices: deviceConfig.Devices{"eth0": {"type": "nic"}}, want: true},
		{name: "removed NIC", oldDevices: deviceConfig.Devices{"eth0": {"type": "nic"}}, want: true},
		{name: "removed NIC retains old volatile resolution", oldConfig: map[string]string{"volatile.removed.name": "eth0"}, oldDevices: deviceConfig.Devices{"removed": {"type": "nic"}}, newDevices: deviceConfig.Devices{"replacement": {"type": "nic", "name": "eth0"}}},
		{name: "NIC becomes non NIC", oldDevices: deviceConfig.Devices{"lan": {"type": "nic"}}, newDevices: deviceConfig.Devices{"lan": {"type": "disk"}}, want: true},
		{name: "non NIC becomes NIC", oldDevices: deviceConfig.Devices{"lan": {"type": "disk"}}, newDevices: deviceConfig.Devices{"lan": {"type": "nic"}}, want: true},
		{name: "non NIC devices ignored", oldConfig: map[string]string{"volatile.root.name": "old"}, newConfig: map[string]string{"volatile.root.name": "new"}, oldDevices: deviceConfig.Devices{"root": {"type": "disk", "name": "old"}, "nil": nil}, newDevices: deviceConfig.Devices{"root": {"type": "disk", "name": "new"}, "empty": {}}},
		{name: "device keys and order irrelevant", oldDevices: deviceConfig.Devices{"a": {"type": "nic", "name": "eth0"}, "b": {"type": "nic", "name": "eth1"}}, newDevices: deviceConfig.Devices{"y": {"type": "nic", "name": "eth1"}, "z": {"type": "nic", "name": "eth0"}}},
		{name: "duplicate effective name added", oldDevices: deviceConfig.Devices{"a": {"type": "nic", "name": "eth0"}}, newDevices: deviceConfig.Devices{"a": {"type": "nic", "name": "eth0"}, "b": {"type": "nic", "name": "eth0"}}},
		{name: "duplicate effective name removed", oldDevices: deviceConfig.Devices{"a": {"type": "nic", "name": "eth0"}, "b": {"type": "nic", "name": "eth0"}}, newDevices: deviceConfig.Devices{"a": {"type": "nic", "name": "eth0"}}},
		{name: "duplicate mixed name sources", oldConfig: map[string]string{"volatile.lan.name": "eth0"}, oldDevices: deviceConfig.Devices{"eth0": {"type": "nic"}, "lan": {"type": "nic"}, "explicit": {"type": "nic", "name": "eth0"}}, newDevices: deviceConfig.Devices{"eth0": {"type": "nic"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkNeedsNewInstanceID(t, nil, tt.oldConfig, tt.newConfig, tt.oldDevices, tt.newDevices, tt.want)
		})
	}
}

func TestNeedsNewInstanceIDChangedConfig(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{key: "cloud-init.vendor-data", want: true},
		{key: "cloud-init.user-data", want: true},
		{key: "cloud-init.network-config", want: true},
		{key: "user.vendor-data", want: true},
		{key: "user.user-data", want: true},
		{key: "user.network-config", want: true},
		{key: "user.unrelated"},
		{key: "user.user-data.extra"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			oldConfig := map[string]string{"volatile.lan.name": "old", "user.keep": "old"}
			newConfig := map[string]string{"volatile.lan.name": "new", "user.keep": "new"}
			oldDevices := deviceConfig.Devices{"lan": {"type": "nic", "name": "eth0"}}
			newDevices := deviceConfig.Devices{"lan": {"type": "nic", "name": "eth0"}}
			checkNeedsNewInstanceID(t, []string{"user.unrelated", tt.key, "user.keep"}, oldConfig, newConfig, oldDevices, newDevices, tt.want)
		})
	}
}

func checkNeedsNewInstanceID(t *testing.T, changedConfig []string, oldConfig, newConfig map[string]string, oldDevices, newDevices deviceConfig.Devices, want bool) {
	t.Helper()

	cloneDevices := func(devices deviceConfig.Devices) deviceConfig.Devices {
		cloned := maps.Clone(devices)
		for name, device := range cloned {
			cloned[name] = maps.Clone(device)
		}

		return cloned
	}

	changedBefore := slices.Clone(changedConfig)
	oldConfigBefore := maps.Clone(oldConfig)
	newConfigBefore := maps.Clone(newConfig)
	oldDevicesBefore := cloneDevices(oldDevices)
	newDevicesBefore := cloneDevices(newDevices)
	got := needsNewInstanceID(changedConfig, oldConfig, newConfig, oldDevices, newDevices)
	if !reflect.DeepEqual(changedConfig, changedBefore) || !reflect.DeepEqual(oldConfig, oldConfigBefore) || !reflect.DeepEqual(newConfig, newConfigBefore) || !reflect.DeepEqual(oldDevices, oldDevicesBefore) || !reflect.DeepEqual(newDevices, newDevicesBefore) {
		t.Error("needsNewInstanceID mutated an input")
	}

	if got != want {
		t.Errorf("needsNewInstanceID() = %v, want %v", got, want)
	}
}
