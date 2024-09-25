package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSortableDevices(t *testing.T) {
	devices := Devices{
		"a-unix1":       Device{"type": "unix"},
		"a-unix2":       Device{"type": "unix"},
		"b-disk1":       Device{"type": "disk", "path": "/foo/bar"},
		"b-disk2":       Device{"type": "disk", "path": "/foo"},
		"b-disk3":       Device{"type": "disk", "path": "/"},
		"z-nic-nested1": Device{"type": "nic", "nested": "foo1"},
		"z-nic-nested2": Device{"type": "nic", "nested": "foo2"},
		"z-nic1":        Device{"type": "nic"},
		"z-nic2":        Device{"type": "nic"},
	}

	expectedSorted := DevicesSortable{
		DeviceNamed{Name: "z-nic1", Config: Device{"type": "nic"}},
		DeviceNamed{Name: "z-nic2", Config: Device{"type": "nic"}},
		DeviceNamed{Name: "z-nic-nested1", Config: Device{"type": "nic", "nested": "foo1"}},
		DeviceNamed{Name: "z-nic-nested2", Config: Device{"type": "nic", "nested": "foo2"}},
		DeviceNamed{Name: "b-disk3", Config: Device{"type": "disk", "path": "/"}},
		DeviceNamed{Name: "b-disk2", Config: Device{"type": "disk", "path": "/foo"}},
		DeviceNamed{Name: "b-disk1", Config: Device{"type": "disk", "path": "/foo/bar"}},
		DeviceNamed{Name: "a-unix1", Config: Device{"type": "unix"}},
		DeviceNamed{Name: "a-unix2", Config: Device{"type": "unix"}},
	}

	result := devices.Sorted()
	assert.Equal(t, expectedSorted, result)

	expectedReversed := DevicesSortable{
		DeviceNamed{Name: "a-unix2", Config: Device{"type": "unix"}},
		DeviceNamed{Name: "a-unix1", Config: Device{"type": "unix"}},
		DeviceNamed{Name: "b-disk1", Config: Device{"type": "disk", "path": "/foo/bar"}},
		DeviceNamed{Name: "b-disk2", Config: Device{"type": "disk", "path": "/foo"}},
		DeviceNamed{Name: "b-disk3", Config: Device{"type": "disk", "path": "/"}},
		DeviceNamed{Name: "z-nic-nested2", Config: Device{"type": "nic", "nested": "foo2"}},
		DeviceNamed{Name: "z-nic-nested1", Config: Device{"type": "nic", "nested": "foo1"}},
		DeviceNamed{Name: "z-nic2", Config: Device{"type": "nic"}},
		DeviceNamed{Name: "z-nic1", Config: Device{"type": "nic"}},
	}

	result = devices.Reversed()
	assert.Equal(t, expectedReversed, result)
}
