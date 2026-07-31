package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"

	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/cmd/incus/color"
	u "github.com/lxc/incus/v7/cmd/incus/usage"
	"github.com/lxc/incus/v7/internal/i18n"
	"github.com/lxc/incus/v7/shared/api"
	cli "github.com/lxc/incus/v7/shared/cmd"
	"github.com/lxc/incus/v7/shared/termios"
	"github.com/lxc/incus/v7/shared/uefi"
	"github.com/lxc/incus/v7/shared/util"
)

type cmdLowLevel struct {
	global *cmdGlobal
}

func (c *cmdLowLevel) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("low-level")
	cmd.Aliases = []string{"debug"}
	cmd.Short = i18n.G("Low-level commands")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Low-level commands for instances`))

	lowLevelBitmapsCmd := cmdLowLevelBitmaps{global: c.global}
	cmd.AddCommand(lowLevelBitmapsCmd.command())

	lowLevelAttachCmd := cmdLowLevelMemory{global: c.global, lowLevel: c}
	cmd.AddCommand(lowLevelAttachCmd.command())

	lowLevelNBDCmd := cmdLowLevelNBD{global: c.global, lowLevel: c}
	cmd.AddCommand(lowLevelNBDCmd.command())

	lowLevelNVRAMCmd := cmdLowLevelNVRAM{global: c.global}
	cmd.AddCommand(lowLevelNVRAMCmd.command())

	lowLevelRepairCmd := cmdLowLevelRepair{global: c.global}
	cmd.AddCommand(lowLevelRepairCmd.command())

	return cmd
}

type cmdLowLevelBitmaps struct {
	global *cmdGlobal
}

func (c *cmdLowLevelBitmaps) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("bitmap")
	cmd.Aliases = []string{"bitmaps"}
	cmd.Short = i18n.G("Manage dirty bitmaps on virtual machines")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Manage dirty bitmaps on virtual machines`))

	// Create.
	lowLevelBitmapsCreateCmd := cmdLowLevelBitmapsCreate{global: c.global}
	cmd.AddCommand(lowLevelBitmapsCreateCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706.
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// Create.
type cmdLowLevelBitmapsCreate struct {
	global *cmdGlobal

	flagGranularity int
	flagPersistent  bool
	flagDisabled    bool
}

var cmdLowLevelBitmapsCreateUsage = u.Usage{u.Instance.Remote(), u.NewName(u.Placeholder(i18n.G("bitmap")))}

func (c *cmdLowLevelBitmapsCreate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("create", cmdLowLevelBitmapsCreateUsage...)
	cmd.Short = i18n.G("Create a dirty bitmap on a virtual machine")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Create a dirty bitmap on all the disks of a running virtual machine`,
	))

	cmd.RunE = c.run
	cli.AddIntFlag(cmd.Flags(), &c.flagGranularity, "granularity", i18n.G("Granularity of the dirty bitmap in bytes"))
	cli.AddBoolFlag(cmd.Flags(), &c.flagPersistent, "persistent", i18n.G("Store the bitmap on disk"))
	cli.AddBoolFlag(cmd.Flags(), &c.flagDisabled, "disabled", i18n.G("Create the bitmap in the disabled state"))

	// completion for instance.
	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdLowLevelBitmapsCreate) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdLowLevelBitmapsCreateUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String

	bitmap := api.StorageVolumeBitmapsPost{
		Name:        parsed[1].String,
		Granularity: c.flagGranularity,
		Persistent:  c.flagPersistent,
		Disabled:    c.flagDisabled,
	}

	err = d.CreateInstanceBitmap(instanceName, bitmap)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to create bitmap: %w"), err)
	}

	return nil
}

type cmdLowLevelRepair struct {
	global *cmdGlobal
}

var cmdLowLevelRepairUsage = u.Usage{u.Instance.Remote(), u.Placeholder(i18n.G("action"))}

func (c *cmdLowLevelRepair) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("repair", cmdLowLevelRepairUsage...)
	cmd.Short = i18n.G("Run a repair action on an instance")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Run a low-level repair action on an instance.

Supported actions:
  rebuild-config-volume    Rebuild the config volume of a stopped QCOW2 backed virtual machine
  rebuild-nvram            Rebuild the virtual machine's UEFI NVRAM`,
	))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		if len(args) == 1 {
			return []string{"rebuild-config-volume"}, cobra.ShellCompDirectiveNoFileComp
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdLowLevelRepair) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdLowLevelRepairUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String

	err = d.RepairInstance(instanceName, api.InstanceDebugRepairPost{Action: parsed[1].String})
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to repair instance: %w"), err)
	}

	return nil
}

