package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/termios"
)

// IncusOS management command.
type cmdAdminOS struct {
	global *cmdGlobal

	flagTarget string
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdAdminOS) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("os")
	cmd.Short = i18n.G("Manage IncusOS systems")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage IncusOS systems`))

	// Applications
	adminOSApplicationCmd := cmdAdminOSApplication{global: c.global, os: c}
	cmd.AddCommand(adminOSApplicationCmd.Command())

	// Debug
	adminOSDebugCmd := cmdAdminOSDebug{global: c.global, os: c}
	cmd.AddCommand(adminOSDebugCmd.Command())

	// Services
	adminOSServiceCmd := cmdAdminOSService{global: c.global, os: c}
	cmd.AddCommand(adminOSServiceCmd.Command())

	// System
	adminOSSystemCmd := cmdAdminOSSystem{global: c.global, os: c}
	cmd.AddCommand(adminOSSystemCmd.Command())

	// Show a warning.
	cmd.PersistentPreRun = func(_ *cobra.Command, _ []string) {
		fmt.Fprint(os.Stderr, i18n.G("WARNING: The IncusOS API and configuration is subject to change")+"\n\n")
	}

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// IncusOS application command.
type cmdAdminOSApplication struct {
	global *cmdGlobal
	os     *cmdAdminOS
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdAdminOSApplication) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("application")
	cmd.Short = i18n.G("Manage IncusOS applications")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage IncusOS applications`))

	// List
	adminOSApplicationListCmd := cmdAdminOSApplicationList{global: c.global, os: c.os}
	cmd.AddCommand(adminOSApplicationListCmd.Command())

	// Show
	adminOSApplicationShowCmd := cmdAdminOSApplicationShow{global: c.global, os: c.os}
	cmd.AddCommand(adminOSApplicationShowCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// List.
type cmdAdminOSApplicationList struct {
	global *cmdGlobal
	os     *cmdAdminOS

	flagFormat string
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdAdminOSApplicationList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list")
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List applications")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List aliases`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`)+"``")

	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		return cli.ValidateFlagFormatForListOutput(cmd.Flag("format").Value.String())
	}

	cmd.RunE = c.Run

	return cmd
}

// Run runs the actual command logic.
func (c *cmdAdminOSApplicationList) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}

	resources, err := c.global.parseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	// Use cluster target if specified.
	apiURL := "/os/1.0/applications"
	if c.os.flagTarget != "" {
		apiURL += "?target=" + c.os.flagTarget
	}

	// Get the list.
	resp, _, err := resource.server.RawQuery("GET", apiURL, nil, "")
	if err != nil {
		return err
	}

	entries, err := resp.MetadataAsStringSlice()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, v := range entries {
		data = append(data, []string{strings.TrimPrefix(v, "/1.0/applications/")})
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("NAME"),
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, conf.Aliases)
}

