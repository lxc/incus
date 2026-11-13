package main

import (
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/cmd/incus/color"
	u "github.com/lxc/incus/v7/cmd/incus/usage"
	"github.com/lxc/incus/v7/internal/i18n"
	"github.com/lxc/incus/v7/internal/instance"
	"github.com/lxc/incus/v7/shared/api"
	cli "github.com/lxc/incus/v7/shared/cmd"
	"github.com/lxc/incus/v7/shared/units"
	"github.com/lxc/incus/v7/shared/util"
)

// instanceWithRemote associates an instance with its remote for JSON/YAML output.
type instanceWithRemote struct {
	api.InstanceFull `yaml:",inline"`

	Remote string `json:"remote" yaml:"remote"`
}

type cmdList struct {
	global *cmdGlobal
	lister *listable[instanceWithRemote]

	flagFast        bool
	flagAllProjects bool
	flagAllRemotes  bool
	rawFormat       bool

	shorthandFilters map[string]func(*api.Instance, *api.InstanceState, string) bool
}

var cmdListUsage = u.Usage{u.Either(u.RemoteColonOpt, u.Flag("all-remotes")), u.Filter.List(0)}

func (c *cmdList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List instances")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`List instances

A single keyword like "web" which will list any instance with a name starting by "web".
A regular expression on the instance name. (e.g. .*web.*01$).
A key/value pair referring to a configuration item. For those, the
namespace can be abbreviated to the smallest unambiguous identifier.
A key/value pair where the key is a shorthand. Multiple values must be delimited by ','. Available shorthands:
  - type={instance type}
  - status={instance current lifecycle status}
  - architecture={instance architecture}
  - location={location name}
  - ipv4={ip or CIDR}
  - ipv6={ip or CIDR}

Examples:
  - "user.blah=abc" will list all instances with the "blah" user property set to "abc".
  - "u.blah=abc" will do the same
  - "security.privileged=true" will list all privileged instances
  - "s.privileged=true" will do the same
  - "type=container" will list all container instances
  - "type=container status=running" will list all running container instances

A regular expression matching a configuration item or its value. (e.g. volatile.eth0.hwaddr=10:66:6a:.*).

When multiple filters are passed, they are added one on top of the other,
selecting instances which satisfy them all.`))

	cmd.Example = cli.FormatSection("", i18n.G(
		`incus list -c nFs46,volatile.eth0.hwaddr:MAC,config:image.os,devices:eth0.parent:ETHP
  Show instances using the "NAME", "BASE IMAGE", "STATE", "IPV4", "IPV6" and "MAC" columns.
  "BASE IMAGE", "MAC" and "IMAGE OS" are custom columns generated from instance configuration keys.
  "ETHP" is a custom column generated from a device key.

incus list -c ns,user.comment:comment
  List instances with their running state and user comment.`,
	))

	c.lister.init(cmd, "ns46tS", map[rune]listColumn[instanceWithRemote]{
		'4': {i18n.G("IPV4"), i18n.G("IPv4 address"), c.ip4ColumnData},
		'6': {i18n.G("IPV6"), i18n.G("IPv6 address"), c.ip6ColumnData},
		'a': {i18n.G("ARCHITECTURE"), i18n.G("Architecture"), c.architectureColumnData},
		'b': {i18n.G("STORAGE POOL"), i18n.G("Storage pool"), c.storagePoolColumnData},
		'c': {i18n.G("CREATED"), i18n.G("Creation date"), c.createdColumnData},
		'd': {i18n.G("DESCRIPTION"), i18n.G("Description"), c.descriptionColumnData},
		'D': {i18n.G("DISK USAGE"), i18n.G("Disk usage"), c.diskUsageColumnData},
		'e': {i18n.G("PROJECT"), i18n.G("Project"), c.projectColumnData},
		'f': {i18n.G("BASE IMAGE"), i18n.G("Base image fingerprint (short)"), c.baseImageColumnData},
		'F': {i18n.G("BASE IMAGE"), i18n.G("Base image fingerprint (long)"), c.baseImageFullColumnData},
		'l': {i18n.G("LAST USED"), i18n.G("Last used date"), c.lastUsedColumnData},
		'm': {i18n.G("MEM USAGE"), i18n.G("Memory usage"), c.memoryUsageColumnData},
		'M': {i18n.G("MEM USAGE%"), i18n.G("Memory usage (%)"), c.memoryUsagePercentColumnData},
		'n': {i18n.G("NAME"), i18n.G("Name"), c.nameColumnData},
		'N': {i18n.G("PROCESSES"), i18n.G("Number of running processes"), c.numberOfProcessesColumnData},
		'p': {i18n.G("PID"), i18n.G("Init process PID"), c.pidColumnData},
		'P': {i18n.G("PROFILES"), i18n.G("Profiles"), c.profilesColumnData},
		'R': {i18n.G("REMOTE"), i18n.G("Remote name"), c.remoteColumnData},
		's': {i18n.G("STATE"), i18n.G("State"), c.statusColumnData},
		'S': {i18n.G("SNAPSHOTS"), i18n.G("Number of snapshots"), c.numberSnapshotsColumnData},
		't': {i18n.G("TYPE"), i18n.G("Type"), c.typeColumnData},
		'u': {i18n.G("CPU USAGE"), i18n.G("CPU usage (in seconds)"), c.cpuUsageSecondsColumnData},
		'U': {i18n.G("STARTED"), i18n.G("Start date"), c.startedColumnData},
		'L': {i18n.G("LOCATION"), i18n.G("Location (cluster member)"), c.locationColumnData},
	}, projectToggle, remoteToggle, locationToggle)

	c.lister.addHook(instanceListHook, i18n.G(`Custom columns can be defined with "[config:|devices:]<key>[:<name>][:<width>]":
    <key>:   Extended configuration or device key to display (if [config:|devices:] is omitted, it defaults to config:)
    <name>:  Name to display in the column header (defaults to <key> if not specified or empty)
    <width>: Max column width (longer results are truncated), 0 = limit to header width, -1 = unlimited (defaults to -1)`))

	// Column adjustments need to be performed pre-parsing.
	c.lister.preCheck(func() error {
		if c.flagFast {
			if c.lister.columnsSpecified() {
				return errors.New(i18n.G("Can't specify --fast with --columns"))
			}

			return c.lister.adjustDefaultColumns("nsacPt")
		}

		return nil
	})

	cmd.RunE = c.run
	cli.AddBoolFlag(cmd.Flags(), &c.flagFast, "fast", i18n.G("Fast mode (same as --columns=nsacPt)"))
	cli.AddBoolFlag(cmd.Flags(), &c.flagAllProjects, "all-projects", i18n.G("Display instances from all projects"))
	cli.AddBoolFlag(cmd.Flags(), &c.flagAllRemotes, "all-remotes", i18n.G("Display instances from all remotes"))

	// This subcommand supports setting “,raw”.
	cmd.Flags().Lookup("format").Usage = i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable them if missing, and suffix ",raw" to skip formatting numeric units, e.g. csv,header,raw`)

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

