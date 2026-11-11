package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v7/cmd/incus/color"
	u "github.com/lxc/incus/v7/cmd/incus/usage"
	"github.com/lxc/incus/v7/internal/i18n"
	cli "github.com/lxc/incus/v7/shared/cmd"
)

type cmdShell struct {
	*cmdExec
	flagShell string
	flagUser  string
}

var cmdShellUsage = u.Usage{u.Instance.Remote()}

func (c *cmdShell) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("shell", cmdShellUsage...)
	cmd.Short = i18n.G("Start a login shell in an instance")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Start a login shell in an instance.

On Linux, this runs "su -l", or "su -l <user>" when --user is given.
On Windows virtual machines, this runs "powershell.exe".`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus shell c1
    Start a login shell in instance "c1"

incus shell --user 1000 c1
    Start a login shell as user 1000 in instance "c1"`))
	cmd.RunE = c.run

	cli.AddStringFlag(cmd.Flags(), &c.flagShell, "shell", "auto", "",
		i18n.G("Shell to run: auto, powershell or su"))
	cli.AddStringFlag(cmd.Flags(), &c.flagUser, "user", "", "",
		i18n.G("User to log in as, by name or UID"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpInstances(toComplete)
	}

	return cmd
}

func (c *cmdShell) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdShellUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	instanceName := parsed[0].RemoteObject.String

	if !slices.Contains([]string{"auto", "powershell", "su"}, c.flagShell) {
		return fmt.Errorf(i18n.G("Unknown shell %q, expected auto, powershell or su"), c.flagShell)
	}

	inst, _, err := d.GetInstance(instanceName)
	if err != nil {
		return err
	}

	shell := c.flagShell
	if shell == "auto" {
		if strings.EqualFold(inst.Config["image.os"], "windows") {
			shell = "powershell"
		} else {
			shell = "su"
		}
	}

	var command []string
	switch shell {
	case "powershell":
		command = []string{"powershell.exe"}
	case "su":
		command = []string{"su", "-l"}
		if c.flagUser != "" {
			command = append(command, c.flagUser)
		}
	}

	c.flagMode = "auto"
	return c.runInstance(d, instanceName, command, 0)
}
