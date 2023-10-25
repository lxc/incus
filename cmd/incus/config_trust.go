package main

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	cli "github.com/lxc/incus/internal/cmd"
	"github.com/lxc/incus/internal/i18n"
	"github.com/lxc/incus/shared/api"
	"github.com/lxc/incus/shared/termios"
	localtls "github.com/lxc/incus/shared/tls"
	"github.com/lxc/incus/shared/util"
)

type cmdConfigTrust struct {
	global *cmdGlobal
	config *cmdConfig
}

func (c *cmdConfigTrust) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("trust")
	cmd.Short = i18n.G("Manage trusted clients")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Manage trusted clients`))

	// Add
	configTrustAddCmd := cmdConfigTrustAdd{global: c.global, config: c.config, configTrust: c}
	cmd.AddCommand(configTrustAddCmd.Command())

	// Add certificate
	configTrustAddCertificateCmd := cmdConfigTrustAddCertificate{global: c.global, config: c.config, configTrust: c}
	cmd.AddCommand(configTrustAddCertificateCmd.Command())

	// Edit
	configTrustEditCmd := cmdConfigTrustEdit{global: c.global, config: c.config, configTrust: c}
	cmd.AddCommand(configTrustEditCmd.Command())

	// List
	configTrustListCmd := cmdConfigTrustList{global: c.global, config: c.config, configTrust: c}
	cmd.AddCommand(configTrustListCmd.Command())

	// List tokens
	configTrustListTokensCmd := cmdConfigTrustListTokens{global: c.global, config: c.config, configTrust: c}
	cmd.AddCommand(configTrustListTokensCmd.Command())

	// Remove
	configTrustRemoveCmd := cmdConfigTrustRemove{global: c.global, config: c.config, configTrust: c}
	cmd.AddCommand(configTrustRemoveCmd.Command())

	// Revoke token
	configTrustRevokeTokenCmd := cmdConfigTrustRevokeToken{global: c.global, config: c.config, configTrust: c}
	cmd.AddCommand(configTrustRevokeTokenCmd.Command())

	// Show
	configTrustShowCmd := cmdConfigTrustShow{global: c.global, config: c.config, configTrust: c}
	cmd.AddCommand(configTrustShowCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, args []string) { _ = cmd.Usage() }
	return cmd
}

// Add.
type cmdConfigTrustAdd struct {
	global      *cmdGlobal
	config      *cmdConfig
	configTrust *cmdConfigTrust

	flagProjects   string
	flagRestricted bool
}

func (c *cmdConfigTrustAdd) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add", i18n.G("[<remote>:]<name>"))
	cmd.Short = i18n.G("Add new trusted client")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Add new trusted client

This will issue a trust token to be used by the client to add itself to the trust store.
`))

	cmd.Flags().BoolVar(&c.flagRestricted, "restricted", false, i18n.G("Restrict the certificate to one or more projects"))
	cmd.Flags().StringVar(&c.flagProjects, "projects", "", i18n.G("List of projects to restrict the certificate to")+"``")

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigTrustAdd) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote.
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]
	if resource.name == "" {
		return fmt.Errorf(i18n.G("A client name must be provided"))
	}

	// Prepare the request.
	cert := api.CertificatesPost{}
	cert.Token = true
	cert.Name = resource.name
	cert.Type = api.CertificateTypeClient
	cert.Restricted = c.flagRestricted

	if c.flagProjects != "" {
		cert.Projects = strings.Split(c.flagProjects, ",")
	}

	// Create the token.
	op, err := resource.server.CreateCertificateToken(cert)
	if err != nil {
		return err
	}

	opAPI := op.Get()
	certificateToken, err := opAPI.ToCertificateAddToken()
	if err != nil {
		return fmt.Errorf(i18n.G("Failed converting token operation to certificate add token: %w"), err)
	}

	if !c.global.flagQuiet {
		fmt.Printf(i18n.G("Client %s certificate add token:")+"\n", cert.Name)
	}

	fmt.Println(certificateToken.String())

	return nil
}

// Add certificate.
type cmdConfigTrustAddCertificate struct {
	global      *cmdGlobal
	config      *cmdConfig
	configTrust *cmdConfigTrust

	flagProjects   string
	flagRestricted bool
	flagName       string
	flagType       string
}

func (c *cmdConfigTrustAddCertificate) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("add-certificate", i18n.G("[<remote>:] <cert>"))
	cmd.Short = i18n.G("Add new trusted client certificate")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Add new trusted client certificate

