package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"

	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/cmd/incus/color"
	u "github.com/lxc/incus/v7/cmd/incus/usage"
	"github.com/lxc/incus/v7/internal/i18n"
	"github.com/lxc/incus/v7/shared/api"
	cli "github.com/lxc/incus/v7/shared/cmd"
	"github.com/lxc/incus/v7/shared/logger"
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

	lowLevelSecureBootCmd := cmdLowLevelSecureBoot{global: c.global}
	cmd.AddCommand(lowLevelSecureBootCmd.command())

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
			return []string{"rebuild-config-volume", "rebuild-nvram"}, cobra.ShellCompDirectiveNoFileComp
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
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`List UEFI GUIDs and variables

The -c option takes a (optionally comma-separated) list of arguments
that control which variable attributes to output when displaying in table
or csv format.

Default column layout is: Gn

Column shorthand chars:
	a - Attributes
	g - GUID
	G - Familiar GUID name or GUID if none
	n - Name
	r - Raw value
	t - Authenticated write timestamp
	v - Interpreted value`))

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

const defaultNVRAMColumns = "Gn"

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

var cmdLowLevelNVRAMSetUsage = u.Usage{u.Instance.Remote(), u.MakeKV(u.Variable, u.Value).List(1)}

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
	parsed, err := c.global.Parse(cmdLowLevelNVRAMSetUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	single := len(parsed[1].List) == 1
	if !single {
		if !d.HasExtension("instance_nvram_bulk_update") {
			return errors.New(i18n.G(`More than one variable was passed but the server is missing the required "instance_nvram_bulk_update" API extension`))
		}

		if !slices.Contains([]string{"json", "yaml"}, c.flagFormat) {
			return errors.New(i18n.G("Bulk NVRAM variable update is only supported for non-binary formats"))
		}
	}

	keys, err := kvToMap(parsed[1])
	if err != nil {
		return err
	}

	bulk := map[string]map[string]*api.InstanceNVRAMVariablePut{}
	for k, v := range keys {
		guid, varName, err := nvramGuessVar(k)
		if err != nil {
			return err
		}

		switch c.flagFormat {
		case "base64", "binary", "efivarfs", "hex":
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
			if err != nil {
				return fmt.Errorf(i18n.G("Failed to set UEFI variable %s:%s: %w"), guid, varName, err)
			}

		case "json", "yaml":
			if cmd.Flags().Changed("attributes") {
				return fmt.Errorf(i18n.G("--attributes cannot be used with --format=%s"), c.flagFormat)
			}

			if cmd.Flags().Changed("timestamp") {
				return fmt.Errorf(i18n.G("--timestamp cannot be used with --format=%s"), c.flagFormat)
			}

			var data *api.InstanceNVRAMVariablePut

			// If the value passed is empty, switch to the unset logic.
			if v == "" {
				if single {
					err = d.DeleteInstanceNVRAMGUIDVar(instanceName, guid, varName)
					if err != nil {
						return fmt.Errorf(i18n.G("Failed to delete variable %s:%s: %w"), guid, varName, err)
					}
				}
			} else {
				data = &api.InstanceNVRAMVariablePut{}
				switch c.flagFormat {
				case "json":
					err = json.Unmarshal([]byte(v), &data)
				case "yaml":
					err = yaml.Load([]byte(v), &data)
				}

				if err != nil {
					return fmt.Errorf(i18n.G("Failed to parse variable %s:%s: %w"), guid, varName, err)
				}

				if single {
					return d.UpdateInstanceNVRAMGUIDVar(instanceName, guid, varName, *data, "")
				}
			}

			_, ok := bulk[guid]
			if !ok {
				bulk[guid] = map[string]*api.InstanceNVRAMVariablePut{}
			}

			bulk[guid][varName] = data
		default:
			return fmt.Errorf(i18n.G("Invalid format: %s"), c.flagFormat)
		}
	}

	return d.UpdateInstanceNVRAM(instanceName, bulk)
}

// Unset.
type cmdLowLevelNVRAMUnset struct {
	global *cmdGlobal
}