type cmdLowLevelMemory struct {
	global   *cmdGlobal
	lowLevel *cmdLowLevel

	flagFormat string
}

var cmdLowLevelMemoryUsage = u.Usage{u.Instance.Remote(), u.Target(u.File)}

func (c *cmdLowLevelMemory) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("dump-memory", cmdLowLevelMemoryUsage...)
	cmd.Short = i18n.G("Export a virtual machine's memory state")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Export the current memory state of a running virtual machine into a dump file.
		This can be useful for debugging or analysis purposes.`,
	))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus low-level dump-memory vm1 memory-dump.elf --format=elf
    Creates an ELF format memory dump of the vm1 instance.`,
	))

	cmd.RunE = c.run
	cli.AddStringFlag(cmd.Flags(), &c.flagFormat, "format|f", "elf", "", i18n.G("Format of memory dump (e.g. elf, win-dmp, kdump-zlib, kdump-raw-zlib, ...)"))

	return cmd
}

func (c *cmdLowLevelMemory) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdLowLevelMemoryUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	path := parsed[1].String

	target, err := os.Create(path)
	if err != nil {
		return err
	}

	rc, err := d.GetInstanceDebugMemory(instanceName, c.flagFormat)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to dump instance memory: %w"), err)
	}

	_, err = util.SafeCopy(target, rc)
	if err != nil {
		return err
	}

	return nil
}

type cmdLowLevelNBD struct {
	global   *cmdGlobal
	lowLevel *cmdLowLevel

	flagAddress string
}

var cmdLowLevelNBDUsage = u.Usage{u.Instance.Remote()}

func (c *cmdLowLevelNBD) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("nbd", cmdLowLevelNBDUsage...)
	cmd.Short = i18n.G("NBD access to all of a virtual machine's disks")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`NBD access to all of a virtual machine's disks

This exposes all the disks of a running virtual machine over a local NBD
server, with each disk reachable as an NBD export named after its Incus
device name.`,
	))

	cli.AddStringFlag(cmd.Flags(), &c.flagAddress, "address", "", "", i18n.G("Specific address to listen on"))

	cmd.RunE = c.run

	// completion for instance.
	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdLowLevelNBD) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdLowLevelNBDUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String

	// Check that the instance exists before starting the NBD server.
	_, _, err = d.GetInstance(instanceName)
	if err != nil {
		return err
	}

	// Proxy to a local listener.
	listenAddr := c.flagAddress

	if listenAddr == "" {
		listenAddr = "127.0.0.1:0" // Listen on a random local port if not specified.
	}

	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to listen for connection: %w"), err)
	}

	fmt.Printf(i18n.G("NBD listening on %v")+"\n", listener.Addr())

	// Track the active connections, the first one starts the NBD session and the
	// following ones attach to it. The server stops the session when all of its
	// connections are closed.
	var connMu sync.Mutex
	activeConns := 0

	for {
		// Wait for a connection.
		nConn, err := listener.Accept()
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to accept incoming connection: %w"), err)
		}

		go func() {
			defer func() { _ = nConn.Close() }()

			fmt.Printf(i18n.G("NBD client connected %q")+"\n", nConn.RemoteAddr())
			defer fmt.Printf(i18n.G("NBD client disconnected %q")+"\n", nConn.RemoteAddr())

			connMu.Lock()
			reuse := activeConns > 0
			activeConns++
			connMu.Unlock()

			defer func() {
				connMu.Lock()
				activeConns--
				connMu.Unlock()
			}()

			// Get a connection to the NBD session.
			conn, err := d.GetInstanceNBDConn(instanceName, incus.InstanceNBDArgs{Reuse: reuse})
			if err != nil {
				fmt.Printf(i18n.G("NBD connection failed: %v")+"\n", err)
				return
			}

			defer func() { _ = conn.Close() }()

			// Proxy the traffic.
			var wg sync.WaitGroup
			wg.Add(2)

			go func() {
				defer wg.Done()

				_, _ = util.SafeCopy(conn, nConn)
				_ = conn.Close()
				_ = nConn.Close()
			}()

			go func() {
				defer wg.Done()

				_, _ = util.SafeCopy(nConn, conn)
				_ = conn.Close()
				_ = nConn.Close()
			}()

			wg.Wait()
		}()
	}
}

