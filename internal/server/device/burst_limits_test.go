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

func TestNicParseLimits(t *testing.T) {
	tests := []struct {
		name     string
		config   deviceConfig.Device
		expected nicLimits
		err      string
	}{
		{
			name:     "No limits",
			config:   deviceConfig.Device{},
			expected: nicLimits{},
		},
		{
			name:     "Sustained limits only",
			config:   deviceConfig.Device{"limits.ingress": "1Mbit", "limits.egress": "2Mbit"},
			expected: nicLimits{ingress: 1000000, egress: 2000000},
		},
		{
			name: "Burst rate and bucket",
			config: deviceConfig.Device{
				"limits.ingress":        "1Mbit",
				"limits.ingress.burst":  "10Mbit",
				"limits.ingress.bucket": "5Mbit",
			},
			expected: nicLimits{
				ingress:       1000000,
				ingressBurst:  10000000,
				ingressBucket: 5000000,
			},
		},
		{
			name: "Max applies to both directions",
			config: deviceConfig.Device{
				"limits.max":        "1Mbit",
				"limits.max.burst":  "10Mbit",
				"limits.max.bucket": "5Mbit",
			},
			expected: nicLimits{
				ingress:       1000000,
				egress:        1000000,
				ingressBurst:  10000000,
				egressBurst:   10000000,
				ingressBucket: 5000000,
				egressBucket:  5000000,
			},
		},
		{
			name: "Max overrides the per-direction keys",
			config: deviceConfig.Device{
				"limits.ingress":        "1Mbit",
				"limits.ingress.burst":  "10Mbit",
				"limits.ingress.bucket": "5Mbit",
				"limits.max":            "2Mbit",
				"limits.max.burst":      "20Mbit",
				"limits.max.bucket":     "10Mbit",
			},
			expected: nicLimits{
				ingress:       2000000,
				egress:        2000000,
				ingressBurst:  20000000,
				egressBurst:   20000000,
				ingressBucket: 10000000,
				egressBucket:  10000000,
			},
		},
		{
			name: "Buckets are independent of the burst rates",
			config: deviceConfig.Device{
				"limits.max":            "1Mbit",
				"limits.ingress.bucket": "5Mbit",
				"limits.egress.bucket":  "10Mbit",
			},
			expected: nicLimits{
				ingress:       1000000,
				egress:        1000000,
				ingressBucket: 5000000,
				egressBucket:  10000000,
			},
		},
		{
			name:   "IOPS values are rejected",
			config: deviceConfig.Device{"limits.ingress": "1Mbit", "limits.ingress.burst": "1000iops"},
			err:    "Invalid limits.ingress.burst value \"1000iops\": Unsupported suffix: iops",
		},
		{
			name:   "The max key is named in its own error",
			config: deviceConfig.Device{"limits.max.bucket": "foo"},
			err:    "Invalid limits.max.bucket value \"foo\": Invalid value: foo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limits, err := nicParseLimits(tt.config)
			if tt.err != "" {
				assert.EqualError(t, err, tt.err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, *limits)
		})
	}
}

func TestNicValidateBurstLimits(t *testing.T) {
	tests := []struct {
		name      string
		config    deviceConfig.Device
		burstRate bool
		err       string
	}{
		{
			name:      "No limits",
			config:    deviceConfig.Device{},
			burstRate: true,
		},
		{
			name:      "Sustained limits only",
			config:    deviceConfig.Device{"limits.ingress": "1Mbit"},
			burstRate: true,
		},
		{
			name: "Valid burst",
			config: deviceConfig.Device{
				"limits.ingress":        "1Mbit",
				"limits.ingress.burst":  "10Mbit",
				"limits.ingress.bucket": "5Mbit",
			},
			burstRate: true,
		},
		{
			name:      "Burst rate without a bucket",
			config:    deviceConfig.Device{"limits.ingress": "1Mbit", "limits.ingress.burst": "10Mbit"},
			burstRate: true,
			err:       "The ingress burst rate requires a matching burst bucket",
		},
		{
			name:      "Bucket without a burst rate",
			config:    deviceConfig.Device{"limits.egress": "1Mbit", "limits.egress.bucket": "5Mbit"},
			burstRate: true,
			err:       "The egress burst bucket requires a matching burst rate",
		},
		{
			name:      "Burst without a sustained limit",
			config:    deviceConfig.Device{"limits.ingress.burst": "10Mbit", "limits.ingress.bucket": "5Mbit"},
			burstRate: true,
			err:       "The ingress burst limit requires a matching sustained limit",
		},
		{
			name: "Burst rate below the sustained limit",
			config: deviceConfig.Device{
				"limits.egress":        "10Mbit",
				"limits.egress.burst":  "1Mbit",
				"limits.egress.bucket": "5Mbit",
			},
			burstRate: true,
			err:       "The egress burst rate must be higher than the sustained limit",
		},
		{
			name: "Bucket below the minimum",
			config: deviceConfig.Device{
				"limits.ingress":        "1Mbit",
				"limits.ingress.burst":  "10Mbit",
				"limits.ingress.bucket": "500bit",
			},
			burstRate: true,
			err:       "The ingress burst bucket must be at least 1000 bit",
		},
		{
			name: "Bucket above the maximum",
			config: deviceConfig.Device{
				"limits.ingress":        "1Mbit",
				"limits.ingress.burst":  "10Mbit",
				"limits.ingress.bucket": "40Gbit",
			},
			burstRate: true,
			err:       "The ingress burst bucket must be at most 34359738360 bit",
		},
		{
			name: "Burst rate above the maximum",
			config: deviceConfig.Device{
				"limits.ingress":        "1Mbit",
				"limits.ingress.burst":  "40Gbit",
				"limits.ingress.bucket": "5Mbit",
			},
			burstRate: true,
			err:       "The ingress burst rate must be at most 34359738360 bit/s",
		},
		{
			name: "Max applies to both directions",
			config: deviceConfig.Device{
				"limits.max":        "1Mbit",
				"limits.max.burst":  "10Mbit",
				"limits.max.bucket": "5Mbit",
			},
			burstRate: true,
		},
		{
			name: "Max burst only covering one direction",
			config: deviceConfig.Device{
				"limits.max":            "1Mbit",
				"limits.max.burst":      "10Mbit",
				"limits.ingress.bucket": "5Mbit",
			},
			burstRate: true,
			err:       "The egress burst rate requires a matching burst bucket",
		},
		{
			name:   "A bucket on its own is valid without a burst rate",
			config: deviceConfig.Device{"limits.ingress": "1Mbit", "limits.ingress.bucket": "5Mbit"},
		},
		{
			name:   "A bucket without a sustained limit",
			config: deviceConfig.Device{"limits.egress.bucket": "5Mbit"},
			err:    "The egress burst limit requires a matching sustained limit",
		},
		{
			name:   "A max bucket on its own is valid without a burst rate",
			config: deviceConfig.Device{"limits.max": "1Mbit", "limits.max.bucket": "5Mbit"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := nicValidateBurstLimits(tt.config, tt.burstRate)
			if tt.err == "" {
				assert.NoError(t, err)
				return
			}

			assert.EqualError(t, err, tt.err)
		})
	}
}
