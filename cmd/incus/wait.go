package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/api"
	cli "github.com/lxc/incus/v6/shared/cmd"
)

type cmdWait struct {
	global *cmdGlobal

	flagInterval int
	flagTimeOut  int
}

func (c *cmdWait) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.Usage("wait", i18n.G("[<remote>:]<instance> <condition>"))
	cmd.Short = i18n.G("Wait for an instance to satisfy a condition")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Wait for an instance to satisfy a condition

Supported cConditions:

  agent            Wait for the VM agent to be running
  ip               Wait for any globally routable IP address
  ipv4             Wait for a globally routable IPv4 address
  ipv6             Wait for a globally routable IPv6 address
  status=STATUS    Wait for the instance status to become STATUS`))
	cmd.Example = cli.FormatSection("", i18n.G(`incus wait v1 agent
	Wait for VM instance v1 to have a functional agent.`))

	cmd.RunE = c.Run

	cmd.Flags().IntVar(&c.flagInterval, "interval", 5, i18n.G("Polling interval (in seconds)")+"``")
	cmd.Flags().IntVar(&c.flagTimeOut, "timeout", -1, i18n.G("Maximum wait time")+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run runs the actual command logic.
func (c *cmdWait) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 2, 2)
	if exit {
		return err
	}

	// Handle remote.
	remote, name, err := conf.ParseRemote(args[0])
	if err != nil {
		return err
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	inst, _, err := d.GetInstance(name)
	if err != nil {
		return err
	}

	start := time.Now()

	for {
		ok, err := c.checkCondition(d, inst, args[1])
		if err != nil {
			return err
		}

		if ok {
			return nil
		}

		if c.flagTimeOut > 0 && time.Since(start) > time.Duration(c.flagTimeOut)*time.Second {
			return fmt.Errorf("Timeout for instance %s for condition: %s", inst.Name, args[1])
		}

		time.Sleep(time.Duration(c.flagInterval) * time.Second)
	}
}

// check the conditions.
func (c *cmdWait) checkCondition(d incus.InstanceServer, inst *api.Instance, condition string) (bool, error) {
	state, _, err := d.GetInstanceState(inst.Name)
	if err != nil {
		return false, nil
	}

	switch {
	case strings.ToLower(condition) == "agent":
		if inst.Type != "virtual-machine" {
			return false, fmt.Errorf("The agent condition is only valid for virtual-machines")
		}

		if state.Processes > 0 {
			return true, nil
		}

	case condition == "ip":
		if c.hasGlobalIP(state, "") {
			return true, nil
		}

	case condition == "ipv4":
		if c.hasGlobalIP(state, "ipv4") {
			return true, nil
		}

	case condition == "ipv6":
		if c.hasGlobalIP(state, "ipv6") {
			return true, nil
		}

	case strings.HasPrefix(condition, "status="):
		status := strings.TrimPrefix(condition, "status=")

		if strings.EqualFold(state.Status, status) {
			return true, nil
		}

	default:
		return false, fmt.Errorf("Unknown condition %q", condition)
	}

	return false, nil
}

// Check if the instance has a globally routable IP address of the specified family.
func (c *cmdWait) hasGlobalIP(state *api.InstanceState, family string) bool {
	for _, net := range state.Network {
		for _, addr := range net.Addresses {
			if addr.Scope != "global" {
				continue
			}

			switch family {
			case "":

				return true
			case "ipv4":
				if addr.Family == "inet" {
					return true
				}

			case "ipv6":
				if addr.Family == "inet6" {
					return true
				}
			}
		}
	}

	return false
}