const (
	configColumnType = "config"
	deviceColumnType = "devices"
	fullColumns      = "46DmMNpSuU"
	stateColumns     = "46DmMNpuU"
	snapshotColumns  = "S"
)

func (c *cmdList) shouldShow(filters []string, inst *api.Instance, state *api.InstanceState) bool {
	c.mapShorthandFilters()

	for _, filter := range filters {
		membs := strings.SplitN(filter, "=", 2)

		key := membs[0]
		var value string
		if len(membs) < 2 {
			value = ""
		} else {
			value = membs[1]
		}

		if c.evaluateShorthandFilter(key, value, inst, state) {
			continue
		}

		return false
	}

	return true
}

func (c *cmdList) evaluateShorthandFilter(key string, value string, inst *api.Instance, state *api.InstanceState) bool {
	const shorthandValueDelimiter = ","
	shorthandFilterFunction, isShorthandFilter := c.shorthandFilters[strings.ToLower(key)]

	if !isShorthandFilter {
		return false
	}

	if !strings.Contains(value, shorthandValueDelimiter) {
		return shorthandFilterFunction(inst, state, value)
	}

	matched := false
	for curValue := range strings.SplitSeq(value, shorthandValueDelimiter) {
		if shorthandFilterFunction(inst, state, curValue) {
			matched = true
		}
	}

	return matched
}

