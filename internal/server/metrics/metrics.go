package metrics

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/lxc/incus/v6/internal/server/auth"
)

// NewMetricSet returns a new MetricSet.
func NewMetricSet(labels map[string]string) *MetricSet {
	out := MetricSet{set: make(map[MetricType][]Sample)}

	if labels != nil {
		out.labels = labels
	} else {
		out.labels = make(map[string]string)
	}

	return &out
}

// FilterSamples filters the existing MetricSet using the given permission checker. Samples not containing "project" and
// "name" labels are skipped.
func (m *MetricSet) FilterSamples(permissionChecker func(object auth.Object) bool) {
	for metricType, samples := range m.set {
		allowedSamples := make([]Sample, 0, len(samples))
		for _, s := range samples {
			projectName := s.Labels["project"]
			instanceName := s.Labels["name"]

			if projectName == "" || instanceName == "" {
				if permissionChecker(auth.ObjectServer()) {
					allowedSamples = append(allowedSamples, s)
				}

				continue
			}

			if permissionChecker(auth.ObjectInstance(projectName, instanceName)) {
				allowedSamples = append(allowedSamples, s)
			}
		}

		m.set[metricType] = allowedSamples
	}
}

// AddSamples adds samples of the type metricType to the MetricSet.
func (m *MetricSet) AddSamples(metricType MetricType, samples ...Sample) {
	for i := 0; i < len(samples); i++ {
		// Add global labels to samples
		for labelName, labelValue := range m.labels {
			// Ensure we always have a valid Labels map
			if samples[i].Labels == nil {
				samples[i].Labels = make(map[string]string)
			}

			samples[i].Labels[labelName] = labelValue
		}
	}

	m.set[metricType] = append(m.set[metricType], samples...)
}

// Merge merges two MetricSets. Missing labels from m's samples are added to all samples in n.
func (m *MetricSet) Merge(metricSet *MetricSet) {
	if metricSet == nil {
		return
	}

	for metricType := range metricSet.set {
		for _, sample := range metricSet.set[metricType] {
			// Add missing labels from m.
			for k, v := range m.labels {
				_, ok := sample.Labels[k]
				if !ok {
					sample.Labels[k] = v
				}
			}

			m.set[metricType] = append(m.set[metricType], sample)
		}
	}
}

func (m *MetricSet) String() string {
	var out strings.Builder
	metricTypes := []MetricType{}

	// Sort output by metric type name
	for metricType := range m.set {
		metricTypes = append(metricTypes, metricType)
	}

	sort.SliceStable(metricTypes, func(i, j int) bool {
		return int(metricTypes[i]) < int(metricTypes[j])
	})

	for _, metricType := range metricTypes {
		// Add HELP message as specified by OpenMetrics
		_, err := out.WriteString(MetricHeaders[metricType] + "\n")
		if err != nil {
			return ""
		}

		metricTypeName := ""

		// ProcsTotal is a gauge according to the OpenMetrics spec as its value can decrease.
		if metricType == ProcsTotal || metricType == CPUs || metricType == GoGoroutines || metricType == GoHeapObjects {
			metricTypeName = "gauge"
		} else if strings.HasSuffix(MetricNames[metricType], "_total") || strings.HasSuffix(MetricNames[metricType], "_seconds") {
			metricTypeName = "counter"
		} else if strings.HasSuffix(MetricNames[metricType], "_bytes") {
			metricTypeName = "gauge"
		}

		// Add TYPE message as specified by OpenMetrics
		_, err = out.WriteString(fmt.Sprintf("# TYPE %s %s\n", MetricNames[metricType], metricTypeName))
		if err != nil {
			return ""
		}

		for _, sample := range m.set[metricType] {
			firstLabel := true
			labels := ""
			labelNames := []string{}

			// Add and sort labels if there are any
			for labelName := range sample.Labels {
				labelNames = append(labelNames, labelName)
			}

			sort.Strings(labelNames)

			for _, labelName := range labelNames {
				if !firstLabel {
					labels += ","
				}

				labels += fmt.Sprintf(`%s="%s"`, labelName, sample.Labels[labelName])
				firstLabel = false
			}

			valueStr := strconv.FormatFloat(sample.Value, 'g', -1, 64)

			if labels != "" {
				_, err = out.WriteString(fmt.Sprintf("%s{%s} %s\n", MetricNames[metricType], labels, valueStr))
			} else {
				_, err = out.WriteString(fmt.Sprintf("%s %s\n", MetricNames[metricType], valueStr))
			}

			if err != nil {
				return ""
			}
		}
	}

	_, err := out.WriteString("# EOF\n")
	if err != nil {
		return ""
	}

	return out.String()
}