type cmdLowLevelNVRAM struct {
	global *cmdGlobal
}

type nvramColumn struct {
	Name string
	Data func(string, string, *api.InstanceNVRAMVariable) string
}

func (c *cmdLowLevelNVRAM) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("nvram")
	cmd.Short = i18n.G("Manage NVRAM on virtual machines")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Manage NVRAM on virtual machines`))

	// Edit.
	lowLevelNVRAMEditCmd := cmdLowLevelNVRAMEdit{global: c.global}
	cmd.AddCommand(lowLevelNVRAMEditCmd.command())

	// Get.
	lowLevelNVRAMGetCmd := cmdLowLevelNVRAMGet{global: c.global}
	cmd.AddCommand(lowLevelNVRAMGetCmd.command())

	// List.
	lowLevelNVRAMListCmd := cmdLowLevelNVRAMList{global: c.global}
	cmd.AddCommand(lowLevelNVRAMListCmd.command())

	// Set.
	lowLevelNVRAMSetCmd := cmdLowLevelNVRAMSet{global: c.global}
	cmd.AddCommand(lowLevelNVRAMSetCmd.command())

	// Unset.
	lowLevelNVRAMUnsetCmd := cmdLowLevelNVRAMUnset{global: c.global}
	cmd.AddCommand(lowLevelNVRAMUnsetCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706.
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// nvramGuessVar guesses a GUID and a variable name from the user input.
func nvramGuessVar(name string) (string, string, error) {
	// First, try the GUID:name syntax, which also allows aliases. Any errors here are fatal, as we
	// have no other sane way to parse colons.
	parts := strings.SplitN(name, ":", 2)
	if len(parts) == 2 {
		guid, err := uefi.ParseGUIDOrName(parts[0])
		if err != nil {
			return "", "", err
		}

		return guid, parts[1], nil
	}

	// Then, try both GUID-name and name-GUID combinations, which don’t allow aliases.
	parts = strings.Split(name, "-")
	n := len(parts)

	// If there is no dash, no namespace is given, so we use EFI_GLOBAL_VARIABLE as a sane default.
	if n == 1 {
		return uefi.EfiGlobalVariableGuid, name, nil
	}

	// People can go wild in how they represent GUIDs, so we dumbly try parsing up to the last dash,
	// then if it fails, from the first dash on.
	guid, err := uefi.ParseGUID(strings.Join(parts[:n-1], "-"))
	if err == nil {
		return guid, parts[n-1], nil
	}

	guid, err = uefi.ParseGUID(strings.Join(parts[1:], "-"))
	if err == nil {
		return guid, parts[0], nil
	}

	// If anything fails, we assume that no namespace is given. Dashes are allowed in UEFI variable
	// names, so this last safety net covers this unlikely case.
	return uefi.EfiGlobalVariableGuid, name, nil
}

// Edit.
type cmdLowLevelNVRAMEdit struct {
	global *cmdGlobal
}

var cmdLowLevelNVRAMEditUsage = u.Usage{u.Instance.Remote(), u.Variable}

func (c *cmdLowLevelNVRAMEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("edit", cmdLowLevelNVRAMEditUsage...)
	cmd.Short = i18n.G("Edit instance UEFI variables")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Edit instance UEFI variables`))

	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// helpTemplate returns a sample YAML configuration and guidelines for editing UEFI variables.
func (c *cmdLowLevelNVRAMEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the UEFI variable.
### Any line starting with a '# will be ignored.`,
	)
}

