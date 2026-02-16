package cmd

import (
	"strings"

	"github.com/lxc/incus/v6/cmd/incus/usage"
)

// Usage formats the command name and sub-command name.
func Usage(name string, args ...string) string {
	if len(args) == 0 {
		return name
	}

	return name + " " + args[0]
}

// U formats the command name and sub-command name. This function is meant to deprecate `Usage`.
func U(name string, args ...usage.Atom) string {
	if len(args) == 0 {
		return name
	}

	elements := make([]string, len(args))
	for i, arg := range args {
		elements[i] = arg.Render()
	}

	return name + " " + strings.Join(elements, " ")
}
