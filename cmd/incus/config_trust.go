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
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v4"

	"github.com/lxc/incus/v7/cmd/incus/color"
	u "github.com/lxc/incus/v7/cmd/incus/usage"
	"github.com/lxc/incus/v7/internal/i18n"
	"github.com/lxc/incus/v7/shared/api"
	cli "github.com/lxc/incus/v7/shared/cmd"
	"github.com/lxc/incus/v7/shared/termios"
	localtls "github.com/lxc/incus/v7/shared/tls"
	"github.com/lxc/incus/v7/shared/util"
)

type cmdConfigTrust struct {
	global *cmdGlobal
	config *cmdConfig
}

type configTrustRowData struct {
	Cert    api.Certificate
	TLSCert *x509.Certificate
}

func (c *cmdConfigTrust) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("trust")
	cmd.Short = i18n.G("Manage trusted clients")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Manage trusted clients`))

	// Add
	configTrustAddCmd := cmdConfigTrustAdd{global: c.global, config: c.config, configTrust: c}
	cmd.AddCommand(configTrustAddCmd.command())

	// Add certificate
	configTrustAddCertificateCmd := cmdConfigTrustAddCertificate{global: c.global, config: c.config, configTrust: c}
	cmd.AddCommand(configTrustAddCertificateCmd.command())

	// Edit
	configTrustEditCmd := cmdConfigTrustEdit{global: c.global, config: c.config, configTrust: c}
	cmd.AddCommand(configTrustEditCmd.command())

	// List
	configTrustListCmd := cmdConfigTrustList{global: c.global, config: c.config, configTrust: c, lister: &listable[configTrustRowData]{global: c.global}}
	cmd.AddCommand(configTrustListCmd.command())

	// List tokens
	configTrustListTokensCmd := cmdConfigTrustListTokens{global: c.global, config: c.config, configTrust: c, lister: &listable[*api.CertificateAddToken]{global: c.global}}
	cmd.AddCommand(configTrustListTokensCmd.command())

	// Remove
	configTrustRemoveCmd := cmdConfigTrustRemove{global: c.global, config: c.config, configTrust: c}
	cmd.AddCommand(configTrustRemoveCmd.command())

	// Revoke token
	configTrustRevokeTokenCmd := cmdConfigTrustRevokeToken{global: c.global, config: c.config, configTrust: c}
	cmd.AddCommand(configTrustRevokeTokenCmd.command())

	// Show
	configTrustShowCmd := cmdConfigTrustShow{global: c.global, config: c.config, configTrust: c}
	cmd.AddCommand(configTrustShowCmd.command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
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

var cmdConfigTrustAddUsage = u.Usage{u.NewName(u.Client).Remote()}

func (c *cmdConfigTrustAdd) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("add", cmdConfigTrustAddUsage...)
	cmd.Short = i18n.G("Add new trusted client")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Add new trusted client

This will issue a trust token to be used by the client to add itself to the trust store.
`,
	))

	cli.AddBoolFlag(cmd.Flags(), &c.flagRestricted, "restricted", i18n.G("Restrict the certificate to one or more projects"))
	cli.AddStringFlag(cmd.Flags(), &c.flagProjects, "projects", "", "", i18n.G("List of projects to restrict the certificate to"))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdConfigTrustAdd) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdConfigTrustAddUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	clientName := parsed[0].RemoteObject.String

	// Prepare the request.
	cert := api.CertificatesPost{}
	cert.Token = true
	cert.Name = clientName
	cert.Type = api.CertificateTypeClient
	cert.Restricted = c.flagRestricted

	if c.flagProjects != "" {
		cert.Projects = strings.Split(c.flagProjects, ",")
	}

	// Create the token.
	op, err := d.CreateCertificateToken(cert)
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

	flagProjects    string
	flagRestricted  bool
	flagName        string
	flagType        string
	flagDescription string
}

var cmdConfigTrustAddCertificateUsage = u.Usage{u.RemoteColonOpt, u.Placeholder(i18n.G("cert.crt"))}

func (c *cmdConfigTrustAddCertificate) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("add-certificate", cmdConfigTrustAddCertificateUsage...)
	cmd.Short = i18n.G("Add new trusted client certificate")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Add new trusted client certificate