// fetchInstancesData retrieves any state and snapshot data needed by the columns.
func (c *cmdList) fetchInstancesData(d incus.InstanceServer, instances []api.Instance) []api.InstanceFull {
	threads := min(len(instances), 10)

	hasSnapshots := c.lister.hasAnyColumn(snapshotColumns)
	hasState := c.lister.hasAnyColumn(stateColumns)

	// Shortcut when needing both state and snapshot info.
	if hasSnapshots && hasState {
		cInfo := []api.InstanceFull{}
		cInfoLock := sync.Mutex{}
		cInfoQueue := make(chan string, threads)
		cInfoWg := sync.WaitGroup{}

		for range threads {
			cInfoWg.Go(func() {
				for {
					cName, more := <-cInfoQueue
					if !more {
						break
					}

					state, _, err := d.GetInstanceFull(cName)
					if err != nil {
						continue
					}

					cInfoLock.Lock()
					cInfo = append(cInfo, *state)
					cInfoLock.Unlock()
				}
			})
		}

		for _, info := range instances {
			cInfoQueue <- info.Name
		}

		close(cInfoQueue)
		cInfoWg.Wait()

		return cInfo
	}

	cStates := map[string]*api.InstanceState{}
	cStatesLock := sync.Mutex{}
	cStatesQueue := make(chan string, threads)
	cStatesWg := sync.WaitGroup{}

	cSnapshots := map[string][]api.InstanceSnapshot{}
	cSnapshotsLock := sync.Mutex{}
	cSnapshotsQueue := make(chan string, threads)
	cSnapshotsWg := sync.WaitGroup{}

	for range threads {
		cStatesWg.Go(func() {
			for {
				cName, more := <-cStatesQueue
				if !more {
					break
				}

				state, _, err := d.GetInstanceState(cName)
				if err != nil {
					continue
				}

				cStatesLock.Lock()
				cStates[cName] = state
				cStatesLock.Unlock()
			}
		})

		cSnapshotsWg.Go(func() {
			for {
				cName, more := <-cSnapshotsQueue
				if !more {
					break
				}

				snaps, err := d.GetInstanceSnapshots(cName)
				if err != nil {
					continue
				}

				cSnapshotsLock.Lock()
				cSnapshots[cName] = snaps
				cSnapshotsLock.Unlock()
			}
		})
	}

	for _, inst := range instances {
		if hasState && inst.IsActive() {
			cStatesQueue <- inst.Name
		}

		if hasSnapshots {
			cSnapshotsQueue <- inst.Name
		}
	}

	close(cStatesQueue)
	close(cSnapshotsQueue)
	cStatesWg.Wait()
	cSnapshotsWg.Wait()

	// Convert to Instance
	data := make([]api.InstanceFull, len(instances))
	for i := range instances {
		data[i].Instance = instances[i]
		data[i].State = cStates[instances[i].Name]
		data[i].Snapshots = cSnapshots[instances[i].Name]
	}

	return data
}

// getInstances retrieves the instances from the given server along with any data needed by the columns.
func (c *cmdList) getInstances(d incus.InstanceServer, filters []string, needsData bool) ([]api.InstanceFull, []string, error) {
	serverFilters, clientFilters := getServerSupportedFilters(filters, []string{"ipv4", "ipv6"}, true)

	if needsData && d.HasExtension("container_full") {
		// Using the GetInstancesFull shortcut
		var instances []api.InstanceFull
		var err error

		serverFilters = prepareInstanceServerFilters(serverFilters, api.InstanceFull{})

		if c.flagAllProjects {
			instances, err = d.GetInstancesFullAllProjectsWithFilter(api.InstanceTypeAny, serverFilters)
		} else {
			instances, err = d.GetInstancesFullWithFilter(api.InstanceTypeAny, serverFilters)
		}

		if err != nil {
			return nil, nil, err
		}

		return instances, clientFilters, nil
	}

	// Get the list of instances
	var instances []api.Instance
	var err error

	serverFilters = prepareInstanceServerFilters(serverFilters, api.Instance{})

	if c.flagAllProjects {
		instances, err = d.GetInstancesAllProjectsWithFilter(api.InstanceTypeAny, serverFilters)
	} else {
		instances, err = d.GetInstancesWithFilter(api.InstanceTypeAny, serverFilters)
	}

	if err != nil {
		return nil, nil, err
	}

	// Fetch any remaining data
	return c.fetchInstancesData(d, instances), clientFilters, nil
}