func (c *cmdLowLevelNVRAMEdit) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdLowLevelNVRAMEditUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	guid, varName, err := nvramGuessVar(parsed[1].String)
	if err != nil {
		return err
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		loader, err := yaml.NewLoader(os.Stdin)
		if err != nil {
			return err
		}

		newData := api.InstanceNVRAMVariablePut{}
		err = loader.Load(&newData)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}

		err = d.UpdateInstanceNVRAMGUIDVar(instanceName, guid, varName, newData, "")
		if err != nil {
			return err
		}

		return nil
	}

	// Extract the current value
	v, etag, err := d.GetInstanceNVRAMGUIDVar(instanceName, guid, varName)
	if err != nil {
		return err
	}

	// If the variable couldn't be dissected, then there's really nothing to edit.
	if v.Data == nil {
		return fmt.Errorf(i18n.G("Incus does not know how to dissect %s:%s"), guid, varName)
	}

	// Empty binary representation so it isn't shown in edit screen (relies on omitempty tag).
	v.Binary = nil

	data, err := yaml.Dump(&v, yaml.WithV2Defaults())
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := cli.TextEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newData := api.InstanceNVRAMVariablePut{}
		err = yaml.Load(content, &newData)
		if err == nil {
			err = d.UpdateInstanceNVRAMGUIDVar(instanceName, guid, varName, newData, etag)
		}

		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Failed to set UEFI variable %s:%s: %s")+"\n", guid, varName, err)
			fmt.Println(i18n.G("Press enter to open the editor again or ctrl+c to abort change"))

			_, err := os.Stdin.Read(make([]byte, 1))
			if err != nil {
				return err
			}

			content, err = cli.TextEditor("", content)
			if err != nil {
				return err
			}

			continue
		}

		break
	}

	return nil
}

// Get.
type cmdLowLevelNVRAMGet struct {
	global *cmdGlobal

	flagFormat string
}

var cmdLowLevelNVRAMGetUsage = u.Usage{u.Instance.Remote(), u.Variable}

func (c *cmdLowLevelNVRAMGet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("get", cmdLowLevelNVRAMGetUsage...)
	cmd.Short = i18n.G("Get values for UEFI variables")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Get values for UEFI variables`))

	cmd.RunE = c.run
	cli.AddStringFlag(cmd.Flags(), &c.flagFormat, "format|f", "yaml", "", i18n.G("Format (base64|binary|efivarfs|hex|json|yaml)"))

	// completion for instance.
	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdLowLevelNVRAMGet) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdLowLevelNVRAMGetUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	name := parsed[1].String
	guid, varName, err := nvramGuessVar(name)
	if err != nil {
		return err
	}

	if slices.Contains([]string{"base64", "binary", "efivarfs", "hex"}, c.flagFormat) {
		data, attributes, err := d.GetRawInstanceNVRAMGUIDVar(instanceName, guid, varName)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to get instance UEFI variable: %w"), err)
		}

		switch c.flagFormat {
		case "base64":
			fmt.Print(base64.StdEncoding.EncodeToString(data))
		case "binary":
			fmt.Print(string(data))
		case "efivarfs":
			attrs := make([]byte, 4)
			binary.LittleEndian.PutUint32(attrs, attributes)
			fmt.Print(string(append(attrs, data...)))
		case "hex":
			fmt.Print(hex.EncodeToString(data))
		}

		return nil
	}

	if !slices.Contains([]string{"json", "yaml"}, c.flagFormat) {
		return fmt.Errorf(i18n.G("Invalid format: %s"), c.flagFormat)
	}

	v, _, err := d.GetInstanceNVRAMGUIDVar(instanceName, guid, varName)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed to get instance UEFI variable: %w"), err)
	}

	var data []byte
	switch c.flagFormat {
	case "json":
		data, err = json.Marshal(v)
	case "yaml":
		data, err = yaml.Dump(v, yaml.WithV2Defaults())
	}

	if err != nil {
		return err
	}

	fmt.Print(string(data))
	return nil
}

// List.
type cmdLowLevelNVRAMList struct {
	global *cmdGlobal

	flagFormat  string
	flagColumns string
}

var cmdLowLevelNVRAMListUsage = u.Usage{u.Instance.Remote(), u.Placeholder(i18n.G("GUID")).Optional()}

func (c *cmdLowLevelNVRAMList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdLowLevelNVRAMListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List UEFI GUIDs and variables")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`List UEFI GUIDs and variables`))

	cmd.RunE = c.run
	cli.AddStringFlag(cmd.Flags(), &c.flagFormat, "format|f", c.global.defaultListFormat(), "", i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`))
	cli.AddStringFlag(cmd.Flags(), &c.flagColumns, "columns|c", defaultNVRAMColumns, "", i18n.G("Columns"))

	// completion for instance.
	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

const defaultNVRAMColumns = "Gnv"