var cmdLowLevelNVRAMUnsetUsage = u.Usage{u.Instance.Remote(), u.Variable.List(1)}

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
	parsed, err := c.global.Parse(cmdLowLevelNVRAMUnsetUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	single := len(parsed[1].List) == 1
	if !single && !d.HasExtension("instance_nvram_bulk_update") {
		return errors.New(i18n.G(`More than one variable was passed but the server is missing the required "instance_nvram_bulk_update" API extension`))
	}

	bulk := map[string]map[string]*api.InstanceNVRAMVariablePut{}
	for _, v := range parsed[1].List {
		guid, varName, err := nvramGuessVar(v.String)
		if err != nil {
			return err
		}

		if single {
			err = d.DeleteInstanceNVRAMGUIDVar(instanceName, guid, varName)
			if err != nil {
				return fmt.Errorf(i18n.G("Failed to delete variable %s:%s: %w"), guid, varName, err)
			}

			if !c.global.flagQuiet {
				fmt.Printf(i18n.G("UEFI variable %s:%s deleted on %s")+"\n", guid, varName, formatRemote(c.global.conf, parsed[0]))
			}

			return nil
		}

		_, ok := bulk[guid]
		if !ok {
			bulk[guid] = map[string]*api.InstanceNVRAMVariablePut{}
		}

		bulk[guid][varName] = nil
	}

	err = d.UpdateInstanceNVRAM(instanceName, bulk)
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("UEFI variables %s deleted on %s\n"), strings.Join(parsed[1].StringList, ", "), formatRemote(c.global.conf, parsed[0]))
	}

	return nil
}

type cmdLowLevelSecureBoot struct {
	global *cmdGlobal
}

type secureBootColumn struct {
	Name string
	Data func(string, *uefi.ESLEntry, *x509.Certificate) string
}

var cmdLowLevelSecureBootESLNames = []string{"pk", "kek", "db", "dbx", "dbt", "mok"}