func (c *cmdList) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdListUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	remoteName := parsed[0].RemoteName
	filters := parsed[1].StringList

	if c.flagAllRemotes && !parsed[0].Skipped {
		return errors.New(i18n.G("Can't specify a remote with --all-remotes"))
	}

	if c.global.flagProject != "" && c.flagAllProjects {
		return errors.New(i18n.G("Can't specify --project with --all-projects"))
	}

	err = c.lister.toggle(projectToggle, c.flagAllProjects)
	if err != nil {
		return err
	}

	err = c.lister.toggle(remoteToggle, c.flagAllRemotes)
	if err != nil {
		return err
	}

	err = c.lister.toggle(locationToggle, c.flagAllRemotes || d.IsClustered())
	if err != nil {
		return err
	}

	// Check for raw unit formatting.
	formatFields := strings.SplitN(c.lister.flagFormat, ",", 2)
	if len(formatFields) == 2 {
		formatOptions := strings.Split(formatFields[1], ",")
		if slices.Contains(formatOptions, "raw") {
			c.rawFormat = true
		}
	}

	// Get the list of servers to query.
	servers := map[string]incus.InstanceServer{}
	if c.flagAllRemotes {
		for name, remote := range c.global.conf.Remotes {
			if remote.Public || remote.Protocol != "incus" {
				continue
			}

			if name == remoteName {
				servers[name] = d
				continue
			}

			server, err := c.global.conf.GetInstanceServer(name)
			if err != nil {
				fmt.Fprintf(os.Stderr, i18n.G("Skipping remote %q: %v")+"\n", name, err)
				continue
			}

			servers[name] = server
		}
	} else {
		servers[remoteName] = d
	}

	rows := []instanceWithRemote{}
	for _, name := range slices.Sorted(maps.Keys(servers)) {
		instances, clientFilters, err := c.getInstances(servers[name], filters, c.lister.hasAnyColumn(fullColumns))
		if err != nil {
			if !c.flagAllRemotes {
				return err
			}

			fmt.Fprintf(os.Stderr, i18n.G("Skipping remote %q: %v")+"\n", name, err)
			continue
		}

		for _, inst := range instances {
			if !c.shouldShow(clientFilters, &inst.Instance, inst.State) {
				continue
			}

			rows = append(rows, instanceWithRemote{InstanceFull: inst, Remote: name})
		}
	}

	if len(rows) == 0 {
		u.HintMissingSpace(args, i18n.G("No matching instance found"))
	}

	return c.lister.run(rows)
}

