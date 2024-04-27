//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/util"
)

type cmdAdminCluster struct {
	global *cmdGlobal
}

func (c *cmdAdminCluster) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("cluster")
	cmd.Short = i18n.G("Low-level cluster administration commands")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Low level administration tools for inspecting and recovering clusters.`))

	cmd.Run = c.Run
	return cmd
}

func (c *cmdAdminCluster) Run(cmd *cobra.Command, args []string) {
	env := getEnviron()
	path, _ := exec.LookPath("incusd")
	if path == "" {
		if util.PathExists("/usr/libexec/incus/incusd") {
			path = "/usr/libexec/incus/incusd"
		} else if util.PathExists("/usr/lib/incus/incusd") {
			path = "/usr/lib/incus/incusd"
		} else if util.PathExists("/opt/incus/bin/incusd") {
			path = "/opt/incus/bin/incusd"
			env = append(env, "LD_LIBRARY_PATH=/opt/incus/lib/")
		}
	}

	if path == "" {
		fmt.Println(i18n.G(`The "cluster" subcommand requires access to internal server data.
To do so, it's actually part of the "incusd" binary rather than "incus".

You can invoke it through "incusd cluster".`))
		os.Exit(1)
	}

	_ = doExec(path, append([]string{"incusd", "cluster"}, args...), env)
}
