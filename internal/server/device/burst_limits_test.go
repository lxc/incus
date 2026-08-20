package device

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	deviceConfig "github.com/lxc/incus/v7/internal/server/device/config"
)

func TestDiskParseLimits(t *testing.T) {
	tests := []struct {
		name     string
		config   deviceConfig.Device
		expected diskBlockLimit
	}{
		{
			name:     "No limits",
			config:   deviceConfig.Device{},
			expected: diskBlockLimit{},
		},
		{
			name:   "Sustained limits only",
			config: deviceConfig.Device{"limits.read": "10MiB", "limits.write": "1000iops"},
			expected: diskBlockLimit{
				readBps:   10 * 1024 * 1024,
				writeIops: 1000,
			},
		},
		{
			name:   "Burst defaults to a one second length",
			config: deviceConfig.Device{"limits.read": "10MiB", "limits.read.burst": "20MiB"},
			expected: diskBlockLimit{
				readBps:         10 * 1024 * 1024,
				readBpsBurst:    20 * 1024 * 1024,
				readBurstLength: 1,
			},
		},
		{
			name: "Explicit burst length",
			config: deviceConfig.Device{
				"limits.read":              "10MiB",
				"limits.read.burst":        "20MiB",
				"limits.read.burst.length": "30s",
			},
			expected: diskBlockLimit{
				readBps:         10 * 1024 * 1024,
				readBpsBurst:    20 * 1024 * 1024,
				readBurstLength: 30,
			},
		},
		{
			name: "Per-direction burst lengths",
			config: deviceConfig.Device{
				"limits.max":                "10MiB",
				"limits.max.burst":          "20MiB",
				"limits.read.burst.length":  "30s",
				"limits.write.burst.length": "5s",
			},
			expected: diskBlockLimit{
				readBps:          10 * 1024 * 1024,
				writeBps:         10 * 1024 * 1024,
				readBpsBurst:     20 * 1024 * 1024,
				writeBpsBurst:    20 * 1024 * 1024,
				readBurstLength:  30,
				writeBurstLength: 5,
			},
		},
		{
			name: "Max applies to both directions",
			config: deviceConfig.Device{
				"limits.max":       "10MiB,1000iops",
				"limits.max.burst": "20MiB,2000iops",
			},
			expected: diskBlockLimit{
				readBps:          10 * 1024 * 1024,
				readIops:         1000,
				writeBps:         10 * 1024 * 1024,
				writeIops:        1000,
				readBpsBurst:     20 * 1024 * 1024,
				readIopsBurst:    2000,
				writeBpsBurst:    20 * 1024 * 1024,
				writeIopsBurst:   2000,
				readBurstLength:  1,
				writeBurstLength: 1,
			},
		},
		{
			name: "Max overrides the per-direction keys",
			config: deviceConfig.Device{
				"limits.read":              "1MiB",
				"limits.read.burst":        "2MiB",
				"limits.read.burst.length": "10s",
				"limits.max":               "10MiB",
				"limits.max.burst":         "20MiB",
				"limits.max.burst.length":  "30s",
			},
			expected: diskBlockLimit{
				readBps:          10 * 1024 * 1024,
				writeBps:         10 * 1024 * 1024,
				readBpsBurst:     20 * 1024 * 1024,
				writeBpsBurst:    20 * 1024 * 1024,
				readBurstLength:  30,
				writeBurstLength: 30,
			},
		},
		{
			name: "A burst length only applies to a bursting direction",
			config: deviceConfig.Device{
				"limits.max":                "10MiB",
				"limits.read.burst":         "20MiB",
				"limits.read.burst.length":  "30s",
				"limits.write.burst.length": "30s",
			},
			expected: diskBlockLimit{
				readBps:         10 * 1024 * 1024,
				writeBps:        10 * 1024 * 1024,
				readBpsBurst:    20 * 1024 * 1024,
				readBurstLength: 30,
			},
		},
		{
			name: "A burst length without a burst limit is ignored",
			config: deviceConfig.Device{
				"limits.read":              "10MiB",
				"limits.read.burst.length": "30s",
			},
			expected: diskBlockLimit{
				readBps: 10 * 1024 * 1024,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limits, err := diskParseLimits(tt.config)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, *limits)
		})
	}
}

func TestDiskValidateBurstLimits(t *testing.T) {
	tests := []struct {
		name   string
		config deviceConfig.Device
		err    string
	}{
		{
			name:   "No limits",
			config: deviceConfig.Device{},
		},
		{
			name:   "Sustained limits only",
			config: deviceConfig.Device{"limits.read": "10MiB"},
		},
		{
			name:   "Valid burst",
			config: deviceConfig.Device{"limits.read": "10MiB", "limits.read.burst": "20MiB"},
		},
		{
			name:   "Burst equal to the sustained limit",
			config: deviceConfig.Device{"limits.read": "10MiB", "limits.read.burst": "10MiB"},
		},
		{
			name:   "Burst without a sustained limit",
			config: deviceConfig.Device{"limits.read.burst": "20MiB"},
			err:    "A read byte/s burst limit requires a matching sustained limit",
		},
		{
			name:   "Burst below the sustained limit",
			config: deviceConfig.Device{"limits.write": "10MiB", "limits.write.burst": "5MiB"},
			err:    "The write byte/s burst limit must be higher than the sustained limit",
		},
		{
			name:   "IOPS burst without an IOPS sustained limit",
			config: deviceConfig.Device{"limits.read": "10MiB", "limits.read.burst": "20MiB,2000iops"},
			err:    "A read IOPS burst limit requires a matching sustained limit",
		},
		{
			name:   "Burst length without a burst limit",
			config: deviceConfig.Device{"limits.read": "10MiB", "limits.read.burst.length": "30s"},
			err:    "limits.read.burst.length requires a read burst I/O limit to be set",
		},
		{
			name:   "Burst length in the other direction",
			config: deviceConfig.Device{"limits.max": "10MiB", "limits.read.burst": "20MiB", "limits.write.burst.length": "30s"},
			err:    "limits.write.burst.length requires a write burst I/O limit to be set",
		},
		{
			name:   "Max burst length without a burst limit",
			config: deviceConfig.Device{"limits.read": "10MiB", "limits.max.burst.length": "30s"},
			err:    "limits.max.burst.length requires a burst I/O limit to be set",
		},
		{
			name:   "Max burst length with a single direction bursting",
			config: deviceConfig.Device{"limits.max": "10MiB", "limits.read.burst": "20MiB", "limits.max.burst.length": "30s"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := diskValidateBurstLimits(tt.config)
			if tt.err == "" {
				assert.NoError(t, err)
				return
			}

			assert.EqualError(t, err, tt.err)
		})
	}
}