// MetricSetFromAPI converts api.Metrics to a MetricSet, and returns it.
func MetricSetFromAPI(metrics *Metrics, labels map[string]string) (*MetricSet, error) {
	set := NewMetricSet(labels)

	// CPU stats
	for _, stats := range metrics.CPU {
		getLabels := func(mode string) map[string]string {
			labels := map[string]string{"mode": mode}
			cpu := ""

			if stats.CPU != "cpu" {
				_, _ = fmt.Sscanf(stats.CPU, "cpu%s", &cpu)
			}

			if cpu != "" {
				labels["cpu"] = cpu
			}

			return labels
		}

		set.AddSamples(CPUSecondsTotal,
			Sample{
				Value:  stats.SecondsIOWait,
				Labels: getLabels("iowait"),
			},
			Sample{
				Value:  stats.SecondsIRQ,
				Labels: getLabels("irq"),
			},
			Sample{
				Value:  stats.SecondsIdle,
				Labels: getLabels("idle"),
			},
			Sample{
				Value:  stats.SecondsNice,
				Labels: getLabels("nice"),
			},
			Sample{
				Value:  stats.SecondsSoftIRQ,
				Labels: getLabels("softirq"),
			},
			Sample{
				Value:  stats.SecondsSteal,
				Labels: getLabels("steal"),
			},
			Sample{
				Value:  stats.SecondsSystem,
				Labels: getLabels("system"),
			},
			Sample{
				Value:  stats.SecondsUser,
				Labels: getLabels("user"),
			},
		)
	}

	// CPUs
	set.AddSamples(CPUs, Sample{Value: float64(metrics.CPUs)})

	// Disk stats
	for _, stats := range metrics.Disk {
		labels := map[string]string{"device": stats.Device}

		set.AddSamples(DiskReadBytesTotal, Sample{Value: float64(stats.ReadBytes), Labels: labels})
		set.AddSamples(DiskReadsCompletedTotal, Sample{Value: float64(stats.ReadsCompleted), Labels: labels})
		set.AddSamples(DiskWritesCompletedTotal, Sample{Value: float64(stats.WritesCompleted), Labels: labels})
		set.AddSamples(DiskWrittenBytesTotal, Sample{Value: float64(stats.WrittenBytes), Labels: labels})
	}

	// Filesystem stats
	for _, stats := range metrics.Filesystem {
		labels := map[string]string{"device": stats.Device, "fstype": stats.FSType, "mountpoint": stats.Mountpoint}

		set.AddSamples(FilesystemAvailBytes, Sample{Value: float64(stats.AvailableBytes), Labels: labels})
		set.AddSamples(FilesystemFreeBytes, Sample{Value: float64(stats.FreeBytes), Labels: labels})
		set.AddSamples(FilesystemSizeBytes, Sample{Value: float64(stats.SizeBytes), Labels: labels})
	}

	// Memory stats
	set.AddSamples(MemoryActiveAnonBytes, Sample{Value: float64(metrics.Memory.ActiveAnonBytes)})
	set.AddSamples(MemoryActiveBytes, Sample{Value: float64(metrics.Memory.ActiveBytes)})
	set.AddSamples(MemoryActiveFileBytes, Sample{Value: float64(metrics.Memory.ActiveFileBytes)})
	set.AddSamples(MemoryCachedBytes, Sample{Value: float64(metrics.Memory.CachedBytes)})
	set.AddSamples(MemoryDirtyBytes, Sample{Value: float64(metrics.Memory.DirtyBytes)})
	set.AddSamples(MemoryHugePagesFreeBytes, Sample{Value: float64(metrics.Memory.HugepagesFreeBytes)})
	set.AddSamples(MemoryHugePagesTotalBytes, Sample{Value: float64(metrics.Memory.HugepagesTotalBytes)})
	set.AddSamples(MemoryInactiveAnonBytes, Sample{Value: float64(metrics.Memory.InactiveAnonBytes)})
	set.AddSamples(MemoryInactiveBytes, Sample{Value: float64(metrics.Memory.InactiveBytes)})
	set.AddSamples(MemoryInactiveFileBytes, Sample{Value: float64(metrics.Memory.InactiveFileBytes)})
	set.AddSamples(MemoryMappedBytes, Sample{Value: float64(metrics.Memory.MappedBytes)})
	set.AddSamples(MemoryMemAvailableBytes, Sample{Value: float64(metrics.Memory.MemAvailableBytes)})
	set.AddSamples(MemoryMemFreeBytes, Sample{Value: float64(metrics.Memory.MemFreeBytes)})
	set.AddSamples(MemoryMemTotalBytes, Sample{Value: float64(metrics.Memory.MemTotalBytes)})
	set.AddSamples(MemoryRSSBytes, Sample{Value: float64(metrics.Memory.RSSBytes)})
	set.AddSamples(MemoryShmemBytes, Sample{Value: float64(metrics.Memory.ShmemBytes)})
	set.AddSamples(MemorySwapBytes, Sample{Value: float64(metrics.Memory.SwapBytes)})
	set.AddSamples(MemoryUnevictableBytes, Sample{Value: float64(metrics.Memory.UnevictableBytes)})
	set.AddSamples(MemoryWritebackBytes, Sample{Value: float64(metrics.Memory.WritebackBytes)})
	set.AddSamples(MemoryOOMKillsTotal, Sample{Value: float64(metrics.Memory.OOMKills)})

	// Network stats
	for _, stats := range metrics.Network {
		labels := map[string]string{"device": stats.Device}

		set.AddSamples(NetworkReceiveBytesTotal, Sample{Value: float64(stats.ReceiveBytes), Labels: labels})
		set.AddSamples(NetworkReceiveDropTotal, Sample{Value: float64(stats.ReceiveDrop), Labels: labels})
		set.AddSamples(NetworkReceiveErrsTotal, Sample{Value: float64(stats.ReceiveErrors), Labels: labels})
		set.AddSamples(NetworkReceivePacketsTotal, Sample{Value: float64(stats.ReceivePackets), Labels: labels})
		set.AddSamples(NetworkTransmitBytesTotal, Sample{Value: float64(stats.TransmitBytes), Labels: labels})
		set.AddSamples(NetworkTransmitDropTotal, Sample{Value: float64(stats.TransmitDrop), Labels: labels})
		set.AddSamples(NetworkTransmitErrsTotal, Sample{Value: float64(stats.TransmitErrors), Labels: labels})
		set.AddSamples(NetworkTransmitPacketsTotal, Sample{Value: float64(stats.TransmitPackets), Labels: labels})
	}

	// Procs stats
	set.AddSamples(ProcsTotal, Sample{Value: float64(metrics.ProcessesTotal)})

	return set, nil
}