func (c *cmdLowLevelSecureBoot) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("secureboot")
	cmd.Short = i18n.G("Manage Secure Boot on virtual machines")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Manage Secure Boot on virtual machines`))

	// Add.
	lowLevelSecureBootAddCmd := cmdLowLevelSecureBootAdd{global: c.global}
	cmd.AddCommand(lowLevelSecureBootAddCmd.command())

	// Export.
	lowLevelSecureBootExportCmd := cmdLowLevelSecureBootExport{global: c.global}
	cmd.AddCommand(lowLevelSecureBootExportCmd.command())

	// Import.
	lowLevelSecureBootImportCmd := cmdLowLevelSecureBootImport{global: c.global}
	cmd.AddCommand(lowLevelSecureBootImportCmd.command())

	// List.
	lowLevelSecureBootListCmd := cmdLowLevelSecureBootList{global: c.global}
	cmd.AddCommand(lowLevelSecureBootListCmd.command())

	// Remove.
	lowLevelSecureBootRemoveCmd := cmdLowLevelSecureBootRemove{global: c.global}
	cmd.AddCommand(lowLevelSecureBootRemoveCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706.
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

func eslParseCert(input []byte, owner string, certOnly bool, bundle bool) (uefi.ESL, error) {
	var esl uefi.ESL
	// Try to parse the file as PEM first.
	rest := input
	var block *pem.Block
	for {
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}

		switch block.Type {
		case "CERTIFICATE":
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf(i18n.G("Failed to parse PEM certificate: %w"), err)
			}

			esl = append(esl, uefi.ESLNode{Type: "x509", Entries: []uefi.ESLEntry{{Owner: owner, Data: cert.Raw}}})
		case "SIGNATURE":
			if certOnly {
				return nil, errors.New(i18n.G("Cannot store a signature in PK, KEK or dbt"))
			}

			if !bundle {
				return nil, errors.New(i18n.G("Cannot add signature without --bundle"))
			}

			eslNode := uefi.ESLNode{Entries: []uefi.ESLEntry{{Owner: owner, Data: block.Bytes}}}
			switch len(block.Bytes) {
			case 32:
				eslNode.Type = "sha256"
			case 48:
				eslNode.Type = "sha384"
			case 64:
				eslNode.Type = "sha512"
			default:
				return nil, fmt.Errorf(i18n.G("Unexpected signature length %d"), len(block.Bytes))
			}

			esl = append(esl, eslNode)
			continue
		default:
			fmt.Fprintf(os.Stderr, color.WarningPrefix+i18n.G("Ignored %s block")+"\n", block.Type)
			continue
		}

		if !bundle {
			break
		}
	}

	if len(esl) == 0 {
		// Now try to parse the file as DER.
		certs, err := x509.ParseCertificates(input)
		if err != nil {
			return nil, errors.New(i18n.G("Failed to parse input file as either PEM or DER certificate"))
		}

		if len(certs) == 0 {
			return nil, errors.New(i18n.G("Input file contains no certificate"))
		}

		for _, cert := range certs {
			esl = append(esl, uefi.ESLNode{Type: "x509", Entries: []uefi.ESLEntry{{Owner: owner, Data: cert.Raw}}})
			if !bundle {
				break
			}
		}
	}

	return esl, nil
}

// Add.
type cmdLowLevelSecureBootAdd struct {
	global *cmdGlobal

	flagBundle bool
	flagOwner  string
	flagSkip   bool
	flagType   string
}

var cmdLowLevelSecureBootAddUsage = u.Usage{u.Instance.Remote(), u.EitherVerbatim(cmdLowLevelSecureBootESLNames...), u.File}

func (c *cmdLowLevelSecureBootAdd) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("add", cmdLowLevelSecureBootAddUsage...)
	cmd.Short = i18n.G("Add Secure Boot signatures")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Add Secure Boot signatures`))

	cmd.RunE = c.run
	cli.AddBoolFlag(cmd.Flags(), &c.flagBundle, "bundle", i18n.G("Consider the input as a certificate and signature bundle instead of a certificate chain"))
	cli.AddStringFlag(cmd.Flags(), &c.flagOwner, "owner", "Incus", "", i18n.G("Set the signature owner"))
	cli.AddBoolFlag(cmd.Flags(), &c.flagSkip, "skip", i18n.G("Skip duplicate signatures"))
	cli.AddStringFlag(cmd.Flags(), &c.flagType, "type|t", "", "", i18n.G("Don’t import the file as a certificate but rather compute and import its digest (sha256|sha384|sha512)"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		if len(args) == 1 {
			return cmdLowLevelSecureBootESLNames, cobra.ShellCompDirectiveNoFileComp
		}

		return nil, cobra.ShellCompDirectiveDefault
	}

	return cmd
}

