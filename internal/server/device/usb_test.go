package device

import (
	"testing"

	"github.com/stretchr/testify/assert"

	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
)

func TestUSBMatchEvent(t *testing.T) {
	event := func(action string, serial string, devNum int) USBEvent {
		return USBEvent{Action: action, Vendor: "04e8", Product: "6860", Serial: serial, BusNum: 7, DevNum: devNum}
	}

	type step struct {
		event USBEvent
		match bool
	}

	tests := []struct {
		name   string
		config deviceConfig.Device
		steps  []step
	}{
		{
			name:   "Without serial",
			config: deviceConfig.Device{"vendorid": "04e8"},
			steps: []step{
				{event("add", "", 22), true},
				{event("remove", "", 22), true},
			},
		},
		{
			name:   "Serial device re-enumerated",
			config: deviceConfig.Device{"vendorid": "04e8", "serial": "RZCY10GL6CN"},
			steps: []step{
				{event("add", "RZCY10GL6CN", 22), true},
				{event("remove", "", 22), true},
				{event("remove", "", 22), false},
				{event("add", "RZCY10GL6CN", 23), true},
				{event("remove", "", 23), true},
			},
		},
		{
			name:   "Other serial on the same model",
			config: deviceConfig.Device{"vendorid": "04e8", "serial": "RZCY10GL6CN"},
			steps: []step{
				{event("add", "R58M40ABCDE", 24), false},
				{event("remove", "", 24), false},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serialDevs := map[[2]int]bool{}
			for i, s := range tt.steps {
				assert.Equal(t, s.match, usbMatchEvent(tt.config, serialDevs, &s.event), "step %d", i)
			}
		})
	}
}
