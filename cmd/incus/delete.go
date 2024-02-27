package main

import (
	"bufio"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/client"
	cli "github.com/lxc/incus/internal/cmd"
	"github.com/lxc/incus/internal/i18n"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/util"
)

type cmdDelete struct {
	global *cmdGlobal

	flagForce          bool
	flagForceProtected bool
	flagInteractive    bool
}

func (c *cmdDelete) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("delete", i18n.G("[<remote>:]<instance> [[<remote>:]<instance>...]"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Delete instances")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Delete instances`))

	cmd.RunE = c.Run
	cmd.Flags().BoolVarP(&c.flagForce, "force", "f", false, i18n.G("Force the removal of running instances"))
	cmd.Flags().BoolVarP(&c.flagInteractive, "interactive", "i", false, i18n.G("Require user confirmation"))

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return c.global.cmpInstances(toComplete)
	}

	return cmd
}

func (c *cmdDelete) promptDelete(name string) error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf(i18n.G("Remove %s (yes/no): "), name)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSuffix(input, "\n")

	if !slices.Contains([]string{i18n.G("yes")}, strings.ToLower(input)) {
		return fmt.Errorf(i18n.G("User aborted delete operation"))
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

func (c *cmdDelete) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, -1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args...)
	if err != nil {
		return err
	}

	// Check that everything exists.
	err = instancesExist(resources)
	if err != nil {
		return err
	}

	// Process with deletion.
	for _, resource := range resources {
		connInfo, err := resource.server.GetConnectionInfo()
		if err != nil {
			return err
		}

		if c.flagInteractive {
			err := c.promptDelete(resource.name)
			if err != nil {
				return err
			}
		}

		ct, _, err := resource.server.GetInstance(resource.name)
		if err != nil {
			return err
		}

		if ct.StatusCode != 0 && ct.StatusCode != api.Stopped {
			if !c.flagForce {
				return fmt.Errorf(i18n.G("The instance is currently running, stop it first or pass --force"))
			}

			req := api.InstanceStatePut{
				Action:  "stop",
				Timeout: -1,
				Force:   true,
			}

			op, err := resource.server.UpdateInstanceState(resource.name, req, "")
			if err != nil {
				return err
			}

			err = op.Wait()
			if err != nil {
				return fmt.Errorf(i18n.G("Stopping the instance failed: %s"), err)
			}

			if ct.Ephemeral {
				continue
			}
		}

		if c.flagForceProtected && util.IsTrue(ct.ExpandedConfig["security.protection.delete"]) {
			// Refresh in case we had to stop it above.
			ct, etag, err := resource.server.GetInstance(resource.name)
			if err != nil {
				return err
			}

			ct.Config["security.protection.delete"] = "false"
			op, err := resource.server.UpdateInstance(resource.name, ct.Writable(), etag)
			if err != nil {
				return err
			}

			err = op.Wait()
			if err != nil {
				return err
			}
		}

		err = c.doDelete(resource.server, resource.name)
		if err != nil {
			return fmt.Errorf(i18n.G("Failed deleting instance %q in project %q: %w"), resource.name, connInfo.Project, err)
		}
	}
	return nil
}
