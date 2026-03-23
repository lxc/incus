package color

import (
	"github.com/fatih/color"

	"github.com/lxc/incus/v6/internal/i18n"
)

func commandHeader(header string) string {
	return color.New(color.FgHiCyan, color.Bold, color.Underline).Sprint(header)
}

// A few prefixes used throughout the Incus client.
var (
	ErrorPrefix                string
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
	DescriptionPrefix = commandHeader(i18n.G("Description:"))
	RawUsagePrefix = i18n.G("Usage:")
	UsagePrefix = commandHeader(RawUsagePrefix)
	AliasesPrefix = commandHeader(i18n.G("Aliases:"))
	ExamplesPrefix = commandHeader(i18n.G("Examples:"))
	AvailableCommandsPrefix = commandHeader(i18n.G("Available Commands:"))
	FlagsPrefix = commandHeader(i18n.G("Flags:"))
	GlobalFlagsPrefix = commandHeader(i18n.G("Global Flags:"))
	AdditionalHelpTopicsPrefix = commandHeader(i18n.G("Additional Help Topics:"))
}
