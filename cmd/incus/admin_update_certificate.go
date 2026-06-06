//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	incus "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/cmd/incus/color"
	u "github.com/lxc/incus/v7/cmd/incus/usage"
	"github.com/lxc/incus/v7/internal/i18n"
	cli "github.com/lxc/incus/v7/shared/cmd"
	"github.com/lxc/incus/v7/shared/util"
)

type cmdAdminUpdateCertificate struct {
	global *cmdGlobal
}

var cmdAdminUpdateCertificateUsage = u.Usage{u.Placeholder(i18n.G("cert.crt")), u.Placeholder(i18n.G("cert.key"))}

func (c *cmdAdminUpdateCertificate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("update-certificate", cmdAdminUpdateCertificateUsage...)
	cmd.Aliases = []string{"update-cert"}
	cmd.Short = i18n.G("Update the server certificate")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Update the server certificate

  This is only for standalone systems, cluster users should use "incus cluster update-certificate" instead".`))
	cmd.RunE = c.run

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) < 2 {
			return nil, cobra.ShellCompDirectiveDefault
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

func (c *cmdAdminUpdateCertificate) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdAdminUpdateCertificateUsage, cmd, args)
	if err != nil {
		return err
	}

	certFile := parsed[0].String
	keyFile := parsed[1].String

	if !util.PathExists(certFile) {
		return fmt.Errorf(i18n.G("Could not find certificate file path: %s"), certFile)
	}

	if !util.PathExists(keyFile) {
		return fmt.Errorf(i18n.G("Could not find certificate key file path: %s"), keyFile)
	}

	cert, err := os.ReadFile(certFile)
	if err != nil {
		return fmt.Errorf(i18n.G("Could not read certificate file: %s with error: %v"), certFile, err)
	}

	key, err := os.ReadFile(keyFile)
	if err != nil {
		return fmt.Errorf(i18n.G("Could not read certificate key file: %s with error: %v"), keyFile, err)
	}

	connArgs := &incus.ConnectionArgs{
		SkipGetServer: true,
	}

	d, err := incus.ConnectIncusUnix("", connArgs)
	if err != nil {
		return err
	}

	server, _, err := d.GetServer()
	if err != nil {
		return err
	}

	if server.Environment.ServerClustered {
		return errors.New(i18n.G("This command is not supported on clusters, use \"incus cluster update-certificate\" instead"))
	}

	req := struct {
		Certificate string `json:"certificate"`
		Key         string `json:"key"`
	}{
		Certificate: string(cert),
		Key:         string(key),
	}

	_, _, err = d.RawQuery("PUT", "/internal/server-certificate", req, "")
	if err != nil {
		return err
	}

	if !c.global.flagQuiet {
		fmt.Println(i18n.G("Successfully updated server certificate"))
	}

	return nil
}