func (c *cmdLowLevelSecureBootAdd) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdLowLevelSecureBootAddUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	guid, varName := util.ESLGUIDVar(parsed[1].String)
	fileName := parsed[2].String
	var input []byte
	if isStdin(fileName) {
		input, err = io.ReadAll(os.Stdin)
	} else {
		input, err = os.ReadFile(fileName)
	}

	if err != nil {
		return fmt.Errorf(i18n.G("Failed reading input file: %w"), err)
	}

	owner, err := uefi.ParseGUIDOrName(c.flagOwner)
	if err != nil {
		return fmt.Errorf(i18n.G("Unable to parse owner %s: %w"), c.flagOwner, err)
	}

	isSignature := cmd.Flags().Changed("type")
	if isSignature && c.flagBundle {
		return errors.New(i18n.G("--bundle cannot be used with --type"))
	}

	certOnly := slices.Contains([]string{"PK", "KEK", "dbt"}, varName)
	var esl, oldESL, keptESL uefi.ESL
	if isSignature {
		if certOnly {
			return errors.New(i18n.G("Cannot store a signature in PK, KEK or dbt"))
		}

		var data []byte
		switch c.flagType {
		case "sha256":
			sum := sha256.Sum256(input)
			data = sum[:]
		case "sha384":
			sum := sha512.Sum384(input)
			data = sum[:]
		case "sha512":
			sum := sha512.Sum512(input)
			data = sum[:]
		default:
			return fmt.Errorf(i18n.G("Unknown signature type %s"), c.flagType)
		}

		esl = append(esl, uefi.ESLNode{Type: c.flagType, Entries: []uefi.ESLEntry{{Owner: owner, Data: data}}})
	} else {
		esl, err = eslParseCert(input, owner, certOnly, c.flagBundle)
		if err != nil {
			return err
		}
	}

	if varName == "PK" && len(esl) > 1 {
		return fmt.Errorf(i18n.G("Only one PK certificate can be enrolled; got %d"), len(esl))
	}

	v, etag, err := d.GetInstanceNVRAMGUIDVar(instanceName, guid, varName)
	if err != nil {
		if varName == "MokList" {
			v = &api.InstanceNVRAMVariable{InstanceNVRAMVariablePut: api.InstanceNVRAMVariablePut{
				Attributes: []string{"NON_VOLATILE", "BOOTSERVICE_ACCESS"},
			}}
		} else {
			now := time.Now().UTC()
			v = &api.InstanceNVRAMVariable{InstanceNVRAMVariablePut: api.InstanceNVRAMVariablePut{
				Attributes: []string{"NON_VOLATILE", "BOOTSERVICE_ACCESS", "RUNTIME_ACCESS", "TIME_BASED_AUTHENTICATED_WRITE_ACCESS"},
				Timestamp:  &now,
			}}
		}
	}

	// We received a JSON object unmarshalled as a map, so we convert it back to JSON to unmarshal it
	// again into our desired type.
	marshalled, err := json.Marshal(v.Data)
	if err != nil {
		// This shouldn’t happen.
		return fmt.Errorf(i18n.G("Unable to parse %s:%s as valid JSON"), uefi.GUIDName(guid), varName)
	}

	err = json.Unmarshal(marshalled, &oldESL)
	if err != nil {
		// This shouldn’t happen.
		return fmt.Errorf(i18n.G("Unable to parse %s:%s as an EFI Signature List"), uefi.GUIDName(guid), varName)
	}

	// First, make sure the store doesn’t already contain the certificates/signatures. If we are
	// dealing with PK, this also checks that there is no certificate already enrolled.
	count := 0
	firstIteration := true
	for _, node := range esl {
		keptNode := uefi.ESLNode{Type: node.Type, Header: node.Header}
		for _, entry := range node.Entries {
			kept := true
		out:
			for _, oldNode := range oldESL {
				for _, oldEntry := range oldNode.Entries {
					if firstIteration {
						count++
					}

					if bytes.Equal(oldEntry.Data, entry.Data) {
						if c.flagSkip {
							count--
							kept = false
							break out
						} else {
							if isSignature {
								return errors.New(i18n.G("The given signature is already present in this ESL"))
							}

							return errors.New(i18n.G("The given certificate is already present in this ESL"))
						}
					}
				}
			}

			firstIteration = false
			if kept {
				keptNode.Entries = append(keptNode.Entries, entry)
			}
		}

		keptESL = append(keptESL, keptNode)
	}

	if varName == "PK" && count > 0 {
		return errors.New(i18n.G("A PK certificate is already enrolled; remove it first to enroll a new one"))
	}

	return d.UpdateInstanceNVRAMGUIDVar(instanceName, guid, varName, api.InstanceNVRAMVariablePut{
		Data:       append(oldESL, keptESL...),
		Attributes: v.Attributes,
		Timestamp:  v.Timestamp,
	}, etag)
}

// Export.
type cmdLowLevelSecureBootExport struct {
	global *cmdGlobal

	flagForce bool
}

var cmdLowLevelSecureBootExportUsage = u.Usage{u.Instance.Remote(), u.EitherVerbatim(cmdLowLevelSecureBootESLNames...), u.Target(u.File).Optional()}

