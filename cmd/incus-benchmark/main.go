package main

import (
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/lxc/incus/v6/shared/util"
)

type cmdGlobal struct {
	flagHelp        bool
	flagParallel    int
	flagProject     string
	flagReportFile  string
	flagReportLabel string
	flagVersion     bool

	srv            incus.InstanceServer
	report         *CSVReport
	reportDuration time.Duration
}

func (c *cmdGlobal) Run(cmd *cobra.Command, args []string) error {
	// Connect to the daemon
	srv, err := incus.ConnectIncusUnix("", nil)
	if err != nil {
		return err
	}

	c.srv = srv.UseProject(c.flagProject)

	// Print the initial header
	err = PrintServerInfo(srv)
	if err != nil {
		return err
	}

	// Setup report handling
	if c.flagReportFile != "" {
		c.report = &CSVReport{Filename: c.flagReportFile}
		if util.PathExists(c.flagReportFile) {
			err := c.report.Load()
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *cmdGlobal) Teardown(cmd *cobra.Command, args []string) error {
	// Nothing to do with not reporting
	if c.report == nil {
		return nil
	}

	label := cmd.Name()
	if c.flagReportLabel != "" {
		label = c.flagReportLabel
	}

	err := c.report.AddRecord(label, c.reportDuration)
	if err != nil {
		return err
	}

	err = c.report.Write()
	if err != nil {
		return err
	}

	return nil
}

func main() {
	app := &cobra.Command{}
	app.Use = "incus-benchmark"
	app.Short = "Benchmark performance of Incus"
	app.Long = `Description:
  Benchmark performance of Incus

  This tool lets you benchmark various actions on a local Incus daemon.

  It can be used just to check how fast a given host is, to
  compare performance on different servers or for performance tracking
  when doing changes to the codebase.

  A CSV report can be produced to be consumed by graphing software.
`
	app.Example = `  # Spawn 20 containers in batches of 4
  incus-benchmark launch --count 20 --parallel 4

  # Create 50 Alpine containers in batches of 10
  incus-benchmark init --count 50 --parallel 10 images:alpine/edge

  # Delete all test containers using dynamic batch size
  incus-benchmark delete`
	app.SilenceUsage = true
	app.CompletionOptions = cobra.CompletionOptions{DisableDefaultCmd: true}

	// Global flags
	globalCmd := cmdGlobal{}
	app.PersistentPreRunE = globalCmd.Run
	app.PersistentPostRunE = globalCmd.Teardown
	app.PersistentFlags().BoolVar(&globalCmd.flagVersion, "version", false, "Print version number")
	app.PersistentFlags().BoolVarP(&globalCmd.flagHelp, "help", "h", false, "Print help")
	app.PersistentFlags().IntVarP(&globalCmd.flagParallel, "parallel", "P", -1, "Number of threads to use"+"``")
	app.PersistentFlags().StringVar(&globalCmd.flagReportFile, "report-file", "", "Path to the CSV report file"+"``")
	app.PersistentFlags().StringVar(&globalCmd.flagReportLabel, "report-label", "", "Label for the new entry in the report [default=ACTION]"+"``")
	app.PersistentFlags().StringVar(&globalCmd.flagProject, "project", api.ProjectDefaultName, "Project to use")

	// Version handling
	app.SetVersionTemplate("{{.Version}}\n")
	app.Version = version.Version

	// init sub-command
	initCmd := cmdInit{global: &globalCmd}
	app.AddCommand(initCmd.Command())

	// launch sub-command
	launchCmd := cmdLaunch{global: &globalCmd, init: &initCmd}
	app.AddCommand(launchCmd.Command())

	// start sub-command
	startCmd := cmdStart{global: &globalCmd}
	app.AddCommand(startCmd.Command())

	// stop sub-command
	stopCmd := cmdStop{global: &globalCmd}
	app.AddCommand(stopCmd.Command())

	// delete sub-command
	deleteCmd := cmdDelete{global: &globalCmd}
	app.AddCommand(deleteCmd.Command())

	// Run the main command and handle errors
	err := app.Execute()
	if err != nil {
		os.Exit(1)
	}
}