func (c *cmdLowLevelNVRAMList) parseColumns() ([]nvramColumn, error) {
	columnsShorthandMap := map[rune]nvramColumn{
		'a': {i18n.G("ATTRIBUTES"), c.attributesColumnData},
		'g': {i18n.G("GUID"), c.guidColumnData},
		'G': {i18n.G("GUID NAME"), c.guidNameColumnData},
		'n': {i18n.G("VARIABLE NAME"), c.nameColumnData},
		'r': {i18n.G("RAW VALUE"), c.rawColumnData},
		't': {i18n.G("TIMESTAMP"), c.timestampColumnData},
		'v': {i18n.G("INTERPRETED VALUE"), c.valueColumnData},
	}

	columnList := strings.Split(c.flagColumns, ",")
	columns := []nvramColumn{}

	for _, columnEntry := range columnList {
		if columnEntry == "" {
			return nil, fmt.Errorf(i18n.G("Empty column entry (redundant, leading or trailing command) in '%s'"), c.flagColumns)
		}

		for _, columnRune := range columnEntry {
			column, ok := columnsShorthandMap[columnRune]
			if !ok {
				return nil, fmt.Errorf(i18n.G("Unknown column shorthand char '%c' in '%s'"), columnRune, columnEntry)
			}

			columns = append(columns, column)
		}
	}

	return columns, nil
}

func (c *cmdLowLevelNVRAMList) attributesColumnData(guid string, name string, v *api.InstanceNVRAMVariable) string {
	return strings.Join(v.Attributes, " + ")
}

func (c *cmdLowLevelNVRAMList) guidColumnData(guid string, name string, v *api.InstanceNVRAMVariable) string {
	return guid
}

func (c *cmdLowLevelNVRAMList) guidNameColumnData(guid string, name string, v *api.InstanceNVRAMVariable) string {
	return uefi.GUIDName(guid)
}

func (c *cmdLowLevelNVRAMList) nameColumnData(guid string, name string, v *api.InstanceNVRAMVariable) string {
	return name
}

func (c *cmdLowLevelNVRAMList) rawColumnData(guid string, name string, v *api.InstanceNVRAMVariable) string {
	return base64.StdEncoding.EncodeToString(v.Binary)
}

func (c *cmdLowLevelNVRAMList) timestampColumnData(guid string, name string, v *api.InstanceNVRAMVariable) string {
	if v.Timestamp == nil {
		return i18n.G("(no timestamp)")
	}

	return v.Timestamp.String()
}

func (c *cmdLowLevelNVRAMList) valueColumnData(guid string, name string, v *api.InstanceNVRAMVariable) string {
	if v.Data == nil {
		return i18n.G("(binary data)")
	}

	repr, err := yaml.Dump(v.Data, yaml.WithV2Defaults())
	if err != nil {
		return fmt.Sprintf(i18n.G("(error: %w)"), err)
	}

	return string(repr)
}

func (c *cmdLowLevelNVRAMList) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdLowLevelNVRAMListUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	var uefiVars map[string]map[string]*api.InstanceNVRAMVariable

	if parsed[1].Skipped {
		uefiVars, err = d.GetInstanceNVRAM(instanceName)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed to get instance UEFI variables: %w"), err)
		}
	} else {
		guid := parsed[1].String
		parsedGUID, err := uefi.ParseGUIDOrName(guid)
		if err != nil {
			return fmt.Errorf(i18n.G("Invalid GUID: %s"), guid)
		}

		vars, err := d.GetInstanceNVRAMGUID(instanceName, parsedGUID)
		if err != nil {
			return err
		}

		uefiVars = map[string]map[string]*api.InstanceNVRAMVariable{guid: vars}
	}

	// Parse column flags.
	columns, err := c.parseColumns()
	if err != nil {
		return err
	}

	// Render the table
	data := [][]string{}
	for guid, vars := range uefiVars {
		for name, v := range vars {
			line := []string{}
			for _, column := range columns {
				line = append(line, column.Data(guid, name, v))
			}

			data = append(data, line)
		}
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{}
	for _, column := range columns {
		header = append(header, column.Name)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, uefiVars)
}

// Set.
type cmdLowLevelNVRAMSet struct {
	global *cmdGlobal

	flagAttributes uint32
	flagFormat     string
	flagTimestamp  int64
}

var cmdLowLevelNVRAMSetUsage = u.Usage{u.Instance.Remote(), u.MakeKV(u.Variable, u.Value)}

