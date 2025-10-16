package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cli/oauth"
	"github.com/google/go-github/v75/github"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	"gopkg.in/yaml.v2"

	incus "github.com/lxc/incus/v6/client"
	cli "github.com/lxc/incus/v6/internal/cmd"
	"github.com/lxc/incus/v6/internal/i18n"
	"github.com/lxc/incus/v6/shared/util"
)

type cmdBug struct {
	global *cmdGlobal
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdBug) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("bug")
	cmd.Short = i18n.G("Prepare bug reports")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Prepare bug reports`))

	// Info
	bugInfoCmd := cmdBugInfo{global: c.global}
	cmd.AddCommand(bugInfoCmd.Command())

	// Report
	bugReportCmd := cmdBugReport{global: c.global}
	cmd.AddCommand(bugReportCmd.Command())

	// Workaround for subcommand usage errors. See: https://github.com/spf13/cobra/issues/706
	cmd.Args = cobra.NoArgs
	cmd.Run = func(cmd *cobra.Command, _ []string) { _ = cmd.Usage() }
	return cmd
}

// Info.
type cmdBugInfo struct {
	global *cmdGlobal

	flagTarget string
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdBugInfo) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("info", i18n.G("[<remote>:]"))
	cmd.Short = i18n.G("Show system details to include in bug reports")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Show system details to include in bug reports`))

	cmd.RunE = c.Run
	cmd.Flags().StringVar(&c.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpRemotes(toComplete, false)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run runs the actual command logic.
func (c *cmdBugInfo) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	var remote string
	if len(args) == 1 {
		remote, _, err = conf.ParseRemote(args[0])
		if err != nil {
			return err
		}
	} else {
		remote, _, err = conf.ParseRemote("")
		if err != nil {
			return err
		}
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	serverInfo, err := cmdBugRemoteServerInfo(d, c.flagTarget)
	if err != nil {
		return err
	}

	fmt.Print(serverInfo)
	return nil
}

func cmdBugRemoteServerInfo(d incus.InstanceServer, target string) (string, error) {
	// Targeting
	if target != "" {
		if !d.IsClustered() {
			return "", errors.New(i18n.G("To use --target, the destination remote must be a cluster"))
		}

		d = d.UseTarget(target)
	}

	serverStatus, _, err := d.GetServer()
	if err != nil {
		return "", err
	}

	unfiltered, err := yaml.Marshal(&serverStatus)
	if err != nil {
		return "", err
	}

	var data map[string]any
	err = yaml.Unmarshal(unfiltered, &data)
	if err != nil {
		return "", err
	}

	filteredFields := map[string][]string{
		"api_extensions": nil,
		"environment":    {"addresses", "certificate", "certificate_fingerprint", "server_name", "server_pid"},
	}

	for field, subfields := range filteredFields {
		if len(subfields) == 0 {
			delete(data, field)
		} else {
			subdata, ok := data[field].(map[any]any)
			if ok {
				for _, subfield := range subfields {
					delete(subdata, subfield)
				}
			}
		}
	}

	filtered, err := yaml.Marshal(&data)
	if err != nil {
		return "", err
	}

	return string(filtered), nil
}

// Report.
type cmdBugReport struct {
	global *cmdGlobal

	flagTarget string
}

// Command returns a cobra.Command for use with (*cobra.Command).AddCommand.
func (c *cmdBugReport) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("report", i18n.G("[<remote>:][<instance>]"))
	cmd.Short = i18n.G("Report a bug")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Report a bug`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus bug report [<remote>:]<instance>
    To report an instance-related bug.

incus bug report [<remote>:]
    To report a server-related bug.`))

	cmd.RunE = c.Run
	cmd.Flags().StringVar(&c.flagTarget, "target", "", i18n.G("Cluster member name")+"``")

	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return c.global.cmpInstances(toComplete)
		}

		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}

