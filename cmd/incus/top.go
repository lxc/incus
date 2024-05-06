package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/client"
	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/units"
)

type cmdTop struct {
	global *cmdGlobal
}

// Command is a method of the cmdTop structure that returns a new cobra Command for displaying resource usage per instance.
func (c *cmdTop) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("top")
	cmd.Short = i18n.G("Display resource usage info per instance")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Displays CPU usage, memory usage, and disk usage per instance`))

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.RunE = c.Run
	return cmd
}

// Run is a method of the cmdTop structure. It implements the logic to call `incus top`.
// This function implements the `top` command. It queries the metrics API at (/1.0/metrics) and renders a list of
// instances with their CPU, memory and disk usage columns.
func (c *cmdTop) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	remoteInput := ""
	if len(args) > 0 {
		remoteInput = args[0]
	}

	remote, _, err := conf.ParseRemote(remoteInput)
	if err != nil {
		return err
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	// These variables can be changed by the UI
	refreshInterval := 5 * time.Second // default 5 seconds, could change this to a flag
	sortingMethod := alphabetical      // default is alphabetical, could change this to a flag

	// Start the ticker for periodic updates
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()

	// Call the update once before the loop
	err = c.updateDisplay(d, refreshInterval, sortingMethod)
	if err != nil {
		return err
	}

	durationChannel := make(chan time.Duration)
	sortingChannel := make(chan sortType)
	interruptChannel := make(chan bool)

	go handleKeystrokes(durationChannel, interruptChannel, sortingChannel) // Handles shortcuts on a separate Goroutine

	for {
		select {
		case shouldStop := <-interruptChannel: // This pauses the UI refresh loop
			if shouldStop {
				ticker.Stop()
			} else {
				err = c.updateDisplay(d, refreshInterval, sortingMethod)
				if err != nil {
					return err
				}

				ticker = time.NewTicker(refreshInterval)
			}

		case <-ticker.C:
			err = c.updateDisplay(d, refreshInterval, sortingMethod)
			if err != nil {
				return err
			}

		case sortType, ok := <-sortingChannel:
			if !ok {
				return nil // Exits if the channel is closed
			}

			sortingMethod = sortType

		case duration, ok := <-durationChannel:
			if !ok {
				return nil // Exits if the channel is closed
			}

			ticker.Stop()
			ticker = time.NewTicker(duration)
			refreshInterval = duration
			fmt.Printf(i18n.G("Updated interval to %v")+"\n", duration)

			// Update display
			err = c.updateDisplay(d, refreshInterval, sortingMethod)
			if err != nil {
				return err
			}
		}
	}
}

func handleKeystrokes(durationChannel chan time.Duration, interruptChannel chan bool, sortingChannel chan sortType) {
	reader := bufio.NewReader(os.Stdin)

	for {
		input, err := reader.ReadString('\n')
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading from stdin: %v", err)
			return
		}

		input = input[:len(input)-1] // Strip newline character
		if input == "d" {
			interruptChannel <- true
			fmt.Print(i18n.G("Enter new delay in seconds:") + " ")

			delayInput, err := reader.ReadString('\n')
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error reading new delay: %v", err)
				return
			}

			delayInput = delayInput[:len(delayInput)-1] // Strip newline character
			delaySec, err := strconv.ParseFloat(delayInput, 64)
			if err != nil || delaySec <= 0 {
				fmt.Println(i18n.G("Invalid input, please enter a positive number"))
				continue
			}

			// Send new duration back to the channel
			durationChannel <- time.Duration(delaySec * float64(time.Second))
		} else if input == "s" {
			interruptChannel <- true
			fmt.Print(i18n.G("Enter a sorting type ('a' for alphabetical, 'c' for CPU, 'm' for memory, 'd' for disk):") + " ")

			sortingInput, err := reader.ReadString('\n')
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error reading sorting type: %v", err)
				return
			}

			sortingInput = sortingInput[:len(sortingInput)-1] // Strip newline character

			// Send sorting type over sorting channel
			switch sortingInput {
			case "a":
				sortingChannel <- alphabetical
			case "c":
				sortingChannel <- cpuUsage
			case "m":
				sortingChannel <- memoryUsage
			case "d":
				sortingChannel <- diskUsage
			default:
				fmt.Println(i18n.G("Invalid sorting type provided"))
			}

			interruptChannel <- false
		}
	}
}

type sortType string

const (
	alphabetical sortType = "Alphabetical"
	cpuUsage     sortType = "CPU Usage"
	memoryUsage  sortType = "Memory Usage"
	diskUsage    sortType = "Disk Usage"
)

type displayData struct {
	instanceName string
	cpuUsage     float64
	memoryUsage  float64
	diskUsage    float64
}

func (dd *displayData) toStringArray() []string {
	dataStringified := [4]string{
		dd.instanceName,
		fmt.Sprintf("%.2f", dd.cpuUsage),
		units.GetByteSizeStringIEC(int64(dd.memoryUsage), 2),
		units.GetByteSizeStringIEC(int64(dd.diskUsage), 2),
	}

	return dataStringified[:]
}

func sortBySortingType(data []displayData, sortingType sortType) {
	sortFuncs := map[sortType]func(i, j int) bool{
		alphabetical: func(i, j int) bool {
			return data[i].instanceName < data[j].instanceName
		},
		cpuUsage: func(i, j int) bool {
			return data[i].cpuUsage > data[j].cpuUsage
		},
		memoryUsage: func(i, j int) bool {
			return data[i].memoryUsage > data[j].memoryUsage
		},
		diskUsage: func(i, j int) bool {
			return data[i].diskUsage > data[j].diskUsage
		},
	}

	sortFunc, ok := sortFuncs[sortingType]
	if ok {
		sort.Slice(data, sortFunc)
	} else {
		fmt.Println(i18n.G("Invalid sorting type"))
	}
}

func (c *cmdTop) updateDisplay(d incus.InstanceServer, refreshInterval time.Duration, sortingType sortType) error {
	rawLogs, err := d.GetMetrics()
	if err != nil {
		return err
	}

	metricSet, names, err := parseMetricsFromString(rawLogs)
	if err != nil {
		return err
	}

	namesLen := len(names)
	data := make([]displayData, namesLen)
	for i := 0; i < namesLen; i++ {
		currentName := names[i]

		cpuSeconds := metricSet.getMetricValue(cpuSecondsTotal, currentName)

		memoryFree := metricSet.getMetricValue(memoryMemAvailableBytes, currentName)
		memoryTotal := metricSet.getMetricValue(memoryMemTotalBytes, currentName)

		diskTotal := metricSet.getMetricValue(filesystemSizeBytes, currentName)
		diskFree := metricSet.getMetricValue(filesystemFreeBytes, currentName)

		data[i] = displayData{
			instanceName: currentName,
			cpuUsage:     cpuSeconds,
			memoryUsage:  memoryTotal - memoryFree,
			diskUsage:    diskTotal - diskFree,
		}
	}

	// Perform sort operation
	sortBySortingType(data, sortingType)

	dataFormatted := make([][]string, namesLen)
	for i := 0; i < namesLen; i++ { // Convert the arrays to a string representation
		dataFormatted[i] = data[i].toStringArray()
	}

	headers := []string{i18n.G("INSTANCE NAME"), i18n.G("CPU TIME(s)"), i18n.G("MEMORY"), i18n.G("DISK")}

	fmt.Print("\033[H\033[2J") // Clear the terminal on each tick
	err = cli.RenderTable("table", headers, dataFormatted, nil)
	if err != nil {
		return err
	}

	fmt.Println(i18n.G("Press 'd' + ENTER to change delay"))
	fmt.Println(i18n.G("Press 's' + ENTER to change sorting method"))
	fmt.Println(i18n.G("Press CTRL-C to exit"))
	fmt.Println()
	fmt.Println(i18n.G("Delay:"), refreshInterval)
	fmt.Println(i18n.G("Sorting Method:"), sortingType)

	return nil
}

type sample struct {
	labels map[string]string
	value  float64
}

type metricType int

type metricSet struct {
	set    map[metricType][]sample
	labels map[string]string
}

const (
	// CPUSecondsTotal represents the total CPU seconds used.
	cpuSecondsTotal metricType = iota
	// FilesystemAvailBytes represents the available bytes on a filesystem.
	filesystemFreeBytes
	// FilesystemSizeBytes represents the size in bytes of a filesystem.
	filesystemSizeBytes
	// MemoryMemAvailableBytes represents the amount of available memory.
	memoryMemAvailableBytes
	// MemoryMemTotalBytes represents the amount of used memory.
	memoryMemTotalBytes
)

// MetricNames associates a metric type to its name.
var metricNames = map[metricType]string{
	cpuSecondsTotal:         "incus_cpu_seconds_total",
	filesystemFreeBytes:     "incus_filesystem_free_bytes",
	filesystemSizeBytes:     "incus_filesystem_size_bytes",
	memoryMemAvailableBytes: "incus_memory_MemAvailable_bytes",
	memoryMemTotalBytes:     "incus_memory_MemTotal_bytes",
}

func (ms *metricSet) getMetricValue(metricType metricType, instanceName string) float64 {
	value := 0.0

	if samples, exists := ms.set[metricType]; exists { // Check if metricType exists
		for _, sample := range samples {
			if sample.labels["name"] == instanceName {
				value += sample.value
			}
		}
	}

	return value
}

// ParseMetricsFromString parses OpenMetrics formatted logs from a string and converts them to a MetricSet.
func parseMetricsFromString(input string) (*metricSet, []string, error) {
	scanner := bufio.NewScanner(strings.NewReader(input))
	metricSet := &metricSet{
		set:    make(map[metricType][]sample),
		labels: make(map[string]string),
	}

	metricLineRegex := regexp.MustCompile(`^(\w+)\{(.+)\}\s+([\d\.]+e[+-]?\d+|[\d\.]+)$`)

	for scanner.Scan() {
		line := scanner.Text()
		matches := metricLineRegex.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		metricName, labelPart, valueStr := matches[1], matches[2], matches[3]
		value, err := strconv.ParseFloat(valueStr, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("Invalid metric value: %v", err)
		}

		metricType, found := findMetricTypeByName(metricName)
		if !found {
			continue
		}

		labels := parseLabels(labelPart)
		sample := sample{
			labels: labels,
			value:  value,
		}

		metricSet.set[metricType] = append(metricSet.set[metricType], sample)
	}

	err := scanner.Err()
	if err != nil {
		return nil, nil, err
	}

	names := []string{}
	if samples, exists := metricSet.set[memoryMemTotalBytes]; exists { // Use a known metric type to gather names
		for _, sample := range samples {
			names = append(names, sample.labels["name"])
		}
	}

	return metricSet, names, nil
}

func parseLabels(input string) map[string]string {
	labels := make(map[string]string)
	for _, pair := range strings.Split(input, ",") {
		kv := strings.Split(pair, "=")
		if len(kv) != 2 {
			continue
		}

		key := strings.TrimSpace(kv[0])
		value := strings.Trim(kv[1], "\"")
		labels[key] = value
	}

	return labels
}

func findMetricTypeByName(name string) (metricType, bool) {
	for typ, typName := range metricNames {
		if typName == name {
			return typ, true
		}
	}

	return 0, false
}