// Show.
type cmdAdminOSApplicationShow struct {
	global *cmdGlobal
	os     *cmdAdminOS
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdAdminOSApplicationShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<application>"))
	cmd.Short = i18n.G("Show IncusOS application details")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show IncusOS application details`))

	cmd.Flags().StringVar(&c.os.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

// Run runs the actual command logic.
func (c *cmdAdminOSApplicationShow) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}

	resources, err := c.global.parseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing application name"))
	}

	// Use cluster target if specified.
	apiURL := "/os/1.0/applications/" + resource.name
	if c.os.flagTarget != "" {
		apiURL = apiURL + "?target=" + c.os.flagTarget
	}

	resp, _, err := resource.server.RawQuery("GET", apiURL, nil, "")
	if err != nil {
		return err
	}

	var rawData any
	err = resp.MetadataAsStruct(&rawData)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(rawData)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// IncusOS debug command.
type cmdAdminOSDebug struct {
	global *cmdGlobal
	os     *cmdAdminOS
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdAdminOSDebug) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("debug")
	cmd.Short = i18n.G("Debug IncusOS systems")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Debug IncusOS systems`))

	// Log
	adminOSDebugLogCmd := cmdAdminOSDebugLog{global: c.global, os: c.os}
	cmd.AddCommand(adminOSDebugLogCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// Log.
type cmdAdminOSDebugLog struct {
	global *cmdGlobal
	os     *cmdAdminOS
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdAdminOSDebugLog) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("log")
	cmd.Short = i18n.G("Get debug log")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Get debug log`))

	cmd.RunE = c.Run

	return cmd
}

// Run runs the actual command logic.
func (c *cmdAdminOSDebugLog) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}

	resources, err := c.global.parseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	// Get the log.
	resp, _, err := resource.server.RawQuery("GET", "/os/1.0/debug/log", nil, "")
	if err != nil {
		return err
	}

	var data []map[string]string
	err = resp.MetadataAsStruct(&data)
	if err != nil {
		return err
	}

	for _, line := range data {
		timeStr := line["__REALTIME_TIMESTAMP"]
		timeInt, err := strconv.ParseInt(timeStr, 10, 64)
		if err != nil {
			continue
		}

		ts := time.UnixMicro(timeInt)

		fmt.Printf("[%s] %s: %s\n", ts.Format(dateLayout), line["SYSLOG_IDENTIFIER"], line["MESSAGE"])
	}

	return nil
}

// IncusOS service command.
type cmdAdminOSService struct {
	global *cmdGlobal
	os     *cmdAdminOS
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdAdminOSService) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("service")
	cmd.Short = i18n.G("Manage IncusOS services")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage IncusOS services`))

	// Edit
	adminOSServiceEditCmd := cmdAdminOSServiceEdit{global: c.global, os: c.os}
	cmd.AddCommand(adminOSServiceEditCmd.Command())

	// List
	adminOSApplicationListCmd := cmdAdminOSServiceList{global: c.global, os: c.os}
	cmd.AddCommand(adminOSApplicationListCmd.Command())

	// Show
	adminOSServiceShowCmd := cmdAdminOSServiceShow{global: c.global, os: c.os}
	cmd.AddCommand(adminOSServiceShowCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// Edit.
type cmdAdminOSServiceEdit struct {
	global *cmdGlobal
	os     *cmdAdminOS
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdAdminOSServiceEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<service>"))
	cmd.Short = i18n.G("Edit IncusOS service configuration")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit IncusOS service configuration`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus admin os service edit [<remote>:]<service> < service.yaml
    Update an IncusOS service configuration using the content of service.yaml.`))

	cmd.Flags().StringVar(&c.os.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

// Run runs the actual command logic.
func (c *cmdAdminOSServiceEdit) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing service name"))
	}

	// Use cluster target if specified.
	apiURL := "/os/1.0/services/" + resource.name
	if c.os.flagTarget != "" {
		apiURL = apiURL + "?target=" + c.os.flagTarget
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		_, _, err := resource.server.RawQuery("PUT", apiURL, os.Stdin, "")
		return err
	}

	// Extract the current value
	resp, _, err := resource.server.RawQuery("GET", apiURL, nil, "")
	if err != nil {
		return err
	}

	var rawData any
	err = resp.MetadataAsStruct(&rawData)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(rawData)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := textEditor("", []byte(string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		var newdata any
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			_, _, err = resource.server.RawQuery("PUT", apiURL, makeJsonable(newdata), "")
		}

		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
			fmt.Println(i18n.G("Press enter to open the editor again or ctrl+c to abort change"))

			_, err := os.Stdin.Read(make([]byte, 1))
			if err != nil {
				return err
			}

			content, err = textEditor("", content)
			if err != nil {
				return err
			}

			continue
		}

		break
	}

	return nil
}

// List.
type cmdAdminOSServiceList struct {
	global *cmdGlobal
	os     *cmdAdminOS

	flagFormat string
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdAdminOSServiceList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list")
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List services")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List aliases`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", c.global.defaultListFormat(), i18n.G(`Format (csv|json|table|yaml|compact|markdown), use suffix ",noheader" to disable headers and ",header" to enable it if missing, e.g. csv,header`)+"``")

	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		return cli.ValidateFlagFormatForListOutput(cmd.Flag("format").Value.String())
	}

	cmd.RunE = c.Run

	return cmd
}

// Run runs the actual command logic.
func (c *cmdAdminOSServiceList) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}

	resources, err := c.global.parseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	// Use cluster target if specified.
	apiURL := "/os/1.0/services"
	if c.os.flagTarget != "" {
		apiURL += "?target=" + c.os.flagTarget
	}

	// Get the list.
	resp, _, err := resource.server.RawQuery("GET", apiURL, nil, "")
	if err != nil {
		return err
	}

	entries, err := resp.MetadataAsStringSlice()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, v := range entries {
		data = append(data, []string{strings.TrimPrefix(v, "/1.0/services/")})
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("NAME"),
	}

	return cli.RenderTable(os.Stdout, c.flagFormat, header, data, conf.Aliases)
}