// Run runs the actual command logic.
func (c *cmdBugReport) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	// Quick checks.
	exit, err := c.global.checkArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	var remote string
	var cName string
	if len(args) == 1 {
		remote, cName, err = conf.ParseRemote(args[0])
		if err != nil {
			return err
		}
	} else {
		remote, cName, err = conf.ParseRemote("")
		if err != nil {
			return err
		}
	}

	if cName != "" && c.flagTarget != "" {
		return errors.New(i18n.G("--target cannot be used with instances"))
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	fmt.Print(i18n.G("This command is dedicated to reporting bugs found in Incus. For all support requests and other questions, please ask on our forum, https://discuss.linuxcontainers.org.") + "\n\n")

	issueTitle, err := c.global.asker.AskString(i18n.G("Choose an issue title:")+" ", "", nil)
	if err != nil {
		return err
	}

	existingIssue, err := c.global.asker.AskBool(i18n.G("Is there an existing issue for this?")+" (yes/no) [default=no]: ", "no")
	if err != nil {
		return err
	}

	if existingIssue {
		fmt.Println("Please only report new bugs.")
		return nil
	}

	upToDate, err := c.global.asker.AskBool(i18n.G("Is this happening on an up to date version of Incus?")+" (yes/no) [default=yes]: ", "yes")
	if err != nil {
		return err
	}

	if !upToDate {
		fmt.Println("Please update your Incus installation to a supported version.")
		return nil
	}

	systemDetails, err := cmdBugRemoteServerInfo(d, c.flagTarget)
	if err != nil {
		return err
	}

	instanceDetails := "_No response_"
	instanceLog := "_No response_"
	if cName != "" {
		rawInstanceDetails, err := cmdConfigShowInstance(d, cName, false)
		if err != nil {
			return err
		}

		instanceDetails = "```yaml\n" + rawInstanceDetails + "```"

		rawInstanceLog, err := cmdInfoInstance(d, cName, true)
		if err != nil {
			return err
		}

		instanceLog = "```\n" + rawInstanceLog + "```"
	}

	currentBehavior, err := c.markdownEditor(i18n.G("What is the current behavior?"), i18n.G("a concise description of what you're experiencing"))
	if err != nil {
		return err
	}

	expectedBehavior, err := c.markdownEditor(i18n.G("What is the expected behavior?"), i18n.G("a concise description of what you expected to happen"))
	if err != nil {
		return err
	}

	reproducer, err := c.markdownEditor(i18n.G("How to reproduce this bug?"), i18n.G("step by step instructions to reproduce the behavior"))
	if err != nil {
		return err
	}

	review, err := c.global.asker.AskBool(i18n.G("We will now submit your issue on GitHub. This is your last chance to review it (wording, secret leakage...) before it is published online. Do you want to review it?")+" (yes/no) [default=yes]: ", "yes")
	if err != nil {
		return err
	}

	issueBody := c.formatIssue(systemDetails, instanceDetails, instanceLog, currentBehavior, expectedBehavior, reproducer)
	if review {
		issueBytes, err := textEditor("", []byte(issueBody), "md")
		if err != nil {
			return err
		}

		issueBody = string(issueBytes)
	}

	host, err := oauth.NewGitHubHost("https://github.com")
	if err != nil {
		return err
	}

	flow := &oauth.Flow{
		Host:     host,
		ClientID: "Ov23liSZjBXJ5xjoFjW6",
		Scopes:   []string{"public_repo"},
		DisplayCode: func(code string, url string) error {
			fmt.Printf("Use the following code at %s: %s\n", url, code)
			return nil
		},
		BrowseURL: func(url string) error {
			_ = util.OpenBrowser(url)
			return nil
		},
	}

	token, err := flow.DeviceFlow()
	if err != nil {
		return err
	}

	tokenSource := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token.Token})
	client := oauth2.NewClient(context.Background(), tokenSource)
	gh := github.NewClient(client)

	issue, _, _ := gh.Issues.Create(context.Background(), "bensmrs", "testrepo", &github.IssueRequest{Title: &issueTitle, Body: &issueBody})
	url := issue.GetHTMLURL()
	fmt.Println("Your issue has been created at " + url)
	_ = util.OpenBrowser(url)

	return nil
}

// markdownEditor opens a text editor to input Markdown text.
func (c *cmdBugReport) markdownEditor(question string, description string) (string, error) {
	data, err := textEditor("", []byte(fmt.Sprintf(i18n.G("### %s\n*Pleuse write %s below the following dashes. You can use Markdown formatting.*")+"\n\n---\n\n\n", question, description)), "md")
	if err != nil {
		return "_No response_", err
	}

	parts := strings.SplitN(string(data), "---", 2)
	if len(parts) < 2 {
		return "_No response_", nil
	}

	answer := strings.TrimSpace(parts[1])
	if answer == "" {
		return "_No response_", nil
	}

	return answer, nil
}

// formatIssue formats the GitHub issue.
func (c *cmdBugReport) formatIssue(systemDetails string, instanceDetails string, instanceLog string, currentBehavior string, expectedBehavior string, reproducer string) string {
	return fmt.Sprintf("*This issue was generated with `incus bug report`.*"+`

### Is there an existing issue for this?

- [x] There is no existing issue for this bug

### Is this happening on an up to date version of Incus?

- [x] This is happening on a supported version of Incus

### Incus system details

`+"```"+`yaml
%s`+"```"+`

### Instance details

%s

### Instance log

%s

### Current behavior

%s

### Expected behavior

%s

### Steps to reproduce

%s`, systemDetails, instanceDetails, instanceLog, currentBehavior, expectedBehavior, reproducer)
}