The following certificate types are supported:
- client (default)
- metrics
`,
	))

	cli.AddBoolFlag(cmd.Flags(), &c.flagRestricted, "restricted", i18n.G("Restrict the certificate to one or more projects"))
	cli.AddStringFlag(cmd.Flags(), &c.flagProjects, "projects", "", "", i18n.G("List of projects to restrict the certificate to"))
	cli.AddStringFlag(cmd.Flags(), &c.flagName, "name", "", "", i18n.G("Alternative certificate name"))
	cli.AddStringFlag(cmd.Flags(), &c.flagType, "type|t", "client", "", i18n.G("Type of certificate"))
	cli.AddStringFlag(cmd.Flags(), &c.flagDescription, "description", "", "", i18n.G("Certificate description"))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdConfigTrustAddCertificate) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdConfigTrustAddCertificateUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	certPath := parsed[1].String

	// Validate flags.
	if !slices.Contains([]string{"client", "metrics"}, c.flagType) {
		return fmt.Errorf(i18n.G("Unknown certificate type %q"), c.flagType)
	}

	if certPath == "-" {
		certPath = "/dev/stdin"
	}

	// Check that the path exists.
	if !util.PathExists(certPath) {
		return fmt.Errorf(i18n.G("Provided certificate path doesn't exist: %s"), certPath)
	}

	// Validate server support for metrics.
	if c.flagType == "metrics" && !d.HasExtension("metrics") {
		return errors.New("The server doesn't implement metrics")
	}

	// Load the certificate.
	x509Cert, err := localtls.ReadCert(certPath)
	if err != nil {
		return err
	}

	var name string
	if c.flagName != "" {
		name = c.flagName
	} else {
		name = filepath.Base(certPath)
	}

	// Add trust relationship.
	cert := api.CertificatesPost{}
	cert.Certificate = base64.StdEncoding.EncodeToString(x509Cert.Raw)
	cert.Name = name
	cert.Description = c.flagDescription

	switch c.flagType {
	case "client":
		cert.Type = api.CertificateTypeClient
	case "metrics":
		cert.Type = api.CertificateTypeMetrics
	}

	cert.Restricted = c.flagRestricted
	if c.flagProjects != "" {
		cert.Projects = strings.Split(c.flagProjects, ",")
	}

	return d.CreateCertificate(cert)
}

// Edit.
type cmdConfigTrustEdit struct {
	global      *cmdGlobal
	config      *cmdConfig
	configTrust *cmdConfigTrust
}

var cmdConfigTrustEditUsage = u.Usage{u.Fingerprint.Remote()}

func (c *cmdConfigTrustEdit) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("edit", cmdConfigTrustEditUsage...)
	cmd.Short = i18n.G("Edit trust configurations as YAML")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Edit trust configurations as YAML`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdConfigTrustEdit) helpTemplate() string {
	return i18n.G(
		`### This is a YAML representation of the certificate.
### Any line starting with a '# will be ignored.
###
### Note that the fingerprint is shown but cannot be changed`,
	)
}

func (c *cmdConfigTrustEdit) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdConfigTrustEditUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	fingerprint := parsed[0].RemoteObject.String

	// If stdin isn't a terminal, read text from it
	if !termios.IsTerminal(getStdinFd()) {
		loader, err := yaml.NewLoader(os.Stdin)
		if err != nil {
			return err
		}

		newdata := api.CertificatePut{}
		err = loader.Load(&newdata)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}

		return d.UpdateCertificate(fingerprint, newdata, "")
	}

	// Extract the current value
	cert, etag, err := d.GetCertificate(fingerprint)
	if err != nil {
		return err
	}

	data, err := yaml.Dump(&cert, yaml.WithV2Defaults())
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
		newdata := api.CertificatePut{}
		err = yaml.Load(content, &newdata)
		if err == nil {
			err = d.UpdateCertificate(fingerprint, newdata, etag)
		}

		// Respawn the editor
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.G("Config parsing error: %s")+"\n", err)
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

// List.
type cmdConfigTrustList struct {
	global      *cmdGlobal
	config      *cmdConfig
	configTrust *cmdConfigTrust
	lister      *listable[configTrustRowData]
}

var cmdConfigTrustListUsage = u.Usage{u.RemoteColonOpt, u.Filter.List(0)}