// Show.
type cmdAdminOSServiceShow struct {
	global *cmdGlobal
	os     *cmdAdminOS
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdAdminOSServiceShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<service>"))
	cmd.Short = i18n.G("Show IncusOS service configuration")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show IncusOS service configuration`))

	cmd.Flags().StringVar(&c.os.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

// Run runs the actual command logic.
func (c *cmdAdminOSServiceShow) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}

	resources, err := c.global.parseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing service name"))
	}

	// Use cluster target if specified.
	apiURL := "/os/1.0/services/" + resource.name
	if c.os.flagTarget != "" {
		apiURL = apiURL + "?target=" + c.os.flagTarget
	}

	resp, _, err := resource.server.RawQuery("GET", apiURL, nil, "")
	if err != nil {
		return err
	}

	var rawData any
	err = resp.MetadataAsStruct(&rawData)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(rawData)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// IncusOS system command.
type cmdAdminOSSystem struct {
	global *cmdGlobal
	os     *cmdAdminOS
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdAdminOSSystem) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("system")
	cmd.Short = i18n.G("Manage IncusOS system details")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage IncusOS system details`))

	// Edit
	adminOSSystemEditCmd := cmdAdminOSSystemEdit{global: c.global, os: c.os}
	cmd.AddCommand(adminOSSystemEditCmd.Command())

	// Show
	adminOSSystemShowCmd := cmdAdminOSSystemShow{global: c.global, os: c.os}
	cmd.AddCommand(adminOSSystemShowCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// Edit.
type cmdAdminOSSystemEdit struct {
	global *cmdGlobal
	os     *cmdAdminOS
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdAdminOSSystemEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<section>"))
	cmd.Short = i18n.G("Edit IncusOS system configuration section")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit IncusOS system configuration section`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus admin os system edit [<remote>:]<section> < section.yaml
    Update an IncusOS system configuration section using the content of section.yaml.`))

	cmd.Flags().StringVar(&c.os.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

// Run runs the actual command logic.
func (c *cmdAdminOSSystemEdit) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.parseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing system section name"))
	}

	// Use cluster target if specified.
	apiURL := "/os/1.0/system/" + resource.name
	if c.os.flagTarget != "" {
		apiURL = apiURL + "?target=" + c.os.flagTarget
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		_, _, err := resource.server.RawQuery("PUT", apiURL, os.Stdin, "")
		return err
	}

	// Extract the current value
	resp, _, err := resource.server.RawQuery("GET", apiURL, nil, "")
	if err != nil {
		return err
	}

	var rawData any
	err = resp.MetadataAsStruct(&rawData)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(rawData)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := textEditor("", []byte(string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		var newdata any
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			_, _, err = resource.server.RawQuery("PUT", apiURL, makeJsonable(newdata), "")
		}

		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
			fmt.Println(i18n.G("Press enter to open the editor again or ctrl+c to abort change"))

			_, err := os.Stdin.Read(make([]byte, 1))
			if err != nil {
				return err
			}

			content, err = textEditor("", content)
			if err != nil {
				return err
			}

			continue
		}

		break
	}

	return nil
}

// Show.
type cmdAdminOSSystemShow struct {
	global *cmdGlobal
	os     *cmdAdminOS
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdAdminOSSystemShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<section>"))
	cmd.Short = i18n.G("Show IncusOS system configuration section")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show IncusOS system configuration section`))

	cmd.Flags().StringVar(&c.os.flagTarget, "target", "", i18n.G("Cluster member name")+"``")
	cmd.RunE = c.Run

	return cmd
}

// Run runs the actual command logic.
func (c *cmdAdminOSSystemShow) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}

	resources, err := c.global.parseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return errors.New(i18n.G("Missing configuration section name"))
	}

	// Use cluster target if specified.
	apiURL := "/os/1.0/system/" + resource.name
	if c.os.flagTarget != "" {
		apiURL = apiURL + "?target=" + c.os.flagTarget
	}

	resp, _, err := resource.server.RawQuery("GET", apiURL, nil, "")
	if err != nil {
		return err
	}

	var rawData any
	err = resp.MetadataAsStruct(&rawData)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(rawData)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}