func (c *cmdLowLevelSecureBootExport) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("export", cmdLowLevelSecureBootExportUsage...)
	cmd.Short = i18n.G("Export Secure Boot signatures")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Export Secure Boot signatures`))

	cli.AddBoolFlag(cmd.Flags(), &c.flagForce, "force|f", i18n.G("Force overwriting existing PEM file"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		if len(args) == 1 {
			return cmdLowLevelSecureBootESLNames, cobra.ShellCompDirectiveNoFileComp
		}

		return nil, cobra.ShellCompDirectiveDefault
	}

	return cmd
}

func (c *cmdLowLevelSecureBootExport) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdLowLevelSecureBootExportUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	db := parsed[1].String
	guid, varName := util.ESLGUIDVar(db)
	hasTarget := !parsed[2].Skipped
	targetName := parsed[2].Get(instanceName + "." + db + ".pem")
	if hasTarget && !isStdout(targetName) && !c.flagForce && util.PathExists(targetName) {
		// Check if the target path already exists.
		return fmt.Errorf(i18n.G("Target path %q already exists"), targetName)
	}

	v, _, err := d.GetInstanceNVRAMGUIDVar(instanceName, guid, varName)
	if err != nil {
		return err
	}

	// We received a JSON object unmarshalled as a map, so we convert it back to JSON to unmarshal it
	// again into our desired type.
	marshalled, err := json.Marshal(v.Data)
	if err != nil {
		// This shouldn’t happen.
		return fmt.Errorf(i18n.G("Unable to parse %s:%s as valid JSON"), uefi.GUIDName(guid), varName)
	}

	var esl uefi.ESL
	err = json.Unmarshal(marshalled, &esl)
	if err != nil {
		// This shouldn’t happen.
		return fmt.Errorf(i18n.G("Unable to parse %s:%s as an EFI Signature List"), uefi.GUIDName(guid), varName)
	}

	var target *os.File
	if isStdout(targetName) {
		target = os.Stdout
	} else {
		target, err = os.Create(targetName)
		if err != nil {
			return err
		}

		defer logger.WarnOnErrorExcept(target.Close, []error{os.ErrClosed}, "Failed to close target file")
	}

	for _, node := range esl {
		switch node.Type {
		case "x509":
			for _, entry := range node.Entries {
				cert, err := x509.ParseCertificate(entry.Data)
				if err != nil {
					return err
				}

				err = pem.Encode(target, &pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
				if err != nil {
					return err
				}
			}

		case "sha256", "sha384", "sha512":
			for _, entry := range node.Entries {
				err = pem.Encode(target, &pem.Block{Type: "SIGNATURE", Bytes: entry.Data})
				if err != nil {
					return err
				}
			}
		default:
			// Technically, some of those signature types are not unknown, but we prefer not to bother
			// supporting them.
			return fmt.Errorf(i18n.G("Unknown signature type %s"), node.Type)
		}
	}

	return nil
}

// Import.
type cmdLowLevelSecureBootImport struct {
	global *cmdGlobal

	flagForce bool
	flagOwner string
}

var cmdLowLevelSecureBootImportUsage = u.Usage{u.Instance.Remote(), u.EitherVerbatim(cmdLowLevelSecureBootESLNames...), u.File}

func (c *cmdLowLevelSecureBootImport) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("import", cmdLowLevelSecureBootExportUsage...)
	cmd.Short = i18n.G("Import Secure Boot signatures")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Import Secure Boot signatures`))

	cli.AddBoolFlag(cmd.Flags(), &c.flagForce, "force|f", i18n.G("Force overwriting existing ESL"))
	cli.AddStringFlag(cmd.Flags(), &c.flagOwner, "owner", "Incus", "", i18n.G("Set the signature owner"))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		if len(args) == 1 {
			return cmdLowLevelSecureBootESLNames, cobra.ShellCompDirectiveNoFileComp
		}

		return nil, cobra.ShellCompDirectiveDefault
	}

	return cmd
}