func instanceListHook(columnEntry string) (listColumn[instanceWithRemote], bool, error) {
	// Config keys always contain a period.
	if !strings.Contains(columnEntry, ".") {
		return listColumn[instanceWithRemote]{}, false, nil
	}

	cc := strings.Split(columnEntry, ":")
	colType := configColumnType
	if (cc[0] == configColumnType || cc[0] == deviceColumnType) && len(cc) > 1 {
		colType = cc[0]
		cc = slices.Delete(cc, 0, 1)
	}

	if len(cc) > 3 {
		return listColumn[instanceWithRemote]{}, false, fmt.Errorf(i18n.G("Invalid config key column format (too many fields): '%s'"), columnEntry)
	}

	k := cc[0]
	if colType == configColumnType {
		_, err := instance.ConfigKeyChecker(k, api.InstanceTypeAny)
		if err != nil {
			return listColumn[instanceWithRemote]{}, false, fmt.Errorf(i18n.G("Invalid config key '%s' in '%s'"), k, columnEntry)
		}
	}

	column := listColumn[instanceWithRemote]{header: k}
	if len(cc) > 1 {
		if len(cc[1]) == 0 && len(cc) != 3 {
			return listColumn[instanceWithRemote]{}, false, fmt.Errorf(i18n.G("Invalid name in '%s', empty string is only allowed when defining maxWidth"), columnEntry)
		}

		column.header = cc[1]
	}

	maxWidth := -1
	if len(cc) > 2 {
		temp, err := strconv.ParseInt(cc[2], 10, 64)
		if err != nil {
			return listColumn[instanceWithRemote]{}, false, fmt.Errorf(i18n.G("Invalid max width (must be an integer) '%s' in '%s'"), cc[2], columnEntry)
		}

		if temp < -1 {
			return listColumn[instanceWithRemote]{}, false, fmt.Errorf(i18n.G("Invalid max width (must -1, 0 or a positive integer) '%s' in '%s'"), cc[2], columnEntry)
		}

		if temp == 0 {
			maxWidth = len(column.header)
		} else {
			maxWidth = int(temp)
		}
	}

	switch colType {
	case configColumnType:
		column.handler = func(cInfo instanceWithRemote) string {
			v, ok := cInfo.Config[k]
			if !ok {
				v = cInfo.ExpandedConfig[k]
			}

			// Truncate the data according to the max width.  A negative max width
			// indicates there is no effective limit.
			if maxWidth > 0 && len(v) > maxWidth {
				return v[:maxWidth]
			}

			return v
		}

	case deviceColumnType:
		column.handler = func(cInfo instanceWithRemote) string {
			d := strings.SplitN(k, ".", 2)
			if len(d) == 1 || len(d) > 2 {
				return ""
			}

			v, ok := cInfo.Devices[d[0]][d[1]]
			if !ok {
				v = cInfo.ExpandedDevices[d[0]][d[1]]
			}

			// Truncate the data according to the max width.  A negative max width
			// indicates there is no effective limit.
			if maxWidth > 0 && len(v) > maxWidth {
				return v[:maxWidth]
			}

			return v
		}
	}

	return column, true, nil
}

func (c *cmdList) getBaseImage(cInfo api.InstanceFull, long bool) string {
	v, ok := cInfo.Config["volatile.base_image"]
	if !ok {
		return ""
	}

	if !long && len(v) >= 12 {
		v = v[:12]
	}

	return v
}

func (c *cmdList) baseImageColumnData(cInfo instanceWithRemote) string {
	return c.getBaseImage(cInfo.InstanceFull, false)
}

func (c *cmdList) baseImageFullColumnData(cInfo instanceWithRemote) string {
	return c.getBaseImage(cInfo.InstanceFull, true)
}

func (c *cmdList) nameColumnData(cInfo instanceWithRemote) string {
	return cInfo.Name
}

func (c *cmdList) descriptionColumnData(cInfo instanceWithRemote) string {
	return cInfo.Description
}

func (c *cmdList) statusColumnData(cInfo instanceWithRemote) string {
	return strings.ToUpper(cInfo.Status)
}

func (c *cmdList) ip4ColumnData(cInfo instanceWithRemote) string {
	if cInfo.IsActive() && cInfo.State != nil && cInfo.State.Network != nil {
		ipv4s := []string{}
		for netName, network := range cInfo.State.Network {
			if network.Type == "loopback" {
				continue
			}

			for _, addr := range network.Addresses {
				if slices.Contains([]string{"link", "local"}, addr.Scope) {
					continue
				}

				if addr.Family == "inet" {
					ipv4s = append(ipv4s, fmt.Sprintf("%s (%s)", addr.Address, netName))
				}
			}
		}

		sort.Sort(sort.Reverse(sort.StringSlice(ipv4s)))
		return strings.Join(ipv4s, "\n")
	}

	return ""
}

