package systemd_report

import "context"

// SocketPath is the systemd-report source for local Incus health metrics.
const SocketPath = "/run/systemd/report/org.linuxcontainers.Incus"

const metricPrefix = "org.linuxcontainers.Incus."

type metricFamily struct {
	name        string
	description string
}

var metricFamilies = []metricFamily{
	{metricPrefix + "databaseReadAvailable", "Whether a read-only Incus database query succeeded"},
	{metricPrefix + "instanceTypeAvailable", "Whether an Incus instance type passed its cached support probe"},
	{metricPrefix + "unresolvedWarnings", "Number of new or acknowledged warning records associated with this Incus node"},
}

// Snapshot contains the node health observations published through systemd-report.
type Snapshot struct {
	DatabaseReadAvailable bool
	WarningsAvailable     bool
	UnresolvedWarnings    int
	InstanceTypes         map[string]bool
}

// SnapshotFunc collects the node health observations for a single systemd-report request.
type SnapshotFunc func(context.Context) (Snapshot, error)

type sample struct {
	Name   string            `json:"name"`
	Fields map[string]string `json:"fields,omitempty"`
	Value  float64           `json:"value"`
}

func (s Snapshot) samples() []sample {
	samples := []sample{{Name: metricFamilies[0].name, Value: boolValue(s.DatabaseReadAvailable)}}
	for _, instanceType := range []string{"container", "virtual-machine"} {
		available, ok := s.InstanceTypes[instanceType]
		if ok {
			samples = append(samples, sample{Name: metricFamilies[1].name, Fields: map[string]string{"type": instanceType}, Value: boolValue(available)})
		}
	}

	if s.WarningsAvailable {
		samples = append(samples, sample{Name: metricFamilies[2].name, Value: float64(s.UnresolvedWarnings)})
	}

	return samples
}

func boolValue(value bool) float64 {
	if value {
		return 1
	}

	return 0
}