The following certificate types are supported:
- client (default)
- metrics
`))

	cmd.Flags().BoolVar(&c.flagRestricted, "restricted", false, i18n.G("Restrict the certificate to one or more projects"))
	cmd.Flags().StringVar(&c.flagProjects, "projects", "", i18n.G("List of projects to restrict the certificate to")+"``")
	cmd.Flags().StringVar(&c.flagName, "name", "", i18n.G("Alternative certificate name")+"``")
	cmd.Flags().StringVar(&c.flagType, "type", "client", i18n.G("Type of certificate")+"``")

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigTrustAddCertificate) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 2)
	if exit {
		return err
	}

	// Validate flags.
	if !util.ValueInSlice(c.flagType, []string{"client", "metrics"}) {
		return fmt.Errorf(i18n.G("Unknown certificate type %q"), c.flagType)
	}

	// Parse remote
	remote := ""
	path := ""
	if len(args) > 1 {
		remote = args[0]
		path = args[1]
	} else {
		path = args[0]
	}

	if path == "-" {
		path = "/dev/stdin"
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	// Check that the path exists.
	if !util.PathExists(path) {
		return fmt.Errorf(i18n.G("Provided certificate path doesn't exist: %s"), path)
	}

	// Validate server support for metrics.
	if c.flagType == "metrics" && !resource.server.HasExtension("metrics") {
		return errors.New("The server doesn't implement metrics")
	}

	// Load the certificate.
	x509Cert, err := localtls.ReadCert(path)
	if err != nil {
		return err
	}

	var name string
	if c.flagName != "" {
		name = c.flagName
	} else {
		name = filepath.Base(path)
	}

	// Add trust relationship.
	cert := api.CertificatesPost{}
	cert.Certificate = base64.StdEncoding.EncodeToString(x509Cert.Raw)
	cert.Name = name

	if c.flagType == "client" {
		cert.Type = api.CertificateTypeClient
	} else if c.flagType == "metrics" {
		cert.Type = api.CertificateTypeMetrics
	}

	cert.Restricted = c.flagRestricted
	if c.flagProjects != "" {
		cert.Projects = strings.Split(c.flagProjects, ",")
	}

	return resource.server.CreateCertificate(cert)
}

// Edit.
type cmdConfigTrustEdit struct {
	global      *cmdGlobal
	config      *cmdConfig
	configTrust *cmdConfigTrust
}

func (c *cmdConfigTrustEdit) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("edit", i18n.G("[<remote>:]<fingerprint>"))
	cmd.Short = i18n.G("Edit trust configurations as YAML")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Edit trust configurations as YAML`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigTrustEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the certificate.
### Any line starting with a '# will be ignored.
###
### Note that the fingerprint is shown but cannot be changed`)
}

func (c *cmdConfigTrustEdit) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing certificate fingerprint"))
	}

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.CertificatePut{}
		err = yaml.Unmarshal(contents, &newdata)
		if err != nil {
			return err
		}

		return resource.server.UpdateCertificate(resource.name, newdata, "")
	}

	// Extract the current value
	cert, etag, err := resource.server.GetCertificate(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&cert)
	if err != nil {
		return err
	}

	// Spawn the editor
	content, err := textEditor("", []byte(c.helpTemplate()+"\n\n"+string(data)))
	if err != nil {
		return err
	}

	for {
		// Parse the text received from the editor
		newdata := api.CertificatePut{}
		err = yaml.Unmarshal(content, &newdata)
		if err == nil {
			err = resource.server.UpdateCertificate(resource.name, newdata, etag)
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
type cmdConfigTrustList struct {
	global      *cmdGlobal
	config      *cmdConfig
	configTrust *cmdConfigTrust

	flagFormat string
}

func (c *cmdConfigTrustList) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list", i18n.G("[<remote>:]"))
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List trusted clients")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List trusted clients`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigTrustList) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Parse remote
	remote := ""
	if len(args) > 0 {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	// List trust relationships
	trust, err := resource.server.GetCertificates()
	if err != nil {
		return err
	}

	data := [][]string{}
	for _, cert := range trust {
		fp := cert.Fingerprint[0:12]

		certBlock, _ := pem.Decode([]byte(cert.Certificate))
		if certBlock == nil {
			return fmt.Errorf(i18n.G("Invalid certificate"))
		}

		tlsCert, err := x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			return err
		}

		const layout = "Jan 2, 2006 at 3:04pm (MST)"
		issue := tlsCert.NotBefore.Format(layout)
		expiry := tlsCert.NotAfter.Format(layout)
		data = append(data, []string{cert.Type, cert.Name, tlsCert.Subject.CommonName, fp, issue, expiry})
	}

	sort.Sort(cli.StringList(data))

	header := []string{
		i18n.G("TYPE"),
		i18n.G("NAME"),
		i18n.G("COMMON NAME"),
		i18n.G("FINGERPRINT"),
		i18n.G("ISSUE DATE"),
		i18n.G("EXPIRY DATE"),
	}

	return cli.RenderTable(c.flagFormat, header, data, trust)
}

// List tokens.
type cmdConfigTrustListTokens struct {
	global      *cmdGlobal
	config      *cmdConfig
	configTrust *cmdConfigTrust

	flagFormat string
}

func (c *cmdConfigTrustListTokens) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("list-tokens", i18n.G("[<remote>:]"))
	cmd.Short = i18n.G("List all active certificate add tokens")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`List all active certificate add tokens`))
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "table", i18n.G("Format (csv|json|table|yaml|compact)")+"``")

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigTrustListTokens) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	// Parse remote.
	remote := ""
	if len(args) == 1 {
		remote = args[0]
	}

	resources, err := c.global.ParseServers(remote)
	if err != nil {
		return err
	}

	resource := resources[0]

	// Get the certificate add tokens. Use default project as join tokens are created in default project.
	ops, err := resource.server.UseProject(api.ProjectDefaultName).GetOperations()
	if err != nil {
		return err
	}

	// Convert the join token operation into encoded form for display.
	type displayToken struct {
		ClientName string
		Token      string
		ExpiresAt  string
	}

	displayTokens := make([]displayToken, 0)

	for _, op := range ops {
		if op.Class != api.OperationClassToken {
			continue
		}

		if op.StatusCode != api.Running {
			continue // Tokens are single use, so if cancelled but not deleted yet its not available.
		}

		joinToken, err := op.ToCertificateAddToken()
		if err != nil {
			continue // Operation is not a valid certificate add token operation.
		}

		var expiresAt string

		// Only show the expiry date if available, otherwise show an empty string.
		if joinToken.ExpiresAt.Unix() > 0 {
			expiresAt = joinToken.ExpiresAt.Format("2006/01/02 15:04 MST")
		}

		displayTokens = append(displayTokens, displayToken{
			ClientName: joinToken.ClientName,
			Token:      joinToken.String(),
			ExpiresAt:  expiresAt,
		})
	}

	// Render the table.
	data := [][]string{}
	for _, token := range displayTokens {
		line := []string{token.ClientName, token.Token, token.ExpiresAt}
		data = append(data, line)
	}

	sort.Sort(cli.SortColumnsNaturally(data))

	header := []string{
		i18n.G("NAME"),
		i18n.G("TOKEN"),
		i18n.G("EXPIRES AT"),
	}

	return cli.RenderTable(c.flagFormat, header, data, displayTokens)
}