func (c *cmdList) ip6ColumnData(cInfo instanceWithRemote) string {
	if cInfo.IsActive() && cInfo.State != nil && cInfo.State.Network != nil {
		ipv6s := []string{}
		for netName, network := range cInfo.State.Network {
			if network.Type == "loopback" {
				continue
			}

			for _, addr := range network.Addresses {
				if slices.Contains([]string{"link", "local"}, addr.Scope) {
					continue
				}

				if addr.Family == "inet6" {
					ipv6s = append(ipv6s, fmt.Sprintf("%s (%s)", addr.Address, netName))
				}
			}
		}

		sort.Sort(sort.Reverse(sort.StringSlice(ipv6s)))
		return strings.Join(ipv6s, "\n")
	}

	return ""
}

func (c *cmdList) projectColumnData(cInfo instanceWithRemote) string {
	return cInfo.Project
}

func (c *cmdList) remoteColumnData(cInfo instanceWithRemote) string {
	return cInfo.Remote
}

func (c *cmdList) memoryUsageColumnData(cInfo instanceWithRemote) string {
	if cInfo.IsActive() && cInfo.State != nil && cInfo.State.Memory.Usage > 0 {
		if !c.rawFormat {
			return units.GetByteSizeStringIEC(cInfo.State.Memory.Usage, 2)
		}

		return strconv.FormatInt(cInfo.State.Memory.Usage, 10)
	}

	return ""
}

func (c *cmdList) memoryUsagePercentColumnData(cInfo instanceWithRemote) string {
	if cInfo.IsActive() && cInfo.State != nil && cInfo.State.Memory.Usage > 0 {
		if cInfo.ExpandedConfig["limits.memory"] != "" {
			memorylimit := cInfo.ExpandedConfig["limits.memory"]

			if strings.Contains(memorylimit, "%") {
				return ""
			}

			val, err := units.ParseByteSizeString(cInfo.ExpandedConfig["limits.memory"])
			if err == nil && val > 0 {
				return fmt.Sprintf("%.1f%%", (float64(cInfo.State.Memory.Usage)/float64(val))*float64(100))
			}
		}
	}

	return ""
}

func (c *cmdList) cpuUsageSecondsColumnData(cInfo instanceWithRemote) string {
	if cInfo.IsActive() && cInfo.State != nil && cInfo.State.CPU.Usage > 0 {
		return fmt.Sprintf("%.2fs", float64(cInfo.State.CPU.Usage)/1000000000)
	}

	return ""
}

func (c *cmdList) diskUsageColumnData(cInfo instanceWithRemote) string {
	rootDisk, _, _ := instance.GetRootDiskDevice(cInfo.ExpandedDevices)

	if cInfo.State != nil && cInfo.State.Disk != nil && cInfo.State.Disk[rootDisk].Usage > 0 {
		if !c.rawFormat {
			return units.GetByteSizeStringIEC(cInfo.State.Disk[rootDisk].Usage, 2)
		}

		return strconv.FormatInt(cInfo.State.Disk[rootDisk].Usage, 10)
	}

	return ""
}

func (c *cmdList) typeColumnData(cInfo instanceWithRemote) string {
	ret := strings.ToUpper(cInfo.Type)

	if ret == "" {
		ret = "CONTAINER"
	}

	if util.IsTrue(cInfo.ExpandedConfig["volatile.container.oci"]) {
		ret = fmt.Sprintf("%s (%s)", ret, i18n.G("APP"))
	}

	if cInfo.Ephemeral {
		ret = fmt.Sprintf("%s (%s)", ret, i18n.G("EPHEMERAL"))
	}

	return ret
}

func (c *cmdList) numberSnapshotsColumnData(cInfo instanceWithRemote) string {
	if cInfo.Snapshots != nil {
		return fmt.Sprintf("%d", len(cInfo.Snapshots))
	}

	return "0"
}

func (c *cmdList) pidColumnData(cInfo instanceWithRemote) string {
	if cInfo.IsActive() && cInfo.State != nil {
		return fmt.Sprintf("%d", cInfo.State.Pid)
	}

	return ""
}

func (c *cmdList) architectureColumnData(cInfo instanceWithRemote) string {
	return cInfo.Architecture
}