func (c *cmdConfigTrustList) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list", cmdConfigTrustListUsage...)
	cmd.Aliases = []string{"ls"}
	cmd.Short = i18n.G("List trusted clients")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("List trusted clients"))

	c.lister.init(cmd, "ntdfE", map[rune]listColumn[configTrustRowData]{
		'n': {i18n.G("NAME"), i18n.G("Name"), c.nameColumnData},
		't': {i18n.G("TYPE"), i18n.G("Certificate type"), c.typeColumnData},
		'c': {i18n.G("COMMON NAME"), i18n.G("Common name"), c.commonNameColumnData},
		'f': {i18n.G("FINGERPRINT"), i18n.G("Fingerprint (short)"), c.fingerprintColumnData},
		'F': {i18n.G("FINGERPRINT"), i18n.G("Fingerprint (long)"), c.fingerprintFullColumnData},
		'd': {i18n.G("DESCRIPTION"), i18n.G("Description"), c.descriptionColumnData},
		'i': {i18n.G("ISSUE DATE"), i18n.G("Issue date"), c.issueDateColumnData},
		'E': {i18n.G("EXPIRES"), i18n.G("Expiration date"), c.expiryDateColumnData},
		'r': {i18n.G("RESTRICTED"), i18n.G("Whether the certificate is restricted"), c.restrictedColumnData},
		'p': {i18n.G("PROJECTS"), i18n.G("Projects to which the certificate is confined"), c.projectColumnData},
	})

	cmd.RunE = c.run

	return cmd
}

func (c *cmdConfigTrustList) typeColumnData(r configTrustRowData) string {
	return r.Cert.Type
}

func (c *cmdConfigTrustList) nameColumnData(r configTrustRowData) string {
	return r.Cert.Name
}

func (c *cmdConfigTrustList) commonNameColumnData(r configTrustRowData) string {
	return r.TLSCert.Subject.CommonName
}

func (c *cmdConfigTrustList) fingerprintColumnData(r configTrustRowData) string {
	return r.Cert.Fingerprint[0:12]
}

func (c *cmdConfigTrustList) fingerprintFullColumnData(r configTrustRowData) string {
	return r.Cert.Fingerprint
}

func (c *cmdConfigTrustList) descriptionColumnData(r configTrustRowData) string {
	return r.Cert.Description
}

func (c *cmdConfigTrustList) issueDateColumnData(r configTrustRowData) string {
	return r.TLSCert.NotBefore.Local().Format(dateLayout)
}

func (c *cmdConfigTrustList) expiryDateColumnData(r configTrustRowData) string {
	return r.TLSCert.NotAfter.Local().Format(dateLayout)
}

func (c *cmdConfigTrustList) restrictedColumnData(r configTrustRowData) string {
	return yesNo(r.Cert.Restricted)
}

func (c *cmdConfigTrustList) projectColumnData(r configTrustRowData) string {
	projects := []string{}
	projects = append(projects, r.Cert.Projects...)

	sort.Strings(projects)
	return strings.Join(projects, "\n")
}

func (c *cmdConfigTrustList) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdConfigTrustListUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	filters := prepareCertificatesFilters(parsed[1].StringList)

	// List trust relationships
	trust, err := d.GetCertificatesWithFilter(filters)
	if err != nil {
		return err
	}

	rows := make([]configTrustRowData, len(trust))
	for i, cert := range trust {
		certBlock, _ := pem.Decode([]byte(cert.Certificate))
		if certBlock == nil {
			return errors.New(i18n.G("Invalid certificate"))
		}

		tlsCert, err := x509.ParseCertificate(certBlock.Bytes)
		if err != nil {
			return err
		}

		rows[i] = configTrustRowData{cert, tlsCert}
	}

	if len(rows) == 0 {
		u.HintMissingSpace(args, i18n.G("No matching client found"))
	}

	return c.lister.run(rows, trust)
}

// List tokens.
type cmdConfigTrustListTokens struct {
	global      *cmdGlobal
	config      *cmdConfig
	configTrust *cmdConfigTrust
	lister      *listable[*api.CertificateAddToken]
}

var cmdConfigTrustListTokensUsage = u.Usage{u.RemoteColonOpt}

func (c *cmdConfigTrustListTokens) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("list-tokens", cmdConfigTrustListTokensUsage...)
	cmd.Short = i18n.G("List all active certificate add tokens")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G("List all active certificate add tokens"))

	c.lister.init(cmd, "ntE", map[rune]listColumn[*api.CertificateAddToken]{
		'n': {i18n.G("NAME"), i18n.G("Name"), c.clientNameColumnData},
		't': {i18n.G("TOKEN"), i18n.G("Token"), c.tokenColumnData},
		'E': {i18n.G("EXPIRES"), i18n.G("Expiration date"), c.expiresAtColumnData},
	})

	cmd.RunE = c.run

	return cmd
}