func (c *cmdLowLevelNVRAMSet) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("set", cmdLowLevelNVRAMSetUsage...)
	cmd.Short = i18n.G("Set values for UEFI variables")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Set values for UEFI variables.`))

	cmd.RunE = c.run
	cli.AddUint32Flag(cmd.Flags(), &c.flagAttributes, "attributes", i18n.G("Set the variable attributes (requires `--format=base64|binary|hex`)"), 7)
	cli.AddStringFlag(cmd.Flags(), &c.flagFormat, "format|f", "yaml", "", i18n.G("Format (base64|binary|efivarfs|hex|json|yaml)"))
	cli.AddInt64Flag(cmd.Flags(), &c.flagTimestamp, "timestamp", i18n.G("Set the variable timestamp (requires `--format=base64|binary|efivarfs|hex`)"))

	// completion for instance.
	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdLowLevelNVRAMSet) run(cmd *cobra.Command, args []string) error {
	// We deliberately only accept a single definition at a time because we don’t want to give users
	// the impression that they are hitting any kind of optimized path. Setting 100 variables leads to
	// 100 full NVRAM rewrites.
	parsed, err := c.global.Parse(cmdLowLevelNVRAMSetUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	keys, err := kvToMap(u.AsSingleton(parsed[1]))
	if err != nil {
		return err
	}

	var errs []error

	for k, v := range keys {
		guid, varName, err := nvramGuessVar(k)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		if slices.Contains([]string{"base64", "binary", "efivarfs", "hex"}, c.flagFormat) {
			attributes := c.flagAttributes
			var data []byte
			switch c.flagFormat {
			case "base64":
				data, err = base64.StdEncoding.DecodeString(v)
				if err != nil {
					return err
				}

			case "binary":
				data = []byte(v)
			case "efivarfs":
				if cmd.Flags().Changed("attributes") {
					return fmt.Errorf(i18n.G("--attributes cannot be used with --format=%s"), "efivarfs")
				}

				if len(v) < 4 {
					return errors.New(i18n.G("Unexpected input size"))
				}

				b := []byte(v)
				attributes = binary.LittleEndian.Uint32(b[:4])
				data = b[4:]
			case "hex":
				data, err = hex.DecodeString(v)
				if err != nil {
					return err
				}
			}

			err = d.UpdateRawInstanceNVRAMGUIDVar(instanceName, guid, varName, data, attributes, c.flagTimestamp)
		} else {
			if cmd.Flags().Changed("attributes") {
				return fmt.Errorf(i18n.G("--attributes cannot be used with --format=%s"), c.flagFormat)
			}

			if cmd.Flags().Changed("timestamp") {
				return fmt.Errorf(i18n.G("--timestamp cannot be used with --format=%s"), c.flagFormat)
			}

			data := api.InstanceNVRAMVariablePut{}
			switch c.flagFormat {
			case "json":
				err = json.Unmarshal([]byte(v), &data)
			case "yaml":
				err = yaml.Load([]byte(v), &data)
			default:
				return fmt.Errorf(i18n.G("Invalid format: %s"), c.flagFormat)
			}

			if err != nil {
				errs = append(errs, fmt.Errorf(i18n.G("Failed to parse variable %s:%s: %w"), guid, varName, err))
				continue
			}

			err = d.UpdateInstanceNVRAMGUIDVar(instanceName, guid, varName, data, "")
		}

		if err != nil {
			errs = append(errs, fmt.Errorf(i18n.G("Failed to set UEFI variable %s:%s: %w"), guid, varName, err))
		}
	}

	return errors.Join(errs...)
}

// Unset.
type cmdLowLevelNVRAMUnset struct {
	global *cmdGlobal
}

var cmdLowLevelNVRAMUnsetUsage = u.Usage{u.Instance.Remote(), u.Variable}

func (c *cmdLowLevelNVRAMUnset) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("unset", cmdLowLevelNVRAMUnsetUsage...)
	cmd.Short = i18n.G("Unset UEFI variables")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("Unset UEFI variables"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdLowLevelNVRAMUnset) run(cmd *cobra.Command, args []string) error {
	// We deliberately only accept a single deletion at a time because we don’t want to give users
	// the impression that they are hitting any kind of optimized path. Deleting 100 variables leads
	// to 100 full NVRAM rewrites.
	parsed, err := c.global.Parse(cmdLowLevelNVRAMUnsetUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	guid, varName, err := nvramGuessVar(parsed[1].String)
	if err != nil {
		return err
	}

	// Delete the UEFI variable.
	err = d.DeleteInstanceNVRAMGUIDVar(instanceName, guid, varName)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("UEFI variable %s:%s deleted on %s")+"\n", guid, varName, formatRemote(c.global.conf, parsed[0]))
	}

	return nil
}