func (c *cmdLowLevelSecureBootImport) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdLowLevelSecureBootImportUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	guid, varName := util.ESLGUIDVar(parsed[1].String)
	fileName := parsed[2].String
	var input []byte
	if isStdin(fileName) {
		input, err = io.ReadAll(os.Stdin)
	} else {
		input, err = os.ReadFile(fileName)
	}

	if err != nil {
		return fmt.Errorf(i18n.G("Failed reading input file: %w"), err)
	}

	owner, err := uefi.ParseGUIDOrName(c.flagOwner)
	if err != nil {
		return fmt.Errorf(i18n.G("Unable to parse owner %s: %w"), c.flagOwner, err)
	}

	esl, err := eslParseCert(input, owner, slices.Contains([]string{"PK", "KEK", "dbt"}, varName), true)
	if err != nil {
		return err
	}

	if varName == "PK" && len(esl) > 1 {
		return fmt.Errorf(i18n.G("Only one PK certificate can be enrolled; got %d"), len(esl))
	}

	v, etag, err := d.GetInstanceNVRAMGUIDVar(instanceName, guid, varName)
	if err != nil {
		if varName == "MokList" {
			v = &api.InstanceNVRAMVariable{InstanceNVRAMVariablePut: api.InstanceNVRAMVariablePut{
				Attributes: []string{"NON_VOLATILE", "BOOTSERVICE_ACCESS"},
			}}
		} else {
			now := time.Now().UTC()
			v = &api.InstanceNVRAMVariable{InstanceNVRAMVariablePut: api.InstanceNVRAMVariablePut{
				Attributes: []string{"NON_VOLATILE", "BOOTSERVICE_ACCESS", "RUNTIME_ACCESS", "TIME_BASED_AUTHENTICATED_WRITE_ACCESS"},
				Timestamp:  &now,
			}}
		}
	} else if !c.flagForce {
		return errors.New(i18n.G("The given ESL already exists; use --force to override"))
	}

	return d.UpdateInstanceNVRAMGUIDVar(instanceName, guid, varName, api.InstanceNVRAMVariablePut{
		Data:       esl,
		Attributes: v.Attributes,
		Timestamp:  v.Timestamp,
	}, etag)
}

// List.
type cmdLowLevelSecureBootList struct {
	global *cmdGlobal

	flagFormat  string
	flagColumns string
}

var cmdLowLevelSecureBootListUsage = u.Usage{u.Instance.Remote(), u.EitherVerbatim(cmdLowLevelSecureBootESLNames...)}