func (c *cmdList) storagePoolColumnData(cInfo instanceWithRemote) string {
	for _, v := range cInfo.ExpandedDevices {
		if v["type"] == "disk" && v["path"] == "/" {
			return v["pool"]
		}
	}

	return ""
}

func (c *cmdList) profilesColumnData(cInfo instanceWithRemote) string {
	return strings.Join(cInfo.Profiles, "\n")
}

func (c *cmdList) createdColumnData(cInfo instanceWithRemote) string {
	if !cInfo.CreatedAt.IsZero() {
		return cInfo.CreatedAt.Local().Format(dateLayout)
	}

	return ""
}

func (c *cmdList) startedColumnData(cInfo instanceWithRemote) string {
	if cInfo.State != nil && !cInfo.State.StartedAt.IsZero() {
		return cInfo.State.StartedAt.Local().Format(dateLayout)
	}

	return ""
}

func (c *cmdList) lastUsedColumnData(cInfo instanceWithRemote) string {
	if !cInfo.LastUsedAt.IsZero() {
		return cInfo.LastUsedAt.Local().Format(dateLayout)
	}

	return ""
}

func (c *cmdList) numberOfProcessesColumnData(cInfo instanceWithRemote) string {
	if cInfo.IsActive() && cInfo.State != nil {
		return fmt.Sprintf("%d", cInfo.State.Processes)
	}

	return ""
}

func (c *cmdList) locationColumnData(cInfo instanceWithRemote) string {
	return cInfo.Location
}

func (c *cmdList) matchByNet(cState *api.InstanceState, query string, family string) bool {
	// Skip if no state.
	if cState == nil {
		return false
	}

	// Skip if no network data.
	if cState.Network == nil {
		return false
	}

	// Consider the filter as a CIDR.
	_, subnet, _ := net.ParseCIDR(query)

	// Go through interfaces.
	for _, network := range cState.Network {
		for _, addr := range network.Addresses {
			if family == "ipv6" && addr.Family != "inet6" {
				continue
			}

			if family == "ipv4" && addr.Family != "inet" {
				continue
			}

			if addr.Address == query {
				return true
			}

			if subnet != nil {
				ipAddr := net.ParseIP(addr.Address)
				if ipAddr != nil && subnet.Contains(ipAddr) {
					return true
				}
			}
		}
	}

	return false
}

func (c *cmdList) matchByIPV6(_ *api.Instance, cState *api.InstanceState, query string) bool {
	return c.matchByNet(cState, query, "ipv6")
}

func (c *cmdList) matchByIPV4(_ *api.Instance, cState *api.InstanceState, query string) bool {
	return c.matchByNet(cState, query, "ipv4")
}

func (c *cmdList) mapShorthandFilters() {
	c.shorthandFilters = map[string]func(*api.Instance, *api.InstanceState, string) bool{
		"ipv4": c.matchByIPV4,
		"ipv6": c.matchByIPV6,
	}
}

// prepareInstanceServerFilters processes and formats filter criteria
// for instances, ensuring they are in a format that the server can interpret.
func prepareInstanceServerFilters(filters []string, i any) []string {
	formatedFilters := []string{}

	for _, filter := range filters {
		membs := strings.SplitN(filter, "=", 2)
		key := membs[0]

		if len(membs) == 1 {
			// A bare name matches as a prefix, kept simple so the server can push it down to the database.
			regexpValue := key
			if !strings.Contains(key, "^") && !strings.Contains(key, "$") {
				regexpValue = "^" + regexpValue + ".*"
			}

			filter = "name=" + regexpValue
		} else {
			firstPart := key
			if strings.Contains(key, ".") {
				firstPart = strings.Split(key, ".")[0]
			}

			if !structHasField(reflect.TypeOf(i), firstPart) {
				filter = fmt.Sprintf("expanded_config.%s", filter)
			}

			if key == "state" {
				filter = fmt.Sprintf("status=%s", membs[1])
			}
		}

		formatedFilters = append(formatedFilters, filter)
	}

	return formatedFilters
}
