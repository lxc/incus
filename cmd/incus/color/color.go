package color

import (
	"github.com/fatih/color"

	"github.com/lxc/incus/v7/internal/i18n"
)

// CommandHeader formats a command help section header.
func CommandHeader(header string) string {
	return color.New(color.FgHiCyan, color.Bold, color.Underline).Sprint(header)
}

// A few prefixes used throughout the Incus client.
var (
	ErrorPrefix                string
	WarningPrefix              string
	DescriptionPrefix          string
	RawUsagePrefix             string
	UsagePrefix                string
	AliasesPrefix              string
	ExamplesPrefix             string
	AvailableCommandsPrefix    string
	FlagsPrefix                string
	GlobalFlagsPrefix          string
	AdditionalHelpTopicsPrefix string
)

// Init initializes the global colored values.
func Init(disable bool) {
	if disable {
		color.NoColor = true
	}

	ErrorPrefix = color.New(color.FgRed, color.Bold).Sprint(i18n.G("Error:"))
	WarningPrefix = color.New(color.FgYellow, color.Bold).Sprint(i18n.G("Warning:"))
	DescriptionPrefix = CommandHeader(i18n.G("Description"))
	RawUsagePrefix = i18n.G("Usage")
	UsagePrefix = CommandHeader(RawUsagePrefix)
	AliasesPrefix = CommandHeader(i18n.G("Aliases"))
	ExamplesPrefix = CommandHeader(i18n.G("Examples"))
	AvailableCommandsPrefix = CommandHeader(i18n.G("Available Commands"))
	FlagsPrefix = CommandHeader(i18n.G("Flags"))
	GlobalFlagsPrefix = CommandHeader(i18n.G("Global Flags"))
	AdditionalHelpTopicsPrefix = CommandHeader(i18n.G("Additional Help Topics"))
}
