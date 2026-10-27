package device

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
	"github.com/lxc/incus/v7/internal/server/instance/instancetype"
)

func TestNicValidateQdisc(t *testing.T) {
	tests := []struct {
		name     string
		config   deviceConfig.Device
		instType instancetype.Type
		err      string
	}{
		{
			name:     "No queuing discipline",
			config:   deviceConfig.Device{},
			instType: instancetype.VM,
		},
		{
			name:     "A queuing discipline without an attachment",
			config:   deviceConfig.Device{"queue.discipline": "fq_codel"},
			instType: instancetype.Container,
		},
		{
			name:     "An attachment on a container",
			config:   deviceConfig.Device{"queue.discipline": "fq_codel", "queue.discipline.attach": "queue"},
			instType: instancetype.Container,
			err:      "The queuing discipline attachment cannot be applied to containers",
		},
		{
			name:     "An attachment without a queuing discipline",
			config:   deviceConfig.Device{"queue.discipline.attach": "queue"},
			instType: instancetype.VM,
			err:      "The queuing discipline attachment requires a queuing discipline",
		},
		{
			name:     "A per queue attachment with an ingress limit",
			config:   deviceConfig.Device{"queue.discipline": "fq_codel", "queue.discipline.attach": "queue", "limits.ingress": "1Mbit"},
			instType: instancetype.VM,
			err:      "The queuing discipline cannot be attached per queue when an ingress limit is set",
		},
		{
			name:     "A per queue attachment with a max limit",
			config:   deviceConfig.Device{"queue.discipline": "fq_codel", "queue.discipline.attach": "queue", "limits.max": "1Mbit"},
			instType: instancetype.VM,
			err:      "The queuing discipline cannot be attached per queue when an ingress limit is set",
		},
		{
			name:     "A root attachment with an ingress limit is valid",
			config:   deviceConfig.Device{"queue.discipline": "fq_codel", "queue.discipline.attach": "root", "limits.ingress": "1Mbit"},
			instType: instancetype.VM,
		},
		{
			name:     "A per queue attachment with an egress limit is valid",
			config:   deviceConfig.Device{"queue.discipline": "fq_codel", "queue.discipline.attach": "queue", "limits.egress": "1Mbit"},
			instType: instancetype.VM,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := nicValidateQdisc(tt.config, tt.instType)
			if tt.err == "" {
				assert.NoError(t, err)
				return
			}

			assert.EqualError(t, err, tt.err)
		})
	}
}

func TestNicQdiscQueueCount(t *testing.T) {
	tests := []struct {
		name     string
		config   map[string]string
		expected int
	}{
		{
			name:     "No CPU limit uses the VM default, floored at two queues",
			config:   map[string]string{},
			expected: 2,
		},
		{
			name:     "A single CPU is still floored at two queues",
			config:   map[string]string{"limits.cpu": "1"},
			expected: 2,
		},
		{
			name:     "A CPU count",
			config:   map[string]string{"limits.cpu": "8"},
			expected: 8,
		},
		{
			name:     "A CPU set",
			config:   map[string]string{"limits.cpu": "0-3,7"},
			expected: 5,
		},
		{
			name:     "An explicit topology",
			config:   map[string]string{"limits.cpu": "sockets=2,cores=2,threads=2"},
			expected: 8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count, err := nicQdiscQueueCount(tt.config)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, count)
		})
	}
}
