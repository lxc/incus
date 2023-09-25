package metrics

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/lxc/incus/internal/server/auth"
)

func TestMetricSet_FilterSamples(t *testing.T) {
	labels := map[string]string{"project": "default", "name": "jammy"}
	newMetricSet := func() *MetricSet {
		m := NewMetricSet(labels)
		require.Equal(t, labels, m.labels)
		m.AddSamples(CPUSecondsTotal, Sample{Value: 10})
		require.Equal(t, []Sample{{Value: 10, Labels: labels}}, m.set[CPUSecondsTotal])
		return m
	}

	m := newMetricSet()
	permissionChecker := func(object auth.Object) bool {
		return object == auth.ObjectInstance("default", "jammy")
	}

	m.FilterSamples(permissionChecker)

	// Should still contain the sample
	require.Equal(t, []Sample{{Value: 10, Labels: labels}}, m.set[CPUSecondsTotal])

	m = newMetricSet()
	permissionChecker = func(object auth.Object) bool {
		return object == auth.ObjectInstance("not-default", "not-jammy")
	}

	m.FilterSamples(permissionChecker)

	// Should no longer contain the sample.
	require.Equal(t, []Sample{}, m.set[CPUSecondsTotal])
}