func (c *cmdLowLevelSecureBootList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdLowLevelSecureBootListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List Secure Boot signatures")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`List Secure Boot signatures

The -c option takes a (optionally comma-separated) list of arguments
that control which signature attributes to output when displaying in table
or csv format.

Default column layout is: tOfs

Column shorthand chars:
	f - Fingerprint (short)
	F - Fingerprint (long)
	i - Issuer (short, for certificates)
	I - Issuer (long, for certificates)
	o - Owner GUID
	O - Owner familiar GUID name or GUID if none
	r - Raw value
	s - Subject (short, for certificates)
	S - Subject (long, for certificates)
	t - Signature type`))

	cmd.RunE = c.run
	cli.AddStringFlag(cmd.Flags(), &c.flagFormat, "format|f", c.global.defaultListFormat(), "", i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`))
	cli.AddStringFlag(cmd.Flags(), &c.flagColumns, "columns|c", defaultSecureBootColumns, "", i18n.G("Columns"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		if len(args) == 1 {
			return cmdLowLevelSecureBootESLNames, cobra.ShellCompDirectiveNoFileComp
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

const defaultSecureBootColumns = "tOfs"

func (c *cmdLowLevelSecureBootList) parseColumns() ([]secureBootColumn, error) {
	columnsShorthandMap := map[rune]secureBootColumn{
		'f': {i18n.G("FINGERPRINT"), c.fingerprintColumnData},
		'F': {i18n.G("FINGERPRINT"), c.fingerprintFullColumnData},
		'i': {i18n.G("ISSUER"), c.issuerColumnData},
		'I': {i18n.G("ISSUER"), c.issuerFullColumnData},
		'o': {i18n.G("OWNER GUID"), c.ownerColumnData},
		'O': {i18n.G("OWNER GUID NAME"), c.ownerNameColumnData},
		'r': {i18n.G("RAW VALUE"), c.rawColumnData},
		's': {i18n.G("SUBJECT"), c.subjectColumnData},
		'S': {i18n.G("SUBJECT"), c.subjectFullColumnData},
		't': {i18n.G("TYPE"), c.typeColumnData},
	}

	columnList := strings.Split(c.flagColumns, ",")
	columns := []secureBootColumn{}

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

// eslFingerprint returns the fingerprint of the given ESL entry.
func eslFingerprint(v *uefi.ESLEntry, cert *x509.Certificate) string {
	var data []byte
	if cert == nil {
		data = v.Data
	} else {
		sum := sha256.Sum256(cert.Raw)
		data = sum[:]
	}

	return fmt.Sprintf("%x", data)
}

func (c *cmdLowLevelSecureBootList) fingerprintColumnData(sigType string, v *uefi.ESLEntry, cert *x509.Certificate) string {
	fingerprint := eslFingerprint(v, cert)
	return fingerprint[:12]
}

func (c *cmdLowLevelSecureBootList) fingerprintFullColumnData(sigType string, v *uefi.ESLEntry, cert *x509.Certificate) string {
	return eslFingerprint(v, cert)
}

func (c *cmdLowLevelSecureBootList) issuerColumnData(sigType string, v *uefi.ESLEntry, cert *x509.Certificate) string {
	if cert == nil {
		return i18n.G("(not a certificate)")
	}

	return cert.Issuer.CommonName
}

func (c *cmdLowLevelSecureBootList) issuerFullColumnData(sigType string, v *uefi.ESLEntry, cert *x509.Certificate) string {
	if cert == nil {
		return i18n.G("(not a certificate)")
	}

	return cert.Issuer.String()
}

func (c *cmdLowLevelSecureBootList) ownerColumnData(sigType string, v *uefi.ESLEntry, cert *x509.Certificate) string {
	return v.Owner
}

func (c *cmdLowLevelSecureBootList) ownerNameColumnData(sigType string, v *uefi.ESLEntry, cert *x509.Certificate) string {
	return uefi.GUIDName(v.Owner)
}

func (c *cmdLowLevelSecureBootList) rawColumnData(sigType string, v *uefi.ESLEntry, cert *x509.Certificate) string {
	return base64.StdEncoding.EncodeToString(v.Data)
}

func (c *cmdLowLevelSecureBootList) subjectColumnData(sigType string, v *uefi.ESLEntry, cert *x509.Certificate) string {
	if cert == nil {
		return i18n.G("(not a certificate)")
	}

	return cert.Subject.CommonName
}

func (c *cmdLowLevelSecureBootList) subjectFullColumnData(sigType string, v *uefi.ESLEntry, cert *x509.Certificate) string {
	if cert == nil {
		return i18n.G("(not a certificate)")
	}

	return cert.Subject.String()
}

func (c *cmdLowLevelSecureBootList) typeColumnData(sigType string, v *uefi.ESLEntry, cert *x509.Certificate) string {
	return sigType
}

func (c *cmdLowLevelSecureBootList) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdLowLevelSecureBootListUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	guid, varName := util.ESLGUIDVar(parsed[1].String)

	v, _, err := d.GetInstanceNVRAMGUIDVar(instanceName, guid, varName)
	if err != nil {
		return err
	}

	// We received a JSON object unmarshalled as a map, so we convert it back to JSON to unmarshal it
	// again into our desired type.
	marshalled, err := json.Marshal(v.Data)
	if err != nil {
		// This shouldn’t happen.
		return fmt.Errorf(i18n.G("Unable to parse %s:%s as valid JSON"), uefi.GUIDName(guid), varName)
	}

	var esl uefi.ESL
	err = json.Unmarshal(marshalled, &esl)
	if err != nil {
		// This shouldn’t happen.
		return fmt.Errorf(i18n.G("Unable to parse %s:%s as an EFI Signature List"), uefi.GUIDName(guid), varName)
	}

	// Parse column flags.
	columns, err := c.parseColumns()
	if err != nil {
		return err
	}

	// Render the table
	data := [][]string{}
	for _, node := range esl {
		for _, entry := range node.Entries {
			line := []string{}
			cert, _ := x509.ParseCertificate(entry.Data)
			for _, column := range columns {
				line = append(line, column.Data(node.Type, &entry, cert))
			}

			data = append(data, line)
		}
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{}
	for _, column := range columns {
		header = append(header, column.Name)
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, esl)
}

// Remove.
type cmdLowLevelSecureBootRemove struct {
	global *cmdGlobal

	flagAll   bool
	flagOwner string
	flagType  string
}

var cmdLowLevelSecureBootRemoveUsage = u.Usage{u.Instance.Remote(), u.EitherVerbatim(cmdLowLevelSecureBootESLNames...), u.Either(u.Fingerprint, u.Flag("all"))}

func (c *cmdLowLevelSecureBootRemove) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("remove", cmdLowLevelSecureBootRemoveUsage...)
	cmd.Aliases = []string{"delete", "rm"}
	cmd.Short = i18n.G("Remove Secure Boot signatures")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Remove Secure Boot signatures`))

	cmd.RunE = c.run
	cli.AddBoolFlag(cmd.Flags(), &c.flagAll, "all|a", i18n.G("Remove all signatures"))
	cli.AddStringFlag(cmd.Flags(), &c.flagOwner, "owner", "", "", i18n.G("Only remove signatures owned by the given owner"))
	cli.AddStringFlag(cmd.Flags(), &c.flagType, "type", "", "", i18n.G("Only remove signatures of the given type"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		if len(args) == 1 {
			return cmdLowLevelSecureBootESLNames, cobra.ShellCompDirectiveNoFileComp
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdLowLevelSecureBootRemove) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdLowLevelSecureBootRemoveUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String
	guid, varName := util.ESLGUIDVar(parsed[1].String)
	hasFingerprint := parsed[2].BranchID == 0
	var fingerprint string
	if hasFingerprint {
		fingerprint = parsed[2].String

		if cmd.Flags().Changed("owner") || cmd.Flags().Changed("type") {
			return errors.New(i18n.G("--owner and --type require --all to be set"))
		}
	}

	v, etag, err := d.GetInstanceNVRAMGUIDVar(instanceName, guid, varName)
	if err != nil {
		return err
	}

	if c.flagAll && !cmd.Flags().Changed("owner") && !cmd.Flags().Changed("type") {
		// Deleting the variable is a good way to purge the ESL.
		return d.DeleteInstanceNVRAMGUIDVar(instanceName, guid, varName)
	}

	// We received a JSON object unmarshalled as a map, so we convert it back to JSON to unmarshal it
	// again into our desired type.
	marshalled, err := json.Marshal(v.Data)
	if err != nil {
		// This shouldn’t happen.
		return fmt.Errorf(i18n.G("Unable to parse %s:%s as valid JSON"), uefi.GUIDName(guid), varName)
	}

	var esl, newESL uefi.ESL
	err = json.Unmarshal(marshalled, &esl)
	if err != nil {
		// This shouldn’t happen.
		return fmt.Errorf(i18n.G("Unable to parse %s:%s as an EFI Signature List"), uefi.GUIDName(guid), varName)
	}

	var owner string
	if cmd.Flags().Changed("owner") {
		owner, err = uefi.ParseGUIDOrName(c.flagOwner)
		if err != nil {
			return fmt.Errorf(i18n.G("Unable to parse owner %s: %w"), c.flagOwner, err)
		}
	}

	removed := 0
	for _, node := range esl {
		if node.Type == c.flagType {
			removed += len(node.Entries)
			continue
		}

		var entries []uefi.ESLEntry
		for _, entry := range node.Entries {
			if entry.Owner == owner {
				removed++
				continue
			}

			cert, _ := x509.ParseCertificate(entry.Data)
			if hasFingerprint && strings.HasPrefix(eslFingerprint(&entry, cert), fingerprint) {
				removed++
				continue
			}

			entries = append(entries, entry)
		}

		if len(entries) > 0 {
			newESL = append(newESL, uefi.ESLNode{Type: node.Type, Header: node.Header, Entries: entries})
		}
	}

	if removed == 0 {
		return errors.New(i18n.G("No signature matches"))
	}

	if hasFingerprint && removed > 1 {
		return errors.New(i18n.G("Several signatures match the given fingerprint"))
	}

	return d.UpdateInstanceNVRAMGUIDVar(instanceName, guid, varName, api.InstanceNVRAMVariablePut{
		Data:       newESL,
		Attributes: v.Attributes,
		Timestamp:  v.Timestamp,
	}, etag)
}