func (c *cmdConfigTrustListTokens) clientNameColumnData(token *api.CertificateAddToken) string {
	return token.ClientName
}

func (c *cmdConfigTrustListTokens) tokenColumnData(token *api.CertificateAddToken) string {
	return token.String()
}

func (c *cmdConfigTrustListTokens) expiresAtColumnData(token *api.CertificateAddToken) string {
	if token.ExpiresAt.IsZero() {
		return " "
	}

	return token.ExpiresAt.Local().Format(dateLayout)
}

func (c *cmdConfigTrustListTokens) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdConfigTrustListTokensUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer

	// Get the certificate add tokens. Use default project as join tokens are created in default project.
	ops, err := d.UseProject(api.ProjectDefaultName).GetOperations()
	if err != nil {
		return err
	}

	joinTokens := []*api.CertificateAddToken{}
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

		joinTokens = append(joinTokens, joinToken)
	}

	return c.lister.run(joinTokens)
}

// Remove.
type cmdConfigTrustRemove struct {
	global      *cmdGlobal
	config      *cmdConfig
	configTrust *cmdConfigTrust
}

var cmdConfigTrustRemoveUsage = u.Usage{u.Fingerprint.Remote()}

func (c *cmdConfigTrustRemove) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("remove", cmdConfigTrustRemoveUsage...)
	cmd.Aliases = []string{"delete", "rm"}
	cmd.Short = i18n.G("Remove trusted client")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Remove trusted client`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdConfigTrustRemove) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdConfigTrustRemoveUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	fingerprint := parsed[0].RemoteObject.String

	// Remove trust relationship
	return d.DeleteCertificate(fingerprint)
}

// List tokens.
type cmdConfigTrustRevokeToken struct {
	global      *cmdGlobal
	config      *cmdConfig
	configTrust *cmdConfigTrust
}

var cmdConfigTrustRevokeTokenUsage = u.Usage{u.Token.Remote()}

func (c *cmdConfigTrustRevokeToken) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("revoke-token", cmdConfigTrustRevokeTokenUsage...)
	cmd.Short = i18n.G("Revoke certificate add token")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Revoke certificate add token`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdConfigTrustRevokeToken) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdConfigTrustRevokeTokenUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	remoteName := parsed[0].RemoteName
	token := parsed[0].RemoteObject.String

	// Get the certificate add tokens. Use default project as certificate add tokens are created in default project.
	ops, err := d.UseProject(api.ProjectDefaultName).GetOperations()
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

		if joinToken.ClientName == token {
			// Delete the operation
			err = d.DeleteOperation(op.ID)
			if err != nil {
				return err
			}

			if !c.global.flagQuiet {
				fmt.Printf(i18n.G("Certificate add token for %s deleted")+"\n", token)
			}

			return nil
		}
	}

	return fmt.Errorf(i18n.G("No certificate add token for member %s on remote: %s"), token, remoteName)
}

// Show.
type cmdConfigTrustShow struct {
	global      *cmdGlobal
	config      *cmdConfig
	configTrust *cmdConfigTrust
}

var cmdConfigTrustShowUsage = u.Usage{u.Fingerprint.Remote()}

func (c *cmdConfigTrustShow) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("show", cmdConfigTrustShowUsage...)
	cmd.Short = i18n.G("Show trust configurations")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(`Show trust configurations`))

	cmd.RunE = c.run

	return cmd
}

func (c *cmdConfigTrustShow) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdConfigTrustShowUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	fingerprint := parsed[0].RemoteObject.String

	// Show the certificate configuration
	cert, _, err := d.GetCertificate(fingerprint)
	if err != nil {
		return err
	}

	data, err := yaml.Dump(&cert, yaml.WithV2Defaults())
	if err != nil {
		return err
	}

	fmt.Printf("%s", data)

	return nil
}

// prepareCertificatesFilters processes and formats filter criteria
// for storage buckets, ensuring they are in a format that the server can interpret.
func prepareCertificatesFilters(filters []string) []string {
	formatedFilters := []string{}

	for _, filter := range filters {
		membs := strings.SplitN(filter, "=", 2)
		key := membs[0]

		if len(membs) == 1 {
			regexpValue := key
			if !strings.Contains(key, "^") && !strings.Contains(key, "$") {
				regexpValue = "^" + regexpValue + "$"
			}

			filter = fmt.Sprintf("name=(%s|^%s.*)", regexpValue, key)
		}

		formatedFilters = append(formatedFilters, filter)
	}

	return formatedFilters
}
