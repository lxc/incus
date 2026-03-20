package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/cmd/incus/color"
	u "github.com/lxc/incus/v6/cmd/incus/usage"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
	cli "github.com/lxc/incus/v6/shared/cmd"
	"github.com/lxc/incus/v6/shared/util"
)

type cmdDelete struct {
	global *cmdGlobal

	flagForce          bool
	flagForceProtected bool
	flagInteractive    bool
}

var cmdDeleteUsage = u.Usage{u.Instance.Remote().List(1)}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("delete", cmdDeleteUsage...)
	cmd.Aliases = []string{"rm", "remove"}
	cmd.Short = i18n.G("Delete instances")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Delete instances`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, i18n.G("Force the removal of running instances"))
	cmd.Flags().BoolVarP(&c.flagInteractive, "interactive", "i", false, i18n.G("Require user confirmation"))

	cmd.ValidArgsFunction = func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpInstances(toComplete)
	}

	return cmd
}

func (c *cmdDelete) promptDelete(p *u.Parsed) error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf(i18n.G("Remove %s (yes/no): "), formatRemote(c.global.conf, p))
	input, _ := reader.ReadString('\n')
	input = strings.TrimSuffix(input, "\n")

	if !slices.Contains([]string{i18n.G("yes")}, strings.ToLower(input)) {
		return errors.New(i18n.G("User aborted delete operation"))
	}

	return nil
}

func (c *cmdDelete) doDelete(d incus.InstanceServer, name string) error {
	// Instance delete
	op, err := d.DeleteInstance(name)
	if err != nil {
		return err
	}

	return op.Wait()
}

func (c *cmdDelete) deleteOne(p *u.Parsed) error {
	conf := c.global.conf
	d := p.RemoteServer
	instanceName := p.RemoteObject.String

	connInfo, err := d.GetConnectionInfo()
	if err != nil {
		return err
	}

	if c.flagInteractive {
		err := c.promptDelete(p)
		if err != nil {
			return err
		}
	}

	ct, _, err := d.GetInstance(instanceName)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed checking instance %s exists: %w"), formatRemote(conf, p), err)
	}

	if ct.StatusCode != 0 && ct.StatusCode != api.Stopped {
		if !c.flagForce {
			return fmt.Errorf(i18n.G("The instance %s is currently running, stop it first or pass --force"), formatRemote(conf, p))
		}

		req := api.InstanceStatePut{
			Action:  "stop",
			Timeout: -1,
			Force:   true,
		}

		op, err := d.UpdateInstanceState(instanceName, req, "")
		if err != nil {
			return err
		}

		err = op.Wait()
		if err != nil {
			return fmt.Errorf(i18n.G("Stopping the instance %s failed: %s"), formatRemote(conf, p), err)
		}

		if ct.Ephemeral {
			return nil
		}
	}

	if c.flagForceProtected && util.IsTrue(ct.ExpandedConfig["security.protection.delete"]) {
		// Refresh in case we had to stop it above.
		ct, etag, err := d.GetInstance(instanceName)
		if err != nil {
			return err
		}

		ct.Config["security.protection.delete"] = "false"
		op, err := d.UpdateInstance(instanceName, ct.Writable(), etag)
		if err != nil {
			return err
		}

		err = op.Wait()
		if err != nil {
			return err
		}
	}

	err = c.doDelete(d, instanceName)
	if err != nil {
		return fmt.Errorf(i18n.G("Failed deleting instance %s in project %q: %w"), formatRemote(conf, p), connInfo.Project, err)
	}

	return nil
}

// Run runs the actual command logic.
func (c *cmdDelete) Run(cmd *cobra.Command, args []string) error {
	parsed, err := cmdDeleteUsage.Parse(c.global.conf, cmd, args)
	if err != nil {
		return err
	}

	var errs []error
	for _, p := range parsed[0].List {
		err := c.deleteOne(p)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}