// Remove.
type cmdConfigTrustRemove struct {
	global      *cmdGlobal
	config      *cmdConfig
	configTrust *cmdConfigTrust
}

func (c *cmdConfigTrustRemove) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("remove", i18n.G("[<remote>:]<fingerprint>"))
	cmd.Aliases = []string{"rm"}
	cmd.Short = i18n.G("Remove trusted client")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Remove trusted client`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigTrustRemove) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 2)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	// Support both legacy "<remote>: <fingerprint>" and current "<remote>:<fingerprint>".
	var fingerprint string
	if len(args) == 2 {
		fingerprint = args[1]
	} else {
		fingerprint = resource.name
	}

	// Remove trust relationship
	return resource.server.DeleteCertificate(fingerprint)
}

// List tokens.
type cmdConfigTrustRevokeToken struct {
	global      *cmdGlobal
	config      *cmdConfig
	configTrust *cmdConfigTrust
}

func (c *cmdConfigTrustRevokeToken) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("revoke-token", i18n.G("[<remote>:] <name>"))
	cmd.Short = i18n.G("Revoke certificate add token")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Revoke certificate add token`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigTrustRevokeToken) Run(cmd *cobra.Command, args []string) error {
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]

	// Get the certificate add tokens. Use default project as certificate add tokens are created in default project.
	ops, err := resource.server.UseProject(api.ProjectDefaultName).GetOperations()
	if err != nil {
		return err
	}

	for _, op := range ops {
		if op.Class != api.OperationClassToken {
			continue
		}

		if op.StatusCode != api.Running {
			continue // Tokens are single use, so if cancelled but not deleted yet its not available.
		}

		joinToken, err := op.ToCertificateAddToken()
		if err != nil {
			continue // Operation is not a valid certificate add token operation.
		}

		if joinToken.ClientName == resource.name {
			// Delete the operation
			err = resource.server.DeleteOperation(op.ID)
			if err != nil {
				return err
			}

			if !c.global.flagQuiet {
				fmt.Printf(i18n.G("Certificate add token for %s deleted")+"\n", resource.name)
			}

			return nil
		}
	}

	return fmt.Errorf(i18n.G("No certificate add token for member %s on remote: %s"), resource.name, resource.remote)
}

// Show.
type cmdConfigTrustShow struct {
	global      *cmdGlobal
	config      *cmdConfig
	configTrust *cmdConfigTrust
}

func (c *cmdConfigTrustShow) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("show", i18n.G("[<remote>:]<fingerprint>"))
	cmd.Short = i18n.G("Show trust configurations")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show trust configurations`))

	cmd.RunE = c.Run

	return cmd
}

func (c *cmdConfigTrustShow) Run(cmd *cobra.Command, args []string) error {
	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 1, 1)
	if exit {
		return err
	}

	// Parse remote
	resources, err := c.global.ParseServers(args[0])
	if err != nil {
		return err
	}

	resource := resources[0]
	client := resource.server

	if resource.name == "" {
		return fmt.Errorf(i18n.G("Missing certificate fingerprint"))
	}

	// Show the certificate configuration
	cert, _, err := client.GetCertificate(resource.name)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(&cert)
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}
