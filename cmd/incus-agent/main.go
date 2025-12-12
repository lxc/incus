package main

import (
	"os"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v6/internal/version"
	"github.com/lxc/incus/v6/shared/logger"
)

type cmdGlobal struct {
	flagVersion bool
	flagHelp    bool
	flagService bool

	flagLogVerbose bool
	flagLogDebug   bool
}

type AgentState struct {
	EnabledFeatures map[string]bool
}

var state AgentState

func main() {
	//Loading the Agent-configuration
	enabled, err := loadAgentConfig()
	if err != nil {
		logger.Warn("Failed to load incus-agent.yml, defaulting to all features enabled", logger.Ctx{"err": err.Error()})
	}
	state.EnabledFeatures = enabled

	// agent command (main)
	agentCmd := cmdAgent{}
	app := agentCmd.Command()
	app.SilenceUsage = true
	app.CompletionOptions = cobra.CompletionOptions{DisableDefaultCmd: true}

	// Workaround for main command
	app.Args = cobra.ArbitraryArgs

	// Global flags
	globalCmd := cmdGlobal{}
	agentCmd.global = &globalCmd
	app.PersistentFlags().BoolVar(&globalCmd.flagVersion, "version", false, "Print version number")
	app.PersistentFlags().BoolVarP(&globalCmd.flagHelp, "help", "h", false, "Print help")
	app.PersistentFlags().BoolVarP(&globalCmd.flagLogVerbose, "verbose", "v", false, "Show all information messages")
	app.PersistentFlags().BoolVarP(&globalCmd.flagLogDebug, "debug", "d", false, "Show all debug messages")
	if runtime.GOOS == "windows" {
		app.PersistentFlags().BoolVar(&globalCmd.flagService, "service", false, "Start as a system service")
	}

	// Version handling
	app.SetVersionTemplate("{{.Version}}\n")
	app.Version = version.Version

	// Run the main command and handle errors
	err = app.Execute()
	if err != nil {
		os.Exit(1)
	}
}
